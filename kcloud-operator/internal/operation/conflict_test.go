// ============================================================
// conflict_test.go: 활성 operation 대비 admission 판정 테스트
// 상세: 충돌 판정표와 1:1. 특히 표가 미뤄 둔 "종속 판정의 범위 한정" 을 여기서 좁힌다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import "testing"

const (
	pciA = "0000:41:00.0"
	pciB = "0000:81:00.0"
)

func partitionClaim(name, pci, node string) Claim {
	return Claim{Name: name, Type: PartitionReconfigure,
		Keys: []ResourceKey{DevicePartitionKey(pci), NodeCordonKey(node)}}
}

// 증명: 활성 작업이 없으면 들어간다.
// 깨는 뮤테이션: Admit 의 기본 반환을 AdmitBlocked 로 바꾸면 실패한다.
func TestAdmitWithNoActiveOperations(t *testing.T) {
	if v, by := Admit(partitionClaim("a", pciA, "w1"), nil); v != AdmitNow || by != "" {
		t.Fatalf("v=%v by=%q", v, by)
	}
}

// 증명: 같은 장치를 다투는 두 파티션 작업은 직렬화된다. 이것이 이 단계의 존재 이유다.
// 깨는 뮤테이션: overlaps/sharesDevice 판정을 건너뛰면 실패한다.
func TestSameDeviceIsBlocked(t *testing.T) {
	active := []Claim{partitionClaim("first", pciA, "w1")}
	v, by := Admit(partitionClaim("second", pciA, "w1"), active)
	if v != AdmitBlocked || by != "first" {
		t.Fatalf("v=%v by=%q", v, by)
	}
}

// 증명: 다른 노드의 같은 종류 작업은 병렬이다(불필요한 직렬화 금지).
// 깨는 뮤테이션: 타입만 보고 막게 바꾸면 실패한다.
func TestDifferentNodeRunsInParallel(t *testing.T) {
	active := []Claim{partitionClaim("first", pciA, "w1")}
	if v, _ := Admit(partitionClaim("second", pciB, "w2"), active); v != AdmitNow {
		t.Fatalf("v=%v", v)
	}
}

// 증명: 드라이버 업그레이드와 파티션 재구성은 같은 노드이기만 해도 막힌다(둘 다 cordon 키 선언).
// 깨는 뮤테이션: NodeCordonKey 를 후보 키에서 빼면 실패한다.
func TestDriverUpgradeBlocksPartitionOnSameNode(t *testing.T) {
	active := []Claim{{Name: "drv", Type: DriverUpgrade,
		Keys: []ResourceKey{DeviceDriverKey(pciB), NodeCordonKey("w1"), NodeRebootKey("w1")}}}
	if v, by := Admit(partitionClaim("part", pciA, "w1"), active); v != AdmitBlocked || by != "drv" {
		t.Fatalf("v=%v by=%q", v, by)
	}
}

// 증명: 같은 타입 쌍이라도 자원이 안 겹치면 수락된다 — 위 테스트의 반대 방향이다.
// 두 방향을 같이 고정해야 "타입만 보고 막는" 구현과 "키까지 보는" 구현이 구분된다.
// 깨는 뮤테이션: touches/Conflicts 의 키 비교를 지우고 타입 쌍만으로 막으면 실패한다.
func TestDriverUpgradeOnOtherNodeIsParallel(t *testing.T) {
	active := []Claim{{Name: "drv", Type: DriverUpgrade,
		Keys: []ResourceKey{DeviceDriverKey(pciB), NodeCordonKey("w2"), NodeRebootKey("w2")}}}
	if v, by := Admit(partitionClaim("part", pciA, "w1"), active); v != AdmitNow {
		t.Fatalf("무관한 노드의 작업을 직렬화했다: v=%v by=%q", v, by)
	}
}

// 증명: 읽기 전용 작업은 자원이 겹쳐도 무엇과도 병렬이다.
// 후보에 활성 작업과 겹치는 키를 일부러 준다 — 키를 비우면 "읽기 전용이라서" 가 아니라
// "겹칠 키가 없어서" 통과해 버려 파괴성 분류를 전혀 검증하지 못한다.
// 깨는 뮤테이션: destructive 표에서 Revalidate 를 true 로 바꾸면 실패한다.
func TestRevalidateIsAlwaysAdmitted(t *testing.T) {
	active := []Claim{partitionClaim("first", pciA, "w1")}
	cand := Claim{Name: "reval", Type: Revalidate,
		Keys: []ResourceKey{DevicePartitionKey(pciA), NodeCordonKey("w1")}}
	if v, by := Admit(cand, active); v != AdmitNow {
		t.Fatalf("읽기 전용 작업을 막았다: v=%v by=%q", v, by)
	}
}

// 증명: 종속 쌍은 막히지 않고 "뒤에 실행" 으로 판정된다.
// 깨는 뮤테이션: AdmitAfter 를 AdmitBlocked 로 접으면 실패한다.
func TestDependentPairYieldsAdmitAfter(t *testing.T) {
	active := []Claim{partitionClaim("part", pciA, "w1")}
	cand := Claim{Name: "dp", Type: DevicePluginRestart,
		Keys: []ResourceKey{ClusterDPConfigKey("nvidia-device-plugin"), DevicePartitionKey(pciA)}}
	if v, by := Admit(cand, active); v != AdmitAfter || by != "part" {
		t.Fatalf("v=%v by=%q", v, by)
	}
}

// 증명: 자원이 하나도 안 겹치는 종속 쌍은 종속이 아니라 병렬이다(충돌 행렬 가
// Coordinator 구현 시점에 좁히라고 미뤄 둔 지점).
// 깨는 뮤테이션: Admit 에서 키 겹침 확인을 빼고 타입만으로 AdmitAfter 를 돌려주면 실패한다.
func TestUnrelatedDependentPairIsParallel(t *testing.T) {
	active := []Claim{partitionClaim("part", pciA, "w1")}
	cand := Claim{Name: "dp", Type: DevicePluginRestart,
		Keys: []ResourceKey{ClusterDPConfigKey("nvidia-device-plugin-flat")}}
	if v, by := Admit(cand, active); v != AdmitNow {
		t.Fatalf("무관한 종속 쌍을 직렬화했다: v=%v by=%q", v, by)
	}
}

// 증명: 여러 개가 막을 때 돌려주는 이름이 결정론적이다.
// 깨는 뮤테이션: 정렬을 빼면 슬라이스에 담긴 순서(zeta 가 먼저)가 그대로 나와 실패한다.
func TestBlockedByIsDeterministic(t *testing.T) {
	active := []Claim{partitionClaim("zeta", pciA, "w1"), partitionClaim("alpha", pciA, "w1")}
	for i := 0; i < 20; i++ {
		if _, by := Admit(partitionClaim("cand", pciA, "w1"), active); by != "alpha" {
			t.Fatalf("by=%q", by)
		}
	}
}

// 증명: 자기 자신은 자기를 막지 않는다(재진입 시 활성 목록에 자신이 들어 있다).
// 깨는 뮤테이션: 이름 비교를 빼면 operation 이 스스로에게 막혀 영원히 Blocked 가 된다.
func TestCandidateDoesNotBlockItself(t *testing.T) {
	c := partitionClaim("me", pciA, "w1")
	if v, _ := Admit(c, []Claim{c}); v != AdmitNow {
		t.Fatalf("v=%v", v)
	}
}
