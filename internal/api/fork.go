package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

const maxTries = 4

// Fork starts threads from another thread's latest diff (the state after its
// last finished turn) and conversation. The diff, not the branch, carries the
// changes: it is in the database, so the source's environment may be paused,
// broken or owned by someone else, and nothing needs pushing first.
func (s *Service) Fork(ctx context.Context, req *bandav1.ForkRequest) (*bandav1.ForkResponse, error) {
	tries := req.Tries
	if len(tries) == 0 {
		tries = []*bandav1.RaceTry{{}}
	}
	if len(tries) > maxTries {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Sprintf("at most %d tries", maxTries))
	}
	src, err := s.store.Get(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	turn, err := s.store.Count(ctx, src.ID, "turnEnded")
	if err != nil {
		return nil, err
	}
	events, err := s.transcript(ctx, src.ID)
	if err != nil {
		return nil, err
	}
	by, instruction := user(ctx), strings.TrimSpace(req.Prompt)
	prompt := forkPrompt(s.names(ctx), src, turn, events, instruction)
	name := "Fork of " + src.Title
	var first *bandav1.Message
	if instruction != "" {
		name, first = title(instruction), &bandav1.Message{Text: instruction}
	}
	race := raceID(len(tries))
	res := &bandav1.ForkResponse{}
	var notes []*bandav1.Event
	for i, try := range tries {
		t, err := s.start(ctx, store.Thread{
			State:         thread.State{OwnerID: by, Status: thread.Starting, Mode: thread.AgentMode},
			Title:         name + tryLabel(i, len(tries)),
			RepoURL:       src.RepoURL,
			BaseBranch:    src.BaseBranch,
			Model:         or(try.Model, src.Model),
			Effort:        or(try.Effort, src.Effort),
			Profile:       src.Profile,
			PendingPrompt: prompt,
			Diff:          src.Diff, // also what the runner applies, see ForkPatch
			ForkedFrom:    src.ID,
			ForkTurn:      turn,
			RaceID:        race,
			// The same safety as the source; "always allow" grants stay with it.
			PermissionMode: src.PermissionMode,
		}, by, first)
		if err != nil {
			return nil, err
		}
		res.Threads = append(res.Threads, toProto(t))
		notes = append(notes, &bandav1.Event{Actor: by, Kind: &bandav1.Event_ForkNote{ForkNote: &bandav1.ForkNote{
			ThreadId: t.ID, Title: t.Title, Turn: int32(turn)}}})
	}
	if _, err := s.apply(ctx, src.ID, nil, notes...); err != nil {
		slog.Error("note forks", "thread", src.ID, "err", err)
	}
	return res, nil
}

func (s *Service) Race(ctx context.Context, req *bandav1.RaceRequest) (*bandav1.RaceResponse, error) {
	first := &bandav1.Message{Text: req.Prompt, Attachments: req.Attachments}
	prompt, images, err := s.firstPrompt(ctx, first, user(ctx))
	if err != nil {
		return nil, err
	}
	if err := checkRepoURL(req.RepoUrl); err != nil {
		return nil, err
	}
	if len(req.Tries) < 2 || len(req.Tries) > maxTries {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Sprintf("a race has 2 to %d tries", maxTries))
	}
	mode, err := newPermissionMode(req.PermissionMode)
	if err != nil {
		return nil, err
	}
	race := raceID(len(req.Tries))
	res := &bandav1.RaceResponse{}
	for i, try := range req.Tries {
		t, err := s.start(ctx, store.Thread{
			State:          thread.State{OwnerID: user(ctx), Status: thread.Starting, Mode: thread.AgentMode},
			Title:          title(req.Prompt) + tryLabel(i, len(req.Tries)),
			RepoURL:        req.RepoUrl,
			BaseBranch:     req.BaseBranch,
			Model:          try.Model,
			Effort:         try.Effort,
			Profile:        req.Profile,
			PendingPrompt:  prompt,
			PendingImages:  images,
			RaceID:         race,
			PermissionMode: mode,
		}, user(ctx), first)
		if err != nil {
			return nil, err
		}
		res.Threads = append(res.Threads, toProto(t))
	}
	return res, nil
}

func (s *Service) RaceCompare(ctx context.Context, req *bandav1.RaceCompareRequest) (*bandav1.RaceCompareResponse, error) {
	threads, err := s.store.Compared(ctx, req.RaceId, req.ThreadIds, user(ctx))
	if err != nil {
		return nil, err
	}
	if len(threads) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, "no such tries")
	}
	sort.SliceStable(threads, func(i, j int) bool { return threads[i].CreatedAt.Before(threads[j].CreatedAt) }) // stable columns
	var ids []string
	for _, t := range threads {
		ids = append(ids, t.ID)
	}
	totals, err := s.store.TurnTotals(ctx, ids)
	if err != nil {
		return nil, err
	}
	res := &bandav1.RaceCompareResponse{}
	for _, t := range threads {
		tt := totals[t.ID]
		reply, err := s.store.LastAgentText(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		res.Entries = append(res.Entries, &bandav1.RaceEntry{Thread: toProto(t), Turns: int32(tt.Turns), CostUsd: tt.CostUSD, DurationMs: tt.DurationMS, Reply: reply})
	}
	return res, nil
}

// recentRaces is how many races RaceList returns.
const recentRaces = 30

func (s *Service) RaceList(ctx context.Context, _ *bandav1.RaceListRequest) (*bandav1.RaceListResponse, error) {
	threads, err := s.store.Races(ctx, user(ctx), recentRaces)
	if err != nil {
		return nil, err
	}
	res := &bandav1.RaceListResponse{}
	for _, t := range threads {
		res.Threads = append(res.Threads, toProto(t))
	}
	return res, nil
}

// RacePick notes the pick in every thread of the race, settles the caller's
// other tries (which pauses them) and unsettles the pick.
func (s *Service) RacePick(ctx context.Context, req *bandav1.RacePickRequest) (*bandav1.RacePickResponse, error) {
	by := user(ctx)
	won, err := s.owned(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	if won.RaceID == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the thread is not part of a race")
	}
	tries, err := s.store.Compared(ctx, won.RaceID, nil, by)
	if err != nil {
		return nil, err
	}
	for _, t := range tries {
		note := &bandav1.Event{Actor: by, Kind: &bandav1.Event_ForkNote{ForkNote: &bandav1.ForkNote{ThreadId: won.ID, Title: won.Title, Picked: true}}}
		if _, err := s.apply(ctx, t.ID, nil, note); err != nil {
			return nil, err
		}
		if t.CheckOwner(by) != nil {
			continue
		}
		if err := s.store.SetSettled(ctx, by, t.ID, t.ID != won.ID); err != nil {
			return nil, err
		}
		if t.ID != won.ID {
			s.suspendIfOwner(ctx, t.ID, "settled")
		}
	}
	s.pokeInbox(by)
	return &bandav1.RacePickResponse{}, nil
}

// ForkPatch hands a fork's runner the source's diff. The fork's own diff
// starts as that copy and is only replaced after its first turn, which comes
// after the checkout that asks for it.
func (s *Service) ForkPatch(ctx context.Context, req *bandav1.ForkPatchRequest) (*bandav1.ForkPatchResponse, error) {
	t, err := s.runnerThread(ctx, req.ThreadId, req.Token)
	if err != nil {
		return nil, err
	}
	if t.ForkedFrom == "" {
		return &bandav1.ForkPatchResponse{}, nil
	}
	return &bandav1.ForkPatchResponse{Patch: t.Diff,
		Message: fmt.Sprintf("Changes of Banda thread %s after turn %d", t.ForkedFrom, t.ForkTurn)}, nil
}

// raceID is a new id shared by n threads started together, none for one.
func raceID(n int) string {
	if n < 2 {
		return ""
	}
	return strings.ToLower(rand.Text())
}

func tryLabel(i, n int) string {
	if n < 2 {
		return ""
	}
	return fmt.Sprintf(" (%d/%d)", i+1, n)
}

// maxTranscript bounds the conversation handed to a fork; older lines go first.
const maxTranscript = 40 << 10

// forkPrompt is a fork's first prompt: where it comes from, the source's
// conversation (people's messages and the agent's replies) and the new
// instruction, or without one a request to sum up.
func forkPrompt(names map[string]string, src store.Thread, turn int, events []*bandav1.Event, instruction string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This thread is a fork of Banda thread %s (%q), made after its turn %d. ", src.ID, src.Title, turn)
	if src.RepoURL == "" {
		b.WriteString("Neither thread has a repository and that thread's files stayed in its environment: you start in an empty folder. ")
	} else {
		fmt.Fprintf(&b, "Your branch already holds that thread's changes, committed on top of %s. ", src.BaseBranch)
	}
	b.WriteString("Its conversation so far:\n\n")
	b.WriteString(conversation(names, events))
	b.WriteString("\n\n---\n\n")
	if instruction == "" {
		instruction = "Sum up in a few lines where the work stands, then wait for instructions."
	}
	b.WriteString(instruction)
	return b.String()
}

// transcript is the thread's latest messages and replies, enough of them for conversation: pages
// from the newest back, as a long thread's are thousands.
func (s *Service) transcript(ctx context.Context, id string) ([]*bandav1.Event, error) {
	const page = 200
	var events []*bandav1.Event
	for before, size := int64(math.MaxInt64), 0; size <= maxTranscript; {
		got, err := s.store.LatestOf(ctx, id, before, page, "message", "agentText")
		if err != nil || len(got) == 0 {
			return events, err
		}
		events = append(got, events...)
		for _, e := range got {
			text := e.GetAgentText().GetText()
			if m := e.GetMessage(); m != nil {
				text = messageText(m)
			}
			size += min(len(text), maxTranscript/4) + 2 // no more than its line in conversation
		}
		if len(got) < page {
			break
		}
		before = got[0].Seq
	}
	return events, nil
}

// conversation is a thread's people's messages and the agent's replies among
// events, the latest maxTranscript of them, for a new session's first prompt.
func conversation(names map[string]string, events []*bandav1.Event) string {
	var lines []string
	size := 0
	for _, e := range events {
		var line string
		switch {
		case e.GetMessage().GetProposal(): // only what its owner sends reaches an agent
			continue
		case e.GetMessage() != nil:
			line = actorName(names, e.Actor) + ": " + messageText(e.GetMessage())
		case e.GetAgentText() != nil:
			line = "You: " + e.GetAgentText().Text
		default:
			continue
		}
		if len(line) > maxTranscript/4 {
			line = strings.ToValidUTF8(line[:maxTranscript/4], "") + " …"
		}
		lines = append(lines, line)
		size += len(line) + 2
	}
	cut := false
	for size > maxTranscript && len(lines) > 1 {
		size -= len(lines[0]) + 2
		lines, cut = lines[1:], true
	}
	out := strings.Join(lines, "\n\n")
	if cut {
		out = "(earlier messages left out)\n\n" + out
	}
	return out
}
