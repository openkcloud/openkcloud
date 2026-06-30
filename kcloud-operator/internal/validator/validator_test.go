// ============================================================
// validator_test.go: Validator 단위 테스트 (fake client 기반)
// 상세: DriverModuleValidator / DevicePluginValidator 의 PASS/FAIL 케이스 검증.
//       envtest 미사용 — controller-runtime fake client 만 사용.
// 생성일: 2026-04-27 | 수정일: 2026-04-28
// ============================================================

package validator

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

// TestWorkloadValidator_SkeletonAlwaysPass 는 skeleton 구현이 항상 PASS 를
// 반환하는지 확인 (후속 PR 에서 실제 워크로드 spawn).
func TestWorkloadValidator_SkeletonAlwaysPass(t *testing.T) {
	v := &WorkloadValidator{}
	res, err := v.Run(context.Background(), nil, "worker-1", "nvidia", "575.64.03")
	if err != nil {
		t.Fatalf("예상치 못한 에러: %v", err)
	}
	if !res.Passed {
		t.Fatalf("skeleton 은 항상 PASS 여야 함: %s", res.Message)
	}
}

// TestDriverModule_HostMismatch_ActionableMessage 는 host kernel module 이
// desiredVersion 과 다를 때 fail 메시지에 host/desired 버전과 actionable 조치
// (ssh + rmmod) 가 포함되는지 검증한다 (R3 fix, plan 2026-04-28).
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
