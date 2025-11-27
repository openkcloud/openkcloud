package types

import (
	"time"
)

// Logger interface for logging operations
type Logger interface {
	Info(msg string, fields ...interface{})
	Warn(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
	Debug(msg string, fields ...interface{})
	Fatal(msg string, fields ...interface{})
	WithError(err error) Logger
	WithDuration(duration time.Duration) Logger
	WithPolicy(policyID, policyName string) Logger
	WithWorkload(workloadID, workloadType string) Logger
	WithEvaluation(evaluationID string) Logger
}

// PolicyType represents the type of policy
type PolicyType string

const (
	PolicyTypeCostOptimization PolicyType = "CostOptimizationPolicy"
	PolicyTypeAutomation       PolicyType = "AutomationRule"
	PolicyTypeWorkloadPriority PolicyType = "WorkloadPriorityPolicy"
	PolicyTypeResourceQuota    PolicyType = "ResourceQuotaPolicy"
	PolicyTypeSLA              PolicyType = "SLAPolicy"
	PolicyTypeSecurity         PolicyType = "SecurityPolicy"
)

// PolicyStatus represents the status of a policy
type PolicyStatus string

const (
	PolicyStatusActive   PolicyStatus = "active"
	PolicyStatusInactive PolicyStatus = "inactive"
	PolicyStatusDraft    PolicyStatus = "draft"
	PolicyStatusArchived PolicyStatus = "archived"
)

// Priority represents policy priority
type Priority int

const (
	PriorityLow      Priority = 10
	PriorityNormal   Priority = 100
	PriorityHigh     Priority = 500
	PriorityCritical Priority = 1000
)

// BasePolicy represents the base policy structure
type BasePolicy struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       PolicyType     `json:"kind" yaml:"kind"`
	Metadata   PolicyMetadata `json:"metadata" yaml:"metadata"`
	Spec       interface{}    `json:"spec" yaml:"spec"`
	Status     PolicyStatus   `json:"status" yaml:"status"`
}

// PolicyMetadata contains policy metadata
type PolicyMetadata struct {
	Name              string            `json:"name" yaml:"name"`
	Namespace         string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Type              PolicyType        `json:"type" yaml:"type"`
	Status            PolicyStatus      `json:"status" yaml:"status"`
	Priority          Priority          `json:"priority" yaml:"priority"`
	Labels            map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	CreationTimestamp time.Time         `json:"creationTimestamp" yaml:"creationTimestamp"`
	LastModified      time.Time         `json:"lastModified" yaml:"lastModified"`
	Version           string            `json:"version" yaml:"version"`
}

// PolicySpec represents the specification of a policy
type PolicySpec struct {
	Type        PolicyType             `json:"type" yaml:"type"`
	Description string                 `json:"description" yaml:"description"`
	Objectives  []Objective            `json:"objectives,omitempty" yaml:"objectives,omitempty"`
	Target      PolicyTarget           `json:"target,omitempty" yaml:"target,omitempty"`
	Rules       []Rule                 `json:"rules" yaml:"rules"`
	Targets     []Target               `json:"targets,omitempty" yaml:"targets,omitempty"`
	Conditions  []Condition            `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Actions     []Action               `json:"actions,omitempty" yaml:"actions,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

// Objective represents a policy objective
type Objective struct {
	Name       string                 `json:"name" yaml:"name"`
	Type       string                 `json:"type" yaml:"type"`
	Target     *string                `json:"target,omitempty" yaml:"target,omitempty"`
	Priority   int                    `json:"priority" yaml:"priority"`
	Parameters map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

// PolicyTarget represents the target of a policy
type PolicyTarget struct {
	Type      string            `json:"type" yaml:"type"`
	Selector  map[string]string `json:"selector" yaml:"selector"`
	Namespace string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

// Rule represents a policy rule
type Rule struct {
	ID          string                 `json:"id" yaml:"id"`
	Name        string                 `json:"name" yaml:"name"`
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Condition   string                 `json:"condition" yaml:"condition"`
	Action      string                 `json:"action" yaml:"action"`
	Priority    int                    `json:"priority" yaml:"priority"`
	Enabled     bool                   `json:"enabled" yaml:"enabled"`
	Parameters  map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

// Target represents a policy target
type Target struct {
	Type      string            `json:"type" yaml:"type"`
	Selector  map[string]string `json:"selector" yaml:"selector"`
	Namespace string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

// Condition represents a policy condition
type Condition struct {
	Type      string                 `json:"type" yaml:"type"`
	Operator  string                 `json:"operator" yaml:"operator"`
	Value     interface{}            `json:"value" yaml:"value"`
	Threshold interface{}            `json:"threshold,omitempty" yaml:"threshold,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Action represents a policy action
type Action struct {
	Type       string                 `json:"type" yaml:"type"`
	Name       string                 `json:"name" yaml:"name"`
	Parameters map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

// CostOptimizationPolicy represents cost optimization policy
type CostOptimizationPolicy struct {
	APIVersion string               `json:"apiVersion" yaml:"apiVersion"`
	Kind       PolicyType           `json:"kind" yaml:"kind"`
	Metadata   PolicyMetadata       `json:"metadata" yaml:"metadata"`
	Spec       CostOptimizationSpec `json:"spec" yaml:"spec"`
	Status     PolicyStatus         `json:"status" yaml:"status"`
}

// CostOptimizationSpec defines cost optimization policy specification
type CostOptimizationSpec struct {
	Priority         Priority                `json:"priority" yaml:"priority"`
	Objectives       []OptimizationObjective `json:"objectives" yaml:"objectives"`
	Constraints      Constraints             `json:"constraints" yaml:"constraints"`
	WorkloadPolicies []WorkloadPolicy        `json:"workloadPolicies" yaml:"workloadPolicies"`
	Automation       []AutomationRule        `json:"automation,omitempty" yaml:"automation,omitempty"`
}

// OptimizationObjective represents a cost optimization objective
type OptimizationObjective struct {
	Type   string  `json:"type" yaml:"type"`
	Weight float64 `json:"weight" yaml:"weight"`
	Target *string `json:"target,omitempty" yaml:"target,omitempty"`
}

// Constraints defines policy constraints
type Constraints struct {
	MaxCostPerHour       float64 `json:"maxCostPerHour,omitempty" yaml:"maxCostPerHour,omitempty"`
	MaxPowerUsage        int     `json:"maxPowerUsage,omitempty" yaml:"maxPowerUsage,omitempty"`
	MinEfficiencyRatio   float64 `json:"minEfficiencyRatio,omitempty" yaml:"minEfficiencyRatio,omitempty"`
	MaxLatencyMs         int     `json:"maxLatencyMs,omitempty" yaml:"maxLatencyMs,omitempty"`
	MinAvailabilityRatio float64 `json:"minAvailabilityRatio,omitempty" yaml:"minAvailabilityRatio,omitempty"`
}

// WorkloadPolicy defines workload-specific policies
type WorkloadPolicy struct {
	Type               string     `json:"type" yaml:"type"`
	PreferredCluster   string     `json:"preferredCluster,omitempty" yaml:"preferredCluster,omitempty"`
	MaxCostPerHour     float64    `json:"maxCostPerHour,omitempty" yaml:"maxCostPerHour,omitempty"`
	AllowSpotInstances bool       `json:"allowSpotInstances,omitempty" yaml:"allowSpotInstances,omitempty"`
	AutoScale          bool       `json:"autoScale,omitempty" yaml:"autoScale,omitempty"`
	MaxLatencyMs       int        `json:"maxLatencyMs,omitempty" yaml:"maxLatencyMs,omitempty"`
	Requirements       *Resources `json:"requirements,omitempty" yaml:"requirements,omitempty"`
}

// AutomationRule represents an automation rule
type AutomationRule struct {
	Trigger    string   `json:"trigger" yaml:"trigger"`
	Action     string   `json:"action" yaml:"action"`
	Delay      *string  `json:"delay,omitempty" yaml:"delay,omitempty"`
	Immediate  bool     `json:"immediate,omitempty" yaml:"immediate,omitempty"`
	Conditions []string `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// AutomationRulePolicy represents a standalone automation rule policy
type AutomationRulePolicy struct {
	APIVersion string             `json:"apiVersion" yaml:"apiVersion"`
	Kind       PolicyType         `json:"kind" yaml:"kind"`
	Metadata   PolicyMetadata     `json:"metadata" yaml:"metadata"`
	Spec       AutomationRuleSpec `json:"spec" yaml:"spec"`
	Status     PolicyStatus       `json:"status" yaml:"status"`
}

// AutomationRuleSpec defines automation rule specification
type AutomationRuleSpec struct {
	Priority   Priority              `json:"priority" yaml:"priority"`
	Conditions []AutomationCondition `json:"conditions" yaml:"conditions"`
	Actions    []AutomationAction    `json:"actions" yaml:"actions"`
	Exceptions []Exception           `json:"exceptions,omitempty" yaml:"exceptions,omitempty"`
	Schedule   *Schedule             `json:"schedule,omitempty" yaml:"schedule,omitempty"`
}

// AutomationCondition represents a condition for automation
type AutomationCondition struct {
	Field    string      `json:"field" yaml:"field"`
	Operator string      `json:"operator" yaml:"operator"`
	Value    interface{} `json:"value" yaml:"value"`
	Duration *string     `json:"duration,omitempty" yaml:"duration,omitempty"`
}

// AutomationAction represents an automation action
type AutomationAction struct {
	Type        string                 `json:"type" yaml:"type"`
	Target      string                 `json:"target,omitempty" yaml:"target,omitempty"`
	Message     string                 `json:"message,omitempty" yaml:"message,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	GracePeriod *string                `json:"gracePeriod,omitempty" yaml:"gracePeriod,omitempty"`
	ConfirmWith *string                `json:"confirmWith,omitempty" yaml:"confirmWith,omitempty"`
}

// Exception represents an exception condition
type Exception struct {
	Condition string `json:"condition" yaml:"condition"`
	Reason    string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// Schedule represents a schedule for time-based automation
type Schedule struct {
	Cron     string `json:"cron,omitempty" yaml:"cron,omitempty"`
	Interval string `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timezone string `json:"timezone,omitempty" yaml:"timezone,omitempty"`
}

// WorkloadPriorityPolicy represents workload priority policy
type WorkloadPriorityPolicy struct {
	APIVersion string               `json:"apiVersion" yaml:"apiVersion"`
	Kind       PolicyType           `json:"kind" yaml:"kind"`
	Metadata   PolicyMetadata       `json:"metadata" yaml:"metadata"`
	Spec       WorkloadPrioritySpec `json:"spec" yaml:"spec"`
	Status     PolicyStatus         `json:"status" yaml:"status"`
}

// WorkloadPrioritySpec defines workload priority specification
type WorkloadPrioritySpec struct {
	PriorityClasses []PriorityClass   `json:"priorityClasses" yaml:"priorityClasses"`
	WorkloadMapping []WorkloadMapping `json:"workloadMapping" yaml:"workloadMapping"`
	DefaultClass    string            `json:"defaultClass,omitempty" yaml:"defaultClass,omitempty"`
}

// PriorityClass represents a priority class
type PriorityClass struct {
	Name             string `json:"name" yaml:"name"`
	Value            int    `json:"value" yaml:"value"`
	PreemptionPolicy string `json:"preemptionPolicy,omitempty" yaml:"preemptionPolicy,omitempty"`
	GlobalDefault    bool   `json:"globalDefault,omitempty" yaml:"globalDefault,omitempty"`
	Description      string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkloadMapping maps workload patterns to priority classes
type WorkloadMapping struct {
	Pattern       string `json:"pattern" yaml:"pattern"`
	PriorityClass string `json:"priorityClass" yaml:"priorityClass"`
}

// Policy represents a generic policy interface
type Policy interface {
	GetMetadata() PolicyMetadata
	GetType() PolicyType
	GetPriority() Priority
	GetStatus() PolicyStatus
	SetStatus(status PolicyStatus)
	Validate() error
}

// Implement Policy interface for CostOptimizationPolicy
func (p *CostOptimizationPolicy) GetMetadata() PolicyMetadata {
	return p.Metadata
}

func (p *CostOptimizationPolicy) GetType() PolicyType {
	return p.Kind
}

func (p *CostOptimizationPolicy) GetPriority() Priority {
	return p.Spec.Priority
}

func (p *CostOptimizationPolicy) GetStatus() PolicyStatus {
	return p.Status
}

func (p *CostOptimizationPolicy) SetStatus(status PolicyStatus) {
	p.Status = status
}

func (p *CostOptimizationPolicy) Validate() error {
	// Basic validation logic
	if p.Metadata.Name == "" {
		return ErrInvalidPolicyName
	}
	if p.Spec.Priority <= 0 {
		return ErrInvalidPriority
	}
	return nil
}

// Implement Policy interface for AutomationRulePolicy
func (p *AutomationRulePolicy) GetMetadata() PolicyMetadata {
	return p.Metadata
}

func (p *AutomationRulePolicy) GetType() PolicyType {
	return p.Kind
}

func (p *AutomationRulePolicy) GetPriority() Priority {
	return p.Spec.Priority
}

func (p *AutomationRulePolicy) GetStatus() PolicyStatus {
	return p.Status
}

func (p *AutomationRulePolicy) SetStatus(status PolicyStatus) {
	p.Status = status
}

func (p *AutomationRulePolicy) Validate() error {
	if p.Metadata.Name == "" {
		return ErrInvalidPolicyName
	}
	if p.Spec.Priority <= 0 {
		return ErrInvalidPriority
	}
	return nil
}

// Implement Policy interface for WorkloadPriorityPolicy
func (p *WorkloadPriorityPolicy) GetMetadata() PolicyMetadata {
	return p.Metadata
}

func (p *WorkloadPriorityPolicy) GetType() PolicyType {
	return p.Kind
}

func (p *WorkloadPriorityPolicy) GetPriority() Priority {
	// WorkloadPriorityPolicy doesn't have a direct priority field
	// Return a default priority
	return PriorityNormal
}

func (p *WorkloadPriorityPolicy) GetStatus() PolicyStatus {
	return p.Status
}

func (p *WorkloadPriorityPolicy) SetStatus(status PolicyStatus) {
	p.Status = status
}

func (p *WorkloadPriorityPolicy) Validate() error {
	if p.Metadata.Name == "" {
		return ErrInvalidPolicyName
	}
	return nil
}
