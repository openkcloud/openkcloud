// ============================================================
// journal.go: 선행 기록 저널
// 상세: 되돌릴 수 없는 행동을 하기 전에 그 의도를 먼저 영속한다. append-only 이고 epoch 이 함께
// 박혀 있어, 크래시 후 "어디까지 갔는가" 를 이력만으로 답할 수 있다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// 저널 단계 이름. 문자열이 status 에 그대로 남으므로 계약이다.
const (
	StepPlanned         = "Planned"
	StepLeaseAcquired   = "LeaseAcquired"
	StepSnapshotTaken   = "SnapshotTaken"
	StepQuiesced        = "Quiesced"
	StepApplyStarted    = "ApplyStarted"
	StepApplyFinished   = "ApplyFinished"
	StepRebootRequested = "RebootRequested"
	// StepRebootObserved 는 노드가 재부팅에서 돌아온 것을 관측한 시점이다.
	StepRebootObserved = "RebootObserved"
	// StepDeviceResetRequested 는 장치 리셋 명령 Job 을 만든 시점이다. 리셋은 되돌릴 수 없으므로
	// 이 단계가 이미 있으면 같은 작업이 두 번째 Job 을 만들지 않는다(Job 이 TTL 로 사라진 뒤에도).
	StepDeviceResetRequested = "DeviceResetRequested"
	StepVerifyStarted        = "VerifyStarted"
	StepCommitted            = "Committed"
	StepRollbackStarted      = "RollbackStarted"
	StepRollbackFinished     = "RollbackFinished"
)

// AppendJournal 은 단계를 이력 끝에 붙인다.
//
// 같은 (step, epoch) 이 이미 있으면 그대로 돌려준다. 재진입 reconcile 이 같은 단계를 다시
// 기록하면 status 가 매 pass 마다 커지고, 그 write 가 자기 자신을 재큐잉해 핫루프가 된다.
// epoch 이 다르면 새 줄이다 — Lease 를 다시 잡고 처음부터 다시 시작한 사실은 남아야 한다.
func AppendJournal(entries []v1alpha1.OperationJournalEntry, step string, epoch int64, at metav1.Time, detail string) []v1alpha1.OperationJournalEntry {
	for _, e := range entries {
		if e.Step == step && e.Epoch == epoch {
			return entries
		}
	}
	return append(entries, v1alpha1.OperationJournalEntry{Step: step, Epoch: epoch, At: at, Detail: detail})
}

// HasStep 은 이 단계가 **어느 epoch 에서든** 기록된 적이 있는지다.
// 복구 판정은 "옛 epoch 에서 이미 적용을 시작했다" 를 알아야 하므로 epoch 으로 한정하지 않는다.
func HasStep(entries []v1alpha1.OperationJournalEntry, step string) bool {
	for _, e := range entries {
		if e.Step == step {
			return true
		}
	}
	return false
}

// LastStep 은 마지막으로 기록된 단계다(빈 저널이면 빈 문자열).
func LastStep(entries []v1alpha1.OperationJournalEntry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].Step
}
