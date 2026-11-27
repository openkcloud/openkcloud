// ============================================================
// conflict.go: 활성 operation 대비 admission 판정
// 상세: 두 작업 사이의 판정(Conflicts) 위에 "지금 들여보내도 되는가" 를 얹는다. 종속 판정의
// 범위 한정(충돌 행렬의 미결 항목)도 여기서 처리한다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"sort"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// Claim 은 한 operation 이 주장하는 자원과 종류다.
type Claim struct {
	Name string
	Type Type
	Keys []ResourceKey
}

// Admission 은 후보를 지금 실행해도 되는지다.
type Admission int

const (
	// AdmitNow: 지금 진행해도 된다.
	AdmitNow Admission = iota
	// AdmitBlocked: 충돌하는 활성 작업이 있다 — 끝날 때까지 기다린다.
	AdmitBlocked
	// AdmitAfter: 충돌이 아니라 순서다 — 선행 작업의 후속 단계로 실행한다.
	AdmitAfter
)

// ClaimOf 는 CR 에서 판정 입력을 뽑는다.
func ClaimOf(op *v1alpha1.AcceleratorOperation) Claim {
	keys := make([]ResourceKey, 0, len(op.Spec.ResourceKeys))
	for _, k := range op.Spec.ResourceKeys {
		keys = append(keys, ResourceKey(k))
	}
	return Claim{Name: op.Name, Type: Type(op.Spec.Type), Keys: keys}
}

// Admit 은 활성 작업 전체에 대해 후보를 판정한다. 충돌이 우선이다 — 하나라도 막고 있으면 종속
// 관계보다 그쪽이 이긴다. 막는 것이 여럿이면 이름 사전순 첫 번째를 돌려준다(결정론: 판정이 흔들리면
// status 가 매 reconcile 마다 바뀐다). 종속(AdmitAfter)은 자원이 실제로 겹칠 때만 성립한다 —
// Conflicts 는 타입 쌍만 보고 종속을 판정하므로 무관한 노드의 두 작업까지 순서화해 버린다. 그
// 범위 한정이 여기다.
func Admit(candidate Claim, active []Claim) (Admission, string) {
	sorted := make([]Claim, len(active))
	copy(sorted, active)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var after string
	for _, a := range sorted {
		if a.Name == candidate.Name {
			continue // 재진입 — 자기 자신은 자기를 막지 않는다.
		}
		switch Conflicts(candidate.Type, a.Type, candidate.Keys, a.Keys) {
		case VerdictConflict:
			return AdmitBlocked, a.Name
		case VerdictDependent:
			if after == "" && touches(candidate.Keys, a.Keys) {
				after = a.Name
			}
		}
	}
	if after != "" {
		return AdmitAfter, after
	}
	return AdmitNow, ""
}

// touches 는 두 키 집합이 같은 자원이나 같은 물리 장치를 건드리는지다.
// Conflicts 의 겹침 규칙과 같은 기준을 쓴다 — 다른 기준을 쓰면 두 판정이 갈라진다.
func touches(a, b []ResourceKey) bool {
	return overlaps(a, b) || sharesDevice(a, b)
}
