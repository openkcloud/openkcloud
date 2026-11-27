// ============================================================
// chart_management_mode_test.go: managementMode 세 값(managed·delegated·external)의
// 차트 렌더 결과를 고정한다.
// 상세: 세 값 모두 템플릿의 enum 검사에서 거절되면 안 된다. furiosa-exporter DaemonSet 은
// 차트가 직접 만드는 유일한 벤더 operand 라(나머지 device-plugin 은 컨트롤러가 런타임에
// 만든다) managementMode 게이트가 여기 하나 더 있다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"strings"
	"testing"
)

// 네 벤더 모두 managementMode 를 CR 에 그대로 반영해야 하고, 세 값 어느 것도 템플릿의
// enum 검사에서 거절되면 안 된다.
func TestChartRendersThreeManagementModes(t *testing.T) {
	for _, vendor := range []string{"nvidia", "furiosa", "rebellions", "tenstorrent"} {
		for _, mode := range []string{"managed", "delegated", "external"} {
			t.Run(vendor+"/"+mode, func(t *testing.T) {
				got := helmTemplate(t, "--set", vendor+".managementMode="+mode)
				want := `managementMode: "` + mode + `"`
				if !strings.Contains(got, want) {
					t.Errorf("%s 렌더에 %q 가 없다 — managementMode 가 policy 에 반영되지 않았다", vendor, want)
				}
			})
		}
	}
}

// furiosa-exporter 는 차트가 직접 만드는 유일한 벤더 operand 다. managementMode 가 managed 가
// 아니면(delegated·external) kcloud 가 배포하지 않으므로 exporter.enabled=true 여도 렌더되지
// 않아야 한다.
func TestChartFuriosaExporterRespectsManagementMode(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		wantRender bool
	}{
		{"managed", true},
		{"delegated", false},
		{"external", false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			got := helmTemplate(t, "--set", "furiosa.exporter.enabled=true", "--set", "furiosa.managementMode="+tc.mode)
			has := strings.Contains(got, "name: kcloud-furiosa-exporter")
			if has != tc.wantRender {
				t.Errorf("mode %s: furiosa-exporter 렌더 = %v, want %v", tc.mode, has, tc.wantRender)
			}
		})
	}
}
