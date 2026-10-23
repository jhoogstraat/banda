-- Who set up the team's GitLab application on a host from Settings, and when.
-- Its ID and secret are in the Kubernetes Secret banda-gitlab-app-<host>,
-- never here; one made by hand (Helm, GitOps) has no row. Users' connections
-- are forge_connections rows like GitHub's.
CREATE TABLE gitlab_apps (
  host       text PRIMARY KEY,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
