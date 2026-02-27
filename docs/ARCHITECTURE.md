# Architecture (v1alpha1)

## Reconciliation Flow

1. Watch `AppIntent`
2. Resolve referenced `RuntimeProfile` (optional)
3. Build effective policy model (`EffectiveIntent` in-memory)
4. Upsert managed resources:
   - PDB
   - NetworkPolicy
   - HPA
5. Update `AppIntent.status.conditions` and `status.observedGeneration`

## Ownership + Drift Model

- Operator sets `ownerReferences` where possible.
- Operator stamps labels:
  - `kubeintent.io/managed=true`
  - `kubeintent.io/app-intent=<name>`
- Non-managed fields are ignored unless conflict touches protected policy fields.

## Conflict Resolution

Priority order:
1. `DriftException` (if active)
2. `AppIntent.spec`
3. `RuntimeProfile.spec.defaults`
4. Safe operator defaults

## Status Conditions

- `Ready`
- `ProfileResolved`
- `PolicyApplied`
- `Degraded`
