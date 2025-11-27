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

// memoryWorkloadStore implements WorkloadStore interface using in-memory storage
type memoryWorkloadStore struct {
	workloads map[string]*types.Workload
	names     map[string]string                   // name -> id mapping
	metrics   map[string][]*types.WorkloadMetrics // workloadID -> metrics
	history   map[string][]*types.WorkloadHistory // workloadID -> history
	mu        sync.RWMutex
}

// NewMemoryWorkloadStore creates a new memory-based workload store
func NewMemoryWorkloadStore() storage.WorkloadStore {
	return &memoryWorkloadStore{
		workloads: make(map[string]*types.Workload),
		names:     make(map[string]string),
		metrics:   make(map[string][]*types.WorkloadMetrics),
		history:   make(map[string][]*types.WorkloadHistory),
	}
}

// Create creates a new workload
func (s *memoryWorkloadStore) Create(ctx context.Context, workload *types.Workload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not provided
	workloadID := s.generateWorkloadID(workload)

	// Check if workload with same name already exists
	if _, exists := s.names[workload.Name]; exists {
		return types.NewWorkloadError(workloadID, workload.Name, string(workload.Type), "create", types.ErrWorkloadAlreadyExists)
	}

	// Set timestamps
	workload.CreatedAt = time.Now()
	workload.UpdatedAt = time.Now()

	// Validate workload
	if err := s.validateWorkload(workload); err != nil {
		return types.NewWorkloadError(workloadID, workload.Name, string(workload.Type), "create", err)
	}

	// Store workload
	s.workloads[workloadID] = workload
	s.names[workload.Name] = workloadID

	// Initialize empty metrics and history
	s.metrics[workloadID] = []*types.WorkloadMetrics{}
	s.history[workloadID] = []*types.WorkloadHistory{}

	return nil
}

// Get retrieves a workload by ID
func (s *memoryWorkloadStore) Get(ctx context.Context, id string) (*types.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workload, exists := s.workloads[id]
	if !exists {
		return nil, types.NewWorkloadError(id, "", "", "get", types.ErrWorkloadNotFound)
	}

	// Return a copy to avoid modification
	workloadCopy := *workload
	return &workloadCopy, nil
}

// Update updates an existing workload
func (s *memoryWorkloadStore) Update(ctx context.Context, workload *types.Workload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	workloadID := s.generateWorkloadID(workload)

	// Check if workload exists
	if _, exists := s.workloads[workloadID]; !exists {
		return types.NewWorkloadError(workloadID, workload.Name, string(workload.Type), "update", types.ErrWorkloadNotFound)
	}

	// Update timestamp
	workload.UpdatedAt = time.Now()

	// Validate workload
	if err := s.validateWorkload(workload); err != nil {
		return types.NewWorkloadError(workloadID, workload.Name, string(workload.Type), "update", err)
	}

	// Update workload
	s.workloads[workloadID] = workload

	// Update name mapping if name changed
	if oldWorkload, exists := s.workloads[workloadID]; exists {
		oldName := oldWorkload.Name
		if oldName != workload.Name {
			delete(s.names, oldName)
			s.names[workload.Name] = workloadID
		}
	}

	return nil
}

// Delete deletes a workload by ID
func (s *memoryWorkloadStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	workload, exists := s.workloads[id]
	if !exists {
		return types.NewWorkloadError(id, "", "", "delete", types.ErrWorkloadNotFound)
	}

	// Remove from all maps
	delete(s.workloads, id)
	delete(s.names, workload.Name)
	delete(s.metrics, id)
	delete(s.history, id)

	return nil
}

// List lists workloads with optional filters
func (s *memoryWorkloadStore) List(ctx context.Context, filters *storage.WorkloadFilters) ([]*types.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var workloads []*types.Workload

	for _, workload := range s.workloads {
		if s.matchesFilters(workload, filters) {
			// Return a copy to avoid modification
			workloadCopy := *workload
			workloads = append(workloads, &workloadCopy)
		}
	}

	// Sort by creation time (newest first)
	sort.Slice(workloads, func(i, j int) bool {
		return workloads[i].CreatedAt.After(workloads[j].CreatedAt)
	})

	// Apply pagination
	if filters != nil {
		if filters.Offset > 0 && filters.Offset < len(workloads) {
			workloads = workloads[filters.Offset:]
		}
		if filters.Limit > 0 && filters.Limit < len(workloads) {
			workloads = workloads[:filters.Limit]
		}
	}

	return workloads, nil
}

// GetByType retrieves workloads by type
func (s *memoryWorkloadStore) GetByType(ctx context.Context, workloadType types.WorkloadType) ([]*types.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var workloads []*types.Workload
	for _, workload := range s.workloads {
		if workload.Type == workloadType {
			workloadCopy := *workload
			workloads = append(workloads, &workloadCopy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(workloads, func(i, j int) bool {
		return workloads[i].Priority > workloads[j].Priority
	})

	return workloads, nil
}

// GetByStatus retrieves workloads by status
func (s *memoryWorkloadStore) GetByStatus(ctx context.Context, status types.WorkloadStatus) ([]*types.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var workloads []*types.Workload
	for _, workload := range s.workloads {
		if workload.Status == status {
			workloadCopy := *workload
			workloads = append(workloads, &workloadCopy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(workloads, func(i, j int) bool {
		return workloads[i].Priority > workloads[j].Priority
	})

	return workloads, nil
}

// GetByCluster retrieves workloads by cluster ID
func (s *memoryWorkloadStore) GetByCluster(ctx context.Context, clusterID string) ([]*types.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var workloads []*types.Workload
	for _, workload := range s.workloads {
		// Check if workload has cluster information in metadata or labels
		if s.workloadBelongsToCluster(workload, clusterID) {
			workloadCopy := *workload
			workloads = append(workloads, &workloadCopy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(workloads, func(i, j int) bool {
		return workloads[i].Priority > workloads[j].Priority
	})

	return workloads, nil
}

// GetByNode retrieves workloads by node ID
func (s *memoryWorkloadStore) GetByNode(ctx context.Context, nodeID string) ([]*types.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var workloads []*types.Workload
	for _, workload := range s.workloads {
		// Check if workload has node information in metadata or labels
		if s.workloadBelongsToNode(workload, nodeID) {
			workloadCopy := *workload
			workloads = append(workloads, &workloadCopy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(workloads, func(i, j int) bool {
		return workloads[i].Priority > workloads[j].Priority
	})

	return workloads, nil
}

// CreateMany creates multiple workloads
func (s *memoryWorkloadStore) CreateMany(ctx context.Context, workloads []*types.Workload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, workload := range workloads {
		workloadID := s.generateWorkloadID(workload)

		// Check if workload with same name already exists
		if _, exists := s.names[workload.Name]; exists {
			return types.NewWorkloadError(workloadID, workload.Name, string(workload.Type), "createMany", types.ErrWorkloadAlreadyExists)
		}

		// Set timestamps
		workload.CreatedAt = time.Now()
		workload.UpdatedAt = time.Now()

		// Validate workload
		if err := s.validateWorkload(workload); err != nil {
			return types.NewWorkloadError(workloadID, workload.Name, string(workload.Type), "createMany", err)
		}

		// Store workload
		s.workloads[workloadID] = workload
		s.names[workload.Name] = workloadID

		// Initialize empty metrics and history
		s.metrics[workloadID] = []*types.WorkloadMetrics{}
		s.history[workloadID] = []*types.WorkloadHistory{}
	}

	return nil
}

// UpdateMany updates multiple workloads
func (s *memoryWorkloadStore) UpdateMany(ctx context.Context, workloads []*types.Workload) error {
	for _, workload := range workloads {
		if err := s.Update(ctx, workload); err != nil {
			return err
		}
	}
	return nil
}

// DeleteMany deletes multiple workloads
func (s *memoryWorkloadStore) DeleteMany(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Search searches workloads with query
func (s *memoryWorkloadStore) Search(ctx context.Context, query *storage.WorkloadSearchQuery) ([]*types.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var workloads []*types.Workload

	for _, workload := range s.workloads {
		if s.matchesSearchQuery(workload, query) {
			workloadCopy := *workload
			workloads = append(workloads, &workloadCopy)
		}
	}

	// Sort results
	if query.SortBy != "" {
		s.sortWorkloads(workloads, query.SortBy, query.SortOrder)
	} else {
		// Default sort by creation time
		sort.Slice(workloads, func(i, j int) bool {
			return workloads[i].CreatedAt.After(workloads[j].CreatedAt)
		})
	}

	// Apply pagination
	if query.Offset > 0 && query.Offset < len(workloads) {
		workloads = workloads[query.Offset:]
	}
	if query.Limit > 0 && query.Limit < len(workloads) {
		workloads = workloads[:query.Limit]
	}

	return workloads, nil
}

// Count counts workloads matching filters
func (s *memoryWorkloadStore) Count(ctx context.Context, filters *storage.WorkloadFilters) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := int64(0)
	for _, workload := range s.workloads {
		if s.matchesFilters(workload, filters) {
			count++
		}
	}

	return count, nil
}

// GetMetrics retrieves workload metrics for a time range
func (s *memoryWorkloadStore) GetMetrics(ctx context.Context, workloadID string, startTime, endTime time.Time) ([]*types.WorkloadMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metrics, exists := s.metrics[workloadID]
	if !exists {
		return nil, types.NewWorkloadError(workloadID, "", "", "getMetrics", types.ErrWorkloadNotFound)
	}

	var filteredMetrics []*types.WorkloadMetrics
	for _, metric := range metrics {
		if metric.Timestamp.After(startTime) && metric.Timestamp.Before(endTime) {
			filteredMetrics = append(filteredMetrics, metric)
		}
	}

	// Sort by timestamp
	sort.Slice(filteredMetrics, func(i, j int) bool {
		return filteredMetrics[i].Timestamp.Before(filteredMetrics[j].Timestamp)
	})

	return filteredMetrics, nil
}

// GetHistory retrieves workload execution history
func (s *memoryWorkloadStore) GetHistory(ctx context.Context, workloadID string, limit int) ([]*types.WorkloadHistory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history, exists := s.history[workloadID]
	if !exists {
		return nil, types.NewWorkloadError(workloadID, "", "", "getHistory", types.ErrWorkloadNotFound)
	}

	// Sort by start time (newest first)
	sort.Slice(history, func(i, j int) bool {
		return history[i].StartTime.After(history[j].StartTime)
	})

	// Apply limit
	if limit > 0 && limit < len(history) {
		history = history[:limit]
	}

	// Return a copy to avoid modification
	result := make([]*types.WorkloadHistory, len(history))
	copy(result, history)

	return result, nil
}

// Health checks the health of the store
func (s *memoryWorkloadStore) Health(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Basic health check - ensure maps are accessible
	_ = len(s.workloads)
	_ = len(s.names)
	_ = len(s.metrics)
	_ = len(s.history)

	return nil
}

// Close closes the store
func (s *memoryWorkloadStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear all data
	s.workloads = make(map[string]*types.Workload)
	s.names = make(map[string]string)
	s.metrics = make(map[string][]*types.WorkloadMetrics)
	s.history = make(map[string][]*types.WorkloadHistory)

	return nil
}

// Helper methods

// generateWorkloadID generates a unique ID for a workload
func (s *memoryWorkloadStore) generateWorkloadID(workload *types.Workload) string {
	if workload.ID != "" {
		return workload.ID
	}
	return fmt.Sprintf("workload-%s-%d", workload.Name, time.Now().UnixNano())
}

// validateWorkload validates a workload
func (s *memoryWorkloadStore) validateWorkload(workload *types.Workload) error {
	if workload.Name == "" {
		return types.ErrInvalidWorkloadType
	}
	if workload.Type == "" {
		return types.ErrInvalidWorkloadType
	}
	if workload.Status == "" {
		return types.ErrInvalidWorkloadStatus
	}
	return nil
}

// matchesFilters checks if a workload matches the given filters
func (s *memoryWorkloadStore) matchesFilters(workload *types.Workload, filters *storage.WorkloadFilters) bool {
	if filters == nil {
		return true
	}

	// Type filter
	if filters.Type != nil && workload.Type != *filters.Type {
		return false
	}

	// Status filter
	if filters.Status != nil && workload.Status != *filters.Status {
		return false
	}

	// Cluster ID filter
	if filters.ClusterID != nil && !s.workloadBelongsToCluster(workload, *filters.ClusterID) {
		return false
	}

	// Node ID filter
	if filters.NodeID != nil && !s.workloadBelongsToNode(workload, *filters.NodeID) {
		return false
	}

	// Namespace filter
	if filters.Namespace != nil && workload.Metadata.Namespace != *filters.Namespace {
		return false
	}

	// Labels filter
	if len(filters.Labels) > 0 {
		for key, value := range filters.Labels {
			if workload.Labels == nil || workload.Labels[key] != value {
				return false
			}
		}
	}

	return true
}

// matchesSearchQuery checks if a workload matches the search query
func (s *memoryWorkloadStore) matchesSearchQuery(workload *types.Workload, query *storage.WorkloadSearchQuery) bool {
	if query == nil {
		return true
	}

	// Apply filters first
	if query.Filters != nil && !s.matchesFilters(workload, query.Filters) {
		return false
	}

	// Apply text search
	if query.Query != "" {
		searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s %s",
			workload.ID,
			workload.Name,
			string(workload.Type),
			string(workload.Status),
			workload.Metadata.Namespace,
		))

		// Add labels to search text
		for key, value := range workload.Labels {
			searchText += " " + strings.ToLower(key) + " " + strings.ToLower(value)
		}

		if !strings.Contains(searchText, strings.ToLower(query.Query)) {
			return false
		}
	}

	return true
}

// workloadBelongsToCluster checks if a workload belongs to a cluster
func (s *memoryWorkloadStore) workloadBelongsToCluster(workload *types.Workload, clusterID string) bool {
	// Check labels
	if workload.Labels != nil {
		if cluster, exists := workload.Labels["cluster"]; exists && cluster == clusterID {
			return true
		}
		if cluster, exists := workload.Labels["cluster-id"]; exists && cluster == clusterID {
			return true
		}
	}
	return false
}

// workloadBelongsToNode checks if a workload belongs to a node
func (s *memoryWorkloadStore) workloadBelongsToNode(workload *types.Workload, nodeID string) bool {
	// Check labels
	if workload.Labels != nil {
		if node, exists := workload.Labels["node"]; exists && node == nodeID {
			return true
		}
		if node, exists := workload.Labels["node-id"]; exists && node == nodeID {
			return true
		}
	}
	return false
}

// sortWorkloads sorts workloads by the specified field
func (s *memoryWorkloadStore) sortWorkloads(workloads []*types.Workload, sortBy, sortOrder string) {
	switch sortBy {
	case "name":
		if sortOrder == "desc" {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Name > workloads[j].Name
			})
		} else {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Name < workloads[j].Name
			})
		}
	case "type":
		if sortOrder == "desc" {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Type > workloads[j].Type
			})
		} else {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Type < workloads[j].Type
			})
		}
	case "status":
		if sortOrder == "desc" {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Status > workloads[j].Status
			})
		} else {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Status < workloads[j].Status
			})
		}
	case "priority":
		if sortOrder == "desc" {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Priority < workloads[j].Priority
			})
		} else {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].Priority > workloads[j].Priority
			})
		}
	case "created":
		if sortOrder == "desc" {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].CreatedAt.Before(workloads[j].CreatedAt)
			})
		} else {
			sort.Slice(workloads, func(i, j int) bool {
				return workloads[i].CreatedAt.After(workloads[j].CreatedAt)
			})
		}
	default:
		// Default sort by creation time (newest first)
		sort.Slice(workloads, func(i, j int) bool {
			return workloads[i].CreatedAt.After(workloads[j].CreatedAt)
		})
	}
}
