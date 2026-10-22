# Banda

A web app where a team builds software together with coding agents. Each thread is one owner, one Claude Code agent and one isolated Kubernetes sandbox with the repo checked out on its own branch. Teammates can browse each other's threads, post into them and mention each other. See [VISION.md](VISION.md) for why, [PLAN.md](PLAN.md) for the architecture, decisions and status, [todo.md](todo.md) for what's left and [docs/deployment.md](docs/deployment.md) for deployment options.

![A thread with the diff pane](docs/screenshots/thread.png)

## Prerequisites

- [mise](https://mise.jdx.dev): `mise install` provides go, node, pnpm, buf, kind, kubectl, helm, podman, jq and actionlint.
- Podman with the default machine (`podman machine init`, at least 8 CPUs and 16 GiB to share it with other projects). `mise.toml` points `podman` and `kind` at its root connection. For Docker (OrbStack, Docker Desktop), set `KIND_EXPERIMENTAL_PROVIDER=docker`, `CONTAINER=docker` and `RUNNER_API_URL=http://host.docker.internal:8787` under `[env]` in a `mise.local.toml`; with Docker Desktop also `HOST_IP` (the host's address as pods see it) for the sandbox policies and `mise run e2e`. On Linux with Docker, `KIND_EXPERIMENTAL_PROVIDER=docker` and `CONTAINER=docker` suffice: the host is the gateway of kind's network (CI runs it that way).

## Dev loop

Everything is a mise task (`mise tasks` lists them). Settings come from the environment, e.g. `DEV_AUTH=1 mise run api`.

1. `mise run up` starts the Podman machine and the kind cluster (agent-sandbox, Postgres on `localhost:5432`, Dex), creating them and the sandbox image the first time, then the web server (`:5173`) and the API (`:8787`). It serves your LAN address; `PUBLIC_HOST=localhost mise run up` serves this machine only. Rerun it to restart.
2. Open the URL it prints, on this machine too (the sign-in cookie belongs to that address), sign in, create a thread.
3. The first time, the thread shows "Claude Code is not logged in". Click **Log in to Claude Code**, open the URL shown in the terminal, sign in with your Claude subscription and paste the code back. The login is kept in your personal volume and shared by all your threads. You can also log in from Settings, which offers an Anthropic API key instead of the subscription.
4. `mise run down` stops the servers and the kind node.

Every thread leaves a sandbox behind until it is settled and pushed. `mise run clean` deletes the sandboxes, and their volumes, of threads with a repository nobody touched for `CLEAN_HOURS` (default 24); a later prompt starts a fresh checkout.

In the app, Cmd/Ctrl-K opens the command palette and `?` lists the keyboard shortcuts.

`mise run image` rebuilds the sandbox image after changing `cmd/runner` or `deploy/sandbox.Dockerfile`; `mise run gen` regenerates `gen/` from `proto/`. Server logs are in `/tmp/banda-run/`.

For private repos, and for the auto-push after every turn on https remotes, add a `GIT_TOKEN` secret in Settings. Secrets are environment variables in all your threads, applied when a thread's environment next starts, and redacted from everything the agent reports. Team secrets, also in Settings, go to every thread of every teammate, so shared credentials (a kubeconfig, database URLs, observability tokens) need no setup; anyone can replace or delete them, Settings shows who set each last, and a personal secret of the same name wins. Every agent can use them: prefer read-only credentials. An admin-made fallback below both:

```sh
kubectl create secret generic banda-git -n banda --from-literal=GIT_TOKEN=...
```

## GitHub and GitLab

Once a thread's branch is pushed, its owner opens a pull request (GitLab: merge request) from the header, or links the one already open for the branch. Everyone then sees it in the thread: a chip with its state, checks, review and conflicts (also on sidebar rows and team board cards), a **Pull request** tab with the description, reviewers, checks, mergeability and the conversation, and a quiet line in the chat when it is opened, its checks fail, it is approved or changes are requested, merged or closed, which the agent also reads with its next prompt. The owner edits it, marks it draft or ready, asks for reviews and merges it; anyone with a token of their own comments and replies. With a token, the repository chip of a new thread also lists your repositories on the forge, filtered as you type. Agents read it with the `read_pull_request` MCP tool and comment on their own thread's with `comment_pull_request`.

![The Pull request tab](docs/screenshots/pull-request.png)

Banda acts with each person's own token, from their secrets in Settings (the same ones their threads get, so a personal secret beats a team one):

| Forge | Secret | Token |
|---|---|---|
| GitHub, GitHub Enterprise Server | `GIT_TOKEN` | Fine-grained: Contents read and write (pushes), Pull requests read and write, Commit statuses read, Checks read (Actions read for workflow runs), Metadata read. Classic: `repo`. |
| GitLab, self-hosted GitLab | `GITLAB_TOKEN`, else `GIT_TOKEN` | A personal access token with the `api` scope (`write_repository` alone pushes but can't open merge requests). |

github.com and gitlab.com are known; name self-hosted hosts in `FORGE_HOSTS`, e.g. `FORGE_HOSTS=git.corp.example=gitlab,ghe.corp.example=github`. The API polls linked pull requests every `PULL_REQUEST_POLL` (default `30s`) while someone has the thread open and ten times slower otherwise, with conditional requests on GitHub; no webhooks are needed. In the cluster the API needs `get` on Secrets in the sandbox namespace to read the tokens. Without a known forge or a token, the header links to GitHub's compare page instead.

## Permissions

The permission chip in the composer sets what the agent asks for in that thread: nothing (bypass, the default), edits and commands (ask), commands other than edits (accept edits), or everything until you approve its plan (plan). Requests appear in the chat; you answer them, or your teammates once you trust them. You can change a command or an edit before allowing it. "Always allow" applies to that tool, or to a narrower rule such as `npm test …` or edits in one directory, for the rest of the thread; the composer's "Allowed" chip lists them and takes them back. When the agent asks a question, everyone can vote on the options; your answer (or a trusted teammate's) is what it gets.

## Profiles

The profile chip in the composer sets who the agent works with in that thread: **Engineer** (Claude Code as it is, the default), **Specification** (talks through what you want in plain language and writes it up as a spec with you; once you confirm it, an Engineer sub-thread builds it, and the agent tells you what came of it), **DevOps** (explores and explains production with the credentials in your environment, read-only first, asking before any change) or **Business** (does business work for you, such as documents, data and automation, and shows results, not code). A profile changes how the agent talks and works with you, never what it may do. Settings sets the profile new threads start with; the owner switches it in a thread, applied once the agent's turns have ended.

## Work without a repository

For work without code, pick **No repository** in the composer's repository chip. The agent starts in an empty folder in the thread's environment; the Files tab and the terminal work on it, but there is no diff, push or pull request. The folder lives only in that environment, so settling the thread never deletes it. A fork gets the conversation but not the files, and sub-threads have no repository either.

## Preparing a repo

Before the agent starts, a thread installs the tools from the repo's `mise.toml`, `.mise.toml` or `.tool-versions` with [mise](https://mise.jdx.dev), then runs `.banda/setup.sh` with bash in the checkout (once per thread; again when the script changes; 15 min timeout). The chat shows each step. If one fails, the thread stops with its output; the terminal still works, and **Retry** runs the setup again.

`.banda/profiles/<name>.md` (`engineer`, `spec`, `devops` or `business`) replaces that profile's text in the agent's system prompt for the repo. It is read whenever the agent starts, so an edit applies from its next start (a resume, or a change of model, effort or profile).

## Sign in

The cluster runs [Dex](https://dexidp.io), a local OIDC provider, reached through the web server at `/dex` (in the cluster, through the API: see Install). Sign in as `alice`, `bob` or `carol` (`@example.com`) with the password `password`. `DEV_AUTH=1 mise run api` skips sign-in and trusts the `X-Banda-User` header instead (default user `dev`).

To use another OIDC provider, set these before `mise run api` (or when running `cmd/api`) and register `$PUBLIC_URL/auth/callback` as its redirect URI:

| Variable | Meaning |
|---|---|
| `OIDC_ISSUER` | Issuer URL, which serves `/.well-known/openid-configuration`. Enables sign-in. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Client credentials. |
| `PUBLIC_URL` | Origin the browser uses, default `http://localhost:5173`. |
| `SESSION_SECRET` | Signs the session cookie. If empty, a random one is used and sessions end when the API restarts. |

Without `OIDC_ISSUER` the API only starts with `DEV_AUTH=1`. Other API settings: `DATABASE_URL`, `SANDBOX_NAMESPACE`, `SANDBOX_IMAGE`, `RUNNER_API_URL`, `IDLE_SUSPEND_AFTER` (default `30m`, checked every `IDLE_SWEEP_EVERY`, default `1m`), `LOGIN_IDLE_AFTER` (default `10m`: login environments without a terminal), `DELETE_SETTLED_AFTER` (default `168h`: settled environments whose work is pushed), `MAX_SUBTHREAD_DEPTH` (default `3`) and `MAX_AGENT_THREADS` (default `8`: agents' sub-threads, see PLAN.md), `OVERLAP_WINDOW` (default `168h`: how recently two threads must have changed for shared files to count as an overlap; `0` turns overlaps off), `SANDBOX_EGRESS_ALLOW` (comma-separated CIDRs sandboxes may reach besides the internet, such as production; see Sandbox isolation), `DEX_URL` (a bundled Dex, e.g. `http://dex.banda.svc:5556`, whose issuer is `$PUBLIC_URL/dex`: the API serves it at `/dex/` and reaches it there directly; the chart sets it), `FORGE_HOSTS` and `PULL_REQUEST_POLL` (see GitHub and GitLab), `TEST_SUSPEND_PAUSE` (tests only: holds every suspend between the thread's status and its pod), `TEST_FORGE_URL` (tests only: a fake forge API in place of every forge's), `ADDR` (default `:8787`), `WEB_DIR` (serve a web build), `WATCH_BUFFER` (default `256`: events a thread stream may lag behind before it catches up from the database; small for tests).

The Browser tab shows apps running in a thread's environment from a second origin: `PREVIEW_ADDR` (default `:8788`) is where the API serves it, `PREVIEW_URL` the URL browsers use for it, by default the host they reach Banda on with that port. Keep it on the same site as Banda (another port, or a sibling subdomain such as `preview.banda.example.com`): the preview signs in with a SameSite cookie. Previewed apps run code from the sandboxes on that site; outside a trusted LAN serve Banda over https (its cookies are then `__Host-`) with the preview on a sibling subdomain.

## Tests

- `mise run test`: Go unit tests (state machine, search queries, inbox and team fan-out, team activity summaries, agent prompt and review rendering, Claude stream mapping (plans and subagents included), sessions, MCP transcript, sub-thread reports and the guard on waking a parent, error mapping, MCP instructions, pull request links and auto-push against a local origin, the GitHub and GitLab providers against fake APIs (create, checks, reviews, comments, merge, errors, conditional requests) and which pull request changes reach the chat, secret names and the order of their sources, repository URLs, egress allowlists, redaction, setup steps, fork patches and prompts, preview tunnel, ports and proxy, profile texts and repo overrides). `node web/src/lib/events.check.ts` checks the web event reducer (tool grouping, subagent nesting, the plan, autoPush lines, fork notes, sub-thread lines, that 20k tool calls stay fast, and that history loaded in pages matches a full replay, permission cards, question votes and `#seq` jumps included) and diff parsing, `node web/src/lib/inbox.check.ts` which inbox changes notify and how sub-threads nest.
- `mise run e2e`: the end-to-end suite in `web/e2e` ([Playwright](https://playwright.dev)): API tests with typed Connect clients and browser tests with one context per user, Dex sign-in included. It runs a fake agent that needs no Claude login (`deploy/fake-claude`, whose directives hold, crash or echo a turn, or ask before a call), and maps `https://github.com/e2e/<repo>.git` to its git server, which `web/e2e/forge.spec.ts` pairs with a fake GitHub and GitLab API (`web/e2e/forge.ts`, through `TEST_FORGE_URL`); `E2E_SCREENSHOTS=1` also saves `docs/screenshots/pull-request.png`. Every run gets a world of its own, derived from the checkout path, so checkouts run it side by side: namespace `banda-e2e-<run>` (sandboxes, volumes, a git server with the fixture repos, Dex), database `banda_e2e_<run>`, the image `localhost/banda-sandbox:fake-<run>` and APIs on free ports; it is deleted afterwards (`E2E_KEEP=1` keeps it). Arguments go to Playwright: `mise run e2e -- e2e/fork.spec.ts`, `mise run e2e -- -g "race"`. `E2E_WORKERS` (default 4) sets the parallelism. Failures leave a trace, the threads' last events and the API log (`/tmp/banda-e2e-<run>/`): `pnpm -C web exec playwright show-report`.
- `deploy/long-thread.sh [N]` seeds a suspended thread with N events (default 20000) straight into Postgres and prints its id, to try or measure long threads without running an agent.

## Install

Banda needs a Kubernetes cluster with a CNI that enforces NetworkPolicy (Calico, Cilium; kind's kindnet does) and the [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller, which is cluster-wide and not part of the chart:

```sh
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.7/sandbox.yaml
```

Everything else is the Helm chart `deploy/helm/banda`, released to `oci://ghcr.io/jhoogstraat/charts/banda` with images `ghcr.io/jhoogstraat/banda-api` and `banda-sandbox` (amd64 and arm64) of the same version. Its [values](deploy/helm/banda/values.yaml) are the settings above: `publicUrl`, `ingress.*` (Banda and the preview on sibling hosts, with TLS), `oidc.*` or `dex.*`, `postgres.*` (bundled, or `postgres.enabled=false` and your own `DATABASE_URL`), `podCIDR` (your pod network, for the ingress policies), `egressAllow`, and `api.env` for the rest.

```sh
helm install banda oci://ghcr.io/jhoogstraat/charts/banda -n banda --create-namespace \
  --set ingress.enabled=true,ingress.host=banda.example.com,ingress.previewHost=preview.banda.example.com,ingress.tlsSecret=banda-tls \
  --set oidc.issuer=https://login.example.com,oidc.clientSecret=... --set podCIDR=10.42.0.0/16
```

Register `$PUBLIC_URL/auth/callback` with your provider. To try it without one, `--set dex.enabled=true` with `dex.users` (see `deploy/kustomize/kind/values.yaml`): the API serves Dex at `$PUBLIC_URL/dex`, so it needs no host of its own (docs/deployment.md, "Sign-in with the bundled Dex"). The session secret, Postgres password and Dex client secret are generated on install and kept on upgrades.

Without Helm: every release has `banda.yaml`, the chart rendered as the Kustomize base renders it, and `deploy/kustomize/base` inflates the chart for Kustomize (`kubectl kustomize --enable-helm`, which runs `helm`); `deploy/kustomize/example` patches it for a real cluster. Neither can keep generated secrets, so both read their settings from a Secret you create first:

```sh
kubectl create namespace banda
kubectl -n banda create secret generic banda-api --from-literal=SESSION_SECRET=$(openssl rand -hex 32) \
  --from-literal=POSTGRES_PASSWORD=$(openssl rand -hex 16) --from-literal=OIDC_ISSUER=... --from-literal=OIDC_CLIENT_ID=... --from-literal=OIDC_CLIENT_SECRET=...
kubectl -n banda apply -f https://github.com/jhoogstraat/banda/releases/download/v1.0.0/banda.yaml
```

The Secret's settings apply where the manifest sets none: it sets `PUBLIC_URL` to `http://localhost:8787` (a port-forward) and `SANDBOX_IMAGE` to the release's, so patch the first for an ingress, as the example overlay does, and add the Ingress.

### In kind

`mise run deploy` builds the API image (with the web build), loads it into kind and installs the chart with the API and Dex (`deploy/kustomize/kind/values.yaml`, also `kubectl kustomize --enable-helm deploy/kustomize/kind`). Reach it with `kubectl --context kind-banda -n banda port-forward svc/banda-api 8787 8788` and sign in at `http://localhost:8787` as alice. Run either `mise run api` or `mise run deploy`, not both: they share the database and the sandboxes. `mise run up` removes the in-cluster API again and points Dex back at the web server. `mise run chart-check` lints the chart and renders it with every values file and overlay.

## Sandbox isolation

Sandboxes run the repo's code and the agent, so NetworkPolicies let them reach only DNS, the internet and the API (`deploy/helm/banda/templates/policies.yaml`), and Postgres and Dex take connections only from the API and from outside the cluster (their templates); the chart installs them, in dev through `mise run up`. An API on this machine (`mise run api`) must listen on a port in 8700–8999 for sandboxes in `banda` to reach it.

Private networks, such as production or a private git host, are closed too until `SANDBOX_EGRESS_ALLOW` lists them: comma-separated CIDRs (`10.20.0.0/16,10.30.1.5/32`; NetworkPolicies can't name hosts). The API applies them when it starts, as the NetworkPolicy `sandboxes-allow` in `SANDBOX_NAMESPACE` (set it in the Secret `banda-api` in the cluster); empty removes it, and a bad CIDR stops the API with an error. Postgres, Dex and the API in the cluster (except on its runner port) still refuse sandboxes inside an allowed range, and the Kubernetes API and kubelets refuse them without a token. Everything else in the range is open, so allow only what agents need: a range over the cluster's own networks opens its other pods and NodePorts, and in dev one over `host.containers.internal` (192.168.127.254) the host's ports, Postgres's among them. For a production Kubernetes cluster, keep it separate from Banda's: put a kubeconfig for it in a secret and allow its API server's address if that is private. Don't give sandboxes a service account in Banda's cluster.

For a shared deployment install the chart (see Install): no NodePorts, a generated Postgres password, never `api.devAuth`, a CNI that enforces NetworkPolicy, and your cluster's pod network in `podCIDR` instead of kind's (`10.244.0.0/16`). The API still connects as Postgres's superuser. See PLAN.md, open risk 9.
