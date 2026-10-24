package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
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
		s.suspendIfOwner(ctx, req.ThreadId, "snoozed")
	} else {
		s.unpark(ctx, req.ThreadId)
	}
	return &bandav1.SnoozeResponse{}, nil
}

func (s *Service) Settle(ctx context.Context, req *bandav1.SettleRequest) (*bandav1.SettleResponse, error) {
	if err := s.store.SetSettled(ctx, user(ctx), req.ThreadId, req.Settled); err != nil {
		return nil, err
	}
	s.pokeInbox(user(ctx))
	if req.Settled {
		s.suspendIfOwner(ctx, req.ThreadId, "settled")
		s.scheduleDelete(ctx, req.ThreadId) // also when it was suspended already
	} else {
		s.unpark(ctx, req.ThreadId)
		s.cancelDelete(ctx, req.ThreadId)
	}
	return &bandav1.SettleResponse{}, nil
}

// scheduleDelete has a suspended environment deleted DeleteSettledAfter its
// owner settled the thread, but only if all its work was on origin when it
// was suspended.
func (s *Service) scheduleDelete(ctx context.Context, id string) {
	t, err := s.store.Get(ctx, id)
	if err != nil || t.Status != thread.Suspended || !t.EnvPushed {
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
func (s *Service) suspendIfOwner(ctx context.Context, id, how string) {
	if _, err := s.owned(ctx, id); err != nil {
		return
	}
	s.mu.Lock()
	s.parking[id] = how
	s.mu.Unlock()
	s.park(ctx, id)
}

// unpark: the owner unsnoozed or unsettled the thread before it was suspended.
func (s *Service) unpark(ctx context.Context, id string) {
	if _, err := s.owned(ctx, id); err != nil {
		return
	}
	s.mu.Lock()
	delete(s.parking, id)
	s.mu.Unlock()
}

// park suspends a thread its owner snoozed or settled. A running turn
// finishes first (its end parks it again), unless it waits for a permission:
// that would keep it open for good, out of the owner's inbox, so the request
// is denied and the turn interrupted.
func (s *Service) park(ctx context.Context, id string) {
	s.mu.Lock()
	how, ok := s.parking[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return
	}
	if t.Status == thread.Working {
		if len(t.Permissions) > 0 {
			s.denyPermissions(ctx, id, "The thread's owner "+how+" it, so it is being suspended.")
			s.do(ctx, id, thread.StopTurn)
		}
		return
	}
	s.mu.Lock()
	delete(s.parking, id)
	s.mu.Unlock()
	if effect, err := s.apply(ctx, id, thread.Suspend{}); err == nil {
		s.do(ctx, id, effect)
	}
}

// restartOnKey suspends the thread, unless a turn runs (its end calls this
// again), if its environment started on its owner's Claude subscription but
// they are on an API key now: it resumes on the key with the next prompt,
// and teammates can run the agent then (thread.State.TeammateTurns).
func (s *Service) restartOnKey(ctx context.Context, id string) {
	t, err := s.store.Get(ctx, id)
	if err != nil || !t.APIKey || t.EnvAPIKey || t.Status != thread.AwaitingInput {
		return // most calls: no write
	}
	if effect, err := s.apply(ctx, id, thread.Suspend{ToKey: true}); err == nil {
		s.do(ctx, id, effect)
	}
}

// SuspendIdle suspends environments that have been idle for longer than idle,
// checking every so often.
func (s *Service) SuspendIdle(ctx context.Context, idle, every time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
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
		s.watchStarts(ctx)
		s.deleteIdle(ctx)
		s.reportDeleted(ctx)
	}
}

// watchStarts watches the starting threads no watchStart looks at, e.g.
// after an API restart; one started in between waits StartPoll to be judged.
func (s *Service) watchStarts(ctx context.Context) {
	ids, err := s.store.Starting(ctx)
	if err != nil {
		slog.Error("find starting threads", "err", err)
	}
	for _, id := range ids {
		s.mu.Lock()
		_, watched := s.starts[id]
		s.mu.Unlock()
		if !watched {
			s.watchStart(id)
		}
	}
}

// deleteIdle deletes the environments due by idleDue now, the same way as
// settled ones: the controller deletes the Sandbox and its home volume, and
// reportDeleted tells the chat.
func (s *Service) deleteIdle(ctx context.Context) {
	if s.DeleteIdleAfter <= 0 {
		return
	}
	ids, err := s.store.IdlePushed(ctx, time.Now().Add(-s.DeleteIdleAfter))
	if err != nil {
		slog.Error("find idle pushed environments", "err", err)
		return
	}
	for _, id := range ids {
		// Under the thread's lock: a post or wake since the query wins.
		_, _, err := s.store.Update(ctx, id, func(tx *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
			now := time.Now()
			if !idleDue(*t, now, s.DeleteIdleAfter) {
				return nil, errNotDue
			}
			if err := s.sandboxes.ScheduleDelete(ctx, id, &now); err != nil {
				return nil, err
			}
			return nil, tx.SetEnvDeleteAt(ctx, id, &now)
		})
		if err != nil && !errors.Is(err, errNotDue) {
			slog.Error("delete idle environment", "thread", id, "err", err)
		}
	}
}

var errNotDue = errors.New("not due for deletion")

// idleDue: the environment is suspended with all its work on origin (never
// without a repository), no deletion is scheduled, and the thread has had no
// activity for after.
func idleDue(t store.Thread, now time.Time, after time.Duration) bool {
	return after > 0 && t.Status == thread.Suspended && t.EnvPushed && t.RepoURL != "" && t.EnvDeleteAt == nil &&
		t.UpdatedAt.Before(now.Add(-after))
}

// reportDeleted tells the chats of environments deleted since the last sweep.
func (s *Service) reportDeleted(ctx context.Context) {
	ids, err := s.store.ClaimDeleted(ctx)
	if err != nil {
		slog.Error("find deleted environments", "err", err)
	}
	for _, id := range ids {
		e := &bandav1.Event{Actor: "system", Kind: &bandav1.Event_Notice{Notice: &bandav1.Notice{
			Text: "The unused environment was deleted; its branch is on origin. Recreate it to continue."}}}
		if _, err := s.apply(ctx, id, nil, e); err != nil {
			slog.Error("report deleted environment", "thread", id, "err", err)
		}
	}
}

func (s *Service) Push(ctx context.Context, req *bandav1.PushRequest) (*bandav1.PushResponse, error) {
	t, err := s.owned(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	if t.RepoURL == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the thread has no repository to push to")
	}
	if !s.sendRunner(req.ThreadId, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Push{Push: &bandav1.Push{}}}) {
		return nil, errNotRunning
	}
	return &bandav1.PushResponse{}, nil
}
