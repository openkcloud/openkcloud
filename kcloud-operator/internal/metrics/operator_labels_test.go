// ============================================================
// operator_labels_test.go: HealthVerdicts 레이블 목록이 internal/health 판정 상수와 일치하는지 대조
// 상세: metrics 는 health 를 import 할 수 없다(순환: health→internal/operation→internal/metrics).
//       그래서 외부 테스트 패키지에서 health 를 대조해 드리프트를 막는다.
// 생성일: 2026-09-22 | 수정일: 2026-09-22
// ============================================================

package metrics_test

import (
	"testing"

	"kcloud-operator/internal/health"
	"kcloud-operator/internal/metrics"
)

// TestHealthVerdictsMatchHealthPackage 는 metrics.HealthVerdicts 가 internal/health 의 판정
// 상수와 정확히 같은 집합인지 본다. 값이 바뀌거나(예: "Healthy"→"OK") 누락되면 실패한다.
func TestHealthVerdictsMatchHealthPackage(t *testing.T) {
	want := map[string]bool{
		health.StateHealthy:     true,
		health.StateDegraded:    true,
		health.StateUnhealthy:   true,
		health.StateUnknown:     true,
		health.StateQuarantined: true,
		health.StateRecovering:  true,
	}
	got := map[string]bool{}
	for _, v := range metrics.HealthVerdicts {
		got[v] = true
	}
	for s := range want {
		if !got[s] {
			t.Errorf("HealthVerdicts 에 health.State %q 가 없다 — operator.go 갱신 필요", s)
		}
	}
	for v := range got {
		if !want[v] {
			t.Errorf("HealthVerdicts 의 %q 는 internal/health 에 없는 값이다", v)
		}
	}
}
