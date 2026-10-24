# To do

What is left before Banda runs fully, ordered by when it's needed. Measured against "What done looks like" in [VISION.md](VISION.md); what is built is listed under Status in [PLAN.md](PLAN.md), deployment options in [docs/deployment.md](docs/deployment.md). Checked 2026-10-22.

## Where we are

Feature work for v1 is essentially complete and covered by the e2e suite with the fake agent: threads, sandboxes and their lifecycle, multiplayer (mentions, chat mode, turn requests, trust, handoffs), permissions, diff review, forks and races, sub-threads and the MCP server, profiles, search, the team board, decisions, preview, files and terminal, auto-push, sandbox isolation, and GitHub/GitLab pull requests through their APIs (against a fake forge). Packaging (Helm, Kustomize, release manifest), CI and releases exist.

What isn't proven is **real use**: no real Claude Code turn has run through Banda yet, and it has never run anywhere but in kind.

| Vision goal | State |
|---|---|
| 1. SSO, Claude login once, works on real repos | Built; real login and turns unverified |
| 2. Easier than local terminals | Built; needs real use to judge |
| 3. Never loses work, no manual cleanup | Built (auto-push, settled deletion); `mise run clean` is still dev-only |
| 4. Team habits: mentions, reviews, forks, handoffs | Built |
| 5. A non-engineer gets work done | Profiles built; untried with a person |
| 6. One person installs it from one page | Helm chart, Kustomize and release manifests (docs/deployment.md); never installed outside kind |

## Roadmap (2026-10-24)

Every risk left is the same one: the real thing behaves differently from the fake (agent, forge, cluster, terms). So no new features until real use asks for one. The milestones swap a fake for the real thing, cheapest first. Agents do the work marked for them; the rest needs Joshua.

1. **The real CLI, without a subscription** (agents, now). All 32 specs rest on `deploy/fake-claude`; if it drifts from the real CLI, the suite proves nothing. `docs/research/claude-headless.md` already ran the real binary against a local fake Messages API. Turn that into contract tests: the pinned `claude` from the sandbox image, a scripted fake Messages API, and the runner's mapping checked per scenario (the "verify against the real CLI" list in Phase 1). They run in a container, never on the Mac, where the login sits in the keychain. Done when they pass in CI and bumping `CLAUDE_VERSION` is a one-line PR that runs them. Done 2026-10-24: `mise run contract` passes in CI; the plan follows the CLI's task tools (agents, now).
2. **Dogfood on this Mac** (Joshua; agents fix). The Phase 1 steps that need a person: your own Claude login, the GitHub App on github.com, bob on the LAN, a week of real work, ideally Banda building Banda. Each annoyance becomes a failing e2e test and a fix within a day.
3. **Cold install** (agents rehearse it, Joshua runs it on the team cluster). Follow docs/deployment.md "Install" word for word on a fresh kind cluster, as a stranger would, and fix every gap in the page; an opt-in ServiceMonitor and alert rules for internal errors and full volumes; a recommendation on multi-node volumes (pin a user's pods to one node, or RWX). Done when one page takes an empty cluster to a signed-in team (vision goal 6).
4. **Comply, then launch** (decided 2026-10-24: no question to Anthropic). Banda follows the terms as written, and strictly where they are unclear (docs/research/claude-terms.md, Decision): on a subscription only the owner starts turns, teammate turns and trust need an API key (agents, now); the Commercial Terms are a prerequisite on the install page. The launch needs nothing more from Anthropic.

**Features**, until the dogfood week ends:

- **Add** only what turns a signal the real CLI already sends into honest state (principles 6 and 7). Each comes with a contract test on milestone 1's fake Messages API, so it waits for milestone 1. The runner drops `rate_limit_event`, `compact_boundary` and every `api_retry` except auth errors today:
  - usage limits: each person has one limit, shared by all their threads (one login per person) and spent faster in Banda than in a terminal, since that's the point. Show the reset time once per person (the board, their threads); hold queued prompts, races and sub-threads; resume when the limit resets; a teammate can carry on in a fork on their own subscription;
  - retries and overload as one calm status line, not a spinner that says nothing;
  - compaction as a line in the chat;
  - the CLI's own slash commands and skills offered in the composer.
- **Freeze** the forges (no new forge feature until github.com and a GitLab are verified) and workload identity (no new recipe until one has worked against a real vault).
- **Cut** after the week whatever nobody used. Candidates: the Pull request tab's conversation and merging (the forge's page is one click away), person pages beside the board's columns, and whichever of the five "what needs me" views (Needs input, Decisions, the board's Needs them, the morning brief, notifications) nobody opens.

On hold until real use asks for them: waking a parent that is starting or errored, accepting a handoff during the briefing, free replies and option previews on permission prompts, per-thread preview origins, webhooks, several replicas, other harnesses.

## Phase 1: run it for real on this Mac (Podman + kind)

The cluster `banda` already runs in `podman-machine-default` (agent-sandbox, Postgres, Dex).

- [ ] `mise run up`, sign in as alice, create a thread on a real repo.
- [ ] Log in to Claude Code with your own subscription (Settings or the thread's terminal); check the login survives a suspend and resume, and is shared by a second thread.
- [ ] Set up the GitHub App from Settings and connect (or add a `GIT_TOKEN` secret); check a real turn auto-pushes the branch and the Push button turns into "Create a pull request".
- [ ] Pull requests against the real forges (only the fake is tested): create, checks from GitHub Actions, a review, a merge on github.com; the same on a GitLab with `FORGE_HOSTS`; check the token scopes in docs/usage.md are enough and no more.
- [ ] Verify against the real CLI what is only sourced from docs (PLAN.md, "Verified only partly" and "Not yet"):
  - [x] a real turn streams correctly (tool calls, thinking, subagents): `mise run contract`. The subagent tool is `Agent`; background tasks are now off, or a subagent would start a turn of its own after the turn.
  - [ ] the TodoWrite plan: the pinned CLI offers no `TodoWrite` in `-p`, so the plan never shows. Decided: map the CLI's task tools (`TaskCreate`/`TaskUpdate`, `CLAUDE_CODE_ENABLE_TODO_TOOLS=1`) to the plan (agents, with the features below).
  - [x] folding a prompt into a running turn: `queued_turn_count` stays 0 even with a prompt queued, so the runner counts by `user_message_uuids` (a thread showed `awaiting_input` during a queued turn).
  - [x] permission modes: `ask`, `accept edits`, `plan` with a real `ExitPlanMode`, `AskUserQuestion` answers, `EnterPlanMode` (contract).
  - [x] switching profile, model and effort mid-thread with `--resume` and `--system-prompt-snapshot off` (contract: the resumed CLI sends them).
  - [x] the Banda MCP tools reached from the real CLI through the runner's `--mcp-config` (contract, a stub server with their names); the real server with a real model still comes with dogfooding.
- [ ] A second person on the LAN (bob) posts, mentions, requests a turn, forks; a handoff to bob with bob's own Claude login.
- [ ] Run a repo with `.banda/setup.sh` and a dev server; check the Browser tab and HMR. The runner turns the CLI's background tasks off (`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS`, so no turn starts after its end), which also takes Bash's `run_in_background`: check the agent still starts a dev server the Browser tab finds, and turn them back on (with a status for background work) if it doesn't.
- [ ] Use it for a week; write down what's annoying.
- [x] Run the API in the cluster instead of on the Mac (`mise run deploy`): signs in with Dex at `/dex` through the API.

## Phase 2: deployable

- [x] Sign-in in the cluster: the API serves the bundled Dex at `$PUBLIC_URL/dex` (`DEX_URL`); real OIDC providers as before.
- [x] Packaging: the Helm chart `deploy/helm/banda` as the one source, Kustomize base and overlays (`kind`, `example`) on top; the mise tasks and the e2e world install it (docs/deployment.md).
- [x] CI: Go vet and tests, `pnpm -C web check`, the `.check.ts` scripts, the generated code, the chart and both images on every pull request; releases push multi-arch images, the chart (OCI) and `banda.yaml` on a `v*` tag.
- [x] CI e2e on kind with Docker: runs the whole suite on every push to `main` (`deploy/dev.sh` takes the host from the kind node's default route; the job frees the runner's disk first; the harness closes each test's browser tabs, which had filled the 8 GB runner).
- [x] Postgres: generated password in a Secret, no NodePort outside kind, or your own database.
- [x] Postgres for real: the API connects as `banda_api` (owns its database, no superuser; made by the API pod's init container, also on upgrades), nightly `pg_dump` to a volume with rotation and a restore procedure (docs/deployment.md, "Postgres"). CloudNativePG or a managed database is the step up.
- [x] Ingress with TLS for `banda.<domain>` and `preview.banda.<domain>` in the chart (`ingress.*`); `PUBLIC_URL`, `PREVIEW_URL` follow.
- [x] Streams survive proxies: every server stream sends a keepalive every 25 s; the chart's `ingress.annotations` comment and the Kustomize example set ingress-nginx's timeouts.
- [ ] Ingress on a real cluster: confirm a quiet thread page stays connected for minutes behind the team's controller.
- [x] `strategy: Recreate` and resource requests on `banda-api`; a liveness probe.
- [x] Versioned schema migrations: `internal/store/migrations/NNNN_*.sql`, each once, under an advisory lock (docs/development.md).
- [ ] An install page: one document from empty cluster to signed-in team (vision goal 6). docs/deployment.md, "Install", was followed word for word on a fresh kind cluster with ingress-nginx and the bundled Dex (Helm and Kustomize, 2026-10-24); it needs a run on the team cluster with a real provider and TLS.
- [ ] Comply with the Claude terms as written (docs/research/claude-terms.md, Decision): teammate turns only on API keys is the last change.

## Open-source launch

- [x] MIT license, SECURITY.md, CONTRIBUTING.md, a README with the quick start and install options.
- [ ] Create `github.com/jhoogstraat/banda`, push `main`; enable private vulnerability reporting (SECURITY.md points to it).
- [ ] Tag `v0.1.0`; after the first release, make the GHCR packages `banda-api`, `banda-sandbox` and `charts/banda` public (GHCR creates them private), then check `helm install` from the README works on a fresh cluster.

## Phase 3: the team cluster

- [ ] A cluster dedicated to Banda, separate from production, with a CNI that enforces NetworkPolicy (Calico, Cilium); its pod network in the chart's `podCIDR`.
- [ ] Multi-node volumes: the per-user Claude volume and thread volumes are RWO. Pin a user's pods to one node, or use RWX storage.
- [x] Sandbox sizes as settings (`SANDBOX_CPU`, `SANDBOX_MEMORY`, … and the chart's `sandboxes:`).
- [ ] Size nodes for concurrent threads.
- [x] Clean up what lingers: token Secrets are owned by their Sandbox; pushed environments idle for `DELETE_IDLE_AFTER` (30 days) are deleted like settled ones. Token Secrets from before this change go in a sweep at API start (drop it once every install has started once).
- [ ] `SANDBOX_EGRESS_ALLOW` for the private networks agents need (git host, staging); a read-only kubeconfig as a team secret for DevOps, or better a kubeconfig with `tokenFile` and workload identity.
- [ ] Workload identity against the real systems: Banda on https, then each recipe in Settings (OpenBao/Vault, Infisical, AWS, GCP, Kubernetes) end to end, and a key rotation (docs/deployment.md); only the tokens themselves are tested so far.
- [x] Monitoring: `/metrics` on `METRICS_ADDR` (threads by status, sandboxes, runners, internal errors, uptime); logs on stdout. PVC disk use comes from the kubelet's `kubelet_volume_stats_*`, not Banda.
- [ ] Monitoring on the team cluster: the chart's opt-in ServiceMonitor and PrometheusRule (`monitoring.*`) are only rendered, never run against a Prometheus operator.

## Built 2026-10-22 (needs real use)

- [x] Settings in sections: Claude Code, Git identity (commits as you, not "Banda"), defaults for new threads, notifications per kind, theme, shortcuts.
- [x] Person pages (`/u/<id>`), names linking to them, and an account menu on your name.
- [x] Banda's own sign-in and signed-out page (`OIDC_PROVIDER_NAME`); the bundled Dex in Banda's look; a favicon.
- [x] Try the light theme and the Dex pages on a phone: every page in dark and light on a desktop and a phone. The thread composer's send button was off a phone's screen, Dex stayed dark under the light theme, amber labels were faint on light; `pages.spec.ts` checks a phone scrolls no page sideways.
- [x] A resume could delete the controller's new pod and lose the prompt (seen as a flaky `repoless.spec.ts` under load): `start()` now deletes only the pod that was there before.

## Later (not needed to run fully)

- [ ] Several API replicas: Postgres `LISTEN/NOTIFY` fan-out; the in-memory "suspend after the turn" moves to the database.
- [x] A GitHub App instead of personal tokens on GitHub: set up from Settings (manifest flow) or by hand, per-user OAuth with rotating refresh tokens, installation tokens for git in sandboxes scoped to the thread's repository (fake forge only).
- [ ] The GitHub App against github.com and a GitHub Enterprise Server: the manifest flow, installing, connecting, token expiry and refresh, pushes touching `.github/workflows`.
- [x] GitLab as an OAuth connection per user and host, beside GitHub's (`forge_connections`): a team application per host set up from Settings, two grants per user (`api` in the API, `write_repository` for sandboxes), rotating refresh tokens (fake forge only).
- [ ] The GitLab application against gitlab.com and a self-hosted GitLab: creating it in a group, the secret check, both consents, token refresh after 2 hours, revocation from GitLab's settings, a push with the git-only token.
- [ ] Webhooks as an add-on to polling for installs that can receive them.
- [x] Pull requests: search on the forge in the repo picker; reviewers and labels from lists; resolving review threads; spotting a pull request the agent opened itself (fake forge only, like the rest).
- [x] Reviewers and labels in the create dialog, not only the Pull request tab.
- [x] New threads and races with attachments; "Spawn sub-thread" with a profile chip and attachments; agents handing off through MCP (`handoff_thread`).
- [x] Files tab ignore rules without git; adding a repository to a repo-less thread.
- [ ] Per-thread preview origins (needs wildcard DNS).
- [ ] Codex and other harnesses, only after checking their terms (non-goal until Claude Code is proven).
