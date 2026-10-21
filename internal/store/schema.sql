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
-- Runner tokens are kept as SHA-256 hashes only (pods read theirs from a
-- Secret), so reading the database doesn't let anyone act as a runner. The
-- plain tokens of before 2026-10-21 become hashes.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS runner_hash text NOT NULL DEFAULT '';
ALTER TABLE threads ALTER COLUMN runner_token SET DEFAULT '';
UPDATE threads SET runner_hash = encode(sha256(convert_to(runner_token, 'UTF8')), 'hex'), runner_token = '' WHERE runner_token <> '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS login_hash text;
UPDATE users SET login_hash = encode(sha256(convert_to(login_token, 'UTF8')), 'hex'), login_token = NULL WHERE login_token IS NOT NULL;

-- Teammate turn requests waiting for the owner, and whether teammates may
-- start turns without approval.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS requests int NOT NULL DEFAULT 0;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS trusted boolean NOT NULL DEFAULT false;
-- The last event seq each viewer has seen; later events make the thread unread.
ALTER TABLE thread_views ADD COLUMN IF NOT EXISTS seen_seq bigint NOT NULL DEFAULT 0;

-- Files posted with messages: images and small text files, referenced from
-- Message.attachments by id. Rows of uploads that were never posted stay.
CREATE TABLE IF NOT EXISTS attachments (
  id         text PRIMARY KEY,
  user_id    text NOT NULL,
  name       text NOT NULL,
  media_type text NOT NULL,
  data       bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Attachment ids of the images that go with pending_prompt.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS pending_images text[] NOT NULL DEFAULT '{}';
-- The thread a sub-thread was spawned from ('' for none).
ALTER TABLE threads ADD COLUMN IF NOT EXISTS parent_id text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS threads_parent ON threads (parent_id) WHERE parent_id <> '';

-- The latest notable event, summarized for the team board (protojson of
-- banda.v1.TeamActivity).
ALTER TABLE threads ADD COLUMN IF NOT EXISTS activity jsonb;

CREATE INDEX IF NOT EXISTS threads_updated ON threads (updated_at DESC);

-- Finds the latest plan before a tail without scanning the thread.
CREATE INDEX IF NOT EXISTS events_plan ON events (thread_id, seq) WHERE body->'toolCall'->>'name' = 'TodoWrite';
-- Where the thread's work lives (bandav1.AutoPushState name), as its runner last reported.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS auto_push text NOT NULL DEFAULT '';
-- When the settled environment is (or was) deleted; null when none is scheduled.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS env_delete_at timestamptz;
-- The suspended environment's work was all on origin when it was suspended (see SuspendEnv).
ALTER TABLE threads ADD COLUMN IF NOT EXISTS env_pushed boolean NOT NULL DEFAULT false;
-- The chat was told its environment was deleted.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS env_deleted boolean NOT NULL DEFAULT false;
-- Which tool calls the agent asks for; its pending permission requests (id ->
-- tool name); and the tools allowed without asking for the rest of the thread.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS permission_mode text NOT NULL DEFAULT 'bypass';
ALTER TABLE threads ADD COLUMN IF NOT EXISTS permissions jsonb NOT NULL DEFAULT '{}';
ALTER TABLE threads ADD COLUMN IF NOT EXISTS allowed_tools text[] NOT NULL DEFAULT '{}';
-- Forks start from another thread's changes after its turn fork_turn; the
-- threads of a race (or of one Fork call) share race_id.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS forked_from text NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN IF NOT EXISTS fork_turn int NOT NULL DEFAULT 0;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS race_id text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS threads_race ON threads (race_id) WHERE race_id <> '';
-- Full-text search over what people and agents said and did: messages and
-- turn requests (with review comments), agent text, tool calls (name and
-- input values). Adding the column fills it for existing events.
ALTER TABLE events ADD COLUMN IF NOT EXISTS search tsvector GENERATED ALWAYS AS (
  to_tsvector('english'::regconfig,
    coalesce(body->'message'->>'text', body->'turnRequest'->'message'->>'text', body->'agentText'->>'text', '') || ' ' ||
    coalesce(body->'toolCall'->>'name', '') || ' ' ||
    regexp_replace(coalesce(body->'toolCall'->>'inputJson', ''), '"\w+":|[{}"]', ' ', 'g'))
  || jsonb_to_tsvector('english'::regconfig, coalesce(body->'message'->'review', body->'turnRequest'->'message'->'review', '[]'), '["string"]')
) STORED;
CREATE INDEX IF NOT EXISTS events_search ON events USING gin (search);
-- Each event's kind (its Event.kind oneof field, e.g. 'message'), so queries
-- that need only some kinds skip the large bodies of the others. Adding the
-- column fills it for existing events.
CREATE OR REPLACE FUNCTION event_kind(body jsonb) RETURNS text IMMUTABLE LANGUAGE sql
  AS $$ SELECT k FROM jsonb_object_keys(body) k WHERE k NOT IN ('seq', 'threadId', 'time', 'actor', 'context', 'truncated') LIMIT 1 $$;
ALTER TABLE events ADD COLUMN IF NOT EXISTS kind text GENERATED ALWAYS AS (event_kind(body)) STORED;
CREATE INDEX IF NOT EXISTS events_kind ON events (thread_id, kind, seq);
-- Who the agent works with (see AgentSettings.profile), and each user's
-- profile for new threads that don't name one.
ALTER TABLE threads ADD COLUMN IF NOT EXISTS profile text NOT NULL DEFAULT 'engineer';
ALTER TABLE users ADD COLUMN IF NOT EXISTS default_profile text NOT NULL DEFAULT 'engineer';
