// ============================================================
// dra.go: DRA 가용성 실측
// 상세: API 서빙 여부와 드라이버가 노드에 실제로 ResourceSlice 를 냈는가는 다른 사실이다(전자는
// 클러스터 업그레이드, 후자는 드라이버 설치가 답이다) — 그래서 구조체에서도 두 관측을 분리해 담는다.
// 생성일: 2026-08-05 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"slices"

	resourcev1 "k8s.io/api/resource/v1"
)

// DRACapability 는 이 클러스터가 지금 DRA 로 무엇을 줄 수 있는가다.
type DRACapability struct {
	// APIServed 는 resource.k8s.io/v1 이 서빙되는가다. false 면 드라이버 유무를 물을 수조차 없다.
	APIServed bool
	// DeviceClasses 는 존재하는 DeviceClass 이름 집합이다.
	DeviceClasses map[string]bool
	// SlicesByNodeDriver 는 노드 → 드라이버 → 그 노드가 그 드라이버로 내놓는 장치 수다.
	SlicesByNodeDriver map[string]map[string]int32
	// PoolsByNodeDriver 는 노드 → 드라이버 → 그 드라이버가 그 노드에서 쓰는 풀 이름 전부다.
	// 할당 집계가 ResourceClaim 결과의 pool 과 맞춰야 하므로 발행 쪽 이름을 그대로 보관한다 —
	// 풀 이름이 노드명이라는 규약에 기대지 않는다. 한 노드·드라이버가 풀 여럿으로 광고하면
	// 광고 수는 모든 풀의 합이므로 할당도 모든 풀에서 세야 한쪽만 세는 비대칭이 생기지 않는다.
	PoolsByNodeDriver map[string]map[string][]string
}

// BuildDRACapability 는 DeviceClass·ResourceSlice 목록을 노드별 DRA capability 로 접는다(순수 함수).
func BuildDRACapability(apiServed bool, classes []resourcev1.DeviceClass, resourceSlices []resourcev1.ResourceSlice) DRACapability {
	out := DRACapability{
		APIServed: apiServed, DeviceClasses: map[string]bool{},
		SlicesByNodeDriver: map[string]map[string]int32{}, PoolsByNodeDriver: map[string]map[string][]string{},
	}
	for i := range classes {
		out.DeviceClasses[classes[i].Name] = true
	}
	for i := range resourceSlices {
		s := &resourceSlices[i]
		// 노드에 묶이지 않은 슬라이스는 "어느 노드가 후보인가" 에 답하지 못한다.
		// 세지 않는다 — 세면 노드 없는 용량이 후보 판정을 통과시킨다.
		if s.Spec.NodeName == nil || *s.Spec.NodeName == "" || s.Spec.Driver == "" {
			continue
		}
		node := *s.Spec.NodeName
		if out.SlicesByNodeDriver[node] == nil {
			out.SlicesByNodeDriver[node] = map[string]int32{}
		}
		out.SlicesByNodeDriver[node][s.Spec.Driver] += int32(len(s.Spec.Devices))
		if out.PoolsByNodeDriver[node] == nil {
			out.PoolsByNodeDriver[node] = map[string][]string{}
		}
		// 같은 풀의 슬라이스가 여러 개여도 이름을 한 번만 담아 할당을 두 번 세지 않는다.
		if pool := s.Spec.Pool.Name; pool != "" &&
			!slices.Contains(out.PoolsByNodeDriver[node][s.Spec.Driver], pool) {
			out.PoolsByNodeDriver[node][s.Spec.Driver] =
				append(out.PoolsByNodeDriver[node][s.Spec.Driver], pool)
		}
	}
	return out
}
