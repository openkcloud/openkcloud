// ============================================================
// freshness.go: evidence TTL·무효화 판정
// 상세: 근거는 시간이 지나면 만료되고(expiresAt), 감시 축의 환경이 바뀌면 그 자리에서 죽는다.
// 판정 불가는 전부 "못 믿는다" 쪽으로 떨어뜨린다(fail-closed).
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"fmt"
	"strings"
	"time"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// 신선도 판정값.
const (
	FreshValid              = "Valid"
	FreshExpired            = "Expired"
	FreshEnvironmentChanged = "EnvironmentChanged"
	FreshAbsent             = "Absent"
	FreshNoLevel            = "NoLevel"
)

// IsFresh 는 이 판정으로 근거를 재사용해도 되는지다.
func IsFresh(verdict string) bool { return verdict == FreshValid }

// CheckFreshness 는 근거를 지금 재사용해도 되는지 판정하고 사유를 함께 돌려준다. 순서가 의미를
// 가진다: 부재 → 등급 없음 → 환경 변화 → 만료. 환경 변화를 만료보다 먼저 보는 이유는 사유의
// 유용성이다 — 재부팅 직후 만료도 함께 왔다면 운영자가 알아야 할 것은 "낡음" 이 아니라 "재부팅" 이다.
// invalidateOn 이 비어 있으면 어떤 축도 감시하지 않는다(환경 변화로는 무효화되지 않는다). 감시 축
// 선택은 AcceleratorVerificationPolicy 의 몫이고 여기서 기본값을 끼워 넣지 않는다.
func CheckFreshness(ev *v1alpha1.AcceleratorEvidence, now time.Time, cur v1alpha1.EnvironmentFingerprint, invalidateOn []string) (string, string) {
	if ev == nil {
		return FreshAbsent, "no evidence recorded for this node"
	}
	if ev.Status.Level == "" {
		return FreshNoLevel, "evidence has no level (a previous verification did not pass)"
	}
	if changed := watchedChanges(ev.Status.Fingerprint, cur, invalidateOn); len(changed) > 0 {
		return FreshEnvironmentChanged, fmt.Sprintf("environment changed since verification: %s", strings.Join(changed, ", "))
	}
	if ev.Status.ExpiresAt == nil {
		// 만료 시각을 모르는 근거는 언제까지 유효한지 말할 수 없다 — 못 믿는 쪽으로 떨어뜨린다.
		return FreshExpired, "evidence has no expiry"
	}
	if !now.Before(ev.Status.ExpiresAt.Time) {
		return FreshExpired, fmt.Sprintf("evidence expired at %s", ev.Status.ExpiresAt.Time.UTC().Format(time.RFC3339))
	}
	return FreshValid, ""
}

// watchedChanges 는 감시 대상 축 중 실제로 바뀐 것만 남긴다.
func watchedChanges(old, cur v1alpha1.EnvironmentFingerprint, invalidateOn []string) []string {
	if len(invalidateOn) == 0 {
		return nil
	}
	watched := make(map[string]struct{}, len(invalidateOn))
	for _, f := range invalidateOn {
		watched[f] = struct{}{}
	}
	var out []string
	for _, f := range ChangedFields(old, cur) {
		if _, ok := watched[f]; ok {
			out = append(out, f)
		}
	}
	return out
}
