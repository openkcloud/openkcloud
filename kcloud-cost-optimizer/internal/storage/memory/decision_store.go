package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// memoryDecisionStore implements DecisionStore interface using in-memory storage
type memoryDecisionStore struct {
	decisions map[string]*types.Decision
	history   map[string][]*types.DecisionHistory // decisionID -> history
	mu        sync.RWMutex
}

// NewMemoryDecisionStore creates a new memory-based decision store
func NewMemoryDecisionStore() storage.DecisionStore {
	return &memoryDecisionStore{
		decisions: make(map[string]*types.Decision),
		history:   make(map[string][]*types.DecisionHistory),
	}
}

// Create creates a new decision
func (s *memoryDecisionStore) Create(ctx context.Context, decision *types.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not provided
	decisionID := s.generateDecisionID(decision)

	// Check if decision with same ID already exists
	if _, exists := s.decisions[decisionID]; exists {
		return types.NewDecisionError(decisionID, string(decision.Type), decision.WorkloadID, decision.PolicyID, "create", types.ErrDecisionAlreadyExists)
	}

	// Set timestamps
	decision.CreatedAt = time.Now()
	decision.UpdatedAt = time.Now()

	// Validate decision
	if err := s.validateDecision(decision); err != nil {
		return types.NewDecisionError(decisionID, string(decision.Type), decision.WorkloadID, decision.PolicyID, "create", err)
	}

	// Store decision
	s.decisions[decisionID] = decision

	// Initialize empty history
	s.history[decisionID] = []*types.DecisionHistory{}

	return nil
}

// Get retrieves a decision by ID
func (s *memoryDecisionStore) Get(ctx context.Context, id string) (*types.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	decision, exists := s.decisions[id]
	if !exists {
		return nil, types.NewDecisionError(id, "", "", "", "get", types.ErrDecisionNotFound)
	}

	// Return a copy to avoid modification
	decisionCopy := *decision
	return &decisionCopy, nil
}

// Update updates an existing decision
func (s *memoryDecisionStore) Update(ctx context.Context, decision *types.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	decisionID := s.generateDecisionID(decision)

	// Check if decision exists
	if _, exists := s.decisions[decisionID]; !exists {
		return types.NewDecisionError(decisionID, string(decision.Type), decision.WorkloadID, decision.PolicyID, "update", types.ErrDecisionNotFound)
	}

	// Update timestamp
	decision.UpdatedAt = time.Now()

	// Validate decision
	if err := s.validateDecision(decision); err != nil {
		return types.NewDecisionError(decisionID, string(decision.Type), decision.WorkloadID, decision.PolicyID, "update", err)
	}

	// Update decision
	s.decisions[decisionID] = decision

	return nil
}

// Delete deletes a decision by ID
func (s *memoryDecisionStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.decisions[id]
	if !exists {
		return types.NewDecisionError(id, "", "", "", "delete", types.ErrDecisionNotFound)
	}

	// Remove from all maps
	delete(s.decisions, id)
	delete(s.history, id)

	return nil
}

// List lists decisions with optional filters
func (s *memoryDecisionStore) List(ctx context.Context, filters *storage.DecisionFilters) ([]*types.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var decisions []*types.Decision

	for _, decision := range s.decisions {
		if s.matchesFilters(decision, filters) {
			// Return a copy to avoid modification
			decisionCopy := *decision
			decisions = append(decisions, &decisionCopy)
		}
	}

	// Sort by creation time (newest first)
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
	})

	// Apply pagination
	if filters != nil {
		if filters.Offset > 0 && filters.Offset < len(decisions) {
			decisions = decisions[filters.Offset:]
		}
		if filters.Limit > 0 && filters.Limit < len(decisions) {
			decisions = decisions[:filters.Limit]
		}
	}

	return decisions, nil
}

// GetByWorkload retrieves decisions by workload ID
func (s *memoryDecisionStore) GetByWorkload(ctx context.Context, workloadID string) ([]*types.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var decisions []*types.Decision
	for _, decision := range s.decisions {
		if decision.WorkloadID == workloadID {
			decisionCopy := *decision
			decisions = append(decisions, &decisionCopy)
		}
	}

	// Sort by creation time (newest first)
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
	})

	return decisions, nil
}

// GetByPolicy retrieves decisions by policy ID
func (s *memoryDecisionStore) GetByPolicy(ctx context.Context, policyID string) ([]*types.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var decisions []*types.Decision
	for _, decision := range s.decisions {
		if decision.PolicyID == policyID {
			decisionCopy := *decision
			decisions = append(decisions, &decisionCopy)
		}
	}

	// Sort by creation time (newest first)
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
	})

	return decisions, nil
}

// GetByStatus retrieves decisions by status
func (s *memoryDecisionStore) GetByStatus(ctx context.Context, status types.DecisionStatus) ([]*types.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var decisions []*types.Decision
	for _, decision := range s.decisions {
		if decision.Status == status {
			decisionCopy := *decision
			decisions = append(decisions, &decisionCopy)
		}
	}

	// Sort by creation time (newest first)
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
	})

	return decisions, nil
}

// GetByType retrieves decisions by type
func (s *memoryDecisionStore) GetByType(ctx context.Context, decisionType types.DecisionType) ([]*types.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var decisions []*types.Decision
	for _, decision := range s.decisions {
		if decision.Type == decisionType {
			decisionCopy := *decision
			decisions = append(decisions, &decisionCopy)
		}
	}

	// Sort by creation time (newest first)
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
	})

	return decisions, nil
}

// CreateMany creates multiple decisions
func (s *memoryDecisionStore) CreateMany(ctx context.Context, decisions []*types.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, decision := range decisions {
		decisionID := s.generateDecisionID(decision)

		// Check if decision with same ID already exists
		if _, exists := s.decisions[decisionID]; exists {
			return types.NewDecisionError(decisionID, string(decision.Type), decision.WorkloadID, decision.PolicyID, "createMany", types.ErrDecisionAlreadyExists)
		}

		// Set timestamps
		decision.CreatedAt = time.Now()
		decision.UpdatedAt = time.Now()

		// Validate decision
		if err := s.validateDecision(decision); err != nil {
			return types.NewDecisionError(decisionID, string(decision.Type), decision.WorkloadID, decision.PolicyID, "createMany", err)
		}

		// Store decision
		s.decisions[decisionID] = decision

		// Initialize empty history
		s.history[decisionID] = []*types.DecisionHistory{}
	}

	return nil
}

// UpdateMany updates multiple decisions
func (s *memoryDecisionStore) UpdateMany(ctx context.Context, decisions []*types.Decision) error {
	for _, decision := range decisions {
		if err := s.Update(ctx, decision); err != nil {
			return err
		}
	}
	return nil
}

// DeleteMany deletes multiple decisions
func (s *memoryDecisionStore) DeleteMany(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Search searches decisions with query
func (s *memoryDecisionStore) Search(ctx context.Context, query *storage.DecisionSearchQuery) ([]*types.Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var decisions []*types.Decision

	for _, decision := range s.decisions {
		if s.matchesSearchQuery(decision, query) {
			decisionCopy := *decision
			decisions = append(decisions, &decisionCopy)
		}
	}

	// Sort results
	if query.SortBy != "" {
		s.sortDecisions(decisions, query.SortBy, query.SortOrder)
	} else {
		// Default sort by creation time
		sort.Slice(decisions, func(i, j int) bool {
			return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
		})
	}

	// Apply pagination
	if query.Offset > 0 && query.Offset < len(decisions) {
		decisions = decisions[query.Offset:]
	}
	if query.Limit > 0 && query.Limit < len(decisions) {
		decisions = decisions[:query.Limit]
	}

	return decisions, nil
}

// Count counts decisions matching filters
func (s *memoryDecisionStore) Count(ctx context.Context, filters *storage.DecisionFilters) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := int64(0)
	for _, decision := range s.decisions {
		if s.matchesFilters(decision, filters) {
			count++
		}
	}

	return count, nil
}

// GetHistory retrieves decision execution history
func (s *memoryDecisionStore) GetHistory(ctx context.Context, decisionID string) ([]*types.DecisionHistory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history, exists := s.history[decisionID]
	if !exists {
		return nil, types.NewDecisionError(decisionID, "", "", "", "getHistory", types.ErrDecisionNotFound)
	}

	// Sort by start time (newest first)
	sort.Slice(history, func(i, j int) bool {
		return history[i].StartTime.After(history[j].StartTime)
	})

	// Return a copy to avoid modification
	result := make([]*types.DecisionHistory, len(history))
	copy(result, history)

	return result, nil
}

// GetAnalytics retrieves analytics for decisions
func (s *memoryDecisionStore) GetAnalytics(ctx context.Context, query *storage.AnalyticsQuery) (*storage.AnalyticsResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// This is a simplified analytics implementation
	// In a real implementation, you would aggregate data based on the query
	result := &storage.AnalyticsResult{
		Metric:     query.Metric,
		Dimensions: query.Dimensions,
		Data:       []storage.AnalyticsDataPoint{},
		Aggregates: make(map[string]interface{}),
		Metadata:   make(map[string]interface{}),
	}

	// Simple aggregation based on filters
	var totalDecisions int64
	var successfulDecisions int64
	var failedDecisions int64

	for _, decision := range s.decisions {
		if s.matchesAnalyticsFilters(decision, query.Filters) {
			totalDecisions++
			if decision.Status == types.DecisionStatusCompleted {
				successfulDecisions++
			} else if decision.Status == types.DecisionStatusFailed {
				failedDecisions++
			}
		}
	}

	result.Aggregates["total"] = totalDecisions
	result.Aggregates["successful"] = successfulDecisions
	result.Aggregates["failed"] = failedDecisions
	if totalDecisions > 0 {
		result.Aggregates["success_rate"] = float64(successfulDecisions) / float64(totalDecisions)
	}

	return result, nil
}

// Health checks the health of the store
func (s *memoryDecisionStore) Health(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Basic health check - ensure maps are accessible
	_ = len(s.decisions)
	_ = len(s.history)

	return nil
}

// Close closes the store
func (s *memoryDecisionStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear all data
	s.decisions = make(map[string]*types.Decision)
	s.history = make(map[string][]*types.DecisionHistory)

	return nil
}

// Helper methods

// generateDecisionID generates a unique ID for a decision
func (s *memoryDecisionStore) generateDecisionID(decision *types.Decision) string {
	if decision.ID != "" {
		return decision.ID
	}
	return fmt.Sprintf("decision-%s-%s-%d", decision.WorkloadID, string(decision.Type), time.Now().UnixNano())
}

// validateDecision validates a decision
func (s *memoryDecisionStore) validateDecision(decision *types.Decision) error {
	if decision.WorkloadID == "" {
		return types.ErrInvalidDecisionType
	}
	if decision.PolicyID == "" {
		return types.ErrInvalidDecisionType
	}
	if decision.Type == "" {
		return types.ErrInvalidDecisionType
	}
	if decision.Status == "" {
		return types.ErrInvalidDecisionStatus
	}
	return nil
}

// matchesFilters checks if a decision matches the given filters
func (s *memoryDecisionStore) matchesFilters(decision *types.Decision, filters *storage.DecisionFilters) bool {
	if filters == nil {
		return true
	}

	// Type filter
	if filters.Type != nil && decision.Type != *filters.Type {
		return false
	}

	// Status filter
	if filters.Status != nil && decision.Status != *filters.Status {
		return false
	}

	// Workload ID filter
	if filters.WorkloadID != nil && decision.WorkloadID != *filters.WorkloadID {
		return false
	}

	// Policy ID filter
	if filters.PolicyID != nil && decision.PolicyID != *filters.PolicyID {
		return false
	}

	// Cluster ID filter
	if filters.ClusterID != nil && decision.ClusterID != *filters.ClusterID {
		return false
	}

	// Time range filter
	if filters.StartTime != nil && decision.CreatedAt.Before(*filters.StartTime) {
		return false
	}
	if filters.EndTime != nil && decision.CreatedAt.After(*filters.EndTime) {
		return false
	}

	return true
}

// matchesSearchQuery checks if a decision matches the search query
func (s *memoryDecisionStore) matchesSearchQuery(decision *types.Decision, query *storage.DecisionSearchQuery) bool {
	if query == nil {
		return true
	}

	// Apply filters first
	if query.Filters != nil && !s.matchesFilters(decision, query.Filters) {
		return false
	}

	// Apply text search
	if query.Query != "" {
		searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s %s %s",
			decision.ID,
			string(decision.Type),
			string(decision.Status),
			string(decision.Reason),
			decision.WorkloadID,
			decision.PolicyID,
		))

		if !strings.Contains(searchText, strings.ToLower(query.Query)) {
			return false
		}
	}

	return true
}

// matchesAnalyticsFilters checks if a decision matches analytics filters
func (s *memoryDecisionStore) matchesAnalyticsFilters(decision *types.Decision, filters *storage.AnalyticsFilters) bool {
	if filters == nil {
		return true
	}

	// Policy ID filter
	if filters.PolicyID != nil && decision.PolicyID != *filters.PolicyID {
		return false
	}

	// Workload ID filter
	if filters.WorkloadID != nil && decision.WorkloadID != *filters.WorkloadID {
		return false
	}

	// Cluster ID filter
	if filters.ClusterID != nil && decision.ClusterID != *filters.ClusterID {
		return false
	}

	// Type filter
	if filters.Type != nil && string(decision.Type) != *filters.Type {
		return false
	}

	// Status filter
	if filters.Status != nil && string(decision.Status) != *filters.Status {
		return false
	}

	return true
}

// sortDecisions sorts decisions by the specified field
func (s *memoryDecisionStore) sortDecisions(decisions []*types.Decision, sortBy, sortOrder string) {
	switch sortBy {
	case "type":
		if sortOrder == "desc" {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Type > decisions[j].Type
			})
		} else {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Type < decisions[j].Type
			})
		}
	case "status":
		if sortOrder == "desc" {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Status > decisions[j].Status
			})
		} else {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Status < decisions[j].Status
			})
		}
	case "confidence":
		if sortOrder == "desc" {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Confidence < decisions[j].Confidence
			})
		} else {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Confidence > decisions[j].Confidence
			})
		}
	case "score":
		if sortOrder == "desc" {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Score < decisions[j].Score
			})
		} else {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].Score > decisions[j].Score
			})
		}
	case "created":
		if sortOrder == "desc" {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].CreatedAt.Before(decisions[j].CreatedAt)
			})
		} else {
			sort.Slice(decisions, func(i, j int) bool {
				return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
			})
		}
	default:
		// Default sort by creation time (newest first)
		sort.Slice(decisions, func(i, j int) bool {
			return decisions[i].CreatedAt.After(decisions[j].CreatedAt)
		})
	}
}
