#!/usr/bin/env bash
set -euo pipefail

DEMO_CLUSTER="${DEMO_CLUSTER:-kubeintent-demo}"

echo "==> Deleting kind cluster ${DEMO_CLUSTER}..."
kind delete cluster --name "${DEMO_CLUSTER}" 2>/dev/null || true
echo "Done."
