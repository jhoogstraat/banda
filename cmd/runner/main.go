// Command runner runs inside each thread's sandbox pod. It dials the API,
// prepares the checkout, waits for a Claude Code login and then drives one
// long-lived `claude` process in stream-json mode.
// See docs/research/claude-headless.md.
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
)

var (
	threadID = os.Getenv("BANDA_THREAD_ID")
	work     = filepath.Join(os.Getenv("HOME"), "work")
	base     = "origin/" + os.Getenv("BANDA_BASE_BRANCH")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil && ctx.Err() == nil {
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
		return sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Event{Event: e}})
	}
	notice := func(text string) error {
		return send(&bandav1.Event{Kind: &bandav1.Event_Notice{Notice: &bandav1.Notice{Text: text}}})
	}
	if err := sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Hello{Hello: &bandav1.Hello{
		ThreadId: threadID, Token: os.Getenv("BANDA_RUNNER_TOKEN")}}}); err != nil {
		return err
	}

	// API -> runner. The terminal works from here on, so the user can log in
	// while we wait for it; prompts and stops queue until claude is up.
	// Losing the API stops claude gracefully and the runner exits.
	// ponytail: no reconnect loop, the container restart is the reconnect.
	cmds := make(chan *bandav1.ConnectResponse, 16)
	term := &terminal{send: sendReq}
	defer term.close()
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
			case *bandav1.ConnectResponse_Push:
				go push(ctx, notice)
			case *bandav1.ConnectResponse_TerminalOpen:
				term.open(k.TerminalOpen)
			case *bandav1.ConnectResponse_TerminalInput:
				term.input(k.TerminalInput.Data)
			case *bandav1.ConnectResponse_TerminalResize:
				term.resize(k.TerminalResize.Cols, k.TerminalResize.Rows)
			case *bandav1.ConnectResponse_TerminalClose:
				term.close()
			}
		}
	}()

	if err := checkout(ctx); err != nil {
		notice("Could not prepare the checkout: " + err.Error())
		return err
	}
	if err := waitForLogin(ctx, notice); err != nil {
		return err
	}
	claude, err := startClaude(ctx)
	if err != nil {
		notice("Could not start Claude Code: " + err.Error())
		return err
	}
	if err := sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Ready{Ready: &bandav1.Ready{}}}); err != nil {
		return err
	}

	// API -> claude.
	go func() {
		defer cancel()
		for {
			var err error
			select {
			case <-ctx.Done():
				return
			case msg := <-cmds:
				switch k := msg.Kind.(type) {
				case *bandav1.ConnectResponse_Prompt:
					err = claude.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": k.Prompt.Text}})
				case *bandav1.ConnectResponse_Stop:
					err = claude.interrupt()
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// claude -> API.
	out := bufio.NewReaderSize(claude.stdout, 1<<20)
	authNoticed := false
	for {
		raw, readErr := out.ReadBytes('\n')
		events, turnEnded, authFailed := translate(raw)
		for _, e := range events {
			if err := send(e); err != nil {
				return err
			}
		}
		if authFailed && !authNoticed {
			authNoticed = true
			claude.interrupt()
			notice(loginHint("Claude Code rejected the login."))
		}
		if turnEnded {
			authNoticed = false
			if err := sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Diff{Diff: &bandav1.Diff{Diff: diff(ctx)}}}); err != nil {
				return err
			}
		}
		if readErr != nil {
			return fmt.Errorf("claude exited: %w", claude.cmd.Wait())
		}
	}
}

// checkout clones the repo onto the thread branch the first time; the home
// volume keeps it across restarts.
func checkout(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(work, ".git")); err == nil {
		return nil
	}
	steps := [][]string{
		{"config", "--global", "user.name", "Banda"},
		{"config", "--global", "user.email", "banda@localhost"},
	}
	if os.Getenv("GIT_TOKEN") != "" {
		// Reads the token from the environment at use time; it is never written to disk.
		steps = append(steps, []string{"config", "--global", "credential.helper", `!f() { echo username=x-access-token; echo "password=$GIT_TOKEN"; }; f`})
	}
	steps = append(steps,
		[]string{"clone", "--branch", os.Getenv("BANDA_BASE_BRANCH"), os.Getenv("BANDA_REPO_URL"), work},
		[]string{"-C", work, "switch", "-c", os.Getenv("BANDA_BRANCH")},
	)
	for _, args := range steps {
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
	}
	return nil
}

func waitForLogin(ctx context.Context, notice func(string) error) error {
	told := false
	for {
		if exec.CommandContext(ctx, "claude", "auth", "status", "--json").Run() == nil {
			return nil
		}
		if !told {
			told = true
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

func loginHint(problem string) string {
	return fmt.Sprintf("%s Open the Terminal tab and run `claude auth login` (or use the Log in button). Fallback: `make login SANDBOX=t-%s` or `kubectl exec -it -n banda t-%s -- claude auth login`. The thread continues once you are logged in.", problem, threadID, threadID)
}

type claudeProc struct {
	cmd    *exec.Cmd
	stdout io.Reader
	mu     sync.Mutex
	stdin  io.Writer
}

func startClaude(ctx context.Context) (*claudeProc, error) {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--dangerously-skip-permissions"} // the sandbox is the boundary
	if m := os.Getenv("BANDA_MODEL"); m != "" {
		args = append(args, "--model", m)
	}
	if e := os.Getenv("BANDA_EFFORT"); e != "" {
		args = append(args, "--effort", e)
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

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = work
	cmd.Stderr = os.Stderr
	// Never kill claude outright: a kill during a token refresh logs the user
	// out of every pod. The pod's grace period is 90s.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 80 * time.Second
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
		return string(out[:max]) + "\n… diff truncated at 1 MiB"
	}
	return string(out)
}
