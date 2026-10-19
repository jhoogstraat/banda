---
name: run-locally
description: Boot Banda locally for testing, reachable from the LAN - Podman machine, kind cluster, sandbox image, API and web dev servers, sign-in. Use when asked to run, start, restart, test or screenshot the app, or when the UI or API is down.
---

# Run Banda locally

Everything goes through `deploy/dev.sh`, run as mise tasks from the repo root. Each task is idempotent: rerun it to restart.

```sh
mise run up     # Podman machine, kind cluster, API (:8787) and web (:5173), on the LAN address
mise run e2e    # end-to-end check with the fake agent on :8790, next to the normal API
mise run down   # stop the servers and the Podman machine
```

`PUBLIC_HOST=localhost mise run up` serves this Mac only. The first `up` creates the cluster and sandbox image and takes several minutes. Run `mise run image` after changing `cmd/runner` or `deploy/sandbox.Dockerfile`, `mise run test` for the Go unit tests.

`up` prints the URL. Sign in as `alice`, `bob` or `carol` (`@example.com`), password `password`. Use that same address on this Mac too: the session cookie belongs to the address used at sign-in. A new thread shows "Claude Code is not logged in" until the user logs in with **Log in to Claude Code** (once per user).

## Rules

- Never run the real `claude` binary for tests: it uses the user's personal Claude subscription. Use `mise run e2e`.
- If `up` stops because another Podman machine is running, ask the user before stopping it: it runs their other containers.
- Do not push.

## Troubleshooting

Server logs are in `/tmp/banda-run/` (`api.log`, `web.log`, `e2e-api.log`).

| Symptom | Fix |
|---|---|
| Vite overlay "Failed to load url …/_tmp_…" | Stale error after `shadcn-svelte add`: `mise run up`. |
| Other devices can't connect | macOS firewall: allow incoming connections for `node`. If the LAN address changed, `mise run up`. |
| Thread stuck in `starting` | `kubectl --context kind-banda -n banda get pods`; the pod is `t-<thread id>`. Check its logs and `api.log`. |
