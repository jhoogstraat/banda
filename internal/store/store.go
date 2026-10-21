// Package store persists threads and their events in Postgres.
package store

import (
	"bytes"
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
	ParentID      string
	LastSeq       int64 // read-only; Update maintains it
	CreatedAt     time.Time
	UpdatedAt     time.Time

	// The viewer's own state; only set by List and ListMentioned.
	SnoozedUntil *time.Time
	Settled      bool
	Unread       bool
	ParentTitle  string
	// Only set by the lists.
	Activity *bandav1.TeamActivity
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
	pending_prompt, runner_token, diff, delivered_seq, last_seq, turns, requests, trusted, parent_id, created_at, updated_at`

func scan(row pgx.Row) (Thread, error) {
	var t Thread
	err := row.Scan(&t.ID, &t.OwnerID, &t.Title, &t.RepoURL, &t.BaseBranch, &t.Branch, &t.Model, &t.Effort,
		&t.Status, &t.Mode, &t.PendingPrompt, &t.RunnerToken, &t.Diff, &t.DeliveredSeq, &t.LastSeq, &t.Turns, &t.Requests, &t.Trusted, &t.ParentID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) Create(ctx context.Context, t Thread) error {
	_, err := s.db.Exec(ctx, `INSERT INTO threads (id, owner_id, title, repo_url, base_branch, branch, model,
		effort, status, mode, pending_prompt, runner_token, parent_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		t.ID, t.OwnerID, t.Title, t.RepoURL, t.BaseBranch, t.Branch, t.Model, t.Effort, t.Status, t.Mode,
		t.PendingPrompt, t.RunnerToken, t.ParentID)
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
	t := threads[0]
	return t, s.db.QueryRow(ctx, `SELECT diff FROM threads WHERE id = $1`, id).Scan(&t.Diff)
}

// Team returns everyone's threads updated after since that their owners
// haven't settled, with viewerID's view.
func (s *Store) Team(ctx context.Context, viewerID string, since time.Time) ([]Thread, error) {
	return s.listWithView(ctx, `t.updated_at > $2 AND NOT EXISTS (SELECT 1 FROM thread_views o
		WHERE o.thread_id = t.id AND o.user_id = t.owner_id AND o.settled_at IS NOT NULL)`, viewerID, since)
}

// Children returns the threads spawned from parentID with viewerID's view.
func (s *Store) Children(ctx context.Context, parentID, viewerID string) ([]Thread, error) {
	return s.listWithView(ctx, `t.parent_id = $2`, viewerID, parentID)
}

// LastAgentText is the thread's latest agent text, or "".
func (s *Store) LastAgentText(ctx context.Context, id string) (string, error) {
	var text string
	err := s.db.QueryRow(ctx, `SELECT coalesce(body->'agentText'->>'text', '') FROM events
		WHERE thread_id = $1 AND body ? 'agentText' ORDER BY seq DESC LIMIT 1`, id).Scan(&text)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return text, err
}

// Pushed reports whether the thread's branch was ever pushed to origin (the
// runner's "Pushed `<branch>` ..." notice).
func (s *Store) Pushed(ctx context.Context, t Thread) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE thread_id = $1
		AND starts_with(body->'notice'->>'text', $2))`, t.ID, "Pushed `"+t.Branch+"`").Scan(&ok)
	return ok, err
}

// ListMentioned returns other users' threads in which the viewer has an
// unanswered mention.
func (s *Store) ListMentioned(ctx context.Context, viewerID string) ([]Thread, error) {
	return s.listWithView(ctx, `t.owner_id <> $1 AND EXISTS (SELECT 1 FROM mentions m
		WHERE m.thread_id = t.id AND m.user_id = $1 AND m.answered_at IS NULL)`, viewerID)
}

// listWithView lists threads matching where, without diffs (they are large);
// $1 is always the viewer.
func (s *Store) listWithView(ctx context.Context, where, viewerID string, args ...any) ([]Thread, error) {
	rows, err := s.db.Query(ctx, `SELECT t.id, t.owner_id, t.title, t.repo_url, t.base_branch, t.branch, t.model,
		t.effort, t.status, t.mode, t.pending_prompt, t.runner_token, '', t.delivered_seq, t.last_seq, t.turns, t.requests, t.trusted, t.parent_id, t.created_at,
		t.updated_at, v.snoozed_until, v.settled_at IS NOT NULL, t.last_seq > coalesce(v.seen_seq, 0), t.activity,
		coalesce((SELECT p.title FROM threads p WHERE p.id = t.parent_id), '')
		FROM threads t LEFT JOIN thread_views v ON v.thread_id = t.id AND v.user_id = $1
		WHERE `+where+` ORDER BY t.updated_at DESC`, append([]any{viewerID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		var activity []byte
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.Title, &t.RepoURL, &t.BaseBranch, &t.Branch, &t.Model, &t.Effort,
			&t.Status, &t.Mode, &t.PendingPrompt, &t.RunnerToken, &t.Diff, &t.DeliveredSeq, &t.LastSeq, &t.Turns, &t.Requests, &t.Trusted, &t.ParentID, &t.CreatedAt, &t.UpdatedAt,
			&t.SnoozedUntil, &t.Settled, &t.Unread, &activity, &t.ParentTitle); err != nil {
			return nil, err
		}
		if activity != nil {
			t.Activity = new(bandav1.TeamActivity)
			if err := protojson.Unmarshal(activity, t.Activity); err != nil {
				return nil, err
			}
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
	var activity []byte // nil keeps the previous one
	for _, e := range events {
		if a := Activity(e); a != nil {
			a.Time = timestamppb.Now()
			if activity, err = protojson.Marshal(a); err != nil {
				return t, nil, err
			}
		}
	}
	var seq int64
	err = tx.QueryRow(ctx, `UPDATE threads SET status=$2, mode=$3, pending_prompt=$4, diff=$5, delivered_seq=$7,
		turns=$8, requests=$9, trusted=$10, model=$11, effort=$12, last_seq=last_seq+$6, activity=coalesce($13, activity), updated_at=now() WHERE id=$1 RETURNING last_seq-$6, updated_at`,
		t.ID, t.Status, t.Mode, t.PendingPrompt, t.Diff, len(events), t.DeliveredSeq, t.Turns, t.Requests, t.Trusted, t.Model, t.Effort, activity).Scan(&seq, &t.UpdatedAt)
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
	return s.events(ctx, `SELECT body FROM events WHERE thread_id = $1 AND seq > $2 ORDER BY seq`, id, afterSeq)
}

// Before returns the newest limit events with seq < beforeSeq, in seq order.
func (s *Store) Before(ctx context.Context, id string, beforeSeq int64, limit int) ([]*bandav1.Event, error) {
	return s.events(ctx, `SELECT body FROM (SELECT seq, body FROM events WHERE thread_id = $1 AND seq < $2
		ORDER BY seq DESC LIMIT $3) e ORDER BY seq`, id, beforeSeq, limit)
}

// Plan returns the latest TodoWrite call of the agent itself (not of a subagent) with seq < beforeSeq, or nil.
func (s *Store) Plan(ctx context.Context, id string, beforeSeq int64) (*bandav1.Event, error) {
	events, err := s.events(ctx, `SELECT body FROM events WHERE thread_id = $1 AND seq < $2
		AND body->'toolCall'->>'name' = 'TodoWrite' AND NOT body->'toolCall' ? 'parentToolCallId'
		ORDER BY seq DESC LIMIT 1`, id, beforeSeq)
	if len(events) == 0 {
		return nil, err
	}
	return events[0], err
}

func (s *Store) events(ctx context.Context, query string, args ...any) ([]*bandav1.Event, error) {
	rows, err := s.db.Query(ctx, query, args...)
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
