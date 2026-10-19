CREATE TABLE IF NOT EXISTS threads (
  id             text PRIMARY KEY,
  owner_id       text NOT NULL,
  title          text NOT NULL,
  repo_url       text NOT NULL,
  base_branch    text NOT NULL,
  branch         text NOT NULL,
  model          text NOT NULL,
  effort         text NOT NULL,
  status         text NOT NULL,
  mode           text NOT NULL,
  pending_prompt text NOT NULL DEFAULT '',
  runner_token   text NOT NULL,
  diff           text NOT NULL DEFAULT '',
  last_seq       bigint NOT NULL DEFAULT 0,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS threads_owner ON threads (owner_id, updated_at DESC);

-- Append-only thread history; body is the protojson of banda.v1.Event.
CREATE TABLE IF NOT EXISTS events (
  thread_id text NOT NULL REFERENCES threads (id) ON DELETE CASCADE,
  seq       bigint NOT NULL,
  body      jsonb NOT NULL,
  PRIMARY KEY (thread_id, seq)
);
