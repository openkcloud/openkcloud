// ============================================================
// accelerator_capability_axes_test.go: capability 3축 타입 회귀 테스트
// 상세: 미검증 기본값이 정직(Supported=false, Verification=Required)한지 + isolation 등급 문자열 고정.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import (
	"encoding/json"
	"testing"
)

func TestSharingCapabilityZeroValueIsHonest(t *testing.T) {
	var c SharingCapability
	if c.TimeSlicing.Supported || c.MultiProcess.Supported || c.Brokered.Supported {
		t.Fatalf("zero value must not claim support: %+v", c)
	}
}

func TestSharingModeSupportJSONOmitsEmpty(t *testing.T) {
	b, err := json.Marshal(SharingModeSupport{Supported: false})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"supported":false}`; got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}

func TestIsolationLevelsAreTheFiveDefinedGrades(t *testing.T) {
	want := []string{"none", "process", "subdevice", "device", "hardware"}
	got := []string{IsolationNone, IsolationProcess, IsolationSubdevice, IsolationDevice, IsolationHardware}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("grade[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeviceStatusCarriesThreeAxes(t *testing.T) {
	d := DeviceStatus{
		ID:             "GPU-1",
		AllocationAPIs: []string{AllocationAPIDevicePlugin},
		SharingCapability: SharingCapability{
			TimeSlicing: SharingModeSupport{Supported: true, Verification: VerificationVerified, MaxReplicas: 8},
		},
		IsolationCapability: IsolationCapability{Compute: IsolationHardware, Memory: IsolationHardware, Fault: IsolationDevice},
	}
	if d.SharingCapability.TimeSlicing.MaxReplicas != 8 {
		t.Fatalf("maxReplicas lost: %+v", d)
	}
	if len(d.AllocationAPIs) != 1 || d.AllocationAPIs[0] != "devicePlugin" {
		t.Fatalf("allocationAPIs = %v", d.AllocationAPIs)
	}
}
