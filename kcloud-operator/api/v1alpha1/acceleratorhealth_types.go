// ============================================================
// acceleratorhealth_types.go: 노드별 health 상태 CR
// 상세: 이름은 노드 이름이다(노드당 하나). status 만 쓰이며 spec 은 관측 대상 식별만 담는다.
// AcceleratorEvidence 가 "그때 정말 그랬는가" 라면 이것은 "지금도 그런가" 다.
// 생성일: 2026-08-04
// ============================================================
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// HealthSignal 은 신호 하나의 관측 기록이다.
type HealthSignal struct {
	Name string `json:"name"`
	// Target 은 장치 단위 신호일 때의 PCI 다. 노드 단위 신호면 비어 있다 —
	// 장치 단위 health 로 넓힐 때의 이행 경로다.
	// +optional
	Target string `json:"target,omitempty"`
	OK     bool   `json:"ok"`
	// +optional
	Message string `json:"message,omitempty"`
}

// DeviceHealth 는 장치 하나의 판정 결과다. 노드 단위 상태가 답하지 못하는
// "어느 장치가 문제인가" 를 운영자가 status 만 보고 알 수 있게 한다.
type DeviceHealth struct {
	PCIAddress string `json:"pciAddress"`
	// +optional
	State string `json:"state,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
}

// AcceleratorHealthSpec 은 관측 대상이다.
type AcceleratorHealthSpec struct {
	NodeName string `json:"nodeName"`
}

// AcceleratorHealthStatus 는 판정 결과다.
type AcceleratorHealthStatus struct {
	// +kubebuilder:validation:Enum=Unknown;Healthy;Degraded;Unhealthy;Quarantined;Recovering
	// +optional
	State string `json:"state,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// AllocationAllowed 가 false 면 배치 판정이 이 노드를 후보에서 뺀다.
	// +optional
	AllocationAllowed bool `json:"allocationAllowed,omitempty"`
	// +optional
	Signals []HealthSignal `json:"signals,omitempty"`
	// DetectedAt 은 현재 상태로 바뀐 시각이다(같은 상태가 이어지면 갱신하지 않는다).
	// +optional
	DetectedAt metav1.Time `json:"detectedAt,omitempty"`
	// RecoveryOperationRef 는 이 상태 때문에 만든 복구 작업 이름이다.
	// +optional
	RecoveryOperationRef string `json:"recoveryOperationRef,omitempty"`
	// RecoveryFailures 는 종점 실패로 끝난 복구 작업 수다(격리 임계의 입력).
	// +optional
	RecoveryFailures int32 `json:"recoveryFailures,omitempty"`
	// SuspectedAt 은 광고 불일치를 처음 본 시각이다. 유예 계산이 이 값을 쓴다 — condition 의
	// 전이 시각에 얹으면 재검증 pass 가 매번 지워 유예가 영영 안 찬다.
	// +optional
	SuspectedAt *metav1.Time `json:"suspectedAt,omitempty"`
	// Devices 는 장치별 판정이다(장치 단위 신호가 없으면 비어 있다).
	// +optional
	Devices []DeviceHealth `json:"devices,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=ah
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="STATE",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="ALLOC",type=boolean,JSONPath=`.status.allocationAllowed`
// +kubebuilder:printcolumn:name="REASON",type=string,JSONPath=`.status.reason`

// AcceleratorHealth 는 노드 하나의 health 상태다.
type AcceleratorHealth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorHealthSpec   `json:"spec,omitempty"`
	Status            AcceleratorHealthStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AcceleratorHealthList 는 목록이다.
type AcceleratorHealthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorHealth `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AcceleratorHealth{}, &AcceleratorHealthList{})
}
