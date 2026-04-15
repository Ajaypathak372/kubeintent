# KubeIntent

*Declare what your services need. Kubernetes will deliver it.*

<!-- TODO: replace placeholder badge URLs once CI workflow name and license are confirmed -->
[![Go](https://img.shields.io/badge/Go-1.22-00ADD8?logo=go)](https://go.dev)
[![CI](https://github.com/Ajaypathak372/kubeintent/actions/workflows/ci.yml/badge.svg)](https://github.com/Ajaypathak372/kubeintent/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ajaypathak/kubeintent)](https://goreportcard.com/report/github.com/ajaypathak/kubeintent)
<!-- TODO: add license badge once LICENSE file exists -->
<!-- [![License](https://img.shields.io/github/license/Ajaypathak372/kubeintent)](LICENSE) -->
[![GitHub stars](https://img.shields.io/github/stars/Ajaypathak372/kubeintent?style=social)](https://github.com/Ajaypathak372/kubeintent)

<p align="center">
  <img src="docs/assets/demo.gif" alt="KubeIntent demo — watch the operator react to an SLO violation in real time" width="720">
</p>

*KubeIntent watching a latency target, detecting a violation, and scaling the service to restore compliance — with every decision logged.*

Kubernetes makes you describe *how* a service should run — replicas, probes, disruption budgets, autoscalers, network policies. It never asks what the service is supposed to *achieve*. So when latency spikes or the bill balloons, nobody can say whether the cluster is doing its job, because nobody wrote down what the job was. KubeIntent closes that gap: you declare the outcome, and the operator handles the mechanism.

## Declare the outcome, not the mechanism

Write one `AppIntent` CR per workload. Describe what the service needs — availability tier, latency target, cost ceiling, security posture, scaling bounds. The operator figures out the rest.

```yaml
apiVersion: kubeintent.io/v1alpha1
kind: AppIntent
metadata:
  name: checkout-intent
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: checkout
  policy:
    availability: gold
    latencyTargetMs: 200
    maxMonthlyCostUSD: 800
    securityTier: hardened
    autoscaling:
      enabled: true
      minReplicas: 3
      maxReplicas: 15
      cpuUtilizationTargetPct: 60
```

From this single resource, the operator materializes a `HorizontalPodAutoscaler`, a `PodDisruptionBudget`, and a `NetworkPolicy` — all configured to match the declared intent. It watches real p99 latency via Prometheus, adjusts the service when the target is missed, and logs every decision with its reason to the CR's status.

## How it works

- **Declare intent** — SLO targets, cost budget, resilience requirements, security tier.
- **Materialize** — the operator creates and maintains the HPA, PDB, and NetworkPolicy.
- **Observe** — Prometheus metrics flow into `status.observedState` (latency, replicas, cost, RPS).
- **React** — when intent is violated, the operator adjusts and logs why in `status.decisions`.

## Try it in 5 minutes

```bash
git clone https://github.com/Ajaypathak372/kubeintent
cd kubeintent
make demo
```

This spins up a kind cluster, installs KubeIntent and Prometheus, deploys a sample service, and prints the exact commands to watch the closed loop in action.

## Install

Install CRDs, namespace, RBAC, and the controller with a single command:

```bash
kubectl apply -f https://raw.githubusercontent.com/Ajaypathak372/kubeintent/refs/heads/main/config/install.yaml
```

Or from a local clone:

```bash
kubectl apply -f config/install.yaml
```

This deploys everything into the `kubeintent-system` namespace. See [installation docs](docs/installation.md) for building from source, kind cluster setup, and Prometheus integration.

## What you can declare

| Field | What it means | Example |
|---|---|---|
| `policy.availability` | Resilience tier: `bronze`, `silver`, or `gold` | `gold` |
| `policy.latencyTargetMs` | Max acceptable p99 latency in milliseconds | `200` |
| `policy.maxMonthlyCostUSD` | Cost ceiling for the workload per month | `800` |
| `policy.securityTier` | Network policy posture: `baseline`, `hardened`, or `strict` | `hardened` |
| `policy.autoscaling.enabled` | Whether to create an HPA | `true` |
| `policy.autoscaling.minReplicas` | HPA floor | `3` |
| `policy.autoscaling.maxReplicas` | HPA ceiling | `15` |
| `policy.autoscaling.cpuUtilizationTargetPct` | CPU target for scaling | `60` |
| `targetRef` | The workload to manage (Deployment) | `apps/v1 Deployment checkout` |
| `runtimeProfileRef` | Optional [RuntimeProfile](docs/crds.md) for shared defaults | `production-default` |

Namespace-level guardrails (`NamespaceIntent`) and temporary overrides (`DriftException`) are also supported. See [CRD reference](docs/crds.md) for details.

## Project status

KubeIntent is **v0.x**. The CRD schema may change before v1.

**What works today**: the `AppIntent` reconciler runs a full closed feedback loop:
- **Materialize**: PDB, NetworkPolicy, and HPA from declared intent, with policy composed by merging `RuntimeProfile` defaults with `NamespaceIntent` guardrails.
- **Observe**: Prometheus telemetry adapter queries p99/p50 latency and RPS, populating `status.observedState`.
- **React**: when latency or cost targets are violated, the controller adjusts HPA scaling (with a 2-minute cooldown) and logs every decision with its reasoning to `status.decisions`.
- **Compliance**: `status.compliance.overall` reports `Meeting`, `AtRisk`, `Violating`, or `Unknown`.
- **Cost model**: per-replica cost projection from configurable node pool pricing.

**What's next**:
- Conformance test suite for policy materialization.
- `DriftException` enforcement in the reconcile loop.
- Multi-metric reaction (CPU, memory, custom metrics beyond latency).
- Dedicated reconcilers for `RuntimeProfile`, `NamespaceIntent`, and `DriftException`.

## Contributing

Contributions are welcome — check the [issue tracker](https://github.com/Ajaypathak372/kubeintent/issues) for `good first issue` labels, and see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

<!-- TODO: add link to Discord/Slack or GitHub Discussions when available -->

## License

<!-- TODO: add a LICENSE file to the repository -->
License not yet specified. See [LICENSE](LICENSE) for details once added.
