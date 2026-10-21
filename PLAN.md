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
| Chat mode | A post by someone other than the owner, or any post that mentions a person, switches the thread to chat mode, unless the post mentions the agent. Only the owner can switch chat mode off; it never ends by itself. In agent mode the owner doesn't need to mention the agent. In chat mode everyone, the owner included, must mention the agent to start a turn. A teammate's turn needs the owner's approval (next row). |
| Teammate turns | A teammate's post that would start a turn becomes a **turn request**: a card in the chat that the owner approves (it runs as the teammate's turn; the prompt says who asked) or declines with an optional note. Requests don't wake a paused environment and can pile up; a pending one puts the thread in the owner's Needs input. Per thread, the owner can trust teammates to run the agent without approval. The owner's own agents posting through MCP never need approval. |
| Sandboxes | [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) v1.0.7, core `Sandbox` CRD only. See [research](docs/research/sandbox-lifecycle.md). |
| Claude login | A per-user volume mounted as `CLAUDE_CONFIG_DIR` into that user's pods. Only the `claude` binary reads or writes it. Users log in from Settings, in a terminal on their **login environment** (`login-<hash>`: the runner image in login mode, mounting only that volume, no secrets; it runs only `claude auth login` or `logout` and is suspended after 10 min without a terminal), or from a thread's terminal. Runners report the login state; Settings shows it. See [research](docs/research/claude-login.md). |
| API key mode | Per user and harness, the user may choose an Anthropic API key instead of the subscription. The key goes to the Secret `banda-auth-<hash>`, which thread pods load only in that mode; the runner then skips the login wait. Never returned by the API. |
| Secrets | Per-user environment variables, set in Settings. Values live only in the Secret `banda-env-<hash>` (Postgres keeps names and dates), loaded into all the user's thread pods after the shared `banda-git`, and redacted by the runner like tokens. Harness credentials, `HOME`, `PATH` and `BANDA_*` are reserved. Settings apply when an environment next starts. |
| Environment setup | Before the agent starts, the runner reports setup steps as events: checkout, `mise install` for the repo's `mise.toml`/`.tool-versions` (tools on the home volume), `.banda/setup.sh` (once per thread, rerun when it changes, 15 min timeout), login. A failed step errors the thread with the step's output; the runner stays up for the terminal and files, and Retry reruns setup. |
| Sign-in | OIDC authorization code flow with PKCE, signed session cookie. Any provider via env; Dex runs locally with three dev users. |
| Cross-thread MCP | Served by the API at `/mcp`, authenticated with the calling thread's runner token. Tools: `list_threads`, `read_thread`, `post_message`, `create_thread`. An agent may only start turns in threads of the same owner. |
| Chat context | When the agent is asked something, the prompt includes the thread messages it hasn't seen yet (chat between people, posts by other agents) and who is asking. |
| Permissions | Per thread, the owner picks a permission mode: **bypass** (the default for now: the sandbox is the boundary), **ask** (reads run, edits and commands ask), **accept edits** (edits run, other commands ask) or **plan** (read-only until the owner approves the plan). They map to Claude Code's `--permission-mode`; requests come over `--permission-prompt-tool stdio` as `can_use_tool` control requests, which the runner holds and turns into a card in the chat next to the tool call ([research](docs/research/claude-headless.md#what-banda-does-checked-2026-10-21)). The owner answers (their machine, their subscription), or teammates once the owner trusts them, the same switch as for turns. A plan only the owner approves, choosing ask or accept edits for the work. "Always allow <tool>" is stored on the thread and answered by the runner, not written to Claude Code's settings, which would land in the checkout or be lost on restart. A pending request puts the thread in Needs input and notifies the owner. The request lives in the claude process: a restart loses it and its turn, and the card says so. |
| Preview | The Browser tab frames what listens on a port in the thread's pod, for everyone. The runner reports listening ports (`/proc/net/tcp{,6}`, above 1023, with the owning process) and dials `localhost:<port>` for tunnels the API opens over its stream (`HttpTunnel`, 8 chunks of 32 KiB in flight per direction), so the API still never reaches into pods. The API serves the preview on **its own origin** (`PREVIEW_ADDR`, default `:8788`; `PREVIEW_URL` overrides the browser-facing URL) and a reverse proxy, WebSockets (Vite HMR) included, forwards through the tunnel. A separate origin keeps apps' absolute paths (`/assets/x.js`) and client routers working, which a path prefix breaks, and keeps the app from scripting Banda. A cookie on that origin selects thread and port; `PreviewOpen` hands a signed token valid for a minute that `/__banda/open` trades for it, so only signed-in users get one. Rejected: path prefixes (break absolute paths and routers), subdomains (wildcard DNS, no LAN IPs), service workers (need a secure context, so not on a LAN address). Upstream sees `Host: localhost:<port>` (dev servers' allowed hosts); Banda's cookies are stripped both ways. Never wakes a paused environment; the owner can. |
| Agent activity | The chat reads as prose: tool calls and thinking between two texts fold into one "Used N tools" row showing the latest action. Common tools render richly (Bash as a terminal with its exit code, edits as a mini diff, searches with match counts); unknown tools keep the generic view. A subagent's tool calls nest in its Task call; its own text is dropped, since the Task result repeats it. The latest TodoWrite list is pinned above the composer as the plan while items are open. |
| Long threads | Opening a thread replays only its last 500 events, preceded by the latest plan (TodoWrite) from before them; scrolling up loads 500 older ones at a time (`History`), merged so the chat is the same as a full replay. The stream cuts tool outputs and long tool input strings to 4 KB; the tool view loads the whole event on request. Off-screen chat items skip layout and paint (`content-visibility`) instead of a virtual list. |
| Live inbox | The sidebar is one server stream per tab (`InboxWatch`): the caller's own and mentioned threads, re-sent whole (coalesced, 250 ms) when one of them gets an event, a thread is created, or the caller's own view changes. Unread is per viewer: events after `thread_views.seen_seq`, which opening a thread or posting in it moves forward. The tab title counts unread threads in Mentioned and Needs input. Browser notifications (asked once from a bell in the account footer) for my thread needing input or failing, unless snoozed or settled, and for new mentions; never for the thread on screen. |
| Presence | Derived from open `WatchThread` streams, per user, broadcast as the full set (seq 0) on every join and when a user's last stream ends. Typing is a unary `SendTyping` the composer calls at most every 2.5 s; clients drop it after 5 s or when that person posts. The agent's "typing" is its status: the activity line above the composer replaces the old Working indicator. No sidebar indicator yet: it would need presence on `Thread`. |
| Diff review | Comments on diff line ranges are collected in the Diff tab (kept in the browser until sent) and posted as one message with structured `review` comments: path, lines (numbered in the branch, or in the base for removed-only ranges), quoted diff lines, text. The agent gets them as markdown. "Ask @agent" (default on for the owner) mentions the agent; the owner's review without it is a note that starts no turn and keeps the mode. A teammate's review asking @agent is a turn request like any teammate post. Posted comments show in the diff where their last line is unchanged. |
| Team board | `/team` shows everyone's threads from the last 7 days that their owners haven't settled, one column per person, with live status, last activity, who has the thread open and unread for me. One `TeamWatch` stream per open board, sharing the inbox's watchers, re-sent whole (coalesced, 1 s) on any stored event or presence change. The last activity (`threads.activity`) is summarized when events are stored, never by scanning them. Lists carry no diffs; only GetThread does. |
| Never losing work | After every turn that changed the diff, the runner pushes the thread branch (committed work only; it never commits for the agent) and reports where the work lives: `pushed` (origin has everything), `unpushed` (uncommitted changes, or the push failed) or `unavailable` (an https remote without a token, SSH, a refused push: the work only lives in the volume). Changes of that state appear as a quiet line in the chat. A settled environment is deleted 7 days after its owner settled the thread (`DELETE_SETTLED_AFTER`), only if suspended and `pushed`: agent-sandbox `shutdownTime` + `shutdownPolicy: Delete`, which takes the home volume with it. Unsettling, a prompt or any other start cancels it. A deleted thread offers its owner "Recreate environment" (a prompt does the same): a fresh checkout continues from the pushed branch, and Claude Code resumes its session, since transcripts live on the owner's Claude volume. |
| Local runtime | Shared `podman-machine-default` (root connection), kind cluster `banda`. |
| Model and effort | The owner can change them at any time from the composer chips; the chat records it as a divider. `claude` takes them only as start flags, so the runner restarts it with `--resume` once no turn is running (closing its stdin, so it exits by itself), never cutting a turn short. Prompts sent before the change still run on the old settings. A paused environment starts with the new ones. |

## Constraint: Claude subscriptions

Source: [Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), checked 2026-10-19.

- Hosting Claude Code in sandboxes is allowed if the **binary is unmodified** and **each end user authenticates with their own credentials**. Banda must not pay for, resell or intermediate usage.
- Products built on the **Agent SDK must use API keys**. Banda's code "may not collect, store, or intermediate Claude.ai credentials or session tokens", and sign-in must go through Anthropic's own flow.
- Explicitly allowed: "an end user signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code".

What this means for Banda:

1. The runner drives the real `claude` binary. It does not use the Agent SDK or the ACP adapter.
2. Banda's code never sees a token. Users run `claude auth login` inside a sandbox: in M1 via `kubectl exec`, from M2 in a terminal pane. The binary writes the login to the user's config volume, which every one of their sandboxes mounts. This is the pattern [Anthropic's devcontainer](https://code.claude.com/docs/en/devcontainer) uses.
3. Never set `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_AUTH_TOKEN` in a pod, and `ANTHROPIC_API_KEY` only when the user chose API key mode. Each of them overrides the user's login. Secrets can't use these names.
4. Refresh tokens rotate, and killing `claude` in the middle of a refresh logs the user out of every pod. To avoid that, the runner stops `claude` gracefully (pods get a 90 s grace period) and the image pins Claude Code ≥ 2.1.301.
5. **Open risk:** the terms assume "ordinary, individual usage" by the subscriber. A teammate can start a turn on the owner's subscription by mentioning the agent, but only once the owner approves it, or in a thread the owner explicitly trusts (see Teammate turns). And since turns run without permission prompts by default (bypass mode; ask, accept edits and plan modes make the agent ask the owner first) in a pod that holds the owner's Claude login (and the optional git token), a teammate could ask the agent to reveal or send them. The runner redacts Anthropic tokens and the git/runner tokens from everything it reports, so they don't reach Banda's database or the UI, but an agent told to exfiltrate them over the network could. Approval by default means the owner reads every teammate request before it runs in their pod; trusting a thread accepts the risk. Ask Anthropic before open-sourcing. Same for the persistent login volume.

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
  - clones the repo and creates the branch, installs the repo's mise tools and runs `.banda/setup.sh`, reporting each step;
  - checks `claude auth status`, and if needed posts a notice asking the user to log in;
  - keeps one `claude` process running and maps its stream-json output to Banda events;
  - sends the diff after each turn.
- **web** (`web/`): SvelteKit, `adapter-static`, with [ai-elements for Svelte](https://github.com/sikandarjodd/ai-elements). 3 panes: sidebar, chat, and a right pane (diff for now).
- **Contract**: `proto/banda/v1/banda.proto`. buf generates the Go code (`gen/`) and the TS code (`web/src/lib/gen/`).
- **MCP** (`internal/api/mcp.go`): the Banda MCP server, passed to every `claude` via `--mcp-config`.
- **Preview**: a second listener of banda-api (`PREVIEW_ADDR`) proxies HTTP and WebSockets to ports in the pod through tunnels over the runner's stream (`internal/tunnel`).
- **Terminal**: the runner opens a PTY on request and streams it through the same dial-in stream; the web client renders it with xterm.js.
- ponytail: one API replica with in-memory fan-out. Switch to Postgres `LISTEN/NOTIFY` when we need a second replica.

## Domain model

| Entity | Fields |
|---|---|
| User | OIDC `sub`, name, email (dev auth: the header value) |
| Thread | owner, repo URL, base branch, branch, title, model, effort, `status`, `mode`, pending prompt, delivered seq (what the agent has seen), open turn requests, trusted, runner token, latest diff, last activity (summary of the latest notable event) |
| Event | thread, seq, protojson body (`banda.v1.Event`). Append-only; the chat is rendered from these. |
| Mention | thread, event seq, user, answered_at. Drives the Mentioned section. |
| ThreadView | user, thread, snoozed_until, settled_at. Per-user overlay. |

## Thread state machine

The implementation is `internal/thread`: a pure `Apply(state, action) (state, effect, error)` with table tests.

```
starting ──EnvReady──▶ working ──TurnEnded──▶ awaiting_input
   │      (initial prompt)  ▲                        │
   │                        └── turn-starting post ◀─┘
   └──EnvReady (no prompt)──▶ awaiting_input
working ──Interrupt──▶ (stop sent; TurnEnded follows)
any ──EnvLost──▶ errored ──Retry / runner reconnects──▶ starting / awaiting_input
awaiting_input / starting / errored ──Suspend (idle, owner snooze/settle)──▶ suspended
suspended ──turn-starting post (kept as pending prompt) / owner Wake──▶ starting
teammate's turn-starting post (untrusted thread) ──▶ request (status unchanged) ──owner Approve──▶ as a post / Decline──▶ dropped
```

Mode is `agent` or `chat`; the rules are in the Decisions table. Posts that don't start a turn are always allowed. Prompts sent while the agent works are queued (Claude Code runs them in order) and the thread stays `working` until all turns have ended. A post that would start a turn is rejected while the environment is starting or errored.

Snooze and settle are per viewer. **Sidebar sections**, computed per viewer:

| Section | Rule | Position |
|---|---|---|
| Mentioned | someone else's thread, I'm mentioned, unanswered | top |
| Needs input | my thread, `awaiting_input`, `errored` or `suspended`, or with a teammate's turn request; not snoozed or settled | top |
| Working | my thread, `starting` or `working`, not snoozed or settled | bottom, collapsed |
| Snoozed | `snoozed_until > now` | bottom, collapsed |
| Settled | `settled_at` set | bottom, collapsed |

## Environment lifecycle

Only the owner's own actions drive the environment. Implemented as below.

| Thread state | Environment action |
|---|---|
| `starting` | create the `Sandbox` (plus the owner's Claude volume if it is missing) |
| `working` | keep running, never suspend |
| `awaiting_input`, idle < 30 min | keep running |
| `awaiting_input`, idle ≥ 30 min | `operatingMode: Suspended`: the pod is deleted, the PVCs stay (a one-minute ticker in banda-api) |
| owner posts or opens the env | `operatingMode: Running`; the runner restarts `claude --resume <session>` |
| owner snoozes | suspend now, resume lazily |
| owner settles | suspend; if all work is pushed, `shutdownTime` = settle time + 7 days with `shutdownPolicy: Delete` (unsettle or any start removes it) |
| deleted (settled, pushed) | owner recreates or posts: a new `Sandbox` and volume, checked out from the pushed branch |
| `errored` → Retry | delete the pod; the controller recreates it on the same PVC |

Every start (create, resume, retry) applies the current pod template, so settings changes (secrets, auth method) take effect then. A user's login environment starts when they open its terminal and is suspended after 10 min without one.

Expected resume time on kind: about 3–10 s. A suspended thread costs only disk. Deferred: warm pools, memory snapshots (upstream has none), gVisor/Kata, [agent-substrate](https://github.com/agent-substrate/substrate) (pre-1.0, no shared login volume, freezes `claude` instead of stopping it; see [docs/research/agent-substrate.md](docs/research/agent-substrate.md)).

## Status

Everything below is implemented on branch `v1` and checked by `mise run e2e` against the kind cluster (fake agent), unless noted.

**Built**
- Threads: create (repo, base branch, model, effort, prompt), one sandbox per thread, branch `banda/<id>`, streaming chat with tool calls, interrupt, retry, diff after every turn.
- State machine: `starting`, `working`, `awaiting_input`, `errored`, `suspended`; agent/chat mode with the posting rules from the Decisions table.
- Teammate turn requests: approve/decline cards in the chat, Needs input for the owner, a per-thread trust toggle in the header.
- Sign-in with OIDC (Dex locally), users, display names.
- Sidebar: search, project filter (by repository) and new-thread button on top; Mentioned, Needs input, then collapsed Working, Snoozed, Settled; mentions-only toggle; browse other people's threads; account footer.
- New thread is an empty chat: the first message creates the thread; repository, branch, model and effort are chips in the composer.
- Team board (`/team`, `G T`, the palette): a live column per teammate with their recent unsettled threads, each card showing status, title, repository, last activity ("Agent: Edit src/x.ts", "Waiting for Alice", "errored: Setup failed: …"), who is looking at it, and unread; filtered by project like the sidebar.
- Live inbox: the sidebar updates itself over `InboxWatch`; unread threads are bold with a dot; mentioned threads show the owner's avatar with a live status dot; browser notifications and an `(n) Banda` tab title for what needs me.
- Per-user snooze and settle; @mentions with autocomplete; mentions are answered when the mentioned person posts.
- Changing model and effort mid-thread (owner only): applied between turns by restarting Claude Code with `--resume`.
- Environment lifecycle: suspend after 30 min idle, when the owner snoozes or settles; resume on the next prompt or when the owner opens the terminal; Claude Code session resumed with `--resume`.
- Diff review: click or drag line numbers to comment, send the pending review as one message (optionally asking @agent); the chat shows each comment with its code and jumps to it in the diff, which marks comments that still apply. The diff has line numbers, a file list, per-file collapse (big files start closed) and sticky file headers.
- Permission prompts: a per-thread mode (bypass, ask, accept edits, plan), inline cards to allow once, always allow a tool or deny with a reason, plan approval by the owner, answers by trusted teammates, Needs input and a notification while one is pending; lost and withdrawn requests are marked.
- Browser tab (everyone): the app running in the sandbox, with a port picker (dev server picked by default), path bar, reload and open in a new tab; WebSockets pass through for HMR. Paused environments say so; the owner can wake them.
- Files tab (everyone, read-only): the runner lists and reads the checkout on request, confined to it (`os.Root`) and redacted; it never wakes a paused environment.
- Terminal tab (owner only) and the "Log in to Claude Code" button; Push button with a pull-request link for GitHub remotes.
- Auto-push after every turn that changed the diff; the Push button becomes "Open a pull request" once a GitHub branch is pushed. Settled, pushed environments are deleted after 7 days and can be recreated from the branch. e2e cleans up its sandboxes (also on failure) and tests against an in-cluster git daemon; `mise run clean` deletes sandboxes of threads untouched for `CLEAN_HOURS` (dev only).
- Settings (backend): subscription or API key per harness, login state reported by runners, per-user secrets as environment variables (write-only, redacted), and a login environment with a login/logout terminal.
- Setup steps as events: checkout, mise tools, `.banda/setup.sh` (once per thread), login. A failing step errors the thread without a crash loop.
- Banda MCP server for agents.
- Agent activity: tool calls folded between texts, rich views for Bash, edits, searches and web tools, subagent calls nested in their Task call, the TodoWrite plan pinned above the composer.
- Long threads: tail on open, older history on scroll up, large tool payloads cut to 4 KB with "load all". A 20k-event thread (19 MB) opens in about 0.5 s with 0.4 MB sent, where it used to send 20 MB and never finished rendering.
- Presence and typing: the thread header shows who else has the thread open (per user across tabs); a line above the composer shows the agent working or writing and who is typing. Both are ephemeral events, never stored.
- Phone layout: the sidebar becomes a drawer and the right pane a full-screen layer. Only the app shell (`+layout.svelte`, shadcn `Sidebar`) and `side-pane.svelte` know the screen size; everything else fills its container and adapts with container queries.
- In-cluster deployment of the API (`mise run deploy`, `deploy/banda.yaml`).
- Keyboard: a Cmd/Ctrl-K command palette (also the search button in every page header, for phones) finds my, mentioned and teammates' threads, recently opened first, and runs the current thread's actions that apply to the viewer (pane and tabs, snooze, settle; owner: interrupt, retry, push, mode). Shortcuts: `c` new thread, `j`/`k` or Alt-↑/↓ through the sidebar, `g s` settings, `/` message box, `Esc`, `?` cheat sheet; never while typing. Not covered by e2e (UI only).

**Verified only partly**
- Real Claude Code: the image starts, detects the missing login and prints the sign-in URL in the terminal. Completing a login and a real turn need a Claude subscription; the stream-json mapping is tested against recorded CLI output.

**Not yet**
- Permission prompts: `AskUserQuestion` (answered with the selected options) and editing a request before allowing it; per-call "always allow" rules (e.g. `Bash(npm test *)`) instead of whole tools; the mode the agent switches to itself (`EnterPlanMode`) is not shown. The real CLI's `ExitPlanMode` input is not verified: the card falls back to the plan in the chat.
- Preview: one preview cookie per browser, so two windows previewing different threads share the one opened last until reloaded; per-thread origins would need wildcard DNS. The preview origin shares the host (and so cookies) with Banda, and the previewed apps of all threads share it with each other: for a hardened setup, point `PREVIEW_URL` at a sibling subdomain. The address bar can't follow navigation inside the frame (cross-origin).
- Codex and other harnesses (check their subscription terms first).
- Several API replicas (needs `LISTEN/NOTIFY` fan-out).

## Repo layout

```
cmd/api/        Go API
cmd/runner/     Go runner, baked into the sandbox image
internal/       thread (state machine), store (Postgres), api, sandbox (k8s)
proto/          Connect API definitions
gen/            generated Go
web/            SvelteKit app
deploy/         kind config, Postgres, sandbox image
deploy/e2e.sh   end-to-end check with the fake agent
docs/research/  sandbox lifecycle, Claude login, Claude headless protocol
```

## Open questions

1. **Teammate turns:** decided: they run on the owner's subscription only after the owner approves, or in threads the owner trusts (see Teammate turns). Still to confirm with Anthropic (question 3).
2. **Repos:** any HTTPS git URL works, with an optional token in the `banda-git` Secret. A GitHub App would allow per-user access and opening PRs via the API.
3. **Anthropic:** before open-sourcing, confirm the persistent login volume and teammate turns with Anthropic.
