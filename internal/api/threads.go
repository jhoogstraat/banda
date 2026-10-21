package api

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/url"
	"strings"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

func (s *Service) CreateThread(ctx context.Context, req *bandav1.CreateThreadRequest) (*bandav1.CreateThreadResponse, error) {
	if req.RepoUrl == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "repo_url is required")
	}
	if err := checkRepoURL(req.RepoUrl); err != nil {
		return nil, err
	}
	t := store.Thread{
		State:         thread.State{OwnerID: user(ctx), Status: thread.Starting, Mode: thread.AgentMode},
		Title:         title(req.Prompt),
		RepoURL:       req.RepoUrl,
		BaseBranch:    or(req.BaseBranch, "main"),
		Model:         or(req.Model, "claude-opus-5-5"),
		Effort:        or(req.Effort, "high"),
		PendingPrompt: req.Prompt,
	}
	t, err := s.start(ctx, t, &bandav1.Message{Text: req.Prompt})
	if err != nil {
		return nil, err
	}
	return &bandav1.CreateThreadResponse{Thread: toProto(t)}, nil
}

// create starts a thread (owner, repo, base, model, effort, permission mode and
// parent taken from t) with its first prompt.
func (s *Service) create(ctx context.Context, t store.Thread, prompt string) (store.Thread, error) {
	if strings.TrimSpace(prompt) == "" {
		return t, connect.NewError(connect.CodeInvalidArgument, "prompt is required")
	}
	t.Title, t.PendingPrompt = title(prompt), prompt
	t.Status, t.Mode = thread.Starting, thread.AgentMode
	t.Model, t.Effort = or(t.Model, "claude-opus-5-5"), or(t.Effort, "high")
	return s.start(ctx, t, &bandav1.Message{Text: prompt})
}

// checkRepoURL refuses credentials in an https URL: everyone sees a thread's
// repository, forks reuse it, and the runner redacts only GIT_TOKEN.
func checkRepoURL(repo string) error {
	if u, err := url.Parse(repo); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.User != nil {
		return connect.NewError(connect.CodeInvalidArgument, "the repository URL must not contain credentials: add a GIT_TOKEN secret in Settings instead")
	}
	return nil
}

// start stores a new thread, posts its first message (if any) as the owner
// and provisions its environment. It sets the id, branch and runner token.
func (s *Service) start(ctx context.Context, t store.Thread, first *bandav1.Message) (store.Thread, error) {
	// Here, so forks of a thread created with a credentialed URL are refused too.
	if err := checkRepoURL(t.RepoURL); err != nil {
		return t, err
	}
	t.ID = strings.ToLower(rand.Text())
	t.Branch = "banda/" + t.ID
	t.RunnerToken = rand.Text()
	if err := s.store.Create(ctx, t); err != nil {
		return t, err
	}
	t, events, err := s.store.Update(ctx, t.ID, func(t *store.Thread) ([]*bandav1.Event, error) {
		if first == nil {
			return nil, nil
		}
		return []*bandav1.Event{messageEvent(t.OwnerID, first)}, nil
	})
	if err != nil {
		return t, err
	}
	s.broadcast(t.ID, events)
	s.provision(ctx, t)
	return t, nil
}

func (s *Service) provision(ctx context.Context, t store.Thread) {
	// Ensure also cancels a scheduled deletion, or recreates a deleted environment.
	if t.EnvDeleteAt != nil {
		if err := s.store.SetEnvDeleteAt(ctx, t.ID, nil); err != nil {
			slog.Error("cancel environment deletion", "thread", t.ID, "err", err)
		}
	}
	// Ensure replaces a running pod; its runner's disconnect is expected.
	s.mu.Lock()
	delete(s.runners, t.ID)
	s.previewChanged(t.ID)
	s.mu.Unlock()
	st, err := s.store.Settings(ctx, t.OwnerID, harness)
	var secrets []store.Secret
	if err == nil {
		secrets, err = s.store.Secrets(ctx, t.OwnerID)
	}
	if err == nil {
		var names []string
		for _, sec := range secrets {
			names = append(names, sec.Name)
		}
		err = s.sandboxes.Ensure(ctx, t, st.AuthMethod, names)
	}
	if err != nil {
		slog.Error("provision sandbox", "thread", t.ID, "err", err)
		s.apply(context.WithoutCancel(ctx), t.ID, thread.EnvLost{})
	}
}

func (s *Service) ListThreads(ctx context.Context, req *bandav1.ListThreadsRequest) (*bandav1.ListThreadsResponse, error) {
	threads, err := s.store.List(ctx, or(req.OwnerId, user(ctx)), user(ctx))
	if err != nil {
		return nil, err
	}
	res := &bandav1.ListThreadsResponse{}
	for _, t := range threads {
		res.Threads = append(res.Threads, toProto(t))
	}
	return res, nil
}

func (s *Service) GetThread(ctx context.Context, req *bandav1.GetThreadRequest) (*bandav1.GetThreadResponse, error) {
	t, err := s.store.GetFor(ctx, req.ThreadId, user(ctx))
	if err != nil {
		return nil, err
	}
	return &bandav1.GetThreadResponse{Thread: toProto(t)}, nil
}

func (s *Service) Trust(ctx context.Context, req *bandav1.TrustRequest) (*bandav1.TrustResponse, error) {
	text := "Teammates' turns need the owner's approval again."
	if req.Trusted {
		text = "Teammates can now run the agent without the owner's approval."
	}
	_, err := s.apply(ctx, req.ThreadId, thread.Trust{By: user(ctx), Trusted: req.Trusted},
		&bandav1.Event{Actor: user(ctx), Kind: &bandav1.Event_Notice{Notice: &bandav1.Notice{Text: text}}})
	return &bandav1.TrustResponse{}, err
}

func (s *Service) Interrupt(ctx context.Context, req *bandav1.InterruptRequest) (*bandav1.InterruptResponse, error) {
	effect, err := s.apply(ctx, req.ThreadId, thread.Interrupt{By: user(ctx)})
	if err != nil {
		return nil, err
	}
	s.do(ctx, req.ThreadId, effect)
	return &bandav1.InterruptResponse{}, nil
}

func (s *Service) SetMode(ctx context.Context, req *bandav1.SetModeRequest) (*bandav1.SetModeResponse, error) {
	mode, ok := modeFromProto[req.Mode]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, "unknown mode")
	}
	_, err := s.apply(ctx, req.ThreadId, thread.SetMode{By: user(ctx), Mode: mode})
	return &bandav1.SetModeResponse{}, err
}

func (s *Service) SetAgentSettings(ctx context.Context, req *bandav1.SetAgentSettingsRequest) (*bandav1.SetAgentSettingsResponse, error) {
	var changed *bandav1.AgentSettings
	_, events, err := s.store.Update(ctx, req.ThreadId, func(t *store.Thread) ([]*bandav1.Event, error) {
		if err := t.CheckOwner(user(ctx)); err != nil {
			return nil, err
		}
		if or(req.Model, t.Model) == t.Model && or(req.Effort, t.Effort) == t.Effort {
			return nil, nil
		}
		t.Model, t.Effort = or(req.Model, t.Model), or(req.Effort, t.Effort)
		changed = agentSettings(*t)
		return []*bandav1.Event{{Actor: t.OwnerID, Kind: &bandav1.Event_AgentSettings{AgentSettings: changed}}}, nil
	})
	if err != nil {
		return nil, err
	}
	s.broadcast(req.ThreadId, events)
	if changed != nil { // without a runner the next start picks them up from the pod template
		s.sendRunner(req.ThreadId, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_AgentSettings{AgentSettings: changed}})
	}
	return &bandav1.SetAgentSettingsResponse{}, nil
}

func agentSettings(t store.Thread) *bandav1.AgentSettings {
	return &bandav1.AgentSettings{Model: t.Model, Effort: t.Effort}
}

func (s *Service) Retry(ctx context.Context, req *bandav1.RetryRequest) (*bandav1.RetryResponse, error) {
	effect, err := s.apply(ctx, req.ThreadId, thread.Retry{By: user(ctx)})
	if err != nil {
		return nil, err
	}
	s.do(ctx, req.ThreadId, effect)
	return &bandav1.RetryResponse{}, nil
}
