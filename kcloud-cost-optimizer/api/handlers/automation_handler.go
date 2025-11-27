package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/internal/automation"
	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// AutomationHandler handles automation-related HTTP requests
type AutomationHandler struct {
	storage    storage.StorageManager
	automation automation.AutomationEngine
	logger     types.Logger
}

// NewAutomationHandler creates a new automation handler
func NewAutomationHandler(storage storage.StorageManager, automation automation.AutomationEngine, logger types.Logger) *AutomationHandler {
	return &AutomationHandler{
		storage:    storage,
		automation: automation,
		logger:     logger,
	}
}

// CreateAutomationRule handles POST /automation/rules
func (h *AutomationHandler) CreateAutomationRule(c *gin.Context) {
	startTime := time.Now()

	var rule automation.AutomationRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		h.logger.WithError(err).Error("failed to bind automation rule JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_rule_format",
			"message": "Failed to parse automation rule JSON",
			"details": err.Error(),
		})
		return
	}

	// Validate rule
	if err := rule.Validate(); err != nil {
		h.logger.WithError(err).Error("automation rule validation failed")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "rule_validation_failed",
			"message": "Automation rule validation failed",
			"details": err.Error(),
		})
		return
	}

	// Create rule
	if err := h.automation.CreateRule(c.Request.Context(), &rule); err != nil {
		h.logger.WithError(err).Error("failed to create automation rule")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rule_creation_failed",
			"message": "Failed to create automation rule",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule created successfully", "rule_id", rule.ID)

	c.JSON(http.StatusCreated, gin.H{
		"message":  "Automation rule created successfully",
		"rule":     rule,
		"duration": duration.String(),
	})
}

// GetAutomationRule handles GET /automation/rules/:id
func (h *AutomationHandler) GetAutomationRule(c *gin.Context) {
	startTime := time.Now()
	ruleID := c.Param("id")

	rule, err := h.automation.GetRule(c.Request.Context(), ruleID)
	if err != nil {
		h.logger.WithError(err).Error("failed to get automation rule")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "rule_not_found",
			"message": "Automation rule not found",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule retrieved successfully", "rule_id", ruleID)

	c.JSON(http.StatusOK, gin.H{
		"rule":     rule,
		"duration": duration.String(),
	})
}

// UpdateAutomationRule handles PUT /automation/rules/:id
func (h *AutomationHandler) UpdateAutomationRule(c *gin.Context) {
	startTime := time.Now()
	ruleID := c.Param("id")

	var rule automation.AutomationRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		h.logger.WithError(err).Error("failed to bind automation rule JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_rule_format",
			"message": "Failed to parse automation rule JSON",
			"details": err.Error(),
		})
		return
	}

	// Ensure ID matches
	rule.ID = ruleID

	// Validate rule
	if err := rule.Validate(); err != nil {
		h.logger.WithError(err).Error("automation rule validation failed")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "rule_validation_failed",
			"message": "Automation rule validation failed",
			"details": err.Error(),
		})
		return
	}

	// Update rule
	if err := h.automation.UpdateRule(c.Request.Context(), &rule); err != nil {
		h.logger.WithError(err).Error("failed to update automation rule")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rule_update_failed",
			"message": "Failed to update automation rule",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule updated successfully", "rule_id", ruleID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Automation rule updated successfully",
		"rule":     rule,
		"duration": duration.String(),
	})
}

// DeleteAutomationRule handles DELETE /automation/rules/:id
func (h *AutomationHandler) DeleteAutomationRule(c *gin.Context) {
	startTime := time.Now()
	ruleID := c.Param("id")

	if err := h.automation.DeleteRule(c.Request.Context(), ruleID); err != nil {
		h.logger.WithError(err).Error("failed to delete automation rule")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rule_deletion_failed",
			"message": "Failed to delete automation rule",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule deleted successfully", "rule_id", ruleID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Automation rule deleted successfully",
		"duration": duration.String(),
	})
}

// ListAutomationRules handles GET /automation/rules
func (h *AutomationHandler) ListAutomationRules(c *gin.Context) {
	startTime := time.Now()

	// Parse query parameters
	filters := &automation.RuleFilters{}

	// Status filter
	if status := c.Query("status"); status != "" {
		filters.Status = &status
	}

	// Type filter
	if ruleType := c.Query("type"); ruleType != "" {
		filters.Type = &ruleType
	}

	// Priority filter
	if priority := c.Query("priority"); priority != "" {
		if p, err := strconv.Atoi(priority); err == nil {
			filters.Priority = &p
		}
	}

	// Enabled filter
	if enabled := c.Query("enabled"); enabled != "" {
		if e, err := strconv.ParseBool(enabled); err == nil {
			filters.Enabled = &e
		}
	}

	// Pagination
	if limit := c.Query("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil && l > 0 {
			filters.Limit = l
		}
	}
	if offset := c.Query("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil && o >= 0 {
			filters.Offset = o
		}
	}

	// Get rules
	rules, err := h.automation.ListRules(c.Request.Context())
	if err != nil {
		h.logger.WithError(err).Error("failed to list automation rules")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rule_list_failed",
			"message": "Failed to list automation rules",
			"details": err.Error(),
		})
		return
	}

	// Get total count
	total, err := h.automation.CountRules(c.Request.Context())
	if err != nil {
		h.logger.WithError(err).Error("failed to count automation rules")
		// Continue without total count
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rules listed successfully", "count", len(rules))

	c.JSON(http.StatusOK, gin.H{
		"rules":    rules,
		"total":    total,
		"count":    len(rules),
		"duration": duration.String(),
	})
}

// EnableAutomationRule handles POST /automation/rules/:id/enable
func (h *AutomationHandler) EnableAutomationRule(c *gin.Context) {
	startTime := time.Now()
	ruleID := c.Param("id")

	if err := h.automation.EnableRule(c.Request.Context(), ruleID); err != nil {
		h.logger.WithError(err).Error("failed to enable automation rule")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rule_enable_failed",
			"message": "Failed to enable automation rule",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule enabled successfully", "rule_id", ruleID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Automation rule enabled successfully",
		"duration": duration.String(),
	})
}

// DisableAutomationRule handles POST /automation/rules/:id/disable
func (h *AutomationHandler) DisableAutomationRule(c *gin.Context) {
	startTime := time.Now()
	ruleID := c.Param("id")

	if err := h.automation.DisableRule(c.Request.Context(), ruleID); err != nil {
		h.logger.WithError(err).Error("failed to disable automation rule")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rule_disable_failed",
			"message": "Failed to disable automation rule",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule disabled successfully", "rule_id", ruleID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Automation rule disabled successfully",
		"duration": duration.String(),
	})
}

// ExecuteAutomationRule handles POST /automation/rules/:id/execute
func (h *AutomationHandler) ExecuteAutomationRule(c *gin.Context) {
	startTime := time.Now()
	ruleID := c.Param("id")

	// Execute rule
	err := h.automation.ExecuteRule(c.Request.Context(), ruleID, map[string]interface{}{})
	if err != nil {
		h.logger.WithError(err).Error("failed to execute automation rule")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rule_execution_failed",
			"message": "Failed to execute automation rule",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule executed successfully", "rule_id", ruleID)

	c.JSON(http.StatusOK, gin.H{
		"result":   "executed",
		"duration": duration.String(),
	})
}

// GetAutomationRuleHistory handles GET /automation/rules/:id/history
func (h *AutomationHandler) GetAutomationRuleHistory(c *gin.Context) {
	startTime := time.Now()
	ruleID := c.Param("id")

	// Parse limit
	limit := 10 // Default limit
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Get history
	filters := &automation.RuleFilters{
		Limit: limit,
	}
	history, err := h.automation.GetRuleHistory(c.Request.Context(), ruleID, filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to get automation rule history")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "history_retrieval_failed",
			"message": "Failed to get automation rule history",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation rule history retrieved successfully", "rule_id", ruleID)

	c.JSON(http.StatusOK, gin.H{
		"history":  history,
		"count":    len(history),
		"duration": duration.String(),
	})
}

// GetAutomationStatistics handles GET /automation/statistics
func (h *AutomationHandler) GetAutomationStatistics(c *gin.Context) {
	requestStartTime := time.Now()

	// Parse time range
	startTimeStr := c.Query("start_time")
	endTimeStr := c.Query("end_time")

	var startTime, endTime time.Time
	var err error

	if startTimeStr != "" {
		startTime, err = time.Parse(time.RFC3339, startTimeStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_start_time",
				"message": "Invalid start_time format, use RFC3339",
				"details": err.Error(),
			})
			return
		}
	} else {
		startTime = time.Now().Add(-24 * time.Hour) // Default to last 24 hours
	}

	if endTimeStr != "" {
		endTime, err = time.Parse(time.RFC3339, endTimeStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_end_time",
				"message": "Invalid end_time format, use RFC3339",
				"details": err.Error(),
			})
			return
		}
	} else {
		endTime = time.Now() // Default to now
	}

	// Get statistics
	filters := &automation.RuleFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
	}
	statistics, err := h.automation.GetStatistics(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to get automation statistics")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "statistics_retrieval_failed",
			"message": "Failed to get automation statistics",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(requestStartTime)
	h.logger.WithDuration(duration).Info("automation statistics retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"statistics": statistics,
		"duration":   duration.String(),
	})
}

// GetAutomationHealth handles GET /automation/health
func (h *AutomationHandler) GetAutomationHealth(c *gin.Context) {
	startTime := time.Now()

	// Get automation engine health
	err := h.automation.Health(c.Request.Context())
	if err != nil {
		h.logger.WithError(err).Error("failed to get automation engine health")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "health_check_failed",
			"message": "Failed to get automation engine health",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("automation health check completed")

	c.JSON(http.StatusOK, gin.H{
		"health":   "healthy",
		"duration": duration.String(),
	})
}
