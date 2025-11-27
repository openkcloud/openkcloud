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

// memoryPolicyStore implements PolicyStore interface using in-memory storage
type memoryPolicyStore struct {
	policies map[string]types.Policy
	names    map[string]string         // name -> id mapping
	versions map[string][]types.Policy // policyID -> versions
	mu       sync.RWMutex
}

// NewMemoryPolicyStore creates a new memory-based policy store
func NewMemoryPolicyStore() storage.PolicyStore {
	return &memoryPolicyStore{
		policies: make(map[string]types.Policy),
		names:    make(map[string]string),
		versions: make(map[string][]types.Policy),
	}
}

// Create creates a new policy
func (s *memoryPolicyStore) Create(ctx context.Context, policy types.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not provided
	policyID := s.generatePolicyID(policy)

	// Check if policy with same name already exists
	if _, exists := s.names[policy.GetMetadata().Name]; exists {
		return types.NewPolicyError(policyID, policy.GetMetadata().Name, string(policy.GetType()), "create", types.ErrPolicyAlreadyExists)
	}

	// Set creation timestamp
	metadata := policy.GetMetadata()
	metadata.CreationTimestamp = time.Now()
	metadata.LastModified = time.Now()

	// Validate policy
	if err := policy.Validate(); err != nil {
		return types.NewPolicyError(policyID, metadata.Name, string(policy.GetType()), "create", err)
	}

	// Store policy
	s.policies[policyID] = policy
	s.names[metadata.Name] = policyID

	// Store version
	if versions, exists := s.versions[policyID]; exists {
		s.versions[policyID] = append(versions, policy)
	} else {
		s.versions[policyID] = []types.Policy{policy}
	}

	return nil
}

// Get retrieves a policy by ID
func (s *memoryPolicyStore) Get(ctx context.Context, id string) (types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	policy, exists := s.policies[id]
	if !exists {
		return nil, types.NewPolicyError(id, "", "", "get", types.ErrPolicyNotFound)
	}

	return policy, nil
}

// Update updates an existing policy
func (s *memoryPolicyStore) Update(ctx context.Context, policy types.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID if not provided
	policyID := s.generatePolicyID(policy)
	metadata := policy.GetMetadata()

	// Check if policy exists
	if _, exists := s.policies[policyID]; !exists {
		return types.NewPolicyError(policyID, metadata.Name, string(policy.GetType()), "update", types.ErrPolicyNotFound)
	}

	// Validate policy
	if err := policy.Validate(); err != nil {
		return types.NewPolicyError(policyID, metadata.Name, string(policy.GetType()), "update", err)
	}

	// Update timestamp
	metadata.LastModified = time.Now()

	// Update policy
	s.policies[policyID] = policy

	// Update name mapping if name changed
	if oldPolicy, exists := s.policies[policyID]; exists {
		oldName := oldPolicy.GetMetadata().Name
		if oldName != metadata.Name {
			delete(s.names, oldName)
			s.names[metadata.Name] = policyID
		}
	}

	// Add new version
	if versions, exists := s.versions[policyID]; exists {
		s.versions[policyID] = append(versions, policy)
	} else {
		s.versions[policyID] = []types.Policy{policy}
	}

	return nil
}

// Delete deletes a policy by ID
func (s *memoryPolicyStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	policy, exists := s.policies[id]
	if !exists {
		return types.NewPolicyError(id, "", "", "delete", types.ErrPolicyNotFound)
	}

	metadata := policy.GetMetadata()

	// Remove from all maps
	delete(s.policies, id)
	delete(s.names, metadata.Name)
	delete(s.versions, id)

	return nil
}

// List lists policies with optional filters
func (s *memoryPolicyStore) List(ctx context.Context, filters *storage.PolicyFilters) ([]types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var policies []types.Policy

	for _, policy := range s.policies {
		if s.matchesFilters(policy, filters) {
			policies = append(policies, policy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].GetPriority() > policies[j].GetPriority()
	})

	// Apply pagination
	if filters != nil {
		if filters.Offset > 0 && filters.Offset < len(policies) {
			policies = policies[filters.Offset:]
		}
		if filters.Limit > 0 && filters.Limit < len(policies) {
			policies = policies[:filters.Limit]
		}
	}

	return policies, nil
}

// GetByName retrieves a policy by name
func (s *memoryPolicyStore) GetByName(ctx context.Context, name string) (types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	policyID, exists := s.names[name]
	if !exists {
		return nil, types.NewPolicyError("", name, "", "getByName", types.ErrPolicyNotFound)
	}

	policy, exists := s.policies[policyID]
	if !exists {
		return nil, types.NewPolicyError(policyID, name, "", "getByName", types.ErrPolicyNotFound)
	}

	return policy, nil
}

// GetByType retrieves policies by type
func (s *memoryPolicyStore) GetByType(ctx context.Context, policyType types.PolicyType) ([]types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var policies []types.Policy
	for _, policy := range s.policies {
		if policy.GetType() == policyType {
			policies = append(policies, policy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].GetPriority() > policies[j].GetPriority()
	})

	return policies, nil
}

// GetByStatus retrieves policies by status
func (s *memoryPolicyStore) GetByStatus(ctx context.Context, status types.PolicyStatus) ([]types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var policies []types.Policy
	for _, policy := range s.policies {
		if policy.GetStatus() == status {
			policies = append(policies, policy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].GetPriority() > policies[j].GetPriority()
	})

	return policies, nil
}

// GetByPriority retrieves policies by priority
func (s *memoryPolicyStore) GetByPriority(ctx context.Context, priority types.Priority) ([]types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var policies []types.Policy
	for _, policy := range s.policies {
		if policy.GetPriority() == priority {
			policies = append(policies, policy)
		}
	}

	// Sort by name
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].GetMetadata().Name < policies[j].GetMetadata().Name
	})

	return policies, nil
}

// GetActivePolicies retrieves all active policies
func (s *memoryPolicyStore) GetActivePolicies(ctx context.Context) ([]types.Policy, error) {
	return s.GetByStatus(ctx, types.PolicyStatusActive)
}

// CreateMany creates multiple policies
func (s *memoryPolicyStore) CreateMany(ctx context.Context, policies []types.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, policy := range policies {
		policyID := s.generatePolicyID(policy)
		metadata := policy.GetMetadata()

		// Check if policy with same name already exists
		if _, exists := s.names[metadata.Name]; exists {
			return types.NewPolicyError(policyID, metadata.Name, string(policy.GetType()), "createMany", types.ErrPolicyAlreadyExists)
		}

		// Set timestamps
		metadata.CreationTimestamp = time.Now()
		metadata.LastModified = time.Now()

		// Validate policy
		if err := policy.Validate(); err != nil {
			return types.NewPolicyError(policyID, metadata.Name, string(policy.GetType()), "createMany", err)
		}

		// Store policy
		s.policies[policyID] = policy
		s.names[metadata.Name] = policyID

		// Store version
		if versions, exists := s.versions[policyID]; exists {
			s.versions[policyID] = append(versions, policy)
		} else {
			s.versions[policyID] = []types.Policy{policy}
		}
	}

	return nil
}

// UpdateMany updates multiple policies
func (s *memoryPolicyStore) UpdateMany(ctx context.Context, policies []types.Policy) error {
	for _, policy := range policies {
		if err := s.Update(ctx, policy); err != nil {
			return err
		}
	}
	return nil
}

// DeleteMany deletes multiple policies
func (s *memoryPolicyStore) DeleteMany(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Search searches policies with query
func (s *memoryPolicyStore) Search(ctx context.Context, query *storage.PolicySearchQuery) ([]types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var policies []types.Policy

	for _, policy := range s.policies {
		if s.matchesSearchQuery(policy, query) {
			policies = append(policies, policy)
		}
	}

	// Sort results
	if query.SortBy != "" {
		s.sortPolicies(policies, query.SortBy, query.SortOrder)
	} else {
		// Default sort by priority
		sort.Slice(policies, func(i, j int) bool {
			return policies[i].GetPriority() > policies[j].GetPriority()
		})
	}

	// Apply pagination
	if query.Offset > 0 && query.Offset < len(policies) {
		policies = policies[query.Offset:]
	}
	if query.Limit > 0 && query.Limit < len(policies) {
		policies = policies[:query.Limit]
	}

	return policies, nil
}

// Count counts policies matching filters
func (s *memoryPolicyStore) Count(ctx context.Context, filters *storage.PolicyFilters) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := int64(0)
	for _, policy := range s.policies {
		if s.matchesFilters(policy, filters) {
			count++
		}
	}

	return count, nil
}

// GetVersions retrieves all versions of a policy
func (s *memoryPolicyStore) GetVersions(ctx context.Context, policyID string) ([]types.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	versions, exists := s.versions[policyID]
	if !exists {
		return nil, types.NewPolicyError(policyID, "", "", "getVersions", types.ErrPolicyNotFound)
	}

	// Return a copy to avoid modification
	result := make([]types.Policy, len(versions))
	copy(result, versions)

	// Sort by creation timestamp (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].GetMetadata().CreationTimestamp.After(result[j].GetMetadata().CreationTimestamp)
	})

	return result, nil
}

// GetLatestVersion retrieves the latest version of a policy
func (s *memoryPolicyStore) GetLatestVersion(ctx context.Context, policyID string) (types.Policy, error) {
	versions, err := s.GetVersions(ctx, policyID)
	if err != nil {
		return nil, err
	}

	if len(versions) == 0 {
		return nil, types.NewPolicyError(policyID, "", "", "getLatestVersion", types.ErrPolicyNotFound)
	}

	return versions[0], nil
}

// Health checks the health of the store
func (s *memoryPolicyStore) Health(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Basic health check - ensure maps are accessible
	_ = len(s.policies)
	_ = len(s.names)
	_ = len(s.versions)

	return nil
}

// Close closes the store
func (s *memoryPolicyStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear all data
	s.policies = make(map[string]types.Policy)
	s.names = make(map[string]string)
	s.versions = make(map[string][]types.Policy)

	return nil
}

// Helper methods

// generatePolicyID generates a unique ID for a policy
func (s *memoryPolicyStore) generatePolicyID(policy types.Policy) string {
	metadata := policy.GetMetadata()
	if metadata.Name != "" {
		return fmt.Sprintf("%s-%s", string(policy.GetType()), metadata.Name)
	}
	return fmt.Sprintf("%s-%d", string(policy.GetType()), time.Now().UnixNano())
}

// matchesFilters checks if a policy matches the given filters
func (s *memoryPolicyStore) matchesFilters(policy types.Policy, filters *storage.PolicyFilters) bool {
	if filters == nil {
		return true
	}

	// Type filter
	if filters.Type != nil && policy.GetType() != *filters.Type {
		return false
	}

	// Status filter
	if filters.Status != nil && policy.GetStatus() != *filters.Status {
		return false
	}

	// Priority filter
	if filters.Priority != nil && policy.GetPriority() != *filters.Priority {
		return false
	}

	// Namespace filter
	if filters.Namespace != nil && policy.GetMetadata().Namespace != *filters.Namespace {
		return false
	}

	// Labels filter
	if len(filters.Labels) > 0 {
		policyLabels := policy.GetMetadata().Labels
		for key, value := range filters.Labels {
			if policyLabels == nil || policyLabels[key] != value {
				return false
			}
		}
	}

	return true
}

// matchesSearchQuery checks if a policy matches the search query
func (s *memoryPolicyStore) matchesSearchQuery(policy types.Policy, query *storage.PolicySearchQuery) bool {
	if query == nil {
		return true
	}

	// Apply filters first
	if query.Filters != nil && !s.matchesFilters(policy, query.Filters) {
		return false
	}

	// Apply text search
	if query.Query != "" {
		metadata := policy.GetMetadata()
		searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s",
			metadata.Name,
			string(policy.GetType()),
			string(policy.GetStatus()),
			metadata.Namespace,
		))

		// Add labels to search text
		for key, value := range metadata.Labels {
			searchText += " " + strings.ToLower(key) + " " + strings.ToLower(value)
		}

		if !strings.Contains(searchText, strings.ToLower(query.Query)) {
			return false
		}
	}

	return true
}

// sortPolicies sorts policies by the specified field
func (s *memoryPolicyStore) sortPolicies(policies []types.Policy, sortBy, sortOrder string) {
	switch sortBy {
	case "name":
		if sortOrder == "desc" {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetMetadata().Name > policies[j].GetMetadata().Name
			})
		} else {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetMetadata().Name < policies[j].GetMetadata().Name
			})
		}
	case "priority":
		if sortOrder == "desc" {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetPriority() < policies[j].GetPriority()
			})
		} else {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetPriority() > policies[j].GetPriority()
			})
		}
	case "created":
		if sortOrder == "desc" {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetMetadata().CreationTimestamp.Before(policies[j].GetMetadata().CreationTimestamp)
			})
		} else {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetMetadata().CreationTimestamp.After(policies[j].GetMetadata().CreationTimestamp)
			})
		}
	case "modified":
		if sortOrder == "desc" {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetMetadata().LastModified.Before(policies[j].GetMetadata().LastModified)
			})
		} else {
			sort.Slice(policies, func(i, j int) bool {
				return policies[i].GetMetadata().LastModified.After(policies[j].GetMetadata().LastModified)
			})
		}
	default:
		// Default sort by priority (highest first)
		sort.Slice(policies, func(i, j int) bool {
			return policies[i].GetPriority() > policies[j].GetPriority()
		})
	}
}
