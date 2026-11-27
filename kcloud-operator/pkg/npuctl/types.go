// types.go: pkg/npuctl 관리 API 1단계 집약 상태 스키마
// 상세: kubectl-npu status --json 및 향후 internal/apiserver(2단계)가 그대로 재사용할
//
//	안정 필드명의 JSON 계약. 필드는 모두 omitempty로 두어 벤더/리소스가 없을 때 생략된다.
//
// 생성일: 2026-07-16 | 수정일: 2026-10-27
package npuctl

import "kcloud-operator/internal/health"

// 벤더 식별자 상수. NPUClusterPolicy.spec의 최상위 키와 1:1 대응하며(rngd는 furiosa의 서브벤더),
// status.go/control.go에서 반복 사용되는 리터럴을 한 곳에서 관리한다.
const (
	VendorNvidia      = "nvidia"
	VendorFuriosa     = "furiosa"
	VendorRngd        = "rngd"
	VendorRebellions  = "rebellions"
	VendorTenstorrent = "tenstorrent"
)

// HealthStateHealthy 는 AcceleratorHealth.status.state(및 devices[].state)의 "정상" 값
// (internal/health.StateHealthy 그대로)이다. cmd/kubectl-npu 는 별도 모듈이라
// internal/health 를 직접 import 할 수 없으므로 이 패키지가 중개한다 —
// vendorcap.go 가 internal/vendorcap 을 중개하는 것과 같은 방식.
const HealthStateHealthy = health.StateHealthy

// ClusterStatus는 한 번의 조회로 얻는 노드×벤더 집약 상태입니다.
type ClusterStatus struct {
	// Vendors는 NPUClusterPolicy.spec 기준 벤더별 enabled 여부 + 해당 벤더의 DriverInstallPolicy 목록입니다.
	Vendors []VendorStatus `json:"vendors,omitempty"`
	// Nodes는 노드별 디바이스(NDR)/allocatable/업그레이드 상태(DUS)입니다.
	Nodes []NodeStatus `json:"nodes,omitempty"`
	// ClusterPolicies는 조회된 NPUClusterPolicy 원본 목록(namespace/name/phase)입니다.
	ClusterPolicies []ClusterPolicyStatus `json:"clusterPolicies,omitempty"`
}

// VendorStatus는 벤더 하나의 활성화 여부와 소속 DriverInstallPolicy 목록입니다.
// Vendor 값은 NPUClusterPolicy.spec의 최상위 키(nvidia/furiosa/rebellions/tenstorrent)이며,
// furiosa의 하위 rngd는 별도 Name="rngd"(SubVendorOf="furiosa")로 표현합니다.
type VendorStatus struct {
	// Name은 벤더 식별자입니다: nvidia | furiosa | rngd | rebellions | tenstorrent.
	Name string `json:"name"`
	// SubVendorOf는 name이 하위 벤더(rngd 등)일 때 상위 벤더명입니다. 최상위 벤더는 비워둡니다.
	SubVendorOf string `json:"subVendorOf,omitempty"`
	// Enabled는 NPUClusterPolicy.spec.<vendor>.enabled 값입니다.
	Enabled bool `json:"enabled"`
	// DevicePluginImage는 NPUClusterPolicy.spec.<vendor>.devicePluginImage 입니다.
	DevicePluginImage string `json:"devicePluginImage,omitempty"`
	// DriverInstallPolicies는 spec.vendor(또는 rngd의 spec.model=="rngd")가 이 벤더와 일치하는 DIP 목록입니다.
	DriverInstallPolicies []DIPStatus `json:"driverInstallPolicies,omitempty"`
}

// DIPStatus는 DriverInstallPolicy의 관리툴 관심 필드입니다(목표 버전/검증 버전 화이트리스트 등).
// DesiredVersion 은 정책이 선언한 버전이다. versionSource=Host 면 노드별 목표는
// Nodes[].Upgrades[].DesiredVersion 이다.
type DIPStatus struct {
	Name             string   `json:"name"`
	Vendor           string   `json:"vendor"`
	Model            string   `json:"model,omitempty"`
	DesiredVersion   string   `json:"desiredVersion,omitempty"`
	Installer        string   `json:"installer,omitempty"`
	VerifiedVersions []string `json:"verifiedVersions,omitempty"`
	AllowDowngrade   bool     `json:"allowDowngrade,omitempty"`
	VersionSource    string   `json:"versionSource,omitempty"`
	AutoUpgrade      bool     `json:"autoUpgrade,omitempty"`
	DrainEnabled     bool     `json:"drainEnabled,omitempty"`
	// TrackOnly 는 설치 경로 없이 버전만 추적하는 정책이다(DriverSpec.TrackOnly). upgrade·rollback 의 대상이 아니다.
	TrackOnly bool `json:"trackOnly,omitempty"`
}

// NodeStatus는 노드 하나의 디바이스/allocatable/업그레이드 상태 요약입니다.
type NodeStatus struct {
	Name string `json:"name"`
	// Excluded 는 이 노드가 배포 대상에서 제외됐는지다. operator 가 붙인
	// kcloud.ai/excluded 라벨을 그대로 옮긴다 — 여기서 다시 판정하지 않는다.
	Excluded bool `json:"excluded,omitempty"`
	// ExcludedReason 은 고정 어휘다: control-plane | policy
	ExcludedReason string `json:"excludedReason,omitempty"`
	// Devices는 NodeDeviceReport.status.devices 입니다.
	Devices []DeviceStatus `json:"devices,omitempty"`
	// PassthroughReserved는 NodeDeviceReport.status.passthroughReserved 입니다(전량 vfio-pci 바인딩 시 true).
	PassthroughReserved bool `json:"passthroughReserved,omitempty"`
	// Allocatable은 node.status.allocatable 전체(quantity 문자열)입니다. 벤더별 리소스명이
	// (furiosa.ai/rngd, nvidia.com/gpu, tenstorrent.com/blackhole, rebellions.ai/ATOM 등) 설정에 따라
	// 달라지므로 필터링하지 않고 그대로 노출합니다.
	Allocatable map[string]string `json:"allocatable,omitempty"`
	// Upgrades는 이 노드에 속한 DriverUpgradeState 목록(벤더별 1개)입니다.
	Upgrades []UpgradeStatus `json:"upgrades,omitempty"`
}

// DeviceStatus는 NodeDeviceReport.status.devices의 한 항목입니다.
type DeviceStatus struct {
	Vendor        string `json:"vendor,omitempty"`
	Model         string `json:"model,omitempty"`
	Count         int32  `json:"count,omitempty"`
	DriverLoaded  bool   `json:"driverLoaded,omitempty"`
	DriverVersion string `json:"driverVersion,omitempty"`
	// DriverBinding: "nvidia" 등 드라이버 바인딩 | "vfio-pci"(passthrough) | "none"(미바인딩).
	DriverBinding string `json:"driverBinding,omitempty"`
	NeedsReboot   bool   `json:"needsReboot,omitempty"`
	// PCIeAddress 는 NodeDeviceReport 의 대표 PCI 주소다. 같은 모델 여러 장을
	// 사람이 구분하는 유일한 축이므로 화면·CLI 가 이것으로 행을 가른다.
	PCIeAddress string `json:"pcieAddress,omitempty"`
	// Product 는 "<vendor>/<model>" 표시명이다(예: nvidia/a30). model 이
	// 비면 "<vendor>/generic" 이 되어 모른다는 사실이 화면에 남는다.
	Product string `json:"product,omitempty"`
}

// UpgradeStatus는 DriverUpgradeState 한 건입니다.
type UpgradeStatus struct {
	Name           string `json:"name"`
	Vendor         string `json:"vendor"`
	Model          string `json:"model,omitempty"`
	State          string `json:"state,omitempty"`
	CurrentVersion string `json:"currentVersion,omitempty"`
	DesiredVersion string `json:"desiredVersion,omitempty"`
	Message        string `json:"message,omitempty"`
	Retries        int32  `json:"retries,omitempty"`
	// RolledBackVersion 은 롤백한 목표 버전이다(DUS status.rolledBackVersion). State 가 Idle 일 때만 재시도 보류를 뜻한다.
	RolledBackVersion string `json:"rolledBackVersion,omitempty"`
	// Models 는 DriverUpgradeState.Status.ObservedModels 를 그대로 옮긴 값이다 — Model 이
	// generic 으로 뭉개진 혼재 노드에서 실제 섞인 제품을 CLI 에서 보기 위해서다.
	Models []string `json:"models,omitempty"`
}

// VendorConditionStatus 는 NPUClusterPolicy 의 벤더별 조건(<Vendor>OperatorReady·<Vendor>DevicePluginReady)
// 하나를 옮긴 것이다. 판정은 컨트롤러가 이미 내렸고 이 패키지는 옮기기만 한다.
type VendorConditionStatus struct {
	Vendor  string `json:"vendor"`
	Status  string `json:"status"` // "True"|"False"|"Unknown"
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// VendorOperatorStatus 는 VendorConditionStatus 의 종전 이름이다 — 호출부와 JSON 스키마 이름을 유지한다.
type VendorOperatorStatus = VendorConditionStatus

// ClusterPolicyStatus는 NPUClusterPolicy 하나의 조회 결과(관측 상태)입니다.
type ClusterPolicyStatus struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Phase/Ready는 status.phase 및 status.conditions[type=Ready] 요약입니다.
	Phase        string `json:"phase,omitempty"`
	Ready        string `json:"ready,omitempty"` // "True"|"False"|"Unknown"|"" (조건 없음)
	ReadyReason  string `json:"readyReason,omitempty"`
	ReadyMessage string `json:"readyMessage,omitempty"`
	// VendorOperators 는 <Vendor>OperatorReady 조건 목록이다(벤더 이름 오름차순).
	VendorOperators []VendorOperatorStatus `json:"vendorOperators,omitempty"`
	// DevicePlugins 는 <Vendor>DevicePluginReady 조건 목록이다(벤더 이름 오름차순). kcloud 가 직접 배포한
	// device plugin 의 준비 상태로, 외부 벤더 operator 감지인 VendorOperators 와 다르다.
	DevicePlugins []VendorConditionStatus `json:"devicePlugins,omitempty"`
}

// NodeDetail 은 노드 하나의 상세다. 여기 실리는 값은 전부 다른 컨트롤러가 이미 판정해
// CRD 에 적어 둔 것이다 — 이 패키지는 옮기기만 하고 다시 판정하지 않는다.
type NodeDetail struct {
	Name           string            `json:"name"`
	Excluded       bool              `json:"excluded,omitempty"`
	ExcludedReason string            `json:"excludedReason,omitempty"`
	Devices        []DeviceStatus    `json:"devices,omitempty"`
	Allocatable    map[string]string `json:"allocatable,omitempty"`
	Health         *HealthView       `json:"health,omitempty"`
	Evidence       []EvidenceView    `json:"evidence,omitempty"`
	Policies       []PolicyView      `json:"policies,omitempty"`
	// Consumers 는 NodeDeviceReport status.consumers(kubelet PodResources 수집)의 사본이다.
	// nil 은 모름(수집 없음·실패), 빈 목록은 쥔 컨테이너 없음 — ConsumersObservedAt 유무로 구분한다.
	Consumers           []ConsumerView `json:"consumers,omitempty"`
	ConsumersObservedAt string         `json:"consumersObservedAt,omitempty"`
	ConsumersError      string         `json:"consumersError,omitempty"`
}

// ConsumerView 는 NodeDeviceReport status.consumers 의 한 항목이다 — 장치 하나를 쥔 컨테이너 하나.
// internal/apiserver.ConsumerView(파드 하나, /api/v1/consumers)와 이름만 같고 단위가 다르다.
type ConsumerView struct {
	Namespace    string `json:"namespace"`
	Pod          string `json:"pod"`
	Container    string `json:"container"`
	ResourceName string `json:"resourceName"`
	DeviceID     string `json:"deviceID"`
}

// HealthView 는 AcceleratorHealth.status 의 표시용 사본이다.
type HealthView struct {
	State             string             `json:"state,omitempty"`
	Reason            string             `json:"reason,omitempty"`
	AllocationAllowed bool               `json:"allocationAllowed,omitempty"`
	Devices           []DeviceHealthView `json:"devices,omitempty"`
}

// DeviceHealthView 는 AcceleratorHealth.status.devices 의 한 항목이다.
type DeviceHealthView struct {
	PCIAddress string `json:"pciAddress,omitempty"`
	State      string `json:"state,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// EvidenceView 는 AcceleratorEvidence 의 표시용 사본이다. 벤더별로 여러 건일 수 있다.
type EvidenceView struct {
	Vendor    string `json:"vendor,omitempty"`
	Level     string `json:"level,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// PolicyView 는 이 노드를 대상으로 삼은 AcceleratorPartitionPolicy 다.
type PolicyView struct {
	Name   string `json:"name,omitempty"`
	Vendor string `json:"vendor,omitempty"`
	Phase  string `json:"phase,omitempty"`
}
