// ============================================================
// chart_uninstall_test.go: 삭제 hook 이 차트에 실제로 노출되는지 고정한다.
// 상세: 게이트는 Go 코드가 아니라 helm hook 이 불러야 동작한다. 서브커맨드가 있어도 템플릿이
// 그 이름으로 Job 을 만들지 않으면 helm uninstall 은 검사 없이 지나간다. 렌더 결과의 스키마
// 적합성은 helm 이 필요해 여기서 보지 않는다.
// 생성일: 2026-09-10 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"os"
	"strings"
	"testing"
)

func TestChartExposesUninstallHooks(t *testing.T) {
	cases := []struct {
		file  string
		wants []string
	}{
		{
			file: "../../deploy/helm/templates/pre-delete-gate.yaml",
			wants: []string{
				`"helm.sh/hook": pre-delete`,
				`args: ["uninstall-gate"]`,
				"UNINSTALL_SKIP_USAGE_CHECK",
				"UNINSTALL_TIMEOUT",
				"backoffLimit: 0",
				".Values.uninstall.gate.enabled",
			},
		},
		{
			file: "../../deploy/helm/templates/post-delete-crds.yaml",
			wants: []string{
				`"helm.sh/hook": post-delete`,
				`spec.group=="npu.ai"`,
				".Values.uninstall.purgeCRDs",
			},
		},
	}
	for _, tc := range cases {
		body, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", tc.file, err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s 에 %q 가 없다", tc.file, want)
			}
		}
	}

	vals, err := os.ReadFile("../../deploy/helm/values.yaml")
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	for _, key := range []string{"uninstall:", "skipUsageCheck:", "purgeCRDs:"} {
		if !strings.Contains(string(vals), key) {
			t.Errorf("values.yaml 에 %q 가 없다 — 값 파일에 적을 자리가 없다", key)
		}
	}
}

// TestGateHookKeepsFailedJob 은 실패한 hook Job 이 남는지 본다. hook-failed 를
// 삭제 정책에 넣으면 보류 사유를 담은 로그가 helm 이 실패를 알리는 순간 사라진다.
func TestGateHookKeepsFailedJob(t *testing.T) {
	body, err := os.ReadFile("../../deploy/helm/templates/pre-delete-gate.yaml")
	if err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if strings.Contains(string(body), "hook-failed") {
		t.Error("hook-delete-policy 에 hook-failed 가 있다 — 보류 사유 로그가 사라진다")
	}
}
