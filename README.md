# Banda

A web app where a team builds software together with coding agents. Each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox with the repo checked out on its own branch. Teammates can browse each other's threads, post into them and mention each other. See [PLAN.md](PLAN.md) for the architecture, decisions and status.

![A thread with the diff pane](docs/screenshots/thread.png)

## Prerequisites

- [mise](https://mise.jdx.dev): `mise install` provides go, node, pnpm, buf, kind and kubectl.
- A container runtime. The Makefile defaults to Podman with a rootful machine named `banda`:

  ```sh
  podman machine init banda --cpus 6 --memory 10240 --disk-size 80 --rootful --now
  ```

  For Docker (OrbStack, Docker Desktop), pass `CONTAINER=docker` to every `make` call. Sandbox pods reach the API on your machine as `host.containers.internal` (Podman) or `host.docker.internal` (Docker); override with `RUNNER_API_URL=...`.

## Dev loop

1. `make cluster image` creates the kind cluster (agent-sandbox, Postgres on `localhost:5432`, Dex on `localhost:5556`) and loads the sandbox image.
2. `make api` in one terminal, `make web` in another.
3. Open http://localhost:5173, sign in, create a thread.
4. The first time, the thread shows "Claude Code is not logged in". Click **Log in to Claude Code**, open the URL shown in the terminal, sign in with your Claude subscription and paste the code back. The login is kept in your personal volume and shared by all your threads.

For private repos, create the secret before creating the thread:

```sh
kubectl create secret generic banda-git -n banda --from-literal=GIT_TOKEN=...
```

## Sign in

`make cluster` also runs [Dex](https://dexidp.io), a local OIDC provider. Sign in as `alice`, `bob` or `carol` (`@example.com`) with the password `password`. `make api AUTH=dev` skips sign-in and trusts the `X-Banda-User` header instead (default user `dev`).

To use another OIDC provider, set these before `make api` (or when running `cmd/api`) and register `$PUBLIC_URL/auth/callback` as its redirect URI:

| Variable | Meaning |
|---|---|
| `OIDC_ISSUER` | Issuer URL, which serves `/.well-known/openid-configuration`. Enables sign-in. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Client credentials. |
| `PUBLIC_URL` | Origin the browser uses, default `http://localhost:5173`. |
| `SESSION_SECRET` | Signs the session cookie. If empty, a random one is used and sessions end when the API restarts. |

Without `OIDC_ISSUER` the API only starts with `DEV_AUTH=1` (what `make api AUTH=dev` sets). Other API settings: `DATABASE_URL`, `SANDBOX_NAMESPACE`, `SANDBOX_IMAGE`, `RUNNER_API_URL`, `IDLE_SUSPEND_AFTER` (default `30m`), `ADDR` (default `:8787`), `WEB_DIR` (serve a web build).

## Tests

- `make test`: Go unit tests (state machine, agent prompt, Claude stream mapping, sessions, MCP transcript, push URLs). `node web/src/lib/events.check.ts` checks the web event reducer.
- `make e2e`: drives a running system through the whole thread lifecycle with a fake agent that needs no Claude login. Run it against the fake image with dev auth:

  ```sh
  make image TARGET=fake IMAGE=localhost/banda-sandbox:fake
  make api AUTH=dev IMAGE=localhost/banda-sandbox:fake   # separate terminal
  make e2e
  ```

## Run the API in the cluster

`make deploy` builds the API image (with the web build), loads it into kind and applies `deploy/banda.yaml`. Settings come from the optional Secret `banda-api` (the variables above). Run either `make api` or `make deploy`, not both. Reach it with `kubectl -n banda port-forward svc/banda-api 8787`.
