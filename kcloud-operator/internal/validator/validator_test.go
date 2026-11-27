// ============================================================
// validator_test.go: Validator 단위 테스트 (fake client 기반)
// 상세: DriverModuleValidator / DevicePluginValidator 의 PASS/FAIL 케이스 검증.
//       envtest 미사용 — controller-runtime fake client 만 사용.
// 생성일: 2026-04-27 | 수정일: 2026-10-13
// ============================================================

package validator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// newScheme 은 corev1 + npu.ai/v1alpha1 이 등록된 runtime.Scheme 을 반환한다.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("clientgoscheme 등록 실패: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("v1alpha1 등록 실패: %v", err)
	}
	return s
}

// makeNDR 는 단일 device 항목을 가진 NodeDeviceReport 를 만든다.
func makeNDR(nodeName, driverVersion string) *v1alpha1.NodeDeviceReport {
	return &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: nodeName},
		Status: v1alpha1.NodeDeviceReportStatus{
			Devices: []v1alpha1.DeviceEntry{
				{
					Vendor:        "nvidia",
					DriverLoaded:  true,
					DriverVersion: driverVersion,
				},
			},
		},
	}
}

// makeDPPod 는 device-plugin Pod 를 만든다. ready=true 면 ContainersReady condition 부착.
func makeDPPod(name, nodeName, vendor string, ready bool) *corev1.Pod {
	cond := corev1.ConditionFalse
	if ready {
		cond = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels: map[string]string{
				"app.kubernetes.io/name": vendor + "-device-plugin",
			},
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.ContainersReady, Status: cond},
			},
		},
	}
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objs...).
		Build()
}

// TestDriverModuleValidator_PassWhenNDRMatches 는 NDR 의 driverVersion 이
// desiredVersion 과 일치할 때 PASS 를 반환하는지 확인한다.
func TestDriverModuleValidator_PassWhenNDRMatches(t *testing.T) {
	const (
		nodeName = "worker-1"
		vendor   = "nvidia"
		desired  = "575.64.03"
	)
	ndr := makeNDR(nodeName, desired)
	c := newFakeClient(t, ndr)

	v := &DriverModuleValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if !res.Passed {
		t.Fatalf("PASS 기대, 실제 FAIL: %s", res.Message)
	}
}

// TestDriverModuleValidator_FailWhenNDRStale 는 NDR 의 driverVersion 이
// 이전 버전이라 desiredVersion 과 다를 때 Passed=false 를 반환하는지 확인한다.
func TestDriverModuleValidator_FailWhenNDRStale(t *testing.T) {
	const (
		nodeName = "worker-1"
		vendor   = "nvidia"
		desired  = "575.64.03"
		stale    = "550.54.15"
	)
	ndr := makeNDR(nodeName, stale)
	c := newFakeClient(t, ndr)

	v := &DriverModuleValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if res.Passed {
		t.Fatalf("FAIL 기대, 실제 PASS: %s", res.Message)
	}
}

// TestDriverModuleValidator_FailWhenNDRMissing 은 NDR 자체가 없을 때
// error 가 아닌 Passed=false 를 반환하는지 확인 (caller 가 재시도).
func TestDriverModuleValidator_FailWhenNDRMissing(t *testing.T) {
	c := newFakeClient(t)
	v := &DriverModuleValidator{}
	res, err := v.Run(context.Background(), c, "worker-missing", "nvidia", "575.64.03")
	if err != nil {
		t.Fatalf("NDR NotFound 는 error 가 아니어야 함: %v", err)
	}
	if res.Passed {
		t.Fatalf("FAIL 기대, 실제 PASS: %s", res.Message)
	}
}

// TestDevicePluginValidator_PassWhenPodReady 는 device-plugin Pod 가
// ContainersReady=True 일 때 PASS 를 반환하는지 확인한다.
func TestDevicePluginValidator_PassWhenPodReady(t *testing.T) {
	const (
		nodeName = "worker-1"
		vendor   = "nvidia"
	)
	pod := makeDPPod("nvidia-device-plugin-abc", nodeName, vendor, true)
	c := newFakeClient(t, pod)

	v := &DevicePluginValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, "")
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if !res.Passed {
		t.Fatalf("PASS 기대, 실제 FAIL: %s", res.Message)
	}
}

// TestDevicePluginValidator_FailWhenPodPending 은 device-plugin Pod 가
// 아직 ContainersReady 가 아닐 때 Passed=false 를 반환하는지 확인한다.
func TestDevicePluginValidator_FailWhenPodPending(t *testing.T) {
	const (
		nodeName = "worker-1"
		vendor   = "nvidia"
	)
	pod := makeDPPod("nvidia-device-plugin-abc", nodeName, vendor, false)
	c := newFakeClient(t, pod)

	v := &DevicePluginValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, "")
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if res.Passed {
		t.Fatalf("FAIL 기대, 실제 PASS: %s", res.Message)
	}
}

// TestDevicePluginValidator_FailWhenPodMissing 은 device-plugin Pod 가
// 노드에 스케줄되지 않은 상태에서 Passed=false 를 반환하는지 확인한다.
func TestDevicePluginValidator_FailWhenPodMissing(t *testing.T) {
	c := newFakeClient(t)
	v := &DevicePluginValidator{}
	res, err := v.Run(context.Background(), c, "worker-1", "nvidia", "")
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if res.Passed {
		t.Fatalf("FAIL 기대, 실제 PASS: %s", res.Message)
	}
}

// TestDevicePluginValidator_PassWhenTTPodInKcloudNS 는 TT device-plugin 이
// kube-system 이 아닌 kcloud 네임스페이스에 "kcloud-tt-device-plugin" 이름으로
// 상주해도 검증이 통과하는지 확인한다. namespace 하드코딩 제거 + "tt" vendor
// 별칭 회귀 고정.
func TestDevicePluginValidator_PassWhenTTPodInKcloudNS(t *testing.T) {
	const nodeName = "k8s-worker3"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kcloud-tt-device-plugin-sn9gr",
			Namespace: "kcloud",
			Labels:    map[string]string{"app.kubernetes.io/name": "kcloud-tt-device-plugin"},
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
			},
		},
	}
	c := newFakeClient(t, pod)
	v := &DevicePluginValidator{}
	res, err := v.Run(context.Background(), c, nodeName, "tenstorrent", "")
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if !res.Passed {
		t.Fatalf("PASS 기대(TT/kcloud), 실제 FAIL: %s", res.Message)
	}
}

// TestWorkloadValidator_CreatesProbeThenPassesOnSucceeded 는 첫 Run 이 probe 파드를
// 노드에 고정해 생성하고 대기(Passed=false), 파드가 Succeeded 가 되면 다음 Run 이 통과하고
// 파드를 지우는 것을 검증한다.
func TestWorkloadValidator_CreatesProbeThenPassesOnSucceeded(t *testing.T) {
	c := newFakeClient(t)
	v := &WorkloadValidator{Namespace: "kcloud", Probe: v1alpha1.WorkloadProbeSpec{Enabled: true, Image: "img", Command: []string{"nvidia-smi"}, ResourceName: "nvidia.com/gpu", TimeoutSeconds: 120}}
	res, err := v.Run(context.Background(), c, "worker-1", "nvidia", "580.126.09")
	if err != nil || res.Passed {
		t.Fatalf("첫 호출은 probe 를 만들고 대기해야 한다: res=%+v err=%v", res, err)
	}
	var pods corev1.PodList
	_ = c.List(context.Background(), &pods, client.InNamespace("kcloud"))
	if len(pods.Items) != 1 || pods.Items[0].Spec.NodeName != "worker-1" {
		t.Fatalf("probe 파드가 노드에 고정돼 만들어져야 한다: %+v", pods.Items)
	}
	if q := pods.Items[0].Spec.Containers[0].Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]; q.Value() != 1 {
		t.Fatalf("장치 1개를 요청해야 한다: %v", pods.Items[0].Spec.Containers[0].Resources.Limits)
	}
	pods.Items[0].Status.Phase = corev1.PodSucceeded
	_ = c.Status().Update(context.Background(), &pods.Items[0])
	res, err = v.Run(context.Background(), c, "worker-1", "nvidia", "580.126.09")
	if err != nil || !res.Passed {
		t.Fatalf("Succeeded 면 통과: res=%+v err=%v", res, err)
	}
	_ = c.List(context.Background(), &pods, client.InNamespace("kcloud"))
	if len(pods.Items) != 0 {
		t.Fatal("통과 뒤 probe 파드를 지워야 한다")
	}
}

// TestWorkloadValidator_FailedProbeDoesNotPass 는 probe 파드가 Failed 면 통과가 아니고,
// 그 파드를 지워 다음 Run 이 새로 만들 수 있게 하는 것을 검증한다.
func TestWorkloadValidator_FailedProbeDoesNotPass(t *testing.T) {
	const desired = "580.126.09"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "kcloud", Name: probePodName("nvidia", "worker-1", desired),
		Labels: map[string]string{probeVersionLabel: sanitizeLabelValue(desired)}},
		Status: corev1.PodStatus{Phase: corev1.PodFailed}}
	c := newFakeClient(t, pod)
	v := &WorkloadValidator{Namespace: "kcloud", Probe: v1alpha1.WorkloadProbeSpec{Enabled: true, Image: "img", ResourceName: "nvidia.com/gpu"}}
	res, err := v.Run(context.Background(), c, "worker-1", "nvidia", desired)
	if err != nil || res.Passed || !strings.Contains(res.Message, "probe 실패") {
		t.Fatalf("Failed 는 통과가 아니다: res=%+v err=%v", res, err)
	}
	var pods corev1.PodList
	_ = c.List(context.Background(), &pods, client.InNamespace("kcloud"))
	if len(pods.Items) != 0 {
		t.Fatal("실패한 probe 파드는 지워야 다음 Run 이 새로 만든다")
	}
}

// TestWorkloadValidator_PendingPastTimeoutDoesNotPass 는 Pending 파드가 TimeoutSeconds 를
// 넘기면 통과가 아니고, 그 파드를 지우는 것을 검증한다.
func TestWorkloadValidator_PendingPastTimeoutDoesNotPass(t *testing.T) {
	const (
		nodeName, vendor, desired = "worker-1", "nvidia", "580.126.09"
	)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "kcloud", Name: probePodName(vendor, nodeName, desired),
		Labels:            map[string]string{probeVersionLabel: sanitizeLabelValue(desired)},
		CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
	}, Status: corev1.PodStatus{Phase: corev1.PodPending}}
	c := newFakeClient(t, pod)
	v := &WorkloadValidator{Namespace: "kcloud", Probe: v1alpha1.WorkloadProbeSpec{Enabled: true, Image: "img", ResourceName: "nvidia.com/gpu", TimeoutSeconds: 10}}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil || res.Passed || !strings.Contains(res.Message, "제한 시간") {
		t.Fatalf("제한 시간 초과는 통과가 아니다: res=%+v err=%v", res, err)
	}
	var pods corev1.PodList
	_ = c.List(context.Background(), &pods, client.InNamespace("kcloud"))
	if len(pods.Items) != 0 {
		t.Fatal("제한 시간 초과 probe 파드는 지워야 한다")
	}
}

// TestWorkloadValidator_StaleVersionProbeIsReplaced 는 같은 이름의 파드가 다른
// desiredVersion 라벨을 달고 있으면(해시 충돌·수동 생성 등 방어적 케이스) 통과로 보지 않고
// 지우는 것을 검증한다.
func TestWorkloadValidator_StaleVersionProbeIsReplaced(t *testing.T) {
	const (
		nodeName, vendor, desired = "worker-1", "nvidia", "580.126.09"
	)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "kcloud", Name: probePodName(vendor, nodeName, desired),
		Labels: map[string]string{probeVersionLabel: sanitizeLabelValue("575.64.03")}, // 다른 버전
	}, Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	c := newFakeClient(t, pod)
	v := &WorkloadValidator{Namespace: "kcloud", Probe: v1alpha1.WorkloadProbeSpec{Enabled: true, Image: "img", ResourceName: "nvidia.com/gpu"}}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil || res.Passed {
		t.Fatalf("라벨 버전이 다르면 통과가 아니다: res=%+v err=%v", res, err)
	}
	var pods corev1.PodList
	_ = c.List(context.Background(), &pods, client.InNamespace("kcloud"))
	if len(pods.Items) != 0 {
		t.Fatal("다른 버전의 probe 파드는 지워야 한다")
	}
}

// TestWorkloadValidator_DeleteFailureDoesNotPass 는 Succeeded 파드의 삭제(cleanup)가
// 실패하면 통과시키지 않는 것을 검증한다 — 지워지지 않는 파드를 "통과" 로 잘못 보고하지 않기 위함.
func TestWorkloadValidator_DeleteFailureDoesNotPass(t *testing.T) {
	const (
		nodeName, vendor, desired = "worker-1", "nvidia", "580.126.09"
	)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "kcloud", Name: probePodName(vendor, nodeName, desired),
		Labels: map[string]string{probeVersionLabel: sanitizeLabelValue(desired)},
	}, Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(pod).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return errors.New("simulated delete failure")
			},
		}).Build()
	v := &WorkloadValidator{Namespace: "kcloud", Probe: v1alpha1.WorkloadProbeSpec{Enabled: true, Image: "img", ResourceName: "nvidia.com/gpu"}}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil || res.Passed || !strings.Contains(res.Message, "정리 실패") {
		t.Fatalf("정리 실패 시 통과하지 않아야 한다: res=%+v err=%v", res, err)
	}
}

// TestWorkloadValidator_EmptyImageDoesNotPass 는 workloadProbe.image 가 비면 파드를 만들지 않고
// 사람이 읽을 수 있는 사유로 비통과하는지 고정한다. 가드가 없으면 c.Create 가 거절되고 Run 이
// 오류를 반환해 handleValidating 이 reconcile 오류로 올린다 — 사유가 DUS·Event 가 아니라 로그에만
// 남고 제한시간까지 오류 재시도가 돈다. 차트 기본값이 `enabled:false, image:""` 라 켜기만 하고
// 이미지를 안 넣는 실수가 바로 이 경로다. ResourceName 빈 값과 같은 형식으로 끝낸다.
func TestWorkloadValidator_EmptyImageDoesNotPass(t *testing.T) {
	c := newFakeClient(t)
	v := &WorkloadValidator{Namespace: "kcloud", Probe: v1alpha1.WorkloadProbeSpec{
		Enabled: true, ResourceName: "nvidia.com/gpu"}}
	res, err := v.Run(context.Background(), c, "worker-1", "nvidia", "580.126.09")
	if err != nil {
		t.Fatalf("빈 image 는 오류가 아니라 비통과여야 한다: %v", err)
	}
	if res.Passed || !strings.Contains(res.Message, "workloadProbe.image") {
		t.Fatalf("사유에 빈 필드 이름이 있어야 한다: %+v", res)
	}
	var pods corev1.PodList
	_ = c.List(context.Background(), &pods, client.InNamespace("kcloud"))
	if len(pods.Items) != 0 {
		t.Fatalf("파드를 만들지 않아야 한다: %+v", pods.Items)
	}
}

// TestWorkloadValidator_ProbePodHasActiveDeadline 은 생성되는 probe 파드가
// activeDeadlineSeconds 를 Timeout() 초로 들고 나오는지 고정한다. 이 필드가 없으면
// 검증 사이클이 Workload 단계 앞에서 끊겨 Run 이 다시 불리지 않을 때 Pending probe 파드가
// 영구히 남아 internal/partition/quiesce.go 의 holdsDevice 가 그 노드를 장치 점유 상태로
// 계속 보고한다(ACPP apply·CapReset quiesce 가 사람이 지울 때까지 막힌다).
// TimeoutSeconds 미지정 기본값(120s)도 같은 식으로 실린다.
func TestWorkloadValidator_ProbePodHasActiveDeadline(t *testing.T) {
	for _, tc := range []struct {
		name           string
		timeoutSeconds int32
		wantDeadline   int64
	}{
		{name: "명시한 TimeoutSeconds", timeoutSeconds: 45, wantDeadline: 45},
		{name: "미지정 기본값", timeoutSeconds: 0, wantDeadline: 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeClient(t)
			v := &WorkloadValidator{Namespace: "kcloud", Probe: v1alpha1.WorkloadProbeSpec{
				Enabled: true, Image: "img", ResourceName: "nvidia.com/gpu", TimeoutSeconds: tc.timeoutSeconds}}
			if _, err := v.Run(context.Background(), c, "worker-1", "nvidia", "580.126.09"); err != nil {
				t.Fatalf("예상치 못한 에러: %v", err)
			}
			var pods corev1.PodList
			_ = c.List(context.Background(), &pods, client.InNamespace("kcloud"))
			if len(pods.Items) != 1 {
				t.Fatalf("probe 파드 1개 기대, 실제 %d", len(pods.Items))
			}
			got := pods.Items[0].Spec.ActiveDeadlineSeconds
			if got == nil {
				t.Fatal("probe 파드에 activeDeadlineSeconds 가 없다 — Pending 파드가 영구 잔존한다")
			}
			if *got != tc.wantDeadline {
				t.Fatalf("activeDeadlineSeconds %d 기대, 실제 %d", tc.wantDeadline, *got)
			}
		})
	}
}

// TestDriverModule_HostMismatch_ActionableMessage 는 host kernel module 이
// desiredVersion 과 다를 때 fail 메시지에 host/desired 버전과 actionable 조치
// (ssh + rmmod) 가 포함되는지 검증한다.
func TestDriverModule_HostMismatch_ActionableMessage(t *testing.T) {
	const (
		nodeName = "k8s-worker1"
		vendor   = "nvidia"
		hostVer  = "595.58.03"
		desired  = "590.48.01"
	)
	ndr := makeNDR(nodeName, hostVer)
	c := newFakeClient(t, ndr)

	v := &DriverModuleValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if res.Passed {
		t.Fatalf("FAIL 기대, 실제 PASS: %s", res.Message)
	}
	for _, want := range []string{hostVer, desired, "rmmod", "ssh", nodeName} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("Message 에 %q 포함 기대, 실제: %s", want, res.Message)
		}
	}
}

// TestDriverModule_NeedsReboot_NoRmmodGuidance 는 NDR 이 needsReboot 을 보고할 때
// rmmod 안내를 내지 않는 것을 고정한다. 모듈 교체(계열 전환·같은 계열 patch 교체)는 목표
// 버전 모듈이 이미 디스크에 준비됐고 구 모듈이 사용 중이라 삽입만 못 하는 상태이며, rmmod 로는
// 풀리지 않는다 — 그 지시를 따르는 사람은 시간만 버린다.
func TestDriverModule_NeedsReboot_NoRmmodGuidance(t *testing.T) {
	const (
		nodeName = "k8s-worker1"
		vendor   = "nvidia"
		hostVer  = "595.84"
		desired  = "580.173.02"
	)
	ndr := makeNDR(nodeName, hostVer)
	ndr.Status.Devices[0].NeedsReboot = true
	c := newFakeClient(t, ndr)

	v := &DriverModuleValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if res.Passed {
		t.Fatalf("FAIL 기대, 실제 PASS: %s", res.Message)
	}
	// "rmmod 로는 풀리지 않는다" 는 남아도 된다 — 손대려는 사람을 막는 정보다.
	// 없어야 하는 것은 그것을 실행하라는 지시다.
	for _, forbidden := range []string{"sudo rmmod", "ssh"} {
		if strings.Contains(res.Message, forbidden) {
			t.Errorf("재부팅 대기 상태에 %q 조치 지시가 남아 있음: %s", forbidden, res.Message)
		}
	}
	for _, want := range []string{hostVer, desired, "재부팅"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("Message 에 %q 포함 기대, 실제: %s", want, res.Message)
		}
	}
}

// TestDriverModule_HostMatch_Pass 는 host kernel module 이 desiredVersion 과
// 일치할 때 PASS 를 반환하는지 검증한다.
func TestDriverModule_HostMatch_Pass(t *testing.T) {
	const (
		nodeName = "k8s-worker1"
		vendor   = "nvidia"
		desired  = "590.48.01"
	)
	ndr := makeNDR(nodeName, desired)
	c := newFakeClient(t, ndr)

	v := &DriverModuleValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if !res.Passed {
		t.Fatalf("PASS 기대, 실제 FAIL: %s", res.Message)
	}
}

// TestDriverModule_NDRNotReported_GenericMessage 는 NDR 의 driverVersion 이
// 비어 있거나 vendor 가 매칭되지 않을 때 generic "미보고" 메시지를 반환하는지 확인.
//   - host 정보가 없으므로 actionable rmmod 메시지를 출력하지 않는다.
func TestDriverModule_NDRNotReported_GenericMessage(t *testing.T) {
	const (
		nodeName = "k8s-worker1"
		vendor   = "nvidia"
		desired  = "590.48.01"
	)
	// driverVersion="" 이고 DriverLoaded=true 라도 hostVer 캡처는 ""이라 generic 분기로 떨어짐.
	ndr := makeNDR(nodeName, "")
	c := newFakeClient(t, ndr)

	v := &DriverModuleValidator{}
	res, err := v.Run(context.Background(), c, nodeName, vendor, desired)
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if res.Passed {
		t.Fatalf("FAIL 기대, 실제 PASS: %s", res.Message)
	}
	if res.Message != "NDR.driverVersion 미보고" {
		t.Errorf("generic 미보고 메시지 기대, 실제: %s", res.Message)
	}
	if strings.Contains(res.Message, "rmmod") || strings.Contains(res.Message, "ssh") {
		t.Errorf("host 정보가 없을 때 actionable 조치는 포함되지 않아야 함, 실제: %s", res.Message)
	}
}

// TestValidator_ContextCancelled 는 ctx cancel 시 즉시 error 를 반환하는지 확인.
func TestValidator_ContextCancelled(t *testing.T) {
	c := newFakeClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, v := range []Validator{
		&DriverModuleValidator{},
		&DevicePluginValidator{},
		&WorkloadValidator{},
	} {
		if _, err := v.Run(ctx, c, "worker-1", "nvidia", "575.64.03"); err == nil {
			t.Errorf("%s: ctx cancel 시 error 기대", v.Name())
		}
	}
}
