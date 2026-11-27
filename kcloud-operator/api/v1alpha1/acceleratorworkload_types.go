// ============================================================
// acceleratorworkload_types.go: AcceleratorWorkload CRD 타입 (추상 사용자 API)
// 상세: 사용자는 장치가 아니라 의도(exclusive|shared|partitioned|
// partitioned-shared)를 쓰고, operator 가 벤더 리소스로 번역한다. 지원되지 않는 조합은 축을
// 지목해 거절한다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// 사용자 추상 접근 모드.
const (
	AccessModeExclusive         = "exclusive"
	AccessModeShared            = "shared"
	AccessModePartitioned       = "partitioned"
	AccessModePartitionedShared = "partitioned-shared"
)

// 공유 실현 수단. SharingCapability 의 세 필드와 1:1 이다.
// (노출 계층인 allocationAPI 와 다른 축이다 — 헷갈리지 말 것.)
const (
	ImplementationAuto         = "auto"
	ImplementationTimeSlicing  = "timeSlicing"
	ImplementationMultiProcess = "multiProcess"
	ImplementationBrokered     = "brokered"
)

// AcceleratorWorkload phase. 번역 전 상태를 따로 두지 않는다 — 컨트롤러가 도는 순간
// 결과는 Translated 아니면 Rejected 다.
const (
	AWPhaseTranslated = "Translated"
	AWPhaseRunning    = "Running"
	AWPhaseRejected   = "Rejected"
)

// condition type.
const (
	AWCondTranslated    = "Translated"
	AWCondWorkloadReady = "WorkloadReady"
)

// reason. 거절 사유는 실패한 축을 사람이 알아볼 수 있게 이름 짓는다.
const (
	AWReasonClassNotFound        = "ClassNotFound"
	AWReasonBackendUnsupported   = "BackendUnsupported"
	AWReasonCapabilityUnverified = "CapabilityUnverified"
	AWReasonNoCandidateNodes     = "NoCandidateNodes"
	AWReasonNoVendorMapping      = "NoVendorMapping"
	AWReasonIsolationTooWeak     = "IsolationTooWeak"
	AWReasonMemoryUnknown        = "MemoryUnknown"
	AWReasonMemoryTooSmall       = "MemoryTooSmall"
	AWReasonProfileNotApplied    = "ProfileNotApplied"
	AWReasonReplicasExceedMax    = "ReplicasExceedMax"
	// AWReasonNodeCordoned: 노드가 cordon(unschedulable) 되어 있다. NoCandidateNodes 와 구분하는
	// 이유는 고칠 방법이 다르기 때문이다 — uncordon 이지 장치/플러그인 문제가 아니다.
	AWReasonNodeCordoned = "NodeCordoned"
	// AWReasonReplicasTooFew: 공유 계열 모드인데 access.replicas 가 2 미만이다.
	// 백엔드 한계가 아니라 사용자 입력 문제이므로 BackendUnsupported 와 구분한다.
	AWReasonReplicasTooFew = "ReplicasTooFew"
	AWReasonDRANotEnabled  = "DRANotEnabled"
	// AWReasonDRAMappingMissing: 클러스터는 DRA 를 서빙하지만 이 AcceleratorClass 가
	// 그 벤더의 DeviceClass 를 선언하지 않는다. 고칠 곳은 클러스터가 아니라 클래스다.
	AWReasonDRAMappingMissing = "DRAMappingMissing"
	// AWReasonDRADriverMissing: 클래스가 가리키는 DeviceClass 가 실재하지 않는다.
	// 벤더 DRA 드라이버가 설치되지 않았다는 뜻이다.
	AWReasonDRADriverMissing = "DRADriverMissing"
	// AWReasonDRAUnsupportedRequest: 클러스터도 드라이버도 멀쩡하지만 DRA 경로가 그 요청의
	// 축(공유·분할·requirements)을 아직 구현하지 않는다. 클러스터 탓이 아니므로
	// BackendUnsupported/CapabilityUnverified 와 구분한다 — 고치는 방법은 allocationAPI 를
	// devicePlugin 으로 돌리는 것이지 장치나 드라이버를 손보는 것이 아니다.
	AWReasonDRAUnsupportedRequest = "DRAUnsupportedRequest"
	// AWReasonDRADoubleAdvertised: DRA 장치를 내놓는 노드가 같은 벤더를 device-plugin 으로도
	// 광고하고 있어 후보에서 뺐다(스케줄러가 카드 1장을 독립 자원 2개로 본다). NoCandidateNodes
	// 와 구분하는 이유는 그 메시지가 사실과 반대로 읽히기 때문이다 — 장치도 드라이버도 멀쩡한데
	// "아무 노드도 장치를 안 낸다" 를 받은 사용자는 멀쩡한 DRA 드라이버를 다시 깐다.
	// 고칠 곳은 그 노드의 device-plugin 광고다.
	AWReasonDRADoubleAdvertised = "DRADoubleAdvertised"
	AWReasonSharingNotApplied   = "SharingNotApplied"
	AWReasonDeviceShared        = "DeviceShared"
	AWReasonTranslated          = "Translated"
	AWReasonWorkloadReady       = "WorkloadReady"
	AWReasonWorkloadNotReady    = "WorkloadNotReady"
	// AWReasonDeploymentConflict: 같은 이름의 Deployment 가 이미 있는데 이 CR 소유가 아니다.
	// 번역은 성공했지만 워크로드를 실현할 수 없다 — 남의 것을 뺏지 않고 사유만 남긴다.
	AWReasonDeploymentConflict = "DeploymentConflict"
	// AWReasonDeploymentInvalid: Deployment 생성/수정을 API server 가 재시도해도 소용없는 사유로
	// 거절했다(Invalid/Forbidden/BadRequest — 잘못된 리소스명, 권한, quota, terminating namespace).
	// 백오프로 영영 재시도하며 status 를 비워 두지 않고 사유를 CR 에 남긴다.
	AWReasonDeploymentInvalid = "DeploymentInvalid"
	// AWReasonResourceClaimTemplateDrift: 이미 있는 ResourceClaimTemplate 이 청구하는 DeviceClass 가
	// 지금 번역이 확정한 것과 다르다. spec 이 불변이라 operator 가 고칠 수 없고, 지웠다 다시 만들면
	// 그 템플릿으로 뜬 Pod 의 claim 이 끊긴다 — 조용히 옛 클래스로 계속 도는 대신 사유를 남긴다.
	AWReasonResourceClaimTemplateDrift = "ResourceClaimTemplateDrift"
)

// AccessSpec 는 장치를 어떻게 쓸 것인가다.
type AccessSpec struct {
	// +kubebuilder:validation:Enum=exclusive;shared;partitioned;partitioned-shared
	Mode string `json:"mode"`
	// Implementation 은 공유를 무엇으로 실현할지다. auto 면 장치가 verified 로 보고한
	// 방식 중 timeSlicing → multiProcess → brokered 순으로 고른다.
	// +kubebuilder:validation:Enum=auto;timeSlicing;multiProcess;brokered
	// +kubebuilder:default=auto
	// +optional
	Implementation string `json:"implementation,omitempty"`
	// Replicas 는 장치 하나를 몇 갈래로 나눠 쓰는가다 — 광고 배수이지 성능 비율이 아니다.
	// spec.workload.replicas(Pod 개수)와 전혀 다른 값이다. 공유 계열 모드에서 2 이상이어야 한다.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=16
	// +optional
	Replicas int32 `json:"replicas,omitempty"`
}

// AcceleratorPreferences 는 선호이지 요구가 아니다 — 만족하는 것이 없으면 거절이 아니라 다음 후보로 넘어간다.
type AcceleratorPreferences struct {
	// Vendors 는 선호 벤더 순서다. 클래스 mappings 에 없는 벤더는 무시된다.
	// +optional
	Vendors []string `json:"vendors,omitempty"`
	// AllocationAPI 는 노출 계층이다. dra 가용 여부는 하드코딩이 아니라
	// DeviceClass·ResourceSlice 실측으로 판정한다(internal/intent/dra.go).
	// +kubebuilder:validation:Enum=auto;devicePlugin;dra
	// +kubebuilder:default=auto
	// +optional
	AllocationAPI string `json:"allocationAPI,omitempty"`
}

// AcceleratorRequest 는 장치 요구다.
type AcceleratorRequest struct {
	// Class 는 AcceleratorClass(cluster-scoped) 이름이다.
	Class  string     `json:"class"`
	Access AccessSpec `json:"access"`
	// Requirements 는 클래스 요구사항을 완화하지 않는다 — 클래스와 이 값 중 강한 쪽이 이긴다.
	// +optional
	Requirements *AcceleratorRequirements `json:"requirements,omitempty"`
	// +optional
	Preferences *AcceleratorPreferences `json:"preferences,omitempty"`
}

// WorkloadTemplate 은 실행할 컨테이너다. operator 가 이것으로 Deployment 하나를 만든다.
type WorkloadTemplate struct {
	Image string `json:"image"`
	// Replicas 는 Pod 개수다(access.replicas 와 다르다).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`
	// +optional
	Command []string `json:"command,omitempty"`
	// +optional
	Args []string `json:"args,omitempty"`
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// AcceleratorWorkloadSpec 는 장치와 무관한 사용자 YAML 이다.
type AcceleratorWorkloadSpec struct {
	Accelerator AcceleratorRequest `json:"accelerator"`
	Workload    WorkloadTemplate   `json:"workload"`
}

// ResolvedAllocation 은 번역 결과다 — 사용자가 쓴 추상 의도가 무엇으로 바뀌었는지 그대로 보여준다.
type ResolvedAllocation struct {
	// +optional
	Vendor string `json:"vendor,omitempty"`
	// +optional
	Mode string `json:"mode,omitempty"`
	// ResourceName 은 Pod 이 실제로 요청하는 extended resource 다.
	// +optional
	ResourceName string `json:"resourceName,omitempty"`
	// DeviceClassName 은 DRA 경로에서 ResourceClaimTemplate 이 참조할 DeviceClass 다.
	// allocationAPI=devicePlugin 이면 비어 있고, dra 면 ResourceName 이 비고 이쪽이 찬다.
	// +optional
	DeviceClassName string `json:"deviceClassName,omitempty"`
	// +optional
	Quantity int32 `json:"quantity,omitempty"`
	// +optional
	AllocationAPI string `json:"allocationAPI,omitempty"`
	// Nodes 는 이 번역이 성립하는 노드 후보다(nodeAffinity 로 박힌다).
	// +optional
	Nodes []string `json:"nodes,omitempty"`
	// Explanation 은 사람이 읽는 근거다. 공유 모드면 replica 사이에 격리가 없다는 사실을 적는다.
	// +optional
	Explanation string `json:"explanation,omitempty"`
}

// WorkloadRejection 은 사용자가 고칠 수 있는 형태의 거절이다. 축을 condition message 에
// 접어 넣고 정규식으로 되짚는 왕복을 없앤다.
type WorkloadRejection struct {
	// +optional
	Axis string `json:"axis,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

type AcceleratorWorkloadStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	Resolved *ResolvedAllocation `json:"resolved,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// Rejection 은 번역이 거절됐을 때의 축·사유다. Translated=False 와 함께 채워진다.
	// 구버전 operator 가 쓴 객체에는 없으므로 소비자는 condition message 폴백을 유지한다.
	// +optional
	Rejection *WorkloadRejection `json:"rejection,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=aw
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="CLASS",type=string,JSONPath=".spec.accelerator.class"
// +kubebuilder:printcolumn:name="MODE",type=string,JSONPath=".spec.accelerator.access.mode"
// +kubebuilder:printcolumn:name="RESOURCE",type=string,JSONPath=".status.resolved.resourceName"
// +kubebuilder:printcolumn:name="PHASE",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
type AcceleratorWorkload struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorWorkloadSpec   `json:"spec,omitempty"`
	Status            AcceleratorWorkloadStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AcceleratorWorkloadList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorWorkload `json:"items"`
}
