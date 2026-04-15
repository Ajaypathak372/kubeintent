# Installation

## One-Command Install

Install CRDs, namespace, RBAC, and the controller with a single apply:

```bash
kubectl apply -f https://raw.githubusercontent.com/Ajaypathak372/kubeintent/refs/heads/main/config/install.yaml
```

Or from a local clone:

```bash
kubectl apply -f config/install.yaml
```

This creates the `kubeintent-system` namespace and deploys:
- All four CRDs (`AppIntent`, `RuntimeProfile`, `NamespaceIntent`, `DriftException`)
- A `ServiceAccount` with the minimum required RBAC
- The controller `Deployment`
- A `ConfigMap` with the default cost model

## Building from Source

### Prerequisites

- Go 1.22+
- Docker
- `kubectl` configured to talk to a cluster

### Build

```bash
# Build the Go binary
make bin

# Build the Docker image
make docker-build

# Or both at once
make build
```

The default image name is `ghcr.io/ajaypathak372/kubeintent:latest`. Override it with:

```bash
make docker-build IMAGE=my-registry/kubeintent:v0.1.0
```

### Deploy

```bash
# Apply CRDs + RBAC + Deployment
make deploy

# Tear down
make undeploy
```

## Kind Cluster Setup

For local development with [kind](https://kind.sigs.k8s.io/):

```bash
# Create a cluster
kind create cluster --name kubeintent-demo

# Build and load the image
make docker-build
kind load docker-image ghcr.io/ajaypathak372/kubeintent:latest --name kubeintent-demo

# Deploy
make deploy
```

### Metrics Server

Kind clusters don't include a metrics server. HPA requires one for CPU-based scaling:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
kubectl patch deployment metrics-server -n kube-system \
  --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
```

The `--kubelet-insecure-tls` flag is needed because kind uses self-signed certificates.

## Prometheus Integration

The closed feedback loop requires Prometheus to observe workload metrics. The operator connects via the `--prometheus-url` flag (default: `http://prometheus.monitoring.svc:9090`).

### Quick Prometheus Setup

```bash
kubectl create namespace monitoring

kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prometheus
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: prometheus
  template:
    metadata:
      labels:
        app: prometheus
    spec:
      containers:
        - name: prometheus
          image: prom/prometheus:v2.51.0
          args: ["--config.file=/etc/prometheus/prometheus.yml"]
          ports:
            - containerPort: 9090
          volumeMounts:
            - name: config
              mountPath: /etc/prometheus
      volumes:
        - name: config
          configMap:
            name: prometheus-config
---
apiVersion: v1
kind: Service
metadata:
  name: prometheus
  namespace: monitoring
spec:
  selector:
    app: prometheus
  ports:
    - port: 9090
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: prometheus-config
  namespace: monitoring
data:
  prometheus.yml: |
    global:
      scrape_interval: 15s
    scrape_configs:
      - job_name: kubernetes-pods
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
            action: keep
            regex: "true"
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_port]
            action: replace
            target_label: __address__
            regex: (.+)
            replacement: ${1}:${2}
            source_labels: [__meta_kubernetes_pod_ip, __meta_kubernetes_pod_annotation_prometheus_io_port]
EOF
```

### Configure the Controller

Patch the controller deployment to point at your Prometheus instance:

```bash
kubectl patch deployment kubeintent-controller-manager -n kubeintent-system \
  --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--prometheus-url=http://prometheus.monitoring.svc:9090"}]'
```

### Workload Annotations

Each `AppIntent` needs PromQL annotations so the operator knows how to query metrics for the target workload:

```yaml
metadata:
  annotations:
    kubeintent.io/promql-p99: 'histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{namespace="default",pod=~"my-app.*"}[2m])) by (le))'
    kubeintent.io/promql-p50: 'histogram_quantile(0.50, sum(rate(http_request_duration_seconds_bucket{namespace="default",pod=~"my-app.*"}[2m])) by (le))'
    kubeintent.io/promql-rps: 'sum(rate(http_request_duration_seconds_count{namespace="default",pod=~"my-app.*"}[1m]))'
```

## Cost Model

The controller reads node pool pricing from a `ConfigMap` mounted at `/etc/kubeintent/cost-model.yaml`. The default config in `install.yaml` includes common AWS instance types. To customize:

```bash
kubectl edit configmap kubeintent-cost-model -n kubeintent-system
```

Format:

```yaml
nodePools:
  - name: m5.large
    hourlyUSD: 0.096
    cpuCores: 2
    memoryGB: 8
```

The cost model estimates monthly cost by matching workload resource requests to the cheapest fitting node pool and extrapolating.
