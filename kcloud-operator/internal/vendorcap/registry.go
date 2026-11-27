// ============================================================
// registry.go: 벤더×제품×능력 선언 레지스트리(명세서 층)
// 상세: "벤더가 무엇을 할 수 있다고 선언했는가"만 담는다 — 실측(성적표 층)은 pkg/npuctl
// 이 evidence 를 조인해 따로 낸다. 새 CRD 없이 이 Go 리터럴이 정본이고, 호환성 매트릭스
// 문서는 여기서 생성된다. Reason/Source 필수 제약은 아래 생성자 주석 참고.
// 생성일: 2026-09-19 | 수정일: 2026-10-27
// ============================================================
package vendorcap

import (
	"sort"

	"kcloud-operator/api/v1alpha1"
)

// State 는 선언 상태다. 벤더 제공(Native)과 kcloud SW 구현(Software)을 구분한다.
type State string

const (
	StateNative      State = "Native"
	StateSoftware    State = "Software"
	StateUnsupported State = "Unsupported"
	StateUnknown     State = "Unknown"
)

// Capability 는 능력 이름이다. 기존 어휘(SharingCapability 필드·Descriptor Backends)를 재사용한다.
type Capability string

const (
	CapDiscovery        Capability = "discovery"
	CapDriver           Capability = "driver"
	CapFirmware         Capability = "firmware"
	CapRuntime          Capability = "runtime"
	CapContainerToolkit Capability = "containerToolkit"
	CapDevicePlugin     Capability = "devicePlugin"
	CapDRA              Capability = "dra"
	CapPartition        Capability = "partition"
	CapTimeSlicing      Capability = "timeSlicing"
	CapMultiProcess     Capability = "multiProcess"
	CapBrokered         Capability = "brokered"
	CapIsolation        Capability = "isolation"
	CapTelemetry        Capability = "telemetry"
	CapHealth           Capability = "health"
	CapReset            Capability = "reset"
)

// Key 는 (vendor, product) 다. product 가 하나뿐인 벤더는 "" 을 쓴다(intent.productResource 와 동일).
type Key struct{ Vendor, Product string }

// AssuranceLevel 은 그 능력을 어디까지 확인했는지를 나타낸다. State 와 나누는 이유는
// "구현했다" 와 "확인했다" 가 다른 물음이기 때문이다 — 코드가 있어도 확인하지 않았거나
// 단위 시험까지만 확인했을 수 있다. "Verification"·"Evidence" 는 API 로 나가는
// Support.Verification 필드와 AcceleratorEvidence CRD 가 이미 써 이름을 바꿀 수 없어,
// 세 번째 이름 Assurance 를 쓴다. 값은 직접 읽지 말고 Support.Assurance() 로 얻는다.
type AssuranceLevel string

const (
	// AssuranceNone 은 확인 근거가 없음을 뜻한다. 적지 않은 항목의 기본값이다.
	AssuranceNone AssuranceLevel = "none"
	// AssuranceUnit 은 단위 시험까지 확인했음을 뜻한다.
	AssuranceUnit AssuranceLevel = "unit"
	// AssuranceCluster 은 실제 클러스터에서 확인했음을 뜻한다(하드웨어 없이 가능한 범위).
	AssuranceCluster AssuranceLevel = "cluster"
	// AssuranceHardware 은 그 장치가 붙은 노드에서 확인했음을 뜻한다. 이 값을 쓰려면
	// Source 에 재현 가능한 경로가 있어야 한다(registry_test 가 강제).
	AssuranceHardware AssuranceLevel = "hardware"
)

// Support 는 한 능력 칸의 선언이다. Verification 은 선언 시점의 기본 검증 상태이며
// (Native/Software → Required, Unsupported/Unknown → NotApplicable), 실측 갱신은 성적표 층이 한다.
type Support struct {
	State        State
	Verification string
	Reason       string
	Source       string
	TestRefs     []string
	assurance    AssuranceLevel // 비공개 — 패키지 밖은 Assurance() 로만 읽는다
}

// Assurance 는 이 항목의 확인 수준을 돌려준다. 필드가 비공개라 패키지 밖은 이 메서드로만
// 접근할 수 있지만, 패키지 안(예 matrix.go)에서도 필드 대신 이 메서드를 써야 한다 —
// 그래야 레지스트리 맵을 직접 읽거나 빈 값을 가진 경우에도 AssuranceNone 으로 읽힌다.
func (s Support) Assurance() AssuranceLevel {
	if s.assurance == "" {
		return AssuranceNone
	}
	return s.assurance
}

// Unsupported/Unknown 은 Reason 이, Native/Software 는 Source 가 있어야 한다 —
// registry_test.go 가 강제한다(여기서는 검증하지 않는다).
func native(src string, tests ...string) Support {
	return Support{State: StateNative, Verification: v1alpha1.VerificationRequired, Source: src, TestRefs: tests}
}
func software(src string, tests ...string) Support {
	return Support{State: StateSoftware, Verification: v1alpha1.VerificationRequired, Source: src, TestRefs: tests}
}
func unsupported(reason, src string) Support {
	return Support{State: StateUnsupported, Verification: v1alpha1.VerificationNotApplicable, Reason: reason, Source: src}
}
func unknown(reason string) Support {
	return Support{State: StateUnknown, Verification: v1alpha1.VerificationNotApplicable, Reason: reason}
}

// withAssurance 는 이미 만든 Support 에 확인 수준을 덧붙인다. AssuranceHardware 를 쓰려면
// s.Source 에 재현 가능한 경로(evidence/ 또는 test/live/)가 있어야 한다 — 없으면
// registry_test.go 의 TestHardwareVerifiedRequiresEvidencePath 가 잡는다.
func withAssurance(level AssuranceLevel, s Support) Support {
	s.assurance = level
	return s
}

// registry 는 정본이다. 근거 없는 Native/Software 를 적지 않는다.
var registry = map[Key]map[Capability]Support{
	{"nvidia", ""}: {
		CapDiscovery:        native("NodeDeviceReport nvidia, kcloud-node-manager PCI 스캔"),
		CapDriver:           withAssurance(AssuranceUnit, native("DriverInstallPolicy nvidia-gpu-ds, driver-installer/nvidia", "internal/controller/driver_upgrade_controller_test.go")),
		CapFirmware:         unsupported("펌웨어 관리 미구현", ""),
		CapRuntime:          native("NVIDIA container toolkit reconciler(toolkitdaemonset)"),
		CapContainerToolkit: native("deploy/helm/values.yaml driverInstallPolicies.nvidia.toolkit"),
		CapDevicePlugin:     native("nvcr.io/nvidia/k8s-device-plugin (values nvidia.devicePluginImage)"),
		CapDRA:              native("dra-driver-nvidia (values nvidia.dra), internal/controller/dra_driver.go"),
		CapPartition:        native("MIG — internal/partition/nvidia, A30 라이브 PASS", "internal/partition/nvidia/geometry_test.go"),
		CapTimeSlicing:      withAssurance(AssuranceUnit, software("device-plugin replicas — internal/partition/nvidia/timeslicing.go, 라이브 검증", "internal/partition/nvidia/timeslicing_test.go", "internal/partition/sharing_test.go")),
		CapMultiProcess:     native("MPS control daemon(upstream k8s-device-plugin) — internal/partition/nvidia/timeslicing.go(ApplySharingConfig·MPSPipeDir), 4-tenant 검증"),
		CapBrokered:         unsupported("brokered 공유 미구현", ""),
		CapIsolation:        withAssurance(AssuranceCluster, native("MIG hardware 격리 — co-run 전량 점유(tenants 4)에서 연산·대역폭 비 ≈1.0 실측, evidence 원본은 이 저장소 밖(재현 하네스 미작성)")),
		CapTelemetry:        native("dcgm-exporter (values nvidia.dcgmExporter) + ServiceMonitor"),
		CapHealth:           native("AcceleratorHealth 컨트롤러"),
		CapReset:            native("nvidia-smi -r/--gpu-reset 로 장치 HW·SW 상태 초기화(root·소비자 없음 조건, MIG vGPU guest 미지원) 문서 확인 — 실측(2026-09-28, k8s-worker1 A30) 시도, device-plugin·dcgm-exporter 의 상시 NVML 핸들로 매번 \"In use by another client\"(exit=255) 거부, cordon+DS 파드 삭제(승인 조치)로 점유 해제 실패; kill·taint·operator 라벨 재사용 세 건은 승인 범위 밖 수동 조치로 시도됐고 전부 되돌림"),
	},
	{"furiosa", "warboy"}: {
		CapDiscovery:        native("NodeDeviceReport furiosa/warboy"),
		CapDriver:           native("DriverInstallPolicy furiosa-warboy-ds"),
		CapFirmware:         unsupported("펌웨어 관리 미구현", ""),
		CapRuntime:          unsupported("런타임(furiosa-llm·libnux)은 워크로드 컨테이너 이미지가 담음 — 노드엔 드라이버·device-plugin·DRA 만 설치, operator 가 노드에 설치·관리할 런타임 없음", ""),
		CapContainerToolkit: unsupported("해당 없음(벤더 툴킷 없음)", ""),
		CapDevicePlugin:     native("ghcr.io/furiosa-ai/k8s-device-plugin (values furiosa.devicePluginImage)"),
		CapDRA:              native("furiosaai/furiosa-dra-driver (values furiosa.dra)"),
		CapPartition:        unsupported("단일 코어 — 파티션 수단 없음", ""),
		CapTimeSlicing:      unsupported("두 번째 open EBUSY(동시 open 실측 2026-09-19)", "test/live/concurrent-open/probe.sh"),
		CapMultiProcess:     unsupported("배타적 open — 두 번째 프로세스 open 시 EBUSY(동시 open 실측 2026-09-19)로 동시 실행 불가, 벤더 broker 없어 중재 대상 없음", "test/live/concurrent-open/probe.sh"),
		CapBrokered:         unsupported("broker 미구현", ""),
		CapIsolation:        unsupported("배타적 open 으로 공유 불가 — 두 프로세스 동시 실행이 안 돼(EBUSY, 동시 open 실측 2026-09-19) 격리를 잴 대상이 없음", "test/live/concurrent-open/probe.sh"),
		CapTelemetry:        native("furiosa-exporter (deploy/helm/templates/furiosa-exporter.yaml)"),
		CapHealth:           native("AcceleratorHealth 컨트롤러"),
		CapReset:            unsupported("공개 인터페이스(furiosa-smi CLI 2026.1·furiosa-smi-go SDK C API v2026.1.1)에 장치 리셋 수단 미발견 — drain 은 장치 활성/비활성 표시이지 HW 리셋 아님", "test/live/device-reset/run.sh"),
	},
	{"furiosa", "rngd"}: {
		CapDiscovery:        native("NodeDeviceReport furiosa/rngd"),
		CapDriver:           native("DriverInstallPolicy furiosa-rngd-ds"),
		CapFirmware:         unsupported("펌웨어 관리 미구현", ""),
		CapRuntime:          unsupported("런타임(furiosa-llm·libnux)은 워크로드 컨테이너 이미지가 담음 — 노드엔 드라이버·device-plugin·DRA 만 설치, operator 가 노드에 설치·관리할 런타임 없음", ""),
		CapContainerToolkit: unsupported("해당 없음", ""),
		CapDevicePlugin:     native("furiosa-rngd-device-plugin (ensureFuriosaRngdDevicePlugin)"),
		CapDRA:              native("furiosaai/furiosa-dra-driver, 라이브 PASS"),
		CapPartition:        native("PE-fusion — internal/partition/rngd, 라이브", "internal/partition/rngd/backend_test.go"),
		CapTimeSlicing:      unsupported("장치가 배타적 open — 두 번째 프로세스 EBUSY(2026-08-11 실측)", "test/live/rngd-multiprocess/probe.sh"),
		CapMultiProcess:     withAssurance(AssuranceCluster, unsupported("배타적 open — 두 프로세스가 같은 장치 노드를 동시에 열면 두 번째가 EBUSY(ResourceBusy)로 튕김(2026-08-11 실측), 벤더 broker 없어 중재할 대상 없음", "test/live/rngd-multiprocess/probe.sh")),
		CapBrokered:         unsupported("broker not implemented", ""),
		CapIsolation:        withAssurance(AssuranceCluster, native("PE 파티션 격리 부분 실측 — 메모리 용량(조각 11.88/47.50 GiB 관측)·장애 봉쇄(이웃 생존) measured, 연산·대역폭은 벤더 부하 도구 한계로 blocked, evidence 원본은 이 저장소 밖(재현 하네스 미작성)")),
		CapTelemetry:        native("furiosa-exporter"),
		CapHealth:           native("AcceleratorHealth 컨트롤러"),
		CapReset:            unsupported("공개 인터페이스(furiosa-smi CLI 2026.1·furiosa-smi-go SDK C API v2026.1.1)에 장치 리셋 수단 미발견 — drain 은 장치 활성/비활성 표시이지 HW 리셋 아님", "test/live/device-reset/run.sh"),
	},
	{"rebellions", ""}: {
		CapDiscovery: native("NodeDeviceReport rebellions(node5 ATOM)"),
		CapDriver: unsupported("TrackOnly DriverInstallPolicy(rebellions-atom-ds) — 버전 관측만, 설치 경로 없음. "+
			"드라이버 패키지(rebellions-dkms)는 벤더 포털 인증이 필요해 호스트 관리자가 설치",
			"deploy/helm/templates/driverinstallpolicy.yaml"),
		CapFirmware:         unsupported("펌웨어 관리 미구현", ""),
		CapRuntime:          unsupported("런타임(rebel-compiler·optimum-rbln·vllm-rbln)은 컨테이너에 pip wheel 로 담기고 드라이버·펌웨어는 host-managed — operator 가 노드에 설치·관리할 런타임 없음", ""),
		CapContainerToolkit: unsupported("해당 없음", ""),
		CapDevicePlugin:     native("rebellions device-plugin (ensureRebellionsDevicePlugin, node5 라이브)"),
		CapDRA:              native("rebellions/k8s-dra-driver-npu (values rebellions.dra) — ResourceSlice 발행은 벤더 바이너리 결함으로 차단(외부 제출 대기)"),
		CapPartition:        unsupported("공개 인터페이스(rbln-smi/rbln-stat/OSS device-plugin·DRA)와 rblnlib-go v0.3.0 소스(pkg/device·pkg/rsdgroup·pkg/rblnsmi) 검토 결과 분할 수단 미발견 — vendor-capability-not-found(공개 소스 조사 2026-09-15); RSD 그룹은 여러 장치를 하나로 묶는 집약이고 장치 분할 API 는 없음", ""),
		CapTimeSlicing:      withAssurance(AssuranceHardware, software("실 워크로드 동시 실행 실측(node5 2026-09-25) — 두 프로세스가 같은 ATOM(device 0)을 동시에 열고 device_open 이 두 프로세스 모두 0.01s 로 배리어에 비례해 늘지 않음(배타 락 부재, tt 의 진 파드 init 팽창과 대비), rbln-smi 가 NPU 0 에 두 컨텍스트 확인(NPU 1 유휴)이 등급의 주근거; 보조로 겹치는 구간의 처리량이 각각 solo 의 0.92배·합 1.83배(rt 동기 실행 실측 확인); 파드 경로 재확인(2026-09-29): descriptor 광고 자원으로 스케줄한 파드 셋(solo·co-run 2)에서 co-run 처리량이 각각 solo 의 0.85배·0.82배, 합 1.66배, 겹침 구간 30초, rbln-smi 컨텍스트 2 확인; 재현은 test/live/rebellions-timeslice/run.sh", "test/live/rebellions-timeslice/run.sh")),
		CapMultiProcess:     unsupported("벤더 broker 없음 — 동시 실행은 되나(2026-09-25 실측) 프로세스별 상한을 강제할 중재 수단 없음", "test/live/rebellions-timeslice/run.sh"),
		CapBrokered:         unsupported("broker 미구현", ""),
		CapIsolation:        withAssurance(AssuranceHardware, unsupported("연산·용량·대역폭·장애 네 항목 co-run 실측(node5 2026-09-28, 장치 0=0000:c3:00.0·장치 1=0000:c4:00.0) — 용량(장치 1 이 K-chain(CHAIN_K=32)·512MiB 를 점유한 채 장치 0 도 같은 크기로 성공, rbln-smi 로 NPU0·NPU1 메모리 독립 확인)·장애(장치 1 kill -9+구성 불일치 실패 동안 장치 0 30초 끝까지 실행, 사후 rbln-smi 정상)는 격리 확인. 연산(비 0.7883)·대역폭(비 0.8829)은 기준(비 ≥0.95)에 못 미쳐 장치 1 부하가 장치 0 처리량으로 빠져나간다 — 별도 물리 장치라도 완전한 성능 격리는 아니다(호스트측 공유 자원 간섭 추정)", "test/live/rebellions-isolation/run.sh")),
		CapTelemetry:        unsupported("exporter 없음", ""),
		CapHealth:           native("AcceleratorHealth 컨트롤러"),
		CapReset:            unsupported("공개 인터페이스(rbln-smi/rbln-stat CLI·rblnlib-go v0.3.0 pkg/device·rblnsmi·rsdgroup·device-plugin·DRA)에 장치 리셋 수단 미발견 — tdr 하위 명령은 공개 문서 미확인", "test/live/device-reset/run.sh"),
	},
	{"tenstorrent", ""}: {
		CapDiscovery:        native("NodeDeviceReport tenstorrent(k8s-worker3 blackhole-p150)"),
		CapDriver:           withAssurance(AssuranceHardware, software("driver-installer/tenstorrent 2.11.0 — 2.8.0→2.11.0 라이브 업그레이드 2026-09-21; 상태 기계 전이(install Job·검증·Idle 수렴) 라이브 확인; 재현은 test/live/tt-kmd-upgrade/run.sh", "test/live/tt-kmd-upgrade/run.sh")),
		CapFirmware:         unsupported("펌웨어 관리 미구현", ""),
		CapRuntime:          unsupported("런타임(tt-metal)은 컨테이너 이미지·PyPI wheel 로 담기고 노드엔 tt-kmd 드라이버·device-plugin 만 설치 — operator 가 노드에 설치·관리할 런타임 없음", ""),
		CapContainerToolkit: unsupported("해당 없음", ""),
		CapDevicePlugin:     software("kcloud 자체 tenstorrent-device-plugin (images/kcloud-operator-images/tenstorrent-device-plugin)"),
		CapDRA:              native("tenstorrent/tt-dra-driver + tt-fabric-manager (values tenstorrent.dra), 라이브 PASS 2026-09-18"),
		CapPartition:        unsupported("장치 분할 API 없음 — tt-smi·tt-kmd 2.11.0 소스·tt-metal 문서 조사(2026-09-22)", ""),
		CapTimeSlicing:      withAssurance(AssuranceHardware, unsupported("증명: spec.sharing→generic device-plugin replicas 광고·주입 기제 동작. 반증: 벤더 런타임(tt-metal)을 쓰는 실제 워크로드 두 개가 UMD 의 /dev/tenstorrent/0 KMD 락에서 직렬화돼 동시 실행 불가(co-run 실측 2026-09-23; 배리어 180/420/900s 에 진 파드 init 251/490/971s, 배리어 증가로도 미해소). 미판단: 락을 잡지 않는 소비자. 재등급 기준: 락을 잡지 않거나 공유하는 소비자 두 개의 동시 워크로드 실측", "test/live/tt-timeslice-throughput/run.sh")),
		CapMultiProcess:     unsupported("단일 카드 broker 데몬 없음(tt-metal mesh 는 다중 장치 scale-out) — tt-smi·tt-kmd 2.11.0 소스·tt-metal 문서 조사(2026-09-22)", ""),
		CapBrokered:         unsupported("broker 미구현", ""),
		CapIsolation:        withAssurance(AssuranceHardware, unsupported("tt-metal UMD 가 /dev/tenstorrent/0 에 KMD 락을 잡아 두 워크로드가 직렬화 — 공유가 불가해 격리를 잴 대상이 없음(co-run 실측 2026-09-23)", "test/live/tt-timeslice-throughput/run.sh")),
		CapTelemetry:        unsupported("exporter 없음", ""),
		CapHealth:           native("AcceleratorHealth 컨트롤러"),
		CapReset:            withAssurance(AssuranceHardware, unsupported("쓸 수 있는 장치 리셋 수단 미제공으로 확정(2026-10-02) — 벤더가 tt-smi -r 을 제공하지만 실행하면 호스트가 내려갔다 스스로 재부팅한다(2026-09-28 두 번 재현 08:33:09Z·13:55:57Z, 두 시각 모두 BMC SEL 에 그 정각 Critical Interrupt PCIe Fatal Error Asserted 뒤 53~56초 만에 OEM System boot event, 실시간 dmesg -w·journalctl -kf 캡처에서 리셋 전후 새 커널 로그 0줄). 노드 전체를 내리는 수단은 카드 하나만 되살리려는 리셋의 목적을 충족하지 못한다. PCIe Fatal Error 의 근본 원인은 미규명이고, 등급 판단과 분리해 2026-09-29 읽기 전용 원인 조사의 후보 순위로 남겨 뒀다(후보 1 은 호스트 firmware-first PCIe 오류 처리 정책)", "test/live/device-reset/run.sh")),
	},
}

// Lookup 은 (vendor, product, capability) 의 선언을 돌려준다. 없으면 ok=false.
func Lookup(vendor, product string, c Capability) (Support, bool) {
	caps, ok := registry[Key{vendor, product}]
	if !ok {
		return Support{}, false
	}
	s, ok := caps[c]
	return s, ok
}

// LookupVendor 는 벤더 단위 선언이다(webhook 처럼 product 를 모르는 호출자용). 그 벤더의
// 어느 product 든 Native/Software 면 그것을 돌려주고, 없으면 Keys() 정렬상 첫 product 의
// 값을 돌려준다 — Keys() 가 정렬돼 있어 이 폴백은 결정적이다. product 를 아는 호출자는
// 이 폴백에 기대지 말고 Lookup 을 직접 써야 한다.
func LookupVendor(vendor string, c Capability) Support {
	var first Support
	seen := false
	for _, k := range Keys() {
		if k.Vendor != vendor {
			continue
		}
		s := registry[k][c]
		if !seen {
			first, seen = s, true
		}
		if s.State == StateNative || s.State == StateSoftware {
			return s
		}
	}
	return first
}

// Keys 는 정렬된 키 목록이다(출력 결정성).
func Keys() []Key {
	out := make([]Key, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Vendor != out[j].Vendor {
			return out[i].Vendor < out[j].Vendor
		}
		return out[i].Product < out[j].Product
	})
	return out
}

// Vendors 는 Keys() 의 Vendor 필드에서 중복을 제거한 벤더 이름 집합이다(정렬됨).
// furiosa 처럼 product 가 둘인 벤더(rngd·warboy)도 한 번만 센다. 벤더별 지표 게이지를
// 매기는 등 product 를 몰라도 되는 호출자가 벤더를 손으로 나열하는 대신 이 집합을 쓰면,
// 새 벤더가 등록될 때 그 자리를 갱신하지 않아도 자동으로 포함된다.
func Vendors() []string {
	keys := Keys()
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if seen[k.Vendor] {
			continue
		}
		seen[k.Vendor] = true
		out = append(out, k.Vendor)
	}
	return out
}

// Capabilities 는 정렬된 능력 목록이다.
func Capabilities() []Capability {
	return []Capability{
		CapDiscovery, CapDriver, CapFirmware, CapRuntime, CapContainerToolkit,
		CapDevicePlugin, CapDRA, CapPartition, CapTimeSlicing, CapMultiProcess,
		CapBrokered, CapIsolation, CapTelemetry, CapHealth, CapReset,
	}
}
