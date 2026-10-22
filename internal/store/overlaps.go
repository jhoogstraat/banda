package store

import (
	"context"
	"regexp"
	"slices"
	"time"
)

// diffPath is a file's path in a unified diff header, as web/src/lib/diff.ts reads it.
var diffPath = regexp.MustCompile(`(?m)^diff --git a/.* b/(.*)$`)

// DiffPaths are the files a unified diff changes, sorted.
func DiffPaths(diff string) []string {
	var paths []string
	for _, m := range diffPath.FindAllStringSubmatch(diff, -1) {
		paths = append(paths, m[1])
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// SetPaths records the files the thread's diff changes; call it in the Update that stores the diff.
func (s *Store) SetPaths(ctx context.Context, id string, paths []string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM thread_paths WHERE thread_id = $1`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `INSERT INTO thread_paths (thread_id, path) SELECT $1, unnest($2::text[])`, id, paths)
	return err
}

// Overlap is another thread whose diff changes some of the same files.
type Overlap struct {
	Of      string // the thread it overlaps with
	ID      string
	Title   string
	OwnerID string
	Status  string
	Paths   []string // sorted
}

// Overlaps returns, for each of the threads ids, the other threads on its repository and base branch that
// change some of the same files. Both must have changed since since and not be settled by their owners;
// the threads of one race or fork, and a fork and its source, are alternatives, not overlaps.
func (s *Store) Overlaps(ctx context.Context, ids []string, since time.Time) ([]Overlap, error) {
	rows, err := s.db.Query(ctx, `SELECT me.id, o.id, o.title, o.owner_id, o.status, array_agg(p.path ORDER BY p.path)
		FROM threads me
		JOIN thread_paths mp ON mp.thread_id = me.id
		JOIN thread_paths p ON p.path = mp.path AND p.thread_id <> me.id
		JOIN threads o ON o.id = p.thread_id
		WHERE me.id = ANY($1) AND me.repo_url <> '' AND o.repo_url = me.repo_url AND o.base_branch = me.base_branch
			AND me.updated_at > $2 AND o.updated_at > $2
			AND NOT EXISTS (SELECT 1 FROM thread_views v WHERE v.user_id = me.owner_id AND v.thread_id = me.id AND v.settled_at IS NOT NULL)
			AND NOT EXISTS (SELECT 1 FROM thread_views v WHERE v.user_id = o.owner_id AND v.thread_id = o.id AND v.settled_at IS NOT NULL)
			AND NOT (me.race_id <> '' AND me.race_id = o.race_id) AND me.forked_from <> o.id AND o.forked_from <> me.id
		GROUP BY me.id, o.id ORDER BY o.updated_at DESC`, ids, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Overlap
	for rows.Next() {
		var o Overlap
		if err := rows.Scan(&o.Of, &o.ID, &o.Title, &o.OwnerID, &o.Status, &o.Paths); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ClaimOverlap reports whether threads a and b share a file they weren't told about yet, and records
// that they were told about paths.
func (s *Store) ClaimOverlap(ctx context.Context, a, b string, paths []string) (bool, error) {
	if a > b {
		a, b = b, a
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO overlap_notices (a, b, paths) VALUES ($1, $2, $3)
		ON CONFLICT (a, b) DO UPDATE SET paths = ARRAY(SELECT DISTINCT unnest(overlap_notices.paths || excluded.paths) ORDER BY 1)
		WHERE NOT excluded.paths <@ overlap_notices.paths`, a, b, paths)
	return tag.RowsAffected() > 0, err
}
