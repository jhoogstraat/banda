package store

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// SearchQuery filters by every field that is set. Kind is "people",
// "agent" or "code" (tool calls and review comments).
type SearchQuery struct {
	Words                string
	Actor, RepoURL, Kind string
	After, Before        *time.Time
	Limit                int
}

type SearchHit struct {
	ThreadID, Title, RepoURL string
	Seq                      int64
	Actor                    string
	Time                     *time.Time
	Kind                     string
	// Odd parts are the matched words.
	Snippet []string
}

// Characters a search term keeps: words, file names and paths.
var termRe = regexp.MustCompile(`[\p{L}\p{N}_][\p{L}\p{N}_./-]*`)

// tsquery turns typed words into a to_tsquery expression that needs every
// word, each as a prefix. Everything else is dropped, so it can't be a syntax
// error. "" if there are no words.
func tsquery(words string) string {
	terms := termRe.FindAllString(words, 8)
	for i, t := range terms {
		terms[i] = strings.TrimRight(t, "./-") + ":*"
	}
	return strings.Join(terms, " & ")
}

const (
	searchKind   = `CASE WHEN e.body ? 'agentText' THEN 'agent' WHEN e.body ? 'toolCall' THEN 'code' ELSE 'people' END`
	searchReview = `coalesce(e.body->'message'->'review', e.body->'turnRequest'->'message'->'review')`
	// The readable text of an event, close to what schema.sql indexes.
	searchDoc = `coalesce(e.body->'message'->>'text', e.body->'turnRequest'->'message'->>'text', e.body->'agentText'->>'text', '') ||
	coalesce(' ' || (SELECT string_agg((r->>'path') || ': ' || (r->>'body'), ' · ') FROM jsonb_array_elements(` + searchReview + `) r), '') ||
	coalesce(e.body->'toolCall'->>'name' || ' ' || regexp_replace(regexp_replace(e.body->'toolCall'->>'inputJson', '"\w+":|[][{}",]', ' ', 'g'), '\s+', ' ', 'g'), '')`
)

// Search finds events by their words across all threads, best match first.
// Every thread is visible to everyone, so there is no viewer.
func (s *Store) Search(ctx context.Context, q SearchQuery) ([]SearchHit, error) {
	tq := tsquery(q.Words)
	if tq == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
WITH hits AS (
  SELECT e.thread_id, e.seq, e.body, ts_rank(e.search, q) AS rank, `+searchKind+` AS kind
  FROM events e, to_tsquery('english', $1) q
  WHERE e.search @@ q
    AND ($2 = '' OR e.body->>'actor' = $2)
    AND ($3 = '' OR e.thread_id IN (SELECT id FROM threads WHERE repo_url = $3))
    AND ($4 = '' OR $4 = `+searchKind+` OR $4 = 'code' AND jsonb_array_length(coalesce(`+searchReview+`, '[]')) > 0)
    AND ($5::timestamptz IS NULL OR (e.body->>'time')::timestamptz >= $5)
    AND ($6::timestamptz IS NULL OR (e.body->>'time')::timestamptz < $6)
  ORDER BY rank DESC, (e.body->>'time') DESC NULLS LAST
  LIMIT $7
)
SELECT e.thread_id, t.title, t.repo_url, e.seq, coalesce(e.body->>'actor', ''), (e.body->>'time')::timestamptz, e.kind,
  ts_headline('english', `+searchDoc+`, to_tsquery('english', $1),
    'StartSel=' || chr(2) || ', StopSel=' || chr(3) || ', MaxWords=35, MinWords=12, MaxFragments=2, FragmentDelimiter=" … "')
FROM hits e JOIN threads t ON t.id = e.thread_id
ORDER BY e.rank DESC, (e.body->>'time') DESC NULLS LAST`,
		tq, q.Actor, q.RepoURL, q.Kind, q.After, q.Before, min(max(q.Limit, 1), 100))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var snippet string
		if err := rows.Scan(&h.ThreadID, &h.Title, &h.RepoURL, &h.Seq, &h.Actor, &h.Time, &h.Kind, &snippet); err != nil {
			return nil, err
		}
		h.Snippet = strings.Split(strings.ReplaceAll(snippet, "\x03", "\x02"), "\x02")
		out = append(out, h)
	}
	return out, rows.Err()
}
