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

// memoryEvaluationStore implements EvaluationStore interface using in-memory storage
type memoryEvaluationStore struct {
	evaluations map[string]*types.EvaluationResult
	mu          sync.RWMutex
}

// NewMemoryEvaluationStore creates a new memory-based evaluation store
func NewMemoryEvaluationStore() storage.EvaluationStore {
	return &memoryEvaluationStore{
		evaluations: make(map[string]*types.EvaluationResult),
	}
}

// Create creates a new evaluation result
func (s *memoryEvaluationStore) Create(ctx context.Context, result *types.EvaluationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not provided
	evaluationID := s.generateEvaluationID(result)

	// Check if evaluation with same ID already exists
	if _, exists := s.evaluations[evaluationID]; exists {
		return types.NewEvaluationError(result.WorkloadID, result.PolicyID, result.PolicyName, "create", types.ErrDecisionAlreadyExists)
	}

	// Set timestamp
	result.Timestamp = time.Now()

	// Validate evaluation result
	if err := s.validateEvaluationResult(result); err != nil {
		return types.NewEvaluationError(result.WorkloadID, result.PolicyID, result.PolicyName, "create", err)
	}

	// Store evaluation result
	s.evaluations[evaluationID] = result

	return nil
}

// Get retrieves an evaluation result by ID
func (s *memoryEvaluationStore) Get(ctx context.Context, id string) (*types.EvaluationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result, exists := s.evaluations[id]
	if !exists {
		return nil, types.NewEvaluationError("", "", "", "get", types.ErrDecisionNotFound)
	}

	// Return a copy to avoid modification
	resultCopy := *result
	return &resultCopy, nil
}

// Update updates an existing evaluation result
func (s *memoryEvaluationStore) Update(ctx context.Context, result *types.EvaluationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	evaluationID := s.generateEvaluationID(result)

	// Check if evaluation exists
	if _, exists := s.evaluations[evaluationID]; !exists {
		return types.NewEvaluationError(result.WorkloadID, result.PolicyID, result.PolicyName, "update", types.ErrDecisionNotFound)
	}

	// Update timestamp
	result.Timestamp = time.Now()

	// Validate evaluation result
	if err := s.validateEvaluationResult(result); err != nil {
		return types.NewEvaluationError(result.WorkloadID, result.PolicyID, result.PolicyName, "update", err)
	}

	// Update evaluation result
	s.evaluations[evaluationID] = result

	return nil
}

// Delete deletes an evaluation result by ID
func (s *memoryEvaluationStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.evaluations[id]
	if !exists {
		return types.NewEvaluationError("", "", "", "delete", types.ErrDecisionNotFound)
	}

	// Remove from map
	delete(s.evaluations, id)

	return nil
}

// List lists evaluation results with optional filters
func (s *memoryEvaluationStore) List(ctx context.Context, filters *storage.EvaluationFilters) ([]*types.EvaluationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*types.EvaluationResult

	for _, result := range s.evaluations {
		if s.matchesFilters(result, filters) {
			// Return a copy to avoid modification
			resultCopy := *result
			results = append(results, &resultCopy)
		}
	}

	// Sort by timestamp (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	// Apply pagination
	if filters != nil {
		if filters.Offset > 0 && filters.Offset < len(results) {
			results = results[filters.Offset:]
		}
		if filters.Limit > 0 && filters.Limit < len(results) {
			results = results[:filters.Limit]
		}
	}

	return results, nil
}

// GetByWorkload retrieves evaluation results by workload ID
func (s *memoryEvaluationStore) GetByWorkload(ctx context.Context, workloadID string) ([]*types.EvaluationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*types.EvaluationResult
	for _, result := range s.evaluations {
		if result.WorkloadID == workloadID {
			resultCopy := *result
			results = append(results, &resultCopy)
		}
	}

	// Sort by timestamp (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	return results, nil
}

// GetByPolicy retrieves evaluation results by policy ID
func (s *memoryEvaluationStore) GetByPolicy(ctx context.Context, policyID string) ([]*types.EvaluationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*types.EvaluationResult
	for _, result := range s.evaluations {
		if result.PolicyID == policyID {
			resultCopy := *result
			results = append(results, &resultCopy)
		}
	}

	// Sort by timestamp (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	return results, nil
}

// GetLatestByWorkload retrieves the latest evaluation result for a workload
func (s *memoryEvaluationStore) GetLatestByWorkload(ctx context.Context, workloadID string) (*types.EvaluationResult, error) {
	results, err := s.GetByWorkload(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, types.NewEvaluationError(workloadID, "", "", "getLatestByWorkload", types.ErrDecisionNotFound)
	}

	// Results are already sorted by timestamp (newest first)
	return results[0], nil
}

// GetWorkloadHistory retrieves evaluation history for a workload
func (s *memoryEvaluationStore) GetWorkloadHistory(ctx context.Context, workloadID string, filters *storage.EvaluationFilters) ([]*types.Evaluation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var evaluations []*types.Evaluation
	for _, result := range s.evaluations {
		if result.WorkloadID == workloadID && s.matchesFilters(result, filters) {
			// Convert EvaluationResult to Evaluation
			resultCopy := *result
			evaluation := &types.Evaluation{
				ID:         s.generateEvaluationID(result),
				PolicyID:   result.PolicyID,
				WorkloadID: result.WorkloadID,
				Status:     types.EvaluationStatusCompleted,
				Result:     &resultCopy,
				StartTime:  result.Timestamp,
				EndTime:    &result.Timestamp,
				Duration:   result.Duration,
			}
			evaluations = append(evaluations, evaluation)
		}
	}

	// Sort by start time (newest first)
	sort.Slice(evaluations, func(i, j int) bool {
		return evaluations[i].StartTime.After(evaluations[j].StartTime)
	})

	// Apply pagination
	if filters != nil {
		if filters.Offset > 0 && filters.Offset < len(evaluations) {
			evaluations = evaluations[filters.Offset:]
		}
		if filters.Limit > 0 && filters.Limit < len(evaluations) {
			evaluations = evaluations[:filters.Limit]
		}
	}

	return evaluations, nil
}

// GetPolicyHistory retrieves evaluation history for a policy
func (s *memoryEvaluationStore) GetPolicyHistory(ctx context.Context, policyID string, filters *storage.EvaluationFilters) ([]*types.Evaluation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var evaluations []*types.Evaluation
	for _, result := range s.evaluations {
		if result.PolicyID == policyID && s.matchesFilters(result, filters) {
			// Convert EvaluationResult to Evaluation
			resultCopy := *result
			evaluation := &types.Evaluation{
				ID:         s.generateEvaluationID(result),
				PolicyID:   result.PolicyID,
				WorkloadID: result.WorkloadID,
				Status:     types.EvaluationStatusCompleted,
				Result:     &resultCopy,
				StartTime:  result.Timestamp,
				EndTime:    &result.Timestamp,
				Duration:   result.Duration,
			}
			evaluations = append(evaluations, evaluation)
		}
	}

	// Sort by start time (newest first)
	sort.Slice(evaluations, func(i, j int) bool {
		return evaluations[i].StartTime.After(evaluations[j].StartTime)
	})

	// Apply pagination
	if filters != nil {
		if filters.Offset > 0 && filters.Offset < len(evaluations) {
			evaluations = evaluations[filters.Offset:]
		}
		if filters.Limit > 0 && filters.Limit < len(evaluations) {
			evaluations = evaluations[:filters.Limit]
		}
	}

	return evaluations, nil
}

// GetStatistics retrieves statistics for evaluations
func (s *memoryEvaluationStore) GetStatistics(ctx context.Context, filters *storage.EvaluationFilters) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]interface{})

	var totalCount, applicableCount int
	var totalScore, totalDuration float64
	policyTypeCounts := make(map[string]int)

	for _, result := range s.evaluations {
		if s.matchesFilters(result, filters) {
			totalCount++
			if result.Applicable {
				applicableCount++
			}
			totalScore += result.Score
			totalDuration += result.Duration.Seconds()

			policyType := string(result.PolicyType)
			policyTypeCounts[policyType]++
		}
	}

	stats["total_count"] = totalCount
	stats["applicable_count"] = applicableCount
	stats["not_applicable_count"] = totalCount - applicableCount

	if totalCount > 0 {
		stats["average_score"] = totalScore / float64(totalCount)
		stats["average_duration_seconds"] = totalDuration / float64(totalCount)
		stats["applicable_percentage"] = float64(applicableCount) / float64(totalCount) * 100
	} else {
		stats["average_score"] = 0.0
		stats["average_duration_seconds"] = 0.0
		stats["applicable_percentage"] = 0.0
	}

	stats["policy_type_counts"] = policyTypeCounts

	return stats, nil
}

// CreateMany creates multiple evaluation results
func (s *memoryEvaluationStore) CreateMany(ctx context.Context, results []*types.EvaluationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, result := range results {
		evaluationID := s.generateEvaluationID(result)

		// Check if evaluation with same ID already exists
		if _, exists := s.evaluations[evaluationID]; exists {
			return types.NewEvaluationError(result.WorkloadID, result.PolicyID, result.PolicyName, "createMany", types.ErrDecisionAlreadyExists)
		}

		// Set timestamp
		result.Timestamp = time.Now()

		// Validate evaluation result
		if err := s.validateEvaluationResult(result); err != nil {
			return types.NewEvaluationError(result.WorkloadID, result.PolicyID, result.PolicyName, "createMany", err)
		}

		// Store evaluation result
		s.evaluations[evaluationID] = result
	}

	return nil
}

// UpdateMany updates multiple evaluation results
func (s *memoryEvaluationStore) UpdateMany(ctx context.Context, results []*types.EvaluationResult) error {
	for _, result := range results {
		if err := s.Update(ctx, result); err != nil {
			return err
		}
	}
	return nil
}

// DeleteMany deletes multiple evaluation results
func (s *memoryEvaluationStore) DeleteMany(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Search searches evaluation results with query
func (s *memoryEvaluationStore) Search(ctx context.Context, query *storage.EvaluationSearchQuery) ([]*types.EvaluationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*types.EvaluationResult

	for _, result := range s.evaluations {
		if s.matchesSearchQuery(result, query) {
			resultCopy := *result
			results = append(results, &resultCopy)
		}
	}

	// Sort results
	if query.SortBy != "" {
		s.sortEvaluationResults(results, query.SortBy, query.SortOrder)
	} else {
		// Default sort by timestamp
		sort.Slice(results, func(i, j int) bool {
			return results[i].Timestamp.After(results[j].Timestamp)
		})
	}

	// Apply pagination
	if query.Offset > 0 && query.Offset < len(results) {
		results = results[query.Offset:]
	}
	if query.Limit > 0 && query.Limit < len(results) {
		results = results[:query.Limit]
	}

	return results, nil
}

// Count counts evaluation results matching filters
func (s *memoryEvaluationStore) Count(ctx context.Context, filters *storage.EvaluationFilters) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := int64(0)
	for _, result := range s.evaluations {
		if s.matchesFilters(result, filters) {
			count++
		}
	}

	return count, nil
}

// Health checks the health of the store
func (s *memoryEvaluationStore) Health(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Basic health check - ensure map is accessible
	_ = len(s.evaluations)

	return nil
}

// Close closes the store
func (s *memoryEvaluationStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear all data
	s.evaluations = make(map[string]*types.EvaluationResult)

	return nil
}

// Helper methods

// generateEvaluationID generates a unique ID for an evaluation result
func (s *memoryEvaluationStore) generateEvaluationID(result *types.EvaluationResult) string {
	return fmt.Sprintf("eval-%s-%s-%d", result.WorkloadID, result.PolicyID, time.Now().UnixNano())
}

// validateEvaluationResult validates an evaluation result
func (s *memoryEvaluationStore) validateEvaluationResult(result *types.EvaluationResult) error {
	if result.WorkloadID == "" {
		return types.ErrInvalidEvaluationInput
	}
	if result.PolicyID == "" {
		return types.ErrInvalidEvaluationInput
	}
	if result.PolicyName == "" {
		return types.ErrInvalidEvaluationInput
	}
	return nil
}

// matchesFilters checks if an evaluation result matches the given filters
func (s *memoryEvaluationStore) matchesFilters(result *types.EvaluationResult, filters *storage.EvaluationFilters) bool {
	if filters == nil {
		return true
	}

	// Policy ID filter
	if filters.PolicyID != nil && result.PolicyID != *filters.PolicyID {
		return false
	}

	// Workload ID filter
	if filters.WorkloadID != nil && result.WorkloadID != *filters.WorkloadID {
		return false
	}

	// Applicable filter
	if filters.Applicable != nil && result.Applicable != *filters.Applicable {
		return false
	}

	// Time range filter
	if filters.StartTime != nil && result.Timestamp.Before(*filters.StartTime) {
		return false
	}
	if filters.EndTime != nil && result.Timestamp.After(*filters.EndTime) {
		return false
	}

	return true
}

// matchesSearchQuery checks if an evaluation result matches the search query
func (s *memoryEvaluationStore) matchesSearchQuery(result *types.EvaluationResult, query *storage.EvaluationSearchQuery) bool {
	if query == nil {
		return true
	}

	// Apply filters first
	if query.Filters != nil && !s.matchesFilters(result, query.Filters) {
		return false
	}

	// Apply text search
	if query.Query != "" {
		searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s",
			result.PolicyID,
			result.PolicyName,
			string(result.PolicyType),
			result.WorkloadID,
		))

		if !strings.Contains(searchText, strings.ToLower(query.Query)) {
			return false
		}
	}

	return true
}

// sortEvaluationResults sorts evaluation results by the specified field
func (s *memoryEvaluationStore) sortEvaluationResults(results []*types.EvaluationResult, sortBy, sortOrder string) {
	switch sortBy {
	case "policyName":
		if sortOrder == "desc" {
			sort.Slice(results, func(i, j int) bool {
				return results[i].PolicyName > results[j].PolicyName
			})
		} else {
			sort.Slice(results, func(i, j int) bool {
				return results[i].PolicyName < results[j].PolicyName
			})
		}
	case "score":
		if sortOrder == "desc" {
			sort.Slice(results, func(i, j int) bool {
				return results[i].Score < results[j].Score
			})
		} else {
			sort.Slice(results, func(i, j int) bool {
				return results[i].Score > results[j].Score
			})
		}
	case "duration":
		if sortOrder == "desc" {
			sort.Slice(results, func(i, j int) bool {
				return results[i].Duration < results[j].Duration
			})
		} else {
			sort.Slice(results, func(i, j int) bool {
				return results[i].Duration > results[j].Duration
			})
		}
	case "applicable":
		if sortOrder == "desc" {
			sort.Slice(results, func(i, j int) bool {
				return !results[i].Applicable && results[j].Applicable
			})
		} else {
			sort.Slice(results, func(i, j int) bool {
				return results[i].Applicable && !results[j].Applicable
			})
		}
	case "timestamp":
		if sortOrder == "desc" {
			sort.Slice(results, func(i, j int) bool {
				return results[i].Timestamp.Before(results[j].Timestamp)
			})
		} else {
			sort.Slice(results, func(i, j int) bool {
				return results[i].Timestamp.After(results[j].Timestamp)
			})
		}
	default:
		// Default sort by timestamp (newest first)
		sort.Slice(results, func(i, j int) bool {
			return results[i].Timestamp.After(results[j].Timestamp)
		})
	}
}
