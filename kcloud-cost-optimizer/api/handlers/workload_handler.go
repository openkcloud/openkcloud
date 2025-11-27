package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// WorkloadHandler handles workload-related HTTP requests
type WorkloadHandler struct {
	storage storage.StorageManager
	logger  types.Logger
}

// NewWorkloadHandler creates a new workload handler
func NewWorkloadHandler(storage storage.StorageManager, logger types.Logger) *WorkloadHandler {
	return &WorkloadHandler{
		storage: storage,
		logger:  logger,
	}
}

// CreateWorkload handles POST /workloads
func (h *WorkloadHandler) CreateWorkload(c *gin.Context) {
	startTime := time.Now()

	var workload types.Workload
	if err := c.ShouldBindJSON(&workload); err != nil {
		h.logger.WithError(err).Error("failed to bind workload JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_workload_format",
			"message": "Failed to parse workload JSON",
			"details": err.Error(),
		})
		return
	}

	// Set timestamps
	now := time.Now()
	workload.CreatedAt = now
	workload.UpdatedAt = now

	// Create workload
	if err := h.storage.Workload().Create(c.Request.Context(), &workload); err != nil {
		h.logger.WithError(err).Error("failed to create workload")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "workload_creation_failed",
			"message": "Failed to create workload",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithWorkload(workload.ID, string(workload.Type)).WithDuration(duration).Info("workload created successfully")

	c.JSON(http.StatusCreated, gin.H{
		"message":  "Workload created successfully",
		"workload": workload,
		"duration": duration.String(),
	})
}

// GetWorkload handles GET /workloads/:id
func (h *WorkloadHandler) GetWorkload(c *gin.Context) {
	startTime := time.Now()
	workloadID := c.Param("id")

	workload, err := h.storage.Workload().Get(c.Request.Context(), workloadID)
	if err != nil {
		h.logger.WithError(err).WithWorkload(workloadID, "").Error("failed to get workload")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "workload_not_found",
			"message": "Workload not found",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithWorkload(workloadID, "").WithDuration(duration).Info("workload retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"workload": workload,
		"duration": duration.String(),
	})
}

// UpdateWorkload handles PUT /workloads/:id
func (h *WorkloadHandler) UpdateWorkload(c *gin.Context) {
	startTime := time.Now()
	workloadID := c.Param("id")

	var workload types.Workload
	if err := c.ShouldBindJSON(&workload); err != nil {
		h.logger.WithError(err).Error("failed to bind workload JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_workload_format",
			"message": "Failed to parse workload JSON",
			"details": err.Error(),
		})
		return
	}

	// Ensure ID matches
	workload.ID = workloadID
	workload.UpdatedAt = time.Now()

	// Update workload
	if err := h.storage.Workload().Update(c.Request.Context(), &workload); err != nil {
		h.logger.WithError(err).WithWorkload(workloadID, "").Error("failed to update workload")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "workload_update_failed",
			"message": "Failed to update workload",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithWorkload(workloadID, "").WithDuration(duration).Info("workload updated successfully")

	c.JSON(http.StatusOK, gin.H{
		"message":  "Workload updated successfully",
		"workload": workload,
		"duration": duration.String(),
	})
}

// DeleteWorkload handles DELETE /workloads/:id
func (h *WorkloadHandler) DeleteWorkload(c *gin.Context) {
	startTime := time.Now()
	workloadID := c.Param("id")

	if err := h.storage.Workload().Delete(c.Request.Context(), workloadID); err != nil {
		h.logger.WithError(err).WithWorkload(workloadID, "").Error("failed to delete workload")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "workload_deletion_failed",
			"message": "Failed to delete workload",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithWorkload(workloadID, "").WithDuration(duration).Info("workload deleted successfully")

	c.JSON(http.StatusOK, gin.H{
		"message":  "Workload deleted successfully",
		"duration": duration.String(),
	})
}

// ListWorkloads handles GET /workloads
func (h *WorkloadHandler) ListWorkloads(c *gin.Context) {
	startTime := time.Now()

	// Parse query parameters
	filters := &storage.WorkloadFilters{}

	// Type filter
	if workloadType := c.Query("type"); workloadType != "" {
		wt := types.WorkloadType(workloadType)
		filters.Type = &wt
	}

	// Status filter
	if status := c.Query("status"); status != "" {
		ws := types.WorkloadStatus(status)
		filters.Status = &ws
	}

	// Cluster ID filter
	if clusterID := c.Query("cluster_id"); clusterID != "" {
		filters.ClusterID = &clusterID
	}

	// Node ID filter
	if nodeID := c.Query("node_id"); nodeID != "" {
		filters.NodeID = &nodeID
	}

	// Namespace filter
	if namespace := c.Query("namespace"); namespace != "" {
		filters.Namespace = &namespace
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

	// Get workloads
	workloads, err := h.storage.Workload().List(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to list workloads")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "workload_list_failed",
			"message": "Failed to list workloads",
			"details": err.Error(),
		})
		return
	}

	// Get total count
	total, err := h.storage.Workload().Count(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to count workloads")
		// Continue without total count
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("workloads listed successfully", "count", len(workloads))

	c.JSON(http.StatusOK, gin.H{
		"workloads": workloads,
		"total":     total,
		"count":     len(workloads),
		"duration":  duration.String(),
	})
}

// GetWorkloadMetrics handles GET /workloads/:id/metrics
func (h *WorkloadHandler) GetWorkloadMetrics(c *gin.Context) {
	requestStartTime := time.Now()
	workloadID := c.Param("id")

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

	// Get metrics
	metrics, err := h.storage.Workload().GetMetrics(c.Request.Context(), workloadID, startTime, endTime)
	if err != nil {
		h.logger.WithError(err).WithWorkload(workloadID, "").Error("failed to get workload metrics")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "metrics_retrieval_failed",
			"message": "Failed to get workload metrics",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(requestStartTime)
	h.logger.WithWorkload(workloadID, "").WithDuration(duration).Info("workload metrics retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"metrics":  metrics,
		"count":    len(metrics),
		"duration": duration.String(),
	})
}

// GetWorkloadHistory handles GET /workloads/:id/history
func (h *WorkloadHandler) GetWorkloadHistory(c *gin.Context) {
	startTime := time.Now()
	workloadID := c.Param("id")

	// Parse limit
	limit := 10 // Default limit
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Get history
	history, err := h.storage.Workload().GetHistory(c.Request.Context(), workloadID, limit)
	if err != nil {
		h.logger.WithError(err).WithWorkload(workloadID, "").Error("failed to get workload history")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "history_retrieval_failed",
			"message": "Failed to get workload history",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithWorkload(workloadID, "").WithDuration(duration).Info("workload history retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"history":  history,
		"count":    len(history),
		"duration": duration.String(),
	})
}

// SearchWorkloads handles GET /workloads/search
func (h *WorkloadHandler) SearchWorkloads(c *gin.Context) {
	startTime := time.Now()

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "missing_query",
			"message": "Query parameter 'q' is required",
		})
		return
	}

	// Build search query
	searchQuery := &storage.WorkloadSearchQuery{
		Query:  query,
		Fields: []string{"id", "name", "type", "status"},
	}

	// Add filters if provided
	filters := &storage.WorkloadFilters{}
	if workloadType := c.Query("type"); workloadType != "" {
		wt := types.WorkloadType(workloadType)
		filters.Type = &wt
	}
	if status := c.Query("status"); status != "" {
		ws := types.WorkloadStatus(status)
		filters.Status = &ws
	}
	searchQuery.Filters = filters

	// Pagination
	if limit := c.Query("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil && l > 0 {
			searchQuery.Limit = l
		}
	}
	if offset := c.Query("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil && o >= 0 {
			searchQuery.Offset = o
		}
	}

	// Search workloads
	workloads, err := h.storage.Workload().Search(c.Request.Context(), searchQuery)
	if err != nil {
		h.logger.WithError(err).Error("failed to search workloads")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "workload_search_failed",
			"message": "Failed to search workloads",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("workloads searched successfully", "query", query, "count", len(workloads))

	c.JSON(http.StatusOK, gin.H{
		"workloads": workloads,
		"count":     len(workloads),
		"query":     query,
		"duration":  duration.String(),
	})
}
