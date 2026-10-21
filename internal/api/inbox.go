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
		return &bandav1.InboxWatchResponse{Threads: mine.Threads, Mentioned: mentioned.Threads},
			append(mine.Threads, mentioned.Threads...), nil
	}, stream.Send)
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
		res := &bandav1.TeamWatchResponse{}
		s.mu.Lock()
		for _, t := range threads {
			p := toProto(t)
			p.PresentUserIds = slices.Sorted(maps.Keys(s.present[t.ID]))
			res.Threads = append(res.Threads, p)
		}
		s.mu.Unlock()
		return res, nil, nil
	}, stream.Send)
}

// follow registers w, then sends snapshot() at once and again each time w is
// woken, at most every delay and only if it changed. Snapshot's threads
// become w.ids.
func follow[T proto.Message](ctx context.Context, s *Service, w *inboxWatcher, delay time.Duration,
	snapshot func() (T, []*bandav1.Thread, error), send func(T) error) error {
	s.mu.Lock()
	s.inbox[w] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inbox, w)
		s.mu.Unlock()
	}()

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
		select {
		case <-ctx.Done():
			return nil
		case <-w.poke:
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
	for w := range s.inbox {
		if w.team || w.ids[id] || events[0].Seq == 1 {
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
