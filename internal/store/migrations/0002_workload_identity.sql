-- The audiences of the team's workload identity tokens (internal/identity),
-- and who added each. Not secret: tokens are minted, never stored.
CREATE TABLE id_token_audiences (
  audience text PRIMARY KEY,
  added_by text NOT NULL,
  added_at timestamptz NOT NULL DEFAULT now()
);
