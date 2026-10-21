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

// DefaultProfile is the profile of the user's new threads that don't name one.
func (s *Store) DefaultProfile(ctx context.Context, userID string) (string, error) {
	profile := "engineer"
	err := s.db.QueryRow(ctx, `SELECT default_profile FROM users WHERE id = $1`, userID).Scan(&profile)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return profile, err
}

func (s *Store) SetDefaultProfile(ctx context.Context, userID, profile string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO users (id, name, default_profile) VALUES ($1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET default_profile = excluded.default_profile`, userID, profile)
	return err
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

// NewRunnerToken gives the thread a new runner token. Only its hash is kept.
func (s *Store) NewRunnerToken(ctx context.Context, id string) (string, error) {
	token := rand.Text()
	_, err := s.db.Exec(ctx, `UPDATE threads SET runner_hash = $2 WHERE id = $1`, id, TokenHash(token))
	return token, err
}

// TokenHash is what the database keeps of a runner token.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
