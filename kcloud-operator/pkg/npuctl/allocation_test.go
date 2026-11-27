// allocation_test.go: CollectAllocationStatus 단위 테스트(fake client, envtest 불필요)
// 상세: NPUClusterPolicy.spec.<vendor>.advertiseBy(선언)와 노드의 dra-owned 라벨(관측)이
// 각각 옮겨지는지, 둘이 어긋나는 경우와 벤더가 꺼진/정책이 없는 경우를 검증한다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// findAllocation 는 벤더 이름으로 행을 찾는다(vendorcap_test.go 의 find 와 같은 역할, 다른 행 타입).
func findAllocation(rows []AllocationRow, vendor string) AllocationRow {
	for _, r := range rows {
		if r.Vendor == vendor {
			return r
		}
	}
	return AllocationRow{}
}

// nvidia 를 dra 로 선언했지만 노드 n2 는 아직 넘어가지 않은 상태다 — 선언과 관측의 이
// 어긋남 자체가 이 명령을 보는 이유이므로, 그대로 옮기는지 본다.
func TestCollectAllocationStatusDeclaredAndObservedCanDisagree(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled:       true,
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDRA},
			},
		},
	}
	n1 := labeledNode("n1", map[string]string{
		"kcloud.ai/nvidia.present":              labelValueTrue,
		npuv1alpha1.DRAOwnedNodeLabel("nvidia"): labelValueTrue,
	})
	n2 := labeledNode("n2", map[string]string{"kcloud.ai/nvidia.present": labelValueTrue})

	c := newTestClient(t, ncp, &n1, &n2)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectAllocationStatus: %v", err)
	}
	got := findAllocation(rows, VendorNvidia)
	if !got.Enabled || got.Declared != npuv1alpha1.AdvertiseByDRA {
		t.Fatalf("선언 미반영: %+v", got)
	}
	if len(got.DRANodes) != 1 || got.DRANodes[0] != "n1" {
		t.Fatalf("관측(dra)이 넘어간 노드만 담아야 한다: %+v", got)
	}
	if len(got.DevicePluginNodes) != 1 || got.DevicePluginNodes[0] != "n2" {
		t.Fatalf("아직 안 넘어간 노드는 devicePlugin 관측으로 남아야 한다: %+v", got)
	}
}

// advertiseBy 를 비워 두면(devicePlugin 이 기본) 실제로 devicePlugin 이 광고 중인 노드가
// 나와야 한다 — 이 흔한 경우를 빈 결과로 내면 아무것도 광고되지 않는 것처럼 잘못 읽힌다.
func TestCollectAllocationStatusDefaultsToDevicePluginWhenUnset(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Rebellions: npuv1alpha1.RebellionsSpec{Enabled: true},
		},
	}
	n1 := labeledNode("n1", map[string]string{"kcloud.ai/rebellions.present": labelValueTrue})

	c := newTestClient(t, ncp, &n1)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectAllocationStatus: %v", err)
	}
	got := findAllocation(rows, VendorRebellions)
	if got.Declared != npuv1alpha1.AdvertiseByDevicePlugin {
		t.Fatalf("advertiseBy 미지정은 devicePlugin 기본값이어야 한다: %+v", got)
	}
	if len(got.DevicePluginNodes) != 1 || got.DevicePluginNodes[0] != "n1" || len(got.DRANodes) != 0 {
		t.Fatalf("실제 광고 중인 노드가 devicePlugin 목록에 없다: %+v", got)
	}
}

// 정책이 아예 없어도(또는 벤더가 꺼져 있어도) 빈 목록이 아니라 다섯 벤더 전부를 Enabled=false
// 로 명시해야 한다 — 빈 결과는 "아무것도 광고하지 않는다"로 잘못 읽힌다.
func TestCollectAllocationStatusNoPolicyListsAllVendorsAsDisabled(t *testing.T) {
	c := newTestClient(t)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectAllocationStatus: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("정책이 없어도 다섯 벤더 행이 있어야 한다: %+v", rows)
	}
	for _, r := range rows {
		if r.Enabled || r.Declared != "" || len(r.DevicePluginNodes) != 0 || len(r.DRANodes) != 0 {
			t.Errorf("꺼진 벤더 행에 값이 지어내졌다: %+v", r)
		}
	}
}

// furiosa(Warboy)와 rngd 는 dra-owned 라벨 키가 다르다(kcloud.ai/furiosa.dra-owned 대
// kcloud.ai/rngd.dra-owned). rngd 라벨만 있는 노드는 furiosa(Warboy) 행에서 dra 로
// 잘못 세면 안 된다 — nodeDRAOwned 의 product 분기를 올바르게 넘기는지 고정한다.
func TestCollectAllocationStatusFuriosaWarboyDoesNotBorrowRngdLabel(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Enabled:       true,
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDRA},
			},
		},
	}
	// furiosa.present(=Warboy) 노드에 rngd 쪽 dra-owned 라벨만 실수로/다른 이유로 붙어 있다.
	n1 := labeledNode("n1", map[string]string{
		"kcloud.ai/furiosa.present":           labelValueTrue,
		npuv1alpha1.DRAOwnedNodeLabel("rngd"): labelValueTrue,
	})

	c := newTestClient(t, ncp, &n1)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectAllocationStatus: %v", err)
	}
	got := findAllocation(rows, VendorFuriosa)
	if len(got.DRANodes) != 0 || len(got.DevicePluginNodes) != 1 || got.DevicePluginNodes[0] != "n1" {
		t.Fatalf("rngd 라벨을 furiosa(Warboy) 관측으로 잘못 셌다: %+v", got)
	}
}

// FuriosaSpec.Unified 인 클러스터의 노드는 furiosa.present·rngd.present 대신 자립 라벨
// kcloud.ai/furiosa-family.present 만 갖는다(npuclusterpolicy_controller.go
// furiosaFamilyNodeLabel). 어느 product 인지 라벨만으로 못 가르면 devicePlugin·dra
// 어느 쪽으로도 단정하지 않고 AmbiguousProductNodes 에 담는다 — 두 API 목록에 넣는 것은
// 틀린 벤더에 틀린 API 를 적는 것과 같다.
func TestCollectAllocationStatusUnifiedFamilyLabelIsAmbiguousForBothProducts(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Enabled: true,
				Unified: true,
				Rngd:    npuv1alpha1.RngdSpec{Enabled: true},
			},
		},
	}
	n1 := labeledNode("n1", map[string]string{"kcloud.ai/furiosa-family.present": labelValueTrue})

	c := newTestClient(t, ncp, &n1)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectAllocationStatus: %v", err)
	}
	furiosa := findAllocation(rows, VendorFuriosa)
	if len(furiosa.DevicePluginNodes) != 0 || len(furiosa.DRANodes) != 0 {
		t.Fatalf("모호한 노드를 furiosa(Warboy) API 로 단정했다: %+v", furiosa)
	}
	if len(furiosa.AmbiguousProductNodes) != 1 || furiosa.AmbiguousProductNodes[0] != "n1" {
		t.Fatalf("모호 노드가 furiosa 행에 없다: %+v", furiosa)
	}
	rngd := findAllocation(rows, VendorRngd)
	if len(rngd.DevicePluginNodes) != 0 || len(rngd.DRANodes) != 0 {
		t.Fatalf("모호한 노드를 rngd API 로 단정했다: %+v", rngd)
	}
	if len(rngd.AmbiguousProductNodes) != 1 || rngd.AmbiguousProductNodes[0] != "n1" {
		t.Fatalf("모호 노드가 rngd 행에도 없다: %+v", rngd)
	}
}

// 한쪽 벤더의 dra-owned 라벨이 있어도(=실제로는 그 벤더일 가능성이 높아도) product 는
// present 라벨이 아니라 이 라벨에서만 나오므로 furiosaProduct 로는 여전히 미상이다.
// 그렇다고 다른 벤더 행에 devicePlugin 으로 적으면 안 된다(review 정정의 예시 그대로).
func TestCollectAllocationStatusAmbiguousNodeWithOneDRALabelIsNotDevicePluginElsewhere(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Enabled: true,
				Unified: true,
				Rngd:    npuv1alpha1.RngdSpec{Enabled: true},
			},
		},
	}
	n1 := labeledNode("n1", map[string]string{
		"kcloud.ai/furiosa-family.present":    labelValueTrue,
		npuv1alpha1.DRAOwnedNodeLabel("rngd"): labelValueTrue,
	})

	c := newTestClient(t, ncp, &n1)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectAllocationStatus: %v", err)
	}
	furiosa := findAllocation(rows, VendorFuriosa)
	if len(furiosa.DevicePluginNodes) != 0 || len(furiosa.DRANodes) != 0 {
		t.Fatalf("확정 못한 노드를 furiosa(Warboy) API 로 단정했다: %+v", furiosa)
	}
	if len(furiosa.AmbiguousProductNodes) != 1 || furiosa.AmbiguousProductNodes[0] != "n1" {
		t.Fatalf("모호 노드가 furiosa 행에 없다: %+v", furiosa)
	}
}

// TestAllocationStatus_ShowsRetiredNodes 는 retired 라벨(DRARetiredNodeLabel)이 있는 노드가
// RetiredNodes 열에 읽기 전용으로 나오는 것을, furiosa(Warboy)·rngd 는 라벨 키가 갈리는
// 것을(nodeDRAOwned 의 product 분기와 같은 이유) 단정한다. 이 조회는 라벨을 읽기만 한다 —
// devicePlugin/dra 판정 자체를 바꾸지 않는다.
func TestAllocationStatus_ShowsRetiredNodes(t *testing.T) {
	t.Run("nvidia", func(t *testing.T) {
		ncp := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Nvidia: npuv1alpha1.NvidiaSpec{Enabled: true},
			},
		}
		n1 := labeledNode("n1", map[string]string{
			"kcloud.ai/nvidia.present":                labelValueTrue,
			npuv1alpha1.DRARetiredNodeLabel("nvidia"): labelValueTrue,
		})
		c := newTestClient(t, ncp, &n1)
		rows, err := c.CollectAllocationStatus(context.Background())
		if err != nil {
			t.Fatalf("CollectAllocationStatus: %v", err)
		}
		got := findAllocation(rows, VendorNvidia)
		if len(got.RetiredNodes) != 1 || got.RetiredNodes[0] != "n1" {
			t.Fatalf("retired 노드가 안 보인다: %+v", got)
		}
		if len(got.DevicePluginNodes) != 1 || got.DevicePluginNodes[0] != "n1" {
			t.Fatalf("retired 표시가 기존 관측 열을 건드렸다: %+v", got)
		}
	})

	t.Run("furiosa와rngd는라벨이갈린다", func(t *testing.T) {
		ncp := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Furiosa: npuv1alpha1.FuriosaSpec{Enabled: true},
			},
		}
		// furiosa.present(=Warboy) 노드에 rngd 쪽 retired 라벨만 있다 — furiosa(Warboy) 의
		// RetiredNodes 로 잘못 세면 안 된다.
		n1 := labeledNode("n1", map[string]string{
			"kcloud.ai/furiosa.present":             labelValueTrue,
			npuv1alpha1.DRARetiredNodeLabel("rngd"): labelValueTrue,
		})
		c := newTestClient(t, ncp, &n1)
		rows, err := c.CollectAllocationStatus(context.Background())
		if err != nil {
			t.Fatalf("CollectAllocationStatus: %v", err)
		}
		got := findAllocation(rows, VendorFuriosa)
		if len(got.RetiredNodes) != 0 {
			t.Fatalf("rngd retired 라벨을 furiosa(Warboy) 로 잘못 셌다: %+v", got)
		}
	})
}

// --- SwitchAllocationAPI ---

// TestSwitchAllocationAPI_NPUClusterPolicy없음 은 정책이 클러스터에 없으면 거절하는지
// 본다(4절 표 — 대상 없음. SetVendorEnabled 와 같은 기존 메시지 관례).
func TestSwitchAllocationAPI_NPUClusterPolicy없음(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.SwitchAllocationAPI(context.Background(), VendorNvidia, npuv1alpha1.AdvertiseByDRA); err == nil {
		t.Fatalf("NPUClusterPolicy 가 없는데 에러가 없다")
	}
}

// TestSwitchAllocationAPI_알수없는벤더나API면거절 은 다섯 벤더가 아니거나 devicePlugin|dra
// 가 아닌 값을 거절하는지 본다.
func TestSwitchAllocationAPI_알수없는벤더나API면거절(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ncp"}}
	c := newTestClient(t, ncp)
	if _, err := c.SwitchAllocationAPI(context.Background(), "acme", npuv1alpha1.AdvertiseByDRA); err == nil {
		t.Errorf("알 수 없는 벤더인데 에러가 없다")
	}
	if _, err := c.SwitchAllocationAPI(context.Background(), VendorNvidia, "auto"); err == nil {
		t.Errorf("알 수 없는 API(auto)인데 에러가 없다")
	}
}

// TestSwitchAllocationAPI_값같으면NoChange 는 이미 그 API 로 선언돼 있으면 patch 없이
// 성공하는지 본다(4절 표 — 값 같음).
func TestSwitchAllocationAPI_값같으면NoChange(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp", ResourceVersion: "1"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDRA}},
		},
	}
	c := newTestClient(t, ncp)
	result, err := c.SwitchAllocationAPI(context.Background(), VendorNvidia, npuv1alpha1.AdvertiseByDRA)
	if err != nil {
		t.Fatalf("SwitchAllocationAPI: %v", err)
	}
	if !result.NoChange {
		t.Errorf("이미 dra 인데 NoChange 가 아님: %+v", result)
	}
	var got npuv1alpha1.NPUClusterPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "ncp"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResourceVersion != "1" {
		t.Errorf("NoChange 인데 patch 됨: %s", got.ResourceVersion)
	}
}

// TestSwitchAllocationAPI_nvidia는patch된다 는 정상 전환이 spec.nvidia.advertiseBy 만
// 바꾸고 다른 벤더는 손대지 않는지 본다.
func TestSwitchAllocationAPI_nvidia는patch된다(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ncp"}}
	c := newTestClient(t, ncp)
	result, err := c.SwitchAllocationAPI(context.Background(), VendorNvidia, npuv1alpha1.AdvertiseByDRA)
	if err != nil {
		t.Fatalf("SwitchAllocationAPI: %v", err)
	}
	if result.Previous != npuv1alpha1.AdvertiseByDevicePlugin || result.New != npuv1alpha1.AdvertiseByDRA ||
		result.NoChange {
		t.Errorf("결과 요약 불일치: %+v", result)
	}
	var got npuv1alpha1.NPUClusterPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "ncp"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Nvidia.AdvertiseBy != npuv1alpha1.AdvertiseByDRA {
		t.Errorf("spec.nvidia.advertiseBy 가 patch 되지 않음: %+v", got.Spec.Nvidia)
	}
	if got.Spec.Furiosa.AdvertiseBy != "" || got.Spec.Furiosa.Rngd.AdvertiseBy != "" {
		t.Errorf("nvidia 전환이 furiosa 블록을 건드림: %+v", got.Spec.Furiosa)
	}
}

// TestSwitchAllocationAPI_rngd는furiosa와분리 는 furiosa 서브벤더 rngd 전환이
// spec.furiosa.rngd.advertiseBy 만 바꾸고 spec.furiosa(Warboy) 자신의 블록은 그대로인지
// 본다 — 두 블록은 서로 다른 AdvertiseSpec 을 각자 갖는다(allocation.go declare() 와
// 같은 다섯 갈래).
func TestSwitchAllocationAPI_rngd는furiosa와분리(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDevicePlugin},
			},
		},
	}
	c := newTestClient(t, ncp)
	if _, err := c.SwitchAllocationAPI(context.Background(), VendorRngd, npuv1alpha1.AdvertiseByDRA); err != nil {
		t.Fatalf("SwitchAllocationAPI: %v", err)
	}
	var got npuv1alpha1.NPUClusterPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "ncp"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Furiosa.Rngd.AdvertiseBy != npuv1alpha1.AdvertiseByDRA {
		t.Errorf("spec.furiosa.rngd.advertiseBy 가 patch 되지 않음: %+v", got.Spec.Furiosa.Rngd)
	}
	if got.Spec.Furiosa.AdvertiseBy != npuv1alpha1.AdvertiseByDevicePlugin {
		t.Errorf("rngd 전환이 furiosa(Warboy) 자신의 블록을 건드림: %+v", got.Spec.Furiosa)
	}
}

// family 라벨만 있는 노드도 NodeDeviceReport 의 장치 모델이 있으면 모호로 남기지 않고
// 해당 product 로 가른다(설계 결정 29).
func TestCollectAllocationStatusFamilyLabelResolvedByNodeDeviceReport(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ncp"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{Enabled: true, Unified: true, Rngd: npuv1alpha1.RngdSpec{Enabled: true}},
		},
	}
	n1 := labeledNode("n1", map[string]string{"kcloud.ai/furiosa-family.present": labelValueTrue})
	ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	ndr.Spec.NodeName = "n1"
	ndr.Status.Devices = []npuv1alpha1.DeviceEntry{{Vendor: "furiosa", Model: "rngd", Count: 1}}

	c := newTestClient(t, ncp, &n1, ndr)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectAllocationStatus: %v", err)
	}
	rngd := findAllocation(rows, VendorRngd)
	if len(rngd.AmbiguousProductNodes) != 0 || len(rngd.DevicePluginNodes) != 1 || rngd.DevicePluginNodes[0] != "n1" {
		t.Fatalf("NDR 가 rngd 라고 말하는데 모호로 남았다: %+v", rngd)
	}
	furiosa := findAllocation(rows, VendorFuriosa)
	if len(furiosa.AmbiguousProductNodes) != 0 || len(furiosa.DevicePluginNodes) != 0 {
		t.Fatalf("Warboy 행에 rngd 노드가 들어갔다: %+v", furiosa)
	}
}

// Warboy·RNGD 가 한 노드에 섞여도(통합 DaemonSet) 둘 다 present 여야 한다 — 모호가 아니다.
func TestVendorPresent_MixedFuriosaNodeIsNotAmbiguous(t *testing.T) {
	n := labeledNode("n1", map[string]string{npuv1alpha1.FuriosaFamilyPresentLabel: labelValueTrue})
	models := []string{"rngd-ca22", "warboy"}
	for vendor, want := range map[string]bool{VendorRngd: true, VendorFuriosa: true} {
		present, ambiguous := vendorPresent(n, vendor, models)
		if present != want || ambiguous {
			t.Errorf("%s: present=%v ambiguous=%v, 기대 present=%v ambiguous=false", vendor, present, ambiguous, want)
		}
	}
}

// family 라벨만 있고 NodeDeviceReport 모델 관측도 없으면(둘 다 없음) 그제서야 모호다.
func TestVendorPresent_FamilyOnlyWithoutModelsIsAmbiguous(t *testing.T) {
	n := labeledNode("n1", map[string]string{npuv1alpha1.FuriosaFamilyPresentLabel: labelValueTrue})
	present, ambiguous := vendorPresent(n, VendorRngd, nil)
	if present || !ambiguous {
		t.Fatalf("관측 없는 family-only 노드는 모호여야 한다: present=%v ambiguous=%v", present, ambiguous)
	}
}

// Warboy 만 있는 노드에서 rngd 를 물으면 부재지 모호가 아니다 — 다른 제품 관측이 있으므로.
func TestVendorPresent_OtherProductOnlyIsAbsentNotAmbiguous(t *testing.T) {
	n := labeledNode("n1", map[string]string{npuv1alpha1.FuriosaFamilyPresentLabel: labelValueTrue})
	present, ambiguous := vendorPresent(n, VendorRngd, []string{"warboy"})
	if present || ambiguous {
		t.Fatalf("Warboy 만 있는 노드에서 rngd 는 부재(모호 아님)여야 한다: present=%v ambiguous=%v", present, ambiguous)
	}
}

// furiosaProductsFromModels 는 모델 목록이 가리키는 제품 집합을 돌려준다 — 혼재 시 둘 다 담긴다.
func TestFuriosaProductsFromModels(t *testing.T) {
	single := map[string][]string{
		VendorRngd:    {"rngd"},
		productWarboy: {"warboy"},
	}
	for want, models := range single {
		got := furiosaProductsFromModels(models)
		if len(got) != 1 || !got[want] {
			t.Errorf("furiosaProductsFromModels(%v) = %v, want {%q: true}", models, got, want)
		}
	}
	got := furiosaProductsFromModels([]string{"rngd", "warboy"})
	if !got[VendorRngd] || !got[productWarboy] || len(got) != 2 {
		t.Errorf("혼재 입력은 두 제품 모두 담겨야 한다: %v", got)
	}
	if got := furiosaProductsFromModels(nil); len(got) != 0 {
		t.Errorf("빈 목록은 빈 집합이어야 한다: %v", got)
	}
}

// TestPreviewAllocationSwitch_쓰지않음 은 Preview 가 patch 없이 같은 요약을 내는지 본다.
func TestPreviewAllocationSwitch_쓰지않음(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ncp", ResourceVersion: "1"}}
	c := newTestClient(t, ncp)
	result, err := c.PreviewAllocationSwitch(context.Background(), VendorNvidia, npuv1alpha1.AdvertiseByDRA)
	if err != nil {
		t.Fatalf("PreviewAllocationSwitch: %v", err)
	}
	if result.Previous != npuv1alpha1.AdvertiseByDevicePlugin || result.New != npuv1alpha1.AdvertiseByDRA {
		t.Errorf("결과 요약 불일치: %+v", result)
	}
	var got npuv1alpha1.NPUClusterPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "ncp"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResourceVersion != "1" {
		t.Errorf("Preview 가 patch 를 실행함: %s", got.ResourceVersion)
	}
}

func TestCollectAllocationStatusReadsAdvertiseSwitch(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ncp"}}
	ncp.Spec.Nvidia.Enabled = true
	ncp.Status.AdvertiseSwitch = []npuv1alpha1.AdvertiseSwitchStatus{
		{
			Vendor: VendorNvidia, Node: "n1", Mode: "dra", Applied: false,
			Reason: "장치를 쥔 워크로드가 있다", BlockingPods: []string{"default/train"},
		},
		{Vendor: VendorNvidia, Node: "n2", Mode: "dra", Applied: true},
	}
	c := newTestClient(t, ncp)
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	nv := findAllocation(rows, VendorNvidia)
	if len(nv.Switch) != 2 || nv.Switch[0].Node != "n1" || nv.Switch[0].Applied ||
		nv.Switch[0].Reason == "" || !nv.Switch[1].Applied {
		t.Fatalf("status.advertiseSwitch 가 행에 옮겨지지 않았다: %+v", nv.Switch)
	}
}
