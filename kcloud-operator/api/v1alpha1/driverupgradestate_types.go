// driverupgradestate_types.go: DriverUpgradeState CRD 타입 정의
// 상세: 노드별 드라이버 업그레이드 상태를 추적하는 클러스터 스코프 CRD
// 생성일: 2026-04-13 | 수정일: 2026-10-14

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DriverModelUnknown은 DriverUpgradeState.Spec.Model에 들어가는 값 중 하나로, 한 노드에
// 같은 벤더 아래 서로 다른 product 장치가 섞여(예: furiosa Warboy+RNGD 혼재) 대표 model
// 을 하나로 정할 수 없을 때의 표시입니다. 이름이 아니라 "모른다"는 뜻입니다 —
// vendorRepresentativeModels(internal/controller)가 이 값을 채웁니다. 이 값을 보는
// 소비자(예: pkg/npuctl)는 특정 model 로 다루면 안 됩니다.
const DriverModelUnknown = "generic"

// RollbackRequestedAnnotation 은 관리자가 롤백을 요청했다는 표시다(값은 요청 시각, RFC3339).
// CLI(kubectl npu driver rollback)가 metadata 에 붙이고, 상태 기계가 RollbackRequestAccepted
// 인 상태에서 읽어 Rollback 으로 전이한 뒤 지운다 — status 는 컨트롤러만 쓴다.
const RollbackRequestedAnnotation = "npu.ai/rollback-requested"

// RollbackRequestAccepted 는 관리자 롤백 요청을 받을 수 있는 상태인지다. 전이 중에는 상태
// 기계의 자동 롤백 경로가 이미 있어 요청을 보류한다(어노테이션은 남겨 둔다).
func RollbackRequestAccepted(state string) bool {
	switch state {
	case "", UpgradeStateIdle, UpgradeStateFailed:
		return true
	}
	return false
}

// UpgradeCycleClosed 는 업그레이드 사이클이 끝나 노드의 driver-upgrading 라벨을 쥐지 않는 상태인지다(Failed 는 cordon 을 남길 수 있다).
// 사이클 라벨 정리(defer·stuck 스윕)가 이 술어 하나를 쓴다.
func UpgradeCycleClosed(state string) bool {
	switch state {
	case "", UpgradeStateIdle, UpgradeStateFailed, UpgradeStateHostAhead, UpgradeStateUnverifiedVersion:
		return true
	}
	return false
}

// VersionObservationAllowed 는 NodeDeviceReport 실측을 status.currentVersion 에 반영하고
// 버전 드리프트를 판정해도 되는 상태인지다. 조정자(internal/controller)와 드리프트 검출기
// (internal/verification)가 같은 술어를 써야 한쪽만 감지가 꺼지는 것을 막는다.
// 사이클 중간은 상태 기계가 DesiredVersion·PreviousVersion 을 소유해 밖에서 덮으면 롤백이 깨지고,
// 그 사이의 불일치는 설계상 정상이라 드리프트로 읽으면 오경보가 되므로 제외한다. Failed 는 사이클 결과(currentVersion)를 보존해야 하므로 제외한다.
// Failed 복구 판정은 currentVersion 을 보지 않고 NDR 의 로드 버전·검증을 읽는다. 받아들이는 것은 Idle 과 두 보고 전용 종점(UnverifiedVersion·HostAhead)뿐이다.
func VersionObservationAllowed(state string) bool {
	switch state {
	case UpgradeStateIdle, UpgradeStateUnverifiedVersion, UpgradeStateHostAhead:
		return true
	}
	return false
}

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
	// UpgradeStateHostAhead 는 호스트에 로드된 드라이버가 정책 버전보다 높고 allowDowngrade 가
	// 꺼져 있을 때 진입하는 보고 전용 종점이다. install Job·cordon 을 만들지 않는다. 정책 버전을
	// 올리거나 allowDowngrade·versionSource=Host 로 바꾸면 Idle 로 돌아간다.
	UpgradeStateHostAhead = "HostAhead"
	// UpgradeStateRebootRequired 는 install 성공 후 로드된 커널 모듈 버전 교체에 노드 재부팅이
	// 필요할 때(NDR.needsReboot=true, 계열 전환·같은 계열 patch 교체 모두) validator 체인 앞에서
	// 진입하는 상태입니다.
	UpgradeStateRebootRequired = "RebootRequired"
	// UpgradeStateRebooting 은 노드 재부팅을 트리거한 뒤 NotReady→Ready 복귀를 대기하는 상태입니다.
	UpgradeStateRebooting = "Rebooting"
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
	// State 는 현재 업그레이드 단계입니다(UpgradeState* 상수 값 중 하나).
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
	// ResolvedImage는 정책 기준으로 고른 설치 이미지입니다 — kernelImages 에 노드 커널의 검증된
	// 항목이 있으면 그 이미지, 없고 kernelImageBuildRevision 이 있으면 태그 규약으로 조립한
	// 이미지, 둘 다 아니면 driver.image 입니다. install Job 에만 실립니다(mode=job).
	ResolvedImage string `json:"resolvedImage,omitempty"`
	// RollbackAttempts는 현재 업그레이드 사이클에서의 롤백 시도 횟수입니다.
	// policy.UpgradePolicy.MaxRollbackAttempts(기본 3)를 초과하면 Failed로 전이합니다.
	// 새 업그레이드 사이클 진입 시(handleIdle) 0으로 초기화됩니다.
	// 같은 목표의 재시도는 RolledBackVersion 이 막습니다.
	RollbackAttempts int32 `json:"rollbackAttempts,omitempty"`
	// RebootAttempts 는 현재 사이클에서 노드 재부팅을 트리거한 횟수입니다.
	// UpgradePolicy.MaxReboots(기본 1) 초과 시 Failed 로 전이하여 무한 재부팅 루프를 방지합니다.
	// 새 사이클(handleIdle) 진입 시 0 으로 초기화됩니다.
	RebootAttempts int32 `json:"rebootAttempts,omitempty"`
	// RebootRequestedTime 은 마지막 재부팅을 트리거한 시각입니다.
	// Rebooting 상태에서 NotReady→Ready 전이 및 rebootTimeout 판정 기준입니다.
	RebootRequestedTime metav1.Time `json:"rebootRequestedTime,omitempty"`
	// RebootBootID 는 reboot Job 생성 직전에 캡처한 node.status.nodeInfo.bootID 입니다(premature-Ready 가드).
	// Rebooting 상태에서 node 가 Ready 로 보여도 bootID 가 이 값과 동일하면 실제 재부팅 전이므로 전진을 보류합니다.
	// 캡처 실패(빈 값) 시에는 기존 isNodeReady 기반으로 보수 전진합니다(교착 방지 fallback).
	RebootBootID string `json:"rebootBootID,omitempty"`
	// Message는 현재 상태에 대한 부가 설명입니다.
	Message string `json:"message,omitempty"`
	// QuiescedDeployments 는 Cordoning 진입 시 scale=0 으로 patch 한 Deployment 목록입니다.
	// Idle/Failed 진입 시 원래 replicas 로 복구하기 위한 backup 이며,
	// `npu.ai/quiesce-on-driver-upgrade=true` 라벨이 붙은 Deployment 만 대상입니다.
	QuiescedDeployments []QuiescedDeployment `json:"quiescedDeployments,omitempty"`
	// DriftFirstSeen 은 Idle 에서 선언 버전과 실측 버전의 불일치를 처음 본 시각이다. 일치하거나
	// 업그레이드 사이클에 들어가면 nil 로 되돌린다. 디바운스 기준이며 다른 목적과 공유하지 않는다.
	DriftFirstSeen *metav1.Time `json:"driftFirstSeen,omitempty"`
	// ObservedModels 는 이 노드에서 이 벤더로 관측된 장치 모델 목록이다(NodeDeviceReport 기준,
	// 정렬·중복 제거). spec.model 이 DriverModelUnknown("generic")으로 뭉개진 혼재 노드에서도
	// 어떤 제품이 섞여 있는지 여기서 읽는다 — 값을 지어내지 않고 관측만 옮긴다.
	// +optional
	ObservedModels []string `json:"observedModels,omitempty"`
	// ConsumedRollbackRequest 는 마지막으로 받아들인 관리자 롤백 요청(어노테이션 값, RFC3339)이다.
	// 어노테이션 제거가 실패해 남아 있어도 같은 값은 다시 받아들이지 않는다 — 제거 재시도가 Idle·Failed 로
	// 돌아간 뒤까지 미뤄지면 오래된 요청이 새 롤백을 시작하던 결함을 막는다.
	// +optional
	ConsumedRollbackRequest string `json:"consumedRollbackRequest,omitempty"`
	// RolledBackVersion 은 롤백한 목표 버전이다(설치·검증 실패, drain 타임아웃 또는 관리자 요청). Idle 은
	// 정책 목표가 이 값이고 정책 generation 이 RolledBackGeneration 과 같으면 새 사이클을 열지 않는다.
	// Job 모드 자체 복구(driverLoaded=false)는 보류 중에도 실행되며, 이 목표 대신 롤백이 복원한 버전을
	// 다시 설치하고 기록을 남긴다.
	// +optional
	RolledBackVersion string `json:"rolledBackVersion,omitempty"`
	// RolledBackGeneration 은 롤백을 시작할 때의 DriverInstallPolicy metadata.generation 이다.
	// +optional
	RolledBackGeneration int64 `json:"rolledBackGeneration,omitempty"`
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
