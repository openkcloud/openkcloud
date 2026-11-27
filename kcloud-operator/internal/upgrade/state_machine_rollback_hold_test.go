// ============================================================
// state_machine_rollback_hold_test.go: 롤백 뒤 같은 목표 재시도 보류와 롤백 보고 시험
// 생성일: 2026-10-13 | 수정일: 2026-10-14
// ============================================================

package upgrade

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
)

// heldGen 은 heldDUS 가 롤백을 기록한 시점의 정책 generation 이다.
const heldGen int64 = 3

// heldDUS 는 롤백을 끝내고 Idle 로 돌아온 DUS 다. cooldown 을 지나도록 전이 시각을 1분 전으로 둔다.
func heldDUS() *v1alpha1.DriverUpgradeState {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verOld, verNew)
	dus.Status.RolledBackVersion = verNew
	dus.Status.RolledBackGeneration = heldGen
	dus.Status.LastTransitionTime = metav1.NewTime(time.Now().Add(-time.Minute))
	return dus
}

func TestRollbackHold_IdleDoesNotRetrySameTarget(t *testing.T) {
	dus := heldDUS()
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(true, verOld))

	for i := 0; i < 2; i++ {
		if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
			t.Fatalf("TransitionState 실패: %v", err)
		}
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, want Idle — 같은 정책에서 이미 롤백한 목표로 사이클을 다시 열면 cordon·롤백이 반복된다", dus.Status.State)
	}
	if !strings.Contains(dus.Status.Message, "재시도 보류") {
		t.Errorf("message=%q — 보류 사유가 status 에 보이지 않는다", dus.Status.Message)
	}
	// 관리자 요청 롤백도 이 보류를 거치므로 원인을 설치 실패로 단정하지 않는다.
	if strings.Contains(dus.Status.Message, "설치 실패") {
		t.Errorf("message=%q — 롤백 원인을 설치 실패로 단정한다", dus.Status.Message)
	}
	// 경고는 메시지가 바뀔 때 한 번만 낸다(60초마다 쌓이지 않는다).
	n := 0
	for len(sm.Recorder.(*record.FakeRecorder).Events) > 0 {
		if strings.Contains(<-sm.Recorder.(*record.FakeRecorder).Events, "UpgradeHeldAfterRollback") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("UpgradeHeldAfterRollback 이벤트 %d건, want 1", n)
	}
	var jobs batchv1.JobList
	_ = sm.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Errorf("install Job %d건 생성 — 보류 중에는 Job 을 만들지 않는다", len(jobs.Items))
	}
}

func TestRollbackHold_ReleasedOnGenerationChange(t *testing.T) {
	dus := heldDUS()
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen + 1 // 정책 spec 변경
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(true, verOld))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRequired {
		t.Errorf("state=%q, want UpgradeRequired — 정책이 바뀌면 한 번 다시 시도한다", dus.Status.State)
	}
	if dus.Status.RolledBackVersion != "" || dus.Status.RolledBackGeneration != 0 {
		t.Errorf("보류 필드가 남음: %q/%d", dus.Status.RolledBackVersion, dus.Status.RolledBackGeneration)
	}
}

func TestRollbackHold_ReleasedWhenTargetChanges(t *testing.T) {
	dus := heldDUS()
	dus.Status.DesiredVersion = verOld
	dip := makeJobDIP(verOld, imgOld, true, true) // 정책을 현재 버전으로 되돌림
	dip.Generation = heldGen + 1
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(true, verOld))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle || dus.Status.RolledBackVersion != "" {
		t.Errorf("state=%q rolledBack=%q, want Idle·빈 값", dus.Status.State, dus.Status.RolledBackVersion)
	}
	if !strings.Contains(dus.Status.Message, "보류 해제") {
		t.Errorf("message=%q — 보류 해제가 status 에 보이지 않는다", dus.Status.Message)
	}
	// 해제 원인은 정책 변경만이 아니다(versionSource=Host 에서는 적재된 호스트 버전이 목표를 바꾼다).
	if strings.Contains(dus.Status.Message, "(정책 변경)") || !strings.Contains(dus.Status.Message, "목표 또는 정책 generation 변경") {
		t.Errorf("message=%q — 해제 원인을 정책 변경으로만 적는다", dus.Status.Message)
	}
}

// 보류 중인 노드가 다른 경로(호스트에서 직접 설치 등)로 보류 목표에 도달하면 보류를 푼다.
// 풀지 않으면 desired 와 current 가 같은 노드가 보류로 계속 표시된다.
func TestRollbackHold_ReleasedWhenNodeReachesTarget(t *testing.T) {
	dus := heldDUS()
	dus.Status.CurrentVersion = verNew
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(true, verNew))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.RolledBackVersion != "" || dus.Status.RolledBackGeneration != 0 {
		t.Errorf("보류 목표에 도달했는데 보류 필드가 남음: %q/%d", dus.Status.RolledBackVersion, dus.Status.RolledBackGeneration)
	}
	if want := "롤백 보류 해제(노드가 보류 목표 " + verNew + " 에 도달)"; dus.Status.Message != want {
		t.Errorf("message=%q, want %q", dus.Status.Message, want)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state=%q, want Idle", dus.Status.State)
	}
}

// installJobOf 는 이 파일 픽스처 노드의 install Job 을 읽는다.
func installJobOf(t *testing.T, sm *UpgradeStateMachine) *batchv1.Job {
	t.Helper()
	var job batchv1.Job
	key := types.NamespacedName{Name: naming.InstallJobName(jVendor, jModel, jNode), Namespace: driverjob.Namespace}
	if err := sm.Get(context.Background(), key, &job); err != nil {
		t.Fatalf("install Job 조회 실패: %v", err)
	}
	return &job
}

// 보류 중 드라이버가 내려가면 실패한 보류 목표가 아니라 롤백이 복원한 버전을 다시 설치한다.
// 보류 목표를 설치하면 실패했을 때 롤백할 버전이 없어 노드가 드라이버 없이 Failed 에 남는다.
func TestRollbackHold_SelfHealReinstallsRestoredVersion(t *testing.T) {
	dus := heldDUS()
	dus.Status.PreviousVersion = verOld
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(false, verOld))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRequired {
		t.Fatalf("state=%q, want UpgradeRequired — 드라이버가 내려간 노드는 보류 중에도 복구한다", dus.Status.State)
	}
	if dus.Status.DesiredVersion != verOld {
		t.Errorf("desired=%q, want %q — 보류 중 복구가 실패한 보류 목표를 다시 설치한다", dus.Status.DesiredVersion, verOld)
	}
	if dus.Status.PreviousVersion != verOld {
		t.Errorf("previous=%q, want %q — 재설치가 실패하면 롤백할 버전이 없다", dus.Status.PreviousVersion, verOld)
	}
	if dus.Status.RolledBackVersion != verNew || dus.Status.RolledBackGeneration != heldGen {
		t.Errorf("보류 기록이 지워짐: %q/%d — 복구 뒤 같은 목표로 다시 시도한다", dus.Status.RolledBackVersion, dus.Status.RolledBackGeneration)
	}

	// cordon·drain 단계는 이 시험의 관심 밖이다. Upgrading 으로 넘겨 install Job 의 이미지·버전을 본다.
	dus.Status.State = v1alpha1.UpgradeStateUpgrading
	if _, _, err := sm.handleUpgradingJob(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleUpgradingJob 실패: %v", err)
	}
	job := installJobOf(t, sm)
	if got := jobContainerImage(job); got != imgOld {
		t.Errorf("install Job image=%q, want %q — 정책 목표 이미지로 복원 버전을 설치한다", got, imgOld)
	}
	if got := jobDriverVersion(job); got != verOld {
		t.Errorf("install Job DRIVER_VERSION=%q, want %q", got, verOld)
	}
}

// 복원 버전이 허용 목록 밖이어도 복구한다. UnverifiedVersion 으로 보내면 handleUnverifiedVersion 이
// 목표(보류 목표, 목록 안)를 보고 Idle 로 되돌려 매 reconcile 같은 판정을 반복한다.
func TestRollbackHold_SelfHealSkipsAllowlistForRestoredVersion(t *testing.T) {
	dus := heldDUS()
	dus.Status.PreviousVersion = verOld
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen
	dip.Spec.VerifiedVersions = []string{verNew}
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(false, verOld))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRequired || dus.Status.DesiredVersion != verOld {
		t.Errorf("state=%q desired=%q, want UpgradeRequired/%q", dus.Status.State, dus.Status.DesiredVersion, verOld)
	}
}

// 보류 기록은 있는데 currentVersion 이 비면 복원할 버전을 모른다. 종전처럼 정책 목표를 설치하고
// 보류를 지운다.
func TestRollbackHold_SelfHealWithoutCurrentVersionInstallsTarget(t *testing.T) {
	dus := heldDUS()
	dus.Status.CurrentVersion = ""
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode(), makeNDRWithDriver(false, verOld))

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRequired || dus.Status.DesiredVersion != verNew {
		t.Errorf("state=%q desired=%q, want UpgradeRequired/%q", dus.Status.State, dus.Status.DesiredVersion, verNew)
	}
	if dus.Status.RolledBackVersion != "" {
		t.Errorf("rolledBack=%q, want 빈 값", dus.Status.RolledBackVersion)
	}
}

// 복원 버전 재설치가 실패하면 같은 복원 버전으로 롤백한다. 보류 목표는 덮이지 않는다.
func TestRollbackHold_RestoreFailureRollsBackToRestoredVersion(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRollback, verOld, verOld)
	dus.Status.PreviousVersion = verOld
	dus.Status.RolledBackVersion = verNew
	dus.Status.RolledBackGeneration = heldGen
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen
	sm := newUpgradeSMWithRecorder(dus, dip)

	if _, _, err := sm.handleRollbackJob(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRollbackJob 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateValidating {
		t.Fatalf("state=%q, want Validating — 복원 버전으로 롤백 Job 을 만든다", dus.Status.State)
	}
	if dus.Status.RolledBackVersion != verNew {
		t.Errorf("rolledBack=%q, want %q", dus.Status.RolledBackVersion, verNew)
	}
	job := installJobOf(t, sm)
	if jobContainerImage(job) != imgOld || jobDriverVersion(job) != verOld {
		t.Errorf("rollback Job=%s/%s, want %s/%s", jobContainerImage(job), jobDriverVersion(job), imgOld, verOld)
	}
}

// 롤백 사이클이 재부팅 뒤 Upgrading 에 다시 들어오면(재설치로 마커 소거) 정책 목표로 고른
// resolvedImage 가 아니라 롤백과 같은 이전 이미지로 되돌린 버전을 설치한다.
func TestHandleUpgradingJob_HeldCycleUsesPreviousImage(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateUpgrading, verOld, verOld)
	dus.Status.PreviousVersion = verOld
	dus.Status.PreviousImage = imgOld
	dus.Status.RolledBackVersion = verNew
	dus.Status.ResolvedImage = imgNew
	dip := makeJobDIP(verNew, imgNew, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip)

	if _, _, err := sm.handleUpgradingJob(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleUpgradingJob 실패: %v", err)
	}
	if got := jobContainerImage(installJobOf(t, sm)); got != imgOld {
		t.Errorf("install Job image=%q, want %q", got, imgOld)
	}
}

// 이전 이미지를 재구성할 수 없으면(ngc 인데 variant 접미사 없음) 정책 목표 이미지로 다른 버전을
// 설치하지 않고 Failed 로 멈춘다.
func TestHandleUpgradingJob_HeldCycleWithoutImageFails(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateUpgrading, verOld, verOld)
	dus.Status.PreviousVersion = verOld
	dus.Status.RolledBackVersion = verNew
	dip := makeJobDIP(verNew, "reg/nv:latest", true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, makeUncordonedNode())

	if _, _, err := sm.handleUpgradingJob(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleUpgradingJob 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed || !strings.Contains(dus.Status.Message, "재구성 실패") {
		t.Errorf("state=%q message=%q, want Failed·재구성 실패", dus.Status.State, dus.Status.Message)
	}
	// 운영자가 상태 메시지만 보지 않도록 Warning 이벤트도 낸다. install Job 은 만들지 않는다.
	refused := false
	for len(sm.Recorder.(*record.FakeRecorder).Events) > 0 {
		if e := <-sm.Recorder.(*record.FakeRecorder).Events; strings.Contains(e, "Warning RestoreImageRefused") {
			refused = true
		}
	}
	if !refused {
		t.Error("RestoreImageRefused Warning 이벤트가 없다")
	}
	var jobs batchv1.JobList
	_ = sm.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Errorf("install Job %d건 생성, want 0", len(jobs.Items))
	}
}

// 보류 중 복구 사이클은 롤백 Job 을 만들지 않으므로 끝날 때 롤백 완료가 아니라 자체 복구 완료를
// 보고한다. 업그레이드 성공 지표도 세지 않는다.
func TestHandleUncordoning_AfterHeldRestore_ReportsSelfHeal(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateUncordoning, verOld, verOld)
	dus.Status.RolledBackVersion = verNew
	sm := newUpgradeSMWithRecorder(dus, makeUncordonedNode())
	before := testutil.ToFloat64(metrics.UpgradeTotal.WithLabelValues(jVendor, "success"))

	if _, _, err := sm.handleUncordoning(context.Background(), dus); err != nil {
		t.Fatalf("handleUncordoning 실패: %v", err)
	}
	if want := "자체 복구 완료(롤백 보류 유지): 보류 목표 " + verNew + " 대신 " + verOld + " 재설치"; dus.Status.Message != want {
		t.Errorf("message=%q, want %q", dus.Status.Message, want)
	}
	if d := testutil.ToFloat64(metrics.UpgradeTotal.WithLabelValues(jVendor, "success")) - before; d != 0 {
		t.Errorf("success 지표 +%v, want 0", d)
	}
	for len(sm.Recorder.(*record.FakeRecorder).Events) > 0 {
		if e := <-sm.Recorder.(*record.FakeRecorder).Events; strings.Contains(e, "RollbackCompleted") {
			t.Errorf("복구 사이클에 RollbackCompleted 이벤트: %s", e)
		}
	}
}

func TestMarkRolledBack_RecordsTargetAndGeneration(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRollback, verNew, verNew)
	dus.Status.PreviousVersion = verOld
	dus.Status.PreviousImage = imgOld
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = 7
	sm := newUpgradeSMWithRecorder(dus, dip)

	if _, _, err := sm.handleRollbackJob(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRollbackJob 실패: %v", err)
	}
	if dus.Status.RolledBackVersion != verNew || dus.Status.RolledBackGeneration != 7 {
		t.Errorf("rolledBack=%q/%d, want %q/7", dus.Status.RolledBackVersion, dus.Status.RolledBackGeneration, verNew)
	}
}

func TestMarkRolledBack_SecondRollbackKeepsTarget(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRollback, verNew, verOld) // 롤백이 DesiredVersion 을 이전 버전으로 바꾼 뒤
	dus.Status.PreviousVersion = verOld
	dus.Status.PreviousImage = imgOld
	dus.Status.RolledBackVersion = verNew
	dus.Status.RolledBackGeneration = 7
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = 7
	sm := newUpgradeSMWithRecorder(dus, dip)

	if _, _, err := sm.handleRollbackJob(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRollbackJob 실패: %v", err)
	}
	if dus.Status.RolledBackVersion != verNew {
		t.Errorf("rolledBack=%q, want %q — 두 번째 롤백이 보류 대상을 이전 버전으로 덮으면 보류가 풀린다", dus.Status.RolledBackVersion, verNew)
	}
}

func TestHandleUncordoning_AfterRollback_ReportsRollback(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateUncordoning, verOld, verOld)
	dus.Status.RolledBackVersion = verNew
	dus.Status.RollbackAttempts = 1 // 실제 롤백 사이클은 롤백 Job 을 만들 때 1 이상이다
	sm := newUpgradeSMWithRecorder(dus, makeUncordonedNode())
	before := testutil.ToFloat64(metrics.UpgradeTotal.WithLabelValues(jVendor, "success"))

	if _, _, err := sm.handleUncordoning(context.Background(), dus); err != nil {
		t.Fatalf("handleUncordoning 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, want Idle", dus.Status.State)
	}
	if !strings.Contains(dus.Status.Message, "롤백 완료") || strings.Contains(dus.Status.Message, "업그레이드 완료") {
		t.Errorf("message=%q — 롤백으로 끝난 사이클을 업그레이드 완료로 보고한다", dus.Status.Message)
	}
	if want := "롤백 완료: " + verNew + " 대신 " + verOld + " 로 복원"; dus.Status.Message != want {
		t.Errorf("message=%q, want %q — 관리자 요청 롤백도 거치므로 원인을 단정하지 않는다", dus.Status.Message, want)
	}
	if d := testutil.ToFloat64(metrics.UpgradeTotal.WithLabelValues(jVendor, "success")) - before; d != 0 {
		t.Errorf("success 지표 +%v, want 0", d)
	}
}

func TestHandleUncordoning_Success_CountsOnce(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateUncordoning, verNew, verNew)
	sm := newUpgradeSMWithRecorder(dus, makeUncordonedNode())
	before := testutil.ToFloat64(metrics.UpgradeTotal.WithLabelValues(jVendor, "success"))

	if _, _, err := sm.handleUncordoning(context.Background(), dus); err != nil {
		t.Fatalf("handleUncordoning 실패: %v", err)
	}
	if dus.Status.Message != "업그레이드 완료" {
		t.Errorf("message=%q, want 업그레이드 완료", dus.Status.Message)
	}
	if d := testutil.ToFloat64(metrics.UpgradeTotal.WithLabelValues(jVendor, "success")) - before; d != 1 {
		t.Errorf("success 지표 +%v, want 1 — 성공 한 번을 두 번 센다", d)
	}
}

func TestMarkRolledBack_ReentryKeepsFirstGeneration(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateRollback, verNew, verNew)
	dus.Status.PreviousVersion = verOld
	dus.Status.PreviousImage = imgOld
	dus.Status.RolledBackVersion = verNew
	dus.Status.RolledBackGeneration = 7
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = 8 // 롤백 재진입 사이에 정책 spec 이 바뀜
	sm := newUpgradeSMWithRecorder(dus, dip)

	if _, _, err := sm.handleRollbackJob(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRollbackJob 실패: %v", err)
	}
	if dus.Status.RolledBackGeneration != 7 {
		t.Errorf("rolledBackGeneration=%d, want 7 — 재진입이 롤백 시작 때 값을 덮으면 그 사이의 정책 변경이 보류를 풀지 못한다",
			dus.Status.RolledBackGeneration)
	}
}

// 데몬셋 모드 롤백도 이전 버전을 확인한 직후 보류 대상을 기록한다. 픽스처에 DaemonSet 이 없어
// 호출은 DS 조회 오류로 끝나지만 기록은 그 앞에서 일어나므로 반환 오류는 보지 않는다.
func TestMarkRolledBack_DaemonSetModeRecords(t *testing.T) {
	dus := makeRollbackDUS("k8s-worker1-nvidia", "k8s-worker1", "nvidia", 0)
	dip := makeRollbackDIP("nvidia-generic", "nvidia", 3)
	dip.Generation = 5
	sm := newUpgradeSMWithRecorder(dus, dip)

	_, _, _ = sm.handleRollback(context.Background(), dus, dip)
	if dus.Status.RolledBackVersion != "580.142" || dus.Status.RolledBackGeneration != 5 {
		t.Errorf("rolledBack=%q/%d, want 580.142/5 — 데몬셋 모드 롤백이 보류 대상을 기록하지 않는다",
			dus.Status.RolledBackVersion, dus.Status.RolledBackGeneration)
	}
}

// versionSource=Host 에서는 로드된 호스트 버전이 바뀌면 정책 generation 변경 없이 목표가 바뀐다.
// 목표 변경 하나만으로 보류가 풀려야 한다.
func TestReleaseRollbackHold_TargetChangeAloneReleases(t *testing.T) {
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Generation = heldGen

	moved := heldDUS()
	releaseRollbackHold(moved, dip, verOld, verOld)
	if moved.Status.RolledBackVersion != "" || moved.Status.RolledBackGeneration != 0 {
		t.Errorf("목표만 바뀌었는데 보류가 남음: %q/%d", moved.Status.RolledBackVersion, moved.Status.RolledBackGeneration)
	}

	same := heldDUS()
	releaseRollbackHold(same, dip, verNew, verOld)
	if same.Status.RolledBackVersion != verNew || same.Status.RolledBackGeneration != heldGen {
		t.Errorf("같은 목표·같은 generation 인데 보류가 풀림: %q/%d", same.Status.RolledBackVersion, same.Status.RolledBackGeneration)
	}
}

// 데몬셋 모드 롤백 시험의 DUS 가 보관한 이전 이미지와, 롤백이 보류 목표로 기록할 버전이다.
const (
	rolledBackImage  = "reg/nv:535.288-v1"
	rolledBackTarget = "580.142"
)

// makeDriverDS 는 데몬셋 모드 롤백이 패치할 드라이버 DaemonSet 이다.
func makeDriverDS(vendor, model, image string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: naming.DriverDSName(vendor, model), Namespace: naming.OperatorNamespace()},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: vendor + "-driver", Image: image}},
		}}},
	}
}

// newUpgradeSMOnDSPatch 는 DaemonSet 패치가 들어오는 순간 onPatch 를 부르고 원래 패치를 이어 가는
// 상태 기계를 만든다. 패치 직전 저장소의 상태를 보려는 시험용이다.
func newUpgradeSMOnDSPatch(onPatch func(ctx context.Context, c client.Client), objs ...client.Object) *UpgradeStateMachine {
	c := fake.NewClientBuilder().
		WithScheme(newUpgradeTestScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.DriverUpgradeState{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch,
				opts ...client.PatchOption) error {
				if _, ok := obj.(*appsv1.DaemonSet); ok {
					onPatch(ctx, c)
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()
	return &UpgradeStateMachine{Client: c, Recorder: record.NewFakeRecorder(64)}
}

// 데몬셋 모드 롤백은 DS 를 패치하기 전에 보류 기록을 저장한다. 드라이버 DaemonSet 컨트롤러는 패치
// 이벤트로 깨어나 이 기록을 보고 이미지를 유지하므로, 저장 전에 패치하면 정책 이미지로 되돌린다.
func TestHandleRollback_DaemonSetPersistsHoldBeforePatch(t *testing.T) {
	dus := makeRollbackDUS("k8s-worker1-nvidia", "k8s-worker1", "nvidia", 0)
	dus.Status.PreviousImage = rolledBackImage
	dip := makeRollbackDIP("nvidia-generic", "nvidia", 3)
	dip.Generation = 5
	ds := makeDriverDS("nvidia", "", "reg/nv:580.142-v1")
	patches := 0
	var atPatch v1alpha1.DriverUpgradeState // DS 패치가 들어온 시점의 저장소 DUS
	sm := newUpgradeSMOnDSPatch(func(ctx context.Context, c client.Client) {
		patches++
		if err := c.Get(ctx, client.ObjectKeyFromObject(dus), &atPatch); err != nil {
			t.Errorf("패치 시점 DUS 조회 실패: %v", err)
		}
	}, dus, dip, ds)

	if _, _, err := sm.handleRollback(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRollback 실패: %v", err)
	}
	if patches != 1 {
		t.Fatalf("DS 패치 %d회, want 1", patches)
	}
	if atPatch.Status.RolledBackVersion != rolledBackTarget || atPatch.Status.RolledBackGeneration != 5 {
		t.Errorf("DS 패치 시점의 저장된 보류 기록=%q/%d, want 580.142/5 — 패치 뒤에 저장하면 DS 컨트롤러가 먼저 되돌린다",
			atPatch.Status.RolledBackVersion, atPatch.Status.RolledBackGeneration)
	}
	var stored v1alpha1.DriverUpgradeState
	if err := sm.Get(context.Background(), client.ObjectKeyFromObject(dus), &stored); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if stored.Status.RolledBackVersion != rolledBackTarget || stored.Status.RolledBackGeneration != 5 {
		t.Errorf("저장된 보류 기록=%q/%d, want 580.142/5", stored.Status.RolledBackVersion, stored.Status.RolledBackGeneration)
	}
	var got appsv1.DaemonSet
	if err := sm.Get(context.Background(), client.ObjectKeyFromObject(ds), &got); err != nil {
		t.Fatalf("DS 조회 실패: %v", err)
	}
	if img := got.Spec.Template.Spec.Containers[0].Image; img != rolledBackImage {
		t.Errorf("DS image=%q, want reg/nv:535.288-v1", img)
	}
}

// 롤백 한 번은 롤백 지표 한 번이다.
func TestHandleRollback_DaemonSetCountsRollbackOnce(t *testing.T) {
	dus := makeRollbackDUS("k8s-worker1-nvidia", "k8s-worker1", "nvidia", 0)
	dus.Status.PreviousImage = rolledBackImage
	dip := makeRollbackDIP("nvidia-generic", "nvidia", 3)
	sm := newUpgradeSMWithRecorder(dus, dip, makeDriverDS("nvidia", "", "reg/nv:580.142-v1"))
	before := testutil.ToFloat64(metrics.RollbackTotal.WithLabelValues("nvidia"))

	if _, _, err := sm.handleRollback(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRollback 실패: %v", err)
	}
	if d := testutil.ToFloat64(metrics.RollbackTotal.WithLabelValues("nvidia")) - before; d != 1 {
		t.Errorf("롤백 지표 +%v, want 1", d)
	}
}

// 재시도를 소진해 Failed 로 가는 경로는 롤백을 하지 않으므로 롤백 지표를 세지 않는다.
func TestHandleRollback_DaemonSetExhaustedDoesNotCountRollback(t *testing.T) {
	dus := makeRollbackDUS("k8s-worker1-nvidia", "k8s-worker1", "nvidia", 3)
	dip := makeRollbackDIP("nvidia-generic", "nvidia", 3)
	sm := newUpgradeSMWithRecorder(dus, dip, makeDriverDS("nvidia", "", "reg/nv:580.142-v1"))
	before := testutil.ToFloat64(metrics.RollbackTotal.WithLabelValues("nvidia"))

	if _, _, err := sm.handleRollback(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleRollback 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Fatalf("state=%q, want Failed", dus.Status.State)
	}
	if d := testutil.ToFloat64(metrics.RollbackTotal.WithLabelValues("nvidia")) - before; d != 0 {
		t.Errorf("롤백 지표 +%v, want 0", d)
	}
}
