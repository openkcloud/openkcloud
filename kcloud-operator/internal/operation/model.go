// ============================================================
// model.go: operation type · resource key · 충돌 판정
// 상세: Coordinator 가 따르는 충돌 판정 계약의 코드 정본이다. 이 패키지는 controller/partition 을
// import 하지 않는다(최하위, 순환 금지).
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"fmt"
	"strings"
)

// Type 은 Coordinator 가 관리하는 작업 종류다.
type Type string

const (
	DriverInstall        Type = "DriverInstall"
	DriverUpgrade        Type = "DriverUpgrade"
	DriverRollback       Type = "DriverRollback"
	PartitionReconfigure Type = "PartitionReconfigure"
	SharingModeChange    Type = "SharingModeChange"
	DevicePluginRestart  Type = "DevicePluginRestart"
	NodeReboot           Type = "NodeReboot"
	Revalidate           Type = "Revalidate"
	Quarantine           Type = "Quarantine"
	RecoverDevice        Type = "RecoverDevice"
	// DeviceReset 은 descriptor 의 resetCommand 를 노드에서 실행한다. 관리자만 만든다 —
	// 되돌릴 수 없으므로 health 가 자동으로 발행하지 않는다.
	DeviceReset Type = "DeviceReset"
)

// AllTypes 는 분류 누락을 테스트가 잡을 수 있도록 전체 목록을 준다.
func AllTypes() []Type {
	return []Type{DriverInstall, DriverUpgrade, DriverRollback, PartitionReconfigure,
		SharingModeChange, DevicePluginRestart, NodeReboot, Revalidate, Quarantine, RecoverDevice,
		DeviceReset}
}

// destructive 는 실제 장치·호스트 상태를 바꾸는 작업인지다. 읽기 전용 작업은 서로, 그리고
// 파괴적 작업과도 병렬 가능하다(관측은 mutation 을 방해하지 않는다).
var destructive = map[Type]bool{
	DriverInstall: true, DriverUpgrade: true, DriverRollback: true,
	PartitionReconfigure: true, SharingModeChange: true, DevicePluginRestart: true,
	NodeReboot: true, RecoverDevice: true, DeviceReset: true,
	Revalidate: false, Quarantine: false, // Quarantine 은 스케줄 차단(라벨/taint)이라 장치를 안 바꾼다.
}

// ResourceKey 는 작업이 읽고 쓰는 자원의 이름이다.
type ResourceKey string

func NodeCordonKey(node string) ResourceKey { return ResourceKey(fmt.Sprintf("node/%s/cordon", node)) }
func NodeRebootKey(node string) ResourceKey { return ResourceKey(fmt.Sprintf("node/%s/reboot", node)) }
func DeviceDriverKey(pci string) ResourceKey {
	return ResourceKey(fmt.Sprintf("device/%s/driver", pci))
}
func DevicePartitionKey(pci string) ResourceKey {
	return ResourceKey(fmt.Sprintf("device/%s/partition", pci))
}
func DeviceSharingKey(pci string) ResourceKey {
	return ResourceKey(fmt.Sprintf("device/%s/sharing", pci))
}
func ClusterDPConfigKey(dsName string) ResourceKey {
	return ResourceKey(fmt.Sprintf("cluster/%s/config", dsName))
}
func ClusterMPSDaemonKey() ResourceKey { return ResourceKey("cluster/nvidia-mps-daemon") }

// Verdict 는 두 작업의 동시 실행 가능성이다.
type Verdict int

const (
	// VerdictParallel: 동시에 실행해도 된다.
	VerdictParallel Verdict = iota
	// VerdictConflict: 동시에 실행하면 안 된다 — 뒤의 작업은 대기하거나 거부된다.
	VerdictConflict
	// VerdictDependent: 순서 의존 — B 는 A 의 후속 단계로 실행될 수 있다(예: partition 후 DP 재시작).
	VerdictDependent
)

// isDestructive 는 파괴성 표를 조회하되, 표에 없는 타입은 보수적으로 파괴적으로 본다.
// 미분류 타입이 조용히 "누구와도 병렬" 로 새면 Coordinator 가 직렬화해야 할 작업을
// 동시에 흘려보낸다 — 안전한 쪽으로 틀리게 한다. 표의 전면성은 테스트가 따로 강제한다.
func isDestructive(t Type) bool {
	d, ok := destructive[t]
	return !ok || d
}

// Conflicts 는 두 작업의 자원 키 교집합과 종류를 보고 판정한다.
// 규칙 순서가 중요하다: 종속 관계를 먼저 보고, 그 다음 자원 겹침을 본다.
func Conflicts(a, b Type, keysA, keysB []ResourceKey) Verdict {
	if !isDestructive(a) || !isDestructive(b) {
		return VerdictParallel // 읽기 전용은 누구와도 병렬
	}
	if isDependentPair(a, b) {
		return VerdictDependent
	}
	// 같은 키를 직접 다투는 경우와, facet 은 달라도 같은 물리 장치를 건드리는 경우 둘 다 충돌이다.
	if overlaps(keysA, keysB) || sharesDevice(keysA, keysB) {
		return VerdictConflict
	}
	return VerdictParallel
}

// isDependentPair 는 "충돌이 아니라 순서"인 쌍이다(종속 행).
func isDependentPair(a, b Type) bool {
	return (a == DevicePluginRestart && b == PartitionReconfigure) ||
		(b == DevicePluginRestart && a == PartitionReconfigure)
}

// deviceOf 는 `device/<pci>/<facet>` 키에서 PCI 주소를 뽑는다. 장치 키가 아니면 빈 문자열이다.
// PCI 주소에는 `/` 가 없으므로 첫 구분자까지가 곧 주소다.
//
// 전제: 모든 장치 키는 facet 을 가진다. facet 없는 `device/<pci>` 형태를 만드는 생성자가
// 새로 생기면 여기서 빈 문자열이 나와 장치 겹침 판정을 조용히 빠져나간다 — 그런 생성자를
// 추가할 때는 이 함수의 파싱을 함께 고쳐야 한다.
func deviceOf(k ResourceKey) string {
	rest, ok := strings.CutPrefix(string(k), "device/")
	if !ok {
		return ""
	}
	pci, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return pci
}

// sharesDevice 는 두 작업이 같은 물리 장치를 건드리는지다. facet(driver·partition·sharing)이
// 달라도 한 장치의 상태는 하나뿐이라 서로를 무효화한다 — driver reload 는 MIG geometry 를,
// partition 재구성은 sharing 복제 수를 무효화한다. 따라서 키가 정확히 같지 않아도 충돌이다.
func sharesDevice(a, b []ResourceKey) bool {
	set := make(map[string]struct{}, len(a))
	for _, k := range a {
		if pci := deviceOf(k); pci != "" {
			set[pci] = struct{}{}
		}
	}
	for _, k := range b {
		if pci := deviceOf(k); pci != "" {
			if _, ok := set[pci]; ok {
				return true
			}
		}
	}
	return false
}

func overlaps(a, b []ResourceKey) bool {
	set := make(map[ResourceKey]struct{}, len(a))
	for _, k := range a {
		set[k] = struct{}{}
	}
	for _, k := range b {
		if _, ok := set[k]; ok {
			return true
		}
	}
	return false
}
