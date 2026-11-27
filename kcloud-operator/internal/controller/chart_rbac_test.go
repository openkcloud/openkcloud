// ============================================================
// chart_rbac_test.go: operator ClusterRole 에서 코드가 쓰지 않는 권한이 렌더되지 않는지 고정한다.
// 상세: secrets·poddisruptionbudgets·roles·rolebindings 는 부재를, clusterroles·
// clusterrolebindings 는 존재를, acceleratorworkloads 의 create·delete 는 존재를 고정한다.
// secrets 는 설치 Secret 이름 하나짜리 get 을 namespaced Role 로만 허용한다.
// 생성일: 2026-09-22 | 수정일: 2026-10-22
// ============================================================
package controller

import (
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"

	"kcloud-operator/internal/driveradapter"
)

// rbacDoc 는 렌더된 rbac.yaml 문서 하나에서 main ClusterRole 을 특정하는 데 필요한 필드만 담는다.
type rbacDoc struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	RoleRef struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"roleRef"`
	Subjects []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"subjects"`
	Rules []rbacRule `json:"rules"`
}

// TestOperatorClusterRoleOmitsUnusedGrants 는 operator ServiceAccount 에 바인딩된 main
// ClusterRole 에서 코드가 client 로 쓰지 않는 네 리소스가 사라졌는지 본다. main ClusterRole
// 은 이름의 위치가 아니라 SA→ClusterRoleBinding→roleRef 사슬로 특정한다 — 렌더에는 다른
// ClusterRole(node-manager 등)도 있어 통째로 문자열을 훑으면 판정이 흐려진다.
// 깨는 뮤테이션: deploy/helm/templates/rbac.yaml 의 operator role 에 secrets 규칙을 되살리면
// 실패한다.
func TestOperatorClusterRoleOmitsUnusedGrants(t *testing.T) {
	roleName, rules := mainClusterRoleRules(t)
	byResource := verbsByResource(rules)

	// 코드가 client 로 쓰지 않는 권한: 다시 실리면 안 된다. 리소스 이름은 파싱된 map 키로 정확히
	// 대조하므로 clusterroles 가 roles 로 오검되지 않는다.
	for _, res := range []string{"secrets", "poddisruptionbudgets", "roles", "rolebindings"} {
		if verbs, ok := byResource[res]; ok {
			t.Errorf("main ClusterRole %q 에 %q 권한이 있다(verbs %v) — 코드가 client 로 쓰지 "+
				"않는다. secrets 는 pod 볼륨과 install-secret Role(이름 하나 get)으로만, PDB 는 Eviction API 가 "+
				"API server 에서 대신 검사, roles/rolebindings 는 코드에 대상이 없다",
				roleName, res, verbs)
		}
	}

	// 인접 grant clusterroles/clusterrolebindings 는 descriptor auto-apply 가 실제로 만든다 —
	// 위 제거가 이쪽까지 지우지 않았는지 함께 고정한다(bind/escalate 포함 grant 유지).
	for _, res := range []string{"clusterroles", "clusterrolebindings"} {
		if _, ok := byResource[res]; !ok {
			t.Errorf("main ClusterRole %q 에서 %q 권한이 사라졌다 — descriptor auto-apply 가 "+
				"생성 DRA 드라이버 ClusterRole 을 만들려면 필요하다", roleName, res)
		}
	}
}

// renderedRBACDocs 는 렌더된 rbac.yaml 의 문서들과 operator ServiceAccount 이름을 돌려준다.
// rbac.yaml 에 SA 는 operator 것 하나뿐이다(node-manager SA 는 다른 템플릿). 그 유일성으로
// 특정하고, 아니면 파일 구조가 바뀐 것이니 크게 실패한다.
func renderedRBACDocs(t *testing.T) ([]rbacDoc, string) {
	t.Helper()
	render := helmTemplate(t, "--show-only", "templates/rbac.yaml")

	var docs []rbacDoc
	for _, chunk := range strings.Split(render, "\n---") {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		var d rbacDoc
		if err := yaml.Unmarshal([]byte(chunk), &d); err != nil {
			t.Fatalf("렌더 문서 파싱 실패: %v\n%s", err, chunk)
		}
		if d.Kind != "" {
			docs = append(docs, d)
		}
	}

	var saNames []string
	for _, d := range docs {
		if d.Kind == rbacv1.ServiceAccountKind {
			saNames = append(saNames, d.Metadata.Name)
		}
	}
	if len(saNames) != 1 {
		t.Fatalf("rbac.yaml 렌더의 ServiceAccount 가 1개가 아니다(%d개: %v) — 이 시험은 "+
			"operator SA 를 그 유일성으로 특정한다", len(saNames), saNames)
	}
	return docs, saNames[0]
}

// TestOperatorInstallSecretRoleIsNamedGetOnly 는 설치 Secret 확인(upgrade.MissingInstallSecret)이
// 쓰는 권한이 operator SA 에 바인딩된 Role 의 이름 하나짜리 get 으로만 렌더되는지 고정한다.
// 빠지면 확인이 Forbidden 으로 건너뛰어져 Secret 이 없을 때 Warboy 설치가 다시 슬롯을 쥔 채
// 멈추고(다른 벤더 설치도 함께 멈춘다), 넓어지면 operator 가 다른 Secret 까지 읽는다.
// 깨는 뮤테이션: rbac.yaml 의 install-secret Role 에서 resourceNames 를 지우거나 list 를 더하면 실패한다.
func TestOperatorInstallSecretRoleIsNamedGetOnly(t *testing.T) {
	docs, operatorSA := renderedRBACDocs(t)

	var roles []string
	for _, d := range docs {
		for _, r := range d.Rules {
			if !contains(r.Resources, "secrets") {
				continue
			}
			roles = append(roles, d.Metadata.Name)
			if d.Kind != "Role" || d.Metadata.Namespace == "" ||
				len(r.APIGroups) != 1 || r.APIGroups[0] != "" || len(r.ResourceNames) != 1 ||
				r.ResourceNames[0] != driveradapter.AptAuthSecretName ||
				len(r.Verbs) != 1 || r.Verbs[0] != "get" {
				t.Errorf("%s %q(ns %q)의 secrets 규칙이 core 그룹·이름 %q 하나·get 만이 아니다: "+
					"groups %q names %v verbs %v", d.Kind, d.Metadata.Name, d.Metadata.Namespace,
					driveradapter.AptAuthSecretName, r.APIGroups, r.ResourceNames, r.Verbs)
			}
		}
	}
	if len(roles) != 1 {
		t.Fatalf("secrets 규칙을 가진 문서가 1개가 아니다: %v", roles)
	}

	for _, d := range docs {
		if d.Kind != "RoleBinding" || d.RoleRef.Kind != "Role" || d.RoleRef.Name != roles[0] {
			continue
		}
		for _, s := range d.Subjects {
			if s.Kind == rbacv1.ServiceAccountKind && s.Name == operatorSA {
				return
			}
		}
	}
	t.Errorf("Role %q 를 operator SA %q 에 바인딩하는 RoleBinding 이 없다", roles[0], operatorSA)
}

// mainClusterRoleRules 는 operator ServiceAccount 에 바인딩된 main ClusterRole 을
// SA→ClusterRoleBinding→roleRef 사슬로 찾아 그 규칙을 반환한다(이름의 위치가 아니라 사슬로
// 특정하는 이유는 TestOperatorClusterRoleOmitsUnusedGrants 설명 참조 — 렌더에는 다른
// ClusterRole(node-manager 등)도 있어 통째로 문자열을 훑으면 판정이 흐려진다).
func mainClusterRoleRules(t *testing.T) (string, []rbacRule) {
	t.Helper()
	docs, operatorSA := renderedRBACDocs(t)

	// operator SA 를 subject 로 갖는 ClusterRoleBinding 의 roleRef 가 main ClusterRole 이다.
	var roleName string
	for _, d := range docs {
		if d.Kind != "ClusterRoleBinding" {
			continue
		}
		for _, s := range d.Subjects {
			if s.Kind == rbacv1.ServiceAccountKind && s.Name == operatorSA {
				roleName = d.RoleRef.Name
			}
		}
	}
	if roleName == "" {
		t.Fatalf("operator SA %q 를 바인딩하는 ClusterRoleBinding 을 렌더에서 못 찾았다", operatorSA)
	}

	var rules []rbacRule
	found := false
	for _, d := range docs {
		if d.Kind == "ClusterRole" && d.Metadata.Name == roleName {
			rules = d.Rules
			found = true
		}
	}
	if !found {
		t.Fatalf("main ClusterRole %q 을 렌더에서 못 찾았다", roleName)
	}
	return roleName, rules
}

// TestOperatorClusterRoleGrantsWorkloadWrite 는 관리 API 의 배포 생성·삭제 라우트가 operator SA
// 로 CR 을 만들고 지우므로 main ClusterRole 에 그 동사가 있어야 함을 고정한다. status 하위
// 자원에는 create·delete 를 주지 않는다.
func TestOperatorClusterRoleGrantsWorkloadWrite(t *testing.T) {
	roleName, rules := mainClusterRoleRules(t)
	byResource := verbsByResource(rules)
	for _, verb := range []string{"create", "delete"} {
		if !contains(byResource["acceleratorworkloads"], verb) {
			t.Errorf("main ClusterRole %q 의 acceleratorworkloads 에 %q 가 없다(verbs %v)", roleName, verb, byResource["acceleratorworkloads"])
		}
		if contains(byResource["acceleratorworkloads/status"], verb) {
			t.Errorf("main ClusterRole %q 의 acceleratorworkloads/status 에 %q 가 있다 — status 는 갱신만", roleName, verb)
		}
	}
	if !contains(byResource["acceleratorworkloads/status"], "update") {
		t.Errorf("main ClusterRole %q 의 acceleratorworkloads/status 에 update 가 없다(verbs %v)", roleName, byResource["acceleratorworkloads/status"])
	}
}

// TestOperatorClusterRoleGrantsVendorOperatorStatusRead 는 벤더 operator 상태 보고
// (vendoroperator_status.go 의 readVendorOperatorState)가 쓰는 get·list 권한이 main
// ClusterRole 에 있는지 고정한다. 이 규칙이 빠지면 List 가 Forbidden 이 되어 조건이 항상
// Unknown(StatusProbeUnavailable)으로만 보고된다(겉보기엔 조용히 실패).
// 깨는 뮤테이션: deploy/helm/templates/rbac.yaml 에서 nvidia.com/clusterpolicies 또는
// rebellions.ai/rblnclusterpolicies 규칙을 지우면 실패한다.
func TestOperatorClusterRoleGrantsVendorOperatorStatusRead(t *testing.T) {
	roleName, rules := mainClusterRoleRules(t)
	byResource := verbsByResource(rules)
	for _, tc := range []struct{ resource string }{
		{"clusterpolicies"},
		{"rblnclusterpolicies"},
	} {
		for _, verb := range []string{"get", "list"} {
			if !contains(byResource[tc.resource], verb) {
				t.Errorf("main ClusterRole %q 의 %s 에 %q 가 없다(verbs %v)", roleName, tc.resource, verb, byResource[tc.resource])
			}
		}
	}
}
