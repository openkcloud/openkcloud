// ============================================================
// naming_test.go: DriverDSName 단위 테스트
// 상세: 드라이버 DaemonSet 이름 생성 규칙 — 벤더/모델 매핑 테이블, 빈 모델 안전성,
//       대소문자 정규화(내부 lower-case 처리) 검증
// 생성일: 2026-06-02
// ============================================================

package naming

import (
	"strings"
	"testing"
)

// TestDriverDSName 는 DriverDSName 의 전체 매핑 테이블을 검증한다.
//
// 매핑 규칙:
//   - nvidia(any model)      → "kcloud-nvidia-driver"
//   - furiosa + warboy       → "kcloud-furiosa-warboy-driver"
//   - furiosa + rngd         → "kcloud-furiosa-rngd-driver"
//   - furiosa + ""           → "kcloud-furiosa-driver" (이중 하이픈 없음)
//   - other + ""             → "kcloud-<vendor>-driver"
//   - other + model          → "kcloud-<vendor>-<model>-driver"
func TestDriverDSName(t *testing.T) {
	cases := []struct {
		name   string
		vendor string
		model  string
		want   string
	}{

		// ── nvidia: model 무시 ──────────────────────────────
		{"nvidia/generic", "nvidia", "generic", "kcloud-nvidia-driver"},
		{"nvidia/empty-model", "nvidia", "", "kcloud-nvidia-driver"},
		{"nvidia/other-model", "nvidia", "a100", "kcloud-nvidia-driver"},

		// ── furiosa: model 보존 ─────────────────────────────
		{"furiosa/warboy", "furiosa", "warboy", "kcloud-furiosa-warboy-driver"},
		{"furiosa/rngd", "furiosa", "rngd", "kcloud-furiosa-rngd-driver"},
		// 빈 model → 이중 하이픈 없이 단순 형태
		{"furiosa/empty-model no double-hyphen", "furiosa", "", "kcloud-furiosa-driver"},

		// ── default vendor: 빈 model ────────────────────────
		{"rebellions/empty-model", "rebellions", "", "kcloud-rebellions-driver"},
		{"custom-vendor/empty-model", "myvend", "", "kcloud-myvend-driver"},

		// ── default vendor: model 포함 ──────────────────────
		{"rebellions/atom", "rebellions", "atom", "kcloud-rebellions-atom-driver"},
		{"custom/v1", "custom", "v1", "kcloud-custom-v1-driver"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DriverDSName(tc.vendor, tc.model)
			if got != tc.want {
				t.Errorf("DriverDSName(%q, %q) = %q, want %q",
					tc.vendor, tc.model, got, tc.want)
			}
		})
	}
}

// TestDriverDSName_MixedCaseNormalization 는 대소문자 혼합 입력이
// 소문자로 정규화되어 올바른 이름을 반환함을 검증한다.
func TestDriverDSName_MixedCaseNormalization(t *testing.T) {
	cases := []struct {
		name   string
		vendor string
		model  string
		want   string
	}{
		{"NVIDIA/Generic", "NVIDIA", "Generic", "kcloud-nvidia-driver"},
		{"Nvidia/generic", "Nvidia", "generic", "kcloud-nvidia-driver"},
		{"Furiosa/Warboy", "Furiosa", "Warboy", "kcloud-furiosa-warboy-driver"},
		{"FURIOSA/RNGD", "FURIOSA", "RNGD", "kcloud-furiosa-rngd-driver"},
		{"Furiosa/empty", "Furiosa", "", "kcloud-furiosa-driver"},
		{"Rebellions/Atom", "Rebellions", "Atom", "kcloud-rebellions-atom-driver"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DriverDSName(tc.vendor, tc.model)
			if got != tc.want {
				t.Errorf("DriverDSName(%q, %q) = %q, want %q",
					tc.vendor, tc.model, got, tc.want)
			}
		})
	}
}

// TestDriverDSName_NoDoubleHyphen 는 빈 model 입력에서 어떤 벤더든
// 이중 하이픈("--")이 결과에 포함되지 않음을 검증한다 (빈 model 안전성).
func TestDriverDSName_NoDoubleHyphen(t *testing.T) {
	vendors := []string{
		"furiosa", "nvidia", "rebellions", "custom",
	}
	for _, vendor := range vendors {
		got := DriverDSName(vendor, "")
		if strings.Contains(got, "--") {
			t.Errorf("DriverDSName(%q, \"\") = %q: 이중 하이픈 포함 (빈 model 처리 버그)",
				vendor, got)
		}
	}
}

// TestDriverDSName_AlwaysHasKcloudPrefix 는 모든 벤더 입력에 대해
// 반환 이름이 "kcloud-" 로 시작함을 검증한다.
func TestDriverDSName_AlwaysHasKcloudPrefix(t *testing.T) {
	pairs := []struct{ vendor, model string }{
		{"nvidia", "generic"},
		{"furiosa", "warboy"},
		{"furiosa", "rngd"},
		{"furiosa", ""},
		{"rebellions", "atom"},
		{"rebellions", ""},
		{"unknown-vendor", ""},
		{"unknown-vendor", "some-model"},
	}
	for _, p := range pairs {
		got := DriverDSName(p.vendor, p.model)
		if !strings.HasPrefix(got, "kcloud-") {
			t.Errorf("DriverDSName(%q, %q) = %q: \"kcloud-\" 접두사 없음",
				p.vendor, p.model, got)
		}
	}
}

// TestDriverDSName_AlwaysHasDriverSuffix 는 모든 입력에 대해
// 반환 이름이 "-driver" 로 끝남을 검증한다.
func TestDriverDSName_AlwaysHasDriverSuffix(t *testing.T) {
	pairs := []struct{ vendor, model string }{
		{"nvidia", ""},
		{"furiosa", "warboy"},
		{"furiosa", ""},
		{"rebellions", "atom"},
	}
	for _, p := range pairs {
		got := DriverDSName(p.vendor, p.model)
		if !strings.HasSuffix(got, "-driver") {
			t.Errorf("DriverDSName(%q, %q) = %q: \"-driver\" 접미사 없음",
				p.vendor, p.model, got)
		}
	}
}
