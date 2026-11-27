/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// ============================================================
// npuclusterpolicy_types.go: NPUClusterPolicy CRD 타입 정의
// 상세: Detector/Nvidia/Furiosa/Rebellions/Tenstorrent vendor spec 포함
// 생성일: 2025-01-01 | 수정일: 2026-10-24
// ============================================================

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type DetectorSpec struct {
	Image string `json:"image,omitempty"`
}

type FuriosaSpec struct {
	// 광고 주체 축(devicePlugin|dra).
	AdvertiseSpec `json:",inline"`
	// 관리 모드 축(managed|external). external 이면 warboy/rngd/unified 배포·회수 전부 건너뛴다.
	ManagementSpec    `json:",inline"`
	Enabled           bool              `json:"enabled"`
	DevicePluginImage string            `json:"devicePluginImage"`
	ConfigMapName     string            `json:"configMapName,omitempty"`
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
	// ExcludeNodeSelector 는 이 벤더의 배포 대상에서 뺄 노드를 고른다 — nodeSelector(양성 선택)는
	// 노드 하나를 빼려면 나머지 전부에 라벨이 필요해 노드가 늘수록 나빠지는데, 이 축은 반대
	// 방향이다. control-plane 배제는 이 필드와 무관하게 항상 걸린다(정책으로 끌 수 없다).
	// +optional
	ExcludeNodeSelector *metav1.LabelSelector `json:"excludeNodeSelector,omitempty"`
	Rngd                RngdSpec              `json:"rngd,omitempty"`
	// DRA 는 이 벤더의 DRA 드라이버 배포 여부다. 광고 주체(AdvertiseBy)와 독립이다.
	// +optional
	DRA *DRASpec `json:"dra,omitempty"`
	// Unified: true 면 Warboy/RNGD 를 하나의 통합 DaemonSet(동봉 바이너리 + PCI 감지 exec)으로
	// 배포한다. 기본 false = 기존 2-DS 경로. 통합 DS 는 자립 라벨(furiosa-family.present)로
	// 양 노드에 스케줄되며, 리소스명(beta.furiosa.ai/npu / furiosa.ai/rngd)은 바이너리가 광고한다.
	// +optional
	Unified bool `json:"unified,omitempty"`
	// UnifiedDevicePluginImage: 통합 DS 가 사용할 동봉 이미지. Unified=true 일 때만 사용.
	// +optional
	UnifiedDevicePluginImage string `json:"unifiedDevicePluginImage,omitempty"`
}

// RngdSpec defines the RNGD-specific (Furiosa RNGD NPU) device plugin configuration.
// Backward-compatible: all fields are omitempty; omit the whole block to keep legacy behavior.
type RngdSpec struct {
	// 광고 주체 축(devicePlugin|dra).
	AdvertiseSpec     `json:",inline"`
	Enabled           bool              `json:"enabled,omitempty"`
	DevicePluginImage string            `json:"devicePluginImage,omitempty"`
	ResourceName      string            `json:"resourceName,omitempty"` // default "furiosa.ai/rngd"
	ConfigMapName     string            `json:"configMapName,omitempty"`
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
	// ExcludeNodeSelector 는 이 벤더의 배포 대상에서 뺄 노드를 고른다. FuriosaSpec.ExcludeNodeSelector 참조.
	// +optional
	ExcludeNodeSelector *metav1.LabelSelector `json:"excludeNodeSelector,omitempty"`
	// PartitionPolicy: "none" (default, 1 instance/card), "single-core" (8), "dual-core" (4), "quad-core" (2).
	// Furiosa libfuriosa-kubernetes PartitioningPolicy 와 1:1 매핑.
	// +kubebuilder:validation:Enum=none;single-core;dual-core;quad-core
	// +optional
	PartitionPolicy string `json:"partitionPolicy,omitempty"`
	// DebugMode 는 device plugin 바이너리에 --debugMode 를 넘길지 정한다. 파티션을 지원하는 별도 빌드는
	// 이 flag 가 없으면 장치를 인식 못한다. 공개 이미지 docker.io/furiosaai/furiosa-device-plugin:2026.1.1
	// 은 이 flag 를 디버그 로그 옵션으로 받는다(--policy 는 unknown flag 로 거부). 기본값 nil=켠 것
	// (기존 유지), 차트는 공개 이미지에 false 를 명시한다.
	// +optional
	DebugMode *bool `json:"debugMode,omitempty"`
	// DRA 는 이 벤더의 DRA 드라이버 배포 여부다. 광고 주체(AdvertiseBy)와 독립이다.
	// +optional
	DRA *DRASpec `json:"dra,omitempty"`
}

type NvidiaSpec struct {
	// 광고 주체 축(devicePlugin|dra).
	AdvertiseSpec `json:",inline"`
	// 관리 모드 축(managed|external). external 이면 device-plugin 을 배포·회수하지 않는다.
	ManagementSpec    `json:",inline"`
	Enabled           bool              `json:"enabled"`
	DevicePluginImage string            `json:"devicePluginImage"`
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
	// ExcludeNodeSelector 는 이 벤더의 배포 대상에서 뺄 노드를 고른다. FuriosaSpec.ExcludeNodeSelector 참조.
	// +optional
	ExcludeNodeSelector *metav1.LabelSelector `json:"excludeNodeSelector,omitempty"`
	// DcgmExporter 는 NVIDIA GPU 텔레메트리(온도/사용률/메모리/전력) exporter 설정이다. NPU 4벤더는
	// hwmon 으로 수집하지만 NVIDIA 드라이버는 hwmon 을 등록하지 않아 NVML 경로의 dcgm-exporter 가
	// 필요하다. 미지정 또는 Enabled=false 면 배포하지 않고 기존 DS 가 있으면 제거한다.
	// +optional
	DcgmExporter *DcgmExporterSpec `json:"dcgmExporter,omitempty"`
	// DRA 는 이 벤더의 DRA 드라이버 배포 여부다. 광고 주체(AdvertiseBy)와 독립이다.
	// +optional
	DRA *DRASpec `json:"dra,omitempty"`
}

// DcgmExporterSpec 은 NVIDIA dcgm-exporter DaemonSet 배포 설정이다.
type DcgmExporterSpec struct {
	// Enabled=false(기본) 면 배포하지 않는다.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// Image 미지정 시 nvcr.io upstream 기본값을 사용한다(air-gap 환경은 미러 경로를 지정).
	// +optional
	Image string `json:"image,omitempty"`
}

// RebellionsSpec defines the Rebellions ATOM+ device plugin configuration.
// Backward-compatible: all fields are omitempty; omit the whole block to keep legacy behavior.
type RebellionsSpec struct {
	// 광고 주체 축(devicePlugin|dra).
	AdvertiseSpec `json:",inline"`
	// 관리 모드 축(managed|external). external 이면 device-plugin 을 배포·회수하지 않는다.
	ManagementSpec    `json:",inline"`
	Enabled           bool   `json:"enabled,omitempty"`
	DevicePluginImage string `json:"devicePluginImage"`
	ResourceName      string `json:"resourceName,omitempty"`   // default "ATOM"
	ResourcePrefix    string `json:"resourcePrefix,omitempty"` // default "rebellions.ai"
	Namespace         string `json:"namespace,omitempty"`      // default "kube-system"(벤더 기본값 rbln-system 과 다름)
	ConfigMapName     string `json:"configMapName,omitempty"`  // default "rbln-device-plugin-config"
	// DevicePluginGeneration 은 device plugin 이미지의 판 형식이다. v0.3 은 SR-IOV 기반 구판
	// (인자 --log-dir, /etc/pcidp ConfigMap), v0.4 는 rbln-device-plugin 신판(인자 없음, 환경변수)이다.
	// 비우면 DevicePluginImage 의 태그로 판별하고, 판별할 수 없으면 v0.3 으로 본다.
	// +kubebuilder:validation:Enum=v0.3;v0.4
	// +optional
	DevicePluginGeneration string            `json:"devicePluginGeneration,omitempty"`
	NodeSelector           map[string]string `json:"nodeSelector,omitempty"`
	// ExcludeNodeSelector 는 이 벤더의 배포 대상에서 뺄 노드를 고른다. FuriosaSpec.ExcludeNodeSelector 참조.
	// +optional
	ExcludeNodeSelector *metav1.LabelSelector `json:"excludeNodeSelector,omitempty"`
	// DRA 는 이 벤더의 DRA 드라이버 배포 여부다. 광고 주체(AdvertiseBy)와 독립이다.
	// +optional
	DRA *DRASpec `json:"dra,omitempty"`
}

// TenstorrentSpec defines the Tenstorrent Blackhole NPU device plugin configuration.
// Backward-compatible: all fields are omitempty; omit the whole block to keep legacy behavior.
type TenstorrentSpec struct {
	// 광고 주체 축(devicePlugin|dra).
	AdvertiseSpec `json:",inline"`
	// 관리 모드 축(managed|external). external 이면 device-plugin 을 배포·회수하지 않는다.
	ManagementSpec    `json:",inline"`
	Enabled           bool              `json:"enabled,omitempty"`
	DevicePluginImage string            `json:"devicePluginImage,omitempty"`
	ResourceName      string            `json:"resourceName,omitempty"` // default "tenstorrent.com/blackhole"
	NodeSelector      map[string]string `json:"nodeSelector,omitempty"`
	// ExcludeNodeSelector 는 이 벤더의 배포 대상에서 뺄 노드를 고른다. FuriosaSpec.ExcludeNodeSelector 참조.
	// +optional
	ExcludeNodeSelector *metav1.LabelSelector `json:"excludeNodeSelector,omitempty"`
	// DRA 는 이 벤더의 DRA 드라이버 배포 여부다. 광고 주체(AdvertiseBy)와 독립이다.
	// +optional
	DRA *DRASpec `json:"dra,omitempty"`
}

// NPUClusterPolicySpec defines the desired state of NPUClusterPolicy.
type NPUClusterPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Detector    *DetectorSpec   `json:"detector,omitempty"`
	Nvidia      NvidiaSpec      `json:"nvidia"`
	Furiosa     FuriosaSpec     `json:"furiosa"`
	Rebellions  RebellionsSpec  `json:"rebellions,omitempty"`
	Tenstorrent TenstorrentSpec `json:"tenstorrent,omitempty"`

	// ImagePullSecrets 는 operator 가 생성하는 자식 DaemonSet(detector/device-plugin) pod spec 과
	// ServiceAccount 에 부착되는 pull secret 목록입니다. helm `imagePullSecrets` 값이 CR 로 전파됩니다.
	// 미지정 시 빈 목록 — 노드레벨(containerd) 인증 경로를 유지하는 하위호환 동작입니다.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

// NPUClusterPolicy Condition 타입 상수
const (
	// ConditionDualAdvertisement 는 한 벤더의 device-plugin 과 DRA 가 같은 노드에서 같은
	// 물리 장치를 동시에 광고하는 창이 열려 있는지를 나타내는 Condition 타입이다. advertiseBy 를
	// devicePlugin 으로 되돌려도 그 벤더의 DRA 드라이버 발행이 아직 안 꺼졌으면 True 로 남는다 —
	// 전진 방향 전환(발행자 준비 → 스위치 적용) 중에는 잠깐 True 였다 꺼지는 것이 정상이다.
	ConditionDualAdvertisement = "DualAdvertisement"
	// ConditionRebellionsDevicePluginReady 는 Rebellions device plugin 파드의 사전 점검(rbln-smi·장치·
	// CDI 이름 파일)과 플러그인 기동 상태다. Rebellions 를 켜고 operator 가 배포하는 모드일 때만 둔다.
	ConditionRebellionsDevicePluginReady = "RebellionsDevicePluginReady"
)

// NPUClusterPolicyStatus defines the observed state of NPUClusterPolicy.
type NPUClusterPolicyStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// AdvertiseSwitch 는 광고 주체 전환이 노드마다 어떻게 됐는가다. 거절된 노드가 왜
	// 거절됐는지 여기서 읽는다 — 조용히 안 되는 것과 이유 있는 거절은 다르다.
	// +optional
	AdvertiseSwitch []AdvertiseSwitchStatus `json:"advertiseSwitch,omitempty"`
	// DRADrivers 는 벤더별 DRA 드라이버 배포 상태다.
	// +optional
	DRADrivers []DRADriverStatus `json:"draDrivers,omitempty"`
}

// AdvertiseSwitchStatus 는 관리 벤더의 장치 노드 하나에 대한 광고 주체 판정이다. 선언
// (Mode)과 관측이 같으면 Applied, 다르면 Reason 이 그 이유다 — devicePlugin 벤더의 노드도
// 항목을 가지므로 "항목 없음" 이 "광고 없음" 으로 읽히지 않는다.
type AdvertiseSwitchStatus struct {
	Vendor string `json:"vendor"`
	Node   string `json:"node"`
	// Mode 는 이 벤더가 선언한 광고 주체다.
	// +kubebuilder:validation:Enum=devicePlugin;dra
	// +optional
	Mode string `json:"mode,omitempty"`
	// Applied 는 이 노드의 광고 주체가 선언과 같은가다(dra: dra-owned 라벨 부여됨,
	// devicePlugin: dra-owned 라벨 없음).
	Applied bool `json:"applied"`
	// Reason 은 같지 않은 사유다(같으면 비어 있다).
	// +optional
	Reason string `json:"reason,omitempty"`
	// BlockingPods 는 장치를 쥐고 있어 전환을 막은 pod 들이다(namespace/name).
	// +optional
	BlockingPods []string `json:"blockingPods,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NPUClusterPolicy is the Schema for the npuclusterpolicies API.
type NPUClusterPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NPUClusterPolicySpec   `json:"spec,omitempty"`
	Status NPUClusterPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NPUClusterPolicyList contains a list of NPUClusterPolicy.
type NPUClusterPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NPUClusterPolicy `json:"items"`
}

// 광고 주체 — 같은 하드웨어를 device-plugin 과 DRA 가 동시에 광고하면 스케줄러가 장치 하나를
// 독립 자원 둘로 본다. 어느 쪽이 광고할지를 벤더(또는 벤더×노드) 단위로 선언한다.
const (
	AdvertiseByDevicePlugin = "devicePlugin"
	AdvertiseByDRA          = "dra"
)

// AdvertiseSpec 은 이 벤더 장치를 누가 광고하는가다. 벤더 블록에 inline 으로 들어간다.
type AdvertiseSpec struct {
	// AdvertiseBy 는 devicePlugin(기본) 또는 dra 다. dra 면 operator 가 대상 노드에
	// DRAOwnedNodeLabel 을 붙이고, 그 노드에서 이 벤더의 device-plugin DaemonSet 이 물러난다.
	// +kubebuilder:validation:Enum=devicePlugin;dra
	// +optional
	AdvertiseBy string `json:"advertiseBy,omitempty"`
	// AdvertiseByNodeSelector 는 dra 로 넘길 노드를 좁힌다. 미지정이면 그 벤더 장치가 있는
	// 모든 노드가 대상이다. DRA 드라이버가 일부 노드에만 깔린 과도기를 위한 축이다.
	// +optional
	AdvertiseByNodeSelector *metav1.LabelSelector `json:"advertiseByNodeSelector,omitempty"`
}

// AdvertisedBy 는 미지정을 devicePlugin 으로 해석한다. 이 필드를 모르는 기존 정책이
// 지금까지와 똑같이 동작해야 한다.
func (a AdvertiseSpec) AdvertisedBy() string {
	if a.AdvertiseBy == "" {
		return AdvertiseByDevicePlugin
	}
	return a.AdvertiseBy
}

// ManagementSpec 은 kcloud 가 이 벤더의 data plane 을 배포하는지(managed), 벤더
// operator 가 이미 배포를 맡고 있어 배포 없이 그 존재만 확인하는지(delegated), 아니면
// kcloud 밖에서 설치된 것을 노드만 관측하는지(external) 다. delegated·external 모두
// device-plugin/DRA 를 만들지도 지우지도 않는다. 장치 발견·라벨과 descriptor 중재는
// 그대로 관리 중으로 본다.
type ManagementSpec struct {
	// ManagementMode 는 managed(기본)·delegated·external 이다. managed 는 kcloud 가 벤더
	// operand 를 직접 배포, delegated 는 벤더 operator 가 이미 배포해 존재만 확인, external 은
	// 드라이버·런타임이 kcloud 밖에서 설치되어 노드만 관측한다. 미지정 시 managed.
	// +kubebuilder:validation:Enum=managed;delegated;external
	// +optional
	ManagementMode string `json:"managementMode,omitempty"`
}

// IsOperatorOwned 는 kcloud 가 배포 주체인지다. managed 일 때만 참이다. 배포 여부를
// 묻는 자리는 이 도우미 하나를 쓴다. 모드 문자열을 직접 비교하는 곳이 늘면 새 모드를
// 더할 때 그 자리를 모두 찾아야 한다.
func (m ManagementSpec) IsOperatorOwned() bool {
	return m.ManagementMode == "" || m.ManagementMode == "managed"
}

// IsDelegated 는 벤더 operator 에 위임한 상태인지다.
func (m ManagementSpec) IsDelegated() bool { return m.ManagementMode == "delegated" }

// IsExternal 은 이 벤더가 external 관리 모드인지 본다. 미지정(빈 값)은 managed 다.
func (m ManagementSpec) IsExternal() bool { return m.ManagementMode == "external" }

// DRASpec 은 이 벤더의 DRA 드라이버를 operator 가 배포할 것인가다. 벤더 블록에
// 포인터로 들어간다 — nil 이 기본이어야 이 필드를 모르는 기존 정책이 지금까지와
// 똑같이 동작한다.
type DRASpec struct {
	// Enabled 는 이 벤더의 DRA 드라이버를 배포할 것인가다. 기본 false.
	// 켜도 광고 주체는 바뀌지 않는다 — 그건 AdvertiseBy 의 몫이다.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// Image 는 드라이버 이미지를 덮는다. 비우면 렌더러 기본값.
	// +optional
	Image string `json:"image,omitempty"`
	// FabricManagerImage 는 Tenstorrent 전용이다. 이 벤더의 드라이버는 장치 발견을
	// 직접 하지 않고 같은 파드의 Fabric Manager agent 에 gRPC 로 맡기므로 이미지가
	// 둘이다. 다른 세 벤더는 이 값을 읽지 않는다. 비우면 렌더러 기본값.
	// +optional
	FabricManagerImage string `json:"fabricManagerImage,omitempty"`
	// Args 는 드라이버 컨테이너 인자를 덮는다. 비우면 렌더러 기본값.
	// +optional
	Args []string `json:"args,omitempty"`
	// Env 는 드라이버 컨테이너 환경변수에 더한다. 같은 이름이면 이쪽이 이긴다.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// IsEnabled 는 nil 수신자를 꺼짐으로 읽는다. 호출부마다 nil 검사를 반복하지 않기 위함이다.
func (d *DRASpec) IsEnabled() bool {
	return d != nil && d.Enabled
}

// DRA 드라이버 배포 상태. Ready 는 "파드가 떴다" 가 아니라 "발행물이 관측된다" 다 —
// 파드가 Running 이어도 ResourceSlice 가 없으면 DRA 는 쓸 수 없다.
const (
	DRAPhaseAbsent     = "Absent"
	DRAPhaseBlocked    = "Blocked"
	DRAPhaseInstalling = "Installing"
	DRAPhaseReady      = "Ready"
)

// DRADriverStatus 는 벤더 하나의 DRA 드라이버 배포 상태다.
type DRADriverStatus struct {
	Vendor string `json:"vendor"`
	// Phase 는 Absent | Blocked | Installing | Ready 다.
	Phase string `json:"phase"`
	// DriverName 은 발행물의 spec.driver 값이다(예: npu.furiosa.ai). 미확인이면 빈 값.
	// +optional
	DriverName string `json:"driverName,omitempty"`
	// PublishedNodes 는 이 드라이버가 ResourceSlice 를 발행 중인 노드 수다.
	// +optional
	PublishedNodes int32 `json:"publishedNodes,omitempty"`
	// Message 는 이 드라이버에 대해 사람이 알아야 할 사실이다. Blocked·Installing 에서는
	// 무엇이 없는지이고, Ready 에서도 반영하지 않은 설정이 있으면 그 통지가 남는다.
	// +optional
	Message string `json:"message,omitempty"`
}

// DRAOwnedNodeLabel 은 "이 노드의 이 벤더 장치는 DRA 가 광고한다" 를 나타내는 노드 라벨이다.
// operator 가 spec 에서 파생해 붙인다 — 사용자가 손으로 붙이는 라벨이 아니므로 spec 을
// 되돌리면 라벨도 사라진다.
func DRAOwnedNodeLabel(vendor string) string {
	return "kcloud.ai/" + vendor + ".dra-owned"
}

// DRARetiredNodeLabel 은 "이 노드는 이 벤더의 DRA 광고를 device-plugin 으로 되돌렸지만
// dra.enabled 는 아직 true 라 DRA 드라이버 파드가 물러나야 한다" 를 나타내는 노드 라벨이다.
// DRAOwnedNodeLabel 을 회수하는 그 reconcile 에서 operator 가 부여하고, 다시 DRA 로
// 돌아오거나 dra.enabled 를 false 로 내리면 회수한다(reconcileDRAOwnedLabels). furiosa·
// rngd 는 DRAOwnedNodeLabel 처럼 서로 다른 키를 쓴다.
func DRARetiredNodeLabel(vendor string) string {
	return "kcloud.ai/" + vendor + ".dra-retired"
}

// PresentNodeLabel 은 node-manager 가 "이 노드에 이 벤더 장치가 있다" 로 붙이는 자립 라벨
// 키다(NFD 비의존, 값은 "true"). furiosa 는 product 별로 furiosa(Warboy)·rngd 키를 따로
// 쓴다. 벤더 이름은 vendorcap 어휘(nvidia·furiosa·rngd·rebellions·tenstorrent)다.
// Go 코드에서 이 라벨 문자열의 단일 출처다(furiosa-family·accelerator 라벨은 벤더 라벨이
// 아니라 여기 속하지 않는다). deploy/helm/values.yaml 이 같은 라벨을 매니페스트마다 별도로
// 정의하므로, 라이브 실효값은 그 차트 값이 정한다 — npuclusterpolicy_controller.go 참고.
func PresentNodeLabel(vendor string) string {
	return "kcloud.ai/" + vendor + ".present"
}

// FuriosaFamilyPresentLabel 은 Warboy·RNGD 공통 자립 라벨이다. node-manager 가 PCI 0x1ed2 장치에
// 붙이고 통합 Furiosa DaemonSet 의 셀렉터가 된다(제품 라벨 furiosa.present·rngd.present 와 함께 붙는다).
const FuriosaFamilyPresentLabel = "kcloud.ai/furiosa-family.present"

// AcceleratorPresentLabel 은 벤더를 가리지 않는 "이 노드에 가속기가 있다" 라벨이다. node-manager 가
// 장치가 하나라도 있으면 벤더 라벨과 함께 붙인다. 옛 이름 kcloud.ai/gpu.present 는 NPU 노드에도
// 붙어 뜻이 어긋나 바꿨고, 0.8.x 동안은 둘을 함께 붙이며 0.9 에서 옛 이름을 뺀다.
const AcceleratorPresentLabel = "kcloud.ai/accelerator.present"
