# Banda

A web app where a team builds software together with coding agents. Each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox with the repo checked out on its own branch. Teammates can browse each other's threads, post into them and mention each other. See [PLAN.md](PLAN.md) for the architecture, decisions and status.

![A thread with the diff pane](docs/screenshots/thread.png)

## Prerequisites

- [mise](https://mise.jdx.dev): `mise install` provides go, node, pnpm, buf, kind, kubectl, podman and jq.
- Podman with a rootful machine named `banda` (`mise run up` creates it). `mise.toml` points `podman` and `kind` at it. For Docker (OrbStack, Docker Desktop), set `KIND_EXPERIMENTAL_PROVIDER=docker`, `CONTAINER=docker` and `RUNNER_API_URL=http://host.docker.internal:8787` under `[env]` in a `mise.local.toml`.

## Dev loop

Everything is a mise task (`mise tasks` lists them). Settings come from the environment, e.g. `DEV_AUTH=1 mise run api`.

1. `mise run up` starts the Podman machine and the kind cluster (agent-sandbox, Postgres on `localhost:5432`, Dex), creating them and the sandbox image the first time, then the web server (`:5173`) and the API (`:8787`). It serves your LAN address; `PUBLIC_HOST=localhost mise run up` serves this machine only. Rerun it to restart.
2. Open the URL it prints, on this machine too (the sign-in cookie belongs to that address), sign in, create a thread.
3. The first time, the thread shows "Claude Code is not logged in". Click **Log in to Claude Code**, open the URL shown in the terminal, sign in with your Claude subscription and paste the code back. The login is kept in your personal volume and shared by all your threads.
4. `mise run down` stops the servers and the machine.

`mise run image` rebuilds the sandbox image after changing `cmd/runner` or `deploy/sandbox.Dockerfile`; `mise run gen` regenerates `gen/` from `proto/`. Server logs are in `/tmp/banda-run/`.

For private repos, create the secret before creating the thread:

```sh
kubectl create secret generic banda-git -n banda --from-literal=GIT_TOKEN=...
```

## Sign in

The cluster runs [Dex](https://dexidp.io), a local OIDC provider, reached through the web server at `/dex`. Sign in as `alice`, `bob` or `carol` (`@example.com`) with the password `password`. `DEV_AUTH=1 mise run api` skips sign-in and trusts the `X-Banda-User` header instead (default user `dev`).

To use another OIDC provider, set these before `mise run api` (or when running `cmd/api`) and register `$PUBLIC_URL/auth/callback` as its redirect URI:

| Variable | Meaning |
|---|---|
| `OIDC_ISSUER` | Issuer URL, which serves `/.well-known/openid-configuration`. Enables sign-in. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Client credentials. |
| `PUBLIC_URL` | Origin the browser uses, default `http://localhost:5173`. |
| `SESSION_SECRET` | Signs the session cookie. If empty, a random one is used and sessions end when the API restarts. |

Without `OIDC_ISSUER` the API only starts with `DEV_AUTH=1`. Other API settings: `DATABASE_URL`, `SANDBOX_NAMESPACE`, `SANDBOX_IMAGE`, `RUNNER_API_URL`, `IDLE_SUSPEND_AFTER` (default `30m`), `ADDR` (default `:8787`), `WEB_DIR` (serve a web build).

## Tests

- `mise run test`: Go unit tests (state machine, agent prompt, Claude stream mapping, sessions, MCP transcript, push URLs). `node web/src/lib/events.check.ts` checks the web event reducer.
- `mise run e2e`: drives the whole thread lifecycle with a fake agent that needs no Claude login (`deploy/e2e.sh`). It builds the fake sandbox image and runs a dev-auth API on `:8790`, next to the normal one.

## Run the API in the cluster

`mise run deploy` builds the API image (with the web build), loads it into kind and applies `deploy/banda.yaml`. Settings come from the optional Secret `banda-api` (the variables above). Run either `mise run api` or `mise run deploy`, not both. Reach it with `kubectl -n banda port-forward svc/banda-api 8787`.
