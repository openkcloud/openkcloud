// ============================================================
// compare.go: 두 backend 의 관측치를 대조한다
// 상세: 클러스터를 모른다 - 관측치 둘만 입력받아, 같다/다르다가 아니라 집합 관계로
// 판정한다(벤더 집합이 생성 집합의 진부분집합인 사례가 있어 이진 판정으론 안 된다).
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package descriptor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// BackendObservation 은 backend 하나를 네 축에서 관측한 값이다.
// 정적 축(AllocationUnits·DeviceNodes)은 컴파일 출력에서, 관측 축(DevNodes·
// ForeignTopLevel)은 라이브 파드 안에서 온다.
type BackendObservation struct {
	Backend         string   `json:"backend"`
	AllocationUnits int      `json:"allocationUnits"`
	DeviceNodes     []string `json:"deviceNodes"`
	DevNodes        []string `json:"devNodes"`
	ForeignTopLevel []string `json:"foreignTopLevel"`
}

// Relation 은 두 집합의 관계다.
type Relation string

const (
	RelationEqual    Relation = "equal"
	RelationSubset   Relation = "subset"   // A ⊂ B
	RelationSuperset Relation = "superset" // A ⊃ B
	RelationDisjoint Relation = "disjoint"
	RelationOverlap  Relation = "overlap" // 겹치지만 어느 쪽도 포함하지 않음
)

// relate 는 두 문자열 집합의 관계와 각 쪽에만 있는 원소를 낸다. 순서와 중복은 무시한다.
// 빈 집합 둘은 equal 이다 - "관측을 못 했다" 와 "없다" 를 여기서 가르지 않는다.
// 빈 관측을 걸러내는 책임은 수집기 쪽에 있다.
func relate(a, b []string) (Relation, []string, []string) {
	sa, sb := toSet(a), toSet(b)
	var onlyA, onlyB []string
	for k := range sa {
		if !sb[k] {
			onlyA = append(onlyA, k)
		}
	}
	for k := range sb {
		if !sa[k] {
			onlyB = append(onlyB, k)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)

	switch {
	case len(onlyA) == 0 && len(onlyB) == 0:
		return RelationEqual, onlyA, onlyB
	case len(onlyA) == 0:
		return RelationSubset, onlyA, onlyB
	case len(onlyB) == 0:
		return RelationSuperset, onlyA, onlyB
	case len(sa) == len(onlyA) && len(sb) == len(onlyB):
		return RelationDisjoint, onlyA, onlyB
	default:
		return RelationOverlap, onlyA, onlyB
	}
}

func toSet(xs []string) map[string]bool {
	s := make(map[string]bool, len(xs))
	for _, x := range xs {
		s[x] = true
	}
	return s
}

// AxisResult 는 축 하나의 대조 결과다.
type AxisResult struct {
	Axis     string   `json:"axis"`
	A        string   `json:"a"`
	B        string   `json:"b"`
	Relation Relation `json:"relation"`
	Match    bool     `json:"match"`
	OnlyInA  []string `json:"onlyInA,omitempty"`
	OnlyInB  []string `json:"onlyInB,omitempty"`
}

// Comparison 은 네 축 전체의 대조 결과다.
type Comparison struct {
	A        string       `json:"a"`
	B        string       `json:"b"`
	Axes     []AxisResult `json:"axes"`
	AllMatch bool         `json:"allMatch"`
}

// Compare 는 관측치 둘을 네 축에서 대조한다. 축 이름과 순서는 손 대조표와 같다 -
// 자동 판정과 사람이 쓴 표를 나란히 놓고 읽을 수 있어야 한다.
func Compare(a, b BackendObservation) Comparison {
	c := Comparison{A: a.Backend, B: b.Backend}

	unitMatch := a.AllocationUnits == b.AllocationUnits
	unitRel := RelationEqual
	if !unitMatch {
		unitRel = RelationOverlap
	}
	c.Axes = append(c.Axes, AxisResult{
		Axis:     "할당 단위 수",
		A:        itoa(a.AllocationUnits),
		B:        itoa(b.AllocationUnits),
		Relation: unitRel,
		Match:    unitMatch,
	})

	c.Axes = append(c.Axes, setAxis("장치 노드 템플릿", a.DeviceNodes, b.DeviceNodes))
	c.Axes = append(c.Axes, setAxis("컨테이너 /dev 노드", a.DevNodes, b.DevNodes))
	c.Axes = append(c.Axes, setAxis("격리(다른 장치 비노출)", a.ForeignTopLevel, b.ForeignTopLevel))

	c.AllMatch = true
	for _, ax := range c.Axes {
		if !ax.Match {
			c.AllMatch = false
		}
	}
	return c
}

func setAxis(name string, a, b []string) AxisResult {
	rel, onlyA, onlyB := relate(a, b)
	return AxisResult{
		Axis:     name,
		A:        summarize(a),
		B:        summarize(b),
		Relation: rel,
		Match:    rel == RelationEqual,
		OnlyInA:  onlyA,
		OnlyInB:  onlyB,
	}
}

// summarize 는 사람이 읽을 요약이다. 원소가 셋을 넘으면 개수로 접는다 - 36개를 표에
// 그대로 쏟으면 표가 읽히지 않는다. 접힌 값은 OnlyInA/OnlyInB 에 그대로 남는다.
func summarize(xs []string) string {
	u := make([]string, 0, len(xs))
	seen := map[string]bool{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			u = append(u, x)
		}
	}
	sort.Strings(u)
	switch {
	case len(u) == 0:
		return "없음"
	case len(u) <= 3:
		return strings.Join(u, " ")
	default:
		return itoa(len(u)) + "개"
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// MarkdownTable 은 손 대조표와 같은 모양의 표를 낸다.
func (c Comparison) MarkdownTable() string {
	var b strings.Builder
	fmt.Fprintf(&b, "| 축 | %s | %s | 관계 | 일치 |\n", c.A, c.B)
	b.WriteString("|---|---|---|---|:--:|\n")
	for _, a := range c.Axes {
		mark := "×"
		if a.Match {
			mark = "○"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | `%s` | %s |\n", a.Axis, a.A, a.B, a.Relation, mark)
	}
	for _, a := range c.Axes {
		if len(a.OnlyInA) > 0 {
			fmt.Fprintf(&b, "\n%s — %s 에만: %s\n", a.Axis, c.A, strings.Join(a.OnlyInA, " "))
		}
		if len(a.OnlyInB) > 0 {
			fmt.Fprintf(&b, "\n%s — %s 에만: %s\n", a.Axis, c.B, strings.Join(a.OnlyInB, " "))
		}
	}
	return b.String()
}
