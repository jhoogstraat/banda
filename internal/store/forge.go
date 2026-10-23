package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GitHubApp is who created the team's GitHub App from Settings.
type GitHubApp struct {
	Host, Owner, CreatedBy string
	CreatedAt              time.Time
	AppID                  int64
	Slug                   string
	Personal               bool // a user account owns it
}

// GitHubAppChange is someone creating, replacing or removing the team's app.
type GitHubAppChange struct {
	Action, By                           string
	At                                   time.Time
	Host, Slug, Owner, WasSlug, WasOwner string
}

// ForgeConnection is a user's OAuth connection to a forge host; its tokens
// are in a Kubernetes Secret.
type ForgeConnection struct {
	UserID, Host, Forge, Login string
	ForgeID                    int64
	AvatarURL                  string
	ConnectedAt                time.Time
	Broken                     bool
}

// GitHubApp returns the app's row for host, nil if it was made by hand.
func (s *Store) GitHubApp(ctx context.Context, host string) (*GitHubApp, error) {
	var a GitHubApp
	err := s.db.QueryRow(ctx, `SELECT host, owner, created_by, created_at, app_id, slug, personal FROM github_apps WHERE host = $1`, host).
		Scan(&a.Host, &a.Owner, &a.CreatedBy, &a.CreatedAt, &a.AppID, &a.Slug, &a.Personal)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

// PutGitHubApp replaces the app's row (there is one app) and records the change.
func (s *Store) PutGitHubApp(ctx context.Context, a GitHubApp, c GitHubAppChange) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM github_apps`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO github_apps (host, owner, created_by, app_id, slug, personal) VALUES ($1, $2, $3, $4, $5, $6)`,
			a.Host, a.Owner, a.CreatedBy, a.AppID, a.Slug, a.Personal); err != nil {
			return err
		}
		return addGitHubAppChange(ctx, tx, c)
	})
}

// GitHubAppOwner records who owns the app now: it can be transferred.
func (s *Store) GitHubAppOwner(ctx context.Context, appID int64, owner string, personal bool) error {
	_, err := s.db.Exec(ctx, `UPDATE github_apps SET owner = $2, personal = $3 WHERE app_id = $1`, appID, owner, personal)
	return err
}

// DeleteGitHubApps forgets the app and records its removal.
func (s *Store) DeleteGitHubApps(ctx context.Context, c GitHubAppChange) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM github_apps`); err != nil {
			return err
		}
		return addGitHubAppChange(ctx, tx, c)
	})
}

func addGitHubAppChange(ctx context.Context, tx pgx.Tx, c GitHubAppChange) error {
	_, err := tx.Exec(ctx, `INSERT INTO github_app_changes (action, by_user, host, slug, owner, was_slug, was_owner) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.Action, c.By, c.Host, c.Slug, c.Owner, c.WasSlug, c.WasOwner)
	return err
}

// LastGitHubAppChange is the latest change to the team's app, nil if none.
func (s *Store) LastGitHubAppChange(ctx context.Context) (*GitHubAppChange, error) {
	var c GitHubAppChange
	err := s.db.QueryRow(ctx, `SELECT action, by_user, at, host, slug, owner, was_slug, was_owner FROM github_app_changes ORDER BY id DESC LIMIT 1`).
		Scan(&c.Action, &c.By, &c.At, &c.Host, &c.Slug, &c.Owner, &c.WasSlug, &c.WasOwner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

// ForgeConnection returns the user's connection to host, nil if none.
func (s *Store) ForgeConnection(ctx context.Context, userID, host string) (*ForgeConnection, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id, host, forge, login, forge_id, avatar_url, connected_at, broken
		FROM forge_connections WHERE user_id = $1 AND host = $2`, userID, host)
	if err != nil {
		return nil, err
	}
	c, err := pgx.CollectOneRow(rows, pgx.RowToStructByPos[ForgeConnection])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

func (s *Store) PutForgeConnection(ctx context.Context, c ForgeConnection) error {
	_, err := s.db.Exec(ctx, `INSERT INTO forge_connections (user_id, host, forge, login, forge_id, avatar_url) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, host) DO UPDATE SET forge = excluded.forge, login = excluded.login, forge_id = excluded.forge_id,
		avatar_url = excluded.avatar_url, connected_at = now(), broken = false`, c.UserID, c.Host, c.Forge, c.Login, c.ForgeID, c.AvatarURL)
	return err
}

func (s *Store) BreakForgeConnection(ctx context.Context, userID, host string) error {
	_, err := s.db.Exec(ctx, `UPDATE forge_connections SET broken = true WHERE user_id = $1 AND host = $2`, userID, host)
	return err
}

func (s *Store) DeleteForgeConnection(ctx context.Context, userID, host string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM forge_connections WHERE user_id = $1 AND host = $2`, userID, host)
	return err
}

// ForgeConnectionUsers are the users connected to host.
func (s *Store) ForgeConnectionUsers(ctx context.Context, host string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id FROM forge_connections WHERE host = $1`, host)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// GitLabApp is who set up the team's GitLab application on a host from Settings.
type GitLabApp struct {
	Host, CreatedBy string
	CreatedAt       time.Time
	ApplicationID   string // '' on rows from before it was kept
}

// GitLabAppChange is someone setting up, replacing or removing a host's application.
type GitLabAppChange struct {
	Action, By                            string
	At                                    time.Time
	Host, ApplicationID, WasApplicationID string
}

// GitLabApp returns the application's row for host, nil if it was made by hand.
func (s *Store) GitLabApp(ctx context.Context, host string) (*GitLabApp, error) {
	var a GitLabApp
	err := s.db.QueryRow(ctx, `SELECT host, created_by, created_at, application_id FROM gitlab_apps WHERE host = $1`, host).
		Scan(&a.Host, &a.CreatedBy, &a.CreatedAt, &a.ApplicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

// PutGitLabApp replaces host's row (one application per host) and records the change.
func (s *Store) PutGitLabApp(ctx context.Context, a GitLabApp, c GitLabAppChange) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO gitlab_apps (host, created_by, application_id) VALUES ($1, $2, $3)
			ON CONFLICT (host) DO UPDATE SET created_by = excluded.created_by, created_at = now(), application_id = excluded.application_id`,
			a.Host, a.CreatedBy, a.ApplicationID); err != nil {
			return err
		}
		return addGitLabAppChange(ctx, tx, c)
	})
}

// DeleteGitLabApp forgets host's row and records the removal.
func (s *Store) DeleteGitLabApp(ctx context.Context, c GitLabAppChange) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM gitlab_apps WHERE host = $1`, c.Host); err != nil {
			return err
		}
		return addGitLabAppChange(ctx, tx, c)
	})
}

func addGitLabAppChange(ctx context.Context, tx pgx.Tx, c GitLabAppChange) error {
	_, err := tx.Exec(ctx, `INSERT INTO gitlab_app_changes (action, by_user, host, application_id, was_application_id) VALUES ($1, $2, $3, $4, $5)`,
		c.Action, c.By, c.Host, c.ApplicationID, c.WasApplicationID)
	return err
}

// LastGitLabAppChange is the latest change to host's application, nil if none.
func (s *Store) LastGitLabAppChange(ctx context.Context, host string) (*GitLabAppChange, error) {
	var c GitLabAppChange
	err := s.db.QueryRow(ctx, `SELECT action, by_user, at, host, application_id, was_application_id FROM gitlab_app_changes
		WHERE host = $1 ORDER BY id DESC LIMIT 1`, host).Scan(&c.Action, &c.By, &c.At, &c.Host, &c.ApplicationID, &c.WasApplicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}
