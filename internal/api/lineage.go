package api

import (
	"context"
	"log/slog"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

func (s *Service) SpawnThread(ctx context.Context, req *bandav1.SpawnThreadRequest) (*bandav1.SpawnThreadResponse, error) {
	parent, err := s.store.Get(ctx, req.ParentId)
	if err != nil {
		return nil, err
	}
	child, err := s.spawn(ctx, parent, user(ctx), user(ctx), req.Prompt, "", req.BaseBranch, req.Model, req.Effort)
	if err != nil {
		return nil, err
	}
	return &bandav1.SpawnThreadResponse{Thread: toProto(child)}, nil
}

func (s *Service) LineageChildren(ctx context.Context, req *bandav1.LineageChildrenRequest) (*bandav1.LineageChildrenResponse, error) {
	children, err := s.store.Children(ctx, req.ThreadId, user(ctx))
	if err != nil {
		return nil, err
	}
	res := &bandav1.LineageChildrenResponse{}
	for _, t := range children {
		res.Threads = append(res.Threads, toProto(t))
	}
	return res, nil
}

// spawn creates a sub-thread of parent owned by owner and announces it in the
// parent as actor. Repo, model and effort default to the parent's. The base
// defaults to the parent's branch once it was pushed (its commits are on
// origin), else to the parent's base branch; for another repo, to main.
func (s *Service) spawn(ctx context.Context, parent store.Thread, owner, actor, prompt, repo, base, model, effort string) (store.Thread, error) {
	repo = or(repo, parent.RepoURL)
	if base == "" && repo != parent.RepoURL {
		base = "main"
	}
	if base == "" {
		base = parent.BaseBranch
		if pushed, err := s.store.Pushed(ctx, parent); err != nil {
			return store.Thread{}, err
		} else if pushed {
			base = parent.Branch
		}
	}
	child, err := s.create(ctx, store.Thread{State: thread.State{OwnerID: owner}, ParentID: parent.ID, RepoURL: repo,
		BaseBranch: base, Model: or(model, parent.Model), Effort: or(effort, parent.Effort),
		PermissionMode: parent.PermissionMode}, prompt) // an agent's sub-thread asks no less than it does
	if err != nil {
		return child, err
	}
	s.lineage(ctx, child, actor, bandav1.Status_STATUS_STARTING, "")
	return child, nil
}

// lineage posts a LineageUpdate about child into its parent's chat.
func (s *Service) lineage(ctx context.Context, child store.Thread, actor string, status bandav1.Status, text string) {
	_, events, err := s.store.Update(ctx, child.ParentID, func(*store.Thread) ([]*bandav1.Event, error) {
		return []*bandav1.Event{{Actor: actor, Kind: &bandav1.Event_LineageUpdate{LineageUpdate: &bandav1.LineageUpdate{
			ThreadId: child.ID, Title: child.Title, Status: status, Text: text}}}}, nil
	})
	if err != nil {
		slog.Error("report to parent thread", "thread", child.ID, "parent", child.ParentID, "err", err)
		return
	}
	s.broadcast(child.ParentID, events)
}

// reportToParent tells a sub-thread's parent when the sub-thread finished
// its turns or failed. Called by broadcast for every batch of events.
func (s *Service) reportToParent(id string, events []*bandav1.Event) {
	status, text := lineageStatus(events)
	if status == bandav1.Status_STATUS_UNSPECIFIED {
		return
	}
	go func() {
		ctx := context.Background()
		t, err := s.store.Get(ctx, id)
		if err != nil || t.ParentID == "" {
			return
		}
		if text == "" {
			last, err := s.store.LastAgentText(ctx, id)
			if err != nil {
				slog.Error("last agent text", "thread", id, "err", err)
			}
			text = firstLine(last)
		}
		s.lineage(ctx, t, "system", status, text)
	}()
}

// lineageStatus is what a batch of a thread's events means for its parent:
// AWAITING_INPUT if its last turn ended (text set if it failed), ERRORED if
// the thread failed (text is why), else UNSPECIFIED.
func lineageStatus(events []*bandav1.Event) (bandav1.Status, string) {
	var ended *bandav1.TurnEnded
	for _, e := range events {
		if t := e.GetTurnEnded(); t != nil {
			ended = t
		}
	}
	for _, e := range events {
		switch sc := e.GetStatusChanged(); sc.GetStatus() {
		case bandav1.Status_STATUS_AWAITING_INPUT:
			if ended == nil { // ready without a turn, e.g. resumed for a terminal
				continue
			}
			if ended.IsError {
				return sc.Status, "Turn failed: " + firstLine(ended.Error)
			}
			return sc.Status, ""
		case bandav1.Status_STATUS_ERRORED:
			return sc.Status, or(firstLine(sc.Reason), "The environment failed or was lost.")
		}
	}
	return bandav1.Status_STATUS_UNSPECIFIED, ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(line); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return line
}
