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
		s.scheduleDelete(ctx, req.ThreadId) // also when it was suspended already
	} else {
		s.cancelDelete(ctx, req.ThreadId)
	}
	return &bandav1.SettleResponse{}, nil
}

// scheduleDelete has a suspended environment deleted DeleteSettledAfter its
// owner settled the thread, but only if all its work is on origin.
func (s *Service) scheduleDelete(ctx context.Context, id string) {
	t, err := s.store.Get(ctx, id)
	if err != nil || t.Status != thread.Suspended || t.AutoPush != bandav1.AutoPushState_AUTO_PUSH_STATE_PUSHED.String() {
		return
	}
	settled, err := s.store.SettledAt(ctx, t.OwnerID, id)
	if err != nil || settled == nil {
		return
	}
	at := settled.Add(s.DeleteSettledAfter)
	if err := s.sandboxes.ScheduleDelete(ctx, id, &at); err != nil {
		slog.Error("schedule environment deletion", "thread", id, "err", err)
		return
	}
	if err := s.store.SetEnvDeleteAt(ctx, id, &at); err != nil {
		slog.Error("store environment deletion", "thread", id, "err", err)
	}
}

// cancelDelete keeps the environment when its owner unsettles the thread
// before it was deleted.
func (s *Service) cancelDelete(ctx context.Context, id string) {
	t, err := s.owned(ctx, id)
	if err != nil || t.EnvDeleteAt == nil || !t.EnvDeleteAt.After(time.Now()) {
		return
	}
	if err := s.sandboxes.ScheduleDelete(ctx, id, nil); err != nil {
		slog.Error("cancel environment deletion", "thread", id, "err", err)
		return
	}
	if err := s.store.SetEnvDeleteAt(ctx, id, nil); err != nil {
		slog.Error("store environment deletion", "thread", id, "err", err)
	}
}

func (s *Service) EnvironmentRecreate(ctx context.Context, req *bandav1.EnvironmentRecreateRequest) (*bandav1.EnvironmentRecreateResponse, error) {
	t, err := s.store.Get(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	if t.EnvDeleteAt == nil || t.EnvDeleteAt.After(time.Now()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the environment was not deleted")
	}
	effect, err := s.apply(ctx, req.ThreadId, thread.Wake{By: user(ctx)})
	if err != nil {
		return nil, err
	}
	s.do(ctx, req.ThreadId, effect)
	return &bandav1.EnvironmentRecreateResponse{}, nil
}

// suspendIfOwner pauses the environment when its owner snoozes or settles
// the thread. Unsnoozing resumes lazily, on the next prompt.
func (s *Service) suspendIfOwner(ctx context.Context, id string) {
	if _, err := s.owned(ctx, id); err != nil {
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
	if _, err := s.owned(ctx, req.ThreadId); err != nil {
		return nil, err
	}
	if !s.sendRunner(req.ThreadId, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Push{Push: &bandav1.Push{}}}) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the environment is not running")
	}
	return &bandav1.PushResponse{}, nil
}
