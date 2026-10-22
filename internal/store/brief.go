package store

import (
	"context"
	"time"
)

// BriefItem is one line of a user's brief.
type BriefItem struct {
	ThreadID, Title, OwnerID, Text string
	Time                           time.Time
}

// Brief is what happened between Since and Until (both the database's clock),
// newest first, at most briefMax per list.
type Brief struct {
	Since, Until                        time.Time
	Finished, Waiting, Spawned, Settled []BriefItem
}

const briefMax = 50

// Brief reads viewerID's brief since they last saw it, or of the last day.
// Every query reads columns kept up to date by Update and the views, never events.
func (s *Store) Brief(ctx context.Context, viewerID string) (Brief, error) {
	var b Brief
	if err := s.db.QueryRow(ctx, `SELECT coalesce((SELECT brief_seen_at FROM users WHERE id = $1), now() - interval '1 day'), now()`,
		viewerID).Scan(&b.Since, &b.Until); err != nil {
		return b, err
	}
	for _, q := range []struct {
		list  *[]BriefItem
		query string
		args  []any
	}{
		{&b.Finished, `SELECT id, title, owner_id, summary, finished_at FROM threads
			WHERE owner_id <> $1 AND updated_at > $2 AND finished_at > $2 ORDER BY finished_at DESC LIMIT $3`, []any{viewerID, b.Since, briefMax}},
		{&b.Waiting, `SELECT * FROM (SELECT t.id, t.title, t.owner_id, coalesce(t.activity->>'text', ''), t.updated_at FROM threads t
			WHERE t.owner_id = $1 AND (t.status IN ('awaiting_input', 'errored') OR t.requests > 0 OR t.permissions <> '{}')
			AND NOT EXISTS (SELECT 1 FROM thread_views v WHERE v.thread_id = t.id AND v.user_id = $1
				AND (v.settled_at IS NOT NULL OR v.snoozed_until > now()))
			UNION ALL SELECT t.id, t.title, t.owner_id, 'mentioned you', t.updated_at FROM threads t
			WHERE t.owner_id <> $1 AND EXISTS (SELECT 1 FROM mentions m WHERE m.thread_id = t.id AND m.user_id = $1 AND m.answered_at IS NULL)
			) w ORDER BY 5 DESC LIMIT $2`, []any{viewerID, briefMax}},
		{&b.Spawned, `SELECT t.id, t.title, t.owner_id, coalesce((SELECT p.title FROM threads p WHERE p.id = t.parent_id), ''), t.created_at
			FROM threads t WHERE t.parent_id <> '' AND t.updated_at > $1 AND t.created_at > $1 ORDER BY t.created_at DESC LIMIT $2`, []any{b.Since, briefMax}},
		{&b.Settled, `SELECT t.id, t.title, t.owner_id, t.summary, o.settled_at FROM thread_views o JOIN threads t
			ON t.id = o.thread_id AND t.owner_id = o.user_id
			WHERE o.settled_at > $2 AND o.user_id <> $1 ORDER BY o.settled_at DESC LIMIT $3`, []any{viewerID, b.Since, briefMax}},
	} {
		var err error
		if *q.list, err = s.briefItems(ctx, q.query, q.args...); err != nil {
			return b, err
		}
	}
	return b, nil
}

func (s *Store) briefItems(ctx context.Context, query string, args ...any) ([]BriefItem, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BriefItem
	for rows.Next() {
		var i BriefItem
		if err := rows.Scan(&i.ThreadID, &i.Title, &i.OwnerID, &i.Text, &i.Time); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// SetBriefSeen records that the user saw their brief up to until; it never moves back.
func (s *Store) SetBriefSeen(ctx context.Context, userID string, until time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET brief_seen_at = greatest(brief_seen_at, $2) WHERE id = $1`, userID, until)
	return err
}
