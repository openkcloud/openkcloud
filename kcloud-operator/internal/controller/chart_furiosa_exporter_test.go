// ============================================================
// chart_furiosa_exporter_test.go: furiosa-exporter DaemonSet 의 파드 라벨 설정 렌더를 고정한다.
// 상세: kubeResourcesLabel 이 켜지면 인자와 kubelet pod-resources 마운트가 함께 들어가야 한다.
// 인자만 있으면 exporter 가 소켓을 못 찾아 라벨이 늘 비고, 마운트만 있으면 기능이 꺼진 채
// 호스트 디렉터리만 연다.
// 생성일: 2026-10-15
// ============================================================
package controller

import (
	"strings"
	"testing"
)

func TestChartFuriosaExporterKubeResourcesLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"기본값", nil, true},
		{"끔", []string{"--set", "furiosa.exporter.kubeResourcesLabel=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := helmTemplate(t, append([]string{"--set", "furiosa.exporter.enabled=true",
				"--show-only", "templates/furiosa-exporter.yaml"}, tc.args...)...)
			for _, s := range []string{`"--kube-resources-label=true"`, "mountPath: /var/lib/kubelet/pod-resources",
				"path: /var/lib/kubelet/pod-resources"} {
				if strings.Contains(got, s) != tc.want {
					t.Errorf("%q 포함 = %v, want %v", s, !tc.want, tc.want)
				}
			}
		})
	}
}
