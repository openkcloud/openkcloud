// ============================================================
// state_machine_reboot_test.go: 모듈 교체(계열 전환 포함) reboot 전이 단위 테스트
// 상세: 재부팅 트리거/완료 판정(bootID 가드), 재부팅 예산, needs-reboot 마커의 신선도 판정을
// 검증한다.
// 생성일: 2026-07-20 | 수정일: 2026-10-14
// ============================================================
package upgrade

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/naming"
)

func rebootNDR(needsReboot bool) *v1alpha1.NodeDeviceReport {
	return &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: jNode},
		Status: v1alpha1.NodeDeviceReportStatus{
			Devices: []v1alpha1.DeviceEntry{
				{Vendor: jVendor, Model: jModel, DriverLoaded: true, DriverVersion: verNew, NeedsReboot: needsReboot, Count: 1},
			},
		},
	}
}

func readyNode(ready bool) *corev1.Node {
	cond := corev1.ConditionTrue
	if !ready {
		cond = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: cond}}},
	}
}

// bootIDPre 는 재부팅 직전 캡처된 bootID 를 모사하는 테스트 상수.
const bootIDPre = "boot-A"

// readyNodeBootID 는 Ready 상태 + 지정한 bootID 를 가진 노드를 만든다(premature-Ready 가드 테스트용).
func readyNodeBootID(bootID string) *corev1.Node {
	n := readyNode(true)
	n.Status.NodeInfo.BootID = bootID
	return n
}

// reboot Job 생성 직전에 node bootID 를 DUS status 에 persist 한다(persist-then-Job).
func TestHandleRebootRequired_PersistsBootID(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebootRequired, verNew, verNew)
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true), readyNodeBootID(bootIDPre))

	if _, _, err := sm.handleRebootRequired(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebootRequired 실패: %v", err)
	}
	if dus.Status.RebootBootID != bootIDPre {
		t.Errorf("RebootBootID=%q, want boot-A (재부팅 전 캡처)", dus.Status.RebootBootID)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebooting {
		t.Errorf("state=%q, want Rebooting", dus.Status.State)
	}
	var job batchv1.Job
	if err := sm.Get(context.Background(), types.NamespacedName{Name: naming.RebootJobName(jNode), Namespace: driverjob.Namespace}, &job); err != nil {
		t.Errorf("reboot Job 미생성: %v", err)
	}
}

// premature-Ready 가드: Ready 이나 bootID 가 캡처값과 동일 → 전진 보류(Rebooting 유지).
func TestHandleRebooting_BootIDUnchanged_Waits(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebooting, verNew, verNew)
	dus.Status.RebootRequestedTime = metav1.Now()
	dus.Status.RebootBootID = bootIDPre
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(false), readyNodeBootID(bootIDPre))

	requeue, _, err := sm.handleRebooting(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("handleRebooting 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebooting {
		t.Errorf("state=%q, want Rebooting (bootID 미변경 → premature-Ready 보류)", dus.Status.State)
	}
	if !requeue {
		t.Error("bootID 미변경인데 requeue=false")
	}
}

// premature-Ready 가드: Ready + bootID 변경 관측 → Upgrading 전진.
func TestHandleRebooting_BootIDChanged_ToUpgrading(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebooting, verNew, verNew)
	dus.Status.RebootRequestedTime = metav1.Now()
	dus.Status.RebootBootID = bootIDPre
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(false), readyNodeBootID("boot-B"))

	if _, _, err := sm.handleRebooting(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebooting 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Errorf("state=%q, want Upgrading (bootID 변경 관측 후 전진)", dus.Status.State)
	}
}

// bootID 빈 값(캡처 실패)이면 기존 isNodeReady 로 보수 전진한다(교착 방지).
func TestHandleRebooting_EmptyBootID_Fallback(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebooting, verNew, verNew)
	dus.Status.RebootRequestedTime = metav1.Now()
	dus.Status.RebootBootID = "" // 캡처 실패 fallback
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(false), readyNodeBootID("boot-B"))

	if _, _, err := sm.handleRebooting(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebooting 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Errorf("state=%q, want Upgrading (빈 bootID fallback 은 isNodeReady 로 전진)", dus.Status.State)
	}
}

// needsReboot=true → handleRebootRequired 가 reboot Job 생성 + Rebooting 전이 + attempts=1.
func TestHandleRebootRequired_TriggersRebootJob(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebootRequired, verNew, verNew)
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true))

	if _, _, err := sm.handleRebootRequired(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebootRequired 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebooting {
		t.Errorf("state=%q, want Rebooting", dus.Status.State)
	}
	if dus.Status.RebootAttempts != 1 {
		t.Errorf("RebootAttempts=%d, want 1", dus.Status.RebootAttempts)
	}
	var job batchv1.Job
	if err := sm.Get(context.Background(), types.NamespacedName{Name: naming.RebootJobName(jNode), Namespace: driverjob.Namespace}, &job); err != nil {
		t.Errorf("reboot Job 미생성: %v", err)
	}
}

// RebootAttempts 가 MaxReboots(기본1) 도달 후 재요구 → Failed(무한 재부팅 방지).
func TestHandleRebootRequired_ExceedsMaxReboots_Failed(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebootRequired, verNew, verNew)
	dus.Status.RebootAttempts = 1 // 이미 1회(=maxReboots 기본)
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true))

	if _, _, err := sm.handleRebootRequired(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebootRequired 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Errorf("state=%q, want Failed (MaxReboots 초과)", dus.Status.State)
	}
}

// kured executor 는 1차 미구현 → reboot Job 미생성, 상태 불변(대기).
func TestHandleRebootRequired_KuredUnsupported(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebootRequired, verNew, verNew)
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Spec.RebootExecutor = "kured"
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true))

	if _, _, err := sm.handleRebootRequired(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebootRequired 실패: %v", err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateRebooting {
		t.Error("kured 미구현인데 Rebooting 전이됨")
	}
	if dus.Status.RebootAttempts != 0 {
		t.Errorf("RebootAttempts=%d, want 0 (kured 미실행)", dus.Status.RebootAttempts)
	}
}

// 노드 Ready → Rebooting → Upgrading(재설치로 마커 소거·재수렴).
func TestHandleRebooting_Ready_ToUpgrading(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebooting, verNew, verNew)
	dus.Status.RebootRequestedTime = metav1.Now()
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(false), readyNode(true))

	if _, _, err := sm.handleRebooting(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebooting 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Errorf("state=%q, want Upgrading (재부팅 후 재설치로 마커 소거)", dus.Status.State)
	}
}

// 노드 NotReady(재부팅 중) → Rebooting 유지(대기 requeue).
func TestHandleRebooting_NotReady_Waits(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebooting, verNew, verNew)
	dus.Status.RebootRequestedTime = metav1.Now()
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true), readyNode(false))

	requeue, _, err := sm.handleRebooting(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("handleRebooting 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebooting {
		t.Errorf("state=%q, want Rebooting (NotReady 대기)", dus.Status.State)
	}
	if !requeue {
		t.Error("NotReady 인데 requeue=false")
	}
}

// rebootTimeout 초과 → Failed.
func TestHandleRebooting_Timeout_Failed(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRebooting, verNew, verNew)
	dus.Status.RebootRequestedTime = metav1.NewTime(time.Now().Add(-20 * time.Minute)) // 15m 초과
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true), readyNode(true))

	if _, _, err := sm.handleRebooting(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRebooting 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Errorf("state=%q, want Failed (rebootTimeout)", dus.Status.State)
	}
}

// 레이스 가드: 재부팅 후(RebootAttempts>=1) Validating 에서 needsReboot 가 stale-true 여도
// RebootRequired 로 재진입하지 않는다(NDR 지연 오판→MaxReboots Failed 방지). validator 체인이 결정.
func TestTransitionState_PostReboot_NoRetriggerOnStaleNDR(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateValidating, verNew, verNew)
	dus.Status.RebootAttempts = 1 // 이미 1회 재부팅함
	dip := makeJobDIP(verNew, imgNew, true, true)
	// NDR 은 stale-true(마커 소거가 아직 NDR 에 미반영된 상태 모사) + install Job 완료.
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true), readyNode(true), makeInstallJob(true, false))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateRebootRequired {
		t.Error("재부팅 후 stale needsReboot 로 RebootRequired 재진입됨 (레이스 가드 실패)")
	}
}

// 첫 검증(RebootAttempts==0)에서는 needsReboot=true 가 정상적으로 RebootRequired 를 트리거한다.
func TestTransitionState_FirstValidation_TriggersReboot(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateValidating, verNew, verNew)
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, rebootNDR(true), readyNode(true), makeInstallJob(true, false))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebootRequired {
		t.Errorf("state=%q, want RebootRequired (첫 검증 needsReboot 트리거)", dus.Status.State)
	}
}

// 재부팅 게이트는 NDR needsReboot 만 본다 — 같은 580 계열 patch 교체(580.126.09 → 580.178.04)도
// 구 모듈이 사용 중이면 모듈 교체에 재부팅이 필요하므로 RebootRequired 로 간다.
func TestTransitionState_SameMajor_Marker_TriggersReboot(t *testing.T) {
	const from, to = "580.126.09", "580.178.04"
	dus := makeJobDUS(v1alpha1.UpgradeStateValidating, from, to)
	dip := makeJobDIP(to, imgNew, true, true)
	ndr := rebootNDR(true)
	ndr.Status.Devices[0].DriverVersion = from // 구 모듈이 아직 로드돼 있다
	sm := newUpgradeSMWithRecorder(dus, dip, ndr, readyNode(true), makeInstallJob(true, false))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebootRequired {
		t.Errorf("state=%q, want RebootRequired (같은 계열 patch 교체의 needsReboot 트리거)", dus.Status.State)
	}
}

// ── needs-reboot 마커의 신선도 ────────────────────
// 재부팅 후 "사이클당 1회" 규율만으로는 needsReboot=true 를 전부 stale 로 간주해, 재부팅 뒤에
// 다시 돈 install Job 이 새로 남긴 진짜 마커까지 무시한다. 판별 기준은 시도 횟수가 아니라
// "관측이 이번 install Job 완료보다 나중인가" 다.

// rebootNDRAt 는 관측 시각(ObservedAt)이 붙은 NDR 이다.
func rebootNDRAt(needsReboot bool, observedAt time.Time) *v1alpha1.NodeDeviceReport {
	ndr := rebootNDR(needsReboot)
	ts := metav1.NewTime(observedAt)
	ndr.Status.ObservedAt = &ts
	return ndr
}

// completedInstallJobAt 는 완료 시각이 지정된 install Job 이다.
func completedInstallJobAt(completed time.Time) *batchv1.Job {
	j := makeInstallJob(true, false)
	ts := metav1.NewTime(completed)
	j.Status.CompletionTime = &ts
	return j
}

// 재부팅 후라도 install Job 완료 뒤에 관측된 마커면 재부팅으로 넘어간다.
func TestTransitionState_PostReboot_MarkerAfterInstall_TriggersReboot(t *testing.T) {
	done := time.Now().Add(-2 * time.Minute)
	dus := makeJobDUS(v1alpha1.UpgradeStateValidating, verNew, verNew)
	dus.Status.RebootAttempts = 1 // 앞 사이클/앞 단계에서 이미 1회 재부팅함
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip,
		rebootNDRAt(true, done.Add(time.Minute)), readyNode(true), completedInstallJobAt(done))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebootRequired {
		t.Errorf("state=%q, want RebootRequired (install 완료 후 새로 남은 마커)", dus.Status.State)
	}
}

// install Job 완료 이전 관측이면 stale 이다 — 재부팅으로 넘어가지 않는다.
func TestTransitionState_PostReboot_MarkerBeforeInstall_NoReboot(t *testing.T) {
	done := time.Now().Add(-2 * time.Minute)
	dus := makeJobDUS(v1alpha1.UpgradeStateValidating, verNew, verNew)
	dus.Status.RebootAttempts = 1
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip,
		rebootNDRAt(true, done.Add(-time.Minute)), readyNode(true), completedInstallJobAt(done))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateRebootRequired {
		t.Error("install 완료 이전 관측(stale)으로 재부팅 전이됨")
	}
}

// 첫 재부팅(attempts==0)은 관측이 install 완료보다 이르더라도 기존대로 트리거한다.
// 신선도 판정은 재부팅 후 재진입을 열기 위한 것이지 첫 재부팅을 늦추려는 것이 아니다(회귀 0).
func TestTransitionState_FirstReboot_StaleMarkerStillTriggers(t *testing.T) {
	done := time.Now()
	dus := makeJobDUS(v1alpha1.UpgradeStateValidating, verOld, verNew)
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip,
		rebootNDRAt(true, done.Add(-time.Minute)), readyNode(true), completedInstallJobAt(done))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebootRequired {
		t.Errorf("state=%q, want RebootRequired (첫 재부팅은 기존 동작 유지)", dus.Status.State)
	}
}

// 마커가 없으면 어떤 경우에도 재부팅으로 넘어가지 않는다 — 불필요한 재부팅이 고착보다 나쁘다.
func TestTransitionState_NoMarker_NeverReboots(t *testing.T) {
	done := time.Now().Add(-2 * time.Minute)
	for _, attempts := range []int32{0, 1} {
		dus := makeJobDUS(v1alpha1.UpgradeStateValidating, verOld, verNew)
		dus.Status.RebootAttempts = attempts
		dip := makeJobDIP(verNew, imgNew, true, true)
		sm := newUpgradeSMWithRecorder(dus, dip,
			rebootNDRAt(false, done.Add(time.Minute)), readyNode(true), completedInstallJobAt(done))

		if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
			t.Fatalf("TransitionState 실패(attempts=%d): %v", attempts, err)
		}
		if dus.Status.State == v1alpha1.UpgradeStateRebootRequired {
			t.Errorf("attempts=%d: 마커가 없는데 재부팅 전이됨", attempts)
		}
	}
}

// 하향(desired < current)에서도 마커가 있으면 재부팅으로 넘어간다.
// 계열 전환 하향은 구 모듈이 사용 중이라 삽입만 못 하는 상태이고 rmmod 로는 풀리지 않는다.
func TestTransitionState_Downgrade_Marker_TriggersReboot(t *testing.T) {
	done := time.Now().Add(-2 * time.Minute)
	// host 는 아직 verNew, 목표는 verOld — 하향.
	dus := makeJobDUS(v1alpha1.UpgradeStateValidating, verNew, verOld)
	dip := makeJobDIP(verOld, imgOld, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip,
		rebootNDRAt(true, done.Add(time.Minute)), readyNode(true), completedInstallJobAt(done))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRebootRequired {
		t.Errorf("state=%q, want RebootRequired (하향 + 마커)", dus.Status.State)
	}
}

// nodeNeedsReboot: NDR 필드 read + maxReboots 기본.
func TestNodeNeedsReboot_And_MaxReboots(t *testing.T) {
	sm := newUpgradeSMWithRecorder(rebootNDR(true))
	need, err := sm.nodeNeedsReboot(context.Background(), jNode, jVendor, jModel)
	if err != nil || !need {
		t.Errorf("nodeNeedsReboot=%v err=%v, want true", need, err)
	}
	if got := maxReboots(makeJobDIP(verNew, imgNew, true, true)); got != 1 {
		t.Errorf("maxReboots default=%d, want 1", got)
	}
}

// detector 가 제품명을 실으면 NodeDeviceReport 는 "a30" 을, DriverUpgradeState.spec.model 은
// 생성 시점 값인 "generic" 을 유지한다. 그 조합에서 두 게이트가 모든 장치를 걸러 항상 false 를
// 내면 증상이 "아무 일도 안 일어남" 이라 드러나지 않는다. 헬퍼 단위 시험만으로는 호출부가
// 헬퍼를 실제로 쓰는지 고정되지 않으므로 게이트 두 곳을 직접 부른다.
func TestGatesSurviveDeviceModelBecomingSpecific(t *testing.T) {
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: jNode},
		Status: v1alpha1.NodeDeviceReportStatus{
			Devices: []v1alpha1.DeviceEntry{
				// 장치 쪽만 판정됨. spec.model 은 여전히 jModel("generic").
				{Vendor: jVendor, Model: "a30", DriverLoaded: false, DriverVersion: verNew,
					NeedsReboot: true, Count: 1},
			},
		},
	}
	sm := newUpgradeSMWithRecorder(ndr)

	need, err := sm.nodeNeedsReboot(context.Background(), jNode, jVendor, jModel)
	if err != nil || !need {
		t.Errorf("nodeNeedsReboot=%v err=%v, want true — 재부팅 게이트가 죽었다", need, err)
	}
	notLoaded, err := sm.driverNotLoaded(context.Background(), jNode, jVendor, jModel)
	if err != nil || !notLoaded {
		t.Errorf("driverNotLoaded=%v err=%v, want true — 자가복구 게이트가 죽었다", notLoaded, err)
	}
}
