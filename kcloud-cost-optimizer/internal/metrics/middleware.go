package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/internal/types"
)

// MetricsMiddleware provides middleware for collecting HTTP metrics
type MetricsMiddleware struct {
	metrics *Metrics
	logger  types.Logger
}

// NewMetricsMiddleware creates a new metrics middleware
func NewMetricsMiddleware(metrics *Metrics, logger types.Logger) *MetricsMiddleware {
	return &MetricsMiddleware{
		metrics: metrics,
		logger:  logger,
	}
}

// HTTPMetricsMiddleware returns a Gin middleware for HTTP metrics
func (mm *MetricsMiddleware) HTTPMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// Get request size
		requestSize := c.Request.ContentLength
		if requestSize < 0 {
			requestSize = 0
		}

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(startTime)

		// Get response size
		responseSize := int64(c.Writer.Size())

		// Record metrics
		mm.metrics.RecordHTTPRequest(
			c.Request.Method,
			c.FullPath(),
			strconv.Itoa(c.Writer.Status()),
			duration,
			requestSize,
			responseSize,
		)

		mm.logger.Debug("HTTP request metrics recorded",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration", duration,
			"request_size", requestSize,
			"response_size", responseSize,
		)
	}
}

// PolicyMetricsMiddleware returns a middleware for policy-specific metrics
func (mm *MetricsMiddleware) PolicyMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(startTime)

		// Extract policy information from context or path
		policyType := c.GetString("policy_type")
		policyName := c.GetString("policy_name")
		result := c.GetString("result")

		if policyType != "" && policyName != "" {
			mm.metrics.RecordPolicyEvaluation(policyType, policyName, result, duration)
		}

		mm.logger.Debug("Policy metrics recorded",
			"policy_type", policyType,
			"policy_name", policyName,
			"result", result,
			"duration", duration,
		)
	}
}

// WorkloadMetricsMiddleware returns a middleware for workload-specific metrics
func (mm *MetricsMiddleware) WorkloadMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(startTime)

		// Extract workload information from context or path
		workloadID := c.GetString("workload_id")
		workloadType := c.GetString("workload_type")
		result := c.GetString("result")

		if workloadID != "" && workloadType != "" {
			// Record workload-specific metrics if needed
			mm.logger.Debug("Workload metrics recorded",
				"workload_id", workloadID,
				"workload_type", workloadType,
				"result", result,
				"duration", duration,
			)
		}
	}
}

// AutomationMetricsMiddleware returns a middleware for automation-specific metrics
func (mm *MetricsMiddleware) AutomationMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(startTime)

		// Extract automation rule information from context or path
		ruleID := c.GetString("rule_id")
		ruleType := c.GetString("rule_type")
		triggerType := c.GetString("trigger_type")
		result := c.GetString("result")

		if ruleID != "" && ruleType != "" {
			mm.metrics.RecordAutomationRuleExecution(ruleID, ruleType, triggerType, result, duration)
		}

		mm.logger.Debug("Automation metrics recorded",
			"rule_id", ruleID,
			"rule_type", ruleType,
			"trigger_type", triggerType,
			"result", result,
			"duration", duration,
		)
	}
}

// StorageMetricsMiddleware returns a middleware for storage-specific metrics
func (mm *MetricsMiddleware) StorageMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(startTime)

		// Extract storage operation information from context
		operation := c.GetString("storage_operation")
		entityType := c.GetString("storage_entity_type")

		if operation != "" && entityType != "" {
			mm.metrics.RecordStorageOperation(operation, entityType, duration)
		}

		mm.logger.Debug("Storage metrics recorded",
			"operation", operation,
			"entity_type", entityType,
			"duration", duration,
		)
	}
}

// ErrorMetricsMiddleware returns a middleware for error metrics
func (mm *MetricsMiddleware) ErrorMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Process request
		c.Next()

		// Check for errors
		if c.Writer.Status() >= 400 {
			errorType := "http_error"
			operation := c.GetString("storage_operation")
			entityType := c.GetString("storage_entity_type")

			if operation != "" && entityType != "" {
				mm.metrics.RecordStorageError(operation, entityType, errorType)
			}

			mm.logger.Debug("Error metrics recorded",
				"status", c.Writer.Status(),
				"operation", operation,
				"entity_type", entityType,
				"error_type", errorType,
			)
		}
	}
}

// CombinedMetricsMiddleware returns a combined middleware with all metrics
func (mm *MetricsMiddleware) CombinedMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// Get request size
		requestSize := c.Request.ContentLength
		if requestSize < 0 {
			requestSize = 0
		}

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(startTime)

		// Get response size
		responseSize := int64(c.Writer.Size())

		// Record HTTP metrics
		mm.metrics.RecordHTTPRequest(
			c.Request.Method,
			c.FullPath(),
			strconv.Itoa(c.Writer.Status()),
			duration,
			requestSize,
			responseSize,
		)

		// Record specific metrics based on path
		path := c.FullPath()
		if mm.isPolicyPath(path) {
			policyType := c.GetString("policy_type")
			policyName := c.GetString("policy_name")
			result := mm.getResultFromStatus(c.Writer.Status())

			if policyType != "" && policyName != "" {
				mm.metrics.RecordPolicyEvaluation(policyType, policyName, result, duration)
			}
		} else if mm.isWorkloadPath(path) {
			// Workload-specific metrics can be added here
		} else if mm.isAutomationPath(path) {
			ruleID := c.GetString("rule_id")
			ruleType := c.GetString("rule_type")
			triggerType := c.GetString("trigger_type")
			result := mm.getResultFromStatus(c.Writer.Status())

			if ruleID != "" && ruleType != "" {
				mm.metrics.RecordAutomationRuleExecution(ruleID, ruleType, triggerType, result, duration)
			}
		}

		// Record storage metrics if applicable
		operation := c.GetString("storage_operation")
		entityType := c.GetString("storage_entity_type")

		if operation != "" && entityType != "" {
			mm.metrics.RecordStorageOperation(operation, entityType, duration)
		}

		// Record error metrics if applicable
		if c.Writer.Status() >= 400 {
			errorType := "http_error"
			if operation != "" && entityType != "" {
				mm.metrics.RecordStorageError(operation, entityType, errorType)
			}
		}

		mm.logger.Debug("Combined metrics recorded",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration", duration,
			"request_size", requestSize,
			"response_size", responseSize,
		)
	}
}

// Helper methods for path detection
func (mm *MetricsMiddleware) isPolicyPath(path string) bool {
	return contains(path, "/policies")
}

func (mm *MetricsMiddleware) isWorkloadPath(path string) bool {
	return contains(path, "/workloads")
}

func (mm *MetricsMiddleware) isAutomationPath(path string) bool {
	return contains(path, "/automation")
}

func (mm *MetricsMiddleware) getResultFromStatus(status int) string {
	if status >= 200 && status < 300 {
		return "success"
	} else if status >= 400 && status < 500 {
		return "client_error"
	} else if status >= 500 {
		return "server_error"
	}
	return "unknown"
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			indexOf(s, substr) >= 0)))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
