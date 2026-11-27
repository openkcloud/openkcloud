// ============================================================
// drift_test.go: 광고 붕괴 감지 판정 테스트
// 상세: 오탐 방향(정상 롤아웃·재부팅 대기·적용 중)을 먼저 고정하고, 그 다음 진짜 붕괴가
// 유예 시간 뒤에 확정되는지를 본다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func healthyInput(now time.Time) DriftInput {
	return DriftInput{
		Expected:      map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		Actual:        map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		NodeReady:     true,
		EvidenceFresh: true,
		Now:           metav1.NewTime(now),
		Grace:         30 * time.Second,
	}
}

func collapsedInput(now time.Time) DriftInput {
	in := healthyInput(now)
	in.Actual = map[string]int32{}
	return in
}

func TestNoDriftWhenAdvertisementMatches(t *testing.T) {
	if v, _ := EvaluateDrift(healthyInput(time.Now())); v != DriftNone {
		t.Fatalf("verdict = %q", v)
	}
}

func TestSurplusAdvertisementIsNotDrift(t *testing.T) {
	in := healthyInput(time.Now())
	in.Actual["nvidia.com/mig-1g.6gb"] = 8
	if v, _ := EvaluateDrift(in); v != DriftNone {
		t.Fatalf("초과 광고를 붕괴로 판정: %q", v)
	}
}

func TestRolloutSuppressesDrift(t *testing.T) {
	// 이 검사가 핵심이다 — 정상 DaemonSet 롤아웃 중 광고 공백은 장애가 아니다.
	now := time.Now()
	in := collapsedInput(now)
	in.RolloutInProgress = true
	// 이미 의심 중이었더라도 롤아웃을 보면 억제로 내려가 카운터가 리셋돼야 한다.
	suspected := metav1.NewTime(now.Add(-5 * time.Minute))
	in.FirstSuspectedAt = &suspected
	v, reason := EvaluateDrift(in)
	if v != DriftSuppressed {
		t.Fatalf("verdict = %q (%s)", v, reason)
	}
}

func TestNodeNotReadySuppressesDrift(t *testing.T) {
	in := collapsedInput(time.Now())
	in.NodeReady = false
	if v, _ := EvaluateDrift(in); v != DriftSuppressed {
		t.Fatalf("verdict = %q", v)
	}
}

func TestApplyInFlightSuppressesDrift(t *testing.T) {
	in := collapsedInput(time.Now())
	in.ApplyInFlight = true
	if v, _ := EvaluateDrift(in); v != DriftSuppressed {
		t.Fatalf("verdict = %q", v)
	}
}

func TestTerminatingSuppressesDrift(t *testing.T) {
	in := collapsedInput(time.Now())
	in.Terminating = true
	if v, _ := EvaluateDrift(in); v != DriftSuppressed {
		t.Fatalf("verdict = %q", v)
	}
}

func TestStaleEvidenceYieldsNoVerdict(t *testing.T) {
	// 비교할 기준선이 낡았으면 아무 말도 하지 않는다 — 낡은 기준으로 장애를 선언하지 않는다.
	in := collapsedInput(time.Now())
	in.EvidenceFresh = false
	if v, _ := EvaluateDrift(in); v != DriftNone {
		t.Fatalf("verdict = %q", v)
	}
}

func TestEmptyExpectationYieldsNoVerdict(t *testing.T) {
	in := collapsedInput(time.Now())
	in.Expected = map[string]int32{}
	if v, _ := EvaluateDrift(in); v != DriftNone {
		t.Fatalf("verdict = %q", v)
	}
}

func TestFirstCollapseIsOnlySuspected(t *testing.T) {
	v, reason := EvaluateDrift(collapsedInput(time.Now()))
	if v != DriftSuspected {
		t.Fatalf("verdict = %q", v)
	}
	if reason == "" {
		t.Fatalf("사유가 비었다")
	}
}

func TestStillSuspectedInsideGrace(t *testing.T) {
	now := time.Now()
	in := collapsedInput(now)
	first := metav1.NewTime(now.Add(-29 * time.Second))
	in.FirstSuspectedAt = &first
	if v, _ := EvaluateDrift(in); v != DriftSuspected {
		t.Fatalf("verdict = %q", v)
	}
}

func TestConfirmedAtGraceBoundary(t *testing.T) {
	now := time.Now()
	in := collapsedInput(now)
	first := metav1.NewTime(now.Add(-30 * time.Second))
	in.FirstSuspectedAt = &first
	v, reason := EvaluateDrift(in)
	if v != DriftConfirmed {
		t.Fatalf("verdict = %q (%s)", v, reason)
	}
	if reason == "" {
		t.Fatalf("확정 사유가 비었다")
	}
}

func TestRecoveryClearsImmediatelyEvenAfterLongSuspicion(t *testing.T) {
	// 켜는 데는 지속성을 요구하고 끄는 데는 요구하지 않는다(비대칭 히스테리시스).
	now := time.Now()
	in := healthyInput(now)
	first := metav1.NewTime(now.Add(-10 * time.Minute))
	in.FirstSuspectedAt = &first
	if v, _ := EvaluateDrift(in); v != DriftNone {
		t.Fatalf("회복했는데 verdict = %q", v)
	}
}

func TestZeroGraceStillRequiresASecondObservation(t *testing.T) {
	// grace 를 0 으로 둬도 첫 관측만으로 확정하지 않는다 — 단발 관측 확정은 informer 지연 한 번에
	// 오탐이 된다.
	in := collapsedInput(time.Now())
	in.Grace = 0
	if v, _ := EvaluateDrift(in); v != DriftSuspected {
		t.Fatalf("verdict = %q", v)
	}
}

// TestConfirmedDriftStaysConfirmed 는 확정 판정이 다음 pass 에서 의심으로 되돌아가지 않는지
// 고정한다. 되돌아가면 확정 상태에는 의심 시작 시각이 없어 유예가 매번 처음부터 재어지고,
// 정책이 Ready 와 Degraded 사이를 초 단위로 진동한다.
func TestConfirmedDriftStaysConfirmed(t *testing.T) {
	now := metav1.Now()
	in := DriftInput{
		Expected:         map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		Actual:           map[string]int32{"nvidia.com/mig-1g.6gb": 0},
		NodeReady:        true,
		EvidenceFresh:    true,
		AlreadyConfirmed: true,
		// 확정 직후라 경과 시간은 0 이다 — 유예를 다시 재면 의심으로 떨어진다.
		FirstSuspectedAt: &now,
		Now:              now,
		Grace:            30 * time.Second,
	}
	got, reason := EvaluateDrift(in)
	if got != DriftConfirmed {
		t.Fatalf("verdict = %v (%s), want DriftConfirmed", got, reason)
	}
}
