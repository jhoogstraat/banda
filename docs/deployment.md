# Deployment

Checked against the repo on 2026-10-22. Banda runs on one Mac in Podman (kind) for development, and on a cluster a team shares. It is open source under MIT, so strangers install it with values nobody can predict.

Settings are in [configuration.md](configuration.md).

## Install

Banda needs a Kubernetes cluster with a CNI that enforces NetworkPolicy (Calico, Cilium; kind's kindnet does) and the [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller, which is cluster-wide and not part of the chart:

```sh
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.7/sandbox.yaml
```

Everything else is the Helm chart `deploy/helm/banda`, released to `oci://ghcr.io/jhoogstraat/charts/banda` with images `ghcr.io/jhoogstraat/banda-api` and `banda-sandbox` (amd64 and arm64) of the same version. Its [values](../deploy/helm/banda/values.yaml) are the settings above: `publicUrl`, `ingress.*` (Banda and the preview on sibling hosts, with TLS), `oidc.*` or `dex.*`, `postgres.*` (bundled, or `postgres.enabled=false` and your own `DATABASE_URL`), `podCIDR` (your pod network, for the ingress policies), `egressAllow`, and `api.env` for the rest.

```sh
helm install banda oci://ghcr.io/jhoogstraat/charts/banda -n banda --create-namespace \
  --set ingress.enabled=true,ingress.host=banda.example.com,ingress.previewHost=preview.banda.example.com,ingress.tlsSecret=banda-tls \
  --set oidc.issuer=https://login.example.com,oidc.clientSecret=... --set podCIDR=10.42.0.0/16
```

Register `$PUBLIC_URL/auth/callback` with your provider. To try it without one, `--set dex.enabled=true` with `dex.users` (see `deploy/kustomize/kind/values.yaml`): the API serves Dex at `$PUBLIC_URL/dex`, so it needs no host of its own (see "Sign-in with the bundled Dex" below). The session secret, both Postgres passwords and the Dex client secret are generated on install and kept on upgrades.

Without Helm: every release has `banda.yaml`, the chart rendered as the Kustomize base renders it, and `deploy/kustomize/base` inflates the chart for Kustomize (`kubectl kustomize --enable-helm`, which runs `helm`); `deploy/kustomize/example` patches it for a real cluster. Neither can keep generated secrets, so both read their settings from a Secret you create first:

```sh
kubectl create namespace banda
kubectl -n banda create secret generic banda-api --from-literal=SESSION_SECRET=$(openssl rand -hex 32) \
  --from-literal=POSTGRES_PASSWORD=$(openssl rand -hex 16) --from-literal=POSTGRES_API_PASSWORD=$(openssl rand -hex 16) --from-literal=OIDC_ISSUER=... --from-literal=OIDC_CLIENT_ID=... --from-literal=OIDC_CLIENT_SECRET=...
kubectl -n banda apply -f https://github.com/jhoogstraat/banda/releases/download/v1.0.0/banda.yaml
```

The Secret's settings apply where the manifest sets none: it sets `PUBLIC_URL` to `http://localhost:8787` (a port-forward) and `SANDBOX_IMAGE` to the release's, so patch the first for an ingress, as the example overlay does, and add the Ingress.

### In kind

`mise run deploy` builds the API image (with the web build), loads it into kind and installs the chart with the API and Dex (`deploy/kustomize/kind/values.yaml`, also `kubectl kustomize --enable-helm deploy/kustomize/kind`). Reach it with `kubectl --context kind-banda -n banda port-forward svc/banda-api 8787 8788` and sign in at `http://localhost:8787` as alice. Run either `mise run api` or `mise run deploy`, not both: they share the database and the sandboxes. `mise run up` removes the in-cluster API again and points Dex back at the web server. `mise run chart-check` lints the chart and renders it with every values file and overlay.

## What has to be deployed

| Piece | How | Notes |
|---|---|---|
| `banda-api` (Go, serves the SPA with `WEB_DIR`) | The chart; in dev `mise run api` on the Mac instead | Creates `Sandbox` CRs, PVCs, Secrets and NetworkPolicies through the Kubernetes API. One replica (in-memory fan-out), `strategy: Recreate`. |
| Postgres | The chart (`postgres.enabled`, default), or your own database through `DATABASE_URL` | The API connects as `banda_api`, not a superuser; nightly dumps to a volume. See "Postgres" below. |
| OIDC provider | Your provider (`oidc.*`), or the chart's Dex (`dex.enabled`) with static users for demos and dev | |
| agent-sandbox controller | Upstream `sandbox.yaml` v1.0.7, applied once by hand | A third-party operator Banda depends on; not in the chart. |
| Sandbox image | `ghcr.io/<owner>/banda-sandbox`, released for amd64 and arm64; in kind `mise run image` | Must be pullable by every node. |
| NetworkPolicies | The chart, plus `sandboxes-allow` written by the API | Need a CNI that enforces them. |

The important fact: **Banda is Kubernetes-native by design.** The API's main job is to create and suspend pods; sandboxes, volumes, secrets and isolation are Kubernetes objects. Any strategy that removes Kubernetes removes the product.

## Chosen: one Helm chart, Kustomize on top

`deploy/helm/banda` is the single source of every manifest. Everything else renders it:

- **Helm** for people installing a release: `helm install banda oci://ghcr.io/jhoogstraat/charts/banda`, values for everything that differs between clusters (images, hosts, OIDC, the pod network, bundled or external Postgres and Dex).
- **Kustomize** for people who prefer patching over values: `deploy/kustomize/base` inflates the chart (`helmCharts:`, built with `kubectl kustomize --enable-helm`, which runs `helm`), overlays patch what comes out. `example` shows a real cluster (images, an Ingress with TLS, the settings Secret, the pod network); `kind` is the dev install. Kustomize can't change the values of a chart a base inflates, so `kind` inflates the chart itself with its values.
- **A rendered manifest** on every GitHub release (`banda.yaml`, what the base renders) for `kubectl apply` without either tool.
- **The dev loop** installs the same chart: `mise run dex` (Postgres, Dex, policies; no API), `mise run deploy` (with the API), and the e2e world renders its Dex and policies from it. The kind values live in `deploy/kustomize/kind/values.yaml`, shared by the overlay and the tasks.

Generated secrets (session secret, Postgres passwords, Dex client secret) survive `helm upgrade` through `lookup`; `helm template` and Kustomize can't look anything up, so the base and the release manifest take every secret from a Secret `banda-api` you create.

### Not chosen

- **Docker/Podman Compose:** the sandboxes still need a cluster with the agent-sandbox controller, so the API would need a kubeconfig into one anyway. Compose doesn't run the part that matters.
- **Plain manifests:** what `deploy/*.yaml` were until the chart. Fine for kind, but image names, hosts, storage, the pod network and passwords differ per cluster, and `sed` doesn't scale.
- **Kustomize alone:** enough for one team, but an open-source install needs values a stranger can set without learning the manifests. Helm gives them; Kustomize stays available on top.
- **An operator (a `Banda` CRD):** Banda has one instance per team, and its dynamic part (one sandbox per thread) is already reconciled by agent-sandbox and the API. For Postgres use CloudNativePG or a managed database if backups and failover are needed; don't write one.
- **The controller in the chart, or a pre-install hook applying it:** it's cluster-wide (CRDs, its own namespace) and shared with anything else using agent-sandbox; a hook would need cluster-admin from a release that otherwise needs a namespace. One documented `kubectl apply` is simpler and honest.
- **GitOps (Argo CD, Flux):** optional on top; both take a chart or a Kustomize overlay as they are.

## Postgres

The bundled Postgres (`postgres.enabled`, default) is one StatefulSet with two roles:

- **`banda`**, the superuser. Its password (`POSTGRES_PASSWORD`) is in the Secret `banda-postgres` (with `api.existingSecret`: in that Secret), read by Postgres on a new volume and by the API pod's init container. You reach it with `kubectl -n banda exec postgres-0 -- psql -U banda` (no password over the pod's socket).
- **`banda_api`**, what the API connects as (`POSTGRES_API_PASSWORD` in `banda-api`). It owns the database `banda` and so runs the migrations, but isn't a superuser and can't create roles or databases. The API never gets the superuser's password, except through `api.existingSecret`, whose keys all reach its environment.

The API pod's init container `postgres-role` makes `banda_api` as the superuser on every start: it creates the role if missing, sets its password from the Secret and hands it the database `banda` and the tables and functions in it that anyone else owns. So an install from before the role upgrades with `helm upgrade` and nothing else: the superuser's password moves from `banda-api` to `banda-postgres` as it was, Postgres restarts once, the API starts as `banda_api` on the same data. With `api.existingSecret`, add `POSTGRES_API_PASSWORD` to that Secret first. A database of your own (`postgres.enabled=false`) is untouched: the API connects with your `DATABASE_URL`, and should own its database, not be a superuser.

### Backups

`postgres.backup` (on by default): the CronJob `postgres-backup` dumps the database `banda` (`pg_dump -Fc`, as `banda_api`) at `schedule` (default 03:00 UTC daily) to the volume `postgres-backup` (`storage`, 5Gi) and keeps the newest `keep` (7). Helm keeps that volume on uninstall. Postgres mounts it read-only at `/backup`, which also places the backups on Postgres's node (the volume is ReadWriteOnce). A dump now: `kubectl -n banda create job --from=cronjob/postgres-backup backup-now`.

The volume sits on the same cluster: copy dumps somewhere else for a real disaster (`kubectl -n banda cp postgres-0:/backup/<dump> <dump>`). The step up, with point-in-time recovery and failover, is [CloudNativePG](https://cloudnative-pg.io) or a managed database through `postgres.enabled=false` and `DATABASE_URL`.

Restore into a scratch database, check it, then swap it in, keeping the old one:

```sh
kubectl -n banda exec postgres-0 -- ls -t /backup   # newest first
kubectl -n banda exec postgres-0 -- psql -U banda -c "CREATE DATABASE banda_restore OWNER banda_api"
kubectl -n banda exec postgres-0 -- pg_restore -U banda -d banda_restore --no-owner --role=banda_api --exit-on-error /backup/banda-<time>.dump
kubectl -n banda exec postgres-0 -- psql -U banda -d banda_restore -c "select count(*) from threads"
kubectl -n banda scale deployment/banda-api --replicas 0
kubectl -n banda exec postgres-0 -- psql -U banda -d postgres -c "ALTER DATABASE banda RENAME TO banda_old" -c "ALTER DATABASE banda_restore RENAME TO banda"
kubectl -n banda scale deployment/banda-api --replicas 1
```

Drop `banda_old` once the restored one is right. A dump copied in from elsewhere goes through `kubectl -n banda exec -i postgres-0 -- pg_restore ... < <dump>` instead (pg_restore reads standard input).

## Sign-in with the bundled Dex

In dev, Vite proxies `/dex` to Dex, whose issuer is `http://<host>:5173/dex`: browsers and the API on the Mac reach the same URL. In the cluster the API serves the SPA itself, so nothing served `/dex`, and the API pod can't reach a browser-facing URL such as `http://localhost:8787/dex` (a port-forward) or, behind an ingress, may not reach its own public host.

Options:

1. **The API proxies `/dex/` to Dex (`DEX_URL`), and sends its own provider requests for the issuer's origin straight to `DEX_URL`.** One public origin, as in dev; no ingress rule; works behind a port-forward. Chosen.
2. **An ingress route for `/dex` to Dex.** Needs an ingress (not in kind or behind a port-forward), and the API still has to reach the public issuer from inside, hairpinning through the load balancer, which many clusters don't allow.
3. **A split discovery URL** (`oidc.InsecureIssuerURLContext`: discover at the internal URL, accept the public issuer). Discovery then hands out the public token and keys endpoints, so the API still has to reach the public URL for the token exchange and keys.

With `dex.enabled` the chart sets the issuer to `$PUBLIC_URL/dex`, `DEX_URL` to the Dex service and Dex's redirect URI to `$PUBLIC_URL/auth/callback`. Requests to the issuer's origin are only the provider's, as only the OIDC client uses that transport, so the API matches on origin alone. Real OIDC providers need none of this. The e2e world signs in the same way: its OIDC API serves the run's Dex at `/dex`.

## Releases

A tag `v1.2.3` (`.github/workflows/release.yml`) builds both images on native amd64 and arm64 runners and pushes them as `ghcr.io/<owner>/banda-api` and `banda-sandbox` with tags `1.2.3`, `1.2` and `latest`, packages the chart with version and appVersion `1.2.3` to `oci://ghcr.io/<owner>/charts/banda`, and creates a GitHub release with generated notes, the chart and `banda.yaml`. CI (`.github/workflows/ci.yml`) runs the Go, web and chart checks, the code generation drift check and both image builds on every pull request; its e2e job on kind with Docker isn't proven on Actions yet and can't fail the build.

## Gaps before a team cluster

Also in [todo.md](../todo.md).

- **Postgres:** the bundled one has a non-superuser role for the API and nightly dumps on a volume in the same cluster ("Postgres" above): copy them off the cluster, or use CloudNativePG or a managed database for point-in-time recovery and failover.
- **Volumes:** sandbox PVCs are RWO (`internal/sandbox/sandbox.go`: "multi-node needs RWX or node pinning"). The per-user Claude volume is mounted by all of a user's pods, so on a multi-node cluster those pods must land on one node, or the volume must be RWX.
- **Pod network:** the ingress policies tell sandboxes from browsers by `podCIDR` (kind's `10.244.0.0/16` by default); set your cluster's.
- **One replica:** stays until Postgres `LISTEN/NOTIFY` fan-out exists.
- **Sizes:** each thread's pod may take 4 CPU, 8 GiB and 10 GiB scratch, plus a 10 GiB volume, by default; the chart's `sandboxes` values change them ([configuration](configuration.md)). Plan nodes for the number of threads running at once, not threads in total (suspended threads cost only disk).
- **Ingress:** thread streams are long-lived; controllers cut a stream quiet for their read timeout (60 s for ingress-nginx). The API sends a keepalive on every stream every 25 s, so any timeout above that works; a long one saves reconnects. For ingress-nginx, in `ingress.annotations`:

  ```yaml
  nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
  nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
  ```

  Other controllers have their own (Traefik's `respondingTimeouts` on the entry point, a cloud load balancer's idle timeout): keep them above 25 s.
- **Monitoring:** the API serves Prometheus metrics at `:9090/metrics` (`api.metricsPort`, the `banda-api` Service's port `metrics`; no ingress routes it): `banda_threads{status}`, `banda_sandboxes{mode}` (running, suspended), `banda_runners_connected`, `banda_internal_errors_total` (each also logged as `internal error` with the call) and `banda_uptime_seconds`. Point a scrape job or ServiceMonitor at it. Volume disk use is the kubelet's `kubelet_volume_stats_used_bytes` (`persistentvolumeclaim` label: `home-t-<thread>`, and `claude-<hash>` for each user's Claude volume). Logs go to stdout.
