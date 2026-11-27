// ============================================================
// precondition_test.go: precondition.go 단위 테스트 — 항상/hardware변경 전제 분리
// 생성일: 2026-07-24 | 수정일: 2026-07-24
// ============================================================
package nvidia

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestPodHoldsGPU(t *testing.T) {
	gpuPod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
			"nvidia.com/gpu": resource.MustParse("1"),
		}},
	}}}}
	if !podHoldsGPU(gpuPod) {
		t.Fatal("nvidia.com/gpu limit 은 GPU 점유로 감지되어야 함")
	}

	migPod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
			"nvidia.com/mig-1g.5gb": resource.MustParse("1"),
		}},
	}}}}
	if !podHoldsGPU(migPod) {
		t.Fatal("nvidia.com/mig- prefix limit 은 GPU 점유로 감지되어야 함")
	}

	cpuPod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
			"cpu": resource.MustParse("1"),
		}},
	}}}}
	if podHoldsGPU(cpuPod) {
		t.Fatal("cpu limit 만 있으면 GPU 점유가 아니어야 함")
	}
}

func TestMigSafe(t *testing.T) {
	// Enabled + GI 없음(geometry "") 만 safe.
	if !migSafe(MigDevice{ModeCurrent: "Enabled"}) {
		t.Fatal("Enabled+no-GI 는 safe")
	}
	if migSafe(MigDevice{ModeCurrent: "Disabled", ModePending: "Disabled", Geometry: "disabled"}) {
		t.Fatal("Disabled 는 unsafe(MIG mode 사전 enable 필요)")
	}
	if migSafe(MigDevice{ModeCurrent: "Enabled", Geometry: "1g.6gb x4"}) {
		t.Fatal("Enabled+기존 GI 는 unsafe")
	}
	if migSafe(MigDevice{ModeCurrent: "Enabled", ObsError: "read failed"}) {
		t.Fatal("ObsError 있으면 fail-closed 로 unsafe")
	}
}

func TestCheckAlways_FailClosed(t *testing.T) {
	r := CheckAlways([]MigDevice{{PCI: "", ObsError: "x"}}, true)
	if !r.PCIMissing || !r.ObservationFailed {
		t.Fatal("항상-전제: PCI/관측 실패 감지")
	}
}
