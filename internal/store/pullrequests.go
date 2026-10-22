package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// PullRequest is the thread's linked pull request as last fetched, or nil.
func (s *Store) PullRequest(ctx context.Context, threadID string) (*bandav1.PullRequest, error) {
	var data []byte
	err := s.db.QueryRow(ctx, `SELECT data FROM pull_requests WHERE thread_id = $1`, threadID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pr := new(bandav1.PullRequest)
	return pr, protojson.Unmarshal(data, pr)
}

// PutPullRequest stores the thread's pull request and records the poll.
func (s *Store) PutPullRequest(ctx context.Context, threadID string, pr *bandav1.PullRequest) error {
	data, err := protojson.Marshal(pr)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO pull_requests (thread_id, data) VALUES ($1, $2)
		ON CONFLICT (thread_id) DO UPDATE SET data = $2, polled_at = now()`, threadID, data)
	return err
}

// Polled is a linked pull request that may be due for a poll.
type Polled struct {
	ThreadID string
	State    bandav1.PullState
	PolledAt time.Time
}

// PullRequestsToPoll lists the linked pull requests that aren't merged, least recently polled first.
func (s *Store) PullRequestsToPoll(ctx context.Context) ([]Polled, error) {
	rows, err := s.db.Query(ctx, `SELECT thread_id, coalesce(data->'summary'->>'state', ''), polled_at FROM pull_requests
		WHERE coalesce(data->'summary'->>'state', '') <> 'PULL_STATE_MERGED' ORDER BY polled_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Polled
	for rows.Next() {
		var p Polled
		var state string
		if err := rows.Scan(&p.ThreadID, &state, &p.PolledAt); err != nil {
			return nil, err
		}
		p.State = bandav1.PullState(bandav1.PullState_value[state])
		out = append(out, p)
	}
	return out, rows.Err()
}
