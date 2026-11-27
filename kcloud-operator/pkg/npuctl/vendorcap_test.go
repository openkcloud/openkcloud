// vendorcap_test.go: 성적표 층 조인 규칙 시험
// 상세: 신선한 AllocationVerified evidence → devicePlugin/dra Verified, ACPP Verified →
// partition Verified(Degraded 우선, 미지원은 NotApplicable)를 표 기반으로 본다. 실제
// 노드 라벨 어휘(furiosa rngd/warboy)·DRA 광고 키·sharing 검증 reason 게이트·
// multi-target 집계·AcceleratorVerificationPolicy 반영도 함께 고정한다.
// 생성일: 2026-09-19 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/vendorcap"
	"kcloud-operator/internal/verification"
)

//nolint:unparam // name 은 오늘 모든 호출에서 "n1" 이다 — 단일 노드 시나리오라 그렇다.
func node(name, vendor string, draOwned bool) corev1.Node {
	l := map[string]string{"kcloud.ai/" + vendor + ".present": labelValueTrue}
	if draOwned {
		l[v1alpha1.DRAOwnedNodeLabel(vendor)] = labelValueTrue
	}
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: l}}
}

// labeledNode 는 라벨을 그대로 지정하는 노드 픽스처다 — furiosa 처럼 present 라벨이 여러
// 개(rngd.present+furiosa-family.present 등) 실리는 라이브 조합을 재현할 때 쓴다.
func labeledNode(name string, labels map[string]string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func evidence(nodeName, level string) v1alpha1.AcceleratorEvidence {
	return v1alpha1.AcceleratorEvidence{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Spec:       v1alpha1.AcceleratorEvidenceSpec{NodeName: nodeName},
		Status:     v1alpha1.AcceleratorEvidenceStatus{Level: level},
	}
}

// evidenceWithVendor 는 컨트롤러가 Spec.Vendor 를 채운 evidence 다 — 다중 벤더 노드에서
// 라벨 순서가 아니라 이 값으로 귀속을 정해야 하는 경로(evidenceVendorProduct)를 시험한다.
func evidenceWithVendor(nodeName, level, vendor string) v1alpha1.AcceleratorEvidence {
	ev := evidence(nodeName, level)
	ev.Spec.Vendor = vendor
	return ev
}

// find 는 (vendor,product,capability) 로 행을 찾는다. product 를 "" 로 주면 registry 가
// product 를 두지 않는 벤더(nvidia/rebellions/tenstorrent, product="") 나 ACPP 파생 행(항상
// product="")을 찾는다 — furiosa 처럼 product 가 여럿인 벤더는 명시적으로 "rngd"/"warboy" 를
// 준다.
func find(rows []VendorCapabilityRow, vendor, product, cap string) VendorCapabilityRow {
	for _, r := range rows {
		if r.Vendor == vendor && r.Product == product && r.Capability == cap {
			return r
		}
	}
	return VendorCapabilityRow{}
}

func alwaysFresh(*v1alpha1.AcceleratorEvidence, corev1.Node) bool { return true }
func neverFresh(*v1alpha1.AcceleratorEvidence, corev1.Node) bool  { return false }

func TestDeriveDevicePluginVerifiedFromFreshAllocationEvidence(t *testing.T) {
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("n1", "AllocationVerified")},
		nil, []corev1.Node{node("n1", "nvidia", false)}, alwaysFresh)
	if got := find(rows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
		t.Fatalf("devicePlugin = %q, want verified", got)
	}
	if got := find(rows, "nvidia", "", "dra").Verified; got != v1alpha1.VerificationRequired {
		t.Fatalf("dra 는 dra-owned 아니면 required 여야 한다: %q", got)
	}
}

func TestDeriveDRAVerifiedWhenNodeDRAOwned(t *testing.T) {
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("n1", "AllocationVerified")},
		nil, []corev1.Node{node("n1", "tenstorrent", true)}, alwaysFresh)
	if got := find(rows, "tenstorrent", "", "dra").Verified; got != v1alpha1.VerificationVerified {
		t.Fatalf("dra = %q, want verified", got)
	}
}

func TestDeriveStaleEvidenceStaysRequired(t *testing.T) {
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("n1", "AllocationVerified")},
		nil, []corev1.Node{node("n1", "nvidia", false)}, neverFresh)
	if got := find(rows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Fatalf("stale evidence 는 required 여야 한다: %q", got)
	}
}

func TestDeriveUnsupportedIsNotApplicable(t *testing.T) {
	rows := DeriveVendorCapabilities(nil, nil, []corev1.Node{node("n1", "rebellions", false)}, alwaysFresh)
	if got := find(rows, "rebellions", "", "partition").Verified; got != v1alpha1.VerificationNotApplicable {
		t.Fatalf("Unsupported 능력은 notApplicable: %q", got)
	}
}

// ACPPCondVerified/ACPPCondDegraded 는 오브젝트 최상위 Status 가 아니라 노드별
// Status.Targets[].Conditions 에 실린다(controller 의 setCond,
// acceleratorpartitionpolicy_controller.go:1401) — AcceleratorPartitionPolicyStatus 에
// top-level Conditions 필드는 없다.
func TestDerivePartitionVerifiedAndDegradedFromACPP(t *testing.T) {
	layout := []v1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 1}}
	verifiedCond := []metav1.Condition{
		{Type: v1alpha1.ACPPCondVerified, Status: metav1.ConditionTrue, Reason: "AllocatableConverged"},
	}
	verified := v1alpha1.AcceleratorPartitionPolicy{
		Spec: v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia", Layout: layout},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{Targets: []v1alpha1.TargetStatus{
			{NodeName: "n1", Conditions: verifiedCond},
		}},
	}
	rows := DeriveVendorCapabilities(nil, []v1alpha1.AcceleratorPartitionPolicy{verified}, nil, alwaysFresh)
	if got := find(rows, "nvidia", "", "partition").Verified; got != v1alpha1.VerificationVerified {
		t.Fatalf("partition = %q, want verified", got)
	}
	degradedCond := metav1.Condition{Type: v1alpha1.ACPPCondDegraded, Status: metav1.ConditionTrue}
	degraded := verified
	degraded.Status.Targets = append([]v1alpha1.TargetStatus{}, verified.Status.Targets...)
	degraded.Status.Targets[0].Conditions = append(degraded.Status.Targets[0].Conditions, degradedCond)
	rows = DeriveVendorCapabilities(nil, []v1alpha1.AcceleratorPartitionPolicy{degraded}, nil, alwaysFresh)
	if got := find(rows, "nvidia", "", "partition").Verified; got != VerificationDegraded {
		t.Fatalf("Degraded 가 Verified 보다 우선해야 한다: %q", got)
	}
}

// --- discovery/driver·fallthrough 전용 어서션 ---

func TestDeriveFunctionallyVerifiedGrantsDiscoveryAndDriverOnly(t *testing.T) {
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("n1", "FunctionallyVerified")},
		nil, []corev1.Node{node("n1", "nvidia", false)}, alwaysFresh)
	for _, cap := range []string{"discovery", "driver"} {
		if got := find(rows, "nvidia", "", cap).Verified; got != v1alpha1.VerificationVerified {
			t.Errorf("%s = %q, want verified", cap, got)
		}
	}
	if got := find(rows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Errorf("FunctionallyVerified 만으로 devicePlugin 까지 verified 면 안 된다: %q", got)
	}
}

func TestDeriveAllocationVerifiedGrantsDiscoveryDriverAndDevicePlugin(t *testing.T) {
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("n1", "AllocationVerified")},
		nil, []corev1.Node{node("n1", "nvidia", false)}, alwaysFresh)
	for _, cap := range []string{"discovery", "driver", "devicePlugin"} {
		if got := find(rows, "nvidia", "", cap).Verified; got != v1alpha1.VerificationVerified {
			t.Errorf("%s = %q, want verified(AllocationVerified 의 fallthrough 로 discovery/driver 도 같이 와야 한다)", cap, got)
		}
	}
}

// --- Furiosa 실측 라벨 어휘(rngd/warboy/furiosa-family) ---

// live rngd-1 은 kcloud.ai/rngd.present + furiosa-family.present 를 붙인다(furiosa.present 는
// 없다) — nodeVendorProduct 가 이 조합에서 product="rngd" 를 확정해야 한다.
func TestDeriveFuriosaRNGDNodeResolvesRNGDProductOnly(t *testing.T) {
	n := labeledNode("rngd-1", map[string]string{
		"kcloud.ai/rngd.present":           labelValueTrue,
		"kcloud.ai/furiosa-family.present": labelValueTrue,
	})
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("rngd-1", "AllocationVerified")},
		nil, []corev1.Node{n}, alwaysFresh)
	if got := find(rows, "furiosa", "rngd", "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("furiosa/rngd devicePlugin = %q, want verified", got)
	}
	if got := find(rows, "furiosa", "warboy", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Errorf("furiosa/warboy 까지 verified 로 새면 안 된다: %q", got)
	}
}

// 실측 Warboy 노드(k8s-worker1)는 furiosa.present + furiosa-family.present 를 갖지만
// nvidia.present·accelerator.present 도 같이 갖는다(다중 벤더 노드 — mixed-node 시험이 따로
// 다룬다). 이 시험은 그 라벨 세트가 "단독으로" 있을 때 product 해석만 본다:
// furiosa.present 자체가 Warboy 노드 라벨이라(api/v1alpha1.PresentNodeLabel 의 계약)
// product="warboy" 로 확정된다 — furiosa-family.present 만으로는 product 를 못 정하지만,
// furiosa.present 가 함께 있으면 더 이상 미상이 아니다.
func TestDeriveFuriosaPresentLabelResolvesWarboyProduct(t *testing.T) {
	n := labeledNode("warboy-only-1", map[string]string{
		"kcloud.ai/furiosa.present":        labelValueTrue,
		"kcloud.ai/furiosa-family.present": labelValueTrue,
	})
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("warboy-only-1", "AllocationVerified")},
		nil, []corev1.Node{n}, alwaysFresh)
	if got := find(rows, "furiosa", "warboy", "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("furiosa/warboy devicePlugin = %q, want verified", got)
	}
	if got := find(rows, "furiosa", "rngd", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Errorf("furiosa/rngd 까지 verified 로 새면 안 된다: %q", got)
	}
}

// furiosa-family.present 만 있고 rngd.present/furiosa.present 가 전혀 없으면
// product 를 못 정해 ""(모든 product 적용)로 떨어진다 — 실측 라이브 노드 중엔 이 조합이 아직
// 없지만(모두 rngd.present 나 furiosa.present 를 함께 갖는다), furiosaProduct 의 진짜 fallback
// 경로로 남겨 시험한다.
func TestDeriveFuriosaFamilyOnlyAppliesToAllProducts(t *testing.T) {
	n := labeledNode("unknown-furiosa-1", map[string]string{
		"kcloud.ai/furiosa-family.present": labelValueTrue,
	})
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("unknown-furiosa-1", "AllocationVerified")},
		nil, []corev1.Node{n}, alwaysFresh)
	for _, product := range []string{"rngd", "warboy"} {
		if got := find(rows, "furiosa", product, "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
			t.Errorf("furiosa/%s devicePlugin = %q, want verified(product 미상 ceiling)", product, got)
		}
	}
}

// DRA-owned 라벨은 advertise 벤더 키로 붙는다(furiosa→"rngd") — furiosa.dra-owned 가 아니라
// rngd.dra-owned 를 봐야 한다.
func TestDeriveFuriosaDRAOwnedUsesRNGDAdvertiseKey(t *testing.T) {
	n := labeledNode("rngd-1", map[string]string{
		"kcloud.ai/rngd.present":           labelValueTrue,
		"kcloud.ai/furiosa-family.present": labelValueTrue,
		v1alpha1.DRAOwnedNodeLabel("rngd"): labelValueTrue,
	})
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("rngd-1", "AllocationVerified")},
		nil, []corev1.Node{n}, alwaysFresh)
	if got := find(rows, "furiosa", "rngd", "dra").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("dra = %q, want verified", got)
	}
	if got := find(rows, "furiosa", "rngd", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Errorf("dra 로 갔으면 devicePlugin 은 required 로 남아야 한다: %q", got)
	}
}

// Warboy 는 advertise 벤더 키가 furiosa 다(RNGD 의 rngd 와 다른 키 — reconcileDRAOwnedLabels
// 가 Warboy 노드엔 kcloud.ai/furiosa.dra-owned 를 붙인다, rngd.dra-owned 가 아니다). furiosa
// 를 rngd 로만 매핑하면 이 라벨이 닿을 길이 없어진다.
func TestDeriveFuriosaWarboyDRAOwnedUsesFuriosaAdvertiseKey(t *testing.T) {
	n := labeledNode("warboy-1", map[string]string{
		"kcloud.ai/furiosa.present":               labelValueTrue,
		"kcloud.ai/furiosa-family.present":        labelValueTrue,
		v1alpha1.DRAOwnedNodeLabel(VendorFuriosa): labelValueTrue,
	})
	rows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidence("warboy-1", "AllocationVerified")},
		nil, []corev1.Node{n}, alwaysFresh)
	if got := find(rows, "furiosa", "warboy", "dra").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("dra = %q, want verified", got)
	}
	if got := find(rows, "furiosa", "warboy", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Errorf("dra 로 갔으면 devicePlugin 은 required 로 남아야 한다: %q", got)
	}
}

// k8s-worker1 은 nvidia.present 와 furiosa.present 를 동시에 갖는다(Warboy 위에 GPU 도
// 꽂힌 노드) — 라벨 순서(nvidia 먼저)로만 벤더를 정하면 Furiosa evidence 가 엉뚱하게
// nvidia 를 verified 로 만든다. evidence 자신의 Spec.Vendor 를 우선해야 한다.
func worker1() corev1.Node {
	return labeledNode("k8s-worker1", map[string]string{
		"kcloud.ai/nvidia.present":         labelValueTrue,
		"kcloud.ai/furiosa.present":        labelValueTrue,
		"kcloud.ai/furiosa-family.present": labelValueTrue,
		"kcloud.ai/accelerator.present":    labelValueTrue,
		"kcloud.ai/gpu.present":            labelValueTrue,
	})
}

func TestDeriveMixedVendorNodeUsesEvidenceVendorNotLabelOrder(t *testing.T) {
	n := worker1()

	furiosaRows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidenceWithVendor("k8s-worker1", "AllocationVerified", "furiosa")},
		nil, []corev1.Node{n}, alwaysFresh)
	if got := find(furiosaRows, "furiosa", "warboy", "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("furiosa/warboy devicePlugin = %q, want verified", got)
	}
	if got := find(furiosaRows, "furiosa", "rngd", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Errorf("furiosa/rngd devicePlugin = %q, want required(이 노드는 warboy)", got)
	}
	for _, cap := range []string{"discovery", "driver", "devicePlugin", "dra"} {
		if got := find(furiosaRows, "nvidia", "", cap).Verified; got != v1alpha1.VerificationRequired {
			t.Errorf("evidence 가 furiosa 인데 nvidia/%s 가 verified 로 새면 안 된다: %q", cap, got)
		}
	}

	nvidiaRows := DeriveVendorCapabilities(
		[]v1alpha1.AcceleratorEvidence{evidenceWithVendor("k8s-worker1", "AllocationVerified", "nvidia")},
		nil, []corev1.Node{n}, alwaysFresh)
	if got := find(nvidiaRows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("nvidia devicePlugin = %q, want verified", got)
	}
	for _, product := range []string{"rngd", "warboy"} {
		if got := find(nvidiaRows, "furiosa", product, "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
			t.Errorf("evidence 가 nvidia 인데 furiosa/%s 가 verified 로 새면 안 된다: %q", product, got)
		}
	}
}

// --- multi-target 집계 ---

func TestDeriveMultiTargetOneVerifiedOneDegradedIsDegraded(t *testing.T) {
	layout := []v1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 1}}
	p := v1alpha1.AcceleratorPartitionPolicy{
		Spec: v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia", Layout: layout},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{Targets: []v1alpha1.TargetStatus{
			{NodeName: "n1", Conditions: []metav1.Condition{
				{Type: v1alpha1.ACPPCondVerified, Status: metav1.ConditionTrue, Reason: "AllocatableConverged"},
			}},
			{NodeName: "n2", Conditions: []metav1.Condition{
				{Type: v1alpha1.ACPPCondDegraded, Status: metav1.ConditionTrue},
			}},
		}},
	}
	rows := DeriveVendorCapabilities(nil, []v1alpha1.AcceleratorPartitionPolicy{p}, nil, alwaysFresh)
	if got := find(rows, "nvidia", "", "partition").Verified; got != VerificationDegraded {
		t.Fatalf("한 target 이라도 Degraded 면 전체 Degraded 여야 한다: %q", got)
	}
}

// --- ACPPCondVerified 의 reason 게이트 ---

// 컨트롤러는 partition 이 Ready 인 뒤에만 sharing 을 얹는다
// (acceleratorpartitionpolicy_controller.go:355-365). sharing 검증도 같은 Verified 조건
// 자리에 SetStatusCondition 으로 덮어써 reason 만 SharingVerified 로 남으므로
// (acpp_sharing.go:579-581), reason=SharingVerified 는 "partition 은 물론 sharing 까지"
// 검증됐다는 뜻이다 — "sharing 만" 으로 좁혀 partition=required 를 기대하면 틀린다.
func TestDeriveACPPVerifiedReasonGatesPartitionVsSharing(t *testing.T) {
	layout := []v1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 1}}
	sharing := &v1alpha1.SharingSpec{Mode: v1alpha1.SharingModeTimeSliced}

	partitionReason := v1alpha1.AcceleratorPartitionPolicy{
		Spec: v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia", Layout: layout, Sharing: sharing},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{Targets: []v1alpha1.TargetStatus{
			{NodeName: "n1", Conditions: []metav1.Condition{
				{Type: v1alpha1.ACPPCondVerified, Status: metav1.ConditionTrue, Reason: "AllocatableConverged"},
			}},
		}},
	}
	rows := DeriveVendorCapabilities(nil, []v1alpha1.AcceleratorPartitionPolicy{partitionReason}, nil, alwaysFresh)
	if got := find(rows, "nvidia", "", "partition").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("partition = %q, want verified(reason=AllocatableConverged)", got)
	}
	if got := find(rows, "nvidia", "", "timeSlicing").Verified; got != v1alpha1.VerificationRequired {
		t.Errorf("partition 사유였는데 timeSlicing 까지 verified 면 안 된다: %q", got)
	}

	sharingReason := v1alpha1.AcceleratorPartitionPolicy{
		Spec: v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia", Layout: layout, Sharing: sharing},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{Targets: []v1alpha1.TargetStatus{
			{NodeName: "n1", Conditions: []metav1.Condition{
				{Type: v1alpha1.ACPPCondVerified, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonSharingVerified},
			}},
		}},
	}
	rows = DeriveVendorCapabilities(nil, []v1alpha1.AcceleratorPartitionPolicy{sharingReason}, nil, alwaysFresh)
	if got := find(rows, "nvidia", "", "timeSlicing").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("timeSlicing = %q, want verified(reason=SharingVerified)", got)
	}
	if got := find(rows, "nvidia", "", "partition").Verified; got != v1alpha1.VerificationVerified {
		t.Errorf("partition = %q, want verified(SharingVerified 는 partition Ready 를 함의한다)", got)
	}
}

// Degraded 는 advertisement drift 감시라 이 ACPP 가 요청한 능력 전부(partition 과 sharing 모두)
// 에 적용된다 — Verified 와 달리 reason 으로 나누지 않는다.
func TestDeriveACPPDegradedCoversAllClaimedCapabilities(t *testing.T) {
	layout := []v1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 1}}
	sharing := &v1alpha1.SharingSpec{Mode: v1alpha1.SharingModeMPS}
	p := v1alpha1.AcceleratorPartitionPolicy{
		Spec: v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia", Layout: layout, Sharing: sharing},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{Targets: []v1alpha1.TargetStatus{
			{NodeName: "n1", Conditions: []metav1.Condition{
				{Type: v1alpha1.ACPPCondDegraded, Status: metav1.ConditionTrue},
			}},
		}},
	}
	rows := DeriveVendorCapabilities(nil, []v1alpha1.AcceleratorPartitionPolicy{p}, nil, alwaysFresh)
	if got := find(rows, "nvidia", "", "partition").Verified; got != VerificationDegraded {
		t.Errorf("partition = %q, want degraded(광고 붕괴는 파티션·공유 구분 안 함)", got)
	}
	if got := find(rows, "nvidia", "", "multiProcess").Verified; got != VerificationDegraded {
		t.Errorf("multiProcess = %q, want degraded", got)
	}
}

// --- CollectVendorCapabilitiesWith 자체 ---

// bootIDBeforeReboot 는 nodeAndReportFor 가 만드는 노드의 현재 BootID(boot-1) 직전 값이다 —
// 재부팅으로 환경이 바뀐 evidence 지문을 만들 때 쓴다.
const bootIDBeforeReboot = "boot-0"

//nolint:unparam // name 은 오늘 모든 호출에서 "n1" 이다 — 단일 노드 시나리오라 그렇다.
func nodeAndReportFor(name string) (*corev1.Node, *v1alpha1.NodeDeviceReport) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"kcloud.ai/nvidia.present": labelValueTrue}},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{BootID: "boot-1", KernelVersion: "6.1.0"}},
	}
	devices := []v1alpha1.DeviceEntry{{Vendor: "nvidia", DriverVersion: "580.1"}}
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: name},
		Status:     v1alpha1.NodeDeviceReportStatus{Devices: devices},
	}
	return n, ndr
}

func TestCollectVendorCapabilitiesWithFreshEvidenceIsVerified(t *testing.T) {
	n, ndr := nodeAndReportFor("n1")
	fp := verification.Compute(n, ndr, "nvidia", 1)
	ev := &v1alpha1.AcceleratorEvidence{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec:       v1alpha1.AcceleratorEvidenceSpec{NodeName: "n1"},
		Status: v1alpha1.AcceleratorEvidenceStatus{
			Level:       "AllocationVerified",
			Fingerprint: fp,
			ExpiresAt:   &metav1.Time{Time: time.Now().Add(time.Hour)},
		},
	}
	c := newTestClient(t, n, ndr, ev)
	rows, err := c.CollectVendorCapabilities(context.Background())
	if err != nil {
		t.Fatalf("CollectVendorCapabilities: %v", err)
	}
	if got := find(rows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
		t.Fatalf("devicePlugin = %q, want verified", got)
	}
}

func TestCollectVendorCapabilitiesWithStaleEvidenceIsRequired(t *testing.T) {
	n, ndr := nodeAndReportFor("n1")
	staleFP := verification.Compute(n, ndr, "nvidia", 1)
	staleFP.BootID = bootIDBeforeReboot // 지금 노드(boot-1)와 달라 환경 변화로 무효화
	ev := &v1alpha1.AcceleratorEvidence{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec:       v1alpha1.AcceleratorEvidenceSpec{NodeName: "n1"},
		Status: v1alpha1.AcceleratorEvidenceStatus{
			Level:       "AllocationVerified",
			Fingerprint: staleFP,
			ExpiresAt:   &metav1.Time{Time: time.Now().Add(time.Hour)},
		},
	}
	c := newTestClient(t, n, ndr, ev)
	rows, err := c.CollectVendorCapabilities(context.Background())
	if err != nil {
		t.Fatalf("CollectVendorCapabilities: %v", err)
	}
	if got := find(rows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Fatalf("stale(재부팅) evidence 는 required 여야 한다: %q", got)
	}
}

// 감시축을 고정 DefaultPolicy 로 두면 운영자가 설치한 AcceleratorVerificationPolicy 를 이
// 뷰만 무시해, 같은 evidence 를 컨트롤러는 신선하다고 이 뷰는 낡았다고 본다. invalidateOn
// 에서 BootID 를 뺀 정책 아래선 재부팅으로 BootID 가 바뀐 evidence 도 verified 여야 한다 —
// 정책 없이 같은 픽스처가 required 로 떨어지는 것은 짝을 이루는 위 시험이 고정하므로,
// 두 시험이 함께 "정책이 실제로 반영된다"를 증명한다. nodeSelector 는 이 노드의 실제
// 라벨로 준다 — 빈 selector 는 노드 라벨 선택 경로를 거치지 않는다.
func TestCollectVendorCapabilitiesHonoursVerificationPolicy(t *testing.T) {
	n, ndr := nodeAndReportFor("n1")
	fp := verification.Compute(n, ndr, "nvidia", 1)
	fp.BootID = bootIDBeforeReboot // 재부팅 전 지문 — 지금 노드(boot-1)와 다르다
	ev := &v1alpha1.AcceleratorEvidence{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec:       v1alpha1.AcceleratorEvidenceSpec{NodeName: "n1"},
		Status: v1alpha1.AcceleratorEvidenceStatus{
			Level:       "AllocationVerified",
			Fingerprint: fp,
			ExpiresAt:   &metav1.Time{Time: time.Now().Add(time.Hour)},
		},
	}
	pol := &v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-nodes"},
		Spec: v1alpha1.AcceleratorVerificationPolicySpec{
			NodeSelector: map[string]string{"kcloud.ai/nvidia.present": labelValueTrue},
			InvalidateOn: []string{verification.FieldDriverVersion, verification.FieldKernelVersion},
		},
	}
	c := newTestClient(t, n, ndr, ev, pol)
	rows, err := c.CollectVendorCapabilities(context.Background())
	if err != nil {
		t.Fatalf("CollectVendorCapabilities: %v", err)
	}
	if got := find(rows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationVerified {
		t.Fatalf("BootID 를 감시축에서 뺀 정책 아래서는 verified 여야 한다: %q", got)
	}
}

// 정책의 nodeSelector 가 이 노드를 고르지 않으면 기본 감시축(BootID 포함)이 남아야 한다 —
// Resolve 를 부르되 selector 를 무시하면 위 시험만으로는 못 잡는다.
func TestCollectVendorCapabilitiesIgnoresNonMatchingPolicy(t *testing.T) {
	n, ndr := nodeAndReportFor("n1")
	fp := verification.Compute(n, ndr, "nvidia", 1)
	fp.BootID = bootIDBeforeReboot
	ev := &v1alpha1.AcceleratorEvidence{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec:       v1alpha1.AcceleratorEvidenceSpec{NodeName: "n1"},
		Status: v1alpha1.AcceleratorEvidenceStatus{
			Level:       "AllocationVerified",
			Fingerprint: fp,
			ExpiresAt:   &metav1.Time{Time: time.Now().Add(time.Hour)},
		},
	}
	pol := &v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "other-nodes"},
		Spec: v1alpha1.AcceleratorVerificationPolicySpec{
			NodeSelector: map[string]string{"kcloud.ai/tenstorrent.present": labelValueTrue},
			InvalidateOn: []string{verification.FieldDriverVersion},
		},
	}
	c := newTestClient(t, n, ndr, ev, pol)
	rows, err := c.CollectVendorCapabilities(context.Background())
	if err != nil {
		t.Fatalf("CollectVendorCapabilities: %v", err)
	}
	if got := find(rows, "nvidia", "", "devicePlugin").Verified; got != v1alpha1.VerificationRequired {
		t.Fatalf("이 노드를 고르지 않는 정책은 기본 감시축을 바꾸면 안 된다: %q", got)
	}
}

// Assurance(선언이 어디까지 확인됐는지, internal/vendorcap.Support.Assurance())는
// Verified(실측 성적표)와 기준이 다르다 — 능력 칸이 Assurance 도 실어야 REST/CLI 가
// "구현했다"와 "확인했다"를 다시 섞지 않는다. Declared/Verified 는 그대로 두고 값만 늘린다.
func TestDeriveCapabilityRowsCarryAssurance(t *testing.T) {
	rows := DeriveVendorCapabilities(nil, nil, nil, alwaysFresh)
	if got := find(rows, "nvidia", "", "driver").Assurance; got != "unit" {
		t.Fatalf("nvidia/driver assurance = %q, want %q", got, "unit")
	}
	// 등급을 적지 않은 칸은 Support.Assurance() 의 기본값(none)으로 읽혀야 한다 — 빈
	// 문자열로 새면 "확인 안 함"과 "기록 안 함"이 구분되지 않는다.
	if got := find(rows, "nvidia", "", "discovery").Assurance; got != "none" {
		t.Fatalf("nvidia/discovery assurance = %q, want %q(기본값)", got, "none")
	}
}

// TestCapabilityRefusal_판정은State만본다 는 capabilityRefusal(partition·sharing
// apply·allocation switch 가 공유하는 판정)을 실제 레지스트리와 분리해 합성 Support 값으로
// 직접 시험한다 — 클러스터도, 오늘 레지스트리에 특정 미지원 조합이 있는지도 필요 없다.
func TestCapabilityRefusal_판정은State만본다(t *testing.T) {
	cases := []struct {
		name    string
		s       vendorcap.Support
		wantErr bool
	}{
		{"Unsupported면거절", vendorcap.Support{State: vendorcap.StateUnsupported, Reason: "테스트 사유"}, true},
		{"Unknown이면거절", vendorcap.Support{State: vendorcap.StateUnknown, Reason: "조사 필요"}, true},
		{"Native면통과", vendorcap.Support{State: vendorcap.StateNative}, false},
		{"Software면통과", vendorcap.Support{State: vendorcap.StateSoftware}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := capabilityRefusal("acme", "partition", tc.s)
			if tc.wantErr && err == nil {
				t.Fatalf("거절돼야 하는데 에러가 없다: %+v", tc.s)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("통과해야 하는데 거절됨: %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.s.Reason) {
				t.Errorf("거절 메시지에 사유가 안 보인다: %v", err)
			}
		})
	}
}

// TestCapabilityRefusal_실제레지스트리로End_to_End 는 합성값이 아니라 실제
// internal/vendorcap 레지스트리에서 오늘 진짜로 Unsupported 인 조합(tenstorrent/partition)
// 으로 LookupVendor→capabilityRefusal 왕복이 실제로 거절되는지 한 번은 확인한다 —
// 단위 시험(위)이 판정 자체를, 이 시험이 그 판정이 실제 데이터와 이어지는지를 각각 본다.
func TestCapabilityRefusal_실제레지스트리로End_to_End(t *testing.T) {
	err := capabilityRefusal("tenstorrent", "partition", vendorcap.LookupVendor("tenstorrent", vendorcap.CapPartition))
	if err == nil {
		t.Fatalf("tenstorrent 는 partition 미지원인데 에러가 없다")
	}
}
