package evaluator

import (
	"context"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// PolicyEvaluator defines the interface for policy evaluation
type PolicyEvaluator interface {
	// Evaluate evaluates a workload against applicable policies
	Evaluate(ctx context.Context, workload *types.Workload, policies []types.Policy) ([]*types.EvaluationResult, error)

	// EvaluateSingle evaluates a workload against a single policy
	EvaluateSingle(ctx context.Context, workload *types.Workload, policy types.Policy) (*types.EvaluationResult, error)

	// GetApplicablePolicies returns policies that are applicable to a workload
	GetApplicablePolicies(ctx context.Context, workload *types.Workload, allPolicies []types.Policy) ([]types.Policy, error)

	// ValidatePolicy validates a policy for correctness
	ValidatePolicy(ctx context.Context, policy types.Policy) error

	// Health checks the health of the evaluator
	Health(ctx context.Context) error
}

// RuleEngine defines the interface for rule evaluation
type RuleEngine interface {
	// EvaluateCondition evaluates a condition against context
	EvaluateCondition(ctx context.Context, condition string, context map[string]interface{}) (bool, error)

	// EvaluateExpression evaluates an expression against context
	EvaluateExpression(ctx context.Context, expression string, context map[string]interface{}) (interface{}, error)

	// ValidateRule validates a rule for syntax correctness
	ValidateRule(ctx context.Context, rule string) error

	// Health checks the health of the rule engine
	Health(ctx context.Context) error
}

// ConflictResolver defines the interface for resolving policy conflicts
type ConflictResolver interface {
	// ResolveConflicts resolves conflicts between evaluation results
	ResolveConflicts(ctx context.Context, results []*types.EvaluationResult) (*types.ConflictResolution, error)

	// DetectConflicts detects conflicts in evaluation results
	DetectConflicts(ctx context.Context, results []*types.EvaluationResult) ([]*ConflictInfo, error)

	// Health checks the health of the conflict resolver
	Health(ctx context.Context) error
}

// ConflictInfo represents information about a conflict
type ConflictInfo struct {
	Type        string                 `json:"type"`
	Severity    string                 `json:"severity"`
	Policies    []string               `json:"policies"`
	Description string                 `json:"description"`
	Details     map[string]interface{} `json:"details,omitempty"`
}

// EvaluationContext represents the context for policy evaluation
type EvaluationContext struct {
	Workload    *types.Workload        `json:"workload"`
	ClusterInfo *ClusterInfo           `json:"clusterInfo,omitempty"`
	NodeInfo    *NodeInfo              `json:"nodeInfo,omitempty"`
	Metrics     map[string]interface{} `json:"metrics,omitempty"`
	Environment map[string]interface{} `json:"environment,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
	RequestID   string                 `json:"requestId,omitempty"`
	UserID      string                 `json:"userId,omitempty"`
}

// ClusterInfo represents cluster information for evaluation
type ClusterInfo struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Type        string                `json:"type"`
	Status      string                `json:"status"`
	Capacity    *ResourceCapacity     `json:"capacity,omitempty"`
	Allocated   *ResourceAllocation   `json:"allocated,omitempty"`
	Available   *ResourceAvailability `json:"available,omitempty"`
	Cost        *CostInfo             `json:"cost,omitempty"`
	Power       *PowerInfo            `json:"power,omitempty"`
	Performance *PerformanceInfo      `json:"performance,omitempty"`
	Labels      map[string]string     `json:"labels,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
}

// NodeInfo represents node information for evaluation
type NodeInfo struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	ClusterID   string                `json:"clusterId"`
	Status      string                `json:"status"`
	Capacity    *ResourceCapacity     `json:"capacity,omitempty"`
	Allocated   *ResourceAllocation   `json:"allocated,omitempty"`
	Available   *ResourceAvailability `json:"available,omitempty"`
	Cost        *CostInfo             `json:"cost,omitempty"`
	Power       *PowerInfo            `json:"power,omitempty"`
	Performance *PerformanceInfo      `json:"performance,omitempty"`
	Labels      map[string]string     `json:"labels,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
}

// ResourceCapacity represents resource capacity
type ResourceCapacity struct {
	CPU     int              `json:"cpu"`
	Memory  string           `json:"memory"`
	Storage string           `json:"storage,omitempty"`
	GPU     *GPUResource     `json:"gpu,omitempty"`
	NPU     *NPUResource     `json:"npu,omitempty"`
	Network *NetworkResource `json:"network,omitempty"`
}

// ResourceAllocation represents current resource allocation
type ResourceAllocation struct {
	CPU     int              `json:"cpu"`
	Memory  string           `json:"memory"`
	Storage string           `json:"storage,omitempty"`
	GPU     *GPUResource     `json:"gpu,omitempty"`
	NPU     *NPUResource     `json:"npu,omitempty"`
	Network *NetworkResource `json:"network,omitempty"`
}

// ResourceAvailability represents available resources
type ResourceAvailability struct {
	CPU     int              `json:"cpu"`
	Memory  string           `json:"memory"`
	Storage string           `json:"storage,omitempty"`
	GPU     *GPUResource     `json:"gpu,omitempty"`
	NPU     *NPUResource     `json:"npu,omitempty"`
	Network *NetworkResource `json:"network,omitempty"`
}

// GPUResource represents GPU resource information
type GPUResource struct {
	Count       int     `json:"count"`
	Type        string  `json:"type,omitempty"`
	Memory      string  `json:"memory,omitempty"`
	Utilization float64 `json:"utilization,omitempty"`
}

// NPUResource represents NPU resource information
type NPUResource struct {
	Count       int     `json:"count"`
	Type        string  `json:"type,omitempty"`
	Memory      string  `json:"memory,omitempty"`
	Utilization float64 `json:"utilization,omitempty"`
}

// NetworkResource represents network resource information
type NetworkResource struct {
	Bandwidth   string  `json:"bandwidth,omitempty"`
	Latency     string  `json:"latency,omitempty"`
	Utilization float64 `json:"utilization,omitempty"`
}

// CostInfo represents cost information
type CostInfo struct {
	CostPerHour   float64 `json:"costPerHour"`
	CostPerCPU    float64 `json:"costPerCPU,omitempty"`
	CostPerMemory float64 `json:"costPerMemory,omitempty"`
	CostPerGPU    float64 `json:"costPerGPU,omitempty"`
	CostPerNPU    float64 `json:"costPerNPU,omitempty"`
	Currency      string  `json:"currency"`
}

// PowerInfo represents power information
type PowerInfo struct {
	PowerConsumption float64 `json:"powerConsumption"`
	PowerEfficiency  float64 `json:"powerEfficiency,omitempty"`
	PowerLimit       float64 `json:"powerLimit,omitempty"`
	Unit             string  `json:"unit"`
}

// PerformanceInfo represents performance information
type PerformanceInfo struct {
	Latency      float64 `json:"latency,omitempty"`
	Throughput   float64 `json:"throughput,omitempty"`
	Availability float64 `json:"availability,omitempty"`
	Reliability  float64 `json:"reliability,omitempty"`
}

// EvaluationOptions represents options for evaluation
type EvaluationOptions struct {
	PolicyIDs          []string      `json:"policyIds,omitempty"`
	Force              bool          `json:"force,omitempty"`
	Timeout            time.Duration `json:"timeout,omitempty"`
	MaxPolicies        int           `json:"maxPolicies,omitempty"`
	IncludeInactive    bool          `json:"includeInactive,omitempty"`
	ConflictResolution string        `json:"conflictResolution,omitempty"`
	Debug              bool          `json:"debug,omitempty"`
	Metrics            bool          `json:"metrics,omitempty"`
}

// EvaluationResult represents the result of policy evaluation
type EvaluationResult struct {
	PolicyID        string                  `json:"policyId"`
	PolicyName      string                  `json:"policyName"`
	PolicyType      types.PolicyType        `json:"policyType"`
	Applicable      bool                    `json:"applicable"`
	Score           float64                 `json:"score"`
	Violations      []*types.Violation      `json:"violations,omitempty"`
	Recommendations []*types.Recommendation `json:"recommendations,omitempty"`
	Constraints     []*types.Constraint     `json:"constraints,omitempty"`
	Metrics         map[string]interface{}  `json:"metrics,omitempty"`
	Duration        time.Duration           `json:"duration"`
	Timestamp       time.Time               `json:"timestamp"`
}

// EvaluationEngine represents the main evaluation engine
type EvaluationEngine interface {
	// EvaluateWorkload evaluates a workload against all applicable policies
	EvaluateWorkload(ctx context.Context, workload *types.Workload, options *EvaluationOptions) ([]*types.EvaluationResult, error)

	// EvaluateWithContext evaluates with additional context
	EvaluateWithContext(ctx context.Context, evalCtx *EvaluationContext, options *EvaluationOptions) ([]*types.EvaluationResult, error)

	// GetRecommendedDecision gets the recommended decision based on evaluation results
	GetRecommendedDecision(ctx context.Context, results []*types.EvaluationResult) (*types.Decision, error)

	// GetMetrics returns evaluation engine metrics
	GetMetrics(ctx context.Context) (map[string]interface{}, error)

	// Health checks the health of the evaluation engine
	Health(ctx context.Context) error
}
