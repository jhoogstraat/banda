package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDB makes an empty database on TEST_DATABASE_URL (default: the dev Postgres) and drops it after the
// test; it skips the test without Postgres.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://banda:banda@localhost:5432/banda"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err == nil {
		err = admin.Ping(ctx)
	}
	if err != nil {
		t.Skipf("no Postgres at %s: %v", url, err)
	}
	name := fmt.Sprintf("banda_migrate_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := pgxpool.New(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	return db
}

func embedded(t *testing.T) fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// with is the real migrations plus extra ones.
func with(t *testing.T, extra map[string]string) fs.FS {
	m := fstest.MapFS{}
	names, _ := fs.Glob(embedded(t), "*.sql")
	for _, n := range names {
		b, _ := fs.ReadFile(embedded(t), n)
		m[n] = &fstest.MapFile{Data: b}
	}
	for n, body := range extra {
		m[n] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func applied(t *testing.T, db *pgxpool.Pool) string {
	var s string
	if err := db.QueryRow(context.Background(), `SELECT string_agg(name, ',' ORDER BY name) FROM schema_migrations`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrateFreshAndAgain(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	fsys := with(t, map[string]string{"0002_note.sql": `ALTER TABLE threads ADD COLUMN note text NOT NULL DEFAULT ''`})
	if err := migrate(ctx, db, fsys); err != nil {
		t.Fatal(err)
	}
	// 0002 isn't idempotent: running everything again must skip it.
	if err := migrate(ctx, db, fsys); err != nil {
		t.Fatal(err)
	}
	if got := applied(t, db); got != "0001_baseline.sql,0002_note.sql" {
		t.Fatalf("applied %s", got)
	}
}

// A database of an earlier idempotent schema (its first version here, with a plain runner token)
// recorded in schema_applied comes up to date, its data fixed.
func TestMigrateExistingDatabase(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	if _, err := db.Exec(ctx, `
		CREATE TABLE threads (id text PRIMARY KEY, owner_id text NOT NULL, title text NOT NULL, repo_url text NOT NULL,
			base_branch text NOT NULL, branch text NOT NULL, model text NOT NULL, effort text NOT NULL, status text NOT NULL,
			mode text NOT NULL, pending_prompt text NOT NULL DEFAULT '', runner_token text NOT NULL, diff text NOT NULL DEFAULT '',
			last_seq bigint NOT NULL DEFAULT 0, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now());
		INSERT INTO threads VALUES ('t1', 'alice', 'x', '', 'main', 'b', 'm', 'e', 'IDLE', 'normal', '', 'tok');
		CREATE TABLE schema_applied (hash text NOT NULL);
		INSERT INTO schema_applied VALUES ('old')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, embedded(t)); err != nil {
		t.Fatal(err)
	}
	var hash, profile string
	if err := db.QueryRow(ctx, `SELECT runner_hash, profile FROM threads`).Scan(&hash, &profile); err != nil {
		t.Fatal(err)
	}
	if hash != fmt.Sprintf("%x", sha256.Sum256([]byte("tok"))) || profile != "engineer" {
		t.Fatalf("runner_hash %q, profile %q", hash, profile)
	}
	var old *string
	if db.QueryRow(ctx, `SELECT to_regclass('schema_applied')::text`).Scan(&old); old != nil {
		t.Fatal("schema_applied is left")
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	db, ctx := testDB(t), context.Background()
	fsys := with(t, map[string]string{
		"0002_bad.sql":  `CREATE TABLE half (x int); SELECT 1/0`,
		"0003_next.sql": `CREATE TABLE next (x int)`,
	})
	err := migrate(ctx, db, fsys)
	if err == nil || !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Fatalf("error %v", err)
	}
	var half, next *string
	db.QueryRow(ctx, `SELECT to_regclass('half')::text, to_regclass('next')::text`).Scan(&half, &next)
	if half != nil || next != nil {
		t.Fatal("the failed migration, or one after it, left a table")
	}
	if got := applied(t, db); got != "0001_baseline.sql" {
		t.Fatalf("applied %s", got)
	}
}

func TestMigrateConcurrentStarts(t *testing.T) {
	db := testDB(t)
	fsys := with(t, map[string]string{"0002_once.sql": `CREATE TABLE once (x int)`}) // fails if applied twice
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = migrate(context.Background(), db, fsys)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := applied(t, db); got != "0001_baseline.sql,0002_once.sql" {
		t.Fatalf("applied %s", got)
	}
}
