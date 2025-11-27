package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/kcloud-opt/policy/internal/storage"
)

// memoryStorageManager implements StorageManager interface using in-memory storage
type memoryStorageManager struct {
	policyStore     storage.PolicyStore
	workloadStore   storage.WorkloadStore
	decisionStore   storage.DecisionStore
	evaluationStore storage.EvaluationStore
	mu              sync.RWMutex
	closed          bool
}

// NewMemoryStorageManager creates a new memory-based storage manager
func NewMemoryStorageManager() storage.StorageManager {
	return &memoryStorageManager{
		policyStore:     NewMemoryPolicyStore(),
		workloadStore:   NewMemoryWorkloadStore(),
		decisionStore:   NewMemoryDecisionStore(),
		evaluationStore: NewMemoryEvaluationStore(),
		closed:          false,
	}
}

// NewStorageManager creates a new storage manager (alias for NewMemoryStorageManager)
func NewStorageManager() storage.StorageManager {
	return NewMemoryStorageManager()
}

// Policy returns the policy store
func (m *memoryStorageManager) Policy() storage.PolicyStore {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil
	}

	return m.policyStore
}

// Workload returns the workload store
func (m *memoryStorageManager) Workload() storage.WorkloadStore {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil
	}

	return m.workloadStore
}

// Decision returns the decision store
func (m *memoryStorageManager) Decision() storage.DecisionStore {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil
	}

	return m.decisionStore
}

// Evaluation returns the evaluation store
func (m *memoryStorageManager) Evaluation() storage.EvaluationStore {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil
	}

	return m.evaluationStore
}

// BeginTransaction begins a new transaction
func (m *memoryStorageManager) BeginTransaction(ctx context.Context) (storage.Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, storage.ErrStorageConnection
	}

	// For memory storage, we'll create a simple transaction wrapper
	// In a real implementation with a database, this would create a proper transaction
	return &memoryTransaction{
		manager: m,
		ctx:     ctx,
	}, nil
}

// GetMetrics returns storage manager metrics
func (m *memoryStorageManager) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, fmt.Errorf("storage manager is closed")
	}

	// Get metrics from each store
	metrics := map[string]interface{}{
		"storage_type": "memory",
		"closed":       m.closed,
	}

	// Add store-specific metrics if available
	if policyStore, ok := m.policyStore.(*memoryPolicyStore); ok {
		policyStore.mu.RLock()
		metrics["policies_count"] = len(policyStore.policies)
		policyStore.mu.RUnlock()
	}

	if workloadStore, ok := m.workloadStore.(*memoryWorkloadStore); ok {
		workloadStore.mu.RLock()
		metrics["workloads_count"] = len(workloadStore.workloads)
		workloadStore.mu.RUnlock()
	}

	if decisionStore, ok := m.decisionStore.(*memoryDecisionStore); ok {
		decisionStore.mu.RLock()
		metrics["decisions_count"] = len(decisionStore.decisions)
		decisionStore.mu.RUnlock()
	}

	if evaluationStore, ok := m.evaluationStore.(*memoryEvaluationStore); ok {
		evaluationStore.mu.RLock()
		metrics["evaluations_count"] = len(evaluationStore.evaluations)
		evaluationStore.mu.RUnlock()
	}

	return metrics, nil
}

// Health checks the health of all stores
func (m *memoryStorageManager) Health(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return storage.ErrStorageConnection
	}

	// Check health of all stores
	if err := m.policyStore.Health(ctx); err != nil {
		return err
	}

	if err := m.workloadStore.Health(ctx); err != nil {
		return err
	}

	if err := m.decisionStore.Health(ctx); err != nil {
		return err
	}

	if err := m.evaluationStore.Health(ctx); err != nil {
		return err
	}

	return nil
}

// Close closes all stores
func (m *memoryStorageManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}

	var err error

	// Close all stores
	if closeErr := m.policyStore.Close(); closeErr != nil {
		err = closeErr
	}

	if closeErr := m.workloadStore.Close(); closeErr != nil {
		if err == nil {
			err = closeErr
		}
	}

	if closeErr := m.decisionStore.Close(); closeErr != nil {
		if err == nil {
			err = closeErr
		}
	}

	if closeErr := m.evaluationStore.Close(); closeErr != nil {
		if err == nil {
			err = closeErr
		}
	}

	m.closed = true

	return err
}

// memoryTransaction implements Transaction interface for memory storage
type memoryTransaction struct {
	manager         *memoryStorageManager
	ctx             context.Context
	policyStore     storage.PolicyStore
	workloadStore   storage.WorkloadStore
	decisionStore   storage.DecisionStore
	evaluationStore storage.EvaluationStore
	committed       bool
	rolledBack      bool
}

// Policy returns the policy store within the transaction
func (t *memoryTransaction) Policy() storage.PolicyStore {
	if t.committed || t.rolledBack {
		return nil
	}

	// For memory storage, we return the same store
	// In a real database implementation, this would return a transactional store
	return t.manager.policyStore
}

// Workload returns the workload store within the transaction
func (t *memoryTransaction) Workload() storage.WorkloadStore {
	if t.committed || t.rolledBack {
		return nil
	}

	return t.manager.workloadStore
}

// Decision returns the decision store within the transaction
func (t *memoryTransaction) Decision() storage.DecisionStore {
	if t.committed || t.rolledBack {
		return nil
	}

	return t.manager.decisionStore
}

// Evaluation returns the evaluation store within the transaction
func (t *memoryTransaction) Evaluation() storage.EvaluationStore {
	if t.committed || t.rolledBack {
		return nil
	}

	return t.manager.evaluationStore
}

// Commit commits the transaction
func (t *memoryTransaction) Commit() error {
	if t.committed {
		return nil // Already committed
	}

	if t.rolledBack {
		return storage.ErrStorageOperation // Cannot commit rolled back transaction
	}

	// For memory storage, commit is a no-op
	// In a real database implementation, this would commit the actual transaction
	t.committed = true

	return nil
}

// Rollback rolls back the transaction
func (t *memoryTransaction) Rollback() error {
	if t.rolledBack {
		return nil // Already rolled back
	}

	if t.committed {
		return storage.ErrStorageOperation // Cannot rollback committed transaction
	}

	// For memory storage, rollback is a no-op
	// In a real database implementation, this would rollback the actual transaction
	t.rolledBack = true

	return nil
}
