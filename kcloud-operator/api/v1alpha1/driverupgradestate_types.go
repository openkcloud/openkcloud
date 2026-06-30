// driverupgradestate_types.go: DriverUpgradeState CRD 타입 정의
// 상세: 노드별 드라이버 업그레이드 상태를 추적하는 클러스터 스코프 CRD
// 생성일: 2026-04-13 | 수정일: 2026-06-15

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// 업그레이드 상태 상수
const (
	UpgradeStateIdle        = "Idle"
	UpgradeStateRequired    = "UpgradeRequired"
	UpgradeStatePreFlight   = "PreFlight"
	UpgradeStateCordoning   = "Cordoning"
	UpgradeStateDraining    = "Draining"
	UpgradeStateUpgrading   = "Upgrading"
	UpgradeStateValidating  = "Validating"
	UpgradeStateUncordoning = "Uncordoning"
	UpgradeStateRollback    = "Rollback"
	// UpgradeStateFailed 는 rollback exhaustion 등으로 인해 자동 복구가 불가능한
	// 터미널 상태이다. 이 상태에서는 reconcile 이 추가 transition / DS image patch
	// 를 수행하지 않고 즉시 반환한다 (수동 조치 필요).
	UpgradeStateFailed = "Failed"
	// UpgradeStateUnverifiedVersion 은 DIP.spec.verifiedVersions 화이트리스트에 없는
	// driver version 이 지정된 경우 진입하는 terminal 상태입니다.
	// DIP.spec.verifiedVersions 를 수정하거나 DUS 를 삭제·재생성하여 복구합니다.
	UpgradeStateUnverifiedVersion = "UnverifiedVersion"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=dus
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="NODE",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="VENDOR",type=string,JSONPath=".spec.vendor"
// +kubebuilder:printcolumn:name="STATE",type=string,JSONPath=".status.state"
// +kubebuilder:printcolumn:name="CURRENT",type=string,JSONPath=".status.currentVersion"
// +kubebuilder:printcolumn:name="DESIRED",type=string,JSONPath=".status.desiredVersion"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
// DriverUpgradeState는 노드별 드라이버 업그레이드 상태를 추적합니다.
type DriverUpgradeState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DriverUpgradeStateSpec   `json:"spec,omitempty"`
	Status DriverUpgradeStateStatus `json:"status,omitempty"`
}

// DriverUpgradeStateSpec은 업그레이드 대상 노드/벤더/모델 정보를 담습니다.
type DriverUpgradeStateSpec struct {
	// NodeName은 업그레이드 대상 노드 이름입니다.
	NodeName string `json:"nodeName"`
	// Vendor는 드라이버 벤더 (예: "furiosa", "nvidia")
	Vendor string `json:"vendor"`
	// Model은 디바이스 모델 (예: "warboy", "a100")
	Model string `json:"model,omitempty"`
}

// DriverUpgradeStateStatus는 업그레이드 진행 상태를 나타냅니다.
type DriverUpgradeStateStatus struct {
	// CurrentVersion은 현재 설치된 드라이버 버전입니다.
	CurrentVersion string `json:"currentVersion,omitempty"`
	// DesiredVersion은 목표 드라이버 버전입니다.
	DesiredVersion string `json:"desiredVersion,omitempty"`
	// State는 현재 업그레이드 단계입니다. (Idle, UpgradeRequired, PreFlight, Cordoning, Draining, Upgrading, Validating, Uncordoning, Rollback)
	State string `json:"state,omitempty"`
	// LastTransitionTime은 마지막 상태 전환 시각입니다.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// Retries는 현재 업그레이드 시도에서의 재시도 횟수입니다.
	Retries int32 `json:"retries,omitempty"`
	// PreviousVersion은 롤백 기준이 되는 이전 드라이버 버전입니다.
	PreviousVersion string `json:"previousVersion,omitempty"`
	// PreviousImage는 롤백 시 복구할 이전 드라이버 컨테이너 이미지 전체 레퍼런스입니다.
	// 빌드 접미사를 포함한 원본 이미지를 그대로 보존하기 위해 태그 치환 대신 원본 이미지를 저장합니다.
	PreviousImage string `json:"previousImage,omitempty"`
	// RollbackAttempts는 현재 업그레이드 사이클에서의 롤백 시도 횟수입니다.
	// policy.UpgradePolicy.MaxRollbackAttempts(기본 3)를 초과하면 Failed로 전이합니다.
	// 새 업그레이드 사이클 진입 시(handleIdle) 0으로 초기화됩니다.
	RollbackAttempts int32 `json:"rollbackAttempts,omitempty"`
	// Message는 현재 상태에 대한 부가 설명입니다.
	Message string `json:"message,omitempty"`
	// QuiescedDeployments 는 Cordoning 진입 시 scale=0 으로 patch 한 Deployment 목록입니다.
	// Idle/Failed 진입 시 원래 replicas 로 복구하기 위한 backup 이며,
	// `npu.ai/quiesce-on-driver-upgrade=true` 라벨이 붙은 Deployment 만 대상입니다.
	// (architectural plan §A6.1 — opt-in quiesce on driver upgrade)
	QuiescedDeployments []QuiescedDeployment `json:"quiescedDeployments,omitempty"`
}

// QuiescedDeployment 는 driver upgrade cycle 동안 일시적으로 scale=0 으로 quiesce 된
// Deployment 의 backup 정보입니다. cycle 종료(Idle/Failed) 시 원래 replicas 로 복구됩니다.
type QuiescedDeployment struct {
	// Namespace 는 quiesce 된 Deployment 의 네임스페이스입니다.
	Namespace string `json:"namespace"`
	// Name 은 quiesce 된 Deployment 의 이름입니다.
	Name string `json:"name"`
	// OriginalReplicas 는 quiesce 직전의 spec.replicas 값입니다 (복구 기준).
	OriginalReplicas int32 `json:"originalReplicas"`
}

// +kubebuilder:object:root=true
type DriverUpgradeStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DriverUpgradeState `json:"items"`
}
