/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// WorkloadOptimizerSpec defines the desired state of WorkloadOptimizer
type WorkloadOptimizerSpec struct {
	// WorkloadType defines the type of workload (training, serving, inference, etc.)
	// +kubebuilder:validation:Enum=training;serving;inference;batch
	// +kubebuilder:validation:Required
	// +kubebuilder:default=serving
	WorkloadType string `json:"workloadType"`

	// Priority defines the priority of the workload (0-100)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// Resources defines the resource requirements for the workload
	// +required
	Resources ResourceRequirements `json:"resources"`

	// CostConstraints defines cost-related constraints and policies
	// +optional
	CostConstraints *CostConstraints `json:"costConstraints,omitempty"`

	// PowerConstraints defines power-related constraints and policies
	// +optional
	PowerConstraints *PowerConstraints `json:"powerConstraints,omitempty"`

	// PlacementPolicy defines node placement and affinity rules
	// +optional
	PlacementPolicy *PlacementPolicy `json:"placementPolicy,omitempty"`

	// AutoScaling defines auto-scaling configuration
	// +optional
	AutoScaling *AutoScalingSpec `json:"autoScaling,omitempty"`
}

// ResourceRequirements defines the resource requirements for a workload
type ResourceRequirements struct {
	// CPU resource requirement
	// +kubebuilder:validation:Pattern=^[0-9]+m?$
	// +required
	CPU string `json:"cpu"`

	// Memory resource requirement
	// +kubebuilder:validation:Pattern=^[0-9]+(E|P|T|G|M|K|Ei|Pi|Ti|Gi|Mi|Ki)?$
	// +required
	Memory string `json:"memory"`

	// GPU resource requirement
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16
	// +optional
	GPU int32 `json:"gpu,omitempty"`

	// NPU resource requirement
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16
	// +optional
	NPU int32 `json:"npu,omitempty"`
}

// CostConstraints defines cost-related constraints and policies
type CostConstraints struct {
	// MaxCostPerHour defines the maximum cost per hour in USD
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10000
	// +required
	MaxCostPerHour float64 `json:"maxCostPerHour"`

	// PreferSpot indicates whether to prefer spot instances
	// +optional
	PreferSpot bool `json:"preferSpot,omitempty"`

	// BudgetLimit defines the total budget limit in USD
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000000
	// +optional
	BudgetLimit *float64 `json:"budgetLimit,omitempty"`
}

// PowerConstraints defines power-related constraints and policies
type PowerConstraints struct {
	// MaxPowerUsage defines the maximum power usage in Watts
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10000
	// +required
	MaxPowerUsage float64 `json:"maxPowerUsage"`

	// PreferGreen indicates whether to prefer green energy sources
	// +optional
	PreferGreen bool `json:"preferGreen,omitempty"`
}

// PlacementPolicy defines node placement and affinity rules
type PlacementPolicy struct {
	// NodeSelector defines node selection criteria
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Affinity defines node affinity rules
	// +optional
	Affinity []AffinityRule `json:"affinity,omitempty"`
}

// AffinityRule defines a single affinity rule
type AffinityRule struct {
	// Type defines the type of affinity rule
	// +kubebuilder:validation:Enum=gpu_workload;cpu_intensive;memory_intensive;storage_intensive
	// +required
	Type string `json:"type"`

	// Key defines the node label key
	// +required
	Key string `json:"key"`

	// Value defines the node label value
	// +required
	Value string `json:"value"`

	// Weight defines the weight of the affinity rule (0-100)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	Weight int32 `json:"weight,omitempty"`
}

// AutoScalingSpec defines auto-scaling configuration
type AutoScalingSpec struct {
	// MinReplicas defines the minimum number of replicas
	// +kubebuilder:validation:Minimum=1
	// +required
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas defines the maximum number of replicas
	// +kubebuilder:validation:Minimum=1
	// +required
	MaxReplicas int32 `json:"maxReplicas"`

	// Metrics defines the metrics used for auto-scaling
	// +optional
	Metrics []ScalingMetric `json:"metrics,omitempty"`
}

// ScalingMetric defines a metric used for auto-scaling
type ScalingMetric struct {
	// Type defines the type of metric
	// +kubebuilder:validation:Enum=cost;power;latency;cpu;memory;gpu
	// +required
	Type string `json:"type"`

	// Threshold defines the threshold value for scaling
	// +kubebuilder:validation:Minimum=0
	// +required
	Threshold float64 `json:"threshold"`
}

// WorkloadOptimizerStatus defines the observed state of WorkloadOptimizer.
type WorkloadOptimizerStatus struct {
	// Phase represents the current phase of the workload optimization
	// +kubebuilder:validation:Enum=Pending;Optimizing;Optimized;Failed;Suspended
	// +kubebuilder:default=Pending
	// +optional
	Phase string `json:"phase,omitempty"`

	// CurrentCost represents the current cost per hour in USD
	// +optional
	CurrentCost *float64 `json:"currentCost,omitempty"`

	// CurrentPower represents the current power usage in Watts
	// +optional
	CurrentPower *float64 `json:"currentPower,omitempty"`

	// AssignedNode represents the currently assigned node
	// +optional
	AssignedNode *string `json:"assignedNode,omitempty"`

	// OptimizationScore represents the current optimization score (0.0-1.0)
	// +kubebuilder:validation:Minimum=0.0
	// +kubebuilder:validation:Maximum=1.0
	// +optional
	OptimizationScore *float64 `json:"optimizationScore,omitempty"`

	// LastOptimizationTime represents the last time optimization was performed
	// +optional
	LastOptimizationTime *metav1.Time `json:"lastOptimizationTime,omitempty"`

	// Replicas represents the current number of replicas
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// ReadyReplicas represents the number of ready replicas
	// +optional
	ReadyReplicas *int32 `json:"readyReplicas,omitempty"`

	// conditions represent the current state of the WorkloadOptimizer resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	// - "CostOptimized": the resource is within cost constraints
	// - "PowerOptimized": the resource is within power constraints
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=all
// +kubebuilder:printcolumn:name="Workload Type",type="string",JSONPath=".spec.workloadType"
// +kubebuilder:printcolumn:name="Priority",type="integer",JSONPath=".spec.priority"
// +kubebuilder:printcolumn:name="CPU",type="string",JSONPath=".spec.resources.cpu"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.resources.memory"
// +kubebuilder:printcolumn:name="GPU",type="integer",JSONPath=".spec.resources.gpu"
// +kubebuilder:printcolumn:name="NPU",type="integer",JSONPath=".spec.resources.npu"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Optimization Score",type="number",JSONPath=".status.optimizationScore"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// WorkloadOptimizer is the Schema for the workloadoptimizers API
type WorkloadOptimizer struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of WorkloadOptimizer
	// +required
	Spec WorkloadOptimizerSpec `json:"spec"`

	// status defines the observed state of WorkloadOptimizer
	// +optional
	Status WorkloadOptimizerStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=all

// WorkloadOptimizerList contains a list of WorkloadOptimizer
type WorkloadOptimizerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkloadOptimizer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkloadOptimizer{}, &WorkloadOptimizerList{})
}
