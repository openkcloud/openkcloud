// ============================================================
// translate_test.go: 추상 의도 → 벤더 리소스 번역 테스트
// 상세: 벤더 우선순위, DRA 거절, 축을 지목하는 오류 전파, 노드 후보 확정.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"kcloud-operator/api/v1alpha1"
)

const nodeK8sWorker1 = "k8s-worker1"

const (
	vendorNvidia  = "nvidia"
	vendorFuriosa = "furiosa"
	nodeWorker1   = "worker1"
	nodeRngd1     = "rngd-1"
	resRngd       = "furiosa.ai/rngd"
)

func classFor(mappings ...v1alpha1.AcceleratorMapping) *v1alpha1.AcceleratorClass {
	c := &v1alpha1.AcceleratorClass{}
	c.Name = "inference-medium"
	c.Spec.Mappings = mappings
	return c
}

func plainNvidiaNode(name string) NodeCapability {
	return NodeCapability{NodeName: name, Vendor: vendorNvidia, Advertised: map[string]int32{"nvidia.com/gpu": 1},
		SharingMode: v1alpha1.SharingModeExclusive}
}

func plainRngdNode(name string) NodeCapability {
	return NodeCapability{NodeName: name, Vendor: vendorFuriosa, Advertised: map[string]int32{resRngd: 1},
		SharingMode: v1alpha1.SharingModeExclusive}
}

func TestTranslateExclusive(t *testing.T) {
	req := Request{ClassName: "inference-medium", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	res, err := Translate(req, []NodeCapability{plainNvidiaNode("worker2"), plainNvidiaNode(nodeWorker1)})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if res.ResourceName != "nvidia.com/gpu" || res.Quantity != 1 || res.AllocationAPI != v1alpha1.AllocationAPIDevicePlugin {
		t.Fatalf("unexpected result %+v", res)
	}
	if len(res.Nodes) != 2 || res.Nodes[0] != nodeWorker1 || res.Nodes[1] != "worker2" {
		t.Fatalf("candidate nodes must be sorted: %v", res.Nodes)
	}
}

// 선호 벤더가 먼저 시도되고, 후보 노드가 생기는 첫 벤더가 이긴다.
func TestTranslateVendorPreferenceWins(t *testing.T) {
	mappings := []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}, rngdMapping()}
	snap := []NodeCapability{plainNvidiaNode(nodeWorker1), plainRngdNode(nodeRngd1)}

	req := Request{ClassName: "c", Mappings: mappings, Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		AllocationAPI: v1alpha1.AllocationAPIAuto}
	res, err := Translate(req, snap)
	if err != nil || res.Vendor != vendorNvidia {
		t.Fatalf("mapping order must decide by default: %+v %v", res, err)
	}

	req.VendorPreference = []string{vendorFuriosa}
	res, err = Translate(req, snap)
	if err != nil || res.Vendor != vendorFuriosa || res.ResourceName != resRngd {
		t.Fatalf("preference ignored: %+v %v", res, err)
	}

	// 선호에 없는 벤더도 후보로 남는다(선호는 요구가 아니다).
	req.VendorPreference = []string{"rebellions"}
	res, err = Translate(req, snap)
	if err != nil || res.Vendor != vendorNvidia {
		t.Fatalf("unmapped preference must fall through to the class order: %+v %v", res, err)
	}
}

// 매핑된 벤더가 노드에 없으면 다음 벤더로 넘어간다(선호가 아니라 후보 유무가 결정한다).
func TestTranslateFallsThroughToVendorThatHasNodes(t *testing.T) {
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}, rngdMapping()},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	res, err := Translate(req, []NodeCapability{plainRngdNode(nodeRngd1)})
	if err != nil || res.Vendor != vendorFuriosa || res.ResourceName != resRngd {
		t.Fatalf("second mapping must win when the first has no node: %+v %v", res, err)
	}
}

func TestTranslateRejectsDRA(t *testing.T) {
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIDRA}
	_, err := Translate(req, []NodeCapability{plainNvidiaNode(nodeWorker1)})
	var rj *Reject
	if !errors.As(err, &rj) {
		t.Fatalf("expected *Reject, got %v", err)
	}
	if rj.Axis != AxisAllocationAPI || rj.Reason != v1alpha1.AWReasonDRANotEnabled {
		t.Fatalf("unexpected reject %+v", rj)
	}

	// dra 사유는 dra 에만 붙는다 — 모르는 값까지 "DRA 가 꺼져 있다" 로 설명하지 않는다.
	req.AllocationAPI = "acme-api"
	_, err = Translate(req, []NodeCapability{plainNvidiaNode(nodeWorker1)})
	if !errors.As(err, &rj) || rj.Axis != AxisAllocationAPI || rj.Reason != v1alpha1.AWReasonBackendUnsupported {
		t.Fatalf("unknown allocationAPI must not borrow the dra reason: %v", err)
	}
}

// 후보가 없을 때는 "노드가 없다" 가 아니라 실패한 축이 나와야 한다.
func TestTranslatePropagatesTheAxisThatFailed(t *testing.T) {
	shared := NodeCapability{NodeName: nodeRngd1, Vendor: vendorFuriosa, Advertised: map[string]int32{resRngd: 1},
		Devices: []v1alpha1.DeviceStatus{rngdDevice()}, SharingMode: v1alpha1.SharingModeExclusive}
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{rngdMapping()},
		Access:        v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Implementation: v1alpha1.ImplementationAuto, Replicas: 2},
		AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, []NodeCapability{shared})
	var rj *Reject
	if !errors.As(err, &rj) {
		t.Fatalf("expected *Reject, got %v", err)
	}
	if rj.Axis != AxisMode || rj.Reason != v1alpha1.AWReasonBackendUnsupported {
		t.Fatalf("axis lost: %+v", rj)
	}
}

// requirements 게이트도 반드시 돈다 — mode 통과가 승인이 아니다.
func TestTranslateRunsTheRequirementsGateToo(t *testing.T) {
	nc := plainNvidiaNode(nodeWorker1)
	nc.Devices = []v1alpha1.DeviceStatus{{ID: "GPU-0",
		IsolationCapability: v1alpha1.IsolationCapability{Compute: v1alpha1.IsolationProcess, Memory: v1alpha1.IsolationProcess}}}
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}},
		Access:        v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		Requirements:  v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationHardware},
		AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, []NodeCapability{nc})
	var rj *Reject
	if !errors.As(err, &rj) || rj.Axis != AxisIsolation || rj.Reason != v1alpha1.AWReasonIsolationTooWeak {
		t.Fatalf("requirements gate skipped: %v", err)
	}
}

// 모호한 furiosa product 는 조용히 아무 제품이나 고르지 않고 그 축을 지목해 거절한다.
func TestTranslateRejectsAmbiguousProduct(t *testing.T) {
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorFuriosa}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, []NodeCapability{plainRngdNode(nodeRngd1)})
	var rj *Reject
	if !errors.As(err, &rj) || rj.Axis != AxisProduct || rj.Reason != v1alpha1.AWReasonNoVendorMapping {
		t.Fatalf("ambiguous product must name its axis: %v", err)
	}
}

// 카탈로그에 없는 벤더는 "노드가 없다" 가 아니라 클래스를 지목해야 한다.
func TestTranslateUnknownVendorNamesTheClass(t *testing.T) {
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: "acme"}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, []NodeCapability{plainNvidiaNode(nodeWorker1)})
	var rj *Reject
	if !errors.As(err, &rj) || rj.Axis != AxisProduct || rj.Reason != v1alpha1.AWReasonNoVendorMapping {
		t.Fatalf("unknown vendor must point at the class: %v", err)
	}
}

// 같은 벤더가 두 번 매핑되면 선택이 비결정적이 된다 — webhook 이 이미 이걸 막지만
// failurePolicy=Ignore 라 webhook 이 죽어도 Translate 스스로 걸러야 한다.
func TestTranslateRejectsDuplicateVendor(t *testing.T) {
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}, {Vendor: "NVIDIA"}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, []NodeCapability{plainNvidiaNode(nodeWorker1)})
	var rj *Reject
	if !errors.As(err, &rj) || rj.Axis != AxisCandidates || rj.Reason != v1alpha1.AWReasonNoVendorMapping {
		t.Fatalf("duplicate vendor must be rejected by axis/reason: %v", err)
	}
}

func TestTranslateNoMappingAtAll(t *testing.T) {
	req := Request{ClassName: "c", Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, nil)
	var rj *Reject
	if !errors.As(err, &rj) || rj.Axis != AxisCandidates || rj.Reason != v1alpha1.AWReasonNoVendorMapping {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestTranslateNoNodesAtAll(t *testing.T) {
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, nil)
	var rj *Reject
	if !errors.As(err, &rj) || rj.Axis != AxisCandidates || rj.Reason != v1alpha1.AWReasonNoCandidateNodes {
		t.Fatalf("unexpected error %v", err)
	}
}

// 공유 결과 설명은 replica 사이에 격리가 없다는 사실을 반드시 담는다(정직성).
func TestTranslateSharedExplanationIsHonest(t *testing.T) {
	nc := NodeCapability{NodeName: nodeWorker1, Vendor: vendorNvidia, Advertised: map[string]int32{"nvidia.com/gpu": 8},
		Devices: []v1alpha1.DeviceStatus{verifiedTimeSlicing(16)}, SharingMode: v1alpha1.SharingModeTimeSliced, SharingReplicas: 4}
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	res, err := Translate(req, []NodeCapability{nc})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if !strings.Contains(res.Explanation, "not isolated") {
		t.Fatalf("explanation hides the lack of isolation: %q", res.Explanation)
	}
	// 시분할 replica 를 독립 장치로 부풀리지 않는다 — 사용자가 받는 것은 요청 1개다.
	if res.Quantity != 1 {
		t.Fatalf("quantity must be the request the user actually gets, got %d", res.Quantity)
	}
}

// 멀티벤더 노드에서도 편의 필드 Vendor 가 아니라 광고 목록으로 벤더를 판정한다.
func TestTranslateMultiVendorNode(t *testing.T) {
	nc := NodeCapability{NodeName: "mixed-1", Vendor: vendorFuriosa,
		Advertised:  map[string]int32{resRngd: 1, "nvidia.com/gpu": 2},
		SharingMode: v1alpha1.SharingModeExclusive}
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia}},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	res, err := Translate(req, []NodeCapability{nc})
	if err != nil || res.Vendor != vendorNvidia || len(res.Nodes) != 1 {
		t.Fatalf("multi-vendor node lost: %+v %v", res, err)
	}
}

// k8s-worker1(Warboy — 같은 furiosa 벤더지만 furiosa.ai/rngd 은 광고하지 않음)과 rngd-1(RNGD, 물리 1대인데
// 4개를 oversubscribed 로 광고해 CheckMode 가 exclusive 를 거절)이 함께 있을 때, exclusive RNGD 요청의
// 거절은 rngd-1 의 실제 사유여야 한다. 벤더만 보는 후보 필터는 worker1 도 들여보내 그 사소한
// "광고 안 함" 이 rngd-1 의 진짜 거절을 가린다. 두 순서 모두 확인한다 — BuildSnapshot 은 이름 사전순
// 정렬로 "k8s-worker1" 이 항상 먼저 오므로, 고침이 반복 순서가 아니라 후보 판정 자체를 고쳐야
// 한다는 것을 보이려면 반대 순서도 함께 돌려야 한다.
func TestTranslateRNGDExclusiveReportsRealRejectionNotWrongNode(t *testing.T) {
	warboy := NodeCapability{NodeName: nodeK8sWorker1, Vendor: vendorFuriosa,
		Advertised: map[string]int32{"beta.furiosa.ai/npu": 1}, SharingMode: v1alpha1.SharingModeExclusive}
	rngd := NodeCapability{NodeName: nodeRngd1, Vendor: vendorFuriosa, Advertised: map[string]int32{resRngd: 4},
		SharingMode: v1alpha1.SharingModeOversubscribed, SharingReplicas: 4}
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{rngdMapping()},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, AllocationAPI: v1alpha1.AllocationAPIAuto}

	for _, order := range [][]NodeCapability{{warboy, rngd}, {rngd, warboy}} {
		_, err := Translate(req, order)
		var rj *Reject
		if !errors.As(err, &rj) {
			t.Fatalf("expected *Reject, got %v", err)
		}
		if strings.Contains(rj.Message, nodeK8sWorker1) {
			t.Fatalf("wrong node reported — rngd-1's real rejection was masked by k8s-worker1's trivial one: %+v", rj)
		}
		if !strings.Contains(rj.Message, nodeRngd1) {
			t.Fatalf("rejection must name the real candidate node rngd-1: %+v", rj)
		}
	}
}

// 파티션 미적용 상태(전체 장치 리소스만 광고)에서도 partitioned 요청은 후보에서 통째로 빠지지
// 않고 ProfileNotApplied 로 거절되어야 한다 — 벤더만 보고 거르는 옛 필터를 리소스 기준으로
// 좁히면서, "아직 적용 안 됨" 을 "광고 안 함" 으로 뭉개는 회귀가 없는지 함께 지킨다.
func TestTranslatePartitionedPendingNodeStaysACandidate(t *testing.T) {
	nc := plainNvidiaNode(nodeWorker1) // nvidia.com/gpu 만 광고, mig-1g.6gb 는 아직 없음(미적용)
	req := Request{ClassName: "c", Mappings: []v1alpha1.AcceleratorMapping{nvidiaMapping()},
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitioned}, AllocationAPI: v1alpha1.AllocationAPIAuto}
	_, err := Translate(req, []NodeCapability{nc})
	var rj *Reject
	if !errors.As(err, &rj) || rj.Axis != AxisProfile || rj.Reason != v1alpha1.AWReasonProfileNotApplied {
		t.Fatalf("pending-partition node dropped instead of reporting ProfileNotApplied: %v", err)
	}
}

func TestDRAMappingRequiresBothFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping v1alpha1.AcceleratorMapping
		wantErr bool
	}{
		{"둘 다 없음 — device-plugin 전용 매핑", v1alpha1.AcceleratorMapping{Vendor: "nvidia"}, false},
		{"둘 다 있음", v1alpha1.AcceleratorMapping{Vendor: "nvidia", DeviceClassName: "gpu.nvidia.com", DRADriver: "gpu.nvidia.com"}, false},
		{"클래스만 있음", v1alpha1.AcceleratorMapping{Vendor: "nvidia", DeviceClassName: "gpu.nvidia.com"}, true},
		{"드라이버만 있음", v1alpha1.AcceleratorMapping{Vendor: "nvidia", DRADriver: "gpu.nvidia.com"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDRAMapping(tc.mapping)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateDRAMapping(%+v) err=%v, wantErr=%v", tc.mapping, err, tc.wantErr)
			}
		})
	}
}

func TestTranslateDRARejections(t *testing.T) {
	mapping := v1alpha1.AcceleratorMapping{Vendor: "nvidia"}
	draMapping := v1alpha1.AcceleratorMapping{Vendor: "nvidia",
		DeviceClassName: "gpu.nvidia.com", DRADriver: "gpu.nvidia.com"}

	for _, tc := range []struct {
		name       string
		mappings   []v1alpha1.AcceleratorMapping
		dra        DRACapability
		snap       []NodeCapability
		wantReason string
	}{
		{"API 미서빙", []v1alpha1.AcceleratorMapping{draMapping}, DRACapability{}, nil, v1alpha1.AWReasonDRANotEnabled},
		{"매핑 없음", []v1alpha1.AcceleratorMapping{mapping}, DRACapability{APIServed: true}, nil, v1alpha1.AWReasonDRAMappingMissing},
		{"DeviceClass 부재", []v1alpha1.AcceleratorMapping{draMapping}, DRACapability{APIServed: true, DeviceClasses: map[string]bool{}}, nil, v1alpha1.AWReasonDRADriverMissing},
		{"노드 없음", []v1alpha1.AcceleratorMapping{draMapping},
			DRACapability{APIServed: true, DeviceClasses: map[string]bool{"gpu.nvidia.com": true}}, nil, v1alpha1.AWReasonNoCandidateNodes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{ClassName: "c", Mappings: tc.mappings, AllocationAPI: v1alpha1.AllocationAPIDRA,
				Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}}
			_, err := TranslateWithDRA(req, tc.snap, tc.dra)
			var rej *Reject
			if !errors.As(err, &rej) {
				t.Fatalf("err = %v, want *Reject", err)
			}
			if rej.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", rej.Reason, tc.wantReason)
			}
		})
	}
}

func TestTranslateDRASuccess(t *testing.T) {
	req := Request{ClassName: "c", AllocationAPI: v1alpha1.AllocationAPIDRA,
		Access:   v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		Mappings: []v1alpha1.AcceleratorMapping{{Vendor: "nvidia", DeviceClassName: "gpu.nvidia.com", DRADriver: "gpu.nvidia.com"}}}
	snap := []NodeCapability{{NodeName: "w1", DRADevices: map[string]int32{"gpu.nvidia.com": 4}}}
	dra := DRACapability{APIServed: true, DeviceClasses: map[string]bool{"gpu.nvidia.com": true}}

	res, err := TranslateWithDRA(req, snap, dra)
	if err != nil {
		t.Fatalf("unexpected reject: %v", err)
	}
	if res.AllocationAPI != v1alpha1.AllocationAPIDRA {
		t.Fatalf("allocationAPI = %q", res.AllocationAPI)
	}
	if res.DeviceClassName != "gpu.nvidia.com" {
		t.Fatalf("deviceClassName = %q", res.DeviceClassName)
	}
	// DRA 경로에서 extended resource 를 함께 채우면 렌더러가 둘 다 요청해 장치를 이중 점유한다.
	if res.ResourceName != "" {
		t.Fatalf("resourceName must be empty on the DRA path, got %q", res.ResourceName)
	}
	if len(res.Nodes) != 1 || res.Nodes[0] != "w1" {
		t.Fatalf("nodes = %v", res.Nodes)
	}
}

// TestTranslateDRAThroughRealSnapshot 은 BuildDRACapability→BuildSnapshot→TranslateWithDRA 를
// 손으로 짠 NodeCapability 없이 실제로 관통시킨다 — 두 계층을 각각 손으로 짠 픽스처로 시험하면
// 계층이 어긋나도(예: BuildSnapshot 이 DRADevices 를 채우는 방식이 바뀌어도) 양쪽 테스트가 모두 초록으로 남는다.
func TestTranslateDRAThroughRealSnapshot(t *testing.T) {
	classes := []resourcev1.DeviceClass{{ObjectMeta: metav1.ObjectMeta{Name: "gpu.nvidia.com"}}}
	slices := []resourcev1.ResourceSlice{{Spec: resourcev1.ResourceSliceSpec{
		Driver:   "gpu.nvidia.com",
		NodeName: ptr.To(nodeWorker1),
		Devices:  []resourcev1.Device{{Name: "gpu-0"}},
	}}}
	dra := BuildDRACapability(true, classes, slices)
	snap := BuildSnapshot([]corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: nodeWorker1}}}, nil, nil, dra)

	req := Request{ClassName: "c", AllocationAPI: v1alpha1.AllocationAPIDRA,
		Access:   v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		Mappings: []v1alpha1.AcceleratorMapping{{Vendor: "nvidia", DeviceClassName: "gpu.nvidia.com", DRADriver: "gpu.nvidia.com"}}}

	res, err := TranslateWithDRA(req, snap, dra)
	if err != nil {
		t.Fatalf("unexpected reject: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0] != nodeWorker1 {
		t.Fatalf("nodes = %v", res.Nodes)
	}
}

// 같은 물리 장치가 device-plugin 과 DRA 양쪽으로 광고되면 스케줄러는 두 자원을 독립으로 본다 —
// 장치 1장에 Pod 2개가 배타 모드로 겹쳐 떨어질 수 있다. 관측되면 fail-closed 로 후보에서 뺀다.
func TestDRACandidateExcludesDoubleAdvertisedNode(t *testing.T) {
	snap := []NodeCapability{{
		NodeName:   "w1",
		Advertised: map[string]int32{"nvidia.com/gpu": 2},
		DRADevices: map[string]int32{"gpu.nvidia.com": 2},
	}}
	got, doubled := draCandidateNodes(snap, "gpu.nvidia.com", "nvidia.com/gpu", 1)
	if len(got) != 0 {
		t.Fatalf("double-advertised node must not be a DRA candidate, got %v", got)
	}
	// 왜 빠졌는지를 호출부가 알아야 사실과 반대인 NoCandidateNodes 를 내지 않는다.
	if len(doubled) != 1 || !strings.Contains(doubled[0], "w1") || !strings.Contains(doubled[0], "nvidia.com/gpu") {
		t.Fatalf("exclusion must name the node and the conflicting resource, got %v", doubled)
	}
}

// 다른 벤더의 device-plugin 광고는 DRA 후보 자격을 막지 않는다.
func TestDRACandidateAllowsOtherVendorDevicePlugin(t *testing.T) {
	snap := []NodeCapability{{
		NodeName:   "w1",
		Advertised: map[string]int32{"tenstorrent.com/blackhole": 1},
		DRADevices: map[string]int32{"gpu.nvidia.com": 2},
	}}
	got, _ := draCandidateNodes(snap, "gpu.nvidia.com", "nvidia.com/gpu", 1)
	if len(got) != 1 {
		t.Fatalf("want w1 as candidate, got %v", got)
	}
}

// product 가 모호해(furiosa: rngd/warboy 둘) 리소스명을 못 정하면 이중 광고 배제를 빈 문자열로
// 조용히 꺼버려선 안 된다 — 그러면 device-plugin 으로도 광고 중인 노드가 그대로 DRA 후보가
// 된다(fail-open). device-plugin 경로와 같은 축으로 거절해야 한다.
func TestTranslateDRARejectsWhenResourceNameAmbiguous(t *testing.T) {
	req := Request{ClassName: "c", AllocationAPI: v1alpha1.AllocationAPIDRA,
		Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		Mappings: []v1alpha1.AcceleratorMapping{
			{Vendor: vendorFuriosa, DeviceClassName: "rngd.furiosa.ai", DRADriver: "rngd.furiosa.ai"},
		}}
	snap := []NodeCapability{{
		NodeName:   "w1",
		Advertised: map[string]int32{resRngd: 2}, // device-plugin 으로도 광고 중
		DRADevices: map[string]int32{"rngd.furiosa.ai": 2},
	}}
	dra := DRACapability{APIServed: true, DeviceClasses: map[string]bool{"rngd.furiosa.ai": true}}

	_, err := TranslateWithDRA(req, snap, dra)
	var rj *Reject
	if !errors.As(err, &rj) {
		t.Fatalf("want reject when resource name is ambiguous, got %v", err)
	}
	if rj.Axis != AxisProduct {
		t.Fatalf("axis = %q, want %q", rj.Axis, AxisProduct)
	}
}

const draClassNvidia = "gpu.nvidia.com"

// draRequest 는 DRA 경로 요청이다(nvidia 매핑 하나).
func draRequest(access v1alpha1.AccessSpec, profile string) Request {
	return Request{ClassName: "c", AllocationAPI: v1alpha1.AllocationAPIDRA, Access: access,
		Mappings: []v1alpha1.AcceleratorMapping{{Vendor: vendorNvidia, NativeProfile: profile,
			DeviceClassName: draClassNvidia, DRADriver: draClassNvidia}}}
}

// translateDRAChain 은 손으로 짠 NodeCapability 를 쓰지 않고 BuildDRACapability→BuildSnapshot→
// TranslateWithDRA 를 그대로 관통시킨다(TestTranslateDRAThroughRealSnapshot 과 같은 이유 —
// 두 계층을 각각 손으로 짜면 계층이 어긋나도 양쪽이 초록으로 남는다).
func translateDRAChain(t *testing.T, req Request, nodes []corev1.Node, ndrs []v1alpha1.NodeDeviceReport) (*Result, error) {
	t.Helper()
	classes := []resourcev1.DeviceClass{{ObjectMeta: metav1.ObjectMeta{Name: draClassNvidia}}}
	slices := make([]resourcev1.ResourceSlice, 0, len(nodes))
	for i := range nodes {
		slices = append(slices, resourcev1.ResourceSlice{Spec: resourcev1.ResourceSliceSpec{
			Driver:   draClassNvidia,
			NodeName: ptr.To(nodes[i].Name),
			Devices:  []resourcev1.Device{{Name: "gpu-0"}},
		}})
	}
	dra := BuildDRACapability(true, classes, slices)
	return TranslateWithDRA(req, BuildSnapshot(nodes, nil, ndrs, dra), dra)
}

// draReject 는 거절을 꺼낸다 — 통과하면 실패다.
func draReject(t *testing.T, res *Result, err error) *Reject {
	t.Helper()
	var rj *Reject
	if !errors.As(err, &rj) {
		t.Fatalf("want reject, got result %+v (err %v)", res, err)
	}
	return rj
}

// DRA 경로는 공유 축을 하나도 보지 않는다(replicas·verified capability·적용 여부). 통과시키면
// status 는 shared 로 배치됐다고 보고하고, webhook 의 "replica 는 서로 격리되지 않는다" 경고는
// DRA Explanation 으로 대체돼 사라진다.
func TestTranslateDRARejectsSharedMode(t *testing.T) {
	res, err := translateDRAChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 8}, ""),
		[]corev1.Node{node(nodeWorker1, nil)}, nil)
	rj := draReject(t, res, err)
	if rj.Reason != v1alpha1.AWReasonDRAUnsupportedRequest || rj.Axis != AxisAllocationAPI {
		t.Fatalf("reason/axis = %q/%q", rj.Reason, rj.Axis)
	}
	if !strings.Contains(rj.Message, "sharing") || !strings.Contains(rj.Message, "devicePlugin") {
		t.Fatalf("message must name the unsupported axis and the remedy: %s", rj.Message)
	}
}

// 분할은 조용한 대체가 가장 위험한 축이다 — DeviceClass 는 nativeProfile 을 요청하지 않으므로
// 통과시키면 사용자는 MIG 조각 대신 아무 장치나 받고 status 는 partitioned 라고 말한다.
// 공유와 다음에 할 일이 다르므로 메시지도 달라야 한다.
func TestTranslateDRARejectsPartitionedMode(t *testing.T) {
	res, err := translateDRAChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitioned}, "1g.6gb"),
		[]corev1.Node{node(nodeWorker1, nil)}, nil)
	rj := draReject(t, res, err)
	if rj.Reason != v1alpha1.AWReasonDRAUnsupportedRequest || rj.Axis != AxisAllocationAPI {
		t.Fatalf("reason/axis = %q/%q", rj.Reason, rj.Axis)
	}
	if !strings.Contains(rj.Message, "nativeProfile") {
		t.Fatalf("message must say the profile is not applied: %s", rj.Message)
	}
	shared, sharedErr := translateDRAChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 2}, ""),
		[]corev1.Node{node(nodeWorker1, nil)}, nil)
	if msg := draReject(t, shared, sharedErr).Message; msg == rj.Message {
		t.Fatalf("partitioned and shared need different next steps, got the same message: %s", msg)
	}
}

// requirements 축도 DRA 경로에는 없다 — 노드를 DeviceClass 와 장치 수로만 고르므로 통과시키면
// minimumMemory 가 조용히 무시된다(8 GiB 노드가 80Gi 요구를 받아들인다).
func TestTranslateDRARejectsRequirements(t *testing.T) {
	req := draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, "")
	req.Requirements = v1alpha1.AcceleratorRequirements{MinimumMemory: ptr.To(resource.MustParse("80Gi"))}
	ndr := v1alpha1.NodeDeviceReport{Spec: v1alpha1.NodeDeviceReportSpec{NodeName: nodeWorker1}}
	ndr.Status.Devices = []v1alpha1.DeviceEntry{{Vendor: vendorNvidia, Count: 1, MemoryMiB: 8 * 1024}}

	res, err := translateDRAChain(t, req, []corev1.Node{node(nodeWorker1, nil)}, []v1alpha1.NodeDeviceReport{ndr})
	rj := draReject(t, res, err)
	if rj.Reason != v1alpha1.AWReasonDRAUnsupportedRequest {
		t.Fatalf("reason = %q", rj.Reason)
	}
	if !strings.Contains(rj.Message, "minimumMemory=80Gi") {
		t.Fatalf("message must name the requirement it cannot enforce: %s", rj.Message)
	}
}

// MIG 로 쪼갠 노드는 nvidia.com/gpu 를 광고하지 않고 조각만 광고한다 — 전체 장치 리소스명
// 하나만 보면 이중 광고 배제가 통째로 새고, device-plugin 과 DRA 가 같은 실리콘을 나눠 준다.
func TestDRACandidateExcludesMIGFragmentNode(t *testing.T) {
	res, err := translateDRAChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, ""),
		[]corev1.Node{node(nodeWorker1, map[string]string{"nvidia.com/mig-1g.6gb": "7"})}, nil)
	rj := draReject(t, res, err)
	if rj.Reason != v1alpha1.AWReasonDRADoubleAdvertised {
		t.Fatalf("reason = %q, want %q", rj.Reason, v1alpha1.AWReasonDRADoubleAdvertised)
	}
	// 조각 리소스명이 그대로 나와야 어느 플러그인을 꺼야 하는지 알 수 있다.
	if !strings.Contains(rj.Message, "nvidia.com/mig-1g.6gb") {
		t.Fatalf("message must name the conflicting fragment resource, got %q", rj.Message)
	}
}

// 광고량 0 은 "장치가 없다" 가 아니라 "플러그인이 지금 0 을 보고 있다"(재시작·전 장치 unhealthy)
// 이다. 그 플러그인은 복구되면 같은 장치를 다시 광고하므로 키가 있는 것만으로 배제한다.
func TestDRACandidateExcludesZeroCountAdvertisement(t *testing.T) {
	res, err := translateDRAChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, ""),
		[]corev1.Node{node(nodeWorker1, map[string]string{"nvidia.com/gpu": "0"})}, nil)
	// 배제 이유가 이중 광고이므로 사유도 그것이다 — 이 노드는 DRA 장치를 내고 있다.
	if rj := draReject(t, res, err); rj.Reason != v1alpha1.AWReasonDRADoubleAdvertised {
		t.Fatalf("reason = %q, want %q", rj.Reason, v1alpha1.AWReasonDRADoubleAdvertised)
	}
}

const draClassRngd = "npu.furiosa.ai"

// draSlice 는 그 노드가 그 드라이버로 장치 1개를 내는 ResourceSlice 다.
func draSlice(nodeName, driver string) resourcev1.ResourceSlice {
	return resourcev1.ResourceSlice{Spec: resourcev1.ResourceSliceSpec{
		Driver: driver, NodeName: ptr.To(nodeName), Devices: []resourcev1.Device{{Name: "dev-0"}}}}
}

// draChain 은 translateDRAChain 과 같은 실제 체인이지만 어느 노드가 어느 드라이버로 장치를
// 내는지 시험마다 따로 짤 수 있다(translateDRAChain 은 모든 노드에 nvidia 슬라이스를 붙인다).
func draChain(t *testing.T, req Request, nodes []corev1.Node, slices []resourcev1.ResourceSlice, classNames ...string) (*Result, error) {
	t.Helper()
	classes := make([]resourcev1.DeviceClass, 0, len(classNames))
	for _, name := range classNames {
		classes = append(classes, resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	dra := BuildDRACapability(true, classes, slices)
	return TranslateWithDRA(req, BuildSnapshot(nodes, nil, nil, dra), dra)
}

// rngdDRAMapping 은 RNGD DRA 매핑이다(product 를 줘야 furiosa 리소스명이 풀린다).
func rngdDRAMapping() v1alpha1.AcceleratorMapping {
	return v1alpha1.AcceleratorMapping{Vendor: vendorFuriosa, Product: "rngd",
		DeviceClassName: draClassRngd, DRADriver: draClassRngd}
}

// RNGD 재현: rngd-1 이 furiosa.ai/rngd 4개와 npu.furiosa.ai DRA 장치 1개를 동시에 내는 순간, 사용자가
// 받는 메시지는 "아무 노드도 그 드라이버로 장치를 내지 않는다" 다 — 사실과 반대라 사용자는 멀쩡한
// DRA 드라이버를 다시 깔러 간다. 고칠 곳은 그 노드의 device-plugin 이다.
func TestTranslateDRARejectsDoubleAdvertisedWithActionableReason(t *testing.T) {
	req := Request{ClassName: "c", AllocationAPI: v1alpha1.AllocationAPIDRA,
		Access:   v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive},
		Mappings: []v1alpha1.AcceleratorMapping{rngdDRAMapping()}}
	res, err := draChain(t, req,
		[]corev1.Node{node(nodeRngd1, map[string]string{resRngd: "4"})},
		[]resourcev1.ResourceSlice{draSlice(nodeRngd1, draClassRngd)}, draClassRngd)

	rj := draReject(t, res, err)
	if rj.Reason != v1alpha1.AWReasonDRADoubleAdvertised {
		t.Fatalf("reason = %q, want %q (message %q)", rj.Reason, v1alpha1.AWReasonDRADoubleAdvertised, rj.Message)
	}
	if !strings.Contains(rj.Message, nodeRngd1) || !strings.Contains(rj.Message, resRngd) {
		t.Fatalf("message must name the node and the conflicting resource, got %q", rj.Message)
	}
}

// 반대편: 정말로 아무도 DRA 장치를 내지 않으면 NoCandidateNodes 가 여전히 정직한 답이다
// (노드는 다른 벤더를 광고 중이라 스냅샷에는 남아 있다 — 이중 광고와 헷갈릴 여지가 없다).
func TestTranslateDRAKeepsNoCandidateNodesWithoutDRADevices(t *testing.T) {
	res, err := draChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, ""),
		[]corev1.Node{node(nodeWorker1, map[string]string{"tenstorrent.com/blackhole": "1"})}, nil, draClassNvidia)
	if rj := draReject(t, res, err); rj.Reason != v1alpha1.AWReasonNoCandidateNodes {
		t.Fatalf("reason = %q, want %q", rj.Reason, v1alpha1.AWReasonNoCandidateNodes)
	}
}

// 한 매핑은 이중 광고로, 다른 매핑은 진짜 장치 없음으로 떨어지는 혼재 상황. 매핑 순서와 무관하게
// 이중 광고가 남아야 한다 — 노드 이름·충돌 리소스·할 일을 가진 쪽은 이것뿐이고, NoCandidateNodes
// 는 사용자가 손댈 곳을 하나도 지목하지 못한다.
func TestTranslateDRADoubleAdvertisedOutranksNoCandidateNodes(t *testing.T) {
	nvidiaMapping := v1alpha1.AcceleratorMapping{Vendor: vendorNvidia,
		DeviceClassName: draClassNvidia, DRADriver: draClassNvidia}
	// worker1 은 nvidia DRA 장치를 내지 않고, rngd-1 은 내지만 이중 광고다.
	nodes := []corev1.Node{
		node(nodeWorker1, map[string]string{"tenstorrent.com/blackhole": "1"}),
		node(nodeRngd1, map[string]string{resRngd: "4"}),
	}
	slices := []resourcev1.ResourceSlice{draSlice(nodeRngd1, draClassRngd)}

	for name, mappings := range map[string][]v1alpha1.AcceleratorMapping{
		"doubleAdvertisedFirst": {rngdDRAMapping(), nvidiaMapping},
		"noDevicesFirst":        {nvidiaMapping, rngdDRAMapping()},
	} {
		t.Run(name, func(t *testing.T) {
			req := Request{ClassName: "c", AllocationAPI: v1alpha1.AllocationAPIDRA,
				Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, Mappings: mappings}
			res, err := draChain(t, req, nodes, slices, draClassNvidia, draClassRngd)
			rj := draReject(t, res, err)
			if rj.Reason != v1alpha1.AWReasonDRADoubleAdvertised {
				t.Fatalf("reason = %q, want %q (message %q)", rj.Reason, v1alpha1.AWReasonDRADoubleAdvertised, rj.Message)
			}
			if !strings.Contains(rj.Message, nodeRngd1) || !strings.Contains(rj.Message, resRngd) {
				t.Fatalf("message must name the node and the conflicting resource, got %q", rj.Message)
			}
		})
	}
}

func TestBuildRequestMergesClassAndWorkload(t *testing.T) {
	class := classFor(v1alpha1.AcceleratorMapping{Vendor: vendorNvidia, NativeProfile: "1g.6gb"})
	class.Spec.Requirements.MinimumIsolation = v1alpha1.IsolationProcess
	aw := &v1alpha1.AcceleratorWorkload{Spec: v1alpha1.AcceleratorWorkloadSpec{
		Accelerator: v1alpha1.AcceleratorRequest{
			Class:        "inference-medium",
			Access:       v1alpha1.AccessSpec{Mode: v1alpha1.AccessModePartitioned},
			Requirements: &v1alpha1.AcceleratorRequirements{MinimumIsolation: v1alpha1.IsolationHardware},
			Preferences:  &v1alpha1.AcceleratorPreferences{Vendors: []string{vendorFuriosa}, AllocationAPI: v1alpha1.AllocationAPIDevicePlugin},
		},
	}}
	req := BuildRequest(aw, class)
	if req.Requirements.MinimumIsolation != v1alpha1.IsolationHardware {
		t.Fatalf("stricter workload requirement lost: %+v", req.Requirements)
	}
	if len(req.VendorPreference) != 1 || req.VendorPreference[0] != vendorFuriosa {
		t.Fatalf("preference lost: %v", req.VendorPreference)
	}
	if req.AllocationAPI != v1alpha1.AllocationAPIDevicePlugin || req.ClassName != "inference-medium" {
		t.Fatalf("unexpected request %+v", req)
	}
	// preferences 가 없으면 auto 로 채운다.
	aw.Spec.Accelerator.Preferences = nil
	if got := BuildRequest(aw, class).AllocationAPI; got != v1alpha1.AllocationAPIAuto {
		t.Fatalf("default allocationAPI=%q want auto", got)
	}
}

// 이중 광고 거절 메시지는 "device plugin 을 끄라" 로 끝나면 안 된다 — 사용자가 손으로 DaemonSet 을
// 건드리면 operator 가 선언한 상태로 되돌린다. 끄는 방법은 spec 의 advertiseBy 뿐이므로 메시지가 그 축을 지목해야 한다.
func TestDRADoubleAdvertisedMessagePointsAtAdvertiseSwitch(t *testing.T) {
	res, err := translateDRAChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, ""),
		[]corev1.Node{node(nodeWorker1, map[string]string{"nvidia.com/gpu": "2"})}, nil)
	rj := draReject(t, res, err)
	if !strings.Contains(rj.Message, "advertiseBy") {
		t.Errorf("메시지가 광고 주체 스위치를 지목하지 않음: %s", rj.Message)
	}
	if !strings.Contains(rj.Message, "dra") {
		t.Errorf("메시지가 어느 값으로 바꾸라는지 말하지 않음: %s", rj.Message)
	}
}

// 광고 주체를 DRA 로 넘긴 노드는 이중 광고가 아니다. device-plugin 이 물러난 뒤에도 allocatable 에는
// 0 으로 굳은 키가 남는데, 그것을 이중 광고로 세면 스위치를 켜도 그 노드가 영원히 DRA 후보가 되지 못한다.
func TestDRACandidateIncludesDRAOwnedNode(t *testing.T) {
	n := node(nodeWorker1, map[string]string{"nvidia.com/gpu": "0"})
	n.Labels = map[string]string{"kcloud.ai/nvidia.dra-owned": "true"}

	res, err := translateDRAChain(t, draRequest(v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}, ""),
		[]corev1.Node{n}, nil)
	if err != nil {
		t.Fatalf("DRA 소유 노드가 거절됨: %v", err)
	}
	if res == nil || len(res.Nodes) != 1 || res.Nodes[0] != nodeWorker1 {
		t.Fatalf("후보가 그 노드여야 함: %+v", res)
	}
	if res.AllocationAPI != v1alpha1.AllocationAPIDRA {
		t.Fatalf("allocationAPI = %q, want dra", res.AllocationAPI)
	}
}
