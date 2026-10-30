package api

import (
	"context"
	"maps"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
)

// inboxDelay and teamDelay coalesce bursts of events (a streaming agent) into
// one snapshot. The team board shows recent threads only.
const inboxDelay, teamDelay, teamWindow = 250 * time.Millisecond, time.Second, 7 * 24 * time.Hour

// inboxWatcher is one InboxWatch stream, woken through poke.
type inboxWatcher struct {
	user string
	poke chan struct{}
	ids  map[string]bool // threads in the last snapshot; guarded by Service.mu
	team bool            // a TeamWatch: every thread counts
}

func (w *inboxWatcher) wake() {
	select {
	case w.poke <- struct{}{}:
	default: // already woken
	}
}

// InboxWatch sends the caller's sidebar whenever it may have changed.
// ponytail: full snapshots, two queries per change per watcher; send deltas if teams or sidebars grow large.
func (s *Service) InboxWatch(ctx context.Context, _ *bandav1.InboxWatchRequest, stream bandav1connect.ThreadServiceInboxWatchServerStream) error {
	w := &inboxWatcher{user: user(ctx), poke: make(chan struct{}, 1)}
	return follow(ctx, s, w, inboxDelay, func() (*bandav1.InboxWatchResponse, []*bandav1.Thread, error) {
		mine, err := s.ListThreads(ctx, &bandav1.ListThreadsRequest{})
		if err != nil {
			return nil, nil, err
		}
		mentioned, err := s.ListMentioned(ctx, &bandav1.ListMentionedRequest{})
		if err != nil {
			return nil, nil, err
		}
		res := &bandav1.InboxWatchResponse{Threads: mine.Threads, Mentioned: mentioned.Threads}
		if res.Decisions, err = s.decisions(ctx); err != nil {
			return nil, nil, err
		}
		st, err := s.store.Settings(ctx, user(ctx), "claude")
		if err != nil {
			return nil, nil, err
		}
		res.LoginNeeded = st.AuthMethod == "subscription" && st.LoginState == "logged_out"
		watched := append(mine.Threads, mentioned.Threads...)
		for _, d := range res.Decisions {
			watched = append(watched, d.Thread)
		}
		return res, watched, nil
	}, stream.Send, &bandav1.InboxWatchResponse{Keepalive: true})
}

// decisions are the requests the caller can answer, with their threads.
func (s *Service) decisions(ctx context.Context) ([]*bandav1.Decision, error) {
	events, err := s.store.Decisions(ctx, user(ctx))
	if err != nil || len(events) == 0 {
		return nil, err
	}
	var ids []string
	for _, e := range events {
		ids = append(ids, e.ThreadId)
	}
	threads, err := s.store.Threads(ctx, ids, user(ctx))
	if err != nil {
		return nil, err
	}
	byID := map[string]*bandav1.Thread{}
	for _, t := range threads {
		byID[t.ID] = toProto(t)
	}
	var out []*bandav1.Decision
	for _, e := range events {
		out = append(out, &bandav1.Decision{Thread: byID[e.ThreadId], Request: e})
	}
	return out, nil
}

// TeamWatch sends everyone's recent threads with their presence whenever a
// stored event or a presence change may have changed them.
// ponytail: one query of all recent threads per second per watcher while anything happens; fine for 10 people × 20 threads.
func (s *Service) TeamWatch(ctx context.Context, _ *bandav1.TeamWatchRequest, stream bandav1connect.ThreadServiceTeamWatchServerStream) error {
	w := &inboxWatcher{user: user(ctx), poke: make(chan struct{}, 1), team: true}
	return follow(ctx, s, w, teamDelay, func() (*bandav1.TeamWatchResponse, []*bandav1.Thread, error) {
		threads, err := s.store.Team(ctx, user(ctx), time.Now().Add(-teamWindow))
		if err != nil {
			return nil, nil, err
		}
		var ids []string
		for _, t := range threads {
			ids = append(ids, t.ID)
		}
		overlaps, err := s.overlaps(ctx, ids...)
		if err != nil {
			return nil, nil, err
		}
		res := &bandav1.TeamWatchResponse{}
		s.mu.Lock()
		for _, t := range threads {
			p := s.listed(t)
			p.Overlaps = overlaps[t.ID]
			p.PresentUserIds = slices.Sorted(maps.Keys(s.present[t.ID]))
			res.Threads = append(res.Threads, p)
		}
		s.mu.Unlock()
		return res, nil, nil
	}, stream.Send, &bandav1.TeamWatchResponse{Keepalive: true})
}

// follow registers w, then sends snapshot() at once and again each time w is
// woken, at most every delay and only if it changed, and keepalive while
// nothing does. Snapshot's threads become w.ids.
func follow[T proto.Message](ctx context.Context, s *Service, w *inboxWatcher, delay time.Duration,
	snapshot func() (T, []*bandav1.Thread, error), send func(T) error, keepalive T) error {
	s.mu.Lock()
	s.inbox[w] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inbox, w)
		s.mu.Unlock()
	}()

	tick := time.NewTicker(s.Keepalive)
	defer tick.Stop()
	var last proto.Message
	for {
		res, threads, err := snapshot()
		if err != nil {
			return err
		}
		ids := map[string]bool{}
		for _, t := range threads {
			ids[t.Id] = true
		}
		s.mu.Lock()
		w.ids = ids
		s.mu.Unlock()
		if last == nil || !proto.Equal(res, last) {
			if err := send(res); err != nil {
				return err
			}
			last = res
		}
	quiet:
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
				if err := send(keepalive); err != nil {
					return err
				}
			case <-w.poke:
				break quiet
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (s *Service) InboxMarkRead(ctx context.Context, req *bandav1.InboxMarkReadRequest) (*bandav1.InboxMarkReadResponse, error) {
	if err := s.store.MarkRead(ctx, user(ctx), req.ThreadId); err != nil {
		return nil, err
	}
	s.pokeInbox(user(ctx))
	return &bandav1.InboxMarkReadResponse{}, nil
}

// inboxChanged wakes the inbox watchers that show thread id, everyone's for a
// new thread (its owner's watchers don't know it yet), and all team watchers.
// Callers hold s.mu.
func (s *Service) inboxChanged(id string, events []*bandav1.Event) {
	if len(events) == 0 || events[0].Seq == 0 { // ephemeral deltas change no sidebar
		return
	}
	asks := slices.ContainsFunc(events, func(e *bandav1.Event) bool { // may be a decision of anyone's
		return e.GetPermissionRequested() != nil || e.GetTurnRequest() != nil
	})
	for w := range s.inbox {
		if w.team || w.ids[id] || events[0].Seq == 1 || asks {
			w.wake()
		}
	}
}

// presenceChanged wakes the team watchers. Callers hold s.mu.
func (s *Service) presenceChanged() {
	for w := range s.inbox {
		if w.team {
			w.wake()
		}
	}
}

// pokeInbox wakes the users' inbox watchers, after a change only they see
// (read, snooze, settle, mentions).
func (s *Service) pokeInbox(users ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for w := range s.inbox {
		if slices.Contains(users, w.user) {
			w.wake()
		}
	}
}
