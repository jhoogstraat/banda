package api

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

func (s *Service) SendMessage(ctx context.Context, req *bandav1.SendMessageRequest) (*bandav1.SendMessageResponse, error) {
	m := req.Message
	if m == nil || strings.TrimSpace(m.Text) == "" && len(m.Review) == 0 && len(m.Attachments) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "message text is required")
	}
	for _, c := range m.Review {
		if c.Path == "" || strings.TrimSpace(c.Body) == "" || c.StartLine < 1 || c.EndLine < c.StartLine {
			return nil, connect.NewError(connect.CodeInvalidArgument, "a review comment needs a path, lines and text")
		}
	}
	by, id := user(ctx), req.ThreadId
	if err := s.checkAttachments(ctx, m, by); err != nil {
		return nil, err
	}
	names := s.names(ctx)
	// An agent posting through MCP (as "agent:<thread>") acts for its
	// thread's owner: e.g. a sub-thread handed to a teammate no longer wakes
	// its parent without the parent's owner.
	agentOwner := ""
	if src, ok := strings.CutPrefix(by, "agent:"); ok {
		t, err := s.store.Get(ctx, src)
		if err != nil {
			return nil, err
		}
		agentOwner = t.OwnerID
	}

	var effect thread.Effect
	var prompt string
	var images []string
	var teammate bool
	unlock := s.lockDelivery(id)
	var stored []*bandav1.Event
	err := retryStale(func() error {
		u, err := s.readUnseen(ctx, id, m)
		if err != nil {
			return err
		}
		_, stored, err = s.store.Update(ctx, id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
			next, eff, err := thread.Apply(t.State, thread.Send{By: by, MentionsAgent: m.MentionsAgent,
				MentionsUsers: len(m.MentionedUserIds) > 0, OwnersAgent: agentOwner == t.OwnerID, Review: len(m.Review) > 0})
			if err != nil {
				return nil, err
			}
			effect = eff
			posted := messageEvent(by, m)
			if eff == thread.RequestTurn {
				posted = &bandav1.Event{Actor: by, Kind: &bandav1.Event_TurnRequest{TurnRequest: &bandav1.TurnRequest{Message: m}}}
			}
			if eff == thread.StartTurn || eff == thread.Provision {
				if err := u.fresh(t); err != nil {
					return nil, err
				}
				teammate = by != t.OwnerID && agentOwner != t.OwnerID
				prompt, images = turnPrompt(t, eff, names, u.events, by, teammate, m, u.texts)
			}
			events := append([]*bandav1.Event{posted}, stateEvents(t.State, next)...)
			for _, e := range events[1:] {
				if mc := e.GetModeChanged(); mc != nil { // the chat says who switched it and why
					e.Actor, mc.Reason = by, "posted"
					if by == t.OwnerID {
						mc.Reason = "mentioned someone"
					}
				}
			}
			t.State = next
			return events, nil
		})
		return err
	})
	if err != nil {
		unlock()
		return nil, err
	}
	s.broadcast(id, stored)
	if effect == thread.StartTurn {
		s.deliver(ctx, id, prompt, images, teammate)
	}
	unlock()
	if err := s.store.Mention(ctx, id, stored[0].Seq, by, m.MentionedUserIds); err != nil {
		slog.Error("store mentions", "thread", id, "err", err)
	}
	if !strings.HasPrefix(by, "agent:") { // an agent posting through MCP has no inbox
		if err := s.store.MarkRead(ctx, by, id); err != nil { // posting means having read it
			slog.Error("mark read", "thread", id, "err", err)
		}
		s.pokeInbox(by)
	}
	s.pokeInbox(m.MentionedUserIds...)
	s.do(ctx, id, effect)
	return &bandav1.SendMessageResponse{}, nil
}

// turnPrompt is the prompt for a turn by `by` that starts with the next
// stored event, given the thread's events since at least its delivered seq.
// It marks them delivered, and for a suspended thread (Provision) keeps the
// prompt and its images as pending, noting a teammate's turn. Other effects
// start no turn: it returns "". texts holds the attached text files (see
// agentPrompt).
func turnPrompt(t *store.Thread, eff thread.Effect, names map[string]string, unseen []*bandav1.Event, by string, teammate bool, m *bandav1.Message, texts map[string]string) (string, []string) {
	if eff != thread.StartTurn && eff != thread.Provision {
		return "", nil
	}
	var fresh []*bandav1.Event
	for _, e := range unseen {
		if e.Seq > t.DeliveredSeq {
			fresh = append(fresh, e)
		}
	}
	prompt, images := agentPrompt(t.OwnerID, names, fresh, by, m, texts)
	t.DeliveredSeq = t.LastSeq + 1 // the event stored next
	if eff == thread.Provision {   // suspended: deliver once the environment is back
		t.PendingPrompt = strings.TrimSpace(t.PendingPrompt + "\n\n" + prompt)
		t.PendingImages = lastImages(append(t.PendingImages, images...))
		t.PendingTeammate = t.PendingTeammate || teammate
	}
	return prompt, images
}

func (s *Service) ApproveTurn(ctx context.Context, req *bandav1.ApproveTurnRequest) (*bandav1.ApproveTurnResponse, error) {
	return &bandav1.ApproveTurnResponse{}, s.resolveTurn(ctx, req.ThreadId, req.RequestSeq, true, "")
}

func (s *Service) DeclineTurn(ctx context.Context, req *bandav1.DeclineTurnRequest) (*bandav1.DeclineTurnResponse, error) {
	return &bandav1.DeclineTurnResponse{}, s.resolveTurn(ctx, req.ThreadId, req.RequestSeq, false, strings.TrimSpace(req.Note))
}

// resolveTurn approves or declines the turn request at seq. An approved request
// runs as the requester's turn.
func (s *Service) resolveTurn(ctx context.Context, id string, seq int64, approve bool, note string) error {
	by, names := user(ctx), s.names(ctx)
	var effect thread.Effect
	var prompt string
	var images []string
	unlock := s.lockDelivery(id)
	_, stored, err := s.store.Update(ctx, id, func(tx *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		var a thread.Action = thread.Decline{By: by}
		if approve {
			a = thread.Approve{By: by}
		}
		next, eff, err := thread.Apply(t.State, a)
		if err != nil {
			return nil, err
		}
		// Read under the thread's lock, so a request resolves only once.
		events, err := tx.EventsOf(ctx, id, min(t.DeliveredSeq, seq-1), slices.Concat(promptKinds, []string{"turnRequest", "turnRequestResolved"})...)
		if err != nil {
			return nil, err
		}
		r := openRequest(events, seq)
		if r == nil {
			return nil, thread.ErrNoRequest
		}
		effect = eff
		m := r.GetTurnRequest().GetMessage()
		texts, err := textAttachments(ctx, tx, append(messages(events), m))
		if err != nil {
			return nil, err
		}
		prompt, images = turnPrompt(t, eff, names, events, r.Actor, true, m, texts)
		out := append([]*bandav1.Event{{Actor: by, Kind: &bandav1.Event_TurnRequestResolved{
			TurnRequestResolved: &bandav1.TurnRequestResolved{RequestSeq: seq, Approved: approve, Note: note}}}},
			stateEvents(t.State, next)...)
		t.State = next
		return out, nil
	})
	if err != nil {
		unlock()
		return err
	}
	s.broadcast(id, stored)
	if effect == thread.StartTurn {
		s.deliver(ctx, id, prompt, images, true)
	}
	unlock()
	s.do(ctx, id, effect)
	return nil
}

// openRequest returns the TurnRequest event at seq if no later event
// resolved it.
func openRequest(events []*bandav1.Event, seq int64) *bandav1.Event {
	var r *bandav1.Event
	for _, e := range events {
		if e.Seq == seq && e.GetTurnRequest() != nil {
			r = e
		}
		if e.GetTurnRequestResolved().GetRequestSeq() == seq {
			return nil
		}
	}
	return r
}

// errStale: events were stored between reading a thread's unseen events and
// locking it.
var errStale = connect.NewError(connect.CodeUnavailable, "the thread is busy, try again")

// unseen are a thread's events after its delivered seq, read before locking
// it (a transaction must not wait for another connection), and the text files
// they and m attach.
type unseen struct {
	events []*bandav1.Event
	texts  map[string]string
	last   int64 // the thread's last seq then
}

func (s *Service) readUnseen(ctx context.Context, id string, m *bandav1.Message) (unseen, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return unseen{}, err
	}
	u := unseen{last: t.LastSeq}
	if u.events, err = s.store.EventsOf(ctx, id, t.DeliveredSeq, promptKinds...); err != nil {
		return u, err
	}
	if n := len(u.events); n > 0 {
		u.last = max(u.last, u.events[n-1].Seq)
	}
	msgs := messages(u.events)
	if m != nil {
		msgs = append(msgs, m)
	}
	u.texts, err = textAttachments(ctx, s.store, msgs)
	return u, err
}

// fresh returns errStale if t, locked, has events u lacks: else u holds all
// that is not delivered, and a prompt built from it can mark it so.
func (u unseen) fresh(t *store.Thread) error {
	if t.LastSeq != u.last {
		return errStale
	}
	return nil
}

// retryStale runs update again while it returns errStale, a few times.
func retryStale(update func() error) error {
	for try := 1; ; try++ {
		if err := update(); !errors.Is(err, errStale) || try == 10 {
			return err
		}
	}
}

// lockDelivery locks a thread's deliveries: hold it from marking a prompt
// delivered until deliver handed it over, so prompts reach claude in seq order.
// Returns the unlock.
func (s *Service) lockDelivery(id string) func() {
	m, _ := s.deliveries.LoadOrStore(id, new(sync.Mutex))
	m.(*sync.Mutex).Lock()
	return m.(*sync.Mutex).Unlock
}

// deliver hands a prompt and its images (attachment ids) to the runner.
// Without one the environment is gone: keep the prompt for the next runner,
// which delivers it once it is ready, if it may run a teammate's turn.
func (s *Service) deliver(ctx context.Context, id, prompt string, images []string, teammate bool) {
	p := &bandav1.Prompt{Text: prompt}
	files, err := s.store.Attachments(ctx, images, true)
	if err != nil {
		slog.Error("load prompt images", "thread", id, "err", err)
	}
	for _, f := range files {
		p.Images = append(p.Images, &bandav1.AttachmentImage{MediaType: f.MediaType, Data: f.Data})
	}
	if s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_Prompt{Prompt: p}}) {
		return
	}
	_, events, err := s.store.Update(context.WithoutCancel(ctx), id, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		t.PendingPrompt = strings.TrimSpace(t.PendingPrompt + "\n\n" + prompt)
		t.PendingImages = lastImages(append(t.PendingImages, images...))
		t.PendingTeammate = t.PendingTeammate || teammate
		return envLost(t, 1, reasonUnreachable), nil
	})
	if err != nil {
		slog.Error("keep undelivered prompt", "thread", id, "err", err)
		return
	}
	s.broadcast(id, events)
}

// lastImages keeps the latest maxPromptImages of a prompt's images.
func lastImages(ids []string) []string {
	return ids[max(0, len(ids)-maxPromptImages):]
}

// messages are the posted messages among events.
func messages(events []*bandav1.Event) []*bandav1.Message {
	var out []*bandav1.Message
	for _, e := range events {
		if m := e.GetMessage(); m != nil {
			out = append(out, m)
		}
	}
	return out
}

// withdrawTurns withdraws t's open turn requests, with a line saying why,
// and turns its trust off: its owner (name) runs Claude Code on a
// subscription, so only they run the agent. Trust stays off if they switch
// back. Call it inside the thread's Update.
func withdrawTurns(ctx context.Context, tx *store.Store, t *store.Thread, name string) ([]*bandav1.Event, error) {
	t.Trusted = false
	if t.Requests == 0 {
		return nil, nil
	}
	events, err := tx.EventsOf(ctx, t.ID, 0, "turnRequest", "turnRequestResolved")
	if err != nil {
		return nil, err
	}
	note := name + " runs the agent on their Claude subscription now, so only they can. Fork to run it on yours."
	var out []*bandav1.Event
	for _, e := range events {
		if e.GetTurnRequest() != nil && openRequest(events, e.Seq) != nil { // ponytail: quadratic in requests, a handful
			out = append(out, &bandav1.Event{Actor: "system", Kind: &bandav1.Event_TurnRequestResolved{
				TurnRequestResolved: &bandav1.TurnRequestResolved{RequestSeq: e.Seq, Note: note}}})
		}
	}
	t.Requests = 0
	return out, nil
}
