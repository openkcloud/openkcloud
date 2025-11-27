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

package scheduler

import (
	"context"
	"fmt"
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// Scheduler handles workload scheduling decisions
type Scheduler struct {
	// Scheduling policies and preferences
	preferSpotInstances bool
	preferGreenEnergy   bool
}

// SchedulingDecision represents a scheduling decision
type SchedulingDecision struct {
	SelectedNode   string
	Score          float64
	Reason         string
	EstimatedCost  float64
	EstimatedPower float64
}

// NewScheduler creates a new scheduler instance
func NewScheduler() *Scheduler {
	return &Scheduler{
		preferSpotInstances: true,
		preferGreenEnergy:   true,
	}
}

// ScheduleWorkload schedules a workload to the best available node
func (s *Scheduler) ScheduleWorkload(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer, nodes []corev1.Node) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no available nodes for scheduling")
	}

	var bestDecision *SchedulingDecision
	bestScore := -1.0

	log.Info("Evaluating nodes for scheduling", "nodeCount", len(nodes))

	for _, node := range nodes {
		decision, err := s.evaluateNode(ctx, wo, node)
		if err != nil {
			log.Error(err, "Failed to evaluate node", "node", node.Name)
			continue
		}

		if decision.Score > bestScore {
			bestScore = decision.Score
			bestDecision = decision
		}
	}

	if bestDecision == nil {
		return nil, fmt.Errorf("no suitable node found for scheduling")
	}

	log.Info("Scheduling decision made",
		"selectedNode", bestDecision.SelectedNode,
		"score", bestDecision.Score,
		"reason", bestDecision.Reason)

	return bestDecision, nil
}

// evaluateNode evaluates a single node for workload scheduling
func (s *Scheduler) evaluateNode(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	// Check if node meets basic requirements
	if !s.nodeMeetsRequirements(wo, node) {
		return &SchedulingDecision{
			SelectedNode: node.Name,
			Score:        0.0,
			Reason:       "Node does not meet basic requirements",
		}, nil
	}

	// Calculate resource availability score
	resourceScore := s.calculateResourceScore(wo, node)

	// Calculate cost efficiency score
	costScore := s.calculateCostScore(wo, node)

	// Calculate power efficiency score
	powerScore := s.calculatePowerScore(wo, node)

	// Calculate placement policy score
	placementScore := s.calculatePlacementScore(wo, node)

	// Calculate final score (weighted average)
	finalScore := (resourceScore*0.4 + costScore*0.3 + powerScore*0.2 + placementScore*0.1)

	// Estimate cost and power for this node
	estimatedCost := s.estimateNodeCost(wo, node)
	estimatedPower := s.estimateNodePower(wo, node)

	decision := &SchedulingDecision{
		SelectedNode:   node.Name,
		Score:          finalScore,
		Reason:         s.generateReason(resourceScore, costScore, powerScore, placementScore),
		EstimatedCost:  estimatedCost,
		EstimatedPower: estimatedPower,
	}

	log.V(1).Info("Node evaluation completed",
		"node", node.Name,
		"resourceScore", resourceScore,
		"costScore", costScore,
		"powerScore", powerScore,
		"placementScore", placementScore,
		"finalScore", finalScore)

	return decision, nil
}

// nodeMeetsRequirements checks if a node meets the basic requirements
func (s *Scheduler) nodeMeetsRequirements(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) bool {
	// Check if node is ready
	if !s.isNodeReady(node) {
		return false
	}

	// Check node selector requirements
	if wo.Spec.PlacementPolicy != nil && wo.Spec.PlacementPolicy.NodeSelector != nil {
		for key, value := range wo.Spec.PlacementPolicy.NodeSelector {
			if node.Labels[key] != value {
				return false
			}
		}
	}

	// Check resource requirements
	return s.hasSufficientResources(wo, node)
}

// isNodeReady checks if a node is in ready state
func (s *Scheduler) isNodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// hasSufficientResources checks if node has sufficient resources
func (s *Scheduler) hasSufficientResources(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) bool {
	// Parse required resources
	cpuReq := s.parseResourceQuantity(wo.Spec.Resources.CPU)
	memoryReq := s.parseResourceQuantity(wo.Spec.Resources.Memory)

	// Get available resources
	cpuAvail := node.Status.Allocatable[corev1.ResourceCPU]
	memoryAvail := node.Status.Allocatable[corev1.ResourceMemory]

	// Check CPU
	if cpuReq.Cmp(cpuAvail) > 0 {
		return false
	}

	// Check Memory
	if memoryReq.Cmp(memoryAvail) > 0 {
		return false
	}

	// Check GPU if required
	if wo.Spec.Resources.GPU > 0 {
		gpuAvail := node.Status.Allocatable["nvidia.com/gpu"]
		gpuReq := resource.MustParse(fmt.Sprintf("%d", wo.Spec.Resources.GPU))
		if gpuReq.Cmp(gpuAvail) > 0 {
			return false
		}
	}

	// Check NPU if required
	if wo.Spec.Resources.NPU > 0 {
		npuAvail := node.Status.Allocatable["npu.com/npu"]
		npuReq := resource.MustParse(fmt.Sprintf("%d", wo.Spec.Resources.NPU))
		if npuReq.Cmp(npuAvail) > 0 {
			return false
		}
	}

	return true
}

// calculateResourceScore calculates resource availability score
func (s *Scheduler) calculateResourceScore(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) float64 {
	cpuReq := s.parseResourceQuantity(wo.Spec.Resources.CPU)
	memoryReq := s.parseResourceQuantity(wo.Spec.Resources.Memory)

	cpuAvail := node.Status.Allocatable[corev1.ResourceCPU]
	memoryAvail := node.Status.Allocatable[corev1.ResourceMemory]

	// Calculate utilization ratios
	cpuUtilization := float64(cpuReq.MilliValue()) / float64(cpuAvail.MilliValue())
	memoryUtilization := float64(memoryReq.Value()) / float64(memoryAvail.Value())

	// Score based on utilization (lower utilization = higher score)
	cpuScore := math.Max(0, 1.0-cpuUtilization)
	memoryScore := math.Max(0, 1.0-memoryUtilization)

	return (cpuScore + memoryScore) / 2.0
}

// calculateCostScore calculates cost efficiency score
func (s *Scheduler) calculateCostScore(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) float64 {
	score := 0.5 // Base score

	// Prefer spot instances if configured
	if s.preferSpotInstances && wo.Spec.CostConstraints != nil && wo.Spec.CostConstraints.PreferSpot {
		if node.Labels["node.kubernetes.io/instance-type"] != "" {
			// Check if it's a spot instance (simplified check)
			if node.Labels["lifecycle"] == "spot" {
				score += 0.3
			}
		}
	}

	// Prefer nodes with lower cost labels
	if costLabel, exists := node.Labels["cost-per-hour"]; exists {
		// Lower cost = higher score (simplified)
		if costLabel == "low" {
			score += 0.2
		} else if costLabel == "medium" {
			score += 0.1
		}
	}

	return math.Min(1.0, score)
}

// calculatePowerScore calculates power efficiency score
func (s *Scheduler) calculatePowerScore(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) float64 {
	score := 0.5 // Base score

	// Prefer green energy if configured
	if s.preferGreenEnergy && wo.Spec.PowerConstraints != nil && wo.Spec.PowerConstraints.PreferGreen {
		if node.Labels["energy-source"] == "renewable" {
			score += 0.3
		}
	}

	// Prefer nodes with lower power consumption
	if powerLabel, exists := node.Labels["power-efficiency"]; exists {
		if powerLabel == "high" {
			score += 0.2
		} else if powerLabel == "medium" {
			score += 0.1
		}
	}

	return math.Min(1.0, score)
}

// calculatePlacementScore calculates placement policy score
func (s *Scheduler) calculatePlacementScore(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) float64 {
	if wo.Spec.PlacementPolicy == nil || len(wo.Spec.PlacementPolicy.Affinity) == 0 {
		return 0.5 // Neutral score if no affinity rules
	}

	totalWeight := 0.0
	totalScore := 0.0

	for _, affinity := range wo.Spec.PlacementPolicy.Affinity {
		weight := float64(affinity.Weight)
		if weight == 0 {
			weight = 50.0 // Default weight
		}

		// Check if node matches affinity
		if node.Labels[affinity.Key] == affinity.Value {
			totalScore += weight
		}
		totalWeight += weight
	}

	if totalWeight == 0 {
		return 0.5
	}

	return totalScore / totalWeight
}

// estimateNodeCost estimates the cost for running workload on this node
func (s *Scheduler) estimateNodeCost(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) float64 {
	// Base cost estimation (simplified)
	baseCost := 10.0 // Base cost per hour

	// Adjust based on node type
	if instanceType, exists := node.Labels["node.kubernetes.io/instance-type"]; exists {
		switch instanceType {
		case "gpu-node":
			baseCost += 20.0
		case "cpu-intensive":
			baseCost += 5.0
		case "memory-intensive":
			baseCost += 8.0
		}
	}

	// Adjust based on spot instance preference
	if wo.Spec.CostConstraints != nil && wo.Spec.CostConstraints.PreferSpot {
		if node.Labels["lifecycle"] == "spot" {
			baseCost *= 0.7 // 30% discount for spot instances
		}
	}

	return baseCost
}

// estimateNodePower estimates the power consumption for running workload on this node
func (s *Scheduler) estimateNodePower(wo *kcloudv1alpha1.WorkloadOptimizer, node corev1.Node) float64 {
	// Base power estimation (simplified)
	basePower := 100.0 // Base power in Watts

	// Add power for GPU
	if wo.Spec.Resources.GPU > 0 {
		basePower += float64(wo.Spec.Resources.GPU) * 300.0 // 300W per GPU
	}

	// Add power for NPU
	if wo.Spec.Resources.NPU > 0 {
		basePower += float64(wo.Spec.Resources.NPU) * 250.0 // 250W per NPU
	}

	// Adjust based on energy source
	if wo.Spec.PowerConstraints != nil && wo.Spec.PowerConstraints.PreferGreen {
		if node.Labels["energy-source"] == "renewable" {
			basePower *= 0.9 // 10% reduction for green energy
		}
	}

	return basePower
}

// generateReason generates a human-readable reason for the scheduling decision
func (s *Scheduler) generateReason(resourceScore, costScore, powerScore, placementScore float64) string {
	reasons := []string{}

	if resourceScore > 0.8 {
		reasons = append(reasons, "excellent resource availability")
	} else if resourceScore > 0.6 {
		reasons = append(reasons, "good resource availability")
	}

	if costScore > 0.8 {
		reasons = append(reasons, "cost efficient")
	}

	if powerScore > 0.8 {
		reasons = append(reasons, "power efficient")
	}

	if placementScore > 0.8 {
		reasons = append(reasons, "meets placement requirements")
	}

	if len(reasons) == 0 {
		return "meets basic requirements"
	}

	return fmt.Sprintf("selected because: %s", fmt.Sprintf("%s", reasons))
}

// parseResourceQuantity parses a resource quantity string
func (s *Scheduler) parseResourceQuantity(quantity string) resource.Quantity {
	q, err := resource.ParseQuantity(quantity)
	if err != nil {
		// Return zero quantity if parsing fails
		return resource.MustParse("0")
	}
	return q
}
