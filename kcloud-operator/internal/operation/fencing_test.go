// ============================================================
// fencing_test.go: epoch 펜싱 테스트
// 상세: 세대가 단조 증가한다는 규율을 고정한다. 낡은 결과 폐기 자체는 컨트롤러의 writeStatus
// 경로에서 검증한다(operation_epoch_test.go).
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import "testing"

// 증명: epoch 은 단조 증가한다.
// 깨는 뮤테이션: NextEpoch 를 항등함수로 바꾸면 실패한다.
func TestNextEpochIsMonotonic(t *testing.T) {
	cur := int64(0)
	for i := 0; i < 5; i++ {
		nxt := NextEpoch(cur)
		if nxt <= cur {
			t.Fatalf("NextEpoch(%d) = %d", cur, nxt)
		}
		cur = nxt
	}
}
