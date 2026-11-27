// ============================================================
// fingerprint_test.go: 환경 지문 계산·비교 테스트
// 상세: 여러 벤더가 섞인 노드에서 결정론적으로 같은 장치를 고르는지, 관측 불가 필드를 조용히
// 채우지 않는지, 어떤 필드가 바뀌었는지를 정확히 말하는지 확인한다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

func testNode(bootID, kernel string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{BootID: bootID, KernelVersion: kernel},
		},
	}
}

func testNDR(devs ...v1alpha1.DeviceEntry) *v1alpha1.NodeDeviceReport {
	return &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "worker1"},
		Status:     v1alpha1.NodeDeviceReportStatus{Devices: devs},
	}
}

func TestComputeReadsBootKernelAndVendorDevice(t *testing.T) {
	ndr := testNDR(
		v1alpha1.DeviceEntry{Vendor: "furiosa", PCIeAddress: "0000:01:00.0", DriverVersion: "1.0.0"},
		v1alpha1.DeviceEntry{Vendor: "nvidia", PCIeAddress: "0000:41:00.0", DriverVersion: "535.104.05", FirmwareVersion: "92.00.45"},
	)
	got := Compute(testNode("boot-1", "5.15.0-181-generic"), ndr, "nvidia", 7)
	want := v1alpha1.EnvironmentFingerprint{
		BootID: "boot-1", KernelVersion: "5.15.0-181-generic",
		DriverVersion: "535.104.05", FirmwareVersion: "92.00.45", Generation: 7,
	}
	if got != want {
		t.Fatalf("Compute = %+v, want %+v", got, want)
	}
}

func TestComputePicksLowestPCIWhenVendorHasSeveralDevices(t *testing.T) {
	// 같은 벤더 장치가 여럿이면 결정론적으로 골라야 한다 — 순서가 바뀔 때마다 지문이 달라지면
	// 아무것도 안 바뀌었는데 evidence 가 계속 무효화된다.
	ndr := testNDR(
		v1alpha1.DeviceEntry{Vendor: "nvidia", PCIeAddress: "0000:81:00.0", DriverVersion: "535.104.05"},
		v1alpha1.DeviceEntry{Vendor: "nvidia", PCIeAddress: "0000:41:00.0", DriverVersion: "535.104.05"},
	)
	if got := Compute(testNode("b", "k"), ndr, "nvidia", 1); got.DriverVersion != "535.104.05" {
		t.Fatalf("driver = %q", got.DriverVersion)
	}
	// 순서를 뒤집어도 같은 결과여야 한다.
	rev := testNDR(ndr.Status.Devices[1], ndr.Status.Devices[0])
	if Compute(testNode("b", "k"), rev, "nvidia", 1) != Compute(testNode("b", "k"), ndr, "nvidia", 1) {
		t.Fatalf("장치 순서에 따라 지문이 달라진다")
	}
}

func TestComputeLeavesUnobservableFieldsEmpty(t *testing.T) {
	// NDR 이 없거나 그 벤더 장치가 없으면 driver/firmware 를 지어내지 않는다.
	got := Compute(testNode("boot-1", "5.15.0"), nil, "nvidia", 2)
	if got.DriverVersion != "" || got.FirmwareVersion != "" {
		t.Fatalf("관측하지 못한 필드를 채웠다: %+v", got)
	}
	if got.BootID != "boot-1" || got.Generation != 2 {
		t.Fatalf("관측 가능한 필드가 비었다: %+v", got)
	}
}

func TestComputeIgnoresVendorFilterWhenVendorEmpty(t *testing.T) {
	ndr := testNDR(v1alpha1.DeviceEntry{Vendor: "furiosa", PCIeAddress: "0000:01:00.0", DriverVersion: "1.0.0"})
	if got := Compute(testNode("b", "k"), ndr, "", 1); got.DriverVersion != "1.0.0" {
		t.Fatalf("vendor 미지정이면 첫 장치를 쓴다: %+v", got)
	}
}

func TestChangedFieldsNamesEveryChangedAxis(t *testing.T) {
	old := v1alpha1.EnvironmentFingerprint{BootID: "b1", KernelVersion: "k1", DriverVersion: "d1", FirmwareVersion: "f1", Generation: 1}
	cur := v1alpha1.EnvironmentFingerprint{BootID: "b2", KernelVersion: "k1", DriverVersion: "d2", FirmwareVersion: "f1", Generation: 2}
	got := ChangedFields(old, cur)
	want := []string{FieldBootID, FieldDriverVersion, FieldGeneration}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFields = %v, want %v", got, want)
	}
}

func TestChangedFieldsEmptyWhenIdentical(t *testing.T) {
	f := v1alpha1.EnvironmentFingerprint{BootID: "b1", Generation: 3}
	if got := ChangedFields(f, f); len(got) != 0 {
		t.Fatalf("동일 지문인데 변경으로 판정: %v", got)
	}
}

func TestChangedFieldsNamesAllFiveAxesWhenAllChange(t *testing.T) {
	// 5축 전부를 바꿔서, 그중 하나라도 비교가 죽으면(예: kernel·firmware 처럼 다른 테스트가
	// 건드리지 않는 축) 바로 여기서 이름이 빠져 잡히게 한다.
	old := v1alpha1.EnvironmentFingerprint{BootID: "b1", KernelVersion: "k1", DriverVersion: "d1", FirmwareVersion: "f1", Generation: 1}
	cur := v1alpha1.EnvironmentFingerprint{BootID: "b2", KernelVersion: "k2", DriverVersion: "d2", FirmwareVersion: "f2", Generation: 2}
	got := ChangedFields(old, cur)
	want := []string{FieldBootID, FieldDriverVersion, FieldFirmwareVersion, FieldGeneration, FieldKernelVersion}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFields = %v, want %v", got, want)
	}
}
