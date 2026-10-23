-- Which application a gitlab_apps row is about: one made by hand later (a
-- Secret with another ID) is the deployment's, not the row's. Rows from
-- before have '' and count as describing the application in place.
ALTER TABLE gitlab_apps ADD COLUMN application_id text NOT NULL DEFAULT '';

-- Every time someone signed in set up, replaced or removed a host's GitLab
-- application, so a change everyone's connections depend on is never
-- silent. application_id is the one it put in place (empty on remove),
-- was_application_id the one it replaced or removed (empty on create).
CREATE TABLE gitlab_app_changes (
  id                 bigserial PRIMARY KEY,
  action             text NOT NULL CHECK (action IN ('created', 'replaced', 'removed')),
  by_user            text NOT NULL,
  at                 timestamptz NOT NULL DEFAULT now(),
  host               text NOT NULL,
  application_id     text NOT NULL DEFAULT '',
  was_application_id text NOT NULL DEFAULT ''
);
CREATE INDEX gitlab_app_changes_host ON gitlab_app_changes (host, id DESC);
