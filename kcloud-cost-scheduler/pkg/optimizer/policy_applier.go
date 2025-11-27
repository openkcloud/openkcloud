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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// PolicyApplier applies cost and power policies to workload optimization
type PolicyApplier struct {
	Client client.Client
}

// PolicyApplicationResult represents the result of applying policies
type PolicyApplicationResult struct {
	CostConstraints   *CostConstraintsResult
	PowerConstraints  *PowerConstraintsResult
	Recommendations   []PolicyRecommendation
	Violations        []PolicyViolation
	OptimizationScore float64
}

// CostConstraintsResult represents the result of cost constraint evaluation
type CostConstraintsResult struct {
	WithinBudget        bool
	BudgetUtilization   float64
	EstimatedCost       float64
	BudgetRemaining     float64
	SpotInstanceSavings float64
	Recommendations     []string
}

// PowerConstraintsResult represents the result of power constraint evaluation
type PowerConstraintsResult struct {
	WithinPowerLimit bool
	PowerUtilization float64
	EstimatedPower   float64
	PowerRemaining   float64
	GreenEnergyScore float64
	CarbonFootprint  float64
	Recommendations  []string
}

// PolicyRecommendation represents a policy-based recommendation
type PolicyRecommendation struct {
	Type        string
	Priority    int
	Description string
	Impact      string
	Action      string
}

// PolicyViolation represents a policy violation
type PolicyViolation struct {
	Type         string
	Severity     string
	Description  string
	CurrentValue float64
	LimitValue   float64
	Action       string
}

// NewPolicyApplier creates a new policy applier
func NewPolicyApplier(client client.Client) *PolicyApplier {
	return &PolicyApplier{
		Client: client,
	}
}

// ApplyPolicies applies cost and power policies to workload optimization
func (pa *PolicyApplier) ApplyPolicies(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	costBreakdown *CostBreakdown, powerWatts float64) (*PolicyApplicationResult, error) {
	log := log.FromContext(ctx)

	result := &PolicyApplicationResult{
		Recommendations: []PolicyRecommendation{},
		Violations:      []PolicyViolation{},
	}

	// Apply cost policies
	costResult, err := pa.applyCostPolicies(ctx, wo, costBreakdown)
	if err != nil {
		return nil, fmt.Errorf("failed to apply cost policies: %w", err)
	}
	result.CostConstraints = costResult

	// Apply power policies
	powerResult, err := pa.applyPowerPolicies(ctx, wo, powerWatts)
	if err != nil {
		return nil, fmt.Errorf("failed to apply power policies: %w", err)
	}
	result.PowerConstraints = powerResult

	// Generate recommendations
	pa.generateRecommendations(result, wo)

	// Calculate overall optimization score
	result.OptimizationScore = pa.calculateOptimizationScore(result)

	log.Info("Policies applied successfully",
		"costWithinBudget", costResult.WithinBudget,
		"powerWithinLimit", powerResult.WithinPowerLimit,
		"optimizationScore", result.OptimizationScore)

	return result, nil
}

// applyCostPolicies applies cost-related policies
func (pa *PolicyApplier) applyCostPolicies(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	costBreakdown *CostBreakdown) (*CostConstraintsResult, error) {
	result := &CostConstraintsResult{
		EstimatedCost:   costBreakdown.FinalCost,
		Recommendations: []string{},
	}

	// Check budget constraints
	if wo.Spec.CostConstraints != nil {
		// Check hourly cost limit
		if wo.Spec.CostConstraints.MaxCostPerHour > 0 {
			result.WithinBudget = costBreakdown.FinalCost <= wo.Spec.CostConstraints.MaxCostPerHour
			result.BudgetUtilization = (costBreakdown.FinalCost / wo.Spec.CostConstraints.MaxCostPerHour) * 100
			result.BudgetRemaining = wo.Spec.CostConstraints.MaxCostPerHour - costBreakdown.FinalCost

			if !result.WithinBudget {
				result.Recommendations = append(result.Recommendations,
					fmt.Sprintf("Cost exceeds hourly limit by $%.2f", costBreakdown.FinalCost-wo.Spec.CostConstraints.MaxCostPerHour))
			}
		}

		// Check budget limit
		if wo.Spec.CostConstraints.BudgetLimit != nil {
			// This would require tracking total spend over time
			// For now, we'll estimate based on hourly cost
			estimatedMonthlyCost := costBreakdown.FinalCost * 24 * 30
			if estimatedMonthlyCost > *wo.Spec.CostConstraints.BudgetLimit {
				result.Recommendations = append(result.Recommendations,
					fmt.Sprintf("Estimated monthly cost $%.2f exceeds budget limit $%.2f",
						estimatedMonthlyCost, *wo.Spec.CostConstraints.BudgetLimit))
			}
		}

		// Check spot instance preference
		if wo.Spec.CostConstraints.PreferSpot {
			spotSavings := costBreakdown.TotalCost * 0.3 // 30% spot discount
			result.SpotInstanceSavings = spotSavings
			result.Recommendations = append(result.Recommendations,
				fmt.Sprintf("Spot instances could save $%.2f/hour", spotSavings))
		}
	}

	// Apply cluster-wide cost policies
	costPolicies, err := pa.getCostPolicies(ctx, wo.Namespace)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to get cost policies")
	} else {
		for _, policy := range costPolicies {
			pa.applyCostPolicy(result, &policy, costBreakdown)
		}
	}

	return result, nil
}

// applyPowerPolicies applies power-related policies
func (pa *PolicyApplier) applyPowerPolicies(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	powerWatts float64) (*PowerConstraintsResult, error) {
	result := &PowerConstraintsResult{
		EstimatedPower:  powerWatts,
		Recommendations: []string{},
	}

	// Check power constraints
	if wo.Spec.PowerConstraints != nil {
		// Check power limit
		result.WithinPowerLimit = powerWatts <= wo.Spec.PowerConstraints.MaxPowerUsage
		result.PowerUtilization = (powerWatts / wo.Spec.PowerConstraints.MaxPowerUsage) * 100
		result.PowerRemaining = wo.Spec.PowerConstraints.MaxPowerUsage - powerWatts

		if !result.WithinPowerLimit {
			result.Recommendations = append(result.Recommendations,
				fmt.Sprintf("Power usage %.2fW exceeds limit %.2fW",
					powerWatts, wo.Spec.PowerConstraints.MaxPowerUsage))
		}

		// Check green energy preference
		if wo.Spec.PowerConstraints.PreferGreen {
			result.GreenEnergyScore = 0.8                    // Assume 80% green energy
			result.CarbonFootprint = powerWatts * 0.5 / 1000 // Simplified carbon footprint calculation
			result.Recommendations = append(result.Recommendations,
				"Consider green energy sources for better sustainability")
		}
	}

	// Apply cluster-wide power policies
	powerPolicies, err := pa.getPowerPolicies(ctx, wo.Namespace)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to get power policies")
	} else {
		for _, policy := range powerPolicies {
			pa.applyPowerPolicy(result, &policy, powerWatts)
		}
	}

	return result, nil
}

// applyCostPolicy applies a single cost policy
func (pa *PolicyApplier) applyCostPolicy(result *CostConstraintsResult, policy *kcloudv1alpha1.CostPolicy,
	costBreakdown *CostBreakdown) {
	// Check if policy applies to this workload
	if !pa.policyAppliesToWorkload(policy, "") { // namespace would be passed here
		return
	}

	// Apply spot instance policy
	if policy.Spec.SpotInstancePolicy != nil && policy.Spec.SpotInstancePolicy.Enabled {
		if policy.Spec.SpotInstancePolicy.MaxSpotPercentage != nil {
			maxSpotCost := costBreakdown.TotalCost * (float64(*policy.Spec.SpotInstancePolicy.MaxSpotPercentage) / 100)
			spotSavings := costBreakdown.TotalCost * 0.3
			if spotSavings > maxSpotCost {
				result.Recommendations = append(result.Recommendations,
					fmt.Sprintf("Spot instance usage exceeds policy limit of %d%%",
						*policy.Spec.SpotInstancePolicy.MaxSpotPercentage))
			}
		}
	}

	// Apply resource cost policy
	if policy.Spec.ResourceCostPolicy != nil {
		if policy.Spec.ResourceCostPolicy.CPUCostPerCorePerHour != nil {
			result.Recommendations = append(result.Recommendations,
				fmt.Sprintf("Policy sets CPU cost to $%.4f per core/hour",
					*policy.Spec.ResourceCostPolicy.CPUCostPerCorePerHour))
		}
	}

	// Check alert thresholds
	for _, threshold := range policy.Spec.AlertThresholds {
		pa.checkCostAlertThreshold(result, threshold, costBreakdown)
	}
}

// applyPowerPolicy applies a single power policy
func (pa *PolicyApplier) applyPowerPolicy(result *PowerConstraintsResult, policy *kcloudv1alpha1.PowerPolicy,
	powerWatts float64) {
	// Check if policy applies to this workload
	if !pa.policyAppliesToWorkload(policy, "") { // namespace would be passed here
		return
	}

	// Apply green energy policy
	if policy.Spec.GreenEnergyPolicy != nil && policy.Spec.GreenEnergyPolicy.Enabled {
		if policy.Spec.GreenEnergyPolicy.MinGreenEnergyPercentage != nil {
			minGreenPercentage := float64(*policy.Spec.GreenEnergyPolicy.MinGreenEnergyPercentage)
			if result.GreenEnergyScore*100 < minGreenPercentage {
				result.Recommendations = append(result.Recommendations,
					fmt.Sprintf("Green energy usage %.1f%% below policy minimum %.1f%%",
						result.GreenEnergyScore*100, minGreenPercentage))
			}
		}

		if policy.Spec.GreenEnergyPolicy.CarbonFootprintTarget != nil {
			if result.CarbonFootprint > *policy.Spec.GreenEnergyPolicy.CarbonFootprintTarget {
				result.Recommendations = append(result.Recommendations,
					fmt.Sprintf("Carbon footprint %.2f kg CO2/h exceeds target %.2f kg CO2/h",
						result.CarbonFootprint, *policy.Spec.GreenEnergyPolicy.CarbonFootprintTarget))
			}
		}
	}

	// Check alert thresholds
	for _, threshold := range policy.Spec.PowerAlertThresholds {
		pa.checkPowerAlertThreshold(result, threshold, powerWatts)
	}
}

// checkCostAlertThreshold checks cost alert thresholds
func (pa *PolicyApplier) checkCostAlertThreshold(result *CostConstraintsResult, threshold kcloudv1alpha1.CostAlertThreshold,
	costBreakdown *CostBreakdown) {
	var currentValue float64
	var limitValue float64

	switch threshold.Type {
	case "budget_usage":
		currentValue = result.BudgetUtilization
		limitValue = threshold.Value
	case "cost_increase":
		// This would require historical data
		currentValue = 0 // Placeholder
		limitValue = threshold.Value
	}

	if currentValue > limitValue {
		_ = PolicyViolation{
			Type:         threshold.Type,
			Severity:     threshold.Severity,
			Description:  fmt.Sprintf("Cost threshold exceeded: %.2f > %.2f", currentValue, limitValue),
			CurrentValue: currentValue,
			LimitValue:   limitValue,
			Action:       threshold.Action,
		}
		// Add to violations (would be added to result.Violations)
	}
}

// checkPowerAlertThreshold checks power alert thresholds
func (pa *PolicyApplier) checkPowerAlertThreshold(result *PowerConstraintsResult, threshold kcloudv1alpha1.PowerAlertThreshold,
	powerWatts float64) {
	var currentValue float64
	var limitValue float64

	switch threshold.Type {
	case "power_usage":
		currentValue = powerWatts
		limitValue = threshold.Value
	case "power_efficiency":
		currentValue = 0.5 // Placeholder - would calculate actual power efficiency
		limitValue = threshold.Value
	case "carbon_footprint":
		currentValue = result.CarbonFootprint
		limitValue = threshold.Value
	}

	if currentValue > limitValue {
		_ = PolicyViolation{
			Type:         threshold.Type,
			Severity:     threshold.Severity,
			Description:  fmt.Sprintf("Power threshold exceeded: %.2f > %.2f", currentValue, limitValue),
			CurrentValue: currentValue,
			LimitValue:   limitValue,
			Action:       threshold.Action,
		}
		// Add to violations (would be added to result.Violations)
	}
}

// generateRecommendations generates policy-based recommendations
func (pa *PolicyApplier) generateRecommendations(result *PolicyApplicationResult, wo *kcloudv1alpha1.WorkloadOptimizer) {
	// Cost optimization recommendations
	if result.CostConstraints != nil {
		if result.CostConstraints.BudgetUtilization > 80 {
			result.Recommendations = append(result.Recommendations, PolicyRecommendation{
				Type:        "cost_optimization",
				Priority:    1,
				Description: "Budget utilization is high, consider optimizing resources",
				Impact:      "High",
				Action:      "scale_down",
			})
		}

		if result.CostConstraints.SpotInstanceSavings > 0 {
			result.Recommendations = append(result.Recommendations, PolicyRecommendation{
				Type:        "spot_instances",
				Priority:    2,
				Description: fmt.Sprintf("Spot instances could save $%.2f/hour", result.CostConstraints.SpotInstanceSavings),
				Impact:      "Medium",
				Action:      "enable_spot",
			})
		}
	}

	// Power optimization recommendations
	if result.PowerConstraints != nil {
		if result.PowerConstraints.PowerUtilization > 80 {
			result.Recommendations = append(result.Recommendations, PolicyRecommendation{
				Type:        "power_optimization",
				Priority:    1,
				Description: "Power usage is high, consider power-efficient alternatives",
				Impact:      "High",
				Action:      "migrate",
			})
		}

		if result.PowerConstraints.GreenEnergyScore < 0.7 {
			result.Recommendations = append(result.Recommendations, PolicyRecommendation{
				Type:        "green_energy",
				Priority:    3,
				Description: "Consider using green energy sources",
				Impact:      "Medium",
				Action:      "prefer_green",
			})
		}
	}
}

// calculateOptimizationScore calculates overall optimization score based on policy compliance
func (pa *PolicyApplier) calculateOptimizationScore(result *PolicyApplicationResult) float64 {
	score := 1.0

	// Cost score
	if result.CostConstraints != nil {
		if result.CostConstraints.WithinBudget {
			score *= 1.0
		} else {
			score *= 0.5 // Penalty for budget violation
		}
	}

	// Power score
	if result.PowerConstraints != nil {
		if result.PowerConstraints.WithinPowerLimit {
			score *= 1.0
		} else {
			score *= 0.5 // Penalty for power violation
		}
	}

	// Recommendation penalty
	penalty := float64(len(result.Recommendations)) * 0.05
	score = math.Max(0.0, score-penalty)

	return math.Round(score*100) / 100
}

// getCostPolicies retrieves applicable cost policies
func (pa *PolicyApplier) getCostPolicies(ctx context.Context, namespace string) ([]kcloudv1alpha1.CostPolicy, error) {
	// TODO: Implement when CRD types are properly generated
	// For now, return empty list
	return []kcloudv1alpha1.CostPolicy{}, nil
}

// getPowerPolicies retrieves applicable power policies
func (pa *PolicyApplier) getPowerPolicies(ctx context.Context, namespace string) ([]kcloudv1alpha1.PowerPolicy, error) {
	// TODO: Implement when CRD types are properly generated
	// For now, return empty list
	return []kcloudv1alpha1.PowerPolicy{}, nil
}

// policyAppliesToWorkload checks if a policy applies to the workload
func (pa *PolicyApplier) policyAppliesToWorkload(policy interface{}, namespace string) bool {
	// Simplified implementation - in reality, this would check namespace and workload selectors
	return true
}
