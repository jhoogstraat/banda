package api

import (
	"context"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
)

// inboxDelay coalesces bursts of events (a streaming agent) into one snapshot.
const inboxDelay = 250 * time.Millisecond

// inboxWatcher is one InboxWatch stream, woken through poke.
type inboxWatcher struct {
	user string
	poke chan struct{}
	ids  map[string]bool // threads in the last snapshot; guarded by Service.mu
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
	s.mu.Lock()
	s.inbox[w] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inbox, w)
		s.mu.Unlock()
	}()

	var last *bandav1.InboxWatchResponse
	for {
		mine, err := s.ListThreads(ctx, &bandav1.ListThreadsRequest{})
		if err != nil {
			return err
		}
		mentioned, err := s.ListMentioned(ctx, &bandav1.ListMentionedRequest{})
		if err != nil {
			return err
		}
		res := &bandav1.InboxWatchResponse{Threads: mine.Threads, Mentioned: mentioned.Threads}
		ids := map[string]bool{}
		for _, t := range append(res.Threads, res.Mentioned...) {
			ids[t.Id] = true
		}
		s.mu.Lock()
		w.ids = ids
		s.mu.Unlock()
		if !proto.Equal(res, last) {
			if err := stream.Send(res); err != nil {
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
		case <-time.After(inboxDelay):
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

// inboxChanged wakes the inbox watchers that show thread id, and everyone's
// for a new thread (its owner's watchers don't know it yet). Callers hold s.mu.
func (s *Service) inboxChanged(id string, events []*bandav1.Event) {
	if len(events) == 0 || events[0].Seq == 0 { // ephemeral deltas change no sidebar
		return
	}
	for w := range s.inbox {
		if w.ids[id] || events[0].Seq == 1 {
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
