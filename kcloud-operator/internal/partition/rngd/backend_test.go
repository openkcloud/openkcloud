// backend_test.go: RNGD backend Validate/Diff 단위 테스트 (fake client)
package rngd

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
	"kcloud-operator/internal/partition"
)

// testDualCorePolicy 는 테스트 전반에서 반복되는 backendPolicy 리터럴 정리(goconst).
const testDualCorePolicy = "dual-core"

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	return s
}

func dsWithPolicy(policy string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-unified-device-plugin", Namespace: "kube-system", UID: "ds-uid-1"},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "furiosa-device-plugin",
				Image: "registry.example.com:5000/kcloud/furiosa-unified-device-plugin:0.1.0", // port-qualified registry
				Env: []corev1.EnvVar{
					{Name: partitionPolicyEnv, Value: policy},
				},
			}},
		}}},
	}
}

// TestMultiProcessSupportFromEnv 는 실측 주입 env(KCLOUD_RNGD_MULTIPROCESS)가 3가지 상태를
// 정직하게(임의 추정 없이) SharingModeSupport 로 옮기는지 검증한다(정직성 원칙).
func TestMultiProcessSupportFromEnv(t *testing.T) {
	t.Setenv(MultiProcessEnv, "")
	if got := MultiProcessSupportFromEnv(); got.Supported || got.Verification != v1alpha1.VerificationRequired {
		t.Fatalf("unset env must stay unverified: %+v", got)
	}
	t.Setenv(MultiProcessEnv, "verified")
	if got := MultiProcessSupportFromEnv(); !got.Supported || got.Verification != v1alpha1.VerificationVerified {
		t.Fatalf("verified env: %+v", got)
	}
	t.Setenv(MultiProcessEnv, "unsupported")
	got := MultiProcessSupportFromEnv()
	if got.Supported || got.Verification != v1alpha1.VerificationVerified {
		t.Fatalf("measured-unsupported must be verified-false: %+v", got)
	}
	if got.Reason == "" {
		t.Fatalf("unsupported must carry a reason")
	}
}

// TestDiscoverFillsCapabilityAxesRngd 는 deviceStatusFor 가 capability 3축을 정직하게 채우는지 검증한다.
func TestDiscoverFillsCapabilityAxesRngd(t *testing.T) {
	d := deviceStatusFor("furiosa", "rngd")
	if d.SharingCapability.TimeSlicing.Supported || d.SharingCapability.MultiProcess.Supported {
		t.Fatalf("RNGD sharing must not be claimed before measurement: %+v", d.SharingCapability)
	}
	if d.SharingCapability.MultiProcess.Verification != v1alpha1.VerificationRequired {
		t.Fatalf("multiProcess verification = %q, want required", d.SharingCapability.MultiProcess.Verification)
	}
	if (d.IsolationCapability != v1alpha1.IsolationCapability{}) {
		// 세 축 전부 미실측이어야 한다. compute 를 subdevice 로 적었던 과거 회귀를 막는 핀이다 —
		// 파티션이 카드의 mgmt·bar·DMA 채널을 공유하고 health 도 카드 단위라 subdevice 근거가 없다
		// (deviceStatusFor 주석의 벤더 코드 인용 참조). 등급을 새로 적으려면 실측이 먼저다.
		t.Fatalf("RNGD isolation must stay unmeasured on every axis, got %+v", d.IsolationCapability)
	}
}

// TestRngdIsolationRejectsMinimumIsolation 는 미실측 격리가 배치 게이트에서 실제로
// fail-closed 로 작동하는지를 소비자(intent.CheckRequirements) 쪽에서 확인한다.
// memory 축이 미실측이므로 minimumIsolation 이 걸린 요청은 항상 거절돼야 한다 — 이 시험이
// 고정하는 것은 거절이 올바른 축(compute)을 지목하는지다.
func TestRngdIsolationRejectsMinimumIsolation(t *testing.T) {
	nc := intent.NodeCapability{NodeName: "rngd-1", Vendor: "furiosa",
		Devices: []v1alpha1.DeviceStatus{deviceStatusFor("furiosa", "rngd")}}
	req := v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationSubdevice}
	access := v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitioned}

	rj := intent.CheckRequirements(nc, req, access, v1alpha1.AcceleratorMapping{NativeProfile: "2core.12gb"})
	if rj == nil {
		t.Fatalf("unmeasured isolation must not satisfy minimumIsolation=%q", req.MinimumIsolation)
	}
	if rj.Reason != v1alpha1.AWReasonCapabilityUnverified {
		t.Fatalf("reject reason = %q, want %q (unmeasured, not too-weak)", rj.Reason, v1alpha1.AWReasonCapabilityUnverified)
	}
	if !strings.Contains(rj.Message, "isolation.compute") {
		t.Fatalf("reject must name the compute axis as unmeasured, got %q", rj.Message)
	}
}

func TestRngdValidate(t *testing.T) {
	b := New(fake.NewClientBuilder().Build())
	if err := b.Validate([]partition.Layout{{Profile: "2core.12gb", CountPerDevice: 4}}); err != nil {
		t.Errorf("valid layout rejected: %v", err)
	}
	if err := b.Validate([]partition.Layout{{Profile: "1core.6gb"}}); err == nil {
		t.Error("LegacyDocumented profile should be rejected")
	}
}

func TestRngdDiff(t *testing.T) {
	c := fake.NewClientBuilder().WithObjects(dsWithPolicy(testDualCorePolicy)).Build()
	b := New(c)
	tg := partition.Target{Ctx: context.Background(), NodeName: "rngd-1",
		DaemonSetName: "furiosa-unified-device-plugin", DaemonSetNamespace: "kube-system"}

	// 같은 policy → no-diff
	d, err := b.Diff(tg, []partition.ResolvedEntry{{Profile: "2core.12gb", BackendPolicy: testDualCorePolicy}})
	if err != nil || d.Changed {
		t.Fatalf("expected no-diff, got %+v err=%v", d, err)
	}
	// 다른 policy → changed
	d, _ = b.Diff(tg, []partition.ResolvedEntry{{Profile: "4core.24gb", BackendPolicy: "quad-core"}})
	if !d.Changed || d.FromPolicy != testDualCorePolicy || d.ToPolicy != "quad-core" {
		t.Fatalf("expected change dual-core→quad-core, got %+v", d)
	}
}

// TestRngdDiff_ResolvedCountGuard 는 fixed-profile 가드(정확히 1 resolved entry)를 검증한다.
// 0개·2개는 error. DS 존재 여부와 무관하게 입력 검증이 먼저 걸린다.
func TestRngdDiff_ResolvedCountGuard(t *testing.T) {
	c := fake.NewClientBuilder().WithObjects(dsWithPolicy(testDualCorePolicy)).Build()
	b := New(c)
	tg := partition.Target{Ctx: context.Background(), NodeName: "rngd-1",
		DaemonSetName: "furiosa-unified-device-plugin", DaemonSetNamespace: "kube-system"}

	if _, err := b.Diff(tg, nil); err == nil {
		t.Error("0 resolved entries should error")
	}
	if _, err := b.Diff(tg, []partition.ResolvedEntry{
		{BackendPolicy: testDualCorePolicy}, {BackendPolicy: "quad-core"},
	}); err == nil {
		t.Error("2 resolved entries should error")
	}
	// DS 부재 + 잘못된 count → count 가드 메시지가 우선(더 유용).
	empty := New(fake.NewClientBuilder().Build())
	if _, err := empty.Diff(tg, nil); err == nil {
		t.Error("count guard should fire before DS fetch")
	}
}

// TestRngdApply 는 DS env patch + rollback state(apply 전 값) + owner 어노테이션을 검증한다.
func TestRngdApply(t *testing.T) {
	ds := dsWithPolicy(testDualCorePolicy)
	ds.Generation = 7
	ds.ResourceVersion = "100"
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(ds).Build()
	b := New(c)
	tg := partition.Target{Ctx: context.Background(), NodeName: "rngd-1", Owner: "acpp-a",
		DaemonSetName: "furiosa-unified-device-plugin", DaemonSetNamespace: "kube-system"}

	rb, err := b.Apply(tg, []partition.ResolvedEntry{{Profile: "4core.24gb", BackendPolicy: "quad-core"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// rollback state = apply 전 값 (전 5필드 검증 — TemplateHash 는 rollback 기준, )
	if rb.DaemonSetUID != "ds-uid-1" || rb.PrevPolicy != testDualCorePolicy || rb.Generation != 7 {
		t.Errorf("rollback state: %+v", rb)
	}
	if rb.ResourceVersion != "100" {
		t.Errorf("rollback ResourceVersion: %q, want 100", rb.ResourceVersion)
	}
	if rb.TemplateHash == "" {
		t.Error("rollback TemplateHash empty — must be stable hash")
	}
	// DS env 가 목표로 변경 + owner 어노테이션 = target.Owner(ACPP .metadata.name, 두 writer 조정)
	var got appsv1.DaemonSet
	_ = c.Get(context.Background(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, &got)
	if envValue(&got) != "quad-core" {
		t.Errorf("env not patched: %q", envValue(&got))
	}
	if got.Annotations[PartitionOwnerAnnotation] != "acpp-a" {
		t.Errorf("owner annotation: %q, want acpp-a (t.Owner)", got.Annotations[PartitionOwnerAnnotation])
	}
}

// TestRngdRollback 은 저장된 DS UID 가 현재와 일치할 때 prevPolicy 로 복원됨을 검증한다.
func TestRngdRollback(t *testing.T) {
	ds := dsWithPolicy("quad-core")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(ds).Build()
	b := New(c)
	tg := partition.Target{Ctx: context.Background(), NodeName: "rngd-1",
		DaemonSetName: "furiosa-unified-device-plugin", DaemonSetNamespace: "kube-system"}

	err := b.Rollback(tg, partition.RollbackState{DaemonSetUID: "ds-uid-1", PrevPolicy: testDualCorePolicy})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var got appsv1.DaemonSet
	_ = c.Get(context.Background(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, &got)
	if envValue(&got) != testDualCorePolicy {
		t.Errorf("not restored: %q", envValue(&got))
	}
}

// TestRngdRollback_UIDMismatchFails 는 DS 재생성(UID 변경) 시 복원을 거부함을 검증한다(RollbackFailed 유도).
func TestRngdRollback_UIDMismatchFails(t *testing.T) {
	ds := dsWithPolicy("quad-core")
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(ds).Build()
	b := New(c)
	tg := partition.Target{Ctx: context.Background(), NodeName: "rngd-1",
		DaemonSetName: "furiosa-unified-device-plugin", DaemonSetNamespace: "kube-system"}
	// DS 가 재생성되어 UID 다름 → 복원 거부(RollbackFailed 유도).
	if err := b.Rollback(tg, partition.RollbackState{DaemonSetUID: "stale-uid", PrevPolicy: testDualCorePolicy}); err == nil {
		t.Error("UID mismatch should fail rollback")
	}
}

func TestRngdDiscover(t *testing.T) {
	ds := dsWithPolicy(testDualCorePolicy)
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "rngd-1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "furiosa", Model: "RNGD", Count: 1, DriverVersion: "2026.1.0", PCIeAddress: "0000:01:00.0"},
		}},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(ds, ndr).Build()
	b := New(c)
	tg := partition.Target{Ctx: context.Background(), NodeName: "rngd-1",
		DaemonSetName: "furiosa-unified-device-plugin", DaemonSetNamespace: "kube-system"}

	res, err := b.Discover(tg)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if res.Backend.UID != "ds-uid-1" || res.Backend.ConfigurationScope != "DaemonSetGlobal" {
		t.Errorf("backend ref: %+v", res.Backend)
	}
	if res.DriverVersion != "2026.1.0" {
		t.Errorf("driverVersion: %q", res.DriverVersion)
	}
	// dual-core → observedLayout 2core.12gb/4
	if len(res.Observed) != 1 || res.Observed[0].BackendPolicy != testDualCorePolicy || res.Observed[0].ObservedCountPerDevice != 4 {
		t.Errorf("observed: %+v", res.Observed)
	}
	if res.Advertisement.Mode != "Flat" || !res.Operations.Apply.Supported {
		t.Errorf("adv/ops: %+v %+v", res.Advertisement, res.Operations)
	}
	if len(res.Devices) != 1 || !res.Devices[0].PartitionCapability.HardwareSupported {
		t.Errorf("devices: %+v", res.Devices)
	}
	// backend.version = 태그만(port-qualified registry 의 ':' 오인 방지).
	if res.Backend.Version != "0.1.0" {
		t.Errorf("backend.version: %q, want 0.1.0", res.Backend.Version)
	}
	// capability profiles 는 LegacyDocumented(1core.6gb/single-core) 제외 — 2개(2core/4core)만.
	profs := res.Devices[0].PartitionCapability.Profiles
	if len(profs) != 2 {
		t.Errorf("expected 2 exposed profiles (no LegacyDocumented), got %d: %+v", len(profs), profs)
	}
	for _, p := range profs {
		if p.SupportLevel == v1alpha1.SupportLegacyDocumented {
			t.Errorf("LegacyDocumented profile leaked: %+v", p)
		}
	}
}

// TestDiscoverAcceptsLowercaseModel 은 detector 가 보내는 실제 표기("rngd")로도 장치가 잡히는지
// 고정한다. 정확 일치("RNGD")로 거르면 status.devices 가 비고, 그 위에 얹힌 검증이 "관측 전무"
// 로 영구 실패한다.
func TestDiscoverAcceptsLowercaseModel(t *testing.T) {
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{{
			Vendor: "furiosa", Model: "rngd", Count: 1, DriverVersion: "2026.1.0",
		}}},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme()).
		WithObjects(dsWithPolicy(testDualCorePolicy), ndr).Build()
	res, err := New(c).Discover(partition.Target{Ctx: context.Background(), NodeName: "rngd-1",
		DaemonSetName: "furiosa-unified-device-plugin", DaemonSetNamespace: "kube-system"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Devices) != 1 {
		t.Fatalf("devices = %d, want 1 (lowercase model must be accepted)", len(res.Devices))
	}
	if res.DriverVersion != "2026.1.0" {
		t.Errorf("driverVersion = %q, want 2026.1.0", res.DriverVersion)
	}
}

// fakeVerifier 는 partition.Verifier 를 구현해 Verify 에 넘겨진 Target 을 기록한다.
type fakeVerifier struct {
	res       *partition.VerifyResult
	err       error
	gotTarget partition.Target
}

func (f *fakeVerifier) VerifyAllocatable(t partition.Target, _ map[string]int32) (*partition.VerifyResult, error) {
	f.gotTarget = t
	return f.res, f.err
}

func (f *fakeVerifier) VerifyAllocation(t partition.Target, _ string) (*partition.VerifyResult, error) {
	f.gotTarget = t
	return f.res, f.err
}

// TestRngdVerifyPassesVendor 는 Target.Vendor 가 주입된 verifier 까지 그대로 전달되는지 검증한다
// (apply 경로 벤더 probe 스위치, furiosa 기능 probe 활성의 전제).
func TestRngdVerifyPassesVendor(t *testing.T) {
	fv := &fakeVerifier{res: &partition.VerifyResult{TestPodAllocated: true}}
	b := New(nil).WithVerifier(fv)
	target := partition.Target{Ctx: context.Background(), NodeName: "rngd-1", Vendor: "furiosa"}

	if _, err := b.Verify(target); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if fv.gotTarget.Vendor != "furiosa" {
		t.Errorf("verifier got Target.Vendor = %q, want %q", fv.gotTarget.Vendor, "furiosa")
	}
}
