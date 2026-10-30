# Sandbox environment lifecycle: research

Checked 2026-10-19. Sources are primary (repos at the tags named, official docs, KEPs) unless marked otherwise. Line links point at `kubernetes-sigs/agent-sandbox` tag `v1.0.7`. Banda now pins v1.0.8.

## TL;DR

- **agent-sandbox v1.0.7** (SIG Apps, all CRDs `v1beta1`) does what Banda needs for v1: one `Sandbox` per thread, a PVC per sandbox via `volumeClaimTemplates`, **suspend/resume** through `spec.operatingMode: Running | Suspended`, and timed teardown through `spec.shutdownTime` + `spec.shutdownPolicy`. Suspend **deletes the Pod and keeps the PVC**, so memory and processes are lost and disk survives. Resume creates a new Pod from the *current* `podTemplate` and reattaches the PVC.
- **No upstream idle auto-suspend and no upstream memory snapshot.** Idle policy is still open ([#849](https://github.com/kubernetes-sigs/agent-sandbox/issues/849), [#968](https://github.com/kubernetes-sigs/agent-sandbox/issues/968)). Memory snapshots exist only as **GKE Pod Snapshots** (GKE with gVisor and GCS) or in **Agent Substrate** (pre-1.0). Banda drives suspend from its own state machine.
- **"Agent substrate" is real**: [agent-substrate/substrate](https://github.com/agent-substrate/substrate), led by Google with Solo.io, v0.5.0 (2026-10-14), pre-1.0. It does true memory+disk suspend/resume with a claimed resume time under 500 ms. It is too young and too heavy for Banda v1, but worth watching; see [agent-substrate.md](agent-substrate.md).
- **kagent** now (v1.0.0-alpha) runs on Agent Substrate and even has a Claude Code harness. It only supports API-key/Bedrock/Vertex credentials injected by its gateway, and its model is declarative agents, sessions and the A2A protocol. **It is a poor fit for Banda**, but its Claude harness is a useful reference.
- **What Banda took**: core `Sandbox` only (no templates, claims or warm pools), suspending after 30 minutes idle and on owner snooze or settle, resuming with `claude --resume`, and deleting after a grace period through `shutdownTime`. The login is on a volume of its own, not in the thread's ([claude-login.md](claude-login.md)).

---

## 1. kubernetes-sigs/agent-sandbox

### Version and maturity

- Latest release: **v1.0.7** (2026-10-15). Releases ship weekly. [v1.0.0](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0) (2026-08-28) removed `v1alpha1` and the conversion webhooks, so all four CRDs serve only `v1beta1`. Upgrading straight from a version below 0.5 is not supported.
- Governed by SIG Apps ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/README.md)). GKE Agent Sandbox (GA, [Google blog 2026-05-21](https://cloud.google.com/blog/products/containers-kubernetes/bringing-you-agent-sandbox-on-gke-and-agent-substrate)) is built on it.
- Go module `sigs.k8s.io/agent-sandbox` needs Go 1.26 or later and depends on `k8s.io/*` v0.37.

### CRDs

| Kind | apiVersion | Lifecycle-relevant fields |
|---|---|---|
| `Sandbox` | `agents.x-k8s.io/v1beta1` | `spec.podTemplate` (`spec` + labels/annotations), `spec.volumeClaimTemplates[]` (immutable), `spec.service` (`*bool`, headless Service, off by default), `spec.operatingMode` (`Running` by default, or `Suspended`), `spec.shutdownTime` (absolute RFC 3339), `spec.shutdownPolicy` (`Retain` by default, or `Delete`). [types](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/api/v1beta1/sandbox_types.go) |
| `SandboxTemplate` | `extensions.agents.x-k8s.io/v1beta1` | Same blueprint (`podTemplate`, `volumeClaimTemplates`, `service`), plus `networkPolicy` / `networkPolicyManagement` (Managed by default, which applies a shared policy: ingress only from the sandbox-router, egress to the public internet only), `envVarsInjectionPolicy`, `volumeClaimTemplatesPolicy`. No `operatingMode` or lifecycle fields. [types](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/extensions/api/v1beta1/sandboxtemplate_types.go) |
| `SandboxWarmPool` | `extensions.agents.x-k8s.io/v1beta1` | `replicas` (supports the `/scale` subresource, so HPA/KEDA can drive it, including scale to zero), `sandboxTemplateRef`, `updateStrategy` (`OnReplenish` by default, or `Recreate`). [types](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/extensions/api/v1beta1/sandboxwarmpool_types.go) |
| `SandboxClaim` | `extensions.agents.x-k8s.io/v1beta1` | `warmPoolRef` (**required**), `lifecycle.shutdownTime`, `lifecycle.ttlSecondsAfterFinished`, `lifecycle.shutdownPolicy` (`Delete` / `DeleteForeground` / `Retain`), `env`, `volumeClaimTemplates`, `additionalPodMetadata`. No `operatingMode`. [types](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/extensions/api/v1beta1/sandboxclaim_types.go) |

No newer kinds exist. The `replicas: 0|1` field on `Sandbox` from alpha was replaced by `operatingMode` in [KEP-694](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/docs/keps/694-kep-for-suspend-and-resume-for-beta/README.md), because HPA and KEDA treat `replicas: 0` as a clean delete.

**Status** ([types L351](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/api/v1beta1/sandbox_types.go#L351)): `podIPs`, `nodeName` (both cleared while suspended), `service`, `serviceFQDN`, `selector`, and conditions:
- `Ready`: True with reason `DependenciesReady`. When False, the reason is one of `DependenciesNotReady`, `SandboxSuspended`, `PodSucceeded`/`PodFailed`, `SandboxExpired`, `ReconcilerError`, `InvalidConfiguration` or `MultiplePods`.
- `Suspended`: `PodTerminated` (True), `PodTerminating`, `NotSuspended`, `PodNotOwned` or `PodStateUnknown`.
- `Finished`: `PodSucceeded` or `PodFailed`.
- `PodScheduled`: mirrored from the Pod.

**Identity**: the Pod name equals the Sandbox name ([v1.0.0 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0)), so the hostname is stable. A headless Service only exists with `spec.service: true`, and then the name must be at most 63 characters.

### How suspend and resume actually work

From the controller source:
- `operatingMode: Suspended` makes the controller **delete the owned Pod** ([sandbox_controller.go L1334](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/controllers/sandbox_controller.go#L1334)). The Sandbox object, its PVCs and its Service stay. `Suspended=True/PodTerminated` appears once the Pod is gone.
- `operatingMode: Running` with no Pod makes the controller create one **from the current `spec.podTemplate`** and mount the same PVCs. Edits to `podTemplate` other than labels and annotations take effect only when a new Pod is created ([PR #1976](https://github.com/kubernetes-sigs/agent-sandbox/pull/1976), doc-only, open). So bumping the runner image while a sandbox is suspended is picked up on resume.
- **Memory, processes, open connections and `emptyDir` contents are lost.** Only PVC data survives.
- PVCs are named `<vctName>-<sandboxName>` and carry a controller ownerRef to the Sandbox ([reconcilePVCs L1711](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/controllers/sandbox_controller.go#L1711)). They survive suspend and expiry with `Retain`, and are **garbage-collected when the Sandbox object is deleted**.

### Expiry: `shutdownTime` / `shutdownPolicy`

When `now >= shutdownTime`, the controller deletes the Pod and Service and then applies the policy ([handleSandboxExpiry L1792](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/controllers/sandbox_controller.go#L1792)):
- `Retain` (the default) keeps the Sandbox and its PVC, clears live status and sets `Ready=False/SandboxExpired`.
- `Delete` deletes the Sandbox, and with it the PVC.

The controller requeues at the expiry time. Moving `shutdownTime` into the future, or removing it, makes the next reconcile recreate the Pod ([checkSandboxExpiry L1873](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/controllers/sandbox_controller.go#L1873)). There is no TTL-since-idle. `SandboxClaim` adds `ttlSecondsAfterFinished`, but deleting a claim cascades to the Sandbox and its PVC ([#729](https://github.com/kubernetes-sigs/agent-sandbox/issues/729)).

### Warm pools and claims

- A `SandboxWarmPool` keeps N ready Sandboxes, each with a running Pod, built from a template. A `SandboxClaim` **adopts** one of them, which keeps the pool-generated name and reports it in `.status.sandbox.name`. Ownership moves to the claim and the pool refills ([quickstart](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/examples/quickstart/README.md)). The quickstart quotes under 2 s from a pool versus 10–30 s cold, where cold includes the image pull.
- A claim that sets `env` or `volumeClaimTemplates` is **forced into a cold start**, because env and volumes can't be injected into a running Pod ([claim types](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/extensions/api/v1beta1/sandboxclaim_types.go#L138)).
- Suspending a claimed sandbox means patching the underlying `Sandbox`, since the claim has no `operatingMode`.
- For Banda, per-thread config (thread ID, API token, repo) and a per-thread PVC both push towards cold starts. Warm pools buy little until clone time and image pulls are measured.

### Snapshot / checkpoint / restore: upstream vs vendor

| Option | Where | State captured | Status |
|---|---|---|---|
| agent-sandbox suspend | upstream | PVC only | Implemented (beta API) |
| Snapshot-provider API in agent-sandbox | upstream | memory + fs | Proposal only. [#694](https://github.com/kubernetes-sigs/agent-sandbox/issues/694) is open, and [KEP PR #762](https://github.com/kubernetes-sigs/agent-sandbox/pull/762) was closed 2026-08-28. Related open issues: [#949](https://github.com/kubernetes-sigs/agent-sandbox/issues/949) (rootfs to OCI), [#1237](https://github.com/kubernetes-sigs/agent-sandbox/issues/1237) (guest agent), [#1343](https://github.com/kubernetes-sigs/agent-sandbox/issues/1343) (resume onto warm workers) |
| GKE Pod Snapshots | **GKE only** | memory + rootfs (whole Pod or rootfs only), **PVs not included** | Needs a GKE Standard cluster at 1.35.2-gke.1842000 or later, gVisor and a GCS bucket. CRDs are `podsnapshot.gke.io/v1` (`PodSnapshotStorageConfig`, `PodSnapshotPolicy`, `PodSnapshotManualTrigger`, `PodSnapshot`). Restores need the same machine series and gVisor version, and external connections are dropped ([concepts](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/pod-snapshots), [how-to](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/pod-snapshots)). agent-sandbox only wraps it in the Python SDK ([gke_extensions/snapshots](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/clients/python/agentic-sandbox-client/k8s_agent_sandbox/gke_extensions/snapshots/README.md)) |
| KEP-2008 container checkpoint | upstream kubelet | checkpoint only, **no restore API** | Beta since 1.30. Marked stale/rotten in 2026 ([issue](https://github.com/kubernetes/enhancements/issues/2008)) |
| KEP-5823 Pod-level checkpoint/restore | upstream (SIG Node) | memory + fs | `implementable`, alpha targeted at **v1.38**, feature gate `PodLevelCheckpointRestore` ([kep.yaml](https://github.com/kubernetes/enhancements/blob/master/keps/sig-node/5823-pod-level-checkpoint-restore/kep.yaml)). Not usable today |
| gVisor `runsc checkpoint/restore` | runtime | memory + fs | Works at the runtime level. Not plumbed through containerd/Kubernetes upstream. GKE Pod Snapshots and Agent Substrate both build on it |

### Isolation runtimes

agent-sandbox only sets `runtimeClassName`, which you supply in the Pod spec ([README scope note](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/README.md)).
- **gVisor**: the [gVisor-on-kind guide](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/examples/quickstart/gvisor.md) bind-mounts the host's `runsc` and needs a **Linux host**, so it does not work as written on macOS.
- **Kata**: the docs use minikube and need nested virtualization.
- `kubectl port-forward` does not work with gVisor or Kata Pods ([v1.0.7 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.7)). This doesn't matter for Banda, whose runner dials out.

### Client libraries

- **Go API types** live at `sigs.k8s.io/agent-sandbox/api/v1beta1`, with `AddToScheme` for controller-runtime and `GroupVersion` `agents.x-k8s.io/v1beta1`. Extensions are at `sigs.k8s.io/agent-sandbox/extensions/api/v1beta1`.
- **Typed clientset**: `sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned`, used as `cs.AgentsV1beta1().Sandboxes(ns)`. Apply configurations, listers and informers sit next to it under `clients/k8s/`.
- **Go SDK** `sigs.k8s.io/agent-sandbox/clients/go/sandbox`: a high-level client built on SandboxClaim + router + `sandboxd` (run commands, files). It has no suspend/resume and **Banda doesn't need it**. ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/clients/go/README.md))
- **Python SDK** `k8s-agent-sandbox` on PyPI, and a TypeScript SDK. Both are claim-centric.

### Install on kind

```sh
kind create cluster --name banda
# Core only (Sandbox CRD + controller in namespace agent-sandbox-system):
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.7/sandbox.yaml
# Extensions, if ever needed: .../v1.0.7/extensions.yaml, or sandbox-with-extensions.yaml for both
kubectl wait --for=condition=Ready pod -l app=agent-sandbox-controller -n agent-sandbox-system --timeout=120s
```

- kind's default StorageClass `standard` (local-path, RWO, `WaitForFirstConsumer`) is enough. A local-path PV is pinned to its node, which is fine on a single-node cluster.
- The data lives inside the kind node container, so it survives `docker restart` but **not `kind delete cluster`**.

### Known lifecycle limitations and open issues

- No idle-based auto-suspend or auto-resume on traffic ([#968](https://github.com/kubernetes-sigs/agent-sandbox/issues/968), [#849](https://github.com/kubernetes-sigs/agent-sandbox/issues/849)). "Scale to Zero" and "Auto Suspend/Resume" are listed as *Planned* in the [roadmap](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/roadmap.md).
- A Pod in phase `Failed` (for example after a node-pressure eviction) is **not recreated**: the Sandbox reports `Finished=PodFailed` and stays stuck. The opt-in `podFailurePolicy: Recreate` ([PR #1156](https://github.com/kubernetes-sigs/agent-sandbox/pull/1156)) was closed unmerged. Workaround: delete the Pod (the controller then creates a new one on the same PVC) or toggle Suspended→Running ([#729](https://github.com/kubernetes-sigs/agent-sandbox/issues/729)).
- `volumeClaimTemplates` can't change after creation, so PVC size is fixed per sandbox.
- Resume is always a Pod start, roughly a second of overhead plus container start ([#1343](https://github.com/kubernetes-sigs/agent-sandbox/issues/1343)). Metrics for suspend and resume are still in review ([#1155](https://github.com/kubernetes-sigs/agent-sandbox/issues/1155)).

## 2. kagent (kagent-dev/kagent, CNCF Sandbox)

- **What it is**: "a Kubernetes native framework for building AI agents" ([README](https://github.com/kagent-dev/kagent/blob/main/README.md)). Agents are declared as CRs (`Agent`, `Harness`, `AgentTemplate`, `SandboxTemplate`, `EgressPolicy`, all `api.kagent.dev/v1alpha3`) and invoked over A2A, gRPC or MCP. Sessions and tasks live in PostgreSQL.
- **Versions**:
  - `main` is **v1.0.0-alpha14** (2026-10-16), and its schema "requires a clean database".
  - The stable line is v0.10.x (v0.10.3, 2026-10-02).
  - v0.9 depended on `sigs.k8s.io/agent-sandbox v0.3.10` for its SandboxAgent CRD. From v0.10 kagent moved to **Agent Substrate** ([go.mod](https://github.com/kagent-dev/kagent/blob/main/go/go.mod) replaces `agent-substrate/substrate` with the fork `kagent-dev/substrate v0.5.0-alpha2`; [blog](https://kagent.dev/blog/the-future-of-kagent)).
- **Lifecycle features** ([runtime-and-lifecycle.md](https://github.com/kagent-dev/kagent/blob/main/docs/architecture/runtime-and-lifecycle.md)):
  - A Session is pinned to one Substrate Actor.
  - Once a turn ends or needs input, a worker suspends the Actor and keeps a DATA snapshot of its `/data` durable directory. The next interaction resumes it through Substrate ingress.
  - Sessions are deleted after 7 days idle (`config.sessions.idleTtl`), and checkpoint/fork is supported.
  - Standalone `Sandbox`es (scratch environments for commands and files) are DB records backed by Actors, not Kubernetes objects ([sandboxes.md](https://github.com/kagent-dev/kagent/blob/main/docs/architecture/sandboxes.md)).
- **Claude harness**: it runs the real `claude` binary with `--input-format/--output-format stream-json` and resumes with `claude -p --resume <id>` after a DATA restore ([harness README](https://github.com/kagent-dev/kagent/blob/main/go/harness/claude/README.md)). That is close to Banda's runner. Credentials, though, are **only Anthropic API keys, Bedrock or Vertex, injected by the gateway** ([config.go](https://github.com/kagent-dev/kagent/blob/main/go/harness/claude/config/config.go)). There is no subscription login.
- **Fit for Banda: poor.**
  - Banda's hard constraint is that each user logs in to the unmodified binary with their own subscription, and Banda never holds the token ([PLAN.md](../../PLAN.md#constraint-claude-subscriptions)). kagent's model is the opposite: platform-held credentials injected at the edge.
  - kagent also brings A2A, its own Postgres, Substrate (gVisor, object storage) and alpha APIs.
  - It has no git-workspace or interactive-terminal concept for a human login.
  - Use it as a design reference (idle quiescence, PreToolUse `defer` so a pending permission survives a restore), not as a dependency.

## 3. "Agent substrate"

Agent Substrate is a specific project: [github.com/agent-substrate/substrate](https://github.com/agent-substrate/substrate).
- Apache-2.0, created 2026-05-13, launched 2026-05-20/21 alongside GKE Agent Sandbox GA ([Google blog](https://cloud.google.com/blog/products/containers-kubernetes/bringing-you-agent-sandbox-on-gke-and-agent-substrate)).
- Maintainers are from Google and Solo.io ([MAINTAINERS.md](https://github.com/agent-substrate/substrate/blob/main/MAINTAINERS.md)). Latest release is v0.5.0 (2026-10-14). The README says "pre-1.0 … no guarantees about backward compatibility".

What it does ([architecture.md](https://github.com/agent-substrate/substrate/blob/main/docs/architecture.md), which itself warns "much of this architecture is aspirational"):
- Many **Actors** (records in Postgres) are multiplexed onto a pool of warm **worker Pods**, defined by the `WorkerPool` CRD.
- An `ActorTemplate` is created via its gRPC API / `kubectl ate`, not as a Kubernetes object. It produces a golden snapshot.
- Suspend checkpoints memory and disk with `runsc` (gVisor) or Cloud Hypervisor (microVM). Plain runc is **not supported**.
- Snapshots go to the node first ("pause"), then get uploaded to GCS/S3 ("suspend").
- The Envoy-based `atenet-router` resumes an actor when an HTTP request carrying `ate-target-actor` arrives. Durable volumes are `durableDir`s.
- Claims: resume in under 500 ms (README, no method published) and 10–30x oversubscription. Local kind (including macOS via the Docker VM) works with gVisor.

Fit for Banda: not for v1.
- It is pre-1.0 and needs its own Postgres, object storage and gRPC API.
- Resume is driven by **inbound** HTTP, which clashes with Banda's runner that dials out.
- `kubectl-ate` has no exec or attach for the terminal-based `claude` login.
- It is the most credible future backend if paused environments need to keep processes, such as dev servers.

## 4. Other Kubernetes-native options (brief)

- **DevWorkspace Operator** ([devfile/devworkspace-operator](https://github.com/devfile/devworkspace-operator), v1.0.0, active): `DevWorkspace.spec.started: bool` stops and starts a dev environment while keeping the PVC, with `idleTimeout`, `persistUserHome` and `cleanupOnStop` in the operator config. Same model as agent-sandbox suspend, but devfile-centric and built for Eclipse Che.
- **KEDA**: scales `SandboxWarmPool` to and from zero through `/scale` ([example](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.7/examples/keda-scale-to-zero/README.md)). It's for pools only, not individual sandboxes.
- **WG Checkpoint Restore** plus KEP-5823 (see above) is the upstream path to memory snapshots. Nothing is usable before v1.38 alpha.

## 5. Claude Code facts that drive persistence

- Transcripts are stored at `~/.claude/projects/<cwd with non-alphanumerics replaced by ->/<session-id>.jsonl`. `--resume <id>` checks the current project first, then all other projects ([sessions](https://code.claude.com/docs/en/sessions#where-transcripts-are-stored)). **Retention defaults to 30 days** (`cleanupPeriodDays`, swept silently at session start) ([settings reference](https://code.claude.com/docs/en/settings-reference#cleanupperioddays)).
- Linux credentials are stored at `~/.claude/.credentials.json` (mode 0600) ([authentication](https://code.claude.com/docs/en/authentication#credential-management)).
- `~/.claude.json` sits in `$HOME`, **outside** `~/.claude/`. It holds the OAuth session, trust decisions and MCP servers ([settings](https://code.claude.com/docs/en/settings), [claude-directory](https://code.claude.com/docs/en/claude-directory)). `CLAUDE_CONFIG_DIR` moves the config directory ([env-vars](https://code.claude.com/docs/en/env-vars)).
- **Implication**: persist the whole `$HOME`, not just `~/.claude`.

---

## What Banda took

One `Sandbox` per thread, created directly by the API, with its timers in the API: upstream has no idle policy, so the only controller-side timer is `shutdownTime`. What it does in each state is in [PLAN.md](../../PLAN.md), "Environment lifecycle".

### Expected resume latency (estimates)

On kind with the image already on the node:
- Pod create to container running: about 1–3 s.
- Runner dial-in plus `claude -p --resume` startup: a few seconds.
- Total: **about 3–10 s** to the first token of a new turn.

A cold create also includes the git clone. The first image pull adds 10–30 s, per the agent-sandbox quickstart.

Cost: a suspended thread costs only its PVC disk. A running idle thread costs the memory of `claude` plus the runner, so set low requests and measure.

### Deferred

- **Warm pools, SandboxTemplate, SandboxClaim**: per-thread env and PVC force cold starts anyway. Revisit if cold create time hurts.
- **Memory snapshots** (GKE Pod Snapshots, Agent Substrate, KEP-5823): none of them work on kind with upstream components today. Revisit when Banda needs processes to survive a pause, or wants resume under a second.
- **gVisor/Kata**: the documented kind path needs a Linux host. Add `runtimeClassName` on a Linux cluster later, before untrusted multi-tenant use.
- **Auto-resume on traffic, router.**
- Watch upstream [#849](https://github.com/kubernetes-sigs/agent-sandbox/issues/849) and [#968](https://github.com/kubernetes-sigs/agent-sandbox/issues/968): when an idle policy lands, delete the API ticker.
