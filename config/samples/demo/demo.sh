#!/usr/bin/env bash
set -euo pipefail

DEMO_CLUSTER="${DEMO_CLUSTER:-kubeintent-demo}"
DEMO_IMAGE="${DEMO_IMAGE:-kubeintent:demo}"
BURNER_IMAGE="cpu-burner:demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ---- prerequisites ----
command -v kind    >/dev/null 2>&1 || { echo "ERROR: kind is not installed.  Install from https://kind.sigs.k8s.io/"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "ERROR: kubectl is not installed."; exit 1; }
command -v docker  >/dev/null 2>&1 || { echo "ERROR: docker is not installed."; exit 1; }

echo "==> Checking kind cluster ${DEMO_CLUSTER}..."
kind get clusters 2>/dev/null | grep -qx "${DEMO_CLUSTER}" || {
    echo "==> Creating kind cluster ${DEMO_CLUSTER}..."
    kind create cluster --name "${DEMO_CLUSTER}" --wait 60s
}

kubectl config use-context "kind-${DEMO_CLUSTER}" >/dev/null

echo "==> Building operator image..."
docker build -t "${DEMO_IMAGE}" .

echo "==> Building cpu-burner demo image..."
docker build -t "${BURNER_IMAGE}" "${SCRIPT_DIR}/cpu-burner"

echo "==> Loading images into kind..."
kind load docker-image "${DEMO_IMAGE}" --name "${DEMO_CLUSTER}"
kind load docker-image "${BURNER_IMAGE}" --name "${DEMO_CLUSTER}"

echo "==> Installing metrics-server (required for CPU-based HPA)..."
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml >/dev/null
kubectl -n kube-system patch deployment metrics-server --type=json \
    -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' >/dev/null 2>&1 || true

echo "==> Deploying Prometheus..."
kubectl apply -f config/samples/demo/prometheus.yaml

echo "==> Installing kubeintent operator..."
kubectl apply -f config/install.yaml

# Patch the controller to use the demo image and point at our Prometheus.
kubectl -n kubeintent-system set image deployment/kubeintent-controller-manager \
    manager="${DEMO_IMAGE}" >/dev/null
kubectl -n kubeintent-system set env deployment/kubeintent-controller-manager \
    PROMETHEUS_URL=http://prometheus.monitoring.svc:9090 --containers=manager >/dev/null 2>&1 || true

# Patch args: remove existing args, set the ones we need.
kubectl -n kubeintent-system patch deployment kubeintent-controller-manager --type=json \
    -p='[{"op":"replace","path":"/spec/template/spec/containers/0/args","value":["--leader-elect","--prometheus-url=http://prometheus.monitoring.svc:9090","--cost-model-config=/etc/kubeintent/cost-model.yaml"]}]' >/dev/null

echo "==> Waiting for Prometheus to be ready..."
kubectl -n monitoring rollout status deployment/prometheus --timeout=90s

echo "==> Waiting for kubeintent controller to be ready..."
kubectl -n kubeintent-system rollout status deployment/kubeintent-controller-manager --timeout=90s

echo "==> Deploying demo workload + AppIntent..."
kubectl apply -f config/samples/demo/workload.yaml

echo "==> Waiting for demo-app to be ready..."
kubectl rollout status deployment/demo-app --timeout=90s

cat <<'EOF'

============================================================
 Demo is ready!
============================================================

Watch the AppIntent status:
  kubectl get appintent demo-app -w

In another terminal, generate load:
  kubectl run load-test --image=williamyeh/hey --restart=Never \
    -- -z 60s -c 50 http://demo-app.default.svc.cluster.local

You should see:
  - CPU rises above 50% on demo-app pods
  - HPA scales pods from 1 toward maxReplicas
  - If load exceeds HPA capacity, p99 rises above 100ms
  - Compliance changes to AtRisk/Violating
  - The operator raises the HPA ceiling further
  - Decisions appear in status.decisions

Tip: watch status changes live:
  watch -n5 'kubectl get appintent demo-app -o jsonpath="{.status}" | python3 -m json.tool'

EOF
