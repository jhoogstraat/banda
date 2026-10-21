// Package store persists threads and their events in Postgres.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	PendingImages []string // attachment ids of pending_prompt's images
	RunnerHash    string   // see TokenHash
	Diff          string
	DeliveredSeq  int64
	LastSeq       int64      // read-only; Update maintains it
	AutoPush      string     // bandav1.AutoPushState name; see SetAutoPush
	EnvDeleteAt   *time.Time // see SetEnvDeleteAt
	EnvPushed     bool       // all work was on origin when the environment was suspended
	// PermissionMode is "bypass", "ask", "accept_edits" or "plan".
	PermissionMode string
	Permissions    map[string]string // pending requests: id -> tool name
	AllowedTools   []string
	ForkedFrom     string // the source thread of a fork
	ForkTurn       int    // turns the source had finished then
	RaceID         string // shared by threads started together
	ParentID       string // the thread this one was spawned from
	CreatedAt      time.Time
	UpdatedAt      time.Time

	// The viewer's own state; only set by List and ListMentioned.
	SnoozedUntil *time.Time
	Settled      bool
	Unread       bool
	// Only set by the lists.
	Activity    *bandav1.TeamActivity
	ParentTitle string
}

// Store is the pool, or inside Update the transaction (see Update).
type Store struct{ db querier }

type querier interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open connects and applies the schema unless the database has it already.
// ponytail: idempotent schema file, switch to versioned migrations once a column changes.
func Open(ctx context.Context, url string) (*Store, error) {
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	// Its ALTERs lock tables even when there is nothing to change, so applying it to a live database can
	// deadlock with the traffic of another API on it: skip it when it's current.
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(schema)))
	var applied string
	if db.QueryRow(ctx, `SELECT hash FROM schema_applied`).Scan(&applied); applied != sum {
		err = applySchema(ctx, db, sum)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// applySchema applies the schema and records its hash. One statement list is one transaction; the lock
// keeps APIs starting together from deadlocking on it. It can still deadlock with another API's traffic,
// and Postgres may pick it as the victim: try again.
func applySchema(ctx context.Context, db *pgxpool.Pool, sum string) error {
	for try := 1; ; try++ {
		_, err := db.Exec(ctx, "SELECT pg_advisory_xact_lock(4242);\n"+schema+`
			CREATE TABLE IF NOT EXISTS schema_applied (hash text NOT NULL);
			DELETE FROM schema_applied;
			INSERT INTO schema_applied VALUES ('`+sum+`');`)
		var pe *pgconn.PgError
		if err == nil || try == 5 || !errors.As(err, &pe) || pe.Code != "40P01" {
			return err
		}
	}
}

// cols are the thread columns Get, Update and the lists read; fields says where
// each goes, in the same order. A new column goes into both, nowhere else.
const cols = `id, owner_id, title, repo_url, base_branch, branch, model, effort, status, mode,
	pending_prompt, runner_hash, diff, delivered_seq, last_seq, turns, requests, trusted,
	permission_mode, permissions, allowed_tools, forked_from, fork_turn, race_id, created_at, updated_at,
	auto_push, env_delete_at, pending_images, parent_id, env_pushed`

func fields(t *Thread) []any {
	return []any{&t.ID, &t.OwnerID, &t.Title, &t.RepoURL, &t.BaseBranch, &t.Branch, &t.Model, &t.Effort, &t.Status, &t.Mode,
		&t.PendingPrompt, &t.RunnerHash, &t.Diff, &t.DeliveredSeq, &t.LastSeq, &t.Turns, &t.Requests, &t.Trusted,
		&t.PermissionMode, &t.Permissions, &t.AllowedTools, &t.ForkedFrom, &t.ForkTurn, &t.RaceID, &t.CreatedAt, &t.UpdatedAt,
		&t.AutoPush, &t.EnvDeleteAt, &t.PendingImages, &t.ParentID, &t.EnvPushed}
}

// listCols are cols of threads t for listWithView, without the diff (large).
var listCols = func() string {
	var out []string
	for _, c := range strings.Split(cols, ",") {
		c = "t." + strings.TrimSpace(c)
		if c == "t.diff" {
			c = "''"
		}
		out = append(out, c)
	}
	return strings.Join(out, ", ")
}()

func scan(row pgx.Row) (Thread, error) {
	var t Thread
	err := row.Scan(fields(&t)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) Create(ctx context.Context, t Thread) error {
	_, err := s.db.Exec(ctx, `INSERT INTO threads (id, owner_id, title, repo_url, base_branch, branch, model,
		effort, status, mode, pending_prompt, runner_hash, permission_mode, diff, forked_from, fork_turn, race_id, parent_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		t.ID, t.OwnerID, t.Title, t.RepoURL, t.BaseBranch, t.Branch, t.Model, t.Effort, t.Status, t.Mode,
		t.PendingPrompt, t.RunnerHash, t.PermissionMode, t.Diff, t.ForkedFrom, t.ForkTurn, t.RaceID, t.ParentID)
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

// Compared returns the threads of a race and the given threads, with
// viewerID's snooze/settle state and their diffs.
func (s *Store) Compared(ctx context.Context, raceID string, ids []string, viewerID string) ([]Thread, error) {
	threads, err := s.listWithView(ctx, `(t.race_id = $2 AND $2 <> '') OR t.id = ANY($3)`, viewerID, raceID, ids)
	for i := range threads {
		if err == nil {
			err = s.db.QueryRow(ctx, `SELECT diff FROM threads WHERE id = $1`, threads[i].ID).Scan(&threads[i].Diff)
		}
	}
	return threads, err
}

// TurnTotals sums the finished turns of each thread: count, cost, duration.
type TurnTotals struct {
	Turns      int
	CostUSD    float64
	DurationMS int64
}

func (s *Store) TurnTotals(ctx context.Context, ids []string) (map[string]TurnTotals, error) {
	rows, err := s.db.Query(ctx, `SELECT thread_id, count(*), coalesce(sum((body->'turnEnded'->>'costUsd')::float8), 0),
		coalesce(sum((body->'turnEnded'->>'durationMs')::bigint), 0)
		FROM events WHERE thread_id = ANY($1) AND kind = 'turnEnded' GROUP BY thread_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TurnTotals{}
	for rows.Next() {
		var id string
		var t TurnTotals
		if err := rows.Scan(&id, &t.Turns, &t.CostUSD, &t.DurationMS); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// Children returns the threads spawned from parentID with viewerID's view.
func (s *Store) Children(ctx context.Context, parentID, viewerID string) ([]Thread, error) {
	return s.listWithView(ctx, `t.parent_id = $2`, viewerID, parentID)
}

// Depth is how many ancestors the thread has (parent_id links).
func (s *Store) Depth(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `WITH RECURSIVE up(id, parent_id) AS (
		SELECT id, parent_id FROM threads WHERE id = $1
		UNION ALL SELECT t.id, t.parent_id FROM threads t JOIN up ON t.id = up.parent_id)
		SELECT count(*) - 1 FROM up`, id).Scan(&n)
	return n, err
}

// AgentSpawned counts the owner's threads that an agent started (their first
// message is by "agent:<thread>") and that are not suspended.
func (s *Store) AgentSpawned(ctx context.Context, owner string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM threads t JOIN events e ON e.thread_id = t.id AND e.seq = 1
		WHERE t.owner_id = $1 AND t.status <> 'suspended' AND e.body->>'actor' LIKE 'agent:%'`, owner).Scan(&n)
	return n, err
}

// LastAgentText is the thread's latest agent text, or "".
func (s *Store) LastAgentText(ctx context.Context, id string) (string, error) {
	var text string
	err := s.db.QueryRow(ctx, `SELECT coalesce(body->'agentText'->>'text', '') FROM events
		WHERE thread_id = $1 AND kind = 'agentText' ORDER BY seq DESC LIMIT 1`, id).Scan(&text)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return text, err
}

// Pushed reports whether the thread's branch was ever pushed to origin.
func (s *Store) Pushed(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE thread_id = $1 AND kind = 'autoPush'
		AND body @> '{"autoPush": {"branchPushed": true}}')`, id).Scan(&ok)
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
	rows, err := s.db.Query(ctx, `SELECT `+listCols+`,
		v.snoozed_until, v.settled_at IS NOT NULL, t.last_seq > coalesce(v.seen_seq, 0), t.activity,
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
		if err := rows.Scan(append(fields(&t), &t.SnoozedUntil, &t.Settled, &t.Unread, &activity, &t.ParentTitle)...); err != nil {
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
// set. If fn returns an error nothing is saved. fn reads through tx, the
// transaction: a query on the pool would wait for a connection while this
// one holds the thread's lock, and with the pool busy with others waiting
// for that lock, nothing moves.
func (s *Store) Update(ctx context.Context, id string, fn func(tx *Store, t *Thread) ([]*bandav1.Event, error)) (Thread, []*bandav1.Event, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Thread{}, nil, err
	}
	defer tx.Rollback(ctx)

	t, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM threads WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return t, nil, err
	}
	events, err := fn(&Store{tx}, &t)
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
		last_seq=last_seq+$6, activity=coalesce($16, activity), pending_images=coalesce($17, '{}'::text[]), env_pushed=$18, updated_at=now() WHERE id=$1 RETURNING last_seq-$6, updated_at`,
		t.ID, t.Status, t.Mode, t.PendingPrompt, t.Diff, len(events), t.DeliveredSeq, t.Turns, t.Requests, t.Trusted, t.Model, t.Effort,
		t.PermissionMode, perms, tools, activity, t.PendingImages, t.EnvPushed).Scan(&seq, &t.UpdatedAt)
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

// EventsOf is Events of the given kinds only (Event.kind oneof JSON names, e.g. "message").
func (s *Store) EventsOf(ctx context.Context, id string, afterSeq int64, kinds ...string) ([]*bandav1.Event, error) {
	return s.events(ctx, `SELECT body FROM events WHERE thread_id = $1 AND seq > $2 AND kind = ANY($3) ORDER BY seq`, id, afterSeq, kinds)
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

func (s *Store) SetAutoPush(ctx context.Context, id, state string) error {
	_, err := s.db.Exec(ctx, `UPDATE threads SET auto_push = $2 WHERE id = $1`, id, state)
	return err
}

// SetEnvDeleteAt records when the environment is deleted; nil when it isn't.
func (s *Store) SetEnvDeleteAt(ctx context.Context, id string, at *time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE threads SET env_delete_at = $2 WHERE id = $1`, id, at)
	return err
}

// ResetEnv forgets a deletion and what was pushed: the environment is (re)started.
func (s *Store) ResetEnv(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE threads SET env_delete_at = NULL, env_pushed = false, env_deleted = false WHERE id = $1`, id)
	return err
}

// ClaimDeleted returns the threads whose environment was deleted since the
// last call, by any API.
func (s *Store) ClaimDeleted(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `UPDATE threads SET env_deleted = true WHERE env_delete_at <= now() AND NOT env_deleted RETURNING id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
