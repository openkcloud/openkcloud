package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/internal/automation"
	"github.com/kcloud-opt/policy/internal/evaluator"
	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// HealthHandler handles health check and system status requests
type HealthHandler struct {
	storage    storage.StorageManager
	evaluator  evaluator.EvaluationEngine
	automation automation.AutomationEngine
	logger     types.Logger
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(storage storage.StorageManager, evaluator evaluator.EvaluationEngine, automation automation.AutomationEngine, logger types.Logger) *HealthHandler {
	return &HealthHandler{
		storage:    storage,
		evaluator:  evaluator,
		automation: automation,
		logger:     logger,
	}
}

// Health handles GET /health
func (h *HealthHandler) Health(c *gin.Context) {
	startTime := time.Now()

	// Basic health check
	status := "healthy"
	details := make(map[string]interface{})

	// Check storage health
	if err := h.storage.Health(c.Request.Context()); err != nil {
		status = "unhealthy"
		details["storage"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		details["storage"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Check evaluator health
	if err := h.evaluator.Health(c.Request.Context()); err != nil {
		status = "unhealthy"
		details["evaluator"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		details["evaluator"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Check automation health
	if err := h.automation.Health(c.Request.Context()); err != nil {
		status = "unhealthy"
		details["automation"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		details["automation"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Determine HTTP status code
	httpStatus := http.StatusOK
	if status == "unhealthy" {
		httpStatus = http.StatusServiceUnavailable
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("health check completed", "status", status)

	c.JSON(httpStatus, gin.H{
		"status":   status,
		"service":  "policy-engine",
		"version":  "1.0.0",
		"details":  details,
		"duration": duration.String(),
	})
}

// Readiness handles GET /ready
func (h *HealthHandler) Readiness(c *gin.Context) {
	startTime := time.Now()

	// Check if all components are ready
	ready := true
	details := make(map[string]interface{})

	// Check storage readiness
	if err := h.storage.Health(c.Request.Context()); err != nil {
		ready = false
		details["storage"] = map[string]interface{}{
			"ready": false,
			"error": err.Error(),
		}
	} else {
		details["storage"] = map[string]interface{}{
			"ready": true,
		}
	}

	// Check evaluator readiness
	if err := h.evaluator.Health(c.Request.Context()); err != nil {
		ready = false
		details["evaluator"] = map[string]interface{}{
			"ready": false,
			"error": err.Error(),
		}
	} else {
		details["evaluator"] = map[string]interface{}{
			"ready": true,
		}
	}

	// Check automation readiness
	if err := h.automation.Health(c.Request.Context()); err != nil {
		ready = false
		details["automation"] = map[string]interface{}{
			"ready": false,
			"error": err.Error(),
		}
	} else {
		details["automation"] = map[string]interface{}{
			"ready": true,
		}
	}

	// Determine HTTP status code
	httpStatus := http.StatusOK
	if !ready {
		httpStatus = http.StatusServiceUnavailable
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("readiness check completed", "ready", ready)

	c.JSON(httpStatus, gin.H{
		"ready":    ready,
		"service":  "policy-engine",
		"details":  details,
		"duration": duration.String(),
	})
}

// Liveness handles GET /live
func (h *HealthHandler) Liveness(c *gin.Context) {
	startTime := time.Now()

	// Simple liveness check - just return OK if the service is running
	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("liveness check completed")

	c.JSON(http.StatusOK, gin.H{
		"alive":    true,
		"service":  "policy-engine",
		"duration": duration.String(),
	})
}

// SystemStatus handles GET /status
func (h *HealthHandler) SystemStatus(c *gin.Context) {
	startTime := time.Now()

	// Get comprehensive system status
	status := make(map[string]interface{})

	// Storage status
	if err := h.storage.Health(c.Request.Context()); err != nil {
		status["storage"] = map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		}
	} else {
		status["storage"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Evaluator status
	if err := h.evaluator.Health(c.Request.Context()); err != nil {
		status["evaluator"] = map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		}
	} else {
		status["evaluator"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Automation status
	if err := h.automation.Health(c.Request.Context()); err != nil {
		status["automation"] = map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		}
	} else {
		status["automation"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// System info
	status["system"] = map[string]interface{}{
		"service": "policy-engine",
		"version": "1.0.0",
		"uptime":  time.Since(startTime).String(),
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("system status retrieved")

	c.JSON(http.StatusOK, gin.H{
		"status":   status,
		"duration": duration.String(),
	})
}

// Metrics handles GET /metrics
func (h *HealthHandler) Metrics(c *gin.Context) {
	startTime := time.Now()

	// Get system metrics
	metrics := make(map[string]interface{})

	// Storage metrics
	if storageMetrics, err := h.storage.GetMetrics(c.Request.Context()); err != nil {
		metrics["storage"] = map[string]interface{}{
			"error": err.Error(),
		}
	} else {
		metrics["storage"] = storageMetrics
	}

	// Evaluator metrics
	if evaluatorMetrics, err := h.evaluator.GetMetrics(c.Request.Context()); err != nil {
		metrics["evaluator"] = map[string]interface{}{
			"error": err.Error(),
		}
	} else {
		metrics["evaluator"] = evaluatorMetrics
	}

	// Automation metrics
	if automationMetrics, err := h.automation.GetMetrics(c.Request.Context()); err != nil {
		metrics["automation"] = map[string]interface{}{
			"error": err.Error(),
		}
	} else {
		metrics["automation"] = automationMetrics
	}

	// System metrics
	metrics["system"] = map[string]interface{}{
		"service": "policy-engine",
		"version": "1.0.0",
		"uptime":  time.Since(startTime).String(),
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("metrics retrieved")

	c.JSON(http.StatusOK, gin.H{
		"metrics":  metrics,
		"duration": duration.String(),
	})
}

// Info handles GET /info
func (h *HealthHandler) Info(c *gin.Context) {
	startTime := time.Now()

	info := map[string]interface{}{
		"service":      "policy-engine",
		"version":      "1.0.0",
		"description":  "Kubernetes Policy Engine for Cost Optimization",
		"build_time":   "2024-01-01T00:00:00Z",
		"git_commit":   "unknown",
		"git_branch":   "main",
		"go_version":   "1.21.0",
		"architecture": "linux/amd64",
		"capabilities": []string{
			"policy-evaluation",
			"policy-enforcement",
			"automation-rules",
			"cost-optimization",
			"workload-priority",
		},
		"endpoints": map[string]string{
			"health":      "/health",
			"readiness":   "/ready",
			"liveness":    "/live",
			"status":      "/status",
			"metrics":     "/metrics",
			"info":        "/info",
			"policies":    "/api/v1/policies",
			"workloads":   "/api/v1/workloads",
			"evaluations": "/api/v1/evaluations",
			"automation":  "/api/v1/automation",
		},
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("service info retrieved")

	c.JSON(http.StatusOK, gin.H{
		"info":     info,
		"duration": duration.String(),
	})
}
