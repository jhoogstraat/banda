# Banda

A web app where a team builds software together with coding agents: each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox. See [PLAN.md](PLAN.md) for the architecture and milestones.

## Prerequisites

- [mise](https://mise.jdx.dev) (`mise install` provides go, node, pnpm, buf, kind and kubectl).
- A container runtime. Docker (OrbStack, Docker Desktop) works as is. Pods must reach the API on the host:
  - Colima: `make api RUNNER_API_URL=http://host.lima.internal:8787`.
  - Podman: pass `CONTAINER=podman` to `make cluster image`, and `make api RUNNER_API_URL=http://host.containers.internal:8787`. Untested so far; if pods can't resolve that name, try gvproxy's host IP `192.168.127.254`.

## Dev loop

1. `make cluster image` creates the kind cluster (agent-sandbox plus Postgres on `localhost:5432`) and loads the sandbox image.
2. `make api` in one terminal, `make web` in another.
3. Open the app and create a thread.
4. When the thread shows the login notice, run `make login SANDBOX=t-<id>` and follow the prompts. Once per user.

For private repos, create the secret before creating the thread:

```sh
kubectl create secret generic banda-git -n banda --from-literal=GIT_TOKEN=...
```
