# KubeIntent

A Kubernetes operator that turns high-level application intent into concrete cluster policies and runtime controls.

## Vision

Instead of manually managing multiple Kubernetes resources, teams declare **intent** in one CRD and the operator continuously materializes/enforces:

- Autoscaling defaults (HPA/KEDA hooks)
- Reliability guardrails (PDB, topology spread, probe policies)
- Security baseline (NetworkPolicy, Pod Security labels)
- Cost controls (resource bounds, optional scheduling hints)

## CRDs (v1alpha1)

- `AppIntent` – desired high-level policy per app/workload.
- `RuntimeProfile` – reusable profile that maps intent to concrete defaults.
- `NamespaceIntent` – namespace-level guardrails and defaults.
- `DriftException` – temporary and auditable override with TTL.

## Repo Layout

- `config/crd/bases/` – CRD YAMLs
- `config/samples/` – example resources
- `api/v1alpha1/` – API type definitions (Go)
- `docs/` – architecture and roadmap

## MVP Scope

MVP reconciles `AppIntent` + optional `RuntimeProfile` into:

1. `PodDisruptionBudget`
2. `NetworkPolicy` (security-tier aware: strict defaults to deny-all egress)
3. `HorizontalPodAutoscaler` (if metrics + scaling policy set)

Guardrail enforcement in v0.1:
- `securityTier`: namespace minimum cannot be weakened by app intent.
- `maxMonthlyCostUSD`: app/profile cannot exceed namespace cap.
- autoscaling bounds: namespace min/max are enforced.

## One-command install (CRDs + controller)

Install everything with one file:

```bash
kubectl apply -f https://raw.githubusercontent.com/Ajaypathak372/kubeintent/refs/heads/main/config/install.yaml
```

Local testing:

```bash
kubectl apply -f config/install.yaml
```

This single file includes:
- All CRDs (`AppIntent`, `RuntimeProfile`, `NamespaceIntent`, `DriftException`)
- `kubeintent-system` namespace
- ServiceAccount + RBAC
- Controller Deployment

## Quick E2E Test

Apply a ready-to-run sample with proper YAML indentation:

```bash
kubectl apply -f config/samples/kubeintent_v1alpha1_e2e_test.yaml
```

Verify generated resources:

```bash
kubectl -n default get appintent checkout-intent
kubectl -n default get hpa checkout-kubeintent-hpa
kubectl -n default get networkpolicy checkout-kubeintent-netpol
kubectl -n default get pdb checkout-kubeintent-pdb
```

## Next Steps

1. Wire controller-runtime manager and reconcilers.
2. Add status conditions and event recording.
3. Add conformance tests for policy materialization.
4. Add optional OPA/Kyverno integration in v1beta1.
