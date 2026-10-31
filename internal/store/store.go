// Package store persists threads and their events in Postgres.
package store

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
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

// The schema: see migrations/0001_baseline.sql for the rules.
//
//go:embed migrations/*.sql
var migrations embed.FS

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
	Profile       string // "engineer", "spec", "devops" or "business"
	PendingPrompt string
	PendingImages []string // attachment ids of pending_prompt's images
	// PendingTeammate: the pending prompt holds a teammate's turn, which runs
	// only in an environment started with the API key (see ready).
	PendingTeammate bool
	RunnerHash      string // see TokenHash
	Diff            string
	DeliveredSeq    int64
	LastSeq         int64      // read-only; Update maintains it
	AutoPush        string     // bandav1.AutoPushState name; see SetAutoPush
	EnvDeleteAt     *time.Time // see SetEnvDeleteAt
	EnvPushed       bool       // all work was on origin when the environment was suspended
	// PermissionMode is "bypass", "ask", "accept_edits" or "plan".
	PermissionMode string
	Permissions    map[string]string // pending requests: id -> tool name
	AllowedTools   []string
	ForkedFrom     string     // the source thread of a fork
	ForkTurn       int        // turns the source had finished then
	RaceID         string     // shared by threads started together
	ParentID       string     // the thread this one was spawned from
	WakeParent     bool       // its reports start a turn of the parent's agent
	WokeParent     *time.Time // when it last did; see SetWokeParent
	Handoffs       int        // accepted handoffs; each starts a new Claude Code session
	CreatedAt      time.Time
	UpdatedAt      time.Time
	EnvStartedAt   time.Time // when provision last started the environment

	// The viewer's own state; only set by List and ListMentioned.
	SnoozedUntil *time.Time
	Settled      bool
	Unread       bool
	// Only set by the lists.
	Activity    *bandav1.TeamActivity
	ParentTitle string
	PullRequest *bandav1.PullRequestSummary
}

// Store is the pool, or inside Update the transaction (see Update).
type Store struct{ db querier }

type querier interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open connects and applies the migrations the database doesn't have yet.
func Open(ctx context.Context, url string) (*Store, error) {
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	sub, _ := fs.Sub(migrations, "migrations")
	if err := migrate(ctx, db, sub); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// migrate applies the *.sql files of fsys in name order, each once, recorded in schema_migrations.
// Skipping the recorded ones without a lock matters: the baseline's ALTERs lock tables even when there
// is nothing to change, which can deadlock with the traffic of another API on a live database.
func migrate(ctx context.Context, db *pgxpool.Pool, fsys fs.FS) error {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	done := map[string]bool{}
	if rows, err := db.Query(ctx, `SELECT name FROM schema_migrations`); err == nil { // missing on a fresh database
		applied, _ := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, n := range applied {
			done[n] = true
		}
	}
	for _, name := range names {
		if done[name] {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err == nil {
			err = applyMigration(ctx, db, name, string(body))
		}
		if err != nil {
			return fmt.Errorf("schema migration %s: %w", name, err)
		}
	}
	return nil
}

// applyMigration applies one migration in a transaction unless it is recorded. The lock keeps APIs
// starting together from applying it twice or deadlocking on it. It can still deadlock with another API's
// traffic, and Postgres may pick it as the victim: try again.
func applyMigration(ctx context.Context, db *pgxpool.Pool, name, body string) error {
	for try := 1; ; try++ {
		err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242);
				CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
				return err
			}
			var done bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM schema_migrations WHERE name = $1)`, name).Scan(&done); err != nil || done {
				return err
			}
			if _, err := tx.Exec(ctx, body); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name)
			return err
		})
		var pe *pgconn.PgError
		if err == nil || try == 5 || !errors.As(err, &pe) || pe.Code != "40P01" {
			return err
		}
	}
}

// cols are the thread columns Get, Update and the lists read; fields says where
// each goes, in the same order. A new column goes into both, nowhere else.
// The last is read-only: the owner's Claude Code auth method (see Settings).
// env_api_key is written by NewRunnerToken only, env_started_at by ResetEnv.
const cols = `id, owner_id, title, repo_url, base_branch, branch, model, effort, status, mode,
	pending_prompt, runner_hash, diff, delivered_seq, last_seq, turns, requests, trusted,
	permission_mode, permissions, allowed_tools, forked_from, fork_turn, race_id, created_at, updated_at,
	auto_push, env_delete_at, pending_images, parent_id, env_pushed, profile, wake_parent, woke_parent_at, handoff_to, handoffs, pending_teammate, env_api_key, env_started_at,
	owner_id IN (SELECT user_id FROM harness_settings WHERE harness = 'claude' AND auth_method = 'api_key')`

func fields(t *Thread) []any {
	return []any{&t.ID, &t.OwnerID, &t.Title, &t.RepoURL, &t.BaseBranch, &t.Branch, &t.Model, &t.Effort, &t.Status, &t.Mode,
		&t.PendingPrompt, &t.RunnerHash, &t.Diff, &t.DeliveredSeq, &t.LastSeq, &t.Turns, &t.Requests, &t.Trusted,
		&t.PermissionMode, &t.Permissions, &t.AllowedTools, &t.ForkedFrom, &t.ForkTurn, &t.RaceID, &t.CreatedAt, &t.UpdatedAt,
		&t.AutoPush, &t.EnvDeleteAt, &t.PendingImages, &t.ParentID, &t.EnvPushed, &t.Profile, &t.WakeParent, &t.WokeParent, &t.HandoffTo, &t.Handoffs, &t.PendingTeammate, &t.EnvAPIKey, &t.EnvStartedAt, &t.APIKey}
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
		effort, status, mode, pending_prompt, runner_hash, permission_mode, diff, forked_from, fork_turn, race_id, parent_id, profile, wake_parent, pending_images)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,coalesce($21, '{}'::text[]))`,
		t.ID, t.OwnerID, t.Title, t.RepoURL, t.BaseBranch, t.Branch, t.Model, t.Effort, t.Status, t.Mode,
		t.PendingPrompt, t.RunnerHash, t.PermissionMode, t.Diff, t.ForkedFrom, t.ForkTurn, t.RaceID, t.ParentID, t.Profile, t.WakeParent, t.PendingImages)
	return err
}

// SetRepository gives a thread without a repository one; Update doesn't
// write these columns.
func (s *Store) SetRepository(ctx context.Context, t Thread) error {
	_, err := s.db.Exec(ctx, `UPDATE threads SET repo_url=$2, base_branch=$3, branch=$4 WHERE id=$1`, t.ID, t.RepoURL, t.BaseBranch, t.Branch)
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

// Races returns the threads of the latest n races, everyone's, newest race
// first and in order of creation within one.
func (s *Store) Races(ctx context.Context, viewerID string, n int) ([]Thread, error) {
	threads, err := s.listWithView(ctx, `t.race_id IN (SELECT race_id FROM threads WHERE race_id <> ''
		GROUP BY race_id ORDER BY max(created_at) DESC LIMIT $2)`, viewerID, n)
	started := map[string]time.Time{}
	for _, t := range threads {
		if s, ok := started[t.RaceID]; !ok || t.CreatedAt.Before(s) {
			started[t.RaceID] = t.CreatedAt
		}
	}
	sort.SliceStable(threads, func(i, j int) bool {
		a, b := threads[i], threads[j]
		if a.RaceID != b.RaceID {
			if sa, sb := started[a.RaceID], started[b.RaceID]; !sa.Equal(sb) {
				return sa.After(sb)
			}
			return a.RaceID < b.RaceID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
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

// LastPost is when a person (not an agent) last posted a message or a turn
// request in any of the threads, or nil.
func (s *Store) LastPost(ctx context.Context, ids ...string) (*time.Time, error) {
	var at *time.Time
	err := s.db.QueryRow(ctx, `SELECT max((body->>'time')::timestamptz) FROM events WHERE thread_id = ANY($1)
		AND kind IN ('message', 'turnRequest') AND body->>'actor' NOT LIKE 'agent:%'`, ids).Scan(&at)
	return at, err
}

// StatusCounts is how many threads have each status.
func (s *Store) StatusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT status, count(*) FROM threads GROUP BY status`)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var status string
	var n int
	_, err = pgx.ForEachRow(rows, []any{&status, &n}, func() error {
		counts[status] = n
		return nil
	})
	return counts, err
}

// SetWokeParent records that the sub-thread just started a turn of its parent's agent.
func (s *Store) SetWokeParent(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE threads SET woke_parent_at = now() WHERE id = $1`, id)
	return err
}

// Pushed reports whether the thread's branch was ever pushed to origin.
func (s *Store) Pushed(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE thread_id = $1 AND kind = 'autoPush'
		AND body @> '{"autoPush": {"branchPushed": true}}')`, id).Scan(&ok)
	return ok, err
}

// ListMentioned returns other users' threads in which the viewer has an
// unanswered mention, or that are offered to them.
func (s *Store) ListMentioned(ctx context.Context, viewerID string) ([]Thread, error) {
	return s.listWithView(ctx, `t.owner_id <> $1 AND (t.handoff_to = $1 OR EXISTS (SELECT 1 FROM mentions m
		WHERE m.thread_id = t.id AND m.user_id = $1 AND m.answered_at IS NULL))`, viewerID)
}

// Threads returns the given threads with viewerID's view, without diffs.
func (s *Store) Threads(ctx context.Context, ids []string, viewerID string) ([]Thread, error) {
	return s.listWithView(ctx, `t.id = ANY($2)`, viewerID, ids)
}

// Decisions returns the pending requests viewerID can answer, oldest first:
// permission requests in their threads or in threads that trust teammates
// (plans only in their own), and turn requests in their threads.
func (s *Store) Decisions(ctx context.Context, viewerID string) ([]*bandav1.Event, error) {
	return s.events(ctx, `SELECT e.body FROM threads t JOIN events e ON e.thread_id = t.id
		WHERE ((t.permissions <> '{}' AND (t.owner_id = $1 OR t.trusted)) OR (t.requests > 0 AND t.owner_id = $1))
		AND e.kind IN ('permissionRequested', 'turnRequest') AND CASE e.kind
		WHEN 'permissionRequested' THEN t.permissions ? (e.body->'permissionRequested'->>'id')
			AND (t.owner_id = $1 OR e.body->'permissionRequested'->>'toolName' <> 'ExitPlanMode')
		ELSE t.owner_id = $1 AND NOT EXISTS (SELECT 1 FROM events r WHERE r.thread_id = t.id AND r.kind = 'turnRequestResolved'
			AND r.body->'turnRequestResolved'->>'requestSeq' = e.seq::text) END
		ORDER BY (e.body->>'time')::timestamptz, e.seq`, viewerID)
}

// listWithView lists threads matching where, without diffs (they are large);
// $1 is always the viewer.
func (s *Store) listWithView(ctx context.Context, where, viewerID string, args ...any) ([]Thread, error) {
	rows, err := s.db.Query(ctx, `SELECT `+listCols+`,
		v.snoozed_until, v.settled_at IS NOT NULL, t.last_seq > coalesce(v.seen_seq, 0), t.activity,
		coalesce((SELECT p.title FROM threads p WHERE p.id = t.parent_id), ''),
		(SELECT pr.data->'summary' FROM pull_requests pr WHERE pr.thread_id = t.id)
		FROM threads t LEFT JOIN thread_views v ON v.thread_id = t.id AND v.user_id = $1
		WHERE `+where+` ORDER BY t.updated_at DESC`, append([]any{viewerID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		var activity, pr []byte
		if err := rows.Scan(append(fields(&t), &t.SnoozedUntil, &t.Settled, &t.Unread, &activity, &t.ParentTitle, &pr)...); err != nil {
			return nil, err
		}
		if activity != nil {
			t.Activity = new(bandav1.TeamActivity)
			if err := protojson.Unmarshal(activity, t.Activity); err != nil {
				return nil, err
			}
		}
		if pr != nil {
			t.PullRequest = new(bandav1.PullRequestSummary)
			if err := protojson.Unmarshal(pr, t.PullRequest); err != nil {
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
	var summary *string // likewise
	finished := false
	for _, e := range events {
		if a := Activity(e); a != nil {
			a.Time = timestamppb.Now()
			if activity, err = protojson.Marshal(a); err != nil {
				return t, nil, err
			}
		}
		switch k := e.Kind.(type) {
		case *bandav1.Event_AgentText:
			if s := snippet(k.AgentText.Text); s != "" {
				summary = &s
			}
		case *bandav1.Event_TurnEnded:
			finished = !k.TurnEnded.IsError
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
		last_seq=last_seq+$6, activity=coalesce($16, activity), pending_images=coalesce($17, '{}'::text[]), env_pushed=$18,
		owner_id=$19, handoff_to=$20, handoffs=$21, profile=$22, summary=coalesce($23, summary), pending_teammate=$25, title=$26,
		finished_at=CASE WHEN $24 THEN now() ELSE finished_at END, updated_at=now() WHERE id=$1 RETURNING last_seq-$6, updated_at`,
		t.ID, t.Status, t.Mode, t.PendingPrompt, t.Diff, len(events), t.DeliveredSeq, t.Turns, t.Requests, t.Trusted, t.Model, t.Effort,
		t.PermissionMode, perms, tools, activity, t.PendingImages, t.EnvPushed, t.OwnerID, t.HandoffTo, t.Handoffs, t.Profile, summary, finished, t.PendingTeammate, t.Title).Scan(&seq, &t.UpdatedAt)
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

// LatestOf is Before of the given kinds only.
func (s *Store) LatestOf(ctx context.Context, id string, beforeSeq int64, limit int, kinds ...string) ([]*bandav1.Event, error) {
	return s.events(ctx, `SELECT body FROM (SELECT seq, body FROM events WHERE thread_id = $1 AND seq < $2 AND kind = ANY($4)
		ORDER BY seq DESC LIMIT $3) e ORDER BY seq`, id, beforeSeq, limit, kinds)
}

// Count is the number of the thread's events of a kind.
func (s *Store) Count(ctx context.Context, id, kind string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM events WHERE thread_id = $1 AND kind = $2`, id, kind).Scan(&n)
	return n, err
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

// ResetEnv forgets a deletion and what was pushed, and notes the time: the environment is (re)started.
func (s *Store) ResetEnv(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE threads SET env_delete_at = NULL, env_pushed = false, env_deleted = false, env_started_at = now() WHERE id = $1`, id)
	return err
}

// IdlePushed returns the threads suspended with all their work on origin,
// without a scheduled deletion and unchanged since before.
func (s *Store) IdlePushed(ctx context.Context, before time.Time) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM threads
		WHERE status = 'suspended' AND env_pushed AND repo_url <> '' AND env_delete_at IS NULL AND updated_at < $1`, before)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Starting returns the threads whose environment is starting.
func (s *Store) Starting(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM threads WHERE status = 'starting'`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
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
