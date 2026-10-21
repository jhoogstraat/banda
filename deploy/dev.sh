#!/usr/bin/env bash
# Local dev stack. Run through mise, which provides the tools and env:
#   mise run up     Podman machine, kind cluster, API and web (PUBLIC_HOST, default: LAN address)
#   mise run e2e    fake-agent end-to-end check on :8790 (preview :8791), next to the normal API
#   mise run down   stop the servers and the kind node
set -euo pipefail
cd "$(dirname "$0")/.."
LOG=/tmp/banda-run
mkdir -p "$LOG"

kc() { kubectl --context kind-banda "$@"; }
free() { lsof -tiTCP:"$1" -sTCP:LISTEN | xargs kill 2>/dev/null || true; }
# start <log name> <cmd...>: run detached, so it outlives this script and the harness.
start() { local name=$1; shift; nohup "$@" >"$LOG/$name.log" 2>&1 </dev/null & PID=$!; }
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
		return
	fi
	podman start banda-control-plane >/dev/null
	for _ in $(seq 60); do kc get nodes >/dev/null 2>&1 && break; sleep 2; done
	kc -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=300s
	kc -n banda rollout status statefulset/postgres --timeout=300s
	kc -n banda rollout status deployment/dex --timeout=300s
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

# One e2e at a time across all checkouts: they share the fake image, :8790 and the dev users.
E2E_LOCK=/tmp/banda-e2e.lock
lock() {
	until mkdir "$E2E_LOCK" 2>/dev/null; do
		[ -f "$E2E_LOCK/pid" ] && ! kill -0 "$(cat "$E2E_LOCK/pid")" 2>/dev/null && rm -rf "$E2E_LOCK"
		sleep 5
	done
	echo $$ >"$E2E_LOCK/pid"
	trap 'free 8790; free 8791; rm -rf "$E2E_LOCK"' EXIT
}

# Environments of earlier runs still point at the e2e API, which is gone: with the lock held,
# pause them (volumes kept) before they fill the node's pod limit.
e2e_leftovers() {
	kc -n banda get sandbox -o json | jq -r '.items[] | select(.spec.operatingMode == "Running")
		| select(any(.spec.podTemplate.spec.containers[0].env[]?; .name == "BANDA_API_URL" and (.value | endswith(":8790")))) | .metadata.name' |
		while read -r sb; do kc -n banda patch sandbox "$sb" --type merge -p '{"spec":{"operatingMode":"Suspended"}}' >/dev/null; done
}

e2e() {
	e2e_leftovers
	TARGET=fake SANDBOX_IMAGE=localhost/banda-sandbox:fake mise run image
	free 8790
	DEV_AUTH=1 SANDBOX_IMAGE=localhost/banda-sandbox:fake ADDR=:8790 PREVIEW_ADDR=:8791 RUNNER_API_URL=http://host.containers.internal:8790 start e2e-api mise run api
	until_ok e2e-api listening 8790
	API=http://localhost:8790 deploy/e2e.sh
}

case ${1:-} in
up) machine; cluster; servers ;;
e2e) lock; machine; cluster; e2e ;;
down) free 8787; free 8788; free 5173; free 8790; free 8791; podman stop banda-control-plane ;;
*) echo "usage: $0 up|e2e|down" >&2; exit 2 ;;
esac
