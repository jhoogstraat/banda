package api

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
)

func (s *Service) Brief(ctx context.Context, _ *bandav1.BriefRequest) (*bandav1.BriefResponse, error) {
	b, err := s.store.Brief(ctx, user(ctx))
	if err != nil {
		return nil, err
	}
	items := func(list []store.BriefItem) []*bandav1.BriefItem {
		var out []*bandav1.BriefItem
		for _, i := range list {
			out = append(out, &bandav1.BriefItem{ThreadId: i.ThreadID, Title: i.Title, OwnerId: i.OwnerID, Text: i.Text, Time: timestamppb.New(i.Time)})
		}
		return out
	}
	return &bandav1.BriefResponse{Since: timestamppb.New(b.Since), Until: timestamppb.New(b.Until),
		Finished: items(b.Finished), Waiting: items(b.Waiting), Spawned: items(b.Spawned), Settled: items(b.Settled)}, nil
}

func (s *Service) BriefSeen(ctx context.Context, req *bandav1.BriefSeenRequest) (*bandav1.BriefSeenResponse, error) {
	return &bandav1.BriefSeenResponse{}, s.store.SetBriefSeen(ctx, user(ctx), req.Until.AsTime())
}
