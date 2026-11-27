// ============================================================
// state_machine_failed_recovered_test.go: 노드가 이미 정책을 만족하는데 Failed 로 남은 상태의
// 탈출 경로 단위 테스트
// 상세: Failed 가 이미 정책을 만족한 노드를 Idle 로 내보내는 탈출 조건과 그 경계(버전 불일치·
//       NDR 로드 버전·driverModule 검증·NDR 없음·cordon 소유권)를 고정한다.
// 생성일: 2026-08-07 | 수정일: 2026-10-13
// ============================================================

package upgrade

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// makeFailedDUSWithVersions 는 버전 축을 채운 Failed DUS 를 반환한다.
func makeFailedDUSWithVersions(current, desired string) *v1alpha1.DriverUpgradeState {
	dus := makeFailedDUS(jVendor)
	dus.Status.CurrentVersion = current
	dus.Status.DesiredVersion = desired
	dus.Status.RollbackAttempts = 4
	return dus
}

// makeNDRWithDriver 는 그 벤더 장치 하나를 담은 NDR 을 반환한다.
func makeNDRWithDriver(loaded bool, version string) *v1alpha1.NodeDeviceReport {
	return &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status: v1alpha1.NodeDeviceReportStatus{
			Devices: []v1alpha1.DeviceEntry{{
				Vendor:        jVendor,
				Model:         jModel,
				DriverVersion: version,
				DriverLoaded:  loaded,
			}},
		},
	}
}

// makeUncordonedNode 는 cordon 되지 않은 노드를 반환한다. 탈출 판정이 cordon 과 무관함을
// 드러내기 위해 이 파일의 기본 노드로 쓴다.
func makeUncordonedNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{"nvidia.com/gpu": qty(2)},
		},
	}
}

// ── 시험 1: 이미 정책을 만족 → Idle 로 탈출 ─────────────────────────────
// 목표 버전이 이미 깔려 있고 드라이버도 로드돼 있으면 이 상태기계가 할 일은 없다.
// 할 일이 없으면 Idle 이다.
func TestHandleFailed_AlreadySatisfiesPolicy_ReturnsToIdle(t *testing.T) {
	dus := makeFailedDUSWithVersions(verNew, verNew)
	dip := makeCordonTestDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(true, verNew))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state=%q, want Idle — 목표 버전이 이미 깔려 있고 드라이버도 로드된 상태에서 "+
			"Failed 로 남으면 사람이 손을 댈 것이 없는데도 손을 기다리게 된다", dus.Status.State)
	}
	if dus.Status.RollbackAttempts != 0 {
		t.Errorf("rollbackAttempts=%d, want 0 — 다음 사이클이 지난 실패의 카운터를 물려받으면 "+
			"첫 시도부터 한도에 가까워진다", dus.Status.RollbackAttempts)
	}
}

// ── 시험 2: 탈출하지 않아야 하는 조합 ───────────────────────────────────
// 안전 쪽 절반이다. 여기서 탈출시키면 Idle 이 설치를 다시 걸고, 같은 이유로 실패해 Failed 로
// 돌아온다. 그 왕복은 지금의 고착보다 나쁘다 — 노드를 계속 건드리기 때문이다.
func TestHandleFailed_NotSatisfied_StaysFailed(t *testing.T) {
	cases := []struct {
		name    string
		current string
		desired string
		loaded  bool
		why     string
	}{
		{
			name: "버전 불일치(다운그레이드 요구)", current: verNew, desired: verOld, loaded: true,
			why: "정책이 더 낮은 버전을 요구한다 — 설치를 다시 걸어도 같은 이유로 실패한다",
		},
		{
			name: "버전 불일치(업그레이드 미완)", current: verOld, desired: verNew, loaded: true,
			why: "아직 목표에 도달하지 않았다",
		},
		{
			name: "버전은 같으나 드라이버 미로드", current: verNew, desired: verNew, loaded: false,
			why: "버전 문자열만 같고 모듈이 빠져 있다 — 정책을 만족한 것이 아니다",
		},
		{
			name: "목표 버전 미지정", current: verNew, desired: "", loaded: true,
			why: "비교할 목표가 없으면 만족 여부를 판정할 수 없다",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dus := makeFailedDUSWithVersions(tc.current, tc.desired)
			dip := makeCordonTestDIP()
			sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(),
				makeNDRWithDriver(tc.loaded, tc.current))

			if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
				t.Fatalf("TransitionState 실패: %v", err)
			}
			if dus.Status.State != v1alpha1.UpgradeStateFailed {
				t.Errorf("state=%q, want Failed — %s", dus.Status.State, tc.why)
			}
		})
	}
}

// ── 시험 3: NDR 이 없으면 탈출하지 않는다 ────────────────────────────────
// 드라이버 로드 여부를 확인할 근거가 없다. 근거가 없는 것을 만족으로 읽으면 안 된다.
func TestHandleFailed_NoDeviceReport_StaysFailed(t *testing.T) {
	dus := makeFailedDUSWithVersions(verNew, verNew)
	dip := makeCordonTestDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode()) // NDR 없음

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Errorf("state=%q, want Failed — NDR 이 없으면 드라이버 로드 여부를 확인할 수 없다",
			dus.Status.State)
	}
}

// ── 시험 4: Failed→Idle 탈출이 남은 cordon 을 푼다 ─────────────────────────
// 해제 로직이 Uncordoning 경로에만 있어서, 복구 탈출로 나가면 드라이버가 정상인데 노드가
// 계속 cordon 된 채로 남을 수 있다 — 탈출 경로에서도 같은 해제를 걸어야 한다.
func TestFailedStateRecovered_ReleasesCordon(t *testing.T) {
	dus := makeFailedDUSWithVersions(verNew, verNew)
	node := makeCordonedNode(dus.Name, nil)
	dus.Spec.NodeName = node.Name // makeCordonedNode 가 쓰는 노드 이름에 맞춘다

	dip := makeCordonTestDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, node, makeNDRWithDriver(true, verNew))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, want Idle", dus.Status.State)
	}

	var got corev1.Node
	if err := sm.Get(context.Background(),
		client.ObjectKey{Name: node.Name}, &got); err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	if got.Spec.Unschedulable {
		t.Error("Idle 로 탈출했는데 노드가 여전히 cordon 상태다 — 드라이버는 정상인데 스케줄이 막힌다")
	}
}

// ── 시험 5: Failed→Idle 탈출이 남의 cordon 을 건드리지 않는다 ──────────────
// ACPP quiesce 와 운영자 수동 cordon 은 소유 표식을 남기지 않는다. 그 cordon 을 우리가 풀면
// 파티션 적용 중이거나 정비 중인 노드에 파드가 다시 들어간다.
func TestFailedStateRecovered_LeavesForeignCordon(t *testing.T) {
	dus := makeFailedDUSWithVersions(verNew, verNew)
	// 빈 문자열을 owner로 주면 표식 없는(= 남의) cordon 이 된다
	node := makeCordonedNode("", nil)
	dus.Spec.NodeName = node.Name

	dip := makeCordonTestDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, node, makeNDRWithDriver(true, verNew))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, want Idle", dus.Status.State)
	}

	var got corev1.Node
	if err := sm.Get(context.Background(),
		client.ObjectKey{Name: node.Name}, &got); err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	if !got.Spec.Unschedulable {
		t.Error("Idle 로 탈출했는데 남의 cordon 이 풀렸다 — 파티션 적용 중이거나 정비 중인 노드에 파드가 들어간다")
	}
}

// ── 시험 6: NDR 로드 버전·검증 결과를 읽는다 ─────────────────────────────
// DUS currentVersion 은 Failed 에서 갱신되지 않는다. current=desired 여도 실제 로드된 모듈이
// 다르거나 node-agent 검증이 실패했으면 복구가 아니다.

// DUS current=desired 여도 NDR 로드 버전이 다르면 복구가 아니다.
func TestHandleFailed_LoadedVersionDiffers_StaysFailed(t *testing.T) {
	dus := makeFailedDUSWithVersions(verOld, verOld)
	sm := newUpgradeSMWithRecorder(dus, makeCordonTestDIP(), makeNDRWithDriver(true, verNew), makeUncordonedNode())
	if _, _, err := sm.handleFailed(context.Background(), dus); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Fatalf("state=%q, Failed 유지 기대", dus.Status.State)
	}
}

// 로드 버전은 맞아도 node-agent 검증의 driverModule 단계가 kernel/userspace 불일치로 실패한 노드는 복구가 아니다.
func TestHandleFailed_DriverModuleValidationFailed_StaysFailed(t *testing.T) {
	dus := makeFailedDUSWithVersions(verOld, verOld)
	ndr := makeNDRWithDriver(true, verOld)
	ndr.Status.Validation = &v1alpha1.ValidationStatus{Passed: false, Steps: []v1alpha1.ValidationStep{{
		Name: "nvidia/driverModule", Passed: false,
		Message: "kernel/userspace mismatch: kernel=580.126.09 userspace=580.178.04",
	}}}
	sm := newUpgradeSMWithRecorder(dus, makeCordonTestDIP(), ndr, makeUncordonedNode())
	if _, _, err := sm.handleFailed(context.Background(), dus); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Fatalf("state=%q, Failed 유지 기대", dus.Status.State)
	}
}

// NDR 에 이 벤더 장치가 하나도 없으면 복구로 보지 않는다.
func TestHandleFailed_NoMatchingDevice_StaysFailed(t *testing.T) {
	dus := makeFailedDUSWithVersions(verOld, verOld)
	ndr := makeNDRWithDriver(true, verOld)
	ndr.Status.Devices[0].Vendor = "furiosa"
	sm := newUpgradeSMWithRecorder(dus, makeCordonTestDIP(), ndr, makeUncordonedNode())
	if _, _, err := sm.handleFailed(context.Background(), dus); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Fatalf("state=%q, Failed 유지 기대", dus.Status.State)
	}
}

// ── 시험: 실패 뒤 새 버전이 실제로 로드된 노드 ─────────────────────────────
// CurrentVersion 은 Failed 에서 갱신되지 않는다. 실패 기록 뒤 사람이 재부팅해 목표 버전이
// 로드됐는데 CurrentVersion 이 옛 값이라는 이유로 Failed 에 남기면 손댈 것이 없는 노드가
// 수동 조치를 기다린다.
func TestHandleFailed_TargetLoadedButCurrentStale_ReturnsToIdle(t *testing.T) {
	dus := makeFailedDUSWithVersions(verOld, verNew) // current 는 사이클 시작 때 값
	dip := makeCordonTestDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(true, verNew))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state=%q, want Idle — NDR 이 목표 버전 로드를 보고하는데 Failed 에 남는다", dus.Status.State)
	}
}

// 복귀 뒤 첫 Idle 판정이 옛 currentVersion 으로 버전 불일치를 보고 사이클을 다시 열지 않는다.
// 쿨다운을 0 으로 두어 쿨다운 대기 덕에 우연히 Idle 에 남는 경우를 막는다.
func TestHandleFailed_RecoveredThenIdleStays(t *testing.T) {
	dus := makeFailedDUSWithVersions(verOld, verNew)
	dip := makeCordonTestDIP()
	dip.Spec.UpgradePolicy.IdleCooldownSeconds = ptr.To(int32(0))
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(true, verNew))
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("1회차 TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, want Idle(복귀)", dus.Status.State)
	}
	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("2회차 TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state=%q, want Idle — 복귀 뒤 옛 currentVersion 으로 사이클을 다시 연다", dus.Status.State)
	}
	if dus.Status.CurrentVersion != verNew {
		t.Errorf("currentVersion=%q, want %q", dus.Status.CurrentVersion, verNew)
	}
}

// driverModuleStepFailed 는 DUS 의 (vendor, model) 로 구한 검증 키의 driverModule 단계 실패만
// 실패로 읽는다. rngd 는 detector 가 "rngd/driverModule" 로 내므로 furiosa 접두로 찾으면 놓친다.
func TestDriverModuleStepFailed(t *testing.T) {
	step := func(name string, passed, na bool) *v1alpha1.ValidationStatus {
		return &v1alpha1.ValidationStatus{Steps: []v1alpha1.ValidationStep{{Name: name, Passed: passed, NotApplicable: na}}}
	}
	cases := []struct {
		name          string
		vendor, model string
		v             *v1alpha1.ValidationStatus
		want          bool
	}{
		{"검증 없음", "nvidia", "a30", nil, false},
		{"단일 벤더 이름 실패", "nvidia", "a30", step("driverModule", false, false), true},
		{"벤더 접두 이름 실패(대소문자 무시)", "nvidia", "a30", step("NVIDIA/driverModule", false, false), true},
		{"통과", "nvidia", "a30", step("nvidia/driverModule", true, false), false},
		{"해당없음", "nvidia", "a30", step("nvidia/driverModule", false, true), false},
		{"다른 벤더 접두 실패", "nvidia", "a30", step("furiosa/driverModule", false, false), false},
		{"다른 단계 실패", "nvidia", "a30", step("nvidia/devicePlugin", false, false), false},
		{"rngd 다중 벤더 노드의 rngd 실패", "furiosa", "rngd", step("rngd/driverModule", false, false), true},
		{"rngd 모델 대소문자 무시", "furiosa", "RNGD", step("rngd/driverModule", false, false), true},
		{"rngd DUS 는 warboy 키 실패를 보지 않음", "furiosa", "rngd", step("furiosa/driverModule", false, false), false},
		{"warboy 실패", "furiosa", "warboy", step("furiosa/driverModule", false, false), true},
		{"warboy DUS 는 rngd 실패를 보지 않음", "furiosa", "warboy", step("rngd/driverModule", false, false), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := validationVendorKey(tc.vendor, tc.model)
			if got := driverModuleStepFailed(tc.v, key); got != tc.want {
				t.Errorf("driverModuleStepFailed(key=%q)=%v, want %v", key, got, tc.want)
			}
		})
	}
}
