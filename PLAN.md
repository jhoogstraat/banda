# Banda: Plan

## Vision

Banda is a web app where a team builds software together with coding agents. Each piece of work is a **thread**: one owner, one coding agent (model, effort), and one isolated Kubernetes environment with the repo checked out on its own branch. Teammates can browse each other's threads, post into them, and discuss right inside the chat.

**Audience:** an internal tool for my team. It may be open-sourced later, but it will never be sold.

## Non-goals (for now)

- Paying for or reselling model usage. Every user brings their own subscription.
- ACP, or any harness other than Claude Code, before Claude Code works end to end.
- An editor. The right pane is for looking (diff, files, browser, terminal), not editing.
- Multi-cluster, autoscaling, high availability.

## Decisions

| Topic | Decision |
|---|---|
| Backend | Go holds all business logic. |
| Web | SvelteKit as a static SPA served by the Go API. No backend-for-frontend: Go owns auth and the API, and a BFF would add a hop for every stream. Add one only if we need SSR. |
| Harness | Claude Code natively: the unmodified `claude` CLI in headless `stream-json` mode. No ACP for now. |
| First milestone | Solo end-to-end slice (see M1). |
| Thread ownership | Threads belong to a user. Anyone can look up another user in the sidebar and see all of their threads (all statuses). Threads you are mentioned in appear in a **Mentioned** section at the top. |
| Posting | Anyone can post in any thread at any time. |
| Chat mode | A post by someone other than the owner, or any post that mentions a person, switches the thread to chat mode, unless the post mentions the agent. Only the owner can switch chat mode off; it never ends by itself. In agent mode the owner doesn't need to mention the agent. In chat mode everyone, the owner included, must mention the agent to start a turn. |
| Sandboxes | [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) v1.0.7, core `Sandbox` CRD only. See [research](docs/research/sandbox-lifecycle.md). |
| Claude login | A per-user volume mounted as `CLAUDE_CONFIG_DIR` into that user's pods. Only the `claude` binary reads or writes it. See [research](docs/research/claude-login.md). |

## Constraint: Claude subscriptions

Source: [Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), checked 2026-10-19.

- Hosting Claude Code in sandboxes is allowed if the **binary is unmodified** and **each end user authenticates with their own credentials**. Banda must not pay for, resell or intermediate usage.
- Products built on the **Agent SDK must use API keys**. Banda's code "may not collect, store, or intermediate Claude.ai credentials or session tokens", and sign-in must go through Anthropic's own flow.
- Explicitly allowed: "an end user signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code".

What this means for Banda:

1. The runner drives the real `claude` binary. It does not use the Agent SDK or the ACP adapter.
2. Banda's code never sees a token. Users run `claude auth login` inside a sandbox: in M1 via `kubectl exec`, from M2 in a terminal pane. The binary writes the login to the user's config volume, which every one of their sandboxes mounts. This is the pattern [Anthropic's devcontainer](https://code.claude.com/docs/en/devcontainer) uses.
3. Never set `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` in a pod. Each of them overrides the user's login.
4. Refresh tokens rotate, and killing `claude` in the middle of a refresh logs the user out of every pod. To avoid that, the runner stops `claude` gracefully (pods get a 90 s grace period) and the image pins Claude Code ≥ 2.1.301.
5. **Open risk:** the terms assume "ordinary, individual usage" by the subscriber. Our chat-mode rule lets a teammate start a turn on the owner's subscription by mentioning the agent. Options: keep it for now (it's an internal team tool), or have the owner approve each teammate request. Ask Anthropic before open-sourcing. Same for the persistent login volume.

## Architecture

```
Browser (SvelteKit SPA)
   │  Connect-RPC over HTTP (unary + server streaming)
   ▼
banda-api (Go) ───────────── Postgres
   │ creates Sandbox + PVCs              ▲
   ▼                                     │ runner dials out (bidi stream over h2c)
Kubernetes (agent-sandbox) ── one Sandbox pod per thread:
                              runner (Go) → `claude -p --input-format stream-json …`
                              volumes: /home/banda (per thread), /home/banda/.claude (per user)
```

- **banda-api** (`cmd/api`): the Connect API for the web client (`ThreadService`) and the runners (`RunnerService`). Also serves the web build when `WEB_DIR` is set.
- **runner** (`cmd/runner`): baked into the sandbox image. It does the following:
  - dials the API and authenticates with a per-thread token;
  - checks `claude auth status`, and if needed posts a notice asking the user to log in;
  - clones the repo and creates the branch;
  - keeps one `claude` process running and maps its stream-json output to Banda events;
  - sends the diff after each turn.
- **web** (`web/`): SvelteKit, `adapter-static`, with [ai-elements for Svelte](https://github.com/sikandarjodd/ai-elements). 3 panes: sidebar, chat, and a right pane (diff for now).
- **Contract**: `proto/banda/v1/banda.proto`. buf generates the Go code (`gen/`) and the TS code (`web/src/lib/gen/`).
- ponytail: one API replica with in-memory fan-out. Switch to Postgres `LISTEN/NOTIFY` when we need a second replica.

## Domain model

| Entity | Fields |
|---|---|
| User | M1: the dev header. M2: OIDC `sub`, name, email, avatar. |
| Thread | owner, repo URL, base branch, branch, title, model, effort, `status`, `mode`, pending prompt, runner token, latest diff |
| Event | thread, seq, protojson body (`banda.v1.Event`). Append-only; the chat is rendered from these. |
| Mention (M3) | event, user, answered_at. Drives the Mentioned section. |
| ThreadView (M2) | user, thread, snoozed_until, settled_at, last_read_seq. Per-user overlay. |

## Thread state machine

The implementation is `internal/thread`: a pure `Apply(state, action) (state, effect, error)` with table tests.

```
starting ──EnvReady──▶ working ──TurnEnded──▶ awaiting_input
   │      (initial prompt)  ▲                        │
   │                        └── turn-starting post ◀─┘
   └──EnvReady (no prompt)──▶ awaiting_input
working ──Interrupt──▶ (stop sent; TurnEnded follows)
any ──EnvLost──▶ errored ──Retry / runner reconnects──▶ starting / awaiting_input
```

Mode is `agent` or `chat`; the rules are in the Decisions table. Posts that don't start a turn are always allowed. A post that would start a turn is rejected while the agent is working (M1: no queue) or while the environment isn't ready.

Snooze and settle (M2) are per viewer. **Sidebar sections**, computed per viewer:

| Section | Rule | Position |
|---|---|---|
| Mentioned | someone else's thread, I'm mentioned, unanswered | top |
| Needs input | my thread, `awaiting_input` or `errored`, not snoozed or settled | top |
| Working | my thread, `starting` or `working`, not snoozed or settled | bottom, collapsed |
| Snoozed | `snoozed_until > now` | bottom, collapsed |
| Settled | `settled_at` set | bottom, collapsed |

## Environment lifecycle

Only the owner's own actions drive the environment. M1 implements create and recreate only; the rest is M4.

| Thread state | Environment action |
|---|---|
| `starting` | create the `Sandbox` (plus the owner's Claude volume if it is missing) |
| `working` | keep running, never suspend |
| `awaiting_input`, idle < 30 min | keep running |
| `awaiting_input`, idle ≥ 30 min | `operatingMode: Suspended`: the pod is deleted, the PVCs stay (a one-minute ticker in banda-api) |
| owner posts or opens the env | `operatingMode: Running`; the runner restarts `claude --resume <session>` |
| owner snoozes | suspend now, resume lazily |
| owner settles | suspend, plus `shutdownTime` = now + 7 days with `shutdownPolicy: Delete` |
| `errored` → Retry | delete the pod; the controller recreates it on the same PVC |

Expected resume time on kind: about 3–10 s. A suspended thread costs only disk. Deferred: warm pools, memory snapshots (upstream has none), gVisor/Kata, [agent-substrate](https://github.com/agent-substrate/substrate) (pre-1.0, and it wakes actors on inbound traffic while our runner dials out).

## Milestones

**M1: Solo slice.** Demo: create a thread on a repo → watch Claude work → see the diff → send a follow-up.
- kind cluster with agent-sandbox. Postgres runs in the cluster. The API runs on the host, and runners reach it via `host.docker.internal`.
- The runner runs `claude` with `--dangerously-skip-permissions`, because the sandbox is the security boundary.
- Claude login: `kubectl exec -it -n banda t-<id> -- claude auth login`, once per user.
- Not in M1: OIDC, other users, mentions UI, snooze/settle, lifecycle automation, MCP, PRs, permission prompts.

**M2: Team basics.** OIDC login (any standard provider). Users list in the sidebar, per-user sections, terminal pane for Claude login. Permission prompts become `awaiting_input`.

**M3: Mentions and chat mode UI.** @mention autocomplete, the Mentioned section, in-app notifications. Give the agent recent chat messages as context when it is mentioned.

**M4: Git and lifecycle.** Push the branch, open a PR, files pane. Suspend and resume per the lifecycle table.

**M5: Banda MCP server.** Agents can list, read and search threads and post to a thread. The server is injected into every sandbox, scoped to the owner's access.

**M6: Second harness and browser.** Codex (check OpenAI's subscription terms the same way). Browser pane previewing a dev-server port.

## Repo layout

```
cmd/api/        Go API
cmd/runner/     Go runner, baked into the sandbox image
internal/       thread (state machine), store (Postgres), api, sandbox (k8s)
proto/          Connect API definitions
gen/            generated Go
web/            SvelteKit app
deploy/         kind config, Postgres, sandbox image
docs/research/  sandbox lifecycle, Claude login, Claude headless protocol
```

## Open questions

1. **Repos:** GitHub App with org repos, or paste any git URL? Which git host? M1 takes any HTTPS URL, plus an optional token in the `banda-git` Secret.
2. **Mid-thread changes:** can the model or effort change in the middle of a thread?
3. **Teammate turns:** do they stay on the owner's subscription? See open risk 5 above.
