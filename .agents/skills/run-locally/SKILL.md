---
name: run-locally
description: Boot Banda locally for testing, reachable from the LAN - Podman machine, kind cluster, sandbox image, API and web dev servers, sign-in. Use when asked to run, start, restart, test or screenshot the app, or when the UI or API is down.
---

# Run Banda locally

The stack: a rootful Podman machine `banda` runs a kind cluster `banda` (agent-sandbox controller, Postgres on `localhost:5432`, Dex on `localhost:5556`, one sandbox pod per thread). The Go API (`:8787`) and the Vite web server (`:5173`) run on the Mac. Sandbox pods reach the API as `host.containers.internal:8787`.

All commands run from the repo root. Tools come from `mise` (`mise install` once).

## Rules

- Never run the real `claude` binary for tests: it uses the user's personal Claude subscription. For automated checks use the fake sandbox image (see "End-to-end check").
- Start long-running servers detached with `nohup … &`, not as harness background tasks: those are killed after their time limit.
- Do not push, and do not stop other Podman machines without asking: they run the user's other containers.

## 1. Podman machine

Podman on macOS runs only one VM at a time.

```sh
podman machine list
```

- `banda` running: continue.
- Another machine running (usually `podman-machine-default`): ask the user before stopping it, it stops their containers. Then `podman machine stop podman-machine-default`.
- `banda` missing: `podman machine init banda --cpus 6 --memory 10240 --disk-size 80 --rootful --now`.

```sh
podman machine start banda
```

## 2. Cluster

If the cluster exists (`podman --connection banda-root ps -a` lists `banda-control-plane`), start its node and wait:

```sh
podman --connection banda-root start banda-control-plane
mise exec -- kubectl --context kind-banda wait --for=condition=Ready pod --all -A --timeout=300s
```

The agent-sandbox controller may show `Error` for a minute after a restart; it recovers by itself.

Otherwise create it and load the sandbox image (takes several minutes):

```sh
make cluster image
```

Rebuild the image (`make image`) after changing `cmd/runner` or `deploy/sandbox.Dockerfile`.

## 3. API and web, reachable from the LAN

`PUBLIC_HOST` is the address browsers use. With a LAN address the web server listens on all interfaces and proxies Dex at `/dex`, so other devices only need port 5173. Dex's issuer must match it, hence `make dex`.

```sh
H=$(ipconfig getifaddr en0)
make dex PUBLIC_HOST=$H
pkill -f "make api"; pkill -f "make web"
lsof -tiTCP:8787 -sTCP:LISTEN | xargs kill 2>/dev/null; lsof -tiTCP:5173 -sTCP:LISTEN | xargs kill 2>/dev/null
mkdir -p /tmp/banda-run
nohup make web PUBLIC_HOST=$H > /tmp/banda-run/web.log 2>&1 < /dev/null &
# The API discovers Dex through the web server, so start it second.
until curl -sf -o /dev/null http://$H:5173/dex/.well-known/openid-configuration; do sleep 1; done
nohup make api PUBLIC_HOST=$H > /tmp/banda-run/api.log 2>&1 < /dev/null &
```

For this Mac only, leave out `PUBLIC_HOST` everywhere (it defaults to `localhost`) and run `make dex` without it.

## 4. Check

```sh
curl -s -o /dev/null -w "web %{http_code}\n" http://$H:5173/
curl -s -o /dev/null -w "login %{http_code}\n" http://$H:5173/auth/login   # 302 to /dex/auth
tail -2 /tmp/banda-run/api.log                                              # "listening on :8787"
```

Open `http://<address>:5173` and sign in as `alice`, `bob` or `carol` (`@example.com`), password `password`. Use that same address on this Mac too: the session cookie belongs to the address used at sign-in. A new thread shows "Claude Code is not logged in" until the user logs in with **Log in to Claude Code** (once per user).

## End-to-end check (no Claude login needed)

```sh
make image TARGET=fake IMAGE=localhost/banda-sandbox:fake
nohup make api AUTH=dev IMAGE=localhost/banda-sandbox:fake ADDR=:8790 RUNNER_API_URL=http://host.containers.internal:8790 > /tmp/banda-run/e2e-api.log 2>&1 < /dev/null &
API=http://localhost:8790 make e2e
```

It runs next to the normal API with its sandboxes calling back to `:8790`; stop it afterwards (`lsof -tiTCP:8790 -sTCP:LISTEN | xargs kill`). `make test` runs the Go unit tests.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `connection refused` from kubectl or podman | The `banda` machine is down: step 1. |
| Vite overlay "Failed to load url …/_tmp_…" | Stale error after `shadcn-svelte add`: restart the web server. |
| Other devices can't connect | macOS firewall: allow incoming connections for `node`. Check the address still matches `ipconfig getifaddr en0`; if it changed, redo step 3. |
| Sign-in loops or "invalid issuer" | `make dex`, `make web` and `make api` used different `PUBLIC_HOST` values: redo step 3. |
| Thread stuck in `starting` | `mise exec -- kubectl --context kind-banda -n banda get pods`; the pod is `t-<thread id>`. Check its logs and `/tmp/banda-run/api.log`. |

## Stop

```sh
pkill -f "make api"; pkill -f "make web"
podman machine stop banda
```
