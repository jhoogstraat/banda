package api

import (
	"context"
	"log/slog"
	"strings"

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
	before, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	unseen, err := s.store.Events(ctx, id, before.DeliveredSeq)
	if err != nil {
		return nil, err
	}
	names := s.names(ctx)
	texts, err := s.textAttachments(ctx, append(messages(unseen), m))
	if err != nil {
		return nil, err
	}

	var effect thread.Effect
	var prompt string
	var images []string
	_, stored, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
		// Only MCP posts as "agent:<thread>", and it lets an agent start turns
		// only in threads of its own owner.
		next, eff, err := thread.Apply(t.State, thread.Send{By: by, MentionsAgent: m.MentionsAgent,
			MentionsUsers: len(m.MentionedUserIds) > 0, OwnersAgent: strings.HasPrefix(by, "agent:"), Review: len(m.Review) > 0})
		if err != nil {
			return nil, err
		}
		effect = eff
		posted := messageEvent(by, m)
		if eff == thread.RequestTurn {
			posted = &bandav1.Event{Actor: by, Kind: &bandav1.Event_TurnRequest{TurnRequest: &bandav1.TurnRequest{Message: m}}}
		}
		prompt, images = turnPrompt(t, eff, names, unseen, by, m, texts)
		events := append([]*bandav1.Event{posted}, stateEvents(t.State, next)...)
		t.State = next
		return events, nil
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(id, stored)
	if err := s.store.Mention(ctx, id, stored[0].Seq, by, m.MentionedUserIds); err != nil {
		slog.Error("store mentions", "thread", id, "err", err)
	}
	if err := s.store.MarkRead(ctx, by, id); err != nil { // posting means having read it
		slog.Error("mark read", "thread", id, "err", err)
	}
	s.pokeInbox(append([]string{by}, m.MentionedUserIds...)...)
	if effect == thread.StartTurn {
		s.deliver(ctx, id, prompt, images)
	}
	s.do(ctx, id, effect)
	return &bandav1.SendMessageResponse{}, nil
}

// turnPrompt is the prompt for a turn by `by` that starts with the next
// stored event, given the thread's events since at least its delivered seq.
// It marks them delivered, and for a suspended thread (Provision) keeps the
// prompt and its images as pending. Other effects start no turn: it returns
// "". texts holds the attached text files (see agentPrompt).
func turnPrompt(t *store.Thread, eff thread.Effect, names map[string]string, unseen []*bandav1.Event, by string, m *bandav1.Message, texts map[string]string) (string, []string) {
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
	_, stored, err := s.store.Update(ctx, id, func(t *store.Thread) ([]*bandav1.Event, error) {
		var a thread.Action = thread.Decline{By: by}
		if approve {
			a = thread.Approve{By: by}
		}
		next, eff, err := thread.Apply(t.State, a)
		if err != nil {
			return nil, err
		}
		// Read under the thread's lock, so a request resolves only once.
		events, err := s.store.Events(ctx, id, min(t.DeliveredSeq, seq-1))
		if err != nil {
			return nil, err
		}
		r := openRequest(events, seq)
		if r == nil {
			return nil, thread.ErrNoRequest
		}
		effect = eff
		m := r.GetTurnRequest().GetMessage()
		texts, err := s.textAttachments(ctx, append(messages(events), m))
		if err != nil {
			return nil, err
		}
		prompt, images = turnPrompt(t, eff, names, events, r.Actor, m, texts)
		out := append([]*bandav1.Event{{Actor: by, Kind: &bandav1.Event_TurnRequestResolved{
			TurnRequestResolved: &bandav1.TurnRequestResolved{RequestSeq: seq, Approved: approve, Note: note}}}},
			stateEvents(t.State, next)...)
		t.State = next
		return out, nil
	})
	if err != nil {
		return err
	}
	s.broadcast(id, stored)
	if effect == thread.StartTurn {
		s.deliver(ctx, id, prompt, images)
	}
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

// deliver hands a prompt and its images (attachment ids) to the runner.
// Without one the environment is gone: keep the prompt for the next runner,
// which delivers it once it is ready.
func (s *Service) deliver(ctx context.Context, id, prompt string, images []string) {
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
	_, events, err := s.store.Update(context.WithoutCancel(ctx), id, func(t *store.Thread) ([]*bandav1.Event, error) {
		t.PendingPrompt = strings.TrimSpace(t.PendingPrompt + "\n\n" + prompt)
		t.PendingImages = lastImages(append(t.PendingImages, images...))
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
