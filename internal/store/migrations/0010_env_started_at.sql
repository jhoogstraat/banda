-- When the thread's environment was last (re)started: the start deadline
-- counts from it, across API restarts. updated_at moves with every post.
ALTER TABLE threads ADD COLUMN env_started_at timestamptz NOT NULL DEFAULT now();
