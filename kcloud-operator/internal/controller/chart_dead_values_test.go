// ============================================================
// chart_dead_values_test.go: 선언돼 있으나 템플릿이 읽지 않던 값 셋이 렌더에
// 반영되는지 고정한다.
// 상세: rollingUpdate.maxUnavailable·serviceAccount.create·rbac.create 는 detector.image 와
// 같은 종류(선언은 있는데 템플릿이 읽지 않아 --set 이 조용히 버려짐)가 될 수 있는 값이다.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"strings"
	"testing"
)

// TestChartRollingUpdateMaxUnavailable 는 기본값(0, deployment.yaml 의 기존
// 하드코딩과 동일 — 단일 replica 롤아웃 중단 회귀 방지)과 --set 오버라이드가
// 둘 다 렌더에 반영되는지 본다. maxSurge 는 선언 값이 없어 항상 1로 하드코딩이다.
func TestChartRollingUpdateMaxUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"기본값 0(기존 하드코딩 보존)", nil, "maxUnavailable: 0\n      maxSurge: 1"},
		{"오버라이드 1", []string{"--set", "rollingUpdate.maxUnavailable=1"}, "maxUnavailable: 1\n      maxSurge: 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := helmTemplate(t, tc.args...)
			if !strings.Contains(got, tc.want) {
				t.Errorf("렌더에 %q 가 없다 — rollingUpdate.maxUnavailable 규약이 깨졌다", tc.want)
			}
		})
	}
}

// TestChartRBACCreateGuard 는 rbac.create=false 일 때 ClusterRole·ClusterRoleBinding·
// Role·RoleBinding 이 전부 사라지고, ServiceAccount(독립 스위치)는 남는지 본다.
func TestChartRBACCreateGuard(t *testing.T) {
	got := helmTemplate(t, "--set", "rbac.create=false")
	for _, name := range []string{
		"kcloud-operator-role",            // operator 본체 ClusterRole/Binding
		"kcloud-node-manager-role",        // detector(node-manager) ClusterRole/Binding
		"kcloud-operator-leader-election", // leader-election Role/RoleBinding
	} {
		if strings.Contains(got, name) {
			t.Errorf("rbac.create=false 인데 %q 가 렌더에 남아있다", name)
		}
	}
	if !strings.Contains(got, "kind: ServiceAccount") {
		t.Error("rbac.create=false 가 serviceAccount.create 까지 꺼버렸다 — 두 스위치는 독립이어야 한다")
	}
}

// TestChartServiceAccountCreateGuard 는 serviceAccount.create=false 일 때 operator
// 의 ServiceAccount 리소스는 사라지지만, 그 이름을 참조하는 Deployment·바인딩은
// 여전히 유효한 이름을 쓰는지 본다 — create:false 는 "기존 SA 를 쓴다"는 뜻이라
// 이름 연결이 끊기면 fresh 클러스터에서 Pod 기동이 실패한다. name 을 준 경우와
// 안 준 경우(helm 기본 scaffold 규약대로 "default" SA 로 떨어져야 한다) 둘 다 본다.
func TestChartServiceAccountCreateGuard(t *testing.T) {
	for _, tc := range []struct {
		name     string
		extraSet []string
		wantSA   string
	}{
		{"이름 지정", []string{"--set", "serviceAccount.name=preexisting-sa"}, "preexisting-sa"},
		{"이름 미지정(default SA)", nil, "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--set", "serviceAccount.create=false"}, tc.extraSet...)
			got := helmTemplate(t, args...)
			if strings.Contains(got, "kind: ServiceAccount\nmetadata:\n  name: "+tc.wantSA) {
				t.Error("serviceAccount.create=false 인데 ServiceAccount 리소스가 여전히 생성된다")
			}
			want := "serviceAccountName: " + tc.wantSA
			if !strings.Contains(got, want) {
				t.Errorf("렌더에 %q 가 없다 — serviceAccount.create=false 의 이름 연결이 깨졌다", want)
			}
		})
	}
}
