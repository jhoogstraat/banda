package api

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/thread"
)

func (s *Service) ListMentioned(ctx context.Context, _ *bandav1.ListMentionedRequest) (*bandav1.ListMentionedResponse, error) {
	threads, err := s.store.ListMentioned(ctx, user(ctx))
	if err != nil {
		return nil, err
	}
	res := &bandav1.ListMentionedResponse{}
	for _, t := range threads {
		res.Threads = append(res.Threads, toProto(t))
	}
	return res, nil
}

func (s *Service) Snooze(ctx context.Context, req *bandav1.SnoozeRequest) (*bandav1.SnoozeResponse, error) {
	var until *time.Time
	if req.Until != nil {
		u := req.Until.AsTime()
		until = &u
	}
	if err := s.store.SetSnooze(ctx, user(ctx), req.ThreadId, until); err != nil {
		return nil, err
	}
	s.pokeInbox(user(ctx))
	if until != nil {
		s.suspendIfOwner(ctx, req.ThreadId)
	}
	return &bandav1.SnoozeResponse{}, nil
}

func (s *Service) Settle(ctx context.Context, req *bandav1.SettleRequest) (*bandav1.SettleResponse, error) {
	if err := s.store.SetSettled(ctx, user(ctx), req.ThreadId, req.Settled); err != nil {
		return nil, err
	}
	s.pokeInbox(user(ctx))
	if req.Settled {
		s.suspendIfOwner(ctx, req.ThreadId)
	}
	return &bandav1.SettleResponse{}, nil
}

// suspendIfOwner pauses the environment when its owner snoozes or settles
// the thread. Unsnoozing resumes lazily, on the next prompt.
// ponytail: settled sandboxes are never deleted; add shutdownTime once branches are pushed by default.
func (s *Service) suspendIfOwner(ctx context.Context, id string) {
	t, err := s.store.Get(ctx, id)
	if err != nil || t.OwnerID != user(ctx) {
		return
	}
	if effect, err := s.apply(ctx, id, thread.Suspend{}); err == nil {
		s.do(ctx, id, effect)
	}
}

// SuspendIdle suspends environments that have been idle for longer than idle.
func (s *Service) SuspendIdle(ctx context.Context, idle time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		ids, err := s.store.Idle(ctx, time.Now().Add(-idle))
		if err != nil {
			slog.Error("find idle threads", "err", err)
			continue
		}
		for _, id := range ids {
			s.mu.Lock()
			_, inTerminal := s.terminals[id] // terminal use is activity too
			s.mu.Unlock()
			if inTerminal {
				continue
			}
			// ponytail: no per-thread lock around apply+do; a post racing this
			// suspend can leave the pod paused while starting until the next sweep.
			if effect, err := s.apply(ctx, id, thread.Suspend{}); err == nil {
				s.do(ctx, id, effect)
			}
		}
		s.suspendIdleLogins(ctx)
	}
}

func (s *Service) Push(ctx context.Context, req *bandav1.PushRequest) (*bandav1.PushResponse, error) {
	t, err := s.store.Get(ctx, req.ThreadId)
	if err != nil {
		return nil, cerr(err)
	}
	if t.OwnerID != user(ctx) {
		return nil, cerr(thread.ErrForbidden)
	}
	if !s.sendRunner(req.ThreadId, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Push{Push: &bandav1.Push{}}}) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the environment is not running")
	}
	return &bandav1.PushResponse{}, nil
}
