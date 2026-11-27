// ============================================================
// capabilities_test.go: 노드 단위 capability 조회 테스트
// 상세: 후보에서 빠진 노드가 목록에서 사라지지 않고 "왜 빠졌는지" 를 달고 남는지 본다
//       (cordon / ACPP 미수렴). 미검증 capability 가 verified=false 로 나가는지도 재확인한다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

func TestBuildNodeCapabilities_CordonedNodeStaysWithReason(t *testing.T) {
	nodes := []corev1.Node{node("worker1", true, map[string]string{"nvidia.com/gpu": "1"})}
	acpps := []v1alpha1.AcceleratorPartitionPolicy{
		acppReady([]v1alpha1.DeviceStatus{{ID: "GPU-aaa", Model: "A30"}}, v1alpha1.SharingModeExclusive, 0),
	}
	got := BuildNodeCapabilities(nodes, acpps, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("cordon 된 노드도 목록에 남아야 함: got %d", len(got))
	}
	if got[0].Candidate {
		t.Fatalf("cordon 노드는 후보가 아니어야 함")
	}
	if got[0].CandidateReason == "" {
		t.Fatalf("후보에서 빠진 이유가 비어 있으면 사용자가 고칠 수 없다")
	}
	if got[0].Schedulable {
		t.Fatalf("Schedulable 은 false 여야 함")
	}
	if len(got[0].Devices) != 1 || got[0].Devices[0].UID != "GPU-aaa" {
		t.Fatalf("장치 capability 가 실려야 함: %#v", got[0].Devices)
	}
}

func TestBuildNodeCapabilities_UnconvergedPolicyIsNotCandidate(t *testing.T) {
	acpp := v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Generation: 2},
		Spec:       v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 1, // 아직 수렴 전
			Targets: []v1alpha1.TargetStatus{{
				NodeName: "worker1", Phase: v1alpha1.ACPPPhaseApplying,
				Devices: []v1alpha1.DeviceStatus{{ID: "GPU-aaa"}},
			}},
		},
	}
	nodes := []corev1.Node{node("worker1", false, map[string]string{"nvidia.com/gpu": "1"})}
	got := BuildNodeCapabilities(nodes, []v1alpha1.AcceleratorPartitionPolicy{acpp}, nil, intent.DRACapability{})
	if len(got) != 1 || got[0].Candidate {
		t.Fatalf("미수렴 정책이 붙은 노드는 후보가 아니어야 함: %#v", got)
	}
	if got[0].CandidateReason == "" {
		t.Fatalf("미수렴 사유가 있어야 함")
	}
}

func TestBuildNodeCapabilities_NoAcceleratorNodeIsExcludedWithReason(t *testing.T) {
	nodes := []corev1.Node{{
		ObjectMeta: metav1.ObjectMeta{Name: "master1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"cpu": resource.MustParse("8"),
		}},
	}}
	got := BuildNodeCapabilities(nodes, nil, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("가속기 없는 노드도 이유와 함께 남는다: got %d", len(got))
	}
	if got[0].Candidate || got[0].CandidateReason == "" {
		t.Fatalf("후보 아님 + 사유가 있어야 함: %#v", got[0])
	}
}

// DRA 로만 광고하는 노드(device-plugin 광고 0)도 후보로 나와야 한다. 빈 DRACapability 로
// 스냅샷을 뜨면 이 노드가 스냅샷에서 통째로 빠져 "아무것도 광고하지 않는다" 는 사유가 붙는데,
// 같은 순간 /preview 는 같은 노드를 dra 워크로드의 후보라고 답한다 — 두 화면이 같은 노드를
// 두고 반대로 말하면 UI 가 어느 쪽도 믿을 수 없다.
func TestBuildNodeCapabilities_DRAOnlyNodeIsCandidate(t *testing.T) {
	nodes := []corev1.Node{node("dra-worker", false, nil)}
	// 실제 관측 경로(BuildDRACapability)를 그대로 태운다 — NodeCapability 를 손으로 지으면
	// 스냅샷이 DRA-only 노드를 버리는지 여부를 애초에 시험하지 못한다.
	dra := intent.BuildDRACapability(true,
		[]resourcev1.DeviceClass{{ObjectMeta: metav1.ObjectMeta{Name: "gpu.nvidia.com"}}},
		[]resourcev1.ResourceSlice{{
			ObjectMeta: metav1.ObjectMeta{Name: "dra-worker-gpu"},
			Spec: resourcev1.ResourceSliceSpec{
				Driver:   "gpu.nvidia.com",
				NodeName: ptr.To("dra-worker"),
				Devices:  []resourcev1.Device{{Name: "gpu-0"}, {Name: "gpu-1"}},
			},
		}})
	got := BuildNodeCapabilities(nodes, nil, nil, dra)
	if len(got) != 1 {
		t.Fatalf("rows %d", len(got))
	}
	if !got[0].Candidate {
		t.Fatalf("DRA 전용 노드가 후보에서 빠졌다: %q", got[0].CandidateReason)
	}
	if got[0].DRADevices["gpu.nvidia.com"] != 2 {
		t.Fatalf("후보인 근거(드라이버별 DRA 장치 수)가 보여야 한다: %#v", got[0].DRADevices)
	}
}

func TestCapabilities_Endpoint(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)
	w := do(t, s, "GET", "/api/v1/capabilities", "", "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
	items, total, _, _ := decodePage[NodeCapabilityView](t, w.Body.Bytes())
	if total != 2 || len(items) != 2 {
		t.Fatalf("노드 2개여야 함: total=%d", total)
	}
}

// /accelerators 의 TestAccelerators_DeniedWithoutNDRAuthz 와 대칭이다 — capabilities 도
// NDR 로 되짚은 값(memoryMiB/sharingReplicas)을 실으므로 nodedevicereports 권한도 필요하다.
func TestCapabilities_DeniedWithoutNDRAuthz(t *testing.T) {
	opts := authnOpts{
		authenticated: true, username: "sa:acpp-only", allowed: true,
		denyResources: map[string]bool{"nodedevicereports": true},
	}
	s, _ := newServer(t, opts, invObjects()...)
	if w := do(t, s, "GET", "/api/v1/capabilities", "", "tok"); w.Code != http.StatusForbidden {
		t.Fatalf("nodedevicereports 권한 없으면 403 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
}

// 사전순 앞의 Failed 정책("worker1-a2-timeslice")이 같은 노드의 Ready
// 정책(readySiblingPolicyName)을 /capabilities 에서 가리고 노드 전체를
// candidate:false 로 만들면 안 된다.
func TestBuildNodeCapabilities_FailedSiblingDoesNotHideReadyPolicy(t *testing.T) {
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
	got := BuildNodeCapabilities(nodes, []v1alpha1.AcceleratorPartitionPolicy{failed, ready}, nil, intent.DRACapability{})
	if len(got) != 1 {
		t.Fatalf("rows %d", len(got))
	}
	if !got[0].Candidate {
		t.Fatalf("Ready 정책이 있는 노드가 후보에서 빠졌다: %q", got[0].CandidateReason)
	}
	if got[0].Policy != readySiblingPolicyName {
		t.Fatalf("Ready 정책이 노출돼야 한다: policy=%q", got[0].Policy)
	}
	if len(got[0].Devices) != 1 || got[0].Devices[0].UID != "PCI-0000:18:00.0" {
		t.Fatalf("Ready 정책의 장치가 실려야 한다: %#v", got[0].Devices)
	}
}
