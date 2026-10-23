-- Who created the team's GitHub App from Settings, and when. Its keys are in
-- the Kubernetes Secret banda-github-app, never here; an app made by hand
-- (Helm, GitOps) has no row.
CREATE TABLE github_apps (
  host       text PRIMARY KEY,
  owner      text NOT NULL,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- A user's OAuth connection to a forge host, as the forge names them. The
-- tokens are in a Kubernetes Secret of its own (banda-forge-<hash>), never here.
CREATE TABLE forge_connections (
  user_id      text NOT NULL,
  host         text NOT NULL,
  forge        text NOT NULL,
  login        text NOT NULL,
  forge_id     bigint NOT NULL,
  avatar_url   text NOT NULL DEFAULT '',
  connected_at timestamptz NOT NULL DEFAULT now(),
  -- The forge refused it (revoked, expired): the user connects again.
  broken       boolean NOT NULL DEFAULT false,
  PRIMARY KEY (user_id, host)
);
