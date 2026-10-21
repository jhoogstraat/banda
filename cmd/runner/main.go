// Command runner runs inside each thread's sandbox pod. It dials the API,
// prepares the environment (setup.go), waits for a Claude Code login and then
// drives one long-lived `claude` process in stream-json mode.
// See docs/research/claude-headless.md.
//
// With BANDA_LOGIN_USER_ID set it runs a user's login environment instead:
// only the terminal, plus the login status after each command.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/tunnel"
)

var (
	threadID  = os.Getenv("BANDA_THREAD_ID")
	loginUser = os.Getenv("BANDA_LOGIN_USER_ID")
	apiKey    = os.Getenv("BANDA_AUTH_METHOD") == "api_key" // the user chose an API key over their subscription
	work      = filepath.Join(os.Getenv("HOME"), "work")
	base      = "origin/" + os.Getenv("BANDA_BASE_BRANCH")
	// errSetup: preparing the environment failed in a way a restart won't fix,
	// e.g. claude failing to start.
	errSetup = errors.New("setup failed")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	err := run(ctx)
	if errors.Is(err, errSetup) {
		// run has disconnected, so the thread shows as errored. Stay up instead
		// of crash-looping until Retry or the idle suspend replaces the pod.
		log.Print(err)
		<-ctx.Done()
		return
	}
	if err != nil && ctx.Err() == nil {
		log.Fatal(err) // the container restarts and resumes the session
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	tr := &http.Transport{Protocols: new(http.Protocols)}
	tr.Protocols.SetUnencryptedHTTP2(true)
	api := bandav1connect.NewRunnerServiceClient(connect.NewClient(connecthttp.NewTransport(&http.Client{Transport: tr}, os.Getenv("BANDA_API_URL"))))
	stream, err := api.Connect(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	// The terminal, push and claude's output all send, from different goroutines.
	var sendMu sync.Mutex
	sendReq := func(r *bandav1.ConnectRequest) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(r)
	}
	send := func(e *bandav1.Event) error {
		scrub(e)
		return sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Event{Event: e}})
	}
	notice := func(text string) error {
		return send(&bandav1.Event{Kind: &bandav1.Event_Notice{Notice: &bandav1.Notice{Text: text}}})
	}
	loginStatus := func(ok bool) error {
		return sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_LoginStatus{LoginStatus: &bandav1.LoginStatus{Harness: "claude", LoggedIn: ok}}})
	}
	if err := sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Hello{Hello: &bandav1.Hello{
		ThreadId: threadID, Token: os.Getenv("BANDA_RUNNER_TOKEN"), LoginUserId: loginUser}}}); err != nil {
		return err
	}

	// API -> runner. The terminal works from here on, so the user can log in
	// while we wait for it; prompts and stops queue until claude is up.
	// Losing the API stops claude gracefully and the runner exits.
	// ponytail: no reconnect loop, the container restart is the reconnect.
	cmds := make(chan *bandav1.ConnectResponse, 16)
	want := &wanted{s: settings{os.Getenv("BANDA_MODEL"), os.Getenv("BANDA_EFFORT")}}
	pushes := &pusher{report: send}
	lastDiff := "" // the first turn after a start always pushes
	term := &terminal{send: sendReq}
	if loginUser != "" {
		term.exited = func() { loginStatus(loggedIn(ctx)) }
	}
	defer term.close()
	tunnels := tunnel.NewMux(func(t *bandav1.HttpTunnel) error {
		return sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_HttpTunnel{HttpTunnel: t}})
	})
	defer tunnels.CloseAll()
	go func() {
		defer cancel()
		for {
			msg, err := stream.Receive()
			if err != nil {
				return
			}
			switch k := msg.Kind.(type) {
			case *bandav1.ConnectResponse_Prompt, *bandav1.ConnectResponse_Stop:
				cmds <- msg
			case *bandav1.ConnectResponse_AgentSettings:
				// Set now so a claude that hasn't started yet starts with them;
				// queued so a running one restarts after the prompts sent before.
				want.set(settings{k.AgentSettings.Model, k.AgentSettings.Effort})
				cmds <- msg
			case *bandav1.ConnectResponse_Push:
				go pushes.manualPush(ctx, notice)
			case *bandav1.ConnectResponse_TerminalOpen:
				term.open(k.TerminalOpen)
			case *bandav1.ConnectResponse_TerminalInput:
				term.input(k.TerminalInput.Data)
			case *bandav1.ConnectResponse_TerminalResize:
				term.resize(k.TerminalResize.Cols, k.TerminalResize.Rows)
			case *bandav1.ConnectResponse_TerminalClose:
				term.close()
			case *bandav1.ConnectResponse_FileRequest:
				if loginUser == "" { // a login environment has no checkout
					go files(ctx, k.FileRequest, sendReq)
				}
			case *bandav1.ConnectResponse_HttpTunnel:
				if t := k.HttpTunnel; t.Port != 0 && loginUser == "" { // the API opens a preview connection
					go dialPort(ctx, tunnels.Open(t.Id), t.Port)
				} else {
					tunnels.Deliver(t)
				}
			}
		}
	}()

	if loginUser != "" {
		loginStatus(loggedIn(ctx))
		<-ctx.Done()
		return nil
	}
	go reportPorts(ctx, sendReq) // the preview works during setup and after it failed
	if err := prepare(ctx, send, notice, loginStatus); err != nil {
		// Reported as a failed step. Stay connected without the agent so the
		// owner can debug in the terminal; exiting would only crash-loop.
		log.Print(err)
		<-ctx.Done()
		return nil
	}
	// Every way out of here stops claude gracefully and waits for it: a kill
	// in the middle of a token refresh would log the user out everywhere.
	var claude *claudeProc
	defer func() {
		cancel()
		if claude != nil {
			go io.Copy(io.Discard, claude.stdout)
			claude.cmd.Wait()
		}
	}()
	for first := true; ; first = false {
		running := want.get()
		if claude, err = startClaude(ctx, running); err != nil {
			claude = nil
			notice("Could not start Claude Code: " + err.Error())
			return fmt.Errorf("%w: %w", errSetup, err)
		}
		if first {
			if err := sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Ready{Ready: &bandav1.Ready{}}}); err != nil {
				return err
			}
		}
		lines := make(chan []byte)
		go func(stdout io.Reader) {
			defer close(lines)
			out := bufio.NewReaderSize(stdout, 1<<20)
			for {
				raw, err := out.ReadBytes('\n')
				if len(raw) > 0 {
					select {
					case lines <- raw:
					case <-ctx.Done():
						return
					}
				}
				if err != nil {
					return
				}
			}
		}(claude.stdout)

		// Relay until the settings changed and no turn is running: claude
		// takes new flags only at start, and a turn is never cut short.
		pending := 0 // prompts written whose turn hasn't ended
		restart := false
		authNoticed := false
		for !restart || pending > 0 {
			select {
			case <-ctx.Done():
				return nil
			case msg := <-cmds:
				var err error
				switch k := msg.Kind.(type) {
				case *bandav1.ConnectResponse_Prompt:
					err = claude.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": k.Prompt.Text}})
					pending++
				case *bandav1.ConnectResponse_Stop:
					err = claude.interrupt()
				case *bandav1.ConnectResponse_AgentSettings:
					restart = want.get() != running
				}
				if err != nil {
					return err
				}
			case raw, ok := <-lines:
				if !ok {
					return fmt.Errorf("claude exited: %w", claude.cmd.Wait())
				}
				events, turnEnded, authFailed := translate(raw)
				for _, e := range events {
					if err := send(e); err != nil {
						return err
					}
				}
				if authFailed && !authNoticed {
					authNoticed = true
					claude.interrupt()
					if apiKey {
						notice("Claude Code rejected the API key. Set a new one in Settings, then retry.")
					} else {
						loginStatus(false)
						notice(loginHint("Claude Code rejected the login."))
					}
				}
				if turnEnded {
					authNoticed = false
					pending = max(0, pending-1) // an interrupt while idle ends no prompt
					d := redact(diff(ctx))
					if err := sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Diff{Diff: &bandav1.Diff{Diff: d}}}); err != nil {
						return err
					}
					if d != lastDiff { // auto-push, so the work also lives on origin
						lastDiff = d
						go pushes.push(ctx)
					}
				}
			}
		}

		// Idle: closing stdin makes claude exit by itself. SIGTERM, still
		// graceful, only if it doesn't.
		claude.stdin.Close()
		proc := claude.cmd.Process
		t := time.AfterFunc(30*time.Second, func() { proc.Signal(syscall.SIGTERM) })
		for range lines {
		}
		if err := claude.cmd.Wait(); err != nil {
			log.Print("claude exit for restart: ", err)
		}
		t.Stop()
	}
}

// settings are what claude runs with.
type settings struct{ model, effort string }

// wanted holds the settings claude should run with. The API sends them on
// connect and on every change.
type wanted struct {
	mu sync.Mutex
	s  settings
}

func (w *wanted) get() settings {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.s
}

func (w *wanted) set(s settings) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.s = s
}

// checkout clones the repo onto the thread branch the first time; the home
// volume keeps it across restarts.
func checkout(ctx context.Context) error {
	git := func(args ...string) error {
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
		return nil
	}
	steps := [][]string{
		{"config", "--global", "user.name", "Banda"},
		{"config", "--global", "user.email", "banda@localhost"},
	}
	if u, err := url.Parse(os.Getenv("BANDA_REPO_URL")); err == nil && u.Scheme == "https" && os.Getenv("GIT_TOKEN") != "" {
		// Only for the repo's host. Reads the token from the environment at use
		// time; it is never written to disk.
		steps = append(steps, []string{"config", "--global", "credential.https://" + u.Host + ".helper",
			`!f() { echo username=x-access-token; echo "password=$GIT_TOKEN"; }; f`})
	}
	if _, err := os.Stat(filepath.Join(work, ".git")); err != nil {
		steps = append(steps, []string{"clone", "--branch", os.Getenv("BANDA_BASE_BRANCH"), os.Getenv("BANDA_REPO_URL"), work})
	}
	for _, args := range steps {
		if err := git(args...); err != nil {
			return err
		}
	}
	// Also covers a previous start that died right after cloning. A fresh
	// checkout of a thread whose environment was deleted continues from the
	// pushed branch.
	branch := os.Getenv("BANDA_BRANCH")
	switch {
	case git("-C", work, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil:
		return nil
	case git("-C", work, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch) == nil:
		return git("-C", work, "switch", branch)
	}
	return git("-C", work, "switch", "-c", branch)
}

// waitForLogin waits for a subscription login, reported as the login step
// only if there is something to wait for.
func (s setup) waitForLogin(ctx context.Context, notice func(string) error, loginStatus func(bool) error) error {
	const title = "Log in to Claude Code"
	told := false
	for {
		if loggedIn(ctx) {
			if told {
				s.report("login", title, bandav1.SetupState_SETUP_STATE_DONE, "")
			}
			return loginStatus(true)
		}
		if !told {
			told = true
			loginStatus(false)
			s.report("login", title, bandav1.SetupState_SETUP_STATE_RUNNING, "")
			if err := notice(loginHint("Claude Code is not logged in.")); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func loggedIn(ctx context.Context) bool {
	return claudeCmd(ctx, "auth", "status", "--json").Run() == nil
}

func loginHint(problem string) string {
	return fmt.Sprintf("%s Open the Terminal tab and run `claude auth login` (or use the Log in button). Fallback: `kubectl exec -it -n banda t-%s -- claude auth login`. The thread continues once you are logged in.", problem, threadID)
}

type claudeProc struct {
	cmd    *exec.Cmd
	stdout io.Reader
	mu     sync.Mutex
	stdin  io.WriteCloser
}

func startClaude(ctx context.Context, s settings) (*claudeProc, error) {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--dangerously-skip-permissions"} // the sandbox is the boundary
	if s.model != "" {
		args = append(args, "--model", s.model)
	}
	if s.effort != "" {
		args = append(args, "--effort", s.effort)
	}
	// Neither flag is restored on --resume, so both are always passed.
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"banda": map[string]any{
		"type": "http", "url": os.Getenv("BANDA_API_URL") + "/mcp",
		"headers": map[string]string{"Authorization": "Bearer " + os.Getenv("BANDA_RUNNER_TOKEN"), "X-Banda-Thread": threadID},
	}}})
	args = append(args, "--mcp-config", string(mcpConfig), "--append-system-prompt", fmt.Sprintf(
		"You are running inside Banda thread %s on git branch %s. Teammates and other agents may post messages into this thread; they appear in your prompt with their name. "+
			"The banda MCP tools (list_threads, read_thread, post_message, create_thread) let you see other threads, post into them and start new threads.",
		threadID, os.Getenv("BANDA_BRANCH")))
	sid := sessionID(threadID)
	if hasTranscript(sid) {
		args = append(args, "--resume", sid)
	} else {
		args = append(args, "--session-id", sid)
	}

	cmd := claudeCmd(ctx, args...)
	cmd.Dir = work
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	return &claudeProc{cmd: cmd, stdout: stdout, stdin: stdin}, cmd.Start()
}

// claudeCmd never kills claude outright: a kill during a token refresh logs
// the user out of every pod. The pod's grace period is 90s.
func claudeCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 80 * time.Second
	return cmd
}

// write sends one stream-json line. A malformed line kills claude, so only
// ever write marshalled values.
func (c *claudeProc) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *claudeProc) interrupt() error {
	return c.write(map[string]any{"type": "control_request", "request_id": rand.Text(), "request": map[string]any{"subtype": "interrupt"}})
}

// sessionID derives a stable UUID from the thread id, so a restarted runner
// resumes the same Claude Code session.
func sessionID(thread string) string {
	h := sha256.Sum256([]byte(thread))
	h[6] = h[6]&0x0f | 0x40
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func hasTranscript(sid string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "projects", "*", sid+".jsonl"))
	return len(m) > 0
}

// diff returns the branch's changes against its base, untracked files
// included, using a throwaway index so the agent's own index is untouched.
func diff(ctx context.Context) string {
	const max = 1 << 20
	idx := filepath.Join(os.TempDir(), "banda-index")
	var out []byte
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "-A"}, {"diff", "--cached", base}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+idx)
		var err error
		if out, err = cmd.Output(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return fmt.Sprintf("git %s failed: %s", args[0], ee.Stderr)
			}
			return err.Error()
		}
	}
	if len(out) > max {
		out = append(out[:max], "\n… diff truncated at 1 MiB"...)
	}
	return string(out) // redact makes it valid UTF-8
}
