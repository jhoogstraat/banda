# Banda

A web app where a team builds software together with coding agents. Each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox with the repo checked out on its own branch. Teammates browse each other's threads, post into them and mention each other. An internal tool for one team, never sold.

- [VISION.md](VISION.md): why Banda exists and the principles for deciding anything not settled yet. Read it before a design choice.
- [PLAN.md](PLAN.md): the decisions made and what is built.
- [todo.md](todo.md): what is left before Banda runs fully, by phase.
- [README.md](README.md): settings and how to run it; [docs/deployment.md](docs/deployment.md): deployment options.

## Layout

- `cmd/api`: Go API (Connect RPC, OIDC sign-in, MCP at `/mcp`). `internal/` holds its packages: `api`, `thread` (state machine), `sandbox` (agent-sandbox pods), `store` (Postgres), `tunnel` (preview connections over the runner's stream, shared with the runner).
- `cmd/runner`: runs inside each sandbox pod, drives the unmodified `claude` CLI and calls back to the API.
- `proto/` → `gen/` via `mise run gen` (buf). Don't edit `gen/`.
- `web/`: SvelteKit static SPA (Svelte 5, shadcn-svelte, pnpm).
- `deploy/`: the Helm chart (`helm/banda`, the one source of the manifests) and Kustomize on top (`kustomize/`; `kind/values.yaml` are the dev values), kind config, Dockerfiles, `dev.sh` (local stack and the e2e world), `fake-claude` (the e2e agent).
- `web/e2e/`: the end-to-end suite (Playwright): `fixtures.ts` is the harness, one spec file per domain.

## Environment

- Tools come from [mise](https://mise.jdx.dev) (`mise.toml`): go, node, pnpm, buf, kind, kubectl, helm, podman, jq, actionlint. Inside the repo they're on `PATH` once mise is activated; otherwise prefix `mise exec --`. There is no Makefile: every command is a mise task (`mise tasks` lists them, settings come from env vars), so run those rather than retyping their commands.
- Containers run in Podman, not Docker. On macOS Podman runs one Linux VM at a time; Banda shares `podman-machine-default` with other projects, so never stop it. mise sets `CONTAINER_CONNECTION=podman-machine-default-root` and `KIND_EXPERIMENTAL_PROVIDER=podman`, so `podman` and `kind` target it. The user has other machines with their own containers: never stop them without asking.
- The kind cluster `banda` (kubectl context `kind-banda`) runs the agent-sandbox controller, Postgres and Dex. The API and web dev servers run on the Mac; sandbox pods reach the API as `host.containers.internal`.
- Start, restart and check the stack with `mise run up` / `e2e` / `down`; see the `run-locally` skill.
- Never run the real `claude` binary for tests: it uses the user's personal subscription. `mise run e2e` uses a fake agent.

## Checks

- `mise run test`: Go unit tests. `mise run e2e`: the end-to-end suite (API and browser) against the kind cluster, in a namespace and database of its own, so checkouts run it side by side. `mise run e2e -- e2e/permissions.spec.ts` runs one file, `-- -g "<title>"` matching tests. A bug fix comes with an e2e test that fails before it; a known bug not fixed yet is reproduced in its spec as `test.fail()`.
- `node web/src/lib/events.check.ts`: web event reducer. `pnpm -C web check`: Svelte types. `mise run chart-check`: the chart and Kustomize overlays. CI (`.github/workflows`) runs these on pull requests; a `v*` tag releases images and the chart.
