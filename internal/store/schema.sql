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

ALTER TABLE threads ADD COLUMN IF NOT EXISTS turns int NOT NULL DEFAULT 0;

-- How each user signs in to each harness, and their login as last reported
-- by one of their environments.
CREATE TABLE IF NOT EXISTS harness_settings (
  user_id          text NOT NULL,
  harness          text NOT NULL,
  auth_method      text NOT NULL DEFAULT 'subscription',
  api_key_set      boolean NOT NULL DEFAULT false,
  login_state      text NOT NULL DEFAULT 'unknown',
  login_checked_at timestamptz,
  PRIMARY KEY (user_id, harness)
);

-- Names of each user's secrets. The values live only in Kubernetes Secrets.
CREATE TABLE IF NOT EXISTS secrets (
  user_id    text NOT NULL,
  name       text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, name)
);

-- Authenticates the runner of the user's login environment.
ALTER TABLE users ADD COLUMN IF NOT EXISTS login_token text;

-- Teammate turn requests waiting for the owner, and whether teammates may
-- start turns without approval.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS requests int NOT NULL DEFAULT 0;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS trusted boolean NOT NULL DEFAULT false;
-- The last event seq each viewer has seen; later events make the thread unread.
ALTER TABLE thread_views ADD COLUMN IF NOT EXISTS seen_seq bigint NOT NULL DEFAULT 0;

-- The latest notable event, summarized for the team board (protojson of
-- banda.v1.TeamActivity).
ALTER TABLE threads ADD COLUMN IF NOT EXISTS activity jsonb;

CREATE INDEX IF NOT EXISTS threads_updated ON threads (updated_at DESC);

-- Finds the latest plan before a tail without scanning the thread.
CREATE INDEX IF NOT EXISTS events_plan ON events (thread_id, seq) WHERE body->'toolCall'->>'name' = 'TodoWrite';
