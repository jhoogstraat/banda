# Proposed prompts: a teammate proposes, the subscriber sends

Researched 2026-10-25 for a product owner's idea: on a subscription, a teammate's `@agent` post becomes a proposal that the thread's owner sends, like a group order that only the host checks out. This builds on [claude-terms.md](claude-terms.md) (2026-10-24), its Q2 and its Decision. Every quote below was retrieved on 2026-10-25 and is copied word for word. All sources are primary: Anthropic's legal pages, the Claude Code docs on code.claude.com, claude.com's Claude Tag docs and Anthropic support articles. As in claude-terms.md, two kinds of statement are kept apart:
- **Text:** what a page says, quoted.
- **Inference:** marked as such. This is how the text reads onto Banda, not a legal conclusion. Only Anthropic can settle these points, and the product owner decided not to ask (claude-terms.md, Decision).

## TL;DR

- **The idea as stated (a bubble with a Send button that runs the teammate's text as the owner's turn) is the turn-request card that Banda already has on an API key, with "Approve" renamed "Send".** claude-terms.md Q2 found that the owner's approval "doesn't change whose account the turn runs on", and the Decision ruled it out on a subscription. Nothing in the text has changed since then. Under the standing strict reading, variant (a) is a no.
- **"Use as draft" (b) is the variant the text has least to say against.** The proposal goes into the owner's composer, and the owner edits it and sends it as their own message. That is the copy-paste analogy with one fewer step, and in the end it is the owner's input ("You are responsible for all Inputs you submit"). It meets the Decision's rule as written: the subscriber starts the turn. It is a small change, web only.
- **What decides the case is whose work it is, not who clicks.** The Team seat rule targets the work: "Users can't route their work through another user's account to use that user's remaining usage". A teammate steering the owner's own task (reviewing, correcting, adding a requirement) reads as the owner's own use. A teammate getting *their own* task done on the owner's subscription reads as the routing the rule forbids, whoever presses Send and however the prompt is worded. Banda can't tell the two apart, so the guard rails have to point the second case to Fork.
- **Anthropic's own products split the same way.** Where several people hand Claude work in a shared space, Anthropic bills the organization, not a member's seat: Claude Tag channel work ("bills to the organization, not to you") and Code Review ("not to the developer who opened the PR or requested the review"). Where a session runs on a person's account, that person starts it: the earlier Claude in Slack ("Each session runs under your own Claude account"), and view-only shared sessions and chats ("can't reply … or continue the conversation"). Two documented features don't fit this split. The GitHub Action on a `CLAUDE_CODE_OAUTH_TOKEN` runs "whenever someone mentions `@claude`", on "the subscription of the person who ran `claude setup-token`". Channels let other allowlisted senders reach your session. Neither amends the terms or the seat articles, and the strict reading doesn't treat them as permission.
- **Invite links to people outside the team (f) make every reading worse**: an outsider is "anyone else" and has no seat in the organization ("To give someone else access, assign them their own seat"). They are also outside Banda's one-team model.

## Summary

Exposure is an **inference**, on the same scale as claude-terms.md. *Clear* means the text names the case as allowed or plainly the owner's own use. *Unclear* means no text decides it. *Conflicts* means a text, read as written, goes against it. "Owner's task" means the teammate steers the owner's work. "Teammate's task" means the teammate gets their own work done through the owner's thread.

| # | Variant | Pro/Max (Consumer Terms) | Team/Enterprise seat | Strict reading (the Decision) |
|---|---|---|---|---|
| a | One-click Send of the teammate's text, verbatim, as the owner's turn | Owner's task: **unclear**. Teammate's task: **conflicts** ("make your Account available to anyone else"; from 2026-11-12 "services that route requests through consumer subscriptions") | Owner's task: **unclear**. Teammate's task: **conflicts** ("route their work through another user's account") | **No**: it is the approve card, which claude-terms.md Q2 and the Decision already rejected on a subscription |
| b | "Use as draft": the proposal goes into the owner's composer; the owner edits or sends it as their own message | Owner's task: **clear to unclear** (the owner's own input). Teammate's task: **unclear to conflicts** | Owner's task: **clear to unclear**. Teammate's task: **conflicts** in substance | **Meets it as written**: the subscriber starts every turn. The substance test of (e) still applies |
| c | Status quo: teammates' posts reach the agent as context with the owner's own prompt | Owner's task: **clear to unclear**. Teammate's task: as (b) | Same as (b) | Meets it (decided 2026-10-24) |
| d | The prompt names the proposer ("bob asks: …") or not | Naming changes nothing about whose account pays. It makes (a) read as bob's request, which is honest. Hiding it would look like disguising someone else's use as the owner's | Same | Name people truthfully, as the prompt does today; never strip a name to make a turn look like the owner's |
| e | Whose work: the owner's task vs the teammate's task | The deciding factor: owner's task → owner's use; teammate's task → "anyone else" | The deciding factor: teammate's task matches "route their work … to use that user's remaining usage" word for word | Only the owner's own task; a teammate's own task goes to Fork |
| f | Invite links to people outside the team | **Conflicts** ("anyone else") | **Conflicts** ("only that person"; "assign them their own seat") | **No** |

---

## Q1. What the text says

### Texts on someone else using the account

The texts are in [claude-terms.md](claude-terms.md#q2-teammate-turns-and-shared-use). The seat articles add:

- [Team seats](https://support.claude.com/en/articles/12004354-purchase-and-manage-seats-on-team-plans), "Who can use a seat":
  > "If a user regularly needs more capacity, an owner can move them to a Premium seat or turn on usage credits."
  >
  > "Reassigning a seat is meant for normal team changes, such as a user joining, leaving, or changing roles. It isn't designed for rotating one seat among several active users."
- [Enterprise seats](https://support.claude.com/en/articles/13393991-purchase-and-manage-seats-on-enterprise-plans), the same, and: "To give someone else access, assign them their own seat."

### Texts on inputs, "on behalf of" and helping others

Consumer Terms (both versions), §3 and §4:

> "You are responsible for all activity under the account through which you access the Services."
>
> "You may not access or use, or help another person to access or use, our Services in the following ways:"

(The list that follows names unlawful use, competing products, reverse engineering, scraping, unauthorized access, infringement, "automated or non-human means" and similar. It says nothing about relaying another person's request.)

> "Our Services may generate responses (we call these “Outputs”) or enable the Services to take actions on your behalf, such as software manipulation, data processing, and system interactions (we call these "Actions"), based on your Inputs."
>
> "You are responsible for all Inputs you submit to our Services and all Actions. You must ensure that you have all rights, licenses, and permissions that are necessary for us to process such Inputs in accordance with our Terms and to provide the Services to you, including for example, to integrate with third-party services, to share Materials with others at your direction, and to take Actions."

[Commercial Terms](https://www.anthropic.com/legal/commercial-terms) (effective Jun 17, 2025), which cover Team and Enterprise:

> "Customer is responsible for all activity under its account."

Inference:
- "On behalf of" appears in the texts only for actions Claude takes for *you* ("Actions … on your behalf") and for developers routing requests "on behalf of their users". No text speaks of a subscriber acting on another person's request or suggestion.
- The Consumer Terms make the subscriber responsible for "all Inputs you submit", wherever the words came from. Sharing outputs with others "at your direction" is contemplated. That covers Banda's open visibility, not other people's inputs.
- "help another person to access or use" is the lead-in to a list of forbidden ways. It shows the terms contemplate a subscriber helping others, but it doesn't say whether helping is allowed outside the list.

### Anthropic's own collaboration features: who starts, whose plan pays

**The earlier Claude Code in Slack.** [Claude Code in Slack](https://code.claude.com/docs/en/slack):

> "This page documents the earlier Claude Code in Slack, which runs each session under an individual user's account. It answers channel @mentions only from Pro and Max accounts, and only in a Slack workspace that no organization has connected to Claude Tag. Claude Tag runs @Claude as your organization's shared identity with admin-configured access."
>
> "Each session runs under your own Claude account, using your connected repositories and your plan limits."
>
> "**From threads**: When you @mention Claude in a thread, it gathers context from all messages in that thread to understand the full conversation."

Its table: "Claude Code Sessions | Each user runs sessions under their own Claude account" and "Usage & Rate Limits | Sessions count against the individual user's plan limits".

**Claude Tag**, Anthropic's team Slack product. [Claude Tag overview](https://claude.com/docs/claude-tag/overview):

> "Claude Tag is available on Team and Enterprise plans, on Anthropic’s first-party service. It isn’t available on individual plans (Free, Pro, or Max), or for third-party deployments."
>
> "team work in shared channels is Claude Tag; personal work on your own files is Cowork or Claude Code."
>
> "Slack users don’t each need a Claude seat to work with Claude in channels."
>
> "Channel work bills by usage to your organization’s usage balance, under a spend limit an Owner sets."
>
> "In one-to-one DMs: a DM from a member who has connected a Claude account runs on that account and bills to that person’s seat."
>
> "One-to-one direct messages from members who have connected a Claude account don’t draw from this balance. Such a DM runs on the sender’s own claude.ai account and follows that seat’s usual usage limits, so the organization spend limit doesn’t apply to it."
>
> "Anyone in the channel can hand it work, and channel work bills to the organization, not to you."

**Code Review.** [Code Review](https://code.claude.com/docs/en/code-review): "someone with write access to the repository can comment `@claude review` on a pull request to start a review". [Code Review costs](https://code.claude.com/docs/en/code-review-costs):

> "Code Review is billed on token usage through usage credits, separately from your plan's included usage. Reviews are billed to your organization, not to the developer who opened the PR or requested the review."

**Cloud sessions and shared chats are view only.** [Use Claude Code in the cloud](https://code.claude.com/docs/en/claude-code-on-the-web), "Share sessions":

> "To share a session, toggle its visibility according to the account types below. After that, share the session link as-is. Recipients see the latest state when they open the link, but their view doesn't update in real time."
>
> "**Visibility options**: **Private** and **Team**. Team visibility makes the session visible to other members of your claude.ai organization"
>
> "**Visibility options**: **Private** and **Public**. Public visibility makes the session visible to any user logged into claude.ai" (Max or Pro)

Continuing a cloud session in a terminal requires "the Claude Code CLI signed in to the same claude.ai account". [Share a chat with specific people](https://support.claude.com/en/articles/16762496-share-a-chat-with-specific-people):

> "Invited people can view a snapshot of the chat but can't reply, copy it into their own account, or continue the conversation."
>
> "Can I give someone edit access or let them continue the chat?
> No. Shared chats are view only."

**Shared projects on claude.ai.** [Project visibility and sharing](https://support.anthropic.com/en/articles/9519189-project-visibility-and-sharing), "Can use": "Members can see project contents, knowledge, and instructions, and chat within the project, but cannot edit it." And: "This will share the project and knowledge base with the member, but your chats will remain private unless you choose to share them."

**The GitHub Action on a subscription token.** [Claude Code GitHub Actions](https://code.claude.com/docs/en/github-actions):

> "`CLAUDE_CODE_OAUTH_TOKEN`: an OAuth token that authenticates with your Claude subscription, available on Pro, Max, Team, and Enterprise plans. Generate one by running `claude setup-token` locally."
>
> "As committed, Claude responds whenever someone mentions `@claude` in an issue or pull request, authenticating with the `ANTHROPIC_API_KEY` secret. If you added `CLAUDE_CODE_OAUTH_TOKEN` instead, change the workflow's `anthropic_api_key` line to `claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}`."
>
> "**Write access**: on issue and pull request events, the triggering user must have write access to the repository."
>
> "For a secret shared across repositories, authenticate with an API key from the Claude Console rather than an OAuth token, since an OAuth token is tied to the subscription of the person who ran `claude setup-token`."
>
> "If you authenticate with an OAuth token, runs use your Claude subscription instead of API billing."

**Channels.** [Push events into a running session with channels](https://code.claude.com/docs/en/channels) (research preview):

> "By default, only your own messages pass through. To let another contact reach Claude, add their handle"
>
> "Every approved channel plugin maintains a sender allowlist: only IDs you've added can push messages, and everyone else is silently dropped."

Inference:
- Where several people hand Claude work in a shared space, Anthropic's team products bill the **organization's usage balance, not any member's seat** (Claude Tag channels, Code Review). Where work runs on a person's account, it is that person's own request (the earlier Slack app, Claude Tag DMs, cloud sessions, projects where each member chats on their own account). Viewers of a shared session or chat **cannot continue it**. The idea under study, others' requests run on one member's seat, is the case Anthropic's own team products are built to avoid.
- Two documented features run other people's messages on one subscriber's account. The GitHub Action on an OAuth token answers "whenever someone mentions `@claude`" from anyone with write access, with no approval at all. Channels pass messages from contacts the subscriber allowlists. They are the strongest texts for the permissive reading: Anthropic documents them with subscriptions. But they are feature docs, not terms. They don't amend "only that person may use it", the same GitHub page steers shared setups to an API key, and the token is configured once by its owner rather than run per request. Under the strict reading they show that Anthropic tolerates some shared triggering. They don't permit it.
- No text found speaks to "proposals" or "suggestions" that a subscriber then runs.

## Q2. The variants against the text

Facts about Banda that every variant shares:
- The owner's login stays in the owner's pod. The teammate never holds it or "signs in", so the credential halves of the texts ("share … login information", "Only the assigned user may sign in") don't apply.
- Teammates already read the whole thread, and their posts already reach the agent. `agentPrompt` (`internal/api/prompt.go`) puts every message posted since the last turn in front of the owner's next prompt, a line each as "bob: …".
- The owner is the one Banda calls the subscriber. Everything turns on whether running a teammate's words is "the owner's use".

### a. One-click Send, verbatim, as the owner's turn

What it is: on a subscription a teammate's `@agent` post becomes a card, and the owner's click runs it. Banda already has this card on an API key (turn requests, `internal/api/turns.go`), where an approved request "runs as the requester's turn". Variant (a) would run it as the owner's.

- **For the owner's use:** the owner reads it and decides, and the click is the owner's act. It is "Inputs you submit" in the sense that the owner submits them.
- **Against:** the input is written by someone else and sent unchanged. The product is built so that others' requests run on one account. Anthropic's own split (Q1) puts that case on the organization's balance, not a seat. claude-terms.md Q2 already weighed exactly this: "The owner's approval makes the owner the one who chooses to run the turn … The text has no notion of 'with the subscriber's approval'." The Decision then ruled: "Nobody but the subscriber starts a turn on a subscription", and Banda met it by offering requests only on an API key.
- **Pro/Max:** "make your Account available to anyone else" doesn't say whether a person's requests count as making the account available when the subscriber presses a button. That is unclear. For a teammate's own task it leans to conflicts. From 2026-11-12, Banda is a service whose feature would "route requests through consumer subscriptions".
- **Team/Enterprise:** "only that person may use it" and "let others use their account". For a teammate's task, "route their work through another user's account to use that user's remaining usage" fits word for word, and the click doesn't change that.
- **Strict reading:** the click is the subscriber's, so the Decision's literal wording could be argued either way. But the Decision was made on the claude-terms.md analysis that approval doesn't make a teammate's turn the owner's. Renaming Approve to Send doesn't answer that analysis. **No.**

### b. "Use as draft"

What it is: the teammate's proposal stays a post in the chat. The owner gets a "Use as draft" button that puts its text into their own composer. There the owner edits it, adds to it, or sends it as it is, as their own message. Nothing runs until the owner sends from their composer, and the message is the owner's (`by` = owner).

- **For the owner's use:** this is the copy-paste analogy. The final input is the owner's own message in the owner's composer, and the terms make the subscriber responsible for "all Inputs you submit" without asking where the words came from. Nobody reads "only that person may use it" as forbidding an engineer from pasting a colleague's bug report into their own terminal. The owner can and often will change it, so the input is theirs in fact, not just by a click.
- **Against:** if the owner sends it unchanged, the difference from (a) is one click in a different place. The substance test of (e) still applies: a teammate's own task done this way is still their work routed through the owner's account.
- **Pro/Max:** for the owner's task, **clear to unclear**: it is the owner's own input on the owner's own work. For a teammate's task, **unclear to conflicts**, as in (a).
- **Team/Enterprise:** the same split. The routing sentence is about work, not keystrokes.
- **Strict reading:** met as written: only the subscriber starts the turn, from their own composer, with text they could change. That holds if Banda also points a teammate's own tasks to Fork (guard rails, Q4).

### c. The status quo

A teammate's post, `@agent` included, is a plain post that the agent reads with the owner's next prompt. Decided on 2026-10-24; proposals have since left it (As built).

- In substance it is close to (b): the teammate's words reach the agent verbatim ("bob: @agent …"), and the owner's next prompt can be as short as "do what bob says". The status quo already accepts that teammates' words reach the agent with the owner's turn. What it rules out is a teammate's post starting a turn.
- (b) adds no exposure over (c). It is arguably clearer, because the owner sees and owns exactly what is sent instead of a short instruction plus context they may not reread.

### d. Naming the proposer

Today's prompt names everyone ("bob: …", and "alice: …" for the owner's own prompt when others posted). For a teammate's approved turn on an API key, "the prompt says who asked".

- **Naming** changes nothing about whose account pays. In (a) it puts on record, in the prompt itself, that the turn is bob's request. That is honest, and it is also the plainest evidence against "owner's use".
- **Not naming**, to make a teammate's request look like the owner's, would change nothing in substance either. It would read as disguising someone else's use: against principle 7 (honest state), and close in spirit to the Usage Policy's "misrepresent". Don't do it.
- **In (b)** the question disappears. The proposal stays in the chat as bob's post, which the agent sees as "bob: …", and the sent message is the owner's. If the owner writes "as bob suggests, …", that is the owner's own wording.

### e. Whose work it is

This is the axis the texts actually turn on.

- **The owner's task** (bob reviews alice's diff, points out a bug, proposes the next step, adds a requirement for alice's feature): the work, the decision and the result are alice's. It is the same as alice acting on a review comment or a Slack message. The strongest case for "ordinary, individual usage" by the subscriber, under every variant where alice sends.
- **A teammate's task** (bob has no subscription, or ran out, and has alice's thread do bob's thing): "route their work through another user's account to use that user's remaining usage" (Team) and "make your Account available to anyone else" (Pro/Max) fit it as written, whichever variant is used. The Enterprise article answers it directly: "To give someone else access, assign them their own seat." In Banda that is Fork, on bob's own login.
- Banda can't tell these apart by looking at a post. The design has to make the owner's task the path the product offers and the teammate's task the one it points away from.

### f. Invite links to people outside the team

Banda has no guests. Whoever signs in through the team's identity provider is on the team and reads every thread (VISION, "Who it is for"). Invite links would add users who aren't in the team, and may not be in the Claude organization.

- Pro/Max: an outsider is "anyone else" in the plainest sense.
- Team/Enterprise: an outsider holds no seat in the organization. "Each seat … is assigned to one named individual", and "To give someone else access, assign them their own seat."
- From 2026-11-12, letting the public propose prompts to a subscriber's agent is close to "provide access to Claude through unauthorized means".
- It is also multi-tenancy in miniature, a non-goal, and it opens a new class of untrusted input to the owner's pod (PLAN.md Constraint 5). **No**, under any reading.

### The analogies, honestly

- **Copy-paste into your own terminal.** *For:* the terms regulate who uses the account and whose inputs they are, not where the words come from. An owner who reads a proposal and sends it from their own composer is doing what every engineer does with a Slack message. *Against:* a product feature is evidence of design. The texts aimed at products ("services that route requests through consumer subscriptions", developers routing "on behalf of their users") target exactly a service built to turn many people's requests into one subscriber's usage. A person copying by hand is an individual's habit; a one-click Send button is a pipeline. Friction is not in the text, but "Send" on someone else's bubble reads as designed routing in a way that "Use as draft" into your own composer does not.
- **The Uber Eats group order.** The analogy cuts against the idea. In a group order everyone adds their own items and the host pays for all of them. That is one person's account paying for others' wants: what the seat rule calls routing "their work through another user's account to use that user's remaining usage". The parallel that fits the text is the owner's own order with friends' advice on what to pick (e), not friends ordering for themselves on the owner's card.

## Q3. Recommendation

**Variant (b), "Use as draft", and not (a).** It meets the Decision as written: only the subscriber starts a turn, from their own composer, with an input that is theirs to change. It adds no exposure over the status quo, which already puts teammates' words in front of the agent with the owner's prompt. Variant (a), the idea as stated, is the API-key approve card under another name. claude-terms.md and the Decision rejected it for a subscription, and nothing in the text has moved since. (f) is out under any reading.

Guard rails that keep (b) inside the strict reading:
1. **No Send on someone else's bubble.** The only way a proposal reaches the agent as a turn is through the owner's own composer, as the owner's own message. No "Send as is", no "Use all", no queue of proposals that sends itself.
2. **Nothing changes for turns.** `thread.Apply` stays as it is: on a subscription a teammate's post starts nothing, there is no trust, and turn requests stay API-key only.
3. **Steering, not delegating, in the words Banda uses.** The teammate's composer line says what the feature is for, and points their own work to Fork. For example: "Only alice runs the agent (their Claude subscription). Suggest a prompt with @agent, and alice decides; for your own task, Fork."
4. **Truthful attribution.** The proposal stays in the chat as the teammate's post. The owner's message is the owner's. Banda never strips or rewrites names to make one look like the other (d).
5. **Team members only.** No invite links or guests (f).

Open risks:
- **The substance test can't be enforced.** An owner who sends a teammate's own tasks unchanged, as a habit, is doing what the Team seat rule forbids, and (b) makes that one click easier. The guard rails make it the path the product points away from, not an impossible one.
- **"Services that route requests through consumer subscriptions"** (from 2026-11-12) is about services, and no text says whether a draft button counts as routing. The strict reading holds only because a person writes or adopts the input every time.
- **Anthropic's own split** (Q1): shared work bills to the organization, a person's work to their seat. A team that wants many people to hand the agent work is, by Anthropic's own design, in API-key or organization-billed territory. In Banda that is API key mode, where turn requests already exist.

## As built

Variant (b), with these additions:
- **The API records a proposal** (`Message.proposal`) when the post is stored: not a turn request, no teammate's turn possible, by a person who isn't the owner, mentioning the agent. A label computed in the browser followed the thread's current auth, so a trusted teammate's real turn would have read as an unsent proposal after a move to a subscription. A client can't set the flag. Messages from before it have none and show as plain posts: what the auth was then isn't recorded.
- **Files and review comments come along**, as chips and as pending review comments in the composer. The comments are editable there and show the quoted code, because everything of a teammate's that the owner sends must be theirs to read and change. The files stay the teammate's uploads: the API refuses someone else's upload, except for a thread's owner and the files on a proposal in that thread. Files can only be kept or removed, not edited.
- **A proposal is never context.** Variant (c) stands for chat, but no longer for proposals: left in, an `@agent` post the owner ignored would still reach the agent with their next prompt, or with the prompt waiting while the environment starts, so "the owner decides" would be false. `postedSince` skips them, so a proposal reaches the agent only as what the owner sends. What the owner removes from a draft stays out too. Decided 2026-10-30.

## Open points the text leaves

1. Whether a subscriber running another person's request, after reading and choosing to, is "making the Account available" (Consumer Terms) or letting "others use their account" (seat articles). The text has no notion of approval or of proposals.
2. Whether a teammate *steering* the owner's work makes the steered turn the teammate's work in the sense of "route their work through another user's account". The inference here is no, but no text draws the line.
3. Whether a product feature that lowers the friction of acting on others' requests is itself a "service that routes requests through consumer subscriptions", or whether only the resulting use counts.
4. How the GitHub Action on a `CLAUDE_CODE_OAUTH_TOKEN` fits with "only that person may use it": colleagues' `@claude` mentions run on one person's subscription, documented by Anthropic, with no approval step.
