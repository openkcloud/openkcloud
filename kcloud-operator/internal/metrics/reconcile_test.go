// ============================================================
// reconcile_test.go: 마지막 reconcile 시각 기록 동작 고정
// 생성일: 2026-09-15 | 수정일: 2026-09-15
// ============================================================

package metrics

import (
	"testing"
	"time"
)

// TestRecordReconcile_AdvancesLastTime 은 기록이 시각을 앞으로만 옮기는지 본다.
func TestRecordReconcile_AdvancesLastTime(t *testing.T) {
	RecordReconcile()
	first := GetLastReconcileTime()
	if first.IsZero() {
		t.Fatal("기록 뒤에도 시각이 zero 다")
	}

	time.Sleep(2 * time.Millisecond)
	RecordReconcile()
	second := GetLastReconcileTime()

	if !second.After(first) {
		t.Errorf("두 번째 기록 %v 가 첫 번째 %v 보다 뒤가 아니다", second, first)
	}
}
