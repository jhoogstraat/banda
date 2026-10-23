package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Audience is one of the team's workload identity token audiences.
type Audience struct {
	Audience string
	AddedBy  string
	AddedAt  time.Time
}

func (s *Store) Audiences(ctx context.Context) ([]Audience, error) {
	rows, err := s.db.Query(ctx, `SELECT audience, added_by, added_at FROM id_token_audiences ORDER BY added_at, audience`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Audience])
}

// AddAudience adds it; one already there keeps who added it.
func (s *Store) AddAudience(ctx context.Context, audience, by string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO id_token_audiences (audience, added_by) VALUES ($1, $2) ON CONFLICT DO NOTHING`, audience, by)
	return err
}

func (s *Store) RemoveAudience(ctx context.Context, audience string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM id_token_audiences WHERE audience = $1`, audience)
	return err
}

// IdentityEmail is the sign-in email workload identity tokens name for a
// user, whether the identity provider verified it, and whether another user
// signed in with the same one (emails are compared ignoring case). Empty for
// a user who never signed in.
func (s *Store) IdentityEmail(ctx context.Context, userID string) (email string, verified, shared bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT email, email_verified,
		EXISTS (SELECT 1 FROM users o WHERE lower(o.email) = lower(u.email) AND o.id <> u.id)
		FROM users u WHERE id = $1`, userID).Scan(&email, &verified, &shared)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return email, verified, shared, err
}

// RecentRepos are the repository URLs of threads, most recently used first.
func (s *Store) RecentRepos(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT repo_url FROM threads WHERE repo_url <> '' GROUP BY repo_url ORDER BY max(updated_at) DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
