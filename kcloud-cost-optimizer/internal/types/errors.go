package types

import (
	"errors"
	"fmt"
)

// Predefined errors for the policy engine
var (
	// Policy errors
	ErrPolicyNotFound         = errors.New("policy not found")
	ErrInvalidPolicyName      = errors.New("invalid policy name")
	ErrInvalidPolicyType      = errors.New("invalid policy type")
	ErrInvalidPriority        = errors.New("invalid priority")
	ErrPolicyAlreadyExists    = errors.New("policy already exists")
	ErrPolicyValidationFailed = errors.New("policy validation failed")
	ErrPolicyConflict         = errors.New("policy conflict detected")
	ErrPolicyInactive         = errors.New("policy is inactive")
	ErrPolicyArchived         = errors.New("policy is archived")

	// Workload errors
	ErrWorkloadNotFound         = errors.New("workload not found")
	ErrInvalidWorkloadType      = errors.New("invalid workload type")
	ErrInvalidWorkloadStatus    = errors.New("invalid workload status")
	ErrWorkloadAlreadyExists    = errors.New("workload already exists")
	ErrWorkloadValidationFailed = errors.New("workload validation failed")
	ErrInsufficientResources    = errors.New("insufficient resources")
	ErrWorkloadNotRunning       = errors.New("workload is not running")
	ErrWorkloadTerminated       = errors.New("workload is terminated")

	// Decision errors
	ErrDecisionNotFound         = errors.New("decision not found")
	ErrInvalidDecisionType      = errors.New("invalid decision type")
	ErrInvalidDecisionStatus    = errors.New("invalid decision status")
	ErrDecisionAlreadyExists    = errors.New("decision already exists")
	ErrDecisionValidationFailed = errors.New("decision validation failed")
	ErrDecisionNotApproved      = errors.New("decision is not approved")
	ErrDecisionExecuting        = errors.New("decision is already executing")
	ErrDecisionCompleted        = errors.New("decision is already completed")
	ErrDecisionFailed           = errors.New("decision execution failed")

	// Evaluation errors
	ErrEvaluationFailed       = errors.New("policy evaluation failed")
	ErrNoApplicablePolicies   = errors.New("no applicable policies found")
	ErrEvaluationTimeout      = errors.New("policy evaluation timeout")
	ErrInvalidEvaluationInput = errors.New("invalid evaluation input")
	ErrEvaluationConflict     = errors.New("evaluation conflict detected")

	// Automation errors
	ErrAutomationRuleNotFound = errors.New("automation rule not found")
	ErrInvalidRuleCondition   = errors.New("invalid rule condition")
	ErrInvalidRuleAction      = errors.New("invalid rule action")
	ErrRuleExecutionFailed    = errors.New("rule execution failed")
	ErrRuleTimeout            = errors.New("rule execution timeout")
	ErrRuleConflict           = errors.New("rule conflict detected")

	// Storage errors
	ErrStorageConnection   = errors.New("storage connection failed")
	ErrStorageOperation    = errors.New("storage operation failed")
	ErrStorageNotFound     = errors.New("storage resource not found")
	ErrStorageConflict     = errors.New("storage conflict")
	ErrStorageTimeout      = errors.New("storage operation timeout")
	ErrStorageUnauthorized = errors.New("storage unauthorized")

	// Configuration errors
	ErrConfigNotFound         = errors.New("configuration not found")
	ErrInvalidConfig          = errors.New("invalid configuration")
	ErrConfigValidationFailed = errors.New("configuration validation failed")
	ErrConfigLoadFailed       = errors.New("configuration load failed")

	// API errors
	ErrInvalidRequest      = errors.New("invalid request")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
	ErrNotFound            = errors.New("not found")
	ErrMethodNotAllowed    = errors.New("method not allowed")
	ErrRequestTimeout      = errors.New("request timeout")
	ErrTooManyRequests     = errors.New("too many requests")
	ErrInternalServerError = errors.New("internal server error")
	ErrServiceUnavailable  = errors.New("service unavailable")

	// Cluster errors
	ErrClusterNotFound         = errors.New("cluster not found")
	ErrClusterUnavailable      = errors.New("cluster unavailable")
	ErrClusterOverloaded       = errors.New("cluster overloaded")
	ErrInvalidClusterConfig    = errors.New("invalid cluster configuration")
	ErrClusterConnectionFailed = errors.New("cluster connection failed")

	// Node errors
	ErrNodeNotFound         = errors.New("node not found")
	ErrNodeUnavailable      = errors.New("node unavailable")
	ErrNodeOverloaded       = errors.New("node overloaded")
	ErrInvalidNodeConfig    = errors.New("invalid node configuration")
	ErrNodeConnectionFailed = errors.New("node connection failed")
)

// PolicyError represents a policy-specific error
type PolicyError struct {
	PolicyID   string `json:"policyId,omitempty"`
	PolicyName string `json:"policyName,omitempty"`
	PolicyType string `json:"policyType,omitempty"`
	Operation  string `json:"operation,omitempty"`
	Err        error  `json:"error"`
}

func (e *PolicyError) Error() string {
	if e.PolicyID != "" {
		return fmt.Sprintf("policy error [%s]: %v", e.PolicyID, e.Err)
	}
	if e.PolicyName != "" {
		return fmt.Sprintf("policy error [%s]: %v", e.PolicyName, e.Err)
	}
	return fmt.Sprintf("policy error: %v", e.Err)
}

func (e *PolicyError) Unwrap() error {
	return e.Err
}

// WorkloadError represents a workload-specific error
type WorkloadError struct {
	WorkloadID   string `json:"workloadId,omitempty"`
	WorkloadName string `json:"workloadName,omitempty"`
	WorkloadType string `json:"workloadType,omitempty"`
	Operation    string `json:"operation,omitempty"`
	Err          error  `json:"error"`
}

func (e *WorkloadError) Error() string {
	if e.WorkloadID != "" {
		return fmt.Sprintf("workload error [%s]: %v", e.WorkloadID, e.Err)
	}
	if e.WorkloadName != "" {
		return fmt.Sprintf("workload error [%s]: %v", e.WorkloadName, e.Err)
	}
	return fmt.Sprintf("workload error: %v", e.Err)
}

func (e *WorkloadError) Unwrap() error {
	return e.Err
}

// DecisionError represents a decision-specific error
type DecisionError struct {
	DecisionID   string `json:"decisionId,omitempty"`
	DecisionType string `json:"decisionType,omitempty"`
	WorkloadID   string `json:"workloadId,omitempty"`
	PolicyID     string `json:"policyId,omitempty"`
	Operation    string `json:"operation,omitempty"`
	Err          error  `json:"error"`
}

func (e *DecisionError) Error() string {
	if e.DecisionID != "" {
		return fmt.Sprintf("decision error [%s]: %v", e.DecisionID, e.Err)
	}
	return fmt.Sprintf("decision error: %v", e.Err)
}

func (e *DecisionError) Unwrap() error {
	return e.Err
}

// EvaluationError represents an evaluation-specific error
type EvaluationError struct {
	WorkloadID string `json:"workloadId,omitempty"`
	PolicyID   string `json:"policyId,omitempty"`
	PolicyName string `json:"policyName,omitempty"`
	Operation  string `json:"operation,omitempty"`
	Err        error  `json:"error"`
}

func (e *EvaluationError) Error() string {
	if e.WorkloadID != "" && e.PolicyID != "" {
		return fmt.Sprintf("evaluation error [workload:%s, policy:%s]: %v", e.WorkloadID, e.PolicyID, e.Err)
	}
	return fmt.Sprintf("evaluation error: %v", e.Err)
}

func (e *EvaluationError) Unwrap() error {
	return e.Err
}

// AutomationError represents an automation-specific error
type AutomationError struct {
	RuleID    string `json:"ruleId,omitempty"`
	RuleName  string `json:"ruleName,omitempty"`
	Action    string `json:"action,omitempty"`
	Condition string `json:"condition,omitempty"`
	Operation string `json:"operation,omitempty"`
	Err       error  `json:"error"`
}

func (e *AutomationError) Error() string {
	if e.RuleID != "" {
		return fmt.Sprintf("automation error [%s]: %v", e.RuleID, e.Err)
	}
	if e.RuleName != "" {
		return fmt.Sprintf("automation error [%s]: %v", e.RuleName, e.Err)
	}
	return fmt.Sprintf("automation error: %v", e.Err)
}

func (e *AutomationError) Unwrap() error {
	return e.Err
}

// StorageError represents a storage-specific error
type StorageError struct {
	Resource  string `json:"resource,omitempty"`
	Operation string `json:"operation,omitempty"`
	Err       error  `json:"error"`
}

func (e *StorageError) Error() string {
	if e.Resource != "" && e.Operation != "" {
		return fmt.Sprintf("storage error [%s:%s]: %v", e.Resource, e.Operation, e.Err)
	}
	return fmt.Sprintf("storage error: %v", e.Err)
}

func (e *StorageError) Unwrap() error {
	return e.Err
}

// ConfigError represents a configuration-specific error
type ConfigError struct {
	ConfigKey  string `json:"configKey,omitempty"`
	ConfigType string `json:"configType,omitempty"`
	Err        error  `json:"error"`
}

func (e *ConfigError) Error() string {
	if e.ConfigKey != "" {
		return fmt.Sprintf("config error [%s]: %v", e.ConfigKey, e.Err)
	}
	return fmt.Sprintf("config error: %v", e.Err)
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}

// APIError represents an API-specific error
type APIError struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Code       string `json:"code,omitempty"`
	Details    string `json:"details,omitempty"`
	Err        error  `json:"error,omitempty"`
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("API error [%d]: %s - %v", e.StatusCode, e.Message, e.Err)
	}
	return fmt.Sprintf("API error [%d]: %s", e.StatusCode, e.Message)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// ClusterError represents a cluster-specific error
type ClusterError struct {
	ClusterID   string `json:"clusterId,omitempty"`
	ClusterName string `json:"clusterName,omitempty"`
	Operation   string `json:"operation,omitempty"`
	Err         error  `json:"error"`
}

func (e *ClusterError) Error() string {
	if e.ClusterID != "" {
		return fmt.Sprintf("cluster error [%s]: %v", e.ClusterID, e.Err)
	}
	if e.ClusterName != "" {
		return fmt.Sprintf("cluster error [%s]: %v", e.ClusterName, e.Err)
	}
	return fmt.Sprintf("cluster error: %v", e.Err)
}

func (e *ClusterError) Unwrap() error {
	return e.Err
}

// NodeError represents a node-specific error
type NodeError struct {
	NodeID    string `json:"nodeId,omitempty"`
	NodeName  string `json:"nodeName,omitempty"`
	Operation string `json:"operation,omitempty"`
	Err       error  `json:"error"`
}

func (e *NodeError) Error() string {
	if e.NodeID != "" {
		return fmt.Sprintf("node error [%s]: %v", e.NodeID, e.Err)
	}
	if e.NodeName != "" {
		return fmt.Sprintf("node error [%s]: %v", e.NodeName, e.Err)
	}
	return fmt.Sprintf("node error: %v", e.Err)
}

func (e *NodeError) Unwrap() error {
	return e.Err
}

// Helper functions for creating typed errors

// NewPolicyError creates a new PolicyError
func NewPolicyError(policyID, policyName, policyType, operation string, err error) *PolicyError {
	return &PolicyError{
		PolicyID:   policyID,
		PolicyName: policyName,
		PolicyType: policyType,
		Operation:  operation,
		Err:        err,
	}
}

// NewWorkloadError creates a new WorkloadError
func NewWorkloadError(workloadID, workloadName, workloadType, operation string, err error) *WorkloadError {
	return &WorkloadError{
		WorkloadID:   workloadID,
		WorkloadName: workloadName,
		WorkloadType: workloadType,
		Operation:    operation,
		Err:          err,
	}
}

// NewDecisionError creates a new DecisionError
func NewDecisionError(decisionID, decisionType, workloadID, policyID, operation string, err error) *DecisionError {
	return &DecisionError{
		DecisionID:   decisionID,
		DecisionType: decisionType,
		WorkloadID:   workloadID,
		PolicyID:     policyID,
		Operation:    operation,
		Err:          err,
	}
}

// NewEvaluationError creates a new EvaluationError
func NewEvaluationError(workloadID, policyID, policyName, operation string, err error) *EvaluationError {
	return &EvaluationError{
		WorkloadID: workloadID,
		PolicyID:   policyID,
		PolicyName: policyName,
		Operation:  operation,
		Err:        err,
	}
}

// NewAutomationError creates a new AutomationError
func NewAutomationError(ruleID, ruleName, action, condition, operation string, err error) *AutomationError {
	return &AutomationError{
		RuleID:    ruleID,
		RuleName:  ruleName,
		Action:    action,
		Condition: condition,
		Operation: operation,
		Err:       err,
	}
}

// NewStorageError creates a new StorageError
func NewStorageError(resource, operation string, err error) *StorageError {
	return &StorageError{
		Resource:  resource,
		Operation: operation,
		Err:       err,
	}
}

// NewConfigError creates a new ConfigError
func NewConfigError(configKey, configType string, err error) *ConfigError {
	return &ConfigError{
		ConfigKey:  configKey,
		ConfigType: configType,
		Err:        err,
	}
}

// NewAPIError creates a new APIError
func NewAPIError(statusCode int, message, code, details string, err error) *APIError {
	return &APIError{
		StatusCode: statusCode,
		Message:    message,
		Code:       code,
		Details:    details,
		Err:        err,
	}
}

// NewClusterError creates a new ClusterError
func NewClusterError(clusterID, clusterName, operation string, err error) *ClusterError {
	return &ClusterError{
		ClusterID:   clusterID,
		ClusterName: clusterName,
		Operation:   operation,
		Err:         err,
	}
}

// NewNodeError creates a new NodeError
func NewNodeError(nodeID, nodeName, operation string, err error) *NodeError {
	return &NodeError{
		NodeID:    nodeID,
		NodeName:  nodeName,
		Operation: operation,
		Err:       err,
	}
}
