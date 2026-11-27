// ============================================================
// chart_driverinstallpolicy_test.go: DriverInstallPolicy 차트 렌더의 기본값을 고정한다.
// 상세: tenstorrent DIP 의 driver.kernelImages 렌더(기본 빈 맵·사이트 값 반영)와, nvidia DIP 의
// driver.version 기본값이 우분투 아카이브에 실재하는 버전인지를 본다. helm 을 os/exec 로 부른다.
// 생성일: 2026-09-22 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"strings"
	"testing"
)

// tenstorrent DIP 만 본다(--show-only). 다른 문서·CRD 가 섞이면 kernelImages 문자열이
// 스키마 쪽에서도 나와 판정이 흐려진다.
const dipTemplate = "templates/driverinstallpolicy.yaml"

// TestTenstorrentKernelImagesAbsentByDefault 는 기본값(빈 맵)에서 kernelImages 가 렌더되지
// 않는지 본다. 빈 맵은 helm `with` 에서 falsy 이므로 줄 자체가 나오면 안 된다.
func TestTenstorrentKernelImagesAbsentByDefault(t *testing.T) {
	got := helmTemplate(t, "--show-only", dipTemplate)
	if strings.Contains(got, "kernelImages:") {
		t.Errorf("기본 렌더에 kernelImages 가 있다 — 빈 맵은 렌더되면 안 된다")
	}
}

// TestTenstorrentKernelImagesRenderWhenSet 는 커널별 이미지 맵을 주면 driver 블록에
// 그대로 렌더되는지 본다.
func TestTenstorrentKernelImagesRenderWhenSet(t *testing.T) {
	const kernelImg = "reg/kcloud/tenstorrent-driver-ds:2.11.0-k5.15.0-190-generic-v1"
	got := helmTemplate(t, "--show-only", dipTemplate,
		"--set-json", `driverInstallPolicies.tenstorrent.driver.kernelImages={"5.15.0-190-generic":"`+kernelImg+`"}`)
	if !strings.Contains(got, "kernelImages:") {
		t.Errorf("맵을 줬는데 kernelImages 가 렌더되지 않았다")
	}
	if !strings.Contains(got, kernelImg) {
		t.Errorf("렌더에 커널 이미지 값 %q 가 없다", kernelImg)
	}
}

// TestChartDefaultNvidiaVersionIsArchiveVersion 은 차트 기본값의 nvidia driver.version 이
// 우분투 아카이브에 실재하는 버전인지 본다.
func TestChartDefaultNvidiaVersionIsArchiveVersion(t *testing.T) {
	// 사전 컴파일 이미지는 사이트별로 주입되고 차트 기본값에는 없다. 그래서 차트 기본값의
	// nvidia driver.version 은 DKMS 경로가 쓰는 값이며 우분투 아카이브에 실재해야 한다.
	// PPA 전용 버전을 기본값에 두면 PPA 가 없는 클러스터가 설치에 실패한다.
	const ppaOnly = "580.126.09"
	got := helmTemplate(t, "--show-only", dipTemplate)
	if strings.Contains(got, "version: \""+ppaOnly+"\"") {
		t.Errorf("차트 기본 nvidia driver.version 이 PPA 전용 버전 %s 다", ppaOnly)
	}
	if !strings.Contains(got, "version: \"580.178.04\"") {
		t.Errorf("차트 기본 nvidia driver.version 이 아카이브 버전이 아니다")
	}
}
