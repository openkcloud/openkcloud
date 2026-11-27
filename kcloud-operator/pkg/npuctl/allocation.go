// allocation.go: pkg/npuctl allocation API 노출 상태 조회
// 상세: 어느 API(devicePlugin|dra)가 각 벤더 장치를 광고하는지, 선언(spec.<vendor>.advertiseBy)
// 과 관측(노드의 dra-owned 라벨)을 함께 옮긴다. 둘은 어긋날 수 있다 — dra 로 선언했지만
// 아직 이 노드가 넘어가지 않았거나 거절된 경우다(거절 사유는 status.AdvertiseSwitch).
// 생성일: 2026-09-28 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/vendorcap"
)

// AllocationRow 는 한 벤더의 allocation API 노출 상태다.
type AllocationRow struct {
	Vendor  string `json:"vendor"`
	Enabled bool   `json:"enabled"`
	// Declared 는 spec.<vendor>.advertiseBy 를 해석한 값이다(devicePlugin|dra, AdvertisedBy()).
	// 정책이 없거나 이 벤더가 꺼져 있으면 비워 둔다 — 의미가 없는 값을 지어내지 않는다.
	Declared string `json:"declared,omitempty"`
	// DevicePluginNodes·DRANodes 는 이 벤더 장치가 있는(kcloud.ai/<vendor>.present) 노드 중
	// 각 API 가 실제로 광고 중(관측)인 노드다. 이름순으로 정렬한다.
	DevicePluginNodes []string `json:"devicePluginNodes,omitempty"`
	DRANodes          []string `json:"draNodes,omitempty"`
	// AmbiguousProductNodes 는 furiosa(Warboy)·rngd 에서만 생긴다. 통합 DaemonSet(Unified)
	// 은 개별 present 라벨 대신 자립 라벨 furiosa-family.present 하나로 두 product 노드
	// 모두에 스케줄되므로, 그 라벨만 있는 노드는 어느 product 인지 못 가린다.
	// NodeDeviceReport 의 장치 모델로 가를 수 있으면 가르고, 그래도 확정 못 하면
	// devicePlugin·dra 어느 쪽으로도 단정하지 않고 여기 담는다 — 틀린 벤더에 틀린 API 를
	// 적는 것보다 모른다고 적는 편이 낫다.
	AmbiguousProductNodes []string `json:"ambiguousProductNodes,omitempty"`
	// RetiredNodes 는 이 벤더의 광고를 device-plugin 으로 되돌렸지만 dra.enabled 가 아직
	// true 라 DRA 드라이버 파드가 물러나는 중이거나 물러난 노드다(kcloud.ai/<vendor>.dra-retired,
	// operator 가 붙인다). 라벨을 읽기만 한다 — 여기서 새 판정을 내리지 않는다.
	RetiredNodes []string `json:"retiredNodes,omitempty"`
	// Switch 는 컨트롤러가 기록한 노드별 판정이다(status.advertiseSwitch, 읽기만).
	Switch []AllocationSwitch `json:"switch,omitempty"`
}

// AllocationSwitch 는 NPUClusterPolicy status.advertiseSwitch 의 노드 항목을 그대로 옮긴 것이다.
type AllocationSwitch struct {
	Node    string `json:"node"`
	Mode    string `json:"mode,omitempty"`
	Applied bool   `json:"applied"`
	Reason  string `json:"reason,omitempty"`
}

// CollectAllocationStatusWith 는 클러스터를 읽어 뷰를 만든다. CLI 와 REST 가 같이 쓴다.
// 벤더는 항상 다섯 키(nvidia/furiosa/rngd/rebellions/tenstorrent) 전부를 돌려준다 —
// 정책이 없거나 꺼진 벤더도 빈 결과 대신 Enabled=false 행으로 명시한다.
func CollectAllocationStatusWith(ctx context.Context, r client.Reader) ([]AllocationRow, error) {
	var ncps npuv1alpha1.NPUClusterPolicyList
	if err := r.List(ctx, &ncps); err != nil && !meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("list npuclusterpolicies: %w", err)
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	// 통합 DaemonSet 노드가 family 라벨만 갖는 경우를 위해 NodeDeviceReport 의 furiosa 장치
	// 모델을 노드별로 모아 둔다(없어도 조회는 계속된다 — 그때는 라벨만으로 판정한다).
	var ndrs npuv1alpha1.NodeDeviceReportList
	if err := r.List(ctx, &ndrs); err != nil && !meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("list nodedevicereports: %w", err)
	}
	furiosaModelsByNode := map[string][]string{}
	for _, ndr := range ndrs.Items {
		for _, d := range ndr.Status.Devices {
			if d.Vendor == VendorFuriosa && d.Model != "" {
				furiosaModelsByNode[ndr.Spec.NodeName] = append(furiosaModelsByNode[ndr.Spec.NodeName], strings.ToLower(d.Model))
			}
		}
	}

	rows := map[string]*AllocationRow{
		VendorNvidia:      {Vendor: VendorNvidia},
		VendorFuriosa:     {Vendor: VendorFuriosa},
		VendorRngd:        {Vendor: VendorRngd},
		VendorRebellions:  {Vendor: VendorRebellions},
		VendorTenstorrent: {Vendor: VendorTenstorrent},
	}
	// 다중 NPUClusterPolicy 는 이론상 가능이나 관리 API 1단계는 첫 항목만 본다
	// (status.go buildVendorIndex 와 같은 관례).
	if len(ncps.Items) > 0 {
		spec := ncps.Items[0].Spec
		declare(rows[VendorNvidia], spec.Nvidia.Enabled, spec.Nvidia.AdvertiseSpec)
		declare(rows[VendorFuriosa], spec.Furiosa.Enabled, spec.Furiosa.AdvertiseSpec)
		declare(rows[VendorRngd], spec.Furiosa.Rngd.Enabled, spec.Furiosa.Rngd.AdvertiseSpec)
		declare(rows[VendorRebellions], spec.Rebellions.Enabled, spec.Rebellions.AdvertiseSpec)
		declare(rows[VendorTenstorrent], spec.Tenstorrent.Enabled, spec.Tenstorrent.AdvertiseSpec)

		for _, s := range ncps.Items[0].Status.AdvertiseSwitch {
			row, ok := rows[s.Vendor]
			if !ok {
				continue
			}
			row.Switch = append(row.Switch, AllocationSwitch{Node: s.Node, Mode: s.Mode, Applied: s.Applied, Reason: s.Reason})
		}
	}

	for _, n := range nodes.Items {
		for vendor, row := range rows {
			if !row.Enabled {
				continue
			}
			present, ambiguous := vendorPresent(n, vendor, furiosaModelsByNode[n.Name])
			if ambiguous {
				row.AmbiguousProductNodes = append(row.AmbiguousProductNodes, n.Name)
				continue
			}
			if !present {
				continue
			}
			// furiosa(Warboy)와 rngd 는 dra-owned 라벨 키가 갈라진다 — nodeDRAOwned 에
			// product 를 명시해야 한다("" 를 주면 둘을 OR 로 섞는다, vendorcap.go 참고).
			product := ""
			if vendor == VendorFuriosa {
				product = productWarboy
			}
			if nodeDRAOwned(n, vendor, product) {
				row.DRANodes = append(row.DRANodes, n.Name)
			} else {
				row.DevicePluginNodes = append(row.DevicePluginNodes, n.Name)
			}
			if nodeDRARetired(n, vendor, product) {
				row.RetiredNodes = append(row.RetiredNodes, n.Name)
			}
		}
	}

	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]AllocationRow, 0, len(names))
	for _, name := range names {
		sort.Strings(rows[name].DevicePluginNodes)
		sort.Strings(rows[name].DRANodes)
		sort.Strings(rows[name].AmbiguousProductNodes)
		sort.Strings(rows[name].RetiredNodes)
		out = append(out, *rows[name])
	}
	return out, nil
}

// vendorPresent 는 이 노드에 그 벤더 장치가 있는지를 본다. furiosa(Warboy)·rngd 는
// 자립 라벨 하나로 못 끝난다 — 통합 DaemonSet 노드가 kcloud.ai/furiosa-family.present 만 가질
// 때는 NodeDeviceReport 의 장치 모델(warboy|rngd)로 한 번 더 가른다. 두 제품이 한 노드에 섞여도
// 모호가 아니라 둘 다 present 다 — 관측이 전혀 없을 때만(product 라벨도 모델도 없음)
// present=false·ambiguous=true 다 — 호출부가 어느 API 로도 단정하지 않고 AmbiguousProductNodes 에
// 담는다.
func vendorPresent(n corev1.Node, vendor string, furiosaModels []string) (present, ambiguous bool) {
	if vendor != VendorFuriosa && vendor != VendorRngd {
		return n.Labels[npuv1alpha1.PresentNodeLabel(vendor)] == labelValueTrue, false
	}
	familyOnly := n.Labels[npuv1alpha1.FuriosaFamilyPresentLabel] == labelValueTrue
	products := furiosaProductsFromModels(furiosaModels)
	if p := furiosaProduct(n); p != "" {
		products[p] = true
	}
	want := productWarboy
	if vendor == VendorRngd {
		want = VendorRngd
	}
	switch {
	case products[want]:
		return true, false
	case len(products) > 0:
		return false, false // 다른 제품만 있다 — 모호가 아니라 부재
	default:
		return false, familyOnly
	}
}

// furiosaProductsFromModels 는 NodeDeviceReport 의 furiosa 장치 모델이 가리키는 제품 집합이다
// (모델 슬러그는 rngd·warboy 접두). 혼재 노드는 둘 다 담긴다 — 전에는 섞이면 "" 를 돌려 두 벤더 모두
// 모호로 빠졌는데, 두 제품이 다 있다는 사실은 모호가 아니라 둘 다 present 다.
func furiosaProductsFromModels(models []string) map[string]bool {
	out := map[string]bool{}
	for _, m := range models {
		switch {
		case strings.HasPrefix(m, VendorRngd):
			out[VendorRngd] = true
		case strings.HasPrefix(m, productWarboy):
			out[productWarboy] = true
		}
	}
	return out
}

// declare 는 정책의 벤더 블록 하나를 행에 반영한다. 꺼진 벤더는 Declared 를 비워 둔다 —
// advertiseBy 는 켜진 벤더에만 의미가 있다.
func declare(row *AllocationRow, enabled bool, adv npuv1alpha1.AdvertiseSpec) {
	row.Enabled = enabled
	if enabled {
		row.Declared = adv.AdvertisedBy()
	}
}

// CollectAllocationStatus 는 Client 편의 메서드다.
func (c *Client) CollectAllocationStatus(ctx context.Context) ([]AllocationRow, error) {
	return CollectAllocationStatusWith(ctx, c.c)
}

// AllocationSwitchResult 는 SwitchAllocationAPI/PreviewAllocationSwitch 의 결과 요약이다.
type AllocationSwitchResult struct {
	Vendor   string `json:"vendor"`
	Previous string `json:"previous"`
	New      string `json:"new"`
	NoChange bool   `json:"noChange,omitempty"`
}

// advertiseSpecFor 는 vendor 에 대응하는 NPUClusterPolicy.spec.<vendor>.AdvertiseSpec
// 포인터를 돌려준다 — declare()(위)가 벤더별로 spec.<vendor>.AdvertiseSpec 을 읽는 것과
// 같은 다섯 갈래다(furiosa 서브벤더 rngd 는 spec.furiosa.rngd 에 별도 블록이 있다).
func advertiseSpecFor(spec *npuv1alpha1.NPUClusterPolicySpec, vendor string) (*npuv1alpha1.AdvertiseSpec, error) {
	switch vendor {
	case VendorNvidia:
		return &spec.Nvidia.AdvertiseSpec, nil
	case VendorFuriosa:
		return &spec.Furiosa.AdvertiseSpec, nil
	case VendorRngd:
		return &spec.Furiosa.Rngd.AdvertiseSpec, nil
	case VendorRebellions:
		return &spec.Rebellions.AdvertiseSpec, nil
	case VendorTenstorrent:
		return &spec.Tenstorrent.AdvertiseSpec, nil
	default:
		return nil, fmt.Errorf("알 수 없는 vendor %q (nvidia|furiosa|rngd|rebellions|tenstorrent 중 하나)", vendor)
	}
}

// allocationCapability 는 to(devicePlugin|dra)를 vendorcap 능력으로 옮긴다.
func allocationCapability(to string) (vendorcap.Capability, error) {
	switch to {
	case npuv1alpha1.AdvertiseByDevicePlugin:
		return vendorcap.CapDevicePlugin, nil
	case npuv1alpha1.AdvertiseByDRA:
		return vendorcap.CapDRA, nil
	default:
		return "", fmt.Errorf("알 수 없는 allocation API %q(devicePlugin|dra 중 하나)", to)
	}
}

// resolveAllocationSwitch 는 PreviewAllocationSwitch·SwitchAllocationAPI 가 공유하는
// 해석 단계다. NPUClusterPolicy 를 찾고, vendorcap 레지스트리로 to 를 지원하는지 보고,
// 결과 요약을 만든다. 아무것도 쓰지 않는다.
func (c *Client) resolveAllocationSwitch(ctx context.Context, vendor, to string) (
	*npuv1alpha1.NPUClusterPolicy, *AllocationSwitchResult, error,
) {
	cap, err := allocationCapability(to)
	if err != nil {
		return nil, nil, err
	}
	var ncps npuv1alpha1.NPUClusterPolicyList
	if err := c.c.List(ctx, &ncps); err != nil {
		return nil, nil, fmt.Errorf("NPUClusterPolicy 목록 조회 실패: %w", err)
	}
	if len(ncps.Items) == 0 {
		return nil, nil, fmt.Errorf("NPUClusterPolicy가 클러스터에 없습니다")
	}
	ncp := &ncps.Items[0]
	adv, err := advertiseSpecFor(&ncp.Spec, vendor)
	if err != nil {
		return nil, nil, err
	}
	if err := capabilityRefusal(vendor, to, vendorcap.LookupVendor(vendor, cap)); err != nil {
		return nil, nil, err
	}
	previous := adv.AdvertisedBy()
	result := &AllocationSwitchResult{Vendor: vendor, Previous: previous, New: to, NoChange: previous == to}
	return ncp, result, nil
}

// PreviewAllocationSwitch 는 SwitchAllocationAPI 와 같은 판정을 거쳐 같은 요약을
// 돌려주되 아무것도 patch 하지 않는다.
func (c *Client) PreviewAllocationSwitch(ctx context.Context, vendor, to string) (*AllocationSwitchResult, error) {
	_, result, err := c.resolveAllocationSwitch(ctx, vendor, to)
	return result, err
}

// SwitchAllocationAPI 는 NPUClusterPolicy.spec.<vendor>.advertiseBy 를 to 로 patch 한다 —
// 선언(declare)만 바꾼다. 노드별 실제 전환(라벨 kcloud.ai/<vendor>.dra-owned)은 컨트롤러가
// 판정하고 거절될 수 있다 — 거절 사유는 NPUClusterPolicyStatus.AdvertiseSwitch 에 남으며
// 이 함수는 그 판정을 대신하지 않는다(위 파일 docstring 참고). vendorcap 레지스트리에
// 이 API 가 명백히 미지원인 벤더면 patch 전에 거절한다.
func (c *Client) SwitchAllocationAPI(ctx context.Context, vendor, to string) (*AllocationSwitchResult, error) {
	ncp, result, err := c.resolveAllocationSwitch(ctx, vendor, to)
	if err != nil {
		return nil, err
	}
	if result.NoChange {
		return result, nil
	}
	adv, err := advertiseSpecFor(&ncp.Spec, vendor)
	if err != nil {
		return nil, err
	}
	patch := client.MergeFrom(ncp.DeepCopy())
	adv.AdvertiseBy = to
	if err := c.c.Patch(ctx, ncp, patch); err != nil {
		return nil, fmt.Errorf("NPUClusterPolicy %q patch 실패: %w", ncp.Name, err)
	}
	return result, nil
}
