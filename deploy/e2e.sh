#!/usr/bin/env bash
# End-to-end check of a running system with the fake agent (no Claude login).
# `mise run e2e` builds the fake image and starts a dev-auth API for it, then runs this.
set -euo pipefail
API=${API:-http://localhost:8787}
REPO=${REPO:-https://github.com/octocat/Hello-World.git}
BASE=${BASE:-master}

kc() { mise exec -- kubectl --context kind-banda -n banda "$@"; }
call() { curl -sf -X POST "$API/banda.v1.$1" -H 'Content-Type: application/json' -H "X-Banda-User: ${3:-dev}" -d "$2"; }
thread() { call ThreadService/GetThread "{\"threadId\":\"$ID\"}" | jq -r ".thread.$1"; }
wait_for() {
	for _ in $(seq 60); do [ "$(thread status)" = "$1" ] && return; sleep 2; done
	echo "not ok - waited for $1, status is $(thread status)"; exit 1
}
ok() { echo "ok - $*"; }
fail() { echo "not ok - $*"; exit 1; }
sql() { kc exec postgres-0 -- psql -U banda -tAc "$1"; }
# The agent's latest reply in thread $ID (the fake names its model and effort).
reply() { sql "select body->'agentText'->>'text' from events where thread_id='$ID' and body ? 'agentText' order by seq desc limit 1"; }
# Output of a terminal stream (OpenTerminal, OpenLoginTerminal).
term() { mise exec -- buf curl --schema proto --protocol connect -H "X-Banda-User: ${3:-dev}" --data "$2" "$API/banda.v1.$1" | jq -r '.data // empty' | base64 -d; }
# What a server stream sent within 4 s, one JSON object per message.
stream() {
	"$BUF" curl --schema proto --protocol connect -H "X-Banda-User: ${3:-dev}" --data "$2" "$API/banda.v1.$1" >"$TMP/stream" 2>&1 &
	local p=$!; sleep 4; kill $p 2>/dev/null || true; wait $p 2>/dev/null || true
	jq -c 'select(.code == null)' "$TMP/stream" 2>/dev/null # drop the error our kill causes
}
BUF=$(mise which buf)
TMP=$(mktemp -d)
# Events of thread $ID with the setup step id $1 in state $2.
steps() { sql "select count(*) from events where thread_id='$ID' and body->'setupStep'->>'id'='$1' and body->'setupStep'->>'state'='SETUP_STATE_$2'"; }
login_state() {
	for _ in $(seq 30); do [ "$(call SettingsService/GetSettings '{}' | jq -r '.accounts[0].loginState')" = "$1" ] && return; sleep 1; done
	fail "login state is not $1"
}

# Login first: it also logs the dev user back in after an aborted run.
term SettingsService/OpenLoginTerminal '{"harness":"claude","action":"LOGIN_ACTION_LOGOUT","cols":80,"rows":24}' | grep -q "logged out"
login_state LOGIN_STATE_LOGGED_OUT
term SettingsService/OpenLoginTerminal '{"harness":"claude","action":"LOGIN_ACTION_LOGIN","cols":80,"rows":24}' | grep -q "Login successful"
login_state LOGIN_STATE_LOGGED_IN
POD=login-$(printf %s dev | shasum -a 256 | cut -c1-16)
kc get pod "$POD" -o json | jq -e '(.spec.containers[0].envFrom == null) and ([.spec.volumes[] | select(.secret or .persistentVolumeClaim)] | length == 1)' >/dev/null
ok "login environment: logout and login in its terminal update the login state; it mounts only the Claude volume"

SECRET="e2e-secret-$RANDOM$RANDOM"
call SettingsService/SetSecret "{\"name\":\"E2E_SECRET\",\"value\":\"$SECRET\"}" >/dev/null
for name in ANTHROPIC_API_KEY ANTHROPIC_MODEL CLAUDE_CODE_OAUTH_TOKEN CLAUDE_X BANDA_THREAD_ID PATH lower 1X; do
	call SettingsService/SetSecret "{\"name\":\"$name\",\"value\":\"x\"}" >/dev/null 2>&1 && fail "secret name $name accepted"
done
call SettingsService/SetSecret '{"name":"E2E_OAUTH","value":"sk-ant-oat01-e2e"}' >/dev/null 2>&1 && fail "a subscription token was accepted as a secret"
call SettingsService/SetAuthMethod '{"harness":"claude","authMethod":"AUTH_METHOD_API_KEY","apiKey":"sk-ant-ort01-e2e"}' >/dev/null 2>&1 && fail "a subscription token was accepted as an API key"
S=$(call SettingsService/GetSettings '{}')
echo "$S" | jq -e '(.secrets | map(.name) | index("E2E_SECRET")) and .accounts[0].harness == "claude"' >/dev/null
echo "$S" | grep -q "$SECRET" && fail "settings return a secret value"
ok "secrets: set, reserved and invalid names and subscription tokens refused, values never returned"

ID=$(call ThreadService/CreateThread "{\"repoUrl\":\"$REPO\",\"baseBranch\":\"$BASE\",\"prompt\":\"e2e: add a file\"}" | jq -r .thread.id)
wait_for STATUS_AWAITING_INPUT
thread diff | grep -q hello1.txt
ok "thread $ID: sandbox started, first turn ran, diff has its file"

[ "$(steps checkout DONE)" = 1 ] && [ "$(steps setup SKIPPED)" = 1 ]
ok "setup steps are reported: checkout done, no .banda/setup.sh"

[ "$(sql "select count(*) from events where thread_id='$ID' and body->'toolCall'->>'name'='TodoWrite'")" = 2 ]
[ "$(sql "select body->'toolCall'->>'name' from events where thread_id='$ID' and body->'toolCall'->>'parentToolCallId'='toolu_task1'")" = Glob ]
[ "$(sql "select count(*) from events where thread_id='$ID' and body->'agentText'->>'text'='Two files.'")" = 0 ]
ok "plans (TodoWrite) are stored; a subagent's tool calls carry their Task call, its text is dropped"
# Bob and carol watch the thread; carol types. Bob sees both present and carol typing.
W=$(mktemp)
BUF=$(mise which buf) # run directly, so kill ends the stream
watch() { exec "$BUF" curl --schema proto --protocol connect -H "X-Banda-User: $1" --data "{\"threadId\":\"$ID\",\"afterSeq\":\"999999\"}" "$API/banda.v1.ThreadService/WatchThread"; }
watch bob >"$W" & BOB=$!
watch carol >/dev/null & CAROL=$!
sleep 3
call ThreadService/SendTyping "{\"threadId\":\"$ID\"}" carol >/dev/null
sleep 1; kill $CAROL; sleep 1; kill $BOB
jq -se 'map(.event) | any(.presenceChanged.userIds == ["bob","carol"]) and any(.typing and .actor == "carol") and (last.presenceChanged.userIds == ["bob"])' "$W" >/dev/null || fail "presence and typing: $(cat "$W")"
[ "$(sql "select count(*) from events where thread_id='$ID' and (body ? 'presenceChanged' or body ? 'typing')")" = 0 ]
rm "$W"
ok "watchers see who is here and who is typing, and nothing of it is stored"

call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"nice, carol what do you think?\"}}" bob >/dev/null
[ "$(thread mode)" = MODE_CHAT ]
ok "a teammate's post switches to chat mode"

call ThreadService/InboxMarkRead "{\"threadId\":\"$ID\"}" >/dev/null
[ "$(thread unread)" = null ] || fail "still unread after InboxMarkRead"
INBOX=$(mktemp)
mise exec -- buf curl --schema proto --protocol connect -H "X-Banda-User: carol" --data '{}' "$API/banda.v1.ThreadService/InboxWatch" >"$INBOX" 2>/dev/null &
INBOX_PID=$!
for _ in $(seq 30); do [ -s "$INBOX" ] && break; sleep 0.5; done
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"@carol over to you\",\"mentionedUserIds\":[\"carol\"]}}" bob >/dev/null
mentioned() { jq -se --arg id "$ID" 'last | .mentioned // [] | map(.id) | index($id)' "$INBOX" >/dev/null 2>&1; }
for _ in $(seq 20); do mentioned && break; sleep 0.5; done
kill $INBOX_PID
mentioned || fail "carol's inbox stream did not get the mention: $(cat "$INBOX")"
[ "$(thread unread)" = true ] || fail "a teammate's post did not make the thread unread for its owner"
ok "the inbox stream pushes a new mention live; a teammate's post makes the thread unread until it is read"

ask() { call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"@agent $1\",\"mentionsAgent\":true}}" bob >/dev/null; }
request() { sql "select seq from events where thread_id='$ID' and body ? 'turnRequest' order by seq desc limit 1"; }
ask "one more"
sleep 2
[ "$(thread status)" = STATUS_AWAITING_INPUT ] && [ "$(thread turnRequests)" = 1 ] || fail "bob's request did not wait"
thread diff | grep -q hello2.txt && fail "bob's request ran without approval"
REQ=$(request)
call ThreadService/ApproveTurn "{\"threadId\":\"$ID\",\"requestSeq\":$REQ}" bob >/dev/null 2>&1 && fail "bob approved his own request"
call ThreadService/ApproveTurn "{\"threadId\":\"$ID\",\"requestSeq\":$REQ}" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
thread diff | grep -q 'bob: @agent one more' || fail "the approved turn did not run as bob's"
[ "$(thread turnRequests)" = null ] || fail "the request is still open"
call ThreadService/ApproveTurn "{\"threadId\":\"$ID\",\"requestSeq\":$REQ}" >/dev/null 2>&1 && fail "a request was approved twice"
ok "a teammate's @agent post waits for the owner; approving runs it as the teammate's turn"

ask "not this"
REQ=$(request)
call ThreadService/DeclineTurn "{\"threadId\":\"$ID\",\"requestSeq\":$REQ,\"note\":\"not now\"}" >/dev/null
sleep 2
[ "$(thread status)" = STATUS_AWAITING_INPUT ] && [ "$(thread turnRequests)" = null ] || fail "decline left the thread $(thread status)"
thread diff | grep -q hello3.txt && fail "a declined request ran"
[ "$(sql "select body->'turnRequestResolved'->>'note' from events where thread_id='$ID' and body ? 'turnRequestResolved' order by seq desc limit 1")" = "not now" ]
ok "declining a request runs no turn and keeps the note"

call ThreadService/Trust "{\"threadId\":\"$ID\",\"trusted\":true}" bob >/dev/null 2>&1 && fail "bob trusted himself"
call ThreadService/Trust "{\"threadId\":\"$ID\",\"trusted\":true}" >/dev/null
[ "$(thread trusted)" = true ]
ask "trusted"
sleep 1; wait_for STATUS_AWAITING_INPUT
thread diff | grep -q 'bob: @agent trusted' || fail "bob's post in a trusted thread did not run"
call ThreadService/Trust "{\"threadId\":\"$ID\",\"trusted\":false}" >/dev/null
ok "in a trusted thread a teammate's @agent post runs directly"

call ThreadService/SetMode "{\"threadId\":\"$ID\",\"mode\":\"MODE_AGENT\"}" bob >/dev/null 2>&1 && { echo "not ok - bob switched modes"; exit 1; }
call ThreadService/SetMode "{\"threadId\":\"$ID\",\"mode\":\"MODE_AGENT\"}" >/dev/null
ok "only the owner ends chat mode"

reply | grep -q "with claude-opus-5-5 · high"
call ThreadService/SetAgentSettings "{\"threadId\":\"$ID\",\"model\":\"claude-haiku-5-5\"}" bob >/dev/null 2>&1 && fail "bob changed the model"
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"keep going\"}}" >/dev/null
call ThreadService/SetAgentSettings "{\"threadId\":\"$ID\",\"model\":\"claude-sonnet-5-5\",\"effort\":\"medium\"}" >/dev/null
[ "$(thread model) $(thread effort)" = "claude-sonnet-5-5 medium" ]
sleep 1; wait_for STATUS_AWAITING_INPUT
reply | grep -q "with claude-opus-5-5 · high" || fail "the running turn was cut short or switched: $(reply)"
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"and again\"}}" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
reply | grep -q "with claude-sonnet-5-5 · medium" || fail "the agent did not restart with the new settings: $(reply)"
[ "$(sql "select body->>'actor' from events where thread_id='$ID' and body->'agentSettings'->>'model'='claude-sonnet-5-5'")" = dev ]
ok "the owner switches model and effort mid-thread: the running turn finishes, the next one runs on the new settings, the chat records it"

REVIEW='{"path":"hello1.txt","startLine":1,"endLine":1,"code":"+hello 1","body":"e2e: say hi instead"}'
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"review\":[{\"path\":\"x\",\"startLine\":1,\"endLine\":1,\"body\":\" \"}]}}" >/dev/null 2>&1 && fail "a review comment without text was accepted"
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"review\":[$REVIEW]}}" >/dev/null
[ "$(thread status)" = STATUS_AWAITING_INPUT ] && [ "$(thread mode)" = MODE_AGENT ]
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"@agent please address these review comments.\",\"mentionsAgent\":true,\"review\":[$REVIEW]}}" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
thread diff | grep -q 'hello1.txt, line 1:.*+hello 1.*e2e: say hi instead' || fail "the agent did not get the review"
ok "the owner's review is a note until it asks @agent; then the agent gets each comment with its code"

call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"@agent see my review\",\"mentionsAgent\":true,\"review\":[{\"path\":\"hello2.txt\",\"startLine\":1,\"endLine\":1,\"code\":\"+hello 2\",\"body\":\"e2e: bob reviews\"}]}}" bob >/dev/null
sleep 2
[ "$(thread turnRequests)" = 1 ] && ! thread diff | grep -q 'e2e: bob reviews' || fail "bob's review ran without approval"
call ThreadService/ApproveTurn "{\"threadId\":\"$ID\",\"requestSeq\":$(request)}" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
thread diff | grep -q 'bob: @agent see my review.*hello2.txt, line 1:.*e2e: bob reviews' || fail "the approved review did not reach the agent"
ok "a teammate's review asking @agent waits for approval, then reaches the agent with its comments"

kc exec "t-$ID" -- sh -c 'curl -sf -X POST "$BANDA_API_URL/mcp" -H "Authorization: Bearer $BANDA_RUNNER_TOKEN" -H "X-Banda-Thread: $BANDA_THREAD_ID" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}"' | grep -q read_thread
ok "the Banda MCP server is reachable from the sandbox"

term ThreadService/OpenTerminal "{\"threadId\":\"$ID\",\"cols\":80,\"rows\":24,\"command\":\"echo e2e-terminal-\$((6*7))\"}" | grep -q e2e-terminal-42
ok "terminal runs commands in the sandbox"

OUT=$(term ThreadService/OpenTerminal "{\"threadId\":\"$ID\",\"cols\":80,\"rows\":24,\"command\":\"printf %s \$E2E_SECRET > leak.txt; echo secret-\$E2E_SECRET key-\${ANTHROPIC_API_KEY:-none}\"}")
echo "$OUT" | grep -q "secret-$SECRET key-none" || fail "terminal output: $OUT"
LEAK=$(call ThreadService/ReadFile "{\"threadId\":\"$ID\",\"path\":\"leak.txt\"}" | jq -r .content)
[ "$LEAK" = "[redacted]" ]
ok "the owner's secrets are in the environment (no API key), and redacted from files"

call ThreadService/ListFiles "{\"threadId\":\"$ID\"}" | jq -e '.paths | index("README")' >/dev/null
call ThreadService/ReadFile "{\"threadId\":\"$ID\",\"path\":\"../.claude/.credentials.json\"}" >/dev/null 2>&1 && { echo "not ok - read outside the checkout"; exit 1; }
ok "files are listed; paths outside the checkout are refused"

call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"start the preview\"}}" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
for _ in $(seq 10); do stream ThreadService/PreviewWatch "{\"threadId\":\"$ID\"}" bob | tail -1 | jq -e '.running and .ports == [{port: 5173, process: "http.server"}]' >/dev/null && break; done
tail -1 <(stream ThreadService/PreviewWatch "{\"threadId\":\"$ID\"}" bob) | jq -e '.ports == [{port: 5173, process: "http.server"}]' >/dev/null || fail "preview ports: $(cat "$TMP/stream")"
ok "the preview lists the dev server the agent started (port 5173, http.server), to teammates too"

URL=$(call ThreadService/PreviewOpen "{\"threadId\":\"$ID\",\"port\":5173,\"path\":\"/\"}" bob | jq -r .url)
PREVIEW=${URL%%/__banda/*}
curl -sf -c "$TMP/jar" -b "$TMP/jar" -L "$URL" | grep -q e2e-preview-page
curl -sf -b "$TMP/jar" "$PREVIEW/assets/app.js" | grep -q e2e-preview-asset
[ "$(curl -s -o /dev/null -w '%{http_code}' "$PREVIEW/assets/app.js")" = 401 ] || fail "the preview answers without its cookie"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$PREVIEW/__banda/open?token=forged&path=/")" = 401 ] || fail "a forged preview token was accepted"
ok "the preview serves the page and its absolute-path asset on $PREVIEW, only with the cookie from PreviewOpen"

call ThreadService/Push "{\"threadId\":\"$ID\"}" >/dev/null
for _ in $(seq 20); do [ "$(sql "select count(*) from events where thread_id='$ID' and body->'notice'->>'text' like 'Push%'")" -gt 0 ] && break; sleep 1; done
[ "$(sql "select count(*) from events where thread_id='$ID' and body->'notice'->>'text' like 'Push%'")" -gt 0 ]
ok "push reports its result as a notice"

# The parent's agent calls MCP the way claude would, with its thread's runner token.
mcp() { kc exec "t-$ID" -- sh -c 'curl -sf -X POST "$BANDA_API_URL/mcp" -H "Authorization: Bearer $BANDA_RUNNER_TOKEN" -H "X-Banda-Thread: $BANDA_THREAD_ID" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d "$0"' "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}"; }
CHILD=$(mcp create_thread '{"prompt":"e2e: a sub-thread"}' | grep -oE 'thread_id[^a-z0-9]+[a-z0-9]{26}' | head -1 | grep -oE '[a-z0-9]{26}$') || fail "create_thread returned no thread id"
PARENT=$ID ID=$CHILD
[ "$(thread parentId)" = "$PARENT" ] && [ "$(thread parentTitle)" != null ] || fail "the sub-thread does not link to its parent"
[ "$(thread baseBranch)" = "$BASE" ] || fail "an unpushed parent's sub-thread should start from its base, got $(thread baseBranch)"
wait_for STATUS_AWAITING_INPUT
ID=$PARENT
lineage() { sql "select body->'lineageUpdate'->>'text' from events where thread_id='$ID' and body->'lineageUpdate'->>'threadId'='$CHILD' and body->'lineageUpdate'->>'status'='$1'"; }
for _ in $(seq 20); do [ -n "$(lineage STATUS_AWAITING_INPUT)" ] && break; sleep 0.5; done
lineage STATUS_AWAITING_INPUT | grep -q hello || fail "the parent got no finish notice: $(lineage STATUS_AWAITING_INPUT)"
[ "$(sql "select count(*) from events where thread_id='$ID' and body->'lineageUpdate'->>'status'='STATUS_STARTING'")" = 1 ] || fail "the spawn was not announced in the parent"
mcp list_children '{}' | grep -q "$CHILD" || fail "list_children does not list the sub-thread"
ok "an agent spawns a sub-thread over MCP; it links to its parent, which hears when it finishes"

BOBS=$(call ThreadService/SpawnThread "{\"parentId\":\"$ID\",\"prompt\":\"e2e: bob's sub-thread\"}" bob | jq -r '.thread | select(.ownerId == "bob" and .parentId != null) | .id')
[ -n "$BOBS" ] || fail "a teammate could not spawn a sub-thread"
call ThreadService/Snooze "{\"threadId\":\"$BOBS\",\"until\":\"2099-01-01T00:00:00Z\"}" bob >/dev/null
[ "$(call ThreadService/LineageChildren "{\"threadId\":\"$ID\"}" | jq -r '.threads | length')" = 2 ] || fail "LineageChildren does not list both sub-threads"
call ThreadService/Snooze "{\"threadId\":\"$CHILD\",\"until\":\"2099-01-01T00:00:00Z\"}" >/dev/null
ok "a teammate spawns a sub-thread of their own; the parent lists both"

call ThreadService/Snooze "{\"threadId\":\"$ID\",\"until\":\"2099-01-01T00:00:00Z\"}" >/dev/null
[ "$(thread status)" = STATUS_SUSPENDED ]
for _ in $(seq 30); do kc get pod "t-$ID" >/dev/null 2>&1 || break; sleep 2; done
! kc get pod "t-$ID" >/dev/null 2>&1
ok "snoozing suspends the environment (pod gone, volumes kept)"

stream ThreadService/PreviewWatch "{\"threadId\":\"$ID\"}" | tail -1 | grep -qx '{}' || fail "preview of a paused environment: $(cat "$TMP/stream")"
[ "$(curl -s -o /dev/null -w '%{http_code}' -b "$TMP/jar" "$PREVIEW/")" = 503 ]
stream ThreadService/PreviewWatch "{\"threadId\":\"$ID\",\"wake\":true}" bob >/dev/null; grep -q permission_denied "$TMP/stream" || fail "a teammate woke the preview: $(cat "$TMP/stream")"
[ "$(thread status)" = STATUS_SUSPENDED ]
ok "the preview of a paused environment says so and does not wake it; teammates can't wake it"

call ThreadService/SetAgentSettings "{\"threadId\":\"$ID\",\"effort\":\"max\"}" >/dev/null
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"wake up\"}}" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
reply | grep -q "with claude-sonnet-5-5 · max" || fail "settings changed while paused were not applied: $(reply)"
thread diff | grep -q hello8.txt
ok "a prompt resumes the environment with the checkout intact and the settings changed while paused"
thread diff | grep -q "Sub-thread $CHILD" || fail "the parent agent's prompt does not mention its sub-thread"
ok "the parent agent hears of its sub-threads in its next prompt"
thread diff | grep -q "$SECRET" && fail "the diff contains a secret"
thread diff | grep -q '\[redacted\]'
ok "secrets are redacted from the diff"

ID=$(call ThreadService/CreateThread '{"repoUrl":"file:///opt/e2e/setup-fails.git","baseBranch":"main","prompt":"e2e: never runs"}' | jq -r .thread.id)
wait_for STATUS_ERRORED
[ "$(steps setup FAILED)" = 1 ]
sql "select body->'setupStep'->>'output' from events where thread_id='$ID' and body->'setupStep'->>'state'='SETUP_STATE_FAILED'" | grep -q 'setup sees \[redacted\]'
sql "select body->'statusChanged'->>'reason' from events where thread_id='$ID' and body ? 'statusChanged' order by seq desc limit 1" | grep -q "^Setup failed"
sleep 5
[ "$(thread status)" = STATUS_ERRORED ] && [ "$(kc get pod "t-$ID" -o jsonpath='{.status.containerStatuses[0].restartCount}')" = 0 ]
term ThreadService/OpenTerminal "{\"threadId\":\"$ID\",\"cols\":80,\"rows\":24,\"command\":\"echo still-up\"}" | grep -q still-up
ok "a failing .banda/setup.sh errors the thread with its redacted output; the runner stays up for the terminal"

ID=$(call ThreadService/CreateThread '{"repoUrl":"file:///opt/e2e/setup-bg.git","baseBranch":"main","prompt":"e2e: setup in the background"}' | jq -r .thread.id)
wait_for STATUS_AWAITING_INPUT
[ "$(steps setup DONE)" = 1 ]
ok "a .banda/setup.sh that leaves a background process running succeeds"

U=e2e-$RANDOM$RANDOM
call SettingsService/SetAuthMethod '{"harness":"claude","authMethod":"AUTH_METHOD_API_KEY"}' "$U" >/dev/null 2>&1 && fail "API key mode without a key"
call SettingsService/SetAuthMethod "{\"harness\":\"claude\",\"authMethod\":\"AUTH_METHOD_API_KEY\",\"apiKey\":\"key-$SECRET\"}" "$U" >/dev/null
S=$(call SettingsService/GetSettings '{}' "$U")
echo "$S" | jq -e '.accounts[0] | .authMethod == "AUTH_METHOD_API_KEY" and .apiKeySet' >/dev/null
echo "$S" | grep -q "$SECRET" && fail "settings return the API key"
ID=$(call ThreadService/CreateThread "{\"repoUrl\":\"$REPO\",\"baseBranch\":\"$BASE\",\"prompt\":\"e2e: api key\"}" "$U" | jq -r .thread.id)
wait_for STATUS_AWAITING_INPUT
[ "$(steps login SKIPPED)" = 1 ]
term ThreadService/OpenTerminal "{\"threadId\":\"$ID\",\"cols\":80,\"rows\":24,\"command\":\"echo key-\${#ANTHROPIC_API_KEY}\"}" "$U" | grep -q "key-$((${#SECRET} + 4))"
ok "API key mode: the key reaches the environment and the login wait is skipped"

call SettingsService/SetAuthMethod '{"harness":"claude","authMethod":"AUTH_METHOD_SUBSCRIPTION"}' "$U" >/dev/null
call ThreadService/Snooze "{\"threadId\":\"$ID\",\"until\":\"2099-01-01T00:00:00Z\"}" "$U" >/dev/null
for _ in $(seq 30); do kc get pod "t-$ID" >/dev/null 2>&1 || break; sleep 2; done
call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"wake up\"}}" "$U" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
term ThreadService/OpenTerminal "{\"threadId\":\"$ID\",\"cols\":80,\"rows\":24,\"command\":\"echo key-\${ANTHROPIC_API_KEY:-none}\"}" "$U" | grep -q key-none
ok "switching back to the subscription applies when the environment resumes"

[ "$(sql "select count(*) from events where body::text like '%$SECRET%'")" = 0 ] || fail "a secret value was stored in events"
ok "no secret value reached the database"

call ThreadService/Snooze "{\"threadId\":\"$ID\",\"until\":\"2099-01-01T00:00:00Z\"}" >/dev/null # leave no pod behind

ID=$(call ThreadService/CreateThread "{\"repoUrl\":\"$REPO\",\"baseBranch\":\"no-such-branch\",\"prompt\":\"e2e: bad base\"}" | jq -r .thread.id)
wait_for STATUS_ERRORED
sleep 15
[ "$(kc get pod "t-$ID" -o jsonpath='{.status.containerStatuses[0].restartCount}')" = 0 ]
[ "$(steps checkout FAILED)" = 1 ]
ok "a bad base branch errors once, without a crash loop"
call ThreadService/Snooze "{\"threadId\":\"$ID\",\"until\":\"2099-01-01T00:00:00Z\"}" >/dev/null

echo "all checks passed"
