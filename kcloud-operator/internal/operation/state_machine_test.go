// ============================================================
// state_machine_test.go: operation 상태 전이표 테스트
// 상세: operation 상태머신 다이어그램과 1:1 대응한다. 다이어그램을 바꾸면 이 테스트도 바꿔야 한다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"os"
	"strings"
	"testing"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// 증명: 설계 문서의 정상 경로가 전이표에 그대로 있다.
// 깨는 뮤테이션: transitions 맵에서 어느 한 줄이라도 지우면 실패한다.
func TestHappyPathTransitions(t *testing.T) {
	steps := []struct {
		from string
		e    Event
		want string
	}{
		{v1alpha1.OpPhasePending, EventPreflightPassed, v1alpha1.OpPhasePlanning},
		{v1alpha1.OpPhasePlanning, EventLeaseAcquired, v1alpha1.OpPhasePrepared},
		{v1alpha1.OpPhasePrepared, EventQuiesced, v1alpha1.OpPhaseQuiescing},
		{v1alpha1.OpPhaseQuiescing, EventApplyStarted, v1alpha1.OpPhaseApplying},
		{v1alpha1.OpPhaseApplying, EventApplyDone, v1alpha1.OpPhaseVerifying},
		{v1alpha1.OpPhaseVerifying, EventVerifyPassed, v1alpha1.OpPhaseSucceeded},
	}
	cur := v1alpha1.OpPhasePending
	for _, s := range steps {
		if cur != s.from {
			t.Fatalf("경로가 어긋났다: cur=%q, 기대 from=%q", cur, s.from)
		}
		got, ok := Next(cur, s.e)
		if !ok || got != s.want {
			t.Fatalf("Next(%q, %q) = (%q, %v), want %q", cur, s.e, got, ok, s.want)
		}
		cur = got
	}
}

// 증명: 재부팅 왕복이 표에 있다(Applying → WaitingForReboot → Applying).
// 깨는 뮤테이션: EventBootObserved 전이를 지우면 재부팅이 낀 작업이 영영 못 돌아온다.
func TestRebootRoundTrip(t *testing.T) {
	if got, ok := Next(v1alpha1.OpPhaseApplying, EventRebootRequired); !ok || got != v1alpha1.OpPhaseWaitingForReboot {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if got, ok := Next(v1alpha1.OpPhaseWaitingForReboot, EventBootObserved); !ok || got != v1alpha1.OpPhaseApplying {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

// 증명: 실패 갈래가 전부 있다.
// 깨는 뮤테이션: EventCompensationFailed 전이를 지우면 보상 실패가 종점 없이 맴돈다.
func TestFailurePaths(t *testing.T) {
	for _, tc := range []struct {
		from string
		e    Event
		want string
	}{
		{v1alpha1.OpPhaseApplying, EventApplyFailed, v1alpha1.OpPhaseRollingBack},
		{v1alpha1.OpPhaseVerifying, EventVerifyFailed, v1alpha1.OpPhaseRollingBack},
		{v1alpha1.OpPhaseRollingBack, EventCompensated, v1alpha1.OpPhaseRolledBack},
		{v1alpha1.OpPhaseRollingBack, EventCompensationFailed, v1alpha1.OpPhaseManualRecoveryRequired},
		{v1alpha1.OpPhasePlanning, EventConflict, v1alpha1.OpPhaseBlocked},
		{v1alpha1.OpPhaseBlocked, EventDependencyResolved, v1alpha1.OpPhasePlanning},
	} {
		if got, ok := Next(tc.from, tc.e); !ok || got != tc.want {
			t.Fatalf("Next(%q, %q) = (%q, %v), want %q", tc.from, tc.e, got, ok, tc.want)
		}
	}
}

// 증명: 종점에서는 아무 데로도 못 간다.
// 깨는 뮤테이션: IsTerminal 을 항상 false 로 만들거나 종점에 전이를 추가하면 실패한다.
func TestTerminalPhasesAreAbsorbing(t *testing.T) {
	for _, p := range []string{v1alpha1.OpPhaseSucceeded, v1alpha1.OpPhaseRolledBack, v1alpha1.OpPhaseManualRecoveryRequired} {
		if !IsTerminal(p) {
			t.Fatalf("%q 가 종점이 아니다", p)
		}
		for _, e := range AllEvents() {
			if _, ok := Next(p, e); ok {
				t.Fatalf("종점 %q 에서 %q 로 전이가 열려 있다", p, e)
			}
		}
	}
}

// 증명: 설계에 없는 전이는 거부된다(예: 검증도 안 하고 성공).
// 깨는 뮤테이션: Next 를 "모르면 통과" 로 바꾸면 실패한다.
func TestUndefinedTransitionIsRejected(t *testing.T) {
	if got, ok := Next(v1alpha1.OpPhaseApplying, EventVerifyPassed); ok {
		t.Fatalf("적용 중에서 바로 검증 통과로 갔다: %q", got)
	}
	if _, ok := Next("NotAPhase", EventApplyDone); ok {
		t.Fatalf("존재하지 않는 phase 에서 전이가 열렸다")
	}
}

// 증명: Go 상수 목록과 CRD enum 이 갈라지지 않는다.
// 깨는 뮤테이션: phase 상수를 추가하고 kubebuilder enum 마커를 안 고치면 실패한다.
func TestCRDEnumMatchesPhaseConstants(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/npu.ai_acceleratoroperations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	crd := string(raw)
	for _, p := range v1alpha1.AllOperationPhases() {
		if !strings.Contains(crd, "- "+p+"\n") {
			t.Fatalf("CRD enum 에 phase %q 가 없다", p)
		}
	}
	// 반대 방향: 전이표가 모르는 phase 가 상수 목록에 있으면 잡는다.
	for _, p := range v1alpha1.AllOperationPhases() {
		if !IsTerminal(p) && !hasAnyTransition(p) {
			t.Fatalf("phase %q 에서 나가는 전이가 하나도 없다(비종점인데 막다른 길)", p)
		}
	}
}
