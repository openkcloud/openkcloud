// ============================================================
// inventory_test.go: 인벤토리 집약(BuildInventory) 단위 테스트
// 상세: 정직성 불변식 세 개를 못박는다 — (1) time-slicing replica 는 행이 되지 않는다,
//       (2) supported 주장만으로 Verified 가 켜지지 않는다, (3) stale/미수렴 데이터는 숨기지 않는다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

func node(name string, cordoned bool, alloc map[string]string) corev1.Node {
	n := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status:     corev1.NodeStatus{Allocatable: corev1.ResourceList{}},
	}
	for k, v := range alloc {
		n.Status.Allocatable[corev1.ResourceName(k)] = resource.MustParse(v)
	}
	return n
}

func ndr(nodeName string, entries ...v1alpha1.DeviceEntry) v1alpha1.NodeDeviceReport {
	return v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: nodeName},
		Status:     v1alpha1.NodeDeviceReportStatus{Devices: entries},
	}
}

// acppReady 는 수렴 완료(observedGeneration==generation, phase=Ready)한 정책이다. 정책/노드
// 이름은 이 테스트 파일에서 항상 "p"/"worker1" 하나뿐이라(unparam) 파라미터로 두지 않고 고정한다.
// readySiblingPolicyName 은 사전순으로 "a2" 뒤에 오는 이름("a30")을 써서 정렬 순서를 고정한다.
const readySiblingPolicyName = "worker1-a30-4x1g"

func acppReady(devices []v1alpha1.DeviceStatus, sharingMode string, replicas int32) v1alpha1.AcceleratorPartitionPolicy {
	return v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Generation: 1},
		Spec:       v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 1,
			Targets: []v1alpha1.TargetStatus{{
				NodeName: "worker1", Phase: v1alpha1.ACPPPhaseReady, Devices: devices,
			}},
			ApplyRecords: []v1alpha1.ApplyRecord{{
				NodeName: "worker1", SharingMode: sharingMode, SharingReplicas: replicas,
			}},
		},
	}
}

// TestBuildInventory_TimeSlicedReplicaIsNotADeviceRow: 정직성 계약 (2).
func TestBuildInventory_TimeSlicedReplicaIsNotADeviceRow(t *testing.T) {
	devs := []v1alpha1.DeviceStatus{{ID: "GPU-aaa", Model: "A30"}}
	// 물리 1장을 4배로 광고 중인 노드.
	nodes := []corev1.Node{node("worker1", false, map[string]string{"nvidia.com/gpu": "4"})}
	acpps := []v1alpha1.AcceleratorPartitionPolicy{acppReady(devs, v1alpha1.SharingModeTimeSliced, 4)}
	ndrs := []v1alpha1.NodeDeviceReport{ndr("worker1", v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "A30", Count: 1})}

	got := BuildInventory(nodes, acpps, ndrs, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("물리 장치 1개 = 1행이어야 함(replica 는 행이 아니다): got %d rows", len(got))
	}
	if got[0].SharingMode != v1alpha1.SharingModeTimeSliced || got[0].SharingReplicas != 4 {
		t.Fatalf("복제 사실은 필드로만 나타나야 함: mode=%q replicas=%d", got[0].SharingMode, got[0].SharingReplicas)
	}
	if got[0].NodeAdvertised["nvidia.com/gpu"] != 4 {
		t.Fatalf("노드 광고량은 그대로 보고해야 함: %v", got[0].NodeAdvertised)
	}
}

// TestBuildInventory_UnverifiedSharingIsNotVerified: 정직성 계약 (1).
func TestBuildInventory_UnverifiedSharingIsNotVerified(t *testing.T) {
	devs := []v1alpha1.DeviceStatus{{
		ID: "GPU-aaa", Model: "A30",
		SharingCapability: v1alpha1.SharingCapability{
			// supported 라고 주장하지만 실측 전이다.
			MultiProcess: v1alpha1.SharingModeSupport{Supported: true, Verification: v1alpha1.VerificationRequired},
			TimeSlicing:  v1alpha1.SharingModeSupport{Supported: true, Verification: v1alpha1.VerificationVerified, MaxReplicas: 8},
		},
	}}
	nodes := []corev1.Node{node("worker1", false, map[string]string{"nvidia.com/gpu": "1"})}
	acpps := []v1alpha1.AcceleratorPartitionPolicy{acppReady(devs, v1alpha1.SharingModeExclusive, 0)}

	got := BuildInventory(nodes, acpps, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("행 1개여야 함: got %d", len(got))
	}
	if got[0].Capability.Sharing.MultiProcess.Verified {
		t.Fatalf("verification=required 는 Verified 가 false 여야 함")
	}
	if !got[0].Capability.Sharing.MultiProcess.Supported {
		t.Fatalf("원본 주장(Supported)은 지워지지 않고 그대로 보고되어야 함")
	}
	if !got[0].Capability.Sharing.TimeSlicing.Verified {
		t.Fatalf("verification=verified 는 Verified 가 true 여야 함")
	}
}

// TestBuildInventory_UnmanagedNodeUsesSyntheticID: ACPP 가 없는 노드도 보이되 출처를 밝힌다.
func TestBuildInventory_UnmanagedNodeUsesSyntheticID(t *testing.T) {
	nodes := []corev1.Node{node("rngd-1", false, map[string]string{"furiosa.ai/rngd": "2"})}
	ndrs := []v1alpha1.NodeDeviceReport{ndr("rngd-1",
		v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "rngd", Count: 2, MemoryMiB: 49152})}

	got := BuildInventory(nodes, nil, ndrs, intent.DRACapability{})
	if len(got) != 2 {
		t.Fatalf("NDR count=2 는 2행이어야 함: got %d", len(got))
	}
	for i := range got {
		if got[i].Source != SourceNDR {
			t.Fatalf("출처가 NDR 로 표기되어야 함: %q", got[i].Source)
		}
		if got[i].UID == "" {
			t.Fatalf("합성 ID 라도 비어 있으면 안 됨")
		}
	}
	if got[0].UID == got[1].UID {
		t.Fatalf("합성 ID 가 중복됨: %q", got[0].UID)
	}
}

// TestBuildInventory_CordonedNodeStillListed: cordon 은 스케줄 판정이지 장치가 사라진 게 아니다.
func TestBuildInventory_CordonedNodeStillListed(t *testing.T) {
	devs := []v1alpha1.DeviceStatus{{ID: "GPU-bbb", Model: "A2"}}
	nodes := []corev1.Node{node("worker1", true, map[string]string{"nvidia.com/gpu": "1"})}
	acpps := []v1alpha1.AcceleratorPartitionPolicy{acppReady(devs, v1alpha1.SharingModeExclusive, 0)}

	got := BuildInventory(nodes, acpps, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("cordon 된 노드의 장치도 목록에 있어야 함: got %d", len(got))
	}
	if got[0].Schedulable {
		t.Fatalf("Schedulable 은 false 로 정직하게 보고해야 함")
	}
	if got[0].Source != SourceACPP || got[0].UID != "GPU-bbb" {
		t.Fatalf("ACPP canonical ID 를 써야 함: source=%q uid=%q", got[0].Source, got[0].UID)
	}
}

// TestBuildInventory_StaleACPPRowIsVisiblyStale: 정직성 계약 (3) 전반부 — 미수렴은 숨기지 않는다.
// spec 이 바뀌어 generation 은 올랐지만(2) 컨트롤러가 아직 반영 못한(observedGeneration=1) 경우,
// status.targets[].phase 에는 낡은 "Ready" 값이 그대로 남아 있다 — 이 낡은 phase/devices 를
// 그대로 믿으면 안 된다는 시나리오를 재현한다.
func TestBuildInventory_StaleACPPRowIsVisiblyStale(t *testing.T) {
	devs := []v1alpha1.DeviceStatus{{ID: "GPU-aaa", Model: "A30"}}
	nodes := []corev1.Node{node("worker1", false, map[string]string{"nvidia.com/gpu": "1"})}
	acpp := acppReady(devs, v1alpha1.SharingModeExclusive, 0)
	acpp.Generation = 2 // observedGeneration 은 acppReady 가 1로 고정 — 재조정 전.
	acpps := []v1alpha1.AcceleratorPartitionPolicy{acpp}

	got := BuildInventory(nodes, acpps, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("stale 노드도 1행은 보여야 함: got %d", len(got))
	}
	if !got[0].Stale {
		t.Fatalf("generation 불일치는 Stale=true 로 나와야 함")
	}
	if got[0].StaleReason == "" {
		t.Fatalf("StaleReason 이 비어 있으면 안 됨")
	}
	if got[0].Source != SourceACPP {
		t.Fatalf("stale 여도 관리 주체는 ACPP 로 남아야 함(미관리로 오분류 금지): got %q", got[0].Source)
	}
}

// TestBuildInventory_PendingACPPNodeNotLabeledUnmanaged: 정직성 계약 (3) 후반부.
// ACPP 가 노드를 타깃하지만 아직 장치를 하나도 보고하지 않은(Phase=Applying) 상태다. 이걸 NDR
// 미관리 경로로 떨어뜨리면 "관리 중" 과 "관리 안 함" 이 API 상 구분 불가능해진다.
func TestBuildInventory_PendingACPPNodeNotLabeledUnmanaged(t *testing.T) {
	nodes := []corev1.Node{node("worker1", false, map[string]string{"nvidia.com/gpu": "1"})}
	acpp := v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Generation: 1},
		Spec:       v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 1,
			Targets:            []v1alpha1.TargetStatus{{NodeName: "worker1", Phase: v1alpha1.ACPPPhaseApplying}},
		},
	}

	got := BuildInventory(nodes, []v1alpha1.AcceleratorPartitionPolicy{acpp}, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("진행 중인 관리 노드도 1행은 보여야 함: got %d", len(got))
	}
	if got[0].Source != SourceACPP {
		t.Fatalf("아직 보고 전이라도 미관리(NDR)로 분류하면 안 됨: got %q", got[0].Source)
	}
	if !got[0].Pending {
		t.Fatalf("장치 미보고 상태는 Pending=true 로 드러나야 함(UID 파싱 없이도 식별 가능해야 함)")
	}
	if got[0].UID != "" {
		t.Fatalf("pending 행은 실제 장치 UID 가 없어야 함: got %q", got[0].UID)
	}
	if got[0].Policy != "p" || got[0].Phase != v1alpha1.ACPPPhaseApplying {
		t.Fatalf("policy/phase 는 버려지면 안 됨: policy=%q phase=%q", got[0].Policy, got[0].Phase)
	}
}

// TestBuildInventory_MultiVendorNodeDeviceVendorMatchesModel: NVIDIA A30/A2 + Furiosa 리소스가
// 같은 노드에 공존하면 리소스명 사전순(furiosa < nvidia)으로 nc.Vendor 가 "furiosa" 가 되므로,
// 장치별 벤더는 반드시 모델 매칭으로 되짚어야 한다.
func TestBuildInventory_MultiVendorNodeDeviceVendorMatchesModel(t *testing.T) {
	devs := []v1alpha1.DeviceStatus{
		{ID: "GPU-a30", Model: "A30"},
		{ID: "GPU-a2", Model: "A2"},
	}
	nodes := []corev1.Node{node("worker1", false, map[string]string{
		"furiosa.ai/rngd": "1",
		"nvidia.com/gpu":  "2",
	})}
	acpps := []v1alpha1.AcceleratorPartitionPolicy{acppReady(devs, v1alpha1.SharingModeExclusive, 0)}
	ndrs := []v1alpha1.NodeDeviceReport{ndr("worker1",
		v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "A30", Count: 1},
		v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "A2", Count: 1},
		v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "rngd", Count: 1},
	)}

	got := BuildInventory(nodes, acpps, ndrs, intent.DRACapability{})
	if len(got) != 2 {
		t.Fatalf("ACPP 장치 2개 = 2행: got %d", len(got))
	}
	for _, v := range got {
		if v.Vendor != "nvidia" {
			t.Fatalf("멀티벤더 노드에서 NVIDIA 장치는 모델 매칭으로 vendor=nvidia 여야 함: model=%q got=%q", v.Model, v.Vendor)
		}
	}
}

// TestBuildInventory_SortedDeterministically: UI 가 새로고침마다 순서가 바뀌면 못 읽는다.
func TestBuildInventory_SortedDeterministically(t *testing.T) {
	nodes := []corev1.Node{
		node("worker2", false, map[string]string{"nvidia.com/gpu": "1"}),
		node("worker1", false, map[string]string{"nvidia.com/gpu": "1"}),
	}
	ndrs := []v1alpha1.NodeDeviceReport{
		ndr("worker1", v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "A30", Count: 1}),
		ndr("worker2", v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "A2", Count: 1}),
	}
	got := BuildInventory(nodes, nil, ndrs, intent.DRACapability{})
	if len(got) != 2 || got[0].NodeName != "worker1" || got[1].NodeName != "worker2" {
		t.Fatalf("노드 이름 사전순이어야 함: %#v", got)
	}
}

// TestBuildInventory_CarriesPartitionInstanceCounts: profile 이름 목록만으로는 논리 파티션 수를
// 셀 수 없다 — layout 항목 1개가 장치당 N개다. expectedCountPerDevice 가 뷰까지 와야 대시보드가
// 정직한 수를 낼 수 있다.
func TestBuildInventory_CarriesPartitionInstanceCounts(t *testing.T) {
	objs := invObjects() // 기존 헬퍼. ACPP TargetStatus.ResolvedLayout 이 들어 있다.
	var nodes []corev1.Node
	var acpps []v1alpha1.AcceleratorPartitionPolicy
	var ndrs []v1alpha1.NodeDeviceReport
	for _, o := range objs {
		switch v := o.(type) {
		case *corev1.Node:
			nodes = append(nodes, *v)
		case *v1alpha1.AcceleratorPartitionPolicy:
			acpps = append(acpps, *v)
		case *v1alpha1.NodeDeviceReport:
			ndrs = append(ndrs, *v)
		}
	}

	rows := BuildInventory(nodes, acpps, ndrs, intent.DRACapability{})
	var found bool
	for _, r := range rows {
		for _, pi := range r.PartitionInstances {
			if pi.Profile == "" {
				t.Errorf("profile 이 비었다: %+v", pi)
			}
			if pi.CountPerDevice < 1 {
				t.Errorf("countPerDevice 가 1 미만이면 인스턴스가 아니다: %+v", pi)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("파티션이 적용된 행이 있는데 PartitionInstances 가 비어 있다")
	}
}

// 화면이 3상태를 낼 수 있으려면 verification 이 JSON 까지 와야 한다.
func TestToCapabilityView_CarriesPartitionVerification(t *testing.T) {
	dev := v1alpha1.DeviceStatus{
		Model: "A30",
		PartitionCapability: v1alpha1.PartitionCapability{
			HardwareSupported: false,
			Reason:            v1alpha1.ReasonHardwareCapabilityMissing,
			Verification:      v1alpha1.VerificationRequired,
		},
	}
	v := toCapabilityView(dev)
	if v.Partition.Verification != v1alpha1.VerificationRequired {
		t.Fatalf("verification 이 투영되지 않음: %+v", v.Partition)
	}
}

// governed-but-pending 행은 확인된 장치가 0개다. 그 행에 "장치당 N 인스턴스" 를 실으면
// 아직 존재하지 않는 장치에 대해 수를 주장하는 셈이다(stale 을 비우는 것과 같은 이유).
// 대시보드 카드는 pending 을 "미상" 으로 빼므로 화면은 안전하지만 원시 JSON 이 새는 자리다.
func TestBuildInventory_PendingRowCarriesNoInstanceCounts(t *testing.T) {
	nodes := []corev1.Node{node("worker1", false, map[string]string{"nvidia.com/gpu": "1"})}
	acpp := v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Generation: 1},
		Spec:       v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 1,
			Targets: []v1alpha1.TargetStatus{{
				// Ready + generation 일치라 stale 이 아니다. 그런데 Devices 가 비어 있어 pending
				// 이다 — 이 조합에서만 stale 게이트가 못 막고 인스턴스 수가 실린다.
				NodeName: "worker1", Phase: v1alpha1.ACPPPhaseReady,
				ResolvedLayout: []v1alpha1.ResolvedLayoutEntry{{Profile: "1g.6gb", ExpectedCountPerDevice: 4}},
			}},
		},
	}
	rows := BuildInventory(nodes, []v1alpha1.AcceleratorPartitionPolicy{acpp}, nil, intent.DRACapability{})
	var sawPending bool
	for _, r := range rows {
		if !r.Pending {
			continue
		}
		sawPending = true
		if len(r.PartitionInstances) != 0 {
			t.Errorf("pending 행이 인스턴스 수를 주장한다: %+v", r.PartitionInstances)
		}
	}
	if !sawPending {
		t.Fatal("픽스처에 pending 행이 없다 — 테스트가 아무것도 검사하지 않았다")
	}
}

// TestBuildInventory_FailedSiblingDoesNotHideReadyRows: devicesForNode 의 첫-매치 단락이
// Failed 정책("worker1-a2-timeslice")을 골라 같은 노드의 Ready 정책(readySiblingPolicyName)
// 장치 행을 뭉개면 안 된다.
func TestBuildInventory_FailedSiblingDoesNotHideReadyRows(t *testing.T) {
	failed := v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1-a2-timeslice", Generation: 1},
		Spec:       v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 1,
			Targets:            []v1alpha1.TargetStatus{{NodeName: "worker1", Phase: v1alpha1.ACPPPhaseFailed}},
		},
	}
	ready := acppReady([]v1alpha1.DeviceStatus{{ID: "PCI-0000:18:00.0", Model: "NVIDIA A30"}}, v1alpha1.SharingModeExclusive, 0)
	ready.Name = readySiblingPolicyName
	ready.Status.Targets[0].ResolvedLayout = []v1alpha1.ResolvedLayoutEntry{{Profile: "1g.6gb", ExpectedCountPerDevice: 4}}

	nodes := []corev1.Node{node("worker1", false, map[string]string{"nvidia.com/gpu": "1", "nvidia.com/mig-1g.6gb": "4"})}
	got := BuildInventory(nodes, []v1alpha1.AcceleratorPartitionPolicy{failed, ready}, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("Ready 정책 장치 1개 = 1행이어야 함: got %d rows: %#v", len(got), got)
	}
	if got[0].Policy != readySiblingPolicyName || got[0].Phase != v1alpha1.ACPPPhaseReady {
		t.Fatalf("행이 Ready 정책 소속이어야 함: policy=%q phase=%q", got[0].Policy, got[0].Phase)
	}
	if got[0].Stale {
		t.Fatalf("Ready 정책 채택 시 stale 이 아니어야 함: %q", got[0].StaleReason)
	}
	if len(got[0].PartitionInstances) != 1 || got[0].PartitionInstances[0].CountPerDevice != 4 {
		t.Fatalf("Ready 정책의 인스턴스 수가 실려야 함: %#v", got[0].PartitionInstances)
	}
}

// TestBuildInventory_SameModelEntriesGetDistinctIDs: 한 노드가 같은 벤더·모델 장치를 여러
// 집계 행으로 보고하면(카드마다 PCI 주소가 달라 detector 가 행을 나눈다) 합성 ID 가 행마다
// 0 부터 다시 매겨져 충돌한다. 예: A30 2장이 모두 "k8s-worker2/nvidia/generic#0" 이 된다.
// 충돌하면 UID 단건 조회가 영원히 첫 장치만 돌려주고 두 번째 장치는 조회할 길이 없다.
func TestBuildInventory_SameModelEntriesGetDistinctIDs(t *testing.T) {
	nodes := []corev1.Node{node("worker2", false, map[string]string{"nvidia.com/gpu": "2"})}
	ndrs := []v1alpha1.NodeDeviceReport{ndr("worker2",
		v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "generic", Count: 1, PCIeAddress: "0000:18:00.0"},
		v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "generic", Count: 1, PCIeAddress: "0000:af:00.0"})}

	got := BuildInventory(nodes, nil, ndrs, intent.DRACapability{})
	if len(got) != 2 {
		t.Fatalf("집계 행 2개 = 2행이어야 함: got %d", len(got))
	}
	if got[0].UID == got[1].UID {
		t.Fatalf("같은 모델 장치 둘의 합성 ID 가 충돌함: %q", got[0].UID)
	}
}

// TestBuildInventory_SyntheticIDIsStableAcrossReportOrder: 집계 행 순서가 뒤집혀도 같은
// 물리 장치(PCI 주소)가 같은 합성 ID 를 받아야 한다. 정렬 기준이 벤더·모델뿐이면 동률 행의
// 순서가 흔들려 UID 가 장치 사이를 오간다 — 그러면 UID 로 장치를 가리킬 수 없다.
func TestBuildInventory_SyntheticIDIsStableAcrossReportOrder(t *testing.T) {
	entry := func(pci string) v1alpha1.DeviceEntry {
		return v1alpha1.DeviceEntry{Vendor: "nvidia", Model: "generic", Count: 1, PCIeAddress: pci}
	}
	asc := []v1alpha1.DeviceEntry{entry("0000:18:00.0"), entry("0000:3b:00.0"),
		entry("0000:5e:00.0"), entry("0000:af:00.0"), entry("0000:d8:00.0")}
	desc := make([]v1alpha1.DeviceEntry, len(asc))
	for i := range asc {
		desc[i] = asc[len(asc)-1-i]
	}
	nodes := []corev1.Node{node("worker2", false, map[string]string{"nvidia.com/gpu": "5"})}

	uidOf := func(entries ...v1alpha1.DeviceEntry) map[string]string {
		got := BuildInventory(nodes, nil, []v1alpha1.NodeDeviceReport{ndr("worker2", entries...)}, intent.DRACapability{})
		m := map[string]string{}
		for i := range got {
			m[got[i].PCIAddress] = got[i].UID
		}
		return m
	}
	forward, reverse := uidOf(asc...), uidOf(desc...)
	for pci, uid := range forward {
		if reverse[pci] != uid {
			t.Fatalf("보고 순서에 따라 %s 의 합성 ID 가 바뀜: %q → %q", pci, uid, reverse[pci])
		}
	}
}

func TestAttachHealth_NoCRMeansNoKey(t *testing.T) {
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", PCIAddress: "0000:18:00.0"}}
	attachHealth(items, nil)
	if items[0].Health != nil {
		t.Fatalf("CR 이 없는 노드의 행에 health 가 붙었다: %+v", items[0].Health)
	}
	raw, _ := json.Marshal(items[0])
	if strings.Contains(string(raw), `"health"`) {
		t.Fatalf("JSON 에 health 키가 있다: %s", raw)
	}
}

func TestAttachHealth_DeviceRowWinsOverNode(t *testing.T) {
	items := []AcceleratorView{
		{UID: "u1", NodeName: "n1", PCIAddress: "0000:18:00.0"},
		{UID: "u2", NodeName: "n1", PCIAddress: "0000:3b:00.0"},
	}
	h := v1alpha1.AcceleratorHealth{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Spec: v1alpha1.AcceleratorHealthSpec{NodeName: "n1"},
		Status: v1alpha1.AcceleratorHealthStatus{State: "Degraded", Reason: "node-level",
			DetectedAt: metav1.NewTime(time.Date(2026, 10, 5, 1, 2, 3, 0, time.UTC)),
			Devices:    []v1alpha1.DeviceHealth{{PCIAddress: "0000:18:00.0", State: "Unhealthy", Reason: "xid-79"}}}}
	attachHealth(items, []v1alpha1.AcceleratorHealth{h})
	if got := items[0].Health; got == nil || got.State != "Unhealthy" || got.Reason != "xid-79" || got.Scope != "device" || got.DetectedAt != "" {
		t.Fatalf("PCI 매칭 행(device 범위는 시각이 없어야 한다): %+v", got)
	}
	if got := items[1].Health; got == nil || got.State != "Degraded" || got.Scope != "node" || got.DetectedAt != "2026-10-05T01:02:03Z" {
		t.Fatalf("노드 폴백 행: %+v", got)
	}
}

func TestAttachHealth_PendingAndStaleRowsCarryNoHealth(t *testing.T) {
	items := []AcceleratorView{{NodeName: "n1", Pending: true}, {UID: "u1", NodeName: "n1", Stale: true}}
	h := v1alpha1.AcceleratorHealth{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: v1alpha1.AcceleratorHealthStatus{State: "Healthy"}}
	attachHealth(items, []v1alpha1.AcceleratorHealth{h})
	for i, it := range items {
		if it.Health != nil {
			t.Fatalf("행 %d(pending/stale)에 health 가 붙었다", i)
		}
	}
}

func TestAttachHealth_EmptyStateIsUnknownNotHealthy(t *testing.T) {
	items := []AcceleratorView{{UID: "u1", NodeName: "n1"}}
	attachHealth(items, []v1alpha1.AcceleratorHealth{{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}})
	if got := items[0].Health; got == nil || got.State != "Unknown" || got.Scope != "node" {
		t.Fatalf("CR 은 있고 판정이 비면 Unknown 이어야 한다: %+v", got)
	}
}

// TestAttachHealth_DeviceRowWithEmptyStateFallsBackToNode: PCI 가 일치하는 장치 판정 항목이
// 있어도 그 State 가 비어 있으면 device 로 확정하지 않고 노드 판정으로 폴백한다(attachHealth 의
// `d.State != ""` 가드 — 이 가드가 빠지면 비어 있는 장치 판정이 노드의 실제 판정(Degraded)을
// 가린다). 같은 PCI 라도 device.State 가 채워지면(Unhealthy) 그제서야 device 가 이긴다는
// 대조를 같은 시험에서 함께 본다.
func TestAttachHealth_DeviceRowWithEmptyStateFallsBackToNode(t *testing.T) {
	// goconst: Scope 값 "node"/"device" 리터럴 비교가 이 패키지에 이미 여러 곳 있다 —
	// 여기서는 상수로 참조해 리터럴 반복을 보태지 않는다.
	const scopeNode, scopeDevice = "node", "device"
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", PCIAddress: "0000:18:00.0"}}
	empty := v1alpha1.AcceleratorHealth{ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: v1alpha1.AcceleratorHealthStatus{State: "Degraded", Reason: "node-level",
			Devices: []v1alpha1.DeviceHealth{{PCIAddress: "0000:18:00.0", State: ""}}}}
	attachHealth(items, []v1alpha1.AcceleratorHealth{empty})
	if got := items[0].Health; got == nil || got.State != "Degraded" || got.Scope != scopeNode {
		t.Fatalf("device.State 공백은 노드 판정으로 폴백해야 함: %+v", got)
	}

	items2 := []AcceleratorView{{UID: "u1", NodeName: "n1", PCIAddress: "0000:18:00.0"}}
	filled := v1alpha1.AcceleratorHealth{ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: v1alpha1.AcceleratorHealthStatus{State: "Degraded", Reason: "node-level",
			Devices: []v1alpha1.DeviceHealth{{PCIAddress: "0000:18:00.0", State: "Unhealthy", Reason: "xid-79"}}}}
	attachHealth(items2, []v1alpha1.AcceleratorHealth{filled})
	if got := items2[0].Health; got == nil || got.State != "Unhealthy" || got.Scope != scopeDevice {
		t.Fatalf("device.State 채워지면 device 가 이겨야 함: %+v", got)
	}
}

// podWith 는 namespace 를 받지 않는다 — 이 파일에서 항상 "a" 하나뿐이라(unparam) acppReady 와
// 같은 규율로 고정한다.
func podWith(name, node string, phase corev1.PodPhase, req map[string]string) corev1.Pod {
	rl := corev1.ResourceList{}
	for k, v := range req {
		rl[corev1.ResourceName(k)] = resource.MustParse(v)
	}
	return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: name},
		Spec:   corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: rl, Limits: rl}}}},
		Status: corev1.PodStatus{Phase: phase}}
}

func TestAttachAllocation_FreeIsAdvertisedMinusRequested(t *testing.T) {
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", NodeAdvertised: map[string]int32{"nvidia.com/gpu": 2}}}
	attachAllocation(items, []corev1.Pod{podWith("p1", "n1", corev1.PodRunning, map[string]string{"nvidia.com/gpu": "1"})})
	if items[0].NodeAllocated["nvidia.com/gpu"] != 1 || items[0].NodeFree["nvidia.com/gpu"] != 1 {
		t.Fatalf("allocated=%v free=%v", items[0].NodeAllocated, items[0].NodeFree)
	}
}

func TestAttachAllocation_NegativeFreeIsKept(t *testing.T) {
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", NodeAdvertised: map[string]int32{"nvidia.com/gpu": 1}}}
	attachAllocation(items, []corev1.Pod{podWith("p1", "n1", corev1.PodRunning, map[string]string{"nvidia.com/gpu": "2"})})
	if items[0].NodeFree["nvidia.com/gpu"] != -1 {
		t.Fatalf("광고보다 많이 할당된 상태는 음수로 그대로 내보내야 한다: %v", items[0].NodeFree)
	}
}

func TestAttachAllocation_FinishedPodsAndOtherNodesDoNotCount(t *testing.T) {
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", NodeAdvertised: map[string]int32{"furiosa.ai/rngd": 4}}}
	attachAllocation(items, []corev1.Pod{
		podWith("done", "n1", corev1.PodSucceeded, map[string]string{"furiosa.ai/rngd": "1"}),
		podWith("failed", "n1", corev1.PodFailed, map[string]string{"furiosa.ai/rngd": "1"}),
		podWith("elsewhere", "n2", corev1.PodRunning, map[string]string{"furiosa.ai/rngd": "1"}),
		podWith("pending", "n1", corev1.PodPending, map[string]string{"furiosa.ai/rngd": "1"}),
		podWith("cpu-only", "n1", corev1.PodRunning, map[string]string{"cpu": "1"}),
	})
	if items[0].NodeAllocated["furiosa.ai/rngd"] != 1 || items[0].NodeFree["furiosa.ai/rngd"] != 3 {
		t.Fatalf("Pending 만 세고 Succeeded·Failed·타 노드·비가속기는 빠져야 한다: %v / %v", items[0].NodeAllocated, items[0].NodeFree)
	}
	if _, ok := items[0].NodeAllocated["cpu"]; ok {
		t.Fatal("cpu 는 가속기 리소스가 아니다")
	}
}

// TestAttachAllocation_RestartableInitContainerCountsPlainDoesNot: K8s 1.28+ 사이드카
// 초기화 컨테이너(restartPolicy: Always)는 파드가 도는 동안 내내 가속기를 쥐므로 더해야
// 하고, 재시작하지 않는 일반 초기화 컨테이너는 시작 단계에만 쥐므로 빼야 한다.
func TestAttachAllocation_RestartableInitContainerCountsPlainDoesNot(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	req := corev1.ResourceList{"furiosa.ai/rngd": resource.MustParse("1")}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "p1"},
		Spec: corev1.PodSpec{
			NodeName: "n1",
			InitContainers: []corev1.Container{
				{Name: "sidecar", RestartPolicy: &always, Resources: corev1.ResourceRequirements{Requests: req}},
				{Name: "init", Resources: corev1.ResourceRequirements{Requests: req}},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", NodeAdvertised: map[string]int32{"furiosa.ai/rngd": 4}}}
	attachAllocation(items, []corev1.Pod{pod})
	if items[0].NodeAllocated["furiosa.ai/rngd"] != 1 {
		t.Fatalf("사이드카 초기화 컨테이너 요청만 세어야 한다(일반 초기화 컨테이너는 제외): %v", items[0].NodeAllocated)
	}
}

// allocatedClaimOn 은 node 풀에서 driver 로 device 하나를 할당받은 ResourceClaim 이다.
func allocatedClaimOn(node, driver, device string) resourcev1.ResourceClaim {
	return resourcev1.ResourceClaim{
		Status: resourcev1.ResourceClaimStatus{
			Allocation: &resourcev1.AllocationResult{
				Devices: resourcev1.DeviceAllocationResult{
					Results: []resourcev1.DeviceRequestAllocationResult{{Driver: driver, Pool: node, Device: device, Request: "req"}},
				},
			},
		},
	}
}

func TestAttachDRAAllocation_CountsSlicesAndClaims(t *testing.T) {
	items := []AcceleratorView{{NodeName: "w1", NodeAdvertised: map[string]int32{"nvidia.com/gpu": 0}}}
	dra := intent.DRACapability{APIServed: true, SlicesByNodeDriver: map[string]map[string]int32{"w1": {"gpu.nvidia.com": 2}}}
	claims := []resourcev1.ResourceClaim{allocatedClaimOn("w1", "gpu.nvidia.com", "gpu-0")} // Pool=노드명, Driver, Device
	attachDRAAllocation(items, dra, claims)
	it := items[0]
	if it.NodeAdvertised["dra:gpu.nvidia.com"] != 2 || it.NodeAllocated["dra:gpu.nvidia.com"] != 1 || it.NodeFree["dra:gpu.nvidia.com"] != 1 {
		t.Fatalf("adv=%v alloc=%v free=%v", it.NodeAdvertised, it.NodeAllocated, it.NodeFree)
	}
}

func TestAttachDRAAllocation_NoSlicesLeavesRowUntouched(t *testing.T) {
	items := []AcceleratorView{{NodeName: "w1", NodeAdvertised: map[string]int32{"nvidia.com/gpu": 2}}}
	attachDRAAllocation(items, intent.DRACapability{}, nil)
	if _, ok := items[0].NodeAdvertised["dra:gpu.nvidia.com"]; ok || len(items[0].NodeAdvertised) != 1 {
		t.Fatalf("슬라이스가 없는 노드에 dra 키가 생겼다: %v", items[0].NodeAdvertised)
	}
}

// draSliceWithPool 은 node 가 driver 로, 노드명과 다를 수 있는 pool 에서 장치 n개를 내는
// ResourceSlice 다. internal/intent/translate_test.go 의 draSlice 와 이름이 같을 뿐 다른
// 패키지의 비공개 함수라 가져다 쓸 수 없어, 기존 서명을 바꾸지 않고 이 시험 전용 지역
// 빌더를 둔다.
func draSliceWithPool(node, driver, pool string, n int) resourcev1.ResourceSlice {
	devices := make([]resourcev1.Device, n)
	for i := range devices {
		devices[i] = resourcev1.Device{Name: fmt.Sprintf("dev%d", i)}
	}
	return resourcev1.ResourceSlice{Spec: resourcev1.ResourceSliceSpec{
		Driver: driver, NodeName: ptr.To(node), Pool: resourcev1.ResourcePool{Name: pool}, Devices: devices,
	}}
}

// 풀 이름이 노드명과 다른 드라이버도 할당이 집계되어야 한다 — res.Pool 을 노드명으로만 찾으면
// 할당이 0 으로 떨어지고 free 가 광고 수와 같아져 여유를 과대 보고한다.
func TestAttachDRAAllocation_PoolNameDiffersFromNode(t *testing.T) {
	slices := []resourcev1.ResourceSlice{draSliceWithPool("k8s-worker1", "example.com/gpu", "pool-a", 4)}
	claims := []resourcev1.ResourceClaim{allocatedClaimOn("pool-a", "example.com/gpu", "dev-0")}
	items := []AcceleratorView{{NodeName: "k8s-worker1"}}

	attachDRAAllocation(items, intent.BuildDRACapability(true, nil, slices), claims)

	if got := items[0].NodeAllocated["dra:example.com/gpu"]; got != 1 {
		t.Fatalf("nodeAllocated=%d (1 기대) — 풀 이름이 노드명과 다르면 할당을 못 센다", got)
	}
	if got := items[0].NodeFree["dra:example.com/gpu"]; got != 3 {
		t.Fatalf("nodeFree=%d (3 기대)", got)
	}
}

// 한 노드·드라이버가 풀 둘로 광고하면 두 풀의 할당을 합산해야 한다 — 마지막 풀만 조회하면
// 다른 풀의 할당을 놓쳐 여유를 과대 보고한다.
func TestAttachDRAAllocation_MultiplePoolsPerNodeDriver(t *testing.T) {
	slices := []resourcev1.ResourceSlice{
		draSliceWithPool("k8s-worker1", "example.com/gpu", "pool-a", 2),
		draSliceWithPool("k8s-worker1", "example.com/gpu", "pool-b", 2),
	}
	claims := []resourcev1.ResourceClaim{
		allocatedClaimOn("pool-a", "example.com/gpu", "dev-0"),
		allocatedClaimOn("pool-b", "example.com/gpu", "dev-1"),
	}
	items := []AcceleratorView{{NodeName: "k8s-worker1"}}

	attachDRAAllocation(items, intent.BuildDRACapability(true, nil, slices), claims)

	if got := items[0].NodeAdvertised["dra:example.com/gpu"]; got != 4 {
		t.Fatalf("nodeAdvertised=%d (4 기대)", got)
	}
	if got := items[0].NodeAllocated["dra:example.com/gpu"]; got != 2 {
		t.Fatalf("nodeAllocated=%d (2 기대) — 풀 하나만 세고 있다", got)
	}
	if got := items[0].NodeFree["dra:example.com/gpu"]; got != 2 {
		t.Fatalf("nodeFree=%d (2 기대)", got)
	}
}

// nodeWithAllocatable 은 status.allocatable 만 채운 노드다. attachVendorID 가
// NodeAdvertised 가 아니라 이 필드를 직접 읽으므로, 그 경로를 단위 시험에서 재현하는 전용
// 픽스처로 쓴다. 노드 이름은 이 파일에서 항상 "n1" 하나뿐이라(unparam) 파라미터로 두지 않고
// podWith·acppReady 와 같은 규율로 고정한다.
func nodeWithAllocatable(alloc map[string]string) corev1.Node {
	rl := corev1.ResourceList{}
	for k, v := range alloc {
		rl[corev1.ResourceName(k)] = resource.MustParse(v)
	}
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: corev1.NodeStatus{Allocatable: rl}}
}

func TestAttachVendorID_RngdIsFuriosaWithModel(t *testing.T) {
	// goconst: "furiosa" 리터럴이 이 패키지에 이미 여러 곳 있어(accelerators_test.go 등) 상수로 뺀다.
	const vendorFuriosa = "furiosa"
	node := nodeWithAllocatable(map[string]string{"furiosa.ai/rngd": "4"})
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", Vendor: "rngd", Model: "rngd",
		NodeAdvertised: map[string]int32{"furiosa.ai/rngd": 4}}}
	attachVendorID(items, []corev1.Node{node})
	if items[0].VendorID != vendorFuriosa || items[0].Model != "rngd" {
		t.Fatalf("vendorId=%q model=%q", items[0].VendorID, items[0].Model)
	}
}

func TestAttachVendorID_ResourceNamesKeepZeroValuedLegacyEntries(t *testing.T) {
	node := nodeWithAllocatable(map[string]string{"rebellions.ai/ATOM": "2", "rebellions/atom": "0", "nvidia.com/gpu": "1"})
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", Vendor: "rebellions",
		NodeAdvertised: map[string]int32{"rebellions.ai/ATOM": 2, "rebellions/atom": 0, "nvidia.com/gpu": 1}}}
	attachVendorID(items, []corev1.Node{node})
	want := []string{"rebellions.ai/ATOM", "rebellions/atom"}
	if items[0].VendorID != "rebellions" || !reflect.DeepEqual(items[0].ResourceNames, want) {
		t.Fatalf("vendorId=%q resourceNames=%v (want %v)", items[0].VendorID, items[0].ResourceNames, want)
	}
}

func TestAttachVendorID_UnknownVendorStaysEmpty(t *testing.T) {
	node := nodeWithAllocatable(map[string]string{"nvidia.com/gpu": "1"})
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", Vendor: ""}}
	attachVendorID(items, []corev1.Node{node})
	if items[0].VendorID != "" || items[0].ResourceNames != nil {
		t.Fatalf("벤더를 모르면 비워야 한다: %q %v", items[0].VendorID, items[0].ResourceNames)
	}
}

// 증명: 카탈로그 밖 옛 등록(rebellions/atom, 값 0)이 NodeAdvertised 에는 없어도 ResourceNames 에는 온다 —
// 화면이 잔재를 판단할 수 있다는 필드 주석의 약속을 실제 입력(노드 allocatable)으로 확인한다.
func TestAttachVendorID_LegacyNameAbsentFromAdvertisedStillListed(t *testing.T) {
	node := nodeWithAllocatable(map[string]string{"rebellions.ai/ATOM": "2", "rebellions/atom": "0", "nvidia.com/gpu": "1"})
	items := []AcceleratorView{{UID: "u1", NodeName: "n1", Vendor: "rebellions", NodeAdvertised: map[string]int32{"rebellions.ai/ATOM": 2}}}
	attachVendorID(items, []corev1.Node{node})
	want := []string{"rebellions.ai/ATOM", "rebellions/atom"}
	if !reflect.DeepEqual(items[0].ResourceNames, want) {
		t.Fatalf("resourceNames=%v want %v", items[0].ResourceNames, want)
	}
}

// TestAttachVendorID_ThroughBuildInventory_CatalogFilteredNameStillListed: loadInventory 와
// 같은 경로(BuildInventory → attachVendorID)를 실제로 돌려 intent.BuildSnapshot 의 fail-closed
// 필터(카탈로그 밖 이름은 NodeAdvertised 에 올리지 않음, snapshot.go)를 재확인한다. rebellions/atom
// 은 그 필터로 NodeAdvertised 에선 빠지지만, attachVendorID 가 노드 allocatable 을 직접 보므로
// ResourceNames 에는 여전히 잡혀야 한다(TestBuildInventory_UnmanagedNodeUsesSyntheticID 의 노드·NDR
// 픽스처 패턴을 재사용, 벤더만 rebellions 로 바꿈 — furiosa 로는 접두 판정이 걸리지 않는다).
func TestAttachVendorID_ThroughBuildInventory_CatalogFilteredNameStillListed(t *testing.T) {
	nodes := []corev1.Node{node("rngd-1", false, map[string]string{"rebellions.ai/ATOM": "2", "rebellions/atom": "0"})}
	ndrs := []v1alpha1.NodeDeviceReport{ndr("rngd-1",
		v1alpha1.DeviceEntry{Vendor: "rebellions", Model: "ATOM", Count: 2})}

	items := BuildInventory(nodes, nil, ndrs, intent.DRACapability{})
	attachVendorID(items, nodes)

	for i := range items {
		if _, ok := items[i].NodeAdvertised["rebellions/atom"]; ok {
			t.Fatalf("카탈로그 밖 이름은 BuildSnapshot 필터로 NodeAdvertised 에 없어야 함: %v", items[i].NodeAdvertised)
		}
		found := false
		for _, n := range items[i].ResourceNames {
			if n == "rebellions/atom" {
				found = true
			}
		}
		if !found {
			t.Fatalf("노드 allocatable 에 있는 옛 등록은 ResourceNames 에는 있어야 함: %v", items[i].ResourceNames)
		}
	}
}
