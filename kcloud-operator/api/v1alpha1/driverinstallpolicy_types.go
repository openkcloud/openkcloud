// ============================================================
// driverinstallpolicy_types.go: DriverInstallPolicy CRD 타입
// 상세: 벤더 하나의 드라이버를 어떤 이미지·버전·방식으로 깔지 선언한다.
// 노드별 전이는 DriverUpgradeState 가 맡고 이 타입은 그 전이가 따를 정책이다.
// 생성일: 2026-04-10 | 수정일: 2026-10-27
// ============================================================
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// 드라이버 설치 방식. ngc 만 이미지 자체가 드라이버 버전을 담는다 — 나머지는 이미지가
// 버전과 무관하고 DRIVER_VERSION 환경 변수로 설치 버전이 정해진다.
const (
	DriverInstallerAPT    = "apt"
	DriverInstallerNGC    = "ngc"
	DriverInstallerScript = "script"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=dip
// DriverInstallPolicy는 노드의 장치 드라이버/툴킷 설치 정책을 정의합니다.
// 예) Vendor: "furiosa"/"nvidia", Model: "warboy"/"generic"(또는 GPU 세대명)
type DriverInstallPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec DriverInstallPolicySpec `json:"spec,omitempty"`
}

// UpgradePolicy defines the upgrade behavior for driver installation
type UpgradePolicy struct {
	// AutoUpgrade enables automatic upgrade when version mismatch is detected
	AutoUpgrade bool `json:"autoUpgrade,omitempty"`
	// DrainEnabled enables cordon+drain before driver upgrade on nodes with active GPU workloads
	DrainEnabled bool `json:"drainEnabled,omitempty"`
	// ForceUpgrade allows drain --force when pods cannot be evicted gracefully
	ForceUpgrade bool `json:"forceUpgrade,omitempty"`
	// MaxUnavailable is the max number of nodes being upgraded simultaneously
	// +kubebuilder:default=1
	MaxUnavailable int32 `json:"maxUnavailable,omitempty"`
	// MaxParallelUpgrades is the maximum number of nodes being upgraded in parallel
	// +kubebuilder:validation:Minimum=1
	MaxParallelUpgrades int32 `json:"maxParallelUpgrades,omitempty"`
	// DrainTimeout is the maximum duration to wait for node drain to complete (e.g. "5m", "10m")
	DrainTimeout string `json:"drainTimeout,omitempty"`
	// ValidationTimeout is the maximum duration to wait for driver validation after upgrade (e.g. "2m")
	ValidationTimeout string `json:"validationTimeout,omitempty"`
	// RollbackOnFailure triggers automatic rollback to the previous driver version on upgrade failure
	RollbackOnFailure bool `json:"rollbackOnFailure,omitempty"`
	// MaxRollbackAttempts는 롤백이 반복 실패할 때 시도할 최대 횟수입니다.
	// 이 값을 초과하면 "Failed" 상태로 전이하여 무한 루프를 방지합니다. 기본값 3.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	MaxRollbackAttempts int32 `json:"maxRollbackAttempts,omitempty"`

	// RollbackTarget 은 롤백 시 어떤 image 로 되돌릴지 결정합니다. previousValidated 는 직전
	// 사이클에서 검증된 PreviousImage 가 있을 때만 롤백하고 없으면 Failed 로 끝내 plain tag
	// 치환의 broken image 회귀를 막습니다. spec(기본, 레거시)은 PreviousImage 없이도 태그만 치환합니다.
	// +kubebuilder:validation:Enum=previousValidated;spec
	RollbackTarget string `json:"rollbackTarget,omitempty"`

	// IdleCooldownSeconds 는 Idle 진입 후 다음 upgrade 트리거를 받지 않을 최소 기간(초)입니다.
	// 미지정(nil) 시 기본 10초, 0 이하는 cooldown 비활성화(레거시 동작)입니다. 연속 트리거가
	// mid-state 로 오인되거나 관측되지 않은 채 새 사이클이 시작되는 것을 막는 안전장치입니다.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=0
	IdleCooldownSeconds *int32 `json:"idleCooldownSeconds,omitempty"`

	// MaxReboots 는 커널 모듈 교체(계열 전환·같은 계열 patch 교체)로 노드 재부팅을 트리거할 최대 횟수입니다.
	// 초과 시 Failed 로 전이(무한 재부팅 방지). 교체 한 번에 재부팅 1회면 충분. 기본 1.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	MaxReboots int32 `json:"maxReboots,omitempty"`
	// RebootTimeout 은 Rebooting 상태에서 노드 Ready 복귀를 대기할 최대 시간(e.g. "15m"). 미지정 시 15m.
	RebootTimeout string `json:"rebootTimeout,omitempty"`
}

// DriverInstallPolicySpec 은 벤더/모델별 드라이버 설치 정책을 담습니다.
type DriverInstallPolicySpec struct {
	// 예: "furiosa", "nvidia"
	// 컨트롤러에서 NodeDeviceReport.status.devices[].vendor 와 매칭
	Vendor string `json:"vendor"`

	// 예: "warboy", "generic" (또는 "a100","a30" 등 필요시 사용)
	// 빈 값 허용(벤더만 매칭)하려면 Controller에서 로직으로 지원
	Model string `json:"model,omitempty"`

	// 설치할 드라이버 사양
	Driver DriverSpec `json:"driver"`

	// (선택) NVIDIA Container Toolkit 등 런타임 툴킷 설치 사양
	Toolkit *ToolkitSpec `json:"toolkit,omitempty"`

	// 허용 커널 버전 패턴 (예: ["5.15.*","6.8.*"])
	KernelAllowlist []string `json:"kernelAllowlist,omitempty"`

	// 재부팅 전략
	// +kubebuilder:validation:Enum=Require;IfNeeded;Never
	RebootStrategy string `json:"rebootStrategy,omitempty"`

	// RebootExecutor 는 재부팅 실행 방식입니다. Job(기본): operator 가 privileged reboot Job 생성.
	// kured: 노드에 kured sentinel annotation 만 부착(kured 운영 클러스터용, cordon/drain 중복 억제 설정 권장).
	// +kubebuilder:validation:Enum=Job;kured
	// +kubebuilder:default=Job
	RebootExecutor string `json:"rebootExecutor,omitempty"`

	// (선택) 잡 템플릿 오버라이드(서비스어카운트, TTL, Backoff 등)
	JobOverrides *JobOverrides `json:"jobOverrides,omitempty"`

	// (선택) 잡/파드에 적용할 노드 셀렉터(특정 노드군에만 설치)
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Upgrade policy for driver version changes
	UpgradePolicy *UpgradePolicy `json:"upgradePolicy,omitempty"`

	// VerifiedVersions 는 검증된 driver version 화이트리스트입니다. non-empty 면 목록에 없는
	// spec.driver.version 의 업그레이드를 거부하고 DUS 를 UnverifiedVersion 상태로 전이합니다.
	// 비어 있으면 버전 검증을 skip 합니다(기존 동작 유지).
	// +optional
	VerifiedVersions []string `json:"verifiedVersions,omitempty"`

	// ImagePullSecrets 는 operator 가 생성하는 driver DaemonSet pod spec 에 부착되는
	// pull secret 목록입니다. helm `imagePullSecrets` 값이 CR 을 통해 전파됩니다.
	// 미지정 시 빈 목록 — 노드레벨 인증 경로를 유지하는 하위호환 동작입니다.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// WorkloadProbe 는 검증 체인의 마지막 단계다. 켜면 드라이버 교체 검증 때 노드마다 장치 1개를 요청하는
	// 파드를 하나 띄워 명령을 실행하고 지운다(Succeeded 면 통과). 노드는 그 시점에 cordon 돼 있어 사용자
	// 워크로드를 밀어내지 않는다. 기본 꺼짐 — 이미지가 air-gap 미러에 있어야 한다.
	// +optional
	WorkloadProbe *WorkloadProbeSpec `json:"workloadProbe,omitempty"`
}

// KernelImageRef 는 kernelImages 맵의 값이다. image 와 같은 image reference 형식이며, 길이
// 상한은 kernelImages 값 CEL 검증의 비용을 API 서버 예산 안에 들이기 위한 것이다.
// +kubebuilder:validation:MaxLength=512
type KernelImageRef string

// DriverSpec은 드라이버 버전/이미지 및 설치 방법을 정의합니다.
// +kubebuilder:validation:XValidation:rule="!has(self.kernelImages) || size(self.kernelImages) == 0 || self.mode == 'job'",message="driver.kernelImages requires driver.mode=job"
// +kubebuilder:validation:XValidation:rule="!has(self.kernelImages) || self.kernelImages.all(k, self.kernelImages[k].matches('^[a-zA-Z0-9._:/-]+:[a-zA-Z0-9._+-]+$'))",message="driver.kernelImages values must be image references"
// +kubebuilder:validation:XValidation:rule="!has(self.kernelImageBuildRevision) || size(self.kernelImageBuildRevision) == 0 || self.mode == 'job'",message="driver.kernelImageBuildRevision requires driver.mode=job"
// +kubebuilder:validation:XValidation:rule="!has(self.kernelImageBuildRevision) || size(self.kernelImageBuildRevision) == 0 || (has(self.version) && size(self.version) > 0)",message="driver.kernelImageBuildRevision requires a non-empty driver.version"
// +kubebuilder:validation:XValidation:rule="!has(self.kernelImageVariant) || size(self.kernelImageVariant) == 0 || self.mode == 'job'",message="driver.kernelImageVariant requires driver.mode=job"
// +kubebuilder:validation:XValidation:rule="(has(self.trackOnly) && self.trackOnly) || (has(self.image) && size(self.image) > 0)",message="driver.image is required unless driver.trackOnly=true"
type DriverSpec struct {
	// 예: "1.7.8" (Furiosa) 또는 "575.64.03"(NVIDIA)
	// 비워두면 인스톨러 스크립트가 "권장 버전"으로 자동 선택하도록 구현 가능
	Version string `json:"version,omitempty"`

	// Image 는 인스톨러 컨테이너 이미지입니다(예: "registry.example.com:5000/furiosa-driver-installer:1.7.8").
	// registry/host:port/repo 부와 tag 부로 구성되며, tag 부는 alphanumerics·`.`·`_`·`+`·`-` 만
	// 허용합니다(sub-version 예: "580.142-v17.1"). 구문만 검증하며 의미론적 검증은 별도입니다.
	// trackOnly=true 인 정책은 설치 경로가 없어 비울 수 있습니다(그 외에는 DriverSpec 의 CEL 규칙이 필수로 요구).
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9._:/-]+:[a-zA-Z0-9._+-]+$`
	Image string `json:"image,omitempty"`

	// KernelImages 는 노드 커널 버전(node.status.nodeInfo.kernelVersion)마다 쓸 사전 컴파일
	// 설치 이미지다. 노드 커널에 매칭되는 항목이 없으면 image(DKMS 컴파일)를 쓴다. 값은 image 와
	// 같은 형식의 검증 빌드 태그(…-vN)여야 하며, mode=job 에서만 유효하다.
	// +optional
	// +kubebuilder:validation:MaxProperties=64
	KernelImages map[string]KernelImageRef `json:"kernelImages,omitempty"`

	// KernelImageBuildRevision 은 사전 컴파일 커널 이미지 태그의 빌드 개정(…-vN)이다. 선언하면
	// kernelImages 에 없는 커널도 "<version>[-variant]-k<커널>-<개정>" 규약으로 조립한다(version
	// 필수, kernelImages 항목이 있으면 그쪽 우선). 비우면(기본) image(DKMS)를, mode=job 에서만 유효.
	// +optional
	// +kubebuilder:validation:Pattern=`^v[0-9]+(\.[0-9]+)?$`
	KernelImageBuildRevision string `json:"kernelImageBuildRevision,omitempty"`

	// KernelImageVariant 는 조립할 태그의 variant 토막이다(버전과 커널 토막 사이). 벤더 발행
	// 규약에 그 토막이 있을 때만 채운다(Furiosa 계열 필수, NVIDIA·Tenstorrent 는 없음). 비우면
	// (기본) "<version>-k<커널>-<빌드 개정>" 을 조립한다. kernelImageBuildRevision 이 비면 무효과.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-zA-Z][a-zA-Z0-9._]*$`
	KernelImageVariant string `json:"kernelImageVariant,omitempty"`

	// Installer 는 설치 방식이다: apt(내/외부 리포지터리), ngc(NVIDIA NGC 드라이버 컨테이너,
	// 커널 매칭 이미지), script(컨테이너 내 스크립트, 자유도 높음).
	// +kubebuilder:validation:Enum=apt;ngc;script
	Installer string `json:"installer,omitempty"`

	// ImageVersionBaked 는 이미지에 드라이버 버전이 구워져 있어 태그가 곧 설치 버전인 경우다(예:
	// tenstorrent-driver-ds:2.8.0-v1, entrypoint 가 DRIVER_VERSION 을 무시). 참이면 apt/script
	// installer 도 롤백 이미지를 이전 버전 태그로 재구성하고, 거짓(기본)이면 DRIVER_VERSION 으로 되돌린다.
	// +optional
	ImageVersionBaked bool `json:"imageVersionBaked,omitempty"`

	// Mode 는 드라이버 설치 방식을 결정합니다. daemonset(기본)은 DaemonSet 으로 드라이버를 상시
	// 실행합니다(컨테이너화 드라이버). job 은 순간 install Job 으로 설치 후 즉시 종료합니다(상주
	// pod 없음, 100% opt-in) — DUS 상태머신이 Job 의 단일 소유자이며 daemonset 경로는 그대로 재사용됩니다.
	// +kubebuilder:validation:Enum=daemonset;job
	// +kubebuilder:default=daemonset
	Mode string `json:"mode,omitempty"`

	// (선택) 인스톨러 환경변수
	Env []KV `json:"env,omitempty"`

	// AllowDowngrade 는 호스트에 이미 설치된 드라이버가 desired 보다 상위 버전일 때
	// 하위 버전으로의 다운그레이드 설치를 허용할지 결정합니다.
	// 기본 false = 다운그레이드 금지(기존 상위 버전 유지, 설치 보류). true 여야 하위 버전 설치 허용.
	// +kubebuilder:default=false
	AllowDowngrade bool `json:"allowDowngrade,omitempty"`

	// VersionSource 는 effective desired 버전을 어디서 채택할지 결정합니다.
	// Policy : 기존 동작 — spec.driver.version 을 desired 로 사용.
	// Host   : 호스트에 설치된 버전을 desired 로 채택(기존 존중). 호스트 무드라이버면 Policy 버전으로 fallback.
	// +kubebuilder:validation:Enum=Policy;Host
	// +kubebuilder:default=Policy
	VersionSource string `json:"versionSource,omitempty"`

	// SkipOnPassthrough 는 노드의 GPU 가 전량 vfio-pci(passthrough)에 바인딩된 경우
	// 드라이버 설치를 보류(skip)할지 결정합니다.
	// 기본 true = 전량 vfio 노드에서 설치 보류(passthrough 보호). false 면 기존 동작(설치 진행).
	// +kubebuilder:default=true
	SkipOnPassthrough bool `json:"skipOnPassthrough,omitempty"`

	// TrackOnly 는 true 면 DriverUpgradeState 로 노드별 드라이버 버전만 추적하고 설치 경로(driver
	// DaemonSet/install Job)는 만들지 않습니다. 설치기 이미지를 만들 수 없는 벤더(Rebellions: 드라이버
	// 패키지가 벤더 포털 인증 뒤에만 받아진다)를 버전 관리 체계에 편입할 때 씁니다. image 는 비울 수
	// 있고 verifiedVersions 는 적용되지 않습니다. 기본 false(설치 경로 활성).
	// +kubebuilder:default=false
	TrackOnly bool `json:"trackOnly,omitempty"`
}

// ToolkitSpec 은 NVIDIA Container Toolkit 등 런타임 툴킷 설치를 정의합니다.
type ToolkitSpec struct {
	// 설치 여부
	Enabled bool `json:"enabled"`

	// (선택) 전용 인스톨러 이미지 (미지정 시 Driver.Image 재사용 가능)
	Image string `json:"image,omitempty"`

	// (선택) 환경변수
	Env []KV `json:"env,omitempty"`
}

// WorkloadProbeSpec 은 샘플 워크로드 검증 설정이다.
type WorkloadProbeSpec struct {
	Enabled bool `json:"enabled"`
	// Image 는 벤더 CLI 가 든 이미지다(예: acpp.probeImages 와 같은 것).
	// 필수. 비면 probe 는 파드를 만들지 않고 비통과(벤더 기본 이미지 없음).
	Image string `json:"image,omitempty"`
	// Command 는 종료 코드 0 이면 통과로 보는 명령이다. 비면 이미지 ENTRYPOINT.
	Command []string `json:"command,omitempty"`
	// ResourceName 은 요청할 확장 자원(예: nvidia.com/gpu).
	// 필수. 비면 probe 는 파드를 만들지 않고 비통과(벤더 기본 리소스명 없음 —
	// furiosa 는 product 가 있어야 리소스명이 정해진다).
	ResourceName string `json:"resourceName,omitempty"`
	// TimeoutSeconds 는 파드 완료 대기 상한(기본 120).
	// +kubebuilder:validation:Minimum=10
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
}

// JobOverrides 는 잡 스펙의 일부를 정책에서 제어하기 위한 필드입니다.
type JobOverrides struct {
	// 서비스어카운트 이름(미지정 시 운영자 기본 SA 사용 또는 빈값)
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// 완료 후 자동 삭제 TTL(초)
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// 실패 재시도 횟수
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// (선택) 파드 우선순위 클래스
	PriorityClassName string `json:"priorityClassName,omitempty"`
}

// KV는 단순 키-값 환경변수 표현입니다.
type KV struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// +kubebuilder:object:root=true
type DriverInstallPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DriverInstallPolicy `json:"items"`
}
