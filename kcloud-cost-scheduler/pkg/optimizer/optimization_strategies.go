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

package optimizer

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// OptimizationStrategy defines different optimization strategies
type OptimizationStrategy struct {
	Name        string
	Description string
	Priority    int
	Applicable  func(*kcloudv1alpha1.WorkloadOptimizer) bool
	Execute     func(context.Context, *OptimizationContext) (*StrategyOptimizationResult, error)
}

// OptimizationContext provides context for optimization strategies
type OptimizationContext struct {
	WorkloadOptimizer *kcloudv1alpha1.WorkloadOptimizer
	CurrentPods       []corev1.Pod
	AvailableNodes    []corev1.Node
	CostCalculator    *CostCalculator
	PowerCalculator   *PowerCalculator
	PolicyApplier     *PolicyApplier
}

// StrategyOptimizationResult represents the result of an optimization strategy
type StrategyOptimizationResult struct {
	StrategyName       string
	EstimatedCost      float64
	EstimatedPower     float64
	Score              float64
	Recommendations    []string
	RequiredActions    []string
	Confidence         float64
	ImplementationTime time.Duration
}

// StrategyManager manages optimization strategies
type StrategyManager struct {
	strategies []OptimizationStrategy
}

// NewStrategyManager creates a new strategy manager with predefined strategies
func NewStrategyManager() *StrategyManager {
	sm := &StrategyManager{
		strategies: []OptimizationStrategy{},
	}

	// Add predefined strategies
	sm.addStrategy(sm.createSpotInstanceStrategy())
	sm.addStrategy(sm.createResourceOptimizationStrategy())
	sm.addStrategy(sm.createPowerOptimizationStrategy())
	sm.addStrategy(sm.createAutoScalingStrategy())
	sm.addStrategy(sm.createNodeSelectionStrategy())
	sm.addStrategy(sm.createWorkloadMigrationStrategy())

	return sm
}

// addStrategy adds a strategy to the manager
func (sm *StrategyManager) addStrategy(strategy OptimizationStrategy) {
	sm.strategies = append(sm.strategies, strategy)
}

// FindBestStrategy finds the best optimization strategy for a workload
func (sm *StrategyManager) FindBestStrategy(ctx context.Context, context *OptimizationContext) (*StrategyOptimizationResult, error) {
	log := log.FromContext(ctx)

	var applicableStrategies []OptimizationStrategy
	for _, strategy := range sm.strategies {
		if strategy.Applicable(context.WorkloadOptimizer) {
			applicableStrategies = append(applicableStrategies, strategy)
		}
	}

	if len(applicableStrategies) == 0 {
		return nil, fmt.Errorf("no applicable optimization strategies found")
	}

	// Sort by priority (higher priority first)
	sort.Slice(applicableStrategies, func(i, j int) bool {
		return applicableStrategies[i].Priority > applicableStrategies[j].Priority
	})

	var bestResult *StrategyOptimizationResult
	bestScore := -1.0

	log.Info("Evaluating optimization strategies", "strategyCount", len(applicableStrategies))

	for _, strategy := range applicableStrategies {
		result, err := strategy.Execute(ctx, context)
		if err != nil {
			log.Error(err, "Strategy execution failed", "strategy", strategy.Name)
			continue
		}

		log.V(1).Info("Strategy evaluated",
			"strategy", strategy.Name,
			"score", result.Score,
			"cost", result.EstimatedCost,
			"power", result.EstimatedPower)

		if result.Score > bestScore {
			bestScore = result.Score
			bestResult = result
		}
	}

	if bestResult == nil {
		return nil, fmt.Errorf("no successful optimization strategies found")
	}

	log.Info("Best strategy selected",
		"strategy", bestResult.StrategyName,
		"score", bestResult.Score,
		"confidence", bestResult.Confidence)

	return bestResult, nil
}

// createSpotInstanceStrategy creates the spot instance optimization strategy
func (sm *StrategyManager) createSpotInstanceStrategy() OptimizationStrategy {
	return OptimizationStrategy{
		Name:        "spot_instance_optimization",
		Description: "Optimize costs by using spot instances",
		Priority:    3,
		Applicable: func(wo *kcloudv1alpha1.WorkloadOptimizer) bool {
			return wo.Spec.CostConstraints != nil && wo.Spec.CostConstraints.PreferSpot
		},
		Execute: func(ctx context.Context, context *OptimizationContext) (*StrategyOptimizationResult, error) {
			log := log.FromContext(ctx)

			// Calculate current cost
			cpuCores := context.CostCalculator.parseCPU(context.WorkloadOptimizer.Spec.Resources.CPU)
			memoryGB := context.CostCalculator.parseMemory(context.WorkloadOptimizer.Spec.Resources.Memory)

			currentCost := context.CostCalculator.CalculateCost(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			// Calculate spot instance cost (30% discount)
			spotCost := currentCost * 0.7
			savings := currentCost - spotCost

			// Calculate power (same as current)
			_ = context.PowerCalculator.CalculatePower(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			// Calculate score based on savings
			score := math.Min(1.0, savings/currentCost*2) // Higher savings = higher score

			log.V(1).Info("Spot instance strategy evaluated",
				"currentCost", currentCost,
				"spotCost", spotCost,
				"savings", savings,
				"score", score)

			return &StrategyOptimizationResult{
				StrategyName:       "spot_instance_optimization",
				EstimatedCost:      spotCost,
				EstimatedPower:     0.0, // TODO: Calculate current power
				Score:              score,
				Recommendations:    []string{fmt.Sprintf("Use spot instances to save $%.2f/hour (%.1f%%)", savings, (savings/currentCost)*100)},
				RequiredActions:    []string{"enable_spot_instances", "configure_interruption_handling"},
				Confidence:         0.8,
				ImplementationTime: time.Minute * 5,
			}, nil
		},
	}
}

// createResourceOptimizationStrategy creates the resource optimization strategy
func (sm *StrategyManager) createResourceOptimizationStrategy() OptimizationStrategy {
	return OptimizationStrategy{
		Name:        "resource_optimization",
		Description: "Optimize resource allocation for better cost efficiency",
		Priority:    2,
		Applicable: func(wo *kcloudv1alpha1.WorkloadOptimizer) bool {
			return true // Always applicable
		},
		Execute: func(ctx context.Context, context *OptimizationContext) (*StrategyOptimizationResult, error) {
			log := log.FromContext(ctx)

			// Analyze current resource usage
			cpuCores := context.CostCalculator.parseCPU(context.WorkloadOptimizer.Spec.Resources.CPU)
			memoryGB := context.CostCalculator.parseMemory(context.WorkloadOptimizer.Spec.Resources.Memory)

			// Suggest optimized resources (reduce by 10-20% if possible)
			optimizedCPU := cpuCores * 0.9
			optimizedMemory := memoryGB * 0.9

			// Ensure minimum viable resources
			if optimizedCPU < 0.1 {
				optimizedCPU = 0.1
			}
			if optimizedMemory < 0.1 {
				optimizedMemory = 0.1
			}

			currentCost := context.CostCalculator.CalculateCost(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			optimizedCost := context.CostCalculator.CalculateCost(optimizedCPU, optimizedMemory,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			savings := currentCost - optimizedCost

			_ = context.PowerCalculator.CalculatePower(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			optimizedPower := context.PowerCalculator.CalculatePower(optimizedCPU, optimizedMemory,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			// Calculate score based on savings and feasibility
			score := 0.0
			if savings > 0 {
				score = math.Min(1.0, savings/currentCost*3) // Higher weight for resource optimization
			}

			log.V(1).Info("Resource optimization strategy evaluated",
				"currentCost", currentCost,
				"optimizedCost", optimizedCost,
				"savings", savings,
				"score", score)

			recommendations := []string{}
			if savings > 0 {
				recommendations = append(recommendations,
					fmt.Sprintf("Reduce CPU from %.2f to %.2f cores", cpuCores, optimizedCPU))
				recommendations = append(recommendations,
					fmt.Sprintf("Reduce memory from %.2f to %.2f GB", memoryGB, optimizedMemory))
				recommendations = append(recommendations,
					fmt.Sprintf("Save $%.2f/hour (%.1f%%)", savings, (savings/currentCost)*100))
			} else {
				recommendations = append(recommendations, "Current resource allocation is already optimized")
			}

			return &StrategyOptimizationResult{
				StrategyName:       "resource_optimization",
				EstimatedCost:      optimizedCost,
				EstimatedPower:     optimizedPower,
				Score:              score,
				Recommendations:    recommendations,
				RequiredActions:    []string{"adjust_resource_requests", "monitor_performance"},
				Confidence:         0.7,
				ImplementationTime: time.Minute * 2,
			}, nil
		},
	}
}

// createPowerOptimizationStrategy creates the power optimization strategy
func (sm *StrategyManager) createPowerOptimizationStrategy() OptimizationStrategy {
	return OptimizationStrategy{
		Name:        "power_optimization",
		Description: "Optimize power consumption for better efficiency",
		Priority:    2,
		Applicable: func(wo *kcloudv1alpha1.WorkloadOptimizer) bool {
			return wo.Spec.PowerConstraints != nil
		},
		Execute: func(ctx context.Context, context *OptimizationContext) (*StrategyOptimizationResult, error) {
			log := log.FromContext(ctx)

			cpuCores := context.CostCalculator.parseCPU(context.WorkloadOptimizer.Spec.Resources.CPU)
			memoryGB := context.CostCalculator.parseMemory(context.WorkloadOptimizer.Spec.Resources.Memory)

			_ = context.PowerCalculator.CalculatePower(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			// Suggest power-optimized configuration
			// Use more efficient CPU cores, reduce GPU usage if possible
			optimizedGPU := context.WorkloadOptimizer.Spec.Resources.GPU
			if optimizedGPU > 0 && context.WorkloadOptimizer.Spec.WorkloadType == "serving" {
				optimizedGPU = optimizedGPU - 1 // Reduce GPU for serving workloads
			}

			optimizedPower := context.PowerCalculator.CalculatePower(cpuCores, memoryGB,
				optimizedGPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			powerSavings := 0.0 - optimizedPower // TODO: Calculate current power

			// Cost remains the same for this strategy
			currentCost := context.CostCalculator.CalculateCost(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			// Calculate score based on power savings
			score := 0.0
			if powerSavings > 0 {
				score = math.Min(1.0, powerSavings/100.0*2) // TODO: Use actual current power
			}

			log.V(1).Info("Power optimization strategy evaluated",
				"currentPower", 0.0, // TODO: Calculate current power
				"optimizedPower", optimizedPower,
				"powerSavings", powerSavings,
				"score", score)

			recommendations := []string{}
			if powerSavings > 0 {
				recommendations = append(recommendations,
					fmt.Sprintf("Reduce power consumption by %.2fW (%.1f%%)", powerSavings, (powerSavings/100.0)*100)) // TODO: Use actual current power
				if optimizedGPU < context.WorkloadOptimizer.Spec.Resources.GPU {
					recommendations = append(recommendations,
						fmt.Sprintf("Reduce GPU count from %d to %d", context.WorkloadOptimizer.Spec.Resources.GPU, optimizedGPU))
				}
			} else {
				recommendations = append(recommendations, "Current power configuration is already optimized")
			}

			return &StrategyOptimizationResult{
				StrategyName:       "power_optimization",
				EstimatedCost:      currentCost,
				EstimatedPower:     optimizedPower,
				Score:              score,
				Recommendations:    recommendations,
				RequiredActions:    []string{"adjust_gpu_allocation", "monitor_power_usage"},
				Confidence:         0.6,
				ImplementationTime: time.Minute * 3,
			}, nil
		},
	}
}

// createAutoScalingStrategy creates the auto-scaling optimization strategy
func (sm *StrategyManager) createAutoScalingStrategy() OptimizationStrategy {
	return OptimizationStrategy{
		Name:        "auto_scaling_optimization",
		Description: "Optimize costs through intelligent auto-scaling",
		Priority:    1,
		Applicable: func(wo *kcloudv1alpha1.WorkloadOptimizer) bool {
			return wo.Spec.AutoScaling != nil
		},
		Execute: func(ctx context.Context, context *OptimizationContext) (*StrategyOptimizationResult, error) {
			log := log.FromContext(ctx)

			// Analyze current scaling configuration
			minReplicas := context.WorkloadOptimizer.Spec.AutoScaling.MinReplicas
			maxReplicas := context.WorkloadOptimizer.Spec.AutoScaling.MaxReplicas

			// Suggest optimized scaling based on workload type
			var suggestedMin, suggestedMax int32
			var score float64

			switch context.WorkloadOptimizer.Spec.WorkloadType {
			case "serving":
				suggestedMin = 2 // Always keep at least 2 for serving
				suggestedMax = maxReplicas
				score = 0.8
			case "training":
				suggestedMin = 1                   // Can scale down to 1 for training
				suggestedMax = min(maxReplicas, 8) // Limit max for training
				score = 0.7
			case "inference":
				suggestedMin = 1
				suggestedMax = min(maxReplicas, 4) // Limit max for inference
				score = 0.9
			default:
				suggestedMin = minReplicas
				suggestedMax = maxReplicas
				score = 0.5
			}

			// Calculate cost impact
			cpuCores := context.CostCalculator.parseCPU(context.WorkloadOptimizer.Spec.Resources.CPU)
			memoryGB := context.CostCalculator.parseMemory(context.WorkloadOptimizer.Spec.Resources.Memory)

			currentCost := context.CostCalculator.CalculateCost(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			// Estimate cost with optimized scaling
			avgReplicas := float64(suggestedMin+suggestedMax) / 2
			optimizedCost := currentCost * avgReplicas

			_ = context.PowerCalculator.CalculatePower(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			optimizedPower := 100.0 * avgReplicas // TODO: Calculate current power

			log.V(1).Info("Auto-scaling strategy evaluated",
				"currentMin", minReplicas,
				"currentMax", maxReplicas,
				"suggestedMin", suggestedMin,
				"suggestedMax", suggestedMax,
				"score", score)

			recommendations := []string{
				fmt.Sprintf("Adjust min replicas from %d to %d", minReplicas, suggestedMin),
				fmt.Sprintf("Adjust max replicas from %d to %d", maxReplicas, suggestedMax),
				fmt.Sprintf("Estimated average cost: $%.2f/hour", optimizedCost),
			}

			return &StrategyOptimizationResult{
				StrategyName:       "auto_scaling_optimization",
				EstimatedCost:      optimizedCost,
				EstimatedPower:     optimizedPower,
				Score:              score,
				Recommendations:    recommendations,
				RequiredActions:    []string{"update_hpa_config", "configure_metrics"},
				Confidence:         0.8,
				ImplementationTime: time.Minute * 1,
			}, nil
		},
	}
}

// createNodeSelectionStrategy creates the node selection optimization strategy
func (sm *StrategyManager) createNodeSelectionStrategy() OptimizationStrategy {
	return OptimizationStrategy{
		Name:        "node_selection_optimization",
		Description: "Optimize costs by selecting the most cost-effective nodes",
		Priority:    1,
		Applicable: func(wo *kcloudv1alpha1.WorkloadOptimizer) bool {
			return true // Always applicable
		},
		Execute: func(ctx context.Context, context *OptimizationContext) (*StrategyOptimizationResult, error) {
			log := log.FromContext(ctx)

			// Analyze available nodes for cost efficiency
			var bestNode *corev1.Node
			var bestScore float64

			for _, node := range context.AvailableNodes {
				score := sm.calculateNodeScore(node, context.WorkloadOptimizer)
				if score > bestScore {
					bestScore = score
					bestNode = &node
				}
			}

			if bestNode == nil {
				return nil, fmt.Errorf("no suitable nodes found")
			}

			// Calculate cost and power for the best node
			cpuCores := context.CostCalculator.parseCPU(context.WorkloadOptimizer.Spec.Resources.CPU)
			memoryGB := context.CostCalculator.parseMemory(context.WorkloadOptimizer.Spec.Resources.Memory)

			// Adjust cost based on node characteristics
			baseCost := context.CostCalculator.CalculateCost(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			nodeCostMultiplier := sm.getNodeCostMultiplier(bestNode)
			optimizedCost := baseCost * nodeCostMultiplier

			basePower := context.PowerCalculator.CalculatePower(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			nodePowerMultiplier := sm.getNodePowerMultiplier(bestNode)
			optimizedPower := basePower * nodePowerMultiplier

			log.V(1).Info("Node selection strategy evaluated",
				"selectedNode", bestNode.Name,
				"nodeScore", bestScore,
				"costMultiplier", nodeCostMultiplier,
				"powerMultiplier", nodePowerMultiplier)

			recommendations := []string{
				fmt.Sprintf("Select node %s for optimal cost efficiency", bestNode.Name),
				fmt.Sprintf("Node cost multiplier: %.2f", nodeCostMultiplier),
				fmt.Sprintf("Estimated cost: $%.2f/hour", optimizedCost),
			}

			return &StrategyOptimizationResult{
				StrategyName:       "node_selection_optimization",
				EstimatedCost:      optimizedCost,
				EstimatedPower:     optimizedPower,
				Score:              bestScore,
				Recommendations:    recommendations,
				RequiredActions:    []string{"update_node_selector", "configure_affinity"},
				Confidence:         0.7,
				ImplementationTime: time.Minute * 2,
			}, nil
		},
	}
}

// createWorkloadMigrationStrategy creates the workload migration optimization strategy
func (sm *StrategyManager) createWorkloadMigrationStrategy() OptimizationStrategy {
	return OptimizationStrategy{
		Name:        "workload_migration_optimization",
		Description: "Optimize by migrating workload to more efficient nodes",
		Priority:    0, // Lowest priority as it's disruptive
		Applicable: func(wo *kcloudv1alpha1.WorkloadOptimizer) bool {
			return wo.Status.AssignedNode != nil && len(*wo.Status.AssignedNode) > 0 // Only if already assigned to a node
		},
		Execute: func(ctx context.Context, context *OptimizationContext) (*StrategyOptimizationResult, error) {
			log := log.FromContext(ctx)

			// This is a placeholder implementation
			// In reality, this would analyze current node vs. potential target nodes

			cpuCores := context.CostCalculator.parseCPU(context.WorkloadOptimizer.Spec.Resources.CPU)
			memoryGB := context.CostCalculator.parseMemory(context.WorkloadOptimizer.Spec.Resources.Memory)

			currentCost := context.CostCalculator.CalculateCost(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			_ = context.PowerCalculator.CalculatePower(cpuCores, memoryGB,
				context.WorkloadOptimizer.Spec.Resources.GPU,
				context.WorkloadOptimizer.Spec.Resources.NPU)

			// Estimate 10% cost reduction through migration
			optimizedCost := currentCost * 0.9
			optimizedPower := 100.0 * 0.95 // 5% power reduction, TODO: Calculate current power

			log.V(1).Info("Workload migration strategy evaluated",
				"currentCost", currentCost,
				"optimizedCost", optimizedCost,
				"costSavings", currentCost-optimizedCost)

			recommendations := []string{
				"Consider migrating to a more cost-effective node",
				fmt.Sprintf("Potential cost savings: $%.2f/hour", currentCost-optimizedCost),
				fmt.Sprintf("Potential power savings: %.2fW", 100.0-optimizedPower), // TODO: Use actual current power
			}

			return &StrategyOptimizationResult{
				StrategyName:       "workload_migration_optimization",
				EstimatedCost:      optimizedCost,
				EstimatedPower:     optimizedPower,
				Score:              0.6, // Lower score due to disruption
				Recommendations:    recommendations,
				RequiredActions:    []string{"plan_migration", "drain_current_node", "schedule_migration"},
				Confidence:         0.5,
				ImplementationTime: time.Minute * 10,
			}, nil
		},
	}
}

// Helper functions

func (sm *StrategyManager) calculateNodeScore(node corev1.Node, wo *kcloudv1alpha1.WorkloadOptimizer) float64 {
	score := 0.5 // Base score

	// Check node labels for cost efficiency
	if costLabel, exists := node.Labels["cost-efficiency"]; exists {
		switch costLabel {
		case "high":
			score += 0.3
		case "medium":
			score += 0.1
		}
	}

	// Check for spot instances
	if node.Labels["lifecycle"] == "spot" {
		score += 0.2
	}

	// Check for green energy
	if node.Labels["energy-source"] == "renewable" {
		score += 0.1
	}

	return math.Min(1.0, score)
}

func (sm *StrategyManager) getNodeCostMultiplier(node *corev1.Node) float64 {
	if costLabel, exists := node.Labels["cost-tier"]; exists {
		switch costLabel {
		case "low":
			return 0.7
		case "medium":
			return 1.0
		case "high":
			return 1.3
		}
	}
	return 1.0
}

func (sm *StrategyManager) getNodePowerMultiplier(node *corev1.Node) float64 {
	if powerLabel, exists := node.Labels["power-efficiency"]; exists {
		switch powerLabel {
		case "high":
			return 0.8
		case "medium":
			return 1.0
		case "low":
			return 1.2
		}
	}
	return 1.0
}

func min(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
