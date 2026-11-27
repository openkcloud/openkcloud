// ============================================================
// rbac_sync_test.go: 생성 RBAC 와 배포 RBAC 가 갈라지지 않는지 고정한다.
// 상세: `make manifests` 는 config/rbac/role.yaml 만 재생성하고, 실제 배포는 손으로 관리하는
// deploy/helm/templates/rbac.yaml 이 쓰인다 — 둘이 갈라지면 kubebuilder 마커를 고쳐도 배포에
// 반영되지 않는다. 대조는 리소스 이름 목록이 아니라 두 파일의 규칙 전체를 비교한다 — 목록
// 방식은 목록에 없는 새 리소스를 그냥 통과시킨다. 의도한 차이는 knownRBACDivergence 에 모은다.
// 생성일: 2026-08-04 | 수정일: 2026-10-22
// ============================================================
package controller

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"sigs.k8s.io/yaml"
)

type rbacRule struct {
	APIGroups     []string `json:"apiGroups"`
	Resources     []string `json:"resources"`
	ResourceNames []string `json:"resourceNames"`
	Verbs         []string `json:"verbs"`
}

type rbacRuleList struct {
	Rules []rbacRule `json:"rules"`
}

// verbsByResource 는 규칙 목록을 리소스 이름 → 정렬된 verb 목록으로 접는다.
// 같은 리소스가 규칙 둘에 나뉘어 적힌 경우 합집합을 취한다 — 덮어쓰면 뒤 규칙만 남아
// 실제로는 있는 권한이 없다고 잘못 보고한다.
func verbsByResource(rules []rbacRule) map[string][]string {
	out := map[string][]string{}
	for _, r := range rules {
		for _, res := range r.Resources {
			for _, v := range r.Verbs {
				if !contains(out[res], v) {
					out[res] = append(out[res], v)
				}
			}
			sort.Strings(out[res])
		}
	}
	return out
}

// generatedOperatorRules 는 controller-gen 이 만든 config/rbac/role.yaml 을 그대로 읽는다
// (템플릿 문법이 없는 순수 YAML).
func generatedOperatorRules(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	if err != nil {
		t.Fatalf("생성된 rbac 파일을 못 읽었다: %v", err)
	}
	var parsed rbacRuleList
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("생성된 rbac 파일을 YAML 로 못 읽었다: %v", err)
	}
	return verbsByResource(parsed.Rules)
}

// deployedOperatorRules 는 helm 차트에서 operator ClusterRole 의 rules 블록만 뽑아 읽는다.
// 파일 전체는 helm 템플릿(metadata.name 에 {{ }})이라 그대로 YAML 파싱이 안 된다 — rules
// 블록 자체는 템플릿 문법이 없는 순수 YAML 이라 그 부분만 문서 구분자(---)까지 잘라낸다.
func deployedOperatorRules(t *testing.T) map[string][]string {
	t.Helper()
	path := filepath.Join("..", "..", "deploy", "helm", "templates", "rbac.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("helm rbac 파일을 못 읽었다: %v", err)
	}
	re := regexp.MustCompile(`(?s)name: \{\{ include "kcloud-operator\.fullname" \. \}\}-role\nrules:\n(.*?)\n---`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatalf("helm rbac 파일에서 operator ClusterRole 의 rules 블록을 못 찾았다 — " +
			"파일 구조가 바뀌었으면 이 시험의 정규식도 같이 바꿔야 한다")
	}
	var parsed rbacRuleList
	if err := yaml.Unmarshal(append([]byte("rules:\n"), m[1]...), &parsed); err != nil {
		t.Fatalf("helm rbac rules 블록을 YAML 로 못 읽었다: %v", err)
	}
	return verbsByResource(parsed.Rules)
}

// knownRBACDivergence 는 두 파일이 갈라진 채로 두기로 한 자리다. 비어 있는 것이 정상이고,
// 항목을 더하려면 왜 배포본이 생성본을 따르지 않아도 되는지를 함께 적어야 한다.
//
// npuclusterpolicies 의 create/delete: NCP 는 사용자가 만드는 CR 이라 operator 는 reconcile
// (status·finalizer)만 한다. kubebuilder 마커에는 두 verb 가 남아 있지만 코드가 쓴 적이 없고,
// 배포본이 최소권한으로 좁힌 쪽이 맞다(deploy/helm/templates/rbac.yaml 의 "최소화" 주석).
var knownRBACDivergence = map[string][]string{
	"npuclusterpolicies": {"create", "delete"},
}

// TestHelmRBACMatchesGeneratedRole 은 생성본(config/rbac/role.yaml)의 모든 규칙이 실제
// 배포되는 helm ClusterRole 에도 있는지 두 파일을 통째로 대조한다. 리소스 이름을 시험에
// 적어 두지 않는 것이 핵심이다 — 적어 두면 목록에 없는 새 리소스가 그냥 통과한다. 이런 누락은
// 기능 하나만 죽이지 않는다: 컨트롤러의 informer 가 list/watch 를 거절당하면 그 실패가
// mgr.Start 의 반환값으로 올라와 operator 파드 전체가 CrashLoopBackOff 에 들어간다.
// 깨는 뮤테이션: helm rbac.yaml 에서 아무 규칙이나 하나 지우거나 verb 하나를 빼면 실패한다.
func TestHelmRBACMatchesGeneratedRole(t *testing.T) {
	generated := generatedOperatorRules(t)
	deployed := deployedOperatorRules(t)

	// 두 파서 중 하나가 빈 결과를 내면 아래 반복문이 빈 동작이 되고 시험이 항진명제가 된다.
	if len(generated) == 0 || len(deployed) == 0 {
		t.Fatalf("규칙을 못 읽었다 — 생성본 %d개, 배포본 %d개", len(generated), len(deployed))
	}

	for res, want := range generated {
		got, ok := deployed[res]
		if !ok {
			t.Errorf("%s: config/rbac/role.yaml 에는 있는데 deploy/helm/templates/rbac.yaml 의 "+
				"operator role 에 없다 — 실제 배포되는 것은 helm 쪽이라 이 리소스를 쓰는 "+
				"컨트롤러가 권한 없이 뜬다(verb %v)", res, want)
			continue
		}
		for _, v := range want {
			if contains(got, v) || contains(knownRBACDivergence[res], v) {
				continue
			}
			t.Errorf("%s: 생성본 verb %q 가 helm 배포본에 없다 — 생성본 %v, 배포본 %v. "+
				"의도한 차이면 knownRBACDivergence 에 사유와 함께 적을 것", res, v, want, got)
		}
	}
}

// requiredGeneratedRBAC 는 생성본에 반드시 남아 있어야 하는 리소스와, 그 리소스에
// 반드시 남아 있어야 하는 verb 다. 위 시험은 생성본을 돌며 배포본을 확인하므로, 생성본
// 쪽에서 리소스가 사라지거나 verb 가 줄면 대조 대상 자체가 함께 줄어 조용히 통과한다 —
// kubebuilder 마커가 지워지거나 marker 의 verbs 태그가 줄면 두 파일이 함께 줄어 경보가
// 없다. 과다 부여는 안전한 쪽으로 틀린 것이지만 이쪽은 기준선이 깎이는 것이다. 각 항목의
// 근거는 옆의 주석에 적는다. 새 컨트롤러가 늘 때 여기 더한다.
var requiredGeneratedRBAC = map[string][]string{
	"nodedevicereports/status":     {"get", "patch", "update"},                                      // 누락하면 라이브 핫루프 재발
	"acceleratorpartitionpolicies": {"create", "delete", "get", "list", "patch", "update", "watch"}, // 누락하면 관리 API 정책 라우트 403
	"acceleratordescriptors":       {"get", "list", "watch"},                                        // 누락하면 operator 파드 전체 CrashLoop
}

// TestGeneratedRoleKeepsRequiredRBAC 는 생성본에서 리소스 키가 살아 있는 것과 그 리소스의
// 필수 verb 가 함께 살아 있는 것, 둘 다를 본다. TestHelmRBACMatchesGeneratedRole 의 전제 —
// "생성본에 그 리소스와 verb 가 아직 있다" — 를 따로 고정해야 양쪽 방향이 막힌다.
// 깨는 뮤테이션 둘: (1) 위 셋 중 하나의 kubebuilder 마커를 지우거나 (2) verbs 목록에서
// 하나를 빼고 `make manifests` 를 돌리면 각각 실패한다 — 리소스 키는 남아도 verb 가 준
// 경우는 이 시험만 잡는다.
func TestGeneratedRoleKeepsRequiredRBAC(t *testing.T) {
	generated := generatedOperatorRules(t)
	if len(generated) == 0 {
		t.Fatal("생성본 규칙을 하나도 못 읽었다 — 아래 개별 리소스 부재 실패로 흩어지지 않게 " +
			"원인을 여기서 먼저 밝힌다")
	}
	for res, wantVerbs := range requiredGeneratedRBAC {
		gotVerbs, ok := generated[res]
		if !ok {
			t.Errorf("%s 가 config/rbac/role.yaml 에서 사라졌다. kubebuilder 마커가 지워졌는지 "+
				"확인할 것 — 이 항목이 없으면 TestHelmRBACMatchesGeneratedRole 이 그 리소스를 "+
				"검사하지 않고 통과한다", res)
			continue
		}
		for _, v := range wantVerbs {
			if !contains(gotVerbs, v) {
				t.Errorf("%s: 필수 verb %q 가 config/rbac/role.yaml 에서 사라졌다 — 현재 verb "+
					"%v. kubebuilder 마커의 verbs 가 줄었는지 확인할 것 — 리소스 키는 남아 있어도 "+
					"verb 가 줄면 TestHelmRBACMatchesGeneratedRole 은 그 줄어든 목록만 대조해 "+
					"통과한다", res, v, gotVerbs)
			}
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
