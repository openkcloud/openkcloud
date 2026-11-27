// ============================================================
// acceleratorverificationpolicy_types.go: AcceleratorVerificationPolicy CRD 타입
// 상세: 어떤 체크를 돌리고, 근거를 얼마나 믿고(ttl), 무엇이 바뀌면 버릴지
// (invalidateOn)를 노드 선택 단위로 정한다. CRD 가 없어도 내장 기본값으로 동작한다.
// 생성일: 2026-07-31
// ============================================================
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AcceleratorVerificationPolicySpec struct {
	// NodeSelector 가 비면 모든 노드에 적용된다. 여러 정책이 한 노드에 맞으면 이름 사전순으로
	// 첫 번째가 이긴다(결정론 — 병합하지 않는다. 병합은 "어느 정책이 이 값을 줬는가" 를 못 말한다).
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// Checks 는 돌릴 체크 목록이다. 비면 내장 기본값을 쓴다.
	// +kubebuilder:validation:items:Enum=DeviceObservation;NodeDeviceReport;Advertisement;AllocationProbe
	// +optional
	Checks []string `json:"checks,omitempty"`
	// TTL 은 근거를 믿는 기간이다(예 30m). 비면 내장 기본값.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`
	// InvalidateOn 은 값이 바뀌면 근거를 즉시 버릴 환경 축이다. 비면 내장 기본값.
	// +kubebuilder:validation:items:Enum=BootID;KernelVersion;DriverVersion;FirmwareVersion;Generation
	// +optional
	InvalidateOn []string `json:"invalidateOn,omitempty"`
	// DriftGracePeriod 는 광고 불일치가 이 시간 이상 지속돼야 Degraded 로 확정한다는 뜻이다.
	// 짧게 두면 DaemonSet 롤아웃 중의 정상적인 광고 공백을 장애로 오인한다.
	// +optional
	DriftGracePeriod *metav1.Duration `json:"driftGracePeriod,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=avp
// +kubebuilder:printcolumn:name="TTL",type=string,JSONPath=".spec.ttl"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
type AcceleratorVerificationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorVerificationPolicySpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type AcceleratorVerificationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorVerificationPolicy `json:"items"`
}
