// ============================================================
// acceleratoroperation_types.go: AcceleratorOperation CRD 타입 (트랜잭션 단위)
// 상세: 장치 변경 한 건의 허가증·저널·펜싱 토큰이다. 실제 mutation 은
// participant 가 기존 적용 경로로 수행하고, 이 객체는 언제 해도 되는지와 어디까지 갔는지를 담는다.
// 생성일: 2026-08-01
// ============================================================
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// operation phase — operation 상태머신과 1:1.
const (
	OpPhasePending                = "Pending"
	OpPhasePlanning               = "Planning"
	OpPhasePrepared               = "Prepared"
	OpPhaseQuiescing              = "Quiescing"
	OpPhaseApplying               = "Applying"
	OpPhaseWaitingForReboot       = "WaitingForReboot"
	OpPhaseVerifying              = "Verifying"
	OpPhaseSucceeded              = "Succeeded"
	OpPhaseRollingBack            = "RollingBack"
	OpPhaseRolledBack             = "RolledBack"
	OpPhaseBlocked                = "Blocked"
	OpPhaseManualRecoveryRequired = "ManualRecoveryRequired"
)

// AllOperationPhases 는 CRD enum 과의 대조에 쓴다. 새 phase 를 추가하고 여기를 빠뜨리면
// 상태머신 테스트가 실패한다.
func AllOperationPhases() []string {
	return []string{
		OpPhasePending, OpPhasePlanning, OpPhasePrepared, OpPhaseQuiescing,
		OpPhaseApplying, OpPhaseWaitingForReboot, OpPhaseVerifying, OpPhaseSucceeded,
		OpPhaseRollingBack, OpPhaseRolledBack, OpPhaseBlocked, OpPhaseManualRecoveryRequired,
	}
}

// OperationOwner 는 이 operation 을 요청한 객체다. UID 까지 담는 이유는 같은 이름으로 재생성된
// 정책의 operation 을 옛 정책의 것으로 오인하지 않기 위해서다(ApplyRecord.OwnerUID 와 같은 규율).
type OperationOwner struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// +optional
	UID string `json:"uid,omitempty"`
	// +optional
	Generation int64 `json:"generation,omitempty"`
}

type AcceleratorOperationSpec struct {
	// Type 은 internal/operation 의 operation 타입 11종 중 하나다.
	// +kubebuilder:validation:Enum=DriverInstall;DriverUpgrade;DriverRollback;PartitionReconfigure;SharingModeChange;DevicePluginRestart;NodeReboot;Revalidate;Quarantine;RecoverDevice;DeviceReset
	Type     string `json:"type"`
	NodeName string `json:"nodeName"`
	// +kubebuilder:validation:Enum=nvidia;furiosa;rebellions;tenstorrent
	// +optional
	Vendor string `json:"vendor,omitempty"`
	// TransactionID 는 요청 정체성이다. 같은 의도의 재요청은 같은 값을 가져 중복 operation 을
	// 막는다. 빈 값을 금지하는 이유: DeviceReset 의 Job 이름이 이 값을 해시에 담는데, 비면
	// 이름이 (노드,벤더)로 붕괴해 앞선 작업의 성공 Job 을 자기 성공으로 오인한다.
	// +kubebuilder:validation:MinLength=1
	TransactionID string         `json:"transactionID"`
	Owner         OperationOwner `json:"owner"`
	// ResourceKeys 는 이 작업이 읽고 쓰는 자원이다(internal/operation 의 키 생성자 산출물).
	// 비어 있으면 충돌 판정이 아무것도 못 찾으므로 읽기 전용 작업이 아닌 한 비우지 않는다.
	// +optional
	ResourceKeys []string `json:"resourceKeys,omitempty"`
}

// OperationJournalEntry 는 write-ahead 저널의 한 줄이다. 되돌릴 수 없는 행동을 하기 **전에**
// 기록하며, Epoch 이 함께 박혀 있어 옛 프로세스가 남긴 줄을 구분할 수 있다.
type OperationJournalEntry struct {
	Step  string      `json:"step"`
	Epoch int64       `json:"epoch"`
	At    metav1.Time `json:"at"`
	// +optional
	Detail string `json:"detail,omitempty"`
}

// OperationSnapshot 은 mutation 직전 상태다. 보상 rollback 의 목표이자 크래시 복구의 대조군이다.
type OperationSnapshot struct {
	// +optional
	Allocatable map[string]int32 `json:"allocatable,omitempty"`
	// Geometry 는 PCI → 관측 geometry 요약이다.
	// +optional
	Geometry map[string]string `json:"geometry,omitempty"`
	// +optional
	MigPhase string `json:"migPhase,omitempty"`
	// +optional
	BootID string `json:"bootID,omitempty"`
	// +optional
	CordonedByPolicy bool `json:"cordonedByPolicy,omitempty"`
}

type AcceleratorOperationStatus struct {
	// +kubebuilder:validation:Enum=Pending;Planning;Prepared;Quiescing;Applying;WaitingForReboot;Verifying;Succeeded;RollingBack;RolledBack;Blocked;ManualRecoveryRequired
	// +optional
	Phase string `json:"phase,omitempty"`
	// Epoch 은 Lease 를 잡을 때마다 증가한다. 이 값보다 낮은 epoch 의 결과는 받아들이지 않는다.
	// +optional
	Epoch int64 `json:"epoch,omitempty"`
	// +optional
	LeaseHolder string `json:"leaseHolder,omitempty"`
	// +optional
	LeaseExpiry *metav1.Time `json:"leaseExpiry,omitempty"`
	// +optional
	Journal []OperationJournalEntry `json:"journal,omitempty"`
	// +optional
	Snapshot *OperationSnapshot `json:"snapshot,omitempty"`
	// BlockedBy 는 이 operation 을 막고 있는 다른 operation 의 이름이다.
	// +optional
	BlockedBy string `json:"blockedBy,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	RollbackAttempts int32 `json:"rollbackAttempts,omitempty"`
	// ObserveFailures 는 복구 판정을 위한 장치 관측이 연속으로 실패한 횟수다. 상한을 넘으면
	// 수동 복구로 보낸다 — 관측 없이 rollback 을 실행하는 것이 가장 위험한 행동이기 때문이다.
	// +optional
	ObserveFailures int32 `json:"observeFailures,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=aop
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TYPE",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="NODE",type=string,JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="PHASE",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="EPOCH",type=integer,JSONPath=".status.epoch"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
type AcceleratorOperation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorOperationSpec   `json:"spec,omitempty"`
	Status            AcceleratorOperationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AcceleratorOperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorOperation `json:"items"`
}
