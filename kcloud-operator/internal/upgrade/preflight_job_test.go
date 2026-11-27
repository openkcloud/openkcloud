// ============================================================
// preflight_job_test.go: PreFlight 설치 전 점검 Job 실행·판정 시험
// 상세: fake client 로 점검 Job 생성·대기·통과·hold 충돌·판정 불가(옛 이미지·이미지 pull 실패와 유예 경계)·낡은 Job 재생성·API 오류를 본다.
//       hold 충돌이면 노드를 cordon 하지 않고 다른 사이클의 노드 라벨도 지우지 않은 채 Failed 로 가는지 단정한다.
//       점검 Job 을 기다리는 회차에는 PreFlight 경고 세 개를 다시 내지 않는지도 본다.
//       점검 Job 이 노드 커널로 고른 사전 컴파일 이미지(status.resolvedImage)를 쓰고 대기 회차에도 그 값이 갱신되는지도 본다.
// 생성일: 2026-10-27 | 수정일: 2026-10-28
// ============================================================

package upgrade

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/naming"
)

const holdConflictMsg = "hold 된 패키지(libx11-6)가 드라이버 설치를 막는다 — libx11-xcb1 : Depends: libx11-6 " +
	"(= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1ubuntu0.3 is to be installed"

// makeJobPod 는 job 소유 파드 하나다. lastState 면 직전 종료 기록(OnFailure 재시작 전)에 둔다.
func makeJobPod(jobName, container string, exitCode int32, reason, message string, lastState bool) *corev1.Pod {
	term := &corev1.ContainerStateTerminated{ExitCode: exitCode, Reason: reason, Message: message,
		FinishedAt: metav1.Now()}
	cs := corev1.ContainerStatus{Name: container}
	if lastState {
		cs.LastTerminationState.Terminated = term
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}
	} else {
		cs.State.Terminated = term
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: jobName + "-p", Namespace: driverjob.Namespace,
			Labels: map[string]string{batchv1.JobNameLabel: jobName}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

// makeWaitingPod 는 job 소유 파드 하나로, container 가 reason 으로 기다리는 중이다.
func makeWaitingPod(job *batchv1.Job, container, reason, message string) *corev1.Pod {
	p := makeJobPod(job.Name, container, 0, "", "", false)
	p.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: message}}
	return p
}

// markJob 은 Job status 를 완료 또는 실패로 둔다.
func markJob(j *batchv1.Job, complete, failed bool) *batchv1.Job {
	if complete {
		j.Status.Succeeded = 1
		j.Status.Conditions = append(j.Status.Conditions,
			batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue})
	}
	if failed {
		j.Status.Failed = 1
		j.Status.Conditions = append(j.Status.Conditions,
			batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue})
	}
	return j
}

func preflightKey() types.NamespacedName {
	return types.NamespacedName{Name: naming.PreflightJobName(jVendor, jModel, jNode), Namespace: driverjob.Namespace}
}

func preflightGone(t *testing.T, sm *UpgradeStateMachine) bool {
	t.Helper()
	err := sm.Get(context.Background(), preflightKey(), &batchv1.Job{})
	return apierrors.IsNotFound(err)
}

func TestPreflightApplicable(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*v1alpha1.DriverInstallPolicy)
		want   bool
	}{
		{"nvidia job apt", func(*v1alpha1.DriverInstallPolicy) {}, true},
		{"daemonset", func(p *v1alpha1.DriverInstallPolicy) { p.Spec.Driver.Mode = "" }, false},
		{"ngc", func(p *v1alpha1.DriverInstallPolicy) { p.Spec.Driver.Installer = v1alpha1.DriverInstallerNGC }, false},
		{"installer empty", func(p *v1alpha1.DriverInstallPolicy) { p.Spec.Driver.Installer = "" }, false},
		{"other vendor", func(p *v1alpha1.DriverInstallPolicy) { p.Spec.Vendor = "tenstorrent" }, false},
		{"trackOnly", func(p *v1alpha1.DriverInstallPolicy) { p.Spec.Driver.TrackOnly = true }, false},
	}
	for _, c := range cases {
		p := makeAptJobDIP(verNew)
		c.mutate(p)
		if got := preflightApplicable(p); got != c.want {
			t.Errorf("%s: preflightApplicable=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestHandlePreFlight_NotApplicable_GoesToCordoning(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeJobDIP(verNew, imgNew, true, true) // ngc — 점검 대상 아님
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true))
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateCordoning || !preflightGone(t, sm) {
		t.Errorf("state=%s preflightGone=%v, want Cordoning·true", dus.Status.State, preflightGone(t, sm))
	}
}

func TestHandlePreFlight_CreatesPreflightJob(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true))
	requeue, after, err := sm.handlePreFlight(context.Background(), dus, dip)
	if err != nil || !requeue || after != 10*time.Second || dus.Status.State != v1alpha1.UpgradeStatePreFlight {
		t.Fatalf("err=%v requeue=%v after=%s state=%s", err, requeue, after, dus.Status.State)
	}
	var job batchv1.Job
	if err := sm.Get(context.Background(), preflightKey(), &job); err != nil {
		t.Fatalf("점검 Job 없음: %v", err)
	}
	c := job.Spec.Template.Spec.Containers[0]
	if c.Name != driverjob.PreflightContainerName ||
		!reflect.DeepEqual(c.Command, []string{"/usr/local/bin/preflight.sh"}) {
		t.Errorf("container=%s command=%v", c.Name, c.Command)
	}
	if !hasEvent(drainEvents(sm), "DriverPreflightStarted") {
		t.Error("DriverPreflightStarted 이벤트 없음")
	}
}

func TestHandlePreFlight_PreflightRunning_Waits(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	running := driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew)
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), running)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStatePreFlight || preflightGone(t, sm) {
		t.Errorf("state=%s gone=%v, want PreFlight·false", dus.Status.State, preflightGone(t, sm))
	}
}

// 점검 Job 을 기다리는 회차에는 PreFlight 경고를 다시 내지 않는다.
func TestHandlePreFlight_WarningsOnlyOnFirstPass(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	// fake client 는 저장할 때 managedFields 항목의 operation·apiVersion·fieldsType 이 비면 거부한다.
	dip.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: "kubectl-edit", Operation: metav1.ManagedFieldsOperationUpdate,
		APIVersion: v1alpha1.GroupVersion.String(), FieldsType: "FieldsV1",
		FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:spec":{}}`)}}}
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true))
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("1차: %v", err)
	}
	if !hasEvent(drainEvents(sm), eventPolicySharedOwnership) {
		t.Fatal("첫 회차에 공동 소유 경고가 없다")
	}
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("2차: %v", err)
	}
	if hasEvent(drainEvents(sm), eventPolicySharedOwnership) {
		t.Error("점검 Job 대기 회차에 경고가 반복됐다")
	}
}

// 나머지 두 PreFlight 경고(probe 예산 초과·사전 컴파일 이미지 미사용)도 점검 Job 대기 회차에는 내지 않는다.
// 경고 하나만 반복돼도 이벤트 스팸 필터가 뒤따르는 점검 결과 경고를 버릴 수 있다.
func TestHandlePreFlight_ProbeBudgetAndImageSkipWarningsOnlyOnFirstPass(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	dip.Spec.UpgradePolicy.ValidationTimeout = "1m"
	dip.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{
		Enabled: true, Image: "probe:v1", ResourceName: "nvidia.com/gpu", TimeoutSeconds: 300}
	dip.Spec.Driver.KernelImages = map[string]v1alpha1.KernelImageRef{
		"6.8.0-1-generic": "reg/nv:590.48.01-k6.8.0-1-generic-v1"}
	node := readyNode(true)
	node.Status.NodeInfo.KernelVersion = "5.15.0-181-generic" // kernelImages 에 없는 커널
	sm := newUpgradeSMWithRecorder(dus, dip, node)
	warnings := []string{"WorkloadProbeTimeoutExceedsBudget", eventKernelImageMissing}
	for i, want := range []bool{true, false} {
		if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
			t.Fatalf("%d차: %v", i+1, err)
		}
		events := drainEvents(sm)
		for _, w := range warnings {
			if got := hasEvent(events, w); got != want {
				t.Errorf("%d차: %s 경고=%v, want %v", i+1, w, got, want)
			}
		}
	}
}

// 노드 커널과 그 커널용 사전 컴파일 이미지(kernelImages 항목)다.
const (
	precompiledKernel = "6.8.0-1-generic"
	precompiledImage  = "reg/nv:" + verNew + "-k" + precompiledKernel + "-v1"
)

// newPrecompiledPreFlightSM 은 노드 커널에 맞는 사전 컴파일 이미지가 kernelImages 에 있는 PreFlight 상태 기계다.
func newPrecompiledPreFlightSM() (*UpgradeStateMachine, *v1alpha1.DriverUpgradeState, *v1alpha1.DriverInstallPolicy) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	dip.Spec.Driver.KernelImages = map[string]v1alpha1.KernelImageRef{precompiledKernel: precompiledImage}
	node := readyNode(true)
	node.Status.NodeInfo.KernelVersion = precompiledKernel
	return newUpgradeSMWithRecorder(dus, dip, node), dus, dip
}

// 점검 Job 은 install Job 과 같은 대상으로 만든다 — 노드 커널로 고른 사전 컴파일 이미지(status.resolvedImage)를 쓴다.
func TestHandlePreFlight_PreflightJobUsesResolvedKernelImage(t *testing.T) {
	sm, dus, dip := newPrecompiledPreFlightSM()
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	var job batchv1.Job
	if err := sm.Get(context.Background(), preflightKey(), &job); err != nil {
		t.Fatalf("점검 Job 없음: %v", err)
	}
	image, version := installTargetOf(dus, dip)
	install := driverjob.RenderInstallJob(dip, jNode, image, version)
	if got := jobContainerImage(&job); got != precompiledImage || got != jobContainerImage(install) {
		t.Errorf("점검 Job image=%q, want 사전 컴파일 이미지 %q(install Job: %q)", got, precompiledImage, jobContainerImage(install))
	}
	if got, want := jobDriverVersion(&job), jobDriverVersion(install); got != want {
		t.Errorf("점검 Job DRIVER_VERSION=%q, want install Job 과 같은 %q", got, want)
	}
}

// 점검 Job 을 기다리는 회차에도 status.resolvedImage 는 지금 정책 기준으로 다시 고른다. 첫 회차에 한정되는 것은 경고뿐이다.
func TestHandlePreFlight_ResolvedImageRefreshedWhileWaiting(t *testing.T) {
	sm, dus, dip := newPrecompiledPreFlightSM()
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("1차: %v", err)
	}
	if dus.Status.ResolvedImage != precompiledImage {
		t.Fatalf("1차 resolvedImage=%q, want %q", dus.Status.ResolvedImage, precompiledImage)
	}
	// 점검 Job 이 있는 대기 회차에서 정책이 사전 컴파일 이미지를 걷으면 기본 이미지로 돌아간다.
	changed := dip.DeepCopy()
	changed.Spec.Driver.KernelImages = nil
	if _, _, err := sm.handlePreFlight(context.Background(), dus, changed); err != nil {
		t.Fatalf("2차: %v", err)
	}
	if dus.Status.ResolvedImage != changed.Spec.Driver.Image {
		t.Errorf("대기 회차 resolvedImage=%q, want 기본 이미지 %q", dus.Status.ResolvedImage, changed.Spec.Driver.Image)
	}
}

func TestHandlePreFlight_PreflightPassed_Cordoning(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	done := markJob(driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew), true, false)
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), done)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateCordoning {
		t.Errorf("state=%s, want Cordoning", dus.Status.State)
	}
	if !preflightGone(t, sm) {
		t.Error("결과를 읽은 점검 Job 이 남았다 — 다음 사이클이 지난 결과를 다시 읽는다")
	}
	if !hasEvent(drainEvents(sm), "DriverPreflightPassed") {
		t.Error("DriverPreflightPassed 이벤트 없음")
	}
}

func TestHandlePreFlight_HoldConflict_FailedWithoutCordon(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	failed := markJob(driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew), false, true)
	pod := makeJobPod(failed.Name, driverjob.PreflightContainerName, 3, "Error", holdConflictMsg, false)
	// 같은 노드에서 다른 사이클이 단 업그레이드 라벨이다. 점검 차단은 cordon 전이라 이 사이클의 라벨이 아니다.
	busy := readyNode(true)
	busy.Labels = map[string]string{
		driverUpgradingLabelKey:         labelValueTrue,
		driverUpgradingBlockingLabelKey: labelValueTrue,
	}
	sm := newUpgradeSMWithRecorder(dus, dip, busy, failed, pod)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Fatalf("state=%s, want Failed", dus.Status.State)
	}
	if !strings.HasPrefix(dus.Status.Message, "설치 전 점검 실패(노드 cordon 하지 않음): "+holdConflictMsg) ||
		!strings.Contains(dus.Status.Message, "kubectl delete dus "+jDUSNm) {
		t.Errorf("message=%q", dus.Status.Message)
	}
	var node corev1.Node
	if err := sm.Get(context.Background(), types.NamespacedName{Name: jNode}, &node); err != nil ||
		node.Spec.Unschedulable {
		t.Errorf("노드가 cordon 됐다(err=%v)", err)
	}
	for _, key := range []string{driverUpgradingLabelKey, driverUpgradingBlockingLabelKey} {
		if node.Labels[key] != labelValueTrue {
			t.Errorf("점검 차단이 다른 사이클의 라벨 %s 를 지웠다(labels=%v)", key, node.Labels)
		}
	}
	install := types.NamespacedName{Name: naming.InstallJobName(jVendor, jModel, jNode), Namespace: driverjob.Namespace}
	if err := sm.Get(context.Background(), install, &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Errorf("install Job 이 생겼다(err=%v)", err)
	}
	if !preflightGone(t, sm) || !hasEvent(drainEvents(sm), "DriverPreflightBlocked") {
		t.Error("점검 Job 미삭제 또는 DriverPreflightBlocked 이벤트 없음")
	}
}

func TestHandlePreFlight_OldImageStartError_Proceeds(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	failed := markJob(driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew), false, true)
	pod := makeJobPod(failed.Name, driverjob.PreflightContainerName, 128, "StartError",
		`exec: "/usr/local/bin/preflight.sh": stat /usr/local/bin/preflight.sh: no such file or directory`, false)
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), failed, pod)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateCordoning {
		t.Errorf("state=%s, want Cordoning — 옛 설치기 이미지가 설치를 막았다", dus.Status.State)
	}
	if !hasEvent(drainEvents(sm), "DriverPreflightInconclusive") {
		t.Error("DriverPreflightInconclusive 이벤트 없음")
	}
}

func TestHandlePreFlight_FailedWithoutPod_Proceeds(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	failed := markJob(driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew), false, true)
	// 파드가 정리돼 종료 기록이 없다 — Job 의 Failed 조건 사유가 유일한 단서다.
	failed.Status.Conditions[0].Reason = "DeadlineExceeded"
	failed.Status.Conditions[0].Message = "Job was active longer than specified deadline"
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), failed)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateCordoning {
		t.Errorf("state=%s, want Cordoning", dus.Status.State)
	}
	var inconclusive string
	for _, e := range drainEvents(sm) {
		if strings.Contains(e, "DriverPreflightInconclusive") {
			inconclusive = e
		}
	}
	if !strings.Contains(inconclusive, "DeadlineExceeded") || !strings.Contains(inconclusive, "specified deadline") ||
		strings.Contains(inconclusive, "exit=") {
		t.Errorf("판정 불가 이벤트=%q, want Job 실패 사유 포함·exit= 없음(종료 코드 0 은 통과처럼 읽힌다)", inconclusive)
	}
}

// 시작 시각을 알 수 없는 Job(Status.StartTime·생성 시각이 모두 없음)은 유예 없이 바로 판정 불가로 넘긴다.
func TestHandlePreFlight_ImagePullBackOff_NoStartTime_Proceeds(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	running := driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew)
	pod := makeWaitingPod(running, driverjob.PreflightContainerName, "ImagePullBackOff", "Back-off pulling image")
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), running, pod)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateCordoning || !preflightGone(t, sm) {
		t.Errorf("state=%s gone=%v, want Cordoning·Job 삭제", dus.Status.State, preflightGone(t, sm))
	}
	ev := drainEvents(sm)
	if !hasEvent(ev, "Warning DriverPreflightInconclusive") ||
		!strings.Contains(strings.Join(ev, "\n"), "ImagePullBackOff") {
		t.Errorf("events=%v", ev)
	}
}

// 유예 경계(preflightPullGrace, 180초) 양쪽의 점검 Job 시작 시각이다. fake client 는 시각의 초 아래를 버리므로 먼저 초
// 단위로 맞추고, 시험이 도는 동안 시계가 흘러도 판정이 바뀌지 않게 경계에서 1초 이상 떨어뜨린다 — 안쪽(179초 전)은
// 올림, 바깥쪽(181초 전)은 내림이다.
func startedJustInsideGrace() metav1.Time {
	return metav1.NewTime(time.Now().Add(-179 * time.Second).Truncate(time.Second).Add(time.Second))
}

func startedJustOutsideGrace() metav1.Time {
	return metav1.NewTime(time.Now().Add(-181 * time.Second).Truncate(time.Second))
}

// newStuckPreflightSM 은 점검 컨테이너가 ImagePullBackOff 로 기다리는 점검 Job 이 있는 PreFlight 상태 기계다.
// mutate 가 점검 Job 을 손보고(시작 시각 등), funcs 가 있으면 클라이언트 호출을 가로챈다.
func newStuckPreflightSM(mutate func(*batchv1.Job), funcs *interceptor.Funcs) (
	*UpgradeStateMachine, *v1alpha1.DriverUpgradeState, *v1alpha1.DriverInstallPolicy,
) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	running := driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew)
	mutate(running)
	pod := makeWaitingPod(running, driverjob.PreflightContainerName, "ImagePullBackOff", "Back-off pulling image")
	c := newFakeClient(funcs, dus, dip, readyNode(true), running, pod)
	return &UpgradeStateMachine{Client: c, Recorder: record.NewFakeRecorder(64)}, dus, dip
}

// 점검 Job 이 시작된 지 preflightPullGrace 가 지나지 않았으면 pull 이 실패해도 kubelet 의 재시도를 기다린다.
// 시작 시각은 Status.StartTime 이고, 없으면 생성 시각이다. 경계 안쪽(179초 전 시작)에서 대기한다.
func TestHandlePreFlight_ImagePullBackOff_WithinGrace_KeepsWaiting(t *testing.T) {
	started := startedJustInsideGrace()
	for name, setStart := range map[string]func(*batchv1.Job){
		"StartTime":         func(j *batchv1.Job) { j.Status.StartTime = &started },
		"CreationTimestamp": func(j *batchv1.Job) { j.CreationTimestamp = started },
	} {
		t.Run(name, func(t *testing.T) {
			sm, dus, dip := newStuckPreflightSM(setStart, nil)
			if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
				t.Fatalf("handlePreFlight: %v", err)
			}
			if dus.Status.State != v1alpha1.UpgradeStatePreFlight || preflightGone(t, sm) {
				t.Errorf("state=%s gone=%v, want PreFlight 대기·Job 유지", dus.Status.State, preflightGone(t, sm))
			}
			if ev := drainEvents(sm); hasEvent(ev, "DriverPreflightInconclusive") {
				t.Errorf("유예 안인데 판정 불가 이벤트가 났다: %v", ev)
			}
		})
	}
}

// 유예가 지나도 pull 이 계속 실패하면 Job 을 지우고 판정 불가로 넘긴다. 이벤트에는 받지 못한 시간이 실린다.
// 경계 바깥쪽(181초 전 시작)에서 진행한다.
func TestHandlePreFlight_ImagePullBackOff_AfterGrace_Proceeds(t *testing.T) {
	started := startedJustOutsideGrace()
	sm, dus, dip := newStuckPreflightSM(func(j *batchv1.Job) { j.Status.StartTime = &started }, nil)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateCordoning || !preflightGone(t, sm) {
		t.Errorf("state=%s gone=%v, want Cordoning·Job 삭제", dus.Status.State, preflightGone(t, sm))
	}
	events := strings.Join(drainEvents(sm), "\n")
	for _, want := range []string{"Warning DriverPreflightInconclusive", "받지 못함", "ImagePullBackOff"} {
		if !strings.Contains(events, want) {
			t.Errorf("이벤트에 %q 없음: %s", want, events)
		}
	}
	// 받지 못한 시간은 초 단위로 반올림해 싣는다. 181~182초가 지났으므로 3m1s 또는 3m2s 다.
	if !regexp.MustCompile(`이미지를 3m[0-9]+s 동안 받지 못함`).MatchString(events) {
		t.Errorf("이벤트에 3분 남짓의 경과(초 단위 반올림)가 없다: %s", events)
	}
}

// 유예가 지난 뒤 점검 Job 삭제가 NotFound 로 실패하면 이미 지워진 것이라 그대로 진행하고, 그 밖의 오류면 오류로
// 올린다. 오류일 때 Job 은 남고 판정 불가 이벤트는 내지 않는다 — 이벤트는 삭제가 끝난 뒤에만 낸다.
func TestHandlePreFlight_ImagePullBackOff_DeleteFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     func(job string) error
		wantErr bool
	}{
		{"NotFound", func(job string) error { return apierrors.NewNotFound(batchv1.Resource("jobs"), job) }, false},
		{"그 밖의 오류", func(string) error { return apierrors.NewInternalError(errors.New("apiserver 응답 없음")) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := startedJustOutsideGrace()
			sm, dus, dip := newStuckPreflightSM(func(j *batchv1.Job) { j.Status.StartTime = &started }, &interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, isJob := obj.(*batchv1.Job); isJob {
						return tc.err(obj.GetName())
					}
					return c.Delete(ctx, obj, opts...)
				}})
			_, _, err := sm.handlePreFlight(context.Background(), dus, dip)
			ev := drainEvents(sm)
			if !tc.wantErr {
				if err != nil || dus.Status.State != v1alpha1.UpgradeStateCordoning ||
					!hasEvent(ev, "Warning DriverPreflightInconclusive") {
					t.Errorf("err=%v state=%s events=%v, want 오류 없이 Cordoning·판정 불가 이벤트", err, dus.Status.State, ev)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "설치 전 점검 Job 삭제 실패") {
				t.Errorf("err=%v, want 점검 Job 삭제 실패 오류", err)
			}
			if dus.Status.State != v1alpha1.UpgradeStatePreFlight || preflightGone(t, sm) || len(ev) != 0 {
				t.Errorf("state=%s gone=%v events=%v, want PreFlight·Job 유지·이벤트 없음",
					dus.Status.State, preflightGone(t, sm), ev)
			}
		})
	}
}

// 점검 파드 목록을 읽지 못하면 판정하지 않고 오류로 올린다. 진행 중인 Job(대기 사유 조회)과 끝난 Job(종료 기록 조회)
// 모두 같다 — 파드를 못 읽고 "이미지 문제 없음"이나 "종료 기록 없음"으로 읽어 넘어가면 안 된다.
func TestRunDriverPreflight_PodListFails_ReturnsError(t *testing.T) {
	errList := errors.New("파드 목록 조회 실패(시험)")
	failPodList := &interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList,
		opts ...client.ListOption) error {
		if _, isPods := list.(*corev1.PodList); isPods {
			return errList
		}
		return c.List(ctx, list, opts...)
	}}
	for name, mutate := range map[string]func(*batchv1.Job){
		"진행 중인 Job": func(*batchv1.Job) {},
		"실패한 Job":   func(j *batchv1.Job) { markJob(j, false, true) },
	} {
		t.Run(name, func(t *testing.T) {
			sm, dus, dip := newStuckPreflightSM(mutate, failPodList)
			if _, _, err := sm.runDriverPreflight(context.Background(), dus, dip); !errors.Is(err, errList) ||
				!strings.Contains(err.Error(), "설치 전 점검 파드 조회 실패") {
				t.Errorf("err=%v, want 파드 조회 실패 오류(%v)", err, errList)
			}
			if preflightGone(t, sm) {
				t.Error("파드를 못 읽었는데 점검 Job 을 지웠다")
			}
		})
	}
}

// 점검 Job 이 이미 있는지 읽다 NotFound 가 아닌 오류로 실패하면 판정하지 않고 오류로 올린다. 첫 조회만 실패시킨다 —
// 그 오류를 삼키고 넘어가면 다음 조회가 성공해 점검이 그대로 이어지므로 오류가 올라오지 않는 것으로 가려낸다.
func TestHandlePreFlight_PreflightLookupFails_ReturnsError(t *testing.T) {
	errGet := errors.New("점검 Job 조회 실패(시험)")
	lookups := 0
	sm, dus, dip := newStuckPreflightSM(func(*batchv1.Job) {}, &interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object,
			opts ...client.GetOption) error {
			if _, isJob := obj.(*batchv1.Job); isJob {
				if lookups++; lookups == 1 {
					return errGet
				}
			}
			return c.Get(ctx, key, obj, opts...)
		}})
	_, _, err := sm.handlePreFlight(context.Background(), dus, dip)
	if !errors.Is(err, errGet) || !strings.Contains(err.Error(), "설치 전 점검 Job 조회 실패") {
		t.Errorf("err=%v, want 점검 Job 조회 실패 문맥이 붙은 오류(%v)", err, errGet)
	}
	if dus.Status.State != v1alpha1.UpgradeStatePreFlight || preflightGone(t, sm) {
		t.Errorf("state=%s gone=%v, want PreFlight·Job 유지", dus.Status.State, preflightGone(t, sm))
	}
}

func TestHandlePreFlight_ContainerCreating_KeepsWaiting(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	running := driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew)
	pod := makeWaitingPod(running, driverjob.PreflightContainerName, "ContainerCreating", "")
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), running, pod)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStatePreFlight || preflightGone(t, sm) {
		t.Errorf("state=%s gone=%v, want PreFlight 대기", dus.Status.State, preflightGone(t, sm))
	}
}

// 지우고 다시 만든 같은 이름 Job 의 옛 파드가 ImagePullBackOff 여도 새 Job 판정에 쓰지 않는다.
func TestHandlePreFlight_StalePodIgnored(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	running := driverjob.RenderPreflightJob(dip, jNode, imgNew, verNew)
	running.UID = "uid-new"
	old := makeWaitingPod(running, driverjob.PreflightContainerName, "ImagePullBackOff", "old")
	old.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: running.Name, UID: "uid-old"}},
		batchv1.SchemeGroupVersion.WithKind("Job"))}
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), running, old)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStatePreFlight {
		t.Errorf("state=%s, want PreFlight(옛 파드 무시)", dus.Status.State)
	}
}

// 이미지를 받지 못해 기다리는 사유만 판정 불가로 넘기고, 시작 중인 사유는 계속 기다린다.
func TestImagePullFailure(t *testing.T) {
	for reason, want := range map[string]bool{
		"ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true, "ErrImageNeverPull": true,
		"ContainerCreating": false, "PodInitializing": false, "CrashLoopBackOff": false, "": false,
	} {
		if got := imagePullFailure(reason); got != want {
			t.Errorf("imagePullFailure(%q)=%v, want %v", reason, got, want)
		}
	}
}

// Failed 조건의 사유가 비면 메시지만 쓰고, 둘 다 비면 다음 Failed 조건을 본다. 적힌 것이 없으면 "기록 없음" 이다.
func TestJobFailedReason(t *testing.T) {
	const none = "기록 없음"
	failedJob := func(status corev1.ConditionStatus, reason, msg string) *batchv1.Job {
		return &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Status: status, Reason: reason, Message: msg}}}}
	}
	emptyThenReason := failedJob(corev1.ConditionTrue, "", "")
	emptyThenReason.Status.Conditions = append(emptyThenReason.Status.Conditions,
		batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"})
	cases := []struct {
		name string
		job  *batchv1.Job
		want string
	}{
		{"reason+message", failedJob(corev1.ConditionTrue, "DeadlineExceeded", "over"), "DeadlineExceeded: over"},
		{"reason only", failedJob(corev1.ConditionTrue, "BackoffLimitExceeded", ""), "BackoffLimitExceeded"},
		{"reason empty", failedJob(corev1.ConditionTrue, "", "over"), "over"},
		{"both empty", failedJob(corev1.ConditionTrue, "", ""), none},
		{"both empty then next", emptyThenReason, "BackoffLimitExceeded"},
		{"condition false", failedJob(corev1.ConditionFalse, "DeadlineExceeded", "over"), none},
		{"no condition", &batchv1.Job{}, none},
	}
	for _, c := range cases {
		if got := jobFailedReason(c.job); got != c.want {
			t.Errorf("%s: jobFailedReason=%q, want %q", c.name, got, c.want)
		}
	}
}

func TestHandlePreFlight_StalePreflightJob_Recreated(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "", verNew)
	dip := makeAptJobDIP(verNew)
	stale := markJob(driverjob.RenderPreflightJob(dip, jNode, imgNew, verOld), true, false)
	sm := newUpgradeSMWithRecorder(dus, dip, readyNode(true), stale)
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("1차 handlePreFlight: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStatePreFlight || !preflightGone(t, sm) {
		t.Fatalf("state=%s gone=%v — 다른 버전의 지난 결과를 썼다", dus.Status.State, preflightGone(t, sm))
	}
	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("2차 handlePreFlight: %v", err)
	}
	var job batchv1.Job
	if err := sm.Get(context.Background(), preflightKey(), &job); err != nil || jobDriverVersion(&job) != verNew {
		t.Errorf("새 점검 Job: err=%v version=%q", err, jobDriverVersion(&job))
	}
}

// 현재 상태(State)와 직전 상태(LastTerminationState)의 종료 기록 중 어느 쪽이 더 최근이든 최근 기록을 고른다.
func TestJobTermination_PicksLatest(t *testing.T) {
	job := makeInstallJob(false, false)
	older := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	newer := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	for _, c := range []struct {
		name            string
		stateAt, lastAt metav1.Time
		wantExit        int32
		wantMessage     string
	}{
		{"현재 상태가 더 최근", newer, older, 1, "state"},
		{"직전 상태가 더 최근", older, newer, 3, "last"},
	} {
		pod := makeJobPod(job.Name, driverjob.InstallContainerName, 3, "Error", "last", true)
		pod.Status.ContainerStatuses[0].LastTerminationState.Terminated.FinishedAt = c.lastAt
		pod.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode: 1, Reason: "Error", Message: "state", FinishedAt: c.stateAt}}
		sm := newUpgradeSMWithRecorder(job, pod)
		got, err := sm.jobTermination(context.Background(), job, driverjob.InstallContainerName)
		if err != nil || !got.found || got.exitCode != c.wantExit || got.message != c.wantMessage {
			t.Errorf("%s: got=%+v err=%v, want 최근 종료(exit %d, %s)", c.name, got, err, c.wantExit, c.wantMessage)
		}
	}
	none, err := newUpgradeSMWithRecorder(job).jobTermination(context.Background(), job, driverjob.InstallContainerName)
	if err != nil || none.found {
		t.Errorf("파드 없음인데 found=%v err=%v", none.found, err)
	}
}

// 종료 메시지는 여러 줄일 수 있다(점검 Job 의 로그 꼬리 등). 상태 메시지·이벤트에 한 줄로 실리도록 공백을 접는다.
func TestJobTermination_FoldsMultilineMessage(t *testing.T) {
	job := makeInstallJob(false, false)
	pod := makeJobPod(job.Name, driverjob.InstallContainerName, 1, "Error", " 첫 줄\n\t둘째  줄\r\n셋째\n", false)
	got, err := newUpgradeSMWithRecorder(job, pod).jobTermination(context.Background(), job, driverjob.InstallContainerName)
	if err != nil || got.message != "첫 줄 둘째 줄 셋째" {
		t.Errorf("message=%q err=%v, want 한 줄로 접힌 %q", got.message, err, "첫 줄 둘째 줄 셋째")
	}
}

// 같은 이름으로 다시 만든 Job 의 종료 기록에는 옛 Job 의 파드가 섞이지 않는다. 지워지는 중인 옛 파드는
// 같은 job-name 라벨을 달고 있어도 다른 UID 의 Job 이 소유한다.
func TestJobTermination_SkipsPodsOfRecreatedJob(t *testing.T) {
	const container, oldMsg, newMsg = driverjob.InstallContainerName, "옛 Job 의 파드", "새 Job 의 파드"
	ctx := context.Background()
	jobKind := batchv1.SchemeGroupVersion.WithKind("Job")
	job := makeInstallJob(false, false)
	job.UID = "uid-new"
	oldJob := job.DeepCopy()
	oldJob.UID = "uid-old"
	recent := metav1.NewTime(time.Now())
	earlier := metav1.NewTime(time.Now().Add(-time.Minute))

	// 옛 파드는 종료 기록이 더 최근이고 종료 코드가 3 이다. 고르면 새 Job 의 hold 충돌로 오판한다.
	oldPod := makeJobPod(job.Name, container, driverjob.ExitCodeHostPackageConflict, "Error", oldMsg, true)
	oldPod.Name += "-old"
	oldPod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(oldJob, jobKind)}
	oldPod.Status.ContainerStatuses[0].LastTerminationState.Terminated.FinishedAt = recent
	newPod := makeJobPod(job.Name, container, 1, "Error", newMsg, true)
	newPod.Name += "-new"
	newPod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(job, jobKind)}
	newPod.Status.ContainerStatuses[0].LastTerminationState.Terminated.FinishedAt = earlier

	got, err := newUpgradeSMWithRecorder(oldPod, newPod).jobTermination(ctx, job, container)
	if err != nil || !got.found || got.exitCode != 1 || got.message != newMsg {
		t.Errorf("got=%+v err=%v, want 새 Job 소유 파드의 종료(exit 1, %s)", got, err, newMsg)
	}
	onlyOld, err := newUpgradeSMWithRecorder(oldPod).jobTermination(ctx, job, container)
	if err != nil || onlyOld.found {
		t.Errorf("옛 Job 의 파드뿐인데 found=%v exit=%d err=%v, want found=false", onlyOld.found, onlyOld.exitCode, err)
	}
}

// jobPodWaiting 은 그 Job 의 파드에서 이름이 같은 컨테이너가 기다리는 사유·메시지만 돌려준다.
func TestJobPodWaiting(t *testing.T) {
	const container, reason, message = driverjob.InstallContainerName, "ImagePullBackOff", "Back-off pulling image"
	ctx := context.Background()
	job := makeInstallJob(false, false)
	other := makeInstallJob(false, false)
	other.Name += "-other"
	own := newUpgradeSMWithRecorder(makeWaitingPod(job, container, reason, message))
	if r, m, err := own.jobPodWaiting(ctx, job, container); err != nil || r != reason || m != message {
		t.Errorf("reason=%q message=%q err=%v, want %q·%q", r, m, err, reason, message)
	}
	if r, m, err := own.jobPodWaiting(ctx, job, driverjob.PreflightContainerName); err != nil || r != "" || m != "" {
		t.Errorf("다른 컨테이너를 물었는데 reason=%q message=%q err=%v", r, m, err)
	}
	// 같은 네임스페이스의 다른 Job(다른 노드의 점검 Job 등)이 가진 파드는 이 Job 판정에 쓰지 않는다.
	others := newUpgradeSMWithRecorder(makeWaitingPod(other, container, reason, message))
	if r, m, err := others.jobPodWaiting(ctx, job, container); err != nil || r != "" || m != "" {
		t.Errorf("다른 Job 의 파드인데 reason=%q message=%q err=%v", r, m, err)
	}
}

// 대기 메시지도 여러 줄일 수 있다(pull 오류 문구 등). 이벤트에 한 줄로 실리도록 공백을 접는다.
func TestJobPodWaiting_FoldsMultilineMessage(t *testing.T) {
	job := makeInstallJob(false, false)
	const container, folded = driverjob.InstallContainerName, "rpc error: code = NotFound desc = 못 받음 실패"
	pod := makeWaitingPod(job, container, "ErrImagePull", " rpc error:\n\tcode = NotFound  desc = 못 받음\r\n실패\n")
	reason, message, err := newUpgradeSMWithRecorder(pod).jobPodWaiting(context.Background(), job, container)
	if err != nil || reason != "ErrImagePull" || message != folded {
		t.Errorf("reason=%q message=%q err=%v, want 한 줄로 접힌 %q", reason, message, err, folded)
	}
}
