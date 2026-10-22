# Development

Read [AGENTS.md](../AGENTS.md) for the layout and [VISION.md](../VISION.md) before a design choice.

## Prerequisites

- [mise](https://mise.jdx.dev): `mise install` provides go, node, pnpm, buf, kind, kubectl, helm, podman, jq and actionlint.
- Podman with the default machine (`podman machine init`, at least 8 CPUs and 16 GiB to share it with other projects). `mise.toml` points `podman` and `kind` at its root connection. For Docker (OrbStack, Docker Desktop), set `KIND_EXPERIMENTAL_PROVIDER=docker`, `CONTAINER=docker` and `RUNNER_API_URL=http://host.docker.internal:8787` under `[env]` in a `mise.local.toml`; with Docker Desktop also `HOST_IP` (the host's address as pods see it) for the sandbox policies and `mise run e2e`. On Linux with Docker, `KIND_EXPERIMENTAL_PROVIDER=docker` and `CONTAINER=docker` suffice: the host is the gateway of kind's network (CI runs it that way).

## Dev loop

Everything is a mise task (`mise tasks` lists them). Settings come from the environment, e.g. `DEV_AUTH=1 mise run api`.

1. `mise run up` starts the Podman machine and the kind cluster (agent-sandbox, Postgres on `localhost:5432`, Dex), creating them and the sandbox image the first time, then the web server (`:5173`) and the API (`:8787`). It serves your LAN address; `PUBLIC_HOST=localhost mise run up` serves this machine only. Rerun it to restart.
2. Open the URL it prints, on this machine too (the sign-in cookie belongs to that address), sign in, create a thread.
3. The first time, the thread shows "Claude Code is not logged in". Click **Log in to Claude Code**, open the URL shown in the terminal, sign in with your Claude subscription and paste the code back. The login is kept in your personal volume and shared by all your threads. You can also log in from Settings, which offers an Anthropic API key instead of the subscription.
4. `mise run down` stops the servers and the kind node.

A thread's sandbox stays until its work is on origin and it is settled (7 days) or idle for `DELETE_IDLE_AFTER` (30 days), too long for a dev loop. `mise run clean` deletes the sandboxes, and their volumes, of threads with a repository nobody touched for `CLEAN_HOURS` (default 24); a later prompt starts a fresh checkout.

In the app, Cmd/Ctrl-K opens the command palette and `?` lists the keyboard shortcuts.

`mise run image` rebuilds the sandbox image after changing `cmd/runner` or `deploy/sandbox.Dockerfile`; `mise run gen` regenerates `gen/` from `proto/`. Server logs are in `/tmp/banda-run/`.

For private repos, and for the auto-push after every turn on https remotes, add a `GIT_TOKEN` secret in Settings. Secrets are environment variables in all your threads, applied when a thread's environment next starts, and redacted from everything the agent reports. Team secrets, also in Settings, go to every thread of every teammate, so shared credentials (a kubeconfig, database URLs, observability tokens) need no setup; anyone can replace or delete them, Settings shows who set each last, and a personal secret of the same name wins. Every agent can use them: prefer read-only credentials. An admin-made fallback below both:

```sh
kubectl create secret generic banda-git -n banda --from-literal=GIT_TOKEN=...
```

## Tests

- `mise run test`: Go unit tests (state machine, search queries, inbox and team fan-out, team activity summaries, agent prompt and review rendering, Claude stream mapping (plans and subagents included), sessions, MCP transcript, sub-thread reports and the guard on waking a parent, error mapping, MCP instructions, pull request links and auto-push against a local origin, the GitHub and GitLab providers against fake APIs (create, checks, reviews, comments, merge, errors, conditional requests) and which pull request changes reach the chat, secret names and the order of their sources, repository URLs, egress allowlists, redaction, setup steps, fork patches and prompts, preview tunnel, ports and proxy, profile texts and repo overrides). `node web/src/lib/events.check.ts` checks the web event reducer (tool grouping, subagent nesting, the plan, autoPush lines, fork notes, sub-thread lines, that 20k tool calls stay fast, and that history loaded in pages matches a full replay, permission cards, question votes and `#seq` jumps included) and diff parsing, `node web/src/lib/inbox.check.ts` which inbox changes notify and how sub-threads nest.
- `mise run e2e`: the end-to-end suite in `web/e2e` ([Playwright](https://playwright.dev)): API tests with typed Connect clients and browser tests with one context per user, Dex sign-in included. It runs a fake agent that needs no Claude login (`deploy/fake-claude`, whose directives hold, crash or echo a turn, or ask before a call), and maps `https://github.com/e2e/<repo>.git` to its git server, which `web/e2e/forge.spec.ts` pairs with a fake GitHub and GitLab API (`web/e2e/forge.ts`, through `TEST_FORGE_URL`); `E2E_SCREENSHOTS=1` also saves `screenshots/pull-request.png`. Every run gets a world of its own, derived from the checkout path, so checkouts run it side by side: namespace `banda-e2e-<run>` (sandboxes, volumes, a git server with the fixture repos, Dex), database `banda_e2e_<run>`, the image `localhost/banda-sandbox:fake-<run>` and APIs on free ports; it is deleted afterwards (`E2E_KEEP=1` keeps it). Arguments go to Playwright: `mise run e2e -- e2e/fork.spec.ts`, `mise run e2e -- -g "race"`. `E2E_WORKERS` (default 4) sets the parallelism. Failures leave a trace, the threads' last events and the API log (`/tmp/banda-e2e-<run>/`): `pnpm -C web exec playwright show-report`.
- Schema changes are new files in `internal/store/migrations/`, `NNNN_what.sql` numbered after the last: the API applies each once, in order, in a transaction, and records it in `schema_migrations`; a failed one stops the API with its name. Never edit an applied migration (`0001_baseline.sql` is frozen); fix forward with a new one. Their tests (`internal/store/migrate_test.go`) use the dev Postgres on `localhost:5432` (`TEST_DATABASE_URL`) and skip without it.
- `deploy/long-thread.sh [N]` seeds a suspended thread with N events (default 20000) straight into Postgres and prints its id, to try or measure long threads without running an agent.
