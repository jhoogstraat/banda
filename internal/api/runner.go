package api

import (
	"context"
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
	r := &runner{ch: make(chan *bandav1.ConnectResponse, 16), done: make(chan struct{})}
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
			if term := s.terminals[id]; term != nil { // the terminal dies with the runner
				close(term)
				delete(s.terminals, id)
			}
		}
		s.mu.Unlock()
		if mine && !login {
			s.settlePermissions(context.WithoutCancel(ctx), id, bandav1.PermissionDecision_PERMISSION_DECISION_LOST)
			s.apply(context.WithoutCancel(ctx), id, thread.EnvLost{})
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

	for {
		msg, err := stream.Receive()
		if err != nil {
			return nil
		}
		switch k := msg.Kind.(type) {
		case *bandav1.ConnectRequest_Ready:
			if err := s.ready(ctx, id, r); err != nil {
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
				if err := s.store.SetAutoPush(ctx, id, ap.State.String()); err != nil {
					slog.Error("store auto-push state", "thread", id, "err", err)
				}
			}
			if e.GetPermissionResolved() != nil {
				e.Actor = "system" // the agent withdrew it
			}
			if e.GetPermissionRequested() != nil || e.GetPermissionResolved() != nil {
				s.permissionEvent(ctx, id, e)
				continue
			}
			var action thread.Action
			var reason string
			if e.GetTurnEnded() != nil {
				action = thread.TurnEnded{}
			}
			// The runner stays up for debugging but won't start the agent.
			if st := e.GetSetupStep(); st.GetState() == bandav1.SetupState_SETUP_STATE_FAILED {
				action, reason = thread.EnvLost{}, "Setup failed: "+st.Title
			}
			if _, err := s.applyBecause(ctx, id, action, reason, e); err != nil {
				slog.Error("store agent event", "thread", id, "err", err)
			}
			if e.GetTurnEnded() != nil { // a request the turn left behind can't be answered any more
				s.settlePermissions(ctx, id, bandav1.PermissionDecision_PERMISSION_DECISION_CANCELED)
			}
		case *bandav1.ConnectRequest_TerminalOutput:
			s.mu.Lock()
			if term := s.terminals[id]; term != nil {
				select {
				case term <- k.TerminalOutput:
				default: // too slow, drop it like a lagging watcher, and end the shell
					close(term)
					delete(s.terminals, id)
					select {
					case r.ch <- &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_TerminalClose{TerminalClose: &bandav1.TerminalClose{}}}:
					default:
					}
				}
			}
			s.mu.Unlock()
		case *bandav1.ConnectRequest_LoginStatus:
			if err := s.store.SetLogin(ctx, owner, k.LoginStatus.Harness, k.LoginStatus.LoggedIn); err != nil {
				slog.Error("store login status", "user", owner, "err", err)
			}
		case *bandav1.ConnectRequest_FileResponse:
			s.fileResponse(k.FileResponse)
		case *bandav1.ConnectRequest_HttpTunnel:
			r.tunnels.Deliver(k.HttpTunnel)
		case *bandav1.ConnectRequest_PreviewPorts:
			s.mu.Lock()
			r.ports = k.PreviewPorts.Ports
			if s.runners[id] == r {
				s.previewChanged(id)
			}
			s.mu.Unlock()
		case *bandav1.ConnectRequest_Diff:
			_, events, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
				t.Diff = k.Diff.Diff
				return []*bandav1.Event{{Actor: "system", Kind: &bandav1.Event_DiffChanged{DiffChanged: &bandav1.DiffChanged{}}}}, nil
			})
			if err != nil {
				slog.Error("store diff", "thread", id, "err", err)
			}
			s.broadcast(id, events)
		}
	}
}

// ready marks the environment ready and hands over the initial prompt if it
// has not been delivered yet.
func (s *Service) ready(ctx context.Context, id string, r *runner) error {
	// A new claude process: requests of the one before are gone.
	s.settlePermissions(ctx, id, bandav1.PermissionDecision_PERMISSION_DECISION_LOST)
	var prompt string
	var images []string
	t, events, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
		next, effect, _ := thread.Apply(t.State, thread.EnvReady{Pending: t.PendingPrompt != ""})
		if effect == thread.StartTurn {
			prompt, t.PendingPrompt = t.PendingPrompt, ""
			images, t.PendingImages = t.PendingImages, nil
			t.DeliveredSeq = t.LastSeq
		}
		events := stateEvents(t.State, next)
		t.State = next
		return events, nil
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

// sendRunner reports false if no runner is connected for the thread, or it
// stops taking commands.
func (s *Service) sendRunner(id string, msg *bandav1.ConnectResponse) bool {
	s.mu.Lock()
	r := s.runners[id]
	s.mu.Unlock()
	return r != nil && r.send(msg)
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
