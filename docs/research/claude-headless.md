# Driving `claude` headless over stream-json

Research for the Banda runner, checked 2026-10-19 against Claude Code **2.1.302** (installed locally). The latest release is 2.1.303 ([CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md)).

How each claim is sourced:

- **[doc]**: documented on code.claude.com. Pages: [headless](https://code.claude.com/docs/en/headless), [cli-reference](https://code.claude.com/docs/en/cli-reference), [permission-modes](https://code.claude.com/docs/en/permission-modes), [sessions](https://code.claude.com/docs/en/sessions), [model-config](https://code.claude.com/docs/en/model-config), [env-vars](https://code.claude.com/docs/en/env-vars), [authentication](https://code.claude.com/docs/en/authentication).
- **[sdk]**: inferred from the Agent SDK sources. These are [claude-agent-sdk-python](https://github.com/anthropics/claude-agent-sdk-python) (`_internal/transport/subprocess_cli.py`, `_internal/query.py`, at commit 498e063) and the `sdk.d.ts` types in `@anthropic-ai/claude-agent-sdk@0.3.303` on npm. The TS GitHub repo ships no source.
- **[obs]**: observed by running the real `claude` 2.1.302 binary against a local fake Messages API (`ANTHROPIC_BASE_URL=http://127.0.0.1:…`, a throwaway API key, an isolated `CLAUDE_CONFIG_DIR`). No login and no Anthropic network were involved. The JSON lines below are verbatim from those runs. Long arrays are shortened only where marked.
- **[bin]**: read from strings inside the `claude` binary. This is the least stable source.
- **[contract]**: checked by `mise run contract` (`cmd/runner/contract_test.go`) against the `claude` pinned in the sandbox image (2.1.301 on 2026-10-24), the same method as [obs], run on every pull request.

---

## 1. Command line

```bash
cd /workspace/repo   # cmd.Dir in Go: the checkout. Keep this path stable across restarts (see §5)
claude -p \
  --input-format stream-json \
  --output-format stream-json \
  --verbose \
  --include-partial-messages \
  --model "$MODEL" \
  --effort "$EFFORT" \
  --dangerously-skip-permissions \
  --session-id "$THREAD_UUID"      # first start; on any restart use: --resume "$THREAD_UUID"
  # optional: --append-system-prompt "…"  --mcp-config '<json>'  --add-dir <dir>
```

Recommended env: `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` and `DISABLE_AUTOUPDATER=1`. Run as a **non-root** user, or set `IS_SANDBOX=1` (see the bypass row).

| Flag | Notes |
|---|---|
| `-p` / `--print` | Required. `--input-format` and `--output-format` only work with `-p`. In `-p` mode the workspace trust dialog is skipped, so the cloned repo's `.claude/settings.json` hooks and `.mcp.json` servers **do run** [doc]. To ignore them, add `--setting-sources user` and/or `--strict-mcp-config`. |
| `--input-format stream-json` | NDJSON user messages and control messages on stdin [doc]. |
| `--output-format stream-json` | NDJSON events on stdout [doc]. |
| `--verbose` | **Required** with stream-json output. Without it the CLI prints `Error: When using --print, --output-format=stream-json requires --verbose` and exits 1 [obs]. The SDK always passes it [sdk]. |
| `--include-partial-messages` | Adds `stream_event` lines (raw Messages API stream events) [doc][obs]. |
| `--model` | Accepts an alias (`default`, `best`, `fable`, `opus`, `sonnet`, `haiku`, `opusplan`, `sonnet[1m]`, `opus[1m]`) or a full ID such as `claude-opus-5-5` [doc]. On `--resume` without `--model`, the session keeps the model in its transcript, including one set mid-session with `set_model` [obs]. |
| `--effort` | `low`, `medium`, `high`, `xhigh`, `max`, or `ultracode` (= `xhigh` with ultracode on) [doc]. Opus/Sonnet/Haiku 5.5 support all five levels and default to `medium`. Other effort models default to `high` [doc]. An unsupported level falls back to the next lower one [doc]. `--effort high` puts `"output_config":{"effort":"high"}` on the API request [obs]. **The `CLAUDE_CODE_EFFORT_LEVEL` env var overrides `--effort`**, so keep it out of the pod env [doc]. |
| Thinking | 5.5-generation and Fable models always use adaptive thinking, which can't be turned off; effort is the control [doc]. In `-p` the request carries `"thinking":{"type":"adaptive","display":"updates"}` [obs]. The hidden flag `--thinking-display summarized` (the SDK passes it) changes that to `"display":"summarized"` [sdk][obs]. Use it to get readable `thinking` blocks. |
| `--permission-mode` | `default` (also accepted as `manual`), `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions` [doc]. Without it, a `-p` run starts in `default`, or in `auto` on sessions without feature-flag fetching [doc]. |
| `--dangerously-skip-permissions` | Same as `--permission-mode bypassPermissions` [doc]. On Linux and macOS it refuses to start as root: `--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons`. The check is skipped when `IS_SANDBOX=1` or `CLAUDE_CODE_BUBBLEWRAP` is set [doc][bin]. **Pass it again on every `-p --resume`**, because the mode is not restored [doc]. |
| `--permission-prompt-tool` | `stdio` sends permission prompts to the host as `can_use_tool` control requests. This is what the SDK uses when `canUseTool` is set [sdk][obs]. Alternatively, pass `mcp__<server>__<tool>` for an MCP tool [doc][obs]. Details in §4. |
| `--permission-prompts none` | Auto-denies anything that would prompt and removes `AskUserQuestion` (v2.1.259+) [doc]. |
| `--session-id <uuid>` | Fixes the session ID up front, so Banda can use the thread UUID. If the ID already exists: `Error: Session ID … is already in use.`, exit 1 [obs]. |
| `--resume <uuid>` | Continues a stored session (§5). Avoid `--continue`, which means "most recent in cwd". `--fork-session` copies the session under a new ID [doc]. |
| `--append-system-prompt` | Appends to the default prompt. The system prompt is recorded on the first request and reused on resume until compaction. New text on resume is ignored unless you pass `--system-prompt-snapshot off` [doc]. |
| `--mcp-config <json\|file>` | Before the first turn, the CLI waits for servers up to `MCP_TIMEOUT` (default 30 s). Status shows in `system/init.mcp_servers` [doc]. **Not restored on resume**, so pass it again [doc]. |
| `--add-dir` | Grants file access only. `.claude/` config is not loaded from these dirs [doc]. Not restored on resume. |
| `--bare` | **Do not use.** It never reads OAuth or the keychain (`ANTHROPIC_API_KEY`/`apiKeyHelper` only), so subscription login breaks [doc]. |
| `--no-session-persistence` | Do not use. It disables resume [doc]. |
| `--replay-user-messages` | Echoes stdin user messages back with `"isReplay":true` [doc][obs]. Optional; the `uuid` + `command_lifecycle` ack below arrives earlier. |
| `--max-turns`, `--max-budget-usd` | Optional per-turn and spend caps [doc]. |

---

## 2. Input (stdin)

Write one JSON object per line, terminated with `\n`, from a single writer goroutine. Keep stdin open for the life of the thread.

### User message

Minimal form, which works [obs]:

```json
{"type":"user","message":{"role":"user","content":"Fix the failing test"}}
```

The SDK form [sdk] adds an optional client `uuid`. With the `uuid`, the reply frames and the `result` carry `user_message_uuid`/`user_message_uuids`, and `command_lifecycle` acks appear [obs]:

```json
{"type":"user","uuid":"aaaaaaaa-0000-4000-8000-000000000001","message":{"role":"user","content":[{"type":"text","text":"Fix the failing test"}]},"parent_tool_use_id":null,"session_id":""}
```

`content` is a Messages API `MessageParam` content: a string, or blocks such as text, base64 image, or document [sdk].

**Sending several messages over time:** write each one whenever you like. When the CLI is idle, the message starts a turn. When a turn is running, the message is **queued**. It either runs as the next turn after the current `result`, or it is folded into the running turn at a tool boundary. Every turn ends with exactly one `result` [obs][sdk]. Observed queue acks for a message sent mid-turn (undocumented frame type, don't depend on it) [obs]:

```json
{"type":"command_lifecycle","command_uuid":"aaaaaaaa-0000-4000-8000-000000000002","state":"queued","uuid":"fdc17398-b9f3-4e39-8fe5-8100db8fd05d","session_id":"ca4221a9-1987-4b6e-a778-0094f37cc083"}
```

The state then moves through `started` and `completed`.

A message written while a tool runs joins the turn at the next tool boundary: it rides along with the tool result in the next request, and one `result` ends both. A message written while the model answers without a tool waits for a turn of its own. Either way the first `result` says `"queued_turn_count":0`, so that field doesn't count waiting prompts. With a `uuid` on each message, the `result` lists the ones it ended in `user_message_uuids` (folded: both; queued: one per result), also for an interrupted turn [contract].

**What Banda does:** it writes prompts as they come, each with a random `uuid`, and counts turns by `user_message_uuids`: a result ends as many prompts as it lists [contract]. `deploy/fake-claude` folds (`e2e-fold`) and reports the same way.

Lines with an unknown `type` are ignored [obs]. **A malformed JSON line kills the process**: `Error parsing streaming input line (type=unknown, 8 chars): SyntaxError`, exit 1 [obs].

### Control requests (runner → CLI)

Envelope [sdk][obs]:

```json
{"type":"control_request","request_id":"<unique per in-flight request>","request":{"subtype":"…", "…":"…"}}
```

Reply on stdout:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"…","response":{…}}}
```

On failure the reply has `"subtype":"error","error":"…"` instead [sdk].

| Subtype | Request body | Observed reply / notes |
|---|---|---|
| `interrupt` | `{"subtype":"interrupt"}`. Optional `"cancel_queued":true` also drops queued messages (capability `interrupt_cancel_queued_v1`) [sdk] | `{"type":"control_response","response":{"subtype":"success","request_id":"req_1","response":{"still_queued":[]}}}` [obs] |
| `set_model` | `{"subtype":"set_model","model":"claude-sonnet-5-5"}`. `null` or `"default"` resets [sdk] | `…"response":{"subtype":"success","request_id":"m_1"}}`. Makes a 1-token validation request [obs]. Applies from the next turn. |
| `apply_flag_settings` | `{"subtype":"apply_flag_settings","settings":{"effortLevel":"low"}}` [sdk] | success. The next request used `"output_config":{"effort":"low"}` [obs]. Alternative: send the user message `/effort low` [doc]. |
| `set_permission_mode` | `{"subtype":"set_permission_mode","mode":"acceptEdits"}` | `…"response":{"mode":"acceptEdits"}` [obs]. You can't switch into `bypassPermissions` unless the CLI was launched with it or with `--allow-dangerously-skip-permissions` [doc]. |
| `initialize` | `{"subtype":"initialize","hooks":null}` | **Optional.** Turns work without it [obs]. The reply holds `commands`, `agents`, `models`, `account` (e.g. `{"tokenSource":"none","apiKeySource":"ANTHROPIC_API_KEY","apiProvider":"firstParty"}`), `pid`, `claude_code_version`, `current_permission_mode`, `session_state`, `capabilities` [obs]. It is mainly needed for SDK-only features (below), but it is handy as a cheap "is anyone logged in" probe through `account` [obs; meaning of fields inferred]. |
| `control_cancel_request` | `{"type":"control_cancel_request","request_id":"…"}` (a top-level type, not a subtype). Either side may send it to withdraw its own pending request [sdk] | Expect the CLI to send this for a pending `can_use_tool` it no longer needs. Stop showing that prompt. |

SDK-only subtypes that Banda doesn't need: `hook_callback` and `mcp_message` (CLI→host, for in-process hooks and in-process MCP servers registered through `initialize`), `set_max_thinking_tokens`, `mcp_status`, `mcp_set_servers`, `get_context_usage`, `rewind_files`, `stop_task`, `cancel_async_message`, `update_settings`, and others [sdk].

### Interrupting a turn

Send `{"type":"control_request","request_id":"req_1","request":{"subtype":"interrupt"}}`. Observed sequence, with the process staying alive and accepting the next message [obs]:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"req_1","response":{"still_queued":[]}}}
{"type":"assistant","message":{"id":"msg_fake1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Hello "}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"context_management":null},"parent_tool_use_id":null,"session_id":"e9fca623-ff3b-49e7-bc24-ff6f95d72ad5","uuid":"511a4050-2317-40f2-a3bb-f65fceb55769","timestamp":"2026-10-18T23:22:22.075Z","request_id":"req_fake","aborted":true}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"parent_tool_use_id":null,"session_id":"e9fca623-ff3b-49e7-bc24-ff6f95d72ad5","uuid":"6df3297c-0b02-43e1-8be0-b044594925bf","timestamp":"2026-10-18T23:22:22.080Z"}
```

Then comes a `result` with `"subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"` (or `aborted_tools` when the interrupt lands during tool execution [sdk]). Map that to `Interrupt → awaiting_input`, not to `TurnFailed`.

Don't use signals to interrupt. SIGINT aborted the turn but **exited the process** with rc 0 and no `result` [obs]. SIGTERM exits 143 with no result for the open turn [doc][obs].

---

## 3. Output (stdout)

Handle these frame types and ignore any others (the union is open; see `SDKMessage` in `sdk.d.ts`).

### `system/init`: emitted at the **start of every turn**, not just once [sdk][obs]

Arrays shortened (`…` is not literal):

```json
{"type":"system","subtype":"init","cwd":"/private/tmp/banda-research/work","session_id":"11111111-2222-4333-8444-555555555555","tools":["Task","Bash","Edit","Read","Write","WebFetch","WebSearch","…"],"mcp_servers":[],"model":"claude-opus-5-5","permissionMode":"bypassPermissions","slash_commands":["compact","context","effort","model","…"],"apiKeySource":"ANTHROPIC_API_KEY","claude_code_version":"2.1.302","output_style":"default","agents":["claude","Explore","general-purpose","Plan","statusline-setup"],"skills":["…"],"plugins":[{"name":"cc-plugin-agents-md","path":"builtin","source":"cc-plugin-agents-md@builtin"}],"capabilities":["interrupt_receipt_v1","interrupt_cancel_queued_v1","interrupt_send_now_v1","msg_lifecycle_v1","request_marker_lists_v1","sdk_mcp_tools_list_changed","sdk_mcp_manifests","mcp_read_resource_v1","mcp_tool_ui_meta_v1","ui_surface_v1","mcp_call_result_not_for_model_v1"],"uuid":"5d1dfc07-6da4-465f-9e36-e4fd8b3b075f","fast_mode_state":"off","view_mode":"default"}
```

Use `capabilities` for feature detection instead of comparing versions [doc].

### `stream_event` (partial deltas, with `--include-partial-messages`) [obs]

```json
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}},"session_id":"11111111-2222-4333-8444-555555555555","parent_tool_use_id":null,"uuid":"5b2427b6-8773-48e5-8c4c-f19d7a36891e","api_message_id":"msg_fake1"}
```

`event` is a raw Messages API stream event: `message_start`, `content_block_start` (`text` / `thinking` / `tool_use`), `content_block_delta` (`text_delta` / `thinking_delta` / `signature_delta` / `input_json_delta`), `content_block_stop`, `message_delta` (`stop_reason`), `message_stop`. Example tool-input delta:

```json
{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\": \"echo hi\", "}},"session_id":"11111111-2222-4333-8444-555555555555","parent_tool_use_id":null,"uuid":"72a5beda-595c-48bc-adf5-8a7bcc74879e","api_message_id":"msg_fake1"}
```

### `assistant`: **one line per completed content block**

Each line holds a single block, and all lines from one API response share `message.id`. `stop_reason` is still `null` on these lines [sdk][obs]. Treat these as authoritative and the deltas as a preview.

```json
{"type":"assistant","message":{"id":"msg_fake1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Running it."}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"context_management":null},"parent_tool_use_id":null,"session_id":"11111111-2222-4333-8444-555555555555","uuid":"f3dd5f47-fbf8-42c4-9917-2fe0379890db","timestamp":"2026-10-18T23:21:49.205Z","request_id":"req_fake"}
{"type":"assistant","message":{"id":"msg_fake1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_fake1","name":"Bash","input":{"command":"echo hi","description":"Print hi"}}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"context_management":null},"parent_tool_use_id":null,"session_id":"11111111-2222-4333-8444-555555555555","uuid":"b1c822f9-7e3c-4a23-abef-1f64f67e359e","timestamp":"2026-10-18T23:21:49.205Z","request_id":"req_fake"}
{"type":"assistant","message":{"id":"msg_fake1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"Let me think.","signature":"sigfake"}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"context_management":null},"parent_tool_use_id":null,"session_id":"11111111-2222-4333-8444-555555555555","uuid":"6a913ef5-cce9-482b-978c-03c4ea524802","timestamp":"2026-10-18T23:21:49.204Z","request_id":"req_fake","thinking_duration_ms":1,"thinking_display":"updates"}
```

Messages from subagents carry a non-null `parent_tool_use_id` [doc].

### Plans and subagents

Observed with 2.1.301 [contract]: the subagent tool is **`Agent`** (`system/init.tools` still lists `Task`), its input `description`, `prompt`, `subagent_type` and optional `model`, `effort`, `run_in_background`, `isolation`. Its lines stream with `parent_tool_use_id` as below; its `tool_result` text starts with a "[Subagent hand-back]" frame and ends with `agentId: …` and a `<usage>` block. Subagents run **in the background by default**: the `tool_result` comes at once ("Async agent launched successfully …"), the subagent's lines follow after it and after the turn's `result`, then `system/task_notification` and a **new turn the CLI starts itself**, with no prompt from the host. `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` removes `run_in_background` from Agent and Bash, so they finish within the turn; the runner sets it. There is no `TodoWrite` in `-p` (nor `Glob`/`Grep`); `CLAUDE_CODE_ENABLE_TODO_TOOLS=1` adds `TaskCreate`, `TaskGet`, `TaskList` and `TaskUpdate` (one task per call) instead.

The shapes below are from `sdk.d.ts` [sdk]:

`TodoWrite` is an ordinary tool call whose input is the **whole** list each time; the latest call is the plan. `status` is `pending`, `in_progress` or `completed`, and `activeForm` is the present-tense label of the item being worked on:

```json
{"type":"assistant","message":{"id":"msg_2","content":[{"type":"tool_use","id":"toolu_todo1","name":"TodoWrite","input":{"todos":[{"content":"Read the failing test","status":"completed","activeForm":"Reading the failing test"},{"content":"Fix the parser","status":"in_progress","activeForm":"Fixing the parser"},{"content":"Run the tests","status":"pending","activeForm":"Running the tests"}]}}]},"parent_tool_use_id":null,"session_id":"s"}
```

A subagent is a `Task` tool call (input `description`, `prompt`, `subagent_type`). Until its `tool_result` arrives, the subagent's own lines stream with `parent_tool_use_id` set to the Task call's id: its prompt as a `user` text block, its text, deltas, tool calls and their results. The final `tool_result` holds the subagent's answer as text blocks:

```json
{"type":"assistant","message":{"id":"msg_3","content":[{"type":"tool_use","id":"toolu_task1","name":"Task","input":{"description":"Find the parser","prompt":"Where is the config parser?","subagent_type":"Explore"}}]},"parent_tool_use_id":null,"session_id":"s"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Where is the config parser?"}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}
{"type":"assistant","message":{"id":"msg_4","content":[{"type":"tool_use","id":"toolu_sub1","name":"Grep","input":{"pattern":"func parse","path":"internal"}}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_sub1","type":"tool_result","content":"Found 1 file\ninternal/config/parse.go"}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}
{"type":"assistant","message":{"id":"msg_5","content":[{"type":"text","text":"It is in internal/config/parse.go."}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_task1","type":"tool_result","content":[{"type":"text","text":"The parser is internal/config/parse.go."}]}]},"parent_tool_use_id":null,"session_id":"s","tool_use_result":{"status":"completed","totalToolUseCount":1}}
```

Banda keeps only the subagent's tool calls (tagged with `parent_tool_call_id`) and results, and nests them in the Task call; its text is repeated by the final result. Newer versions may name the tool `Agent`; the nesting doesn't depend on the name.

### `user`: tool results (`tool_use_result` holds the structured tool output) [obs]

```json
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_fake1","type":"tool_result","content":"hi","is_error":false}]},"parent_tool_use_id":null,"session_id":"11111111-2222-4333-8444-555555555555","uuid":"25dabb5f-c33b-4e05-b002-a3ea242f02c8","timestamp":"2026-10-18T23:21:49.266Z","tool_use_result":{"stdout":"hi","stderr":"","interrupted":false,"isImage":false,"noOutputExpected":false},"tool_result_meta":[{"id":"toolu_fake1","permission_decision":{"decision":"accept","source":"config","reason_type":"mode"}}]}
```

### `result`: exactly one per turn [obs]

```json
{"duration_api_ms":11,"stop_reason":"end_turn","session_id":"11111111-2222-4333-8444-555555555555","total_cost_usd":0.000208,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":8,"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"","iterations":[],"speed":"standard","fallback_credit":null},"modelUsage":{"claude-opus-5-5":{"inputTokens":12,"outputTokens":8,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":0,"costUSD":0.000208,"contextWindow":1000000,"maxOutputTokens":128000,"thinkingTokens":0,"canonicalModel":"claude-opus-5-5","provider":"firstParty","costBasis":"list"}},"permission_denials":[],"terminal_reason":"completed","is_error":false,"num_turns":1,"subtype":"success","api_error_status":null,"result":"Hello from the fake API.","type":"result","duration_ms":45,"uuid":"c2b02033-6e0c-403b-99bb-4a1c24ff1a65","queued_turn_count":0,"result_index":0}
```

(Shortened: telemetry fields such as `subagent_stats`, `ttft_*` and `fast_mode_*` were dropped.)

- `subtype`: `success` | `error_during_execution` | `error_max_turns` | `error_max_budget_usd` | `error_max_structured_output_retries` [sdk].
- **Branch on `is_error` and `terminal_reason`, not on `subtype`.** API and auth errors arrive as `"subtype":"success","is_error":true,"terminal_reason":"api_error"` [obs].
- `usage` is per turn. `total_cost_usd` and `modelUsage` are cumulative for the process, and for the session across resumes, so read the latest value instead of summing [sdk]. On a subscription the cost is a client-side estimate, not a bill [doc].
- `terminal_reason` values: `completed`, `aborted_streaming`, `aborted_tools`, `api_error`, `max_turns`, `prompt_too_long`, `budget_exhausted`, … [sdk].

### Other frames worth handling

- `{"type":"system","subtype":"status","status":"requesting",…}`: a model request is in flight [obs].
- `system/api_retry` (`attempt`, `max_retries`, `retry_delay_ms`, `error_status`, `error` category) [doc][obs]. See §6 for auth.
- `system/session_state_changed`, emitted with `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` [doc]. It is the authoritative turn-over signal [sdk]: `{"type":"system","subtype":"session_state_changed","state":"idle","uuid":"7fd77dda-617f-425f-aa9c-19db38bc697d","session_id":"e9fca623-ff3b-49e7-bc24-ff6f95d72ad5"}`. States are `running`, `idle`, `requires_action`.
- `system/thinking_tokens` (estimate while thinking) [obs], `system/permission_denied` [sdk], `system/compact_boundary`, `system/task_*` (subagents), `system/hook_*`, `rate_limit_event` (seen on a subscription session) [obs]. Ignore unknown types.
- `control_request` **from** the CLI: `can_use_tool` (§4). With hooks or SDK MCP servers also `hook_callback` and `mcp_message`, which Banda doesn't use.

---

## 4. Permissions

### M1: the pod is the boundary

Use `--dangerously-skip-permissions` (or `--permission-mode bypassPermissions`).

- Run the CLI as a non-root user. Otherwise set `IS_SANDBOX=1`, or the CLI refuses to start [doc][bin].
- A few calls still prompt under bypass, for example `rm` on critical paths. In `-p` with no prompt host, these are **denied** and the turn continues [doc].
- In the tool result, approved calls show `"permission_decision":{"decision":"accept","source":"config","reason_type":"mode"}` [obs].

### M2 option A (recommended): control protocol over stdio

Launch with `--permission-mode default --permission-prompt-tool stdio`. Swap `default` for `acceptEdits` or `auto` to cut down on prompts. The CLI then writes this and **blocks the turn until answered** (no timeout seen in 30 s) [obs]:

```json
{"type":"control_request","request_id":"e819300a-b2d4-4b50-8f0c-eb958085a24c","request":{"subtype":"can_use_tool","tool_name":"Bash","display_name":"Bash","input":{"command":"touch banda.txt","description":"Print hi"},"description":"Print hi","permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"touch banda.txt"}],"behavior":"allow","destination":"localSettings"},{"type":"addDirectories","directories":["/private/tmp/banda-research/work"],"destination":"session"},{"type":"setMode","mode":"acceptEdits","destination":"session"}],"blocked_path":"/private/tmp/banda-research/work/banda.txt","tool_use_id":"toolu_fake1"}}
```

Allow. Always send `updatedInput`: before v2.1.207 an allow without it was rejected [doc]. To grant "always allow", add `"updatedPermissions":[<one of permission_suggestions>]` [sdk].

```json
{"type":"control_response","response":{"subtype":"success","request_id":"e819300a-b2d4-4b50-8f0c-eb958085a24c","response":{"behavior":"allow","updatedInput":{"command":"touch banda.txt","description":"Print hi"}}}}
```

Deny. Optionally add `"interrupt":true` to also end the turn [sdk].

```json
{"type":"control_response","response":{"subtype":"success","request_id":"8a1c2442-80c0-47a7-a28d-e3dd8b15b8ad","response":{"behavior":"deny","message":"User denied this in the Banda UI"}}}
```

The model then gets `{"type":"tool_result","content":"User denied this in the Banda UI","is_error":true,…}` [obs].

Notes:

- Map a pending `can_use_tool` to `PermissionRequested → awaiting_input`.
- If stdin closes while a prompt is pending, the call fails with `Tool permission request failed: AbortError: Tool permission stream closed before response received` [obs].
- The richer fields (`decision_reason`, `title`, `requires_user_interaction`, `agent_id` for subagents) are listed in `SDKControlPermissionRequest` [sdk].
- `AskUserQuestion` also arrives as `can_use_tool`. Answer it with allow plus `updatedInput:{questions, answers}` [doc: agent-sdk/user-input]. Details in "Questions" below.

### What Banda does (checked 2026-10-21)

Banda took option A. Sources for this part: the [Agent SDK permissions](https://code.claude.com/docs/en/agent-sdk/permissions), [user input](https://code.claude.com/docs/en/agent-sdk/user-input) and [permission modes](https://code.claude.com/docs/en/permission-modes) pages [doc], and `sdk.d.ts`/`sdk-tools.d.ts` of `@anthropic-ai/claude-agent-sdk@0.3.306` [sdk]. Since 2026-10-24 [contract] checks against the real binary: ask (allow runs the call, deny's message is the tool result), accept edits (a Write runs unasked, `python3 -c …` asks), plan with `ExitPlanMode` and the switch to accept edits on approval, `AskUserQuestion` answers, `EnterPlanMode` (unasked; a Write then asks), and an MCP tool asking in ask mode.

- **Launch**: `--permission-mode default --permission-prompt-tool stdio --allow-dangerously-skip-permissions`, then `set_permission_mode` to the thread's mode before the first prompt and whenever the owner changes it. The mode isn't restored on `--resume`, so the runner sets it on every start. `bypassPermissions` can only be entered later if the CLI was launched with it or with `--allow-dangerously-skip-permissions` [doc].
- **Banda's modes → `--permission-mode`**: bypass → `bypassPermissions`, ask → `default` (reads and read-only Bash run, edits and other commands ask), accept edits → `acceptEdits` (edits and `mkdir`/`touch`/`rm`/`mv`/`cp`/`sed` inside the checkout run, other commands ask), plan → `plan`. `auto` (a classifier) and `dontAsk` aren't offered [doc].
- **Evaluation order**: hooks → deny rules → ask rules → mode → allow rules → `can_use_tool`. Only what nothing earlier resolves reaches the host. Even in `bypassPermissions`, `rm`/`rmdir` on critical paths, ask rules, `AskUserQuestion` and MCP tools that require user interaction still arrive [doc].
- **`can_use_tool` fields** (`SDKControlPermissionRequest`): `tool_name`, `input`, `tool_use_id` (required), `permission_suggestions`, `blocked_path`, `decision_reason` and `decision_reason_type`, `title`, `display_name`, `description`, `agent_id` (subagents), and hints `suppress_always_allow_rule`, `default_to_no`, `requires_user_interaction` [sdk]. The runner forwards `tool_use_id` (to place the card next to its call), the tool, the input and `decision_reason` or `description` as the reason.
- **Answers**: allow is `{"behavior":"allow","updatedInput":<the input as received>}`, deny `{"behavior":"deny","message":"…"}` (see above). The callback may stay pending indefinitely; the CLI has no timeout [doc].
- **"Always allow" lives in Banda, not in the CLI.** Echoing a `permission_suggestions` entry in `updatedPermissions` would write a rule to the session, which a restart forgets, or to `.claude/settings.local.json` in the checkout, which ends up in the diff. Instead the thread stores the tool names and the runner answers their requests itself.
- **Plan mode.** In `-p` plan mode keeps its blocks: edits and file-modifying shell commands reach `can_use_tool` even if an allow rule matches [doc]. The plan arrives as a `can_use_tool` for the tool **`ExitPlanMode`**. Its schema is open (`ExitPlanModeInput` has only the deprecated `allowedPrompts`, plus `[k: string]: unknown`), and its output carries `plan` and `filePath` [sdk]. Observed [contract]: in plan mode the CLI tells the model to write its plan to `$CLAUDE_CONFIG_DIR/plans/<slug>.md` (the only file it may edit; the write doesn't ask), the model calls `ExitPlanMode` with `{}`, and the CLI adds `plan` (the file's text) and `planFilePath` to the input, in both the `assistant` line and the `can_use_tool` request. Allowing answers "User has approved your plan. …" with the plan. So the card shows `input.plan`; without a plan file it points at the plan in the chat. Approving answers allow and then sends `set_permission_mode` to the chosen working mode, the same switch the interactive dialog makes [doc]. "Keep planning" denies with the owner's note.
- **Withdrawn requests**: the CLI may send `{"type":"control_cancel_request","request_id":…}` for a pending prompt it no longer needs [sdk]; Banda marks the card withdrawn. Any request still pending when its turn's `result` arrives is marked withdrawn too.
- **Other host-bound control requests** (`hook_callback`, `mcp_message`, …) only come when registered through `initialize`, which Banda doesn't do. The runner still answers any of them with an error, so a surprise subtype can't leave the turn hanging.
- **Questions (`AskUserQuestion`)**, checked 2026-10-22 against the [user input](https://code.claude.com/docs/en/agent-sdk/user-input) page [doc], `AskUserQuestionInput`/`AskUserQuestionOutput` in `sdk-tools.d.ts` of `@anthropic-ai/claude-agent-sdk@0.3.301` [sdk] and the 2.1.306 binary's tool prompt [bin]; since 2026-10-24 run by [contract]: the model gets `Your questions have been answered: "Which database?"="Postgres", "Which extras?"="Cache, Search". …`. The input is `{"questions":[{"question","header" (≤12 chars),"options":[{"label","description","preview"?}] (2–4),"multiSelect"}]}` (1–4 questions); `preview` only appears with the SDK's `toolConfig.askUserQuestion.previewFormat`, which the CLI's `-p` doesn't set. The answer is allow with `updatedInput` = the input as received plus `"answers":{<question text>: <answer>}`: the chosen option's `label`, several joined by `", "` (an array of labels is accepted too), or the user's own text for "Other", which the CLI asks hosts to offer and never lists itself. Optional: `annotations:{<question>:{notes, preview}}`, and `response` (a free reply instead of answers: the model then gets "The user responded: …"). A deny's `message` reaches the model as usual. It is asked even in `bypassPermissions` (above), `--permission-prompts none` removes the tool, and subagents can't use it. Banda keeps the question's input in the runner and adds the owner's `answers` to it, so the keys are the CLI's own question texts.
- **Edited input**: allowing with a different `updatedInput` runs that instead; the model isn't told [doc]. Banda lets the owner change a Bash command or an edit's new text; the runner sends the owner's input as `updatedInput`. The owner edits the redacted copy the UI shows, so a secret in the original input would be replaced by `[redacted]`.
- **Per-call rules**: Claude Code's own rule syntax is `Bash(npm test:*)` (a command prefix) and `Edit(<path glob>)` for the edit tools [doc: permissions]; `permission_suggestions` carries the same shapes (`addRules` with `ruleContent`, see the example above). Banda keeps the syntax but matches the rules itself in the runner (`cmd/runner/permissions.go`), for the reasons above: `Bash(<prefix>:*)` matches the command or the prefix followed by arguments, never a command with shell operators (`;&|<>$()`, a backtick, a backslash or a newline); `Edit(<dir>/**)` matches Edit, Write, MultiEdit and NotebookEdit under that directory of the checkout.
- **Mode switches by the agent**: `EnterPlanMode` (input `{}`) switches the session to plan itself and answers "Entered plan mode. …"; its permission check allows it without asking except behind a flag that asks (in neither `auto` nor `plan`), and it is refused in subagents [bin]. Observed [contract]: in ask mode it switches without a `can_use_tool`, and a Write then asks. The SDK also documents `system/status` lines carrying `permissionMode` [sdk], not observed. Banda takes a successful `EnterPlanMode` result as the switch (the runner remembers `plan` for a restarted claude, and the API records a `PermissionModeChanged` by `agent`); approving the plan (`ExitPlanMode`) switches back as before.
- **Restarts**: the pending request lives in the claude process. If the pod or the runner restarts, the turn and its request are gone (stdin closes: `Tool permission stream closed before response received`, see above); Banda marks the card lost when the runner disconnects or the next one is ready.

### M2 option B (the plan's current wording): MCP prompt tool

Launch with `--mcp-config '{"mcpServers":{"banda":{"type":"stdio","command":"…"}}}' --permission-prompt-tool mcp__banda__approve` and verify the result with [obs]:

- The tool is called with arguments `{"tool_name":"Bash","input":{"command":"touch banda.txt","description":"Print hi"},"tool_use_id":"toolu_fake1"}`.
- It must return `{"content":[{"type":"text","text":"{\"behavior\":\"allow\",\"updatedInput\":{…}}"}]}`, or the JSON text `{"behavior":"deny","message":"…"}`.

This needs an extra MCP server process that talks back to the runner. Option A gives the same result over the pipe the runner already owns. Either way the binary stays unmodified. Option A speaks the same wire protocol the Agent SDK uses, without importing the SDK. Whether that matters for the "built on the Agent SDK" clause in PLAN.md is for the plan owner to judge.

---

## 5. Session persistence and resume

- **Location:** `$CLAUDE_CONFIG_DIR/projects/<cwd with every non-alphanumeric char replaced by ->/<session-id>.jsonl`. `CLAUDE_CONFIG_DIR` defaults to `~/.claude` [doc]. Observed: `/tmp/banda-research/cfg/projects/-private-tmp-banda-research-work/22222222-2222-4333-8444-555555555555.jsonl`, built from the cwd realpath [obs]. Set `CLAUDE_CODE_PROJECT_DIR_NAME` together with `CLAUDE_CONFIG_DIR` to pin the directory name (v2.1.234+) [doc]. Retention defaults to 30 days (`cleanupPeriodDays`) [doc]. The transcript format is internal [doc].
- **Credentials** live in the same dir on Linux: `$CLAUDE_CONFIG_DIR/.credentials.json`, mode 0600 [doc]. Surviving a pod restart therefore needs a persistent volume for `~/.claude`, which covers both the transcript and the login. That is the grey area in PLAN.md open question 4.
- **Resume after a restart:** `claude -p … --resume <session-id>` with the same flags as before. Re-pass `--dangerously-skip-permissions`, `--mcp-config`, `--add-dir` and `--settings`, because they are not restored [doc]. History and model are restored [doc][obs].
  - Lookup by ID works from **any** cwd [doc][obs]. However, the resumed session **runs in the new cwd**: `system/init.cwd` showed the new directory, and tools act there [obs]. The transcript keeps appending to the original file [obs]. Practical rule: always mount the checkout at the same path, e.g. `/workspace/repo`.
  - Start every thread with `--session-id <thread-uuid>`, so the ID is known before the first event, and use `--resume <thread-uuid>` afterwards. `--session-id` on an existing ID fails [obs].
- **A turn cut off by a crash:** on resume the next prompt drives the conversation. To re-run the cut-off turn automatically, set `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1` [doc].

---

## 6. Gotchas

- **Line size:** Go's `bufio.Scanner` defaults to a 64 KB token limit. `system/init` alone is about 2.5 KB, and tool results can be MBs. Use `bufio.Reader.ReadBytes('\n')` or `scanner.Buffer(buf, 64<<20)`. The Python SDK caps lines at 1 MB by default [sdk].
- **Pipes:** drain stderr continuously so the pipe can't fill. Invalid-flag errors and CLI warnings go to stderr [doc]. Serialize stdin writes. Stdout is line-delimited JSON with no extra buffering concerns observed.
- **stdin lifetime:** EOF while idle means exit 0. EOF mid-turn means the CLI finishes the turn, emits its `result`, then exits 0 [obs]. A `-p` run may wait up to 10 min after EOF for background work (`CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS`) [doc]. For a graceful stop, close stdin and wait up to ~5 s before SIGTERM, which is what the SDK does so the transcript flushes [sdk].
- **Exit codes:** 0 for a normal end; 1 for invalid flags, a malformed stdin line, a duplicate `--session-id`, or a one-shot run whose last result was an error [obs]; 143 for SIGTERM [doc][obs].
- **Not logged in:** returns immediately with no network call [obs]. Shortened (`"…":"…"` stands for omitted fields):
  ```json
  {"type":"assistant","message":{"id":"2d21e45a-e1d9-46d7-a3cf-725f0685b213","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","type":"message","content":[{"type":"text","text":"Not logged in · Please run /login"}],"…":"…"},"parent_tool_use_id":null,"session_id":"71521006-3237-4d5b-9810-8df34b349029","uuid":"16a4f643-b836-4c50-81df-52c023a1837f","error":"authentication_failed","is_api_error_message":true}
  ```
  It is followed by a `result` with `"is_error":true,"result":"Not logged in · Please run /login","terminal_reason":"api_error"`.
- **Rejected credential (401):** the CLI retries about 10 times over about 3 minutes, emitting `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":565,"error_status":401,"error":"authentication_failed",…}` each time. Only then does the `result` arrive: `"is_error":true,"api_error_status":401`, with the text `Invalid API key · Fix external API key` in API-key mode [obs]. The OAuth wording will differ. Runner policy: on the first `api_retry` whose `error` is `authentication_failed`, `oauth_org_not_allowed`, `account_on_hold` or `billing_error`, surface "Claude login needed" and send `interrupt`. The long-lived process survives error results and accepts the next message [obs].
- **`system/init` repeats every turn.** Don't treat it as the process start.
- **Mid-session changes:** `set_model` and `apply_flag_settings{effortLevel}` apply from the next request [obs]. This matches the plan's "SetConfig applies from the next turn".
- **`CLAUDE_CODE_ENTRYPOINT`:** the SDK sets it to `sdk-py`/`sdk-ts`. If the runner inherits an env that has it, unset it, together with `CLAUDECODE` (the SDK strips that one) [sdk].
- **Pin the CLI version** in the sandbox image and disable the auto-updater. Use `system/init.capabilities` for feature checks.
