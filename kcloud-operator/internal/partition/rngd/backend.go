// ============================================================
// backend.go: RNGD 파티션 backend — DS env(RNGD_PARTITION_POLICY) 기반 apply
// 상세: discover/validate/diff/apply/rollback/verify. 적용 범위 = DaemonSet 전역(DaemonSetGlobal).
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package rngd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition"
)

const (
	RngdResourceName    = "furiosa.ai/rngd"
	partitionPolicyEnv  = "RNGD_PARTITION_POLICY"
	pluginContainerName = "furiosa-device-plugin"
)

// PartitionOwnerAnnotation 은 이 DS 의 partition env 를 ACPP 가 소유함을 표시한다(두 writer 조정).
const PartitionOwnerAnnotation = "npu.ai/partition-owner"

// MultiProcessEnv 는 라이브 실측(test/live/rngd-multiprocess) 결과를 operator 에 주입하는 env 다.
// 값: "verified"(동시 실행 확인) | "unsupported"(실측 결과 불가) | 미설정(미실측).
// 코드가 임의로 지원을 주장하지 않게 하기 위한 명시 주입 경로다(정직성 원칙).
const MultiProcessEnv = "KCLOUD_RNGD_MULTIPROCESS"

// MultiProcessSupportFromEnv 는 실측 주입값을 SharingModeSupport 로 옮긴다.
func MultiProcessSupportFromEnv() v1alpha1.SharingModeSupport {
	switch os.Getenv(MultiProcessEnv) {
	case "verified":
		return v1alpha1.SharingModeSupport{
			Supported: true, Verification: v1alpha1.VerificationVerified,
			Reason: "measured: concurrent processes on one RNGD device",
		}
	case "unsupported":
		return v1alpha1.SharingModeSupport{
			Supported: false, Verification: v1alpha1.VerificationVerified,
			Reason: "measured: concurrent process execution not possible",
		}
	default:
		return v1alpha1.SharingModeSupport{
			Supported: false, Verification: v1alpha1.VerificationRequired,
			Reason: "runtime multi-process not measured yet",
		}
	}
}

var _ partition.Backend = (*Backend)(nil)

// Backend 은 RNGD 파티션 backend 다.
type Backend struct {
	c        client.Client
	verifier partition.Verifier
}

func New(c client.Client) *Backend { return &Backend{c: c} }

// WithVerifier 는 하드웨어 의존 검증 seam 을 주입한다(envtest 는 fake 주입, nil 이면 reconciler 가 verify 건너뜀).
func (b *Backend) WithVerifier(v partition.Verifier) *Backend { b.verifier = v; return b }

func (b *Backend) Vendor() string { return "furiosa" }

// Validate 는 각 layout profile 을 mapping 규칙으로 검증한다.
func (b *Backend) Validate(layout []partition.Layout) error {
	for _, l := range layout {
		if err := ValidateLayout(l.Profile, l.CountPerDevice); err != nil {
			return err
		}
	}
	return nil
}

// Diff 는 현재 DS env policy 와 목표 backendPolicy 를 비교한다(no-diff 조건부 전이).
func (b *Backend) Diff(t partition.Target, resolved []partition.ResolvedEntry) (partition.DiffResult, error) {
	// 입력 검증을 API fetch 전에 수행 — 잘못된 resolved 는 DS 조회 실패보다 이 메시지가 더 유용.
	if len(resolved) != 1 {
		return partition.DiffResult{}, fmt.Errorf("rngd: expected exactly 1 resolved entry (fixed-profile), got %d", len(resolved))
	}
	ds, err := b.getDS(t)
	if err != nil {
		return partition.DiffResult{}, err
	}
	cur := envValue(ds)
	target := resolved[0].BackendPolicy
	return partition.DiffResult{Changed: cur != target, FromPolicy: cur, ToPolicy: target}, nil
}

// Discover 는 evidence 우선순위로 RNGD 상태를 조회한다:
// (1) 물리 inventory+UUID(NDR), (2) DP config(RNGD_PARTITION_POLICY), (3) allocatable.
// MVP-1 은 policy env + NDR inventory 교차 확인. profiles = mapping 테이블, advertisement = Flat.
func (b *Backend) Discover(t partition.Target) (*partition.DiscoverResult, error) {
	ds, err := b.getDS(t)
	if err != nil {
		return nil, err
	}
	res := &partition.DiscoverResult{
		Backend: v1alpha1.BackendRef{
			Kind: "DaemonSet", Namespace: ds.Namespace, Name: ds.Name,
			UID: string(ds.UID), Version: dsImageVersion(ds),
			ConfigurationScope: "DaemonSetGlobal",
		},
		Operations: v1alpha1.OperationsStatus{
			Apply:    v1alpha1.OperationStatus{Supported: true},
			Rollback: v1alpha1.OperationStatus{Supported: true},
		},
		Advertisement: v1alpha1.AdvertisementStatus{
			Mode: "Flat", ProfileNamedSupported: false,
			Reason: v1alpha1.ReasonDevicePluginFlatAdvertisementOnly,
		},
	}

	// (2) 현재 backend policy → observed/resolved layout (역방향 resolve).
	cur := envValue(ds)
	if rp, ok := ResolvePolicy(cur); ok {
		res.Observed = []v1alpha1.ObservedLayoutEntry{{BackendPolicy: cur, ObservedCountPerDevice: rp.CountPerDevice}}
		res.Resolved = []v1alpha1.ResolvedLayoutEntry{{Profile: rp.Profile, BackendPolicy: cur, ExpectedCountPerDevice: rp.CountPerDevice}}
	} else if cur != "" {
		res.Observed = []v1alpha1.ObservedLayoutEntry{{BackendPolicy: cur}}
	}

	// (1) NDR inventory → devices + driverVersion.
	var ndr v1alpha1.NodeDeviceReport
	if err := b.c.Get(t.Ctx, types.NamespacedName{Name: t.NodeName}, &ndr); err == nil {
		for _, d := range ndr.Status.Devices {
			// 모델 표기는 detector 판에 따라 대소문자가 갈린다(라이브 0.6.4 는 "rngd"). 정확
			// 일치로 거르면 장치가 하나도 안 잡혀 status.devices 가 비고, 그 위에 얹힌 검증이
			// "관측 전무" 로 영구 실패한다.
			if !strings.EqualFold(d.Vendor, "furiosa") || !strings.EqualFold(d.Model, "rngd") {
				continue
			}
			res.DriverVersion = d.DriverVersion
			dev := v1alpha1.DeviceStatus{
				ID:         deviceID(d),
				PCIAddress: d.PCIeAddress,
				Model:      d.Model,
				PartitionCapability: v1alpha1.PartitionCapability{
					HardwareSupported: true, PartitionModel: "FixedProfile", MixedProfilesSupported: false,
					Profiles: capabilityProfiles(),
				},
				Operations: res.Operations,
			}
			axes := deviceStatusFor(d.Vendor, d.Model)
			dev.SharingCapability = axes.SharingCapability
			dev.IsolationCapability = axes.IsolationCapability
			dev.AllocationAPIs = axes.AllocationAPIs
			res.Devices = append(res.Devices, dev)
		}
	}
	return res, nil
}

// capabilityProfiles 는 mapping 테이블에서 노출 가능한 profile(비-Legacy)만 반환한다.
func capabilityProfiles() []v1alpha1.ProfileSupport {
	out := make([]v1alpha1.ProfileSupport, 0, len(rngdProfileTable))
	for _, p := range rngdProfileTable {
		if p.SupportLevel == v1alpha1.SupportLegacyDocumented {
			continue // 노출 전 공식 표기 확인 필요
		}
		out = append(out, v1alpha1.ProfileSupport{Name: p.Profile, MaxInstancesPerDevice: p.CountPerDevice, SupportLevel: p.SupportLevel})
	}
	return out
}

// deviceStatusFor 는 RNGD 장치 status 를 만들며 capability 3축을 채운다. TimeSlicing/Brokered 는
// 미구현(required), MultiProcess 는 실측 결과(MultiProcessEnv)를 반영한다. vendor 매개변수는
// 실측 근거가 없어 시그니처만 유지한다(호출부 대칭, 향후 벤더별 분기 대비).
// Isolation 세 축은 미실측(빈 문자열)이다 — FuriosaAI 의 libfuriosa-kubernetes 코드가 PE
// 필터링에서도 관리 인터페이스·DMA·health 를 카드 단위로 공유해 subdevice 주장을 반증하고,
// none 도 minimumIsolation=none 요구를 잘못 통과시켜 쓸 수 없다. 격상은 PE 간 간섭 실측이 선행 조건이다.
func deviceStatusFor(_, model string) v1alpha1.DeviceStatus {
	return v1alpha1.DeviceStatus{
		Model:          model,
		AllocationAPIs: []string{v1alpha1.AllocationAPIDevicePlugin},
		SharingCapability: v1alpha1.SharingCapability{
			TimeSlicing:  v1alpha1.SharingModeSupport{Supported: false, Verification: v1alpha1.VerificationRequired, Reason: "furiosa device-plugin has no replica/time-slicing option"},
			MultiProcess: MultiProcessSupportFromEnv(),
			Brokered:     v1alpha1.SharingModeSupport{Supported: false, Verification: v1alpha1.VerificationRequired, Reason: "broker not implemented"},
		},
		IsolationCapability: v1alpha1.IsolationCapability{},
	}
}

// deviceID 는 canonical ID(UUID 우선, 없으면 PCI). 숫자 index 금지.
func deviceID(d v1alpha1.DeviceEntry) string {
	if d.PCIeAddress != "" {
		return "PCI-" + d.PCIeAddress
	}
	return d.Model
}

// dsImageVersion 은 plugin 컨테이너 이미지 태그를 반환한다(backend.version, driver 와 분리).
// registry:port 접두(예 registry.example.com:5000/...)의 ':' 를 태그로 오인하지 않도록,
// 마지막 '/' 뒤(레지스트리·경로 제거)에서 태그 ':' 를 찾는다. digest(@) 나 태그 미지정은 "".
func dsImageVersion(ds *appsv1.DaemonSet) string {
	for _, ctr := range ds.Spec.Template.Spec.Containers {
		if ctr.Name != pluginContainerName {
			continue
		}
		ref := ctr.Image
		if slash := strings.LastIndex(ref, "/"); slash >= 0 {
			ref = ref[slash+1:] // registry[:port]/path 제거 → name[:tag]
		}
		if i := strings.LastIndex(ref, ":"); i >= 0 {
			return ref[i+1:]
		}
	}
	return ""
}

// Apply 는 DS RNGD_PARTITION_POLICY 를 목표 policy 로 patch 하고 apply 전 상태를 rollback state 로 반환한다.
// 적용 범위 = DaemonSetGlobal. owner 어노테이션으로 NPUClusterPolicy 와의 경합을 차단.
func (b *Backend) Apply(t partition.Target, resolved []partition.ResolvedEntry) (*partition.RollbackState, error) {
	if len(resolved) != 1 {
		return nil, fmt.Errorf("rngd: expected exactly 1 resolved entry, got %d", len(resolved))
	}
	ds, err := b.getDS(t)
	if err != nil {
		return nil, err
	}
	prev := envValue(ds)
	state := &partition.RollbackState{
		DaemonSetUID:    string(ds.UID),
		PrevPolicy:      prev,
		Generation:      ds.Generation,
		ResourceVersion: ds.ResourceVersion,
		TemplateHash:    templateHash(ds),
	}
	setEnvValue(ds, partitionPolicyEnv, resolved[0].BackendPolicy)
	if ds.Annotations == nil {
		ds.Annotations = map[string]string{}
	}
	ds.Annotations[PartitionOwnerAnnotation] = t.Owner // 소유 ACPP 식별자(.metadata.name) — 두 writer 조정
	if err := b.c.Update(t.Ctx, ds); err != nil {
		return nil, err
	}
	return state, nil
}

// setEnvValue 는 plugin 컨테이너의 name env 를 value 로 설정한다(없으면 append).
func setEnvValue(ds *appsv1.DaemonSet, name, value string) {
	for ci := range ds.Spec.Template.Spec.Containers {
		ctr := &ds.Spec.Template.Spec.Containers[ci]
		if ctr.Name != pluginContainerName {
			continue
		}
		for ei := range ctr.Env {
			if ctr.Env[ei].Name == name {
				ctr.Env[ei].Value = value
				return
			}
		}
		ctr.Env = append(ctr.Env, corev1.EnvVar{Name: name, Value: value})
		return
	}
}

// templateHash 는 pod template 의 안정 해시다(rollback 복원 검증 기준, ).
func templateHash(ds *appsv1.DaemonSet) string {
	b, _ := json.Marshal(ds.Spec.Template.Spec)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Rollback 은 저장된 DS UID 가 현재 DS 와 일치할 때만 prevPolicy 로 복원한다(RollbackFailed).
func (b *Backend) Rollback(t partition.Target, s partition.RollbackState) error {
	ds, err := b.getDS(t)
	if err != nil {
		return err
	}
	if string(ds.UID) != s.DaemonSetUID {
		return fmt.Errorf("rngd: rollback UID mismatch: DS=%s saved=%s", ds.UID, s.DaemonSetUID)
	}
	setEnvValue(ds, partitionPolicyEnv, s.PrevPolicy)
	return b.c.Update(t.Ctx, ds)
}

// Verify 는 주입된 verifier 에 위임한다(nil 이면 검증 skip 신호로 nil,nil).
func (b *Backend) Verify(t partition.Target) (*partition.VerifyResult, error) {
	if b.verifier == nil {
		return nil, nil
	}
	// 목표 allocatable = 카드 수 × countPerDevice 는 reconciler 가 계산해 전달하는 편이 자연스러우나,
	// MVP-1 은 flat 리소스 존재만 확인(1-파티션 테스트 Pod). 상세는 reconciler 에서.
	return b.verifier.VerifyAllocation(t, RngdResourceName)
}

func (b *Backend) getDS(t partition.Target) (*appsv1.DaemonSet, error) {
	var ds appsv1.DaemonSet
	key := types.NamespacedName{Name: t.DaemonSetName, Namespace: t.DaemonSetNamespace}
	if err := b.c.Get(t.Ctx, key, &ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

// envValue 는 plugin 컨테이너에서 partitionPolicyEnv 값을 읽는다(없으면 "").
func envValue(ds *appsv1.DaemonSet) string {
	for _, ctr := range ds.Spec.Template.Spec.Containers {
		if ctr.Name != pluginContainerName {
			continue
		}
		for _, e := range ctr.Env {
			if e.Name == partitionPolicyEnv {
				return e.Value
			}
		}
	}
	return ""
}
