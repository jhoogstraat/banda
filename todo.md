# To do

What is left before Banda runs fully, ordered by when it's needed. Measured against "What done looks like" in [VISION.md](VISION.md); what is built is listed under Status in [PLAN.md](PLAN.md), deployment options in [docs/deployment.md](docs/deployment.md). Checked 2026-10-22.

## Where we are

Feature work for v1 is essentially complete and covered by the e2e suite with the fake agent: threads, sandboxes and their lifecycle, multiplayer (mentions, chat mode, turn requests, trust, handoffs), permissions, diff review, forks and races, sub-threads and the MCP server, profiles, search, the team board, decisions, preview, files and terminal, auto-push, sandbox isolation.

What isn't proven is **real use**: no real Claude Code turn has run through Banda yet, and it has never run anywhere but in kind.

| Vision goal | State |
|---|---|
| 1. SSO, Claude login once, works on real repos | Built; real login and turns unverified |
| 2. Easier than local terminals | Built; needs real use to judge |
| 3. Never loses work, no manual cleanup | Built (auto-push, settled deletion); `mise run clean` is still dev-only |
| 4. Team habits: mentions, reviews, forks, handoffs | Built |
| 5. A non-engineer gets work done | Profiles built; untried with a person |
| 6. One person installs it from one page | Helm chart, Kustomize and release manifests (README, Install); never installed outside kind |

## Phase 1: run it for real on this Mac (Podman + kind)

The cluster `banda` already runs in `podman-machine-default` (agent-sandbox, Postgres, Dex).

- [ ] `mise run up`, sign in as alice, create a thread on a real repo.
- [ ] Log in to Claude Code with your own subscription (Settings or the thread's terminal); check the login survives a suspend and resume, and is shared by a second thread.
- [ ] Add a `GIT_TOKEN` secret; check a real turn auto-pushes the branch and the Push button turns into "Create a pull request".
- [ ] Pull requests against the real forges (only the fake is tested): create, checks from GitHub Actions, a review, a merge on github.com; the same on a GitLab with `FORGE_HOSTS`; check the token scopes in README's table are enough and no more.
- [ ] Verify against the real CLI what is only sourced from docs (PLAN.md, "Verified only partly" and "Not yet"):
  - [ ] a real turn streams correctly (tool calls, thinking, subagents, the TodoWrite plan);
  - [ ] `queued_turn_count` when a prompt is folded into a running turn;
  - [ ] permission modes: `ask`, `accept edits`, `plan` with a real `ExitPlanMode`, `AskUserQuestion` answers, `EnterPlanMode`;
  - [ ] switching profile, model and effort mid-thread with `--resume` and `--system-prompt-snapshot off`;
  - [ ] the Banda MCP tools from a real agent (`list_threads`, `create_thread` with `wake_parent`).
- [ ] A second person on the LAN (bob) posts, mentions, requests a turn, forks; a handoff to bob with bob's own Claude login.
- [ ] Run a repo with `.banda/setup.sh` and a dev server; check the Browser tab and HMR.
- [ ] Use it for a week; write down what's annoying.
- [x] Run the API in the cluster instead of on the Mac (`mise run deploy`): signs in with Dex at `/dex` through the API.

## Phase 2: deployable

- [x] Sign-in in the cluster: the API serves the bundled Dex at `$PUBLIC_URL/dex` (`DEX_URL`); real OIDC providers as before.
- [x] Packaging: the Helm chart `deploy/helm/banda` as the one source, Kustomize base and overlays (`kind`, `example`) on top; the mise tasks and the e2e world install it (docs/deployment.md).
- [x] CI: Go vet and tests, `pnpm -C web check`, the `.check.ts` scripts, the generated code, the chart and both images on every pull request; releases push multi-arch images, the chart (OCI) and `banda.yaml` on a `v*` tag.
- [ ] CI e2e on kind with Docker: written (`deploy/dev.sh` derives the host from kind's network), not yet green on Actions; drop its `continue-on-error` once it is.
- [x] Postgres: generated password in a Secret, no NodePort outside kind, or your own database.
- [ ] Postgres for real: a non-superuser role for the API, backups (or CloudNativePG / a managed database).
- [x] Ingress with TLS for `banda.<domain>` and `preview.banda.<domain>` in the chart (`ingress.*`); `PUBLIC_URL`, `PREVIEW_URL` follow.
- [ ] Ingress on a real cluster: check the controller keeps thread streams open (read timeouts).
- [x] `strategy: Recreate` and resource requests on `banda-api`; a liveness probe.
- [ ] Versioned schema migrations before the first column change (`internal/store/store.go`).
- [ ] An install page: one document from empty cluster to signed-in team (vision goal 6). README's Install section is the start; it needs a run on a real cluster.
- [ ] Decide on Claude: confirm the persistent login volume and teammate turns with Anthropic (PLAN.md, open question 3) before more than the core team uses it.

## Phase 3: the team cluster

- [ ] A cluster dedicated to Banda, separate from production, with a CNI that enforces NetworkPolicy (Calico, Cilium); its pod network in the chart's `podCIDR`.
- [ ] Multi-node volumes: the per-user Claude volume and thread volumes are RWO. Pin a user's pods to one node, or use RWX storage.
- [ ] Sandbox sizes as settings instead of fixed 4 CPU / 8 GiB (`internal/sandbox/sandbox.go`); size nodes for concurrent threads.
- [ ] Clean up what lingers: per-thread token Secrets outlive deleted Sandboxes; sandboxes of threads never settled stay forever outside dev (`mise run clean` is dev-only).
- [ ] `SANDBOX_EGRESS_ALLOW` for the private networks agents need (git host, staging); a read-only kubeconfig as a team secret for DevOps.
- [ ] Monitoring: API logs and errors, sandbox count, disk use of PVCs.

## Later (not needed to run fully)

- [ ] Several API replicas: Postgres `LISTEN/NOTIFY` fan-out; the in-memory "suspend after the turn" moves to the database.
- [ ] A GitHub App (per-user OAuth) instead of personal tokens; webhooks as an add-on to polling for installs that can receive them.
- [ ] Pull requests: search on the forge in the repo picker; reviewers and labels from lists; resolving review threads; spotting a pull request the agent opened itself.
- [ ] New threads with attachments; "Spawn sub-thread" with a profile chip; agents handing off through MCP.
- [ ] Files tab ignore rules without git; adding a repository to a repo-less thread.
- [ ] Per-thread preview origins (needs wildcard DNS).
- [ ] Codex and other harnesses, only after checking their terms (non-goal until Claude Code is proven).
