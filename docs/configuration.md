# Configuration

The API reads its settings from environment variables; in the cluster the chart sets them from its [values](../deploy/helm/banda/values.yaml) (`api.env` for anything without a value of its own). Deploying is in [deployment.md](deployment.md).

## Sign in

The cluster runs [Dex](https://dexidp.io), a local OIDC provider, reached through the web server at `/dex` (in the cluster, through the API: see [deployment.md](deployment.md)). Sign in as `alice`, `bob` or `carol` (`@example.com`) with the password `password`. `DEV_AUTH=1 mise run api` skips sign-in and trusts the `X-Banda-User` header instead (default user `dev`).

To use another OIDC provider, set these before `mise run api` (or when running `cmd/api`) and register `$PUBLIC_URL/auth/callback` as its redirect URI:

| Variable | Meaning |
|---|---|
| `OIDC_ISSUER` | Issuer URL, which serves `/.well-known/openid-configuration`. Enables sign-in. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Client credentials. |
| `PUBLIC_URL` | Origin the browser uses, default `http://localhost:5173`. |
| `SESSION_SECRET` | Signs the session cookie. If empty, a random one is used and sessions end when the API restarts. |

Without `OIDC_ISSUER` the API only starts with `DEV_AUTH=1`. Other API settings: `DATABASE_URL`, `SANDBOX_NAMESPACE`, `SANDBOX_IMAGE`, `RUNNER_API_URL`, `IDLE_SUSPEND_AFTER` (default `30m`, checked every `IDLE_SWEEP_EVERY`, default `1m`), `LOGIN_IDLE_AFTER` (default `10m`: login environments without a terminal), `DELETE_SETTLED_AFTER` (default `168h`: settled environments whose work is pushed), `MAX_SUBTHREAD_DEPTH` (default `3`) and `MAX_AGENT_THREADS` (default `8`: agents' sub-threads, see [PLAN.md](../PLAN.md)), `OVERLAP_WINDOW` (default `168h`: how recently two threads must have changed for shared files to count as an overlap; `0` turns overlaps off), `SANDBOX_EGRESS_ALLOW` (comma-separated CIDRs sandboxes may reach besides the internet, such as production; see Sandbox isolation), `DEX_URL` (a bundled Dex, e.g. `http://dex.banda.svc:5556`, whose issuer is `$PUBLIC_URL/dex`: the API serves it at `/dex/` and reaches it there directly; the chart sets it), `FORGE_HOSTS` and `PULL_REQUEST_POLL` (see [usage.md](usage.md#github-and-gitlab)), `TEST_SUSPEND_PAUSE` (tests only: holds every suspend between the thread's status and its pod), `TEST_FORGE_URL` (tests only: a fake forge API in place of every forge's), `ADDR` (default `:8787`), `WEB_DIR` (serve a web build), `WATCH_BUFFER` (default `256`: events a thread stream may lag behind before it catches up from the database; small for tests).

The Browser tab shows apps running in a thread's environment from a second origin: `PREVIEW_ADDR` (default `:8788`) is where the API serves it, `PREVIEW_URL` the URL browsers use for it, by default the host they reach Banda on with that port. Keep it on the same site as Banda (another port, or a sibling subdomain such as `preview.banda.example.com`): the preview signs in with a SameSite cookie. Previewed apps run code from the sandboxes on that site; outside a trusted LAN serve Banda over https (its cookies are then `__Host-`) with the preview on a sibling subdomain.

## Sandbox isolation

Sandboxes run the repo's code and the agent, so NetworkPolicies let them reach only DNS, the internet and the API (`deploy/helm/banda/templates/policies.yaml`), and Postgres and Dex take connections only from the API and from outside the cluster (their templates); the chart installs them, in dev through `mise run up`. An API on this machine (`mise run api`) must listen on a port in 8700–8999 for sandboxes in `banda` to reach it.

Private networks, such as production or a private git host, are closed too until `SANDBOX_EGRESS_ALLOW` lists them: comma-separated CIDRs (`10.20.0.0/16,10.30.1.5/32`; NetworkPolicies can't name hosts). The API applies them when it starts, as the NetworkPolicy `sandboxes-allow` in `SANDBOX_NAMESPACE` (set it in the Secret `banda-api` in the cluster); empty removes it, and a bad CIDR stops the API with an error. Postgres, Dex and the API in the cluster (except on its runner port) still refuse sandboxes inside an allowed range, and the Kubernetes API and kubelets refuse them without a token. Everything else in the range is open, so allow only what agents need: a range over the cluster's own networks opens its other pods and NodePorts, and in dev one over `host.containers.internal` (192.168.127.254) the host's ports, Postgres's among them. For a production Kubernetes cluster, keep it separate from Banda's: put a kubeconfig for it in a secret and allow its API server's address if that is private. Don't give sandboxes a service account in Banda's cluster.

For a shared deployment install the chart ([deployment.md](deployment.md)): no NodePorts, a generated Postgres password, never `api.devAuth`, a CNI that enforces NetworkPolicy, and your cluster's pod network in `podCIDR` instead of kind's (`10.244.0.0/16`). The API still connects as Postgres's superuser. See [PLAN.md](../PLAN.md), open risk 9.
