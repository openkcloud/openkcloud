/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// SystemMetricsCollector collects system-wide metrics from Kubernetes
type SystemMetricsCollector struct {
	client  client.Client
	metrics *MetricsCollector
}

// NewSystemMetricsCollector creates a new system metrics collector
func NewSystemMetricsCollector(client client.Client, metrics *MetricsCollector) *SystemMetricsCollector {
	return &SystemMetricsCollector{
		client:  client,
		metrics: metrics,
	}
}

// CollectNodeMetrics collects metrics from all nodes
func (smc *SystemMetricsCollector) CollectNodeMetrics(ctx context.Context) error {
	log := log.FromContext(ctx)

	var nodes corev1.NodeList
	if err := smc.client.List(ctx, &nodes); err != nil {
		log.Error(err, "Failed to list nodes")
		return err
	}

	for _, node := range nodes.Items {
		smc.collectNodeMetrics(ctx, &node)
	}

	return nil
}

// collectNodeMetrics collects metrics for a single node
func (smc *SystemMetricsCollector) collectNodeMetrics(ctx context.Context, node *corev1.Node) {
	log := log.FromContext(ctx)

	nodeName := node.Name
	instanceType := smc.getInstanceType(node)

	// Calculate CPU utilization
	cpuUtilization := smc.calculateCPUUtilization(node)
	smc.metrics.RecordNodeMetrics(nodeName, "cpu", cpuUtilization, 0, 0, instanceType)

	// Calculate memory utilization
	memoryUtilization := smc.calculateMemoryUtilization(node)
	smc.metrics.RecordNodeMetrics(nodeName, "memory", memoryUtilization, 0, 0, instanceType)

	// Estimate cost and power (simplified)
	cost := smc.estimateNodeCost(node)
	power := smc.estimateNodePower(node)
	smc.metrics.RecordNodeMetrics(nodeName, "overall", (cpuUtilization+memoryUtilization)/2, cost, power, instanceType)

	log.V(1).Info("Collected node metrics",
		"node", nodeName,
		"cpuUtilization", cpuUtilization,
		"memoryUtilization", memoryUtilization,
		"cost", cost,
		"power", power)
}

// getInstanceType extracts instance type from node labels
func (smc *SystemMetricsCollector) getInstanceType(node *corev1.Node) string {
	if instanceType, exists := node.Labels["node.kubernetes.io/instance-type"]; exists {
		return instanceType
	}
	if instanceType, exists := node.Labels["beta.kubernetes.io/instance-type"]; exists {
		return instanceType
	}
	return "unknown"
}

// calculateCPUUtilization calculates CPU utilization for a node
func (smc *SystemMetricsCollector) calculateCPUUtilization(node *corev1.Node) float64 {
	allocatable := node.Status.Allocatable[corev1.ResourceCPU]
	capacity := node.Status.Capacity[corev1.ResourceCPU]

	if capacity.IsZero() {
		return 0.0
	}

	// Simplified calculation - in reality, this would use metrics server
	_ = allocatable.MilliValue()
	capacityMilli := capacity.MilliValue()

	if capacityMilli == 0 {
		return 0.0
	}

	// Assume 50% utilization for demonstration
	return 0.5
}

// calculateMemoryUtilization calculates memory utilization for a node
func (smc *SystemMetricsCollector) calculateMemoryUtilization(node *corev1.Node) float64 {
	allocatable := node.Status.Allocatable[corev1.ResourceMemory]
	capacity := node.Status.Capacity[corev1.ResourceMemory]

	if capacity.IsZero() {
		return 0.0
	}

	// Simplified calculation - in reality, this would use metrics server
	_ = allocatable.Value()
	capacityBytes := capacity.Value()

	if capacityBytes == 0 {
		return 0.0
	}

	// Assume 60% utilization for demonstration
	return 0.6
}

// estimateNodeCost estimates the cost of a node per hour
func (smc *SystemMetricsCollector) estimateNodeCost(node *corev1.Node) float64 {
	instanceType := smc.getInstanceType(node)

	// Simplified cost estimation based on instance type
	costMap := map[string]float64{
		"t3.micro":     0.0104,
		"t3.small":     0.0208,
		"t3.medium":    0.0416,
		"t3.large":     0.0832,
		"t3.xlarge":    0.1664,
		"m5.large":     0.096,
		"m5.xlarge":    0.192,
		"m5.2xlarge":   0.384,
		"c5.large":     0.085,
		"c5.xlarge":    0.17,
		"c5.2xlarge":   0.34,
		"g4dn.xlarge":  0.526,
		"g4dn.2xlarge": 1.052,
	}

	if cost, exists := costMap[instanceType]; exists {
		return cost
	}

	// Default cost for unknown instance types
	return 0.1
}

// estimateNodePower estimates the power consumption of a node
func (smc *SystemMetricsCollector) estimateNodePower(node *corev1.Node) float64 {
	instanceType := smc.getInstanceType(node)

	// Simplified power estimation based on instance type
	powerMap := map[string]float64{
		"t3.micro":     10,
		"t3.small":     20,
		"t3.medium":    40,
		"t3.large":     80,
		"t3.xlarge":    160,
		"m5.large":     100,
		"m5.xlarge":    200,
		"m5.2xlarge":   400,
		"c5.large":     90,
		"c5.xlarge":    180,
		"c5.2xlarge":   360,
		"g4dn.xlarge":  300,
		"g4dn.2xlarge": 600,
	}

	if power, exists := powerMap[instanceType]; exists {
		return power
	}

	// Default power for unknown instance types
	return 100
}

// CollectWorkloadOptimizerMetrics collects metrics from WorkloadOptimizer resources
func (smc *SystemMetricsCollector) CollectWorkloadOptimizerMetrics(ctx context.Context) error {
	log := log.FromContext(ctx)

	var workloads kcloudv1alpha1.WorkloadOptimizerList
	if err := smc.client.List(ctx, &workloads); err != nil {
		log.Error(err, "Failed to list WorkloadOptimizer resources")
		return err
	}

	for _, workload := range workloads.Items {
		smc.collectWorkloadOptimizerMetrics(ctx, &workload)
	}

	return nil
}

// collectWorkloadOptimizerMetrics collects metrics for a single WorkloadOptimizer
func (smc *SystemMetricsCollector) collectWorkloadOptimizerMetrics(ctx context.Context, workload *kcloudv1alpha1.WorkloadOptimizer) {
	log := log.FromContext(ctx)

	namespace := workload.Namespace
	name := workload.Name
	workloadType := workload.Spec.WorkloadType

	// Record phase
	if workload.Status.Phase != "" {
		smc.metrics.RecordWorkloadOptimizerPhase(namespace, name, workload.Status.Phase, workloadType)
	}

	// Record optimization score
	if workload.Status.OptimizationScore != nil {
		smc.metrics.RecordWorkloadOptimizerScore(namespace, name, workloadType, *workload.Status.OptimizationScore)
	}

	// Record cost
	if workload.Status.CurrentCost != nil {
		smc.metrics.RecordWorkloadOptimizerCost(namespace, name, workloadType, *workload.Status.CurrentCost)
	}

	// Record power
	if workload.Status.CurrentPower != nil {
		smc.metrics.RecordWorkloadOptimizerPower(namespace, name, workloadType, *workload.Status.CurrentPower)
	}

	log.V(1).Info("Collected WorkloadOptimizer metrics",
		"namespace", namespace,
		"name", name,
		"workloadType", workloadType,
		"phase", workload.Status.Phase)
}

// CollectPolicyMetrics collects metrics from policy resources
func (smc *SystemMetricsCollector) CollectPolicyMetrics(ctx context.Context) error {
	log := log.FromContext(ctx)

	// Collect CostPolicy metrics
	var costPolicies kcloudv1alpha1.CostPolicyList
	if err := smc.client.List(ctx, &costPolicies); err != nil {
		log.Error(err, "Failed to list CostPolicy resources")
	} else {
		for _, policy := range costPolicies.Items {
			smc.collectCostPolicyMetrics(ctx, &policy)
		}
	}

	// Collect PowerPolicy metrics
	var powerPolicies kcloudv1alpha1.PowerPolicyList
	if err := smc.client.List(ctx, &powerPolicies); err != nil {
		log.Error(err, "Failed to list PowerPolicy resources")
	} else {
		for _, policy := range powerPolicies.Items {
			smc.collectPowerPolicyMetrics(ctx, &policy)
		}
	}

	return nil
}

// collectCostPolicyMetrics collects metrics for a CostPolicy
func (smc *SystemMetricsCollector) collectCostPolicyMetrics(ctx context.Context, policy *kcloudv1alpha1.CostPolicy) {
	log := log.FromContext(ctx)

	policyName := policy.Name
	policyType := "cost"

	// Record budget utilization
	if policy.Status.BudgetUtilization != nil {
		smc.metrics.RecordBudgetUtilization(policy.Namespace, policyName, "policy", *policy.Status.BudgetUtilization)
	}

	// Record compliance (simplified)
	compliance := 1.0
	if policy.Status.Phase == "Violated" {
		compliance = 0.0
	}
	smc.metrics.RecordPolicyCompliance(policyType, policyName, compliance)

	log.V(1).Info("Collected CostPolicy metrics",
		"namespace", policy.Namespace,
		"name", policyName,
		"phase", policy.Status.Phase,
		"compliance", compliance)
}

// collectPowerPolicyMetrics collects metrics for a PowerPolicy
func (smc *SystemMetricsCollector) collectPowerPolicyMetrics(ctx context.Context, policy *kcloudv1alpha1.PowerPolicy) {
	log := log.FromContext(ctx)

	policyName := policy.Name
	policyType := "power"

	// Record power efficiency
	// TODO: Add efficiency field to PowerPolicyStatus
	// if policy.Status.Efficiency != nil {
	//	smc.metrics.RecordPowerEfficiency(policy.Namespace, policyName, "policy", *policy.Status.Efficiency)
	// }

	// Record compliance (simplified)
	compliance := 1.0
	if policy.Status.Phase == "Violated" {
		compliance = 0.0
	}
	smc.metrics.RecordPolicyCompliance(policyType, policyName, compliance)

	log.V(1).Info("Collected PowerPolicy metrics",
		"namespace", policy.Namespace,
		"name", policyName,
		"phase", policy.Status.Phase,
		"compliance", compliance)
}

// CollectPodMetrics collects metrics from pods
func (smc *SystemMetricsCollector) CollectPodMetrics(ctx context.Context) error {
	log := log.FromContext(ctx)

	var pods corev1.PodList
	if err := smc.client.List(ctx, &pods); err != nil {
		log.Error(err, "Failed to list pods")
		return err
	}

	optimizedPods := 0
	totalPods := len(pods.Items)

	for _, pod := range pods.Items {
		if pod.Annotations != nil {
			if optimized, exists := pod.Annotations["kcloud.io/optimized"]; exists && optimized == "true" {
				optimizedPods++
			}
		}
	}

	// Record optimization ratio
	if totalPods > 0 {
		optimizationRatio := float64(optimizedPods) / float64(totalPods)
		log.V(1).Info("Pod optimization metrics",
			"totalPods", totalPods,
			"optimizedPods", optimizedPods,
			"optimizationRatio", optimizationRatio)
	}

	return nil
}

// StartPeriodicCollection starts periodic collection of all metrics
func (smc *SystemMetricsCollector) StartPeriodicCollection(ctx context.Context) {
	log := log.FromContext(ctx)

	ticker := time.NewTicker(60 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("Periodic metrics collection stopped")
				return
			case <-ticker.C:
				smc.collectAllMetrics(ctx)
			}
		}
	}()

	log.Info("Periodic metrics collection started")
}

// collectAllMetrics collects all system metrics
func (smc *SystemMetricsCollector) collectAllMetrics(ctx context.Context) {
	log := log.FromContext(ctx)

	log.V(1).Info("Starting periodic metrics collection")

	// Collect node metrics
	if err := smc.CollectNodeMetrics(ctx); err != nil {
		log.Error(err, "Failed to collect node metrics")
	}

	// Collect WorkloadOptimizer metrics
	if err := smc.CollectWorkloadOptimizerMetrics(ctx); err != nil {
		log.Error(err, "Failed to collect WorkloadOptimizer metrics")
	}

	// Collect policy metrics
	if err := smc.CollectPolicyMetrics(ctx); err != nil {
		log.Error(err, "Failed to collect policy metrics")
	}

	// Collect pod metrics
	if err := smc.CollectPodMetrics(ctx); err != nil {
		log.Error(err, "Failed to collect pod metrics")
	}

	log.V(1).Info("Completed periodic metrics collection")
}
