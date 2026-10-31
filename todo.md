# To do

What is left before Banda runs fully, measured against "What done looks like" in [VISION.md](VISION.md). What Banda does and its known limits are in [PLAN.md](PLAN.md). Finished items leave this file; git keeps the history.

## Where we are

Feature work for v1 is done and covered by the e2e suite with the fake agent, and packaging, CI and releases exist. What isn't proven is **real use**: no real Claude Code turn has run through Banda, and it has never run anywhere but in kind. Every risk left is the same one, that the real thing behaves differently from the fake. So no new features until real use asks for one.

| Vision goal | State |
|---|---|
| 1. SSO, Claude login once, works on real repos | Built; real login and turns unverified |
| 2. Easier than local terminals | Built; needs real use to judge |
| 3. Never loses work, no manual cleanup | Built |
| 4. Team habits: mentions, reviews, forks, handoffs | Built |
| 5. A non-engineer gets work done | Profiles built; untried with a person |
| 6. One person installs it from one page | Built; never installed outside kind |

## 1. Dogfood on this Mac (Joshua; agents fix)

A week of real work, ideally Banda building Banda. Each annoyance becomes a failing e2e test and a fix within a day.

- [ ] `mise run up`, sign in as alice, create a thread on a real repo.
- [ ] Log in to Claude Code with your own account (Settings or the thread's terminal); check the login survives a suspend and resume, and is shared by a second thread. In the EEA that account is a Team or Enterprise seat or an API key.
- [ ] Set up the GitHub App from Settings and connect (or add a `GIT_TOKEN` secret); check a real turn auto-pushes the branch and the Push button turns into "Create a pull request".
- [ ] Pull requests on github.com: create, checks from GitHub Actions, a review, a merge; check the token scopes in docs/usage.md are enough and no more.
- [ ] The Banda MCP tools with a real model (the contract test calls a stub with their names).
- [ ] A thread on the team's Console identity against the real Console (docs/configuration.md): a first turn, the balance of a linked Team plan's credits after it (Anthropic's page names API keys only), a restart within 5 minutes, and a token past its first refresh.
- [ ] A second person on the LAN (bob) posts, mentions, suggests a prompt, requests a turn (the owner on an API key), forks; a handoff to bob with bob's own Claude login.
- [ ] A repo with `.banda/setup.sh` and a dev server: the Browser tab and HMR. With background tasks off, Bash has no `run_in_background`, but a server started with `&` survives the turn (`TestContractDevServer`). Left for a real model: does it use `&` on the first try, and does it redirect the log to a file it can read? If it doesn't, add one line to the runner's system prompt rather than turning background tasks back on.
- [ ] When you hit a Claude usage limit, save `kubectl -n banda logs t-<thread> | grep rate_limit_event` before the pod suspends (30 min idle): the shape the usage limit feature below waits for.
- [ ] Use it for a week; write down what's annoying.

Until the week ends:

- **Add** only what turns a signal the real CLI already sends into honest state (principles 6 and 7), with a contract test. One waits: usage limits. Each person has one limit, shared by all their threads and spent faster in Banda than in a terminal, since that's the point. Show the reset time once per person (the board, their threads), hold queued prompts, races and sub-threads, resume when the limit resets, and let a teammate carry on in a fork on their own login. It is blocked: the pinned CLI reads the limit's reset only when signed in to claude.ai, so a contract run (API key, by design) sees no reset (docs/research/claude-headless.md §6, `TestContractUsageLimit`). Meanwhile the turn ends with the CLI's own limit text as its error, and the runner logs each `rate_limit_event` to the pod's log.
- **Freeze** the forges: no new forge feature until github.com is verified.
- **Cut** after the week whatever nobody used. Candidates: the Pull request tab's conversation and merging (the forge's page is one click away), person pages beside the board's columns, and whichever of the five "what needs me" views (Needs input, Decisions, the board's Needs them, the morning brief, notifications) nobody opens.

## 2. The team cluster (agents rehearse, Joshua runs it)

Done when one page, docs/deployment.md "Install", takes an empty cluster to a signed-in team (vision goal 6).

- [ ] A cluster dedicated to Banda, separate from production, with a CNI that enforces NetworkPolicy and its pod network in the chart's `podCIDR`.
- [ ] Follow "Install" on it, with a real identity provider and TLS.
- [ ] Ingress: a quiet thread page stays connected for minutes behind the team's controller.
- [ ] `SANDBOX_EGRESS_ALLOW` for the private networks agents need (git host, staging); a read-only kubeconfig as a team secret for DevOps, or better a kubeconfig with `tokenFile` and workload identity (the team secret `KUBECONFIG_YAML`, the Kubernetes recipe in Settings).
- [ ] Workload identity on Banda's real https address, then the AWS and Google Cloud recipes against real accounts, and Vault itself (only OpenBao ran).
- [ ] Monitoring: the chart's ServiceMonitor and PrometheusRule against the team's Prometheus, and `BandaVolumeFull` on the team's storage.
- [ ] Sizing: measure a real model's turn. docs/deployment.md, "Sizing", comes from builds with the fake agent.
- [ ] Still unrehearsed anywhere: Cilium, amd64 nodes, a cloud load balancer or hostNetwork controller, preview bodies over 1 MiB, Infisical's CA field and a key rotation against it.

## 3. Forges beyond what has run

- [ ] The GitHub App against github.com and a GitHub Enterprise Server: the manifest flow, installing, connecting, token expiry and refresh, pushes touching `.github/workflows`. On GitHub Enterprise the header's Create a pull request needs the fix GitLab got: the URL it checks is github.com's only.
- [ ] The GitLab application against gitlab.com. Unchecked on any GitLab so far: two consents from different accounts, the 10-minute sweep of abandoned flows, `glab` with `GITLAB_TOKEN`, the admin's Trusted checkbox, `SSL_CERT_FILE` on a Linux API.

## 4. Launch (needs Joshua's go-ahead)

- [ ] Make the repository public and enable private vulnerability reporting (SECURITY.md points to it).
- [ ] Tag `v0.1.0`; after the first release, make the GHCR packages `banda-api`, `banda-sandbox` and `charts/banda` public (GHCR creates them private), then check `helm install` from the README works on a fresh cluster.

## Later

- [ ] Several API restarts in a row can leave a thread showing `awaiting_input` with no runner for up to 5 minutes; it recovers by itself (docs/deployment.md).

On hold until real use asks for them: several API replicas (Postgres `LISTEN/NOTIFY` fan-out, with the in-memory "suspend after the turn" moved to the database), webhooks, per-thread preview origins (need wildcard DNS), waking a parent that is starting or errored, accepting a handoff during the briefing, free replies and option previews on permission prompts, and other harnesses, only after checking their terms.
