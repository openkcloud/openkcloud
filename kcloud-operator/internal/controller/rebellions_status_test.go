// ============================================================
// rebellions_status_test.go: RebellionsDevicePluginReady 조건 보고 시험
// 상세: 파드 상태에서 노드별 사유를 모으는 규칙(직전 종료, 노드 중복 제거, CrashLoopBackOff, 종료 기록 없이
//       시작하지 못하는 컨테이너와 정상 기동 대기의 구분), 조건의 사유 순서·메시지, 조건이 바뀔 때만 이벤트,
//       조회 실패 시 조건 유지, 비활성 시 제거를 고정한다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
)

// rbllnsTestPod 는 노드에 놓인 플러그인 파드다. prep·plugin 은 각 컨테이너의 상태다.
func rbllnsTestPod(name, node string, prep corev1.ContainerStatus, plugin *corev1.ContainerStatus) *corev1.Pod {
	prep.Name = rbllnsPrepContainerName
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: naming.KubeSystemNamespace,
			Labels: map[string]string{"app.kubernetes.io/name": rbllnsDaemonSetName}},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{prep}},
	}
	if plugin != nil {
		s := *plugin
		s.Name = rbllnsDaemonSetName
		p.Status.ContainerStatuses = []corev1.ContainerStatus{s}
	}
	return p
}

func rbllnsExited(code int32) corev1.ContainerStatus {
	return corev1.ContainerStatus{State: corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{ExitCode: code}}}
}

func rbllnsBackoff(lastCode int32) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: lastCode}},
	}
}

// rbllnsWaiting 은 종료 기록(LastTerminationState) 없이 reason 으로 기다리는 컨테이너 상태다.
func rbllnsWaiting(reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}}
}

func TestRbllnsPodFailures(t *testing.T) {
	running := corev1.ContainerStatus{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
	crash := corev1.ContainerStatus{State: corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}
	pullErr := rbllnsWaiting("ErrImagePull")
	initializing := rbllnsWaiting("PodInitializing")
	pods := []corev1.Pod{
		*rbllnsTestPod("a", "worker4", rbllnsExited(rbllnsPrepExitNoSmi), nil),
		*rbllnsTestPod("a2", "worker4", rbllnsBackoff(rbllnsPrepExitNoSmi), nil), // 롤링 중 같은 노드의 두 번째 파드
		*rbllnsTestPod("b", "worker5", rbllnsBackoff(rbllnsPrepExitNoDevice), nil),
		*rbllnsTestPod("c", "worker6", rbllnsExited(0), &crash),
		*rbllnsTestPod("d", "worker7", running, nil),
		*rbllnsTestPod("e", "worker8", rbllnsExited(0), &running),
		*rbllnsTestPod("f", "worker9", rbllnsWaiting("ImagePullBackOff"), nil),             // 사전 점검 이미지를 못 받아 종료 기록이 없다
		*rbllnsTestPod("g", "worker10", rbllnsExited(0), &pullErr),                         // 플러그인 이미지를 못 받는다
		*rbllnsTestPod("h", "worker11", running, &initializing),                            // 사전 점검이 도는 동안 플러그인은 정상 대기
		*rbllnsTestPod("i", "worker12", rbllnsWaiting("ContainerCreating"), &initializing), // 사전 점검 컨테이너를 만드는 중
	}
	want := map[string]map[string]struct{}{
		rbllnsReasonNoSmi:            {"worker4": {}},
		rbllnsReasonNoDevice:         {"worker5": {}},
		rbllnsReasonPluginCrashLoop:  {"worker6": {}},
		rbllnsReasonPrepFailed:       {"worker9": {}},
		rbllnsReasonPluginNotStarted: {"worker10": {}},
	}
	if got := rbllnsPodFailures(pods); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRebellionsPluginCondition(t *testing.T) {
	const note = "v0.3 형식(태그 v0.3.6)"
	ok := rebellionsPluginCondition(note, false, nil, 7)
	if ok.Status != metav1.ConditionTrue || ok.Reason != rbllnsReasonPrepared || ok.Message != "판: "+note ||
		ok.ObservedGeneration != 7 || ok.Type != npuv1alpha1.ConditionRebellionsDevicePluginReady {
		t.Errorf("정상 조건 %+v", ok)
	}
	failures := map[string]map[string]struct{}{
		rbllnsReasonPluginCrashLoop: {"w9": {}},
		rbllnsReasonNoSmi:           {"w2": {}, "w1": {}},
	}
	bad := rebellionsPluginCondition(note, false, failures, 7)
	if bad.Status != metav1.ConditionFalse || bad.Reason != rbllnsReasonNoSmi ||
		bad.Message != "판: v0.3 형식(태그 v0.3.6); RblnSmiMissing: w1, w2; PluginCrashLoop: w9" {
		t.Errorf("실패 조건 %+v", bad)
	}
	if unset := rebellionsPluginCondition(note, true, failures, 7); unset.Reason != rbllnsReasonHostExecUnset {
		t.Errorf("HOST_EXEC_IMAGE 미설정이 첫 사유가 아니다: %s", unset.Reason)
	}
	// 시작하지 못한 플러그인(PluginNotStarted)은 PluginCrashLoop 뒤에 적는다.
	late := rebellionsPluginCondition(note, false, map[string]map[string]struct{}{
		rbllnsReasonPluginNotStarted: {"w8": {}},
		rbllnsReasonPluginCrashLoop:  {"w9": {}},
	}, 7)
	if late.Status != metav1.ConditionFalse || late.Reason != rbllnsReasonPluginCrashLoop ||
		late.Message != "판: v0.3 형식(태그 v0.3.6); PluginCrashLoop: w9; PluginNotStarted: w8" {
		t.Errorf("PluginNotStarted 가 PluginCrashLoop 뒤에 오지 않는다: %+v", late)
	}
}

func rbllnsStatusPolicy() *npuv1alpha1.NPUClusterPolicy {
	p := rbllnsTestPolicy(rbllnsTestImageV03)
	p.Name, p.Namespace = "rbln-status", "default"
	return p
}

func rbllnsEventCount(rec *record.FakeRecorder) int {
	n := 0
	for {
		select {
		case <-rec.Events:
			n++
		default:
			return n
		}
	}
}

func TestReportRebellionsDevicePlugin(t *testing.T) {
	t.Setenv(HostExecImageEnv, rbllnsTestHostExec)
	ctx := context.Background()
	p := rbllnsStatusPolicy()
	rec := record.NewFakeRecorder(10)
	r := newRbllnsTestReconciler()
	r.Recorder = rec

	r.reportRebellionsDevicePlugin(ctx, p, true)
	c := apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionRebellionsDevicePluginReady)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("파드가 없으면 True 여야 한다: %+v", c)
	}
	if c.Message != "판: v0.3 형식(태그 v0.3.6)" {
		t.Errorf("조건 메시지에 판과 메모가 없다: %q", c.Message)
	}
	if n := rbllnsEventCount(rec); n != 1 {
		t.Errorf("처음 세팅 이벤트 %d 개, want 1", n)
	}
	r.reportRebellionsDevicePlugin(ctx, p, true)
	if n := rbllnsEventCount(rec); n != 0 {
		t.Errorf("조건이 같은데 이벤트 %d 개", n)
	}

	if err := r.Create(ctx, rbllnsTestPod("a", "worker4", rbllnsExited(rbllnsPrepExitNoSmi), nil)); err != nil {
		t.Fatal(err)
	}
	r.reportRebellionsDevicePlugin(ctx, p, true)
	c = apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionRebellionsDevicePluginReady)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != rbllnsReasonNoSmi {
		t.Errorf("실패 파드가 반영되지 않았다: %+v", c)
	}
	if n := rbllnsEventCount(rec); n != 1 {
		t.Errorf("전이 이벤트 %d 개, want 1", n)
	}

	r.reportRebellionsDevicePlugin(ctx, p, false)
	if apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionRebellionsDevicePluginReady) != nil {
		t.Error("비활성인데 조건이 남았다")
	}
}

// 파드 조회가 실패하면 조건을 바꾸지 않는다 — 조회 오류로 True 가 되면 안 된다.
func TestReportRebellionsDevicePluginKeepsConditionOnListError(t *testing.T) {
	p := rbllnsStatusPolicy()
	apimeta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
		Type: npuv1alpha1.ConditionRebellionsDevicePluginReady, Status: metav1.ConditionFalse,
		Reason: rbllnsReasonNoSmi, Message: "이전 값"})
	s := sharingScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errors.New("injected list failure")
		},
	}).Build()
	rec := record.NewFakeRecorder(10)
	r := &NPUClusterPolicyReconciler{Client: c, Scheme: s, Recorder: rec}
	r.reportRebellionsDevicePlugin(context.Background(), p, true)
	got := apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionRebellionsDevicePluginReady)
	if got == nil || got.Status != metav1.ConditionFalse || got.Message != "이전 값" {
		t.Errorf("조회 실패인데 조건이 바뀌었다: %+v", got)
	}
	if n := rbllnsEventCount(rec); n != 0 {
		t.Errorf("조회 실패인데 이벤트 %d 개", n)
	}
}
