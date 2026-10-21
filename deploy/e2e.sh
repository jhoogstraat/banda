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
# Output of a terminal stream (OpenTerminal, OpenLoginTerminal).
term() { mise exec -- buf curl --schema proto --protocol connect -H "X-Banda-User: ${3:-dev}" --data "$2" "$API/banda.v1.$1" | jq -r '.data // empty' | base64 -d; }
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

call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"@agent one more\",\"mentionsAgent\":true}}" bob >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
thread diff | grep -q hello2.txt
ok "mentioning @agent in chat mode starts a turn"

call ThreadService/SetMode "{\"threadId\":\"$ID\",\"mode\":\"MODE_AGENT\"}" bob >/dev/null 2>&1 && { echo "not ok - bob switched modes"; exit 1; }
call ThreadService/SetMode "{\"threadId\":\"$ID\",\"mode\":\"MODE_AGENT\"}" >/dev/null
ok "only the owner ends chat mode"

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

call ThreadService/Push "{\"threadId\":\"$ID\"}" >/dev/null
for _ in $(seq 20); do [ "$(sql "select count(*) from events where thread_id='$ID' and body->'notice'->>'text' like 'Push%'")" -gt 0 ] && break; sleep 1; done
[ "$(sql "select count(*) from events where thread_id='$ID' and body->'notice'->>'text' like 'Push%'")" -gt 0 ]
ok "push reports its result as a notice"

call ThreadService/Snooze "{\"threadId\":\"$ID\",\"until\":\"2099-01-01T00:00:00Z\"}" >/dev/null
[ "$(thread status)" = STATUS_SUSPENDED ]
for _ in $(seq 30); do kc get pod "t-$ID" >/dev/null 2>&1 || break; sleep 2; done
! kc get pod "t-$ID" >/dev/null 2>&1
ok "snoozing suspends the environment (pod gone, volumes kept)"

call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"wake up\"}}" >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
thread diff | grep -q hello3.txt
ok "a prompt resumes the environment with the checkout intact"
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
