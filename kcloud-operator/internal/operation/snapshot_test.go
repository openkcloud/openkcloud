// ============================================================
// snapshot_test.go: 적용 전 스냅샷 구성 테스트
// 상세: 되돌릴 목표와 크래시 복구의 대조군이 되는 값이라, 관측하지 못한 축을 채우지 않는 것이 핵심이다.
// 생성일: 2026-08-01
// ============================================================
package operation

import (
	"math"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func snapNode(bootID string, alloc map[string]int64) *corev1.Node {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "w1"},
		Status: corev1.NodeStatus{
			NodeInfo:    corev1.NodeSystemInfo{BootID: bootID},
			Allocatable: corev1.ResourceList{},
		},
	}
	for k, v := range alloc {
		n.Status.Allocatable[corev1.ResourceName(k)] = *resource.NewQuantity(v, resource.DecimalSI)
	}
	return n
}

// 증명: 스냅샷이 노드 allocatable·bootID·관측 geometry 를 그대로 담는다.
// 깨는 뮤테이션: allocatable 복사를 빼면 rollback 목표가 사라져 실패한다.
func TestBuildSnapshotCapturesState(t *testing.T) {
	got := BuildSnapshot(snapNode("boot-1", map[string]int64{"nvidia.com/gpu": 2}),
		map[string]string{"0000:41:00.0": ""}, "Prepared", true)
	if got.BootID != "boot-1" || got.MigPhase != "Prepared" || !got.CordonedByPolicy {
		t.Fatalf("got=%+v", got)
	}
	if got.Allocatable["nvidia.com/gpu"] != 2 {
		t.Fatalf("allocatable=%+v", got.Allocatable)
	}
	if _, ok := got.Geometry["0000:41:00.0"]; !ok {
		t.Fatalf("geometry=%+v", got.Geometry)
	}
}

// 증명: 노드를 못 읽으면 스냅샷을 만들지 않는다(빈 스냅샷으로 되돌리면 자원을 0 으로 만든다).
// 깨는 뮤테이션: nil 노드에 빈 스냅샷을 돌려주게 바꾸면 실패한다.
func TestBuildSnapshotWithoutNodeIsNil(t *testing.T) {
	if got := BuildSnapshot(nil, nil, "", false); got != nil {
		t.Fatalf("got=%+v", got)
	}
}

// 증명: 스냅샷이 입력 맵을 붙들지 않는다(호출자가 나중에 고쳐도 스냅샷이 안 흔들린다).
// 깨는 뮤테이션: 맵을 그대로 대입하면 실패한다.
func TestBuildSnapshotCopiesGeometry(t *testing.T) {
	geom := map[string]string{"0000:41:00.0": "1g.6gb x4"}
	got := BuildSnapshot(snapNode("b", nil), geom, "", false)
	geom["0000:41:00.0"] = "changed"
	if got.Geometry["0000:41:00.0"] != "1g.6gb x4" {
		t.Fatalf("스냅샷이 입력 맵을 공유한다: %+v", got.Geometry)
	}
}

// 증명: allocatable 이 큰 값(예: 바이트 단위 memory)에서도 int32 로 조용히 음수로 뒤집히지
// 않는다 — internal/verification/verifier.go 의 allocatableOf 가 이미 겪은 것과 같은 버그
// 계열이다(스냅샷은 rollback 의 목표값이라, 여기서 뒤집히면 되돌릴 때 음수 요청을 만든다).
// 깨는 뮤테이션: 상한 clamp 를 빼고 int32(q.Value()) 를 그대로 캐스팅하면 실패한다.
func TestBuildSnapshotClampsOversizedAllocatable(t *testing.T) {
	n := snapNode("b", nil)
	n.Status.Allocatable["memory"] = *resource.NewQuantity(int64(math.MaxInt32)+1000, resource.DecimalSI)
	got := BuildSnapshot(n, nil, "", false)
	if got.Allocatable["memory"] < 0 {
		t.Fatalf("allocatable 이 음수로 뒤집혔다: %d", got.Allocatable["memory"])
	}
	if got.Allocatable["memory"] != math.MaxInt32 {
		t.Fatalf("allocatable = %d, want clamp to MaxInt32", got.Allocatable["memory"])
	}
}
