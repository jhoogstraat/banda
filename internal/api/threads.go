package api

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
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
	mode, err := newPermissionMode(req.PermissionMode)
	if err != nil {
		return nil, err
	}
	t, err := s.create(ctx, store.Thread{State: thread.State{OwnerID: user(ctx)}, RepoURL: req.RepoUrl,
		BaseBranch: req.BaseBranch, Model: req.Model, Effort: req.Effort, PermissionMode: mode}, user(ctx), req.Prompt)
	if err != nil {
		return nil, err
	}
	return &bandav1.CreateThreadResponse{Thread: toProto(t)}, nil
}

// create starts a thread (owner, repo, base, model, effort, permission mode and
// parent taken from t, see start) with its first prompt, posted by `by`.
func (s *Service) create(ctx context.Context, t store.Thread, by, prompt string) (store.Thread, error) {
	if strings.TrimSpace(prompt) == "" {
		return t, connect.NewError(connect.CodeInvalidArgument, "prompt is required")
	}
	t.Title, t.PendingPrompt = title(prompt), prompt
	t.Status, t.Mode = thread.Starting, thread.AgentMode
	return s.start(ctx, t, by, &bandav1.Message{Text: prompt})
}

// scpLike is git's short ssh form, user@host:path.
var scpLike = regexp.MustCompile(`^\w[\w.~-]*@\w[\w.-]*:[\w./~-]+$`)

// checkRepoURL accepts only URLs that git clones plainly: anything else may
// reach git as a transport that runs commands (ext::) or as an option. It
// refuses credentials in an https URL: everyone sees a thread's repository,
// forks reuse it, and the runner redacts only GIT_TOKEN.
func checkRepoURL(repo string) error {
	if scpLike.MatchString(repo) {
		return nil
	}
	u, err := url.Parse(repo) // refuses control characters
	if err != nil || !slices.Contains([]string{"https", "http", "ssh", "git", "file"}, u.Scheme) || u.Opaque != "" || u.Host == "" && u.Scheme != "file" {
		return connect.NewError(connect.CodeInvalidArgument, "the repository must be an https, http, ssh, git or file URL, or user@host:path")
	}
	if (u.Scheme == "https" || u.Scheme == "http") && u.User != nil {
		return connect.NewError(connect.CodeInvalidArgument, "the repository URL must not contain credentials: add a GIT_TOKEN secret in Settings instead")
	}
	return nil
}

// start stores a new thread, posts its first message (if any) as `by` (the
// owner, or the agent that spawned it) and provisions its environment. It sets the id and branch,
// and the defaults for what t leaves empty.
func (s *Service) start(ctx context.Context, t store.Thread, by string, first *bandav1.Message) (store.Thread, error) {
	t.BaseBranch, t.Model, t.Effort = or(t.BaseBranch, "main"), or(t.Model, "claude-opus-5-5"), or(t.Effort, "high")
	t.PermissionMode = or(t.PermissionMode, "bypass")
	// Here, so forks of a thread created with a credentialed URL are refused too.
	if err := checkRepoURL(t.RepoURL); err != nil {
		return t, err
	}
	t.ID = strings.ToLower(rand.Text())
	t.Branch = "banda/" + t.ID
	if err := s.store.Create(ctx, t); err != nil {
		return t, err
	}
	t, events, err := s.store.Update(ctx, t.ID, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		if first == nil {
			return nil, nil
		}
		t.DeliveredSeq = t.LastSeq + 1 // the pending prompt holds it
		return []*bandav1.Event{messageEvent(by, first)}, nil
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
	if err := s.store.ResetEnv(ctx, t.ID); err != nil {
		slog.Error("cancel environment deletion", "thread", t.ID, "err", err)
	}
	// Ensure replaces a running pod; its runner's disconnect is expected, and
	// everything bound to that runner ends now.
	s.mu.Lock()
	if r := s.runners[t.ID]; r != nil {
		r.tunnels.CloseAll()
	}
	delete(s.runners, t.ID)
	s.closeTerminal(t.ID)
	s.previewChanged(t.ID)
	s.mu.Unlock()
	st, err := s.store.Settings(ctx, t.OwnerID, harness)
	var secrets []string
	if err == nil {
		secrets, err = s.store.SecretNames(ctx, t.OwnerID)
	}
	var token string // a new one for every pod: an old pod's can't connect any more
	if err == nil {
		token, err = s.store.NewRunnerToken(ctx, t.ID)
	}
	if err == nil {
		err = s.sandboxes.Ensure(ctx, t, token, st.AuthMethod, secrets)
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
	_, events, err := s.store.Update(ctx, req.ThreadId, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
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
