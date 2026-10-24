package api

import (
	"context"
	"maps"
	"math"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
)

func (s *Service) WatchThread(ctx context.Context, req *bandav1.WatchThreadRequest, stream bandav1connect.ThreadServiceWatchThreadServerStream) error {
	ch, behind := s.watch(req.ThreadId, user(ctx))
	defer s.unwatch(req.ThreadId, user(ctx), ch)

	// Subscribe before replaying so nothing falls in between; skip duplicates.
	last := req.AfterSeq
	var stored []*bandav1.Event
	var err error
	if last == 0 && req.Tail > 0 {
		stored, err = s.store.Before(ctx, req.ThreadId, math.MaxInt64, int(req.Tail))
		if err == nil && len(stored) > 0 && stored[0].Seq > 1 {
			for _, kind := range []string{"planChanged", "slashCommands"} {
				var latest []*bandav1.Event
				if latest, err = s.store.LatestOf(ctx, req.ThreadId, stored[0].Seq, 1, kind); err != nil {
					break
				}
				for _, e := range latest {
					e.Context = true
				}
				stored = append(latest, stored...)
			}
		}
	} else {
		stored, err = s.store.Events(ctx, req.ThreadId, last)
	}
	if err != nil {
		return err
	}
	for _, e := range stored {
		if err := stream.Send(&bandav1.WatchThreadResponse{Event: slim(e)}); err != nil {
			return err
		}
		if !e.Context {
			last = e.Seq
		}
	}
	// catchUp sends the stored events after last.
	catchUp := func() error {
		missed, err := s.store.Events(ctx, req.ThreadId, last)
		for _, m := range missed {
			if err == nil {
				err = stream.Send(&bandav1.WatchThreadResponse{Event: slim(m)})
			}
			last = m.Seq
		}
		return err
	}
	deliver := func(e *bandav1.Event) error {
		if e.Seq != 0 && e.Seq <= last {
			return nil
		}
		if e.Seq > last+1 { // missed some (concurrent updates broadcast out of order, or it fell behind)
			return catchUp()
		}
		if e.Seq != 0 {
			last = e.Seq
		}
		return stream.Send(&bandav1.WatchThreadResponse{Event: slim(e)})
	}
	tick := time.NewTicker(s.Keepalive)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := stream.Send(&bandav1.WatchThreadResponse{}); err != nil {
				return err
			}
		case e := <-ch:
			if err := deliver(e); err != nil {
				return err
			}
		case <-behind:
			// Events didn't fit: the ephemeral ones are lost, the stored ones are in the store.
			// What did fit comes first, so deltas don't land after their text.
			for drained := false; !drained; {
				select {
				case e := <-ch:
					err = deliver(e)
				default:
					drained, err = true, catchUp()
				}
				if err != nil {
					return err
				}
			}
		}
	}
}

// watch subscribes to a thread's events as user. Everyone, the new watcher
// included, then gets the current presence. behind gets a value when an
// event didn't fit into the channel.
func (s *Service) watch(id, user string) (ch chan *bandav1.Event, behind chan struct{}) {
	ch, behind = make(chan *bandav1.Event, s.WatchBuffer), make(chan struct{}, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchers[id] == nil {
		s.watchers[id] = map[chan *bandav1.Event]chan struct{}{}
	}
	s.watchers[id][ch] = behind
	if s.present[id] == nil {
		s.present[id] = map[string]int{}
	}
	s.present[id][user]++
	s.broadcastLocked(id, []*bandav1.Event{s.presence(id)})
	s.presenceChanged()
	return ch, behind
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
	s.presenceChanged()
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

// broadcast fans events out. A watcher that cannot keep up misses them and
// catches up from the store.
// ponytail: in-memory fan-out, single API replica; Postgres LISTEN/NOTIFY for more.
func (s *Service) broadcast(id string, events []*bandav1.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inboxChanged(id, events)
	s.broadcastLocked(id, events)
	s.reportToParent(id, events)
}

func (s *Service) broadcastLocked(id string, events []*bandav1.Event) {
	for ch, behind := range s.watchers[id] {
		for _, e := range events {
			select {
			case ch <- e:
			default:
				select {
				case behind <- struct{}{}:
				default:
				}
			}
		}
	}
}
