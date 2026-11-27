// ============================================================
// chart_console_rbac_test.go: 콘솔용 extraServiceAccounts 묶음이 SA·바인딩으로 렌더되는지 고정한다.
// 상세: console-rbac.yaml 은 기본 콘솔 SA 외에, extraServiceAccounts 항목마다 SA 와 viewer
// 바인딩을(role=operator 면 operator 바인딩까지) 렌더한다. 알 수 없는 role 은 렌더 자체가 실패해야 한다.
// 생성일: 2026-10-06 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"os/exec"
	"strings"
	"testing"
)

func TestChartConsoleExtraServiceAccountRendersSAAndBinding(t *testing.T) {
	out := helmTemplate(t, "--set", "api.enabled=true",
		"--set", "api.console.extraServiceAccounts[0].name=partner-ui",
		"--set", "api.console.extraServiceAccounts[0].role=operator")
	for _, want := range []string{"name: kcloud-operator-console-partner-ui\n", "name: kcloud-operator-console-partner-ui-operator\n", "name: kcloud-operator-console-partner-ui-viewer\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("렌더에 %q 가 없다", strings.TrimSpace(want))
		}
	}
}

func TestChartConsoleExtraServiceAccountDefaultsToViewerOnly(t *testing.T) {
	out := helmTemplate(t, "--set", "api.enabled=true", "--set", "api.console.extraServiceAccounts[0].name=ro")
	if !strings.Contains(out, "name: kcloud-operator-console-ro-viewer\n") || strings.Contains(out, "console-ro-operator") {
		t.Fatal("role 생략은 viewer 바인딩만 낸다")
	}
}

func TestChartConsoleExtraServiceAccountRejectsUnknownRole(t *testing.T) {
	bin := helmBinary(t)
	out, err := exec.Command(bin, "template", "t", "../../deploy/helm", "--set", "api.enabled=true",
		"--set", "api.console.extraServiceAccounts[0].name=x", "--set", "api.console.extraServiceAccounts[0].role=admin").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "viewer 또는 operator") {
		t.Fatalf("알 수 없는 role 은 렌더가 실패해야 한다: err=%v out=%s", err, out)
	}
}
