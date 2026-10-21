package store

import (
	"context"
	"time"
)

type User struct {
	ID    string
	Name  string
	Email string
}

func (s *Store) UpsertUser(ctx context.Context, u User) error {
	_, err := s.db.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, email = excluded.email`, u.ID, u.Name, u.Email)
	return err
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, email FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.Email); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetSnooze sets (or with nil clears) the viewer's snooze of a thread.
func (s *Store) SetSnooze(ctx context.Context, viewerID, threadID string, until *time.Time) error {
	_, err := s.db.Exec(ctx, `INSERT INTO thread_views (user_id, thread_id, snoozed_until) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, thread_id) DO UPDATE SET snoozed_until = excluded.snoozed_until`, viewerID, threadID, until)
	return err
}

func (s *Store) SetSettled(ctx context.Context, viewerID, threadID string, settled bool) error {
	_, err := s.db.Exec(ctx, `INSERT INTO thread_views (user_id, thread_id, settled_at)
		VALUES ($1, $2, CASE WHEN $3 THEN now() END)
		ON CONFLICT (user_id, thread_id) DO UPDATE SET settled_at = excluded.settled_at`, viewerID, threadID, settled)
	return err
}

// MarkRead marks the thread read for the viewer up to its latest event.
func (s *Store) MarkRead(ctx context.Context, viewerID, threadID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO thread_views (user_id, thread_id, seen_seq) SELECT $1, id, last_seq FROM threads WHERE id = $2
		ON CONFLICT (user_id, thread_id) DO UPDATE SET seen_seq = greatest(thread_views.seen_seq, excluded.seen_seq)`, viewerID, threadID)
	return err
}

// Mention records that the users were mentioned by the event at seq, and
// marks the poster's own open mentions in the thread as answered.
func (s *Store) Mention(ctx context.Context, threadID string, seq int64, poster string, mentioned []string) error {
	if _, err := s.db.Exec(ctx, `UPDATE mentions SET answered_at = now()
		WHERE thread_id = $1 AND user_id = $2 AND answered_at IS NULL`, threadID, poster); err != nil {
		return err
	}
	for _, u := range mentioned {
		if _, err := s.db.Exec(ctx, `INSERT INTO mentions (thread_id, seq, user_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, threadID, seq, u); err != nil {
			return err
		}
	}
	return nil
}

// Idle returns threads waiting for input (or stuck starting or errored)
// with no activity since before.
func (s *Store) Idle(ctx context.Context, before time.Time) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM threads
		WHERE status IN ('awaiting_input', 'starting', 'errored') AND updated_at < $1`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EnsureUser adds a user known only by id (dev auth) without touching
// existing names.
func (s *Store) EnsureUser(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO users (id, name) VALUES ($1, $1) ON CONFLICT DO NOTHING`, id)
	return err
}
