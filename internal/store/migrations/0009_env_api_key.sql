-- A claude process keeps the login it started with: env_api_key records
-- whether the thread's environment started with the owner's API key, and
-- pending_teammate whether its pending prompt holds a teammate's turn, which
-- never runs in one started on a subscription.
ALTER TABLE threads ADD COLUMN env_api_key boolean NOT NULL DEFAULT false,
  ADD COLUMN pending_teammate boolean NOT NULL DEFAULT false;
