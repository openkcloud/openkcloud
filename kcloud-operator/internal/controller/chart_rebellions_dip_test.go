// ============================================================
// chart_rebellions_dip_test.go: Rebellions TrackOnly DriverInstallPolicy 렌더를 고정한다.
// 상세: 설치기 이미지가 없는 벤더라 image 줄 없이 trackOnly=true·mode=job·versionSource=Host 로 렌더돼야
// 하고, enabled=false 면 사라지며, version 을 주면 그대로 실린다. trackOnly 를 끄는 값은 스키마가 거절한다.
// 생성일: 2026-10-27 | 수정일: 2026-10-27
// ============================================================
package controller

import (
	"strings"
	"testing"
)

// rebellionsDIPBlock 은 렌더 출력에서 rebellions-atom-ds 문서 하나를 잘라 낸다.
func rebellionsDIPBlock(t *testing.T, rendered string) string {
	t.Helper()
	idx := strings.Index(rendered, "name: rebellions-atom-ds")
	if idx < 0 {
		t.Fatalf("rebellions-atom-ds 가 렌더되지 않았다")
	}
	block := rendered[idx:]
	if end := strings.Index(block, "\n---"); end >= 0 {
		block = block[:end]
	}
	return block
}

func TestChartRebellionsDIPIsTrackOnlyWithoutImage(t *testing.T) {
	block := rebellionsDIPBlock(t, helmTemplate(t))
	for _, want := range []string{
		"vendor: rebellions", "model: atom", "mode: job", "trackOnly: true",
		`versionSource: "Host"`, `kcloud.ai/rebellions.present: "true"`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("rebellions 정책에 %q 가 없다:\n%s", want, block)
		}
	}
	for _, forbid := range []string{"image:", "\n    version:", "kernelImages", "upgradePolicy", "workloadProbe"} {
		if strings.Contains(block, forbid) {
			t.Errorf("TrackOnly 정책에 %q 가 렌더됐다 — 설치 경로 키는 내지 않는다:\n%s", forbid, block)
		}
	}
}

func TestChartRebellionsDIPDisabledRendersNothing(t *testing.T) {
	got := helmTemplate(t, "--set", "driverInstallPolicies.rebellions.enabled=false")
	if strings.Contains(got, "rebellions-atom-ds") {
		t.Errorf("enabled=false 인데 rebellions 정책이 렌더됐다")
	}
}

func TestChartRebellionsDIPVersionRendersWhenSet(t *testing.T) {
	block := rebellionsDIPBlock(t, helmTemplate(t, "--set", "driverInstallPolicies.rebellions.driver.version=3.0.0"))
	if !strings.Contains(block, `version: "3.0.0"`) {
		t.Errorf("version 을 주면 렌더돼야 한다:\n%s", block)
	}
}

// rebellions 정책은 설치기가 없어 trackOnly 를 끌 수 없다 — 끄면 설치 경로가 열리는데 이미지가 없다.
func TestChartValuesSchemaPinsRebellionsTrackOnly(t *testing.T) {
	ok, out := helmRenderWithSet(t, "driverInstallPolicies.rebellions.driver.trackOnly=false")
	if ok {
		t.Fatalf("rebellions trackOnly=false 가 통과했다 — 설치기 없는 벤더에 설치 경로가 열린다")
	}
	if !strings.Contains(out, "trackOnly") {
		t.Errorf("거절 이유에 trackOnly 가 없다:\n%s", out)
	}
}

// 1.31~1.34 프리셋은 Rebellions 를 공개 범위 밖으로 두므로 이 정책도 렌더되지 않아야 한다 — 0.8.2 이하의
// webhook 이 image 없는 정책을 거절해 한 번에 올리는 upgrade 가 실패하는 일을 막는다.
func TestChartRebellionsDIPOffInLinePreset(t *testing.T) {
	got := helmTemplate(t, "-f", "../../deploy/helm/values-k8s1.34.yaml")
	if strings.Contains(got, "rebellions-atom-ds") {
		t.Errorf("1.34 프리셋인데 rebellions 정책이 렌더됐다")
	}
}

// 추적 전용 정책은 job 모드만 뜻이 있다 — daemonset 으로 바꾸면 webhook 이 켜진 곳에서만 거절되므로 스키마에서 막는다.
func TestChartValuesSchemaPinsRebellionsMode(t *testing.T) {
	ok, out := helmRenderWithSet(t, "driverInstallPolicies.rebellions.driver.mode=daemonset")
	if ok {
		t.Fatalf("rebellions driver.mode=daemonset 이 통과했다")
	}
	if !strings.Contains(out, "mode") {
		t.Errorf("거절 이유에 mode 가 없다:\n%s", out)
	}
}
