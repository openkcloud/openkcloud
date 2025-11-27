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

// PowerPolicySpec defines the desired state of PowerPolicy
type PowerPolicySpec struct {
	// MaxPowerUsage defines the maximum power usage in Watts
	// +kubebuilder:validation:Minimum=0
	// +required
	MaxPowerUsage float64 `json:"maxPowerUsage"`

	// PowerEfficiencyTarget defines the target power efficiency (Watts per compute unit)
	// +kubebuilder:validation:Minimum=0
	// +optional
	PowerEfficiencyTarget *float64 `json:"powerEfficiencyTarget,omitempty"`

	// GreenEnergyPolicy defines the policy for green energy usage
	// +optional
	GreenEnergyPolicy *GreenEnergyPolicy `json:"greenEnergyPolicy,omitempty"`

	// PowerSchedulingPolicy defines the policy for power-aware scheduling
	// +optional
	PowerSchedulingPolicy *PowerSchedulingPolicy `json:"powerSchedulingPolicy,omitempty"`

	// PowerAlertThresholds defines thresholds for power alerts
	// +optional
	PowerAlertThresholds []PowerAlertThreshold `json:"powerAlertThresholds,omitempty"`

	// NamespaceSelector defines which namespaces this policy applies to
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// WorkloadSelector defines which workloads this policy applies to
	// +optional
	WorkloadSelector *metav1.LabelSelector `json:"workloadSelector,omitempty"`
}

// GreenEnergyPolicy defines the policy for green energy usage
type GreenEnergyPolicy struct {
	// Enabled indicates whether green energy preference is enabled
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// MinGreenEnergyPercentage defines the minimum percentage of green energy (0-100)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	MinGreenEnergyPercentage *int32 `json:"minGreenEnergyPercentage,omitempty"`

	// CarbonFootprintTarget defines the target carbon footprint in kg CO2 per hour
	// +kubebuilder:validation:Minimum=0
	// +optional
	CarbonFootprintTarget *float64 `json:"carbonFootprintTarget,omitempty"`
}

// PowerSchedulingPolicy defines the policy for power-aware scheduling
type PowerSchedulingPolicy struct {
	// PowerAwareScheduling indicates whether power-aware scheduling is enabled
	// +optional
	PowerAwareScheduling bool `json:"powerAwareScheduling,omitempty"`

	// PeakPowerAvoidance indicates whether to avoid peak power hours
	// +optional
	PeakPowerAvoidance bool `json:"peakPowerAvoidance,omitempty"`

	// PeakPowerHours defines the peak power hours (24-hour format)
	// +optional
	PeakPowerHours []string `json:"peakPowerHours,omitempty"`

	// PowerBudgetAllocation defines how power budget is allocated across workloads
	// +kubebuilder:validation:Enum=equal;priority_based;workload_based
	// +optional
	PowerBudgetAllocation string `json:"powerBudgetAllocation,omitempty"`
}

// PowerAlertThreshold defines a threshold for power alerts
type PowerAlertThreshold struct {
	// Type defines the type of threshold
	// +kubebuilder:validation:Enum=power_usage;power_efficiency;carbon_footprint
	// +required
	Type string `json:"type"`

	// Value defines the threshold value
	// +kubebuilder:validation:Minimum=0
	// +required
	Value float64 `json:"value"`

	// Severity defines the severity level of the alert
	// +kubebuilder:validation:Enum=info;warning;critical
	// +optional
	Severity string `json:"severity,omitempty"`

	// Action defines the action to take when threshold is exceeded
	// +kubebuilder:validation:Enum=notify;scale_down;migrate;pause
	// +optional
	Action string `json:"action,omitempty"`
}

// PowerPolicyStatus defines the observed state of PowerPolicy
type PowerPolicyStatus struct {
	// Phase represents the current phase of the power policy
	// +kubebuilder:validation:Enum=Active;Suspended;Violated
	// +optional
	Phase string `json:"phase,omitempty"`

	// CurrentPowerUsage represents the current power usage in Watts
	// +optional
	CurrentPowerUsage *float64 `json:"currentPowerUsage,omitempty"`

	// PowerEfficiency represents the current power efficiency (Watts per compute unit)
	// +optional
	PowerEfficiency *float64 `json:"powerEfficiency,omitempty"`

	// GreenEnergyPercentage represents the current green energy percentage (0-100)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	GreenEnergyPercentage *float64 `json:"greenEnergyPercentage,omitempty"`

	// CarbonFootprint represents the current carbon footprint in kg CO2 per hour
	// +optional
	CarbonFootprint *float64 `json:"carbonFootprint,omitempty"`

	// LastUpdated represents the last time the status was updated
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// Violations represents the number of policy violations
	// +optional
	Violations *int32 `json:"violations,omitempty"`

	// conditions represent the current state of the PowerPolicy resource
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// PowerPolicy is the Schema for the powerpolicies API
type PowerPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of PowerPolicy
	// +required
	Spec PowerPolicySpec `json:"spec"`

	// status defines the observed state of PowerPolicy
	// +optional
	Status PowerPolicyStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// PowerPolicyList contains a list of PowerPolicy
type PowerPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PowerPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PowerPolicy{}, &PowerPolicyList{})
}
