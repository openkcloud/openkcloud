// ============================================================
// corpus_test.go: 합성 표본 모음으로 검증기를 두들긴다
// 상세: 유효 archetype 에 변형을 얹어 무효 표본을 만든다. 기대 판정은 변형 규칙이 선언하므로
// 검증기 구현과 독립이다 - 순환 평가를 피한다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package descriptor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func archetypeNames() []string {
	return []string{"rngd", "nvidia-a30", "tenstorrent-blackhole"}
}

// loadArchetype 은 archetype YAML 을 일반 map 으로 읽는다. 구조체로 읽으면 패치가
// 표현할 수 있는 변형이 줄어든다 - 타입 뒤집기는 map 단계에서만 만들 수 있다.
func loadArchetype(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "archetypes", name+".yaml"))
	if err != nil {
		t.Fatalf("archetype 읽기 실패: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("archetype 이 YAML 이 아니다: %v", err)
	}
	return m
}

func TestArchetypesAreValid(t *testing.T) {
	for _, n := range archetypeNames() {
		t.Run(n, func(t *testing.T) {
			m := loadArchetype(t, n)
			spec, err := decodeSpec(m)
			if err != nil {
				t.Fatalf("디코드 실패: %v", err)
			}
			if errs := Validate(spec); len(errs) != 0 {
				t.Fatalf("유효 archetype 이 거절됐다: %v", errs.ToAggregate())
			}
			ok, refusals := FeasibleBackends(spec)
			if len(ok) != len(spec.Backends) {
				t.Fatalf("선언한 backend 가 전부 실현 가능해야 한다: ok=%v 거절=%v", ok, refusals)
			}
			// 장치 발견은 이제 런타임이 한다 - 컴파일은 spec.DeviceNodes 템플릿을
			// 치환 없이 그대로 실어 보내는지만 본다.
			if _, err := CompileDevicePlugin(spec); err != nil {
				t.Fatalf("컴파일 실패: %v", err)
			}
		})
	}
}

// 규칙이 조용히 사라지는 것을 막는다. 규칙을 더하거나 지우면 이 수를 함께 바꾼다.
// 치환자 없이 장치가 여럿인 조합은 규칙에서 빠져 있다 — 런타임 발견이 도입되며 이
// 조합은 단일 고정 장치로 처리돼 더는 컴파일 단계 결함이 아니기 때문이다.
func TestMutationRuleCount(t *testing.T) {
	rules := MutationRules()
	if len(rules) != 26 {
		t.Fatalf("변형 규칙은 26개다: %d", len(rules))
	}
	fam := map[string]int{}
	for _, r := range rules {
		fam[r.Family]++
	}
	for _, f := range []string{"필드 삭제", "타입 뒤집기", "경계값", "경로 탈출", "중복 선언", "모순 선언"} {
		if fam[f] == 0 {
			t.Errorf("갈래 %q 에 규칙이 없다", f)
		}
	}
}

// judge 는 표본 하나를 네 단계에 차례로 태워 실제로 걸러진 자리와 사유를 낸다.
// 어느 단계에서도 안 걸리면 StageAccept 다.
func judge(m map[string]any) (Stage, string) {
	spec, err := decodeSpec(m)
	if err != nil {
		return StageParse, err.Error()
	}
	if errs := Validate(spec); len(errs) > 0 {
		return StageValidate, errs.ToAggregate().Error()
	}
	ok, refusals := FeasibleBackends(spec)
	if len(ok) < len(spec.Backends) {
		reason := ""
		if len(refusals) > 0 {
			reason = refusals[0].Reason
		}
		return StageFeasible, reason
	}
	// deviceIDs 없이 컴파일한다 - 발견은 이제 런타임 몫이다. Validate·
	// FeasibleBackends 를 통과한 표본은 더는 여기서 걸러지지 않는다(치환자
	// 유무 판정이 사라졌다). 그래도 컴파일 단계를 남겨 두는 이유는 이 함수들이
	// 계약(Validate 재확인·FeasibleBackends 판정)을 여전히 지키는지 보기 위해서다.
	if _, err := CompileDevicePlugin(spec); err != nil {
		return StageCompile, err.Error()
	}
	if contains(spec.Backends, "dra") {
		if _, err := CompileDRA(spec); err != nil {
			return StageCompile, err.Error()
		}
	}
	return StageAccept, ""
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// corpus 전체를 돌린다. 실패한 표본은 false accept / false reject 로 갈라 세고,
// 하나라도 있으면 실패한다. 집계는 결과 문서로 옮긴다.
func TestCorpusFindsEveryPlantedDefect(t *testing.T) {
	var falseAccept, falseReject, wrongStage int
	for _, arch := range archetypeNames() {
		for _, rule := range MutationRules() {
			t.Run(arch+"/"+rule.Name, func(t *testing.T) {
				m := loadArchetype(t, arch)
				rule.Patch(m)
				got, reason := judge(m)

				switch {
				case got == StageAccept && rule.ExpectStage != StageAccept:
					falseAccept++
					t.Errorf("통과했다(false accept). 기대=%s 사유=%s", rule.ExpectStage, rule.Why)
				case got != StageAccept && rule.ExpectStage == StageAccept:
					falseReject++
					t.Errorf("거절됐다(false reject). %s", reason)
				case got != rule.ExpectStage:
					wrongStage++
					t.Errorf("걸러진 자리가 다르다. 기대=%s 실제=%s 사유=%s",
						rule.ExpectStage, got, reason)
				}

				if rule.ExpectField != "" && got == rule.ExpectStage &&
					!strings.Contains(reason, rule.ExpectField) {
					t.Errorf("사유에 %q 가 없다: %s", rule.ExpectField, reason)
				}
			})
		}
	}
	t.Logf("false accept=%d false reject=%d 자리 어긋남=%d 표본=%d",
		falseAccept, falseReject, wrongStage, len(archetypeNames())*len(MutationRules()))
}
