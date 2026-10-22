package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
	"github.com/jhoogstraat/banda/internal/tunnel"
)

var errNotRunning = connect.NewError(connect.CodeFailedPrecondition, "the environment is not running")

// runner is a connected runner: commands go to ch until done is closed.
type runner struct {
	ch   chan *bandav1.ConnectResponse
	done chan struct{}
	// tunnels carries the preview's connections into the pod; ports is what
	// listens there. ports is guarded by Service.mu.
	tunnels *tunnel.Mux
	ports   []*bandav1.PreviewPort
	pong    chan struct{}
}

func (s *Service) Connect(ctx context.Context, stream bandav1connect.RunnerServiceConnectServerStream) error {
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return connect.NewError(connect.CodeInvalidArgument, "first message must be hello")
	}
	// id is the thread, or for a login environment its loginKey.
	id, owner, login := hello.ThreadId, hello.LoginUserId, hello.LoginUserId != ""
	r := &runner{ch: make(chan *bandav1.ConnectResponse, 16), done: make(chan struct{}), pong: make(chan struct{}, 1)}
	if login {
		if !s.store.CheckLoginToken(ctx, owner, hello.Token) {
			return connect.NewError(connect.CodeUnauthenticated, "unknown user or bad token")
		}
		id = loginKey(owner)
	} else {
		t, err := s.runnerThread(ctx, id, hello.Token)
		if err != nil {
			return err
		}
		owner = t.OwnerID
		// The pod's environment has the settings it was created with; a
		// container restart in the same pod would miss later changes.
		r.ch <- &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_AgentSettings{AgentSettings: agentSettings(t)}}
	}

	// The token alone doesn't replace a runner that still answers: that one
	// is the pod's. A restarted container finds its old stream gone, or dead
	// and not answering.
	s.mu.Lock()
	old := s.runners[id]
	s.mu.Unlock()
	if old != nil && old.answers() {
		return connect.NewError(connect.CodeAlreadyExists, "a runner is already connected for this environment")
	}

	r.tunnels = tunnel.NewMux(func(t *bandav1.HttpTunnel) error {
		if !r.send(&bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_HttpTunnel{HttpTunnel: t}}) {
			return errNotRunning
		}
		return nil
	})
	s.mu.Lock()
	s.runners[id] = r
	s.previewChanged(id)
	s.mu.Unlock()
	defer close(r.done)
	defer r.tunnels.CloseAll()
	defer func() {
		s.mu.Lock()
		mine := s.runners[id] == r
		if mine {
			delete(s.runners, id)
			s.previewChanged(id)
			s.closeTerminal(id) // the terminal dies with the runner
		}
		s.mu.Unlock()
		if mine && !login {
			s.settlePermissions(context.WithoutCancel(ctx), id, bandav1.PermissionDecision_PERMISSION_DECISION_LOST)
			_, events, err := s.store.Update(context.WithoutCancel(ctx), id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
				return envLost(t, 0, reasonDisconnected), nil
			})
			if err != nil {
				slog.Error("store lost environment", "thread", id, "err", err)
			}
			s.broadcast(id, events)
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-r.ch:
				if err := stream.Send(msg); err != nil {
					slog.Warn("send to runner", "thread", id, "err", err)
					return
				}
			}
		}
	}()

	// What the runner sent before it went away still happened: store it all,
	// though the stream's context ends with the stream.
	bg := context.WithoutCancel(ctx)
	for {
		msg, err := stream.Receive()
		if err != nil {
			return nil
		}
		switch k := msg.Kind.(type) {
		case *bandav1.ConnectRequest_Ready:
			if err := s.ready(bg, id, r); err != nil {
				return err
			}
		case *bandav1.ConnectRequest_Event:
			e := k.Event
			e.Actor = "agent"
			if e.GetNotice() != nil {
				e.Actor = "system"
			}
			if e.GetAgentTextDelta() != nil {
				e.ThreadId, e.Seq, e.Time = id, 0, timestamppb.Now()
				s.broadcast(id, []*bandav1.Event{e})
				continue
			}
			if ap := e.GetAutoPush(); ap != nil {
				if err := s.store.SetAutoPush(bg, id, ap.State.String()); err != nil {
					slog.Error("store auto-push state", "thread", id, "err", err)
				}
			}
			if e.GetPermissionResolved() != nil {
				e.Actor = "system" // the agent withdrew it
			}
			if e.GetPermissionRequested() != nil || e.GetPermissionResolved() != nil {
				s.permissionEvent(bg, id, e)
				if e.GetPermissionRequested() != nil { // asking keeps a parked thread's turn open
					go s.park(bg, id)
				}
				continue
			}
			var action thread.Action
			var reason string
			if te := e.GetTurnEnded(); te != nil {
				action = thread.TurnEnded{Prompts: int(te.Prompts)}
			}
			// The runner stays up for debugging but won't start the agent.
			if st := e.GetSetupStep(); st.GetState() == bandav1.SetupState_SETUP_STATE_FAILED {
				action, reason = thread.EnvLost{}, "Setup failed: "+st.Title
			}
			if _, err := s.applyBecause(bg, id, action, reason, e); err != nil {
				slog.Error("store agent event", "thread", id, "err", err)
			}
			if e.GetTurnEnded() != nil { // a request the turn left behind can't be answered any more
				s.settlePermissions(bg, id, bandav1.PermissionDecision_PERMISSION_DECISION_CANCELED)
				go s.park(bg, id) // not here: suspending waits for this stream
			}
		case *bandav1.ConnectRequest_TerminalOutput:
			s.mu.Lock()
			if term := s.terminals[id]; term != nil {
				select {
				case term <- k.TerminalOutput:
				default: // too slow, drop it like a lagging watcher, and end the shell
					s.closeTerminal(id)
					select {
					case r.ch <- &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalClose{TerminalClose: &bandav1.TerminalClose{}}}:
					default:
					}
				}
			}
			s.mu.Unlock()
		case *bandav1.ConnectRequest_LoginStatus:
			if err := s.store.SetLogin(bg, owner, k.LoginStatus.Harness, k.LoginStatus.LoggedIn); err != nil {
				slog.Error("store login status", "user", owner, "err", err)
			}
		case *bandav1.ConnectRequest_Pushed:
			s.mu.Lock()
			if ch := s.pushes[id]; ch != nil {
				select {
				case ch <- k.Pushed:
				default:
				}
			}
			s.mu.Unlock()
		case *bandav1.ConnectRequest_FileResponse:
			s.fileResponse(k.FileResponse)
		case *bandav1.ConnectRequest_HttpTunnel:
			r.tunnels.Deliver(k.HttpTunnel)
		case *bandav1.ConnectRequest_Pong:
			select {
			case r.pong <- struct{}{}:
			default:
			}
		case *bandav1.ConnectRequest_PreviewPorts:
			s.mu.Lock()
			r.ports = k.PreviewPorts.Ports
			if s.runners[id] == r {
				s.previewChanged(id)
			}
			s.mu.Unlock()
		case *bandav1.ConnectRequest_Diff:
			// The runner sends it after every turn: only a change makes viewers fetch it again.
			_, events, err := s.store.Update(bg, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
				if t.Diff == k.Diff.Diff {
					return nil, errUnchanged
				}
				t.Diff = k.Diff.Diff
				return []*bandav1.Event{{Actor: "system", Kind: &bandav1.Event_DiffChanged{DiffChanged: &bandav1.DiffChanged{}}}}, nil
			})
			if err != nil && err != errUnchanged {
				slog.Error("store diff", "thread", id, "err", err)
			}
			s.broadcast(id, events)
		}
	}
}

// errUnchanged makes an Update save nothing.
var errUnchanged = errors.New("unchanged")

// ready marks the environment ready and hands over the pending prompt, with
// what was posted since it was kept.
func (s *Service) ready(ctx context.Context, id string, r *runner) error {
	// A new claude process: requests of the one before are gone.
	s.settlePermissions(ctx, id, bandav1.PermissionDecision_PERMISSION_DECISION_LOST)
	names := s.names(ctx)
	var prompt string
	var images []string
	defer s.lockDelivery(id)()
	var t store.Thread
	var events []*bandav1.Event
	err := retryStale(func() error {
		u, err := s.readUnseen(ctx, id, nil)
		if err != nil {
			return err
		}
		t, events, err = s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
			next, effect, _ := thread.Apply(t.State, thread.EnvReady{Pending: t.PendingPrompt != ""})
			events := lostTurns(t.State, 0) // e.g. the API restarted mid-turn
			if effect == thread.StartTurn {
				// The pending prompt covers the events through DeliveredSeq.
				if err := u.fresh(t); err != nil {
					return nil, err
				}
				posted, more := postedSince(names, u.events, u.texts)
				prompt, images = t.PendingPrompt, lastImages(append(t.PendingImages, more...))
				if posted != "" {
					prompt += "\n\nMessages posted in this thread since:\n" + posted
				}
				t.PendingPrompt, t.PendingImages = "", nil
				t.DeliveredSeq = t.LastSeq
			}
			events = append(events, stateEvents(t.State, next)...)
			t.State = next
			return events, nil
		})
		return err
	})
	if err != nil {
		return err
	}
	s.broadcast(id, events)
	s.configure(t) // before the prompt, so its first tool call already asks as it should
	if prompt != "" {
		s.deliver(ctx, id, prompt, images)
	}
	return nil
}

// lostTurns reports the turns a thread in state st loses with its claude
// process, but the kept ones whose prompt is pending: a notice, and a failed
// turn end, which its parent hears of.
func lostTurns(st thread.State, kept int) []*bandav1.Event {
	n := st.Turns - kept
	if st.Status != thread.Working || n < 1 {
		return nil
	}
	text := "The agent stopped in the middle of a turn: the turn was lost"
	if n == 2 {
		text += ", and 1 prompt queued behind it"
	} else if n > 2 {
		text += fmt.Sprintf(", and %d prompts queued behind it", n-1)
	}
	return []*bandav1.Event{
		{Actor: "system", Kind: &bandav1.Event_Notice{Notice: &bandav1.Notice{Text: text + ". Post again what is still needed."}}},
		{Actor: "system", Kind: &bandav1.Event_TurnEnded{TurnEnded: &bandav1.TurnEnded{IsError: true, Error: "the agent stopped mid-turn"}}},
	}
}

// envLost applies EnvLost to t (kept: see lostTurns) and returns the events to
// store; reason says why on the status change.
func envLost(t *store.Thread, kept int, reason string) []*bandav1.Event {
	events := lostTurns(t.State, kept)
	next, _, _ := thread.Apply(t.State, thread.EnvLost{})
	for _, e := range stateEvents(t.State, next) {
		if sc := e.GetStatusChanged(); sc != nil {
			sc.Reason = reason
		}
		events = append(events, e)
	}
	t.State = next
	return events
}

// Why an environment was lost, on its status change and the team board.
const (
	reasonDisconnected = "Lost the environment: its runner disconnected (the pod crashed, restarted or was deleted)"
	reasonUnreachable  = "Lost the environment: its runner was gone when a prompt was sent"
	reasonProvision    = "Couldn't create the environment"
)

// sendRunner reports false if no runner is connected for the thread, or it
// stops taking commands.
func (s *Service) sendRunner(id string, msg *bandav1.ConnectResponse) bool {
	s.mu.Lock()
	r := s.runners[id]
	s.mu.Unlock()
	return r != nil && r.send(msg)
}

// answers reports whether the runner answers a ping within 5 seconds.
func (r *runner) answers() bool {
	select {
	case <-r.pong: // a late answer to an earlier ping
	default:
	}
	if !r.send(&bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Ping{Ping: &bandav1.Ping{}}}) {
		return false
	}
	select {
	case <-r.pong:
		return true
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
	return false
}

func (r *runner) send(msg *bandav1.ConnectResponse) bool {
	select {
	case r.ch <- msg:
		return true
	case <-r.done:
	case <-time.After(10 * time.Second):
	}
	return false
}
