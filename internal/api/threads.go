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
	mode, err := newPermissionMode(req.PermissionMode)
	if err != nil {
		return nil, err
	}
	t, err := s.create(ctx, store.Thread{State: thread.State{OwnerID: user(ctx)}, RepoURL: req.RepoUrl,
		BaseBranch: req.BaseBranch, Model: req.Model, Effort: req.Effort, Profile: req.Profile, PermissionMode: mode}, user(ctx), &bandav1.Message{Text: req.Prompt, Attachments: req.Attachments})
	if err != nil {
		return nil, err
	}
	return &bandav1.CreateThreadResponse{Thread: toProto(t)}, nil
}

// create starts a thread (owner, repo, base, model, effort, profile, permission
// mode and parent taken from t, see start) with its first message, posted by `by`.
func (s *Service) create(ctx context.Context, t store.Thread, by string, first *bandav1.Message) (store.Thread, error) {
	var err error
	if t.PendingPrompt, t.PendingImages, err = s.firstPrompt(ctx, first, by); err != nil {
		return t, err
	}
	t.Title = title(first.Text)
	t.Status, t.Mode = thread.Starting, thread.AgentMode
	return s.start(ctx, t, by, first)
}

// firstPrompt is a new thread's first prompt, from its first message (whose
// attachments it checks): text files inlined, images named in the text and
// returned as ids, which wait with the prompt while the environment starts.
func (s *Service) firstPrompt(ctx context.Context, m *bandav1.Message, by string) (string, []string, error) {
	if strings.TrimSpace(m.Text) == "" {
		return "", nil, connect.NewError(connect.CodeInvalidArgument, "prompt is required")
	}
	if err := s.checkAttachments(ctx, m, by); err != nil {
		return "", nil, err
	}
	texts, err := textAttachments(ctx, s.store, []*bandav1.Message{m})
	if err != nil {
		return "", nil, err
	}
	prompt, images := messageBody(m, texts)
	return prompt, images, nil
}

// scpLike is git's short ssh form, user@host:path.
var scpLike = regexp.MustCompile(`^\w[\w.~-]*@\w[\w.-]*:[\w./~-]+$`)

// checkRepoURL accepts only URLs that git clones plainly: anything else may
// reach git as a transport that runs commands (ext::) or as an option. It
// refuses credentials in an https URL: everyone sees a thread's repository,
// forks reuse it, and the runner redacts only GIT_TOKEN. It refuses whitespace
// and shell characters, which no git host needs and which would land in the
// shell and CEL of Settings' identity recipes. Empty is no repository.
func checkRepoURL(repo string) error {
	if repo == "" || scpLike.MatchString(repo) {
		return nil
	}
	u, err := url.Parse(repo) // refuses control characters
	if err != nil || !slices.Contains([]string{"https", "http", "ssh", "git", "file"}, u.Scheme) || u.Opaque != "" || u.Host == "" && u.Scheme != "file" {
		return connect.NewError(connect.CodeInvalidArgument, "the repository must be an https, http, ssh, git or file URL, or user@host:path")
	}
	if (u.Scheme == "https" || u.Scheme == "http") && u.User != nil {
		return connect.NewError(connect.CodeInvalidArgument, "the repository URL must not contain credentials: connect GitHub, or add a GIT_TOKEN secret, in Settings instead")
	}
	if strings.ContainsAny(repo, " \t\r\n`$;&|<>()\\\"'*?[]{}!#") {
		return connect.NewError(connect.CodeInvalidArgument, "the repository URL must not contain whitespace, quotes, backslashes or any of $`;&|<>()*?[]{}!#")
	}
	return nil
}

// start stores a new thread, posts its first message (if any) as `by` (the
// owner, or the agent that spawned it) and provisions its environment. It sets the id and branch,
// and the defaults for what t leaves empty (the owner's from Settings, else Banda's).
// A thread without a repository has no branches.
func (s *Service) start(ctx context.Context, t store.Thread, by string, first *bandav1.Message) (store.Thread, error) {
	d, err := s.store.Defaults(ctx, t.OwnerID)
	if err != nil {
		return t, err
	}
	t.BaseBranch, t.Model, t.Effort = or(t.BaseBranch, "main"), or(t.Model, or(d.Model, "claude-opus-5-5")), or(t.Effort, or(d.Effort, "high"))
	t.PermissionMode, t.Profile = or(t.PermissionMode, or(d.PermissionMode, "bypass")), or(t.Profile, d.Profile)
	// Here, so forks of a thread created with a credentialed URL are refused too.
	if err := checkRepoURL(t.RepoURL); err != nil {
		return t, err
	}
	if err := checkProfile(t.Profile); err != nil {
		return t, err
	}
	t.ID = strings.ToLower(rand.Text())
	t.Branch = "banda/" + t.ID
	if t.RepoURL == "" {
		t.BaseBranch, t.Branch = "", ""
	}
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
	var git store.GitIdentity // the owner's: after a handoff, the new owner's
	if err == nil {
		git, err = s.store.GitIdentity(ctx, t.OwnerID)
	}
	var token string // a new one for every pod: an old pod's can't connect any more
	if err == nil {
		token, err = s.store.NewRunnerToken(ctx, t.ID, st.AuthMethod == "api_key")
	}
	if err == nil {
		err = s.sandboxes.Ensure(ctx, t, token, st.AuthMethod, secrets, git)
	}
	if err != nil {
		slog.Error("provision sandbox", "thread", t.ID, "err", err)
		s.applyBecause(context.WithoutCancel(ctx), t.ID, thread.EnvLost{}, reasonProvision)
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
	overlaps, err := s.overlaps(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	p := toProto(t)
	p.Overlaps = overlaps[t.ID]
	p.ForgeReady = p.Forge != "" && s.forgeReady(ctx, t)
	return &bandav1.GetThreadResponse{Thread: p}, nil
}

func (s *Service) Trust(ctx context.Context, req *bandav1.TrustRequest) (*bandav1.TrustResponse, error) {
	text := "Teammates' turns need the owner's approval again."
	if req.Trusted {
		text = "Teammates can now run the agent without the owner's approval."
	}
	_, err := s.apply(ctx, req.ThreadId, thread.Trust{By: user(ctx), Trusted: req.Trusted},
		&bandav1.Event{Actor: user(ctx), Kind: &bandav1.Event_Notice{Notice: &bandav1.Notice{Text: text}}})
	s.wakeInboxes()
	return &bandav1.TrustResponse{}, err
}

// wakeInboxes refreshes every inbox: trust changed, so pending permission
// requests became teammates' decisions or stopped being theirs.
func (s *Service) wakeInboxes() {
	s.mu.Lock()
	for w := range s.inbox {
		w.wake()
	}
	s.mu.Unlock()
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
	if req.Profile != "" {
		if err := checkProfile(req.Profile); err != nil {
			return nil, err
		}
	}
	var changed *bandav1.AgentSettings
	_, events, err := s.store.Update(ctx, req.ThreadId, func(_ *store.Store, t *store.Thread) ([]*bandav1.Event, error) {
		if err := t.CheckOwner(user(ctx)); err != nil {
			return nil, err
		}
		model, effort, profile := or(req.Model, t.Model), or(req.Effort, t.Effort), or(req.Profile, t.Profile)
		if model == t.Model && effort == t.Effort && profile == t.Profile {
			return nil, nil
		}
		// The event says what changed, so the chat's divider can.
		event := &bandav1.AgentSettings{}
		if model != t.Model || effort != t.Effort {
			event.Model, event.Effort = model, effort
		}
		if profile != t.Profile {
			event.Profile = profile
		}
		t.Model, t.Effort, t.Profile = model, effort, profile
		changed = agentSettings(*t)
		return []*bandav1.Event{{Actor: t.OwnerID, Kind: &bandav1.Event_AgentSettings{AgentSettings: event}}}, nil
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
	return &bandav1.AgentSettings{Model: t.Model, Effort: t.Effort, Profile: t.Profile}
}

// profiles are the agent's profiles. Their texts are the runner's
// (cmd/runner/profiles), which a repo may replace.
var profiles = []string{"engineer", "spec", "devops", "business"}

func checkProfile(p string) error {
	if !slices.Contains(profiles, p) {
		return connect.NewError(connect.CodeInvalidArgument, "the profile must be engineer, spec, devops or business")
	}
	return nil
}

func (s *Service) Retry(ctx context.Context, req *bandav1.RetryRequest) (*bandav1.RetryResponse, error) {
	effect, err := s.apply(ctx, req.ThreadId, thread.Retry{By: user(ctx)})
	if err != nil {
		return nil, err
	}
	s.do(ctx, req.ThreadId, effect)
	return &bandav1.RetryResponse{}, nil
}
