# Banda

A web app where a team builds software together with coding agents. Each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox with the repo checked out on its own branch. Teammates browse each other's threads, post into them and mention each other. An internal tool for one team, never sold. [PLAN.md](PLAN.md) has the decisions and status, [README.md](README.md) the settings.

## Layout

- `cmd/api`: Go API (Connect RPC, OIDC sign-in, MCP at `/mcp`). `internal/` holds its packages: `api`, `thread` (state machine), `sandbox` (agent-sandbox pods), `store` (Postgres).
- `cmd/runner`: runs inside each sandbox pod, drives the unmodified `claude` CLI and calls back to the API.
- `proto/` → `gen/` via `mise run gen` (buf). Don't edit `gen/`.
- `web/`: SvelteKit static SPA (Svelte 5, shadcn-svelte, pnpm).
- `deploy/`: kind config, manifests, Dockerfiles, `dev.sh` (local stack), `e2e.sh`.

## Environment

- Tools come from [mise](https://mise.jdx.dev) (`mise.toml`): go, node, pnpm, buf, kind, kubectl, podman, jq. Inside the repo they're on `PATH` once mise is activated; otherwise prefix `mise exec --`. There is no Makefile: every command is a mise task (`mise tasks` lists them, settings come from env vars), so run those rather than retyping their commands.
- Containers run in Podman, not Docker. On macOS Podman runs one Linux VM at a time; Banda shares `podman-machine-default` with other projects, so never stop it. mise sets `CONTAINER_CONNECTION=podman-machine-default-root` and `KIND_EXPERIMENTAL_PROVIDER=podman`, so `podman` and `kind` target it. The user has other machines with their own containers: never stop them without asking.
- The kind cluster `banda` (kubectl context `kind-banda`) runs the agent-sandbox controller, Postgres and Dex. The API and web dev servers run on the Mac; sandbox pods reach the API as `host.containers.internal`.
- Start, restart and check the stack with `mise run up` / `e2e` / `down`; see the `run-locally` skill.
- Never run the real `claude` binary for tests: it uses the user's personal subscription. `mise run e2e` uses a fake agent.

## Checks

- `mise run test`: Go unit tests. `mise run e2e`: whole thread lifecycle against a running cluster.
- `node web/src/lib/events.check.ts`: web event reducer. `pnpm -C web check`: Svelte types.
