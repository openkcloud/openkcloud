// ============================================================
// backend.go: NVIDIA 파티션 backend — discovery + MIG apply/rollback/diff/verify
// 상세: NDR 장치 → 공통 capability 형식 + per-PCI MIG target 추적. Apply/Rollback 은
// exec(JobExecutor) seam 으로 CommandStep 을 실행하고, Verify 는 verifier seam 에 위임한다.
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package nvidia

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition"
)

// migCapableRe 는 MIG 지원 모델(A30/A100/H100)을 토큰 경계로 매칭한다.
// substring 매칭은 RTX A3000(비-MIG)을 A30 으로 오분류하므로 \b 경계 필수.
var migCapableRe = regexp.MustCompile(`(?i)\b(a30|a100|h100)\b`)

var (
	ErrMixedPlacementUnsupported = errors.New("nvidia: mixed placement unsupported in MVP-2")
	ErrInvalidCount              = errors.New("nvidia: countPerDevice out of range")
	ErrProfileUnsupported        = errors.New("nvidia: profile not supported by target GPU")
)

// advProfileNamed 는 MIG 조각을 프로파일명(nvidia.com/mig-<profile>)으로 광고함을 나타내는 Advertisement.Mode 값.
const advProfileNamed = "ProfileNamed"

// MPS 판정 사유. 두 판정 사이트(Discover 의 비-MIG 분기 스위치 / deviceStatusFor)가 같은 어휘를
// 쓴다 — 부분 실패한 관측을 신뢰할지에 대해 두 사이트가 서로 다르게 답하면 안 된다.
const (
	reasonMPSModeUnobserved = "MIG 상태를 확정하지 못해 MPS 가능 여부를 판정할 수 없다"
	reasonMPSObserveFailed  = "MIG 모드를 관측하지 못해 MPS 가능 여부를 판정할 수 없다"
	reasonMPSMigEnabled     = "MIG 가 켜진 장치에서는 MPS 를 쓸 수 없다(상호 배타)"
	reasonMPSMigDisabled    = "MIG 가 꺼진 장치라 MIG-MPS 상호배타가 성립하지 않는다"
	reasonMPSNoMigHardware  = "MIG 미지원 장치라 MIG-MPS 상호배타가 성립하지 않는다"
)

// 공유 기능의 실측 결과를 operator 에 주입하는 env 다(RNGD 의 MultiProcessEnv 와 같은 규율,
// 정직성 원칙) — "verified" 일 때만 검증 축이 오르고, 그 외/미설정은 required 다. 기능마다
// 따로 두는 이유는 MPS 와 time-slicing 이 서로 다른 실측이기 때문이다. 이 값은 상태 표기만
// 바꾸지 않는다 — 워크로드 승인 게이트다(internal/intent/mode.go 의 checkSharing).
// MultiProcessEnv 만 켜면 auto 가 MPS 를 고르고도 MaxReplicas 미기입으로 거절되므로,
// TimeSlicingEnv 도 함께 켠다. env 는 클러스터 단위라 이종 노드 추가에는 재검증이 필요하다.
const (
	MultiProcessEnv = "KCLOUD_NVIDIA_MULTIPROCESS"
	TimeSlicingEnv  = "KCLOUD_NVIDIA_TIMESLICING"
)

// verifiedIfMeasured 는 실측 주입이 있을 때만 검증 축을 올린다. 미설정·오타 등 "verified" 가
// 아닌 값은 전부 required 다(fail-closed — 오타가 승격으로 읽히면 안 된다).
func verifiedIfMeasured(env string) string {
	if os.Getenv(env) == "verified" {
		return v1alpha1.VerificationVerified
	}
	return v1alpha1.VerificationRequired
}

// Backend 는 c(K8s client) 로 NDR 조회, supported(profile→MaxInstancesPerDevice) 로 Validate 시
// 대상 GPU 의 profile 지원 범위를 판정한다. supported/targets 는 Discover 가 채우며 테스트는
// withSupported/withTargets 로 직접 주입한다. exec/verifier 는 하드웨어 의존 seam(WithExecutor/WithVerifier).
type Backend struct {
	c         client.Client
	exec      Executor
	verifier  partition.Verifier
	supported map[string]int32
	targets   []MigDevice
	expected  map[string]int32
	// observations 는 operator-driven MIG 관측(PCI→Observation, ). nil 이면 Discover 는
	// NDR MIG 필드를, non-nil 이면 이 관측을 MIG-capable 장치의 진실 소스로 쓴다.
	observations map[string]Observation
	// profilesUnobservable 은 target 중 mode 가 Disabled 라 profile 목록을 관측할 수 없는 것이
	// 있는지다. Validate 가 이 경우 supported 부재를 "미지원"으로 오판하지 않게 한다.
	profilesUnobservable bool
}

func New(c client.Client) *Backend { return &Backend{c: c} }

// withSupported 는 테스트에서 Discover 없이 supported profile 맵을 직접 주입한다.
func (b *Backend) withSupported(m map[string]int32) *Backend { b.supported = m; return b }

// withTargets 는 테스트에서 Discover 없이 MIG target 목록을 직접 주입한다.
func (b *Backend) withTargets(d []MigDevice) *Backend { b.targets = d; return b }

// Targets 는 마지막 Discover(또는 withTargets 주입)로 채워진 MIG target 목록을 반환한다.
func (b *Backend) Targets() []MigDevice { return b.targets }

// WithTargetPCIs 는 snapshot PCI 만으로 targets 를 세운다 — 삭제 rollback 이 라이브 Discover 없이
// 저장된 GPUPCIs 를 disable 대상으로 삼게 한다(, snapshot-only deletion).
func (b *Backend) WithTargetPCIs(pcis []string) *Backend {
	b.targets = make([]MigDevice, 0, len(pcis))
	for _, p := range pcis {
		b.targets = append(b.targets, MigDevice{PCI: p})
	}
	return b
}

// WithExecutor 는 Apply/Rollback 이 사용할 CommandStep 실행 seam 을 주입한다.
func (b *Backend) WithExecutor(e Executor) *Backend { b.exec = e; return b }

// WithVerifier 는 Verify 가 사용할 검증 seam 을 주입한다.
func (b *Backend) WithVerifier(v partition.Verifier) *Backend { b.verifier = v; return b }

// WithExpected 는 Verify 가 VerifyAllocatable 에 넘길 기대 allocatable 맵을 주입한다(mig+full-gpu, reconciler 구성).
func (b *Backend) WithExpected(m map[string]int32) *Backend { b.expected = m; return b }

// WithObservations 는 operator-driven MIG 관측(PCI→Observation)을 주입한다.
// 주입시 Discover 가 MIG-capable 장치의 mode/geometry/lgip 를 NDR 필드 대신 이 관측에서 읽는다.
func (b *Backend) WithObservations(obs map[string]Observation) *Backend {
	b.observations = obs
	return b
}

// targetSelectors 는 targets 중 PCI 가 채워진 선택자 목록이다(Rollback 이 disable 대상을 고를 때 사용).
func (b *Backend) targetSelectors() []string {
	sels := make([]string, 0, len(b.targets))
	for _, d := range b.targets {
		if d.PCI != "" {
			sels = append(sels, d.PCI)
		}
	}
	return sels
}

func (b *Backend) Vendor() string { return "nvidia" }

// Discover 는 NDR NVIDIA 장치를 공통 capability 형식으로 변환한다(, criterion 12).
// MIG profile 은 DeviceEntry.MigLgipOutput 파싱. MIG-capable 장치가 하나라도 있으면 b.targets/b.supported
// 를 채우고 Apply/Rollback 을 supported=true, Advertisement.Mode="ProfileNamed" 로 보고한다.
// b.supported 는 targets 전원의 MigLgipOutput 교집합(profile 이 전 target 에 존재해야 하고, MaxInstances 는 최소값).
func (b *Backend) Discover(t partition.Target) (*partition.DiscoverResult, error) {
	var ndr v1alpha1.NodeDeviceReport
	if err := b.c.Get(t.Ctx, types.NamespacedName{Name: t.NodeName}, &ndr); err != nil {
		return nil, err
	}
	b.targets = nil
	b.supported = map[string]int32{}
	b.profilesUnobservable = false

	unsupportedApply := v1alpha1.OperationsStatus{
		Apply:    v1alpha1.OperationStatus{Supported: false, Reason: v1alpha1.ReasonApplyBackendNotInstalled},
		Rollback: v1alpha1.OperationStatus{Supported: false},
	}
	res := &partition.DiscoverResult{
		Operations:    unsupportedApply,
		Advertisement: v1alpha1.AdvertisementStatus{Mode: "Flat"},
	}
	var profileSets []map[string]int32
	for _, d := range ndr.Status.Devices {
		if d.Vendor != "nvidia" {
			continue
		}
		// 드라이버 버전은 첫 nvidia 장치에서 채운다(reconciler 의 항상-전제 DriverReady 판정 소스, ).
		if res.DriverVersion == "" && d.DriverVersion != "" {
			res.DriverVersion = d.DriverVersion
		}
		dev := v1alpha1.DeviceStatus{
			ID: deviceID(d), PCIAddress: d.PCIeAddress, Model: d.Model,
			Operations: unsupportedApply,
		}
		// MIG mode/geometry/lgip 소스: observations 주입시 관측에서(NDR MIG 필드 무시, ), 아니면 NDR.
		mc, mp, geom, obsErr, lgip := d.MigModeCurrent, d.MigModePending, d.MigCurrentGeometry, d.MigObservationError, d.MigLgipOutput
		if b.observations != nil {
			obs, ok := b.observations[d.PCIeAddress]
			if !ok {
				obs = Observation{ModeCurrent: modeUnknown, ModePending: modeUnknown, Err: "no observation for device"}
			}
			mc, mp, geom, obsErr, lgip = obs.ModeCurrent, obs.ModePending, obs.Geometry, obs.Err, obs.LgipOutput
		}
		// MIG-capability 판정: observations 주입시 lgip profiles 유무로(detector model="generic" 우회, ),
		// 관측 없으면 model regex(envtest fallback). A2 등 비-MIG 는 lgip 에 profile 이 없어 제외된다.
		profiles := ParseMigProfiles(lgip)
		migCap := migCapable(d.Model)
		if b.observations != nil {
			// mode 가 Disabled 면 `mig -lgip` 가 profile 을 전혀 내놓지 않는다 — profile 유무만으로
			// 판정하면 정작 mode enable 이 필요한 GPU 가 target 에서 빠진다(닭-달걀).
			// mig.mode.current=Disabled 는 MIG 지원 GPU 에서만 나오는 값이므로(미지원은 N/A) 그 자체가
			// capability 신호다. Unknown/N/A 는 여전히 제외하고, 관측 에러가 붙은 값도 신뢰하지 않는다
			// (pending 파싱 실패처럼 current 는 Disabled 인 채 Err 만 채워지는 경우가 있다 — fail-closed).
			migCap = len(profiles) > 0 || (mc == modeDisabled && obsErr == "")
		}
		if migCap {
			// mode Disabled 로 profile 을 못 본 장치는 교집합에 넣지 않는다 — 빈 집합을 끼우면
			// 같은 노드의 Enabled 장치가 관측한 profile 까지 전부 지워진다.
			unobservable := len(profiles) == 0 && mc == modeDisabled
			b.profilesUnobservable = b.profilesUnobservable || unobservable
			b.targets = append(b.targets, MigDevice{
				PCI: d.PCIeAddress, ModeCurrent: mc, ModePending: mp,
				Geometry: geom, ObsError: obsErr,
			})
			if !unobservable {
				profileSets = append(profileSets, profileMap(profiles))
			}
		} else {
			dev.SharingCapability.MultiProcess = mpsSupportForNonMigDevice(mc, obsErr, d.Model)
		}
		// observed = "mode 가 실제 정보를 줬는가". NDR 필드 경로에서 obsErr == "" 는 "detector 가
		// 에러를 안 적었다" 일 뿐이고 "detector 가 아예 안 봤다"까지 포함한다 — 그것만 보고
		// verified 를 찍으면 아무것도 측정하지 않은 행에 "측정됨" 이 실린다.
		// 관측 주입 경로는 영향이 없다: parseObservation 이 내는 mc 는 항상 Enabled/Disabled/NA
		// 이거나 Err 를 동반한다(observe.go:74·78·93).
		observed := obsErr == "" && (mc == modeEnabled || mc == modeDisabled || mc == modeNA)
		// migCap=false 인데 mode 가 Enabled 면 드라이버가 우리 판정과 모순한다 — Enabled 는 MIG
		// 지원 GPU 에서만 나오는 값이므로(위 migCap 주석과 같은 논리), profile 을 못 읽은 것은
		// "측정된 부정" 이 아니라 미해결이다. `nvidia-smi mig -lgip` 만 실패하는 경우가 여기다:
		// parseObservation 은 cur==Enabled 일 때 lgi 만 검증하고 lgip 은 보지 않으며(observe.go:90-95),
		// observeScript 가 2>&1 로 stderr 를 캡처하므로 실패가 Err 없이 파싱 불가 문자열로 들어온다.
		if !migCap && mc == modeEnabled {
			observed = false
		}
		dev.PartitionCapability = partitionCapabilityFrom(migCap, profiles, obsErr, observed)
		res.Devices = append(res.Devices, dev)
	}
	if len(b.targets) > 0 {
		res.Operations.Apply = v1alpha1.OperationStatus{Supported: true}
		res.Operations.Rollback = v1alpha1.OperationStatus{Supported: true}
		res.Advertisement.Mode = advProfileNamed
		b.supported = intersectProfiles(profileSets)
	}
	// capability 3축은 MIG-capable(target) 장치에만 채운다 — b.supported 는 위에서
	// 방금 확정됐으므로 이 시점 이후에만 정확하다. TimeSlicing/Isolation/AllocationAPIs 는 비-MIG
	// 장치(A2 등)에 대해 zero-value(미실측) 로 남는다(의도적 범위 제한).
	// MultiProcess 는 이미 위 루프에서 채운다(관측 mc 가 그 시점에만 있어서다).
	for i := range res.Devices {
		if !res.Devices[i].PartitionCapability.HardwareSupported {
			continue
		}
		axes := b.deviceStatusFor(targetFor(b.targets, res.Devices[i].PCIAddress), res.Devices[i].Model)
		res.Devices[i].SharingCapability = axes.SharingCapability
		res.Devices[i].IsolationCapability = axes.IsolationCapability
		res.Devices[i].AllocationAPIs = axes.AllocationAPIs
	}
	return res, nil
}

// mpsSupportForNonMigDevice 는 MIG-capable 이 아닌(또는 그렇게 판정된) 장치의 MPS 지원을 낸다.
// migCap=false 는 "MIG 자체가 없다"와 "관측이 실패해 확정 못 했다"를 둘 다 포괄한다 — 모델
// 문자열만으로 가르면 detector 가 host nvidia-smi 를 못 읽어 A30 을 generic 으로 보고하는
// 상황에서 틀린다. 관측된 mode(Enabled/Disabled)가 있으면 model 보다 그 신호를 우선한다 —
// 그 값은 MIG 지원 GPU 에서만 나오기 때문이다. 신뢰할 신호가 없을 때(Unknown/빈값/에러 섞인
// N/A)만 model 로 폴백하고, model 도 generic/빈값이면 판정을 거부한다.
func mpsSupportForNonMigDevice(mc, obsErr, model string) v1alpha1.SharingModeSupport {
	mps := v1alpha1.SharingModeSupport{Verification: v1alpha1.VerificationRequired}
	switch {
	case mc == modeEnabled || mc == modeDisabled:
		mps.Reason = reasonMPSModeUnobserved
	case mc == modeNA && obsErr == "":
		// obsErr 가 붙은 N/A 는 신뢰하지 않는다 — 형제 판정 사이트(deviceStatusFor)는 ObsError 가
		// 있으면 무조건 판정을 거부하므로, 부분 실패한 관측을 신뢰할지에 대해 두 사이트가 반대로
		// 답하면 안 된다.
		mps.Supported = true
		mps.Reason = reasonMPSNoMigHardware
	case model != "" && model != v1alpha1.DriverModelUnknown && !migCapable(model):
		// model 문자열이 실재하고("" 도 DriverModelUnknown("generic") 도 아님) migCapable 이
		// 아니라고 할 때만 신뢰한다 — DriverModelUnknown("generic")/빈값은 detector 가 host
		// nvidia-smi 를 못 읽어 "모른다"는 뜻이지 "아니다"가 아니다
		// (driver_upgrade_controller.go 의 같은 관례).
		mps.Supported = true
		mps.Reason = reasonMPSNoMigHardware
	default:
		mps.Reason = reasonMPSModeUnobserved
	}
	// 지원 판정이 선 장치에만 실측 주입을 반영한다 — 미지원 판정에 "실측 완료" 가 붙으면
	// status 가 스스로 모순한다(deviceStatusFor 도 같은 규칙).
	if mps.Supported {
		mps.Verification = verifiedIfMeasured(MultiProcessEnv)
	}
	return mps
}

// targetFor 는 PCI 로 관측된 MigDevice(ModeCurrent/ObsError 포함)를 찾는다 — 못 찾으면
// zero-value(미관측 취급, fail-closed)를 준다.
func targetFor(targets []MigDevice, pci string) MigDevice {
	for _, t := range targets {
		if t.PCI == pci {
			return t
		}
	}
	return MigDevice{PCI: pci}
}

// deviceStatusFor 는 관측된 MIG 장치 하나를 status 로 옮기며 capability 3축을 채운다.
// time-slicing 은 device-plugin 기능이라 하드웨어 무관하게 지원이지만, 검증 축은 실측 주입
// (TimeSlicingEnv)이 있을 때만 올라간다(정직성 원칙).
func (b *Backend) deviceStatusFor(d MigDevice, model string) v1alpha1.DeviceStatus {
	migCapable := len(b.supported) > 0
	iso := v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationDevice, Memory: v1alpha1.IsolationDevice, Fault: v1alpha1.IsolationDevice}
	if migCapable {
		// MIG 는 SM/메모리 슬라이스가 하드웨어로 분리된다(NVIDIA MIG User Guide).
		iso = v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationHardware, Memory: v1alpha1.IsolationHardware, Fault: v1alpha1.IsolationDevice}
	}
	// MPS 는 MIG 와 상호 배타다 — supported 는 관측에 근거하고, verification 은 실측 주입이 있을
	// 때만 올린다(위 MultiProcessEnv 주석 참조). 판정 어휘는 Discover 의 비-MIG 분기 스위치와
	// 반드시 같아야 한다: mode 를 못 읽은 상태("" / Unknown)를 "Enabled 아님 → MPS 가능" 으로
	// 통과시키면(fail-open), 관측 Job 이 실패한 바로 그 상태에 supported=true 가 실린다 — mode 가
	// 실제 정보를 준 경우에만 내고, 어느 갈래든 사유를 남긴다(빈 사유는 근거 없는 단언이 된다).
	mps := v1alpha1.SharingModeSupport{Verification: v1alpha1.VerificationRequired}
	switch {
	case d.ObsError != "":
		mps.Reason = reasonMPSObserveFailed
	case d.ModeCurrent == modeEnabled:
		mps.Reason = reasonMPSMigEnabled
	case d.ModeCurrent == modeDisabled:
		mps.Supported = true
		mps.Reason = reasonMPSMigDisabled
	default: // "" / Unknown — 관측 정보 없음.
		mps.Reason = reasonMPSModeUnobserved
	}
	if mps.Supported {
		mps.Verification = verifiedIfMeasured(MultiProcessEnv)
	}

	return v1alpha1.DeviceStatus{
		ID:             d.PCI,
		PCIAddress:     d.PCI,
		Model:          model,
		AllocationAPIs: []string{v1alpha1.AllocationAPIDevicePlugin},
		SharingCapability: v1alpha1.SharingCapability{
			TimeSlicing: v1alpha1.SharingModeSupport{
				Supported:    true,
				Verification: verifiedIfMeasured(TimeSlicingEnv),
				MaxReplicas:  maxTimeSlicingReplicas,
				Reason:       "nvidia device-plugin sharing.timeSlicing",
			},
			MultiProcess: mps,
			Brokered:     v1alpha1.SharingModeSupport{Supported: false, Verification: v1alpha1.VerificationNotApplicable},
		},
		IsolationCapability: iso,
	}
}

// maxTimeSlicingReplicas 는 정책적 상한이다(하드웨어 상한 아님 — replica 는 성능 비율이 아니다, ).
const maxTimeSlicingReplicas = 16

// Validate 는 형식·단일성·count 경계·profile 지원을 검증한다.
// 빈 layout 은 fail-closed 로 거부한다 — sharing-only(layout 없는 순수 공유) 정책은 controller
// 가 이 함수를 부르지 않고 runSharingOnly 로 라우팅한다. 이 함수는 sharing 을 볼 수 없으므로
// 여기서 빈 layout 을 통과시키면 아무것도 요청하지 않는 정책까지 하드웨어 경로에 들어온다.
func (b *Backend) Validate(layout []partition.Layout) error {
	if len(layout) == 0 {
		return partition.ErrUnsupported
	}
	if len(layout) > 1 {
		return ErrMixedPlacementUnsupported
	}
	l := layout[0]
	if !partition.IsNvidiaMIGProfile(l.Profile) {
		return partition.ErrUnsupported
	}
	if l.CountPerDevice <= 0 {
		return ErrInvalidCount
	}
	max, ok := b.supported[l.Profile]
	if !ok {
		// MIG mode 가 꺼져 있으면 `mig -lgip` 가 profile 을 내놓지 않아 supported 를 채울 수 없다.
		// 이때 "미지원" 으로 단정하면 mode enable 자체가 영영 시작되지 않으므로, 형식
		// 검증(profile 정규식 + count>0)만 통과시키고 실제 판정은 하드웨어(-cgi)에 맡긴다.
		// -cgi 는 실패 시 non-zero 로 끝나고 apply 는 fail-closed 로 rollback 된다.
		if b.profilesUnobservable {
			return nil
		}
		return ErrProfileUnsupported
	}
	if l.CountPerDevice > max {
		return ErrInvalidCount
	}
	return nil
}

// Diff 는 targets 의 관측 geometry 만 비교한다(note) — 소유(ownership) 술어는 reconciler 몫.
// resolved 가 단일 항목이 아니거나 targets 가 비어있으면 안전하게 Changed=true 로 보고한다.
func (b *Backend) Diff(_ partition.Target, resolved []partition.ResolvedEntry) (partition.DiffResult, error) {
	if len(resolved) != 1 || len(b.targets) == 0 {
		return partition.DiffResult{Changed: true}, nil
	}
	r := resolved[0]
	want := GeometrySummary(r.Profile, r.ExpectedCountPerDevice)
	for _, d := range b.targets {
		if d.Geometry != want {
			return partition.DiffResult{Changed: true, ToPolicy: want}, nil
		}
	}
	return partition.DiffResult{Changed: false, ToPolicy: want}, nil
}

// Apply 는 targets 각각에 대해 profile name 기반 GeometrySpec 을 세워 typed CommandStep 을 실행한다.
// rb 는 exec 실패시에도 non-nil 로 반환한다(부분 적용 후 rollback 판단 근거).
func (b *Backend) Apply(t partition.Target, resolved []partition.ResolvedEntry) (*partition.RollbackState, error) {
	if len(resolved) != 1 {
		return nil, ErrMixedPlacementUnsupported
	}
	r := resolved[0]
	specs := make([]GeometrySpec, 0, len(b.targets))
	for _, d := range b.targets {
		if d.PCI == "" {
			return nil, fmt.Errorf("nvidia: empty PCI, cannot target safely")
		}
		specs = append(specs, GeometrySpec{GPUSelector: d.PCI, ProfileName: r.Profile, Count: r.ExpectedCountPerDevice})
	}
	rb := &partition.RollbackState{PrevPolicy: "disabled"}
	if b.exec == nil {
		return rb, nil
	}
	steps := BuildApplySteps(specs)
	name, hash := OperationID(t.Owner, t.Generation, t.NodeName, "apply", steps)
	if err := b.exec.Run(t.Ctx, name, hash, t.NodeName, steps); err != nil {
		return rb, err
	}
	return rb, nil
}

// Rollback 은 targets 의 PCI 선택자로 GI 제거 시퀀스를 실행한다(mode 유지, GI 만 제거).
func (b *Backend) Rollback(t partition.Target, _ partition.RollbackState) error {
	if b.exec == nil {
		return nil
	}
	sels := b.targetSelectors()
	if len(sels) == 0 {
		return nil
	}
	steps := BuildDisableSteps(sels)
	name, hash := OperationID(t.Owner, t.Generation, t.NodeName, "rollback", steps)
	return b.exec.Run(t.Ctx, name, hash, t.NodeName, steps)
}

// Verify 는 stateless 다 — 매 호출 injected b.expected(reconciler 가 mig+nvidia.com/gpu 포함 구성)를 verifier 에 넘긴다.
func (b *Backend) Verify(t partition.Target) (*partition.VerifyResult, error) {
	if b.verifier == nil {
		return nil, nil
	}
	return b.verifier.VerifyAllocatable(t, b.expected)
}

// migCapable 은 모델명으로 MIG 지원 여부를 판정한다(A2 는 미지원 — spec Task-0).
// 토큰 경계 매칭 — RTX A3000 등 substring 오분류 방지.
func migCapable(model string) bool {
	return migCapableRe.MatchString(model)
}

// deviceID 는 canonical ID(PCI 우선, 없으면 model). 숫자 index 금지.
// PCI 빈 값이면 "PCI-" 접두가 비어 충돌하므로 model 로 폴백.
func deviceID(d v1alpha1.DeviceEntry) string {
	if d.PCIeAddress != "" {
		return "PCI-" + d.PCIeAddress
	}
	return d.Model
}

// partitionCapabilityFrom 은 MIG capability 판정을 PartitionCapability 로 조립한다(pure, 테스트용 추출).
// migCap 은 이미 계산된 하드웨어 판정, obsErr 는 그 판정의 관측 성패, observed 는 MIG mode 가 실제
// 정보를 줬는지다.
// HardwareSupported=false 는 "측정해서 못 한다"(observed) 와 "관측 실패/미관측이라 모른다" 를
// Verification 으로 갈라야 한다 — 안 그러면 관측 실패가 하드웨어 사실로 화면에 단언된다.
func partitionCapabilityFrom(migCap bool, profiles []MigProfile, obsErr string, observed bool) v1alpha1.PartitionCapability {
	// 규칙은 두 분기에 같다: mode 를 실제로 읽었고 에러도 없었으면 verified, 아니면 required.
	// supported=true 쪽에도 필요하다 — profile 파싱은 성공했지만 obsErr 가 붙은 경우
	// (예: pending 파싱 실패) 부분 실패한 관측으로 지원을 주장하는 셈이기 때문이다.
	// obsErr == "" 만으로는 부족하다: 아무도 안 본 장치도 obsErr 가 비어 있다.
	verification := v1alpha1.VerificationRequired
	if observed && obsErr == "" {
		verification = v1alpha1.VerificationVerified
	}
	if migCap {
		return v1alpha1.PartitionCapability{
			HardwareSupported: true, PartitionModel: "ProfileConstrained", MixedProfilesSupported: true,
			Profiles: toProfileSupport(profiles), Verification: verification,
		}
	}
	return v1alpha1.PartitionCapability{
		HardwareSupported: false, PartitionModel: "None", Reason: v1alpha1.ReasonHardwareCapabilityMissing,
		Verification: verification,
	}
}

func toProfileSupport(ps []MigProfile) []v1alpha1.ProfileSupport {
	out := make([]v1alpha1.ProfileSupport, 0, len(ps))
	for _, p := range ps {
		out = append(out, v1alpha1.ProfileSupport{Name: p.Name, MaxInstancesPerDevice: p.MaxInstances, SupportLevel: v1alpha1.SupportDocumented})
	}
	return out
}

// GeometrySummary 는 Diff·검증이 관측 Geometry 와 비교하는 목표 문자열이다(format: "<profile> x<count>").
// 패키지 밖(검증 계층·테스트 fixture)에서도 같은 문자열을 만들어야 하므로 공개한다 — 기대값을
// 손으로 적으면 형식이 바뀌는 날 조용히 어긋난다.
func GeometrySummary(profile string, count int32) string {
	return fmt.Sprintf("%s x%d", profile, count)
}

// profileMap 은 MigProfile 목록을 name→MaxInstances 맵으로 변환한다(intersectProfiles 입력).
func profileMap(ps []MigProfile) map[string]int32 {
	m := make(map[string]int32, len(ps))
	for _, p := range ps {
		m[p.Name] = p.MaxInstances
	}
	return m
}

// intersectProfiles 는 sets 전원에 존재하는 profile 만 남기고 MaxInstances 는 최소값을 취한다
// (b.supported 는 여러 target GPU 모두가 지원하는 profile 만 신뢰해야 한다).
func intersectProfiles(sets []map[string]int32) map[string]int32 {
	out := map[string]int32{}
	if len(sets) == 0 {
		return out
	}
	for name, first := range sets[0] {
		lo := first
		present := true
		for _, s := range sets[1:] {
			v, ok := s[name]
			if !ok {
				present = false
				break
			}
			if v < lo {
				lo = v
			}
		}
		if present {
			out[name] = lo
		}
	}
	return out
}
