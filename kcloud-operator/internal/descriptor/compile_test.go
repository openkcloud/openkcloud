// ============================================================
// compile_test.go: descriptor → backend 설정 컴파일 시험
// 상세: 설정의 모든 값이 descriptor 에서만 나와야 한다. 어디선가 벤더 기본값이
//
//	끼어들면 "명세서가 단일 출처" 라는 주장이 깨진다.
//
// 생성일: 2026-08-11
// ============================================================
package descriptor

import (
	"encoding/json"
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func rngdSpec() npuv1alpha1.AcceleratorDescriptorSpec {
	return npuv1alpha1.AcceleratorDescriptorSpec{
		Vendor:         "rngd",
		Product:        "rngd",
		AllocationUnit: npuv1alpha1.AllocationUnitDevice,
		Identity: npuv1alpha1.DescriptorIdentity{
			Source:             npuv1alpha1.IdentitySourcePCIAddress,
			StableAcrossReboot: true,
		},
		DeviceNodes: []string{"/dev/rngd/{{id}}*"},
		Backends:    []string{npuv1alpha1.BackendDevicePlugin, npuv1alpha1.BackendDRA},
	}
}

func TestCompileDevicePluginEmitsTemplates(t *testing.T) {
	out, err := CompileDevicePlugin(rngdSpec())
	if err != nil {
		t.Fatalf("컴파일 실패: %v", err)
	}
	var got struct {
		ResourceName string   `json:"resourceName"`
		DeviceNodes  []string `json:"deviceNodes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("JSON 아님: %v", err)
	}
	if got.ResourceName != "rngd/rngd" {
		t.Errorf("리소스명이 descriptor 에서 안 나왔다: %q", got.ResourceName)
	}
	// 컴파일은 치환하지 않는다 — 템플릿을 그대로 실어야 런타임이 발견한다.
	if len(got.DeviceNodes) != 1 || got.DeviceNodes[0] != "/dev/rngd/{{id}}*" {
		t.Errorf("deviceNodes 템플릿이 그대로 실리지 않았다: %v", got.DeviceNodes)
	}
}

// 정합이 깨진 descriptor 는 컴파일 자체를 거절한다.
func TestCompileDevicePluginRejectsInvalidSpec(t *testing.T) {
	s := rngdSpec()
	s.Vendor = ""
	if _, err := CompileDevicePlugin(s); err == nil {
		t.Fatal("정합 오류인데 통과했다")
	}
}

func TestCompileDRAEmitsTemplates(t *testing.T) {
	out, err := CompileDRA(rngdSpec())
	if err != nil {
		t.Fatalf("컴파일 실패: %v", err)
	}
	var got struct {
		DriverName  string   `json:"driverName"`
		DeviceClass string   `json:"deviceClass"`
		DeviceNodes []string `json:"deviceNodes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("JSON 아님: %v", err)
	}
	if got.DriverName != DriverNameFor(rngdSpec()) || len(got.DeviceNodes) != 1 {
		t.Errorf("DRA 컴파일 결과가 잘못됐다: %+v", got)
	}
}

// 재부팅을 건너 안정하지 않은 식별자면 DRA 로 컴파일하지 않는다. 정적 검증기와 같은 판정이어야 한다.
func TestCompileDRARefusesUnstableIdentity(t *testing.T) {
	s := rngdSpec()
	s.Identity.StableAcrossReboot = false
	if _, err := CompileDRA(s); err == nil {
		t.Fatal("불안정 식별자인데 통과했다")
	}
}

func TestCompileDevicePluginThreadsReplicas(t *testing.T) {
	s := rngdSpec()
	s.Backends = []string{npuv1alpha1.BackendDevicePlugin}
	s.Sharing = &npuv1alpha1.DescriptorSharing{Mode: npuv1alpha1.SharingModeTimeSliced, Replicas: 3}
	out, err := CompileDevicePlugin(s)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Replicas int32 `json:"replicas"`
	}
	_ = json.Unmarshal(out, &got)
	if got.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3", got.Replicas)
	}
}
