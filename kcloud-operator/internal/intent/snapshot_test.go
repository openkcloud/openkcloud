// ============================================================
// snapshot_test.go: capability 스냅샷 테스트
// 상세: 가속기 없는 노드 제외, ACPP 저널 반영, stale(fail closed) 판정, fake client Load.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/health"
)

func node(name string, alloc map[string]string) corev1.Node {
	rl := corev1.ResourceList{}
	for k, v := range alloc {
		rl[corev1.ResourceName(k)] = resource.MustParse(v)
	}
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NodeStatus{Allocatable: rl}}
}

func TestBuildSnapshotSkipsNodesWithoutAccelerators(t *testing.T) {
	snap := BuildSnapshot([]corev1.Node{
		node("cpu-only", map[string]string{"cpu": "8", "memory": "16Gi"}),
		node("worker1", map[string]string{"cpu": "8", "nvidia.com/gpu": "2"}),
	}, nil, nil, DRACapability{})
	if len(snap) != 1 || snap[0].NodeName != "worker1" {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	if snap[0].Vendor != "nvidia" || snap[0].Advertised["nvidia.com/gpu"] != 2 {
		t.Fatalf("vendor/advertised wrong: %+v", snap[0])
	}
	// ACPP 가 없으면 아무것도 적용되지 않은 것이다.
	if snap[0].SharingMode != v1alpha1.SharingModeExclusive || snap[0].Stale {
		t.Fatalf("default sharing/stale wrong: %+v", snap[0])
	}
}

// 재현, ACPP 선택 결정성)를 검증하는 테스트 헬퍼라 항상 그렇다. 다른 노드를 쓰는 테스트가 생기면
// 자연히 값이 달라진다.
//
//nolint:unparam // nodeName 은 모든 호출에서 "worker1" 이다 — 단일 노드 시나리오(과거 apply 저널
func readyACPP(nodeName string, gen int64, devices []v1alpha1.DeviceStatus, profiles []string, sharingMode string, replicas int32) v1alpha1.AcceleratorPartitionPolicy {
	resolved := make([]v1alpha1.ResolvedLayoutEntry, 0, len(profiles))
	for _, p := range profiles {
		resolved = append(resolved, v1alpha1.ResolvedLayoutEntry{Profile: p})
	}
	return v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-" + nodeName, Generation: gen},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: gen,
			Targets: []v1alpha1.TargetStatus{{
				NodeName: nodeName, Phase: v1alpha1.ACPPPhaseReady,
				Devices: devices, ResolvedLayout: resolved,
			}},
			ApplyRecords: []v1alpha1.ApplyRecord{{NodeName: nodeName, SharingMode: sharingMode, SharingReplicas: replicas}},
		},
	}
}

// ndr 은 노드의 물리 장치 관측이다(vendor 별 집계 행). nodeName 을 넘기는 것은 이 값이
// Spec.NodeName 과 맞아야 조회된다는 사실을 호출부에서 보이게 하기 위해서다(readyACPP 와 동일).
//
//nolint:unparam // nodeName 은 오늘 모든 호출에서 "worker1" 이다 — 단일 노드 시나리오라 그렇다.
func ndr(nodeName, vendor string, count int32) v1alpha1.NodeDeviceReport {
	return v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "ndr-" + nodeName},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: nodeName},
		Status:     v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{{Vendor: vendor, Count: count}}},
	}
}

// cordon 된 노드는 후보가 아니다 — allocatable 은 그대로지만 스케줄러가 Pod 을 올리지 않는다.
func TestBuildSnapshotSkipsCordonedNodes(t *testing.T) {
	cordoned := node("worker1", map[string]string{"nvidia.com/gpu": "2"})
	cordoned.Spec.Unschedulable = true
	snap := BuildSnapshot([]corev1.Node{cordoned, node("worker2", map[string]string{"nvidia.com/gpu": "1"})}, nil, nil, DRACapability{})
	if len(snap) != 1 || snap[0].NodeName != "worker2" {
		t.Fatalf("cordoned node still a candidate: %+v", snap)
	}
}

// 저널이 exclusive 라고 해도 광고 수가 물리 장치 수보다 많으면 광고가 초과 상태다. 원인(복제냐
// 하드웨어 서브유닛이냐)은 이 관측만으로 알 수 없으므로 timeSliced 로 단정하지 않는다.
// 손으로 고친 sharing ConfigMap 등 ACPP 밖에서 켜진 초과 광고를 여기서 잡는다.
func TestBuildSnapshotDetectsOutOfBandSharing(t *testing.T) {
	acpp := readyACPP("worker1", 1, []v1alpha1.DeviceStatus{{ID: "gpu0"}}, nil, v1alpha1.SharingModeExclusive, 0)
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "8"})},
		[]v1alpha1.AcceleratorPartitionPolicy{acpp}, []v1alpha1.NodeDeviceReport{ndr("worker1", "nvidia", 2)}, DRACapability{})
	if snap[0].SharingMode != v1alpha1.SharingModeOversubscribed || snap[0].SharingReplicas != 4 {
		t.Fatalf("out-of-band oversubscription not detected: %+v", snap[0])
	}
	// 그 결과 exclusive 는 여전히 거절된다(fail-closed) — 다만 메커니즘은 단정하지 않는다.
	rj := CheckMode(snap[0], v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, v1alpha1.AcceleratorMapping{Vendor: "nvidia"})
	if rj == nil {
		t.Fatalf("exclusive admitted onto an oversubscribed device: %+v", rj)
	}
	if strings.Contains(rj.Message, "replica") {
		t.Fatalf("rejection must not assert replication as the mechanism: %q", rj.Message)
	}
}

// 광고가 무너진 노드에는 새 워크로드를 배치하지 않는다 — Degraded 는 Ready 가 아니므로
// 기존 fail-closed 규칙이 그대로 적용된다. 이 동작은 의도된 것이며 여기서 고정한다.
func TestDegradedTargetMarksNodeStale(t *testing.T) {
	acpp := v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Generation: 2},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 2,
			Targets: []v1alpha1.TargetStatus{{
				NodeName: "worker1", Phase: v1alpha1.ACPPPhaseDegraded,
			}},
		},
	}
	nc := &NodeCapability{NodeName: "worker1"}
	applyACPPStatus(nc, []v1alpha1.AcceleratorPartitionPolicy{acpp})
	if !nc.Stale {
		t.Fatalf("Degraded 노드가 stale 로 표시되지 않았다")
	}
	if !strings.Contains(nc.StaleReason, v1alpha1.ACPPPhaseDegraded) {
		t.Fatalf("stale 사유에 phase 가 없다: %q", nc.StaleReason)
	}
}

// RNGD 재현: 물리 RNGD 1대(NDR count=1)가 하드웨어 PE 4개를 furiosa.ai/rngd=4 로 광고한다. 복제가
// 아니라 하드웨어 서브유닛이므로 timeSliced 로 단정하면 안 되고, 원인 미확정 상태(oversubscribed)로만
// 표시해야 한다. BuildSnapshot 이 실제로 만드는 입력 모양 그대로 짠다(필드를 손으로 꽂지 않는다).
func TestBuildSnapshotRNGDPEsAreOversubscribedNotTimeSliced(t *testing.T) {
	n := node("rngd-1", map[string]string{"furiosa.ai/rngd": "4"})
	snap := BuildSnapshot([]corev1.Node{n}, nil, []v1alpha1.NodeDeviceReport{ndr("rngd-1", "furiosa", 1)}, DRACapability{})
	if len(snap) != 1 {
		t.Fatalf("snapshot %+v", snap)
	}
	nc := snap[0]
	if nc.SharingMode != v1alpha1.SharingModeOversubscribed {
		t.Fatalf("RNGD PE partitioning misreported as %q, want %q (cause is undetermined, not replication)",
			nc.SharingMode, v1alpha1.SharingModeOversubscribed)
	}
	if nc.SharingReplicas != 4 {
		t.Fatalf("ratio lost: %+v", nc)
	}
}

// 나눠떨어지지 않는 비율은 배수를 특정할 수 없다 — 내림한 값을 발표하면 그 값과 일치하는
// shared 요청이 통과한다. 미상(0)으로 두어 exclusive 도 shared 도 모두 거절되게 한다.
func TestBuildSnapshotSharingRatioNotAMultiple(t *testing.T) {
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "3"})},
		nil, []v1alpha1.NodeDeviceReport{ndr("worker1", "nvidia", 2)}, DRACapability{})
	nc := snap[0]
	if nc.SharingMode != v1alpha1.SharingModeOversubscribed || nc.SharingReplicas != 0 {
		t.Fatalf("floored replica factor published: %+v", nc)
	}
	// 3/2 를 내림한 1 도, 어떤 값도 통과시키지 않는다.
	for _, replicas := range []int32{2, 3} {
		rj := CheckMode(nc, v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: replicas},
			v1alpha1.AcceleratorMapping{Vendor: "nvidia"})
		if rj == nil {
			t.Fatalf("shared replicas=%d admitted on an unknown sharing factor", replicas)
		}
	}
}

// 광고 수 == 물리 장치 수면 공유가 아니다. NDR 관측이 없으면(0) 비교 근거가 없으므로 저널을 그대로 둔다.
func TestBuildSnapshotObservedSharingStaysQuietWithoutEvidence(t *testing.T) {
	nodes := []corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "2"})}
	for name, reports := range map[string][]v1alpha1.NodeDeviceReport{
		"equal count": {ndr("worker1", "nvidia", 2)},
		"no NDR":      nil,
	} {
		snap := BuildSnapshot(nodes, nil, reports, DRACapability{})
		if snap[0].SharingMode != v1alpha1.SharingModeExclusive || snap[0].SharingReplicas != 0 {
			t.Fatalf("%s: sharing invented: %+v", name, snap[0])
		}
	}
	// 파티션 노드는 리소스명이 조각을 가리키므로 물리 장치 수와 비교하지 않는다.
	acpp := readyACPP("worker1", 1, nil, []string{"1g.6gb"}, v1alpha1.SharingModeExclusive, 0)
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/mig-1g.6gb": "7"})},
		[]v1alpha1.AcceleratorPartitionPolicy{acpp}, []v1alpha1.NodeDeviceReport{ndr("worker1", "nvidia", 1)}, DRACapability{})
	if snap[0].SharingMode != v1alpha1.SharingModeExclusive {
		t.Fatalf("partitioned node misread as shared: %+v", snap[0])
	}
}

func TestBuildSnapshotReadsAppliedJournal(t *testing.T) {
	devs := []v1alpha1.DeviceStatus{{ID: "PCI-0000:3b:00.0"}}
	acpp := readyACPP("worker1", 3, devs, []string{"1g.6gb"}, v1alpha1.SharingModeTimeSliced, 4)
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/mig-1g.6gb": "4"})},
		[]v1alpha1.AcceleratorPartitionPolicy{acpp}, nil, DRACapability{})
	if len(snap) != 1 {
		t.Fatalf("snapshot %+v", snap)
	}
	nc := snap[0]
	if nc.SharingMode != v1alpha1.SharingModeTimeSliced || nc.SharingReplicas != 4 {
		t.Fatalf("journal not read: %+v", nc)
	}
	if len(nc.PartitionProfiles) != 1 || nc.PartitionProfiles[0] != "1g.6gb" {
		t.Fatalf("profiles %v", nc.PartitionProfiles)
	}
	if len(nc.Devices) != 1 || nc.Devices[0].ID != "PCI-0000:3b:00.0" {
		t.Fatalf("devices %+v", nc.Devices)
	}
}

func TestBuildSnapshotFailsClosedOnStaleACPP(t *testing.T) {
	stale := readyACPP("worker1", 5, nil, nil, "", 0)
	stale.Status.ObservedGeneration = 4 // 아직 수렴하지 않음
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "1"})},
		[]v1alpha1.AcceleratorPartitionPolicy{stale}, nil, DRACapability{})
	if !snap[0].Stale || snap[0].StaleReason == "" {
		t.Fatalf("stale generation not detected: %+v", snap[0])
	}

	notReady := readyACPP("worker1", 5, nil, nil, "", 0)
	notReady.Status.Targets[0].Phase = v1alpha1.ACPPPhaseApplying
	snap = BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "1"})},
		[]v1alpha1.AcceleratorPartitionPolicy{notReady}, nil, DRACapability{})
	if !snap[0].Stale {
		t.Fatalf("non-Ready phase not detected: %+v", snap[0])
	}
}

func TestBuildSnapshotTakesSmallestDeviceMemory(t *testing.T) {
	ndr := v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "worker1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "nvidia", MemoryMiB: 24576},
			{Vendor: "nvidia", MemoryMiB: 15360},
			{Vendor: "nvidia", MemoryMiB: 0}, // 미관측은 무시
		}},
	}
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "2"})}, nil,
		[]v1alpha1.NodeDeviceReport{ndr}, DRACapability{})
	if snap[0].MemoryMiB != 15360 {
		t.Fatalf("MemoryMiB=%d want 15360", snap[0].MemoryMiB)
	}
}

// TestBuildSnapshotVendorIsDeterministicOnMultiVendorNode 는 회귀 방지용이다: 한 노드가 NVIDIA 와
// Furiosa 리소스를 동시에 광고하면(이 클러스터의 실제 worker1 처럼), 반복 실행해도 항상 같은
// Vendor(리소스명 사전순으로 가장 앞선 쪽)가 나와야 한다.
func TestBuildSnapshotVendorIsDeterministicOnMultiVendorNode(t *testing.T) {
	n := node("worker1", map[string]string{"nvidia.com/gpu": "2", "furiosa.ai/rngd": "1"})
	var want string
	for i := 0; i < 20; i++ {
		snap := BuildSnapshot([]corev1.Node{n}, nil, nil, DRACapability{})
		if len(snap) != 1 {
			t.Fatalf("snapshot %+v", snap)
		}
		if want == "" {
			want = snap[0].Vendor
		} else if snap[0].Vendor != want {
			t.Fatalf("nondeterministic Vendor: got %q, want %q (run %d)", snap[0].Vendor, want, i)
		}
	}
	// "furiosa.ai/rngd" < "nvidia.com/gpu" 사전순 — furiosa 가 이겨야 한다.
	if want != "furiosa" {
		t.Fatalf("Vendor = %q, want %q (sorted-first resource)", want, "furiosa")
	}
}

// TestBuildSnapshotACPPSelectionIsDeterministic 는 회귀 방지용이다: 같은 노드를 타깃하는 ACPP 가
// 둘 있으면(전제 위반이지만 방어적으로), 입력 순서와 무관하게 항상 이름 사전순으로 앞선 쪽("acpp-a")이 이겨야 한다.
func TestBuildSnapshotACPPSelectionIsDeterministic(t *testing.T) {
	acppA := readyACPP("worker1", 1, nil, nil, v1alpha1.SharingModeTimeSliced, 2)
	acppA.Name = "acpp-a"
	acppB := readyACPP("worker1", 1, nil, nil, v1alpha1.SharingModeTimeSliced, 9)
	acppB.Name = "acpp-b"

	forward := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "1"})},
		[]v1alpha1.AcceleratorPartitionPolicy{acppA, acppB}, nil, DRACapability{})
	backward := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "1"})},
		[]v1alpha1.AcceleratorPartitionPolicy{acppB, acppA}, nil, DRACapability{})

	if forward[0].SharingReplicas != 2 || backward[0].SharingReplicas != 2 {
		t.Fatalf("expected acpp-a (replicas=2) to win regardless of input order: forward=%+v backward=%+v",
			forward[0], backward[0])
	}
}

// TestBuildSnapshotFailedSiblingDoesNotHideReadyPolicy 는 회귀 방지용이다: 같은 노드를 타깃하는 ACPP
// 둘 중 이름 사전순 앞("worker1-a2-timeslice", "a2" < "a30")이 Failed 여도 뒤의 Ready 정책
// ("worker1-a30-4x1g")의 관측이 채택되고 노드는 stale 이 아니어야 한다 — 첫 매치만 보고 즉시
// 반환하면 Ready 정책의 장치·프로파일이 가려진다.
func TestBuildSnapshotFailedSiblingDoesNotHideReadyPolicy(t *testing.T) {
	failed := readyACPP("worker1", 1, nil, nil, "", 0)
	failed.Name = "worker1-a2-timeslice"
	failed.Status.Targets[0].Phase = v1alpha1.ACPPPhaseFailed
	ready := readyACPP("worker1", 1, []v1alpha1.DeviceStatus{{ID: "PCI-0000:18:00.0"}}, []string{"1g.6gb"}, "", 0)
	ready.Name = "worker1-a30-4x1g"

	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "1", "nvidia.com/mig-1g.6gb": "4"})},
		[]v1alpha1.AcceleratorPartitionPolicy{failed, ready}, nil, DRACapability{})
	if len(snap) != 1 {
		t.Fatalf("snapshot %+v", snap)
	}
	if snap[0].Stale {
		t.Fatalf("Ready 정책이 있는데 Failed 형제 정책이 노드를 stale 로 만들었다: %+v", snap[0])
	}
	if len(snap[0].Devices) != 1 || snap[0].Devices[0].ID != "PCI-0000:18:00.0" {
		t.Fatalf("Ready 정책의 장치 관측이 채택돼야 한다: %+v", snap[0].Devices)
	}
	if len(snap[0].PartitionProfiles) != 1 || snap[0].PartitionProfiles[0] != "1g.6gb" {
		t.Fatalf("Ready 정책의 profile 이 채택돼야 한다: %v", snap[0].PartitionProfiles)
	}
}

// Ready 정책이 하나도 없으면 기존 fail-closed 그대로 stale 이어야 한다(사유는 사전순 첫 정책).
func TestBuildSnapshotAllNonReadySiblingsStillStale(t *testing.T) {
	a := readyACPP("worker1", 1, nil, nil, "", 0)
	a.Name = "acpp-a"
	a.Status.Targets[0].Phase = v1alpha1.ACPPPhaseFailed
	b := readyACPP("worker1", 1, nil, nil, "", 0)
	b.Name = "acpp-b"
	b.Status.Targets[0].Phase = v1alpha1.ACPPPhaseApplying

	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "1"})},
		[]v1alpha1.AcceleratorPartitionPolicy{b, a}, nil, DRACapability{})
	if !snap[0].Stale || !strings.Contains(snap[0].StaleReason, "acpp-a") {
		t.Fatalf("Ready 부재 시 stale + 사전순 첫 정책 사유여야 한다: %+v", snap[0])
	}
}

// DRA 만 광고하는 노드가 스냅샷에서 사라지면 안 된다.
func TestBuildSnapshotKeepsDRAOnlyNode(t *testing.T) {
	nodes := []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "dra-only"}}}
	dra := DRACapability{APIServed: true, SlicesByNodeDriver: map[string]map[string]int32{
		"dra-only": {"gpu.nvidia.com": 4},
	}}
	snap := BuildSnapshot(nodes, nil, nil, dra)
	if len(snap) != 1 {
		t.Fatalf("DRA-only node dropped from snapshot: %+v", snap)
	}
	if snap[0].DRADevices["gpu.nvidia.com"] != 4 {
		t.Fatalf("DRADevices = %+v", snap[0].DRADevices)
	}
}

func TestLoadReadsClusterObjects(t *testing.T) {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1 scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("v1alpha1 scheme: %v", err)
	}
	n := node("worker1", map[string]string{"nvidia.com/gpu": "1"})
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(&n).Build()
	snap, _, err := Load(context.Background(), c)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(snap) != 1 || snap[0].NodeName != "worker1" {
		t.Fatalf("snapshot %+v", snap)
	}
}

// TestApplyHealthDropsBlockedNode 는 할당이 막힌 노드가 배치 후보에서 빠지는지 본다.
// 이 축이 없으면 health 는 상태만 예쁘게 적고 워크로드는 그대로 그 노드로 간다.
func TestApplyHealthDropsBlockedNode(t *testing.T) {
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "2"})}, nil, nil, DRACapability{})
	if len(snap) != 1 || snap[0].Stale {
		t.Fatalf("전제가 틀렸다 — 정상 노드가 이미 배제돼 있다: %+v", snap)
	}
	got := ApplyHealth(snap, []v1alpha1.AcceleratorHealth{{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Status: v1alpha1.AcceleratorHealthStatus{
			State: "Quarantined", AllocationAllowed: false, Reason: "RepeatedRecoveryFailure",
		},
	}})
	if !got[0].Stale {
		t.Fatal("격리 노드가 후보로 남았다")
	}
	if got[0].StaleReason == "" {
		t.Fatal("배제 사유가 비었다 — 운영자가 왜 배치가 안 되는지 알 수 없다")
	}
}

// TestApplyHealthKeepsHealthyNode 는 정상 노드가 영향을 안 받는지 본다(회귀 방지).
func TestApplyHealthKeepsHealthyNode(t *testing.T) {
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "2"})}, nil, nil, DRACapability{})
	got := ApplyHealth(snap, []v1alpha1.AcceleratorHealth{{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Status:     v1alpha1.AcceleratorHealthStatus{State: "Healthy", AllocationAllowed: true},
	}})
	if got[0].Stale {
		t.Fatalf("정상 노드가 배제됐다: %s", got[0].StaleReason)
	}
}

// TestApplyHealthIgnoresUnjudgedNode 는 아직 판정 전인 노드를 막지 않는지 본다.
func TestApplyHealthIgnoresUnjudgedNode(t *testing.T) {
	snap := BuildSnapshot([]corev1.Node{node("worker1", map[string]string{"nvidia.com/gpu": "2"})}, nil, nil, DRACapability{})
	got := ApplyHealth(snap, []v1alpha1.AcceleratorHealth{{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
	}})
	if got[0].Stale {
		t.Fatal("판정 전 노드를 막았다 — 감시 도입이 곧 전면 차단이 된다")
	}
}

// TestApplyHealthRemovesOnlyTheUnhealthyDevice 는 고장난 장치만 후보에서 빠지고
// 노드 자체는 남는지 본다 — 장치 하나 때문에 노드 전체를 버리면 용량이 낭비된다.
// 깨는 뮤테이션: 장치 필터를 지우면 두 장치가 다 남아 실패한다.
func TestApplyHealthRemovesOnlyTheUnhealthyDevice(t *testing.T) {
	snap := []NodeCapability{{
		NodeName: "n1",
		Devices: []v1alpha1.DeviceStatus{
			{PCIAddress: "0000:18:00.0"},
			{PCIAddress: "0000:86:00.0"},
		},
	}}
	// State/AllocationAllowed 는 손으로 지어낸 조합이 아니라 실제 health.Evaluate() 가 장치
	// 하나만 고장났을 때 실제로 내는 값이다(장치 하나가 살아 있으면 Degraded+allow=true —
	// 노드를 통째로 버리지 않는다. internal/health/evaluate.go 의 장치축 fold 참고).
	healths := []v1alpha1.AcceleratorHealth{{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: v1alpha1.AcceleratorHealthStatus{
			State: "Degraded", Reason: "DriverNotLoaded", AllocationAllowed: true,
			Devices: []v1alpha1.DeviceHealth{
				{PCIAddress: "0000:18:00.0", State: "Healthy"},
				{PCIAddress: "0000:86:00.0", State: "Unhealthy", Reason: "DriverNotLoaded"},
			},
		},
	}}
	got := ApplyHealth(snap, healths)
	if got[0].Stale {
		t.Fatalf("장치 하나 때문에 노드 전체를 버렸다: %+v", got[0])
	}
	if len(got[0].Devices) != 1 || got[0].Devices[0].PCIAddress != "0000:18:00.0" {
		t.Fatalf("고장난 장치가 후보에 남았다: %+v", got[0].Devices)
	}
}

// TestApplyHealthKeepsUnknownDevices 는 관측 못 한 장치를 성급히 빼지 않는지 본다.
// 빼 버리면 관측이 잠깐 끊긴 순간마다 용량이 출렁인다 — 노드 축(AllocationAllowed)이
// 이미 그 구간을 막고 있다.
func TestApplyHealthKeepsUnknownDevices(t *testing.T) {
	snap := []NodeCapability{{NodeName: "n1",
		Devices: []v1alpha1.DeviceStatus{{PCIAddress: "0000:18:00.0"}}}}
	healths := []v1alpha1.AcceleratorHealth{{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: v1alpha1.AcceleratorHealthStatus{
			State: "Healthy", AllocationAllowed: true,
			Devices: []v1alpha1.DeviceHealth{{PCIAddress: "0000:18:00.0", State: "Unknown"}},
		},
	}}
	got := ApplyHealth(snap, healths)
	if len(got[0].Devices) != 1 {
		t.Fatalf("관측 못 한 장치를 뺐다: %+v", got[0].Devices)
	}
}

// TestApplyHealthAcceptsRealEvaluateOutput 는 internal/health 와 internal/intent 를 실제로 이어
// 본다 — 단위 시험은 각자 통과해도 조합에서 어긋날 수 있다(fold 가 노드를 통째로 막으면 이 아래
// 장치 필터는 존재해도 절대 실행되지 않는다). health.Evaluate() 가 낸 진짜 Result 를 그대로 옮겨
// ApplyHealth 에 먹인다.
// 깨는 뮤테이션: evaluate.go 의 fold 를 "장치 하나만 고장나도 allow=false" 로 되돌리면
// 노드가 Stale 로 막혀 장치 필터에 닿지도 못하고 실패한다.
func TestApplyHealthAcceptsRealEvaluateOutput(t *testing.T) {
	now := time.Now()
	seen := now.Add(-10 * time.Second)
	res := health.Evaluate(health.Inputs{
		NodeReady: true, NDRObservedAt: &seen, DriverLoaded: true,
		DevicePluginReady: true,
		Devices: []health.DeviceInput{
			{PCI: "0000:18:00.0", DriverLoaded: true},
			{PCI: "0000:86:00.0", DriverLoaded: false},
		},
		Now: now,
	}, health.DefaultPolicy())

	devs := make([]v1alpha1.DeviceHealth, len(res.Devices))
	for i, d := range res.Devices {
		devs[i] = v1alpha1.DeviceHealth{PCIAddress: d.PCI, State: d.State, Reason: d.Reason}
	}
	healths := []v1alpha1.AcceleratorHealth{{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: v1alpha1.AcceleratorHealthStatus{
			State: res.State, Reason: res.Reason, AllocationAllowed: res.AllocationAllowed, Devices: devs,
		},
	}}
	snap := []NodeCapability{{
		NodeName: "n1",
		Devices: []v1alpha1.DeviceStatus{
			{PCIAddress: "0000:18:00.0"},
			{PCIAddress: "0000:86:00.0"},
		},
	}}

	got := ApplyHealth(snap, healths)
	if got[0].Stale {
		t.Fatalf("장치 하나만 고장났는데 노드 전체가 막혔다: state=%s allow=%v", res.State, res.AllocationAllowed)
	}
	if len(got[0].Devices) != 1 || got[0].Devices[0].PCIAddress != "0000:18:00.0" {
		t.Fatalf("고장난 장치가 후보에 남았다: %+v", got[0].Devices)
	}
}
