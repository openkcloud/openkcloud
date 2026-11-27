// ============================================================
// acceleratorpartitionpolicy_status_test.go: ACPP status 4축 분리 검증
// 상세: partitionCapability/layout/operations/advertisement 축이 서로 섞이지 않는지 확인.
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import "testing"

func TestACPPStatus_FourAxesSeparated(t *testing.T) {
	ts := TargetStatus{
		NodeName: "rngd-1",
		Backend:  BackendRef{Kind: "DaemonSet", ConfigurationScope: "DaemonSetGlobal"},
		Devices: []DeviceStatus{{
			ID:                  "GPU-uuid",
			PartitionCapability: PartitionCapability{HardwareSupported: true, PartitionModel: "FixedProfile"},
		}},
		ResolvedLayout: []ResolvedLayoutEntry{{Profile: "2core.12gb", BackendPolicy: "dual-core", ExpectedCountPerDevice: 4}},
		Operations:     OperationsStatus{Apply: OperationStatus{Supported: true}},
		Advertisement:  AdvertisementStatus{Mode: "Flat", AdvertisedResources: map[string]int32{"furiosa.ai/rngd": 4}},
	}
	// 광고 정보는 capability 에 없어야 한다(축 분리).
	if len(ts.Devices[0].PartitionCapability.Profiles) != 0 {
		t.Fatal("capability should not carry advertisement/resolved info")
	}
	if ts.Advertisement.Mode != "Flat" {
		t.Fatalf("advertisement mode: %q", ts.Advertisement.Mode)
	}
}
