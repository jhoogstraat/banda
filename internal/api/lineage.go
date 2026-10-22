package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

func (s *Service) SpawnThread(ctx context.Context, req *bandav1.SpawnThreadRequest) (*bandav1.SpawnThreadResponse, error) {
	parent, err := s.store.Get(ctx, req.ParentId)
	if err != nil {
		return nil, err
	}
	child, err := s.spawn(ctx, parent, user(ctx), user(ctx), req.Prompt, "", req.BaseBranch, req.Model, req.Effort, req.Profile, false)
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
// parent as actor. Repo, model, effort and profile default to the parent's. The base
// defaults to the parent's branch once it was pushed (its commits are on
// origin), else to the parent's base branch; for another repo, see start.
// With wake, its reports also wake the parent's agent (see wakeParent).
func (s *Service) spawn(ctx context.Context, parent store.Thread, owner, actor, prompt, repo, base, model, effort, profile string, wake bool) (store.Thread, error) {
	repo = or(repo, parent.RepoURL)
	if base == "" && repo == parent.RepoURL {
		base = parent.BaseBranch
		if pushed, err := s.store.Pushed(ctx, parent.ID); err != nil {
			return store.Thread{}, err
		} else if pushed {
			base = parent.Branch
		}
	}
	by := actor
	if actor == "agent" { // the parent's agent wrote the prompt
		by = "agent:" + parent.ID
	}
	child, err := s.create(ctx, store.Thread{State: thread.State{OwnerID: owner}, ParentID: parent.ID, RepoURL: repo,
		BaseBranch: base, Model: or(model, parent.Model), Effort: or(effort, parent.Effort), Profile: or(profile, parent.Profile),
		PermissionMode: parent.PermissionMode, WakeParent: wake}, by, &bandav1.Message{Text: prompt}) // an agent's sub-thread asks no less than it does
	if err != nil {
		return child, err
	}
	s.lineage(ctx, child, actor, bandav1.Status_STATUS_STARTING, "")
	return child, nil
}

// checkFanOut refuses a sub-thread by parent's agent beyond the depth and
// running-thread limits, saying what to do instead.
// ponytail: check then create, so parallel calls can overshoot the running cap by a few.
func (s *Service) checkFanOut(ctx context.Context, parent store.Thread) error {
	depth, err := s.store.Depth(ctx, parent.ID)
	if err != nil {
		return err
	}
	if depth+1 > s.MaxSubthreadDepth {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Sprintf(
			"sub-threads may nest only %d deep and this thread is at depth %d: do the work here, or ask the parent thread to split it", s.MaxSubthreadDepth, depth))
	}
	n, err := s.store.AgentSpawned(ctx, parent.OwnerID)
	if err != nil {
		return err
	}
	if n >= s.MaxAgentThreads {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Sprintf(
			"the owner has at most %d running sub-threads started by agents: wait until some finish and are paused, or do the work here", s.MaxAgentThreads))
	}
	return nil
}

// maxLineage is how much of a reply or an error a LineageUpdate quotes.
const maxLineage = 200

// lineage posts a LineageUpdate about child into its parent's chat.
func (s *Service) lineage(ctx context.Context, child store.Thread, actor string, status bandav1.Status, text string) {
	_, events, err := s.store.Update(ctx, child.ParentID, func(*store.Store, *store.Thread) ([]*bandav1.Event, error) {
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
// its turns or failed, and wakes the parent's agent if the sub-thread asks
// for it. Called by broadcast, holding s.mu, for every batch of events.
func (s *Service) reportToParent(id string, events []*bandav1.Event) {
	status, text := lineageStatus(events)
	if status == bandav1.Status_STATUS_UNSPECIFIED {
		return
	}
	// After the report of the batch before, so "finished" and "failed" keep their order.
	prev, done := s.reports[id], make(chan struct{})
	s.reports[id] = done
	go func() {
		defer func() {
			close(done)
			s.mu.Lock()
			if s.reports[id] == done {
				delete(s.reports, id)
			}
			s.mu.Unlock()
		}()
		if prev != nil {
			<-prev
		}
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
			text = firstLine(last, maxLineage)
		}
		s.lineage(ctx, t, "system", status, text)
		if t.WakeParent {
			s.wakeParent(ctx, t, &bandav1.LineageUpdate{ThreadId: t.ID, Title: t.Title, Status: status, Text: text})
		}
	}()
}

// wakeParent starts a turn of the parent's agent about a report u of child,
// as child's own agent posting with mention_agent would: no approval, a
// paused parent wakes, a working one queues it, chat mode doesn't stop it.
// A starting or errored parent isn't woken. mayWake is the loop guard.
func (s *Service) wakeParent(ctx context.Context, child store.Thread, u *bandav1.LineageUpdate) {
	posted, err := s.store.LastPost(ctx, child.ID, child.ParentID)
	if err != nil {
		slog.Error("last post", "thread", child.ID, "err", err)
		return
	}
	if !mayWake(child.WokeParent, posted) {
		return
	}
	_, err = s.SendMessage(WithUser(ctx, "agent:"+child.ID), &bandav1.SendMessageRequest{ThreadId: child.ParentID,
		Message: &bandav1.Message{Text: wakeText(u), MentionsAgent: true}})
	if err != nil {
		slog.Warn("wake parent thread", "thread", child.ID, "parent", child.ParentID, "err", err)
		return
	}
	if err := s.store.SetWokeParent(ctx, child.ID); err != nil {
		slog.Error("record parent wake", "thread", child.ID, "err", err)
	}
}

// mayWake is the loop guard, so that two agents don't keep each other busy
// on the owner's subscription without a person: a sub-thread wakes its parent
// the first time (woke is nil), then only once a person posted in either
// thread (posted is the latest such post) since it last did.
func mayWake(woke, posted *time.Time) bool {
	return woke == nil || posted != nil && posted.After(*woke)
}

// wakeText is what a report asks of the parent's agent it wakes.
func wakeText(u *bandav1.LineageUpdate) string {
	return lineageLine(u) + "\nYou started this sub-thread with wake_parent. Read it with read_thread (thread_id " + u.ThreadId +
		", include_diff for its changes) and carry on from its result."
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
				return sc.Status, "Turn failed: " + firstLine(ended.Error, maxLineage)
			}
			return sc.Status, ""
		case bandav1.Status_STATUS_ERRORED:
			return sc.Status, or(firstLine(sc.Reason, maxLineage), "The environment failed or was lost.")
		}
	}
	return bandav1.Status_STATUS_UNSPECIFIED, ""
}
