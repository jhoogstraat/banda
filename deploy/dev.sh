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

C=${CONTAINER:-podman}

# Podman on macOS runs one VM at a time, so Banda shares the default machine with other projects.
machine() {
	[ "$C" = podman ] && [ "$(uname)" = Darwin ] || return 0
	[ "$(podman machine inspect podman-machine-default --format '{{.State}}')" = running ] ||
		podman machine start podman-machine-default
}

# chart [<helm args>]: the chart (deploy/helm/banda) in kind's namespace banda with the kind values.
# Values set before (api.enabled, publicUrl) stay unless given again. --take-ownership and
# --force-conflicts adopt what kubectl applied before the chart existed.
chart() {
	helm --kube-context kind-banda upgrade --install banda deploy/helm/banda -n banda --create-namespace --take-ownership --force-conflicts \
		--reset-then-reuse-values -f deploy/kustomize/kind/values.yaml --wait --timeout 5m "$@"
}

# policies <namespace>: the sandbox policies of the chart, for a namespace with sandboxes but no release.
policies() {
	helm template banda deploy/helm/banda -n "$1" --show-only templates/policies.yaml | kc -n "$1" apply -f - >/dev/null
}

# The host as sandboxes see it, for an API running here: Podman's host.containers.internal, or with
# Docker on Linux the gateway of kind's network, the node's default route (`docker network inspect kind`
# may list its subnets without a Gateway). HOST_IP overrides it (Docker Desktop).
host_ip() {
	if [ -n "${HOST_IP:-}" ]; then echo "$HOST_IP"
	elif [ "$C" = podman ]; then echo 192.168.127.254
	else $C exec banda-control-plane ip -4 route show default | awk '{print $3; exit}'
	fi
}

cluster() {
	local clusters
	# A Podman that doesn't answer is no missing cluster: `mise run cluster` would apply this checkout's controller to it.
	clusters=$(kind get clusters 2>/dev/null) || { echo "kind can't reach the cluster's container engine" >&2; exit 1; }
	if ! grep -qx banda <<<"$clusters"; then
		mise run cluster
		# E2E_IMAGE (CI): the e2e suite brings its image, it needs no dev image.
		[ -n "${E2E_IMAGE:-}" ] || mise run image
	else
		$C start banda-control-plane >/dev/null
		for _ in $(seq 60); do kc get nodes >/dev/null 2>&1 && break; sleep 2; done
		kc -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=300s
		# Clusters made before the chart (`mise run up` upgrades it; parallel e2e runs mustn't race on it).
		helm --kube-context kind-banda status banda -n banda >/dev/null 2>&1 || chart --set api.enabled=false >/dev/null
		kc -n banda rollout status statefulset/postgres --timeout=300s
		kc -n banda rollout status deployment/dex --timeout=300s
	fi
	coredns
	host_api banda 8700 8999 # mise run api, and other checkouts' on ports of their own
}

# Pods here have no IPv6, so CoreDNS answers AAAA itself. Upstream it can hang: gvproxy, Podman's DNS on a
# Mac, can fail every AAAA after a change of network until the VM restarts, and each name a pod looks up then
# takes seconds per search domain. CoreDNS reloads its config within a minute.
coredns() {
	local corefile
	corefile=$(kc -n kube-system get configmap coredns -o 'jsonpath={.data.Corefile}')
	grep -q 'template IN AAAA' <<<"$corefile" && return
	corefile=$(sed 's/^\( *\)forward \. /\1template IN AAAA . {\n\1    rcode NOERROR\n\1}\n&/' <<<"$corefile")
	kc -n kube-system create configmap coredns --from-literal=Corefile="$corefile" --dry-run=client -o yaml | kc apply -f -
}

# host_api <namespace> <port> [<end port>]: lets the namespace's sandboxes reach an API on this machine,
# at host_ip, on that port only: Postgres, the kind API server and everything else on the host stay out
# of reach (the chart's policies).
host_api() {
	kc -n "$1" apply -f - >/dev/null <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: host-api-$2}
spec:
  podSelector: {matchExpressions: [{key: banda.dev/sandbox, operator: Exists}]}
  policyTypes: [Egress]
  egress: [{to: [{ipBlock: {cidr: $(host_ip)/32}}], ports: [{port: $2${3:+, endPort: $3}}]}]
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
# a git server and Dex), database banda_e2e_<run>, images localhost/banda-sandbox:fake-<run> and
# localhost/banda-login:fake-<run> (the login environments') and two APIs
# on free ports (dev auth, and OIDC for the sign-in tests), serving the web build. <run> comes from the
# checkout path; a rerun wipes its world first. E2E_KEEP=1 keeps the namespace and database.
# E2E_IMAGE and E2E_LOGIN_IMAGE name fake images built already (CI builds them from the images job's layer cache).
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
	export SANDBOX_IMAGE=${E2E_IMAGE:-localhost/banda-sandbox:fake-$run}
	export SANDBOX_LOGIN_IMAGE=${E2E_LOGIN_IMAGE:-localhost/banda-login:fake-$run}
	{ TARGET=fake mise run image &&
		SANDBOX_IMAGE=$SANDBOX_LOGIN_IMAGE E2E_IMAGE=${E2E_LOGIN_IMAGE:-} TARGET=login-fake mise run image; } >"$LOG/image.log" 2>&1 & local image=$!
	go build -o "$LOG/api" ./cmd/api & local api=$!
	pnpm -C web build >"$LOG/web.log" 2>&1 & local web=$!
	pnpm -C web exec playwright install chromium >/dev/null & local chromium=$!
	e2e_wipe
	# The APIs connect as banda_api, as the chart's does: owner of its databases, not a superuser.
	pg -c "DO \$\$ BEGIN CREATE ROLE banda_api LOGIN PASSWORD 'banda'; EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END \$\$" \
		-c "DROP DATABASE IF EXISTS $db WITH (FORCE)" -c "CREATE DATABASE $db OWNER banda_api"
	kc create ns "$ns" >/dev/null
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
	read -r p pp op opp dp < <(node web/e2e/ports.ts 5)
	# The chart's sandbox policies and Dex, which the OIDC API serves at /dex as it would in a cluster.
	helm template banda deploy/helm/banda -n "$ns" -f deploy/kustomize/kind/values.yaml --set api.enabled=false,postgres.enabled=false \
		--set dex.nodePort=null,publicUrl=http://localhost:$op | kc -n "$ns" apply -f - >/dev/null
	wait $image || { tail -30 "$LOG/image.log"; exit 1; }
	# The fixture repos at git://e2e-git/<name>.git; readonly refuses pushes.
	kc -n "$ns" run e2e-git --image="$SANDBOX_IMAGE" --image-pull-policy=Never --port=9418 --expose --command -- sh -c \
		'cp -r /opt/e2e /tmp/git && cp -r /tmp/git/plain.git /tmp/git/readonly.git &&
		for r in plain setup-fails setup-bg setup-hold profiles; do git --git-dir=/tmp/git/$r.git config daemon.receivepack true; done &&
		exec git daemon --reuseaddr --base-path=/tmp/git --export-all /tmp/git' >/dev/null
	kc -n "$ns" rollout status deployment/dex --timeout=300s >/dev/null
	start dex kubectl --context kind-banda -n "$ns" port-forward svc/dex "$dp:5556"; PIDS+=("$PID")
	until_ok dex curl -sf "http://localhost:$dp/dex/.well-known/openid-configuration"
	wait $api
	wait $web || { tail -30 "$LOG/web.log"; exit 1; }
	export DATABASE_URL=postgres://banda_api:banda@localhost:5432/$db SANDBOX_NAMESPACE=$ns WEB_DIR=web/build DELETE_SETTLED_AFTER=20s IDLE_SWEEP_EVERY=2s
	# Every thread on the plain repo writes hello1.txt: overlaps would tie all tests together. Their test turns them on.
	export OVERLAP_WINDOW=0
	host_api "$ns" "$p"; host_api "$ns" "$op"
	export E2E_HOST; E2E_HOST=$(host_ip)
	DEV_AUTH=1 ADDR=:$p PREVIEW_ADDR=:$pp RUNNER_API_URL=http://$E2E_HOST:$p start api "$LOG/api"; PIDS+=("$PID")
	local api_pid=$PID
	# It shares the database but leaves sweeps to the main API, whose watchers see what they change.
	IDLE_SWEEP_EVERY=24h OIDC_ISSUER=http://localhost:$op/dex OIDC_PROVIDER_NAME=Dex DEX_URL=http://localhost:$dp OIDC_CLIENT_ID=banda OIDC_CLIENT_SECRET=banda-dev-secret \
		PUBLIC_URL=http://localhost:$op ADDR=:$op PREVIEW_ADDR=:$opp RUNNER_API_URL=http://$E2E_HOST:$op start oidc-api "$LOG/api"; PIDS+=("$PID")
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
		e2e_wipe || true
		pg -c "DROP DATABASE IF EXISTS $db WITH (FORCE)" || true
	fi
	rm -f "$LOG/pid"
}
# The run's namespace and those of its tests' own APIs (startApi in web/e2e/fixtures.ts).
e2e_wipe() {
	kc delete ns "$ns" --ignore-not-found --timeout=300s >/dev/null
	kc delete ns -l "banda.dev/e2e-run=$ns" --timeout=300s >/dev/null
}
pg() { kc -n banda exec postgres-0 -- psql -U banda -q -v ON_ERROR_STOP=1 "$@"; }

# Dev only: every UI check leaves sandboxes behind. Deletes the thread sandboxes (their
# volumes go with them) older than CLEAN_HOURS whose thread wasn't touched in that time or is gone.
clean() {
	local h=${CLEAN_HOURS:-24} keep
	keep=$(kc -n banda exec postgres-0 -- psql -U banda -tAc "select 't-' || id from threads where updated_at > now() - interval '$h hours' or repo_url = ''")
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
policies) shift; policies "$1" ;; # the e2e harness, for a namespace of a test's own
chart) shift; chart "$@" ;; # mise run dex, deploy
down) free 8787; free 8788; free 5173; $C stop banda-control-plane ;;
*) echo "usage: $0 up|e2e|clean|down|chart" >&2; exit 2 ;;
esac
