// ============================================================
// corpus.go: 유효 descriptor 를 변형해 무효 표본을 만든다
// 상세: 규칙이 표본과 기대 판정(MutationRule.Why)을 함께 선언해 검증기 구현을 보지
// 않고 오라클이 된다. 손으로 표본을 짓지 않는 이유는, 프로덕션이 만들 수 없는
// 조합을 시험해도 아무것도 증명되지 않기 때문이다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package descriptor

import (
	"fmt"

	"sigs.k8s.io/yaml"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// decodeSpec 은 일반 map 을 CRD 타입으로 디코드한다. strict 디코드는 파일에서
// descriptor 를 읽는 경로(CLI·검증기)를 모사한다 - apiserver 경로와는 다르다.
// apiserver 는 structural schema 로 모르는 필드를 가지치기(prune)할 뿐 거절하지
// 않는다. 그래서 "모르는 필드" 계열 변형이 여기서 잡는 것은 파일 경로의 엄격함이지
// apiserver 경로의 관용이 아니다 - 두 경로가 갈리는 지점을 혼동하지 않는다.
func decodeSpec(m map[string]any) (npuv1alpha1.AcceleratorDescriptorSpec, error) {
	var spec npuv1alpha1.AcceleratorDescriptorSpec
	raw, err := yaml.Marshal(m)
	if err != nil {
		return spec, fmt.Errorf("재직렬화 실패: %w", err)
	}
	if err := yaml.UnmarshalStrict(raw, &spec); err != nil {
		return spec, fmt.Errorf("디코드 실패: %w", err)
	}
	return spec, nil
}

// Stage 는 표본이 걸러지는 자리다.
type Stage string

const (
	StageParse    Stage = "parse"
	StageValidate Stage = "validate"
	StageFeasible Stage = "feasible"
	StageCompile  Stage = "compile"
	StageAccept   Stage = "accept" // 네 단계를 전부 통과
)

// MutationRule 하나가 표본 하나를 만들고 그 표본의 정답을 함께 선언한다.
// ExpectStage 는 "여기서 걸러져야 한다" 이고 ExpectField 는 사유에 나와야 할 필드
// 경로다(빈 값이면 확인하지 않는다). Why 는 왜 그 판정이 정답인지이며 결과 문서로 간다.
type MutationRule struct {
	Name        string
	Family      string
	Patch       func(map[string]any)
	ExpectStage Stage
	ExpectField string
	Why         string
}

func setPath(m map[string]any, key string, v any) { m[key] = v }
func delPath(m map[string]any, key string)        { delete(m, key) }

func identity(m map[string]any) map[string]any {
	id, ok := m["identity"].(map[string]any)
	if !ok {
		id = map[string]any{}
		m["identity"] = id
	}
	return id
}

// MutationRules 는 여섯 갈래의 변형 규칙 전부다. 각 규칙은 archetype 마다 한 표본을
// 만들므로 표본 수는 규칙 수 × archetype 수다.
func MutationRules() []MutationRule {
	return []MutationRule{
		// --- 필드 삭제 ---
		{Name: "벤더 삭제", Family: "필드 삭제",
			Patch:       func(m map[string]any) { delPath(m, "vendor") },
			ExpectStage: StageValidate, ExpectField: "spec.vendor",
			Why: "리소스명의 앞 절반이 없어 광고할 이름을 만들 수 없다"},
		{Name: "제품 삭제", Family: "필드 삭제",
			Patch:       func(m map[string]any) { delPath(m, "product") },
			ExpectStage: StageValidate, ExpectField: "spec.product",
			Why: "리소스명의 뒤 절반이 없다"},
		{Name: "할당 단위 삭제", Family: "필드 삭제",
			Patch:       func(m map[string]any) { delPath(m, "allocationUnit") },
			ExpectStage: StageValidate, ExpectField: "spec.allocationUnit",
			Why: "빈 문자열은 알려진 단위 둘 중 어느 것도 아니다"},
		{Name: "식별자 출처 삭제", Family: "필드 삭제",
			Patch:       func(m map[string]any) { delete(identity(m), "source") },
			ExpectStage: StageValidate, ExpectField: "spec.identity.source",
			Why: "장치를 무엇으로 지목할지 정해지지 않는다"},
		{Name: "장치 노드 삭제", Family: "필드 삭제",
			Patch:       func(m map[string]any) { delPath(m, "deviceNodes") },
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes",
			Why: "주입할 것이 없으면 어느 backend 로도 못 간다"},
		{Name: "backend 삭제", Family: "필드 삭제",
			Patch:       func(m map[string]any) { delPath(m, "backends") },
			ExpectStage: StageValidate, ExpectField: "spec.backends",
			Why: "만들 대상이 없다"},

		// --- 타입 뒤집기 ---
		{Name: "장치 노드를 문자열로", Family: "타입 뒤집기",
			Patch:       func(m map[string]any) { setPath(m, "deviceNodes", "/dev/x") },
			ExpectStage: StageParse, ExpectField: "",
			Why: "목록 자리에 스칼라가 오면 디코드가 막아야 한다"},
		{Name: "안정성 플래그를 문자열로", Family: "타입 뒤집기",
			Patch:       func(m map[string]any) { identity(m)["stableAcrossReboot"] = "yes" },
			ExpectStage: StageParse, ExpectField: "",
			Why: "불리언 자리의 문자열은 디코드가 막아야 한다"},
		{Name: "backend 를 map 으로", Family: "타입 뒤집기",
			Patch:       func(m map[string]any) { setPath(m, "backends", map[string]any{"a": 1}) },
			ExpectStage: StageParse, ExpectField: "",
			Why: "목록 자리의 매핑은 디코드가 막아야 한다"},
		{Name: "모르는 최상위 필드", Family: "타입 뒤집기",
			Patch:       func(m map[string]any) { setPath(m, "vendorr", "rngd") },
			ExpectStage: StageParse, ExpectField: "",
			Why: "오타 난 필드가 조용히 무시되면 관리자가 왜 안 먹는지 되짚을 수 없다. " +
				"다만 이 판정은 파일을 읽는 경로에만 해당한다 — apiserver 는 가지치기한다"},

		// --- 경계값 ---
		{Name: "벤더가 공백만", Family: "경계값",
			Patch:       func(m map[string]any) { setPath(m, "vendor", "   ") },
			ExpectStage: StageValidate, ExpectField: "spec.vendor",
			Why: "공백만 있는 이름은 없는 것과 같다"},
		{Name: "벤더에 공백 포함", Family: "경계값",
			Patch:       func(m map[string]any) { setPath(m, "vendor", "acme corp") },
			ExpectStage: StageValidate, ExpectField: "spec.vendor",
			Why: "리소스명 acme corp/x 는 쿠버네티스가 받지 않는다"},
		{Name: "제품명에 슬래시", Family: "경계값",
			Patch:       func(m map[string]any) { setPath(m, "product", "a/30") },
			ExpectStage: StageValidate, ExpectField: "spec.product",
			Why: "리소스명이 nvidia/a/30 이 되어 구분자가 둘이다 - 확장 리소스명은 " +
				"prefix/name 두 토막뿐이라 kubelet 이 광고를 거절한다"},
		{Name: "제품명 63자 초과", Family: "경계값",
			Patch: func(m map[string]any) {
				long := ""
				for i := 0; i < 100; i++ {
					long += "a"
				}
				setPath(m, "product", long)
			},
			ExpectStage: StageValidate, ExpectField: "spec.product",
			Why: "확장 리소스명의 이름 토막은 63자까지다. 넘으면 kubelet 이 광고를 거절한다"},
		{Name: "장치 노드 빈 목록", Family: "경계값",
			Patch:       func(m map[string]any) { setPath(m, "deviceNodes", []any{}) },
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes",
			Why: "삭제와 같은 결과여야 한다"},
		{Name: "장치 노드가 빈 문자열", Family: "경계값",
			Patch:       func(m map[string]any) { setPath(m, "deviceNodes", []any{""}) },
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes[0]",
			Why: "절대경로가 아니다"},

		// --- 경로 탈출 ---
		{Name: "상위 경로 탈출", Family: "경로 탈출",
			Patch:       func(m map[string]any) { setPath(m, "deviceNodes", []any{"/dev/../etc/shadow"}) },
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes[0]",
			Why: "슬래시로 시작하지만 /dev 밖을 가리킨다. 특권 컨테이너가 이 경로를 주입하면 호스트 파일이 들어온다"},
		{Name: "중간 경로 탈출", Family: "경로 탈출",
			Patch:       func(m map[string]any) { setPath(m, "deviceNodes", []any{"/dev/rngd/../../root"}) },
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes[0]",
			Why: "정규화하면 /root 다"},
		{Name: "정규화되지 않은 경로", Family: "경로 탈출",
			Patch:       func(m map[string]any) { setPath(m, "deviceNodes", []any{"/dev/./rngd//npu0"}) },
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes[0]",
			Why: "같은 장치를 두 이름으로 쓰면 중복 판정이 무력해진다"},
		{Name: "정규화됐지만 /dev 밖", Family: "경로 탈출",
			Patch:       func(m map[string]any) { setPath(m, "deviceNodes", []any{"/etc/shadow"}) },
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes[0]",
			Why: "정규화 검사는 통과하지만 장치가 아닌 호스트 파일이다 — " +
				"특권 컨테이너에 주입되면 그대로 읽힌다"},

		// --- 중복 선언 ---
		{Name: "같은 backend 두 번", Family: "중복 선언",
			Patch:       func(m map[string]any) { setPath(m, "backends", []any{"dra", "dra"}) },
			ExpectStage: StageValidate, ExpectField: "spec.backends",
			Why: "배포물이 두 벌 나오면 같은 이름으로 두 번 적용된다"},
		{Name: "같은 장치 노드 두 번", Family: "중복 선언",
			// archetype 자신의 첫 노드를 복제한다. 고정 문자열을 넣으면 그 표본이
			// 더는 이 archetype 에서 파생한 것이 아니게 된다.
			Patch: func(m map[string]any) {
				nodes, _ := m["deviceNodes"].([]any)
				if len(nodes) == 0 {
					return
				}
				setPath(m, "deviceNodes", []any{nodes[0], nodes[0]})
			},
			ExpectStage: StageValidate, ExpectField: "spec.deviceNodes",
			Why: "중복 주입은 CDI 스펙에서 중복 항목이 된다"},

		// --- 모순 선언 ---
		{Name: "불안정 식별자로 DRA 선언", Family: "모순 선언",
			Patch: func(m map[string]any) {
				identity(m)["stableAcrossReboot"] = false
				setPath(m, "backends", []any{"dra"})
			},
			ExpectStage: StageFeasible, ExpectField: "",
			Why: "정합은 깨지지 않았지만 DRA 로는 만들 수 없다. 거절 사유가 값으로 나와야 한다"},
		{Name: "리셋 필요 선언에 수단 없음", Family: "모순 선언",
			Patch: func(m map[string]any) {
				m["cleanup"] = map[string]any{"requiresDeviceReset": true}
			},
			ExpectStage: StageValidate, ExpectField: "spec.cleanup.resetCommand",
			Why: "반납 후 상태를 만들 길이 없다"},
		{Name: "모르는 backend 이름", Family: "모순 선언",
			Patch:       func(m map[string]any) { setPath(m, "backends", []any{"cdi"}) },
			ExpectStage: StageValidate, ExpectField: "spec.backends[0]",
			Why: "선언한 것을 만들 수 없다"},

		// --- 경계값(sharing) ---
		{Name: "sharing replicas 범위 밖", Family: "경계값",
			// 세 archetype 모두 backends 에 devicePlugin 을 이미 선언하므로 sharing 만
			// 추가하면 replicas 하한(2) 위반이 spec.sharing.replicas 에서 걸린다.
			Patch: func(m map[string]any) {
				setPath(m, "sharing", map[string]any{"mode": "timeSliced", "replicas": 1})
			},
			ExpectStage: StageValidate, ExpectField: "spec.sharing.replicas",
			Why: "replicas 1 은 공유가 아니다 - 시분할 하한은 2"},
	}
}
