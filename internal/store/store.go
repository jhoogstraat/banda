// Package store persists threads and their events in Postgres.
package store

import (
	"bytes"
	"cmp"
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
	DeliveredSeq  int64
	// PermissionMode is "bypass", "ask", "accept_edits" or "plan".
	PermissionMode string
	Permissions    map[string]string // pending requests: id -> tool name
	AllowedTools   []string
	LastSeq        int64 // read-only; Update maintains it
	CreatedAt      time.Time
	UpdatedAt      time.Time

	// The viewer's own state; only set by List and ListMentioned.
	SnoozedUntil *time.Time
	Settled      bool
	Unread       bool
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
	pending_prompt, runner_token, diff, delivered_seq, last_seq, turns, requests, trusted, permission_mode, permissions, allowed_tools, created_at, updated_at`

func scan(row pgx.Row) (Thread, error) {
	var t Thread
	err := row.Scan(&t.ID, &t.OwnerID, &t.Title, &t.RepoURL, &t.BaseBranch, &t.Branch, &t.Model, &t.Effort,
		&t.Status, &t.Mode, &t.PendingPrompt, &t.RunnerToken, &t.Diff, &t.DeliveredSeq, &t.LastSeq, &t.Turns, &t.Requests, &t.Trusted, &t.PermissionMode, &t.Permissions, &t.AllowedTools, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) Create(ctx context.Context, t Thread) error {
	_, err := s.db.Exec(ctx, `INSERT INTO threads (id, owner_id, title, repo_url, base_branch, branch, model,
		effort, status, mode, pending_prompt, runner_token, permission_mode) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		t.ID, t.OwnerID, t.Title, t.RepoURL, t.BaseBranch, t.Branch, t.Model, t.Effort, t.Status, t.Mode,
		t.PendingPrompt, t.RunnerToken, cmp.Or(t.PermissionMode, "bypass"))
	return err
}

func (s *Store) Get(ctx context.Context, id string) (Thread, error) {
	return scan(s.db.QueryRow(ctx, `SELECT `+cols+` FROM threads WHERE id = $1`, id))
}

// List returns ownerID's threads with viewerID's snooze/settle state.
func (s *Store) List(ctx context.Context, ownerID, viewerID string) ([]Thread, error) {
	return s.listWithView(ctx, `t.owner_id = $2`, viewerID, ownerID)
}

// Recent returns the n most recently updated threads of ownerID (of all
// owners if empty) with viewerID's snooze/settle state.
func (s *Store) Recent(ctx context.Context, ownerID, viewerID string, n int) ([]Thread, error) {
	return s.listWithView(ctx, `t.id IN (SELECT id FROM threads WHERE $2 = '' OR owner_id = $2
		ORDER BY updated_at DESC LIMIT $3)`, viewerID, ownerID, n)
}

// GetFor returns a thread with viewerID's snooze/settle state.
func (s *Store) GetFor(ctx context.Context, id, viewerID string) (Thread, error) {
	threads, err := s.listWithView(ctx, `t.id = $2`, viewerID, id)
	if err != nil {
		return Thread{}, err
	}
	if len(threads) == 0 {
		return Thread{}, ErrNotFound
	}
	return threads[0], nil
}

// ListMentioned returns other users' threads in which the viewer has an
// unanswered mention.
func (s *Store) ListMentioned(ctx context.Context, viewerID string) ([]Thread, error) {
	return s.listWithView(ctx, `t.owner_id <> $1 AND EXISTS (SELECT 1 FROM mentions m
		WHERE m.thread_id = t.id AND m.user_id = $1 AND m.answered_at IS NULL)`, viewerID)
}

// listWithView lists threads matching where; $1 is always the viewer.
func (s *Store) listWithView(ctx context.Context, where, viewerID string, args ...any) ([]Thread, error) {
	rows, err := s.db.Query(ctx, `SELECT t.id, t.owner_id, t.title, t.repo_url, t.base_branch, t.branch, t.model,
		t.effort, t.status, t.mode, t.pending_prompt, t.runner_token, t.diff, t.delivered_seq, t.last_seq, t.turns, t.requests, t.trusted, t.permission_mode, t.permissions, t.allowed_tools, t.created_at,
		t.updated_at, v.snoozed_until, v.settled_at IS NOT NULL, t.last_seq > coalesce(v.seen_seq, 0)
		FROM threads t LEFT JOIN thread_views v ON v.thread_id = t.id AND v.user_id = $1
		WHERE `+where+` ORDER BY t.updated_at DESC`, append([]any{viewerID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.Title, &t.RepoURL, &t.BaseBranch, &t.Branch, &t.Model, &t.Effort,
			&t.Status, &t.Mode, &t.PendingPrompt, &t.RunnerToken, &t.Diff, &t.DeliveredSeq, &t.LastSeq, &t.Turns, &t.Requests, &t.Trusted, &t.PermissionMode, &t.Permissions, &t.AllowedTools, &t.CreatedAt, &t.UpdatedAt,
			&t.SnoozedUntil, &t.Settled, &t.Unread); err != nil {
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
	perms, tools := t.Permissions, t.AllowedTools
	if perms == nil { // the columns are NOT NULL
		perms = map[string]string{}
	}
	if tools == nil {
		tools = []string{}
	}
	var seq int64
	err = tx.QueryRow(ctx, `UPDATE threads SET status=$2, mode=$3, pending_prompt=$4, diff=$5, delivered_seq=$7,
		turns=$8, requests=$9, trusted=$10, model=$11, effort=$12, permission_mode=$13, permissions=$14, allowed_tools=$15,
		last_seq=last_seq+$6, updated_at=now() WHERE id=$1 RETURNING last_seq-$6, updated_at`,
		t.ID, t.Status, t.Mode, t.PendingPrompt, t.Diff, len(events), t.DeliveredSeq, t.Turns, t.Requests, t.Trusted, t.Model, t.Effort,
		t.PermissionMode, perms, tools).Scan(&seq, &t.UpdatedAt)
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
		body = bytes.ReplaceAll(body, []byte(`\u0000`), []byte(`\ufffd`)) // jsonb rejects NUL
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
