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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// MetricsCollector collects and exposes Prometheus metrics for the kcloud-operator
type MetricsCollector struct {
	// WorkloadOptimizer metrics
	workloadOptimizerTotal  prometheus.Counter
	workloadOptimizerActive prometheus.Gauge
	workloadOptimizerPhase  *prometheus.GaugeVec
	workloadOptimizerScore  *prometheus.HistogramVec
	workloadOptimizerCost   *prometheus.HistogramVec
	workloadOptimizerPower  *prometheus.HistogramVec

	// Scheduling metrics
	schedulingTotal    prometheus.Counter
	schedulingSuccess  prometheus.Counter
	schedulingFailure  prometheus.Counter
	schedulingDuration *prometheus.HistogramVec
	schedulingScore    *prometheus.HistogramVec

	// Cost optimization metrics
	costOptimizationTotal      prometheus.Counter
	costOptimizationSavings    prometheus.Counter
	costOptimizationViolations prometheus.Counter
	costPerHour                *prometheus.GaugeVec
	budgetUtilization          *prometheus.GaugeVec

	// Power optimization metrics
	powerOptimizationTotal      prometheus.Counter
	powerOptimizationSavings    prometheus.Counter
	powerOptimizationViolations prometheus.Counter
	powerUsage                  *prometheus.GaugeVec
	powerEfficiency             *prometheus.GaugeVec

	// Webhook metrics
	webhookTotal    prometheus.Counter
	webhookSuccess  prometheus.Counter
	webhookFailure  prometheus.Counter
	webhookDuration *prometheus.HistogramVec

	// Node metrics
	nodeUtilization *prometheus.GaugeVec
	nodeCost        *prometheus.GaugeVec
	nodePower       *prometheus.GaugeVec

	// Policy metrics
	policyViolations *prometheus.CounterVec
	policyCompliance *prometheus.GaugeVec
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		// WorkloadOptimizer metrics
		workloadOptimizerTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_workloadoptimizer_total",
			Help: "Total number of WorkloadOptimizer resources processed",
		}),
		workloadOptimizerActive: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "kcloud_workloadoptimizer_active",
			Help: "Number of active WorkloadOptimizer resources",
		}),
		workloadOptimizerPhase: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_workloadoptimizer_phase",
			Help: "Current phase of WorkloadOptimizer resources",
		}, []string{"namespace", "name", "phase", "workload_type"}),
		workloadOptimizerScore: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kcloud_workloadoptimizer_score",
			Help:    "Optimization score of WorkloadOptimizer resources",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11), // 0.0 to 1.0 in 0.1 increments
		}, []string{"namespace", "name", "workload_type"}),
		workloadOptimizerCost: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kcloud_workloadoptimizer_cost_per_hour",
			Help:    "Cost per hour of WorkloadOptimizer resources",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // 0.1 to 51.2
		}, []string{"namespace", "name", "workload_type"}),
		workloadOptimizerPower: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kcloud_workloadoptimizer_power_usage",
			Help:    "Power usage of WorkloadOptimizer resources",
			Buckets: prometheus.ExponentialBuckets(10, 2, 10), // 10 to 5120 Watts
		}, []string{"namespace", "name", "workload_type"}),

		// Scheduling metrics
		schedulingTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_scheduling_total",
			Help: "Total number of scheduling operations",
		}),
		schedulingSuccess: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_scheduling_success_total",
			Help: "Total number of successful scheduling operations",
		}),
		schedulingFailure: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_scheduling_failure_total",
			Help: "Total number of failed scheduling operations",
		}),
		schedulingDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kcloud_scheduling_duration_seconds",
			Help:    "Duration of scheduling operations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to 32s
		}, []string{"algorithm", "workload_type"}),
		schedulingScore: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kcloud_scheduling_score",
			Help:    "Score of scheduling decisions",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11), // 0.0 to 1.0 in 0.1 increments
		}, []string{"algorithm", "workload_type"}),

		// Cost optimization metrics
		costOptimizationTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_cost_optimization_total",
			Help: "Total number of cost optimization operations",
		}),
		costOptimizationSavings: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_cost_optimization_savings_total",
			Help: "Total cost savings from optimization",
		}),
		costOptimizationViolations: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_cost_optimization_violations_total",
			Help: "Total number of cost constraint violations",
		}),
		costPerHour: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_cost_per_hour_usd",
			Help: "Current cost per hour in USD",
		}, []string{"namespace", "name", "workload_type"}),
		budgetUtilization: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_budget_utilization_ratio",
			Help: "Budget utilization ratio (0.0 to 1.0)",
		}, []string{"namespace", "name", "workload_type"}),

		// Power optimization metrics
		powerOptimizationTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_power_optimization_total",
			Help: "Total number of power optimization operations",
		}),
		powerOptimizationSavings: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_power_optimization_savings_total",
			Help: "Total power savings from optimization",
		}),
		powerOptimizationViolations: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_power_optimization_violations_total",
			Help: "Total number of power constraint violations",
		}),
		powerUsage: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_power_usage_watts",
			Help: "Current power usage in Watts",
		}, []string{"namespace", "name", "workload_type"}),
		powerEfficiency: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_power_efficiency_ratio",
			Help: "Power efficiency ratio (0.0 to 1.0)",
		}, []string{"namespace", "name", "workload_type"}),

		// Webhook metrics
		webhookTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_webhook_total",
			Help: "Total number of webhook operations",
		}),
		webhookSuccess: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_webhook_success_total",
			Help: "Total number of successful webhook operations",
		}),
		webhookFailure: promauto.NewCounter(prometheus.CounterOpts{
			Name: "kcloud_webhook_failure_total",
			Help: "Total number of failed webhook operations",
		}),
		webhookDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kcloud_webhook_duration_seconds",
			Help:    "Duration of webhook operations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to 32s
		}, []string{"webhook_type", "operation"}),

		// Node metrics
		nodeUtilization: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_node_utilization_ratio",
			Help: "Node resource utilization ratio (0.0 to 1.0)",
		}, []string{"node", "resource_type"}),
		nodeCost: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_node_cost_per_hour_usd",
			Help: "Node cost per hour in USD",
		}, []string{"node", "instance_type"}),
		nodePower: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_node_power_usage_watts",
			Help: "Node power usage in Watts",
		}, []string{"node", "instance_type"}),

		// Policy metrics
		policyViolations: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "kcloud_policy_violations_total",
			Help: "Total number of policy violations",
		}, []string{"policy_type", "policy_name", "violation_type"}),
		policyCompliance: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kcloud_policy_compliance_ratio",
			Help: "Policy compliance ratio (0.0 to 1.0)",
		}, []string{"policy_type", "policy_name"}),
	}
}

// RecordWorkloadOptimizerCreated records a WorkloadOptimizer creation
func (mc *MetricsCollector) RecordWorkloadOptimizerCreated(namespace, name, workloadType string) {
	mc.workloadOptimizerTotal.Inc()
	mc.workloadOptimizerActive.Inc()
	mc.workloadOptimizerPhase.WithLabelValues(namespace, name, "Pending", workloadType).Set(1)
}

// RecordWorkloadOptimizerDeleted records a WorkloadOptimizer deletion
func (mc *MetricsCollector) RecordWorkloadOptimizerDeleted(namespace, name, workloadType string) {
	mc.workloadOptimizerActive.Dec()
	mc.workloadOptimizerPhase.WithLabelValues(namespace, name, "Pending", workloadType).Set(0)
}

// RecordWorkloadOptimizerPhase records a WorkloadOptimizer phase change
func (mc *MetricsCollector) RecordWorkloadOptimizerPhase(namespace, name, phase, workloadType string) {
	// Reset all phases to 0
	phases := []string{"Pending", "Optimizing", "Optimized", "Failed", "Suspended"}
	for _, p := range phases {
		mc.workloadOptimizerPhase.WithLabelValues(namespace, name, p, workloadType).Set(0)
	}
	// Set current phase to 1
	mc.workloadOptimizerPhase.WithLabelValues(namespace, name, phase, workloadType).Set(1)
}

// RecordWorkloadOptimizerScore records a WorkloadOptimizer optimization score
func (mc *MetricsCollector) RecordWorkloadOptimizerScore(namespace, name, workloadType string, score float64) {
	mc.workloadOptimizerScore.WithLabelValues(namespace, name, workloadType).Observe(score)
}

// RecordWorkloadOptimizerCost records a WorkloadOptimizer cost
func (mc *MetricsCollector) RecordWorkloadOptimizerCost(namespace, name, workloadType string, cost float64) {
	mc.workloadOptimizerCost.WithLabelValues(namespace, name, workloadType).Observe(cost)
	mc.costPerHour.WithLabelValues(namespace, name, workloadType).Set(cost)
}

// RecordWorkloadOptimizerPower records a WorkloadOptimizer power usage
func (mc *MetricsCollector) RecordWorkloadOptimizerPower(namespace, name, workloadType string, power float64) {
	mc.workloadOptimizerPower.WithLabelValues(namespace, name, workloadType).Observe(power)
	mc.powerUsage.WithLabelValues(namespace, name, workloadType).Set(power)
}

// RecordSchedulingOperation records a scheduling operation
func (mc *MetricsCollector) RecordSchedulingOperation(algorithm, workloadType string, success bool, duration time.Duration, score float64) {
	mc.schedulingTotal.Inc()
	if success {
		mc.schedulingSuccess.Inc()
	} else {
		mc.schedulingFailure.Inc()
	}
	mc.schedulingDuration.WithLabelValues(algorithm, workloadType).Observe(duration.Seconds())
	mc.schedulingScore.WithLabelValues(algorithm, workloadType).Observe(score)
}

// RecordCostOptimization records a cost optimization operation
func (mc *MetricsCollector) RecordCostOptimization(savings float64, violation bool) {
	mc.costOptimizationTotal.Inc()
	if savings > 0 {
		mc.costOptimizationSavings.Add(savings)
	}
	if violation {
		mc.costOptimizationViolations.Inc()
	}
}

// RecordPowerOptimization records a power optimization operation
func (mc *MetricsCollector) RecordPowerOptimization(savings float64, violation bool) {
	mc.powerOptimizationTotal.Inc()
	if savings > 0 {
		mc.powerOptimizationSavings.Add(savings)
	}
	if violation {
		mc.powerOptimizationViolations.Inc()
	}
}

// RecordWebhookOperation records a webhook operation
func (mc *MetricsCollector) RecordWebhookOperation(webhookType, operation string, success bool, duration time.Duration) {
	mc.webhookTotal.Inc()
	if success {
		mc.webhookSuccess.Inc()
	} else {
		mc.webhookFailure.Inc()
	}
	mc.webhookDuration.WithLabelValues(webhookType, operation).Observe(duration.Seconds())
}

// RecordNodeMetrics records node metrics
func (mc *MetricsCollector) RecordNodeMetrics(node, resourceType string, utilization float64, cost, power float64, instanceType string) {
	mc.nodeUtilization.WithLabelValues(node, resourceType).Set(utilization)
	mc.nodeCost.WithLabelValues(node, instanceType).Set(cost)
	mc.nodePower.WithLabelValues(node, instanceType).Set(power)
}

// RecordPolicyViolation records a policy violation
func (mc *MetricsCollector) RecordPolicyViolation(policyType, policyName, violationType string) {
	mc.policyViolations.WithLabelValues(policyType, policyName, violationType).Inc()
}

// RecordPolicyCompliance records policy compliance
func (mc *MetricsCollector) RecordPolicyCompliance(policyType, policyName string, compliance float64) {
	mc.policyCompliance.WithLabelValues(policyType, policyName).Set(compliance)
}

// RecordBudgetUtilization records budget utilization
func (mc *MetricsCollector) RecordBudgetUtilization(namespace, name, workloadType string, utilization float64) {
	mc.budgetUtilization.WithLabelValues(namespace, name, workloadType).Set(utilization)
}

// RecordPowerEfficiency records power efficiency
func (mc *MetricsCollector) RecordPowerEfficiency(namespace, name, workloadType string, efficiency float64) {
	mc.powerEfficiency.WithLabelValues(namespace, name, workloadType).Set(efficiency)
}

// StartMetricsCollection starts periodic metrics collection
func (mc *MetricsCollector) StartMetricsCollection(ctx context.Context) {
	log := log.FromContext(ctx)

	ticker := time.NewTicker(30 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("Metrics collection stopped")
				return
			case <-ticker.C:
				mc.collectSystemMetrics(ctx)
			}
		}
	}()

	log.Info("Metrics collection started")
}

// collectSystemMetrics collects system-wide metrics
func (mc *MetricsCollector) collectSystemMetrics(ctx context.Context) {
	log := log.FromContext(ctx)

	// This would typically collect metrics from the Kubernetes API
	// For now, we'll just log that we're collecting metrics
	log.V(1).Info("Collecting system metrics")

	// TODO: Implement actual system metrics collection
	// - Node utilization
	// - Cluster resource usage
	// - Policy compliance rates
	// - Overall optimization scores
}
