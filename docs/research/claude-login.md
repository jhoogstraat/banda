# Keeping a user's Claude login across their thread pods

Researched 2026-10-19 against Claude Code **v2.1.302** (latest release is v2.1.303, 2026-10-18).
Unmarked sources are primary: code.claude.com docs, Anthropic legal and support pages, the `anthropics/claude-code` repo, issues and changelog, plus the CLI's own `--help`. Two other kinds of source are marked:
- **[secondary]**: blogs, news and community comments.
- **[binary]**: strings read from the shipped v2.1.302 binary. They are undocumented and may change.

## TL;DR

- On Linux the login lives in `$CLAUDE_CONFIG_DIR/.credentials.json`, mode 0600. Account metadata lives in `.claude.json`. Set `CLAUDE_CONFIG_DIR` so that both land in one directory.
- Access tokens last about 8 h. Refresh tokens **rotate**, and reusing an old one revokes the whole login.
- Processes that share one config directory coordinate the refresh through a lock file. This is documented for "one machine" only. Open bugs show everyone getting logged out when a process dies in the middle of a refresh.
- Anthropic's own devcontainer persists `~/.claude` in a named volume with `CLAUDE_CONFIG_DIR` pointing at it. That is the closest official precedent for Banda.
- Policy allows end users to sign in to the unmodified binary on a hosted platform. It forbids the platform's code from collecting, storing or intermediating Claude.ai tokens.
- **Pick:** one persistent volume per user, mounted as `CLAUDE_CONFIG_DIR` into that user's pods only. Only the `claude` binary reads or writes it. Login is `claude auth login` in the pod terminal.

---

## 1. Credential storage, token lifetime, concurrency

**Where credentials live**, per [Authentication › Credential management](https://code.claude.com/docs/en/authentication#credential-management):
- > "On Linux, credentials are stored in `~/.claude/.credentials.json` with file mode `0600`."
- > "If you've set the `CLAUDE_CONFIG_DIR` environment variable, Claude Code keeps the `.credentials.json` file under that directory instead"
- `CLAUDE_CONFIG_DIR` is documented as: "Override the configuration directory (default: `~/.claude`). All settings, session history, and plugins are stored under this path." ([env-vars](https://code.claude.com/docs/en/env-vars)). It cannot be set from project or local settings.
- `~/.claude.json` holds "OAuth session, per-project trust decisions, your personal MCP servers, and UI toggles" ([.claude directory](https://code.claude.com/docs/en/claude-directory)). The devcontainer page says that mounting only `~/.claude` "doesn't keep you signed in". You need `CLAUDE_CONFIG_DIR` so that `.claude.json` is written inside the volume ([devcontainer](https://code.claude.com/docs/en/devcontainer#persist-authentication-and-settings-across-rebuilds)).
- [binary] Writes are temp-file plus atomic `rename()`. Issue [#76561](https://github.com/anthropics/claude-code/issues/76561) shows the consequence: a **symlinked** `.credentials.json` is replaced by a private file on the first refresh. A `subPath` mount of the single file would break the same way. **Share the directory, never the file.**

**Lifetime and rotation:**
- **Access token: about 8 hours.** The changelog for v2.1.86 (2026-03-27) mentions "after the OAuth token refreshes (8 hours after login)". This is not stated on the auth page.
- **Refresh tokens rotate, and reuse is fatal.** Three primary sources show this:
  - [Troubleshooting](https://code.claude.com/docs/en/troubleshoot-install#not-logged-in-or-token-expired): "Before v2.1.211, waking the machine from sleep could cause two sessions to renew with the same token, which revoked the saved login and prompted every open session to log in again at once."
  - [Errors](https://code.claude.com/docs/en/errors): "your OAuth token expired or was rotated by another session".
  - The `Login expired` entry: "the OAuth service rejected the stored refresh token, so Claude Code cleared the saved credentials".
- **The login itself expires.** At startup the CLI warns "`Your login expires in 3 days · run /login to renew`" ([auth docs](https://code.claude.com/docs/en/authentication#renew-an-expiring-login)). The server returns `refresh_token_expires_in` [binary]. The actual duration is not documented, so expect a periodic re-login even in the best case.

**Concurrent processes:**
- Documented behaviour: "Parallel sessions on one machine share a saved login and coordinate its renewal so that only one process refreshes the token at a time" ([troubleshooting](https://code.claude.com/docs/en/troubleshoot-install#not-logged-in-or-token-expired)).
- After a login in one window, other sessions on the same config directory pick it up: "an interactive session showing this message starts using that login on its own" ([errors › Not logged in](https://code.claude.com/docs/en/errors#not-logged-in)).
- [binary] The lock is `$CLAUDE_CONFIG_DIR/.oauth_refresh.lock` (a lock directory) plus `.oauth_refresh.lock.owner`.
  - The owner record stores `pid`, `pidDomain` and `pidSpace`. A holder in a different PID namespace is reported as `foreign_pid_space` and is not taken over by PID. Only a lock that has gone stale by mtime is taken over.
  - Processes re-read `.credentials.json` when its mtime changes.
  - So the design tolerates sharers in other containers. The docs only promise "one machine".
- Race fixes in the changelog:
  - 2.1.129: "OAuth refresh race after wake-from-sleep that could log out all running sessions"
  - 2.1.133: "parallel sessions all dead-ending at 401 after a refresh-token race wiped shared credentials"
  - 2.1.136: "a concurrent credential write could overwrite a freshly-rotated OAuth token"
  - 2.1.248: lock-holder handling
  - **2.1.301 (2026-10-15)**: "unexpected sign-outs after a short command such as `claude auth status` … exited in the middle of a token refresh"
- Still-open issues that matter for Banda:
  - [#102985](https://github.com/anthropics/claude-code/issues/102985) (2026-10-17): a headless Linux box with about 10 stream-json sessions plus `claude -p` jobs sharing one `.credentials.json` loses its login every 1–2 days. The reporter's reading: a process **killed between the refresh POST and the write** loses the rotated token. The next lock holder then gets `invalid_grant` and clears the file for everyone. They stopped the logouts by no longer killing processes that touch the shared credentials.
  - [#76561](https://github.com/anthropics/claude-code/issues/76561): symlink sharing plus rotation cascades to logouts.
  - [#98693](https://github.com/anthropics/claude-code/issues/98693): concurrent `claude --print` calls invalidate the desktop app's session.

**Is a shared RWX volume across pods safe?** Workable, with conditions. It is not documented as supported beyond one machine.
- The lock and re-read logic live in the shared directory, so pods on the same volume do coordinate.
- The residual risk is a pod killed mid-refresh. Kubernetes does that on every delete.
- The failure mode is mild: every pod for that user is logged out at once, and one `claude auth login` in any pod fixes all of them.
- Required conditions:
  - Share the whole directory. Do not copy, symlink or `subPath` the file. A per-pod copy forks the rotation chain and guarantees logouts.
  - Pin Claude Code to ≥ 2.1.301.
  - Shut down gracefully: forward SIGTERM to `claude` and allow a grace period of 60 s or more.
  - Never run `claude` subcommands under hard kill timeouts.
  - Use a filesystem with atomic `rename`/`mkdir`. Local disk and NFS both qualify.

## 2. Headless login in a container (no browser)

- **Interactive `claude`:** "press `c` to copy the login URL". If the browser cannot reach the local callback, "paste it into the terminal at the `Paste code here if prompted` prompt. This happens when the browser can't reach Claude Code's local callback server, which is common in WSL2, SSH sessions, and containers" ([auth](https://code.claude.com/docs/en/authentication#log-in-to-claude-code)).
- **`claude auth login`** is the scriptable form. It "reads the pasted code from standard input" ([troubleshooting](https://code.claude.com/docs/en/troubleshoot-install#oauth-login-fails-in-wsl2-ssh-or-containers)). It was added in 2.1.41, and paste-code support for containers came in 2.1.126.
  - Flags per `claude auth login --help`: `--claudeai` (default), `--console`, `--email <email>`, `--sso`.
  - The flow uses PKCE (`code_challenge_method`/`code_verifier` [binary]). A pasted code is useless without the verifier held by that `claude` process.
- **`claude auth status [--json|--text]`** "Exits with code 0 if logged in, 1 if not". Its JSON includes `authMethod` (`none`, `claude.ai`, `oauth_token`, `api_key`, …) and `configDirectory` ([CLI reference](https://code.claude.com/docs/en/cli-reference)). **`claude auth logout`** logs out.
- **No device-code flow for claude.ai.** Device authorization appears only for Claude apps gateway sign-in (changelog). In `-p` mode, "Built-in commands that only run in the terminal interface, such as `/login`, aren't available" ([headless](https://code.claude.com/docs/en/headless)). Login therefore has to happen in a separate terminal process on the same config directory.

## 3. `setup-token`, `CLAUDE_CODE_OAUTH_TOKEN`, `apiKeyHelper`, other hooks

**`claude setup-token`**, per [auth › Generate a long-lived token](https://code.claude.com/docs/en/authentication#generate-a-long-lived-token):
- What it is: "For CI pipelines, scripts, or other environments where interactive browser login isn't available, generate a one-year OAuth token". It "does not save the token anywhere; copy it and set it as the `CLAUDE_CODE_OAUTH_TOKEN` environment variable".
- Limits: it "requires a Pro, Max, Team, or Enterprise plan. It can only make model requests, so it can't establish Remote Control sessions or fetch claude.ai connectors."
- Bare mode: "`--bare` does not read `CLAUDE_CODE_OAUTH_TOKEN`".
- Precedence: rank 5, above `/login` credentials. A token left in the environment therefore overrides a pod's login. `-p` works with it, since it is the GitHub Actions path.
- There is no refresh, so there are no rotation races. [#76561](https://github.com/anthropics/claude-code/issues/76561) migrated to it for that reason.
- Open bugs: [#79602](https://github.com/anthropics/claude-code/issues/79602) (session shows as "Claude API"), [#81350](https://github.com/anthropics/claude-code/issues/81350) (model gating), [#84903](https://github.com/anthropics/claude-code/issues/84903) (401), [#101890](https://github.com/anthropics/claude-code/issues/101890) (scrubbed from subprocesses).

**Intended use and where Anthropic tells you to put the token:**
- GitHub Actions: add it as a repository secret named `CLAUDE_CODE_OAUTH_TOKEN`. `/install-github-app` even writes that secret for you. The docs add that an OAuth token "is tied to the subscription of the person who ran `claude setup-token`" ([GitHub Actions](https://code.claude.com/docs/en/github-actions)).
- Codespaces: "store `ANTHROPIC_API_KEY` or a `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token` as a Codespaces secret" ([devcontainer](https://code.claude.com/docs/en/devcontainer#persist-authentication-and-settings-across-rebuilds)).
- So Anthropic itself sends users to put the token into a third-party platform's **user-managed secret store**.

**Can Banda receive or store a user's setup-token?** If Banda's UI or API accepts the token and saves it in Postgres or a Secret it creates, that is Banda's code collecting and storing a Claude.ai credential. That is what the policy forbids (§5). The Codespaces case is a general-purpose, user-managed secret store, not a Claude-specific feature. In Banda, the closest analogue is the user creating their own Kubernetes Secret with kubectl. That only works for people with cluster access.

**Other hooks:**
- **`apiKeyHelper`**: a shell command whose output "sends … as both the `X-Api-Key` and `Authorization: Bearer` headers". It is cached for 5 min and re-run on 401/403 ([settings reference](https://code.claude.com/docs/en/settings-reference#apikeyhelper)). It is meant for API keys and gateway tokens. A setup-token returned by it gets a 401 ([#97350](https://github.com/anthropics/claude-code/issues/97350)), so it is **not** a subscription path.
- **`CLAUDE_CODE_OAUTH_REFRESH_TOKEN` + `CLAUDE_CODE_OAUTH_SCOPES`**: "`claude auth login` exchanges this token directly instead of opening a browser … Useful for provisioning authentication in automated environments" ([env-vars](https://code.claude.com/docs/en/env-vars)). Banda would have to hold a refresh token, which is forbidden. Sharing one across pods would also fork the rotation chain. Avoid.
- `CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR` exists [binary] but is undocumented.

## 4. Anthropic's devcontainer precedent

The [docs](https://code.claude.com/docs/en/devcontainer#persist-authentication-and-settings-across-rebuilds) say:

> "By default, the container's home directory is discarded on rebuild, so engineers must sign in again each time. Claude Code stores its authentication token, user settings, and session history under the `~/.claude` directory. … Mount a named volume at `~/.claude` and set `CLAUDE_CONFIG_DIR` to the same path so Claude Code writes `.claude.json` inside the volume."

The reference config, [`.devcontainer/devcontainer.json`](https://github.com/anthropics/claude-code/blob/main/.devcontainer/devcontainer.json) on `main`, contains:

```json
"remoteUser": "node",
"mounts": [
  "source=claude-code-bashhistory-${devcontainerId},target=/commandhistory,type=volume",
  "source=claude-code-config-${devcontainerId},target=/home/node/.claude,type=volume"
],
"containerEnv": {
  "CLAUDE_CONFIG_DIR": "/home/node/.claude",
  ...
}
```

The docs also warn: with `--dangerously-skip-permissions`, "dev containers do not prevent a malicious project from exfiltrating anything accessible inside the container, including the Claude Code credentials stored in `~/.claude`."

## 5. Policy

**(a) Hosting, with each end user on their own subscription.** From [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance):
- > "preinstalling or running Claude Code in your products or services (e.g. in hosted sandboxes or other agent infrastructure) requires agreeing to our Commercial Terms of Service and complying with the conditions below"
- > "**The Claude Code binary must not be modified.** … customers may not remove, disable, or restrict any authentication method built into it"
- > "**Customers may not pay for, resell, or intermediate Claude usage on their end users' behalf.** Each end user must authenticate with their own Anthropic API key, Claude subscription plan credentials, or 3P inference provider credential"
- > "Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code"
- The [support article](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan) (update of 2026-10-07): "You can still use the Claude Agent SDK, `claude -p`, and third-party apps with your subscription limits."

**(b) Storing credentials:**
- > "Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow."
- The explicit carve-out covers only "their own API keys or third-party inference provider credentials — for example, configuring an API key in a development environment, secrets manager, or machine image". **Subscription OAuth tokens are not in it.**
- > "Anthropic reserves the right to take measures to enforce these restrictions and may do so without prior notice."
- What this means for Banda:
  - The binary writing its own `.credentials.json` to a disk is how Claude Code works everywhere.
  - A volume that outlives the pod is the grey zone. Anthropic's own devcontainer docs do exactly that.
  - Banda's code reading, copying, backing up, or accepting tokens through its UI is clearly out.

**(c) "Ordinary, individual usage" and teammates:**
- [Legal](https://code.claude.com/docs/en/legal-and-compliance#acceptable-use): "Advertised usage limits for Pro and Max plans assume ordinary, individual usage of Claude Code and the Agent SDK."
- OAuth "is intended exclusively for purchasers of Claude Free, Pro, Max, Team, and Enterprise subscription plans".
- [Support](https://support.claude.com/en/articles/13189465-log-in-to-your-claude-account): "Subscription plans can only be used by subscribers."
- [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) §2 (effective 2025-10-08): "You may not share your Account login information, Anthropic API key, or Account credentials with anyone else or make your Account available to anyone else".
- Consequence: a teammate whose message starts a turn on the owner's subscription is someone else using that subscription. Credentials are not shared, but it goes against "only by subscribers" and "individual usage". **Only the owner should be able to start agent turns.** A teammate's `@agent` should wait for the owner's one-click "run as me". This resolves PLAN.md open question 2.

**2026 changes (primary, dated via Wayback snapshots of the legal page):**
- **2026-02-18:** the "Authentication and credential use" section appears. It is absent on 2026-02-01. It added the "no Claude.ai login in third-party apps / no routing through Free, Pro, Max credentials" rule and "without prior notice" enforcement.
- **Between 2026-08-16 and 2026-08-30:** three additions, all absent in the 2026-08-16 snapshot:
  - the "Can customers offer Claude Code in their products?" section (hosted sandboxes, unmodified binary, own credentials)
  - the sentence "may not collect, store, or intermediate Claude.ai credentials or session tokens"
  - the hosted-platform carve-out
- **Support page:** "Use of third-party tools that misrepresent their identity to Anthropic's servers, attempt to route third-party traffic against subscription limits … may be enforced against". Third-party tools may be charged to usage credits.
- **Agent SDK article:** 2026-06-15 "We've paused the previously-announced changes to Claude Agent SDK usage", and the 2026-10-07 monthly API credits note.
- [secondary] News reports a 2026-01-09 technical block of subscription OAuth in third-party harnesses. They also report a 2026-04-04 move of third-party harness usage to extra usage. Sources: [WinBuzzer](https://winbuzzer.com/2026/02/19/anthropic-bans-claude-subscription-oauth-in-third-party-apps-xcxwbn/), [MindStudio](https://www.mindstudio.ai/blog/anthropic-openclaw-ban-oauth-authentication). Neither affects the unmodified `claude` binary.

---

## Recommendation

Ranked best first:

| # | Option | UX | Policy risk | Technical risk | kind fit |
|---|---|---|---|---|---|
| **A** | **Per-user volume as `CLAUDE_CONFIG_DIR`, binary-owned, mounted only into that user's pods** | Log in once; it lasts until the login expires or a race clears it | **Low–medium.** Same pattern as Anthropic's devcontainer. Banda's code never touches the token. Persistence beyond the pod is the open point. | Shared-file refresh races (#102985). Mitigated by graceful shutdown and v ≥ 2.1.301. The failure is one re-login for all pods. | Good. kind's default local-path class is RWO, and RWO "still can allow multiple pods to access … that volume when the pods are running on the same node" ([k8s](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes)). |
| B | Log in per pod (current M1 plan) | About 30 s of URL plus pasted code on every thread | **None** beyond the baseline | None. Each pod has its own refresh chain. | Trivial |
| C | One long-lived pod per user that runs all of that user's threads (worktrees) | Log in once | Low | This is the documented "parallel sessions on one machine" case, but per-thread isolation is lost | Conflicts with one Sandbox per thread |
| D | User-managed `setup-token` | Generate once a year | **High** if Banda accepts or stores it. Acceptable only if users create their own k8s Secret. | No refresh races. Inference only. Several open bugs. | Easy technically |
| — | Reject: refresh-token env provisioning, copying or symlinking `.credentials.json`, `apiKeyHelper` with an OAuth token, Agent SDK | | Collects tokens or isn't allowed | Forks the rotation chain or gets 401 | |

Two other paths sit outside this ranking:
- A user-supplied Console API key (via `apiKeyHelper` or an env Secret) is explicitly allowed. It is not a subscription, though.
- Anthropic's [self-hosted environments](https://code.claude.com/docs/en/self-hosted-environments) need no credentials from the host. But they are a Team/Enterprise beta and are driven from claude.ai, not by Banda.

**Pick A, and keep B as the fallback.** It is the only option that gives one login per user without Banda's code ever seeing a token. Anthropic documents exactly this pattern for containers. Its worst failure is "log in again once", and that applies to all pods together. Decided 2026-10-24: no question to Anthropic; Banda complies with the terms as written ([claude-terms.md](claude-terms.md), Decision). If they come to rule the volume out, switch it to the pod's lifetime and you are back at B with no other code change.

**Implementation for A:**

1. **Image.** Pin Claude Code ≥ 2.1.301 and set `DISABLE_AUTOUPDATER=1`. Run as a non-root user, uid 1000, `HOME=/home/agent`. `--dangerously-skip-permissions` refuses to run as root.
2. **Volume.** banda-api creates an empty PVC `claude-<userID>` on the user's first thread.
   - On kind: the default `standard` StorageClass with RWO, since all pods share one node.
   - Multi-node later: an RWX class (NFS, CephFS), or pin a user's pods to one node.
   - Never mount it into banda-api. Never read it, copy it or back it up.
3. **Sandbox `podTemplate`.** agent-sandbox takes a full PodSpec, so reference the existing claim directly (don't use `volumeClaimTemplates`):
   ```yaml
   securityContext: { runAsUser: 1000, fsGroup: 1000 }
   terminationGracePeriodSeconds: 90
   volumes: [{ name: claude, persistentVolumeClaim: { claimName: claude-<userID> } }]
   containers:
   - name: runner
     env:
     - { name: CLAUDE_CONFIG_DIR, value: /home/agent/.claude }
     - { name: CLAUDE_CODE_PROJECT_DIR_NAME, value: <threadID> }   # per-thread transcripts in the shared dir (v2.1.234+)
     volumeMounts: [{ name: claude, mountPath: /home/agent/.claude }]   # whole dir, no subPath
   ```
   Do **not** set `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` in the pod. They outrank the login.
4. **Preflight.** The runner runs `claude auth status --json` without a short kill timeout.
   - Exit 1 means the thread goes to `awaiting_input` with "Claude login needed".
   - The user then logs in with `claude auth login` in the terminal: `kubectl exec -it <pod> -- claude auth login` in M1, the terminal pane in M2.
   - Paste the code from the browser. Every other pod of that user reads the new `.credentials.json` on its own.
   - If a `-p` process does not, restart it with `--resume <session-id>`.
5. **Graceful stop.** The runner traps SIGTERM, stops `claude` cleanly, and waits for it to exit. Never `kill -9` `claude` and never run `claude …` under short timeouts. This is the main cause of shared-file logouts (#102985).
6. **Detect expiry.** Map stream-json `error: authentication_failed` and `Login expired` to `errored`, with a "Log in again" action.
7. **Hygiene.**
   - Don't record PTY input in the terminal pane: the pasted OAuth code passes through it.
   - "Forget my Claude login" runs `claude auth logout` in a pod, then deletes the PVC.
   - Mount a user's PVC only into pods that user owns.
   - Agent turns are started only by the owner (§5c).
   - Add egress limits later: the devcontainer warning about exfiltration under skip-permissions applies here too.
