// ============================================================
// chart_detector_image_test.go: detector.image 전체 경로 덮어쓰기가 렌더에
// 반영되는지 고정한다.
// 상세: clusterpolicy.yaml 의 detector 이미지는 crdUpgrade.kubectlImage 와 같은 규약을 쓴다 —
// .Values.detector.image 가 있으면 그대로, 없으면 registry+repository+tag 로 조립한다.
// 생성일: 2026-10-04 | 수정일: 2026-11-03
// ============================================================
package controller

import (
	"strings"
	"testing"
)

// TestChartDetectorImageOverride 는 detector.image 를 지정하지 않으면 기존
// registry+repository+tag 조립이 그대로 살고, 지정하면 그 값이 조립을 대신하는지
// 둘 다 본다.
func TestChartDetectorImageOverride(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"기본값 조립", nil, `image: "ghcr.io/openkcloud/kcloud-node-manager:0.6.21"`},
		{
			"전체 경로 덮어쓰기",
			[]string{"--set", "detector.image=registry.example.com/mirror/npu-detector:v9.9.9"},
			`image: "registry.example.com/mirror/npu-detector:v9.9.9"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := helmTemplate(t, tc.args...)
			if !strings.Contains(got, tc.want) {
				t.Errorf("렌더에 %q 가 없다 — detector.image 규약이 깨졌다", tc.want)
			}
		})
	}
}
