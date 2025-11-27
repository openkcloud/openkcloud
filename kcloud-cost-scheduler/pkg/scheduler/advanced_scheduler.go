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
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// AdvancedScheduler provides advanced scheduling capabilities
type AdvancedScheduler struct {
	*Scheduler
	resourceReservations map[string]*ResourceReservation
	schedulingHistory    []SchedulingEvent
	metrics              *SchedulingMetrics
	mutex                sync.RWMutex
}

// ResourceReservation represents a resource reservation on a node
type ResourceReservation struct {
	NodeName       string
	ReservedCPU    resource.Quantity
	ReservedMemory resource.Quantity
	ReservedGPU    int32
	ReservedNPU    int32
	WorkloadID     string
	ExpiresAt      time.Time
	Priority       int32
}

// SchedulingEvent represents a scheduling event for history tracking
type SchedulingEvent struct {
	Timestamp  time.Time
	WorkloadID string
	NodeName   string
	Decision   SchedulingDecision
	Duration   time.Duration
	Success    bool
	Reason     string
}

// SchedulingMetrics tracks scheduling performance metrics
type SchedulingMetrics struct {
	TotalSchedules      int64
	SuccessfulSchedules int64
	FailedSchedules     int64
	AverageScore        float64
	AverageDuration     time.Duration
	CostSavings         float64
	PowerSavings        float64
	LastUpdated         time.Time
}

// SchedulingPolicy defines advanced scheduling policies
type SchedulingPolicy struct {
	Name                string
	Algorithm           SchedulingAlgorithm
	ResourceConstraints *ResourceConstraints
	CostConstraints     *CostConstraints
	PowerConstraints    *PowerConstraints
	TimeConstraints     *TimeConstraints
	Enabled             bool
	Priority            int
}

// SchedulingAlgorithm defines different scheduling algorithms
type SchedulingAlgorithm string

const (
	AlgorithmRoundRobin     SchedulingAlgorithm = "round_robin"
	AlgorithmLeastLoaded    SchedulingAlgorithm = "least_loaded"
	AlgorithmCostOptimized  SchedulingAlgorithm = "cost_optimized"
	AlgorithmPowerOptimized SchedulingAlgorithm = "power_optimized"
	AlgorithmBalanced       SchedulingAlgorithm = "balanced"
	AlgorithmPriorityBased  SchedulingAlgorithm = "priority_based"
)

// ResourceConstraints defines resource-based constraints
type ResourceConstraints struct {
	MinCPU    resource.Quantity
	MaxCPU    resource.Quantity
	MinMemory resource.Quantity
	MaxMemory resource.Quantity
	MinGPU    int32
	MaxGPU    int32
	MinNPU    int32
	MaxNPU    int32
}

// CostConstraints defines cost-based constraints
type CostConstraints struct {
	MaxCostPerHour         float64
	MaxCostPerDay          float64
	MaxCostPerMonth        float64
	PreferSpotInstances    bool
	CostOptimizationWeight float64
}

// PowerConstraints defines power-based constraints
type PowerConstraints struct {
	MaxPowerUsage           float64
	PreferGreenEnergy       bool
	PowerOptimizationWeight float64
}

// TimeConstraints defines time-based constraints
type TimeConstraints struct {
	MaxSchedulingTime  time.Duration
	PreferredTimeSlots []TimeSlot
	AvoidTimeSlots     []TimeSlot
}

// TimeSlot represents a time slot
type TimeSlot struct {
	Start time.Time
	End   time.Time
}

// NewAdvancedScheduler creates a new advanced scheduler
func NewAdvancedScheduler() *AdvancedScheduler {
	return &AdvancedScheduler{
		Scheduler:            NewScheduler(),
		resourceReservations: make(map[string]*ResourceReservation),
		schedulingHistory:    []SchedulingEvent{},
		metrics: &SchedulingMetrics{
			LastUpdated: time.Now(),
		},
	}
}

// ScheduleWithPolicy schedules a workload using advanced policies
func (as *AdvancedScheduler) ScheduleWithPolicy(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodes []corev1.Node, policy *SchedulingPolicy) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	if policy == nil {
		// Use default policy
		policy = as.getDefaultPolicy()
	}

	startTime := time.Now()
	log.Info("Starting advanced scheduling",
		"workload", wo.Name,
		"algorithm", policy.Algorithm,
		"nodeCount", len(nodes))

	// Filter nodes based on constraints
	filteredNodes, err := as.filterNodesByConstraints(nodes, policy)
	if err != nil {
		return nil, fmt.Errorf("failed to filter nodes: %w", err)
	}

	if len(filteredNodes) == 0 {
		return nil, fmt.Errorf("no nodes meet the scheduling constraints")
	}

	// Apply scheduling algorithm
	var decision *SchedulingDecision
	switch policy.Algorithm {
	case AlgorithmRoundRobin:
		decision, err = as.scheduleRoundRobin(ctx, wo, filteredNodes, policy)
	case AlgorithmLeastLoaded:
		decision, err = as.scheduleLeastLoaded(ctx, wo, filteredNodes, policy)
	case AlgorithmCostOptimized:
		decision, err = as.scheduleCostOptimized(ctx, wo, filteredNodes, policy)
	case AlgorithmPowerOptimized:
		decision, err = as.schedulePowerOptimized(ctx, wo, filteredNodes, policy)
	case AlgorithmBalanced:
		decision, err = as.scheduleBalanced(ctx, wo, filteredNodes, policy)
	case AlgorithmPriorityBased:
		decision, err = as.schedulePriorityBased(ctx, wo, filteredNodes, policy)
	default:
		decision, err = as.scheduleBalanced(ctx, wo, filteredNodes, policy)
	}

	if err != nil {
		as.recordSchedulingEvent(ctx, wo.Name, "", SchedulingDecision{},
			time.Since(startTime), false, err.Error())
		return nil, err
	}

	// Reserve resources if scheduling is successful
	if decision != nil {
		err = as.reserveResources(ctx, wo, decision.SelectedNode, policy)
		if err != nil {
			log.Error(err, "Failed to reserve resources", "node", decision.SelectedNode)
			// Continue with scheduling even if reservation fails
		}
	}

	// Record scheduling event
	as.recordSchedulingEvent(ctx, wo.Name, decision.SelectedNode, *decision,
		time.Since(startTime), true, "Success")

	// Update metrics
	as.updateMetrics(*decision, time.Since(startTime))

	log.Info("Advanced scheduling completed",
		"selectedNode", decision.SelectedNode,
		"score", decision.Score,
		"duration", time.Since(startTime))

	return decision, nil
}

// scheduleRoundRobin implements round-robin scheduling
func (as *AdvancedScheduler) scheduleRoundRobin(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodes []corev1.Node, policy *SchedulingPolicy) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	// Sort nodes by name for consistent round-robin
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	// Find the least recently used node
	var selectedNode *corev1.Node
	minLastUsed := time.Now()

	for i := range nodes {
		node := &nodes[i]
		lastUsed := as.getNodeLastUsedTime(node.Name)
		if lastUsed.Before(minLastUsed) {
			minLastUsed = lastUsed
			selectedNode = node
		}
	}

	if selectedNode == nil {
		selectedNode = &nodes[0] // Fallback to first node
	}

	decision, err := as.evaluateNode(ctx, wo, *selectedNode)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Round-robin scheduling selected node",
		"node", selectedNode.Name,
		"lastUsed", minLastUsed)

	return decision, nil
}

// scheduleLeastLoaded implements least-loaded scheduling
func (as *AdvancedScheduler) scheduleLeastLoaded(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodes []corev1.Node, policy *SchedulingPolicy) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	var bestNode *corev1.Node
	bestScore := -1.0

	for i := range nodes {
		node := &nodes[i]
		utilization := as.calculateNodeUtilization(node)
		score := 1.0 - utilization // Lower utilization = higher score

		if score > bestScore {
			bestScore = score
			bestNode = node
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no suitable node found")
	}

	decision, err := as.evaluateNode(ctx, wo, *bestNode)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Least-loaded scheduling selected node",
		"node", bestNode.Name,
		"utilization", as.calculateNodeUtilization(bestNode))

	return decision, nil
}

// scheduleCostOptimized implements cost-optimized scheduling
func (as *AdvancedScheduler) scheduleCostOptimized(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodes []corev1.Node, policy *SchedulingPolicy) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	var bestNode *corev1.Node
	bestCost := math.Inf(1)

	for i := range nodes {
		node := &nodes[i]
		cost := as.estimateNodeCost(wo, node)

		if cost < bestCost {
			bestCost = cost
			bestNode = node
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no suitable node found")
	}

	decision, err := as.evaluateNode(ctx, wo, *bestNode)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Cost-optimized scheduling selected node",
		"node", bestNode.Name,
		"estimatedCost", bestCost)

	return decision, nil
}

// schedulePowerOptimized implements power-optimized scheduling
func (as *AdvancedScheduler) schedulePowerOptimized(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodes []corev1.Node, policy *SchedulingPolicy) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	var bestNode *corev1.Node
	bestPower := math.Inf(1)

	for i := range nodes {
		node := &nodes[i]
		power := as.estimateNodePower(wo, node)

		if power < bestPower {
			bestPower = power
			bestNode = node
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no suitable node found")
	}

	decision, err := as.evaluateNode(ctx, wo, *bestNode)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Power-optimized scheduling selected node",
		"node", bestNode.Name,
		"estimatedPower", bestPower)

	return decision, nil
}

// scheduleBalanced implements balanced scheduling (default algorithm)
func (as *AdvancedScheduler) scheduleBalanced(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodes []corev1.Node, policy *SchedulingPolicy) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	var bestNode *corev1.Node
	bestScore := -1.0

	for i := range nodes {
		node := &nodes[i]

		// Calculate balanced score considering multiple factors
		costScore := as.calculateCostScore(wo, node)
		powerScore := as.calculatePowerScore(wo, node)
		resourceScore := as.calculateResourceScore(wo, node)

		// Weighted combination
		balancedScore := (costScore*0.4 + powerScore*0.3 + resourceScore*0.3)

		if balancedScore > bestScore {
			bestScore = balancedScore
			bestNode = node
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no suitable node found")
	}

	decision, err := as.evaluateNode(ctx, wo, *bestNode)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Balanced scheduling selected node",
		"node", bestNode.Name,
		"balancedScore", bestScore)

	return decision, nil
}

// schedulePriorityBased implements priority-based scheduling
func (as *AdvancedScheduler) schedulePriorityBased(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodes []corev1.Node, policy *SchedulingPolicy) (*SchedulingDecision, error) {
	log := log.FromContext(ctx)

	// Sort nodes by priority (considering workload priority and node characteristics)
	sort.Slice(nodes, func(i, j int) bool {
		priorityI := as.calculateNodePriority(wo, &nodes[i])
		priorityJ := as.calculateNodePriority(wo, &nodes[j])
		return priorityI > priorityJ
	})

	// Select the highest priority node that meets requirements
	for i := range nodes {
		node := &nodes[i]
		if as.nodeMeetsRequirements(wo, *node) {
			decision, err := as.evaluateNode(ctx, wo, *node)
			if err != nil {
				continue
			}

			log.V(1).Info("Priority-based scheduling selected node",
				"node", node.Name,
				"priority", as.calculateNodePriority(wo, node))

			return decision, nil
		}
	}

	return nil, fmt.Errorf("no suitable node found for priority-based scheduling")
}

// Helper methods

func (as *AdvancedScheduler) getDefaultPolicy() *SchedulingPolicy {
	return &SchedulingPolicy{
		Name:      "default",
		Algorithm: AlgorithmBalanced,
		Enabled:   true,
		Priority:  0,
	}
}

func (as *AdvancedScheduler) filterNodesByConstraints(nodes []corev1.Node, policy *SchedulingPolicy) ([]corev1.Node, error) {
	var filteredNodes []corev1.Node

	for _, node := range nodes {
		if as.nodeMeetsPolicyConstraints(&node, policy) {
			filteredNodes = append(filteredNodes, node)
		}
	}

	return filteredNodes, nil
}

func (as *AdvancedScheduler) nodeMeetsPolicyConstraints(node *corev1.Node, policy *SchedulingPolicy) bool {
	// Check resource constraints
	if policy.ResourceConstraints != nil {
		if !as.nodeMeetsResourceConstraints(node, policy.ResourceConstraints) {
			return false
		}
	}

	// Check cost constraints
	if policy.CostConstraints != nil {
		if !as.nodeMeetsCostConstraints(node, policy.CostConstraints) {
			return false
		}
	}

	// Check power constraints
	if policy.PowerConstraints != nil {
		if !as.nodeMeetsPowerConstraints(node, policy.PowerConstraints) {
			return false
		}
	}

	return true
}

func (as *AdvancedScheduler) nodeMeetsResourceConstraints(node *corev1.Node, constraints *ResourceConstraints) bool {
	allocatable := node.Status.Allocatable

	// Check CPU constraints
	if constraints.MinCPU.Cmp(allocatable[corev1.ResourceCPU]) > 0 {
		return false
	}
	if constraints.MaxCPU.Cmp(allocatable[corev1.ResourceCPU]) < 0 {
		return false
	}

	// Check Memory constraints
	if constraints.MinMemory.Cmp(allocatable[corev1.ResourceMemory]) > 0 {
		return false
	}
	if constraints.MaxMemory.Cmp(allocatable[corev1.ResourceMemory]) < 0 {
		return false
	}

	// Check GPU constraints
	if constraints.MinGPU > 0 {
		gpuAllocatable := allocatable["nvidia.com/gpu"]
		minGPUQuantity := resource.MustParse(fmt.Sprintf("%d", constraints.MinGPU))
		if minGPUQuantity.Cmp(gpuAllocatable) > 0 {
			return false
		}
	}

	return true
}

func (as *AdvancedScheduler) nodeMeetsCostConstraints(node *corev1.Node, constraints *CostConstraints) bool {
	// Check spot instance preference
	if constraints.PreferSpotInstances {
		if node.Labels["lifecycle"] != "spot" {
			return false
		}
	}

	// Check cost limits (simplified)
	if constraints.MaxCostPerHour > 0 {
		nodeCost := as.getNodeCostPerHour(node)
		if nodeCost > constraints.MaxCostPerHour {
			return false
		}
	}

	return true
}

func (as *AdvancedScheduler) nodeMeetsPowerConstraints(node *corev1.Node, constraints *PowerConstraints) bool {
	// Check green energy preference
	if constraints.PreferGreenEnergy {
		if node.Labels["energy-source"] != "renewable" {
			return false
		}
	}

	// Check power limits (simplified)
	if constraints.MaxPowerUsage > 0 {
		nodePower := as.getNodePowerUsage(node)
		if nodePower > constraints.MaxPowerUsage {
			return false
		}
	}

	return true
}

func (as *AdvancedScheduler) calculateNodeUtilization(node *corev1.Node) float64 {
	// Simplified utilization calculation
	// In reality, this would consider actual resource usage
	return 0.5 // Placeholder
}

func (as *AdvancedScheduler) estimateNodeCost(wo *kcloudv1alpha1.WorkloadOptimizer, node *corev1.Node) float64 {
	// Use the base scheduler's cost estimation
	return as.estimateNodeCost(wo, node)
}

func (as *AdvancedScheduler) estimateNodePower(wo *kcloudv1alpha1.WorkloadOptimizer, node *corev1.Node) float64 {
	// Use the base scheduler's power estimation
	return as.estimateNodePower(wo, node)
}

func (as *AdvancedScheduler) calculateCostScore(wo *kcloudv1alpha1.WorkloadOptimizer, node *corev1.Node) float64 {
	// Use the base scheduler's cost score calculation
	return as.calculateCostScore(wo, node)
}

func (as *AdvancedScheduler) calculatePowerScore(wo *kcloudv1alpha1.WorkloadOptimizer, node *corev1.Node) float64 {
	// Use the base scheduler's power score calculation
	return as.calculatePowerScore(wo, node)
}

func (as *AdvancedScheduler) calculateResourceScore(wo *kcloudv1alpha1.WorkloadOptimizer, node *corev1.Node) float64 {
	// Use the base scheduler's resource score calculation
	return as.calculateResourceScore(wo, node)
}

func (as *AdvancedScheduler) calculateNodePriority(wo *kcloudv1alpha1.WorkloadOptimizer, node *corev1.Node) float64 {
	priority := float64(wo.Spec.Priority)

	// Adjust priority based on node characteristics
	if node.Labels["priority-tier"] == "high" {
		priority *= 1.2
	} else if node.Labels["priority-tier"] == "low" {
		priority *= 0.8
	}

	return priority
}

func (as *AdvancedScheduler) getNodeLastUsedTime(nodeName string) time.Time {
	as.mutex.RLock()
	defer as.mutex.RUnlock()

	// Find the most recent scheduling event for this node
	for i := len(as.schedulingHistory) - 1; i >= 0; i-- {
		if as.schedulingHistory[i].NodeName == nodeName {
			return as.schedulingHistory[i].Timestamp
		}
	}

	return time.Time{} // Zero time if never used
}

func (as *AdvancedScheduler) reserveResources(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer,
	nodeName string, policy *SchedulingPolicy) error {
	as.mutex.Lock()
	defer as.mutex.Unlock()

	reservation := &ResourceReservation{
		NodeName:       nodeName,
		ReservedCPU:    resource.MustParse(wo.Spec.Resources.CPU),
		ReservedMemory: resource.MustParse(wo.Spec.Resources.Memory),
		ReservedGPU:    wo.Spec.Resources.GPU,
		ReservedNPU:    wo.Spec.Resources.NPU,
		WorkloadID:     wo.Name,
		ExpiresAt:      time.Now().Add(time.Hour * 24), // 24-hour reservation
		Priority:       wo.Spec.Priority,
	}

	as.resourceReservations[wo.Name] = reservation
	return nil
}

func (as *AdvancedScheduler) recordSchedulingEvent(ctx context.Context, workloadID, nodeName string,
	decision SchedulingDecision, duration time.Duration, success bool, reason string) {
	as.mutex.Lock()
	defer as.mutex.Unlock()

	event := SchedulingEvent{
		Timestamp:  time.Now(),
		WorkloadID: workloadID,
		NodeName:   nodeName,
		Decision:   decision,
		Duration:   duration,
		Success:    success,
		Reason:     reason,
	}

	as.schedulingHistory = append(as.schedulingHistory, event)

	// Keep only last 1000 events
	if len(as.schedulingHistory) > 1000 {
		as.schedulingHistory = as.schedulingHistory[1:]
	}
}

func (as *AdvancedScheduler) updateMetrics(decision SchedulingDecision, duration time.Duration) {
	as.mutex.Lock()
	defer as.mutex.Unlock()

	as.metrics.TotalSchedules++
	as.metrics.SuccessfulSchedules++
	as.metrics.AverageScore = (as.metrics.AverageScore*float64(as.metrics.SuccessfulSchedules-1) + decision.Score) / float64(as.metrics.SuccessfulSchedules)
	as.metrics.AverageDuration = (as.metrics.AverageDuration*time.Duration(as.metrics.SuccessfulSchedules-1) + duration) / time.Duration(as.metrics.SuccessfulSchedules)
	as.metrics.LastUpdated = time.Now()
}

func (as *AdvancedScheduler) getNodeCostPerHour(node *corev1.Node) float64 {
	// Simplified cost calculation
	if costLabel, exists := node.Labels["cost-per-hour"]; exists {
		switch costLabel {
		case "low":
			return 5.0
		case "medium":
			return 10.0
		case "high":
			return 20.0
		}
	}
	return 10.0 // Default cost
}

func (as *AdvancedScheduler) getNodePowerUsage(node *corev1.Node) float64 {
	// Simplified power calculation
	if powerLabel, exists := node.Labels["power-usage"]; exists {
		switch powerLabel {
		case "low":
			return 100.0
		case "medium":
			return 200.0
		case "high":
			return 400.0
		}
	}
	return 200.0 // Default power
}

// GetMetrics returns current scheduling metrics
func (as *AdvancedScheduler) GetMetrics() *SchedulingMetrics {
	as.mutex.RLock()
	defer as.mutex.RUnlock()

	// Return a copy to avoid race conditions
	metrics := *as.metrics
	return &metrics
}

// GetSchedulingHistory returns recent scheduling history
func (as *AdvancedScheduler) GetSchedulingHistory(limit int) []SchedulingEvent {
	as.mutex.RLock()
	defer as.mutex.RUnlock()

	if limit <= 0 || limit > len(as.schedulingHistory) {
		limit = len(as.schedulingHistory)
	}

	// Return last 'limit' events
	start := len(as.schedulingHistory) - limit
	if start < 0 {
		start = 0
	}

	history := make([]SchedulingEvent, limit)
	copy(history, as.schedulingHistory[start:])
	return history
}

// CleanupExpiredReservations removes expired resource reservations
func (as *AdvancedScheduler) CleanupExpiredReservations() {
	as.mutex.Lock()
	defer as.mutex.Unlock()

	now := time.Now()
	for workloadID, reservation := range as.resourceReservations {
		if now.After(reservation.ExpiresAt) {
			delete(as.resourceReservations, workloadID)
		}
	}
}
