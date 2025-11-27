package storage

import (
	"context"
	"errors"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// Storage-related errors
var (
	ErrStorageConnection    = errors.New("storage connection error")
	ErrStorageOperation     = errors.New("storage operation error")
	ErrStorageNotFound      = errors.New("resource not found")
	ErrStorageAlreadyExists = errors.New("resource already exists")
	ErrStorageInvalidData   = errors.New("invalid data provided")
	ErrStorageTimeout       = errors.New("storage operation timeout")
)

// PolicyStore defines the interface for policy storage operations
type PolicyStore interface {
	// Basic CRUD operations
	Create(ctx context.Context, policy types.Policy) error
	Get(ctx context.Context, id string) (types.Policy, error)
	Update(ctx context.Context, policy types.Policy) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filters *PolicyFilters) ([]types.Policy, error)

	// Policy-specific operations
	GetByName(ctx context.Context, name string) (types.Policy, error)
	GetByType(ctx context.Context, policyType types.PolicyType) ([]types.Policy, error)
	GetByStatus(ctx context.Context, status types.PolicyStatus) ([]types.Policy, error)
	GetByPriority(ctx context.Context, priority types.Priority) ([]types.Policy, error)
	GetActivePolicies(ctx context.Context) ([]types.Policy, error)

	// Bulk operations
	CreateMany(ctx context.Context, policies []types.Policy) error
	UpdateMany(ctx context.Context, policies []types.Policy) error
	DeleteMany(ctx context.Context, ids []string) error

	// Search and filtering
	Search(ctx context.Context, query *PolicySearchQuery) ([]types.Policy, error)
	Count(ctx context.Context, filters *PolicyFilters) (int64, error)

	// Version management
	GetVersions(ctx context.Context, policyID string) ([]types.Policy, error)
	GetLatestVersion(ctx context.Context, policyID string) (types.Policy, error)

	// Health and maintenance
	Health(ctx context.Context) error
	Close() error
}

// WorkloadStore defines the interface for workload storage operations
type WorkloadStore interface {
	// Basic CRUD operations
	Create(ctx context.Context, workload *types.Workload) error
	Get(ctx context.Context, id string) (*types.Workload, error)
	Update(ctx context.Context, workload *types.Workload) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filters *WorkloadFilters) ([]*types.Workload, error)

	// Workload-specific operations
	GetByType(ctx context.Context, workloadType types.WorkloadType) ([]*types.Workload, error)
	GetByStatus(ctx context.Context, status types.WorkloadStatus) ([]*types.Workload, error)
	GetByCluster(ctx context.Context, clusterID string) ([]*types.Workload, error)
	GetByNode(ctx context.Context, nodeID string) ([]*types.Workload, error)

	// Bulk operations
	CreateMany(ctx context.Context, workloads []*types.Workload) error
	UpdateMany(ctx context.Context, workloads []*types.Workload) error
	DeleteMany(ctx context.Context, ids []string) error

	// Search and filtering
	Search(ctx context.Context, query *WorkloadSearchQuery) ([]*types.Workload, error)
	Count(ctx context.Context, filters *WorkloadFilters) (int64, error)

	// Metrics and history
	GetMetrics(ctx context.Context, workloadID string, startTime, endTime time.Time) ([]*types.WorkloadMetrics, error)
	GetHistory(ctx context.Context, workloadID string, limit int) ([]*types.WorkloadHistory, error)

	// Health and maintenance
	Health(ctx context.Context) error
	Close() error
}

// DecisionStore defines the interface for decision storage operations
type DecisionStore interface {
	// Basic CRUD operations
	Create(ctx context.Context, decision *types.Decision) error
	Get(ctx context.Context, id string) (*types.Decision, error)
	Update(ctx context.Context, decision *types.Decision) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filters *DecisionFilters) ([]*types.Decision, error)

	// Decision-specific operations
	GetByWorkload(ctx context.Context, workloadID string) ([]*types.Decision, error)
	GetByPolicy(ctx context.Context, policyID string) ([]*types.Decision, error)
	GetByStatus(ctx context.Context, status types.DecisionStatus) ([]*types.Decision, error)
	GetByType(ctx context.Context, decisionType types.DecisionType) ([]*types.Decision, error)

	// Bulk operations
	CreateMany(ctx context.Context, decisions []*types.Decision) error
	UpdateMany(ctx context.Context, decisions []*types.Decision) error
	DeleteMany(ctx context.Context, ids []string) error

	// Search and filtering
	Search(ctx context.Context, query *DecisionSearchQuery) ([]*types.Decision, error)
	Count(ctx context.Context, filters *DecisionFilters) (int64, error)

	// History and analytics
	GetHistory(ctx context.Context, decisionID string) ([]*types.DecisionHistory, error)
	GetAnalytics(ctx context.Context, query *AnalyticsQuery) (*AnalyticsResult, error)

	// Health and maintenance
	Health(ctx context.Context) error
	Close() error
}

// EvaluationStore defines the interface for evaluation result storage operations
type EvaluationStore interface {
	// Basic CRUD operations
	Create(ctx context.Context, result *types.EvaluationResult) error
	Get(ctx context.Context, id string) (*types.EvaluationResult, error)
	Update(ctx context.Context, result *types.EvaluationResult) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filters *EvaluationFilters) ([]*types.EvaluationResult, error)

	// Evaluation-specific operations
	GetByWorkload(ctx context.Context, workloadID string) ([]*types.EvaluationResult, error)
	GetByPolicy(ctx context.Context, policyID string) ([]*types.EvaluationResult, error)
	GetLatestByWorkload(ctx context.Context, workloadID string) (*types.EvaluationResult, error)
	GetWorkloadHistory(ctx context.Context, workloadID string, filters *EvaluationFilters) ([]*types.Evaluation, error)
	GetPolicyHistory(ctx context.Context, policyID string, filters *EvaluationFilters) ([]*types.Evaluation, error)
	GetStatistics(ctx context.Context, filters *EvaluationFilters) (map[string]interface{}, error)

	// Bulk operations
	CreateMany(ctx context.Context, results []*types.EvaluationResult) error
	UpdateMany(ctx context.Context, results []*types.EvaluationResult) error
	DeleteMany(ctx context.Context, ids []string) error

	// Search and filtering
	Search(ctx context.Context, query *EvaluationSearchQuery) ([]*types.EvaluationResult, error)
	Count(ctx context.Context, filters *EvaluationFilters) (int64, error)

	// Health and maintenance
	Health(ctx context.Context) error
	Close() error
}

// Filter structures for different store types

// PolicyFilters defines filters for policy queries
type PolicyFilters struct {
	Type      *types.PolicyType   `json:"type,omitempty"`
	Status    *types.PolicyStatus `json:"status,omitempty"`
	Priority  *types.Priority     `json:"priority,omitempty"`
	Namespace *string             `json:"namespace,omitempty"`
	Labels    map[string]string   `json:"labels,omitempty"`
	Limit     int                 `json:"limit,omitempty"`
	Offset    int                 `json:"offset,omitempty"`
}

// WorkloadFilters defines filters for workload queries
type WorkloadFilters struct {
	Type      *types.WorkloadType   `json:"type,omitempty"`
	Status    *types.WorkloadStatus `json:"status,omitempty"`
	ClusterID *string               `json:"clusterId,omitempty"`
	NodeID    *string               `json:"nodeId,omitempty"`
	Namespace *string               `json:"namespace,omitempty"`
	Labels    map[string]string     `json:"labels,omitempty"`
	Limit     int                   `json:"limit,omitempty"`
	Offset    int                   `json:"offset,omitempty"`
}

// DecisionFilters defines filters for decision queries
type DecisionFilters struct {
	Type       *types.DecisionType   `json:"type,omitempty"`
	Status     *types.DecisionStatus `json:"status,omitempty"`
	WorkloadID *string               `json:"workloadId,omitempty"`
	PolicyID   *string               `json:"policyId,omitempty"`
	ClusterID  *string               `json:"clusterId,omitempty"`
	StartTime  *time.Time            `json:"startTime,omitempty"`
	EndTime    *time.Time            `json:"endTime,omitempty"`
	Limit      int                   `json:"limit,omitempty"`
	Offset     int                   `json:"offset,omitempty"`
}

// EvaluationFilters defines filters for evaluation queries
type EvaluationFilters struct {
	PolicyID   *string    `json:"policyId,omitempty"`
	WorkloadID *string    `json:"workloadId,omitempty"`
	Applicable *bool      `json:"applicable,omitempty"`
	Status     *string    `json:"status,omitempty"`
	Result     *string    `json:"result,omitempty"`
	StartTime  *time.Time `json:"startTime,omitempty"`
	EndTime    *time.Time `json:"endTime,omitempty"`
	Limit      int        `json:"limit,omitempty"`
	Offset     int        `json:"offset,omitempty"`
}

// Search query structures

// PolicySearchQuery defines search parameters for policies
type PolicySearchQuery struct {
	Query     string         `json:"query"`
	Fields    []string       `json:"fields,omitempty"`
	Filters   *PolicyFilters `json:"filters,omitempty"`
	SortBy    string         `json:"sortBy,omitempty"`
	SortOrder string         `json:"sortOrder,omitempty"`
	Limit     int            `json:"limit,omitempty"`
	Offset    int            `json:"offset,omitempty"`
}

// WorkloadSearchQuery defines search parameters for workloads
type WorkloadSearchQuery struct {
	Query     string           `json:"query"`
	Fields    []string         `json:"fields,omitempty"`
	Filters   *WorkloadFilters `json:"filters,omitempty"`
	SortBy    string           `json:"sortBy,omitempty"`
	SortOrder string           `json:"sortOrder,omitempty"`
	Limit     int              `json:"limit,omitempty"`
	Offset    int              `json:"offset,omitempty"`
}

// DecisionSearchQuery defines search parameters for decisions
type DecisionSearchQuery struct {
	Query     string           `json:"query"`
	Fields    []string         `json:"fields,omitempty"`
	Filters   *DecisionFilters `json:"filters,omitempty"`
	SortBy    string           `json:"sortBy,omitempty"`
	SortOrder string           `json:"sortOrder,omitempty"`
	Limit     int              `json:"limit,omitempty"`
	Offset    int              `json:"offset,omitempty"`
}

// EvaluationSearchQuery defines search parameters for evaluations
type EvaluationSearchQuery struct {
	Query     string             `json:"query"`
	Fields    []string           `json:"fields,omitempty"`
	Filters   *EvaluationFilters `json:"filters,omitempty"`
	SortBy    string             `json:"sortBy,omitempty"`
	SortOrder string             `json:"sortOrder,omitempty"`
	Limit     int                `json:"limit,omitempty"`
	Offset    int                `json:"offset,omitempty"`
}

// Analytics structures

// AnalyticsQuery defines parameters for analytics queries
type AnalyticsQuery struct {
	Metric      string            `json:"metric"`
	Dimensions  []string          `json:"dimensions,omitempty"`
	Filters     *AnalyticsFilters `json:"filters,omitempty"`
	StartTime   time.Time         `json:"startTime"`
	EndTime     time.Time         `json:"endTime"`
	Granularity string            `json:"granularity,omitempty"`
	Limit       int               `json:"limit,omitempty"`
}

// AnalyticsFilters defines filters for analytics queries
type AnalyticsFilters struct {
	PolicyID   *string `json:"policyId,omitempty"`
	WorkloadID *string `json:"workloadId,omitempty"`
	ClusterID  *string `json:"clusterId,omitempty"`
	NodeID     *string `json:"nodeId,omitempty"`
	Type       *string `json:"type,omitempty"`
	Status     *string `json:"status,omitempty"`
}

// AnalyticsResult represents the result of an analytics query
type AnalyticsResult struct {
	Metric     string                 `json:"metric"`
	Dimensions []string               `json:"dimensions"`
	Data       []AnalyticsDataPoint   `json:"data"`
	Aggregates map[string]interface{} `json:"aggregates,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// AnalyticsDataPoint represents a single data point in analytics results
type AnalyticsDataPoint struct {
	Timestamp time.Time              `json:"timestamp"`
	Values    map[string]interface{} `json:"values"`
	Count     int64                  `json:"count,omitempty"`
}

// StorageManager defines the interface for managing all storage operations
type StorageManager interface {
	// Store accessors
	Policy() PolicyStore
	Workload() WorkloadStore
	Decision() DecisionStore
	Evaluation() EvaluationStore

	// Transaction support
	BeginTransaction(ctx context.Context) (Transaction, error)

	// Metrics and monitoring
	GetMetrics(ctx context.Context) (map[string]interface{}, error)

	// Health and maintenance
	Health(ctx context.Context) error
	Close() error
}

// Transaction defines the interface for database transactions
type Transaction interface {
	// Store accessors within transaction
	Policy() PolicyStore
	Workload() WorkloadStore
	Decision() DecisionStore
	Evaluation() EvaluationStore

	// Transaction control
	Commit() error
	Rollback() error
}
