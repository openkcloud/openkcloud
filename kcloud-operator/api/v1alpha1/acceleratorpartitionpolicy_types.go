// ============================================================
// acceleratorpartitionpolicy_types.go: AcceleratorPartitionPolicy CRD 타입
// 상세: 이기종 가속기(RNGD/NVIDIA) 파티션 통합 인터페이스. spec + status 4축.
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// PartitionLayout 는 한 profile 의 파티션 레이아웃 요청이다.
type PartitionLayout struct {
	// Profile 은 벤더 공식 표기(RNGD: 2core.12gb|4core.24gb, NVIDIA: 1g.6gb 등).
	Profile string `json:"profile"`
	// CountPerDevice 는 선택된 각 물리 장치에 생성할 해당 profile 인스턴스 수.
	// FixedProfile backend(RNGD)는 선택적(주면 검증만), mixed backend(NVIDIA)는 필수.
	// +optional
	CountPerDevice int32 `json:"countPerDevice,omitempty"`
}

// AcceleratorPartitionPolicySpec 는 원하는 파티션 상태다. layout 과 sharing 둘 다 비면
// 정책이 아무것도 요청하지 않으므로 admission 에서 거부한다.
// +kubebuilder:validation:XValidation:rule="(has(self.layout) && self.layout.size() > 0) || has(self.sharing)",message="spec.layout and spec.sharing cannot both be empty"
type AcceleratorPartitionPolicySpec struct {
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +kubebuilder:validation:Enum=furiosa;nvidia;rebellions;tenstorrent
	Vendor string `json:"vendor"`
	// Layout 은 파티션 레이아웃 요청이다. 비우면 파티션 없음 — sharing 만 단독으로 쓸 수 있다
	// (순수 시분할 등). 이때 파티션 apply 경로(cordon·MIG mode·GI)는 타지 않는다.
	// +optional
	Layout []PartitionLayout `json:"layout,omitempty"`
	// DeletionPolicy 는 삭제 시 하드웨어 상태를 어디까지 되돌릴지 정한다. RestoreMode 는 vendor nvidia
	// (MIG mode 복원)에서만 유효하고 다른 벤더는 webhook 이 거절한다 — 그 벤더의 삭제는 항상 Retain 이다.
	// +kubebuilder:validation:Enum=Retain;RestoreMode
	// +kubebuilder:default=Retain
	DeletionPolicy string `json:"deletionPolicy,omitempty"`
	// Sharing 은 공유 요청이다(nil = exclusive). layout 과 조합하면 partitioned-shared 가 된다.
	// +optional
	Sharing *SharingSpec `json:"sharing,omitempty"`
}

// deletionPolicy 값.
const (
	// DeletionPolicyRetain 은 파티션만 회수하고 MIG mode 는 그대로 둔다(기본값, 기존 동작).
	DeletionPolicyRetain = "Retain"
	// DeletionPolicyRestoreMode 는 파티션 회수에 더해 MIG mode 를 Disabled 로 되돌린다.
	// 그 노드를 다시 공유 전용으로 쓰려면 필요하며, NVIDIA 드라이버가 mode 전환에 재부팅을
	// 요구하므로 이 값을 쓰면 삭제 과정에 노드 재부팅이 포함된다.
	DeletionPolicyRestoreMode = "RestoreMode"
)

// 공유 모드(사용자 추상 모드 중 MVP 2종. partitioned/partitioned-shared 는
// layout + sharing 조합으로 표현되므로 별도 값이 아니다).
const (
	SharingModeExclusive  = "exclusive"
	SharingModeTimeSliced = "timeSliced"
	// SharingModeMPS 는 NVIDIA MPS(다중 프로세스 서비스)다. time-slicing 과 달리 노드에
	// mps-control-daemon 이 함께 떠야 하고, MIG 가 켜진 장치에서는 쓸 수 없다.
	SharingModeMPS = "mps"
	// SharingModeOversubscribed 는 "광고된 리소스 수가 NDR 이 보고한 물리 장치 수를 넘는다"는
	// 관측 그 자체만을 뜻하며 원인은 확정하지 않는다 — (a) device-plugin 이 시분할로 복제
	// 중이거나(격리 없음), (b) 벤더 device-plugin 이 하드웨어 서브유닛을 그대로 광고한
	// 것(격리 있음, 예: Furiosa RNGD 1대가 PE 4개로 광고됨) 중 어느 쪽에서도 같이 나온다.
	// CheckMode 는 이 상태도 exclusive 처럼 안전하게 거절하지만, timeSliced 처럼 "복제라 격리
	// 없음"이라고 단정하지 않는다 — timeSliced 는 ApplyRecord 저널의 실측 증거가 있을 때만 선언한다.
	SharingModeOversubscribed = "oversubscribed"
)

// TimeSlicingSpec 는 시간공유 요청이다. replicas 는 성능 비율이 아니라 광고 배수다.
type TimeSlicingSpec struct {
	// +kubebuilder:validation:Minimum=2
	// +kubebuilder:validation:Maximum=16
	Replicas int32 `json:"replicas"`
	// FailRequestsGreaterThanOne 은 replica 1개 초과 요청을 kubelet 단계에서 거절할지다.
	// +optional
	FailRequestsGreaterThanOne bool `json:"failRequestsGreaterThanOne,omitempty"`
}

// SharingSpec 는 공유 요청 축이다. nil 이면 exclusive.
type SharingSpec struct {
	// +kubebuilder:validation:Enum=exclusive;timeSliced;mps
	Mode string `json:"mode"`
	// +optional
	TimeSlicing *TimeSlicingSpec `json:"timeSlicing,omitempty"`
	// +optional
	MPS *MPSSpec `json:"mps,omitempty"`
}

// MPSSpec 는 MPS 공유 요청이다. replica 는 광고 배수이며 물리 장치 수가 아니다.
// device-plugin config 의 sharing.mps.failRequestsGreaterThanOne 은 항상 true 로 강제 렌더된다
// (upstream historical MPS 안전장치 보존 — replicas>1 요청 거부). 이 스펙에는 이를 뒤집을 필드가 없다.
type MPSSpec struct {
	// +kubebuilder:validation:Minimum=2
	Replicas int32 `json:"replicas"`
}

// EffectiveSharingMode 는 nil-safe 모드 조회다.
func (s AcceleratorPartitionPolicySpec) EffectiveSharingMode() string {
	if s.Sharing == nil || s.Sharing.Mode == "" {
		return SharingModeExclusive
	}
	return s.Sharing.Mode
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=acpp
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="VENDOR",type=string,JSONPath=".spec.vendor"
// +kubebuilder:printcolumn:name="PHASE",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
type AcceleratorPartitionPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorPartitionPolicySpec   `json:"spec,omitempty"`
	Status            AcceleratorPartitionPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AcceleratorPartitionPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorPartitionPolicy `json:"items"`
}

// --- status 4축 ---

type AcceleratorPartitionPolicyStatus struct {
	ObservedGeneration int64          `json:"observedGeneration,omitempty"`
	Phase              string         `json:"phase,omitempty"` // 전 target 최저 수렴 phase
	Targets            []TargetStatus `json:"targets,omitempty"`
	ApplyRecords       []ApplyRecord  `json:"applyRecords,omitempty"`
}

// ApplyRecord 는 nvidia MIG apply 의 durable journal + 삭제 복원 snapshot 이다.
type ApplyRecord struct {
	NodeName             string   `json:"nodeName"`
	GPUPCIs              []string `json:"gpuPCIs,omitempty"`
	OwnerUID             string   `json:"ownerUID,omitempty"`
	BaselineGPUCount     int32    `json:"baselineGPUCount,omitempty"`
	ExpectedMigCount     int32    `json:"expectedMigCount,omitempty"`
	ExpectedFullGPUCount int32    `json:"expectedFullGPUCount,omitempty"`
	Profile              string   `json:"profile,omitempty"`
	Count                int32    `json:"count,omitempty"`
	Generation           int64    `json:"generation,omitempty"`
	MigPhase             string   `json:"migPhase,omitempty"`
	// CordonedByPolicy 는 MIG mode enable 절차가 노드를 cordon 했는지다. 운영자가 미리
	// cordon 해 둔 노드를 정책이 멋대로 uncordon 하지 않도록, 우리가 잠근 노드만 되돌린다.
	CordonedByPolicy bool `json:"cordonedByPolicy,omitempty"`
	// RebootAttempts 는 MIG mode 확정을 위해 이 정책이 요청한 재부팅 횟수다. Job 생성
	// 직전에 증가·영속하며, 상한을 넘으면 재부팅 대신 Failed 로 끝낸다(무한 재부팅 방지).
	RebootAttempts int32 `json:"rebootAttempts,omitempty"`
	// DisableRebootAttempts 는 deletionPolicy=RestoreMode 삭제 경로가 MIG mode 를 되돌리기 위해
	// 요청한 재부팅 횟수다. RebootAttempts(mode enable)와 별도 예산이다 — 공유하면 enable 이
	// 상한을 소진해 Failed 로 끝난 뒤에는 disable 이 재부팅을 한 번도 못 쓰고 finalizer 가 영구 유지된다.
	DisableRebootAttempts int32 `json:"disableRebootAttempts,omitempty"`
	// SharingMode/SharingReplicas 는 공유 적용 저널이다 — mutation 이전에 영속하고, 삭제 시
	// 이 값으로 device-plugin 설정 원복 대상을 판단한다(파티션 저널과 같은 규율).
	SharingMode     string `json:"sharingMode,omitempty"`
	SharingReplicas int32  `json:"sharingReplicas,omitempty"`
	// SharingRolledBackGeneration 은 같은 spec.generation 에서 sharing verify 가 이미 실패해
	// rollback 됐음을 기록한다. 값이 acpp.Generation 과 같으면 재적용하지 않고 terminal Failed 로
	// 끝낸다 — 재시도 경로는 spec 변경(generation 증가)뿐이다.
	SharingRolledBackGeneration int64 `json:"sharingRolledBackGeneration,omitempty"`
}

type TargetStatus struct {
	NodeName      string         `json:"nodeName,omitempty"`
	Phase         string         `json:"phase,omitempty"`
	Backend       BackendRef     `json:"backend,omitempty"`
	DriverVersion string         `json:"driverVersion,omitempty"`
	Devices       []DeviceStatus `json:"devices,omitempty"`
	// 3-state layout — RNGD 는 DS 전역이라 target 수준(device 아님).
	RequestedLayout []PartitionLayout     `json:"requestedLayout,omitempty"`
	ResolvedLayout  []ResolvedLayoutEntry `json:"resolvedLayout,omitempty"`
	ObservedLayout  []ObservedLayoutEntry `json:"observedLayout,omitempty"`
	Operations      OperationsStatus      `json:"operations,omitempty"`
	Advertisement   AdvertisementStatus   `json:"advertisement,omitempty"`
	Conditions      []metav1.Condition    `json:"conditions,omitempty"`
}

// BackendRef 는 apply 대상 + rollback 기준.
type BackendRef struct {
	Kind               string       `json:"kind,omitempty"` // DaemonSet
	Namespace          string       `json:"namespace,omitempty"`
	Name               string       `json:"name,omitempty"`
	UID                string       `json:"uid,omitempty"`
	Version            string       `json:"version,omitempty"`            // DP 이미지 버전(driverVersion 과 분리)
	ConfigurationScope string       `json:"configurationScope,omitempty"` // DaemonSetGlobal
	Rollback           RollbackInfo `json:"rollback,omitempty"`
}

// RollbackInfo 는 apply 전 저장하는 복원 기준.
type RollbackInfo struct {
	PrevPolicy      string `json:"prevPolicy,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	TemplateHash    string `json:"templateHash,omitempty"`
}

type DeviceStatus struct {
	ID                  string              `json:"id,omitempty"` // canonical: UUID/PCI(숫자 index 금지)
	PCIAddress          string              `json:"pciAddress,omitempty"`
	Model               string              `json:"model,omitempty"`
	PartitionCapability PartitionCapability `json:"partitionCapability,omitempty"`
	// SharingCapability 는 공유 방식별 지원·검증 상태다.
	// +optional
	SharingCapability SharingCapability `json:"sharingCapability,omitempty"`
	// IsolationCapability 는 실측 근거가 있는 격리 등급이다(미실측은 빈 문자열).
	// +optional
	IsolationCapability IsolationCapability `json:"isolationCapability,omitempty"`
	// AllocationAPIs 는 이 장치를 노출할 수 있는 API 다(MVP: devicePlugin).
	// +optional
	AllocationAPIs []string         `json:"allocationAPIs,omitempty"`
	Operations     OperationsStatus `json:"operations,omitempty"` // 장치별 apply 지원(A30/A2 분리)
}

type PartitionCapability struct {
	HardwareSupported      bool             `json:"hardwareSupported"`
	PartitionModel         string           `json:"partitionModel,omitempty"` // FixedProfile|ProfileConstrained|None
	MixedProfilesSupported bool             `json:"mixedProfilesSupported,omitempty"`
	Profiles               []ProfileSupport `json:"profiles,omitempty"`
	Reason                 string           `json:"reason,omitempty"`
	// Verification 은 required|verified|notApplicable. HardwareSupported=false 의 뜻을
	// 갈라준다: verified 면 "측정했고 이 하드웨어는 파티션을 못 한다", required 면
	// "관측 자체가 실패해 아직 모른다". 빈 문자열은 구버전 리포트(미기록)다.
	// +optional
	Verification string `json:"verification,omitempty"`
}

type ProfileSupport struct {
	Name                  string `json:"name"`
	MaxInstancesPerDevice int32  `json:"maxInstancesPerDevice,omitempty"`
	SupportLevel          string `json:"supportLevel,omitempty"` // Verified|Documented|LegacyDocumented
}

type ResolvedLayoutEntry struct {
	Profile                string `json:"profile,omitempty"`
	BackendPolicy          string `json:"backendPolicy,omitempty"`
	ExpectedCountPerDevice int32  `json:"expectedCountPerDevice,omitempty"`
}

type ObservedLayoutEntry struct {
	BackendPolicy          string `json:"backendPolicy,omitempty"`
	ObservedCountPerDevice int32  `json:"observedCountPerDevice,omitempty"`
}

type OperationsStatus struct {
	Apply    OperationStatus `json:"apply,omitempty"`
	Rollback OperationStatus `json:"rollback,omitempty"`
}

type OperationStatus struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

type AdvertisementStatus struct {
	Mode                  string           `json:"mode,omitempty"` // Flat|ProfileNamed
	ProfileNamedSupported bool             `json:"profileNamedSupported,omitempty"`
	Reason                string           `json:"reason,omitempty"`
	AdvertisedResources   map[string]int32 `json:"advertisedResources,omitempty"`
}

// phase (상태머신)
const (
	ACPPPhasePending                 = "Pending"
	ACPPPhaseDiscoveringCapabilities = "DiscoveringCapabilities"
	ACPPPhaseValidating              = "Validating"
	ACPPPhaseWaitingForDrain         = "WaitingForWorkloadsToDrain"
	ACPPPhaseApplying                = "Applying"
	ACPPPhaseRestartingDevicePlugin  = "RestartingDevicePlugin"
	ACPPPhaseVerifyingAllocatable    = "VerifyingAllocatableResources"
	ACPPPhaseVerifyingAllocation     = "VerifyingWorkloadAllocation"
	ACPPPhaseReady                   = "Ready"
	ACPPPhaseRollingBack             = "RollingBack"
	ACPPPhaseRestored                = "PreviousConfigurationRestored" // phase=Failed 취급
	ACPPPhaseRollbackFailed          = "RollbackFailed"
	ACPPPhaseFailed                  = "Failed"
	ACPPPhaseUnsupported             = "Unsupported" // NVIDIA MVP apply 미지원
	// ACPPPhaseDegraded 는 검증까지 통과했으나 그 뒤 광고가 무너진 상태다. 하드웨어는 그대로일
	// 수 있으므로 실패가 아니고, 그렇다고 Ready 도 아니다 — 재적용은 하지 않는다(감지 전용).
	ACPPPhaseDegraded = "Degraded"
)

// condition type
const (
	ACPPCondCapabilitiesDiscovered = "CapabilitiesDiscovered"
	ACPPCondValidated              = "Validated"
	ACPPCondApplied                = "Applied"
	ACPPCondVerified               = "Verified"
	ACPPCondRolledBack             = "RollbackSucceeded"
	ACPPCondDegraded               = "Degraded"
)

// reason
const (
	ReasonTargetConflict                    = "TargetConflict"
	ReasonBackendScopeMismatch              = "BackendScopeMismatch"
	ReasonVendorMismatch                    = "VendorMismatch"
	ReasonApplyBackendNotInstalled          = "ApplyBackendNotInstalled"
	ReasonHardwareCapabilityMissing         = "HardwareCapabilityMissing"
	ReasonDevicePluginFlatAdvertisementOnly = "DevicePluginFlatAdvertisementOnly"
	ReasonQuiesceRequired                   = "QuiesceRequired"
	ReasonDriverNotReady                    = "DriverNotReady"
	ReasonNodeNotCordoned                   = "NodeNotCordoned"
	ReasonExistingMigConfiguration          = "ExistingMigConfiguration"
	ReasonMigModeNotEnabled                 = "MigModeNotEnabled"
	ReasonUnstableGpuIdentity               = "UnstableGpuIdentity"
	ReasonObservationUnavailable            = "ObservationUnavailable"
	ReasonBaselineInconsistent              = "BaselineInconsistent"
	ReasonOwnershipConflict                 = "OwnershipConflict"
	ReasonSharingUnsupported                = "SharingUnsupported"
	ReasonSharingInvalid                    = "SharingInvalid"
	ReasonSharingApplyFailed                = "SharingApplyFailed"
	ReasonSharingNotConverged               = "SharingNotConverged"
	ReasonSharingVerified                   = "SharingVerified"
	// ReasonSharingDaemonNotReady 는 mps control daemon 이 아직 안 뜬 상태다(transient — 이미지
	// pull/스케줄링 대기). ApplyFailed 와 구분한다: 실패가 아니라 대기이고, 재시도로 풀린다.
	ReasonSharingDaemonNotReady = "SharingDaemonNotReady"
	// ReasonEvidenceDisagreement 는 spec·장치·노드 보고·광고가 서로 다른 말을 해 commit 을
	// 거부했다는 뜻이다. 재시도로 풀릴 수 있으므로 terminal 이 아니다.
	ReasonEvidenceDisagreement = "EvidenceDisagreement"
	// 광고 붕괴 감시(monitorDrift) 사유 4종.
	ReasonAdvertisementDrift           = "AdvertisementDrift"
	ReasonAdvertisementDriftSuspected  = "AdvertisementDriftSuspected"
	ReasonAdvertisementConsistent      = "AdvertisementConsistent"
	ReasonAdvertisementCheckSuppressed = "AdvertisementCheckSuppressed"
)

// supportLevel
const (
	SupportVerified         = "Verified"
	SupportDocumented       = "Documented"
	SupportLegacyDocumented = "LegacyDocumented"
)

// migPhase (nvidia MIG apply journal, )
const (
	MigPhasePrepared     = "Prepared"
	MigPhaseLockAcquired = "LockAcquired"
	// MIG mode enable 전용 phase — GI 생성(Applying~) 이전 단계다. 하드웨어 mutation 은
	// -mig 1 뿐이고 GI 는 아직 없으므로 삭제 경로의 rollback 대상이 아니다(hardwareChanged 제외).
	MigPhaseQuiescing       = "Quiescing"
	MigPhaseModeEnabling    = "ModeEnabling"
	MigPhaseRebootRequested = "RebootRequested"
	MigPhaseRebootWaiting   = "RebootWaiting"

	MigPhaseApplying       = "Applying"
	MigPhaseApplied        = "Applied"
	MigPhaseRestartingDP   = "RestartingDevicePlugin"
	MigPhaseVerifying      = "Verifying"
	MigPhaseReady          = "Ready"
	MigPhaseRollingBack    = "RollingBack"
	MigPhaseRolledBack     = "RolledBack"
	MigPhaseCleanupBlocked = "CleanupBlocked"
)

// MigFinalizer 는 nvidia MIG apply 삭제 복원 보장 finalizer 다.
const MigFinalizer = "npu.ai/acpp-mig-cleanup"
