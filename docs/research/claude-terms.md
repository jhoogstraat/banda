# Claude terms: hosting, teammate turns, open-sourcing

Researched 2026-10-24 for roadmap milestone 4 ("Terms, then launch") and PLAN.md open question 3. Every quote below was retrieved on 2026-10-24 and is copied word for word. All sources are primary: Anthropic's legal pages, the Claude Code docs on code.claude.com, Anthropic support articles and the `anthropics/claude-code` repo. Wayback Machine snapshots of those pages are used only to date changes. Two kinds of statement are kept apart:
- **Text:** what a page says, quoted.
- **Inference:** marked as such. This is how the text reads onto Banda, not a legal conclusion. Only Anthropic can settle these points.

## TL;DR

- Hosting the unmodified `claude` binary, with each user signing in to their own subscription, is named as allowed in the text. The condition is agreeing to the Commercial Terms, and PLAN.md leaves that out. No text covers keeping that login on a per-user volume on a hosted platform. The closest texts are Anthropic's own devcontainer docs and the authentication docs, which describe exactly that for one engineer's container.
- Teammate turns are the weak point. Four texts speak against someone else using an account: "make your Account available to anyone else" (Consumer Terms), "Users can't route their work through another user's account to use that user's remaining usage" (Team seats), "only that person may use it" (Team and Enterprise seats), and, **from 2026-11-12**, a Usage Policy line against "services that route requests through consumer subscriptions". The owner's approval doesn't change whose account the turn runs on.
- The Agent SDK sentence is about more than the SDK: "does not allow third party developers to offer claude.ai login or rate limits for their products, *including* agents built on the Claude Agent SDK". The legal page then exempts "an end user signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code". On its face Banda falls in that exemption. Two points are open: whether Banda's "log in" screens count as "offering" login, and whether relaying the login code through Banda's terminal stream counts as "intermediating" it.
- The text explicitly allows API keys, Bedrock, Vertex AI and Foundry for a team's "own authorized users". It also allows Team and Enterprise seats, but those still belong to one person each. Claude Code's self-hosted environments are a Team/Enterprise beta.
- None of this is new to PLAN.md except the dates and the exact scope. The 2026-11-12 Usage Policy line is new since the last check (2026-10-19), and so is the full Consumer Terms sentence.

## Summary

Exposure is an **inference**: *clear* means the text names Banda's case as allowed, *unclear* means no text decides it, and *conflicts* means a text, read as written, goes against Banda's current design.

| # | Question | What the text says | Banda's exposure | Source |
|---|---|---|---|---|
| 1a | Host the unmodified CLI in a per-user container, each user on their own subscription | Allowed "where a platform hosts Claude Code", if the binary is unmodified and every end user authenticates with their own credentials. Running it "in hosted sandboxes" "requires agreeing to our Commercial Terms" | **Clear**, with one gap: who agrees to the Commercial Terms for a self-hosted install (each team that runs Banda?) | [Q1](#q1-hosting-the-cli-and-keeping-the-login) |
| 1b | Pro/Max as well as Team/Enterprise seats | OAuth is "intended exclusively for purchasers of Claude Free, Pro, Max, Team, and Enterprise subscription plans". The hosting exemption says "their own Claude subscription" without naming plans | **Clear** for the plans; but Pro/Max fall under the Consumer Terms, which the hosting section doesn't mention | [Q1](#q1-hosting-the-cli-and-keeping-the-login) |
| 1c | Login through Anthropic's own flow | "sign-in to a Claude account must complete through Anthropic's own flow"; developers "may not collect, store, or intermediate Claude.ai credentials or session tokens" | **Unclear**: the flow is Anthropic's (`claude auth login`, claude.ai in the user's browser), but the code the user pastes back travels through Banda's API in the terminal stream | [Q1](#q1-hosting-the-cli-and-keeping-the-login) |
| 1d | The login kept on a per-user volume across restarts | Documented for dev containers: "mount a named volume at `~/.claude` and set `CLAUDE_CONFIG_DIR`". Nothing about hosted platforms | **Unclear**: same mechanism, but the docs show it for one engineer's own container | [Q1](#q1-hosting-the-cli-and-keeping-the-login) |
| 1e | Turns driven by `claude -p` and by agents | Consumer Terms forbid "automated or non-human means" "except … where we otherwise explicitly permit it". Support: "You can still use the Claude Agent SDK, `claude -p`, and third-party apps with your subscription limits" | **Unclear** for turns no person started (MCP `create_thread`, `wake_parent`) | [Q1](#q1-hosting-the-cli-and-keeping-the-login) |
| 2 | A teammate's prompt runs on the owner's subscription after approval, or in a trusted thread | "make your Account available to anyone else" (Consumer); "Users can't route their work through another user's account to use that user's remaining usage" (Team); "only that person may use it" (Team, Enterprise); from 2026-11-12 "route requests through consumer subscriptions" (Usage Policy) | **Conflicts**, as written, for Team seats; **unclear to conflicts** for Pro/Max. Approval or trust doesn't change whose account pays | [Q2](#q2-teammate-turns-and-shared-use) |
| 3 | Open-source Banda: "may not offer claude.ai login or rate limits" | Exact wording below. It covers "their products, including agents built on the Claude Agent SDK", not just the SDK. The legal page exempts users signing in to the unmodified binary on a hosted platform | **Unclear**: the exemption fits; a "Log in to Claude" button in Banda's Settings may or may not count as "offering" login. Contact: "contact sales" | [Q3](#q3-open-sourcing-a-tool-that-wraps-the-cli) |
| 4 | Alternatives | API keys, Bedrock, Vertex AI and Foundry are allowed "for use by the customer's own authorized users", billed to the key owner. Team/Enterprise seats are per person. Self-hosted environments: Team/Enterprise beta | **Clear** for API keys (Banda's API key mode). A team key would make teammate turns a non-issue, but trades away principle 1's "own subscription" | [Q4](#q4-alternatives-the-text-allows) |

---

## Q1. Hosting the CLI and keeping the login

**Hosting.** [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) (retrieved 2026-10-24), "Can customers offer Claude Code in their products?":

> "Unless we've mutually agreed otherwise, preinstalling or running Claude Code in your products or services (e.g. in hosted sandboxes or other agent infrastructure) requires agreeing to our Commercial Terms of Service and complying with the conditions below:"
>
> "**The Claude Code binary must not be modified.** Claude Code must be installed and run as published by Anthropic, and customers may not remove, disable, or restrict any authentication method built into it (including methods that permit signing in with a Claude account or the user's own API key)."
>
> "**Customers may not pay for, resell, or intermediate Claude usage on their end users' behalf.** Each end user must authenticate with their own Anthropic API key, Claude subscription plan credentials, or 3P inference provider credential (Amazon Bedrock, Google Cloud's Agent Platform, Microsoft Foundry). That usage is billed directly to the end user under their own agreement with Anthropic or, for third-party inference providers, with the applicable provider."

Same page, "Authentication and credential use", last paragraph:

> "Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code as described under *Can customers offer Claude Code in their products?* above."

Same page, "License":

> "Commercial Terms of Service - for Team, Enterprise, and Claude API users"
> "Consumer Terms of Service - for Free, Pro, and Max users"

Same page, on names:

> "You can accurately say, in plain text, that your product has Claude Code preinstalled or that it runs Claude Code. But you can't use the Claude Code or Anthropic names or logos as part of your own product, feature, or company name, in your own logo, or in a way that suggests Anthropic built, endorses, or is partnered with your product."

[Commercial Terms](https://www.anthropic.com/legal/commercial-terms) (effective Jun 17, 2025), A.1:

> "Subject to these Terms, Anthropic gives Customer permission to use the Services, including to power products and services Customer makes available to its own customers and end users (“Users”)."

Dated with Wayback snapshots of the legal page: the "Can customers offer Claude Code" section, the "collect, store, or intermediate" sentence, the hosted-platform exemption and the name-and-logo paragraph are absent on 2026-08-16 and present on 2026-08-30. They are unchanged in every snapshot since then, the latest from 2026-10-20, and on the live page.

Inference:
- The texts name Banda's case: the unmodified binary on a hosted platform, each user on their own subscription, Banda paying for nothing.
- The condition is the Commercial Terms. The text doesn't say who "you" is when an open-source tool is self-hosted by a team. The likely reading is the team or company that runs it, not Banda's authors. That party would agree to the Commercial Terms even if every user signs in with a Pro or Max plan, which falls under the Consumer Terms.
- The binary must not be restricted in "any authentication method built into it". Banda takes nothing away from the binary, but it refuses Secrets named `ANTHROPIC_API_KEY` and its like outside API key mode (PLAN.md, Constraint, point 3). That is a choice about Banda's own UI, not a change to the binary. Worth one line in the question anyway.

**The login flow.** Legal and compliance, "Authentication and credential use":

> "**OAuth authentication** is intended exclusively for purchasers of Claude Free, Pro, Max, Team, and Enterprise subscription plans and is designed to support ordinary use of Claude Code and other native Anthropic applications."
>
> "Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow."

[Authentication](https://code.claude.com/docs/en/authentication), "Log in to Claude Code":

> "If your browser shows a login code instead of redirecting back after you sign in, paste it into the terminal at the `Paste code here if prompted` prompt. This happens when the browser can't reach Claude Code's local callback server, which is common in WSL2, SSH sessions, and containers."

Inference: the sign-in happens on claude.ai in the user's own browser. The binary is what asks for and stores the result. But in Banda the user pastes the code into a web terminal, and the keystrokes travel browser → API → runner → PTY (PLAN.md, Architecture, "Terminal"). Banda doesn't keep them, but the code passes through Banda's code. Whether a one-time authorization code counts as a "credential or session token", and whether passing it on unread counts as "intermediating", is not answered by the text. Anthropic's docs show the same paste over SSH, which is also a relay. The [IAM](https://code.claude.com/docs/en/iam) page now serves the same content as Authentication.

**Persistence.** Authentication, "Log in to Claude Code":

> "**Earlier sign-in**: you ran `claude auth login` in your shell before your first session, or you rebuilt a dev container that keeps your credentials in a volume."

[Development containers](https://code.claude.com/docs/en/devcontainer), "Persist authentication and settings across rebuilds":

> "By default, the container's home directory is discarded on rebuild, so engineers must sign in again each time."
>
> "To keep the directory and the file, mount a named volume at `~/.claude` and set `CLAUDE_CONFIG_DIR` to the same path so Claude Code writes `.claude.json` inside the volume."

Same page, warning:

> "When executed with `--dangerously-skip-permissions`, dev containers do not prevent a malicious project from exfiltrating anything accessible inside the container, including the Claude Code credentials stored in `~/.claude`."

Inference: Banda's per-user volume is this mechanism. The docs show it for an engineer's own container, though, not for a platform that keeps volumes for its users. The binary writes and reads the file, and Banda never mounts the volume into the API. Whether a hosted platform keeping that volume is still "the binary storing its own login", or the platform "storing Claude.ai credentials", the text doesn't say. Both readings are possible.

**Automated turns.** [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) (effective Oct 8, 2025), §3:

> "Except when you are accessing our Services via an Anthropic API Key or where we otherwise explicitly permit it, to access the Services through automated or non-human means, whether through a bot, script, or otherwise."

[Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan):

> "Update October 7, 2026: Claude Max and Team plans now include monthly API credits, which cover the Claude Agent SDK, claude -p, the Claude API, and Claude Managed Agents. You can still use the Claude Agent SDK, claude -p, and third-party apps with your subscription limits."

[Monthly API credits for Max and Team plans](https://support.claude.com/en/articles/17154008-monthly-api-credits-for-max-and-team-plans):

> "When you're signed in with your Claude plan instead, claude -p and Agent SDK usage still draw from your plan's usage limits and don't use your API credits."

Inference: Banda's runner drives `claude -p --input-format stream-json`. The support article reads as the explicit permission the Consumer Terms leave room for, as long as a person sends the prompt. Turns no person started are less clearly covered: an agent creating a sub-thread through MCP, or `wake_parent` waking the parent.

## Q2. Teammate turns and shared use

[Consumer Terms](https://www.anthropic.com/legal/consumer-terms) (effective Oct 8, 2025), §2, in full:

> "You may not share your Account login information, Anthropic API key, or Account credentials with anyone else or make your Account available to anyone else. You are responsible for all activity occurring under your Account and agree to notify us immediately if you become aware of any unauthorized access to your Account by sending an email to support@anthropic.com."

Note (2026-10-25, [proposed-prompts.md](proposed-prompts.md#re-check-of-claude-termsmds-quotes)): anthropic.com serves the Consumer Terms by country (`x-middleware-rewrite: /legal/consumer-terms/DE`). The quote above is the EEA version (Anthropic Ireland, [/legal/consumer-terms/DE](https://www.anthropic.com/legal/consumer-terms/DE)). The US version ([/legal/consumer-terms/US](https://www.anthropic.com/legal/consumer-terms/US), Anthropic, PBC; same effective date) words the same rule as two sentences: "You may not share your Account login information, Anthropic API key, or Account credentials with anyone else. You also may not make your Account available to anyone else." The EEA version also has, in §11, "**Non-commercial use only.** You agree that you will not use our Services for any commercial or business purposes", which the US version lacks. It rules out work use of Pro/Max in the EEA under the strict reading (Decision, 2026-10-25). The other Consumer Terms quotes here are the same in both versions.

Same, §3:

> "You are responsible for all activity under the account through which you access the Services."

[Purchase and manage seats on Team plans](https://support.claude.com/en/articles/12004354-purchase-and-manage-seats-on-team-plans), "Who can use a seat":

> "Each seat on a Team plan is assigned to one named individual, and only that person may use it. This means:
> Only the assigned user may sign in to the account. Users can't share login credentials or let others use their account.
> Users can't route their work through another user's account to use that user's remaining usage.
> Unused usage on one seat can't be transferred to another user."

[Purchase and manage seats on Enterprise plans](https://support.claude.com/en/articles/13393991-purchase-and-manage-seats-on-enterprise-plans):

> "Each Enterprise seat is assigned to one named individual, and only that person may use it."
> "Only the assigned user may sign in to the account. Users can't share login credentials or let others use their account.
> Users can't use another user's account to get around their own spend limits or usage limits."

[Log in to your Claude account](https://support.claude.com/en/articles/13189465-log-in-to-your-claude-account), "Authenticating to subscription plans":

> "Subscription plans can only be used by subscribers, and the usage included in these plans is designed to support ordinary use of native Anthropic applications, including the Claude web, desktop, and mobile applications and Claude Code."
>
> "Use of third-party tools that misrepresent their identity to Anthropic's servers, attempt to route third-party traffic against subscription limits, or otherwise violate applicable terms or policies is prohibited and such use may be enforced against."

Legal and compliance, "Acceptable use":

> "Advertised usage limits for Pro and Max plans assume ordinary, individual usage of Claude Code and the Agent SDK."

[Usage Policy](https://www.anthropic.com/legal/aup), version **effective Nov 12, 2026**, "Do Not Abuse Our Platform":

> "Resell, proxy, or otherwise provide access to Claude through unauthorized means, including services that route requests through consumer subscriptions or misrepresent the product or client being used"

The live page already shows this version. The Wayback snapshot of 2026-09-01 shows the version effective September 15, 2025, which doesn't contain the sentence. The 2026-10-18 snapshot shows the new version. Until 2026-11-12 the old version applies.

Inference:
- In Banda, the owner's login stays in the owner's pod and the teammate never sees it, so the "share … login information … or Account credentials" half doesn't fit.
- The other halves do. A teammate's approved turn is a prompt written by someone else that runs on the owner's account, so it can read as "make your Account available to anyone else" (Pro/Max), "let others use their account" and "route their work through another user's account" (Team), or "provide access to Claude … through consumer subscriptions" (Usage Policy, from 2026-11-12).
- The owner's approval makes the owner the one who chooses to run the turn. Whether that makes it the owner's use, like the owner pasting a teammate's request into their own terminal, or a teammate's use, is the question. The text has no notion of "with the subscriber's approval".
- Trusted threads, where a teammate's prompt runs with no approval, are the furthest from "only that person may use it".
- Handoffs are not exposed. After an accept, turns run on the new owner's own login (PLAN.md, Constraint, point 10).

## Q3. Open-sourcing a tool that wraps the CLI

[Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview), note under "Get started":

> "Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK. Use the API key authentication methods described in the Quickstart instead."

The same sentence is in the 2026-04-08 snapshot. Back then it ended "Please use the API key authentication methods described in this document instead."

Same page, "License and terms":

> "Use of the Claude Agent SDK is governed by Anthropic's Commercial Terms of Service, including when you use it to power products and services that you make available to your own customers and end users"

Legal and compliance, "Authentication and credential use":

> "**Developers** building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication through Claude Console or a supported cloud provider."
>
> "For questions about permitted authentication methods for your use case, please contact sales."

[Log in to your Claude account](https://support.claude.com/en/articles/13189465-log-in-to-your-claude-account):

> "The preferred way to access Anthropic services using third-party software, tools, or services (“third-party tools”), including open-source projects, is through API key authentication through Claude Console or a supported cloud provider. Anthropic may at its discretion allow paid subscribers who have enabled usage credits to use certain third-party tools to access Anthropic services included in paid subscription plans, but reserves the right to draw use of such third-party tools from usage credits rather than subscription limits."
>
> "If you're building a product, application, or tool for others, use API key authentication through Claude Console or a supported cloud provider."

[anthropics/claude-code README](https://github.com/anthropics/claude-code/blob/main/README.md) points only to the Commercial Terms and the Privacy Policy. Its [CHANGELOG](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md) (latest 2.1.309) has no entry on terms or third-party use.

Inference:
- The SDK note's scope is "their products", with the SDK named as one case ("including"), so a CLI wrapper is not outside it just because it doesn't use the SDK. The legal page frames the same rule as being about "products or services that interact with Claude's capabilities".
- Against that stands the explicit exemption: "an end user signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code". Banda fits its words.
- What stays open for an open-source release:
  - Banda's Settings has a "Log in to Claude" screen, its login environment, and shows the login state. Is that "offering claude.ai login" in Banda's product, or the binary's own login shown in a terminal?
  - The support article's "open-source projects" and "a product, application, or tool for others" point to API keys as "preferred". The words are "preferred" and "use", not "must".
  - The support article lets Anthropic draw third-party tool usage "from usage credits rather than subscription limits". Is a hosted unmodified binary a "third-party tool" in that sense? Banda would then cost users extra usage, not plan usage.
- Who would agree to the Commercial Terms is open as well (Q1): an open-source project has no single "customer".

## Q4. Alternatives the text allows

Listed only so the trade-off is visible. Principle 1 stays as it is.

Legal and compliance:

> "This does not restrict how customers provision and manage their own API keys or third-party inference provider credentials — for example, configuring an API key in a development environment, secrets manager, or machine image for use by the customer's own authorized users — provided the resulting usage is billed to the key owner under their agreement with Anthropic (or the applicable provider) and is not resold or intermediated as described above."

[Authentication](https://code.claude.com/docs/en/authentication):

> "Claude Code supports multiple authentication methods depending on your setup. Individual users can log in with a claude.ai account, while teams can use Claude for Teams or Enterprise, the Claude Console, or a cloud provider like Amazon Bedrock, Google Cloud's Agent Platform, or Microsoft Foundry."

> "**Claude for Teams**: self-service plan with collaboration features, admin tools, SSO, billing management, and server-managed settings for organization-wide Claude Code configuration. Best for smaller teams."

[Self-hosted environments](https://code.claude.com/docs/en/self-hosted-environments):

> "Self-hosted environments are in public beta on Team and Enterprise plans and are off by default."

| Option | What the text allows | Teammate turns | Cost to principle 1 |
|---|---|---|---|
| Each user's own subscription (today) | Allowed on a hosted platform | Q2 | none |
| Team/Enterprise seats | Same as above; one named person per seat | Same as Q2 ("route their work through another user's account") | none |
| A team API key (Console, Bedrock, Vertex AI, Foundry) | Allowed "for use by the customer's own authorized users", billed to the key owner | Text allows it: the team is the key owner | Gives up "each user's own subscription"; the key is long-lived, so principle 2 asks for a gateway or `apiKeyHelper` rather than the key in pods |
| Each user's own API key (Banda's API key mode) | Allowed | Same as a subscription: someone else's key | none beyond cost |
| Self-hosted environments | Team/Enterprise beta; sessions started from claude.ai | Not Banda's model | Replaces Banda's agent loop |

Inference: only the team-key row makes teammate turns plainly allowed by the text, and it is the one that departs from principle 1.

---

## Where PLAN.md's "Constraint: Claude subscriptions" doesn't match the text

1. **The Commercial Terms condition is missing.** PLAN.md: "Hosting Claude Code in sandboxes is allowed if the binary is unmodified and each end user authenticates with their own credentials." The text adds that it "requires agreeing to our Commercial Terms of Service", "unless we've mutually agreed otherwise".
2. **The API key rule is broader than the SDK.** PLAN.md: "Products built on the Agent SDK must use API keys." The text: "Developers building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication". The SDK page's ban on offering claude.ai login is for "their products, including agents built on the Claude Agent SDK". Banda is outside it only through the hosted-binary exemption, not because it doesn't use the SDK.
3. **The exemption is quoted without its condition.** PLAN.md stops at "…including where a platform hosts Claude Code". The text goes on: "as described under *Can customers offer Claude Code in their products?* above", which brings in the Commercial Terms and the conditions of point 1.
4. **"Ordinary, individual usage" is weaker than the texts that matter.** PLAN.md point 5: "the terms assume 'ordinary, individual usage' by the subscriber". That sentence is about how "advertised usage limits" are sized. The texts that speak to teammate turns are elsewhere and stronger, and PLAN.md doesn't cite them: the full Consumer Terms sentence ("or make your Account available to anyone else"), the Team seat rule ("Users can't route their work through another user's account to use that user's remaining usage") and the Usage Policy line in force from 2026-11-12.
5. **"Banda's code never sees a token"** (point 2). It is true for tokens. But the login code the user pastes travels through Banda's API and runner stream to the PTY. Whether that code is a "credential" under "may not … intermediate" is open (Q1).
6. **Team/Enterprise users fall under the Commercial Terms, Pro/Max under the Consumer Terms.** PLAN.md cites only the legal page and treats subscriptions as one case. The Team and Enterprise seat rules are the strictest on shared use.
7. **The source date.** PLAN.md cites the legal page "checked 2026-10-19". It is unchanged on 2026-10-24, but the Usage Policy changed in the meantime: the new version is published and in force from 2026-11-12. Re-check the pages when they change.
8. In `docs/research/claude-login.md`, §5:
   - The Consumer Terms quote stops before "or make your Account available to anyone else".
   - The new legal sections are dated to "between 2026-08-04 and 2026-10-19". They narrow to 2026-08-16 to 2026-08-30.
   - The Recommendation refers to "PLAN.md open question 6", which is now question 3.

## Decision (2026-10-24)

No message goes to Anthropic. Banda complies with the text as written, and takes the strict reading where it is unclear:

| Point | Strict reading | How Banda meets it |
|---|---|---|
| Hosting (1a) | Whoever runs Banda has agreed to Anthropic's Commercial Terms | A prerequisite on the install page (docs/deployment.md) and in the README; Banda can't check it |
| Work use of Pro/Max in the EEA (added 2026-10-25) | The EEA Consumer Terms, §11: "you will not use our Services for any commercial or business purposes" | Banda is a work tool, so in the EEA and Switzerland each person runs Claude Code on a Team or Enterprise seat or an API key, not Pro/Max: a prerequisite on the install page and in the README; Banda can't check it |
| Teammate turns (2) | Nobody but the subscriber starts a turn on a subscription | Teammate turns and trust only when the owner runs Claude Code on an API key (PLAN.md, Teammate turns) |
| The login code (1c) | Banda never collects, stores or reads a Claude credential, and sign-in completes in Anthropic's flow | The user runs the binary's own `claude auth login` and signs in on claude.ai in their own browser. The code they paste passes through the terminal stream as through an SSH session, which Anthropic's docs describe for containers; Banda neither logs nor stores it, and learns the login state from `claude auth status`, not the credentials file |
| Offering login (3) | No Claude login form of Banda's own | Banda's buttons open a terminal running the binary's login, named as Claude Code's |
| The login volume (1d) | Only the mechanism Anthropic documents for dev containers, for one user | Each user's volume is mounted only by their own pods, never by the API |
| Turns no person started (1e) | Only the owner's own agent, for the owner, through `claude -p` (explicitly allowed) | Agents' sub-threads are their owner's; `wake_parent` wakes once, then only after a person posted; depth and running caps |
| Names and logos | Plain text only | "Runs Claude Code" in text; no Claude or Anthropic logo |

Re-check the quoted pages when they change.
