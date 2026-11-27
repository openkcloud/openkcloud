// ============================================================
// mode.go: 추상 접근 모드 지원 판정
// 상세: 사용자 의도가 이 노드에서 성립하는지만 본다. 실패하면 축(Axis)·사유(Reason)를 담은 Reject
// 를 돌려줄 뿐 다른 모드로 갈아끼우지 않는다. verified 로 보고되지 않은 공유 방식으로는 라우팅하지 않는다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"fmt"
	"strings"

	"kcloud-operator/api/v1alpha1"
)

// 거절 축. 사용자 YAML 의 경로를 그대로 쓴다 — 어디를 고쳐야 하는지 바로 보이게.
const (
	AxisMode           = "spec.accelerator.access.mode"
	AxisReplicas       = "spec.accelerator.access.replicas"
	AxisImplementation = "spec.accelerator.access.implementation"
	AxisProfile        = "spec.accelerator.class.mappings[].nativeProfile"
	AxisProduct        = "spec.accelerator.class.mappings[].product"
	AxisIsolation      = "spec.accelerator.requirements.minimumIsolation"
	AxisMemory         = "spec.accelerator.requirements.minimumMemory"
	AxisAllocationAPI  = "spec.accelerator.preferences.allocationAPI"
	AxisCandidates     = "candidates"
)

// Reject 는 번역 거절이다. *Reject 로 받아 nil 비교할 것 — error 인터페이스 변수에 담지 말 것.
// (*Reject)(nil) 을 error 에 대입하면 그 error 는 nil 이 아니게 된다 — Go 의 typed-nil 함정.
// Error() 는 그런 사고로 nil 수신자가 들어와도 panic 하지 않도록 방어한다.
type Reject struct {
	Axis    string
	Reason  string
	Message string
}

func (r *Reject) Error() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s (axis: %s)", r.Reason, r.Message, r.Axis)
}

// CheckMode 는 한 노드가 요청 "모드" 만 만족하는지 본다. nil 은 모드 축 통과일 뿐 전체 승인이
// 아니다 — isolation/memory/allocationAPI 요구사항은 보지 않으므로, 완전한 판정을 하려면
// requirements 게이트와 함께 호출해야 한다.
func CheckMode(nc NodeCapability, access v1alpha1.AccessSpec, m v1alpha1.AcceleratorMapping) *Reject {
	if nc.Stale {
		return &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonCapabilityUnverified,
			Message: fmt.Sprintf("node %s: capability data is not trustworthy — %s", nc.NodeName, nc.StaleReason)}
	}
	partitioned := access.Mode == v1alpha1.AccessModePartitioned || access.Mode == v1alpha1.AccessModePartitionedShared
	shared := access.Mode == v1alpha1.AccessModeShared || access.Mode == v1alpha1.AccessModePartitionedShared

	// profile 을 먼저 본다. 리소스명이 profile 에서 나오므로, 순서를 뒤집으면 "적용되지 않은
	// profile" 이 "광고하지 않는 리소스" 로 뭉개져 엉뚱한 축을 지목하게 된다.
	if partitioned {
		if m.NativeProfile == "" {
			return &Reject{Axis: AxisProfile, Reason: v1alpha1.AWReasonProfileNotApplied,
				Message: fmt.Sprintf("vendor %s mapping has no nativeProfile, which mode %q requires", m.Vendor, access.Mode)}
		}
		if !containsString(nc.PartitionProfiles, m.NativeProfile) {
			return &Reject{Axis: AxisProfile, Reason: v1alpha1.AWReasonProfileNotApplied,
				Message: fmt.Sprintf("node %s has no applied partition profile %q (applied: %v) — an AcceleratorPartitionPolicy must apply it first",
					nc.NodeName, m.NativeProfile, nc.PartitionProfiles)}
		}
	} else if len(nc.PartitionProfiles) > 0 && !strings.EqualFold(m.Vendor, "nvidia") {
		// 파티션을 flat 광고하는 벤더(RNGD 등)는 리소스명이 전체 장치와 조각을 구분하지 못하므로,
		// 하나라도 파티션이 적용된 노드에서는 전체 장치를 줄 수 있다고 말할 수 없다.
		// NVIDIA mixed MIG 는 리소스명이 이미 구분한다(nvidia.com/gpu vs nvidia.com/mig-*) —
		// A30 만 분할하고 A2 는 그대로인 노드에서 exclusive 를 통째로 막지 않는다. 그 노드가
		// 실제로 전체 장치를 못 주면 아래 Advertised[name] <= 0 검사가 fail-closed 로 잡는다.
		return &Reject{Axis: AxisMode, Reason: v1alpha1.AWReasonBackendUnsupported,
			Message: fmt.Sprintf("node %s is partitioned into %v, so it cannot hand out the whole device that mode %q asks for",
				nc.NodeName, nc.PartitionProfiles, access.Mode)}
	}

	name, err := ResourceFor(m, access.Mode)
	if err != nil {
		// ResourceFor 가 에러를 돌려주는 유일한 경우는 product 가 모호한 것(furiosa)이다 — 고칠
		// 필드가 있으므로 candidates 가 아니라 그 필드를 지목한다.
		return &Reject{Axis: AxisProduct, Reason: v1alpha1.AWReasonNoVendorMapping, Message: err.Error()}
	}
	if name == "" {
		return &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonNoVendorMapping,
			Message: fmt.Sprintf("vendor %q has no known device-plugin resource name", m.Vendor)}
	}
	if nc.Advertised[name] <= 0 {
		return &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonNoCandidateNodes,
			Message: fmt.Sprintf("node %s does not advertise %s", nc.NodeName, name)}
	}

	if shared {
		return checkSharing(nc, access)
	}
	switch nc.SharingMode {
	case v1alpha1.SharingModeTimeSliced:
		return &Reject{Axis: AxisMode, Reason: v1alpha1.AWReasonDeviceShared,
			Message: fmt.Sprintf("node %s advertises time-sliced replicas (x%d); a replica is not an independent device, so mode %q cannot be satisfied here",
				nc.NodeName, nc.SharingReplicas, access.Mode)}
	case v1alpha1.SharingModeOversubscribed:
		// 원인(복제냐 벤더의 하드웨어 서브유닛이냐)을 모르므로 "replica" 라고 단정하지 않는다 —
		// 다만 독립 장치임을 보일 수 없는 한 exclusive 는 여전히 fail-closed 로 거절한다.
		ratio := "an unconfirmed ratio"
		if nc.SharingReplicas > 0 {
			ratio = fmt.Sprintf("x%d", nc.SharingReplicas)
		}
		return &Reject{Axis: AxisMode, Reason: v1alpha1.AWReasonCapabilityUnverified,
			Message: fmt.Sprintf("node %s advertises more units (%s) than the reported physical devices; whether those extra units are independent hardware partitions or unisolated copies of the same device has not been established, so mode %q cannot be satisfied here",
				nc.NodeName, ratio, access.Mode)}
	}
	return nil
}

// checkSharing 은 공유 계열 모드의 게이트다. 순서가 중요하다 — 장치 capability(무엇이 가능한가)를
// 먼저 보고, 그다음 적용 상태(지금 켜져 있는가)를 본다. 그래야 "가능하지도 않다" 와
// "가능하지만 아직 안 켰다" 가 다른 메시지로 나온다.
func checkSharing(nc NodeCapability, access v1alpha1.AccessSpec) *Reject {
	if access.Replicas < 2 {
		return &Reject{Axis: AxisReplicas, Reason: v1alpha1.AWReasonReplicasTooFew,
			Message: fmt.Sprintf("mode %q needs access.replicas >= 2 (got %d); one sharer is just exclusive", access.Mode, access.Replicas)}
	}
	if len(nc.Devices) == 0 {
		return &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonCapabilityUnverified,
			Message: fmt.Sprintf("node %s reports no device capability, so sharing support cannot be confirmed", nc.NodeName)}
	}
	for _, dev := range nc.Devices {
		sup, implName := pickSharingSupport(dev.SharingCapability, access.Implementation)
		// 사용자가 implementation 을 콕 집어 요청했으면 고칠 필드는 access.mode 가 아니라
		// access.implementation 이다 — auto 는 operator 가 고른 것이므로 mode 를 지목한다.
		axis := axisForImplementation(access.Implementation)
		if !sup.Supported {
			// SharingCapability 가 제로값이면 Reason 도 비어 "supported=false ()" 가 된다 —
			// "지원하지 않는다고 보고했다" 와 "아무 보고도 없다" 를 구분해 말한다.
			why := sup.Reason
			if why == "" {
				why = "no capability reported"
			}
			return &Reject{Axis: axis, Reason: v1alpha1.AWReasonBackendUnsupported,
				Message: fmt.Sprintf("node %s device %s: sharing.%s.supported=false (%s)", nc.NodeName, dev.ID, implName, why)}
		}
		if sup.Verification != v1alpha1.VerificationVerified {
			return &Reject{Axis: axis, Reason: v1alpha1.AWReasonCapabilityUnverified,
				Message: fmt.Sprintf("node %s device %s: sharing.%s.verification=%q, not %q — refusing to route a workload to a sharing mode that has not been measured",
					nc.NodeName, dev.ID, implName, sup.Verification, v1alpha1.VerificationVerified)}
		}
		// maxReplicas 를 "미측정 = 무제한" 으로 읽으면 이 경로의 유일한 fail-open 이 된다 —
		// 다른 미측정 값은 전부 거절하므로 여기도 거절한다.
		if sup.MaxReplicas <= 0 {
			return &Reject{Axis: AxisReplicas, Reason: v1alpha1.AWReasonCapabilityUnverified,
				Message: fmt.Sprintf("node %s device %s: sharing.%s.maxReplicas is unset, so replicas=%d cannot be confirmed",
					nc.NodeName, dev.ID, implName, access.Replicas)}
		}
		if access.Replicas > sup.MaxReplicas {
			return &Reject{Axis: AxisReplicas, Reason: v1alpha1.AWReasonReplicasExceedMax,
				Message: fmt.Sprintf("node %s device %s: access.replicas=%d exceeds sharing.%s.maxReplicas=%d", nc.NodeName, dev.ID, access.Replicas, implName, sup.MaxReplicas)}
		}
	}
	if nc.SharingMode == v1alpha1.SharingModeOversubscribed {
		// SharingNotApplied 는 "아무 것도 적용되지 않았다" 는 뜻이라 여기에 맞지 않는다 — 이
		// 노드는 무언가(원인 미확정)를 광고하고 있다. 사유를 그 불확실성 그대로 말한다.
		return &Reject{Axis: AxisMode, Reason: v1alpha1.AWReasonCapabilityUnverified,
			Message: fmt.Sprintf("node %s: advertisement exceeds reported physical devices for an undetermined reason, so a sharing configuration cannot be confirmed, which mode %q requires", nc.NodeName, access.Mode)}
	}
	if nc.SharingMode != v1alpha1.SharingModeTimeSliced {
		// multiProcess/brokered 가 verified 로 보고되는 날에도 이 검사는 그대로다 — 적용 저널의
		// SharingMode 는 오늘 exclusive|timeSliced|oversubscribed 뿐이므로
		// (acceleratorpartitionpolicy_types.go:44-58), 두 번째 구현이 실제로 verified 가 되면
		// 여기를 access.Implementation 기준으로 좁혀야 한다.
		return &Reject{Axis: AxisMode, Reason: v1alpha1.AWReasonSharingNotApplied,
			Message: fmt.Sprintf("node %s: no AcceleratorPartitionPolicy has applied a sharing configuration, which mode %q requires", nc.NodeName, access.Mode)}
	}
	// 정확히 일치해야 한다 — access.replicas 는 "장치를 몇 갈래로 나누는가"이므로 적용된 나눔수가
	// 요청보다 커도(더 잘게 쪼개져 있어도) 사용자가 상정한 몫 크기와 달라 조용한 대체가 된다.
	if nc.SharingReplicas != access.Replicas {
		verb := "smaller than"
		if nc.SharingReplicas > access.Replicas {
			verb = "larger than"
		}
		return &Reject{Axis: AxisReplicas, Reason: v1alpha1.AWReasonReplicasExceedMax,
			Message: fmt.Sprintf("node %s: applied sharing replicas=%d is %s the requested %d — the device is split %d ways, not %d",
				nc.NodeName, nc.SharingReplicas, verb, access.Replicas, nc.SharingReplicas, access.Replicas)}
	}
	return nil
}

// axisForImplementation 은 device-capability 거절이 지목할 축이다. 사용자가 implementation 을
// 콕 집어 요청했으면(auto 가 아니면) 고칠 필드는 access.implementation 이다 — auto 는 operator 가
// 고른 것이라 access.mode 를 지목한다.
func axisForImplementation(implementation string) string {
	if implementation != "" && implementation != v1alpha1.ImplementationAuto {
		return AxisImplementation
	}
	return AxisMode
}

// pickSharingSupport 는 implementation 축을 SharingCapability 필드로 옮긴다.
// auto 는 verified 인 것 중 timeSlicing → multiProcess → brokered 순으로 고르고, 하나도 없으면
// timeSlicing 을 돌려준다 — 거절 메시지가 구체적인 필드를 지목하게 하기 위해서다.
func pickSharingSupport(sc v1alpha1.SharingCapability, implementation string) (v1alpha1.SharingModeSupport, string) {
	switch implementation {
	case v1alpha1.ImplementationTimeSlicing:
		return sc.TimeSlicing, v1alpha1.ImplementationTimeSlicing
	case v1alpha1.ImplementationMultiProcess:
		return sc.MultiProcess, v1alpha1.ImplementationMultiProcess
	case v1alpha1.ImplementationBrokered:
		return sc.Brokered, v1alpha1.ImplementationBrokered
	}
	candidates := []struct {
		sup  v1alpha1.SharingModeSupport
		name string
	}{
		{sc.TimeSlicing, v1alpha1.ImplementationTimeSlicing},
		{sc.MultiProcess, v1alpha1.ImplementationMultiProcess},
		{sc.Brokered, v1alpha1.ImplementationBrokered},
	}
	for _, c := range candidates {
		if c.sup.Supported && c.sup.Verification == v1alpha1.VerificationVerified {
			return c.sup, c.name
		}
	}
	return sc.TimeSlicing, v1alpha1.ImplementationTimeSlicing
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
