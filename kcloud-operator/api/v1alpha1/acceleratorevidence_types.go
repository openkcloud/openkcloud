// ============================================================
// acceleratorevidence_types.go: AcceleratorEvidence CRD 타입 (검증 근거의 1급 모델)
// 상세: 노드당 하나 — "무엇을 관측했고(level), 어느 환경에서
// 나왔으며(fingerprint), 언제까지 유효한지(expiresAt)" 를 정책과 분리해 남긴다.
// 생성일: 2026-07-31
// ============================================================
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// evidence level 3종. 아래로 갈수록 강한 근거이며, 강한 level 은 약한 level 의 근거를 포함한다.
const (
	// EvidenceLevelObserved: 장치를 실제로 읽었고 NodeDeviceReport 와 어긋나지 않는다.
	EvidenceLevelObserved = "Observed"
	// EvidenceLevelFunctionallyVerified: 위에 더해 노드가 기대한 자원을 광고하고 있다.
	EvidenceLevelFunctionallyVerified = "FunctionallyVerified"
	// EvidenceLevelAllocationVerified: 위에 더해 테스트 Pod 가 그 자원을 실제로 할당받는다.
	EvidenceLevelAllocationVerified = "AllocationVerified"
)

// 검증 체크 이름. AcceleratorVerificationPolicy 의 spec.checks 값과 같은 문자열을 쓴다 —
// 정책이 고른 체크와 evidence 에 남는 체크가 같은 어휘여야 대조가 가능하다.
const (
	EvidenceCheckDeviceObservation = "DeviceObservation"
	EvidenceCheckNodeDeviceReport  = "NodeDeviceReport"
	EvidenceCheckAdvertisement     = "Advertisement"
	EvidenceCheckAllocationProbe   = "AllocationProbe"
)

// EnvironmentFingerprint 는 이 근거가 어느 환경에서 나왔는지다.
// 하나라도 바뀌면 그 환경에서 얻은 근거는 더 이상 이 환경을 설명하지 못한다.
type EnvironmentFingerprint struct {
	// BootID 는 node.status.nodeInfo.bootID — 재부팅을 가장 확실하게 잡는 축이다.
	// +optional
	BootID string `json:"bootID,omitempty"`
	// +optional
	KernelVersion string `json:"kernelVersion,omitempty"`
	// DriverVersion/FirmwareVersion 은 NodeDeviceReport 의 해당 벤더 장치에서 읽는다.
	// +optional
	DriverVersion string `json:"driverVersion,omitempty"`
	// +optional
	FirmwareVersion string `json:"firmwareVersion,omitempty"`
	// Generation 은 이 근거를 만든 정책의 metadata.generation 이다.
	// +optional
	Generation int64 `json:"generation,omitempty"`
}

// EvidenceCheck 는 체크 하나의 결과다. Target 은 그 체크가 본 대상(장치 PCI 등)이며,
// 노드 전체를 본 체크는 비어 있다.
type EvidenceCheck struct {
	Name string `json:"name"`
	// +optional
	Target string `json:"target,omitempty"`
	Passed bool   `json:"passed"`
	// +optional
	Message string `json:"message,omitempty"`
	// ProbedAt 은 이 AllocationProbe 를 실제로 실행한 시각이다(AllocationProbe check 에만
	// 실린다). ObservedAt/ExpiresAt 과 달리 매 갱신에 새로 쓰지 않고 실제 probe 시각에 고정해,
	// 재사용 pass 가 재조정마다 창을 밀리게 하지 않는다.
	// +optional
	ProbedAt *metav1.Time `json:"probedAt,omitempty"`
}

type AcceleratorEvidenceSpec struct {
	NodeName string `json:"nodeName"`
	// +kubebuilder:validation:Enum=nvidia;furiosa;rebellions;tenstorrent
	// +optional
	Vendor string `json:"vendor,omitempty"`
}

type AcceleratorEvidenceStatus struct {
	// Level 은 도달한 근거 등급이다. 어느 체크든 실패하면 **빈 문자열** 이다 —
	// "일부는 봤다" 를 등급으로 승격하지 않는다(정직성 원칙).
	// +kubebuilder:validation:Enum=Observed;FunctionallyVerified;AllocationVerified
	// +optional
	Level string `json:"level,omitempty"`
	// +optional
	Fingerprint EnvironmentFingerprint `json:"fingerprint,omitempty"`
	// +optional
	Checks []EvidenceCheck `json:"checks,omitempty"`
	// AdvertisedResources 는 검증 시점에 통과한 기대 광고량이다. drift 감시는 이 값과
	// 현재 allocatable 을 비교한다 — 그 자리에서 다시 계산하면 "검증 후에 무너졌다" 를
	// 말할 수 없다.
	// +optional
	AdvertisedResources map[string]int32 `json:"advertisedResources,omitempty"`
	// +optional
	ObservedAt metav1.Time `json:"observedAt,omitempty"`
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// +optional
	SourcePolicy string `json:"sourcePolicy,omitempty"`
	// Reason 은 Level 이 비어 있을 때 왜 비었는지다.
	// +optional
	Reason string `json:"reason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=aev
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="NODE",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="LEVEL",type=string,JSONPath=".status.level"
// +kubebuilder:printcolumn:name="EXPIRES",type=date,JSONPath=".status.expiresAt"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
type AcceleratorEvidence struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorEvidenceSpec   `json:"spec,omitempty"`
	Status            AcceleratorEvidenceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AcceleratorEvidenceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorEvidence `json:"items"`
}
