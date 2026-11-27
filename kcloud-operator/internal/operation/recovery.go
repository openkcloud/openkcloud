// ============================================================
// recovery.go: 크래시 복구 판정
// 상세: 적용 도중 프로세스가 죽었을 때 commit 과 rollback 중 무엇인지를 정한다. 저널 → 장치 관측
// 순으로 보고, 근거(evidence)는 크래시 이전 값이라 지금 상태를 설명하지 못해(재부팅 시 무효) 안 쓴다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"fmt"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// RecoveryDecision 은 복구 판정 결과다.
type RecoveryDecision string

const (
	// RecoverRestart: 하드웨어를 아직 안 건드렸다 — 처음부터 다시 한다.
	RecoverRestart RecoveryDecision = "Restart"
	// RecoverCommit: 장치가 이미 목표 상태다 — 검증으로 넘어간다.
	RecoverCommit RecoveryDecision = "Commit"
	// RecoverRollback: 장치가 목표와 다르다 — 보상한다.
	RecoverRollback RecoveryDecision = "Rollback"
	// RecoverWait: 관측이 안 된다 — 판정하지 않고 기다린다.
	RecoverWait RecoveryDecision = "Wait"
	// RecoverManual: 관측이 계속 안 된다 — 사람에게 넘긴다.
	RecoverManual RecoveryDecision = "Manual"
)

// RecoveryInput 은 판정에 필요한 값 전부다.
type RecoveryInput struct {
	Journal []v1alpha1.OperationJournalEntry
	// ObservationOK 는 장치 관측이 성공했는지다. 실패한 관측값을 넘기지 않는다.
	ObservationOK bool
	// ObservedGeometry 는 PCI → 실제 geometry 다.
	ObservedGeometry map[string]string
	// ExpectedGeometry 는 목표 geometry 요약이다. 비어 있으면 관측으로 commit 을 주장할 수 없다.
	ExpectedGeometry   string
	ObserveFailures    int32
	MaxObserveFailures int32
}

// DecideRecovery 는 복구 방향과 사유를 돌려준다. 순서가 계약이다 — 저널을 먼저 보는 이유는 그것이
// "무엇을 하려 했는가" 의 유일한 durable 진술이기 때문이고, 적용 시작 기록이 없으면 하드웨어는
// 안 바뀌었으므로 관측을 볼 필요조차 없다. 관측은 "실제로 어디까지 갔는가" 만 답하므로 나중에 본다.
// 오늘의 유일한 호출 지점(참여자의 적용 실패 보고)은 StepApplyStarted 가 이미 기록된 뒤라 첫 분기를
// 거치지 않지만, 이 함수는 순수 판정 함수이고 저널 없는 입력에 rollback 을 돌려주는 쪽이 더
// 위험하므로 검사를 남긴다 — 호출 지점이 늘어나면 실제로 쓰인다.
func DecideRecovery(in RecoveryInput) (RecoveryDecision, string) {
	if !HasStep(in.Journal, StepApplyStarted) {
		return RecoverRestart, "no apply was journalled; hardware was not touched"
	}
	if !in.ObservationOK {
		if in.ObserveFailures >= in.MaxObserveFailures && in.MaxObserveFailures > 0 {
			return RecoverManual, fmt.Sprintf("device observation failed %d times; refusing to guess", in.ObserveFailures)
		}
		// 관측 없이 되돌리면 멀쩡한 상태를 부술 수 있다 — 모를 때는 아무것도 하지 않는다.
		return RecoverWait, "device observation unavailable; withholding judgement"
	}
	if in.ExpectedGeometry == "" {
		return RecoverRollback, "no target geometry to compare against; cannot claim the apply landed"
	}
	if len(in.ObservedGeometry) == 0 {
		return RecoverRollback, "no device observed; cannot claim the apply landed"
	}
	for pci, got := range in.ObservedGeometry {
		if got != in.ExpectedGeometry {
			return RecoverRollback, fmt.Sprintf("device %s is %q, expected %q", pci, got, in.ExpectedGeometry)
		}
	}
	return RecoverCommit, "every observed device already matches the target"
}
