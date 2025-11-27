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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// MetricsCollector collects and aggregates scheduling metrics
type MetricsCollector struct {
	client             client.Client
	metrics            *SchedulingMetrics
	nodeMetrics        map[string]*NodeMetrics
	workloadMetrics    map[string]*WorkloadMetrics
	policyMetrics      map[string]*PolicyMetrics
	mutex              sync.RWMutex
	lastCollection     time.Time
	collectionInterval time.Duration
}

// NodeMetrics tracks metrics for individual nodes
type NodeMetrics struct {
	NodeName            string
	TotalSchedules      int64
	SuccessfulSchedules int64
	FailedSchedules     int64
	AverageScore        float64
	AverageCost         float64
	AveragePower        float64
	Utilization         float64
	LastScheduled       time.Time
	LastUpdated         time.Time
}

// WorkloadMetrics tracks metrics for individual workloads
type WorkloadMetrics struct {
	WorkloadName        string
	WorkloadType        string
	TotalSchedules      int64
	SuccessfulSchedules int64
	FailedSchedules     int64
	AverageScore        float64
	AverageCost         float64
	AveragePower        float64
	AverageDuration     time.Duration
	PreferredNodes      []string
	LastScheduled       time.Time
	LastUpdated         time.Time
}

// PolicyMetrics tracks metrics for scheduling policies
type PolicyMetrics struct {
	PolicyName          string
	Algorithm           SchedulingAlgorithm
	TotalSchedules      int64
	SuccessfulSchedules int64
	FailedSchedules     int64
	AverageScore        float64
	AverageDuration     time.Duration
	CostSavings         float64
	PowerSavings        float64
	LastUsed            time.Time
	LastUpdated         time.Time
}

// SchedulingReport provides a comprehensive scheduling report
type SchedulingReport struct {
	Period              time.Duration
	TotalSchedules      int64
	SuccessfulSchedules int64
	FailedSchedules     int64
	SuccessRate         float64
	AverageScore        float64
	AverageDuration     time.Duration
	TotalCostSavings    float64
	TotalPowerSavings   float64
	TopNodes            []NodeMetrics
	TopWorkloads        []WorkloadMetrics
	PolicyUsage         map[string]int64
	Recommendations     []string
	GeneratedAt         time.Time
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(client client.Client) *MetricsCollector {
	return &MetricsCollector{
		client:             client,
		metrics:            &SchedulingMetrics{},
		nodeMetrics:        make(map[string]*NodeMetrics),
		workloadMetrics:    make(map[string]*WorkloadMetrics),
		policyMetrics:      make(map[string]*PolicyMetrics),
		lastCollection:     time.Now(),
		collectionInterval: time.Minute * 5,
	}
}

// CollectMetrics collects scheduling metrics from the cluster
func (mc *MetricsCollector) CollectMetrics(ctx context.Context) error {
	log := log.FromContext(ctx)

	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	log.V(1).Info("Starting metrics collection")

	// Collect node metrics
	if err := mc.collectNodeMetrics(ctx); err != nil {
		log.Error(err, "Failed to collect node metrics")
	}

	// Collect workload metrics
	if err := mc.collectWorkloadMetrics(ctx); err != nil {
		log.Error(err, "Failed to collect workload metrics")
	}

	// Collect policy metrics
	if err := mc.collectPolicyMetrics(ctx); err != nil {
		log.Error(err, "Failed to collect policy metrics")
	}

	// Update global metrics
	mc.updateGlobalMetrics()

	mc.lastCollection = time.Now()

	log.V(1).Info("Metrics collection completed",
		"nodes", len(mc.nodeMetrics),
		"workloads", len(mc.workloadMetrics),
		"policies", len(mc.policyMetrics))

	return nil
}

// RecordSchedulingEvent records a scheduling event for metrics
func (mc *MetricsCollector) RecordSchedulingEvent(ctx context.Context, event SchedulingEvent) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	// Update node metrics
	if event.NodeName != "" {
		mc.updateNodeMetrics(event.NodeName, event)
	}

	// Update workload metrics
	mc.updateWorkloadMetrics(event.WorkloadID, event)

	// Update global metrics
	mc.updateGlobalMetricsFromEvent(event)
}

// GetSchedulingReport generates a comprehensive scheduling report
func (mc *MetricsCollector) GetSchedulingReport(ctx context.Context, period time.Duration) (*SchedulingReport, error) {
	log := log.FromContext(ctx)

	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	log.V(1).Info("Generating scheduling report", "period", period)

	report := &SchedulingReport{
		Period:      period,
		GeneratedAt: time.Now(),
	}

	// Calculate global metrics
	report.TotalSchedules = mc.metrics.TotalSchedules
	report.SuccessfulSchedules = mc.metrics.SuccessfulSchedules
	report.FailedSchedules = mc.metrics.FailedSchedules
	report.AverageScore = mc.metrics.AverageScore
	report.AverageDuration = mc.metrics.AverageDuration
	report.TotalCostSavings = mc.metrics.CostSavings
	report.TotalPowerSavings = mc.metrics.PowerSavings

	if report.TotalSchedules > 0 {
		report.SuccessRate = float64(report.SuccessfulSchedules) / float64(report.TotalSchedules) * 100
	}

	// Get top nodes by performance
	report.TopNodes = mc.getTopNodes(10)

	// Get top workloads by activity
	report.TopWorkloads = mc.getTopWorkloads(10)

	// Get policy usage statistics
	report.PolicyUsage = mc.getPolicyUsageStats()

	// Generate recommendations
	report.Recommendations = mc.generateRecommendations()

	log.V(1).Info("Scheduling report generated",
		"totalSchedules", report.TotalSchedules,
		"successRate", report.SuccessRate)

	return report, nil
}

// GetNodeMetrics returns metrics for a specific node
func (mc *MetricsCollector) GetNodeMetrics(nodeName string) (*NodeMetrics, bool) {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	metrics, exists := mc.nodeMetrics[nodeName]
	return metrics, exists
}

// GetWorkloadMetrics returns metrics for a specific workload
func (mc *MetricsCollector) GetWorkloadMetrics(workloadName string) (*WorkloadMetrics, bool) {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	metrics, exists := mc.workloadMetrics[workloadName]
	return metrics, exists
}

// GetPolicyMetrics returns metrics for a specific policy
func (mc *MetricsCollector) GetPolicyMetrics(policyName string) (*PolicyMetrics, bool) {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	metrics, exists := mc.policyMetrics[policyName]
	return metrics, exists
}

// Helper methods

func (mc *MetricsCollector) collectNodeMetrics(ctx context.Context) error {
	var nodes corev1.NodeList
	if err := mc.client.List(ctx, &nodes); err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, node := range nodes.Items {
		if _, exists := mc.nodeMetrics[node.Name]; !exists {
			mc.nodeMetrics[node.Name] = &NodeMetrics{
				NodeName:    node.Name,
				LastUpdated: time.Now(),
			}
		}
	}

	return nil
}

func (mc *MetricsCollector) collectWorkloadMetrics(ctx context.Context) error {
	// TODO: Implement when CRD types are properly generated
	// For now, this is a placeholder
	return nil
}

func (mc *MetricsCollector) collectPolicyMetrics(ctx context.Context) error {
	// TODO: Implement when SchedulingPolicy CRD is available
	// For now, initialize with default policies
	defaultPolicies := []string{"default", "cost-optimized", "power-optimized", "high-performance", "low-latency"}

	for _, policyName := range defaultPolicies {
		if _, exists := mc.policyMetrics[policyName]; !exists {
			mc.policyMetrics[policyName] = &PolicyMetrics{
				PolicyName:  policyName,
				LastUpdated: time.Now(),
			}
		}
	}

	return nil
}

func (mc *MetricsCollector) updateNodeMetrics(nodeName string, event SchedulingEvent) {
	if metrics, exists := mc.nodeMetrics[nodeName]; exists {
		metrics.TotalSchedules++
		if event.Success {
			metrics.SuccessfulSchedules++
		} else {
			metrics.FailedSchedules++
		}
		metrics.AverageScore = (metrics.AverageScore*float64(metrics.TotalSchedules-1) + event.Decision.Score) / float64(metrics.TotalSchedules)
		metrics.AverageCost = (metrics.AverageCost*float64(metrics.TotalSchedules-1) + event.Decision.EstimatedCost) / float64(metrics.TotalSchedules)
		metrics.AveragePower = (metrics.AveragePower*float64(metrics.TotalSchedules-1) + event.Decision.EstimatedPower) / float64(metrics.TotalSchedules)
		metrics.LastScheduled = event.Timestamp
		metrics.LastUpdated = time.Now()
	}
}

func (mc *MetricsCollector) updateWorkloadMetrics(workloadID string, event SchedulingEvent) {
	if metrics, exists := mc.workloadMetrics[workloadID]; exists {
		metrics.TotalSchedules++
		if event.Success {
			metrics.SuccessfulSchedules++
		} else {
			metrics.FailedSchedules++
		}
		metrics.AverageScore = (metrics.AverageScore*float64(metrics.TotalSchedules-1) + event.Decision.Score) / float64(metrics.TotalSchedules)
		metrics.AverageCost = (metrics.AverageCost*float64(metrics.TotalSchedules-1) + event.Decision.EstimatedCost) / float64(metrics.TotalSchedules)
		metrics.AveragePower = (metrics.AveragePower*float64(metrics.TotalSchedules-1) + event.Decision.EstimatedPower) / float64(metrics.TotalSchedules)
		metrics.AverageDuration = (metrics.AverageDuration*time.Duration(metrics.TotalSchedules-1) + event.Duration) / time.Duration(metrics.TotalSchedules)
		metrics.LastScheduled = event.Timestamp
		metrics.LastUpdated = time.Now()

		// Track preferred nodes
		if event.Success && event.NodeName != "" {
			metrics.PreferredNodes = append(metrics.PreferredNodes, event.NodeName)
			// Keep only last 10 preferred nodes
			if len(metrics.PreferredNodes) > 10 {
				metrics.PreferredNodes = metrics.PreferredNodes[1:]
			}
		}
	}
}

func (mc *MetricsCollector) updateGlobalMetrics() {
	// Calculate global metrics from individual metrics
	totalSchedules := int64(0)
	successfulSchedules := int64(0)
	failedSchedules := int64(0)
	totalScore := 0.0
	_ = time.Duration(0) // totalDuration placeholder
	totalCostSavings := 0.0
	totalPowerSavings := 0.0

	for _, nodeMetrics := range mc.nodeMetrics {
		totalSchedules += nodeMetrics.TotalSchedules
		successfulSchedules += nodeMetrics.SuccessfulSchedules
		failedSchedules += nodeMetrics.FailedSchedules
		totalScore += nodeMetrics.AverageScore * float64(nodeMetrics.TotalSchedules)
		totalCostSavings += nodeMetrics.AverageCost * float64(nodeMetrics.TotalSchedules)
		totalPowerSavings += nodeMetrics.AveragePower * float64(nodeMetrics.TotalSchedules)
	}

	if totalSchedules > 0 {
		mc.metrics.TotalSchedules = totalSchedules
		mc.metrics.SuccessfulSchedules = successfulSchedules
		mc.metrics.FailedSchedules = failedSchedules
		mc.metrics.AverageScore = totalScore / float64(totalSchedules)
		mc.metrics.CostSavings = totalCostSavings
		mc.metrics.PowerSavings = totalPowerSavings
	}

	mc.metrics.LastUpdated = time.Now()
}

func (mc *MetricsCollector) updateGlobalMetricsFromEvent(event SchedulingEvent) {
	mc.metrics.TotalSchedules++
	if event.Success {
		mc.metrics.SuccessfulSchedules++
	} else {
		mc.metrics.FailedSchedules++
	}
	mc.metrics.AverageScore = (mc.metrics.AverageScore*float64(mc.metrics.TotalSchedules-1) + event.Decision.Score) / float64(mc.metrics.TotalSchedules)
	mc.metrics.AverageDuration = (mc.metrics.AverageDuration*time.Duration(mc.metrics.TotalSchedules-1) + event.Duration) / time.Duration(mc.metrics.TotalSchedules)
	mc.metrics.LastUpdated = time.Now()
}

func (mc *MetricsCollector) getTopNodes(limit int) []NodeMetrics {
	var nodes []NodeMetrics
	for _, metrics := range mc.nodeMetrics {
		nodes = append(nodes, *metrics)
	}

	// Sort by success rate and average score
	for i := 0; i < len(nodes)-1; i++ {
		for j := i + 1; j < len(nodes); j++ {
			scoreI := mc.calculateNodePerformanceScore(&nodes[i])
			scoreJ := mc.calculateNodePerformanceScore(&nodes[j])
			if scoreI < scoreJ {
				nodes[i], nodes[j] = nodes[j], nodes[i]
			}
		}
	}

	if len(nodes) > limit {
		nodes = nodes[:limit]
	}

	return nodes
}

func (mc *MetricsCollector) getTopWorkloads(limit int) []WorkloadMetrics {
	var workloads []WorkloadMetrics
	for _, metrics := range mc.workloadMetrics {
		workloads = append(workloads, *metrics)
	}

	// Sort by total schedules
	for i := 0; i < len(workloads)-1; i++ {
		for j := i + 1; j < len(workloads); j++ {
			if workloads[i].TotalSchedules < workloads[j].TotalSchedules {
				workloads[i], workloads[j] = workloads[j], workloads[i]
			}
		}
	}

	if len(workloads) > limit {
		workloads = workloads[:limit]
	}

	return workloads
}

func (mc *MetricsCollector) getPolicyUsageStats() map[string]int64 {
	stats := make(map[string]int64)
	for _, metrics := range mc.policyMetrics {
		stats[metrics.PolicyName] = metrics.TotalSchedules
	}
	return stats
}

func (mc *MetricsCollector) calculateNodePerformanceScore(metrics *NodeMetrics) float64 {
	if metrics.TotalSchedules == 0 {
		return 0
	}

	successRate := float64(metrics.SuccessfulSchedules) / float64(metrics.TotalSchedules)
	return successRate * metrics.AverageScore
}

func (mc *MetricsCollector) generateRecommendations() []string {
	var recommendations []string

	// Analyze success rate
	if mc.metrics.TotalSchedules > 0 {
		successRate := float64(mc.metrics.SuccessfulSchedules) / float64(mc.metrics.TotalSchedules)
		if successRate < 0.8 {
			recommendations = append(recommendations, "Scheduling success rate is below 80%. Consider reviewing node capacity and workload requirements.")
		}
	}

	// Analyze average score
	if mc.metrics.AverageScore < 0.6 {
		recommendations = append(recommendations, "Average scheduling score is low. Consider optimizing node selection algorithms.")
	}

	// Analyze duration
	if mc.metrics.AverageDuration > time.Minute*2 {
		recommendations = append(recommendations, "Average scheduling duration is high. Consider optimizing scheduling algorithms.")
	}

	// Analyze node utilization
	underutilizedNodes := 0
	for _, nodeMetrics := range mc.nodeMetrics {
		if nodeMetrics.TotalSchedules < 5 {
			underutilizedNodes++
		}
	}

	if underutilizedNodes > len(mc.nodeMetrics)/2 {
		recommendations = append(recommendations, "Many nodes are underutilized. Consider workload redistribution.")
	}

	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Scheduling performance is optimal. No immediate recommendations.")
	}

	return recommendations
}

// StartPeriodicCollection starts periodic metrics collection
func (mc *MetricsCollector) StartPeriodicCollection(ctx context.Context) {
	ticker := time.NewTicker(mc.collectionInterval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				if err := mc.CollectMetrics(ctx); err != nil {
					log.FromContext(ctx).Error(err, "Periodic metrics collection failed")
				}
			}
		}
	}()
}
