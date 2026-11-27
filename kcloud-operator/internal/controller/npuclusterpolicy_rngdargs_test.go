// ============================================================
// npuclusterpolicy_rngdargs_test.go: RNGD device plugin 의 binary args 조합을 고정한다.
// 상세: 공개 이미지는 --debugMode·--policy 를 모두 거부하므로 둘 다 끈 조합에서 args 가
// 비어야 한다.
// 생성일: 2026-09-09 | 수정일: 2026-10-11
// ============================================================
package controller

import "testing"

func TestRngdDevicePluginArgs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy string
		debug  *bool
		want   []string
	}{
		{"기본값은 기존 동작을 유지한다", "", nil, []string{"--debugMode"}},
		{"none 정책도 --policy 를 붙이지 않는다", "none", nil, []string{"--debugMode"}},
		{"공개 이미지용: 둘 다 끄면 args 가 비어야 한다", "none", boolPtr(false), nil},
		{"debug 를 꺼도 정책은 살아 있다", "dual-core", boolPtr(false), []string{"--policy=dual-core"}},
		{"둘 다 켜면 순서대로", "quad-core", boolPtr(true), []string{"--debugMode", "--policy=quad-core"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rngdDevicePluginArgs(tc.policy, tc.debug)
			if len(got) != len(tc.want) {
				t.Fatalf("args=%v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("args=%v, want %v", got, tc.want)
				}
			}
		})
	}
}
