package api

import (
	"cmp"
	"context"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
)

var searchKinds = map[bandav1.SearchKind]string{
	bandav1.SearchKind_SEARCH_KIND_PEOPLE: "people",
	bandav1.SearchKind_SEARCH_KIND_AGENT:  "agent",
	bandav1.SearchKind_SEARCH_KIND_CODE:   "code",
}

func (s *Service) Search(ctx context.Context, req *bandav1.SearchRequest) (*bandav1.SearchResponse, error) {
	when := func(t *timestamppb.Timestamp) *time.Time {
		if t == nil {
			return nil
		}
		v := t.AsTime()
		return &v
	}
	hits, err := s.store.Search(ctx, store.SearchQuery{Words: req.Query, Actor: req.Actor, RepoURL: req.RepoUrl,
		Kind: searchKinds[req.Kind], After: when(req.After), Before: when(req.Before), Limit: cmp.Or(int(req.Limit), 20)})
	if err != nil {
		return nil, err
	}
	res := &bandav1.SearchResponse{}
	for _, h := range hits {
		r := &bandav1.SearchResult{ThreadId: h.ThreadID, ThreadTitle: h.Title, RepoUrl: h.RepoURL, Seq: h.Seq,
			Actor: h.Actor, Snippet: h.Snippet}
		if h.Time != nil {
			r.Time = timestamppb.New(*h.Time)
		}
		for k, name := range searchKinds {
			if name == h.Kind {
				r.Kind = k
			}
		}
		res.Results = append(res.Results, r)
	}
	return res, nil
}

// snippetText marks the matched words of a snippet with **.
func snippetText(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 {
			p = "**" + p + "**"
		}
		b.WriteString(p)
	}
	return b.String()
}
