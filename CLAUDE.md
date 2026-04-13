# CLAUDE.md

This file gives future Claude Code sessions the context to be productive in
KubeIntent without re-exploring the repo. It is read automatically at the
start of every session. Keep it concrete and fact-based; do not invent
conventions, targets, or files.

## Project Overview

KubeIntent is a Kubernetes operator that turns high-level **application
intent** into concrete cluster policies. Instead of hand-maintaining HPA,
PDB, and NetworkPolicy YAML per workload, teams declare one `AppIntent`
CR describing the desired outcome (availability tier, latency target,
cost cap, security tier, autoscaling), and the controller materializes
and keeps the derived resources in sync. The core design principle is
**outcome over mechanism**: users describe what they want; the operator
chooses how to achieve it. The MVP is open-loop (spec → derived
resources); the roadmap is a closed feedback loop where observed runtime
state feeds back into the decision.

## Architecture

Standard controller-runtime layout, but **hand-rolled, not scaffolded by
Kubebuilder**: there is no `PROJECT` file, no `zz_generated_*.go`, and
no `controller-gen` usage. `DeepCopyObject` implementations and CRD YAML
are maintained by hand.

- `api/v1alpha1/` — CRD Go types (`types.go`) and scheme registration
  (`groupversion.go`). DeepCopy is hand-written.
- `internal/reconcile/` — reconciler implementations. Currently only
  `appintent_controller.go` exists; it is the single reconciler
  registered with the manager.
- `main.go` — controller-runtime manager setup, scheme registration,
  reconciler wiring, health checks.
- `config/crd/bases/` — hand-written CRD YAML, one file per kind.
- `config/samples/` — example CRs used for manual E2E checks.
- `config/operator/operator.yaml` — controller Deployment, ServiceAccount,
  ClusterRole/Binding.
- `config/install.yaml` — single-file bundle of CRDs + namespace + RBAC
  + Deployment; the canonical installation entry point.
- `docs/ARCHITECTURE.md` — short reconciliation-flow overview.

Framework: `sigs.k8s.io/controller-runtime` v0.19.0. Kubernetes libraries
at v0.31.1. Go module path `github.com/ajaypathak/kubeintent`.

## CRD Types (api/v1alpha1)

Only `AppIntent` has an active reconciler. The other three are read by
the `AppIntent` reconciler to compute effective policy; none has its own
controller yet.

- **AppIntent** (short name `ai`, **Namespaced**, has status subresource)
  — the primary user-facing CRD. `spec.targetRef` points at a workload
  (MVP only supports `apps/v1` Deployment). `spec.runtimeProfileRef`
  optionally pulls defaults from a `RuntimeProfile` by name.
  `spec.policy` (`IntentPolicy`) holds `availability`
  (bronze/silver/gold), `latencyTargetMs`, `maxMonthlyCostUSD`,
  `securityTier` (baseline/hardened/strict), and `autoscaling` (enabled,
  min/max replicas, CPU target pct). `status` carries
  `observedGeneration` and `conditions`.
- **RuntimeProfile** (short name `rp`, **Cluster-scoped** — confirmed in
  `config/crd/bases/kubeintent.io_runtimeprofiles.yaml`) — reusable
  bundle of defaults: `availability`, `securityTier`, `autoscaling`.
  Consumed by `AppIntent.spec.runtimeProfileRef`.
- **NamespaceIntent** (short name `ni`, **Namespaced**) — namespace-level
  guardrails. `spec.priority` picks the winner when multiple exist in a
  namespace (highest wins; tiebreak by name). `spec.policy` acts as a
  floor/ceiling: security tier can only be strengthened, cost cap can
  only be lowered, autoscaling min/max are clamped inward.
- **DriftException** (short name `de`, **Namespaced**) — time-bounded
  exception record with `appIntentRef`, `expiresAt` (ISO date-time),
  `fields`, `reason`. **Currently defined in the API but not consumed
  by any reconciler** — planned, not wired. `docs/ARCHITECTURE.md`
  lists it as the top priority in conflict resolution, but the code
  does not honor that yet.

## Key Conventions

These are observed from the code as it stands. Where a convention is
absent, it is called out — do not invent one.

- **Logging**: controller-runtime logger pulled via
  `log.FromContext(ctx)` inside `Reconcile`. Manager uses
  `sigs.k8s.io/controller-runtime/pkg/log/zap` with `Development: true`.
  Log calls are sparse — mostly `logger.Error(err, "...")` on
  status-update failure.
- **Errors**: returned bare (`return ctrl.Result{}, err`). There is **no
  error wrapping with `fmt.Errorf("%w", ...)`** in the current
  reconciler. Do not introduce a new convention without discussion.
- **Status updates**: `r.Status().Update(ctx, &intent)` (full update,
  not patch). Conditions are set via `meta.SetStatusCondition` (wrapped
  as `metaSetCondition`). There is **no conflict-retry on status update
  today**; a conflict bubbles up as a reconcile error and controller-
  runtime requeues. If you add conflict handling, be consistent with
  the existing sites.
- **Events**: **no `EventRecorder` is wired today.** `main.go` does not
  pull one from the manager and the reconciler records no events. RBAC
  in `config/operator/operator.yaml` already grants `events` create/
  patch, so the permission side is ready, but the Go wiring is not.
  Status conditions are currently the only progress/failure signal.
- **Managed-resource labels**: every materialized child gets
  `kubeintent.io/managed=true` and `kubeintent.io/app-intent=<name>`
  plus a `controllerutil.SetControllerReference` to the owning
  `AppIntent`. Use `mergeLabels` to preserve existing labels.
- **Child naming**: `<targetName>-kubeintent-{pdb,netpol,hpa}`. Keep
  this pattern when adding new children.
- **Create-or-update**: `controllerutil.CreateOrUpdate` with a mutation
  closure that both sets the owner ref and mutates the spec.
- **Tests**: **there are no `*_test.go` files in the repo.** CI runs
  `go test ./...` but has nothing to run. Any new reconciler-scoped
  test will be the first — pick a structure deliberately.
- **Formatting**: `gofmt` is enforced in CI — the `test` job fails if
  `gofmt -l` reports anything. Run `gofmt -w` before committing.
- **Imports**: grouped stdlib → `k8s.io`/`sigs.k8s.io` → internal
  (`github.com/ajaypathak/...`). Mirror the order in
  `internal/reconcile/appintent_controller.go`.

## Build, Test, and Run

The `Makefile` is intentionally minimal. **There are no `generate`,
`manifests`, `test`, or `install` targets** — do not reference targets
that do not exist.

Existing Make targets:
- `make run` — run the controller locally against the current
  kubecontext (`go run ./main.go`).
- `make bin` — build all Go packages (`go build ./...`).
- `make docker-build` — build the controller image
  (`docker build -t $(IMAGE) .`, default
  `ghcr.io/ajaypathak372/kubeintent:latest`).
- `make build` — `bin` + `docker-build`.
- `make deploy` — apply the full install bundle
  (`kubectl apply -f config/install.yaml`): CRDs + namespace + RBAC +
  Deployment. Assumes the image referenced in `config/install.yaml` is
  already available to the target cluster.
- `make undeploy` — tear the bundle down
  (`kubectl delete -f config/install.yaml`).
- `make tidy` — `go mod tidy`.

Common commands that do **not** have a Make target:
- Run tests: `go test ./...` (no test files exist yet; CI runs this).
- Check formatting: `gofmt -l <files>` — CI fails on any output.
- Install CRDs only: `kubectl apply -f config/crd/bases/`.
- Deploy just the controller manifests (no CRDs):
  `kubectl apply -f config/operator/operator.yaml`.
- Push the locally built image to a registry (no `docker-push` target
  today; use `docker push $(IMAGE)` directly, or `kind load docker-image`
  for kind clusters).
- Apply a sample: `kubectl apply -f config/samples/kubeintent_v1alpha1_e2e_test.yaml`.

**Code generation**: because Kubebuilder scaffolding is not in use,
there is nothing to re-run after changing CRD Go types. You must
**manually update** all of: `api/v1alpha1/types.go` (including the
hand-written `DeepCopyObject`), the corresponding file in
`config/crd/bases/`, and the `config/install.yaml` bundle. Keep them
in sync by hand.

**Go version mismatch to be aware of**: `go.mod` declares `go 1.22.0`,
the `Dockerfile` uses `golang:1.22`, but `.github/workflows/ci.yml`
pins `GO_VERSION: "1.24.x"`. If you touch the Go version or reach for
newer language features, reconcile all three. (TODO: confirm whether
the CI 1.24.x is intentional or drift.)

## Current State and Roadmap

**Today (MVP, open loop)**: the `AppIntent` reconciler materializes a
`PodDisruptionBudget` (fixed `minAvailable: 1`), a `NetworkPolicy`
(strict tier → deny-all egress; baseline/hardened → allow-all egress),
and a `HorizontalPodAutoscaler` (when `autoscaling.enabled`, with
CPU-utilization target and min/max replicas). Effective policy is
composed by merging `RuntimeProfile` → `NamespaceIntent` → `AppIntent`
and then constraining by namespace guardrails (security tier can only
be strengthened, cost cap can only be lowered, autoscaling bounds are
clamped inward). Status conditions `PolicyApplied`, `Ready`, and
`Degraded` are set.

**Not yet implemented**:
- No dedicated reconciler for `RuntimeProfile`, `NamespaceIntent`, or
  `DriftException`.
- `DriftException` is not consulted anywhere in code.
- No event recording.
- No tests.
- No observed state, telemetry ingestion, cost model, or feedback loop.

**Next major milestone — closed feedback loop**: `ObservedState` (CRD
or status substructure), a telemetry adapter (Prometheus-only to
start), a cost model, a reactive reconciler that uses observed data to
adjust decisions, a decisions log (auditability), and a compliance
summary. Work in this direction is in scope; work outside it should be
discussed first.

## Things to Avoid

- Do not add features outside the closed-loop roadmap without asking.
- Do not introduce new top-level dependencies without discussion.
  Specifically: no observability backends beyond Prometheus, and no
  LLM / AI libraries — those belong in a separate SaaS project, not
  in this operator.
- Do not expand the CRD surface area. The four existing CRDs
  (`AppIntent`, `RuntimeProfile`, `NamespaceIntent`, `DriftException`)
  must be closed-loop and battle-tested before a fifth is considered.
- Keep the operator installable as a single Deployment with no
  external services required. Prometheus is optional for
  materialization today and will be required only for observation in
  the closed-loop milestone.
- Do not add a web UI. The UX is CLI-first via `kubectl`.
- Do not "upgrade" the project to Kubebuilder scaffolding, add
  `controller-gen`, or autogenerate DeepCopy / CRD YAML without
  discussion. The hand-rolled layout is a deliberate choice.
- Do not bypass CI formatting checks — `gofmt` is enforced.

## Verification Checklist

Run the relevant items below before declaring a change done, and
confirm the output of each before claiming success.

- `go build ./...` (or `make bin`) succeeds.
- `go test ./...` passes (CI runs this; currently no tests exist, so
  any new ones must also pass).
- `gofmt -l` reports nothing for files you touched (CI fails otherwise).
- If CRD Go types in `api/v1alpha1/types.go` changed:
  - the corresponding file in `config/crd/bases/` is updated by hand;
  - `config/install.yaml` is updated so the bundle stays consistent;
  - hand-written `DeepCopyObject` covers any new pointer / slice / map
    fields.
- New fields on CRD Go types use `omitempty` on JSON tags where
  appropriate and carry a short comment describing their purpose.
- If the change touches the reconciler, manually verify against a kind
  cluster — unit tests alone are not sufficient. Apply
  `config/install.yaml`, then a sample from `config/samples/`, and
  inspect generated child resources plus the `AppIntent` status
  conditions.
- If you added a new reconciled kind, it is registered in both
  `api/v1alpha1/groupversion.go` (scheme) and `main.go`
  (`SetupWithManager`).
