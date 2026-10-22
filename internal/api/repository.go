package api

import (
	"context"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// AddRepository gives a thread without a repository one. The environment
// restarts, and its runner turns the agent's folder into a checkout of the
// thread branch, keeping the files already there as uncommitted changes.
// From then on it is a thread like any other.
//
// Until a checkout of it succeeded (a wrong URL or base branch), the
// repository can be changed the same way, or removed with an empty URL: the
// failed checkout left the folder as it was.
func (s *Service) AddRepository(ctx context.Context, req *bandav1.AddRepositoryRequest) (*bandav1.AddRepositoryResponse, error) {
	repo, base := strings.TrimSpace(req.RepoUrl), or(strings.TrimSpace(req.BaseBranch), "main")
	if repo != "" {
		if err := checkRepoURL(repo); err != nil {
			return nil, err
		}
	}
	var effect thread.Effect
	_, events, err := s.store.Update(ctx, req.ThreadId, func(tx *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		checkedOut := false
		if t.RepoURL != "" {
			steps, err := tx.EventsOf(ctx, t.ID, 0, "setupStep")
			if err != nil {
				return nil, err
			}
			checkedOut = slices.ContainsFunc(steps, func(e *bandav1.Event) bool {
				st := e.GetSetupStep()
				return st.Id == "checkout" && st.State == bandav1.SetupState_SETUP_STATE_DONE
			})
		} else if repo == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, "a repository URL is required")
		}
		next, eff, err := thread.Apply(t.State, thread.AddRepo{By: user(ctx), CheckedOut: checkedOut})
		if err != nil {
			return nil, err
		}
		t.RepoURL, t.BaseBranch, t.Branch = repo, base, "banda/"+t.ID
		if repo == "" {
			t.BaseBranch, t.Branch = "", ""
		}
		if err := tx.SetRepository(ctx, *t); err != nil {
			return nil, err
		}
		effect = eff
		out := append([]*bandav1.Event{{Actor: t.OwnerID, Kind: &bandav1.Event_RepoAdded{RepoAdded: &bandav1.RepoAdded{RepoUrl: t.RepoURL, BaseBranch: t.BaseBranch}}}},
			stateEvents(t.State, next)...)
		t.State = next
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(req.ThreadId, events)
	s.do(ctx, req.ThreadId, effect)
	return &bandav1.AddRepositoryResponse{}, nil
}
