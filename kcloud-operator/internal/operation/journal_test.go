// ============================================================
// journal_test.go: 선행 기록 저널 테스트
// 상세: append-only·멱등·순서 보존을 고정한다. 크래시 복구가 이 이력만 보고 판정하므로
// 한 줄이라도 유실되거나 뒤집히면 판정이 틀린다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

func at(sec int) metav1.Time {
	return metav1.NewTime(time.Date(2026, 8, 1, 0, 0, sec, 0, time.UTC))
}

// 증명: 저널이 순서대로 쌓인다.
// 깨는 뮤테이션: AppendJournal 이 앞에 끼워 넣게 바꾸면 실패한다.
func TestAppendKeepsOrder(t *testing.T) {
	var j []v1alpha1.OperationJournalEntry
	j = AppendJournal(j, StepPlanned, 1, at(1), "")
	j = AppendJournal(j, StepLeaseAcquired, 1, at(2), "")
	j = AppendJournal(j, StepApplyStarted, 1, at(3), "1g.6gb x4")
	if len(j) != 3 || j[0].Step != StepPlanned || j[2].Step != StepApplyStarted {
		t.Fatalf("j=%+v", j)
	}
	if j[2].Detail != "1g.6gb x4" {
		t.Fatalf("detail=%q", j[2].Detail)
	}
}

// 증명: 같은 단계를 같은 epoch 에서 두 번 써도 한 줄이다(재진입 reconcile 이 저널을 부풀리지 않는다).
// 깨는 뮤테이션: 멱등 검사를 빼면 status 가 매 pass 마다 커져 apiserver write 가 끝없이 발생한다.
func TestAppendIsIdempotentWithinEpoch(t *testing.T) {
	var j []v1alpha1.OperationJournalEntry
	j = AppendJournal(j, StepApplyStarted, 1, at(1), "")
	j = AppendJournal(j, StepApplyStarted, 1, at(9), "다른 detail")
	if len(j) != 1 {
		t.Fatalf("중복 기록됐다: %+v", j)
	}
	if !j[0].At.Equal(&[]metav1.Time{at(1)}[0]) {
		t.Fatalf("첫 기록 시각이 덮어써졌다: %v", j[0].At)
	}
}

// 증명: epoch 이 다르면 같은 단계라도 새 줄이다(재획득 후 다시 시작한 사실이 남아야 한다).
// 깨는 뮤테이션: 멱등 검사에서 epoch 비교를 빼면 실패한다.
func TestAppendRecordsSameStepInNewEpoch(t *testing.T) {
	var j []v1alpha1.OperationJournalEntry
	j = AppendJournal(j, StepApplyStarted, 1, at(1), "")
	j = AppendJournal(j, StepApplyStarted, 2, at(2), "")
	if len(j) != 2 {
		t.Fatalf("j=%+v", j)
	}
}

// 증명: HasStep 은 epoch 과 무관하게 전 이력을 본다 — 복구 판정이 "옛 epoch 에서 이미 적용을
// 시작했다" 를 알아야 하기 때문이다.
// 깨는 뮤테이션: HasStep 을 현재 epoch 으로 한정하면 크래시 후 적용 이력이 사라져 실패한다.
func TestHasStepSpansEpochs(t *testing.T) {
	j := []v1alpha1.OperationJournalEntry{{Step: StepApplyStarted, Epoch: 1, At: at(1)}}
	if !HasStep(j, StepApplyStarted) {
		t.Fatalf("옛 epoch 의 적용 이력을 못 찾았다")
	}
	if HasStep(j, StepCommitted) {
		t.Fatalf("없는 단계를 있다고 했다")
	}
}

// 증명: LastStep 이 마지막 줄을 준다.
// 깨는 뮤테이션: 첫 줄을 돌려주게 바꾸면 실패한다.
func TestLastStep(t *testing.T) {
	j := []v1alpha1.OperationJournalEntry{
		{Step: StepPlanned, Epoch: 1}, {Step: StepApplyStarted, Epoch: 1},
	}
	if LastStep(j) != StepApplyStarted {
		t.Fatalf("LastStep=%q", LastStep(j))
	}
	if LastStep(nil) != "" {
		t.Fatalf("빈 저널의 마지막 단계가 비어 있지 않다")
	}
}

// 증명: 여러 단계를 실제 콜사이트가 부르는 순서 그대로 쌓아도(계획→lease→스냅샷→적용) 그 순서가
// 그대로 보존된다 — TestAppendKeepsOrder 는 3단계만 확인해 중간에 순서가 한 번 뒤집혀도 우연히
// 들키지 않을 수 있다(예: 뒤에서 두 번째와 마지막만 바뀌는 뮤테이션). 전 단계 이름을 인접 쌍으로
// 대조해 전체 순서를 고정한다.
// 깨는 뮤테이션: AppendJournal 이 정렬되지 않은 위치(예: 두 번째)에 끼워 넣으면 실패한다.
func TestAppendPreservesFullCallOrder(t *testing.T) {
	steps := []string{StepPlanned, StepLeaseAcquired, StepSnapshotTaken, StepQuiesced,
		StepApplyStarted, StepApplyFinished, StepVerifyStarted, StepCommitted}
	var j []v1alpha1.OperationJournalEntry
	for i, s := range steps {
		j = AppendJournal(j, s, 1, at(i+1), "")
	}
	if len(j) != len(steps) {
		t.Fatalf("len(j)=%d, want %d: %+v", len(j), len(steps), j)
	}
	for i, s := range steps {
		if j[i].Step != s {
			t.Fatalf("j[%d].Step = %q, want %q (전체 호출 순서가 보존되지 않았다): %+v", i, j[i].Step, s, j)
		}
	}
}
