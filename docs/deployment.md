# Deployment strategies

Checked against the repo on 2026-10-22. Goal: run Banda first on one Mac in Podman, then on a cluster the team shares.

## What has to be deployed

| Piece | Today | Notes |
|---|---|---|
| `banda-api` (Go, serves the SPA with `WEB_DIR`) | `mise run api` on the Mac, or `deploy/banda.yaml` in kind | Creates `Sandbox` CRs, PVCs, Secrets and NetworkPolicies through the Kubernetes API. One replica (in-memory fan-out). |
| Postgres | `deploy/postgres.yaml` (StatefulSet, `banda:banda` superuser, NodePort) | Dev-grade. |
| OIDC provider | Dex (`deploy/dex.yaml`, three static users) | Any OIDC provider in production. |
| agent-sandbox controller | upstream `sandbox.yaml` v1.0.7 | A third-party operator Banda depends on. |
| Sandbox image | `deploy/sandbox.Dockerfile`, loaded into kind with `mise run image` | Must be pullable by every node. |
| NetworkPolicies | `deploy/sandbox-policy.yaml`, plus `sandboxes-allow` written by the API | Need a CNI that enforces them. |

The important fact: **Banda is Kubernetes-native by design.** The API's main job is to create and suspend pods; sandboxes, volumes, secrets and isolation are Kubernetes objects. Any strategy that removes Kubernetes removes the product.

## The options

### Docker/Podman Compose: no

Compose could run the API, Postgres and Dex as containers, but the sandboxes still need a cluster with the agent-sandbox controller, so the API would need a kubeconfig into kind anyway. That is the current dev setup with one more moving part, and it gives two ways to run the control plane to keep working. Compose doesn't run the part that matters. Skip it.

### Plain manifests + mise (today)

`deploy/*.yaml`, applied by mise tasks, with `sed` for the Dex host. Fine for kind. It breaks down once there are two environments: the image names (`localhost/...:dev`), hosts, storage class and the Postgres password differ, and `sed` doesn't scale.

### Kustomize: recommended next step

Built into `kubectl` (`kubectl apply -k`), so no new tool in `mise.toml`. One `deploy/base/` (API, RBAC, Postgres, sandbox policy) and two overlays:

- `deploy/overlays/kind/`: today's dev values (Dex, NodePorts, `localhost/` images);
- `deploy/overlays/team/`: registry images, an Ingress with TLS, no NodePorts, a generated Postgres password, the company's OIDC settings in `banda-api`.

This fits one team installing one copy: exactly the audience in [VISION.md](../VISION.md). Overlays handle the differences, and every value stays a plain manifest you can read.

### Helm chart: only when strangers install it

Helm pays off when people you don't know install Banda with values you can't predict: open-sourcing it. Until then a chart adds templating, a values schema and a release lifecycle with one user. Revisit if Banda is open-sourced; a Kustomize base converts to a chart easily.

### An operator (a `Banda` CRD): no

An operator manages the lifecycle of many instances of something (upgrades, backups, failover). Banda has one instance per team, and its dynamic part (one sandbox per thread) is already reconciled by two controllers: agent-sandbox and the API itself (the idle sweep, suspend, resume, delete). A `BandaInstance` CRD would wrap a Deployment and a StatefulSet in Go code that must then be built, versioned and tested. For Postgres, use an existing operator (CloudNativePG) or a managed database if backups and failover are ever needed; don't write one.

### GitOps (Argo CD / Flux): later, optional

Once the team cluster exists, pointing Argo CD or Flux at `deploy/overlays/team` gives reviewable, revertable deploys. It needs Kustomize (or Helm) first; it doesn't replace it.

## Recommendation

1. **Now, on the Mac:** keep `mise run up` (API and web on the Mac, everything else in kind). It works today. Then prove the in-cluster path with `mise run deploy` (see the gaps below).
2. **Before the team cluster:** move `deploy/*.yaml` into a Kustomize base with `kind` and `team` overlays; have the mise tasks apply `-k deploy/overlays/kind`.
3. **Team cluster:** one cluster dedicated to Banda (not production), the `team` overlay, a registry for both images, Ingress with TLS on `banda.<domain>` and `preview.banda.<domain>`, the company's OIDC, and a CNI that enforces NetworkPolicy (Calico or Cilium).
4. **Not doing:** Compose, an operator, Helm (until open source).

## Gaps before the in-cluster deployment works

Found while reading `deploy/` and `cmd/api`; these are also in [todo.md](../todo.md).

- **Sign-in in the cluster:** in dev Vite proxies `/dex` to Dex; the API serving the SPA doesn't, and Dex's issuer is `http://localhost:5173/dex`. `mise run deploy` therefore only works with `DEV_AUTH=1`. Fix: an issuer both the browser and the API pod reach (an Ingress, or the API proxying `/dex`), or a real OIDC provider.
- **Images:** `localhost/banda-api:dev` and `localhost/banda-sandbox:dev` with `IfNotPresent` only work in kind. Build, tag and push to a registry (CI), and set `SANDBOX_IMAGE`.
- **Ingress and TLS:** only port-forward today. Banda needs https for its `__Host-` cookies and the preview origin on a sibling subdomain (PLAN.md, open risk 6).
- **Postgres:** superuser `banda:banda`, NodePort, no backups. Needs a generated password in a Secret, a non-superuser role for the API, no NodePort, backups. Schema changes are an idempotent file, fine until a column changes.
- **Volumes:** sandbox PVCs are RWO (`internal/sandbox/sandbox.go`: "multi-node needs RWX or node pinning"). The per-user Claude volume is mounted by all of a user's pods, so on a multi-node cluster those pods must land on one node, or the volume must be RWX.
- **Pod network:** the ingress policies name kind's pod CIDR `10.244.0.0/16`; another cluster needs its own.
- **One replica:** `replicas: 1` stays until Postgres `LISTEN/NOTIFY` fan-out exists. Use `strategy: Recreate` so a rollout never runs two.
- **Sizes:** sandbox resources are fixed (4 CPU, 8 GiB, 10 GiB scratch). Plan nodes for the number of threads running at once, not threads in total (suspended threads cost only disk).
