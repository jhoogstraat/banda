# Deployment

Checked against the repo on 2026-10-24; "Install" followed word for word on a fresh kind cluster. Banda runs on one Mac in Podman (kind) for development, and on a cluster a team shares. It is open source under MIT, so strangers install it with values nobody can predict.

Settings are in [configuration.md](configuration.md).

## Install

From an empty cluster to a signed-in team. You need:

- A Kubernetes cluster with a CNI that enforces NetworkPolicy (Calico, Cilium; kind's kindnet does) and a default StorageClass (`kubectl get storageclass`). On more than one node, one whose volumes attach to any node (a cloud disk, Ceph RBD, Longhorn), with Banda's nodes in one zone: "Volumes" below.
- An ingress controller, and its IngressClass name (`kubectl get ingressclass`).
- Two hostnames pointing at the ingress controller, e.g. `banda.example.com` and `preview.banda.example.com` (the Browser tab; a sibling or subdomain of the first), and a TLS Secret covering both in the namespace `banda` (`kubectl create namespace banda` first), from cert-manager or `kubectl -n banda create secret tls banda-tls --cert=... --key=...`.
- Your cluster's pod network, e.g. `kubectl cluster-info dump | grep -m1 -o -- '--cluster-cidr=[^"]*'` (a node's `spec.podCIDR` is only its slice).
- Your organization's agreement to Anthropic's Commercial Terms: running Claude Code in hosted sandboxes requires it ([legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)). A Claude Team or Enterprise plan or an API account is under them. Each person still logs in with their own subscription or API key.
- An OIDC provider with a client for Banda and the redirect URI `https://banda.example.com/auth/callback`; or none, with the bundled Dex below.

1. The [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller, which is cluster-wide and not part of the chart:

   ```sh
   kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.7/sandbox.yaml
   ```

2. A values file, `banda.values.yaml` (every value: [values.yaml](../deploy/helm/banda/values.yaml); the API's settings: [configuration.md](configuration.md), through `api.env`):

   ```yaml
   podCIDR: 10.42.0.0/16
   ingress:
     enabled: true
     className: nginx
     host: banda.example.com
     previewHost: preview.banda.example.com
     tlsSecret: banda-tls
   oidc:
     issuer: https://login.example.com
     clientSecret: ...
     providerName: Acme # "Sign in with Acme"
   ```

   Without a provider, replace `oidc` with the bundled Dex and a user per teammate. The API serves it at `https://banda.example.com/dex`, so it needs no host of its own ("Sign-in with the bundled Dex" below). The password hash is bcrypt: `htpasswd -nbBC 10 "" <password> | cut -d: -f2`; `userID` is any unique string, e.g. from `uuidgen`.

   ```yaml
   dex:
     enabled: true
     users:
       - {email: ann@example.com, username: ann, userID: 1f0c2a52-..., hash: "$2y$10$..."}
   ```

3. The chart, released to `oci://ghcr.io/jhoogstraat/charts/banda` with the images `ghcr.io/jhoogstraat/banda-api` and `banda-sandbox` (amd64 and arm64) of the same version. The session secret, both Postgres passwords and Dex's client secret are generated on install and kept on upgrades.

   ```sh
   helm install banda oci://ghcr.io/jhoogstraat/charts/banda -n banda --create-namespace -f banda.values.yaml
   kubectl -n banda rollout status deployment/banda-api
   ```

4. Open `https://banda.example.com` and sign in. Before the first thread, each teammate connects Claude Code once (**Connect** above the composer): it opens Claude Code's login in a terminal, and the login is shared by all their threads. Then [usage.md](usage.md). Each thread's pod starts in the namespace `banda`: `kubectl -n banda get pods`.

Upgrades: `helm upgrade banda oci://ghcr.io/jhoogstraat/charts/banda -n banda -f banda.values.yaml --version <new>`.

### Without Helm

Every release has `banda.yaml`, the chart rendered as the Kustomize base renders it, and `deploy/kustomize/base` inflates the chart for Kustomize (`kubectl kustomize --enable-helm`, which runs `helm`); `deploy/kustomize/example` patches it for a real cluster. Neither can keep generated secrets, so both read their settings from a Secret you create first:

```sh
kubectl create namespace banda
kubectl -n banda create secret generic banda-api --from-literal=SESSION_SECRET=$(openssl rand -hex 32) \
  --from-literal=POSTGRES_PASSWORD=$(openssl rand -hex 16) --from-literal=POSTGRES_API_PASSWORD=$(openssl rand -hex 16) --from-literal=OIDC_ISSUER=... --from-literal=OIDC_CLIENT_ID=... --from-literal=OIDC_CLIENT_SECRET=...
kubectl -n banda apply -f https://github.com/jhoogstraat/banda/releases/download/v1.0.0/banda.yaml
```

The Secret's settings apply where the manifest sets none: it sets `PUBLIC_URL` to `http://localhost:8787` (a port-forward) and `SANDBOX_IMAGE` to the release's, so patch the first for an ingress, as the example overlay does, and add the Ingress. Apply an overlay with `kubectl kustomize --enable-helm <overlay> | kubectl -n banda apply -f -`: the `-n` matters, as Kustomize leaves what a chart inflated without a namespace, which would otherwise land in `default`. Neither has Dex: for that, an overlay inflates the chart itself with its values, as `deploy/kustomize/kind` does, with `api.existingSecret: banda-api`, `dex.enabled` and `dex.users` (the Secret then needs only `OIDC_CLIENT_SECRET` of the OIDC settings).

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

## GitHub App

Settings creates the team's GitHub App through GitHub's manifest flow and stores it in the Secret `banda-github-app` in the sandbox namespace (the release's namespace with the chart). For GitOps, or an app you already have, create that Secret yourself, with these keys:

| Key | Value |
|---|---|
| `host` | `github.com` (default) or a GitHub Enterprise Server host named in `FORGE_HOSTS` as `github` |
| `app-id` | The app's numeric id |
| `slug` | Its name in URLs (`https://<host>/apps/<slug>`) |
| `client-id`, `client-secret` | Its OAuth client, for users connecting |
| `private-key` | A private key of the app, PEM |

```sh
kubectl -n banda create secret generic banda-github-app --from-literal=app-id=123456 --from-literal=slug=banda-acme \
  --from-literal=client-id=Iv23li... --from-literal=client-secret=... --from-file=private-key=banda-acme.private-key.pem
```

Give the app the permissions Settings asks for (contents and workflows read and write, metadata read, pull requests read and write, checks and commit statuses read), the callback URL `$PUBLIC_URL/github/callback`, "Expire user authorization tokens" on, and no webhook. Banda reads the Secret within 30 seconds of a change; Settings then shows it as set up by the deployment, and nobody can replace it from Settings. Another app in the Secret (a new `app-id` or `host`) ends every connection to the old one: Banda revokes each with the old app's keys and deletes its tokens, and everyone connects again. No pod mounts it: the private key is the one long-lived credential left, and only the API holds it. Users' tokens are in `banda-forge-<hash>` Secrets beside it, likewise.

Make the app an organization's. Whoever owns it can install it anywhere they administer and, through its keys on GitHub, reach every repository it is installed on, whatever their own access. One created under a personal account moves on GitHub: the app's settings → **Advanced** → **Transfer ownership**, to an organization (the person needs to own it). The id, keys and installations stay, so the Secret doesn't change.

## GitLab application

Settings keeps each GitLab host's application in the Secret `banda-gitlab-app-<host>` (e.g. `banda-gitlab-app-gitlab.com`; for a `host:port` in `FORGE_HOSTS`, `banda-gitlab-app-<host>-<port>`) in the sandbox namespace. To make it by hand, create the application on GitLab (a group's with few Owners, or the instance's under Admin → Applications: whoever can edit it can get every connected teammate's tokens) with the Redirect URI `$PUBLIC_URL/gitlab/callback`, Confidential, and the scopes `api` and `write_repository` only, then the Secret with these keys:

| Key | Value |
|---|---|
| `application-id` | The application's ID |
| `secret` | Its secret |
| `host` | Optional: the Secret's name says it (`gitlab.com`, or a host named in `FORGE_HOSTS` as `gitlab`) |

```sh
kubectl -n banda create secret generic banda-gitlab-app-gitlab.com --from-literal=application-id=... --from-literal=secret=gloas-...
```

Banda reads it within 30 seconds of a change; Settings then shows it as set up by the deployment, without checking it (a wrong secret shows on the first connect). No pod mounts it; users' tokens of both grants are in their `banda-forge-<hash>` Secrets beside it, likewise. Settings can't replace one made by hand: change the Secret. Another `application-id` in it ends everyone's connections through the old application, each revoked with the old credentials first (Banda notices while running; connections to an application replaced while the API was down end when GitLab refuses them). A new `secret` for the same application keeps them.

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

## Rotating the identity keys

Workload identity tokens ([configuration](configuration.md#workload-identity)) are signed with the keys in the Secret `banda-identity` in the sandbox namespace, each named `<unix seconds>.pem` after when it starts signing. The API publishes every key in it and signs with the newest that is due; it reads the Secret when it starts. To rotate without breaking verifiers that cache the keys (Banda serves them cacheable for 5 minutes; some cache longer):

```sh
NS=banda                              # SANDBOX_NAMESPACE
FROM=$(( $(date +%s) + 3600 ))        # signs from an hour from now
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$FROM.pem"
kubectl -n $NS patch secret banda-identity --type merge \
  -p "{\"data\":{\"$FROM.pem\":\"$(base64 < "$FROM.pem" | tr -d '\n')\"}}"
rm "$FROM.pem"
kubectl -n $NS rollout restart deployment/banda-api   # publishes the new key at once
```

The API reads the Secret only when it starts, so the restart is what publishes the key; without it the new key never signs. Verifiers that copied the keys (`jwt_validation_pubkeys` in OpenBao/Vault) need the new one before `$FROM`: Settings shows them. From `$FROM` on the new key signs. Keep the old key for at least 20 minutes after `$FROM`: its last tokens live 15 minutes, and verifiers may cache the key set for 5. Then remove it and restart the API again:

```sh
kubectl -n $NS patch secret banda-identity --type json -p '[{"op":"remove","path":"/data/<old>.pem"}]'
kubectl -n $NS rollout restart deployment/banda-api
```

Deleting the Secret makes the API create a new key on its next start, which signs at once: tokens of the old key stop verifying and verifiers that copied the keys need the new one. Do that only if a key leaked. There is no rotation UI.

## Releases

A tag `v1.2.3` (`.github/workflows/release.yml`) builds both images on native amd64 and arm64 runners and pushes them as `ghcr.io/<owner>/banda-api` and `banda-sandbox` with tags `1.2.3`, `1.2` and `latest`, packages the chart with version and appVersion `1.2.3` to `oci://ghcr.io/<owner>/charts/banda`, and creates a GitHub release with generated notes, the chart and `banda.yaml`. CI (`.github/workflows/ci.yml`) runs the Go, web and chart checks, the code generation drift check and both image builds on every pull request and push to `main`, and the whole e2e suite on kind with Docker (about 30 minutes) on `main` only.

## Volumes

Banda creates ReadWriteOnce volumes in the default StorageClass: one per thread (`home-t-<thread>`, the checkout) and one per person (`claude-<hash>`, their Claude Code login, config and transcripts). Each person's Claude volume is mounted by all of their pods at once, threads and login environment alike, so the API asks Kubernetes to run all of one person's pods on one node (a required pod affinity on the label `banda.dev/user`). The first of them goes wherever it fits and the rest follow it. Different people's pods spread over the nodes. Nothing to set. What follows from it:

- **Storage:** with volumes that attach to any node (a cloud disk, Ceph RBD, Longhorn), everything works on any number of nodes. Cloud disks live in one zone, so keep Banda's nodes in one zone (a node pool in one zone): a pod can't use a disk in another zone. Node-local storage (local-path, kind's default, many on-prem clusters) keeps each volume on the node it was first used on. A handoff then works only if the recipient's pods run on the same node as the thread's volume, or the recipient has no Claude volume yet. Otherwise the thread's environment never starts: its pod stays `Pending` ("didn't match PersistentVolume's node affinity") and the thread stays starting. Hand it back, or have the recipient fork it instead (a fork starts on a new volume). A node lost with node-local storage takes its volumes with it: pushed work is on the forge, the rest is gone.
- **Capacity:** one person's running threads share one node. Their requests are small (50m CPU, 128 MiB each; [sizes](configuration.md)), so the ceiling is usually the node's pod limit (110 by default) or their combined real use. When the node is full, their next environment waits for room.
- **Draining a node:** with attachable storage, each person's pods come back on another node once the last of their pods on the drained node is gone. Kubernetes retries a pod waiting for its group only every 5 minutes, so expect up to 5 minutes for the first of them. With node-local storage they wait for the node to come back.
- **Forks** start on a new volume, wherever the forker's pods are. They copy nothing from the source's volume (the diff comes from Postgres).
- An RWX Claude volume (NFS, CephFS) would let one person's pods spread over nodes. Banda has no setting for it yet, since no team has needed it.

## Gaps before a team cluster

Also in [todo.md](../todo.md).

- **Postgres:** the bundled one has a non-superuser role for the API and nightly dumps on a volume in the same cluster ("Postgres" above): copy them off the cluster, or use CloudNativePG or a managed database for point-in-time recovery and failover.
- **Pod network:** the ingress policies tell sandboxes from browsers by `podCIDR` (kind's `10.244.0.0/16` by default); set your cluster's.
- **One replica:** stays until Postgres `LISTEN/NOTIFY` fan-out exists.
- **Sizes:** each thread's pod may take 4 CPU, 8 GiB and 10 GiB scratch, plus a 10 GiB volume, by default; the chart's `sandboxes` values change them ([configuration](configuration.md)). Plan nodes for the number of threads running at once, not threads in total (suspended threads cost only disk).
- **Ingress:** thread streams are long-lived; controllers cut a stream quiet for their read timeout (60 s for ingress-nginx). The API sends a keepalive on every stream every 25 s, so any timeout above that works; a long one saves reconnects. For ingress-nginx, in `ingress.annotations`:

  ```yaml
  nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
  nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
  ```

  Other controllers have their own (Traefik's `respondingTimeouts` on the entry point, a cloud load balancer's idle timeout): keep them above 25 s.
- **Monitoring:** the API serves Prometheus metrics at `:9090/metrics` (`api.metricsPort`, the `banda-api` Service's port `metrics`; no ingress routes it): `banda_threads{status}`, `banda_sandboxes{mode}` (running, suspended), `banda_runners_connected`, `banda_internal_errors_total` (each also logged as `internal error` with the call) and `banda_uptime_seconds`. Point a scrape job at it, or with the Prometheus operator set `monitoring.serviceMonitor.enabled`. Volume disk use is the kubelet's `kubelet_volume_stats_used_bytes` (`persistentvolumeclaim` label: `home-t-<thread>`, and `claude-<hash>` for each user's Claude volume), not Banda's. `monitoring.prometheusRule.enabled` adds two alerts: `BandaInternalErrors` when `banda_internal_errors_total` rose in the last 10 minutes, and `BandaVolumeFull` when a thread's, a user's Claude or Postgres's volume is fuller than `volumeFull` (0.9) for 15 minutes, from the kubelet's numbers. Both resources take `labels` for the Prometheus that should select them (kube-prometheus-stack: `release: <its release>`). Logs go to stdout.
