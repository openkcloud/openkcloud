// ============================================================
// acpp_baseline_expectation_test.go: apply 후 기대 allocatable 산출 회귀 시험
// 상세: 조각낸 GPU 는 온전한 GPU 로 광고되지 않는다는 사실을 고정한다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/partition/nvidia"
)

// TestNvidiaPhysicalGPUsCountsInventoryNotAdvertisement 는 물리 GPU 수가 인벤토리에서 나오는지
// 본다 — allocatable 은 어느 device-plugin 이 떠 있는지에 따라 달라져 기준이 될 수 없다.
func TestNvidiaPhysicalGPUsCountsInventoryNotAdvertisement(t *testing.T) {
	ndr := &npuv1alpha1.NodeDeviceReport{Status: npuv1alpha1.NodeDeviceReportStatus{
		Devices: []npuv1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "generic", Count: 1},
			{Vendor: "nvidia", Model: "generic"}, // count 미기재 = 1대
			{Vendor: "furiosa", Model: "warboy", Count: 1},
		},
	}}
	if got := nvidiaPhysicalGPUs(ndr); got != 2 {
		t.Fatalf("nvidiaPhysicalGPUs = %d, want 2 (nvidia 만, count 미기재는 1)", got)
	}
}

// TestComputeBaselineExcludesPartitionedGPUFromExpectation 는 조각낼 GPU 가 기대 온전-GPU 수에서
// 빠지는지 고정한다. 빠지지 않으면 A30 1장을 조각낸 뒤에도 nvidia.com/gpu 2 를 기다리게 되고,
// mixed device-plugin 은 1 만 광고하므로 verify 가 180초를 채우고 rollback 한다.
// 픽스처의 allocatable 2 는 "mode 는 Enabled 인데 GI 가 없어 flat 이 아직 온전한 GPU 로 광고하는"
// 실제 상태다 — 이 상태가 조각내기 직전의 정상 입력이다.
func TestComputeBaselineExcludesPartitionedGPUFromExpectation(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceName(nvidiaGPUResource): resource.MustParse("2"),
		}},
	}
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1"},
		Status: npuv1alpha1.NodeDeviceReportStatus{Devices: []npuv1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "generic", Count: 1, PCIeAddress: "0000:18:00.0"},
			{Vendor: "nvidia", Model: "generic", Count: 1, PCIeAddress: "0000:af:00.0"},
		}},
	}
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).WithObjects(node, ndr).Build()
	r := &AcceleratorPartitionPolicyReconciler{Client: c, Scheme: c.Scheme()}
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{ObjectMeta: metav1.ObjectMeta{Name: "worker1-a30-4x1g"}}
	rec, err := r.computeBaseline(context.Background(), "k8s-worker1", acpp,
		[]partition.ResolvedEntry{{Profile: "1g.6gb", ExpectedCountPerDevice: 4}},
		[]nvidia.MigDevice{{PCI: "0000:18:00.0"}})
	if err != nil {
		t.Fatalf("computeBaseline: %v", err)
	}
	if rec.BaselineGPUCount != 2 {
		t.Fatalf("baselineGPUCount = %d, want 2 (복원 기준은 광고 그대로)", rec.BaselineGPUCount)
	}
	if rec.ExpectedFullGPUCount != 1 {
		t.Fatalf("expectedFullGPUCount = %d, want 1 (물리 2 - 조각낼 1)", rec.ExpectedFullGPUCount)
	}
	exp := expectedAllocatable(rec)
	if exp[nvidiaGPUResource] != 1 {
		t.Fatalf("expected full GPU = %d, want 1", exp[nvidiaGPUResource])
	}
	if exp["nvidia.com/mig-1g.6gb"] != 4 {
		t.Fatalf("expected mig count = %d, want 4", exp["nvidia.com/mig-1g.6gb"])
	}
}

// TestMigGeometryEmptyAcceptsDisabledSentinel 은 "조각 없음" 의 두 표기를 모두 받는지 고정한다.
// mode 가 꺼진 GPU 는 geometry 를 "disabled" 로 보고하므로, 빈 문자열만 받으면 복원이 끝난
// 노드가 "아직 조각이 남았다" 로 읽혀 정리가 영구히 막힌다.
func TestMigGeometryEmptyAcceptsDisabledSentinel(t *testing.T) {
	for _, g := range []string{"", "disabled", "Disabled"} {
		if !migGeometryEmpty(g) {
			t.Errorf("migGeometryEmpty(%q) = false, want true", g)
		}
	}
	if migGeometryEmpty("1g.6gb x4") {
		t.Error("실제 조각 요약은 비어 있다고 보면 안 된다")
	}
}
