// ============================================================
// state_machine.go: operation 상태 전이표
// 상세: operation 상태머신의 코드 정본이다. 표에 없는 전이는 전부 거부한다 — "모르면 통과" 로
// 두면 검증을 건너뛴 성공이 상태머신 자체에서 새어 나온다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import v1alpha1 "kcloud-operator/api/v1alpha1"

// Event 는 상태를 옮기는 사건이다.
type Event string

const (
	EventConflict           Event = "Conflict"
	EventPreflightPassed    Event = "PreflightPassed"
	EventLeaseAcquired      Event = "LeaseAcquired"
	EventQuiesced           Event = "Quiesced"
	EventApplyStarted       Event = "ApplyStarted"
	EventRebootRequired     Event = "RebootRequired"
	EventBootObserved       Event = "BootObserved"
	EventApplyDone          Event = "ApplyDone"
	EventApplyFailed        Event = "ApplyFailed"
	EventVerifyPassed       Event = "VerifyPassed"
	EventVerifyFailed       Event = "VerifyFailed"
	EventCompensated        Event = "Compensated"
	EventCompensationFailed Event = "CompensationFailed"
	EventDependencyResolved Event = "DependencyResolved"
)

// AllEvents 는 종점 흡수성 테스트가 전 사건을 훑을 수 있게 한다.
func AllEvents() []Event {
	return []Event{EventConflict, EventPreflightPassed, EventLeaseAcquired, EventQuiesced,
		EventApplyStarted, EventRebootRequired, EventBootObserved, EventApplyDone,
		EventApplyFailed, EventVerifyPassed, EventVerifyFailed, EventCompensated,
		EventCompensationFailed, EventDependencyResolved}
}

// transitions 는 (현재 phase, 사건) → 다음 phase 다. 여기 없는 조합은 전이가 아니다.
var transitions = map[string]map[Event]string{
	v1alpha1.OpPhasePending: {
		EventPreflightPassed: v1alpha1.OpPhasePlanning,
		EventConflict:        v1alpha1.OpPhaseBlocked,
	},
	v1alpha1.OpPhasePlanning: {
		EventConflict:      v1alpha1.OpPhaseBlocked,
		EventLeaseAcquired: v1alpha1.OpPhasePrepared,
	},
	v1alpha1.OpPhasePrepared: {
		EventQuiesced:    v1alpha1.OpPhaseQuiescing,
		EventApplyFailed: v1alpha1.OpPhaseRollingBack,
	},
	v1alpha1.OpPhaseQuiescing: {
		EventApplyStarted: v1alpha1.OpPhaseApplying,
		EventApplyFailed:  v1alpha1.OpPhaseRollingBack,
	},
	v1alpha1.OpPhaseApplying: {
		EventRebootRequired: v1alpha1.OpPhaseWaitingForReboot,
		EventApplyDone:      v1alpha1.OpPhaseVerifying,
		EventApplyFailed:    v1alpha1.OpPhaseRollingBack,
	},
	v1alpha1.OpPhaseWaitingForReboot: {
		EventBootObserved: v1alpha1.OpPhaseApplying,
		EventApplyFailed:  v1alpha1.OpPhaseRollingBack,
	},
	v1alpha1.OpPhaseVerifying: {
		EventVerifyPassed: v1alpha1.OpPhaseSucceeded,
		EventVerifyFailed: v1alpha1.OpPhaseRollingBack,
	},
	v1alpha1.OpPhaseRollingBack: {
		EventCompensated:        v1alpha1.OpPhaseRolledBack,
		EventCompensationFailed: v1alpha1.OpPhaseManualRecoveryRequired,
	},
	v1alpha1.OpPhaseBlocked: {
		EventDependencyResolved: v1alpha1.OpPhasePlanning,
	},
}

// Next 는 전이 결과와 그 전이가 정의돼 있는지를 돌려준다.
func Next(from string, e Event) (string, bool) {
	to, ok := transitions[from][e]
	return to, ok
}

// IsTerminal 은 흡수 상태인지다. 종점에서는 어떤 사건으로도 나가지 않는다.
func IsTerminal(phase string) bool {
	switch phase {
	case v1alpha1.OpPhaseSucceeded, v1alpha1.OpPhaseRolledBack, v1alpha1.OpPhaseManualRecoveryRequired:
		return true
	}
	return false
}

// hasAnyTransition 은 비종점 phase 가 막다른 길이 아닌지 테스트가 확인할 때 쓴다.
func hasAnyTransition(phase string) bool { return len(transitions[phase]) > 0 }
