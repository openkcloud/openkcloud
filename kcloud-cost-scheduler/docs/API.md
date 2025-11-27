# KCloud Workload Optimizer Operator API Documentation

## Overview

This document provides comprehensive API documentation for the KCloud Workload Optimizer Operator, including all Custom Resource Definitions (CRDs), their specifications, and usage examples.

## Table of Contents

- [WorkloadOptimizer](#workloadoptimizer)
- [CostPolicy](#costpolicy)
- [PowerPolicy](#powerpolicy)
- [API Examples](#api-examples)
- [Best Practices](#best-practices)

## WorkloadOptimizer

The `WorkloadOptimizer` CRD defines optimization policies for individual workloads, including resource requirements, cost constraints, power constraints, and placement policies.

### Specification

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: <workload-name>
  namespace: <namespace>
spec:
  workloadType: <workload-type>
  priority: <priority>
  resourceRequirements:
    cpu: <cpu-requirement>
    memory: <memory-requirement>
    gpu: <gpu-count>
    npu: <npu-count>
  costConstraints:
    maxCostPerHour: <max-cost-per-hour>
    budgetLimit: <budget-limit>
  powerConstraints:
    maxPowerUsage: <max-power-usage>
  placementPolicy:
    nodeAffinity: <node-affinity>
    nodeAntiAffinity: <node-anti-affinity>
  autoScaling:
    enabled: <boolean>
    minReplicas: <min-replicas>
    maxReplicas: <max-replicas>
    targetCPU: <target-cpu-percentage>
    targetMemory: <target-memory-percentage>
status:
  phase: <phase>
  optimizationScore: <score>
  currentCost: <current-cost>
  currentPower: <current-power>
  assignedNode: <assigned-node>
  conditions: <conditions>
  lastUpdated: <timestamp>
```

### Fields

#### spec.workloadType
- **Type**: `string`
- **Required**: `true`
- **Enum**: `training`, `inference`, `batch`, `streaming`
- **Description**: Type of workload to optimize
- **Default**: `training`

#### spec.priority
- **Type**: `integer`
- **Required**: `true`
- **Range**: `1-10`
- **Description**: Workload priority (higher is more important)
- **Default**: `5`

#### spec.resourceRequirements
- **Type**: `object`
- **Required**: `true`
- **Description**: Resource requirements for the workload

##### spec.resourceRequirements.cpu
- **Type**: `string`
- **Required**: `true`
- **Description**: CPU requirement (e.g., "2", "100m")
- **Example**: `"4"`, `"500m"`

##### spec.resourceRequirements.memory
- **Type**: `string`
- **Required**: `true`
- **Description**: Memory requirement (e.g., "4Gi", "512Mi")
- **Example**: `"8Gi"`, `"1Gi"`

##### spec.resourceRequirements.gpu
- **Type**: `integer`
- **Required**: `false`
- **Range**: `0-16`
- **Description**: Number of GPUs required
- **Default**: `0`

##### spec.resourceRequirements.npu
- **Type**: `integer`
- **Required**: `false`
- **Range**: `0-16`
- **Description**: Number of NPUs required
- **Default**: `0`

#### spec.costConstraints
- **Type**: `object`
- **Required**: `false`
- **Description**: Cost optimization constraints

##### spec.costConstraints.maxCostPerHour
- **Type**: `number`
- **Required**: `false`
- **Range**: `0-10000`
- **Description**: Maximum cost per hour in USD
- **Default**: `10.0`

##### spec.costConstraints.budgetLimit
- **Type**: `number`
- **Required**: `false`
- **Range**: `0+`
- **Description**: Total budget limit in USD
- **Default**: `1000.0`

#### spec.powerConstraints
- **Type**: `object`
- **Required**: `false`
- **Description**: Power optimization constraints

##### spec.powerConstraints.maxPowerUsage
- **Type**: `number`
- **Required**: `false`
- **Range**: `0-10000`
- **Description**: Maximum power usage in watts
- **Default**: `500.0`

#### spec.placementPolicy
- **Type**: `object`
- **Required**: `false`
- **Description**: Node placement policy

##### spec.placementPolicy.nodeAffinity
- **Type**: `NodeAffinity`
- **Required**: `false`
- **Description**: Node affinity rules for scheduling

##### spec.placementPolicy.nodeAntiAffinity
- **Type**: `NodeAntiAffinity`
- **Required**: `false`
- **Description**: Node anti-affinity rules for scheduling

#### spec.autoScaling
- **Type**: `object`
- **Required**: `false`
- **Description**: Auto-scaling configuration

##### spec.autoScaling.enabled
- **Type**: `boolean`
- **Required**: `false`
- **Description**: Enable auto-scaling
- **Default**: `false`

##### spec.autoScaling.minReplicas
- **Type**: `integer`
- **Required**: `false`
- **Range**: `1+`
- **Description**: Minimum number of replicas
- **Default**: `1`

##### spec.autoScaling.maxReplicas
- **Type**: `integer`
- **Required**: `false`
- **Range**: `1+`
- **Description**: Maximum number of replicas
- **Default**: `10`

##### spec.autoScaling.targetCPU
- **Type**: `integer`
- **Required**: `false`
- **Range**: `1-100`
- **Description**: Target CPU utilization percentage
- **Default**: `70`

##### spec.autoScaling.targetMemory
- **Type**: `integer`
- **Required**: `false`
- **Range**: `1-100`
- **Description**: Target memory utilization percentage
- **Default**: `80`

### Status Fields

#### status.phase
- **Type**: `string`
- **Enum**: `Pending`, `Optimizing`, `Optimized`, `Failed`, `Suspended`
- **Description**: Current phase of the WorkloadOptimizer
- **Default**: `Pending`

#### status.optimizationScore
- **Type**: `number`
- **Range**: `0.0-1.0`
- **Description**: Current optimization score
- **Default**: `0.0`

#### status.currentCost
- **Type**: `number`
- **Description**: Current estimated cost per hour in USD

#### status.currentPower
- **Type**: `number`
- **Description**: Current estimated power usage in watts

#### status.assignedNode
- **Type**: `string`
- **Description**: Node where the workload is currently assigned

#### status.conditions
- **Type**: `array`
- **Description**: Current conditions of the WorkloadOptimizer

#### status.lastUpdated
- **Type**: `string`
- **Format**: `date-time`
- **Description**: Last time the status was updated

## CostPolicy

The `CostPolicy` CRD defines cost management policies that can be applied to multiple workloads or namespaces.

### Specification

```yaml
apiVersion: kcloud.io/v1alpha1
kind: CostPolicy
metadata:
  name: <policy-name>
  namespace: <namespace>
spec:
  budgetLimit: <budget-limit>
  costPerHourLimit: <cost-per-hour-limit>
  spotInstancePolicy:
    enabled: <boolean>
    maxPrice: <max-price>
  alertThresholds:
    budgetUtilization: <budget-utilization-percentage>
    costIncrease: <cost-increase-percentage>
  namespaceSelector:
    matchLabels: <match-labels>
    matchExpressions: <match-expressions>
status:
  phase: <phase>
  currentCost: <current-cost>
  totalBudgetUsed: <total-budget-used>
  conditions: <conditions>
  lastUpdated: <timestamp>
```

### Fields

#### spec.budgetLimit
- **Type**: `number`
- **Required**: `true`
- **Range**: `0+`
- **Description**: Maximum budget limit in USD
- **Example**: `1000.0`

#### spec.costPerHourLimit
- **Type**: `number`
- **Required**: `true`
- **Range**: `0-10000`
- **Description**: Maximum cost per hour in USD
- **Example**: `10.0`

#### spec.spotInstancePolicy
- **Type**: `object`
- **Required**: `false`
- **Description**: Spot instance policy configuration

##### spec.spotInstancePolicy.enabled
- **Type**: `boolean`
- **Required**: `false`
- **Description**: Enable spot instances
- **Default**: `false`

##### spec.spotInstancePolicy.maxPrice
- **Type**: `number`
- **Required**: `false`
- **Range**: `0+`
- **Description**: Maximum price for spot instances
- **Default**: `5.0`

#### spec.alertThresholds
- **Type**: `object`
- **Required**: `false`
- **Description**: Alert thresholds for cost-related metrics

##### spec.alertThresholds.budgetUtilization
- **Type**: `number`
- **Required**: `false`
- **Range**: `0-100`
- **Description**: Budget utilization percentage threshold
- **Default**: `80.0`

##### spec.alertThresholds.costIncrease
- **Type**: `number`
- **Required**: `false`
- **Range**: `0-1000`
- **Description**: Cost increase percentage threshold
- **Default**: `20.0`

#### spec.namespaceSelector
- **Type**: `LabelSelector`
- **Required**: `false`
- **Description**: Selector for namespaces this policy applies to

### Status Fields

#### status.phase
- **Type**: `string`
- **Enum**: `Pending`, `Active`, `Violated`, `Suspended`
- **Description**: Current phase of the CostPolicy
- **Default**: `Pending`

#### status.currentCost
- **Type**: `number`
- **Description**: Current cost per hour in USD

#### status.totalBudgetUsed
- **Type**: `number`
- **Description**: Total budget used so far in USD

## PowerPolicy

The `PowerPolicy` CRD defines power management policies for optimizing energy consumption and efficiency.

### Specification

```yaml
apiVersion: kcloud.io/v1alpha1
kind: PowerPolicy
metadata:
  name: <policy-name>
  namespace: <namespace>
spec:
  maxPowerUsage: <max-power-usage>
  efficiencyTarget: <efficiency-target>
  greenEnergyPolicy:
    enabled: <boolean>
    preference: <preference>
  alertThresholds:
    powerUsage: <power-usage-percentage>
    efficiency: <efficiency-percentage>
  namespaceSelector:
    matchLabels: <match-labels>
    matchExpressions: <match-expressions>
status:
  phase: <phase>
  currentPowerUsage: <current-power-usage>
  currentEfficiency: <current-efficiency>
  conditions: <conditions>
  lastUpdated: <timestamp>
```

### Fields

#### spec.maxPowerUsage
- **Type**: `number`
- **Required**: `true`
- **Range**: `0-10000`
- **Description**: Maximum power usage in watts
- **Example**: `500.0`

#### spec.efficiencyTarget
- **Type**: `number`
- **Required**: `true`
- **Range**: `0-100`
- **Description**: Target efficiency percentage
- **Example**: `80.0`

#### spec.greenEnergyPolicy
- **Type**: `object`
- **Required**: `false`
- **Description**: Green energy policy configuration

##### spec.greenEnergyPolicy.enabled
- **Type**: `boolean`
- **Required**: `false`
- **Description**: Enable green energy preference
- **Default**: `false`

##### spec.greenEnergyPolicy.preference
- **Type**: `string`
- **Required**: `false`
- **Enum**: `renewable`, `low-carbon`, `any`
- **Description**: Preference for green energy sources
- **Default**: `any`

#### spec.alertThresholds
- **Type**: `object`
- **Required**: `false`
- **Description**: Alert thresholds for power-related metrics

##### spec.alertThresholds.powerUsage
- **Type**: `number`
- **Required**: `false`
- **Range**: `0-100`
- **Description**: Power usage percentage threshold
- **Default**: `90.0`

##### spec.alertThresholds.efficiency
- **Type**: `number`
- **Required**: `false`
- **Range**: `0-100`
- **Description**: Efficiency percentage threshold
- **Default**: `70.0`

### Status Fields

#### status.phase
- **Type**: `string`
- **Enum**: `Pending`, `Active`, `Violated`, `Suspended`
- **Description**: Current phase of the PowerPolicy
- **Default**: `Pending`

#### status.currentPowerUsage
- **Type**: `number`
- **Description**: Current power usage in watts

#### status.currentEfficiency
- **Type**: `number`
- **Description**: Current efficiency percentage

## API Examples

### Basic WorkloadOptimizer

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: basic-training
  namespace: default
spec:
  workloadType: training
  priority: 5
  resourceRequirements:
    cpu: "4"
    memory: "8Gi"
    gpu: 1
    npu: 0
  costConstraints:
    maxCostPerHour: 10.0
    budgetLimit: 1000.0
  powerConstraints:
    maxPowerUsage: 500.0
```

### Advanced WorkloadOptimizer with Auto-scaling

```yaml
apiVersion: kcloud.io/v1alpha1
kind: WorkloadOptimizer
metadata:
  name: advanced-inference
  namespace: production
spec:
  workloadType: inference
  priority: 8
  resourceRequirements:
    cpu: "8"
    memory: "16Gi"
    gpu: 2
    npu: 0
  costConstraints:
    maxCostPerHour: 25.0
    budgetLimit: 5000.0
  powerConstraints:
    maxPowerUsage: 1000.0
  placementPolicy:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: node-type
            operator: In
            values: ["gpu-optimized"]
  autoScaling:
    enabled: true
    minReplicas: 2
    maxReplicas: 20
    targetCPU: 75
    targetMemory: 85
```

### CostPolicy for Development Environment

```yaml
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
```

### PowerPolicy for Green Computing

```yaml
apiVersion: kcloud.io/v1alpha1
kind: PowerPolicy
metadata:
  name: green-power-policy
  namespace: production
spec:
  maxPowerUsage: 1000.0
  efficiencyTarget: 85.0
  greenEnergyPolicy:
    enabled: true
    preference: renewable
  alertThresholds:
    powerUsage: 90.0
    efficiency: 75.0
  namespaceSelector:
    matchLabels:
      environment: production
```

## Best Practices

### Resource Requirements

1. **CPU**: Use millicores for fractional CPU (e.g., "500m" for 0.5 CPU)
2. **Memory**: Use standard Kubernetes memory units (e.g., "1Gi", "512Mi")
3. **GPU/NPU**: Specify exact counts based on workload requirements

### Cost Constraints

1. **Budget Planning**: Set realistic budget limits based on historical usage
2. **Spot Instances**: Enable for non-critical workloads to reduce costs
3. **Alert Thresholds**: Set appropriate thresholds for early warning

### Power Constraints

1. **Efficiency Targets**: Set achievable efficiency targets (70-90%)
2. **Green Energy**: Enable for environmentally conscious deployments
3. **Power Monitoring**: Monitor power usage trends and adjust limits

### Placement Policies

1. **Node Affinity**: Use for hardware-specific requirements (GPU, NPU)
2. **Anti-affinity**: Use to distribute workloads across nodes
3. **Taints and Tolerations**: Combine with node taints for specialized nodes

### Auto-scaling

1. **Replica Limits**: Set appropriate min/max replicas based on workload
2. **Target Metrics**: Use CPU and memory targets for scaling decisions
3. **Gradual Scaling**: Avoid aggressive scaling to prevent instability

### Monitoring and Observability

1. **Metrics**: Use Prometheus metrics for monitoring optimization effectiveness
2. **Events**: Monitor Kubernetes events for optimization activities
3. **Logs**: Check operator logs for troubleshooting optimization issues
