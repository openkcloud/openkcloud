// ============================================================
// reconcile.go: Reconcile 호출 시각 추적 헬퍼
// 상세: 마지막 reconcile 시각을 atomic 으로 기록하여 liveness probe 가
//       stale informer / zombie controller 감지에 활용할 수 있도록 한다.
// 생성일: 2026-04-27
// ============================================================

package metrics

import (
	"sync/atomic"
	"time"
)

// lastReconcileNanos: 마지막 reconcile 호출 시각 (UnixNano).
// liveness probe 가 stale informer / zombie controller 감지에 사용.
var lastReconcileNanos atomic.Int64

// RecordReconcile 는 현재 시각을 마지막 reconcile 시각으로 기록한다.
func RecordReconcile() {
	lastReconcileNanos.Store(time.Now().UnixNano())
}

// GetLastReconcileTime 은 마지막으로 기록된 reconcile 시각을 반환한다.
// 아직 reconcile 이 한 번도 실행되지 않았으면 zero time 을 반환한다.
func GetLastReconcileTime() time.Time {
	n := lastReconcileNanos.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}
