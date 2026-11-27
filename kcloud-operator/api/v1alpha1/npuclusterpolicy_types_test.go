// ============================================================
// npuclusterpolicy_types_test.go: NPUClusterPolicy 타입의 기본값 해석 시험
// 생성일: 2026-08-07 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import "testing"

// nil DRASpec 은 "안 켰다" 여야 한다. 이 필드를 모르는 기존 정책이 지금까지와
// 똑같이 동작해야 하므로, nil 을 켜짐으로 읽으면 조용히 드라이버가 깔린다.
func TestDRASpecIsEnabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec *DRASpec
		want bool
	}{
		{"nil 은 꺼짐", nil, false},
		{"빈 구조체는 꺼짐", &DRASpec{}, false},
		{"명시적 켜짐", &DRASpec{Enabled: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.IsEnabled(); got != tc.want {
				t.Errorf("IsEnabled()=%v, want %v", got, tc.want)
			}
		})
	}
}

// 벤더 블록마다 DRA 축이 실제로 붙어 있는지 본다. 필드를 추가하면서 한 곳을
// 빠뜨리는 것이 이 작업에서 실제로 일어날 수 있는 누락이다.
func TestDRASpecInlinedInVendorBlocks(t *testing.T) {
	p := NPUClusterPolicy{}
	p.Spec.Nvidia.DRA = &DRASpec{Enabled: true}
	p.Spec.Furiosa.DRA = &DRASpec{Enabled: true}
	p.Spec.Furiosa.Rngd.DRA = &DRASpec{Enabled: true}
	p.Spec.Tenstorrent.DRA = &DRASpec{Enabled: true}

	for name, s := range map[string]*DRASpec{
		"nvidia":       p.Spec.Nvidia.DRA,
		"furiosa":      p.Spec.Furiosa.DRA,
		"furiosa.rngd": p.Spec.Furiosa.Rngd.DRA,
		"tenstorrent":  p.Spec.Tenstorrent.DRA,
	} {
		if !s.IsEnabled() {
			t.Errorf("%s 블록의 DRA 축이 동작하지 않는다", name)
		}
	}
}

// 세 모드가 서로 배타적인지 본다. 두 도우미가 동시에 참이면 배포 판정이 갈린다.
func TestManagementModeThreeValues(t *testing.T) {
	cases := []struct {
		mode      string
		owned     bool
		delegated bool
		external  bool
	}{
		{"", true, false, false}, // 미지정 = managed
		{"managed", true, false, false},
		{"delegated", false, true, false},
		{"external", false, false, true},
	}
	for _, c := range cases {
		m := ManagementSpec{ManagementMode: c.mode}
		if got := m.IsOperatorOwned(); got != c.owned {
			t.Errorf("mode %q IsOperatorOwned = %v, want %v", c.mode, got, c.owned)
		}
		if got := m.IsDelegated(); got != c.delegated {
			t.Errorf("mode %q IsDelegated = %v, want %v", c.mode, got, c.delegated)
		}
		if got := m.IsExternal(); got != c.external {
			t.Errorf("mode %q IsExternal = %v, want %v", c.mode, got, c.external)
		}
	}
}
