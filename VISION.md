# Banda: Vision

Read this before you decide anything that isn't settled in [PLAN.md](PLAN.md). PLAN.md says *what* was decided; this file says *why*, so you can decide the next thing the same way.

## The one sentence

**Banda lets a small team work with coding agents the way they already work with each other: in the open, in parallel, and without anyone losing track of anything or anyone losing work.**

## The problem

Coding agents are now good enough that one engineer runs several at once. But today each agent lives on one laptop, in one terminal, behind one person:

- **Nobody else can see it.** A teammate can't look at what your agent is doing, help it or take over. The work, the reasoning and the dead ends stay private until a pull request appears.
- **It is tied to a machine.** Close the laptop and the agent stops. Run three at once and they fight over the same checkout, ports and `node_modules`.
- **Work gets lost.** Uncommitted changes in a forgotten worktree, a session nobody can find again, a branch never pushed.
- **Attention is the bottleneck.** With five agents running, the hard part is knowing which one needs you *now*, not starting the next one.
- **Not everyone is an engineer.** The person who knows what the product should do often can't drive a terminal agent.

## What Banda is

A web app for one team. Every piece of work is a **thread**:

- **one owner**: the person whose subscription pays and whose decision it is;
- **one agent**: the unmodified Claude Code CLI, with a model, effort, profile and permission mode;
- **one environment**: an isolated Kubernetes sandbox with the repo checked out on its own branch, which sleeps when idle and wakes when needed.

Around threads, Banda adds what a team needs and a terminal lacks:

| Need | How Banda meets it |
|---|---|
| See each other's work | Every thread is readable by everyone: chat, tool calls, diff, files, the running app. The team board and search span all of it. |
| Help each other | Anyone posts into any thread, mentions people, reviews the diff line by line, forks a thread to try another way. |
| Stay in control of your own agent | Only the owner runs the agent, changes how it runs, or answers its permission prompts. On a subscription nobody else ever does (Anthropic's terms); teammates talk to the owner and fork to run it on their own. On an API key, a teammate's request is a card the owner approves, or the owner trusts teammates. |
| Run many agents without chaos | One sandbox per thread: no shared checkout, ports or dependencies. Races run 2–4 tries of one prompt and compare them. Sub-threads split work. |
| Know what needs you | The sidebar and `/decisions` show only what blocks on you; "the agent is done" is news, not a demand. Overlaps warn when two threads touch the same files. |
| Never lose work | Auto-push after every turn, a push before every suspend, nothing deleted that isn't on origin. |
| Include non-engineers | Profiles (Specification, DevOps, Business) change how the agent talks and works with a person, not what it may do. |
| Let agents work together | A Banda MCP server lets agents read, search and post into other threads and spawn sub-threads, with guards against loops and prompt injection. |

## Who it is for

**One team, roughly 3–15 people, who trust each other.** Mostly engineers, plus the product, ops and business people around them. It is an internal tool; it may be open-sourced, it will never be sold.

This shapes everything: visibility is the default (no per-thread ACLs), the scale is tens of threads per person and not thousands, one API replica is fine, and the setup must be something one person can run.

## Principles: how to decide

When a choice isn't covered by PLAN.md, apply these in order. Earlier ones win.

1. **Stay inside the Claude terms.** The unmodified `claude` binary, each user's own subscription, signed in through Anthropic's own flow. Banda never sees, stores or relays a Claude credential, and never pays for or resells usage. If a feature needs that, the answer is no. (See PLAN.md, Constraint.)
2. **Never lose work, never leak secrets.** Prefer a slower or uglier path that can't drop a user's changes or expose a credential. Delete nothing that isn't verifiably on origin. Treat the sandbox, the repo's code, other agents' posts and previewed apps as untrusted. No long-lived credential goes into a sandbox: the API mints short-lived tokens scoped to the thread's owner and repository when they are needed, and long-lived keys stay with the API.
3. **The owner decides about their thread.** Their subscription, their secrets, their pod. Teammates can see and suggest everything; they act only with the owner's approval or trust, and never on the owner's subscription. Agents get no more power than their owner.
4. **Open by default inside the team.** If a teammate could benefit from seeing it, they can see it. Don't add privacy controls a trusting team didn't ask for.
5. **Attention is the scarce resource.** Surface only what blocks on a person. Prefer one calm line over a banner, a count over a list, a live update over a refresh button. Never notify for the page already on screen.
6. **Claude Code as it is.** Wrap the real CLI and map its output; don't reimplement the agent, fork its behaviour or hide its features. When Claude Code gains something, Banda should get it nearly for free.
7. **Honest state.** Every status, error and banner says what is really true and what the person can do about it. No spinners that lie, no "something went wrong".
8. **Small and boring.** One Go binary, one Postgres, one SPA, Kubernetes for sandboxes. No extra services (search is Postgres, fan-out is in memory until a second replica is needed). Mark each deliberate shortcut with a `ponytail:` comment naming its ceiling.
9. **Every behaviour is tested end to end.** If it isn't in `mise run e2e` against the real cluster with the fake agent, it isn't done. Bugs come with a failing test first.

### Quick tests for a proposal

- Would it make Banda pay for, hold or relay a Claude credential? → **No.**
- Could it delete or hide work that isn't on origin? → Redesign it.
- Does it let someone act on another person's thread without approval? → Needs the owner's trust.
- Does it start a turn on someone's subscription that they didn't start themselves? → **No.** Only on an API key, with the owner's approval or trust.
- Does it put a long-lived or broader-than-needed credential in a sandbox? → Mint a short-lived, scoped one in the API instead.
- Does it add a new service, dependency or setting? → Show that the existing ones can't do it.
- Does it add a notification, badge or section? → Show it is something that blocks a person.
- Does it only matter beyond one team (billing, multi-tenancy, org admin, quotas)? → Non-goal.

## Non-goals

- Selling, billing, metering or reselling model usage.
- Multi-tenancy, organisations, per-thread access control.
- An editor or IDE. The right pane is for looking (diff, files, browser, terminal); editing is the agent's job or the terminal's.
- Other harnesses (Codex, ACP) before Claude Code works fully end to end, and only if their terms allow hosting.
- High availability, autoscaling, multi-cluster. One team, one cluster, one API replica until that hurts.

## What "done" looks like

Banda has fulfilled its vision for v1 when the team, on its own cluster, for a normal week:

1. signs in with the company's SSO, each person logs in to Claude Code once, and threads just work on the team's real repos;
2. runs most agent work through Banda instead of local terminals, because it is easier, not because it is required;
3. never loses a change, and never needs an admin to clean up sandboxes or disks by hand;
4. sees each other's work and helps through mentions, reviews, forks and handoffs as a habit;
5. includes at least one non-engineer who gets real work done through a profile;
6. can be installed or upgraded by one person following one page.

Where we are against this is tracked in [todo.md](todo.md).
