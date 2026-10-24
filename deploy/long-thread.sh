#!/usr/bin/env bash
# Seeds a suspended thread owned by $OWNER (default dev) with $1 events (default 20000), straight into
# Postgres (database $DB, default banda), and prints its id. For e2e paging checks and measuring long
# threads; no agent runs.
# Each turn of 25 events: a message, the plan (planChanged), a thought, 10 tool calls with results (1.5 KB, every 7th turn
# one of 30 KB), agent text, the turn end.
set -euo pipefail
N=${1:-20000}
ID=long$(od -An -N8 -tx8 /dev/urandom | tr -d " ")
mise exec -- kubectl --context kind-banda -n banda exec -i postgres-0 -- psql -U banda -d "${DB:-banda}" -q -v ON_ERROR_STOP=1 -v id="$ID" -v n="$N" -v owner="${OWNER:-dev}" <<'SQL'
BEGIN;
INSERT INTO users (id, name) VALUES (:'owner', :'owner') ON CONFLICT DO NOTHING;
INSERT INTO threads (id, owner_id, title, repo_url, base_branch, branch, model, effort, status, mode, runner_token, last_seq)
VALUES (:'id', :'owner', 'long thread (' || :n || ' events)', 'https://example.com/long.git', 'main', 'banda/' || :'id',
  'claude-opus-5-5', 'high', 'suspended', 'agent', 'none', :n);
INSERT INTO events (thread_id, seq, body)
SELECT :'id', s, jsonb_build_object('seq', s::text, 'threadId', :'id', 'actor', CASE WHEN p = 0 THEN :'owner' ELSE 'agent' END,
  'time', to_char((now() - make_interval(secs => :n - s)) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')) ||
  CASE
    WHEN p = 0 THEN jsonb_build_object('message', jsonb_build_object('text', 'turn ' || t || ': keep going'))
    WHEN p = 1 THEN jsonb_build_object('planChanged', jsonb_build_object('items', jsonb_build_array(
        jsonb_build_object('content', 'Turn ' || t || ' step A', 'status', 'completed', 'activeForm', 'Doing A'),
        jsonb_build_object('content', 'Turn ' || t || ' step B', 'status', 'in_progress', 'activeForm', 'Doing turn ' || t || ' step B'),
        jsonb_build_object('content', 'Turn ' || t || ' step C', 'status', 'pending', 'activeForm', 'Doing C'))))
    WHEN p = 2 THEN jsonb_build_object('agentThinking', jsonb_build_object('text', 'Turn ' || t || ': next step B.'))
    WHEN p < 23 AND p % 2 = 1 THEN jsonb_build_object('toolCall', jsonb_build_object('id', 't' || t || '_' || p, 'name',
      (ARRAY['Read', 'Bash', 'Grep', 'Edit', 'Glob'])[p / 2 % 5 + 1], 'inputJson',
      jsonb_build_object('file_path', '/home/banda/work/internal/f' || p || '.go', 'command', 'go test ./...', 'pattern', 'func ',
        'old_string', E'a\nb', 'new_string', E'a\nc')::text))
    WHEN p < 23 THEN jsonb_build_object('toolResult', jsonb_build_object('toolCallId', 't' || t || '_' || (p - 1), 'output',
      repeat(E'some typical line of tool output, about 50 chars\n', CASE WHEN t % 7 = 0 AND p = 4 THEN 600 ELSE 30 END)))
    WHEN p = 23 THEN jsonb_build_object('agentText', jsonb_build_object('text', 'Turn ' || t || ' done: **all tests pass**.'))
    ELSE jsonb_build_object('turnEnded', jsonb_build_object('durationMs', '1000', 'costUsd', 0.01))
  END
FROM generate_series(1, :n) s, LATERAL (SELECT (s - 1) % 25 AS p, (s - 1) / 25 AS t) x;
COMMIT;
SQL
echo "$ID"
