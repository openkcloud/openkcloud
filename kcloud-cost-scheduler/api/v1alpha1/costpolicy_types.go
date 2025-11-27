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

// CostPolicySpec defines the desired state of CostPolicy
type CostPolicySpec struct {
	// BudgetLimit defines the total budget limit in USD
	// +kubebuilder:validation:Minimum=0
	// +required
	BudgetLimit float64 `json:"budgetLimit"`

	// MonthlyBudget defines the monthly budget limit in USD
	// +kubebuilder:validation:Minimum=0
	// +optional
	MonthlyBudget *float64 `json:"monthlyBudget,omitempty"`

	// CostPerHourLimit defines the maximum cost per hour in USD
	// +kubebuilder:validation:Minimum=0
	// +optional
	CostPerHourLimit *float64 `json:"costPerHourLimit,omitempty"`

	// SpotInstancePolicy defines the policy for spot instances
	// +optional
	SpotInstancePolicy *SpotInstancePolicy `json:"spotInstancePolicy,omitempty"`

	// ResourceCostPolicy defines cost policies for different resource types
	// +optional
	ResourceCostPolicy *ResourceCostPolicy `json:"resourceCostPolicy,omitempty"`

	// AlertThresholds defines thresholds for cost alerts
	// +optional
	AlertThresholds []CostAlertThreshold `json:"alertThresholds,omitempty"`

	// NamespaceSelector defines which namespaces this policy applies to
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// WorkloadSelector defines which workloads this policy applies to
	// +optional
	WorkloadSelector *metav1.LabelSelector `json:"workloadSelector,omitempty"`
}

// SpotInstancePolicy defines the policy for spot instances
type SpotInstancePolicy struct {
	// Enabled indicates whether spot instances are allowed
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// MaxSpotPercentage defines the maximum percentage of spot instances (0-100)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	MaxSpotPercentage *int32 `json:"maxSpotPercentage,omitempty"`

	// InterruptionHandling defines how to handle spot instance interruptions
	// +kubebuilder:validation:Enum=terminate;drain;migrate
	// +optional
	InterruptionHandling string `json:"interruptionHandling,omitempty"`
}

// ResourceCostPolicy defines cost policies for different resource types
type ResourceCostPolicy struct {
	// CPUCostPerCorePerHour defines the cost per CPU core per hour in USD
	// +kubebuilder:validation:Minimum=0
	// +optional
	CPUCostPerCorePerHour *float64 `json:"cpuCostPerCorePerHour,omitempty"`

	// MemoryCostPerGiPerHour defines the cost per GiB of memory per hour in USD
	// +kubebuilder:validation:Minimum=0
	// +optional
	MemoryCostPerGiPerHour *float64 `json:"memoryCostPerGiPerHour,omitempty"`

	// GPUCostPerHour defines the cost per GPU per hour in USD
	// +kubebuilder:validation:Minimum=0
	// +optional
	GPUCostPerHour *float64 `json:"gpuCostPerHour,omitempty"`

	// NPUCostPerHour defines the cost per NPU per hour in USD
	// +kubebuilder:validation:Minimum=0
	// +optional
	NPUCostPerHour *float64 `json:"npuCostPerHour,omitempty"`
}

// CostAlertThreshold defines a threshold for cost alerts
type CostAlertThreshold struct {
	// Type defines the type of threshold
	// +kubebuilder:validation:Enum=budget_usage;cost_increase;anomaly
	// +required
	Type string `json:"type"`

	// Value defines the threshold value (percentage for budget_usage, multiplier for cost_increase)
	// +kubebuilder:validation:Minimum=0
	// +required
	Value float64 `json:"value"`

	// Severity defines the severity level of the alert
	// +kubebuilder:validation:Enum=info;warning;critical
	// +optional
	Severity string `json:"severity,omitempty"`

	// Action defines the action to take when threshold is exceeded
	// +kubebuilder:validation:Enum=notify;scale_down;pause;terminate
	// +optional
	Action string `json:"action,omitempty"`
}

// CostPolicyStatus defines the observed state of CostPolicy
type CostPolicyStatus struct {
	// Phase represents the current phase of the cost policy
	// +kubebuilder:validation:Enum=Active;Suspended;Violated
	// +optional
	Phase string `json:"phase,omitempty"`

	// CurrentSpend represents the current spend in USD
	// +optional
	CurrentSpend *float64 `json:"currentSpend,omitempty"`

	// MonthlySpend represents the current monthly spend in USD
	// +optional
	MonthlySpend *float64 `json:"monthlySpend,omitempty"`

	// BudgetUtilization represents the budget utilization percentage (0-100)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	BudgetUtilization *float64 `json:"budgetUtilization,omitempty"`

	// LastUpdated represents the last time the status was updated
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// Violations represents the number of policy violations
	// +optional
	Violations *int32 `json:"violations,omitempty"`

	// conditions represent the current state of the CostPolicy resource
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// CostPolicy is the Schema for the costpolicies API
type CostPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of CostPolicy
	// +required
	Spec CostPolicySpec `json:"spec"`

	// status defines the observed state of CostPolicy
	// +optional
	Status CostPolicyStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// CostPolicyList contains a list of CostPolicy
type CostPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CostPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CostPolicy{}, &CostPolicyList{})
}
