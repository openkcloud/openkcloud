package types

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// WorkloadType represents the type of workload
type WorkloadType string

const (
	WorkloadTypeDeployment WorkloadType = "deployment"
	WorkloadTypeMLTraining WorkloadType = "ml_training"
	WorkloadTypeInference  WorkloadType = "inference"
	WorkloadTypeBatch      WorkloadType = "batch"
	WorkloadTypeRealTime   WorkloadType = "realtime"
	WorkloadTypeWeb        WorkloadType = "web"
	WorkloadTypeDatabase   WorkloadType = "database"
	WorkloadTypeCache      WorkloadType = "cache"
	WorkloadTypeStorage    WorkloadType = "storage"
)

// WorkloadStatus represents the status of a workload
type WorkloadStatus string

const (
	WorkloadStatusPending   WorkloadStatus = "pending"
	WorkloadStatusRunning   WorkloadStatus = "running"
	WorkloadStatusCompleted WorkloadStatus = "completed"
	WorkloadStatusFailed    WorkloadStatus = "failed"
	WorkloadStatusCancelled WorkloadStatus = "cancelled"
	WorkloadStatusSuspended WorkloadStatus = "suspended"
)

// Workload represents a workload in the system
type Workload struct {
	ID           string               `json:"id" yaml:"id"`
	Name         string               `json:"name" yaml:"name"`
	Type         WorkloadType         `json:"type" yaml:"type"`
	Status       WorkloadStatus       `json:"status" yaml:"status"`
	Priority     Priority             `json:"priority" yaml:"priority"`
	Labels       map[string]string    `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations  map[string]string    `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Requirements Resources            `json:"requirements" yaml:"requirements"`
	Constraints  *WorkloadConstraints `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Metadata     WorkloadMetadata     `json:"metadata" yaml:"metadata"`
	CreatedAt    time.Time            `json:"createdAt" yaml:"createdAt"`
	UpdatedAt    time.Time            `json:"updatedAt" yaml:"updatedAt"`
}

// Requirements represents resource requirements
type Requirements struct {
	CPU     string `json:"cpu" yaml:"cpu"`
	Memory  string `json:"memory" yaml:"memory"`
	Storage string `json:"storage,omitempty" yaml:"storage,omitempty"`
	GPU     string `json:"gpu,omitempty" yaml:"gpu,omitempty"`
	NPU     string `json:"npu,omitempty" yaml:"npu,omitempty"`
}

// Resources represents workload resource requirements
type Resources struct {
	CPU     int                  `json:"cpu" yaml:"cpu"`
	Memory  string               `json:"memory" yaml:"memory"`
	Storage string               `json:"storage,omitempty" yaml:"storage,omitempty"`
	GPU     *GPURequirements     `json:"gpu,omitempty" yaml:"gpu,omitempty"`
	NPU     *NPURequirements     `json:"npu,omitempty" yaml:"npu,omitempty"`
	Network *NetworkRequirements `json:"network,omitempty" yaml:"network,omitempty"`
}

// GPURequirements represents GPU requirements
type GPURequirements struct {
	Count   int    `json:"count" yaml:"count"`
	Type    string `json:"type,omitempty" yaml:"type,omitempty"`
	Memory  string `json:"memory,omitempty" yaml:"memory,omitempty"`
	Compute string `json:"compute,omitempty" yaml:"compute,omitempty"`
}

// NPURequirements represents NPU requirements
type NPURequirements struct {
	Count     int    `json:"count" yaml:"count"`
	Type      string `json:"type,omitempty" yaml:"type,omitempty"`
	Memory    string `json:"memory,omitempty" yaml:"memory,omitempty"`
	Precision string `json:"precision,omitempty" yaml:"precision,omitempty"`
}

// NetworkRequirements represents network requirements
type NetworkRequirements struct {
	Bandwidth string   `json:"bandwidth,omitempty" yaml:"bandwidth,omitempty"`
	Latency   string   `json:"latency,omitempty" yaml:"latency,omitempty"`
	Protocols []string `json:"protocols,omitempty" yaml:"protocols,omitempty"`
}

// WorkloadConstraints represents workload constraints
type WorkloadConstraints struct {
	MaxCostPerHour    float64           `json:"maxCostPerHour,omitempty" yaml:"maxCostPerHour,omitempty"`
	MaxExecutionTime  string            `json:"maxExecutionTime,omitempty" yaml:"maxExecutionTime,omitempty"`
	PreferredClusters []string          `json:"preferredClusters,omitempty" yaml:"preferredClusters,omitempty"`
	ForbiddenClusters []string          `json:"forbiddenClusters,omitempty" yaml:"forbiddenClusters,omitempty"`
	NodeSelectors     map[string]string `json:"nodeSelectors,omitempty" yaml:"nodeSelectors,omitempty"`
	Tolerations       []Toleration      `json:"tolerations,omitempty" yaml:"tolerations,omitempty"`
	Affinity          *Affinity         `json:"affinity,omitempty" yaml:"affinity,omitempty"`
}

// Toleration represents a workload toleration
type Toleration struct {
	Key      string `json:"key" yaml:"key"`
	Operator string `json:"operator" yaml:"operator"`
	Value    string `json:"value,omitempty" yaml:"value,omitempty"`
	Effect   string `json:"effect" yaml:"effect"`
}

// Affinity represents workload affinity rules
type Affinity struct {
	NodeAffinity    *NodeAffinity    `json:"nodeAffinity,omitempty" yaml:"nodeAffinity,omitempty"`
	PodAffinity     *PodAffinity     `json:"podAffinity,omitempty" yaml:"podAffinity,omitempty"`
	PodAntiAffinity *PodAntiAffinity `json:"podAntiAffinity,omitempty" yaml:"podAntiAffinity,omitempty"`
}

// NodeAffinity represents node affinity rules
type NodeAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution  *NodeSelector             `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty" yaml:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
	PreferredDuringSchedulingIgnoredDuringExecution []PreferredSchedulingTerm `json:"preferredDuringSchedulingIgnoredDuringExecution,omitempty" yaml:"preferredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// PodAffinity represents pod affinity rules
type PodAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution  []PodAffinityTerm         `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty" yaml:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
	PreferredDuringSchedulingIgnoredDuringExecution []WeightedPodAffinityTerm `json:"preferredDuringSchedulingIgnoredDuringExecution,omitempty" yaml:"preferredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// PodAntiAffinity represents pod anti-affinity rules
type PodAntiAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution  []PodAffinityTerm         `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty" yaml:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
	PreferredDuringSchedulingIgnoredDuringExecution []WeightedPodAffinityTerm `json:"preferredDuringSchedulingIgnoredDuringExecution,omitempty" yaml:"preferredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// NodeSelector represents node selector requirements
type NodeSelector struct {
	NodeSelectorTerms []NodeSelectorTerm `json:"nodeSelectorTerms" yaml:"nodeSelectorTerms"`
}

// NodeSelectorTerm represents a node selector term
type NodeSelectorTerm struct {
	MatchExpressions []NodeSelectorRequirement `json:"matchExpressions,omitempty" yaml:"matchExpressions,omitempty"`
	MatchFields      []NodeSelectorRequirement `json:"matchFields,omitempty" yaml:"matchFields,omitempty"`
}

// NodeSelectorRequirement represents a node selector requirement
type NodeSelectorRequirement struct {
	Key      string   `json:"key" yaml:"key"`
	Operator string   `json:"operator" yaml:"operator"`
	Values   []string `json:"values,omitempty" yaml:"values,omitempty"`
}

// PreferredSchedulingTerm represents a preferred scheduling term
type PreferredSchedulingTerm struct {
	Weight     int              `json:"weight" yaml:"weight"`
	Preference NodeSelectorTerm `json:"preference" yaml:"preference"`
}

// PodAffinityTerm represents a pod affinity term
type PodAffinityTerm struct {
	LabelSelector *LabelSelector `json:"labelSelector,omitempty" yaml:"labelSelector,omitempty"`
	Namespaces    []string       `json:"namespaces,omitempty" yaml:"namespaces,omitempty"`
	TopologyKey   string         `json:"topologyKey" yaml:"topologyKey"`
}

// WeightedPodAffinityTerm represents a weighted pod affinity term
type WeightedPodAffinityTerm struct {
	Weight          int             `json:"weight" yaml:"weight"`
	PodAffinityTerm PodAffinityTerm `json:"podAffinityTerm" yaml:"podAffinityTerm"`
}

// LabelSelector represents a label selector
type LabelSelector struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty" yaml:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty" yaml:"matchExpressions,omitempty"`
}

// LabelSelectorRequirement represents a label selector requirement
type LabelSelectorRequirement struct {
	Key      string   `json:"key" yaml:"key"`
	Operator string   `json:"operator" yaml:"operator"`
	Values   []string `json:"values,omitempty" yaml:"values,omitempty"`
}

// WorkloadMetadata represents workload metadata
type WorkloadMetadata struct {
	Namespace    string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Owner        string            `json:"owner,omitempty" yaml:"owner,omitempty"`
	Team         string            `json:"team,omitempty" yaml:"team,omitempty"`
	Project      string            `json:"project,omitempty" yaml:"project,omitempty"`
	Environment  string            `json:"environment,omitempty" yaml:"environment,omitempty"`
	CostCenter   string            `json:"costCenter,omitempty" yaml:"costCenter,omitempty"`
	Tags         []string          `json:"tags,omitempty" yaml:"tags,omitempty"`
	CustomFields map[string]string `json:"customFields,omitempty" yaml:"customFields,omitempty"`
}

// WorkloadMetrics represents workload metrics
type WorkloadMetrics struct {
	WorkloadID   string    `json:"workloadId" yaml:"workloadId"`
	CPUUsage     float64   `json:"cpuUsage" yaml:"cpuUsage"`
	MemoryUsage  float64   `json:"memoryUsage" yaml:"memoryUsage"`
	GPUUsage     float64   `json:"gpuUsage,omitempty" yaml:"gpuUsage,omitempty"`
	NPUUsage     float64   `json:"npuUsage,omitempty" yaml:"npuUsage,omitempty"`
	NetworkUsage float64   `json:"networkUsage,omitempty" yaml:"networkUsage,omitempty"`
	CostPerHour  float64   `json:"costPerHour" yaml:"costPerHour"`
	PowerUsage   float64   `json:"powerUsage,omitempty" yaml:"powerUsage,omitempty"`
	Latency      float64   `json:"latency,omitempty" yaml:"latency,omitempty"`
	Throughput   float64   `json:"throughput,omitempty" yaml:"throughput,omitempty"`
	ErrorRate    float64   `json:"errorRate,omitempty" yaml:"errorRate,omitempty"`
	Timestamp    time.Time `json:"timestamp" yaml:"timestamp"`
}

// WorkloadHistory represents workload execution history
type WorkloadHistory struct {
	WorkloadID    string          `json:"workloadId" yaml:"workloadId"`
	Status        WorkloadStatus  `json:"status" yaml:"status"`
	ClusterID     string          `json:"clusterId,omitempty" yaml:"clusterId,omitempty"`
	NodeID        string          `json:"nodeId,omitempty" yaml:"nodeId,omitempty"`
	StartTime     time.Time       `json:"startTime" yaml:"startTime"`
	EndTime       *time.Time      `json:"endTime,omitempty" yaml:"endTime,omitempty"`
	Duration      time.Duration   `json:"duration,omitempty" yaml:"duration,omitempty"`
	Cost          float64         `json:"cost" yaml:"cost"`
	PowerConsumed float64         `json:"powerConsumed,omitempty" yaml:"powerConsumed,omitempty"`
	Reason        string          `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message       string          `json:"message,omitempty" yaml:"message,omitempty"`
	Events        []WorkloadEvent `json:"events,omitempty" yaml:"events,omitempty"`
}

// WorkloadEvent represents a workload event
type WorkloadEvent struct {
	Type      string                 `json:"type" yaml:"type"`
	Reason    string                 `json:"reason" yaml:"reason"`
	Message   string                 `json:"message" yaml:"message"`
	Timestamp time.Time              `json:"timestamp" yaml:"timestamp"`
	Source    string                 `json:"source,omitempty" yaml:"source,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty" yaml:"data,omitempty"`
}

// Helper methods for Workload

// IsRunning returns true if the workload is currently running
func (w *Workload) IsRunning() bool {
	return w.Status == WorkloadStatusRunning
}

// IsCompleted returns true if the workload has completed
func (w *Workload) IsCompleted() bool {
	return w.Status == WorkloadStatusCompleted
}

// IsFailed returns true if the workload has failed
func (w *Workload) IsFailed() bool {
	return w.Status == WorkloadStatusFailed
}

// IsTerminated returns true if the workload has terminated (completed, failed, or cancelled)
func (w *Workload) IsTerminated() bool {
	return w.Status == WorkloadStatusCompleted ||
		w.Status == WorkloadStatusFailed ||
		w.Status == WorkloadStatusCancelled
}

// GetTotalResources returns the total resource requirements as a string
func (w *Workload) GetTotalResources() string {
	resources := w.Requirements
	result := ""

	if resources.CPU > 0 {
		result += fmt.Sprintf("CPU:%d ", resources.CPU)
	}
	if resources.Memory != "" {
		result += fmt.Sprintf("Memory:%s ", resources.Memory)
	}
	if resources.GPU != nil && resources.GPU.Count > 0 {
		result += fmt.Sprintf("GPU:%d ", resources.GPU.Count)
	}
	if resources.NPU != nil && resources.NPU.Count > 0 {
		result += fmt.Sprintf("NPU:%d ", resources.NPU.Count)
	}

	return strings.TrimSpace(result)
}

// MatchesPattern returns true if the workload name matches the given pattern
func (w *Workload) MatchesPattern(pattern string) bool {
	// Simple pattern matching - can be extended with regex
	if pattern == "" {
		return false
	}

	// Wildcard matching
	if strings.Contains(pattern, "*") {
		// Convert pattern to regex
		regexPattern := strings.ReplaceAll(pattern, "*", ".*")
		matched, _ := regexp.MatchString(regexPattern, w.Name)
		return matched
	}

	// Exact match
	return w.Name == pattern
}

// HasLabel returns true if the workload has the specified label
func (w *Workload) HasLabel(key, value string) bool {
	if w.Labels == nil {
		return false
	}
	return w.Labels[key] == value
}

// HasAnnotation returns true if the workload has the specified annotation
func (w *Workload) HasAnnotation(key, value string) bool {
	if w.Annotations == nil {
		return false
	}
	return w.Annotations[key] == value
}
