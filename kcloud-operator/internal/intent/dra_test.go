// ============================================================
// dra_test.go: DRA 가용성 실측 테스트
// 상세: API 서빙 여부와 드라이버별 노드 장치 수를 분리해 수집하는지, 노드에 묶이지 않은
// ResourceSlice 를 후보 판정에서 제외하는지 본다.
// 생성일: 2026-08-05 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestBuildDRACapability(t *testing.T) {
	classes := []resourcev1.DeviceClass{{ObjectMeta: metav1.ObjectMeta{Name: "gpu.nvidia.com"}}}
	slices := []resourcev1.ResourceSlice{
		{Spec: resourcev1.ResourceSliceSpec{
			Driver:   "gpu.nvidia.com",
			NodeName: ptr.To("k8s-worker1"),
			Pool:     resourcev1.ResourcePool{Name: "pool-x"},
			Devices:  []resourcev1.Device{{Name: "gpu-0"}, {Name: "gpu-1"}},
		}},
		{Spec: resourcev1.ResourceSliceSpec{ // 같은 노드·드라이버·풀의 두 번째 슬라이스 — 이름 중복 저장 여부를 아래서 본다
			Driver:   "gpu.nvidia.com",
			NodeName: ptr.To("k8s-worker1"),
			Pool:     resourcev1.ResourcePool{Name: "pool-x"},
		}},
	}
	got := BuildDRACapability(true, classes, slices)
	if !got.APIServed {
		t.Fatal("APIServed should be true")
	}
	if !got.DeviceClasses["gpu.nvidia.com"] {
		t.Fatalf("DeviceClasses missing gpu.nvidia.com: %+v", got.DeviceClasses)
	}
	if n := got.SlicesByNodeDriver["k8s-worker1"]["gpu.nvidia.com"]; n != 2 {
		t.Fatalf("device count = %d, want 2", n)
	}
	// 슬라이스 둘이 같은 풀이므로, 중복 없이 담겼다면 목록 길이는 1이어야 한다.
	if pools := got.PoolsByNodeDriver["k8s-worker1"]["gpu.nvidia.com"]; len(pools) != 1 || pools[0] != "pool-x" {
		t.Fatalf("pools = %v, want [pool-x]", pools)
	}
}

// 노드에 묶이지 않은 슬라이스(NodeName=nil)는 노드 후보 판정에 쓸 수 없다.
func TestBuildDRACapabilitySkipsNodelessSlices(t *testing.T) {
	slices := []resourcev1.ResourceSlice{
		{Spec: resourcev1.ResourceSliceSpec{Driver: "d", Devices: []resourcev1.Device{{Name: "x"}}}},
	}
	got := BuildDRACapability(true, nil, slices)
	if len(got.SlicesByNodeDriver) != 0 {
		t.Fatalf("nodeless slice must not produce a node entry: %+v", got.SlicesByNodeDriver)
	}
}
