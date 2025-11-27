package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockStorageManager is a mock implementation of storage.StorageManager
type MockStorageManager struct {
	mock.Mock
}

// Called is a helper method to access mock.Mock.Called
func (m *MockStorageManager) Called(args ...interface{}) mock.Arguments {
	return m.Mock.Called(args...)
}

// On is a helper method to access mock.Mock.On
func (m *MockStorageManager) On(methodName string, arguments ...interface{}) *mock.Call {
	return m.Mock.On(methodName, arguments...)
}

// AssertExpectations is a helper method to access mock.Mock.AssertExpectations
func (m *MockStorageManager) AssertExpectations(t mock.TestingT) bool {
	return m.Mock.AssertExpectations(t)
}

func (m *MockStorageManager) Policy() storage.PolicyStore {
	args := m.Called()
	if len(args) > 0 {
		return args.Get(0).(storage.PolicyStore)
	}
	return nil
}

func (m *MockStorageManager) Workload() storage.WorkloadStore {
	args := m.Called()
	return args.Get(0).(storage.WorkloadStore)
}

func (m *MockStorageManager) Decision() storage.DecisionStore {
	args := m.Called()
	return args.Get(0).(storage.DecisionStore)
}

func (m *MockStorageManager) Evaluation() storage.EvaluationStore {
	args := m.Called()
	return args.Get(0).(storage.EvaluationStore)
}

func (m *MockStorageManager) Health(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockStorageManager) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockStorageManager) BeginTransaction(ctx context.Context) (storage.Transaction, error) {
	args := m.Called(ctx)
	return args.Get(0).(storage.Transaction), args.Error(1)
}

func (m *MockStorageManager) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockPolicyStore is a mock implementation of storage.PolicyStore
type MockPolicyStore struct {
	mock.Mock
}

// Called is a helper method to access mock.Mock.Called
func (m *MockPolicyStore) Called(args ...interface{}) mock.Arguments {
	return m.Mock.Called(args...)
}

// On is a helper method to access mock.Mock.On
func (m *MockPolicyStore) On(methodName string, arguments ...interface{}) *mock.Call {
	return m.Mock.On(methodName, arguments...)
}

// AssertExpectations is a helper method to access mock.Mock.AssertExpectations
func (m *MockPolicyStore) AssertExpectations(t mock.TestingT) bool {
	return m.Mock.AssertExpectations(t)
}

func (m *MockPolicyStore) Create(ctx context.Context, policy types.Policy) error {
	args := m.Called(ctx, policy)
	return args.Error(0)
}

func (m *MockPolicyStore) Get(ctx context.Context, id string) (*types.Policy, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Policy), args.Error(1)
}

func (m *MockPolicyStore) Update(ctx context.Context, policy types.Policy) error {
	args := m.Called(ctx, policy)
	return args.Error(0)
}

func (m *MockPolicyStore) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockPolicyStore) List(ctx context.Context, filters *storage.PolicyFilters) ([]types.Policy, error) {
	args := m.Called(ctx, filters)
	return args.Get(0).([]types.Policy), args.Error(1)
}

func (m *MockPolicyStore) Count(ctx context.Context, filters *storage.PolicyFilters) (int, error) {
	args := m.Called(ctx, filters)
	return args.Int(0), args.Error(1)
}

func (m *MockPolicyStore) Search(ctx context.Context, query *storage.PolicySearchQuery) ([]types.Policy, error) {
	args := m.Called(ctx, query)
	return args.Get(0).([]types.Policy), args.Error(1)
}

func (m *MockPolicyStore) GetVersions(ctx context.Context, id string) ([]types.Policy, error) {
	args := m.Called(ctx, id)
	return args.Get(0).([]types.Policy), args.Error(1)
}

func (m *MockPolicyStore) Health(ctx context.Context) (map[string]interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockPolicyStore) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func TestPolicyHandler_CreatePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy creation", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policy
		policy := &types.CostOptimizationPolicy{
			Metadata: types.PolicyMetadata{
				Name:      "test-policy",
				Type:      types.PolicyTypeCostOptimization,
				Status:    types.PolicyStatusActive,
				Priority:  100,
				Namespace: "default",
			},
			Spec: types.CostOptimizationSpec{
				Priority: 100,
			},
		}

		mockPolicyStore.On("Create", mock.Anything, policy).Return(nil)

		// Create request
		policyJSON, _ := json.Marshal(policy)
		req, _ := http.NewRequest("POST", "/policies", bytes.NewBuffer(policyJSON))
		req.Header.Set("Content-Type", "application/json")

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.CreatePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusCreated, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		mockStorage := &MockStorageManager{}
		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create request with invalid JSON
		req, _ := http.NewRequest("POST", "/policies", bytes.NewBuffer([]byte("invalid json")))
		req.Header.Set("Content-Type", "application/json")

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.CreatePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("storage error", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policy
		policy := &types.CostOptimizationPolicy{
			Metadata: types.PolicyMetadata{
				Name:   "test-policy",
				Type:   types.PolicyTypeCostOptimization,
				Status: types.PolicyStatusActive,
			},
			Spec: types.CostOptimizationSpec{
				Priority: 100,
			},
		}

		mockPolicyStore.On("Create", mock.Anything, policy).Return(assert.AnError)

		// Create request
		policyJSON, _ := json.Marshal(policy)
		req, _ := http.NewRequest("POST", "/policies", bytes.NewBuffer(policyJSON))
		req.Header.Set("Content-Type", "application/json")

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.CreatePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}

func TestPolicyHandler_GetPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy retrieval", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policy
		policy := &types.CostOptimizationPolicy{
			Metadata: types.PolicyMetadata{
				Name:      "test-policy",
				Type:      types.PolicyTypeCostOptimization,
				Status:    types.PolicyStatusActive,
				Priority:  100,
				Namespace: "default",
			},
			Spec: types.CostOptimizationSpec{
				Priority: 100,
			},
		}

		mockPolicyStore.On("Get", mock.Anything, "test-policy").Return(policy, nil)

		// Create request
		req, _ := http.NewRequest("GET", "/policies/test-policy", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "test-policy"}}

		// Call handler
		handler.GetPolicy(c)

		// Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})

	t.Run("policy not found", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		mockPolicyStore.On("Get", mock.Anything, "non-existent-policy").Return(nil, assert.AnError)

		// Create request
		req, _ := http.NewRequest("GET", "/policies/non-existent-policy", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "non-existent-policy"}}

		// Call handler
		handler.GetPolicy(c)

		// Assertions
		assert.Equal(t, http.StatusNotFound, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}

func TestPolicyHandler_UpdatePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy update", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policy
		policy := &types.CostOptimizationPolicy{
			Metadata: types.PolicyMetadata{
				Name:      "test-policy",
				Type:      types.PolicyTypeCostOptimization,
				Status:    types.PolicyStatusActive,
				Priority:  200, // Updated priority
				Namespace: "default",
			},
			Spec: types.CostOptimizationSpec{
				Priority: 100,
			},
		}

		mockPolicyStore.On("Update", mock.Anything, policy).Return(nil)

		// Create request
		policyJSON, _ := json.Marshal(policy)
		req, _ := http.NewRequest("PUT", "/policies/test-policy", bytes.NewBuffer(policyJSON))
		req.Header.Set("Content-Type", "application/json")

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "test-policy"}}

		// Call handler
		handler.UpdatePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})

	t.Run("storage error", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policy
		policy := &types.CostOptimizationPolicy{
			Metadata: types.PolicyMetadata{
				Name:   "test-policy",
				Type:   types.PolicyTypeCostOptimization,
				Status: types.PolicyStatusActive,
			},
			Spec: types.CostOptimizationSpec{
				Priority: 100,
			},
		}

		mockPolicyStore.On("Update", mock.Anything, policy).Return(assert.AnError)

		// Create request
		policyJSON, _ := json.Marshal(policy)
		req, _ := http.NewRequest("PUT", "/policies/test-policy", bytes.NewBuffer(policyJSON))
		req.Header.Set("Content-Type", "application/json")

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "test-policy"}}

		// Call handler
		handler.UpdatePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}

func TestPolicyHandler_DeletePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy deletion", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		mockPolicyStore.On("Delete", mock.Anything, "test-policy").Return(nil)

		// Create request
		req, _ := http.NewRequest("DELETE", "/policies/test-policy", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "test-policy"}}

		// Call handler
		handler.DeletePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})

	t.Run("storage error", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		mockPolicyStore.On("Delete", mock.Anything, "test-policy").Return(assert.AnError)

		// Create request
		req, _ := http.NewRequest("DELETE", "/policies/test-policy", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "test-policy"}}

		// Call handler
		handler.DeletePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}

func TestPolicyHandler_ListPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy listing", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policies
		policies := []*types.CostOptimizationPolicy{
			{
				Metadata: types.PolicyMetadata{
					Name:   "policy-1",
					Type:   types.PolicyTypeCostOptimization,
					Status: types.PolicyStatusActive,
				},
				Spec: types.CostOptimizationSpec{
					Priority: 100,
				},
			},
			{
				Metadata: types.PolicyMetadata{
					Name:   "policy-2",
					Type:   types.PolicyTypeAutomation,
					Status: types.PolicyStatusActive,
				},
				Spec: types.CostOptimizationSpec{
					Priority: 200,
				},
			},
		}

		mockPolicyStore.On("List", mock.Anything, mock.Anything).Return(policies, nil)
		mockPolicyStore.On("Count", mock.Anything, mock.Anything).Return(2, nil)

		// Create request
		req, _ := http.NewRequest("GET", "/policies", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.ListPolicies(c)

		// Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})

	t.Run("storage error", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		mockPolicyStore.On("List", mock.Anything, mock.Anything).Return([]*types.CostOptimizationPolicy{}, assert.AnError)

		// Create request
		req, _ := http.NewRequest("GET", "/policies", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.ListPolicies(c)

		// Assertions
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}

func TestPolicyHandler_EnablePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy enable", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policy
		policy := &types.CostOptimizationPolicy{
			Metadata: types.PolicyMetadata{
				Name:   "test-policy",
				Type:   types.PolicyTypeCostOptimization,
				Status: types.PolicyStatusInactive,
			},
			Spec: types.CostOptimizationSpec{
				Priority: 100,
			},
		}

		mockPolicyStore.On("Get", mock.Anything, "test-policy").Return(policy, nil)
		mockPolicyStore.On("Update", mock.Anything, mock.AnythingOfType("types.Policy")).Return(nil)

		// Create request
		req, _ := http.NewRequest("POST", "/policies/test-policy/enable", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "test-policy"}}

		// Call handler
		handler.EnablePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})

	t.Run("policy not found", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		mockPolicyStore.On("Get", mock.Anything, "non-existent-policy").Return(nil, assert.AnError)

		// Create request
		req, _ := http.NewRequest("POST", "/policies/non-existent-policy/enable", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "non-existent-policy"}}

		// Call handler
		handler.EnablePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusNotFound, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}

func TestPolicyHandler_DisablePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy disable", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policy
		policy := &types.CostOptimizationPolicy{
			Metadata: types.PolicyMetadata{
				Name:   "test-policy",
				Type:   types.PolicyTypeCostOptimization,
				Status: types.PolicyStatusActive,
			},
			Spec: types.CostOptimizationSpec{
				Priority: 100,
			},
		}

		mockPolicyStore.On("Get", mock.Anything, "test-policy").Return(policy, nil)
		mockPolicyStore.On("Update", mock.Anything, mock.AnythingOfType("types.Policy")).Return(nil)

		// Create request
		req, _ := http.NewRequest("POST", "/policies/test-policy/disable", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = []gin.Param{{Key: "id", Value: "test-policy"}}

		// Call handler
		handler.DisablePolicy(c)

		// Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}

func TestPolicyHandler_SearchPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("successful policy search", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create test policies
		policies := []*types.CostOptimizationPolicy{
			{
				Metadata: types.PolicyMetadata{
					Name: "cost-optimization-policy",
					Type: types.PolicyTypeCostOptimization,
				},
				Spec: types.CostOptimizationSpec{
					Priority: 100,
				},
			},
		}

		mockPolicyStore.On("Search", mock.Anything, mock.AnythingOfType("*storage.PolicySearchQuery")).Return(policies, nil)

		// Create request
		req, _ := http.NewRequest("GET", "/policies/search?q=cost", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.SearchPolicies(c)

		// Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})

	t.Run("missing query parameter", func(t *testing.T) {
		mockStorage := &MockStorageManager{}
		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		// Create request without query parameter
		req, _ := http.NewRequest("GET", "/policies/search", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.SearchPolicies(c)

		// Assertions
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("storage error", func(t *testing.T) {
		// Setup mocks
		mockStorage := &MockStorageManager{}
		mockPolicyStore := &MockPolicyStore{}
		mockStorage.On("Policy").Return(mockPolicyStore)

		var logger types.Logger = nil
		handler := NewPolicyHandler(mockStorage, logger)

		mockPolicyStore.On("Search", mock.Anything, mock.AnythingOfType("*storage.PolicySearchQuery")).Return([]*types.CostOptimizationPolicy{}, assert.AnError)

		// Create request
		req, _ := http.NewRequest("GET", "/policies/search?q=test", nil)

		// Create response recorder
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req

		// Call handler
		handler.SearchPolicies(c)

		// Assertions
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		mockPolicyStore.AssertExpectations(t)
	})
}
