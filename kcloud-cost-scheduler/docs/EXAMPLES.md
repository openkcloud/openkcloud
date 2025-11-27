# KCloud Workload Optimizer Operator Examples

This document provides comprehensive examples for using the KCloud Workload Optimizer Operator in various scenarios.

## Table of Contents

- [Quick Start](#quick-start)
- [Machine Learning Workloads](#machine-learning-workloads)
- [Inference Serving](#inference-serving)
- [Batch Processing](#batch-processing)
- [Streaming Workloads](#streaming-workloads)
- [Multi-Environment Setup](#multi-environment-setup)
- [Cost Optimization](#cost-optimization)
- [Power Optimization](#power-optimization)
- [Advanced Scheduling](#advanced-scheduling)
- [Monitoring and Alerting](#monitoring-and-alerting)

## Quick Start

### 1. Install the Operator

```bash
# Using Helm (recommended)
helm install kcloud-operator ./charts/kcloud-operator \
  --namespace kcloud-operator-system \
  --create-namespace

# Using kubectl
kubectl apply -f config/crd/bases/
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

### 2. Create Your First WorkloadOptimizer

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: my-first-workload
  namespace: default
spec:
  workloadType: training
  priority: 5
  resourceRequirements:
    cpu: "2"
    memory: "4Gi"
    gpu: 1
  costConstraints:
    maxCostPerHour: 10.0
    budgetLimit: 1000.0
  powerConstraints:
    maxPowerUsage: 500.0
```

### 3. Verify Installation

```bash
# Check operator status
kubectl get pods -n kcloud-operator-system

# Check CRDs
kubectl get crd | grep kcloud.io

# Check WorkloadOptimizer
kubectl get workloadoptimizer my-first-workload
kubectl describe workloadoptimizer my-first-workload
```

## Machine Learning Workloads

### Deep Learning Training

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: bert-training
  namespace: ml-workloads
  labels:
    app: bert-training
    workload-type: training
spec:
  workloadType: training
  priority: 8
  resourceRequirements:
    cpu: "32"
    memory: "128Gi"
    gpu: 8
    npu: 0
  costConstraints:
    maxCostPerHour: 100.0
    budgetLimit: 5000.0
  powerConstraints:
    maxPowerUsage: 4000.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: accelerator
            operator: In
            values: ["nvidia-gpu"]
          - key: gpu.nvidia.com/class
            operator: In
            values: ["compute"]
  autoScaling:
    enabled: true
    minReplicas: 1
    maxReplicas: 5
    targetCPU: 80
    targetMemory: 85
```

### Computer Vision Training

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: vision-training
  namespace: ml-workloads
spec:
  workloadType: training
  priority: 7
  resourceRequirements:
    cpu: "16"
    memory: "64Gi"
    gpu: 4
    npu: 0
  costConstraints:
    maxCostPerHour: 50.0
    budgetLimit: 2000.0
  powerConstraints:
    maxPowerUsage: 2000.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: node-type
            operator: In
            values: ["gpu-optimized"]
          - key: gpu.nvidia.com/memory
            operator: Gt
            values: ["24000"]
```

### Reinforcement Learning

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: rl-training
  namespace: ml-workloads
spec:
  workloadType: training
  priority: 6
  resourceRequirements:
    cpu: "8"
    memory: "32Gi"
    gpu: 2
    npu: 0
  costConstraints:
    maxCostPerHour: 25.0
    budgetLimit: 1000.0
  powerConstraints:
    maxPowerUsage: 1000.0
  autoScaling:
    enabled: true
    minReplicas: 2
    maxReplicas: 10
    targetCPU: 70
    targetMemory: 80
```

## Inference Serving

### Real-time Inference

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: realtime-inference
  namespace: inference
spec:
  workloadType: inference
  priority: 9
  resourceRequirements:
    cpu: "4"
    memory: "16Gi"
    gpu: 1
    npu: 0
  costConstraints:
    maxCostPerHour: 15.0
    budgetLimit: 2000.0
  powerConstraints:
    maxPowerUsage: 500.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: node-type
            operator: In
            values: ["inference-optimized"]
  autoScaling:
    enabled: true
    minReplicas: 3
    maxReplicas: 50
    targetCPU: 60
    targetMemory: 70
```

### Batch Inference

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: batch-inference
  namespace: inference
spec:
  workloadType: batch
  priority: 5
  resourceRequirements:
    cpu: "8"
    memory: "32Gi"
    gpu: 2
    npu: 0
  costConstraints:
    maxCostPerHour: 20.0
    budgetLimit: 1000.0
  powerConstraints:
    maxPowerUsage: 1000.0
  placementPolicy:
    nodeAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        preference:
          matchExpressions:
          - key: workload-type
            operator: In
            values: ["batch"]
```

## Batch Processing

### Data Processing Pipeline

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: data-pipeline
  namespace: batch
spec:
  workloadType: batch
  priority: 4
  resourceRequirements:
    cpu: "16"
    memory: "64Gi"
    gpu: 0
    npu: 0
  costConstraints:
    maxCostPerHour: 30.0
    budgetLimit: 1500.0
  powerConstraints:
    maxPowerUsage: 800.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: node-type
            operator: In
            values: ["cpu-optimized"]
  autoScaling:
    enabled: true
    minReplicas: 1
    maxReplicas: 20
    targetCPU: 80
    targetMemory: 85
```

### ETL Processing

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: etl-processing
  namespace: batch
spec:
  workloadType: batch
  priority: 6
  resourceRequirements:
    cpu: "12"
    memory: "48Gi"
    gpu: 0
    npu: 0
  costConstraints:
    maxCostPerHour: 25.0
    budgetLimit: 2000.0
  powerConstraints:
    maxPowerUsage: 600.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: storage-type
            operator: In
            values: ["ssd"]
```

## Streaming Workloads

### Real-time Stream Processing

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: stream-processing
  namespace: streaming
spec:
  workloadType: streaming
  priority: 7
  resourceRequirements:
    cpu: "8"
    memory: "32Gi"
    gpu: 0
    npu: 0
  costConstraints:
    maxCostPerHour: 20.0
    budgetLimit: 1000.0
  powerConstraints:
    maxPowerUsage: 400.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: network-bandwidth
            operator: Gt
            values: ["10G"]
  autoScaling:
    enabled: true
    minReplicas: 2
    maxReplicas: 30
    targetCPU: 70
    targetMemory: 75
```

## Multi-Environment Setup

### Development Environment

```yaml
# Development CostPolicy
apiVersion: kcloud.io/v1alpha1
kind: CostPolicy
metadata:
  name: dev-cost-policy
  namespace: development
spec:
  budgetLimit: 500.0
  costPerHourLimit: 5.0
  spotInstancePolicy:
    enabled: true
    maxPrice: 2.0
  alertThresholds:
    budgetUtilization: 80.0
    costIncrease: 20.0
  namespaceSelector:
    matchLabels:
      environment: development

---
# Development PowerPolicy
apiVersion: kcloud.io/v1alpha1
kind: PowerPolicy
metadata:
  name: dev-power-policy
  namespace: development
spec:
  maxPowerUsage: 200.0
  efficiencyTarget: 70.0
  greenEnergyPolicy:
    enabled: false
    preference: any
  alertThresholds:
    powerUsage: 90.0
    efficiency: 60.0
  namespaceSelector:
    matchLabels:
      environment: development
```

### Production Environment

```yaml
# Production CostPolicy
apiVersion: kcloud.io/v1alpha1
kind: CostPolicy
metadata:
  name: prod-cost-policy
  namespace: production
spec:
  budgetLimit: 10000.0
  costPerHourLimit: 100.0
  spotInstancePolicy:
    enabled: false
    maxPrice: 0.0
  alertThresholds:
    budgetUtilization: 85.0
    costIncrease: 15.0
  namespaceSelector:
    matchLabels:
      environment: production

---
# Production PowerPolicy
apiVersion: kcloud.io/v1alpha1
kind: PowerPolicy
metadata:
  name: prod-power-policy
  namespace: production
spec:
  maxPowerUsage: 2000.0
  efficiencyTarget: 85.0
  greenEnergyPolicy:
    enabled: true
    preference: renewable
  alertThresholds:
    powerUsage: 90.0
    efficiency: 80.0
  namespaceSelector:
    matchLabels:
      environment: production
```

## Cost Optimization

### Spot Instance Optimization

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: spot-optimized-workload
  namespace: cost-optimized
spec:
  workloadType: batch
  priority: 3
  resourceRequirements:
    cpu: "4"
    memory: "16Gi"
    gpu: 0
    npu: 0
  costConstraints:
    maxCostPerHour: 5.0
    budgetLimit: 500.0
  powerConstraints:
    maxPowerUsage: 200.0
  placementPolicy:
    nodeAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        preference:
          matchExpressions:
          - key: instance-type
            operator: In
            values: ["spot"]
```

### Budget-Aware Workload

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: budget-aware-workload
  namespace: cost-optimized
spec:
  workloadType: training
  priority: 5
  resourceRequirements:
    cpu: "8"
    memory: "32Gi"
    gpu: 2
    npu: 0
  costConstraints:
    maxCostPerHour: 20.0
    budgetLimit: 2000.0
  powerConstraints:
    maxPowerUsage: 1000.0
  autoScaling:
    enabled: true
    minReplicas: 1
    maxReplicas: 5
    targetCPU: 75
    targetMemory: 80
```

## Power Optimization

### Green Computing Workload

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: green-computing-workload
  namespace: green-computing
spec:
  workloadType: training
  priority: 6
  resourceRequirements:
    cpu: "16"
    memory: "64Gi"
    gpu: 4
    npu: 0
  costConstraints:
    maxCostPerHour: 50.0
    budgetLimit: 3000.0
  powerConstraints:
    maxPowerUsage: 2000.0
  placementPolicy:
    nodeAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        preference:
          matchExpressions:
          - key: energy-source
            operator: In
            values: ["renewable"]
```

### Power-Efficient Inference

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: power-efficient-inference
  namespace: power-optimized
spec:
  workloadType: inference
  priority: 7
  resourceRequirements:
    cpu: "4"
    memory: "16Gi"
    gpu: 1
    npu: 0
  costConstraints:
    maxCostPerHour: 15.0
    budgetLimit: 1500.0
  powerConstraints:
    maxPowerUsage: 300.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: power-efficiency
            operator: Gt
            values: ["80"]
```

## Advanced Scheduling

### Multi-GPU Workload

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: multi-gpu-workload
  namespace: advanced-scheduling
spec:
  workloadType: training
  priority: 8
  resourceRequirements:
    cpu: "32"
    memory: "128Gi"
    gpu: 8
    npu: 0
  costConstraints:
    maxCostPerHour: 100.0
    budgetLimit: 5000.0
  powerConstraints:
    maxPowerUsage: 4000.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: gpu.nvidia.com/class
            operator: In
            values: ["compute"]
          - key: gpu.nvidia.com/memory
            operator: Gt
            values: ["24000"]
    nodeAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchExpressions:
            - key: workload-type
              operator: In
              values: ["training"]
          topologyKey: kubernetes.io/hostname
```

### NPU-Optimized Workload

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: npu-optimized-workload
  namespace: advanced-scheduling
spec:
  workloadType: inference
  priority: 9
  resourceRequirements:
    cpu: "8"
    memory: "32Gi"
    gpu: 0
    npu: 4
  costConstraints:
    maxCostPerHour: 30.0
    budgetLimit: 2000.0
  powerConstraints:
    maxPowerUsage: 800.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: accelerator
            operator: In
            values: ["npu"]
          - key: npu.vendor
            operator: In
            values: ["huawei", "cambricon"]
```

## Monitoring and Alerting

### Prometheus Monitoring Setup

```yaml
# ServiceMonitor for Prometheus
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

---
# PrometheusRule for alerting
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
    
    - alert: WorkloadOptimizerHighPowerUsage
      expr: kcloud_workloadoptimizer_current_power > 1000
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "WorkloadOptimizer power usage is high"
        description: "WorkloadOptimizer {{ $labels.name }} has high power usage: {{ $value }}W"
    
    - alert: CostPolicyBudgetExceeded
      expr: kcloud_costpolicy_budget_utilization > 90
      for: 2m
      labels:
        severity: critical
      annotations:
        summary: "Cost policy budget exceeded"
        description: "Cost policy {{ $labels.name }} budget utilization: {{ $value }}%"
```

### Grafana Dashboard

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kcloud-operator-dashboard
  namespace: kcloud-operator-system
  labels:
    grafana_dashboard: "1"
data:
  dashboard.json: |
    {
      "dashboard": {
        "title": "KCloud Workload Optimizer",
        "panels": [
          {
            "title": "WorkloadOptimizer Status",
            "type": "stat",
            "targets": [
              {
                "expr": "kcloud_workloadoptimizer_total",
                "legendFormat": "Total WorkloadOptimizers"
              }
            ]
          },
          {
            "title": "Cost Optimization",
            "type": "graph",
            "targets": [
              {
                "expr": "kcloud_workloadoptimizer_current_cost",
                "legendFormat": "{{ name }}"
              }
            ]
          },
          {
            "title": "Power Usage",
            "type": "graph",
            "targets": [
              {
                "expr": "kcloud_workloadoptimizer_current_power",
                "legendFormat": "{{ name }}"
              }
            ]
          }
        ]
      }
    }
```

## Troubleshooting

### Common Issues

1. **WorkloadOptimizer stuck in Pending phase**
   ```bash
   kubectl describe workloadoptimizer <name>
   kubectl get events --field-selector involvedObject.name=<name>
   ```

2. **High cost alerts**
   ```bash
   kubectl get costpolicy
   kubectl describe costpolicy <name>
   ```

3. **Power usage violations**
   ```bash
   kubectl get powerpolicy
   kubectl describe powerpolicy <name>
   ```

4. **Scheduling failures**
   ```bash
   kubectl get nodes
   kubectl describe nodes
   kubectl get pods --all-namespaces | grep Pending
   ```

### Debug Commands

```bash
# Check operator logs
kubectl logs -n kcloud-operator-system deployment/kcloud-operator -f

# Check CRD status
kubectl get crd | grep kcloud.io

# Check webhook configuration
kubectl get mutatingwebhookconfigurations
kubectl get validatingwebhookconfigurations

# Check metrics endpoint
kubectl port-forward -n kcloud-operator-system svc/kcloud-operator 8080:8080
curl http://localhost:8080/metrics
```
