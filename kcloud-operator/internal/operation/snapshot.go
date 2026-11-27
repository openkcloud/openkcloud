// ============================================================
// snapshot.go: 적용 전 상태 스냅샷
// 상세: 보상 rollback 의 목표이자 크래시 복구의 대조군이다. 관측하지 못한 축은 담지 않는다 —
// 빈 스냅샷으로 되돌리면 멀쩡한 자원을 0 으로 만든다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"math"

	corev1 "k8s.io/api/core/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// BuildSnapshot 은 mutation 직전 상태를 담는다. 노드를 읽지 못했으면 nil 이다 — 스냅샷이 없는
// 것과 "전부 0 이었다" 는 완전히 다른 주장이고, 후자를 지어내면 rollback 이 자원을 지운다.
func BuildSnapshot(node *corev1.Node, geometry map[string]string, migPhase string, cordonedByPolicy bool) *v1alpha1.OperationSnapshot {
	if node == nil {
		return nil
	}
	alloc := make(map[string]int32, len(node.Status.Allocatable))
	for k, q := range node.Status.Allocatable {
		v := q.Value()
		// memory 처럼 바이트 단위로 큰 값이 섞여 들어올 수 있다(internal/verification/verifier.go
		// 의 allocatableOf 와 같은 규율) — int32 로 그냥 캐스팅하면 조용히 음수로 뒤집혀,
		// rollback 이 그 값을 목표로 삼으면 음수 요청을 만든다. 상한을 넘으면 clamp 한다.
		if v > math.MaxInt32 {
			v = math.MaxInt32
		}
		alloc[string(k)] = int32(v)
	}
	geom := make(map[string]string, len(geometry))
	for k, v := range geometry {
		geom[k] = v
	}
	return &v1alpha1.OperationSnapshot{
		Allocatable:      alloc,
		Geometry:         geom,
		MigPhase:         migPhase,
		BootID:           node.Status.NodeInfo.BootID,
		CordonedByPolicy: cordonedByPolicy,
	}
}
