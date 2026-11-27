// ============================================================
// verifier_test.go: Verifier 배선 테스트
// 상세: 노드·NDR·정책을 읽어 판정하고 evidence 를 남기는 이음매를 확인한다. 지문 계산·판정·TTL 이
// 각각 맞아도 이음매가 틀리면 evidence 가 거짓을 말하므로 여기서 함께 본다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

func resourceQuantity(v int64) *resource.Quantity {
	return resource.NewQuantity(v, resource.DecimalSI)
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

type fakeProber struct {
	allocated bool
	// result 가 있으면 그대로 돌려준다(종료코드·출력까지 실어 보내는 경로 확인용).
	result    *ProbeResult
	called    int
	gotVendor string
}

func (p *fakeProber) Probe(_ context.Context, _, _, vendor string) (ProbeResult, error) {
	p.called++
	p.gotVendor = vendor
	if p.result != nil {
		return *p.result, nil
	}
	return ProbeResult{Allocated: p.allocated}, nil
}

func seedNodeAndReport(t *testing.T, geom string, allocatable map[string]int64) (*corev1.Node, *v1alpha1.NodeDeviceReport) {
	t.Helper()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1", Labels: map[string]string{"role": "gpu"}},
		Status: corev1.NodeStatus{
			NodeInfo:    corev1.NodeSystemInfo{BootID: "boot-1", KernelVersion: "5.15.0-181-generic"},
			Allocatable: corev1.ResourceList{},
		},
	}
	for k, v := range allocatable {
		node.Status.Allocatable[corev1.ResourceName(k)] = *resourceQuantity(v)
	}
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "worker1"},
		Status: v1alpha1.NodeDeviceReportStatus{
			Devices: []v1alpha1.DeviceEntry{{
				Vendor: "nvidia", PCIeAddress: testPCI, DriverVersion: "535.104.05",
				MigCurrentGeometry: geom,
			}},
		},
	}
	return node, ndr
}

func verifyRequest(geom string) Request {
	return Request{
		NodeName: "worker1", Vendor: "nvidia", SourcePolicy: "acpp-1", Generation: 3,
		Expectation: Expectation{
			Profile: "1g.6gb", CountPerDevice: 4, Geometry: geom,
			Allocatable:   map[string]int32{"nvidia.com/mig-1g.6gb": 4},
			ProbeResource: "nvidia.com/mig-1g.6gb",
		},
		ObservedGeometry:  map[string]string{testPCI: geom},
		ObservationErrors: map[string]string{},
	}
}

func TestVerifyWritesEvidenceWithFingerprintAndExpiry(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()

	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	v := &Verifier{Client: c, Now: func() time.Time { return now }}
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status.Level != v1alpha1.EvidenceLevelFunctionallyVerified {
		t.Fatalf("level = %q, reason=%q checks=%+v", ev.Status.Level, ev.Status.Reason, ev.Status.Checks)
	}
	if ev.Status.Fingerprint.BootID != "boot-1" || ev.Status.Fingerprint.DriverVersion != "535.104.05" {
		t.Fatalf("지문이 노드·보고에서 오지 않았다: %+v", ev.Status.Fingerprint)
	}
	if ev.Status.Fingerprint.Generation != 3 {
		t.Fatalf("generation = %d", ev.Status.Fingerprint.Generation)
	}
	if ev.Status.ExpiresAt == nil || !ev.Status.ExpiresAt.Time.Equal(now.Add(DefaultPolicy().TTL)) {
		t.Fatalf("만료 시각이 TTL 과 다르다: %+v", ev.Status.ExpiresAt)
	}
	if ev.Status.AdvertisedResources["nvidia.com/mig-1g.6gb"] != 4 {
		t.Fatalf("검증 시점 광고량이 보존되지 않았다: %+v", ev.Status.AdvertisedResources)
	}
	if ev.Spec.NodeName != "worker1" || ev.Spec.Vendor != "nvidia" {
		t.Fatalf("spec = %+v", ev.Spec)
	}
}

func TestVerifyRecordsFailureWithoutLevelAndWithoutError(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	// 광고가 부족한 노드 — 판정은 실패하지만 이것은 API 에러가 아니다.
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 1})
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()

	v := &Verifier{Client: c, Now: time.Now}
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatalf("판정 실패를 에러로 올렸다: %v", err)
	}
	if ev.Status.Level != "" {
		t.Fatalf("불일치인데 등급이 붙었다: %q", ev.Status.Level)
	}
	if ev.Status.Reason == "" {
		t.Fatalf("실패 사유가 비었다")
	}
	if len(ev.Status.AdvertisedResources) != 0 {
		t.Fatalf("검증 실패인데 광고량을 기록했다 — drift 기준선이 오염된다: %+v", ev.Status.AdvertisedResources)
	}
}

func TestVerifyIsIdempotentAndUpdatesInPlace(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	v := &Verifier{Client: c, Now: time.Now}
	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	var list v1alpha1.AcceleratorEvidenceList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("evidence 가 %d 개다 — 노드당 하나여야 한다", len(list.Items))
	}
}

func TestVerifyRunsProbeOnlyWhenPolicyAsksForIt(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	avpObj := &v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "with-probe"},
		Spec: v1alpha1.AcceleratorVerificationPolicySpec{
			NodeSelector: map[string]string{"role": "gpu"},
			Checks: []string{
				v1alpha1.EvidenceCheckDeviceObservation,
				v1alpha1.EvidenceCheckNodeDeviceReport,
				v1alpha1.EvidenceCheckAdvertisement,
				v1alpha1.EvidenceCheckAllocationProbe,
			},
		},
	}
	p := &fakeProber{allocated: true}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr, avpObj).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	v := &Verifier{Client: c, Prober: p, Now: time.Now}
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if p.called != 1 {
		t.Fatalf("프로브 호출 %d회", p.called)
	}
	if p.gotVendor != "nvidia" {
		t.Fatalf("프로브에 벤더가 전달되지 않았다: %q", p.gotVendor)
	}
	if ev.Status.Level != v1alpha1.EvidenceLevelAllocationVerified {
		t.Fatalf("level = %q", ev.Status.Level)
	}
}

// TestVerifyRecordsProbeExitCodeInCheckMessage 는 벤더 기능 probe 의 종료코드·출력이 근거의
// AllocationProbe check Message 에 남는지 본다. 종료코드가 사라지면 "실 워크로드가 왜 실패했는가"
// 를 근거만 보고는 알 수 없다 — 파드는 이미 지워진 뒤다.
func TestVerifyRecordsProbeExitCodeInCheckMessage(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	avpObj := &v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "with-probe"},
		Spec: v1alpha1.AcceleratorVerificationPolicySpec{
			NodeSelector: map[string]string{"role": "gpu"},
			Checks:       []string{v1alpha1.EvidenceCheckAllocationProbe},
		},
	}
	exit := int32(3)
	p := &fakeProber{result: &ProbeResult{Allocated: false, ExitCode: &exit, Output: "no devices found"}}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr, avpObj).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	v := &Verifier{Client: c, Prober: p, Now: time.Now}
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	var probeCheck *v1alpha1.EvidenceCheck
	for i := range ev.Status.Checks {
		if ev.Status.Checks[i].Name == v1alpha1.EvidenceCheckAllocationProbe {
			probeCheck = &ev.Status.Checks[i]
		}
	}
	if probeCheck == nil {
		t.Fatalf("AllocationProbe check 가 없다: %+v", ev.Status.Checks)
	}
	if probeCheck.Passed {
		t.Error("종료코드 3 인 probe 를 통과로 기록했다")
	}
	if !strings.Contains(probeCheck.Message, "exit=3") {
		t.Errorf("Message 에 종료코드가 없다: %q", probeCheck.Message)
	}
	if !strings.Contains(probeCheck.Message, "no devices found") {
		t.Errorf("Message 에 probe 출력이 없다: %q", probeCheck.Message)
	}
}

func TestVerifyDoesNotProbeWithoutAProber(t *testing.T) {
	// 정책이 프로브를 요구해도 프로버가 없으면 "돌렸다" 고 말하지 않는다 — 통과도 시키지 않는다.
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	avpObj := &v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "with-probe"},
		Spec: v1alpha1.AcceleratorVerificationPolicySpec{
			Checks: []string{v1alpha1.EvidenceCheckAllocationProbe},
		},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr, avpObj).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	v := &Verifier{Client: c, Now: time.Now}
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status.Level != "" {
		t.Fatalf("프로버 없이 할당 검증 등급을 줬다: %q", ev.Status.Level)
	}
}

func TestVerifyDoesNotProbeWhenPolicyDoesNotAskEvenWithProber(t *testing.T) {
	// 프로버가 배선돼 있어도 정책이 요구하지 않으면 돌리지 않는다 — 세 조건(정책·프로버·자원명)이
	// 각각 독립적으로 게이트여야 한다. 이 레그가 죽으면 프로버를 미리 꽂아 둔 모든 노드가 정책
	// 설정과 무관하게 매 검증마다 테스트 Pod 를 띄우게 된다.
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	p := &fakeProber{allocated: true}
	v := &Verifier{Client: c, Prober: p, Now: time.Now} // 정책 미배치 → DefaultPolicy(), probe off
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if p.called != 0 {
		t.Fatalf("정책이 요구하지 않았는데 프로브가 %d회 호출됐다", p.called)
	}
	if ev.Status.Level != v1alpha1.EvidenceLevelFunctionallyVerified {
		t.Fatalf("level = %q", ev.Status.Level)
	}
}

func TestVerifyDoesNotProbeWhenProbeResourceEmpty(t *testing.T) {
	// 정책이 프로브를 요구하고 프로버도 있지만, 이 요청이 확인할 자원명을 안 준 경우 —
	// 프로브를 시도하지 않고, 시도 못 한 것을 실패로 기록한다(관측 부재를 통과로 승격하지 않는다).
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	avpObj := &v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "with-probe"},
		Spec: v1alpha1.AcceleratorVerificationPolicySpec{
			Checks: []string{v1alpha1.EvidenceCheckAllocationProbe},
		},
	}
	p := &fakeProber{allocated: true}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr, avpObj).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	v := &Verifier{Client: c, Prober: p, Now: time.Now}
	req := verifyRequest(geom)
	req.Expectation.ProbeResource = ""
	ev, err := v.Verify(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if p.called != 0 {
		t.Fatalf("자원명이 없는데 프로브가 %d회 호출됐다", p.called)
	}
	if ev.Status.Level != "" {
		t.Fatalf("프로브를 시도 못 했는데 등급이 붙었다: %q", ev.Status.Level)
	}
}

func TestAllocatableOfClampsLargeQuantitiesInsteadOfTruncating(t *testing.T) {
	// node.status.allocatable 의 memory 는 바이트 단위다 — 270GB 는 int32 범위를 넘어(overflow)
	// int32(q.Value()) 로 그냥 캐스팅하면 음수로 뒤집힌다. drift 감시(다음 태스크)가 이 맵을
	// 현재값과 비교하므로, 뒤집힌 음수가 들어가면 원인을 찾기 어려운 오탐이 된다.
	node := &corev1.Node{
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceMemory: *resourceQuantity(270 * 1024 * 1024 * 1024),
			},
		},
	}
	got := allocatableOf(node)
	if got[string(corev1.ResourceMemory)] < 0 {
		t.Fatalf("큰 수량이 음수로 절단됐다: %d", got[string(corev1.ResourceMemory)])
	}
}

func TestLoadReturnsNilWhenAbsent(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	v := &Verifier{Client: c, Now: time.Now}
	ev, err := v.Load(context.Background(), "nope")
	if err != nil || ev != nil {
		t.Fatalf("ev=%v err=%v", ev, err)
	}
}

// TestVerifyThenFreshnessSeam 은 검증→지문→신선도 판정을 잇는 이음매다 — 각 단계가 따로 통과해도 이어
// 붙였을 때 재부팅 후 근거가 계속 신선하다고 나오면 아무 소용이 없다.
func TestVerifyThenFreshnessSeam(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	now := time.Now()
	v := &Verifier{Client: c, Now: func() time.Time { return now }}
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultPolicy()

	same := Compute(node, ndr, "nvidia", 3)
	if verdict, _ := CheckFreshness(ev, now, same, p.InvalidateOn); verdict != FreshValid {
		t.Fatalf("방금 만든 근거가 신선하지 않다: %q", verdict)
	}

	rebooted := node.DeepCopy()
	rebooted.Status.NodeInfo.BootID = "boot-2"
	after := Compute(rebooted, ndr, "nvidia", 3)
	if verdict, reason := CheckFreshness(ev, now, after, p.InvalidateOn); verdict != FreshEnvironmentChanged {
		t.Fatalf("재부팅 후에도 근거가 살아 있다: %q (%s)", verdict, reason)
	}
}

// TestReportGeometryKeepsDevicesWithoutPCI 는 PCI 를 채우지 않는 벤더(Furiosa detector)의 보고서도
// "장치가 있다" 로 읽히는지 고정한다. PCI 없는 항목을 버리면 evalNodeDeviceReport 가 멀쩡한
// 보고서를 "보고서 없음" 으로 판정해 근거가 영영 등급을 못 받는다.
func TestReportGeometryKeepsDevicesWithoutPCI(t *testing.T) {
	ndr := &v1alpha1.NodeDeviceReport{Status: v1alpha1.NodeDeviceReportStatus{
		Devices: []v1alpha1.DeviceEntry{{Vendor: "furiosa", Model: "rngd", Count: 1}},
	}}
	got := reportGeometry(ndr, "furiosa", nil)
	if len(got) != 1 {
		t.Fatalf("reportGeometry = %v, want one entry for the PCI-less device", got)
	}
}

// TestReportGeometryScopesToTargets 는 정책이 건드리지 않은 장치를 보고서 대조에서 빼는지 본다.
// 빼지 않으면 같은 노드의 비대상 GPU(A2)가 정책의 기대 geometry 로 판정돼 절대 통과할 수 없다
// (실제로 "report geometry \"\" != expected \"1g.6gb x4\"" 가 A2 에서도 났다).
func TestReportGeometryScopesToTargets(t *testing.T) {
	ndr := &v1alpha1.NodeDeviceReport{Status: v1alpha1.NodeDeviceReportStatus{
		Devices: []v1alpha1.DeviceEntry{
			{Vendor: "nvidia", PCIeAddress: "0000:18:00.0", MigCurrentGeometry: "1g.6gb x4"},
			{Vendor: "nvidia", PCIeAddress: "0000:86:00.0"},
		},
	}}
	got := reportGeometry(ndr, "nvidia", []string{"0000:18:00.0"})
	if len(got) != 1 || got["0000:18:00.0"] != "1g.6gb x4" {
		t.Fatalf("reportGeometry = %v, want only the target device", got)
	}
}

// probeVerifierSetup 은 probe 결과 재사용 테스트가 공유하는 클러스터·정책 구성이다.
func probeVerifierSetup(t *testing.T, p AllocationProber) (*Verifier, *time.Time) {
	t.Helper()
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	avpObj := &v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "with-probe"},
		Spec: v1alpha1.AcceleratorVerificationPolicySpec{
			Checks: []string{v1alpha1.EvidenceCheckAllocationProbe},
		},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr, avpObj).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	current := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	v := &Verifier{Client: c, Prober: p, Now: func() time.Time { return current }}
	return v, &current
}

func findCheck(ev *v1alpha1.AcceleratorEvidence, name, target string) *v1alpha1.EvidenceCheck {
	for i := range ev.Status.Checks {
		if ev.Status.Checks[i].Name == name && ev.Status.Checks[i].Target == target {
			return &ev.Status.Checks[i]
		}
	}
	return nil
}

// TestVerifyReusesPassedProbeWithinTTLWindow 는 probe 결과 재사용 규칙을 고정한다: 같은 노드·벤더·
// 자원에 대해 직전 AllocationProbe 가 evidence TTL 안에서 이미 pass 했으면 probe Pod 를 다시
// 띄우지 않고 그 결과를 재사용한다. 라이브에서 한 번의 수렴에 11·17개 probe Pod 가 스케줄된
// 기록이 있다(test/live/acpp-probe/README.md) — 이 재사용이 그 수를 줄인다.
func TestVerifyReusesPassedProbeWithinTTLWindow(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	p := &fakeProber{allocated: true}
	v, current := probeVerifierSetup(t, p)

	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	if p.called != 1 {
		t.Fatalf("첫 호출 프로브 횟수 = %d, want 1", p.called)
	}

	*current = current.Add(5 * time.Minute) // DefaultPolicy TTL(30분) 안
	ev, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if p.called != 1 {
		t.Fatalf("재사용창 안인데 프로브가 다시 불렸다: 호출 횟수 = %d", p.called)
	}
	probeCheck := findCheck(ev, v1alpha1.EvidenceCheckAllocationProbe, "nvidia.com/mig-1g.6gb")
	if probeCheck == nil || !probeCheck.Passed {
		t.Fatalf("재사용 check 가 없거나 실패로 기록됐다: %+v", ev.Status.Checks)
	}
	if !strings.Contains(probeCheck.Message, "reused") {
		t.Errorf("재사용 근거가 메시지에 없다: %q", probeCheck.Message)
	}
}

// TestVerifyReprobesWhenEvidenceExpired 는 재사용창(=직전 probe 시각+TTL)을 한 번에 넘기면
// 다시 probe 하는지 본다 — 창을 TTL 이상으로 늘리면 실 장치 실패를 그만큼 늦게 잡는다.
// (창이 재조정마다 밀리지 않는지는 TestVerifyReuseWindowDoesNotSlide 가 따로 본다.)
func TestVerifyReprobesWhenEvidenceExpired(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	p := &fakeProber{allocated: true}
	v, current := probeVerifierSetup(t, p)

	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}

	*current = current.Add(DefaultPolicy().TTL + time.Minute) // 창을 넘김
	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	if p.called != 2 {
		t.Fatalf("만료된 evidence 인데 재사용했다: 프로브 호출 횟수 = %d, want 2", p.called)
	}
}

// TestVerifyReprobesWhenPreviousProbeFailed 는 직전 probe 가 실패였으면 창 안이어도 다시
// 확인하는지 본다 — 실패를 재사용하면 이미 안 되는 장치를 계속 통과로 보고할 수 있다.
func TestVerifyReprobesWhenPreviousProbeFailed(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	p := &fakeProber{allocated: false}
	v, current := probeVerifierSetup(t, p)

	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}

	*current = current.Add(5 * time.Minute) // 창 안
	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	if p.called != 2 {
		t.Fatalf("실패했던 probe 를 재사용했다: 프로브 호출 횟수 = %d, want 2", p.called)
	}
}

// TestVerifyReprobesWhenProbeResourceDiffers 는 요청 자원명이 바뀌면 창 안이어도 다시 확인하는지
// 본다 — 저장된 check 의 Target 은 이전 자원을 가리키므로 다른 자원을 그 결과로 대신할 수 없다.
func TestVerifyReprobesWhenProbeResourceDiffers(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	p := &fakeProber{allocated: true}
	v, current := probeVerifierSetup(t, p)

	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}

	*current = current.Add(5 * time.Minute) // 창 안
	other := verifyRequest(geom)
	other.Expectation.ProbeResource = "nvidia.com/mig-2g.12gb"
	if _, err := v.Verify(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if p.called != 2 {
		t.Fatalf("다른 자원인데 이전 probe 를 재사용했다: 프로브 호출 횟수 = %d, want 2", p.called)
	}
}

// TestVerifyReuseWindowDoesNotSlide 는 재사용이 창을 밀지 못하는지 본다: 실제 probe 시각 기준
// TTL 안의 재사용은 probe 를 다시 안 띄우되(20분 시점) 창 자체는 실제 probe 시각에 고정돼,
// 실제 probe 시각+40분(TTL 30분 밖)에는 다시 probe 한다. 재사용창을 evidence ExpiresAt 으로
// 잡으면 매 재조정이 ExpiresAt 을 갱신해 창이 무한히 밀려 실 probe 가 영영 안 돈다.
func TestVerifyReuseWindowDoesNotSlide(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	p := &fakeProber{allocated: true}
	v, current := probeVerifierSetup(t, p)

	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	if p.called != 1 {
		t.Fatalf("첫 probe 호출 = %d, want 1", p.called)
	}

	*current = current.Add(20 * time.Minute) // 실제 probe 시각+20분 — 창(30분) 안이라 재사용
	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	if p.called != 1 {
		t.Fatalf("20분 시점은 창 안인데 probe 가 다시 불렸다: 호출 횟수 = %d", p.called)
	}

	*current = current.Add(20 * time.Minute) // 실제 probe 시각+40분 — 창 밖이라 재probe
	if _, err := v.Verify(context.Background(), verifyRequest(geom)); err != nil {
		t.Fatal(err)
	}
	if p.called != 2 {
		t.Fatalf("실제 probe 시각+40분(창 밖)인데 창이 밀려 재probe 안 됨: 호출 횟수 = %d, want 2", p.called)
	}
}

// TestVerifyReuseKeepsProbedAtAndDoesNotGrowMessage 는 재사용이 (a) 직전 probe 시각(ProbedAt)을
// 바꾸지 않고 이어싣는지, (b) 재사용 표시를 한 번만 붙여 message 가 pass 마다 자라지 않는지 본다.
// ProbedAt 이 재사용마다 갱신되면 창이 밀리고, message 가 중첩되면 etcd 객체가 무한 성장한다.
func TestVerifyReuseKeepsProbedAtAndDoesNotGrowMessage(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	p := &fakeProber{allocated: true}
	v, current := probeVerifierSetup(t, p)

	ev0, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	c0 := findCheck(ev0, v1alpha1.EvidenceCheckAllocationProbe, "nvidia.com/mig-1g.6gb")
	if c0 == nil || c0.ProbedAt == nil {
		t.Fatalf("실제 probe check 에 ProbedAt 이 없다: %+v", ev0.Status.Checks)
	}
	firstProbedAt := c0.ProbedAt.Time

	reuseLen := -1
	for i := 0; i < 5; i++ {
		*current = current.Add(2 * time.Minute) // 매번 창(30분) 안
		ev, verr := v.Verify(context.Background(), verifyRequest(geom))
		if verr != nil {
			t.Fatal(verr)
		}
		if p.called != 1 {
			t.Fatalf("재사용 %d회차인데 probe 가 다시 불렸다: 호출 횟수 = %d", i+1, p.called)
		}
		c := findCheck(ev, v1alpha1.EvidenceCheckAllocationProbe, "nvidia.com/mig-1g.6gb")
		if c == nil || c.ProbedAt == nil || !c.ProbedAt.Time.Equal(firstProbedAt) {
			t.Fatalf("재사용이 ProbedAt 을 바꿨다: %v, want %v", c.ProbedAt, firstProbedAt)
		}
		if reuseLen < 0 {
			reuseLen = len(c.Message)
		} else if len(c.Message) != reuseLen {
			t.Fatalf("재사용 message 가 자란다: %d회차 len=%d(1회차 len=%d) msg=%q", i+1, len(c.Message), reuseLen, c.Message)
		}
	}
}

func TestVerifySkipsStatusWriteWhenUnchanged(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	v := &Verifier{Client: c, Now: func() time.Time { return now }}
	first, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	// 같은 세계, 1분 뒤 — 내용이 같으므로 쓰지 않는다(resourceVersion 불변, ExpiresAt 불변).
	now = now.Add(time.Minute)
	second, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if second.ResourceVersion != first.ResourceVersion {
		t.Fatalf("내용이 같은데 status 를 다시 썼다: rv %s → %s", first.ResourceVersion, second.ResourceVersion)
	}
	if !second.Status.ExpiresAt.Time.Equal(first.Status.ExpiresAt.Time) {
		t.Fatalf("no-op pass 가 ExpiresAt 을 바꿨다: %v → %v", first.Status.ExpiresAt, second.Status.ExpiresAt)
	}
	// 만료까지 TTL/2 미만으로 남으면 시각을 갱신해 쓴다 — CheckFreshness/usableBaseline 이 만료로 읽지 않게.
	now = first.Status.ExpiresAt.Add(-DefaultPolicy().TTL/2 + time.Second)
	third, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if third.ResourceVersion == first.ResourceVersion {
		t.Fatalf("만료가 가까운데 ExpiresAt 을 갱신하지 않았다")
	}
	if !third.Status.ExpiresAt.Time.Equal(now.Add(DefaultPolicy().TTL)) {
		t.Fatalf("갱신된 ExpiresAt 이 now+TTL 이 아니다: %v", third.Status.ExpiresAt)
	}
}

func TestVerifyWritesWhenChecksChange(t *testing.T) {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	node, ndr := seedNodeAndReport(t, geom, map[string]int64{"nvidia.com/mig-1g.6gb": 4})
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	v := &Verifier{Client: c, Now: func() time.Time { return now }}
	first, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	// 광고가 줄면 판정이 바뀐다 — 시각 외 내용이 달라졌으므로 써야 한다.
	var cur corev1.Node
	if err := c.Get(context.Background(), types.NamespacedName{Name: "worker1"}, &cur); err != nil {
		t.Fatal(err)
	}
	cur.Status.Allocatable["nvidia.com/mig-1g.6gb"] = *resourceQuantity(1)
	if err := c.Status().Update(context.Background(), &cur); err != nil {
		t.Fatal(err)
	}
	second, err := v.Verify(context.Background(), verifyRequest(geom))
	if err != nil {
		t.Fatal(err)
	}
	if second.ResourceVersion == first.ResourceVersion || second.Status.Level != "" {
		t.Fatalf("판정이 바뀌었는데 쓰지 않았다: rv %s→%s level=%q", first.ResourceVersion, second.ResourceVersion, second.Status.Level)
	}
}

func TestVerifyMatchesReportByProductForDescriptorRequests(t *testing.T) {
	// rngd descriptor 는 vendor="rngd" 로 부르지만 NDR 은 vendor=furiosa/model=rngd 로 보고한다 —
	// 벤더 키 대조는 빈 보고서가 되고, 제품 키 대조는 장치를 찾는다. 벤더 플러그인이 꺼져
	// validation 이 실패해도 descriptor 요청은 그것을 적용하지 않는다.
	node, ndr := seedNodeAndReport(t, "", map[string]int64{"furiosa.ai/rngd": 1})
	ndr.Status.Devices = []v1alpha1.DeviceEntry{{Vendor: "furiosa", Model: "rngd", PCIeAddress: testPCI, DriverVersion: "2026.1.0"}}
	ndr.Status.Validation = &v1alpha1.ValidationStatus{Passed: false, Vendor: "furiosa"}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(node, ndr).
		WithStatusSubresource(&v1alpha1.AcceleratorEvidence{}).Build()
	v := &Verifier{Client: c, Now: time.Now}
	ev, err := v.Verify(context.Background(), Request{
		NodeName: "worker1", Vendor: "rngd", SourcePolicy: "desc-rngd", Generation: 1,
		Expectation:       Expectation{ProbeResource: "furiosa.ai/rngd"},
		ObservedGeometry:  map[string]string{"PCI-" + testPCI: ""},
		ObservationErrors: map[string]string{},
		Product:           "rngd",
		ExpectedDevices:   1,
		DescriptorManaged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status.Level != v1alpha1.EvidenceLevelObserved {
		t.Fatalf("level = %q reason=%q checks=%+v", ev.Status.Level, ev.Status.Reason, ev.Status.Checks)
	}
}
