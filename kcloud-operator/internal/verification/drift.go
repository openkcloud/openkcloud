// ============================================================
// drift.go: 검증 통과 후 광고 붕괴 감지 (감지 전용 — 되돌리지 않는다)
// 상세: fresh evidence 가 기록한 광고 기준선과 현재 allocatable 을 비교한다. 정상 롤아웃·재부팅 대기·적용
// 중은 억제하고, 붕괴가 유예 시간 이상 지속돼야 확정한다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DriftVerdict 는 광고 감시 판정이다.
type DriftVerdict string

const (
	// DriftNone: 광고가 기준선을 충족하거나, 비교할 기준선이 없다.
	DriftNone DriftVerdict = "None"
	// DriftSuppressed: 지금은 판단하지 않는다(롤아웃·재부팅·적용 중 등). 의심 카운터를 리셋한다.
	DriftSuppressed DriftVerdict = "Suppressed"
	// DriftSuspected: 불일치를 봤지만 아직 유예 시간을 넘지 않은 상태다.
	DriftSuspected DriftVerdict = "Suspected"
	// DriftConfirmed: 불일치가 유예 시간 이상 지속되는 상태다.
	DriftConfirmed DriftVerdict = "Confirmed"
)

// DriftInput 은 판정에 필요한 값 전부다. 수집은 호출자가 한다.
type DriftInput struct {
	// Expected 는 검증 통과 시점에 evidence 가 기록한 광고량이다(그 자리에서 재계산한 값이 아니다).
	Expected map[string]int32
	Actual   map[string]int32

	RolloutInProgress bool
	NodeReady         bool
	ApplyInFlight     bool
	Terminating       bool
	// EvidenceFresh 가 false 면 비교 기준이 없다 — 판정하지 않는다.
	EvidenceFresh bool

	// FirstSuspectedAt 은 이 불일치를 처음 본 시각이다(nil = 이번이 처음).
	FirstSuspectedAt *metav1.Time
	// AlreadyConfirmed 는 직전 pass 가 이미 확정 판정을 내렸는지다. 확정 뒤에는 유예를 다시 재지
	// 않는다 — 확정 상태에는 "의심 시작 시각" 이 없어서, 다시 재면 매 pass 마다 의심으로
	// 되돌아가 확정이 1초도 유지되지 않는다(Ready↔Degraded 무한 진동).
	AlreadyConfirmed bool
	Now              metav1.Time
	Grace            time.Duration
}

// EvaluateDrift 는 광고 붕괴 여부를 판정하고 사유를 함께 돌려준다.
//
// 순서가 계약이다: 기준선 없음 → 억제 → 일치 → 디바운스. 억제를 일치보다 먼저 보는 이유는
// 억제 상황에서는 일치·불일치 어느 쪽도 의미가 없기 때문이다.
func EvaluateDrift(in DriftInput) (DriftVerdict, string) {
	if !in.EvidenceFresh || len(in.Expected) == 0 {
		return DriftNone, ""
	}
	if reason := suppressionReason(in); reason != "" {
		return DriftSuppressed, reason
	}
	missing := shortfalls(in.Expected, in.Actual)
	if len(missing) == 0 {
		return DriftNone, ""
	}
	detail := strings.Join(missing, ", ")
	if in.AlreadyConfirmed {
		return DriftConfirmed, "advertisement below verified baseline: " + detail
	}
	if in.FirstSuspectedAt == nil {
		return DriftSuspected, "advertisement below verified baseline: " + detail
	}
	// 경계는 확정으로 본다. Grace=0 이어도 첫 관측만으로는 확정되지 않는다 —
	// FirstSuspectedAt 이 nil 인 pass 는 위에서 이미 Suspected 로 빠져나갔기 때문이다.
	if in.Now.Sub(in.FirstSuspectedAt.Time) >= in.Grace {
		return DriftConfirmed, fmt.Sprintf("advertisement below verified baseline for %s: %s",
			in.Now.Sub(in.FirstSuspectedAt.Time).Round(time.Second), detail)
	}
	return DriftSuspected, "advertisement below verified baseline: " + detail
}

func suppressionReason(in DriftInput) string {
	switch {
	case in.RolloutInProgress:
		return "device plugin rollout in progress"
	case !in.NodeReady:
		return "node is not Ready"
	case in.ApplyInFlight:
		return "an apply is in flight on this node"
	case in.Terminating:
		return "the policy or node is terminating"
	}
	return ""
}

// shortfalls 는 기준선에 못 미치는 자원을 사람이 읽을 문장으로 돌려준다(정렬 — status 안정성).
func shortfalls(expected, actual map[string]int32) []string {
	var out []string
	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want := expected[k]
		if want <= 0 {
			continue
		}
		if got := actual[k]; got < want {
			out = append(out, fmt.Sprintf("%s=%d (expected %d)", k, got, want))
		}
	}
	return out
}
