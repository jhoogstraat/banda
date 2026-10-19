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

-- Last event seq included in a prompt to the agent, so chat messages posted
-- in between can be handed over as context.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS delivered_seq bigint NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS users (
  id         text PRIMARY KEY,
  name       text NOT NULL,
  email      text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Each viewer's own snooze/settle state of a thread.
CREATE TABLE IF NOT EXISTS thread_views (
  user_id       text NOT NULL,
  thread_id     text NOT NULL REFERENCES threads (id) ON DELETE CASCADE,
  snoozed_until timestamptz,
  settled_at    timestamptz,
  PRIMARY KEY (user_id, thread_id)
);

CREATE TABLE IF NOT EXISTS mentions (
  thread_id   text NOT NULL REFERENCES threads (id) ON DELETE CASCADE,
  seq         bigint NOT NULL,
  user_id     text NOT NULL,
  answered_at timestamptz,
  PRIMARY KEY (thread_id, seq, user_id)
);

CREATE INDEX IF NOT EXISTS mentions_open ON mentions (user_id) WHERE answered_at IS NULL;
