// ============================================================
// acceleratorworkload_test.go: AcceleratorWorkload validator 테스트
// 상세: 클래스 미존재 거절, 지원 조합 통과, RNGD shared 거절, 공유 승인 시 경고 반환.
// 생성일: 2026-07-30 | 수정일: 2026-07-30
// ============================================================
package webhook

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1 scheme: %v", err)
	}
	if err := npuv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("v1alpha1 scheme: %v", err)
	}
	return s
}

func gpuNode(count string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Status:     corev1.NodeStatus{Allocatable: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse(count)}},
	}
}

func nvidiaClass() *npuv1alpha1.AcceleratorClass {
	return &npuv1alpha1.AcceleratorClass{
		ObjectMeta: metav1.ObjectMeta{Name: "inference-medium"},
		Spec: npuv1alpha1.AcceleratorClassSpec{
			Mappings: []npuv1alpha1.AcceleratorMapping{{Vendor: "nvidia", NativeProfile: "1g.6gb"}},
		},
	}
}

func workload(mode string, replicas int32) *npuv1alpha1.AcceleratorWorkload {
	return &npuv1alpha1.AcceleratorWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "img", Namespace: "default"},
		Spec: npuv1alpha1.AcceleratorWorkloadSpec{
			Accelerator: npuv1alpha1.AcceleratorRequest{
				Class:  "inference-medium",
				Access: npuv1alpha1.AccessSpec{Mode: mode, Implementation: npuv1alpha1.ImplementationAuto, Replicas: replicas},
			},
			Workload: npuv1alpha1.WorkloadTemplate{Image: "registry.example.com:5000/kcloud/inference:v1"},
		},
	}
}

func build(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
}

func TestValidateAcceleratorWorkloadExclusivePasses(t *testing.T) {
	c := build(t, nvidiaClass(), gpuNode("2"))
	w, err := validateAcceleratorWorkload(context.Background(), c, workload(npuv1alpha1.AccessModeExclusive, 0))
	if err != nil {
		t.Fatalf("exclusive rejected: %v", err)
	}
	if len(w) != 0 {
		t.Fatalf("unexpected warnings %v", w)
	}
}

func TestValidateAcceleratorWorkloadMissingClass(t *testing.T) {
	c := build(t, gpuNode("2"))
	if _, err := validateAcceleratorWorkload(context.Background(), c, workload(npuv1alpha1.AccessModeExclusive, 0)); err == nil {
		t.Fatal("missing class accepted")
	} else if !strings.Contains(err.Error(), "inference-medium") {
		t.Fatalf("error must name the class: %v", err)
	}
}

func TestValidateAcceleratorWorkloadRejectsUnverifiedSharing(t *testing.T) {
	c := build(t, nvidiaClass(), gpuNode("2"))
	_, err := validateAcceleratorWorkload(context.Background(), c, workload(npuv1alpha1.AccessModeShared, 4))
	if err == nil {
		t.Fatal("shared accepted without any device capability")
	}
	if !strings.Contains(err.Error(), "axis:") {
		t.Fatalf("rejection must name the axis: %v", err)
	}
}

// 공유가 실제로 적용·검증된 노드에서는 통과하되, 격리가 없다는 경고를 함께 돌려준다.
func TestValidateAcceleratorWorkloadSharedWarns(t *testing.T) {
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ts", Generation: 1},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 1,
			Targets: []npuv1alpha1.TargetStatus{{
				NodeName: "worker1", Phase: npuv1alpha1.ACPPPhaseReady,
				Devices: []npuv1alpha1.DeviceStatus{{ID: "d0", SharingCapability: npuv1alpha1.SharingCapability{
					TimeSlicing: npuv1alpha1.SharingModeSupport{Supported: true, Verification: npuv1alpha1.VerificationVerified, MaxReplicas: 16},
				}}},
			}},
			ApplyRecords: []npuv1alpha1.ApplyRecord{{NodeName: "worker1", SharingMode: npuv1alpha1.SharingModeTimeSliced, SharingReplicas: 4}},
		},
	}
	c := build(t, nvidiaClass(), gpuNode("8"), acpp)
	w, err := validateAcceleratorWorkload(context.Background(), c, workload(npuv1alpha1.AccessModeShared, 4))
	if err != nil {
		t.Fatalf("verified+applied sharing rejected: %v", err)
	}
	if len(w) != 1 || !strings.Contains(w[0], "not isolated") {
		t.Fatalf("shared admission must warn about isolation: %v", w)
	}
}

// 번역이 더는 성립하지 않아도 metadata 만 고치는 업데이트는 통과해야 한다 — 그렇지 않으면
// quiesce opt-in 라벨조차 붙일 수 없고, DELETE 는 검증하지 않으므로 복구책이
// delete-and-recreate 밖에 없게 된다.
func TestAWValidatorUpdateSkipsUnchangedSpec(t *testing.T) {
	v := &AWValidator{Client: build(t)} // 클래스도 노드도 없다 — 지금 번역하면 반드시 실패한다.
	old := workload(npuv1alpha1.AccessModeExclusive, 0)
	updated := old.DeepCopy()
	updated.Labels = map[string]string{"npu.ai/quiesce-on-driver-upgrade": "true"}
	if _, err := v.ValidateUpdate(context.Background(), old, updated); err != nil {
		t.Fatalf("metadata-only update rejected: %v", err)
	}
	// spec 이 바뀌면 다시 검증한다.
	updated.Spec.Accelerator.Access.Mode = npuv1alpha1.AccessModeShared
	if _, err := v.ValidateUpdate(context.Background(), old, updated); err == nil {
		t.Fatal("spec change admitted without validation")
	}
}

func TestValidateAcceleratorWorkloadWrongType(t *testing.T) {
	c := build(t)
	if _, err := validateAcceleratorWorkload(context.Background(), c, &npuv1alpha1.NPUClusterPolicy{}); err == nil {
		t.Fatal("wrong type accepted")
	}
}
