package types

import (
	"time"
)

// DecisionType represents the type of decision
type DecisionType string

const (
	DecisionTypeSchedule    DecisionType = "schedule"
	DecisionTypeReschedule  DecisionType = "reschedule"
	DecisionTypeMigrate     DecisionType = "migrate"
	DecisionTypeScale       DecisionType = "scale"
	DecisionTypeTerminate   DecisionType = "terminate"
	DecisionTypeSuspend     DecisionType = "suspend"
	DecisionTypeResume      DecisionType = "resume"
	DecisionTypeOptimize    DecisionType = "optimize"
	DecisionTypeConsolidate DecisionType = "consolidate"
)

// DecisionStatus represents the status of a decision
type DecisionStatus string

const (
	DecisionStatusPending   DecisionStatus = "pending"
	DecisionStatusApproved  DecisionStatus = "approved"
	DecisionStatusRejected  DecisionStatus = "rejected"
	DecisionStatusExecuting DecisionStatus = "executing"
	DecisionStatusCompleted DecisionStatus = "completed"
	DecisionStatusFailed    DecisionStatus = "failed"
	DecisionStatusCancelled DecisionStatus = "cancelled"
)

// DecisionReason represents the reason for a decision
type DecisionReason string

const (
	DecisionReasonCostOptimization        DecisionReason = "cost_optimization"
	DecisionReasonPerformanceOptimization DecisionReason = "performance_optimization"
	DecisionReasonResourceUtilization     DecisionReason = "resource_utilization"
	DecisionReasonPolicyCompliance        DecisionReason = "policy_compliance"
	DecisionReasonSLAViolation            DecisionReason = "sla_violation"
	DecisionReasonPowerOptimization       DecisionReason = "power_optimization"
	DecisionReasonAutomationRule          DecisionReason = "automation_rule"
	DecisionReasonManual                  DecisionReason = "manual"
	DecisionReasonSystemMaintenance       DecisionReason = "system_maintenance"
)

// EvaluationStatus represents the status of an evaluation
type EvaluationStatus string

const (
	EvaluationStatusPending   EvaluationStatus = "pending"
	EvaluationStatusRunning   EvaluationStatus = "running"
	EvaluationStatusCompleted EvaluationStatus = "completed"
	EvaluationStatusFailed    EvaluationStatus = "failed"
	EvaluationStatusCancelled EvaluationStatus = "cancelled"
)

// Decision represents a policy decision
type Decision struct {
	ID                 string                 `json:"id" yaml:"id"`
	Type               DecisionType           `json:"type" yaml:"type"`
	Status             DecisionStatus         `json:"status" yaml:"status"`
	Reason             DecisionReason         `json:"reason" yaml:"reason"`
	WorkloadID         string                 `json:"workloadId" yaml:"workloadId"`
	PolicyID           string                 `json:"policyId" yaml:"policyId"`
	ClusterID          string                 `json:"clusterId,omitempty" yaml:"clusterId,omitempty"`
	NodeID             string                 `json:"nodeId,omitempty" yaml:"nodeId,omitempty"`
	RecommendedCluster string                 `json:"recommendedCluster,omitempty" yaml:"recommendedCluster,omitempty"`
	RecommendedNode    string                 `json:"recommendedNode,omitempty" yaml:"recommendedNode,omitempty"`
	EstimatedCost      float64                `json:"estimatedCost,omitempty" yaml:"estimatedCost,omitempty"`
	EstimatedPower     float64                `json:"estimatedPower,omitempty" yaml:"estimatedPower,omitempty"`
	EstimatedLatency   float64                `json:"estimatedLatency,omitempty" yaml:"estimatedLatency,omitempty"`
	Confidence         float64                `json:"confidence" yaml:"confidence"`
	Score              float64                `json:"score" yaml:"score"`
	Message            string                 `json:"message,omitempty" yaml:"message,omitempty"`
	Details            map[string]interface{} `json:"details,omitempty" yaml:"details,omitempty"`
	Metadata           DecisionMetadata       `json:"metadata" yaml:"metadata"`
	CreatedAt          time.Time              `json:"createdAt" yaml:"createdAt"`
	UpdatedAt          time.Time              `json:"updatedAt" yaml:"updatedAt"`
	ExecutedAt         *time.Time             `json:"executedAt,omitempty" yaml:"executedAt,omitempty"`
}

// DecisionMetadata contains decision metadata
type DecisionMetadata struct {
	Source      string            `json:"source" yaml:"source"`
	Version     string            `json:"version" yaml:"version"`
	RequestID   string            `json:"requestId,omitempty" yaml:"requestId,omitempty"`
	UserID      string            `json:"userId,omitempty" yaml:"userId,omitempty"`
	Timestamp   time.Time         `json:"timestamp" yaml:"timestamp"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// EvaluationResult represents the result of policy evaluation
type EvaluationResult struct {
	ID              string                 `json:"id" yaml:"id"`
	PolicyID        string                 `json:"policyId" yaml:"policyId"`
	PolicyName      string                 `json:"policyName" yaml:"policyName"`
	PolicyType      PolicyType             `json:"policyType" yaml:"policyType"`
	WorkloadID      string                 `json:"workloadId" yaml:"workloadId"`
	Applicable      bool                   `json:"applicable" yaml:"applicable"`
	Score           float64                `json:"score" yaml:"score"`
	Violations      []Violation            `json:"violations,omitempty" yaml:"violations,omitempty"`
	Recommendations []Recommendation       `json:"recommendations,omitempty" yaml:"recommendations,omitempty"`
	Constraints     []Constraint           `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Metrics         map[string]interface{} `json:"metrics,omitempty" yaml:"metrics,omitempty"`
	Duration        time.Duration          `json:"duration" yaml:"duration"`
	Timestamp       time.Time              `json:"timestamp" yaml:"timestamp"`
}

// Evaluation represents a policy evaluation
type Evaluation struct {
	ID           string            `json:"id" yaml:"id"`
	PolicyID     string            `json:"policyId" yaml:"policyId"`
	WorkloadID   string            `json:"workloadId" yaml:"workloadId"`
	Status       EvaluationStatus  `json:"status" yaml:"status"`
	Result       *EvaluationResult `json:"result,omitempty" yaml:"result,omitempty"`
	StartTime    time.Time         `json:"startTime" yaml:"startTime"`
	EndTime      *time.Time        `json:"endTime,omitempty" yaml:"endTime,omitempty"`
	Duration     time.Duration     `json:"duration,omitempty" yaml:"duration,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty" yaml:"errorMessage,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Violation represents a policy violation
type Violation struct {
	Type      string                 `json:"type" yaml:"type"`
	Severity  string                 `json:"severity" yaml:"severity"`
	Message   string                 `json:"message" yaml:"message"`
	Field     string                 `json:"field,omitempty" yaml:"field,omitempty"`
	Value     interface{}            `json:"value,omitempty" yaml:"value,omitempty"`
	Expected  interface{}            `json:"expected,omitempty" yaml:"expected,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty" yaml:"details,omitempty"`
	Timestamp time.Time              `json:"timestamp" yaml:"timestamp"`
}

// Recommendation represents a policy recommendation
type Recommendation struct {
	Type      string                 `json:"type" yaml:"type"`
	Priority  string                 `json:"priority" yaml:"priority"`
	Message   string                 `json:"message" yaml:"message"`
	Action    string                 `json:"action" yaml:"action"`
	Impact    string                 `json:"impact" yaml:"impact"`
	Effort    string                 `json:"effort" yaml:"effort"`
	Details   map[string]interface{} `json:"details,omitempty" yaml:"details,omitempty"`
	Timestamp time.Time              `json:"timestamp" yaml:"timestamp"`
}

// Constraint represents a policy constraint
type Constraint struct {
	Type        string                 `json:"type" yaml:"type"`
	Name        string                 `json:"name" yaml:"name"`
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Value       interface{}            `json:"value" yaml:"value"`
	Operator    string                 `json:"operator" yaml:"operator"`
	Enforced    bool                   `json:"enforced" yaml:"enforced"`
	Details     map[string]interface{} `json:"details,omitempty" yaml:"details,omitempty"`
}

// ConflictResolution represents conflict resolution information
type ConflictResolution struct {
	ConflictingPolicies []string               `json:"conflictingPolicies" yaml:"conflictingPolicies"`
	ResolutionStrategy  string                 `json:"resolutionStrategy" yaml:"resolutionStrategy"`
	SelectedPolicy      string                 `json:"selectedPolicy" yaml:"selectedPolicy"`
	Reason              string                 `json:"reason" yaml:"reason"`
	Details             map[string]interface{} `json:"details,omitempty" yaml:"details,omitempty"`
	Timestamp           time.Time              `json:"timestamp" yaml:"timestamp"`
}

// DecisionHistory represents decision execution history
type DecisionHistory struct {
	DecisionID   string          `json:"decisionId" yaml:"decisionId"`
	WorkloadID   string          `json:"workloadId" yaml:"workloadId"`
	Action       string          `json:"action" yaml:"action"`
	Status       DecisionStatus  `json:"status" yaml:"status"`
	StartTime    time.Time       `json:"startTime" yaml:"startTime"`
	EndTime      *time.Time      `json:"endTime,omitempty" yaml:"endTime,omitempty"`
	Duration     time.Duration   `json:"duration,omitempty" yaml:"duration,omitempty"`
	Result       string          `json:"result" yaml:"result"`
	ErrorMessage string          `json:"errorMessage,omitempty" yaml:"errorMessage,omitempty"`
	Events       []DecisionEvent `json:"events,omitempty" yaml:"events,omitempty"`
}

// DecisionEvent represents a decision execution event
type DecisionEvent struct {
	Type      string                 `json:"type" yaml:"type"`
	Message   string                 `json:"message" yaml:"message"`
	Timestamp time.Time              `json:"timestamp" yaml:"timestamp"`
	Source    string                 `json:"source,omitempty" yaml:"source,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty" yaml:"data,omitempty"`
}

// Helper methods for Decision

// IsApproved returns true if the decision is approved
func (d *Decision) IsApproved() bool {
	return d.Status == DecisionStatusApproved
}

// IsPending returns true if the decision is pending
func (d *Decision) IsPending() bool {
	return d.Status == DecisionStatusPending
}

// IsExecuting returns true if the decision is being executed
func (d *Decision) IsExecuting() bool {
	return d.Status == DecisionStatusExecuting
}

// IsCompleted returns true if the decision has completed
func (d *Decision) IsCompleted() bool {
	return d.Status == DecisionStatusCompleted
}

// IsFailed returns true if the decision has failed
func (d *Decision) IsFailed() bool {
	return d.Status == DecisionStatusFailed
}

// IsTerminated returns true if the decision has terminated (completed, failed, or cancelled)
func (d *Decision) IsTerminated() bool {
	return d.Status == DecisionStatusCompleted ||
		d.Status == DecisionStatusFailed ||
		d.Status == DecisionStatusCancelled
}

// CanBeExecuted returns true if the decision can be executed
func (d *Decision) CanBeExecuted() bool {
	return d.Status == DecisionStatusApproved
}

// GetExecutionDuration returns the execution duration if completed
func (d *Decision) GetExecutionDuration() *time.Duration {
	if d.ExecutedAt != nil && d.IsCompleted() {
		duration := d.ExecutedAt.Sub(d.CreatedAt)
		return &duration
	}
	return nil
}

// AddDetail adds a detail to the decision
func (d *Decision) AddDetail(key string, value interface{}) {
	if d.Details == nil {
		d.Details = make(map[string]interface{})
	}
	d.Details[key] = value
}

// GetDetail retrieves a detail from the decision
func (d *Decision) GetDetail(key string) (interface{}, bool) {
	if d.Details == nil {
		return nil, false
	}
	value, exists := d.Details[key]
	return value, exists
}

// SetStatus updates the decision status and timestamp
func (d *Decision) SetStatus(status DecisionStatus) {
	d.Status = status
	d.UpdatedAt = time.Now()

	if status == DecisionStatusCompleted || status == DecisionStatusFailed {
		now := time.Now()
		d.ExecutedAt = &now
	}
}

// Helper methods for EvaluationResult

// HasViolations returns true if there are policy violations
func (er *EvaluationResult) HasViolations() bool {
	return len(er.Violations) > 0
}

// HasRecommendations returns true if there are recommendations
func (er *EvaluationResult) HasRecommendations() bool {
	return len(er.Recommendations) > 0
}

// GetViolationsBySeverity returns violations filtered by severity
func (er *EvaluationResult) GetViolationsBySeverity(severity string) []Violation {
	var filtered []Violation
	for _, violation := range er.Violations {
		if violation.Severity == severity {
			filtered = append(filtered, violation)
		}
	}
	return filtered
}

// GetRecommendationsByPriority returns recommendations filtered by priority
func (er *EvaluationResult) GetRecommendationsByPriority(priority string) []Recommendation {
	var filtered []Recommendation
	for _, recommendation := range er.Recommendations {
		if recommendation.Priority == priority {
			filtered = append(filtered, recommendation)
		}
	}
	return filtered
}

// AddViolation adds a violation to the evaluation result
func (er *EvaluationResult) AddViolation(violation Violation) {
	er.Violations = append(er.Violations, violation)
}

// AddRecommendation adds a recommendation to the evaluation result
func (er *EvaluationResult) AddRecommendation(recommendation Recommendation) {
	er.Recommendations = append(er.Recommendations, recommendation)
}

// AddConstraint adds a constraint to the evaluation result
func (er *EvaluationResult) AddConstraint(constraint Constraint) {
	er.Constraints = append(er.Constraints, constraint)
}

// SetMetric sets a metric value
func (er *EvaluationResult) SetMetric(key string, value interface{}) {
	if er.Metrics == nil {
		er.Metrics = make(map[string]interface{})
	}
	er.Metrics[key] = value
}

// GetMetric retrieves a metric value
func (er *EvaluationResult) GetMetric(key string) (interface{}, bool) {
	if er.Metrics == nil {
		return nil, false
	}
	value, exists := er.Metrics[key]
	return value, exists
}
