// ============================================================
// testsupport.go: 시험이 능력 선언 한 칸을 갈아 끼우는 갈고리
// 상세: 레지스트리는 Go 리터럴 하나라 "한 벤더의 두 제품 등급이 갈린" 상황을 실제 선언으로
// 만들 수 없다. 게이트가 제품별로 등급을 보는지 고정하려면 선언을 잠시 갈아 끼워야
// 한다. 프로덕션 경로는 이 함수를 부르지 않는다.
// 생성일: 2026-10-01 | 수정일: 2026-10-11
// ============================================================
package vendorcap

// SetForTest 는 (vendor, product) 의 능력 한 칸을 s 로 갈아 끼우고, 원래대로 되돌리는
// 함수를 돌려준다. 시험은 t.Cleanup 에 그 함수를 건다.
func SetForTest(vendor, product string, c Capability, s Support) func() {
	k := Key{vendor, product}
	caps, existed := registry[k]
	if !existed {
		registry[k] = map[Capability]Support{c: s}
		return func() { delete(registry, k) }
	}
	prev, had := caps[c]
	caps[c] = s
	return func() {
		if had {
			caps[c] = prev
			return
		}
		delete(caps, c)
	}
}
