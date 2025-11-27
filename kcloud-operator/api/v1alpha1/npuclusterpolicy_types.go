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

type FuriosaSpec struct {
	Enabled           bool   `json:"enabled"`
	DevicePluginImage string `json:"devicePluginImage"`
	ConfigMapName     string `json:"configMapName,omitempty"`
}

type NvidiaSpec struct {
	Enabled           bool   `json:"enabled"`
	DevicePluginImage string `json:"devicePluginImage"`
}

// NPUClusterPolicySpec defines the desired state of NPUClusterPolicy.
type NPUClusterPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Nvidia  NvidiaSpec  `json:"nvidia"`
	Furiosa FuriosaSpec `json:"furiosa"`
}

// NPUClusterPolicyStatus defines the observed state of NPUClusterPolicy.
type NPUClusterPolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Phase string `json:"phase,omitempty"`
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

func init() {
	SchemeBuilder.Register(&NPUClusterPolicy{}, &NPUClusterPolicyList{})
}
