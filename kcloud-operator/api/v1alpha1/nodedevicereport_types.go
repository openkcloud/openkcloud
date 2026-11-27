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
	// DriverBinding 은 해당 장치의 PCI 커널 드라이버 바인딩 상태입니다.
	// "nvidia"=드라이버 바인딩, "vfio-pci"=passthrough 바인딩, "none"=미바인딩(free). passthrough 감지용.
	DriverBinding string `json:"driverBinding,omitempty"`
	// GFD 소스 필드 — node-agent 가 스캔 시 sysfs/PCI 에서 best-effort 추출합니다(미가용 시 공란).
	MemoryMiB         int64  `json:"memoryMiB,omitempty"`         // 디바이스 메모리(MiB)
	ComputeCapability string `json:"computeCapability,omitempty"` // NVIDIA sm_xx / 벤더 capability 문자열
	PCIeAddress       string `json:"pcieAddress,omitempty"`       // 대표 PCI 주소(라벨/디버깅용)
	// MigModeCurrent/Pending 은 NVIDIA MIG 모드 관측값이다(detector per-PCI, ).
	// Disabled 만 apply 안전. Unknown(조회/파싱 실패) 또는 Enabled 는 apply 차단.
	MigModeCurrent string `json:"migModeCurrent,omitempty"`
	MigModePending string `json:"migModePending,omitempty"`
	// MigCurrentGeometry 는 현 GI 요약("1g.6gb x4"). ""/"disabled" = 없음. truthful Diff 진실 소스.
	MigCurrentGeometry string `json:"migCurrentGeometry,omitempty"`
	// MigObservationError 는 관측 실패 사유(fail-closed 근거). 비어있지 않으면 apply 차단.
	MigObservationError string `json:"migObservationError,omitempty"`
	FirmwareVersion     string `json:"firmwareVersion,omitempty"` // 가용 시 펌웨어/BIOS 버전
	// MigLgipOutput 은 `nvidia-smi mig -lgip` 원문입니다(MIG profile discovery 소스).
	// node-manager(detector) 가 채웁니다. NVIDIA 이외 벤더는 공란.
	MigLgipOutput string `json:"migLgipOutput,omitempty"`
	// DriverVersionOnDisk 는 설치된(디스크) 드라이버 버전 = 재부팅 후 로드될 버전입니다.
	// 로드된 버전(DriverVersion, /proc)과 다르면 대역외 드리프트입니다. detector 가 nvidia
	// dpkg 에서 채우며, CRD 스키마에 있어야 apiserver 가 detector 방출을 pruning 하지 않습니다.
	DriverVersionOnDisk string `json:"driverVersionOnDisk,omitempty"`
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
	// PassthroughReserved 는 노드에 GPU 가 존재하며 전량 vfio-pci(passthrough)에 바인딩된 경우 true 입니다.
	// 관리자가 passthrough 예약 노드를 식별하고 드라이버 설치 보류 판단을 보조합니다.
	PassthroughReserved bool `json:"passthroughReserved,omitempty"`
	// Validation 은 node-agent 가 상시/정기 실행하는 벤더별 검증 결과입니다.
	Validation *ValidationStatus `json:"validation,omitempty"`
	// ObservedAt 은 node-agent 가 이 보고서를 마지막으로 갱신한 시각입니다. 비어 있으면 "관측 시각
	// 미상" 이고, health 판정은 그 상태를 Healthy 로 승격하지 않습니다(모르는 것을 아는 것처럼
	// 다루지 않는다 — ).
	// +optional
	ObservedAt *metav1.Time `json:"observedAt,omitempty"`
	// Consumers 는 이 노드에서 장치를 쥔 컨테이너 목록이다(kubelet PodResources). nil 은 "모른다"
	// (수집 실패·미보고), 빈 목록은 "쥔 컨테이너가 없다" — 둘을 구분한다.
	// +optional
	Consumers []DeviceConsumer `json:"consumers,omitempty"`
	// ConsumersObservedAt 은 Consumers 를 읽은 시각이다. Consumers 가 nil 이 아니면 채운다.
	// +optional
	ConsumersObservedAt *metav1.Time `json:"consumersObservedAt,omitempty"`
	// ConsumersError 는 소켓을 읽지 못한 사유다. 채워져 있으면 Consumers 는 nil 이다.
	// +optional
	ConsumersError string `json:"consumersError,omitempty"`
}

// DeviceConsumer 는 kubelet PodResources API 가 낸, 장치 하나를 쥔 컨테이너 하나다. 추측하지 않는다.
type DeviceConsumer struct {
	// DeviceID 는 kubelet 이 낸 device_ids 원문이다(벤더 device-plugin 이 정한 문자열, PCI 가 아니다).
	DeviceID     string `json:"deviceID"`
	ResourceName string `json:"resourceName"`
	Namespace    string `json:"namespace"`
	Pod          string `json:"pod"`
	Container    string `json:"container"`
}

// ValidationStatus 는 node-agent Validation 능력의 노드 검증 결과입니다.
type ValidationStatus struct {
	// Passed 는 게이트 step 전부 통과 여부입니다(AC-1).
	Passed bool `json:"passed"`
	// LastRunTime 은 마지막 검증 시각입니다(node-agent 가 RFC3339 로 기록).
	LastRunTime metav1.Time `json:"lastRunTime,omitempty"`
	// Vendor 는 검증 대상 벤더입니다(멀티벤더 노드는 콤마 구분).
	Vendor string `json:"vendor,omitempty"`
	// Steps 는 step 별 결과입니다(AC-3: 실패 step 식별).
	Steps []ValidationStep `json:"steps,omitempty"`
}

// ValidationStep 은 5단계 각각의 결과입니다.
type ValidationStep struct {
	// Name 은 step 이름입니다("driverModule"|"deviceNode"|"devicePlugin"|"runtime"|"sampleWorkload").
	// 멀티벤더 노드는 "<vendor>/<step>" 접두사가 붙습니다.
	Name string `json:"name"`
	// Passed 는 해당 step 통과 여부입니다.
	Passed bool `json:"passed"`
	// NotApplicable 은 Passed 와 별개 축입니다 — 통과도 실패도 아닌 "해당없음" 상태를 표시합니다
	// (예: control-plane 배제 노드). 이 이름·json 태그는 detector(node-device-detector/types.go 의
	// ValidationStep)와 정확히 같아야 합니다 — 다르면 구조적 스키마가 그 값을 조용히 잘라냅니다.
	NotApplicable bool `json:"notApplicable,omitempty"`
	// Message 는 실패/보조 사유입니다(운영자 디버깅).
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
type NodeDeviceReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeDeviceReport `json:"items"`
}
