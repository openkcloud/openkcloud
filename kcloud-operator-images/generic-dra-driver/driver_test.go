// ============================================================
// driver_test.go: claim 준비/회수 시험
// 상세: 설정에 없는 장치를 claim 이 요구하면 오류를 돌려주는 것과, 준비·회수가 CDI 스펙
// 파일에 실제로 반영되는 것을 고정한다. host tempdir 에 파일을 깔아 발견을 유도한다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"testing"

	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func testDriver(t *testing.T) (*driver, string) {
	t.Helper()
	hostRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostRoot, "dev", "rngd", "0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "dev", "rngd", "0", "mgmt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Config{
		DriverName:  "npu.example.com",
		DeviceClass: "npu.example.com",
		DeviceNodes: []string{"/dev/rngd/{{id}}/mgmt"},
	}
	return &driver{
		config:   c,
		nodeName: "node-1",
		cdiDir:   t.TempDir(),
		hostRoot: hostRoot,
		prepared: map[types.UID][]string{},
	}, hostRoot
}

func claimAllocating(uid types.UID, driverName, deviceID string) *resourceapi.ResourceClaim {
	return &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{UID: uid},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Request: "req0", Driver: driverName, Pool: "node-1", Device: deviceID},
					},
				},
			},
		},
	}
}

func TestPrepareResourceClaimRejectsUnknownDevice(t *testing.T) {
	d, _ := testDriver(t)
	claim := claimAllocating("uid-1", "npu.example.com", "99")

	result := d.prepareResourceClaim(claim)
	if result.Err == nil {
		t.Fatal("설정에 없는 장치 요구가 조용히 통과했다")
	}
}

func TestPrepareResourceClaimWritesCDISpecForKnownDevice(t *testing.T) {
	d, _ := testDriver(t)
	claim := claimAllocating("uid-2", "npu.example.com", "0")

	result := d.prepareResourceClaim(claim)
	if result.Err != nil {
		t.Fatalf("알려진 장치 준비가 실패했다: %v", result.Err)
	}
	if len(result.Devices) != 1 {
		t.Fatalf("준비된 장치가 1개여야 한다: %+v", result.Devices)
	}
	if result.Devices[0].DeviceName != "0" {
		t.Fatalf("잘못된 장치 이름: %+v", result.Devices[0])
	}
	if want := "npu.example.com/device=0"; result.Devices[0].CDIDeviceIDs[0] != want {
		t.Fatalf("CDI 장치 ID: got %q want %q", result.Devices[0].CDIDeviceIDs[0], want)
	}

	files, _ := filepath.Glob(filepath.Join(d.cdiDir, "*.yaml"))
	if len(files) != 1 {
		t.Fatalf("CDI 스펙 파일이 1개여야 한다: %v", files)
	}
}

func TestUnprepareResourceClaimRemovesCDISpec(t *testing.T) {
	d, _ := testDriver(t)
	claim := claimAllocating("uid-3", "npu.example.com", "0")

	if result := d.prepareResourceClaim(claim); result.Err != nil {
		t.Fatalf("prepare: %v", result.Err)
	}
	if err := d.unprepareResourceClaim(claim.UID); err != nil {
		t.Fatalf("unprepare: %v", err)
	}

	files, _ := filepath.Glob(filepath.Join(d.cdiDir, "*.yaml"))
	if len(files) != 0 {
		t.Errorf("회수 후 스펙이 남았다: %v", files)
	}
	if _, ok := d.prepared[claim.UID]; ok {
		t.Error("회수 후에도 prepared 맵에 claim 이 남아있다")
	}
}
