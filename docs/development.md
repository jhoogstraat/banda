# Development

Read [AGENTS.md](../AGENTS.md) for the layout and [VISION.md](../VISION.md) before a design choice.

## Prerequisites

- [mise](https://mise.jdx.dev): `mise install` provides go, node, pnpm, buf, kind, kubectl, helm, podman, jq and actionlint.
- Podman with the default machine (`podman machine init`, at least 8 CPUs and 16 GiB to share it with other projects). `mise.toml` points `podman` and `kind` at its root connection. For Docker (OrbStack, Docker Desktop), set `KIND_EXPERIMENTAL_PROVIDER=docker`, `CONTAINER=docker` and `RUNNER_API_URL=http://host.docker.internal:8787` under `[env]` in a `mise.local.toml`; with Docker Desktop also `HOST_IP` (the host's address as pods see it) for the sandbox policies and `mise run e2e`. On Linux with Docker, `KIND_EXPERIMENTAL_PROVIDER=docker` and `CONTAINER=docker` suffice: the host is the gateway of kind's network, the kind node's default route (CI runs the `@key` e2e tests that way, `.github/workflows/ci.yml`).

## Dev loop

Everything is a mise task (`mise tasks` lists them). Settings come from the environment, e.g. `DEV_AUTH=1 mise run api`.

1. `mise run up` starts the Podman machine and the kind cluster (agent-sandbox, Postgres on `localhost:5432`, Dex), creating them and the sandbox image the first time, then the web server (`:5173`) and the API (`:8787`). It serves your LAN address; `PUBLIC_HOST=localhost mise run up` serves this machine only. Rerun it to restart.
2. Open the URL it prints, on this machine too (the sign-in cookie belongs to that address), sign in, create a thread.
3. The first time, the thread shows "Claude Code is not logged in". Click **Log in to Claude Code**, open the URL shown in the terminal, sign in with your Claude subscription and paste the code back. The login is kept in your personal volume and shared by all your threads. You can also log in from Settings, which offers an Anthropic API key instead of the subscription.
4. `mise run down` stops the servers and the kind node.

A thread's sandbox stays until its work is on origin and it is settled (7 days) or idle for `DELETE_IDLE_AFTER` (30 days), too long for a dev loop. `mise run clean` deletes the sandboxes, and their volumes, of threads with a repository nobody touched for `CLEAN_HOURS` (default 24); a later prompt starts a fresh checkout.

In the app, Cmd/Ctrl-K opens the command palette and `?` lists the keyboard shortcuts.

`mise run image` rebuilds the sandbox image after changing `cmd/runner` or `deploy/sandbox.Dockerfile`; `mise run gen` regenerates `gen/` from `proto/`. Server logs are in `/tmp/banda-run/`.

For private repos, and for the auto-push after every turn on https remotes, set up the GitHub App or the GitLab application in Settings and connect (docs/usage.md), or add a `GIT_TOKEN` secret ([usage](usage.md#settings)). An admin-made fallback below personal and team secrets:

```sh
kubectl create secret generic banda-git -n banda --from-literal=GIT_TOKEN=...
```

## Tests

- `mise run test`: Go unit tests, next to the code they test. `node web/src/lib/events.check.ts` checks the web event reducer and diff parsing, `node web/src/lib/inbox.check.ts` which inbox changes notify and how sub-threads nest, `node web/src/lib/identity-recipes.check.ts` that the workload identity recipes carry the real values.
- `mise run contract`: the runner against the real `claude` pinned in the sandbox image (`cmd/runner/contract_test.go`, build tag `contract`). It builds the image's `sandbox` target (`CONTRACT_IMAGE` names one built already, as in CI), cross-compiles the test binary and runs it in that image with `--network none`, a throwaway `ANTHROPIC_API_KEY` and an empty `CLAUDE_CONFIG_DIR`; the test serves a scripted fake Messages API and a stub of Banda's MCP server on localhost, starts `claude` with the runner's own `startClaude` and maps its output with `translate` and the permission code. No login, no Anthropic, never on the Mac. It covers a turn with thinking, Bash and a subagent, every permission mode, plans, questions, folded and queued prompts, an interrupt, a restart with a new profile, model and effort, and an MCP call (about 15 s). Arguments go to the test binary: `mise run contract -test.run Fold`. Run it when you bump `CLAUDE_VERSION`, change how the runner talks to `claude` or change `deploy/fake-claude`; CI runs it on every pull request. Where `deploy/fake-claude` still differs from the real CLI: its Agent result is plain text without the CLI's hand-back frame.
- `mise run e2e`: the end-to-end suite in `web/e2e` ([Playwright](https://playwright.dev)): API tests with typed Connect clients and browser tests with one context per user, Dex sign-in included. It runs a fake agent that needs no Claude login (`deploy/fake-claude`, whose directives hold, crash or echo a turn, or ask before a call), and maps `https://github.com/e2e/<repo>.git` to its git server, which `web/e2e/forge.spec.ts` pairs with a fake GitHub and GitLab API (`web/e2e/forge.ts`, through `TEST_FORGE_URL`; its GitHub App endpoints and pages for `web/e2e/github-app.spec.ts`, GitLab's OAuth endpoints, enforcing scopes, for `web/e2e/gitlab-oauth.spec.ts`; it answers what a real GitLab CE 19.5.3 answered, but approves consents at once, never says `checking` for mergeability and doesn't enforce a project's merge rules unless a test sets `blocked`; test what the web shows for GitLab with a `gitlab.com` repository, as `gitlab-oauth.spec.ts` does: `FORGE_HOSTS=github.com=gitlab` in `forge.spec.ts` keeps GitHub's URLs, which once hid a header that never showed on a real GitLab); `E2E_SCREENSHOTS=1` also regenerates the README's pictures, `docs/screenshots/thread.png` and `pull-request.png`: `E2E_SCREENSHOTS=1 mise run e2e -- -g "the README"`. Every run gets a world of its own, derived from the checkout path, so checkouts run it side by side: namespace `banda-e2e-<run>` (sandboxes, volumes, a git server with the fixture repos, Dex), database `banda_e2e_<run>`, the images `localhost/banda-sandbox:fake-<run>` and `localhost/banda-login:fake-<run>` (the login environments', with the same fake `claude`) and APIs on free ports; it is deleted afterwards (`E2E_KEEP=1` keeps it). `E2E_IMAGE` and `E2E_LOGIN_IMAGE` name fake images built already, which it loads instead (CI). Arguments go to Playwright: `mise run e2e -- e2e/fork.spec.ts`, `mise run e2e -- -g "race"`. CI runs only the tests tagged `@key`, the main journeys and the security guarantees, on pushes to `main`: `mise run e2e -- --grep @key`. `E2E_WORKERS` (default 4) sets the parallelism. Failures leave a trace, the threads' last events and the API log (`/tmp/banda-e2e-<run>/`): `pnpm -C web exec playwright show-report`.
- Schema changes are new files in `internal/store/migrations/`, `NNNN_what.sql` numbered after the last: the API applies each once, in order, in a transaction, and records it in `schema_migrations`; a failed one stops the API with its name. Never edit an applied migration (`0001_baseline.sql` is frozen); fix forward with a new one. Their tests (`internal/store/migrate_test.go`) use the dev Postgres on `localhost:5432` (`TEST_DATABASE_URL`) and skip without it.
- `deploy/long-thread.sh [N]` seeds a suspended thread with N events (default 20000) straight into Postgres and prints its id, to try or measure long threads without running an agent.

## UI conventions

The web app is a tool people work in: familiar controls, dense, quiet. Reuse `web/src/lib/components/ui` and the theme tokens in `routes/layout.css`.

- Colors come from the theme tokens: `success`, `warning`, `info`, `diff-add`, `diff-del` for dots, tints and borders, their `-foreground` for text and icons. Raw Tailwind hues stay in the always-dark terminal block and the forges' own merged violet.
- `attention` marks what blocks a person, and only that; `warning` is caution. the `attention-dot` utility is for dots, which Highlighter outlines on a light page, `attention-surface` with `attention-foreground` for marks and counts, `attention-text` for text and icons on the page.
- There are three themes in `routes/layout.css`: Highlighter, the default, in `:root` and `.dark`, and Logbook and Common room under `data-theme`. A new token is set in every theme, fonts are `--theme-*` tokens, and `lib/themes.ts` lists the themes.
- A theme's structure uses the `highlighter:`, `logbook:` and `common-room:` variants, not branches in a script. The `marker` is a solid fill because `e2e/ui.spec.ts` reads `background-color`.
- Text sits at 4.5:1 or better in every theme and mode (`e2e/ui.spec.ts` measures it).
- Status shows as a `StatusDot` and a word. No colored bars on a card's edge.
- Labels are sentence case at `text-xs`, never tiny tracked capitals.
- Motion says something changed, under 300ms, on named properties (no `transition-all`). `layout.css` handles `prefers-reduced-motion`.
- A truncated string gets a `title`. Long unbroken text wraps by itself (`overflow-wrap` on `html`).
- A destructive action asks with the browser's `confirm()`.
- Controls are 36px tall on touch screens (`pointer-coarse:`), and composer chips scroll sideways on a phone. A small inline control takes the `hit` utility; in the chat, where each item clips at its box, `pointer-coarse:` padding.
- Destructive actions ask through `confirmAction` (`confirm-dialog.svelte`), whose button names the action, not the browser's `confirm()`.
- Check a change on a phone and in light mode: the dev default is dark.
