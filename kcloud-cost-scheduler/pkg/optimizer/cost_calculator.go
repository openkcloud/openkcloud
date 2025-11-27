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
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CostCalculator calculates the cost of running workloads
type CostCalculator struct {
	// Cost per CPU core per hour in USD
	CPUCostPerCorePerHour float64
	// Cost per GB memory per hour in USD
	MemoryCostPerGBPerHour float64
	// Cost per GPU per hour in USD
	GPUCostPerHour float64
	// Cost per NPU per hour in USD
	NPUCostPerHour float64
	// Base infrastructure cost per hour in USD
	BaseInfrastructureCostPerHour float64
	// Spot instance discount factor (0.0-1.0)
	SpotInstanceDiscount float64
	// Reserved instance discount factor (0.0-1.0)
	ReservedInstanceDiscount float64
}

// CostBreakdown provides detailed cost breakdown
type CostBreakdown struct {
	CPUCost            float64
	MemoryCost         float64
	GPUCost            float64
	NPUCost            float64
	InfrastructureCost float64
	TotalCost          float64
	DiscountApplied    float64
	FinalCost          float64
}

// PricingTier represents different pricing tiers
type PricingTier struct {
	Name             string
	CPUMultiplier    float64
	MemoryMultiplier float64
	GPUMultiplier    float64
	NPUMultiplier    float64
}

// NewCostCalculator creates a new cost calculator with default pricing
func NewCostCalculator() *CostCalculator {
	return &CostCalculator{
		CPUCostPerCorePerHour:         0.05, // $0.05 per CPU core per hour
		MemoryCostPerGBPerHour:        0.01, // $0.01 per GB memory per hour
		GPUCostPerHour:                2.50, // $2.50 per GPU per hour (NVIDIA A100)
		NPUCostPerHour:                2.00, // $2.00 per NPU per hour
		BaseInfrastructureCostPerHour: 0.10, // $0.10 base infrastructure cost
		SpotInstanceDiscount:          0.30, // 30% discount for spot instances
		ReservedInstanceDiscount:      0.20, // 20% discount for reserved instances
	}
}

// CalculateCost calculates the total cost for running a workload
func (c *CostCalculator) CalculateCost(cpuCores, memoryGB float64, gpuCount, npuCount int32) float64 {
	breakdown := c.CalculateCostBreakdown(cpuCores, memoryGB, gpuCount, npuCount)
	return breakdown.FinalCost
}

// CalculateCostBreakdown provides detailed cost breakdown
func (c *CostCalculator) CalculateCostBreakdown(cpuCores, memoryGB float64, gpuCount, npuCount int32) *CostBreakdown {
	breakdown := &CostBreakdown{}

	// Calculate individual component costs
	breakdown.CPUCost = cpuCores * c.CPUCostPerCorePerHour
	breakdown.MemoryCost = memoryGB * c.MemoryCostPerGBPerHour
	breakdown.GPUCost = float64(gpuCount) * c.GPUCostPerHour
	breakdown.NPUCost = float64(npuCount) * c.NPUCostPerHour
	breakdown.InfrastructureCost = c.BaseInfrastructureCostPerHour

	// Calculate total cost before discounts
	breakdown.TotalCost = breakdown.CPUCost + breakdown.MemoryCost +
		breakdown.GPUCost + breakdown.NPUCost + breakdown.InfrastructureCost

	// Apply discounts (default: no discount)
	breakdown.DiscountApplied = 0.0
	breakdown.FinalCost = breakdown.TotalCost

	return breakdown
}

// CalculateCostWithDiscounts calculates cost with specific discount factors
func (c *CostCalculator) CalculateCostWithDiscounts(cpuCores, memoryGB float64, gpuCount, npuCount int32,
	spotDiscount, reservedDiscount float64) *CostBreakdown {
	breakdown := c.CalculateCostBreakdown(cpuCores, memoryGB, gpuCount, npuCount)

	// Apply spot instance discount
	if spotDiscount > 0 {
		spotSavings := breakdown.TotalCost * spotDiscount
		breakdown.DiscountApplied += spotSavings
	}

	// Apply reserved instance discount (additional)
	if reservedDiscount > 0 {
		reservedSavings := breakdown.TotalCost * reservedDiscount
		breakdown.DiscountApplied += reservedSavings
	}

	breakdown.FinalCost = breakdown.TotalCost - breakdown.DiscountApplied
	return breakdown
}

// CalculateCostByTier calculates cost based on pricing tier
func (c *CostCalculator) CalculateCostByTier(cpuCores, memoryGB float64, gpuCount, npuCount int32, tier *PricingTier) *CostBreakdown {
	if tier == nil {
		return c.CalculateCostBreakdown(cpuCores, memoryGB, gpuCount, npuCount)
	}

	// Apply tier multipliers
	adjustedCPUCost := cpuCores * c.CPUCostPerCorePerHour * tier.CPUMultiplier
	adjustedMemoryCost := memoryGB * c.MemoryCostPerGBPerHour * tier.MemoryMultiplier
	adjustedGPUCost := float64(gpuCount) * c.GPUCostPerHour * tier.GPUMultiplier
	adjustedNPUCost := float64(npuCount) * c.NPUCostPerHour * tier.NPUMultiplier

	breakdown := &CostBreakdown{
		CPUCost:            adjustedCPUCost,
		MemoryCost:         adjustedMemoryCost,
		GPUCost:            adjustedGPUCost,
		NPUCost:            adjustedNPUCost,
		InfrastructureCost: c.BaseInfrastructureCostPerHour,
		TotalCost:          adjustedCPUCost + adjustedMemoryCost + adjustedGPUCost + adjustedNPUCost + c.BaseInfrastructureCostPerHour,
		DiscountApplied:    0.0,
		FinalCost:          adjustedCPUCost + adjustedMemoryCost + adjustedGPUCost + adjustedNPUCost + c.BaseInfrastructureCostPerHour,
	}

	return breakdown
}

// CalculateDailyCost calculates daily cost (24 hours)
func (c *CostCalculator) CalculateDailyCost(cpuCores, memoryGB float64, gpuCount, npuCount int32) float64 {
	hourlyCost := c.CalculateCost(cpuCores, memoryGB, gpuCount, npuCount)
	return hourlyCost * 24
}

// CalculateMonthlyCost calculates monthly cost (30 days)
func (c *CostCalculator) CalculateMonthlyCost(cpuCores, memoryGB float64, gpuCount, npuCount int32) float64 {
	dailyCost := c.CalculateDailyCost(cpuCores, memoryGB, gpuCount, npuCount)
	return dailyCost * 30
}

// CalculateYearlyCost calculates yearly cost (365 days)
func (c *CostCalculator) CalculateYearlyCost(cpuCores, memoryGB float64, gpuCount, npuCount int32) float64 {
	monthlyCost := c.CalculateMonthlyCost(cpuCores, memoryGB, gpuCount, npuCount)
	return monthlyCost * 12
}

// GetPricingTier returns predefined pricing tiers
func (c *CostCalculator) GetPricingTier(tierName string) *PricingTier {
	tiers := map[string]*PricingTier{
		"standard": {
			Name:             "Standard",
			CPUMultiplier:    1.0,
			MemoryMultiplier: 1.0,
			GPUMultiplier:    1.0,
			NPUMultiplier:    1.0,
		},
		"premium": {
			Name:             "Premium",
			CPUMultiplier:    1.5,
			MemoryMultiplier: 1.3,
			GPUMultiplier:    1.8,
			NPUMultiplier:    1.6,
		},
		"economy": {
			Name:             "Economy",
			CPUMultiplier:    0.7,
			MemoryMultiplier: 0.8,
			GPUMultiplier:    0.6,
			NPUMultiplier:    0.7,
		},
		"spot": {
			Name:             "Spot",
			CPUMultiplier:    0.7,
			MemoryMultiplier: 0.7,
			GPUMultiplier:    0.7,
			NPUMultiplier:    0.7,
		},
	}

	return tiers[tierName]
}

// EstimateCostSavings estimates potential cost savings
func (c *CostCalculator) EstimateCostSavings(currentCost, optimizedCost float64) *CostSavings {
	if currentCost <= 0 {
		return &CostSavings{
			Percentage: 0.0,
			Amount:     0.0,
		}
	}

	savings := currentCost - optimizedCost
	percentage := (savings / currentCost) * 100

	return &CostSavings{
		Percentage: math.Max(0, percentage),
		Amount:     math.Max(0, savings),
	}
}

// CostSavings represents cost savings information
type CostSavings struct {
	Percentage float64 // Percentage savings
	Amount     float64 // Absolute savings amount
}

// ParseResourceString parses resource string (e.g., "2", "500m", "4Gi")
func (c *CostCalculator) ParseResourceString(resourceStr string) (float64, error) {
	if strings.HasSuffix(resourceStr, "m") {
		// Millicores
		value, err := strconv.ParseFloat(strings.TrimSuffix(resourceStr, "m"), 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse millicores: %w", err)
		}
		return value / 1000.0, nil
	} else if strings.HasSuffix(resourceStr, "Gi") {
		// Gigabytes
		value, err := strconv.ParseFloat(strings.TrimSuffix(resourceStr, "Gi"), 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse gigabytes: %w", err)
		}
		return value, nil
	} else if strings.HasSuffix(resourceStr, "Mi") {
		// Megabytes
		value, err := strconv.ParseFloat(strings.TrimSuffix(resourceStr, "Mi"), 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse megabytes: %w", err)
		}
		return value / 1024.0, nil
	} else {
		// Plain number (assume cores or GB)
		value, err := strconv.ParseFloat(resourceStr, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse resource value: %w", err)
		}
		return value, nil
	}
}

// parseCPU parses CPU resource string (alias for ParseResourceString for CPU)
func (c *CostCalculator) parseCPU(cpu string) float64 {
	value, err := c.ParseResourceString(cpu)
	if err != nil {
		return 0
	}
	return value
}

// parseMemory parses memory resource string (alias for ParseResourceString for memory)
func (c *CostCalculator) parseMemory(memory string) float64 {
	value, err := c.ParseResourceString(memory)
	if err != nil {
		return 0
	}
	return value
}

// CalculateCostFromResourceString calculates cost from resource string
func (c *CostCalculator) CalculateCostFromResourceString(cpuStr, memoryStr string, gpuCount, npuCount int32) (float64, error) {
	cpuCores, err := c.ParseResourceString(cpuStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse CPU: %w", err)
	}

	memoryGB, err := c.ParseResourceString(memoryStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse memory: %w", err)
	}

	return c.CalculateCost(cpuCores, memoryGB, gpuCount, npuCount), nil
}

// CompareCosts compares two cost scenarios
func (c *CostCalculator) CompareCosts(scenario1, scenario2 *CostBreakdown) *CostComparison {
	return &CostComparison{
		Scenario1Cost:  scenario1.FinalCost,
		Scenario2Cost:  scenario2.FinalCost,
		Difference:     scenario1.FinalCost - scenario2.FinalCost,
		PercentageDiff: ((scenario1.FinalCost - scenario2.FinalCost) / scenario1.FinalCost) * 100,
		BetterScenario: func() string {
			if scenario1.FinalCost < scenario2.FinalCost {
				return "Scenario 1"
			}
			return "Scenario 2"
		}(),
	}
}

// CostComparison represents a comparison between two cost scenarios
type CostComparison struct {
	Scenario1Cost  float64
	Scenario2Cost  float64
	Difference     float64
	PercentageDiff float64
	BetterScenario string
}
