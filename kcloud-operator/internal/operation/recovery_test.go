// ============================================================
// recovery_test.go: 크래시 복구 판정 테스트
// 상세: 저널이 1순위, 장치 관측이 2순위, 관측 불가는 판정 보류라는 규율을 고정한다.
// 생성일: 2026-08-01
// ============================================================
package operation

import (
	"testing"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

func recoveryBase() RecoveryInput {
	return RecoveryInput{
		Journal: []v1alpha1.OperationJournalEntry{
			{Step: StepPlanned, Epoch: 1}, {Step: StepApplyStarted, Epoch: 1},
		},
		ObservationOK:      true,
		ObservedGeometry:   map[string]string{"0000:41:00.0": "1g.6gb x4"},
		ExpectedGeometry:   "1g.6gb x4",
		MaxObserveFailures: 5,
	}
}

// 증명: 저널에 적용 시작이 없으면 하드웨어는 안 바뀌었다 — 되돌릴 것이 없으므로 처음부터 한다.
// 깨는 뮤테이션: 저널 검사를 빼면 적용도 안 한 작업을 rollback 하려 들어 실패한다.
func TestNoApplyInJournalMeansRestart(t *testing.T) {
	in := recoveryBase()
	in.Journal = []v1alpha1.OperationJournalEntry{{Step: StepPlanned, Epoch: 1}}
	in.ObservedGeometry = map[string]string{"0000:41:00.0": ""}
	if d, _ := DecideRecovery(in); d != RecoverRestart {
		t.Fatalf("d=%q", d)
	}
}

// 증명: 적용을 시작했고 장치가 목표와 같으면 commit 이다(보고된 실패보다 관측이 이긴다).
// 깨는 뮤테이션: 관측 대조를 빼고 무조건 rollback 하면, 이미 성공한 적용을 되돌려 실패한다.
func TestAppliedAndMatchingMeansCommit(t *testing.T) {
	if d, _ := DecideRecovery(recoveryBase()); d != RecoverCommit {
		t.Fatalf("d=%q", d)
	}
}

// 증명: 적용을 시작했는데 장치가 목표와 다르면 rollback 이다.
// 깨는 뮤테이션: 비교를 반대로 하면 실패한다.
func TestAppliedAndMismatchedMeansRollback(t *testing.T) {
	in := recoveryBase()
	in.ObservedGeometry = map[string]string{"0000:41:00.0": "2g.12gb x2"}
	d, reason := DecideRecovery(in)
	if d != RecoverRollback {
		t.Fatalf("d=%q", d)
	}
	if reason == "" {
		t.Fatalf("사유가 비었다")
	}
}

// 증명: 장치 하나라도 목표와 다르면 commit 하지 않는다(부분 성공을 성공으로 승격 금지).
// 깨는 뮤테이션: 첫 장치만 보고 판정하면 실패한다.
func TestPartialMatchIsNotCommit(t *testing.T) {
	in := recoveryBase()
	in.ObservedGeometry = map[string]string{
		"0000:41:00.0": "1g.6gb x4",
		"0000:81:00.0": "",
	}
	if d, _ := DecideRecovery(in); d != RecoverRollback {
		t.Fatalf("d=%q", d)
	}
}

// 증명: 관측이 안 되면 판정하지 않는다. 관측 없이 rollback 하는 것이 가장 위험하다.
// 깨는 뮤테이션: ObservationOK 검사를 빼면 관측 실패가 곧바로 rollback 이 되어 실패한다.
func TestUnobservableMeansWait(t *testing.T) {
	in := recoveryBase()
	in.ObservationOK = false
	if d, _ := DecideRecovery(in); d != RecoverWait {
		t.Fatalf("d=%q", d)
	}
}

// 증명: 관측 실패가 상한을 넘으면 사람에게 넘긴다(영원히 기다리지 않는다).
// 깨는 뮤테이션: 상한 비교를 빼면 무한 대기라 실패한다.
func TestObserveFailureLimitEscalatesToManual(t *testing.T) {
	in := recoveryBase()
	in.ObservationOK = false
	in.ObserveFailures = 5
	if d, _ := DecideRecovery(in); d != RecoverManual {
		t.Fatalf("d=%q", d)
	}
}

// 증명: 상한이 0(미설정)이면 승격하지 않고 기다린다 — 상한을 안 준 호출자가 첫 관측 실패에
// 곧바로 사람을 부르는 것을 막는다.
// 깨는 뮤테이션: MaxObserveFailures > 0 조건을 빼면 0 >= 0 이 참이라 Manual 로 새어 실패한다.
func TestZeroLimitDoesNotEscalate(t *testing.T) {
	in := recoveryBase()
	in.ObservationOK = false
	in.MaxObserveFailures = 0
	if d, _ := DecideRecovery(in); d != RecoverWait {
		t.Fatalf("d=%q", d)
	}
}

// 증명: 기대 geometry 가 없는 작업(공유 전용 등)은 관측으로 commit 을 주장하지 않는다.
// 관측값도 비워야 이 규칙을 실제로 고정한다 — 관측이 기대와 다르면 어차피 rollback 이 나와
// 빈 기대 검사를 지워도 테스트가 통과해 버린다(그러면 아무것도 증명하지 못한다).
// 깨는 뮤테이션: 빈 기대 검사를 빼면 "" == "" 로 전부 일치가 되어 근거 없는 commit 이 나온다.
func TestEmptyExpectationDoesNotCommit(t *testing.T) {
	in := recoveryBase()
	in.ExpectedGeometry = ""
	in.ObservedGeometry = map[string]string{"0000:41:00.0": ""}
	if d, _ := DecideRecovery(in); d != RecoverRollback {
		t.Fatalf("기대가 없는데 commit 했다: d=%q", d)
	}
}
