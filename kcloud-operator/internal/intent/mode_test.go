// ============================================================
// mode_test.go: 추상 모드 지원 판정 테스트
// 상세: 정직성 원칙이 코드로 지켜지는지 — 미검증 공유 거절, 시분할 노드에 exclusive 거절,
// 미적용 profile 거절. 조용한 강등이 일어나면 이 테스트가 깨진다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"strings"
	"testing"

	"kcloud-operator/api/v1alpha1"
)

func nvidiaMapping() v1alpha1.AcceleratorMapping {
	return v1alpha1.AcceleratorMapping{Vendor: "nvidia", NativeProfile: "1g.6gb"}
}

// rngdMapping 은 Product 를 명시한다 — furiosa 는 product 없이는 rngd/warboy 를 가릴 수 없다
// (internal/intent/resources.go 의 ResourceFor 계약). nvidiaMapping 과 닮아 보여도 중복이 아니다.
func rngdMapping() v1alpha1.AcceleratorMapping {
	return v1alpha1.AcceleratorMapping{Vendor: "furiosa", Product: "rngd", NativeProfile: "2core.12gb"}
}

// verifiedTimeSlicing 은 라이브 실측이 끝난 NVIDIA 장치다.
func verifiedTimeSlicing(max int32) v1alpha1.DeviceStatus {
	return v1alpha1.DeviceStatus{
		ID: "PCI-0000:3b:00.0",
		SharingCapability: v1alpha1.SharingCapability{
			TimeSlicing: v1alpha1.SharingModeSupport{Supported: true, Verification: v1alpha1.VerificationVerified, MaxReplicas: max},
		},
		IsolationCapability: v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationHardware, Memory: v1alpha1.IsolationHardware, Fault: v1alpha1.IsolationDevice},
	}
}

// rngdDevice 는 실제 RNGD backend 가 보고하는 상태다 — MultiProcess 는 계측기가 무효라 미검증이다.
func rngdDevice() v1alpha1.DeviceStatus {
	return v1alpha1.DeviceStatus{
		ID: "PCI-0000:27:00.0",
		SharingCapability: v1alpha1.SharingCapability{
			TimeSlicing:  v1alpha1.SharingModeSupport{Supported: false, Verification: v1alpha1.VerificationRequired, Reason: "furiosa device-plugin has no replica/time-slicing option"},
			MultiProcess: v1alpha1.SharingModeSupport{Supported: false, Verification: v1alpha1.VerificationRequired, Reason: "measurement instrument invalid"},
			Brokered:     v1alpha1.SharingModeSupport{Supported: false, Verification: v1alpha1.VerificationRequired, Reason: "broker not implemented"},
		},
		IsolationCapability: v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationSubdevice},
	}
}

// rejectAsError 는 CheckMode 호출자가 실수로 저지를 수 있는 대입을 재현한다 — 함수 경계를
// 넘겨서 staticcheck 가 "항상 참/거짓"으로 정적 증명하지 못하게 한다(실제 호출자의 대입도
// 마찬가지로 정적으로 보이지 않는 곳, 예: 리턴값을 error 필드에 담는 코드에서 일어난다).
func rejectAsError(rj *Reject) error { return rj }

// (*Reject)(nil) 을 error 인터페이스 변수에 담으면 그 error != nil 이다 — Go 의 typed-nil 함정.
// 이 테스트는 그 함정이 실제로 있다는 것과, Error() 가 그런 경우에도 panic 하지 않는다는 것을
// 함께 고정한다. 호출자는 반드시 *Reject 로 받아 nil 비교해야 한다(doc, mode.go:30-32).
func TestRejectNilAsError(t *testing.T) {
	var rj *Reject
	err := rejectAsError(rj)
	if err == nil {
		t.Fatal("typed nil *Reject unexpectedly compares equal to a nil error interface")
	}
	if got := err.Error(); got != "" {
		t.Fatalf("nil *Reject.Error() should be empty, got %q", got)
	}
}

func TestCheckModeExclusiveOnPlainNode(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 2}, SharingMode: v1alpha1.SharingModeExclusive}
	if rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, nvidiaMapping()); rj != nil {
		t.Fatalf("exclusive on a plain node rejected: %v", rj)
	}
}

// 시분할 replica 는 독립 장치가 아니다 — 그 노드는 exclusive 를 만족할 수 없다.
func TestCheckModeExclusiveRejectedOnTimeSlicedNode(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 8},
		SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 4}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, nvidiaMapping())
	if rj == nil {
		t.Fatal("exclusive accepted on a time-sliced node")
	}
	if rj.Reason != v1alpha1.AWReasonDeviceShared || rj.Axis != AxisMode {
		t.Fatalf("unexpected reject %+v", rj)
	}
	if !strings.Contains(rj.Message, "not an independent device") {
		t.Fatalf("message does not explain why: %q", rj.Message)
	}
}

// 광고 수가 물리 장치 수를 넘는다는 관측만으로는 메커니즘(복제 vs 하드웨어 서브유닛)을 증명하지
// 않는다 — exclusive 는 여전히 fail-closed 로 거절하되, 사유가 "replica" 를 근거로 들면 안 되고
// 불확실성을 말해야 한다(RNGD 재현: rngd-1 은 PE 4개를 광고하는 물리 1대다).
func TestCheckModeExclusiveRejectedOnOversubscribedNode(t *testing.T) {
	nc := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 4},
		SharingMode: v1alpha1.SharingModeOversubscribed, SharingReplicas: 4}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, rngdMapping())
	if rj == nil {
		t.Fatal("exclusive accepted on an oversubscribed node")
	}
	if strings.Contains(rj.Message, "replica") {
		t.Fatalf("message must not assert replication as the mechanism: %q", rj.Message)
	}
	if rj.Reason == v1alpha1.AWReasonDeviceShared {
		t.Fatalf("must not reuse the timeSliced reason for an undetermined cause: %+v", rj)
	}
}

// shared 요청도 조용히 통과시키지 않는다 — 공유가 실제로 설정되었다는 것을 보일 수 없다.
// 미적용(SharingNotApplied)과는 원인이 다르므로 같은 사유를 재사용하지 않는다.
func TestCheckModeSharedRejectedOnOversubscribedNode(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 8},
		Devices: []v1alpha1.DeviceStatus{verifiedTimeSlicing(4)}, SharingMode: v1alpha1.SharingModeOversubscribed, SharingReplicas: 4}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4}, nvidiaMapping())
	if rj == nil {
		t.Fatal("shared accepted on an oversubscribed node")
	}
	if rj.Reason == v1alpha1.AWReasonSharingNotApplied {
		t.Fatalf("oversubscribed must not reuse the generic \"not applied\" reason — the causes differ: %+v", rj)
	}
}

// RNGD 에 shared 요청 → 조용한 강등이 아니라 거절이어야 한다.
func TestCheckModeSharedRejectedOnRNGD(t *testing.T) {
	nc := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 1},
		Devices: []v1alpha1.DeviceStatus{rngdDevice()}, SharingMode: v1alpha1.SharingModeExclusive}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Implementation: v1alpha1.ImplementationAuto, Replicas: 2}, rngdMapping())
	if rj == nil {
		t.Fatal("shared accepted on RNGD")
	}
	if rj.Axis != AxisMode || rj.Reason != v1alpha1.AWReasonBackendUnsupported {
		t.Fatalf("unexpected reject %+v", rj)
	}
	if !strings.Contains(rj.Message, "supported=false") {
		t.Fatalf("message must name the capability field: %q", rj.Message)
	}
}

// 명시적으로 multiProcess 를 요청해도 미검증이면 거절이며, 축 이름이 그대로 나와야 한다.
func TestCheckModeSharedRejectedWhenUnverified(t *testing.T) {
	dev := rngdDevice()
	dev.SharingCapability.MultiProcess = v1alpha1.SharingModeSupport{Supported: true, Verification: v1alpha1.VerificationRequired}
	nc := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 1},
		Devices: []v1alpha1.DeviceStatus{dev}, SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 2}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Implementation: v1alpha1.ImplementationMultiProcess, Replicas: 2}, rngdMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonCapabilityUnverified {
		t.Fatalf("unverified sharing accepted or wrong reason: %+v", rj)
	}
	// 사용자가 implementation 을 콕 집어 요청했으니 고칠 필드는 access.implementation 이지 access.mode 가 아니다.
	if rj.Axis != AxisImplementation {
		t.Fatalf("explicit implementation reject must point at AxisImplementation, not %q", rj.Axis)
	}
	if !strings.Contains(rj.Message, "multiProcess") {
		t.Fatalf("message must name the implementation: %q", rj.Message)
	}
}

// live RNGD 는 multiProcess 도 supported=false+required 로 보고한다 — 명시 요청도 거절이어야 한다.
func TestCheckModeSharedRejectedOnRNGDMultiProcessExplicit(t *testing.T) {
	nc := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 1},
		Devices: []v1alpha1.DeviceStatus{rngdDevice()}, SharingMode: v1alpha1.SharingModeExclusive}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Implementation: v1alpha1.ImplementationMultiProcess, Replicas: 2}, rngdMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonBackendUnsupported || rj.Axis != AxisImplementation {
		t.Fatalf("explicit multiProcess on RNGD not rejected on the right axis/reason: %+v", rj)
	}
	if !strings.Contains(rj.Message, "multiProcess") || !strings.Contains(rj.Message, "supported=false") {
		t.Fatalf("message must name multiProcess and supported=false: %q", rj.Message)
	}
}

func TestCheckModeSharedNeedsAppliedSharing(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 2},
		Devices: []v1alpha1.DeviceStatus{verifiedTimeSlicing(16)}, SharingMode: v1alpha1.SharingModeExclusive}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4}, nvidiaMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonSharingNotApplied {
		t.Fatalf("shared accepted without an applied sharing config: %+v", rj)
	}
}

func TestCheckModeSharedReplicaBounds(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 8},
		Devices: []v1alpha1.DeviceStatus{verifiedTimeSlicing(4)}, SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 4}
	if rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4}, nvidiaMapping()); rj != nil {
		t.Fatalf("replicas within bounds rejected: %v", rj)
	}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 8}, nvidiaMapping())
	if rj == nil || rj.Axis != AxisReplicas || rj.Reason != v1alpha1.AWReasonReplicasExceedMax {
		t.Fatalf("maxReplicas not enforced: %+v", rj)
	}
	rj = CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 1}, nvidiaMapping())
	if rj == nil || rj.Axis != AxisReplicas {
		t.Fatalf("replicas=1 must be rejected for shared: %+v", rj)
	}
}

// 적용된 나눔수가 요청보다 크면(1/8 로 쪼개진 노드에 1/4 요청) 몫 크기가 다르므로 거절이어야
// 한다 — "일단 몫이 있으니 통과"는 조용한 대체다.
func TestCheckModeSharedRejectedWhenAppliedReplicasLargerThanRequested(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 8},
		Devices: []v1alpha1.DeviceStatus{verifiedTimeSlicing(16)}, SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 8}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4}, nvidiaMapping())
	if rj == nil || rj.Axis != AxisReplicas || rj.Reason != v1alpha1.AWReasonReplicasExceedMax {
		t.Fatalf("applied replicas=8 for a requested 4 must be rejected: %+v", rj)
	}
	if !strings.Contains(rj.Message, "larger than") {
		t.Fatalf("message must explain the mismatch direction: %q", rj.Message)
	}
}

func TestCheckModePartitionedNeedsAppliedProfile(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		PartitionProfiles: []string{"1g.6gb"}, SharingMode: v1alpha1.SharingModeExclusive}
	if rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitioned}, nvidiaMapping()); rj != nil {
		t.Fatalf("applied profile rejected: %v", rj)
	}
	other := nvidiaMapping()
	other.NativeProfile = "2g.12gb"
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitioned}, other)
	if rj == nil || rj.Axis != AxisProfile || rj.Reason != v1alpha1.AWReasonProfileNotApplied {
		t.Fatalf("unapplied profile accepted: %+v", rj)
	}
}

// 파티션을 flat 광고하는 벤더(RNGD)는 분할된 노드에서 통째 장치를 줄 수 없다 —
// furiosa.ai/rngd 가 양수여도 그게 전체 장치인지 조각인지 리소스명으로는 알 수 없다.
func TestCheckModeExclusiveRejectedOnPartitionedFlatVendorNode(t *testing.T) {
	nc := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 4},
		PartitionProfiles: []string{"2core.12gb"}, SharingMode: v1alpha1.SharingModeExclusive}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, rngdMapping())
	if rj == nil || rj.Axis != AxisMode {
		t.Fatalf("exclusive accepted on a partitioned flat-advertising node: %+v", rj)
	}
}

// NVIDIA mixed MIG 는 리소스명이 전체 장치와 조각을 이미 구분한다(worker1: A30 분할 + A2 그대로).
// A30 에 profile 을 적용했다고 A2 로 만족되는 exclusive 까지 막지 않는다.
func TestCheckModeExclusiveAllowedOnMixedMIGNode(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 1, "nvidia.com/mig-1g.6gb": 7},
		PartitionProfiles: []string{"1g.6gb"}, SharingMode: v1alpha1.SharingModeExclusive}
	if rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, nvidiaMapping()); rj != nil {
		t.Fatalf("exclusive rejected although the node advertises a whole GPU: %v", rj)
	}
	// 전체 장치가 하나도 남지 않았으면 광고량 검사가 fail-closed 로 잡는다.
	nc.Advertised = map[string]int32{"nvidia.com/mig-1g.6gb": 7}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, nvidiaMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonNoCandidateNodes {
		t.Fatalf("exclusive accepted with no whole GPU advertised: %+v", rj)
	}
}

// maxReplicas 미설정을 "무제한" 으로 읽으면 이 경로의 유일한 fail-open 이 된다.
func TestCheckSharingRejectsUnsetMaxReplicas(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 1},
		Devices: []v1alpha1.DeviceStatus{verifiedTimeSlicing(0)}, SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 4}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4}, nvidiaMapping())
	if rj == nil || rj.Axis != AxisReplicas || rj.Reason != v1alpha1.AWReasonCapabilityUnverified {
		t.Fatalf("unset maxReplicas read as unlimited: %+v", rj)
	}
}

// 공유 모드의 replicas<2 는 백엔드 한계가 아니라 사용자 입력 문제다.
func TestCheckSharingReplicasTooFewNamesInputError(t *testing.T) {
	nc := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 1},
		Devices: []v1alpha1.DeviceStatus{verifiedTimeSlicing(4)}, SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 4}
	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 1}, nvidiaMapping())
	if rj == nil || rj.Axis != AxisReplicas || rj.Reason != v1alpha1.AWReasonReplicasTooFew {
		t.Fatalf("replicas<2 reason wrong: %+v", rj)
	}
}

func TestCheckModeRejectsStaleAndUnadvertised(t *testing.T) {
	stale := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/gpu": 1},
		Stale: true, StaleReason: "acpp not converged"}
	rj := CheckMode(stale, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, nvidiaMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonCapabilityUnverified {
		t.Fatalf("stale node accepted: %+v", rj)
	}

	empty := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 1}}
	rj = CheckMode(empty, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, nvidiaMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonNoCandidateNodes {
		t.Fatalf("node not advertising the resource accepted: %+v", rj)
	}
}

// ResourceFor 의 두 실패는 사용자 조치가 다르다 — product 가 모호하면 그 필드를 고치면 풀리고,
// 벤더 자체를 모르면 고칠 필드가 없다(candidates). 축이 갈려야 한다.
func TestCheckModeRejectsAmbiguousVendorMapping(t *testing.T) {
	nc := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 1}}

	rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		v1alpha1.AcceleratorMapping{Vendor: "furiosa"}) // product 없음 → ResourceFor 에러
	if rj == nil || rj.Axis != AxisProduct || rj.Reason != v1alpha1.AWReasonNoVendorMapping {
		t.Fatalf("ambiguous furiosa mapping not rejected on AxisProduct: %+v", rj)
	}
	if !strings.Contains(rj.Message, "product must be one of") {
		t.Fatalf("message must surface the ResourceFor error: %q", rj.Message)
	}

	rj = CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		v1alpha1.AcceleratorMapping{Vendor: "amd"}) // 미지 벤더 → name == ""
	if rj == nil || rj.Axis != AxisCandidates || rj.Reason != v1alpha1.AWReasonNoVendorMapping {
		t.Fatalf("unknown vendor accepted or on the wrong axis: %+v", rj)
	}
}

// partitioned-shared 는 profile 게이트와 sharing 게이트를 동시에 지나는 유일한 모드다.
func TestCheckModePartitionedShared(t *testing.T) {
	base := NodeCapability{NodeName: "worker1", Vendor: "nvidia", Advertised: map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		PartitionProfiles: []string{"1g.6gb"}, SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 2}

	// profile 적용 + verified 공유 → 통과.
	pass := base
	pass.Devices = []v1alpha1.DeviceStatus{verifiedTimeSlicing(2)}
	if rj := CheckMode(pass, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitionedShared, Replicas: 2}, nvidiaMapping()); rj != nil {
		t.Fatalf("applied profile + verified sharing rejected: %v", rj)
	}

	// profile 적용 + 미검증 공유(RNGD 는 partition 만 적용, sharing 은 미검증) → sharing 축 거절.
	rngdPartitioned := NodeCapability{NodeName: "rngd-1", Vendor: "furiosa", Advertised: map[string]int32{"furiosa.ai/rngd": 1},
		PartitionProfiles: []string{"2core.12gb"}, Devices: []v1alpha1.DeviceStatus{rngdDevice()}, SharingMode: v1alpha1.SharingModeExclusive}
	rj := CheckMode(rngdPartitioned, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitionedShared, Implementation: v1alpha1.ImplementationAuto, Replicas: 2}, rngdMapping())
	if rj == nil || rj.Reason != v1alpha1.AWReasonBackendUnsupported {
		t.Fatalf("partitioned-shared on RNGD must reject on the sharing axis: %+v", rj)
	}
}
