package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// overlaps returns the overlaps of the threads ids by thread; none when OverlapWindow is zero.
func (s *Service) overlaps(ctx context.Context, ids ...string) (map[string][]*bandav1.Overlap, error) {
	if s.OverlapWindow <= 0 || len(ids) == 0 {
		return nil, nil
	}
	found, err := s.store.Overlaps(ctx, ids, time.Now().Add(-s.OverlapWindow))
	out := map[string][]*bandav1.Overlap{}
	for _, o := range found {
		out[o.Of] = append(out[o.Of], &bandav1.Overlap{ThreadId: o.ID, Title: o.Title, OwnerId: o.OwnerID,
			Status: statusToProto[thread.Status(o.Status)], Paths: o.Paths})
	}
	return out, err
}

// noticeOverlaps tells id and each thread it overlaps with, in their chats (and so their agents), once the
// two share a file they weren't told about.
func (s *Service) noticeOverlaps(ctx context.Context, id string) {
	all, err := s.overlaps(ctx, id)
	if err != nil {
		slog.Error("overlaps", "thread", id, "err", err)
		return
	}
	if len(all[id]) == 0 {
		return
	}
	me, err := s.store.Get(ctx, id)
	if err != nil {
		slog.Error("overlaps", "thread", id, "err", err)
		return
	}
	for _, o := range all[id] {
		if fresh, err := s.store.ClaimOverlap(ctx, id, o.ThreadId, o.Paths); err != nil || !fresh {
			if err != nil {
				slog.Error("claim overlap", "thread", id, "with", o.ThreadId, "err", err)
			}
			continue
		}
		s.postOverlap(ctx, id, &bandav1.Overlap{ThreadId: o.ThreadId, Title: o.Title, OwnerId: o.OwnerId, Paths: o.Paths})
		s.postOverlap(ctx, o.ThreadId, &bandav1.Overlap{ThreadId: id, Title: me.Title, OwnerId: me.OwnerID, Paths: o.Paths})
	}
}

func (s *Service) postOverlap(ctx context.Context, id string, o *bandav1.Overlap) {
	_, events, err := s.store.Update(ctx, id, func(*store.Store, *store.Thread) ([]*bandav1.Event, error) {
		return []*bandav1.Event{{Actor: "system", Kind: &bandav1.Event_Overlap{Overlap: o}}}, nil
	})
	if err != nil {
		slog.Error("post overlap", "thread", id, "with", o.ThreadId, "err", err)
		return
	}
	s.broadcast(id, events)
}

// overlapLine describes an overlap for the agent and transcripts.
func overlapLine(names map[string]string, o *bandav1.Overlap) string {
	paths := o.Paths
	more := ""
	if len(paths) > 10 {
		paths, more = paths[:10], fmt.Sprintf(" and %d more", len(paths)-10)
	}
	return fmt.Sprintf("[Now overlaps with thread %s %q by %s, which changes the same files: %s%s]",
		o.ThreadId, o.Title, actorName(names, o.OwnerId), strings.Join(paths, ", "), more)
}
