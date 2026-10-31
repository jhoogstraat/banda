package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Settings is a user's account for one harness.
type Settings struct {
	AuthMethod     string // "subscription" or "api_key"
	APIKeySet      bool
	LoginState     string // "unknown", "logged_in" or "logged_out"
	LoginCheckedAt *time.Time
}

type Secret struct {
	Name      string
	UpdatedAt time.Time
}

type TeamSecret struct {
	Name      string
	UpdatedAt time.Time
	UpdatedBy string
}

// Settings returns the defaults if the user never changed them.
func (s *Store) Settings(ctx context.Context, userID, harness string) (Settings, error) {
	st := Settings{AuthMethod: "subscription", LoginState: "unknown"}
	err := s.db.QueryRow(ctx, `SELECT auth_method, api_key_set, login_state, login_checked_at FROM harness_settings
		WHERE user_id = $1 AND harness = $2`, userID, harness).Scan(&st.AuthMethod, &st.APIKeySet, &st.LoginState, &st.LoginCheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return st, err
}

// SetAuthMethod stores the method; apiKeySet only ever turns on.
func (s *Store) SetAuthMethod(ctx context.Context, userID, harness, method string, apiKeySet bool) error {
	_, err := s.db.Exec(ctx, `INSERT INTO harness_settings (user_id, harness, auth_method, api_key_set) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, harness) DO UPDATE SET auth_method = excluded.auth_method,
		api_key_set = harness_settings.api_key_set OR excluded.api_key_set`, userID, harness, method, apiKeySet)
	return err
}

func (s *Store) SetLogin(ctx context.Context, userID, harness string, loggedIn bool) error {
	state := "logged_out"
	if loggedIn {
		state = "logged_in"
	}
	_, err := s.db.Exec(ctx, `INSERT INTO harness_settings (user_id, harness, login_state, login_checked_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, harness) DO UPDATE SET login_state = excluded.login_state, login_checked_at = now()`, userID, harness, state)
	return err
}

func (s *Store) Secrets(ctx context.Context, userID string) ([]Secret, error) {
	rows, err := s.db.Query(ctx, `SELECT name, updated_at FROM secrets WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Secret])
}

func (s *Store) PutSecret(ctx context.Context, userID, name string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO secrets (user_id, name) VALUES ($1, $2)
		ON CONFLICT (user_id, name) DO UPDATE SET updated_at = now()`, userID, name)
	return err
}

func (s *Store) DeleteSecret(ctx context.Context, userID, name string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM secrets WHERE user_id = $1 AND name = $2`, userID, name)
	return err
}

// Defaults is what the user's new threads start with when they don't say:
// a profile always, the rest empty for Banda's default.
type Defaults struct {
	Profile, Model, Effort, PermissionMode string
}

func (s *Store) Defaults(ctx context.Context, userID string) (Defaults, error) {
	d := Defaults{Profile: "engineer"}
	err := s.db.QueryRow(ctx, `SELECT default_profile, default_model, default_effort, default_permission_mode FROM users WHERE id = $1`,
		userID).Scan(&d.Profile, &d.Model, &d.Effort, &d.PermissionMode)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return d, err
}

// SetDefaults stores all but the profile (SetDefaultProfile).
func (s *Store) SetDefaults(ctx context.Context, userID string, d Defaults) error {
	_, err := s.db.Exec(ctx, `INSERT INTO users (id, name, default_model, default_effort, default_permission_mode) VALUES ($1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET default_model = excluded.default_model, default_effort = excluded.default_effort,
		default_permission_mode = excluded.default_permission_mode`, userID, d.Model, d.Effort, d.PermissionMode)
	return err
}

// GitIdentity is a user's name and email for commits: their own choice
// (Name, Email; empty when none) and their sign-in's.
type GitIdentity struct {
	Name, Email, SignInName, SignInEmail string
}

// Commits is the identity commits get.
func (g GitIdentity) Commits() (name, email string) {
	name, email = g.Name, g.Email
	if name == "" {
		name = g.SignInName
	}
	if email == "" {
		email = g.SignInEmail
	}
	return name, email
}

func (s *Store) GitIdentity(ctx context.Context, userID string) (GitIdentity, error) {
	g := GitIdentity{SignInName: userID} // a user who never signed in (dev auth) is named by their id
	err := s.db.QueryRow(ctx, `SELECT git_name, git_email, name, email FROM users WHERE id = $1`, userID).
		Scan(&g.Name, &g.Email, &g.SignInName, &g.SignInEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return g, err
}

func (s *Store) SetGitIdentity(ctx context.Context, userID, name, email string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO users (id, name, git_name, git_email) VALUES ($1, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET git_name = excluded.git_name, git_email = excluded.git_email`, userID, name, email)
	return err
}

func (s *Store) SetDefaultProfile(ctx context.Context, userID, profile string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO users (id, name, default_profile) VALUES ($1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET default_profile = excluded.default_profile`, userID, profile)
	return err
}

func (s *Store) TeamSecrets(ctx context.Context) ([]TeamSecret, error) {
	rows, err := s.db.Query(ctx, `SELECT name, updated_at, updated_by FROM team_secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TeamSecret])
}

func (s *Store) PutTeamSecret(ctx context.Context, name, by string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO team_secrets (name, updated_by) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET updated_by = excluded.updated_by, updated_at = now()`, name, by)
	return err
}

func (s *Store) DeleteTeamSecret(ctx context.Context, name string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM team_secrets WHERE name = $1`, name)
	return err
}

// SecretNames are the variables a user's threads get from secrets: the
// team's and their own.
func (s *Store) SecretNames(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT name FROM team_secrets UNION SELECT name FROM secrets WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// NewLoginToken gives the user's login environment a new runner token,
// creating the user if needed. Only its hash is kept.
func (s *Store) NewLoginToken(ctx context.Context, userID string) (string, error) {
	token := rand.Text()
	_, err := s.db.Exec(ctx, `INSERT INTO users (id, name, login_hash) VALUES ($1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET login_hash = excluded.login_hash`, userID, TokenHash(token))
	return token, err
}

func (s *Store) CheckLoginToken(ctx context.Context, userID, token string) bool {
	var want *string
	s.db.QueryRow(ctx, `SELECT login_hash FROM users WHERE id = $1`, userID).Scan(&want)
	return want != nil && token != "" && subtle.ConstantTimeCompare([]byte(*want), []byte(TokenHash(token))) == 1
}

// DropRunnerToken invalidates the thread's runner token, for a handoff: the
// old owner's pod must not act for the thread, or mint tokens naming the new
// owner, before provision gives the new pod its own.
func (s *Store) DropRunnerToken(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE threads SET runner_hash = '' WHERE id = $1`, id)
	return err
}

// NewRunnerToken gives the thread a new runner token, for a pod started
// with the API key or not. Only its hash is kept; with it, whether that pod,
// the only one whose runner can connect, has the key (State.EnvAPIKey), and
// whether it moved off the previous pod's Claude volume, which holds the
// session (a thread with no runner token yet, or after a handoff, has none).
func (s *Store) NewRunnerToken(ctx context.Context, id string, apiKey bool) (string, error) {
	token := rand.Text()
	_, err := s.db.Exec(ctx, `UPDATE threads SET runner_hash = $2, env_api_key = $3,
		session_moved = session_moved OR (runner_hash <> '' AND env_api_key <> $3) WHERE id = $1`, id, TokenHash(token), apiKey)
	return token, err
}

// TakeSessionMoved reports whether the thread's environment moved to another
// Claude volume since the last call.
func (s *Store) TakeSessionMoved(ctx context.Context, id string) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE threads SET session_moved = false WHERE id = $1 AND session_moved`, id)
	return tag.RowsAffected() > 0, err
}

// TokenHash is what the database keeps of a runner token.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
