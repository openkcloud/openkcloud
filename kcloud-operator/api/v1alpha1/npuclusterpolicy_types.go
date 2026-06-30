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

// ============================================================
// npuclusterpolicy_types.go: NPUClusterPolicy CRD 타입 정의
// 상세: Detector/Nvidia/Furiosa/Rebellions vendor spec 포함
// 생성일: 2025-01-01 | 수정일: 2026-04-29
// ============================================================

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type DetectorSpec struct {
	Image string `json:"image,omitempty"`
}

type FuriosaSpec struct {
	Enabled           bool              `json:"enabled"`
	DevicePluginImage string            `json:"devicePluginImage"`
	ConfigMapName     string            `json:"configMapName,omitempty"`
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
	Rngd              RngdSpec          `json:"rngd,omitempty"`
}

// RngdSpec defines the RNGD-specific (Furiosa RNGD NPU) device plugin configuration.
// Backward-compatible: all fields are omitempty; omit the whole block to keep legacy behavior.
type RngdSpec struct {
	Enabled           bool              `json:"enabled,omitempty"`
	DevicePluginImage string            `json:"devicePluginImage,omitempty"`
	ResourceName      string            `json:"resourceName,omitempty"` // default "furiosa.ai/rngd"
	ConfigMapName     string            `json:"configMapName,omitempty"`
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
	// PartitionPolicy: "none" (default, 1 instance/card), "single-core" (8), "dual-core" (4), "quad-core" (2).
	// Furiosa libfuriosa-kubernetes PartitioningPolicy 와 1:1 매핑.
	// +kubebuilder:validation:Enum=none;single-core;dual-core;quad-core
	// +optional
	PartitionPolicy string `json:"partitionPolicy,omitempty"`
}

type NvidiaSpec struct {
	Enabled           bool              `json:"enabled"`
	DevicePluginImage string            `json:"devicePluginImage"`
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
}

// RebellionsSpec defines the Rebellions ATOM+ device plugin configuration.
// Backward-compatible: all fields are omitempty; omit the whole block to keep legacy behavior.
type RebellionsSpec struct {
	Enabled           bool              `json:"enabled,omitempty"`
	DevicePluginImage string            `json:"devicePluginImage"`
	ResourceName      string            `json:"resourceName,omitempty"`   // default "ATOM"
	ResourcePrefix    string            `json:"resourcePrefix,omitempty"` // default "rebellions.ai"
	Namespace         string            `json:"namespace,omitempty"`      // default "rbln-system"
	ConfigMapName     string            `json:"configMapName,omitempty"`  // default "rbln-device-plugin-config"
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
}

// NPUClusterPolicySpec defines the desired state of NPUClusterPolicy.
type NPUClusterPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Detector   *DetectorSpec  `json:"detector,omitempty"`
	Nvidia     NvidiaSpec     `json:"nvidia"`
	Furiosa    FuriosaSpec    `json:"furiosa"`
	Rebellions RebellionsSpec `json:"rebellions,omitempty"`
}

// NPUClusterPolicyStatus defines the observed state of NPUClusterPolicy.
type NPUClusterPolicyStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NPUClusterPolicy is the Schema for the npuclusterpolicies API.
type NPUClusterPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NPUClusterPolicySpec   `json:"spec,omitempty"`
	Status NPUClusterPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NPUClusterPolicyList contains a list of NPUClusterPolicy.
type NPUClusterPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NPUClusterPolicy `json:"items"`
}
