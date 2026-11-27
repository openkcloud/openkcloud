// ============================================================
// acceleratorhealthpolicy_types.go: health 감시 임계와 복구 매핑 정책 CRD
// 상세: 노드 selector 별 임계값과 상태별 조치를 정의한다. CRD 가 없어도 기본값으로 감시가 돈다.
// 생성일: 2026-08-04
// ============================================================
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// HealthChecks 는 신호별 임계다. 0 은 "기본값을 쓴다" 는 뜻이다(끄기가 아니다) —
// 끄려면 Disabled 목록에 신호 이름을 적는다.
type HealthChecks struct {
	// +optional
	NDRFreshnessSeconds int32 `json:"ndrFreshnessSeconds,omitempty"`
	// +optional
	DriverHeartbeatFailureThreshold int32 `json:"driverHeartbeatFailureThreshold,omitempty"`
	// +optional
	AdvertisementGraceSeconds int32 `json:"advertisementGraceSeconds,omitempty"`
	// RepeatedFailureThreshold 는 같은 노드에서 몇 번 연속 복구가 실패하면 격리할지다.
	// +optional
	RepeatedFailureThreshold int32 `json:"repeatedFailureThreshold,omitempty"`
	// MonitorIntervalSeconds 는 이 노드를 다시 볼 간격이다(기본 30초). 짧게 잡으면 짧은 장애도
	// 잡히지만 API 부하가 는다. operator 의 자가치유 중 13초 안에 끝나는 장애가 있어, 기본
	// 주기로는 그 창을 아예 못 본다.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MonitorIntervalSeconds int32 `json:"monitorIntervalSeconds,omitempty"`
	// Disabled 는 끌 신호 이름이다(신호 상수와 같은 어휘).
	// +optional
	Disabled []string `json:"disabled,omitempty"`
}

// RemediationAction 은 상태별 조치다.
// +kubebuilder:validation:Enum=None;StopNewAllocation;RequestRecovery;Quarantine
type RemediationAction string

// 조치 4종. None 은 "관찰만" 이다.
const (
	RemediationNone              RemediationAction = "None"
	RemediationStopNewAllocation RemediationAction = "StopNewAllocation"
	RemediationRequestRecovery   RemediationAction = "RequestRecovery"
	RemediationQuarantine        RemediationAction = "Quarantine"
)

// RemediationRule 은 한 원인에 대한 조치다.
type RemediationRule struct {
	Action RemediationAction `json:"action"`
	// RecoveryCooldownSeconds 는 같은 원인으로 다시 작업을 만들기까지의 최소 간격이다.
	// +optional
	RecoveryCooldownSeconds int32 `json:"recoveryCooldownSeconds,omitempty"`
}

// AcceleratorHealthPolicySpec 은 감시 임계와 원인별 조치다.
type AcceleratorHealthPolicySpec struct {
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// +optional
	Checks HealthChecks `json:"checks,omitempty"`
	// Remediation 은 원인 이름 → 조치다(원인 상수와 같은 어휘).
	// +optional
	Remediation map[string]RemediationRule `json:"remediation,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=ahp

// AcceleratorHealthPolicy 는 health 감시 정책이다.
type AcceleratorHealthPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorHealthPolicySpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// AcceleratorHealthPolicyList 는 목록이다.
type AcceleratorHealthPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorHealthPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AcceleratorHealthPolicy{}, &AcceleratorHealthPolicyList{})
}
