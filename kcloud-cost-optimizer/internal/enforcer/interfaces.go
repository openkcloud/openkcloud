package enforcer

import (
	"context"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// PolicyEnforcer defines the interface for policy enforcement
type PolicyEnforcer interface {
	// Enforce enforces a policy decision
	Enforce(ctx context.Context, decision *types.Decision) error

	// EnforceMany enforces multiple policy decisions
	EnforceMany(ctx context.Context, decisions []*types.Decision) error

	// GetEnforcementStatus gets the status of policy enforcement
	GetEnforcementStatus(ctx context.Context, decisionID string) (*EnforcementStatus, error)

	// CancelEnforcement cancels ongoing policy enforcement
	CancelEnforcement(ctx context.Context, decisionID string) error

	// Health checks the health of the enforcer
	Health(ctx context.Context) error
}

// EnforcementStatus represents the status of policy enforcement
type EnforcementStatus struct {
	DecisionID  string                 `json:"decisionId"`
	Status      EnforcementState       `json:"status"`
	Progress    float64                `json:"progress"`
	Message     string                 `json:"message"`
	StartedAt   time.Time              `json:"startedAt"`
	CompletedAt *time.Time             `json:"completedAt,omitempty"`
	Duration    *time.Duration         `json:"duration,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty"`
	Events      []EnforcementEvent     `json:"events,omitempty"`
}

// EnforcementState represents the state of enforcement
type EnforcementState string

const (
	EnforcementStatePending   EnforcementState = "pending"
	EnforcementStateRunning   EnforcementState = "running"
	EnforcementStateCompleted EnforcementState = "completed"
	EnforcementStateFailed    EnforcementState = "failed"
	EnforcementStateCancelled EnforcementState = "cancelled"
	EnforcementStateTimeout   EnforcementState = "timeout"
)

// EnforcementEvent represents an enforcement event
type EnforcementEvent struct {
	Type      string                 `json:"type"`
	Message   string                 `json:"message"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// ActionExecutor defines the interface for executing specific actions
type ActionExecutor interface {
	// CanExecute checks if this executor can handle the given action type
	CanExecute(actionType string) bool

	// Execute executes the action
	Execute(ctx context.Context, action *Action) (*ActionResult, error)

	// Validate validates the action before execution
	Validate(action *Action) error

	// Health checks the health of the executor
	Health(ctx context.Context) error
}

// Action represents an action to be executed
type Action struct {
	Type        string                 `json:"type"`
	Target      string                 `json:"target"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Timeout     time.Duration          `json:"timeout,omitempty"`
	RetryPolicy *RetryPolicy           `json:"retryPolicy,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// ActionResult represents the result of action execution
type ActionResult struct {
	ActionType string                 `json:"actionType"`
	Success    bool                   `json:"success"`
	Message    string                 `json:"message"`
	Data       map[string]interface{} `json:"data,omitempty"`
	Duration   time.Duration          `json:"duration"`
	Timestamp  time.Time              `json:"timestamp"`
	Error      string                 `json:"error,omitempty"`
	RetryCount int                    `json:"retryCount"`
}

// RetryPolicy defines retry behavior for actions
type RetryPolicy struct {
	MaxRetries int           `json:"maxRetries"`
	Interval   time.Duration `json:"interval"`
	Backoff    BackoffType   `json:"backoff"`
}

// BackoffType represents the backoff strategy
type BackoffType string

const (
	BackoffTypeLinear      BackoffType = "linear"
	BackoffTypeExponential BackoffType = "exponential"
	BackoffTypeFixed       BackoffType = "fixed"
)

// EnforcementEngine defines the main enforcement engine interface
type EnforcementEngine interface {
	// RegisterExecutor registers an action executor
	RegisterExecutor(executor ActionExecutor) error

	// UnregisterExecutor unregisters an action executor
	UnregisterExecutor(actionType string) error

	// ExecuteAction executes a single action
	ExecuteAction(ctx context.Context, action *Action) (*ActionResult, error)

	// ExecuteActions executes multiple actions
	ExecuteActions(ctx context.Context, actions []*Action) ([]*ActionResult, error)

	// Health checks the health of the enforcement engine
	Health(ctx context.Context) error
}

// Common action types
const (
	ActionTypeSchedule   = "schedule"
	ActionTypeReschedule = "reschedule"
	ActionTypeMigrate    = "migrate"
	ActionTypeScale      = "scale"
	ActionTypeTerminate  = "terminate"
	ActionTypeSuspend    = "suspend"
	ActionTypeResume     = "resume"
	ActionTypeNotify     = "notify"
	ActionTypeUpdate     = "update"
	ActionTypeDelete     = "delete"
	ActionTypeCreate     = "create"
)

// Action execution context
type ActionContext struct {
	Decision    *types.Decision        `json:"decision"`
	Workload    *types.Workload        `json:"workload"`
	ClusterInfo *ClusterInfo           `json:"clusterInfo,omitempty"`
	NodeInfo    *NodeInfo              `json:"nodeInfo,omitempty"`
	Environment map[string]interface{} `json:"environment,omitempty"`
	UserID      string                 `json:"userId,omitempty"`
	RequestID   string                 `json:"requestId,omitempty"`
}

// ClusterInfo represents cluster information for enforcement
type ClusterInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// NodeInfo represents node information for enforcement
type NodeInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ClusterID   string            `json:"clusterId"`
	Status      string            `json:"status"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}
