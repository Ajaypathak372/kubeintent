# CRD Reference

All CRDs are in the `kubeintent.io` API group, version `v1alpha1`.

## AppIntent

**Scope**: Namespaced | **Short name**: `ai` | **Status subresource**: yes

The primary user-facing CRD. One `AppIntent` per workload. The operator materializes and manages an HPA, PDB, and NetworkPolicy based on the declared policy.

### Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `targetRef.apiVersion` | string | yes | API version of the target workload (e.g., `apps/v1`) |
| `targetRef.kind` | string | yes | Kind of the target workload (only `Deployment` supported today) |
| `targetRef.name` | string | yes | Name of the target workload |
| `runtimeProfileRef` | string | no | Name of a cluster-scoped `RuntimeProfile` to pull defaults from |
| `policy.availability` | string | no | Resilience tier: `bronze`, `silver`, or `gold` |
| `policy.latencyTargetMs` | integer | no | Maximum acceptable p99 latency in milliseconds |
| `policy.maxMonthlyCostUSD` | number | no | Cost ceiling for the workload per month (USD) |
| `policy.securityTier` | string | no | Network policy posture: `baseline`, `hardened`, or `strict` |
| `policy.autoscaling.enabled` | boolean | no | Whether to create an HPA |
| `policy.autoscaling.minReplicas` | integer | no | HPA floor (minimum 1) |
| `policy.autoscaling.maxReplicas` | integer | no | HPA ceiling (minimum 1) |
| `policy.autoscaling.cpuUtilizationTargetPct` | integer | no | CPU utilization target for HPA (1-100) |

### Status

| Field | Type | Description |
|---|---|---|
| `observedGeneration` | integer | Generation of the spec that was last reconciled |
| `conditions` | array | Standard Kubernetes conditions (`PolicyApplied`, `Ready`, `Degraded`) |
| `observedState.lastObservedAt` | date-time | When telemetry was last collected |
| `observedState.latency.p99` | string | Observed p99 latency as a Go duration (e.g., `42ms`) |
| `observedState.latency.p50` | string | Observed p50 latency |
| `observedState.currentReplicas` | integer | Current replica count |
| `observedState.currentMonthlyCostUSD` | number | Estimated monthly cost at current scale |
| `observedState.observedRPS` | number | Observed requests per second |
| `decisions` | array | Recent controller decisions (max 50), each with `id`, `at`, `action`, `reason`, `fromValue`, `toValue`, `inputs` |
| `compliance.overall` | string | One of `Meeting`, `AtRisk`, `Violating`, `Unknown` |

### Printer Columns

```
NAME       TARGET     COMPLIANCE   P99    REPLICAS   COST/MONTH   AGE
demo-app   demo-app   Meeting      42ms   3          3.504        5m
```

### Prometheus Annotations

The operator reads PromQL queries from annotations on the AppIntent:

| Annotation | Description |
|---|---|
| `kubeintent.io/promql-p99` | PromQL query returning p99 latency in seconds |
| `kubeintent.io/promql-p50` | PromQL query returning p50 latency in seconds |
| `kubeintent.io/promql-rps` | PromQL query returning request rate |

---

## RuntimeProfile

**Scope**: Cluster | **Short name**: `rp`

Reusable bundle of policy defaults. Referenced by `AppIntent.spec.runtimeProfileRef`.

### Spec

| Field | Type | Description |
|---|---|---|
| `defaults.availability` | string | Default availability tier: `bronze`, `silver`, or `gold` |
| `defaults.securityTier` | string | Default security tier: `baseline`, `hardened`, or `strict` |
| `defaults.autoscaling.enabled` | boolean | Default autoscaling toggle |
| `defaults.autoscaling.minReplicas` | integer | Default HPA floor |
| `defaults.autoscaling.maxReplicas` | integer | Default HPA ceiling |
| `defaults.autoscaling.cpuUtilizationTargetPct` | integer | Default CPU target (1-100) |

---

## NamespaceIntent

**Scope**: Namespaced | **Short name**: `ni`

Namespace-level guardrails. When multiple `NamespaceIntent` resources exist in a namespace, the one with the highest `priority` wins (ties broken alphabetically by name).

### Spec

| Field | Type | Description |
|---|---|---|
| `priority` | integer | Priority for conflict resolution (highest wins) |
| `policy.availability` | string | Minimum availability tier for the namespace |
| `policy.securityTier` | string | Minimum security tier (can only be strengthened by apps) |
| `policy.maxMonthlyCostUSD` | number | Cost ceiling (app intents cannot exceed this) |
| `policy.autoscaling.enabled` | boolean | Autoscaling toggle |
| `policy.autoscaling.minReplicas` | integer | Minimum floor for HPAs in the namespace |
| `policy.autoscaling.maxReplicas` | integer | Maximum ceiling for HPAs in the namespace |
| `policy.autoscaling.cpuUtilizationTargetPct` | integer | CPU target (1-100) |

---

## DriftException

**Scope**: Namespaced | **Short name**: `de`

Time-bounded override for a specific `AppIntent`. Allows temporary deviation from declared policy with an audit trail.

> **Note**: `DriftException` is defined in the API but not yet enforced by the reconciler.

### Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `appIntentRef` | string | yes | Name of the `AppIntent` this exception applies to |
| `expiresAt` | date-time | yes | When this exception expires (ISO 8601) |
| `fields` | array of strings | yes | Which policy fields are overridden |
| `reason` | string | yes | Human-readable justification for the exception |
