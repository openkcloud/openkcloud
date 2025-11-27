package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/internal/evaluator"
	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// EvaluationHandler handles evaluation-related HTTP requests
type EvaluationHandler struct {
	storage   storage.StorageManager
	evaluator evaluator.EvaluationEngine
	logger    types.Logger
}

// NewEvaluationHandler creates a new evaluation handler
func NewEvaluationHandler(storage storage.StorageManager, evaluator evaluator.EvaluationEngine, logger types.Logger) *EvaluationHandler {
	return &EvaluationHandler{
		storage:   storage,
		evaluator: evaluator,
		logger:    logger,
	}
}

// EvaluateWorkload handles POST /evaluations
func (h *EvaluationHandler) EvaluateWorkload(c *gin.Context) {
	startTime := time.Now()

	var request struct {
		WorkloadID string   `json:"workload_id" binding:"required"`
		PolicyIDs  []string `json:"policy_ids,omitempty"`
		Force      bool     `json:"force,omitempty"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		h.logger.WithError(err).Error("failed to bind evaluation request JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request_format",
			"message": "Failed to parse evaluation request JSON",
			"details": err.Error(),
		})
		return
	}

	// Get workload
	workload, err := h.storage.Workload().Get(c.Request.Context(), request.WorkloadID)
	if err != nil {
		h.logger.WithError(err).WithWorkload(request.WorkloadID, "").Error("failed to get workload for evaluation")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "workload_not_found",
			"message": "Workload not found",
			"details": err.Error(),
		})
		return
	}

	// Evaluate workload
	options := &evaluator.EvaluationOptions{
		PolicyIDs: request.PolicyIDs,
		Force:     request.Force,
	}
	evaluationResult, err := h.evaluator.EvaluateWorkload(c.Request.Context(), workload, options)
	if err != nil {
		h.logger.WithError(err).WithWorkload(request.WorkloadID, "").Error("failed to evaluate workload")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "evaluation_failed",
			"message": "Failed to evaluate workload",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithWorkload(request.WorkloadID, "").WithDuration(duration).Info("workload evaluation completed successfully")

	c.JSON(http.StatusOK, gin.H{
		"evaluation": evaluationResult,
		"duration":   duration.String(),
	})
}

// GetEvaluation handles GET /evaluations/:id
func (h *EvaluationHandler) GetEvaluation(c *gin.Context) {
	startTime := time.Now()
	evaluationID := c.Param("id")

	evaluation, err := h.storage.Evaluation().Get(c.Request.Context(), evaluationID)
	if err != nil {
		h.logger.WithError(err).WithEvaluation(evaluationID).Error("failed to get evaluation")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "evaluation_not_found",
			"message": "Evaluation not found",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithEvaluation(evaluationID).WithDuration(duration).Info("evaluation retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"evaluation": evaluation,
		"duration":   duration.String(),
	})
}

// ListEvaluations handles GET /evaluations
func (h *EvaluationHandler) ListEvaluations(c *gin.Context) {
	startTime := time.Now()

	// Parse query parameters
	filters := &storage.EvaluationFilters{}

	// Workload ID filter
	if workloadID := c.Query("workload_id"); workloadID != "" {
		filters.WorkloadID = &workloadID
	}

	// Policy ID filter
	if policyID := c.Query("policy_id"); policyID != "" {
		filters.PolicyID = &policyID
	}

	// Status filter
	if status := c.Query("status"); status != "" {
		filters.Status = &status
	}

	// Result filter
	if result := c.Query("result"); result != "" {
		filters.Result = &result
	}

	// Time range filters
	if startTimeStr := c.Query("start_time"); startTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			filters.StartTime = &t
		}
	}
	if endTimeStr := c.Query("end_time"); endTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			filters.EndTime = &t
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

	// Get evaluations
	evaluations, err := h.storage.Evaluation().List(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to list evaluations")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "evaluation_list_failed",
			"message": "Failed to list evaluations",
			"details": err.Error(),
		})
		return
	}

	// Get total count
	total, err := h.storage.Evaluation().Count(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to count evaluations")
		// Continue without total count
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("evaluations listed successfully", "count", len(evaluations))

	c.JSON(http.StatusOK, gin.H{
		"evaluations": evaluations,
		"total":       total,
		"count":       len(evaluations),
		"duration":    duration.String(),
	})
}

// GetEvaluationHistory handles GET /evaluations/history
func (h *EvaluationHandler) GetEvaluationHistory(c *gin.Context) {
	requestStartTime := time.Now()

	// Parse query parameters
	workloadID := c.Query("workload_id")
	policyID := c.Query("policy_id")

	if workloadID == "" && policyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "missing_identifier",
			"message": "Either workload_id or policy_id is required",
		})
		return
	}

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
		startTime = time.Now().Add(-7 * 24 * time.Hour) // Default to last 7 days
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

	// Parse limit
	limit := 100 // Default limit
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Get history
	var history []*types.Evaluation
	filters := &storage.EvaluationFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
		Limit:     limit,
	}

	if workloadID != "" {
		history, err = h.storage.Evaluation().GetWorkloadHistory(c.Request.Context(), workloadID, filters)
	} else {
		history, err = h.storage.Evaluation().GetPolicyHistory(c.Request.Context(), policyID, filters)
	}

	if err != nil {
		h.logger.WithError(err).Error("failed to get evaluation history")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "history_retrieval_failed",
			"message": "Failed to get evaluation history",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(requestStartTime)
	h.logger.WithDuration(duration).Info("evaluation history retrieved successfully", "count", len(history))

	c.JSON(http.StatusOK, gin.H{
		"history":  history,
		"count":    len(history),
		"duration": duration.String(),
	})
}

// GetEvaluationStatistics handles GET /evaluations/statistics
func (h *EvaluationHandler) GetEvaluationStatistics(c *gin.Context) {
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
	filters := &storage.EvaluationFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
	}
	statistics, err := h.storage.Evaluation().GetStatistics(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to get evaluation statistics")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "statistics_retrieval_failed",
			"message": "Failed to get evaluation statistics",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(requestStartTime)
	h.logger.WithDuration(duration).Info("evaluation statistics retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"statistics": statistics,
		"duration":   duration.String(),
	})
}

// BulkEvaluateWorkloads handles POST /evaluations/bulk
func (h *EvaluationHandler) BulkEvaluateWorkloads(c *gin.Context) {
	startTime := time.Now()

	var request struct {
		WorkloadIDs []string `json:"workload_ids" binding:"required"`
		PolicyIDs   []string `json:"policy_ids,omitempty"`
		Force       bool     `json:"force,omitempty"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		h.logger.WithError(err).Error("failed to bind bulk evaluation request JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request_format",
			"message": "Failed to parse bulk evaluation request JSON",
			"details": err.Error(),
		})
		return
	}

	// Validate workload count
	if len(request.WorkloadIDs) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "too_many_workloads",
			"message": "Maximum 100 workloads allowed in bulk evaluation",
		})
		return
	}

	// Get workloads
	workloads := make([]*types.Workload, 0, len(request.WorkloadIDs))
	for _, workloadID := range request.WorkloadIDs {
		workload, err := h.storage.Workload().Get(c.Request.Context(), workloadID)
		if err != nil {
			h.logger.WithError(err).WithWorkload(workloadID, "").Error("failed to get workload for bulk evaluation")
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "workload_not_found",
				"message": "Workload not found",
				"details": err.Error(),
			})
			return
		}
		workloads = append(workloads, workload)
	}

	// Bulk evaluate workloads
	results := make([]types.Evaluation, 0, len(workloads))
	for _, workload := range workloads {
		options := &evaluator.EvaluationOptions{
			PolicyIDs: request.PolicyIDs,
			Force:     request.Force,
		}
		evaluationResult, err := h.evaluator.EvaluateWorkload(c.Request.Context(), workload, options)
		if err != nil {
			h.logger.WithError(err).WithWorkload(workload.ID, "").Error("failed to evaluate workload in bulk")
			// Continue with other workloads
			continue
		}
		// Convert evaluation results to evaluations
		for _, result := range evaluationResult {
			evaluation := &types.Evaluation{
				ID:         result.ID,
				PolicyID:   result.PolicyID,
				WorkloadID: result.WorkloadID,
				Status:     types.EvaluationStatusCompleted,
				Result:     result,
				StartTime:  time.Now(),
				EndTime:    &time.Time{},
				Duration:   result.Duration,
			}
			results = append(results, *evaluation)
		}
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("bulk workload evaluation completed", "requested", len(request.WorkloadIDs), "completed", len(results))

	c.JSON(http.StatusOK, gin.H{
		"results":   results,
		"requested": len(request.WorkloadIDs),
		"completed": len(results),
		"duration":  duration.String(),
	})
}

// GetEvaluationHealth handles GET /evaluations/health
func (h *EvaluationHandler) GetEvaluationHealth(c *gin.Context) {
	startTime := time.Now()

	// Get evaluator health
	err := h.evaluator.Health(c.Request.Context())
	if err != nil {
		h.logger.WithError(err).Error("failed to get evaluator health")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "health_check_failed",
			"message": "Failed to get evaluator health",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("evaluation health check completed")

	c.JSON(http.StatusOK, gin.H{
		"health":   "healthy",
		"duration": duration.String(),
	})
}
