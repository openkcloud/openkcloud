// ============================================================
// freshness_test.go: evidence TTL·무효화 판정 테스트
// 상세: 만료, 지문 변화(감시 축만), 부재, 등급 없음 네 갈래를 고정한다.
// 생성일: 2026-07-31
// ============================================================
package verification

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

func evidenceAt(level string, observed time.Time, ttl time.Duration, fp v1alpha1.EnvironmentFingerprint) *v1alpha1.AcceleratorEvidence {
	exp := metav1.NewTime(observed.Add(ttl))
	return &v1alpha1.AcceleratorEvidence{
		Status: v1alpha1.AcceleratorEvidenceStatus{
			Level:       level,
			ObservedAt:  metav1.NewTime(observed),
			ExpiresAt:   &exp,
			Fingerprint: fp,
		},
	}
}

var baseFP = v1alpha1.EnvironmentFingerprint{
	BootID: "b1", KernelVersion: "k1", DriverVersion: "d1", FirmwareVersion: "f1", Generation: 4,
}

var allAxes = []string{FieldBootID, FieldDriverVersion, FieldFirmwareVersion, FieldGeneration, FieldKernelVersion}

func TestAbsentEvidenceIsNotFresh(t *testing.T) {
	v, _ := CheckFreshness(nil, time.Now(), baseFP, allAxes)
	if v != FreshAbsent || IsFresh(v) {
		t.Fatalf("verdict = %q", v)
	}
}

func TestEvidenceWithoutLevelIsNotFresh(t *testing.T) {
	now := time.Now()
	ev := evidenceAt("", now, time.Hour, baseFP)
	v, _ := CheckFreshness(ev, now, baseFP, allAxes)
	if v != FreshNoLevel || IsFresh(v) {
		t.Fatalf("verdict = %q — 등급 없는 근거를 신선하다고 판정하면 안 된다", v)
	}
}

func TestValidWithinTTL(t *testing.T) {
	now := time.Now()
	ev := evidenceAt(v1alpha1.EvidenceLevelFunctionallyVerified, now, 30*time.Minute, baseFP)
	v, _ := CheckFreshness(ev, now.Add(29*time.Minute), baseFP, allAxes)
	if v != FreshValid || !IsFresh(v) {
		t.Fatalf("verdict = %q", v)
	}
}

func TestExpiredAtBoundary(t *testing.T) {
	now := time.Now()
	ev := evidenceAt(v1alpha1.EvidenceLevelFunctionallyVerified, now, 30*time.Minute, baseFP)
	// 경계는 만료로 본다 — 근거의 유효기간이 끝난 그 순간부터 믿지 않는다.
	v, _ := CheckFreshness(ev, now.Add(30*time.Minute), baseFP, allAxes)
	if v != FreshExpired {
		t.Fatalf("verdict = %q", v)
	}
}

func TestBootIDChangeInvalidates(t *testing.T) {
	now := time.Now()
	ev := evidenceAt(v1alpha1.EvidenceLevelAllocationVerified, now, time.Hour, baseFP)
	cur := baseFP
	cur.BootID = "b2"
	v, reason := CheckFreshness(ev, now, cur, allAxes)
	if v != FreshEnvironmentChanged {
		t.Fatalf("verdict = %q", v)
	}
	if reason == "" || !contains(reason, FieldBootID) {
		t.Fatalf("사유에 바뀐 축이 없다: %q", reason)
	}
}

func TestDriverChangeInvalidates(t *testing.T) {
	now := time.Now()
	ev := evidenceAt(v1alpha1.EvidenceLevelObserved, now, time.Hour, baseFP)
	cur := baseFP
	cur.DriverVersion = "d2"
	if v, _ := CheckFreshness(ev, now, cur, allAxes); v != FreshEnvironmentChanged {
		t.Fatalf("verdict = %q", v)
	}
}

func TestUnwatchedAxisDoesNotInvalidate(t *testing.T) {
	// 정책이 firmware 를 감시하지 않으면 firmware 가 바뀌어도 근거는 살아 있다.
	now := time.Now()
	ev := evidenceAt(v1alpha1.EvidenceLevelObserved, now, time.Hour, baseFP)
	cur := baseFP
	cur.FirmwareVersion = "f2"
	if v, _ := CheckFreshness(ev, now, cur, []string{FieldBootID, FieldDriverVersion}); v != FreshValid {
		t.Fatalf("감시하지 않는 축의 변화로 무효화했다: %q", v)
	}
}

func TestMissingExpiryIsExpired(t *testing.T) {
	// expiresAt 이 없는 근거는 만료 시각을 모른다 — 모르면 못 믿는다(fail-closed).
	ev := &v1alpha1.AcceleratorEvidence{
		Status: v1alpha1.AcceleratorEvidenceStatus{
			Level: v1alpha1.EvidenceLevelObserved, Fingerprint: baseFP,
		},
	}
	if v, _ := CheckFreshness(ev, time.Now(), baseFP, allAxes); v != FreshExpired {
		t.Fatalf("verdict = %q", v)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
