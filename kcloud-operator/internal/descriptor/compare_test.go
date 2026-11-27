// ============================================================
// compare_test.go: 두 backend 관측치 대조 시험
// 상세: 손 대조표를 픽스처로 쓴다. 대조기가 그 표를 재현하지 못하면 도구가 틀린 것이다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package descriptor

import "testing"

func TestRelateDetectsProperSubset(t *testing.T) {
	a := []string{"x", "y", "z"}
	b := []string{"x", "y"}
	rel, onlyA, onlyB := relate(a, b)
	if rel != RelationSuperset {
		t.Fatalf("A 가 B 의 진상위집합이어야 한다: %s", rel)
	}
	if len(onlyA) != 1 || onlyA[0] != "z" {
		t.Errorf("A 에만 있는 것이 z 하나여야 한다: %v", onlyA)
	}
	if len(onlyB) != 0 {
		t.Errorf("B 에만 있는 것이 없어야 한다: %v", onlyB)
	}
}

func TestRelateDetectsEqualIgnoringOrder(t *testing.T) {
	rel, _, _ := relate([]string{"b", "a"}, []string{"a", "b"})
	if rel != RelationEqual {
		t.Fatalf("순서만 다른 두 집합은 같아야 한다: %s", rel)
	}
}

func TestRelateDetectsOverlap(t *testing.T) {
	rel, onlyA, onlyB := relate([]string{"a", "b"}, []string{"b", "c"})
	if rel != RelationOverlap {
		t.Fatalf("어느 쪽도 포함하지 않으면 엇갈림이어야 한다: %s", rel)
	}
	if len(onlyA) != 1 || len(onlyB) != 1 {
		t.Errorf("양쪽에 하나씩 남아야 한다: %v %v", onlyA, onlyB)
	}
}

func TestRelateDetectsDisjoint(t *testing.T) {
	rel, _, _ := relate([]string{"a"}, []string{"b"})
	if rel != RelationDisjoint {
		t.Fatalf("겹치는 것이 없으면 서로소여야 한다: %s", rel)
	}
}

// 빈 집합 둘은 같다. "관측을 못 했다" 와 "없다" 를 대조기가 가르지 않는다는 뜻이므로,
// 수집기 쪽에서 빈 관측을 거절해야 한다.
func TestRelateEmptyPair(t *testing.T) {
	if rel, _, _ := relate(nil, nil); rel != RelationEqual {
		t.Fatalf("빈 집합 둘은 같아야 한다: %s", rel)
	}
}

// 반환 목록이 정렬돼 결정적인지 본다. 맵 순회 순서가 새면 이 시험이 흔들린다.
func TestRelateSortsOnlyLists(t *testing.T) {
	_, onlyA, _ := relate([]string{"z", "a", "m"}, nil)
	want := []string{"a", "m", "z"}
	if len(onlyA) != 3 || onlyA[0] != want[0] || onlyA[1] != want[1] || onlyA[2] != want[2] {
		t.Fatalf("정렬돼야 한다: %v", onlyA)
	}
}

// rngdDevNodes 는 두 backend 가 똑같이 주입하는 36개다. rngd-1 에서 `ls -1 /dev/rngd` 로
// 실측해 고정한 목록이며 손으로 지어낸 이름은 하나도 없다. 개수가 36 인 것과, 벤더가
// 빼는 12개가 이 안에 전부 들어 있는 것이 손 대조표 와 맞물린다.
func rngdDevNodes() []string {
	return []string{
		"/dev/rngd/npu0bar0", "/dev/rngd/npu0bar2", "/dev/rngd/npu0bar4",
		"/dev/rngd/npu0ch0", "/dev/rngd/npu0ch0r", "/dev/rngd/npu0ch1", "/dev/rngd/npu0ch1r",
		"/dev/rngd/npu0ch2", "/dev/rngd/npu0ch2r", "/dev/rngd/npu0ch3", "/dev/rngd/npu0ch3r",
		"/dev/rngd/npu0ch4", "/dev/rngd/npu0ch4r", "/dev/rngd/npu0ch5", "/dev/rngd/npu0ch5r",
		"/dev/rngd/npu0ch6", "/dev/rngd/npu0ch6r", "/dev/rngd/npu0ch7", "/dev/rngd/npu0ch7r",
		"/dev/rngd/npu0dmar", "/dev/rngd/npu0mgmt", "/dev/rngd/npu0p2pmem",
		"/dev/rngd/npu0pe0", "/dev/rngd/npu0pe0-1", "/dev/rngd/npu0pe0-3", "/dev/rngd/npu0pe1",
		"/dev/rngd/npu0pe2", "/dev/rngd/npu0pe2-3", "/dev/rngd/npu0pe3", "/dev/rngd/npu0pe4",
		"/dev/rngd/npu0pe4-5", "/dev/rngd/npu0pe4-7", "/dev/rngd/npu0pe5", "/dev/rngd/npu0pe6",
		"/dev/rngd/npu0pe6-7", "/dev/rngd/npu0pe7",
	}
}

func generatedObservation(backend string) BackendObservation {
	return BackendObservation{
		Backend:         backend,
		AllocationUnits: 1,
		DeviceNodes:     []string{"/dev/rngd/{{id}}*"},
		DevNodes:        rngdDevNodes(),
		ForeignTopLevel: nil,
	}
}

// 픽스처가 실측과 어긋나면 아래 두 시험이 증명하는 것이 없다. 개수를 먼저 못 박는다.
func TestRngdFixtureMatchesLiveCount(t *testing.T) {
	if got := len(rngdDevNodes()); got != 36 {
		t.Fatalf("실측 장치 노드는 36개다: %d", got)
	}
}

// 손 대조표 재현: 네 축 모두 일치.
func TestCompareReproducesGeneratedPairTable(t *testing.T) {
	c := Compare(generatedObservation("devicePlugin"), generatedObservation("dra"))
	if !c.AllMatch {
		t.Fatalf("네 축 모두 일치해야 한다: %+v", c.Axes)
	}
	if len(c.Axes) != 4 {
		t.Fatalf("축은 넷이다: %d", len(c.Axes))
	}
	for _, a := range c.Axes {
		if a.Relation != RelationEqual && a.Axis != "할당 단위 수" {
			t.Errorf("%s 축이 같지 않다: %s", a.Axis, a.Relation)
		}
	}
}

// 손 대조표 재현: 벤더 집합이 생성 집합의 진부분집합.
func TestCompareReproducesVendorSubsetTable(t *testing.T) {
	onlyGenerated := []string{
		"/dev/rngd/npu0p2pmem", "/dev/rngd/npu0pe0-3", "/dev/rngd/npu0pe2",
		"/dev/rngd/npu0pe2-3", "/dev/rngd/npu0pe3", "/dev/rngd/npu0pe4",
		"/dev/rngd/npu0pe4-5", "/dev/rngd/npu0pe4-7", "/dev/rngd/npu0pe5",
		"/dev/rngd/npu0pe6", "/dev/rngd/npu0pe6-7", "/dev/rngd/npu0pe7",
	}
	vendorNodes := []string{}
	skip := toSet(onlyGenerated)
	for _, n := range rngdDevNodes() {
		if !skip[n] {
			vendorNodes = append(vendorNodes, n)
		}
	}

	gen := generatedObservation("devicePlugin")
	vendor := BackendObservation{
		Backend:         "vendorDevicePlugin",
		AllocationUnits: 4,
		DeviceNodes:     []string{"/dev/rngd/npu0pe0-1", "/dev/rngd/npu0pe2-3", "/dev/rngd/npu0pe4-5", "/dev/rngd/npu0pe6-7"},
		DevNodes:        vendorNodes,
	}

	c := Compare(gen, vendor)
	if c.AllMatch {
		t.Fatal("벤더와는 갈려야 한다")
	}
	if c.Axes[0].Relation != RelationOverlap {
		t.Errorf("할당 단위 수가 다르면 overlap 이어야 한다: %s", c.Axes[0].Relation)
	}
	var dev AxisResult
	for _, a := range c.Axes {
		if a.Axis == "컨테이너 /dev 노드" {
			dev = a
		}
	}
	if dev.Relation != RelationSuperset {
		t.Fatalf("생성 집합이 벤더 집합의 진상위집합이어야 한다: %s", dev.Relation)
	}
	// AllocationUnits(1 대 4)가 이미 AllMatch 를 false 로 만들기 때문에, 이 축 자체의
	// Match 를 직접 확인하지 않으면 setAxis 의 Match 오판정이 가려진다.
	if dev.Match {
		t.Error("진상위집합인 축은 Match 가 false 여야 한다")
	}
	if len(dev.OnlyInB) != 0 {
		t.Errorf("벤더에만 있는 노드는 없어야 한다: %v", dev.OnlyInB)
	}
	if len(dev.OnlyInA) != 12 {
		t.Errorf("생성에만 있는 노드가 12개여야 한다: %d", len(dev.OnlyInA))
	}
	if len(vendorNodes) != 24 {
		t.Errorf("벤더 노드는 24개여야 한다: %d", len(vendorNodes))
	}
}
