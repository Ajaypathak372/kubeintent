#!/usr/bin/env bash
set -euo pipefail

DEMO_CLUSTER="${DEMO_CLUSTER:-kubeintent-demo}"
DEMO_IMAGE="${DEMO_IMAGE:-kubeintent:demo}"

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

echo "==> Loading image into kind..."
kind load docker-image "${DEMO_IMAGE}" --name "${DEMO_CLUSTER}"

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
  kubectl get appintent demo-app -o yaml -w

In another terminal, generate load:
  kubectl run -it --rm load --image=williamyeh/hey --restart=Never \
    -- -z 60s -c 80 http://demo-app.default.svc.cluster.local

You should see:
  - p99 latency rise above 100ms
  - compliance change to AtRisk or Violating
  - the operator scale the HPA maxReplicas up
  - a Decision appear in status.decisions explaining why

Tip: watch status changes live:
  watch -n5 'kubectl get appintent demo-app -o jsonpath="{.status}" | python3 -m json.tool'

EOF
