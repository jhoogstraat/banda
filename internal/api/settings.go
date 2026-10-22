package api

import (
	"context"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/store"
)

// Secret and API key values go straight to Kubernetes Secrets: they are never
// stored in Postgres, logged or returned.

const harness = "claude" // the only one so far

var (
	authToProto   = map[string]bandav1.AuthMethod{"subscription": bandav1.AuthMethod_AUTH_METHOD_SUBSCRIPTION, "api_key": bandav1.AuthMethod_AUTH_METHOD_API_KEY}
	authFromProto = map[bandav1.AuthMethod]string{bandav1.AuthMethod_AUTH_METHOD_SUBSCRIPTION: "subscription", bandav1.AuthMethod_AUTH_METHOD_API_KEY: "api_key"}
	loginToProto  = map[string]bandav1.LoginState{"unknown": bandav1.LoginState_LOGIN_STATE_UNKNOWN, "logged_in": bandav1.LoginState_LOGIN_STATE_LOGGED_IN, "logged_out": bandav1.LoginState_LOGIN_STATE_LOGGED_OUT}
	loginCommands = map[bandav1.LoginAction]string{bandav1.LoginAction_LOGIN_ACTION_LOGIN: "claude auth login", bandav1.LoginAction_LOGIN_ACTION_LOGOUT: "claude auth logout"}

	secretName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	// Harness credentials and what the environment itself relies on.
	reserved         = []string{"OPENAI_API_KEY", "CODEX_HOME", "HOME", "PATH"}
	reservedPrefixes = []string{"ANTHROPIC_", "CLAUDE_", "BANDA_"}
	// Claude subscription OAuth tokens (access, refresh). Banda must never
	// store them; see PLAN.md, "Constraint: Claude subscriptions".
	oauthToken = regexp.MustCompile(`sk-ant-o[ar]t`)
)

func checkSecretName(name string) error {
	if !secretName.MatchString(name) {
		return connect.NewError(connect.CodeInvalidArgument, "a secret name is an environment variable name: A-Z, 0-9 and _, not starting with a digit")
	}
	if slices.Contains(reserved, name) || slices.ContainsFunc(reservedPrefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
		return connect.NewError(connect.CodeInvalidArgument, name+" is reserved")
	}
	return nil
}

func checkNotOAuth(value string) error {
	if oauthToken.MatchString(value) {
		return connect.NewError(connect.CodeInvalidArgument, "This looks like a Claude subscription token, which Banda must not store. Connect your subscription under Accounts instead, or use an API key (sk-ant-api…).")
	}
	return nil
}

func (s *Service) GetSettings(ctx context.Context, _ *bandav1.GetSettingsRequest) (*bandav1.GetSettingsResponse, error) {
	u := user(ctx)
	st, err := s.store.Settings(ctx, u, harness)
	if err != nil {
		return nil, err
	}
	secrets, err := s.store.Secrets(ctx, u)
	if err != nil {
		return nil, err
	}
	acc := &bandav1.HarnessAccount{Harness: harness, DisplayName: "Claude Code", AuthMethod: authToProto[st.AuthMethod],
		ApiKeySet: st.APIKeySet, LoginState: loginToProto[st.LoginState]}
	if st.LoginCheckedAt != nil {
		acc.LoginCheckedAt = timestamppb.New(*st.LoginCheckedAt)
	}
	d, err := s.store.Defaults(ctx, u)
	if err != nil {
		return nil, err
	}
	git, err := s.store.GitIdentity(ctx, u)
	if err != nil {
		return nil, err
	}
	res := &bandav1.GetSettingsResponse{Accounts: []*bandav1.HarnessAccount{acc}, DefaultProfile: d.Profile,
		GitIdentity:    &bandav1.GitIdentity{Name: git.Name, Email: git.Email, SignInName: git.SignInName, SignInEmail: git.SignInEmail},
		ThreadDefaults: &bandav1.ThreadDefaults{Model: d.Model, Effort: d.Effort, PermissionMode: permissionModeToProto[d.PermissionMode]}}
	for _, sec := range secrets {
		res.Secrets = append(res.Secrets, &bandav1.SecretInfo{Name: sec.Name, UpdatedAt: timestamppb.New(sec.UpdatedAt)})
	}
	return res, nil
}

func (s *Service) SetAuthMethod(ctx context.Context, req *bandav1.SetAuthMethodRequest) (*bandav1.SetAuthMethodResponse, error) {
	method, ok := authFromProto[req.AuthMethod]
	if req.Harness != harness || !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, "unknown harness or auth method")
	}
	u, key := user(ctx), strings.TrimSpace(req.ApiKey)
	st, err := s.store.Settings(ctx, u, harness)
	if err != nil {
		return nil, err
	}
	if err := checkNotOAuth(key); err != nil {
		return nil, err
	}
	if method == "api_key" && key == "" && !st.APIKeySet {
		return nil, connect.NewError(connect.CodeInvalidArgument, "an API key is required")
	}
	if key != "" {
		if err := s.sandboxes.SetAPIKey(ctx, u, key); err != nil {
			return nil, err
		}
	}
	return &bandav1.SetAuthMethodResponse{}, s.store.SetAuthMethod(ctx, u, harness, method, key != "")
}

func (s *Service) SetSecret(ctx context.Context, req *bandav1.SetSecretRequest) (*bandav1.SetSecretResponse, error) {
	if err := checkSecretName(req.Name); err != nil {
		return nil, err
	}
	if err := checkNotOAuth(req.Value); err != nil {
		return nil, err
	}
	// The name first: environments started meanwhile must know to redact it.
	if err := s.store.PutSecret(ctx, user(ctx), req.Name); err != nil {
		return nil, err
	}
	defer s.tokensChanged(user(ctx))
	return &bandav1.SetSecretResponse{}, s.sandboxes.SetSecret(ctx, user(ctx), req.Name, []byte(req.Value))
}

func (s *Service) DeleteSecret(ctx context.Context, req *bandav1.DeleteSecretRequest) (*bandav1.DeleteSecretResponse, error) {
	if !secretName.MatchString(req.Name) { // reserved names are fine: none exist
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid secret name")
	}
	defer s.tokensChanged(user(ctx))
	// The value first, so environments keep redacting it until it is gone.
	if err := s.sandboxes.SetSecret(ctx, user(ctx), req.Name, nil); err != nil {
		return nil, err
	}
	return &bandav1.DeleteSecretResponse{}, s.store.DeleteSecret(ctx, user(ctx), req.Name)
}

func (s *Service) SetDefaultProfile(ctx context.Context, req *bandav1.SetDefaultProfileRequest) (*bandav1.SetDefaultProfileResponse, error) {
	if err := checkProfile(req.Profile); err != nil {
		return nil, err
	}
	return &bandav1.SetDefaultProfileResponse{}, s.store.SetDefaultProfile(ctx, user(ctx), req.Profile)
}

func (s *Service) SetThreadDefaults(ctx context.Context, req *bandav1.SetThreadDefaultsRequest) (*bandav1.SetThreadDefaultsResponse, error) {
	d := req.GetDefaults()
	mode, err := newPermissionMode(d.GetPermissionMode())
	if err != nil {
		return nil, err
	}
	if len(d.GetModel()) > 100 || len(d.GetEffort()) > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the model or effort is too long")
	}
	return &bandav1.SetThreadDefaultsResponse{}, s.store.SetDefaults(ctx, user(ctx), store.Defaults{Model: d.GetModel(), Effort: d.GetEffort(), PermissionMode: mode})
}

// What git refuses in a name or email, and line breaks.
var badIdentity = regexp.MustCompile(`[<>\n\r]`)

func (s *Service) SetGitIdentity(ctx context.Context, req *bandav1.SetGitIdentityRequest) (*bandav1.SetGitIdentityResponse, error) {
	name, email := strings.TrimSpace(req.Name), strings.TrimSpace(req.Email)
	if badIdentity.MatchString(name+email) || len(name) > 200 || len(email) > 200 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "a name or email can't contain <, > or line breaks")
	}
	if email != "" && !strings.Contains(email, "@") {
		return nil, connect.NewError(connect.CodeInvalidArgument, "that isn't an email address")
	}
	return &bandav1.SetGitIdentityResponse{}, s.store.SetGitIdentity(ctx, user(ctx), name, email)
}

func (s *Service) ListTeamSecrets(ctx context.Context, _ *bandav1.ListTeamSecretsRequest) (*bandav1.ListTeamSecretsResponse, error) {
	secrets, err := s.store.TeamSecrets(ctx)
	if err != nil {
		return nil, err
	}
	res := &bandav1.ListTeamSecretsResponse{}
	for _, sec := range secrets {
		res.Secrets = append(res.Secrets, &bandav1.SecretInfo{Name: sec.Name, UpdatedAt: timestamppb.New(sec.UpdatedAt), UpdatedBy: sec.UpdatedBy})
	}
	return res, nil
}

// SetTeamSecret and DeleteTeamSecret work like SetSecret and DeleteSecret,
// on the team's secrets: anyone signed in may change them.
func (s *Service) SetTeamSecret(ctx context.Context, req *bandav1.SetTeamSecretRequest) (*bandav1.SetTeamSecretResponse, error) {
	if err := checkSecretName(req.Name); err != nil {
		return nil, err
	}
	if err := checkNotOAuth(req.Value); err != nil {
		return nil, err
	}
	if err := s.store.PutTeamSecret(ctx, req.Name, user(ctx)); err != nil {
		return nil, err
	}
	defer s.tokensChanged("")
	return &bandav1.SetTeamSecretResponse{}, s.sandboxes.SetTeamSecret(ctx, req.Name, []byte(req.Value))
}

func (s *Service) DeleteTeamSecret(ctx context.Context, req *bandav1.DeleteTeamSecretRequest) (*bandav1.DeleteTeamSecretResponse, error) {
	if !secretName.MatchString(req.Name) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid secret name")
	}
	defer s.tokensChanged("")
	if err := s.sandboxes.SetTeamSecret(ctx, req.Name, nil); err != nil {
		return nil, err
	}
	return &bandav1.DeleteTeamSecretResponse{}, s.store.DeleteTeamSecret(ctx, req.Name)
}

// loginKey is how a user's login runner is keyed in runners and terminals,
// next to thread ids (which never contain a colon).
func loginKey(userID string) string { return loginPrefix + userID }

const loginPrefix = "login:"

// OpenLoginTerminal starts or resumes the caller's login environment and runs
// the harness's login or logout command there; nothing else can run in it.
func (s *Service) OpenLoginTerminal(ctx context.Context, req *bandav1.OpenLoginTerminalRequest, stream bandav1connect.SettingsServiceOpenLoginTerminalServerStream) error {
	command, ok := loginCommands[req.Action]
	if req.Harness != harness || !ok {
		return connect.NewError(connect.CodeInvalidArgument, "unknown harness or action")
	}
	u := user(ctx)
	s.loginUsed(u)
	defer s.loginUsed(u)
	s.mu.Lock()
	_, up := s.runners[loginKey(u)]
	s.mu.Unlock()
	if !up { // suspended, never started, or stuck: a fresh pod
		newToken := func() (string, error) { return s.store.NewLoginToken(ctx, u) }
		if err := s.sandboxes.EnsureLogin(ctx, u, newToken); err != nil {
			return err
		}
	}
	return s.terminal(ctx, loginKey(u), &bandav1.TerminalOpen{Cols: req.Cols, Rows: req.Rows, Command: command}, stream.Send)
}

func (s *Service) WriteLoginTerminal(ctx context.Context, req *bandav1.WriteLoginTerminalRequest) (*bandav1.WriteTerminalResponse, error) {
	return &bandav1.WriteTerminalResponse{}, s.toTerminal(loginKey(user(ctx)), terminalInput(req.Data))
}

func (s *Service) ResizeLoginTerminal(ctx context.Context, req *bandav1.ResizeLoginTerminalRequest) (*bandav1.ResizeTerminalResponse, error) {
	return &bandav1.ResizeTerminalResponse{}, s.toTerminal(loginKey(user(ctx)), terminalResize(req.Cols, req.Rows))
}

func (s *Service) loginUsed(userID string) {
	s.mu.Lock()
	s.loginLastUsed[userID] = time.Now()
	s.mu.Unlock()
}

// suspendIdleLogins suspends login environments that have had no terminal
// for LoginIdle. After an API restart every connected one counts as idle.
func (s *Service) suspendIdleLogins(ctx context.Context) {
	var idle []string
	s.mu.Lock()
	for key := range s.runners {
		if u, ok := strings.CutPrefix(key, loginPrefix); ok && s.terminals[key] == nil && time.Since(s.loginLastUsed[u]) > s.LoginIdle {
			idle = append(idle, u)
		}
	}
	s.mu.Unlock()
	for _, u := range idle {
		if err := s.sandboxes.SuspendLogin(ctx, u); err != nil {
			slog.Error("suspend login environment", "user", u, "err", err)
		}
	}
}
