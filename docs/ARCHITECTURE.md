# Architecture (v1alpha1)

## Reconciliation Flow

The `AppIntent` reconciler runs a four-phase loop on every reconcile cycle:

### 1. Materialize

Create or update managed resources to match the declared intent:

- **PodDisruptionBudget** — `minAvailable: 1` (fixed for now).
- **NetworkPolicy** — `strict` tier gets deny-all egress; `baseline`/`hardened` allow all egress.
- **HorizontalPodAutoscaler** — created when `autoscaling.enabled` is true. Starts with conservative `maxReplicas` (equal to `minReplicas`); the react phase raises the ceiling as needed.

Effective policy is computed by merging three layers:
1. `RuntimeProfile` defaults (cluster-scoped, optional)
2. `AppIntent` spec (workload-specific)
3. `NamespaceIntent` constraints (security tier strengthened, cost cap lowered, autoscaling bounds clamped inward)

### 2. Observe

Query Prometheus for real-time telemetry using PromQL expressions from annotations on the `AppIntent`:
- `kubeintent.io/promql-p99` — p99 request latency
- `kubeintent.io/promql-p50` — p50 request latency
- `kubeintent.io/promql-rps` — request rate

Results are written to `status.observedState` along with replica count (from the Deployment) and estimated monthly cost (from the cost model).

### 3. Compute Compliance

Compare observed state against declared targets:
- **Meeting** — all targets satisfied.
- **AtRisk** — p99 latency exceeds 90% of the target but hasn't breached it.
- **Violating** — p99 latency exceeds the target, or cost exceeds the budget.
- **Unknown** — no telemetry available.

Written to `status.compliance.overall`.

### 4. React

When a violation is detected, the controller takes action:
- **ScaleReplicas** — raises HPA `maxReplicas` by 1 to give the autoscaler room to add capacity.
- **Blocked** — scaling is warranted but would exceed the cost ceiling.
- **NoOp** — no actionable violation, or already at the spec ceiling.

A 2-minute cooldown prevents thrashing between react cycles. Every decision is logged to `status.decisions` with its reasoning and the observed inputs that drove it.

## Ownership + Drift Model

- Operator sets `ownerReferences` on all managed resources (garbage collection on `AppIntent` deletion).
- Managed resources are stamped with labels:
  - `kubeintent.io/managed=true`
  - `kubeintent.io/app-intent=<name>`
- Non-managed fields are preserved; only KubeIntent-owned fields are reconciled.

## Child Resource Naming

All managed resources follow the pattern: `<targetName>-kubeintent-{pdb,netpol,hpa}`.

## Conflict Resolution

Priority order for policy fields:
1. `DriftException` (if active) — *not yet enforced*
2. `AppIntent.spec`
3. `RuntimeProfile.spec.defaults`
4. Safe operator defaults

## Status Conditions

- `Ready` — all managed resources are in the desired state.
- `ProfileResolved` — the referenced `RuntimeProfile` was found and applied.
- `PolicyApplied` — managed resources were successfully created or updated.
- `Degraded` — an error occurred during reconciliation.

## Cost Model

The controller loads node pool pricing from a YAML ConfigMap at startup (`--cost-model-config` flag). It estimates monthly cost by:
1. Reading CPU and memory requests from the target Deployment.
2. Finding the cheapest node pool that fits.
3. Multiplying `hourlyUSD * 730 hours * replicas`.

## Telemetry

The `TelemetryProvider` interface abstracts metric queries. The only implementation today is the Prometheus adapter (`internal/telemetry/prometheus.go`), configured via the `--prometheus-url` flag. It issues instant vector queries against the Prometheus HTTP API.
