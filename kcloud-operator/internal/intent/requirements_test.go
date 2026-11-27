// ============================================================
// requirements_test.go: 격리 등급·최소 메모리 게이트 테스트
// 상세: 미실측 격리는 만족으로 치지 않고, 공유 모드는 격리를 none 으로 강등하며,
// 분할 모드의 메모리는 profile 이름에서 읽는다.
// 생성일: 2026-07-29 | 수정일: 2026-07-29
// ============================================================
package intent

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	"kcloud-operator/api/v1alpha1"
)

func TestIsolationRank(t *testing.T) {
	for level, want := range map[string]int{
		"":                          -1,
		v1alpha1.IsolationNone:      0,
		v1alpha1.IsolationProcess:   1,
		v1alpha1.IsolationSubdevice: 2,
		v1alpha1.IsolationDevice:    3,
		v1alpha1.IsolationHardware:  4,
		"banana":                    -1,
	} {
		if got := IsolationRank(level); got != want {
			t.Fatalf("IsolationRank(%q)=%d want %d", level, got, want)
		}
	}
}

func TestProfileMemoryMiB(t *testing.T) {
	for profile, want := range map[string]int64{
		"1g.6gb":     6144,
		"2g.12gb":    12288,
		"2core.12gb": 12288,
		"4core.24gb": 24576,
	} {
		got, ok := ProfileMemoryMiB(profile)
		if !ok || got != want {
			t.Fatalf("ProfileMemoryMiB(%q)=(%d,%v) want (%d,true)", profile, got, ok, want)
		}
	}
	if _, ok := ProfileMemoryMiB("dual-core"); ok {
		t.Fatal("profile without an encoded size must report unknown")
	}
	if _, ok := ProfileMemoryMiB(""); ok {
		t.Fatal("empty profile must report unknown")
	}
}

func TestMergeRequirementsTakesTheStricter(t *testing.T) {
	small := resource.MustParse("4Gi")
	big := resource.MustParse("16Gi")
	base := v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationProcess, MinimumMemory: &big}
	over := v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationHardware, MinimumMemory: &small}
	got := MergeRequirements(base, &over)
	if got.MinimumIsolation != v1alpha1.IsolationHardware {
		t.Fatalf("isolation=%q want hardware", got.MinimumIsolation)
	}
	if got.MinimumMemory.Value() != big.Value() {
		t.Fatalf("memory=%s want 16Gi", got.MinimumMemory)
	}
	if got := MergeRequirements(base, nil); got.MinimumIsolation != v1alpha1.IsolationProcess {
		t.Fatalf("nil override changed the base: %+v", got)
	}
}

func TestCheckRequirementsIsolation(t *testing.T) {
	strong := NodeCapability{NodeName: "worker1", Devices: []v1alpha1.DeviceStatus{{ID: "d0",
		IsolationCapability: v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationHardware, Memory: v1alpha1.IsolationHardware}}}}
	exclusive := v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}
	req := v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationSubdevice}
	if rj := CheckRequirements(strong, req, exclusive, nvidiaMapping()); rj != nil {
		t.Fatalf("hardware isolation rejected for a subdevice requirement: %v", rj)
	}

	weak := NodeCapability{NodeName: "rngd-1", Devices: []v1alpha1.DeviceStatus{{ID: "d0",
		IsolationCapability: v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationSubdevice}}}}
	rj := CheckRequirements(weak, req, exclusive, rngdMapping())
	if rj == nil || rj.Axis != AxisIsolation || rj.Reason != v1alpha1.AWReasonCapabilityUnverified {
		t.Fatalf("unmeasured memory isolation must fail closed: %+v", rj)
	}
}

// CheckRequirements 는 CheckMode 호출 여부에 기대지 않고 스스로 Stale 을 거절해야 한다 —
// 그렇지 않으면 shared 계열 + minimumIsolation=none 조합이 stale 노드를 그냥 통과시킨다.
// 이 테스트는 CheckMode 를 아예 호출하지 않는다.
func TestCheckRequirementsRejectsStaleWithoutCheckMode(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Stale: true, StaleReason: "AcceleratorPartitionPolicy has not converged"}
	shared := v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 2}
	req := v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationNone}
	rj := CheckRequirements(nc, req, shared, nvidiaMapping())
	if rj == nil || rj.Axis != AxisCandidates || rj.Reason != v1alpha1.AWReasonCapabilityUnverified {
		t.Fatalf("stale node must be rejected by CheckRequirements alone: %+v", rj)
	}
}

// 시분할 replica 사이에는 격리가 없다 — 장치가 hardware 격리를 하더라도 공유 모드는 none 이다.
func TestCheckRequirementsSharedDowngradesIsolation(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Devices: []v1alpha1.DeviceStatus{{ID: "d0",
		IsolationCapability: v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationHardware, Memory: v1alpha1.IsolationHardware}}}}
	shared := v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 2}
	rj := CheckRequirements(nc, v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationProcess}, shared, nvidiaMapping())
	if rj == nil || rj.Axis != AxisIsolation || rj.Reason != v1alpha1.AWReasonIsolationTooWeak {
		t.Fatalf("shared mode must be reported as no isolation: %+v", rj)
	}
	if rj := CheckRequirements(nc, v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationNone}, shared, nvidiaMapping()); rj != nil {
		t.Fatalf("minimumIsolation=none rejected for shared: %v", rj)
	}
}

func TestCheckRequirementsMemory(t *testing.T) {
	eight := resource.MustParse("8Gi")
	req := v1alpha1.AcceleratorRequirements{MinimumMemory: &eight}
	exclusive := v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}

	big := NodeCapability{NodeName: "worker1", MemoryMiB: 24576}
	if rj := CheckRequirements(big, req, exclusive, nvidiaMapping()); rj != nil {
		t.Fatalf("24Gi device rejected for an 8Gi requirement: %v", rj)
	}
	small := NodeCapability{NodeName: "worker2", MemoryMiB: 6144}
	rj := CheckRequirements(small, req, exclusive, nvidiaMapping())
	if rj == nil || rj.Axis != AxisMemory || rj.Reason != v1alpha1.AWReasonMemoryTooSmall {
		t.Fatalf("6Gi device accepted for an 8Gi requirement: %+v", rj)
	}
	unknown := NodeCapability{NodeName: "worker3"}
	rj = CheckRequirements(unknown, req, exclusive, nvidiaMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonMemoryUnknown {
		t.Fatalf("unknown memory must fail closed: %+v", rj)
	}
}

// 분할 모드는 전체 장치 메모리가 아니라 profile 이 주는 메모리와 비교해야 한다.
func TestCheckRequirementsMemoryUsesProfileWhenPartitioned(t *testing.T) {
	eight := resource.MustParse("8Gi")
	req := v1alpha1.AcceleratorRequirements{MinimumMemory: &eight}
	nc := NodeCapability{NodeName: "worker1", MemoryMiB: 24576}
	partitioned := v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitioned}

	rj := CheckRequirements(nc, req, partitioned, nvidiaMapping()) // 1g.6gb = 6Gi < 8Gi
	if rj == nil || rj.Reason != v1alpha1.AWReasonMemoryTooSmall {
		t.Fatalf("6Gi partition accepted for an 8Gi requirement on a 24Gi card: %+v", rj)
	}
	big := nvidiaMapping()
	big.NativeProfile = "2g.12gb"
	if rj := CheckRequirements(nc, req, partitioned, big); rj != nil {
		t.Fatalf("12Gi partition rejected for an 8Gi requirement: %v", rj)
	}
	opaque := nvidiaMapping()
	opaque.NativeProfile = "vendor-defined"
	rj = CheckRequirements(nc, req, partitioned, opaque)
	if rj == nil || rj.Reason != v1alpha1.AWReasonMemoryUnknown {
		t.Fatalf("opaque profile must fail closed: %+v", rj)
	}
}
