// Package store persists threads and their events in Postgres.
package store

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/thread"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("thread not found")

type Thread struct {
	thread.State
	ID            string
	Title         string
	RepoURL       string
	BaseBranch    string
	Branch        string
	Model         string
	Effort        string
	PendingPrompt string
	RunnerToken   string
	Diff          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Store struct{ db *pgxpool.Pool }

// Open connects and applies the schema.
// ponytail: idempotent schema file, switch to versioned migrations once a column changes.
func Open(ctx context.Context, url string) (*Store, error) {
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(ctx, schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

const cols = `id, owner_id, title, repo_url, base_branch, branch, model, effort, status, mode,
	pending_prompt, runner_token, diff, created_at, updated_at`

func scan(row pgx.Row) (Thread, error) {
	var t Thread
	err := row.Scan(&t.ID, &t.OwnerID, &t.Title, &t.RepoURL, &t.BaseBranch, &t.Branch, &t.Model, &t.Effort,
		&t.Status, &t.Mode, &t.PendingPrompt, &t.RunnerToken, &t.Diff, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) Create(ctx context.Context, t Thread) error {
	_, err := s.db.Exec(ctx, `INSERT INTO threads (id, owner_id, title, repo_url, base_branch, branch, model,
		effort, status, mode, pending_prompt, runner_token) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		t.ID, t.OwnerID, t.Title, t.RepoURL, t.BaseBranch, t.Branch, t.Model, t.Effort, t.Status, t.Mode,
		t.PendingPrompt, t.RunnerToken)
	return err
}

func (s *Store) Get(ctx context.Context, id string) (Thread, error) {
	return scan(s.db.QueryRow(ctx, `SELECT `+cols+` FROM threads WHERE id = $1`, id))
}

func (s *Store) List(ctx context.Context, ownerID string) ([]Thread, error) {
	rows, err := s.db.Query(ctx, `SELECT `+cols+` FROM threads WHERE owner_id = $1 ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Update locks the thread, lets fn change it and return events to append,
// then saves both atomically. It returns the stored events with seq and time
// set. If fn returns an error nothing is saved.
func (s *Store) Update(ctx context.Context, id string, fn func(*Thread) ([]*bandav1.Event, error)) (Thread, []*bandav1.Event, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Thread{}, nil, err
	}
	defer tx.Rollback(ctx)

	t, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM threads WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return t, nil, err
	}
	events, err := fn(&t)
	if err != nil {
		return t, nil, err
	}
	var seq int64
	err = tx.QueryRow(ctx, `UPDATE threads SET status=$2, mode=$3, pending_prompt=$4, diff=$5,
		last_seq=last_seq+$6, updated_at=now() WHERE id=$1 RETURNING last_seq-$6, updated_at`,
		t.ID, t.Status, t.Mode, t.PendingPrompt, t.Diff, len(events)).Scan(&seq, &t.UpdatedAt)
	if err != nil {
		return t, nil, err
	}
	for _, e := range events {
		seq++
		e.ThreadId, e.Seq, e.Time = t.ID, seq, timestamppb.New(t.UpdatedAt)
		body, err := protojson.Marshal(e)
		if err != nil {
			return t, nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO events (thread_id, seq, body) VALUES ($1, $2, $3)`, t.ID, seq, body); err != nil {
			return t, nil, err
		}
	}
	return t, events, tx.Commit(ctx)
}

func (s *Store) Events(ctx context.Context, id string, afterSeq int64) ([]*bandav1.Event, error) {
	rows, err := s.db.Query(ctx, `SELECT body FROM events WHERE thread_id = $1 AND seq > $2 ORDER BY seq`, id, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*bandav1.Event
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		e := new(bandav1.Event)
		if err := protojson.Unmarshal(body, e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
