package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics provides Prometheus metrics for the policy engine
type Metrics struct {
	// HTTP metrics
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPRequestSize     *prometheus.HistogramVec
	HTTPResponseSize    *prometheus.HistogramVec

	// Policy metrics
	PolicyTotal              prometheus.Gauge
	PolicyActive             prometheus.Gauge
	PolicyInactive           prometheus.Gauge
	PolicyEvaluationsTotal   *prometheus.CounterVec
	PolicyEvaluationDuration *prometheus.HistogramVec
	PolicyValidationTotal    *prometheus.CounterVec

	// Workload metrics
	WorkloadTotal   prometheus.Gauge
	WorkloadRunning prometheus.Gauge
	WorkloadStopped prometheus.Gauge
	WorkloadPending prometheus.Gauge
	WorkloadFailed  prometheus.Gauge

	// Decision metrics
	DecisionTotal    *prometheus.CounterVec
	DecisionDuration *prometheus.HistogramVec
	DecisionSuccess  *prometheus.CounterVec
	DecisionFailure  *prometheus.CounterVec

	// Automation metrics
	AutomationRuleTotal             prometheus.Gauge
	AutomationRuleActive            prometheus.Gauge
	AutomationRuleExecutionsTotal   *prometheus.CounterVec
	AutomationRuleExecutionDuration *prometheus.HistogramVec
	AutomationRuleSuccess           *prometheus.CounterVec
	AutomationRuleFailure           *prometheus.CounterVec

	// Storage metrics
	StorageOperationsTotal   *prometheus.CounterVec
	StorageOperationDuration *prometheus.HistogramVec
	StorageErrorsTotal       *prometheus.CounterVec

	// System metrics
	SystemUptime      prometheus.Gauge
	SystemMemoryUsage prometheus.Gauge
	SystemCPUUsage    prometheus.Gauge
	SystemGoroutines  prometheus.Gauge

	// Cached values for GetMetrics
	cachedMetrics map[string]float64

	logger types.Logger
}

// NewMetrics creates a new metrics instance
func NewMetrics(logger types.Logger) *Metrics {
	return &Metrics{
		logger:        logger,
		cachedMetrics: make(map[string]float64),
	}
}

// Initialize initializes all Prometheus metrics
func (m *Metrics) Initialize() {
	m.logger.Info("Initializing Prometheus metrics...")

	// HTTP metrics
	m.HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status_code"},
	)

	m.HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "policy_engine_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	m.HTTPRequestSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "policy_engine_http_request_size_bytes",
			Help:    "HTTP request size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "endpoint"},
	)

	m.HTTPResponseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "policy_engine_http_response_size_bytes",
			Help:    "HTTP response size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "endpoint"},
	)

	// Policy metrics
	m.PolicyTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_policies_total",
			Help: "Total number of policies",
		},
	)

	m.PolicyActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_policies_active",
			Help: "Number of active policies",
		},
	)

	m.PolicyInactive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_policies_inactive",
			Help: "Number of inactive policies",
		},
	)

	m.PolicyEvaluationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_policy_evaluations_total",
			Help: "Total number of policy evaluations",
		},
		[]string{"policy_type", "policy_name", "result"},
	)

	m.PolicyEvaluationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "policy_engine_policy_evaluation_duration_seconds",
			Help:    "Policy evaluation duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"policy_type", "policy_name"},
	)

	m.PolicyValidationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_policy_validations_total",
			Help: "Total number of policy validations",
		},
		[]string{"policy_type", "result"},
	)

	// Workload metrics
	m.WorkloadTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_workloads_total",
			Help: "Total number of workloads",
		},
	)

	m.WorkloadRunning = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_workloads_running",
			Help: "Number of running workloads",
		},
	)

	m.WorkloadStopped = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_workloads_stopped",
			Help: "Number of stopped workloads",
		},
	)

	m.WorkloadPending = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_workloads_pending",
			Help: "Number of pending workloads",
		},
	)

	m.WorkloadFailed = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_workloads_failed",
			Help: "Number of failed workloads",
		},
	)

	// Decision metrics
	m.DecisionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_decisions_total",
			Help: "Total number of decisions",
		},
		[]string{"decision_type", "policy_type", "result"},
	)

	m.DecisionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "policy_engine_decision_duration_seconds",
			Help:    "Decision duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"decision_type", "policy_type"},
	)

	m.DecisionSuccess = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_decisions_success_total",
			Help: "Total number of successful decisions",
		},
		[]string{"decision_type", "policy_type"},
	)

	m.DecisionFailure = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_decisions_failure_total",
			Help: "Total number of failed decisions",
		},
		[]string{"decision_type", "policy_type", "error_type"},
	)

	// Automation metrics
	m.AutomationRuleTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_automation_rules_total",
			Help: "Total number of automation rules",
		},
	)

	m.AutomationRuleActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_automation_rules_active",
			Help: "Number of active automation rules",
		},
	)

	m.AutomationRuleExecutionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_automation_rule_executions_total",
			Help: "Total number of automation rule executions",
		},
		[]string{"rule_id", "rule_type", "trigger_type"},
	)

	m.AutomationRuleExecutionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "policy_engine_automation_rule_execution_duration_seconds",
			Help:    "Automation rule execution duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"rule_id", "rule_type"},
	)

	m.AutomationRuleSuccess = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_automation_rule_success_total",
			Help: "Total number of successful automation rule executions",
		},
		[]string{"rule_id", "rule_type"},
	)

	m.AutomationRuleFailure = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_automation_rule_failure_total",
			Help: "Total number of failed automation rule executions",
		},
		[]string{"rule_id", "rule_type", "error_type"},
	)

	// Storage metrics
	m.StorageOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_storage_operations_total",
			Help: "Total number of storage operations",
		},
		[]string{"operation", "entity_type"},
	)

	m.StorageOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "policy_engine_storage_operation_duration_seconds",
			Help:    "Storage operation duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "entity_type"},
	)

	m.StorageErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_storage_errors_total",
			Help: "Total number of storage errors",
		},
		[]string{"operation", "entity_type", "error_type"},
	)

	// System metrics
	m.SystemUptime = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_system_uptime_seconds",
			Help: "System uptime in seconds",
		},
	)

	m.SystemMemoryUsage = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_system_memory_usage_bytes",
			Help: "System memory usage in bytes",
		},
	)

	m.SystemCPUUsage = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_system_cpu_usage_percent",
			Help: "System CPU usage percentage",
		},
	)

	m.SystemGoroutines = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "policy_engine_system_goroutines",
			Help: "Number of goroutines",
		},
	)

	m.logger.Info("Prometheus metrics initialized successfully")
}

// RecordHTTPRequest records HTTP request metrics
func (m *Metrics) RecordHTTPRequest(method, endpoint, statusCode string, duration time.Duration, requestSize, responseSize int64) {
	m.HTTPRequestsTotal.WithLabelValues(method, endpoint, statusCode).Inc()
	m.HTTPRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
	m.HTTPRequestSize.WithLabelValues(method, endpoint).Observe(float64(requestSize))
	m.HTTPResponseSize.WithLabelValues(method, endpoint).Observe(float64(responseSize))
}

// RecordPolicyEvaluation records policy evaluation metrics
func (m *Metrics) RecordPolicyEvaluation(policyType, policyName, result string, duration time.Duration) {
	m.PolicyEvaluationsTotal.WithLabelValues(policyType, policyName, result).Inc()
	m.PolicyEvaluationDuration.WithLabelValues(policyType, policyName).Observe(duration.Seconds())
}

// RecordPolicyValidation records policy validation metrics
func (m *Metrics) RecordPolicyValidation(policyType, result string) {
	m.PolicyValidationTotal.WithLabelValues(policyType, result).Inc()
}

// RecordDecision records decision metrics
func (m *Metrics) RecordDecision(decisionType, policyType, result string, duration time.Duration) {
	m.DecisionTotal.WithLabelValues(decisionType, policyType, result).Inc()
	m.DecisionDuration.WithLabelValues(decisionType, policyType).Observe(duration.Seconds())

	if result == "success" {
		m.DecisionSuccess.WithLabelValues(decisionType, policyType).Inc()
	} else {
		m.DecisionFailure.WithLabelValues(decisionType, policyType, result).Inc()
	}
}

// RecordAutomationRuleExecution records automation rule execution metrics
func (m *Metrics) RecordAutomationRuleExecution(ruleID, ruleType, triggerType, result string, duration time.Duration) {
	m.AutomationRuleExecutionsTotal.WithLabelValues(ruleID, ruleType, triggerType).Inc()
	m.AutomationRuleExecutionDuration.WithLabelValues(ruleID, ruleType).Observe(duration.Seconds())

	if result == "success" {
		m.AutomationRuleSuccess.WithLabelValues(ruleID, ruleType).Inc()
	} else {
		m.AutomationRuleFailure.WithLabelValues(ruleID, ruleType, result).Inc()
	}
}

// RecordStorageOperation records storage operation metrics
func (m *Metrics) RecordStorageOperation(operation, entityType string, duration time.Duration) {
	m.StorageOperationsTotal.WithLabelValues(operation, entityType).Inc()
	m.StorageOperationDuration.WithLabelValues(operation, entityType).Observe(duration.Seconds())
}

// RecordStorageError records storage error metrics
func (m *Metrics) RecordStorageError(operation, entityType, errorType string) {
	m.StorageErrorsTotal.WithLabelValues(operation, entityType, errorType).Inc()
}

// UpdatePolicyCounts updates policy count metrics
func (m *Metrics) UpdatePolicyCounts(total, active, inactive int) {
	m.PolicyTotal.Set(float64(total))
	m.PolicyActive.Set(float64(active))
	m.PolicyInactive.Set(float64(inactive))

	// Cache values
	m.cachedMetrics["policies_total"] = float64(total)
	m.cachedMetrics["policies_active"] = float64(active)
	m.cachedMetrics["policies_inactive"] = float64(inactive)
}

// UpdateWorkloadCounts updates workload count metrics
func (m *Metrics) UpdateWorkloadCounts(total, running, stopped, pending, failed int) {
	m.WorkloadTotal.Set(float64(total))
	m.WorkloadRunning.Set(float64(running))
	m.WorkloadStopped.Set(float64(stopped))
	m.WorkloadPending.Set(float64(pending))
	m.WorkloadFailed.Set(float64(failed))

	// Cache values
	m.cachedMetrics["workloads_total"] = float64(total)
	m.cachedMetrics["workloads_running"] = float64(running)
	m.cachedMetrics["workloads_stopped"] = float64(stopped)
	m.cachedMetrics["workloads_pending"] = float64(pending)
	m.cachedMetrics["workloads_failed"] = float64(failed)
}

// UpdateAutomationRuleCounts updates automation rule count metrics
func (m *Metrics) UpdateAutomationRuleCounts(total, active int) {
	m.AutomationRuleTotal.Set(float64(total))
	m.AutomationRuleActive.Set(float64(active))

	// Cache values
	m.cachedMetrics["automation_rules_total"] = float64(total)
	m.cachedMetrics["automation_rules_active"] = float64(active)
}

// UpdateSystemMetrics updates system metrics
func (m *Metrics) UpdateSystemMetrics(uptime time.Duration, memoryUsage, cpuUsage float64, goroutines int) {
	m.SystemUptime.Set(uptime.Seconds())
	m.SystemMemoryUsage.Set(memoryUsage)
	m.SystemCPUUsage.Set(cpuUsage)
	m.SystemGoroutines.Set(float64(goroutines))

	// Cache values
	m.cachedMetrics["system_uptime"] = uptime.Seconds()
	m.cachedMetrics["system_memory_usage"] = memoryUsage
	m.cachedMetrics["system_cpu_usage"] = cpuUsage
	m.cachedMetrics["system_goroutines"] = float64(goroutines)
}

// GetMetrics returns current metrics as a map
func (m *Metrics) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	metrics := make(map[string]interface{})

	// Return cached gauge metrics
	for key, value := range m.cachedMetrics {
		metrics[key] = value
	}

	return metrics, nil
}

// Health checks the health of the metrics system
func (m *Metrics) Health(ctx context.Context) error {
	// Check if metrics are initialized
	if m.HTTPRequestsTotal == nil {
		return fmt.Errorf("HTTP metrics not initialized")
	}

	if m.PolicyTotal == nil {
		return fmt.Errorf("policy metrics not initialized")
	}

	if m.WorkloadTotal == nil {
		return fmt.Errorf("workload metrics not initialized")
	}

	if m.AutomationRuleTotal == nil {
		return fmt.Errorf("automation metrics not initialized")
	}

	return nil
}
