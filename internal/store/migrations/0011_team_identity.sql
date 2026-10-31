-- team_identity: this thread runs Claude Code on the team's Console identity,
-- whatever its owner's login. session_moved: its environment moved between the
-- owner's two Claude volumes, so the next prompt carries the conversation.
ALTER TABLE threads ADD COLUMN team_identity boolean NOT NULL DEFAULT false,
  ADD COLUMN session_moved boolean NOT NULL DEFAULT false;

-- Key environments mounted the volume with the owner's subscription login
-- until now: none of them may run a teammate's turn. Each restarts onto the
-- new volume, as after a switch to a key.
UPDATE threads SET env_api_key = false WHERE env_api_key;
