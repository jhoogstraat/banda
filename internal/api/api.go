// Package api implements the ThreadService, UserService and SettingsService
// for the web client and the RunnerService the sandbox runners dial into.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/sandbox"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

type Service struct {
	store     *store.Store
	sandboxes *sandbox.Client

	mu       sync.Mutex
	watchers map[string]map[chan *bandav1.Event]struct{}
	// present counts each thread's open WatchThread streams per user.
	present map[string]map[string]int
	// runners and terminals are keyed by thread id, or loginKey for login
	// environments.
	runners map[string]*runner
	// terminals holds each runner's single terminal subscriber (see terminal).
	terminals map[string]chan *bandav1.TerminalOutput
	// loginLastUsed is when each user last had a login terminal.
	loginLastUsed map[string]time.Time
	// files holds the requests waiting for a runner's answer (see fileRequest).
	files map[string]chan *bandav1.FileResponse
}

// runner is a connected runner: commands go to ch until done is closed.
type runner struct {
	ch   chan *bandav1.ConnectResponse
	done chan struct{}
}

func New(st *store.Store, sb *sandbox.Client) *Service {
	return &Service{
		store:     st,
		sandboxes: sb,
		watchers:  map[string]map[chan *bandav1.Event]struct{}{},
		present:   map[string]map[string]int{},
		runners:   map[string]*runner{},
		terminals: map[string]chan *bandav1.TerminalOutput{},
		files:     map[string]chan *bandav1.FileResponse{},

		loginLastUsed: map[string]time.Time{},
	}
}

func (s *Service) CreateThread(ctx context.Context, req *bandav1.CreateThreadRequest) (*bandav1.CreateThreadResponse, error) {
	if req.RepoUrl == "" || strings.TrimSpace(req.Prompt) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "repo_url and prompt are required")
	}
	id := strings.ToLower(rand.Text())
	t := store.Thread{
		State:         thread.State{OwnerID: user(ctx), Status: thread.Starting, Mode: thread.AgentMode},
		ID:            id,
		Title:         title(req.Prompt),
		RepoURL:       req.RepoUrl,
		BaseBranch:    or(req.BaseBranch, "main"),
		Branch:        "banda/" + id,
		Model:         or(req.Model, "claude-opus-5-5"),
		Effort:        or(req.Effort, "high"),
		PendingPrompt: req.Prompt,
		RunnerToken:   rand.Text(),
	}
	if err := s.store.Create(ctx, t); err != nil {
		return nil, err
	}
	t, events, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
		return []*bandav1.Event{messageEvent(t.OwnerID, &bandav1.Message{Text: req.Prompt})}, nil
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(id, events)
	s.provision(ctx, t)
	return &bandav1.CreateThreadResponse{Thread: toProto(t)}, nil
}

func (s *Service) ListThreads(ctx context.Context, req *bandav1.ListThreadsRequest) (*bandav1.ListThreadsResponse, error) {
	threads, err := s.store.List(ctx, or(req.OwnerId, user(ctx)), user(ctx))
	if err != nil {
		return nil, err
	}
	res := &bandav1.ListThreadsResponse{}
	for _, t := range threads {
		res.Threads = append(res.Threads, toProto(t))
	}
	return res, nil
}

func (s *Service) GetThread(ctx context.Context, req *bandav1.GetThreadRequest) (*bandav1.GetThreadResponse, error) {
	t, err := s.store.GetFor(ctx, req.ThreadId, user(ctx))
	if err != nil {
		return nil, cerr(err)
	}
	return &bandav1.GetThreadResponse{Thread: toProto(t)}, nil
}

func (s *Service) SendMessage(ctx context.Context, req *bandav1.SendMessageRequest) (*bandav1.SendMessageResponse, error) {
	m := req.Message
	if m == nil || strings.TrimSpace(m.Text) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "message text is required")
	}
	by, id := user(ctx), req.ThreadId
	before, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, cerr(err)
	}
	unseen, err := s.store.Events(ctx, id, before.DeliveredSeq)
	if err != nil {
		return nil, err
	}
	names := s.names(ctx)

	var effect thread.Effect
	var prompt string
	_, stored, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
		next, eff, err := thread.Apply(t.State, thread.Send{By: by, MentionsAgent: m.MentionsAgent, MentionsUsers: len(m.MentionedUserIds) > 0})
		if err != nil {
			return nil, err
		}
		effect = eff
		if eff == thread.StartTurn || eff == thread.Provision {
			var fresh []*bandav1.Event
			for _, e := range unseen {
				if e.Seq > t.DeliveredSeq {
					fresh = append(fresh, e)
				}
			}
			prompt = agentPrompt(t.OwnerID, names, fresh, by, m.Text)
			t.DeliveredSeq = t.LastSeq + 1 // the message stored below
			if eff == thread.Provision {   // suspended: deliver once the environment is back
				t.PendingPrompt = strings.TrimSpace(t.PendingPrompt + "\n\n" + prompt)
			}
		}
		events := append([]*bandav1.Event{messageEvent(by, m)}, stateEvents(t.State, next)...)
		t.State = next
		return events, nil
	})
	if err != nil {
		return nil, cerr(err)
	}
	s.broadcast(id, stored)
	if err := s.store.Mention(ctx, id, stored[0].Seq, by, m.MentionedUserIds); err != nil {
		slog.Error("store mentions", "thread", id, "err", err)
	}
	if effect == thread.StartTurn {
		s.deliver(ctx, id, prompt)
	}
	s.do(ctx, id, effect)
	return &bandav1.SendMessageResponse{}, nil
}

func (s *Service) Interrupt(ctx context.Context, req *bandav1.InterruptRequest) (*bandav1.InterruptResponse, error) {
	effect, err := s.apply(ctx, req.ThreadId, thread.Interrupt{By: user(ctx)})
	if err != nil {
		return nil, err
	}
	s.do(ctx, req.ThreadId, effect)
	return &bandav1.InterruptResponse{}, nil
}

func (s *Service) SetMode(ctx context.Context, req *bandav1.SetModeRequest) (*bandav1.SetModeResponse, error) {
	mode, ok := modeFromProto[req.Mode]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, "unknown mode")
	}
	_, err := s.apply(ctx, req.ThreadId, thread.SetMode{By: user(ctx), Mode: mode})
	return &bandav1.SetModeResponse{}, err
}

func (s *Service) Retry(ctx context.Context, req *bandav1.RetryRequest) (*bandav1.RetryResponse, error) {
	effect, err := s.apply(ctx, req.ThreadId, thread.Retry{By: user(ctx)})
	if err != nil {
		return nil, err
	}
	s.do(ctx, req.ThreadId, effect)
	return &bandav1.RetryResponse{}, nil
}

func (s *Service) WatchThread(ctx context.Context, req *bandav1.WatchThreadRequest, stream bandav1connect.ThreadServiceWatchThreadServerStream) error {
	ch := s.watch(req.ThreadId, user(ctx))
	defer s.unwatch(req.ThreadId, user(ctx), ch)

	// Subscribe before replaying so nothing falls in between; skip duplicates.
	last := req.AfterSeq
	stored, err := s.store.Events(ctx, req.ThreadId, last)
	if err != nil {
		return err
	}
	for _, e := range stored {
		if err := stream.Send(&bandav1.WatchThreadResponse{Event: e}); err != nil {
			return err
		}
		last = e.Seq
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-ch:
			if !ok {
				return connect.NewError(connect.CodeUnavailable, "watcher fell behind, reconnect")
			}
			if e.Seq != 0 && e.Seq <= last {
				continue
			}
			if e.Seq > last+1 { // concurrent updates were broadcast out of order: catch up from the store
				missed, err := s.store.Events(ctx, req.ThreadId, last)
				if err != nil {
					return err
				}
				for _, m := range missed {
					if err := stream.Send(&bandav1.WatchThreadResponse{Event: m}); err != nil {
						return err
					}
					last = m.Seq
				}
				continue
			}
			if err := stream.Send(&bandav1.WatchThreadResponse{Event: e}); err != nil {
				return err
			}
			if e.Seq != 0 {
				last = e.Seq
			}
		}
	}
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
	if login {
		if !s.store.CheckLoginToken(ctx, owner, hello.Token) {
			return connect.NewError(connect.CodeUnauthenticated, "unknown user or bad token")
		}
		id = loginKey(owner)
	} else {
		t, err := s.store.Get(ctx, id)
		if err != nil || subtle.ConstantTimeCompare([]byte(t.RunnerToken), []byte(hello.Token)) != 1 {
			return connect.NewError(connect.CodeUnauthenticated, "unknown thread or bad token")
		}
		owner = t.OwnerID
	}

	r := &runner{ch: make(chan *bandav1.ConnectResponse, 16), done: make(chan struct{})}
	s.mu.Lock()
	s.runners[id] = r
	s.mu.Unlock()
	defer close(r.done)
	defer func() {
		s.mu.Lock()
		mine := s.runners[id] == r
		if mine {
			delete(s.runners, id)
			if term := s.terminals[id]; term != nil { // the terminal dies with the runner
				close(term)
				delete(s.terminals, id)
			}
		}
		s.mu.Unlock()
		if mine && !login {
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
	var prompt string
	_, events, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
		next, effect, _ := thread.Apply(t.State, thread.EnvReady{Pending: t.PendingPrompt != ""})
		if effect == thread.StartTurn {
			prompt, t.PendingPrompt = t.PendingPrompt, ""
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
	if prompt != "" {
		s.deliver(ctx, id, prompt)
	}
	return nil
}

// apply runs an action (nil = none) through the state machine and stores the
// given events plus any status/mode change, then fans them out.
func (s *Service) apply(ctx context.Context, id string, a thread.Action, events ...*bandav1.Event) (thread.Effect, error) {
	return s.applyBecause(ctx, id, a, "", events...)
}

// applyBecause is apply with a reason for the status change.
func (s *Service) applyBecause(ctx context.Context, id string, a thread.Action, reason string, events ...*bandav1.Event) (thread.Effect, error) {
	var effect thread.Effect
	_, stored, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
		if a == nil {
			return events, nil
		}
		next, eff, err := thread.Apply(t.State, a)
		if err != nil {
			return nil, err
		}
		effect = eff
		changes := stateEvents(t.State, next)
		for _, c := range changes {
			if sc := c.GetStatusChanged(); sc != nil {
				sc.Reason = reason
			}
		}
		out := append(events, changes...)
		t.State = next
		return out, nil
	})
	if err != nil {
		return thread.None, cerr(err)
	}
	s.broadcast(id, stored)
	return effect, nil
}

// do carries out an effect. StartTurn needs a prompt, so callers handle it.
func (s *Service) do(ctx context.Context, id string, e thread.Effect) {
	switch e {
	case thread.StopTurn:
		s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Stop{Stop: &bandav1.Stop{}}})
	case thread.Provision:
		if t, err := s.store.Get(ctx, id); err == nil {
			s.provision(ctx, t)
		}
	case thread.SuspendEnv:
		if err := s.sandboxes.Suspend(ctx, id); err != nil {
			slog.Error("suspend sandbox", "thread", id, "err", err)
		}
	}
}

func (s *Service) provision(ctx context.Context, t store.Thread) {
	// Ensure replaces a running pod; its runner's disconnect is expected.
	s.mu.Lock()
	delete(s.runners, t.ID)
	s.mu.Unlock()
	st, err := s.store.Settings(ctx, t.OwnerID, harness)
	var secrets []store.Secret
	if err == nil {
		secrets, err = s.store.Secrets(ctx, t.OwnerID)
	}
	if err == nil {
		var names []string
		for _, sec := range secrets {
			names = append(names, sec.Name)
		}
		err = s.sandboxes.Ensure(ctx, t, st.AuthMethod, names)
	}
	if err != nil {
		slog.Error("provision sandbox", "thread", t.ID, "err", err)
		s.apply(context.WithoutCancel(ctx), t.ID, thread.EnvLost{})
	}
}

// deliver hands a prompt to the runner. Without one the environment is gone:
// keep the prompt for the next runner, which delivers it once it is ready.
func (s *Service) deliver(ctx context.Context, id, prompt string) {
	if s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Prompt{Prompt: &bandav1.Prompt{Text: prompt}}}) {
		return
	}
	_, events, err := s.store.Update(context.WithoutCancel(ctx), id, func(t *store.Thread) ([]*bandav1.Event, error) {
		t.PendingPrompt = strings.TrimSpace(t.PendingPrompt + "\n\n" + prompt)
		next, _, _ := thread.Apply(t.State, thread.EnvLost{})
		events := stateEvents(t.State, next)
		t.State = next
		return events, nil
	})
	if err != nil {
		slog.Error("keep undelivered prompt", "thread", id, "err", err)
		return
	}
	s.broadcast(id, events)
}

// sendRunner reports false if no runner is connected for the thread, or it
// stops taking commands.
func (s *Service) sendRunner(id string, msg *bandav1.ConnectResponse) bool {
	s.mu.Lock()
	r := s.runners[id]
	s.mu.Unlock()
	if r == nil {
		return false
	}
	select {
	case r.ch <- msg:
		return true
	case <-r.done:
	case <-time.After(10 * time.Second):
	}
	return false
}

// watch subscribes to a thread's events as user. Everyone, the new watcher
// included, then gets the current presence.
func (s *Service) watch(id, user string) chan *bandav1.Event {
	ch := make(chan *bandav1.Event, 256)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchers[id] == nil {
		s.watchers[id] = map[chan *bandav1.Event]struct{}{}
	}
	s.watchers[id][ch] = struct{}{}
	if s.present[id] == nil {
		s.present[id] = map[string]int{}
	}
	s.present[id][user]++
	s.broadcastLocked(id, []*bandav1.Event{s.presence(id)})
	return ch
}

func (s *Service) unwatch(id, user string, ch chan *bandav1.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watchers[id][ch]; ok {
		delete(s.watchers[id], ch)
		close(ch)
	}
	if s.present[id][user]--; s.present[id][user] > 0 {
		return // another tab of the same user is still here
	}
	delete(s.present[id], user)
	if len(s.present[id]) == 0 {
		delete(s.present, id)
	}
	s.broadcastLocked(id, []*bandav1.Event{s.presence(id)})
}

// presence is the ephemeral PresenceChanged event for a thread. Callers hold mu.
func (s *Service) presence(id string) *bandav1.Event {
	users := slices.Sorted(maps.Keys(s.present[id]))
	return &bandav1.Event{ThreadId: id, Time: timestamppb.Now(), Actor: "system",
		Kind: &bandav1.Event_PresenceChanged{PresenceChanged: &bandav1.PresenceChanged{UserIds: users}}}
}

func (s *Service) SendTyping(ctx context.Context, req *bandav1.TypingRequest) (*bandav1.TypingResponse, error) {
	s.broadcast(req.ThreadId, []*bandav1.Event{{ThreadId: req.ThreadId, Time: timestamppb.Now(), Actor: user(ctx),
		Kind: &bandav1.Event_Typing{Typing: &bandav1.Typing{}}}})
	return &bandav1.TypingResponse{}, nil
}

// broadcast fans events out. A watcher that cannot keep up is dropped and
// reconnects from its last seq.
// ponytail: in-memory fan-out, single API replica; Postgres LISTEN/NOTIFY for more.
func (s *Service) broadcast(id string, events []*bandav1.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastLocked(id, events)
}

func (s *Service) broadcastLocked(id string, events []*bandav1.Event) {
	for ch := range s.watchers[id] {
		for _, e := range events {
			select {
			case ch <- e:
				continue
			default:
			}
			delete(s.watchers[id], ch)
			close(ch)
			break
		}
	}
}

func messageEvent(by string, m *bandav1.Message) *bandav1.Event {
	return &bandav1.Event{Actor: by, Kind: &bandav1.Event_Message{Message: m}}
}

func stateEvents(old, next thread.State) []*bandav1.Event {
	var out []*bandav1.Event
	if old.Status != next.Status {
		out = append(out, &bandav1.Event{Actor: "system", Kind: &bandav1.Event_StatusChanged{
			StatusChanged: &bandav1.StatusChanged{Status: statusToProto[next.Status]}}})
	}
	if old.Mode != next.Mode {
		out = append(out, &bandav1.Event{Actor: "system", Kind: &bandav1.Event_ModeChanged{
			ModeChanged: &bandav1.ModeChanged{Mode: modeToProto[next.Mode]}}})
	}
	return out
}

func cerr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error())
	case errors.Is(err, thread.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err.Error())
	case errors.Is(err, thread.ErrNotReady):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error())
	}
	return err
}

var (
	statusToProto = map[thread.Status]bandav1.Status{
		thread.Starting:      bandav1.Status_STATUS_STARTING,
		thread.Working:       bandav1.Status_STATUS_WORKING,
		thread.AwaitingInput: bandav1.Status_STATUS_AWAITING_INPUT,
		thread.Errored:       bandav1.Status_STATUS_ERRORED,
		thread.Suspended:     bandav1.Status_STATUS_SUSPENDED,
	}
	modeToProto   = map[thread.Mode]bandav1.Mode{thread.AgentMode: bandav1.Mode_MODE_AGENT, thread.ChatMode: bandav1.Mode_MODE_CHAT}
	modeFromProto = map[bandav1.Mode]thread.Mode{bandav1.Mode_MODE_AGENT: thread.AgentMode, bandav1.Mode_MODE_CHAT: thread.ChatMode}
)

func toProto(t store.Thread) *bandav1.Thread {
	p := &bandav1.Thread{
		Id:         t.ID,
		OwnerId:    t.OwnerID,
		Title:      t.Title,
		RepoUrl:    t.RepoURL,
		Branch:     t.Branch,
		Model:      t.Model,
		Effort:     t.Effort,
		Status:     statusToProto[t.Status],
		Mode:       modeToProto[t.Mode],
		Diff:       t.Diff,
		CreatedAt:  timestamppb.New(t.CreatedAt),
		UpdatedAt:  timestamppb.New(t.UpdatedAt),
		BaseBranch: t.BaseBranch,
		Settled:    t.Settled,
	}
	if t.SnoozedUntil != nil {
		p.SnoozedUntil = timestamppb.New(*t.SnoozedUntil)
	}
	return p
}

func title(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	if r := []rune(line); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return line
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
