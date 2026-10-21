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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	// queueCmd never blocks: setup and the login wait can take long, and the
	// terminal, files and previews keep working meanwhile.
	queueCmd, cmds := unbounded[*bandav1.ConnectResponse]()
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
			case *bandav1.ConnectResponse_Prompt, *bandav1.ConnectResponse_Stop, *bandav1.ConnectResponse_Permission:
				queueCmd(msg)
			case *bandav1.ConnectResponse_AgentSettings:
				// Set now so a claude that hasn't started yet starts with them;
				// queued so a running one restarts after the prompts sent before.
				want.set(settings{k.AgentSettings.Model, k.AgentSettings.Effort})
				queueCmd(msg)
			case *bandav1.ConnectResponse_Push:
				if k.Push.Suspend {
					go func() {
						sendReq(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Pushed{Pushed: pushes.push(ctx)}})
					}()
				} else {
					go pushes.manualPush(ctx, notice)
				}
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
	forkPatch := func(ctx context.Context) (*bandav1.ForkPatchResponse, error) {
		return api.ForkPatch(ctx, &bandav1.ForkPatchRequest{ThreadId: threadID, Token: os.Getenv("BANDA_RUNNER_TOKEN")})
	}
	if err := prepare(ctx, send, notice, loginStatus, forkPatch); err != nil {
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
	perms := newPermissions()
	for first := true; ; first = false {
		running := want.get()
		if claude, err = startClaude(ctx, running); err != nil {
			claude = nil
			notice("Could not start Claude Code: " + err.Error())
			return fmt.Errorf("%w: %w", errSetup, err)
		}
		if l := perms.modeLine(); l != nil { // a new process starts in default mode
			claude.write(l)
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
					err = claude.write(userMessage(k.Prompt))
					pending++
				case *bandav1.ConnectResponse_Stop:
					err = claude.interrupt()
				case *bandav1.ConnectResponse_AgentSettings:
					restart = want.get() != running
				case *bandav1.ConnectResponse_Permission:
					for _, l := range perms.apply(k.Permission) {
						if err = claude.write(l); err != nil {
							break
						}
					}
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
					if te := e.GetTurnEnded(); te != nil {
						// The CLI folds prompts into a running turn, so a result can end
						// several: all written but those it still queues.
						ended := max(pending-queuedTurns(raw), 0)
						te.Prompts, pending = int32(ended), pending-ended
					}
					if r := e.GetPermissionRequested(); r != nil {
						if auto := perms.ask(r); auto != nil { // always allowed in this thread
							claude.write(auto)
							continue
						}
					}
					if r := e.GetPermissionResolved(); r != nil {
						perms.drop(r.Id)
					}
					if err := send(e); err != nil {
						return err
					}
				}
				if id, ok := unsupportedRequest(raw); ok { // never leave the CLI waiting
					claude.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": "not supported by Banda"}})
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
					perms.clear()
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

// unbounded returns a send that never blocks for long and the channel it
// feeds, in order. Its goroutine lives as long as the process.
func unbounded[T any]() (func(T), <-chan T) {
	in, out := make(chan T), make(chan T)
	go func() {
		var q []T
		for {
			var send chan T // nil, so never ready, while q is empty
			var next T
			if len(q) > 0 {
				send, next = out, q[0]
			}
			select {
			case v := <-in:
				q = append(q, v)
			case send <- next:
				q = q[1:]
			}
		}
	}()
	return func(v T) { in <- v }, out
}
