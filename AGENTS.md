# Banda

A web app where a team builds software together with coding agents. Each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox with the repo checked out on its own branch. Teammates browse each other's threads, post into them and mention each other. An internal tool for one team, never sold.

- [VISION.md](VISION.md): why Banda exists and the principles for deciding anything not settled yet. Read it before a design choice.
- [PLAN.md](PLAN.md): the decisions made, how Banda works and its known limits.
- [todo.md](todo.md): what is left before Banda runs fully.
- [README.md](README.md): what Banda is and the quick start; `docs/`: [usage](docs/usage.md), [configuration](docs/configuration.md) (every setting), [deployment](docs/deployment.md) (Helm, Kustomize, releases), [development](docs/development.md) (dev loop, tests).

When you notice an inefficiency or make a mistake that a clearer instruction would have prevented, improve this file (or the relevant skill or doc) right away so it doesn't happen again.

## Layout

- `cmd/api`: Go API (Connect RPC, OIDC sign-in, MCP at `/mcp`). `internal/` holds its packages: `api`, `thread` (state machine), `sandbox` (agent-sandbox pods), `store` (Postgres), `tunnel` (preview connections over the runner's stream, shared with the runner), `identity` (the workload identity issuer).
- `cmd/runner`: runs inside each sandbox pod, drives the unmodified `claude` CLI and calls back to the API.
- `proto/` → `gen/` via `mise run gen` (buf). Don't edit `gen/`.
- `web/`: SvelteKit static SPA (Svelte 5, shadcn-svelte, pnpm).
- `deploy/`: the Helm chart (`helm/banda`, the one source of the manifests) and Kustomize on top (`kustomize/`; `kind/values.yaml` are the dev values), kind config, Dockerfiles, `dev.sh` (local stack and the e2e world), `fake-claude` (the e2e agent).
- `web/e2e/`: the end-to-end suite (Playwright): `fixtures.ts` is the harness, one spec file per domain.

## Environment

- Tools come from [mise](https://mise.jdx.dev) (`mise.toml`): go, node, pnpm, buf, kind, kubectl, helm, podman, jq, actionlint. Inside the repo they're on `PATH` once mise is activated; otherwise prefix `mise exec --`. There is no Makefile: every command is a mise task (`mise tasks` lists them, settings come from env vars), so run those rather than retyping their commands.
- Containers run in Podman, not Docker. On macOS Podman runs one Linux VM at a time; Banda shares `podman-machine-default` with other projects, so never stop it. mise sets `CONTAINER_CONNECTION=podman-machine-default-root` and `KIND_EXPERIMENTAL_PROVIDER=podman`, so `podman` and `kind` target it. The user has other machines with their own containers: never stop them without asking.
- The kind cluster `banda` (kubectl context `kind-banda`) runs the agent-sandbox controller, Postgres and Dex. The API and web dev servers run on the Mac; sandbox pods reach the API as `host.containers.internal`. A second kind cluster gets a kubeconfig of its own (`kind create cluster --kubeconfig <file>`): the default context is shared with everything else on the Mac.
- Start, restart and check the stack with `mise run up` / `e2e` / `down`; see the `run-locally` skill.
- Never run the real `claude` binary for tests: on the Mac it finds the user's personal subscription in the keychain. The one exception is `mise run contract`: the pinned `claude` inside the sandbox image, in a container with `--network none`, a throwaway `ANTHROPIC_API_KEY`, an empty `CLAUDE_CONFIG_DIR` and `ANTHROPIC_BASE_URL` at a fake Messages API in the same container. Never loosen any of these, run it on the Mac or with a real login. `mise run e2e` uses a fake agent.

- Security: sandboxes are untrusted and get only short-lived, narrowly scoped credentials minted by the API (VISION.md principle 2); long-lived keys never enter a pod, and secret values never reach Postgres, logs or the browser.

## Checks

- `mise run test`: Go unit tests. `mise run e2e`: the end-to-end suite (API and browser) against the kind cluster, in a namespace and database of its own, so checkouts run it side by side. `mise run e2e -- e2e/permissions.spec.ts` runs one file, `-- -g "<title>"` matching tests. Key tests (the main journeys and security guarantees) are tagged `@key`: `mise run e2e -- --grep @key`. A bug fix comes with an e2e test that fails before it; a known bug not fixed yet is reproduced in its spec as `test.fail()`. `mise run contract`: the runner against the real pinned CLI (`cmd/runner/contract_test.go`); run it when you change how the runner talks to `claude`, `deploy/fake-claude` or `CLAUDE_VERSION`, and make the fake follow what it finds.
- `node web/src/lib/events.check.ts`: web event reducer. `pnpm -C web check`: Svelte types. `mise run chart-check`: the chart and Kustomize overlays. CI (`.github/workflows`) runs these on pull requests and on `main`, the `@key` e2e tests only on `main`; a `v*` tag releases images and the chart.

## Git

- Once a goal is defined and its result approved, the work ends when it is merged on `main` and tested: land it and see CI pass, don't stop at an uncommitted or unmerged result.
- The remote is `github.com/jhoogstraat/banda` (private for now). Pushing to `main` is fine once your changes are squashed into a single commit with a message that says what changed and why, or squash-merge a pull request. Test locally first (`mise run test`, and `mise run e2e` with the specs the change touches, the whole suite for a change that reaches every thread); CI then runs on `main`, and a failure there is fixed forward. Never force-push `main`, and don't create tags: a `v*` tag publishes a release.
