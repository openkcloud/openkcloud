package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// PolicyHandler handles policy-related HTTP requests
type PolicyHandler struct {
	storage storage.StorageManager
	logger  types.Logger
}

// NewPolicyHandler creates a new policy handler
func NewPolicyHandler(storage storage.StorageManager, logger types.Logger) *PolicyHandler {
	return &PolicyHandler{
		storage: storage,
		logger:  logger,
	}
}

// CreatePolicy handles POST /policies
func (h *PolicyHandler) CreatePolicy(c *gin.Context) {
	startTime := time.Now()

	var policy types.Policy
	if err := c.ShouldBindJSON(&policy); err != nil {
		h.logger.WithError(err).Error("failed to bind policy JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_policy_format",
			"message": "Failed to parse policy JSON",
			"details": err.Error(),
		})
		return
	}

	// Validate policy
	if err := policy.Validate(); err != nil {
		h.logger.WithError(err).Error("policy validation failed")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "policy_validation_failed",
			"message": "Policy validation failed",
			"details": err.Error(),
		})
		return
	}

	// Create policy
	if err := h.storage.Policy().Create(c.Request.Context(), policy); err != nil {
		h.logger.WithError(err).Error("failed to create policy")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "policy_creation_failed",
			"message": "Failed to create policy",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithPolicy(policy.GetMetadata().Name, "").WithDuration(duration).Info("policy created successfully")

	c.JSON(http.StatusCreated, gin.H{
		"message":  "Policy created successfully",
		"policy":   policy,
		"duration": duration.String(),
	})
}

// GetPolicy handles GET /policies/:id
func (h *PolicyHandler) GetPolicy(c *gin.Context) {
	startTime := time.Now()
	policyID := c.Param("id")

	policy, err := h.storage.Policy().Get(c.Request.Context(), policyID)
	if err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to get policy")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "policy_not_found",
			"message": "Policy not found",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithPolicy(policyID, "").WithDuration(duration).Info("policy retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"policy":   policy,
		"duration": duration.String(),
	})
}

// UpdatePolicy handles PUT /policies/:id
func (h *PolicyHandler) UpdatePolicy(c *gin.Context) {
	startTime := time.Now()
	policyID := c.Param("id")

	var policy types.Policy
	if err := c.ShouldBindJSON(&policy); err != nil {
		h.logger.WithError(err).Error("failed to bind policy JSON")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_policy_format",
			"message": "Failed to parse policy JSON",
			"details": err.Error(),
		})
		return
	}

	// Validate policy
	if err := policy.Validate(); err != nil {
		h.logger.WithError(err).Error("policy validation failed")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "policy_validation_failed",
			"message": "Policy validation failed",
			"details": err.Error(),
		})
		return
	}

	// Update policy
	if err := h.storage.Policy().Update(c.Request.Context(), policy); err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to update policy")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "policy_update_failed",
			"message": "Failed to update policy",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithPolicy(policyID, "").WithDuration(duration).Info("policy updated successfully")

	c.JSON(http.StatusOK, gin.H{
		"message":  "Policy updated successfully",
		"policy":   policy,
		"duration": duration.String(),
	})
}

// DeletePolicy handles DELETE /policies/:id
func (h *PolicyHandler) DeletePolicy(c *gin.Context) {
	startTime := time.Now()
	policyID := c.Param("id")

	if err := h.storage.Policy().Delete(c.Request.Context(), policyID); err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to delete policy")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "policy_deletion_failed",
			"message": "Failed to delete policy",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithPolicy(policyID, "").WithDuration(duration).Info("policy deleted successfully")

	c.JSON(http.StatusOK, gin.H{
		"message":  "Policy deleted successfully",
		"duration": duration.String(),
	})
}

// ListPolicies handles GET /policies
func (h *PolicyHandler) ListPolicies(c *gin.Context) {
	startTime := time.Now()

	// Parse query parameters
	filters := &storage.PolicyFilters{}

	// Type filter
	if policyType := c.Query("type"); policyType != "" {
		pt := types.PolicyType(policyType)
		filters.Type = &pt
	}

	// Status filter
	if status := c.Query("status"); status != "" {
		ps := types.PolicyStatus(status)
		filters.Status = &ps
	}

	// Priority filter
	if priority := c.Query("priority"); priority != "" {
		if p, err := strconv.Atoi(priority); err == nil {
			pp := types.Priority(p)
			filters.Priority = &pp
		}
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

	// Get policies
	policies, err := h.storage.Policy().List(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to list policies")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "policy_list_failed",
			"message": "Failed to list policies",
			"details": err.Error(),
		})
		return
	}

	// Get total count
	total, err := h.storage.Policy().Count(c.Request.Context(), filters)
	if err != nil {
		h.logger.WithError(err).Error("failed to count policies")
		// Continue without total count
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("policies listed successfully", "count", len(policies))

	c.JSON(http.StatusOK, gin.H{
		"policies": policies,
		"total":    total,
		"count":    len(policies),
		"duration": duration.String(),
	})
}

// EnablePolicy handles POST /policies/:id/enable
func (h *PolicyHandler) EnablePolicy(c *gin.Context) {
	startTime := time.Now()
	policyID := c.Param("id")

	policy, err := h.storage.Policy().Get(c.Request.Context(), policyID)
	if err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to get policy")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "policy_not_found",
			"message": "Policy not found",
			"details": err.Error(),
		})
		return
	}

	policy.SetStatus(types.PolicyStatusActive)

	if err := h.storage.Policy().Update(c.Request.Context(), policy); err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to enable policy")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "policy_enable_failed",
			"message": "Failed to enable policy",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithPolicy(policyID, "").WithDuration(duration).Info("policy enabled successfully")

	c.JSON(http.StatusOK, gin.H{
		"message":  "Policy enabled successfully",
		"policy":   policy,
		"duration": duration.String(),
	})
}

// DisablePolicy handles POST /policies/:id/disable
func (h *PolicyHandler) DisablePolicy(c *gin.Context) {
	startTime := time.Now()
	policyID := c.Param("id")

	policy, err := h.storage.Policy().Get(c.Request.Context(), policyID)
	if err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to get policy")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "policy_not_found",
			"message": "Policy not found",
			"details": err.Error(),
		})
		return
	}

	policy.SetStatus(types.PolicyStatusInactive)

	if err := h.storage.Policy().Update(c.Request.Context(), policy); err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to disable policy")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "policy_disable_failed",
			"message": "Failed to disable policy",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithPolicy(policyID, "").WithDuration(duration).Info("policy disabled successfully")

	c.JSON(http.StatusOK, gin.H{
		"message":  "Policy disabled successfully",
		"policy":   policy,
		"duration": duration.String(),
	})
}

// GetPolicyVersions handles GET /policies/:id/versions
func (h *PolicyHandler) GetPolicyVersions(c *gin.Context) {
	startTime := time.Now()
	policyID := c.Param("id")

	versions, err := h.storage.Policy().GetVersions(c.Request.Context(), policyID)
	if err != nil {
		h.logger.WithError(err).WithPolicy(policyID, "").Error("failed to get policy versions")
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "policy_versions_not_found",
			"message": "Failed to get policy versions",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithPolicy(policyID, "").WithDuration(duration).Info("policy versions retrieved successfully")

	c.JSON(http.StatusOK, gin.H{
		"versions": versions,
		"count":    len(versions),
		"duration": duration.String(),
	})
}

// SearchPolicies handles GET /policies/search
func (h *PolicyHandler) SearchPolicies(c *gin.Context) {
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
	searchQuery := &storage.PolicySearchQuery{
		Query:  query,
		Fields: []string{"name", "type", "status"},
	}

	// Add filters if provided
	filters := &storage.PolicyFilters{}
	if policyType := c.Query("type"); policyType != "" {
		pt := types.PolicyType(policyType)
		filters.Type = &pt
	}
	if status := c.Query("status"); status != "" {
		ps := types.PolicyStatus(status)
		filters.Status = &ps
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

	// Search policies
	policies, err := h.storage.Policy().Search(c.Request.Context(), searchQuery)
	if err != nil {
		h.logger.WithError(err).Error("failed to search policies")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "policy_search_failed",
			"message": "Failed to search policies",
			"details": err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	h.logger.WithDuration(duration).Info("policies searched successfully", "query", query, "count", len(policies))

	c.JSON(http.StatusOK, gin.H{
		"policies": policies,
		"count":    len(policies),
		"query":    query,
		"duration": duration.String(),
	})
}
