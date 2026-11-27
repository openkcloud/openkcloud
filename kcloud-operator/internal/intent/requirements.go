// ============================================================
// requirements.go: 격리 등급·최소 메모리 게이트
// 상세: 근거가 없으면 만족으로 치지 않는다(fail closed). 공유 계열 모드는 replica 간 격리가
// 모드별로 미실측이라 none 으로 강등해 비교한다(이유는 checkIsolation 참고) — 거절 메시지는
// 장치의 등급이 아니라 이 게이트가 쓰는 값을 밝힌다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"kcloud-operator/api/v1alpha1"
)

const (
	// mibPerGiB 는 profile 이름의 "6gb" 를 GiB 로 읽을 때 쓰는 배수다.
	// NVIDIA MIG 와 RNGD 모두 profile 표기의 숫자를 GiB 급으로 쓴다.
	mibPerGiB = 1024
	// bytesPerMiB 는 resource.Quantity(바이트)를 MiB 로 옮길 때 쓴다.
	bytesPerMiB = 1024 * 1024
)

// profileSizeRe 는 profile 꼬리의 메모리 표기를 잡는다(1g.6gb, 2core.12gb 등).
var profileSizeRe = regexp.MustCompile(`(?i)(\d+)gb$`)

// isolationOrder 는 격리 등급의 순서다. 모르는 값·빈 값은 -1 로,
// 어떤 요구도 만족하지 못한다.
var isolationOrder = map[string]int{
	v1alpha1.IsolationNone:      0,
	v1alpha1.IsolationProcess:   1,
	v1alpha1.IsolationSubdevice: 2,
	v1alpha1.IsolationDevice:    3,
	v1alpha1.IsolationHardware:  4,
}

// IsolationRank 는 격리 등급의 서열이다. 미실측(빈 문자열)은 -1 이다.
func IsolationRank(level string) int {
	if r, ok := isolationOrder[level]; ok {
		return r
	}
	return -1
}

// ProfileMemoryMiB 는 벤더 profile 이름에 인코딩된 메모리를 읽는다.
// 인코딩이 없으면 (0,false) — 호출부는 이를 "모른다" 로 다뤄 거절해야 한다.
func ProfileMemoryMiB(profile string) (int64, bool) {
	m := profileSizeRe.FindStringSubmatch(strings.TrimSpace(profile))
	if m == nil {
		return 0, false
	}
	gib, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || gib <= 0 {
		return 0, false
	}
	return gib * mibPerGiB, true
}

// MergeRequirements 는 클래스 요구와 워크로드 요구 중 강한 쪽을 남긴다.
// 워크로드가 클래스를 완화할 수는 없다.
func MergeRequirements(base v1alpha1.AcceleratorRequirements, override *v1alpha1.AcceleratorRequirements) v1alpha1.AcceleratorRequirements {
	out := *base.DeepCopy()
	if override == nil {
		return out
	}
	if IsolationRank(override.MinimumIsolation) > IsolationRank(out.MinimumIsolation) {
		out.MinimumIsolation = override.MinimumIsolation
	}
	if override.MinimumMemory != nil && (out.MinimumMemory == nil || override.MinimumMemory.Value() > out.MinimumMemory.Value()) {
		q := override.MinimumMemory.DeepCopy()
		out.MinimumMemory = &q
	}
	return out
}

// CheckRequirements 는 노드가 요구사항을 만족하는지 본다. nil 이면 만족이다.
// CheckMode 와 나란히 호출해야 한다 — 이 게이트는 isolation/memory 축만 보고 mode 축은
// 보지 않는다. 다만 Stale 만은 두 게이트가 서로 다른 축을 보더라도 항상 거절해야 하는
// 공통 전제라, 호출 순서에 기대지 않고 여기서도 스스로 확인한다.
func CheckRequirements(nc NodeCapability, req v1alpha1.AcceleratorRequirements, access v1alpha1.AccessSpec, m v1alpha1.AcceleratorMapping) *Reject {
	if nc.Stale {
		return &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonCapabilityUnverified,
			Message: fmt.Sprintf("node %s: capability data is not trustworthy — %s", nc.NodeName, nc.StaleReason)}
	}
	if rj := checkIsolation(nc, req, access); rj != nil {
		return rj
	}
	return checkMemory(nc, req, access, m)
}

func checkIsolation(nc NodeCapability, req v1alpha1.AcceleratorRequirements, access v1alpha1.AccessSpec) *Reject {
	want := IsolationRank(req.MinimumIsolation)
	if want < 0 {
		return nil // 요구 없음
	}
	// 공유 계열은 replica 사이 격리가 모드별로 실측돼 있지 않아 none 으로 비교한다.
	// ACPP status 가 MPS 에 process 를 적는 것과 여기가 갈리는 지점이다(파일 헤더 참고).
	// 메시지가 장치를 "격리 없음" 이라고 단정하지 않는 이유가 그것이다 — 같은 노드의
	// status 와 모순되게 읽히면 사용자가 둘을 화해시킬 방법이 없다.
	if access.Mode == v1alpha1.AccessModeShared || access.Mode == v1alpha1.AccessModePartitionedShared {
		if want > IsolationRank(v1alpha1.IsolationNone) {
			return &Reject{Axis: AxisIsolation, Reason: v1alpha1.AWReasonIsolationTooWeak,
				Message: fmt.Sprintf("mode %q: replica isolation is not measured per mode, so this gate compares at %q, which cannot satisfy minimumIsolation=%q (ACPP status may report a higher per-axis grade for the same node)",
					access.Mode, v1alpha1.IsolationNone, req.MinimumIsolation)}
		}
		return nil
	}
	if len(nc.Devices) == 0 {
		return &Reject{Axis: AxisIsolation, Reason: v1alpha1.AWReasonCapabilityUnverified,
			Message: fmt.Sprintf("node %s reports no device isolation data, so minimumIsolation=%q cannot be confirmed", nc.NodeName, req.MinimumIsolation)}
	}
	for _, dev := range nc.Devices {
		iso := dev.IsolationCapability
		// Fault 는 일부러 뺀다 — 이 축이 재는 것은 장애 격리(고장전파 반경)지
		// minimumIsolation(, 테넌트 간 compute/memory 분리)이 묻는 것이 아니다.
		// map 이 아니라 슬라이스로 돈다 — map 순회는 무작위라 두 축이 모두 실패하면 거절 메시지가
		// 조정마다 바뀌고, 2분 주기 재조정이 그때마다 status condition 을 다시 쓴다.
		for _, axis := range []struct{ name, level string }{{"compute", iso.Compute}, {"memory", iso.Memory}} {
			axisName, level := axis.name, axis.level
			rank := IsolationRank(level)
			if rank < 0 {
				return &Reject{Axis: AxisIsolation, Reason: v1alpha1.AWReasonCapabilityUnverified,
					Message: fmt.Sprintf("node %s device %s: isolation.%s is unmeasured, so minimumIsolation=%q cannot be confirmed", nc.NodeName, dev.ID, axisName, req.MinimumIsolation)}
			}
			if rank < want {
				return &Reject{Axis: AxisIsolation, Reason: v1alpha1.AWReasonIsolationTooWeak,
					Message: fmt.Sprintf("node %s device %s: isolation.%s=%q is weaker than minimumIsolation=%q", nc.NodeName, dev.ID, axisName, level, req.MinimumIsolation)}
			}
		}
	}
	return nil
}

func checkMemory(nc NodeCapability, req v1alpha1.AcceleratorRequirements, access v1alpha1.AccessSpec, m v1alpha1.AcceleratorMapping) *Reject {
	if req.MinimumMemory == nil {
		return nil
	}
	wantMiB := req.MinimumMemory.Value() / bytesPerMiB
	availMiB, source := nc.MemoryMiB, "NodeDeviceReport memoryMiB"
	if access.Mode == v1alpha1.AccessModePartitioned || access.Mode == v1alpha1.AccessModePartitionedShared {
		// 분할 노드에서 전체 장치 메모리와 비교하면 과대 보고다 — profile 이 주는 몫과 비교한다.
		mib, ok := ProfileMemoryMiB(m.NativeProfile)
		if !ok {
			return &Reject{Axis: AxisMemory, Reason: v1alpha1.AWReasonMemoryUnknown,
				Message: fmt.Sprintf("profile %q does not encode its memory size, so minimumMemory=%s cannot be checked", m.NativeProfile, req.MinimumMemory)}
		}
		availMiB, source = mib, fmt.Sprintf("profile %s", m.NativeProfile)
	}
	if availMiB <= 0 {
		return &Reject{Axis: AxisMemory, Reason: v1alpha1.AWReasonMemoryUnknown,
			Message: fmt.Sprintf("node %s reports no device memory, so minimumMemory=%s cannot be checked", nc.NodeName, req.MinimumMemory)}
	}
	if availMiB < wantMiB {
		return &Reject{Axis: AxisMemory, Reason: v1alpha1.AWReasonMemoryTooSmall,
			Message: fmt.Sprintf("node %s offers %d MiB (%s) but minimumMemory=%s needs %d MiB", nc.NodeName, availMiB, source, req.MinimumMemory, wantMiB)}
	}
	return nil
}
