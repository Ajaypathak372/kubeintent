# Concepts

## Intent-Driven Operations

KubeIntent introduces **intent-driven operations** to Kubernetes. Instead of configuring individual resources (HPA, PDB, NetworkPolicy) by hand, you declare what a service needs to achieve and the operator determines how to get there.

This is the difference between *outcome* and *mechanism*:

| Mechanism (traditional) | Outcome (KubeIntent) |
|---|---|
| "Set HPA min=3, max=10, CPU target 60%" | "Keep p99 latency under 200ms" |
| "Create a PDB with minAvailable: 1" | "Availability tier: gold" |
| "Write a deny-all NetworkPolicy" | "Security tier: strict" |

## The Closed Feedback Loop

The operator runs a continuous loop for each `AppIntent`:

1. **Materialize** — create or update the HPA, PDB, and NetworkPolicy to match the declared policy.
2. **Observe** — query Prometheus for real-time metrics: p99/p50 latency, request rate, replica count, and estimated cost.
3. **Compute compliance** — compare observed state against the declared targets. The result is one of:
   - `Meeting` — all targets satisfied.
   - `AtRisk` — approaching a target threshold (e.g., p99 > 90% of latency target).
   - `Violating` — a target is breached.
   - `Unknown` — insufficient telemetry to determine compliance.
4. **React** — if a violation is detected, the controller takes action (e.g., raises HPA max replicas) and logs the decision with its reasoning to `status.decisions`. A 2-minute cooldown prevents thrashing.

## Policy Composition

Effective policy for a workload is composed from three layers, applied in order:

1. **RuntimeProfile** (cluster-scoped) — shared defaults for availability, security tier, and autoscaling. Referenced by name from `AppIntent.spec.runtimeProfileRef`.
2. **NamespaceIntent** (namespace-scoped) — guardrails that constrain what workloads in a namespace can do. Security tier can only be *strengthened*, cost cap can only be *lowered*, and autoscaling bounds are clamped inward.
3. **AppIntent** (namespace-scoped) — the workload-specific policy. Fields set here override profile defaults, subject to namespace guardrails.

The merge order is: RuntimeProfile defaults < AppIntent spec < NamespaceIntent constraints.

## Ownership and Drift

Every resource the operator creates is stamped with:
- `ownerReferences` pointing back to the `AppIntent` (Kubernetes garbage collection).
- `kubeintent.io/managed=true` label.
- `kubeintent.io/app-intent=<name>` label.

If a managed resource is modified externally, the next reconcile loop restores it to match the declared intent. The `DriftException` CRD (planned) will allow temporary, auditable overrides.

## Decisions Audit Trail

Every action the controller takes (or considers) is recorded as a `Decision` entry in `status.decisions`:
- **ScaleReplicas** — HPA max replicas was increased to handle a violation.
- **NoOp** — the controller evaluated the state and determined no action was needed.
- **Blocked** — an action was warranted but blocked (e.g., cost ceiling would be exceeded).

Each decision includes the observed inputs that drove it (p99 latency, CPU utilization, RPS) and a human-readable reason. The list is capped at 50 entries, oldest-first eviction.
