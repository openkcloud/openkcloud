# KCloud Workload Optimizer Operator Deployment Guide

This guide provides comprehensive instructions for deploying the KCloud Workload Optimizer Operator in various environments.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation Methods](#installation-methods)
- [Configuration](#configuration)
- [Environment-Specific Deployments](#environment-specific-deployments)
- [Monitoring Setup](#monitoring-setup)
- [Security Considerations](#security-considerations)
- [Troubleshooting](#troubleshooting)
- [Upgrade Procedures](#upgrade-procedures)
- [Uninstallation](#uninstallation)

## Prerequisites

### System Requirements

- **Kubernetes**: Version 1.19 or later
- **Helm**: Version 3.0 or later
- **kubectl**: Latest stable version
- **Docker**: For building custom images

### Cluster Requirements

- **CPU**: Minimum 2 cores per node
- **Memory**: Minimum 4GB per node
- **Storage**: Minimum 20GB available space
- **Network**: Cluster networking enabled

### Required Permissions

The operator requires the following permissions:
- Cluster-wide read access to nodes, pods, services
- Read/write access to custom resources (WorkloadOptimizer, CostPolicy, PowerPolicy)
- Webhook configuration permissions
- RBAC management permissions

## Installation Methods

### Method 1: Helm Installation (Recommended)

#### Basic Installation

```bash
# Add the Helm repository
helm repo add kcloud-operator https://keti-cloud-platform.github.io/k8s-workload-operator
helm repo update

# Install with default values
helm install kcloud-operator kcloud-operator/kcloud-operator \
  --namespace kcloud-operator-system \
  --create-namespace
```

#### Custom Installation

```bash
# Create custom values file
cat > custom-values.yaml << EOF
operator:
  image:
    repository: your-registry/kcloud-operator
    tag: v1.0.0
  resources:
    limits:
      cpu: 1000m
      memory: 1Gi
    requests:
      cpu: 200m
      memory: 256Mi

metrics:
  enabled: true
  serviceMonitor:
    enabled: true

webhook:
  enabled: true

samples:
  workloadOptimizer:
    enabled: true
  costPolicy:
    enabled: true
  powerPolicy:
    enabled: true
EOF

# Install with custom values
helm install kcloud-operator kcloud-operator/kcloud-operator \
  --namespace kcloud-operator-system \
  --create-namespace \
  --values custom-values.yaml
```

#### Production Installation

```bash
# Production values
cat > production-values.yaml << EOF
operator:
  replicaCount: 3
  image:
    repository: your-registry/kcloud-operator
    tag: v1.0.0
  resources:
    limits:
      cpu: 1000m
      memory: 1Gi
    requests:
      cpu: 200m
      memory: 256Mi
  nodeSelector:
    node-type: operator
  tolerations:
  - key: operator
    operator: Exists
    effect: NoSchedule

service:
  type: ClusterIP

rbac:
  create: true

webhook:
  enabled: true
  certificate:
    create: true

metrics:
  enabled: true
  serviceMonitor:
    enabled: true
    interval: 30s

monitoring:
  prometheus:
    enabled: true
  grafana:
    enabled: true

crds:
  install: true
  keep: false

namespace:
  create: true
  name: kcloud-operator-system
EOF

# Install for production
helm install kcloud-operator kcloud-operator/kcloud-operator \
  --namespace kcloud-operator-system \
  --create-namespace \
  --values production-values.yaml
```

### Method 2: kubectl Installation

#### Step 1: Install CRDs

```bash
# Install CRDs
kubectl apply -f config/crd/bases/

# Verify CRD installation
kubectl get crd | grep kcloud.io
```

#### Step 2: Install RBAC

```bash
# Install RBAC resources
kubectl apply -f config/rbac/

# Verify RBAC installation
kubectl get clusterrole | grep kcloud-operator
kubectl get clusterrolebinding | grep kcloud-operator
```

#### Step 3: Install Operator

```bash
# Create namespace
kubectl create namespace kcloud-operator-system

# Install operator deployment
kubectl apply -f config/manager/

# Verify operator installation
kubectl get pods -n kcloud-operator-system
```

#### Step 4: Install Webhooks (Optional)

```bash
# Install webhook configuration
kubectl apply -f config/webhook/

# Verify webhook installation
kubectl get mutatingwebhookconfigurations
kubectl get validatingwebhookconfigurations
```

### Method 3: Operator Lifecycle Manager (OLM)

#### Install OLM

```bash
# Install OLM
kubectl apply -f https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v0.24.0/crds.yaml
kubectl apply -f https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v0.24.0/olm.yaml
```

#### Create Operator Group

```yaml
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: kcloud-operator-group
  namespace: kcloud-operator-system
spec:
  targetNamespaces:
  - kcloud-operator-system
```

#### Install Operator

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: kcloud-operator-subscription
  namespace: kcloud-operator-system
spec:
  channel: stable
  name: kcloud-operator
  source: kcloud-operator-catalog
  sourceNamespace: olm
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `WATCH_NAMESPACE` | Namespace to watch (empty = all) | `""` |
| `POD_NAME` | Operator pod name | `""` |
| `OPERATOR_NAME` | Operator identifier | `"kcloud-operator"` |
| `LOG_LEVEL` | Log level | `"info"` |
| `METRICS_BIND_ADDRESS` | Metrics server address | `":8080"` |
| `HEALTH_PROBE_BIND_ADDRESS` | Health probe address | `":8081"` |
| `LEADER_ELECTION` | Enable leader election | `true` |
| `LEADER_ELECTION_ID` | Leader election ID | `"a4e61049.keti.kcloud"` |

### Configuration via ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kcloud-operator-config
  namespace: kcloud-operator-system
data:
  config.yaml: |
    optimization:
      interval: "5m"
      costOptimization:
        enabled: true
        defaultCostPerHour: 5.0
        defaultBudgetLimit: 1000.0
      powerOptimization:
        enabled: true
        defaultMaxPowerUsage: 500.0
        defaultEfficiencyTarget: 80.0
      scheduling:
        enabled: true
        defaultAlgorithm: "balanced"
        defaultPriority: 5
```

### Resource Limits

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kcloud-operator
  namespace: kcloud-operator-system
spec:
  template:
    spec:
      containers:
      - name: manager
        resources:
          limits:
            cpu: 1000m
            memory: 1Gi
          requests:
            cpu: 200m
            memory: 256Mi
```

## Environment-Specific Deployments

### Development Environment

```bash
# Development values
cat > dev-values.yaml << EOF
operator:
  image:
    tag: dev
  resources:
    limits:
      cpu: 500m
      memory: 512Mi
    requests:
      cpu: 100m
      memory: 128Mi

metrics:
  enabled: true

samples:
  workloadOptimizer:
    enabled: true
  costPolicy:
    enabled: true
  powerPolicy:
    enabled: true

namespace:
  name: kcloud-dev
EOF

# Install for development
helm install kcloud-operator ./charts/kcloud-operator \
  --namespace kcloud-dev \
  --create-namespace \
  --values dev-values.yaml
```

### Staging Environment

```bash
# Staging values
cat > staging-values.yaml << EOF
operator:
  replicaCount: 2
  image:
    tag: staging
  resources:
    limits:
      cpu: 1000m
      memory: 1Gi
    requests:
      cpu: 200m
      memory: 256Mi

metrics:
  enabled: true
  serviceMonitor:
    enabled: true

monitoring:
  prometheus:
    enabled: true

namespace:
  name: kcloud-staging
EOF

# Install for staging
helm install kcloud-operator ./charts/kcloud-operator \
  --namespace kcloud-staging \
  --create-namespace \
  --values staging-values.yaml
```

### Production Environment

```bash
# Production values
cat > prod-values.yaml << EOF
operator:
  replicaCount: 3
  image:
    repository: your-registry/kcloud-operator
    tag: v1.0.0
  resources:
    limits:
      cpu: 1000m
      memory: 1Gi
    requests:
      cpu: 200m
      memory: 256Mi
  nodeSelector:
    node-type: operator
  tolerations:
  - key: operator
    operator: Exists
    effect: NoSchedule
  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchExpressions:
            - key: app.kubernetes.io/name
              operator: In
              values: ["kcloud-operator"]
          topologyKey: kubernetes.io/hostname

service:
  type: ClusterIP

rbac:
  create: true

webhook:
  enabled: true
  certificate:
    create: true

metrics:
  enabled: true
  serviceMonitor:
    enabled: true
    interval: 30s

monitoring:
  prometheus:
    enabled: true
  grafana:
    enabled: true

crds:
  install: true
  keep: false

namespace:
  create: true
  name: kcloud-operator-system
  annotations:
    pod-security.kubernetes.io/enforce: restricted
  labels:
    environment: production
EOF

# Install for production
helm install kcloud-operator ./charts/kcloud-operator \
  --namespace kcloud-operator-system \
  --create-namespace \
  --values prod-values.yaml
```

## Monitoring Setup

### Prometheus Integration

#### Install Prometheus Operator

```bash
# Add Prometheus Helm repository
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# Install Prometheus
helm install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace
```

#### Configure ServiceMonitor

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kcloud-operator-metrics
  namespace: kcloud-operator-system
  labels:
    app: kcloud-operator
spec:
  selector:
    matchLabels:
      app: kcloud-operator
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

### Grafana Dashboard

#### Import Dashboard

```bash
# Create dashboard ConfigMap
kubectl apply -f config/prometheus/grafana-dashboard.yaml

# Verify dashboard
kubectl get configmap -n kcloud-operator-system | grep grafana
```

#### Access Grafana

```bash
# Port forward to Grafana
kubectl port-forward -n monitoring svc/prometheus-grafana 3000:80

# Access Grafana
open http://localhost:3000
# Username: admin
# Password: prom-operator
```

### Alerting Rules

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kcloud-operator-alerts
  namespace: kcloud-operator-system
spec:
  groups:
  - name: kcloud-operator
    rules:
    - alert: WorkloadOptimizerHighCost
      expr: kcloud_workloadoptimizer_current_cost > 50
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "WorkloadOptimizer cost is high"
        description: "WorkloadOptimizer {{ $labels.name }} has high cost: {{ $value }}"
    
    - alert: CostPolicyBudgetExceeded
      expr: kcloud_costpolicy_budget_utilization > 90
      for: 2m
      labels:
        severity: critical
      annotations:
        summary: "Cost policy budget exceeded"
        description: "Cost policy {{ $labels.name }} budget utilization: {{ $value }}%"
```

## Security Considerations

### Network Policies

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kcloud-operator-network-policy
  namespace: kcloud-operator-system
spec:
  podSelector:
    matchLabels:
      app: kcloud-operator
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: kube-system
    ports:
    - protocol: TCP
      port: 8443
  egress:
  - to:
    - namespaceSelector: {}
    ports:
    - protocol: TCP
      port: 443
    - protocol: TCP
      port: 80
```

### Pod Security Standards

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: kcloud-operator-system
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
```

### RBAC Security

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kcloud-operator-minimal
rules:
- apiGroups: [""]
  resources: ["pods", "nodes", "services"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["kcloud.io"]
  resources: ["workloadoptimizers", "costpolicies", "powerpolicies"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

## Troubleshooting

### Common Issues

#### 1. Operator Not Starting

```bash
# Check pod status
kubectl get pods -n kcloud-operator-system

# Check pod logs
kubectl logs -n kcloud-operator-system deployment/kcloud-operator

# Check events
kubectl get events -n kcloud-operator-system --sort-by=.lastTimestamp
```

#### 2. CRDs Not Installed

```bash
# Check CRD status
kubectl get crd | grep kcloud.io

# Reinstall CRDs
kubectl apply -f config/crd/bases/
```

#### 3. Webhook Issues

```bash
# Check webhook configuration
kubectl get mutatingwebhookconfigurations
kubectl get validatingwebhookconfigurations

# Check webhook service
kubectl get svc -n kcloud-operator-system

# Test webhook endpoint
kubectl get endpoints -n kcloud-operator-system
```

#### 4. RBAC Issues

```bash
# Check RBAC resources
kubectl get clusterrole | grep kcloud-operator
kubectl get clusterrolebinding | grep kcloud-operator

# Check service account
kubectl get sa -n kcloud-operator-system
```

### Debug Commands

```bash
# Check operator status
kubectl get deployment -n kcloud-operator-system
kubectl describe deployment kcloud-operator -n kcloud-operator-system

# Check resource usage
kubectl top pods -n kcloud-operator-system

# Check network connectivity
kubectl exec -n kcloud-operator-system deployment/kcloud-operator -- nslookup kubernetes.default

# Check metrics endpoint
kubectl port-forward -n kcloud-operator-system svc/kcloud-operator 8080:8080
curl http://localhost:8080/metrics
```

### Log Analysis

```bash
# Follow operator logs
kubectl logs -n kcloud-operator-system deployment/kcloud-operator -f

# Check specific log levels
kubectl logs -n kcloud-operator-system deployment/kcloud-operator --previous

# Export logs for analysis
kubectl logs -n kcloud-operator-system deployment/kcloud-operator > operator.log
```

## Upgrade Procedures

### Helm Upgrade

```bash
# Check current version
helm list -n kcloud-operator-system

# Upgrade to new version
helm upgrade kcloud-operator kcloud-operator/kcloud-operator \
  --namespace kcloud-operator-system \
  --values custom-values.yaml

# Verify upgrade
helm list -n kcloud-operator-system
kubectl get pods -n kcloud-operator-system
```

### kubectl Upgrade

```bash
# Backup current configuration
kubectl get deployment kcloud-operator -n kcloud-operator-system -o yaml > backup.yaml

# Update CRDs
kubectl apply -f config/crd/bases/

# Update operator
kubectl apply -f config/manager/

# Verify upgrade
kubectl get pods -n kcloud-operator-system
kubectl get crd | grep kcloud.io
```

### Rolling Update

```bash
# Set image
kubectl set image deployment/kcloud-operator manager=your-registry/kcloud-operator:v1.1.0 -n kcloud-operator-system

# Check rollout status
kubectl rollout status deployment/kcloud-operator -n kcloud-operator-system

# Rollback if needed
kubectl rollout undo deployment/kcloud-operator -n kcloud-operator-system
```

## Uninstallation

### Helm Uninstallation

```bash
# Uninstall operator
helm uninstall kcloud-operator --namespace kcloud-operator-system

# Remove CRDs (optional)
helm uninstall kcloud-operator --namespace kcloud-operator-system --set crds.keep=false

# Clean up namespace
kubectl delete namespace kcloud-operator-system
```

### kubectl Uninstallation

```bash
# Remove operator
kubectl delete -f config/manager/

# Remove RBAC
kubectl delete -f config/rbac/

# Remove webhooks
kubectl delete -f config/webhook/

# Remove CRDs
kubectl delete -f config/crd/bases/

# Clean up namespace
kubectl delete namespace kcloud-operator-system
```

### Complete Cleanup

```bash
# Remove all resources
kubectl delete all --all -n kcloud-operator-system
kubectl delete pvc --all -n kcloud-operator-system
kubectl delete configmap --all -n kcloud-operator-system
kubectl delete secret --all -n kcloud-operator-system

# Remove CRDs
kubectl delete crd workloadoptimizers.kcloud.io
kubectl delete crd costpolicies.kcloud.io
kubectl delete crd powerpolicies.kcloud.io

# Remove namespace
kubectl delete namespace kcloud-operator-system
```

### Verification

```bash
# Verify complete removal
kubectl get all -n kcloud-operator-system
kubectl get crd | grep kcloud.io
kubectl get clusterrole | grep kcloud-operator
kubectl get clusterrolebinding | grep kcloud-operator
```
