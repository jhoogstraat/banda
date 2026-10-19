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
sql() { kc exec postgres-0 -- psql -U banda -tAc "$1"; }

ID=$(call ThreadService/CreateThread "{\"repoUrl\":\"$REPO\",\"baseBranch\":\"$BASE\",\"prompt\":\"e2e: add a file\"}" | jq -r .thread.id)
wait_for STATUS_AWAITING_INPUT
thread diff | grep -q hello1.txt
ok "thread $ID: sandbox started, first turn ran, diff has its file"

call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"nice, carol what do you think?\"}}" bob >/dev/null
[ "$(thread mode)" = MODE_CHAT ]
ok "a teammate's post switches to chat mode"

call ThreadService/SendMessage "{\"threadId\":\"$ID\",\"message\":{\"text\":\"@agent one more\",\"mentionsAgent\":true}}" bob >/dev/null
sleep 1; wait_for STATUS_AWAITING_INPUT
thread diff | grep -q hello2.txt
ok "mentioning @agent in chat mode starts a turn"

call ThreadService/SetMode "{\"threadId\":\"$ID\",\"mode\":\"MODE_AGENT\"}" bob >/dev/null 2>&1 && { echo "not ok - bob switched modes"; exit 1; }
call ThreadService/SetMode "{\"threadId\":\"$ID\",\"mode\":\"MODE_AGENT\"}" >/dev/null
ok "only the owner ends chat mode"

kc exec "t-$ID" -- sh -c 'curl -sf -X POST "$BANDA_API_URL/mcp" -H "Authorization: Bearer $BANDA_RUNNER_TOKEN" -H "X-Banda-Thread: $BANDA_THREAD_ID" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}"' | grep -q read_thread
ok "the Banda MCP server is reachable from the sandbox"

mise exec -- buf curl --schema proto --protocol connect -H "X-Banda-User: dev" \
	--data "{\"threadId\":\"$ID\",\"cols\":80,\"rows\":24,\"command\":\"echo e2e-terminal-\$((6*7))\"}" \
	"$API/banda.v1.ThreadService/OpenTerminal" | jq -r '.data // empty' | base64 -d | grep -q e2e-terminal-42
ok "terminal runs commands in the sandbox"

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

echo "all checks passed"
