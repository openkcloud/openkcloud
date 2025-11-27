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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// PolicyManager manages scheduling policies and their lifecycle
type PolicyManager struct {
	client           client.Client
	policies         map[string]*SchedulingPolicy
	workloadPolicies map[string]string // workload name -> policy name
	mutex            sync.RWMutex
	lastUpdate       time.Time
}

// PolicyTemplate defines a template for creating scheduling policies
type PolicyTemplate struct {
	Name        string
	Description string
	Algorithm   SchedulingAlgorithm
	Constraints *PolicyConstraints
	Default     bool
}

// PolicyConstraints defines constraints for a policy template
type PolicyConstraints struct {
	ResourceConstraints *ResourceConstraints
	CostConstraints     *CostConstraints
	PowerConstraints    *PowerConstraints
	TimeConstraints     *TimeConstraints
}

// NewPolicyManager creates a new policy manager
func NewPolicyManager(client client.Client) *PolicyManager {
	pm := &PolicyManager{
		client:           client,
		policies:         make(map[string]*SchedulingPolicy),
		workloadPolicies: make(map[string]string),
		lastUpdate:       time.Now(),
	}

	// Initialize with default policies
	pm.initializeDefaultPolicies()
	return pm
}

// GetPolicyForWorkload returns the appropriate policy for a workload
func (pm *PolicyManager) GetPolicyForWorkload(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer) (*SchedulingPolicy, error) {
	log := log.FromContext(ctx)

	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	// Check if workload has a specific policy assigned
	if policyName, exists := pm.workloadPolicies[wo.Name]; exists {
		if policy, found := pm.policies[policyName]; found {
			log.V(1).Info("Using assigned policy for workload",
				"workload", wo.Name,
				"policy", policyName)
			return policy, nil
		}
	}

	// Determine policy based on workload characteristics
	policy := pm.selectPolicyForWorkload(wo)

	log.V(1).Info("Selected policy for workload",
		"workload", wo.Name,
		"policy", policy.Name,
		"algorithm", policy.Algorithm)

	return policy, nil
}

// CreatePolicy creates a new scheduling policy
func (pm *PolicyManager) CreatePolicy(ctx context.Context, name string, template *PolicyTemplate) (*SchedulingPolicy, error) {
	log := log.FromContext(ctx)

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if _, exists := pm.policies[name]; exists {
		return nil, fmt.Errorf("policy %s already exists", name)
	}

	policy := &SchedulingPolicy{
		Name:      name,
		Algorithm: template.Algorithm,
		Enabled:   true,
		Priority:  0,
	}

	if template.Constraints != nil {
		policy.ResourceConstraints = template.Constraints.ResourceConstraints
		policy.CostConstraints = template.Constraints.CostConstraints
		policy.PowerConstraints = template.Constraints.PowerConstraints
		policy.TimeConstraints = template.Constraints.TimeConstraints
	}

	pm.policies[name] = policy

	log.Info("Created new scheduling policy",
		"policy", name,
		"algorithm", template.Algorithm)

	return policy, nil
}

// UpdatePolicy updates an existing scheduling policy
func (pm *PolicyManager) UpdatePolicy(ctx context.Context, name string, updates *SchedulingPolicy) error {
	log := log.FromContext(ctx)

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	policy, exists := pm.policies[name]
	if !exists {
		return fmt.Errorf("policy %s does not exist", name)
	}

	// Update policy fields
	if updates.Algorithm != "" {
		policy.Algorithm = updates.Algorithm
	}
	if updates.ResourceConstraints != nil {
		policy.ResourceConstraints = updates.ResourceConstraints
	}
	if updates.CostConstraints != nil {
		policy.CostConstraints = updates.CostConstraints
	}
	if updates.PowerConstraints != nil {
		policy.PowerConstraints = updates.PowerConstraints
	}
	if updates.TimeConstraints != nil {
		policy.TimeConstraints = updates.TimeConstraints
	}
	if updates.Priority != 0 {
		policy.Priority = updates.Priority
	}

	pm.lastUpdate = time.Now()

	log.Info("Updated scheduling policy", "policy", name)
	return nil
}

// DeletePolicy deletes a scheduling policy
func (pm *PolicyManager) DeletePolicy(ctx context.Context, name string) error {
	log := log.FromContext(ctx)

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if _, exists := pm.policies[name]; !exists {
		return fmt.Errorf("policy %s does not exist", name)
	}

	// Remove policy from workloads
	for workloadName, policyName := range pm.workloadPolicies {
		if policyName == name {
			delete(pm.workloadPolicies, workloadName)
		}
	}

	delete(pm.policies, name)
	pm.lastUpdate = time.Now()

	log.Info("Deleted scheduling policy", "policy", name)
	return nil
}

// AssignPolicyToWorkload assigns a specific policy to a workload
func (pm *PolicyManager) AssignPolicyToWorkload(ctx context.Context, workloadName, policyName string) error {
	log := log.FromContext(ctx)

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if _, exists := pm.policies[policyName]; !exists {
		return fmt.Errorf("policy %s does not exist", policyName)
	}

	pm.workloadPolicies[workloadName] = policyName
	pm.lastUpdate = time.Now()

	log.Info("Assigned policy to workload",
		"workload", workloadName,
		"policy", policyName)

	return nil
}

// ListPolicies returns all available policies
func (pm *PolicyManager) ListPolicies() map[string]*SchedulingPolicy {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	policies := make(map[string]*SchedulingPolicy)
	for name, policy := range pm.policies {
		policies[name] = policy
	}
	return policies
}

// GetPolicy returns a specific policy by name
func (pm *PolicyManager) GetPolicy(name string) (*SchedulingPolicy, bool) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	policy, exists := pm.policies[name]
	return policy, exists
}

// RefreshPolicies refreshes policies from external sources (e.g., CRDs)
func (pm *PolicyManager) RefreshPolicies(ctx context.Context) error {
	log := log.FromContext(ctx)

	// TODO: Implement when SchedulingPolicy CRD is available
	// For now, this is a placeholder

	log.V(1).Info("Refreshed scheduling policies")
	return nil
}

// selectPolicyForWorkload selects the most appropriate policy for a workload
func (pm *PolicyManager) selectPolicyForWorkload(wo *kcloudv1alpha1.WorkloadOptimizer) *SchedulingPolicy {
	// Select policy based on workload characteristics
	switch wo.Spec.WorkloadType {
	case "training":
		return pm.getPolicyForTrainingWorkload(wo)
	case "serving":
		return pm.getPolicyForServingWorkload(wo)
	case "inference":
		return pm.getPolicyForInferenceWorkload(wo)
	case "batch":
		return pm.getPolicyForBatchWorkload(wo)
	default:
		return pm.getDefaultPolicy()
	}
}

// getPolicyForTrainingWorkload returns policy for training workloads
func (pm *PolicyManager) getPolicyForTrainingWorkload(wo *kcloudv1alpha1.WorkloadOptimizer) *SchedulingPolicy {
	// Training workloads prefer cost optimization
	if policy, exists := pm.policies["training-cost-optimized"]; exists {
		return policy
	}

	// Create default training policy
	return &SchedulingPolicy{
		Name:      "training-default",
		Algorithm: AlgorithmCostOptimized,
		CostConstraints: &CostConstraints{
			PreferSpotInstances:    true,
			CostOptimizationWeight: 0.8,
		},
		Enabled:  true,
		Priority: 1,
	}
}

// getPolicyForServingWorkload returns policy for serving workloads
func (pm *PolicyManager) getPolicyForServingWorkload(wo *kcloudv1alpha1.WorkloadOptimizer) *SchedulingPolicy {
	// Serving workloads prefer balanced scheduling
	if policy, exists := pm.policies["serving-balanced"]; exists {
		return policy
	}

	// Create default serving policy
	return &SchedulingPolicy{
		Name:      "serving-default",
		Algorithm: AlgorithmBalanced,
		ResourceConstraints: &ResourceConstraints{
			MinCPU:    resource.MustParse("0.5"),
			MinMemory: resource.MustParse("1Gi"),
		},
		Enabled:  true,
		Priority: 2,
	}
}

// getPolicyForInferenceWorkload returns policy for inference workloads
func (pm *PolicyManager) getPolicyForInferenceWorkload(wo *kcloudv1alpha1.WorkloadOptimizer) *SchedulingPolicy {
	// Inference workloads prefer power optimization
	if policy, exists := pm.policies["inference-power-optimized"]; exists {
		return policy
	}

	// Create default inference policy
	return &SchedulingPolicy{
		Name:      "inference-default",
		Algorithm: AlgorithmPowerOptimized,
		PowerConstraints: &PowerConstraints{
			PreferGreenEnergy:       true,
			PowerOptimizationWeight: 0.7,
		},
		Enabled:  true,
		Priority: 3,
	}
}

// getPolicyForBatchWorkload returns policy for batch workloads
func (pm *PolicyManager) getPolicyForBatchWorkload(wo *kcloudv1alpha1.WorkloadOptimizer) *SchedulingPolicy {
	// Batch workloads prefer least loaded scheduling
	if policy, exists := pm.policies["batch-least-loaded"]; exists {
		return policy
	}

	// Create default batch policy
	return &SchedulingPolicy{
		Name:      "batch-default",
		Algorithm: AlgorithmLeastLoaded,
		TimeConstraints: &TimeConstraints{
			MaxSchedulingTime: time.Minute * 5,
		},
		Enabled:  true,
		Priority: 0,
	}
}

// getDefaultPolicy returns the default scheduling policy
func (pm *PolicyManager) getDefaultPolicy() *SchedulingPolicy {
	if policy, exists := pm.policies["default"]; exists {
		return policy
	}

	return &SchedulingPolicy{
		Name:      "default",
		Algorithm: AlgorithmBalanced,
		Enabled:   true,
		Priority:  0,
	}
}

// initializeDefaultPolicies initializes default scheduling policies
func (pm *PolicyManager) initializeDefaultPolicies() {
	// Default balanced policy
	pm.policies["default"] = &SchedulingPolicy{
		Name:      "default",
		Algorithm: AlgorithmBalanced,
		Enabled:   true,
		Priority:  0,
	}

	// Cost-optimized policy
	pm.policies["cost-optimized"] = &SchedulingPolicy{
		Name:      "cost-optimized",
		Algorithm: AlgorithmCostOptimized,
		CostConstraints: &CostConstraints{
			PreferSpotInstances:    true,
			CostOptimizationWeight: 0.9,
		},
		Enabled:  true,
		Priority: 1,
	}

	// Power-optimized policy
	pm.policies["power-optimized"] = &SchedulingPolicy{
		Name:      "power-optimized",
		Algorithm: AlgorithmPowerOptimized,
		PowerConstraints: &PowerConstraints{
			PreferGreenEnergy:       true,
			PowerOptimizationWeight: 0.9,
		},
		Enabled:  true,
		Priority: 1,
	}

	// High-performance policy
	pm.policies["high-performance"] = &SchedulingPolicy{
		Name:      "high-performance",
		Algorithm: AlgorithmPriorityBased,
		ResourceConstraints: &ResourceConstraints{
			MinCPU:    resource.MustParse("2"),
			MinMemory: resource.MustParse("4Gi"),
		},
		Enabled:  true,
		Priority: 2,
	}

	// Low-latency policy
	pm.policies["low-latency"] = &SchedulingPolicy{
		Name:      "low-latency",
		Algorithm: AlgorithmRoundRobin,
		TimeConstraints: &TimeConstraints{
			MaxSchedulingTime: time.Second * 30,
		},
		Enabled:  true,
		Priority: 3,
	}
}

// ValidatePolicy validates a scheduling policy
func (pm *PolicyManager) ValidatePolicy(policy *SchedulingPolicy) error {
	if policy.Name == "" {
		return fmt.Errorf("policy name cannot be empty")
	}

	if policy.Algorithm == "" {
		return fmt.Errorf("policy algorithm cannot be empty")
	}

	// Validate algorithm
	validAlgorithms := map[SchedulingAlgorithm]bool{
		AlgorithmRoundRobin:     true,
		AlgorithmLeastLoaded:    true,
		AlgorithmCostOptimized:  true,
		AlgorithmPowerOptimized: true,
		AlgorithmBalanced:       true,
		AlgorithmPriorityBased:  true,
	}

	if !validAlgorithms[policy.Algorithm] {
		return fmt.Errorf("invalid algorithm: %s", policy.Algorithm)
	}

	// Validate constraints
	if policy.ResourceConstraints != nil {
		if err := pm.validateResourceConstraints(policy.ResourceConstraints); err != nil {
			return fmt.Errorf("invalid resource constraints: %w", err)
		}
	}

	if policy.CostConstraints != nil {
		if err := pm.validateCostConstraints(policy.CostConstraints); err != nil {
			return fmt.Errorf("invalid cost constraints: %w", err)
		}
	}

	if policy.PowerConstraints != nil {
		if err := pm.validatePowerConstraints(policy.PowerConstraints); err != nil {
			return fmt.Errorf("invalid power constraints: %w", err)
		}
	}

	return nil
}

// validateResourceConstraints validates resource constraints
func (pm *PolicyManager) validateResourceConstraints(constraints *ResourceConstraints) error {
	if constraints.MinCPU.Cmp(constraints.MaxCPU) > 0 {
		return fmt.Errorf("min CPU cannot be greater than max CPU")
	}

	if constraints.MinMemory.Cmp(constraints.MaxMemory) > 0 {
		return fmt.Errorf("min memory cannot be greater than max memory")
	}

	if constraints.MinGPU > constraints.MaxGPU {
		return fmt.Errorf("min GPU cannot be greater than max GPU")
	}

	if constraints.MinNPU > constraints.MaxNPU {
		return fmt.Errorf("min NPU cannot be greater than max NPU")
	}

	return nil
}

// validateCostConstraints validates cost constraints
func (pm *PolicyManager) validateCostConstraints(constraints *CostConstraints) error {
	if constraints.MaxCostPerHour < 0 {
		return fmt.Errorf("max cost per hour cannot be negative")
	}

	if constraints.MaxCostPerDay < 0 {
		return fmt.Errorf("max cost per day cannot be negative")
	}

	if constraints.MaxCostPerMonth < 0 {
		return fmt.Errorf("max cost per month cannot be negative")
	}

	if constraints.CostOptimizationWeight < 0 || constraints.CostOptimizationWeight > 1 {
		return fmt.Errorf("cost optimization weight must be between 0 and 1")
	}

	return nil
}

// validatePowerConstraints validates power constraints
func (pm *PolicyManager) validatePowerConstraints(constraints *PowerConstraints) error {
	if constraints.MaxPowerUsage < 0 {
		return fmt.Errorf("max power usage cannot be negative")
	}

	if constraints.PowerOptimizationWeight < 0 || constraints.PowerOptimizationWeight > 1 {
		return fmt.Errorf("power optimization weight must be between 0 and 1")
	}

	return nil
}

// GetPolicyStatistics returns statistics about policy usage
func (pm *PolicyManager) GetPolicyStatistics() map[string]int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	stats := make(map[string]int)
	for _, policyName := range pm.workloadPolicies {
		stats[policyName]++
	}

	// Add policies with zero usage
	for policyName := range pm.policies {
		if _, exists := stats[policyName]; !exists {
			stats[policyName] = 0
		}
	}

	return stats
}
