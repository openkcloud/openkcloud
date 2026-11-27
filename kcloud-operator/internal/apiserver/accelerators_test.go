// ============================================================
// accelerators_test.go: /accelerators 3종 엔드포인트 핸들러 테스트
// 상세: fake reader 로 200/404/필터/페이지네이션을 본다. 인증·인가는 미들웨어 시험에서 이미
//       덮었으므로 여기서는 okAuth 로 통과시키고 응답 형태만 검증한다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"encoding/json"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
)

// decodePage 는 목록 봉투를 items 타입까지 풀어 준다.
func decodePage[T any](t *testing.T, body []byte) (items []T, total, limit, offset int) {
	t.Helper()
	var env struct {
		Items  []T `json:"items"`
		Total  int `json:"total"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("응답 JSON 파싱 실패: %v (%s)", err, string(body))
	}
	return env.Items, env.Total, env.Limit, env.Offset
}

// invObjects 는 worker1(NVIDIA, ACPP 관리) + rngd-1(Furiosa, 미관리) 두 노드를 만든다.
func invObjects() []client.Object {
	w1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"nvidia.com/gpu": resource.MustParse("1"),
		}},
	}
	r1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"furiosa.ai/rngd": resource.MustParse("2"),
		}},
	}
	acpp := &v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-policy", Generation: 1},
		Spec:       v1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 1,
			Targets: []v1alpha1.TargetStatus{{
				NodeName: "worker1", Phase: v1alpha1.ACPPPhaseReady,
				Devices: []v1alpha1.DeviceStatus{{ID: "GPU-aaa", Model: "A30", PCIAddress: "0000:3b:00.0"}},
				// 실제 A30 이 보고하는 형태(1g.6gb 프로파일, 장치당 4인스턴스) — 논리 파티션
				// 수 테스트(TestBuildInventory_CarriesPartitionInstanceCounts)가 이 픽스처를 쓴다.
				ResolvedLayout: []v1alpha1.ResolvedLayoutEntry{{Profile: "1g.6gb", ExpectedCountPerDevice: 4}},
			}},
		},
	}
	n1 := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "worker1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "A30", Count: 1, MemoryMiB: 24576},
		}},
	}
	n2 := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "rngd-1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "furiosa", Model: "rngd", Count: 2, MemoryMiB: 49152},
		}},
	}
	return []client.Object{w1, r1, acpp, n1, n2}
}

func TestAccelerators_ListsAllPhysicalDevices(t *testing.T) {
	// worker1 에 DRA ResourceSlice 1장을 더한다 — attachDRAAllocation 호출이 핸들러에서
	// 지워지면 아래 dra: 키 단언이 깨진다(변이로 확인한 결속).
	objs := append(invObjects(), &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1-gpu"},
		Spec: resourcev1.ResourceSliceSpec{
			Driver:   "gpu.nvidia.com",
			NodeName: ptr.To("worker1"),
			Devices:  []resourcev1.Device{{Name: "gpu-0"}},
		},
	})
	s, _ := newServer(t, okAuth(), objs...)
	w := do(t, s, "GET", "/api/v1/accelerators", "", "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
	items, total, limit, offset := decodePage[AcceleratorView](t, w.Body.Bytes())
	if total != 3 || len(items) != 3 {
		t.Fatalf("worker1 1장 + rngd-1 2장 = 3행이어야 함: total=%d len=%d", total, len(items))
	}
	if limit != defaultLimit || offset != 0 {
		t.Fatalf("기본 페이지여야 함: limit=%d offset=%d", limit, offset)
	}
	found := false
	for _, it := range items {
		if it.NodeName != "worker1" {
			continue
		}
		found = true
		if it.NodeAdvertised["dra:gpu.nvidia.com"] != 1 {
			t.Fatalf("worker1 은 ResourceSlice 로 gpu.nvidia.com 1장을 광고해야 함: %v", it.NodeAdvertised)
		}
	}
	if !found {
		t.Fatal("worker1 행을 찾지 못함")
	}
}

func TestAccelerators_VendorFilter(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)
	w := do(t, s, "GET", "/api/v1/accelerators?vendor=furiosa", "", "tok")
	items, total, _, _ := decodePage[AcceleratorView](t, w.Body.Bytes())
	if total != 2 || len(items) != 2 {
		t.Fatalf("furiosa 2행이어야 함: total=%d", total)
	}
	for _, it := range items {
		if it.Vendor != "furiosa" {
			t.Fatalf("필터가 안 걸림: %q", it.Vendor)
		}
	}
}

func TestAccelerators_Pagination(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)
	w := do(t, s, "GET", "/api/v1/accelerators?limit=1&offset=2", "", "tok")
	items, total, limit, offset := decodePage[AcceleratorView](t, w.Body.Bytes())
	if total != 3 || len(items) != 1 || limit != 1 || offset != 2 {
		t.Fatalf("total 은 전체, items 는 1개여야 함: total=%d len=%d limit=%d offset=%d", total, len(items), limit, offset)
	}
}

func TestAcceleratorByUID_FoundAndNotFound(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)

	w := do(t, s, "GET", "/api/v1/accelerators/GPU-aaa", "", "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("canonical UID 조회는 200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
	var got AcceleratorView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	if got.UID != "GPU-aaa" || got.Source != SourceACPP {
		t.Fatalf("잘못된 장치: %#v", got)
	}

	if w := do(t, s, "GET", "/api/v1/accelerators/nope", "", "tok"); w.Code != http.StatusNotFound {
		t.Fatalf("없는 UID 는 404 여야 함: got %d", w.Code)
	}
}

// 합성 ID 는 "/" 를 포함한다 — 경로에 그대로 들어가도 조회돼야 한다.
func TestAcceleratorByUID_SyntheticIDWithSlashes(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)
	w := do(t, s, "GET", "/api/v1/accelerators/rngd-1/furiosa/rngd%230", "", "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("합성 ID 조회는 200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
}

func TestNodeAccelerators_OKAndUnknownNode404(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)

	w := do(t, s, "GET", "/api/v1/nodes/rngd-1/accelerators", "", "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
	items, total, _, _ := decodePage[AcceleratorView](t, w.Body.Bytes())
	if total != 2 || len(items) != 2 {
		t.Fatalf("rngd-1 은 2행이어야 함: total=%d", total)
	}

	if w := do(t, s, "GET", "/api/v1/nodes/ghost/accelerators", "", "tok"); w.Code != http.StatusNotFound {
		t.Fatalf("없는 노드는 404 여야 함(빈 목록 아님): got %d", w.Code)
	}
}

// acceleratorpartitionpolicies 만 허용되고 nodedevicereports 는 거부되면 403 이어야
// 한다 — 미관리 노드(NDR 단독 출처) 행을 ACPP 권한만으로 볼 수 있으면 안 된다.
func TestAccelerators_DeniedWithoutNDRAuthz(t *testing.T) {
	opts := authnOpts{
		authenticated: true, username: "sa:acpp-only", allowed: true,
		denyResources: map[string]bool{"nodedevicereports": true},
	}
	s, _ := newServer(t, opts, invObjects()...)
	if w := do(t, s, "GET", "/api/v1/accelerators", "", "tok"); w.Code != http.StatusForbidden {
		t.Fatalf("nodedevicereports 권한 없으면 403 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
}

// 후행 슬래시는 uid="" 로 {uid...} 를 매칭시킨다 — Pending 행(zero-value UID)이
// 조회되지 않고 404 여야 한다.
func TestAcceleratorByUID_EmptyUID404(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)
	if w := do(t, s, "GET", "/api/v1/accelerators/", "", "tok"); w.Code != http.StatusNotFound {
		t.Fatalf("빈 UID(후행 슬래시)는 404 여야 함: got %d (%s)", w.Code, w.Body.String())
	}
}

// 기존 /api/v1/nodes/{node} 가 새 하위 경로 때문에 깨지지 않아야 한다.
func TestNodeStatus_StillRoutesAfterSubpathAdded(t *testing.T) {
	s, _ := newServer(t, okAuth(), invObjects()...)
	if w := do(t, s, "GET", "/api/v1/nodes/worker1", "", "tok"); w.Code != http.StatusOK {
		t.Fatalf("기존 노드 상태 경로가 200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
}
