// ============================================================
// model_test.go: operation type·resource key·충돌 판정 계약 테스트
// 상세: 충돌 판정표와 1:1 대응한다. 표를 바꾸면 이 테스트도 바꿔야 한다.
// 생성일: 2026-07-31
// ============================================================
package operation

import "testing"

func TestResourceKeyFormats(t *testing.T) {
	for _, tc := range []struct {
		got  ResourceKey
		want string
	}{
		{NodeCordonKey("worker1"), "node/worker1/cordon"},
		{NodeRebootKey("worker1"), "node/worker1/reboot"},
		{DeviceDriverKey("0000:17:00.0"), "device/0000:17:00.0/driver"},
		{DevicePartitionKey("0000:17:00.0"), "device/0000:17:00.0/partition"},
		{DeviceSharingKey("0000:17:00.0"), "device/0000:17:00.0/sharing"},
		{ClusterDPConfigKey("nvidia-device-plugin"), "cluster/nvidia-device-plugin/config"},
		{ClusterMPSDaemonKey(), "cluster/nvidia-mps-daemon"},
	} {
		if string(tc.got) != tc.want {
			t.Fatalf("key = %q, want %q", tc.got, tc.want)
		}
	}
}

func TestConflictMatrixMatchesDesignDoc(t *testing.T) {
	pci := "0000:17:00.0"
	drv := []ResourceKey{DeviceDriverKey(pci), NodeRebootKey("worker1"), NodeCordonKey("worker1")}
	part := []ResourceKey{DevicePartitionKey(pci), NodeCordonKey("worker1")}
	share := []ResourceKey{DeviceSharingKey(pci), ClusterDPConfigKey("nvidia-device-plugin")}
	otherNode := []ResourceKey{DevicePartitionKey("0000:af:00.0"), NodeCordonKey("worker2")}
	// 장치는 다르고 device-plugin ConfigMap 만 공유한다 — cluster 단위 공유 상태가 충돌의
	// 유일한 이유가 되게 해서 정확 키 일치 규칙을 고정한다.
	shareOtherDevice := []ResourceKey{DeviceSharingKey("0000:af:00.0"), ClusterDPConfigKey("nvidia-device-plugin")}

	cases := []struct {
		name   string
		a, b   Type
		ka, kb []ResourceKey
		want   Verdict
	}{
		{"driver vs partition", DriverUpgrade, PartitionReconfigure, drv, part, VerdictConflict},
		{"driver vs sharing", DriverUpgrade, SharingModeChange, drv, share, VerdictConflict},
		{"partition vs sharing (같은 장치)", PartitionReconfigure, SharingModeChange, part, share, VerdictConflict},
		{"dp restart vs partition", DevicePluginRestart, PartitionReconfigure, share, part, VerdictDependent},
		{"다른 노드", PartitionReconfigure, PartitionReconfigure, part, otherNode, VerdictParallel},
		{"같은 DP ConfigMap", SharingModeChange, SharingModeChange, share, shareOtherDevice, VerdictConflict},
		// 후보에 상대와 겹치는 키를 준다 — 키를 비우면 "읽기 전용이라서" 가 아니라 "겹칠 키가
		// 없어서" 병렬이 나와 파괴성 분류를 전혀 고정하지 못한다.
		{"revalidate 는 읽기 전용", Revalidate, PartitionReconfigure, part, part, VerdictParallel},
		// 장치 리셋은 되돌릴 수 없으므로 같은 노드의 다른 파괴적 작업과 겹칠 수 없다.
		{"device reset vs dp restart", DeviceReset, DevicePluginRestart, part, part, VerdictConflict},
	}
	for _, tc := range cases {
		if got := Conflicts(tc.a, tc.b, tc.ka, tc.kb); got != tc.want {
			t.Errorf("%s: Conflicts = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestConflictIsSymmetric(t *testing.T) {
	pci := "0000:17:00.0"
	drv := []ResourceKey{DeviceDriverKey(pci)}
	part := []ResourceKey{DevicePartitionKey(pci)}
	if Conflicts(DriverUpgrade, PartitionReconfigure, drv, part) != Conflicts(PartitionReconfigure, DriverUpgrade, part, drv) {
		t.Fatalf("충돌 판정은 대칭이어야 한다")
	}
}

func TestAllOperationTypesAreClassified(t *testing.T) {
	// 새 operation type 을 추가하고 충돌 규칙을 빠뜨리는 것을 막는다.
	for _, ty := range AllTypes() {
		if _, ok := destructive[ty]; !ok {
			t.Fatalf("operation type %q 의 파괴성 분류가 없다", ty)
		}
	}
}

// keySets 는 대칭성·전면성 검사에 쓰는 자원 키 조합이다. 장치 겹침(같은 PCI 의 다른 facet),
// 노드 겹침, 클러스터 겹침, 겹침 없음, 빈 키를 모두 포함해 판정 분기를 빠짐없이 밟는다.
func keySets() map[string][]ResourceKey {
	const pciA, pciB = "0000:17:00.0", "0000:af:00.0"
	return map[string][]ResourceKey{
		"empty":       {},
		"driverA":     {DeviceDriverKey(pciA), NodeRebootKey("worker1"), NodeCordonKey("worker1")},
		"partitionA":  {DevicePartitionKey(pciA), NodeCordonKey("worker1")},
		"sharingA":    {DeviceSharingKey(pciA), ClusterDPConfigKey("nvidia-device-plugin")},
		"partitionB":  {DevicePartitionKey(pciB), NodeCordonKey("worker2")},
		"clusterOnly": {ClusterMPSDaemonKey()},
	}
}

// TestConflictIsSymmetricForAllTypePairs 는 11종 전체 쌍 × 자원 키 조합 전체에서
// Conflicts(a,b) == Conflicts(b,a) 임을 확인한다. 한 쌍만 표본 검사하면 비대칭 규칙이
// 새로 들어와도 통과하므로, 전수로 돌린다.
func TestConflictIsSymmetricForAllTypePairs(t *testing.T) {
	sets := keySets()
	for _, a := range AllTypes() {
		for _, b := range AllTypes() {
			for na, ka := range sets {
				for nb, kb := range sets {
					ab := Conflicts(a, b, ka, kb)
					ba := Conflicts(b, a, kb, ka)
					if ab != ba {
						t.Errorf("비대칭: Conflicts(%s,%s,%s,%s)=%v 인데 역방향은 %v", a, b, na, nb, ab, ba)
					}
				}
			}
		}
	}
}

// TestDestructiveTableCoversExactlyAllTypes 는 파괴성 표가 11종을 정확히 덮는지 본다.
// AllTypes 에 없는 유령 항목이나 중복이 있으면 표가 코드 정본 구실을 못한다.
func TestDestructiveTableCoversExactlyAllTypes(t *testing.T) {
	all := AllTypes()
	if len(all) != 11 {
		t.Fatalf("AllTypes 는 11종이어야 한다, got %d", len(all))
	}
	seen := map[Type]bool{}
	for _, ty := range all {
		if seen[ty] {
			t.Fatalf("AllTypes 에 %q 가 중복이다", ty)
		}
		seen[ty] = true
	}
	if len(destructive) != len(all) {
		t.Fatalf("파괴성 표 항목 %d 개, AllTypes %d 종 — 개수가 다르다", len(destructive), len(all))
	}
	for ty := range destructive {
		if !seen[ty] {
			t.Errorf("파괴성 표에 AllTypes 에 없는 %q 가 있다", ty)
		}
	}
}

// TestUnclassifiedTypeIsTreatedAsDestructive 는 표에 없는 타입이 조용히 "병렬 가능" 으로
// 새는 것을 막는다. 미분류는 보수적으로 파괴적 취급이라 자원이 겹치면 충돌로 잡혀야 한다.
func TestUnclassifiedTypeIsTreatedAsDestructive(t *testing.T) {
	part := []ResourceKey{DevicePartitionKey("0000:17:00.0")}
	if got := Conflicts(Type("FutureUnlistedOp"), PartitionReconfigure, part, part); got != VerdictConflict {
		t.Fatalf("미분류 타입의 자원 겹침 = %v, want %v", got, VerdictConflict)
	}
}
