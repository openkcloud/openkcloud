package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeDeviceReport Condition 타입 상수
const (
	// ConditionUpgradeInProgress는 노드에서 드라이버 업그레이드가 진행 중임을 나타냅니다.
	ConditionUpgradeInProgress = "UpgradeInProgress"
	// ConditionUpgradePending는 드라이버 업그레이드가 예약되었음을 나타냅니다.
	ConditionUpgradePending = "UpgradePending"
	// ConditionCordonedForUpgrade는 업그레이드를 위해 노드가 cordon 처리되었음을 나타냅니다.
	ConditionCordonedForUpgrade = "CordonedForUpgrade"
	// ConditionUpgradeSucceeded는 드라이버 업그레이드가 성공적으로 완료되었음을 나타냅니다.
	ConditionUpgradeSucceeded = "UpgradeSucceeded"
	// ConditionUpgradeFailed는 드라이버 업그레이드가 실패했음을 나타냅니다.
	ConditionUpgradeFailed = "UpgradeFailed"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=ndr
// +kubebuilder:subresource:status
type NodeDeviceReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeDeviceReportSpec   `json:"spec,omitempty"`
	Status NodeDeviceReportStatus `json:"status,omitempty"`
}

type NodeDeviceReportSpec struct {
	// 이 리포트가 속하는 노드 이름 (immutable 권장)
	NodeName string `json:"nodeName"`
}

type DeviceEntry struct {
	Vendor              string `json:"vendor,omitempty"` // "furiosa" | "nvidia" 등
	Model               string `json:"model,omitempty"`  // "warboy" 등
	Count               int32  `json:"count,omitempty"`
	DriverLoaded        bool   `json:"driverLoaded,omitempty"`
	DriverVersion       string `json:"driverVersion,omitempty"`
	DriverVersionDetail string `json:"driverVersionDetail,omitempty"` // 상세 버전 정보 (한 줄 요약)
	NeedsReboot         bool   `json:"needsReboot,omitempty"`
}

type Condition struct {
	Type    string `json:"type,omitempty"`
	Status  string `json:"status,omitempty"` // "True"|"False"|"Unknown"
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type NodeDeviceReportStatus struct {
	Devices    []DeviceEntry `json:"devices,omitempty"`
	Conditions []Condition   `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
type NodeDeviceReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeDeviceReport `json:"items"`
}
