# Banda

A web app where a team builds software together with coding agents. Each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox with the repo checked out on its own branch. Teammates can browse each other's threads, post into them and mention each other. See [PLAN.md](PLAN.md) for the architecture, decisions and status.

![A thread with the diff pane](docs/screenshots/thread.png)

## Prerequisites

- [mise](https://mise.jdx.dev): `mise install` provides go, node, pnpm, buf, kind, kubectl, podman and jq.
- Podman with the default machine (`podman machine init`, at least 8 CPUs and 16 GiB to share it with other projects). `mise.toml` points `podman` and `kind` at its root connection. For Docker (OrbStack, Docker Desktop), set `KIND_EXPERIMENTAL_PROVIDER=docker`, `CONTAINER=docker` and `RUNNER_API_URL=http://host.docker.internal:8787` under `[env]` in a `mise.local.toml`.

## Dev loop

Everything is a mise task (`mise tasks` lists them). Settings come from the environment, e.g. `DEV_AUTH=1 mise run api`.

1. `mise run up` starts the Podman machine and the kind cluster (agent-sandbox, Postgres on `localhost:5432`, Dex), creating them and the sandbox image the first time, then the web server (`:5173`) and the API (`:8787`). It serves your LAN address; `PUBLIC_HOST=localhost mise run up` serves this machine only. Rerun it to restart.
2. Open the URL it prints, on this machine too (the sign-in cookie belongs to that address), sign in, create a thread.
3. The first time, the thread shows "Claude Code is not logged in". Click **Log in to Claude Code**, open the URL shown in the terminal, sign in with your Claude subscription and paste the code back. The login is kept in your personal volume and shared by all your threads. You can also log in from Settings, which offers an Anthropic API key instead of the subscription.
4. `mise run down` stops the servers and the kind node.

Every thread leaves a sandbox behind until it is settled and pushed. `mise run clean` deletes the sandboxes, and their volumes, of threads nobody touched for `CLEAN_HOURS` (default 24); a later prompt starts a fresh checkout.

In the app, Cmd/Ctrl-K opens the command palette and `?` lists the keyboard shortcuts.

`mise run image` rebuilds the sandbox image after changing `cmd/runner` or `deploy/sandbox.Dockerfile`; `mise run gen` regenerates `gen/` from `proto/`. Server logs are in `/tmp/banda-run/`.

For private repos, and for the auto-push after every turn on https remotes, add a `GIT_TOKEN` secret in Settings. Secrets are environment variables in all your threads, applied when a thread's environment next starts, and redacted from everything the agent reports. A shared fallback for everyone:

```sh
kubectl create secret generic banda-git -n banda --from-literal=GIT_TOKEN=...
```

## Permissions

The permission chip in the composer sets what the agent asks for in that thread: nothing (bypass, the default), edits and commands (ask), commands other than edits (accept edits), or everything until you approve its plan (plan). Requests appear in the chat; you answer them, or your teammates once you trust them. "Always allow" applies to that tool for the rest of the thread.

## Preparing a repo

Before the agent starts, a thread installs the tools from the repo's `mise.toml`, `.mise.toml` or `.tool-versions` with [mise](https://mise.jdx.dev), then runs `.banda/setup.sh` with bash in the checkout (once per thread; again when the script changes; 15 min timeout). The chat shows each step. If one fails, the thread stops with its output; the terminal still works, and **Retry** runs the setup again.

## Sign in

The cluster runs [Dex](https://dexidp.io), a local OIDC provider, reached through the web server at `/dex`. Sign in as `alice`, `bob` or `carol` (`@example.com`) with the password `password`. `DEV_AUTH=1 mise run api` skips sign-in and trusts the `X-Banda-User` header instead (default user `dev`).

To use another OIDC provider, set these before `mise run api` (or when running `cmd/api`) and register `$PUBLIC_URL/auth/callback` as its redirect URI:

| Variable | Meaning |
|---|---|
| `OIDC_ISSUER` | Issuer URL, which serves `/.well-known/openid-configuration`. Enables sign-in. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Client credentials. |
| `PUBLIC_URL` | Origin the browser uses, default `http://localhost:5173`. |
| `SESSION_SECRET` | Signs the session cookie. If empty, a random one is used and sessions end when the API restarts. |

Without `OIDC_ISSUER` the API only starts with `DEV_AUTH=1`. Other API settings: `DATABASE_URL`, `SANDBOX_NAMESPACE`, `SANDBOX_IMAGE`, `RUNNER_API_URL`, `IDLE_SUSPEND_AFTER` (default `30m`, checked every `IDLE_SWEEP_EVERY`, default `1m`), `LOGIN_IDLE_AFTER` (default `10m`: login environments without a terminal), `DELETE_SETTLED_AFTER` (default `168h`: settled environments whose work is pushed), `MAX_SUBTHREAD_DEPTH` (default `3`) and `MAX_AGENT_THREADS` (default `8`: agents' sub-threads, see PLAN.md), `ADDR` (default `:8787`), `WEB_DIR` (serve a web build).

The Browser tab shows apps running in a thread's environment from a second origin: `PREVIEW_ADDR` (default `:8788`) is where the API serves it, `PREVIEW_URL` the URL browsers use for it, by default the host they reach Banda on with that port. Keep it on the same site as Banda (another port, or a sibling subdomain such as `preview.banda.example.com`): the preview signs in with a SameSite cookie. Previewed apps run code from the sandboxes on that site; outside a trusted LAN serve Banda over https (its cookies are then `__Host-`) with the preview on a sibling subdomain.

## Tests

- `mise run test`: Go unit tests (state machine, search queries, inbox and team fan-out, team activity summaries, agent prompt and review rendering, Claude stream mapping (plans and subagents included), sessions, MCP transcript, sub-thread reports, error mapping, MCP instructions, pull request links and auto-push against a local origin, secret names, redaction, setup steps, fork patches and prompts, preview tunnel, ports and proxy). `node web/src/lib/events.check.ts` checks the web event reducer (tool grouping, subagent nesting, the plan, autoPush lines, fork notes, sub-thread lines, that 20k tool calls stay fast, and that history loaded in pages matches a full replay, permission cards and `#seq` jumps included) and diff parsing, `node web/src/lib/inbox.check.ts` which inbox changes notify and how sub-threads nest.
- `mise run e2e`: the end-to-end suite in `web/e2e` ([Playwright](https://playwright.dev)): API tests with typed Connect clients and browser tests with one context per user, Dex sign-in included. It runs a fake agent that needs no Claude login (`deploy/fake-claude`, whose directives hold, crash or echo a turn). Every run gets a world of its own, derived from the checkout path, so checkouts run it side by side: namespace `banda-e2e-<run>` (sandboxes, volumes, a git server with the fixture repos, Dex), database `banda_e2e_<run>`, the image `localhost/banda-sandbox:fake-<run>` and APIs on free ports; it is deleted afterwards (`E2E_KEEP=1` keeps it). Arguments go to Playwright: `mise run e2e -- e2e/fork.spec.ts`, `mise run e2e -- -g "race"`. `E2E_WORKERS` (default 4) sets the parallelism. Failures leave a trace, the threads' last events and the API log (`/tmp/banda-e2e-<run>/`): `pnpm -C web exec playwright show-report`.
- `deploy/long-thread.sh [N]` seeds a suspended thread with N events (default 20000) straight into Postgres and prints its id, to try or measure long threads without running an agent.

## Run the API in the cluster

`mise run deploy` builds the API image (with the web build), loads it into kind and applies `deploy/banda.yaml`. Settings come from the optional Secret `banda-api` (the variables above). Run either `mise run api` or `mise run deploy`, not both. Reach it with `kubectl -n banda port-forward svc/banda-api 8787 8788`.
