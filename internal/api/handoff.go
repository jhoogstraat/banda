package api

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// A handoff passes a thread to a teammate: its next turns run on their
// subscription with their secrets, so they must accept it. The branch,
// checkout and chat stay; Claude Code's transcript is on the old owner's
// Claude volume, so the new owner's session starts fresh from the
// conversation and the briefing the agent wrote for them.

func (s *Service) HandoffOffer(ctx context.Context, req *bandav1.HandoffOfferRequest) (*bandav1.HandoffOfferResponse, error) {
	return &bandav1.HandoffOfferResponse{}, s.offer(ctx, req.ThreadId, user(ctx), user(ctx), req.ToUserId, req.Note)
}

// offer offers thread id to the teammate `to` for its owner `by`, posted as
// actor: the owner, or their agent ("agent:<id>", through MCP), whose note is
// its briefing: the offer then gives no reason for lacking one.
func (s *Service) offer(ctx context.Context, id, by, actor, to, note string) error {
	note = strings.TrimSpace(note)
	names := s.names(ctx)
	if names[to] == "" {
		return connect.NewError(connect.CodeInvalidArgument, "no such teammate")
	}
	var effect thread.Effect
	var prompt string
	var images []string
	unlock := s.lockDelivery(id)
	var stored []*bandav1.Event
	err := retryStale(func() error {
		u, err := s.readUnseen(ctx, id, nil)
		if err != nil {
			return err
		}
		_, stored, err = s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
			next, eff, err := thread.Apply(t.State, thread.Offer{By: by, To: to})
			if err != nil {
				return nil, err
			}
			effect = eff
			offer := &bandav1.HandoffOffered{ToUserId: to, Note: note}
			if eff == thread.StartTurn {
				if err := u.fresh(t); err != nil {
					return nil, err
				}
				prompt, images = turnPrompt(t, eff, names, u.events, by, false, &bandav1.Message{Text: briefingRequest(names, by, to, note)}, u.texts)
			} else if actor == by {
				offer.NoBriefing = map[thread.Status]string{thread.Working: "the agent is working", thread.Suspended: "the environment is paused",
					thread.Starting: "the environment is starting", thread.Errored: "the environment is unavailable"}[t.Status]
			}
			events := append([]*bandav1.Event{{Actor: actor, Kind: &bandav1.Event_HandoffOffered{HandoffOffered: offer}}}, stateEvents(t.State, next)...)
			t.State = next
			return events, nil
		})
		return err
	})
	if err != nil {
		unlock()
		return err
	}
	s.broadcast(id, stored)
	if effect == thread.StartTurn {
		s.deliver(ctx, id, prompt, images, false)
	}
	unlock()
	s.pokeInbox(to)
	return nil
}

// briefingRequest asks the agent, as the owner's last turn, to brief the
// teammate who takes the thread over.
func briefingRequest(names map[string]string, from, to, note string) string {
	text := fmt.Sprintf("%s is handing this thread over to %s. Your next turns run in a new Claude Code session for %s that gets this conversation, but not your tool calls. "+
		"Write a short briefing for %s: the state of the work, what's done, what's next and the open risks.", actorName(names, from), actorName(names, to), actorName(names, to), actorName(names, to))
	if note != "" {
		text += fmt.Sprintf(" %s's note for them: %s", actorName(names, from), note)
	}
	return text
}

func (s *Service) HandoffAccept(ctx context.Context, req *bandav1.HandoffAcceptRequest) (*bandav1.HandoffAcceptResponse, error) {
	by, names := user(ctx), s.names(ctx)
	var from string
	_, stored, err := s.store.Update(ctx, req.ThreadId, func(tx *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		next, _, err := thread.Apply(t.State, thread.Accept{By: by})
		if err != nil {
			return nil, err
		}
		events, err := tx.EventsOf(ctx, t.ID, 0, "message", "agentText", "handoffOffered")
		if err != nil {
			return nil, err
		}
		offer := lastOffer(events)
		// With the owner, under the row lock: the old pod's token stops working
		// now, not once provision gets to it.
		if err := tx.DropRunnerToken(ctx, t.ID); err != nil {
			return nil, err
		}
		from = t.OwnerID
		// The conversation holds every message so far, also one waiting for a start.
		t.PendingPrompt, t.PendingImages, t.PendingTeammate = handoffPrompt(names, *t, events, offer, by), nil, false
		t.DeliveredSeq = t.LastSeq + 1 // the event stored next
		t.AllowedTools = nil           // "always allow" was the old owner's choice, for their environment
		t.Handoffs++
		out := append([]*bandav1.Event{{Actor: by, Kind: &bandav1.Event_HandoffResolved{HandoffResolved: &bandav1.HandoffResolved{
			OfferSeq: offer.GetSeq(), Accepted: true}}}}, stateEvents(t.State, next)...)
		st, err := tx.Settings(ctx, by, harness)
		if err != nil {
			return nil, err
		}
		t.State = next
		if t.APIKey = st.AuthMethod == "api_key"; !t.APIKey {
			withdrawn, err := withdrawTurns(ctx, tx, t, or(names[by], by))
			return append(out, withdrawn...), err
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(req.ThreadId, stored)
	s.unpark(ctx, req.ThreadId) // the old owner's snooze or settle
	s.do(ctx, req.ThreadId, thread.Provision)
	s.pokeInbox(by, from)
	return &bandav1.HandoffAcceptResponse{}, nil
}

func (s *Service) HandoffDecline(ctx context.Context, req *bandav1.HandoffDeclineRequest) (*bandav1.HandoffDeclineResponse, error) {
	by := user(ctx)
	var to string
	_, stored, err := s.store.Update(ctx, req.ThreadId, func(tx *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		to = t.HandoffTo
		next, _, err := thread.Apply(t.State, thread.Withdraw{By: by})
		if err != nil {
			return nil, err
		}
		events, err := tx.EventsOf(ctx, t.ID, 0, "handoffOffered")
		if err != nil {
			return nil, err
		}
		t.State = next
		return []*bandav1.Event{{Actor: by, Kind: &bandav1.Event_HandoffResolved{HandoffResolved: &bandav1.HandoffResolved{
			OfferSeq: lastOffer(events).GetSeq()}}}}, nil
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(req.ThreadId, stored)
	s.pokeInbox(to)
	return &bandav1.HandoffDeclineResponse{}, nil
}

// lastOffer is the latest HandoffOffered event among events, or nil.
func lastOffer(events []*bandav1.Event) *bandav1.Event {
	var offer *bandav1.Event
	for _, e := range events {
		if e.GetHandoffOffered() != nil {
			offer = e
		}
	}
	return offer
}

// handoffPrompt is the new owner's first prompt: the thread, the conversation
// up to the offer, the briefing the agent wrote after it, and what was said
// since.
func handoffPrompt(names map[string]string, t store.Thread, events []*bandav1.Event, offer *bandav1.Event, to string) string {
	var said []*bandav1.Event
	var briefing []string
	for _, e := range events {
		if offer != nil && e.Seq > offer.Seq && e.GetAgentText() != nil {
			briefing = append(briefing, e.GetAgentText().Text)
		} else {
			said = append(said, e)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This is Banda thread %s (%q) on branch %s. %s handed it over to %s, who you work for from now on. "+
		"This is a new Claude Code session: the checkout holds all the work so far. The conversation so far (people's messages and your replies, without tool calls):\n\n",
		t.ID, t.Title, t.Branch, actorName(names, t.OwnerID), actorName(names, to))
	b.WriteString(conversation(names, said))
	note := offer.GetHandoffOffered().GetNote()
	if strings.HasPrefix(offer.GetActor(), "agent:") { // the agent's own briefing
		briefing = append([]string{note}, briefing...)
	} else if note != "" {
		fmt.Fprintf(&b, "\n\n%s's note on the handover: %s", actorName(names, t.OwnerID), note)
	}
	if len(briefing) > 0 {
		fmt.Fprintf(&b, "\n\n---\n\nYour briefing for %s, written before the handover:\n\n%s", actorName(names, to), strings.Join(briefing, "\n\n"))
	}
	fmt.Fprintf(&b, "\n\n---\n\nTell %s in a line or two that you have the context, then wait for instructions.", actorName(names, to))
	return b.String()
}
