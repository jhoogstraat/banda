-- Which app a github_apps row is about, and whether a user account owns it
-- (it then reaches every repository it is installed on, whatever their access).
-- Rows from before have app_id 0: they count as describing the app in place.
ALTER TABLE github_apps
  ADD COLUMN app_id   bigint  NOT NULL DEFAULT 0,
  ADD COLUMN slug     text    NOT NULL DEFAULT '',
  ADD COLUMN personal boolean NOT NULL DEFAULT false;

-- Every time someone signed in created, replaced or removed the team's
-- GitHub App, so a change everyone's connections depend on is never silent.
-- slug and owner are the app it put in place (empty on remove), was_* the one
-- it replaced or removed (empty on create).
CREATE TABLE github_app_changes (
  id        bigserial PRIMARY KEY,
  action    text NOT NULL CHECK (action IN ('created', 'replaced', 'removed')),
  by_user   text NOT NULL,
  at        timestamptz NOT NULL DEFAULT now(),
  host      text NOT NULL,
  slug      text NOT NULL DEFAULT '',
  owner     text NOT NULL DEFAULT '',
  was_slug  text NOT NULL DEFAULT '',
  was_owner text NOT NULL DEFAULT ''
);
