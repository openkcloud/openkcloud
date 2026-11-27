// ============================================================
// chart_policy_ownership_test.go: 설치 정책이 helm hook 이 아니라 릴리스 자원인지 고정한다.
// 상세: hook 으로 두면 업그레이드마다 삭제·재생성되어 uid 가 바뀌고, 정책을 소유자로 참조하는
// DaemonSet 이 연쇄로 다시 만들어진다. 정책에 직접 넣은 값도 사라진다.
// 생성일: 2026-09-24 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"strings"
	"testing"
)

// 템플릿에 helm hook 주석이 하나도 없어야 한다.
func TestDriverInstallPolicyIsNotAHelmHook(t *testing.T) {
	tpl := readChartFile(t, "templates/driverinstallpolicy.yaml")
	for _, forbidden := range []string{
		`"helm.sh/hook"`,
		`"helm.sh/hook-weight"`,
		`"helm.sh/hook-delete-policy"`,
	} {
		if strings.Contains(tpl, forbidden) {
			t.Errorf("설치 정책 템플릿에 %s 가 남아 있다. 업그레이드마다 재생성된다", forbidden)
		}
	}
}
