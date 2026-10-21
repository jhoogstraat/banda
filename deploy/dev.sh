#!/usr/bin/env bash
# Local dev stack. Run through mise, which provides the tools and env:
#   mise run up     Podman machine, kind cluster, API and web (PUBLIC_HOST, default: LAN address)
#   mise run e2e    the end-to-end suite (web/e2e) with the fake agent, in a world of its own
#   mise run down   stop the servers and the kind node
#   mise run clean  delete sandboxes of threads untouched for CLEAN_HOURS (default 24)
set -euo pipefail
cd "$(dirname "$0")/.."
LOG=/tmp/banda-run
mkdir -p "$LOG"

kc() { kubectl --context kind-banda "$@"; }
free() { lsof -tiTCP:"$1" -sTCP:LISTEN | xargs kill 2>/dev/null || true; }
# start <log name> <cmd...>: run detached, so it outlives this script and the harness.
start() { local name=$1; shift; nohup "$@" >"$LOG/$name.log" 2>&1 </dev/null & PID=$!; disown; }
# until_ok <log name> <cmd...>: retry for 5 minutes, fail early if the last started server died.
until_ok() {
	local name=$1; shift
	for _ in $(seq 300); do
		"$@" >/dev/null 2>&1 && return
		kill -0 "$PID" 2>/dev/null || { tail -20 "$LOG/$name.log"; echo "$name exited" >&2; exit 1; }
		sleep 1
	done
	echo "timed out: $*" >&2; exit 1
}
listening() { lsof -tiTCP:"$1" -sTCP:LISTEN; }

# Podman on macOS runs one VM at a time, so Banda shares the default machine with other projects.
machine() {
	[ "$(podman machine inspect podman-machine-default --format '{{.State}}')" = running ] ||
		podman machine start podman-machine-default
}

cluster() {
	if ! kind get clusters 2>/dev/null | grep -qx banda; then
		mise run cluster
		mise run image
	else
		podman start banda-control-plane >/dev/null
		for _ in $(seq 60); do kc get nodes >/dev/null 2>&1 && break; sleep 2; done
		kc -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=300s
		kc -n banda rollout status statefulset/postgres --timeout=300s
		kc -n banda rollout status deployment/dex --timeout=300s
	fi
	# Again on every start, so clusters made before a policy get it.
	kc apply -f deploy/postgres.yaml >/dev/null
	kc -n banda apply -f deploy/sandbox-policy.yaml >/dev/null
	host_api banda 8700 8999 # mise run api, and other checkouts' on ports of their own
}

# host_api <namespace> <port> [<end port>]: lets the namespace's sandboxes reach an API on this machine,
# at host.containers.internal (Podman's address for it), on that port only: Postgres, the kind API server
# and everything else on the host stay out of reach (deploy/sandbox-policy.yaml).
host_api() {
	kc -n "$1" apply -f - >/dev/null <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: host-api-$2}
spec:
  podSelector: {matchExpressions: [{key: banda.dev/sandbox, operator: Exists}]}
  policyTypes: [Egress]
  egress: [{to: [{ipBlock: {cidr: 192.168.127.254/32}}], ports: [{port: $2${3:+, endPort: $3}}]}]
EOF
}

servers() {
	export PUBLIC_HOST=${PUBLIC_HOST:-$(ipconfig getifaddr en0 || echo localhost)}
	local h=$PUBLIC_HOST
	mise run dex
	free 8787; free 8788; free 5173
	start web mise run web
	# The API discovers Dex through the web server, so it starts second.
	until_ok web curl -sf "http://$h:5173/dex/.well-known/openid-configuration"
	start api mise run api
	until_ok api listening 8787
	curl -s -o /dev/null -w "web %{http_code}\n" "http://$h:5173/"
	curl -s -o /dev/null -w "login %{http_code} (302 expected)\n" "http://$h:5173/auth/login"
	echo "Open http://$h:5173 and sign in as alice@example.com / password. Logs: $LOG"
}

# mise run e2e [-- <Playwright args>]: the Playwright suite in web/e2e against a world of its own, so
# runs from several checkouts go side by side: namespace banda-e2e-<run> (sandboxes, volumes, secrets,
# a git server and Dex), database banda_e2e_<run>, image localhost/banda-sandbox:fake-<run> and two APIs
# on free ports (dev auth, and OIDC for the sign-in tests), serving the web build. <run> comes from the
# checkout path; a rerun wipes its world first. E2E_KEEP=1 keeps the namespace and database.
e2e() {
	run=$(printf %s "$PWD" | shasum | cut -c1-8)
	LOG=/tmp/banda-e2e-$run ns=banda-e2e-$run db=banda_e2e_$run
	mkdir -p "$LOG"
	if [ -f "$LOG/pid" ] && kill -0 "$(cat "$LOG/pid")" 2>/dev/null; then
		echo "an e2e run of this checkout is still running (pid $(cat "$LOG/pid"))" >&2; exit 1
	fi
	echo $$ >"$LOG/pid"
	PIDS=()
	trap e2e_down EXIT
	export SANDBOX_IMAGE=localhost/banda-sandbox:fake-$run
	TARGET=fake mise run image >"$LOG/image.log" 2>&1 & local image=$!
	go build -o "$LOG/api" ./cmd/api & local api=$!
	pnpm -C web build >"$LOG/web.log" 2>&1 & local web=$!
	pnpm -C web exec playwright install chromium >/dev/null & local chromium=$!
	kc delete ns "$ns" --ignore-not-found --timeout=300s >/dev/null
	pg -c "DROP DATABASE IF EXISTS $db WITH (FORCE)" -c "CREATE DATABASE $db"
	kc create ns "$ns" >/dev/null
	kc -n "$ns" apply -f deploy/sandbox-policy.yaml >/dev/null
	kc -n "$ns" apply -f - >/dev/null <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: sandboxes-to-e2e-git}
spec:
  podSelector: {matchExpressions: [{key: banda.dev/sandbox, operator: Exists}]}
  policyTypes: [Egress]
  egress: [{to: [{podSelector: {matchLabels: {run: e2e-git}}}], ports: [{port: 9418}]}]
EOF
	local p pp op opp dp
	read -r p pp op opp dp < <(node -e 'const n = require("net"); Promise.all([1, 2, 3, 4, 5].map(() => new Promise((r) => { const s = n.createServer().listen(0, () => r(s)); })))
		.then((ss) => { console.log(ss.map((s) => s.address().port).join(" ")); ss.forEach((s) => s.close()); })')
	sed -e "s|namespace: banda|namespace: $ns|" -e "s|issuer: http://localhost:5173|issuer: http://localhost:$dp|" -e "s|localhost:5173/auth|localhost:$op/auth|" \
		-e 's/type: NodePort/type: ClusterIP/' -e '/nodePort:/d' deploy/dex.yaml | kc apply -f - >/dev/null
	wait $image || { tail -30 "$LOG/image.log"; exit 1; }
	# The fixture repos at git://e2e-git/<name>.git; readonly refuses pushes.
	kc -n "$ns" run e2e-git --image="$SANDBOX_IMAGE" --image-pull-policy=Never --port=9418 --expose --command -- sh -c \
		'cp -r /opt/e2e /tmp/git && cp -r /tmp/git/plain.git /tmp/git/readonly.git &&
		for r in plain setup-fails setup-bg setup-hold; do git --git-dir=/tmp/git/$r.git config daemon.receivepack true; done &&
		exec git daemon --reuseaddr --base-path=/tmp/git --export-all /tmp/git' >/dev/null
	kc -n "$ns" rollout status deployment/dex --timeout=300s >/dev/null
	start dex kubectl --context kind-banda -n "$ns" port-forward svc/dex "$dp:5556"; PIDS+=("$PID")
	until_ok dex curl -sf "http://localhost:$dp/dex/.well-known/openid-configuration"
	wait $api
	wait $web || { tail -30 "$LOG/web.log"; exit 1; }
	export DATABASE_URL=postgres://banda:banda@localhost:5432/$db SANDBOX_NAMESPACE=$ns WEB_DIR=web/build DELETE_SETTLED_AFTER=20s IDLE_SWEEP_EVERY=2s
	host_api "$ns" "$p"; host_api "$ns" "$op"
	DEV_AUTH=1 ADDR=:$p PREVIEW_ADDR=:$pp RUNNER_API_URL=http://host.containers.internal:$p start api "$LOG/api"; PIDS+=("$PID")
	local api_pid=$PID
	OIDC_ISSUER=http://localhost:$dp/dex OIDC_CLIENT_ID=banda OIDC_CLIENT_SECRET=banda-dev-secret PUBLIC_URL=http://localhost:$op \
		ADDR=:$op PREVIEW_ADDR=:$opp RUNNER_API_URL=http://host.containers.internal:$op start oidc-api "$LOG/api"; PIDS+=("$PID")
	until_ok oidc-api listening "$op"
	PID=$api_pid until_ok api listening "$p"
	kc -n "$ns" wait --for=condition=Ready pod/e2e-git --timeout=120s >/dev/null
	wait $chromium
	echo "e2e world $ns: API http://localhost:$p (log $LOG/api.log), OIDC API http://localhost:$op, Dex :$dp"
	E2E_API=http://localhost:$p E2E_OIDC_API=http://localhost:$op E2E_NAMESPACE=$ns E2E_DB=$db E2E_DIR=$LOG E2E_LOG=$LOG/api.log \
		pnpm -C web exec playwright test "$@" || { echo "Logs: $LOG. Report: pnpm -C web exec playwright show-report" >&2; exit 1; }
}

e2e_down() {
	kill ${PIDS[@]+"${PIDS[@]}"} 2>/dev/null || true
	if [ -n "${E2E_KEEP:-}" ]; then
		echo "kept namespace $ns and database $db"
	else
		kc delete ns "$ns" --ignore-not-found --timeout=300s >/dev/null || true
		pg -c "DROP DATABASE IF EXISTS $db WITH (FORCE)" || true
	fi
	rm -f "$LOG/pid"
}
pg() { kc -n banda exec postgres-0 -- psql -U banda -q -v ON_ERROR_STOP=1 "$@"; }

# Dev only: every UI check leaves sandboxes behind. Deletes the thread sandboxes (their
# volumes go with them) older than CLEAN_HOURS whose thread wasn't touched in that time or is gone.
clean() {
	local h=${CLEAN_HOURS:-24} keep
	keep=$(kc -n banda exec postgres-0 -- psql -U banda -tAc "select 't-' || id from threads where updated_at > now() - interval '$h hours'")
	kc -n banda get sandboxes -l banda.dev/thread -o json |
		jq -r --argjson h "$h" '.items[] | select(.metadata.creationTimestamp | fromdate < now - $h * 3600) | .metadata.name' |
		while read -r name; do
			grep -qxF "$name" <<<"$keep" || kc -n banda delete sandbox "$name" --wait=false
		done
}

case ${1:-} in
up) machine; cluster; servers ;;
e2e) shift; machine; cluster; e2e "$@" ;;
clean) clean ;;
host-api) shift; host_api "$@" ;; # the e2e harness, for an API of a test's own
down) free 8787; free 8788; free 5173; podman stop banda-control-plane ;;
*) echo "usage: $0 up|e2e|clean|down" >&2; exit 2 ;;
esac
