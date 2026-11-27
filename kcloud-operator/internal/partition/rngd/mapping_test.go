// ============================================================
// mapping_test.go: RNGD profile↔backendPolicy 매핑·validation 단위 테스트
// 생성일: 2026-07-23
// ============================================================
package rngd

import (
	"errors"
	"testing"
)

func TestResolveProfile_Table(t *testing.T) {
	cases := []struct {
		profile, policy string
		count           int32
		support         string
	}{
		{"2core.12gb", "dual-core", 4, "Verified"},
		{"4core.24gb", "quad-core", 2, "Verified"},
		{"1core.6gb", "single-core", 8, "LegacyDocumented"},
	}
	for _, c := range cases {
		got, ok := ResolveProfile(c.profile)
		if !ok {
			t.Fatalf("profile %q not resolved", c.profile)
		}
		if got.BackendPolicy != c.policy || got.CountPerDevice != c.count || got.SupportLevel != c.support {
			t.Errorf("%q → %+v, want policy=%s count=%d support=%s", c.profile, got, c.policy, c.count, c.support)
		}
	}
}

func TestResolvePolicy_Reverse(t *testing.T) {
	got, ok := ResolvePolicy("dual-core")
	if !ok || got.Profile != "2core.12gb" {
		t.Fatalf("dual-core reverse: %+v ok=%v", got, ok)
	}
	// 미지 policy 는 (zero, false) 반환 — false 브랜치 검증.
	if _, ok := ResolvePolicy("octa-core"); ok {
		t.Error("unknown policy should return ok=false")
	}
}

func TestValidateLayout(t *testing.T) {
	// Verified: 통과 (count 생략)
	if err := ValidateLayout("2core.12gb", 0); err != nil {
		t.Errorf("2core.12gb/0 should pass: %v", err)
	}
	// Documented: 통과 (매핑만, 라이브 실증 후속)
	if err := ValidateLayout("4core.24gb", 2); err != nil {
		t.Errorf("4core.24gb/2 should pass: %v", err)
	}
	// LegacyDocumented: 거부
	if err := ValidateLayout("1core.6gb", 8); !errors.Is(err, ErrProfileNotSupported) {
		t.Errorf("1core.6gb should be rejected as ErrProfileNotSupported, got %v", err)
	}
	// 미지 profile: 거부
	if err := ValidateLayout("9core.99gb", 0); !errors.Is(err, ErrProfileUnknown) {
		t.Errorf("unknown profile: got %v", err)
	}
	// count 불일치: 거부 (fixed-profile 은 count 고정)
	if err := ValidateLayout("2core.12gb", 3); !errors.Is(err, ErrCountMismatch) {
		t.Errorf("count mismatch: got %v", err)
	}
}
