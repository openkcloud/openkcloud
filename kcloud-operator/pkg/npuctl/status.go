// status.go: pkg/npuctl 상태 집약(aggregation) 로직
// 상세: NPUClusterPolicy/DriverInstallPolicy/NodeDeviceReport/DriverUpgradeState(CR)와
// core/v1 Node(allocatable)를 한 번에 조회해 ClusterStatus로 합친다. kubectl-npu
// status --json 및 사람용 텍스트 출력이 공통으로 사용하는 단일 진입점이다.
// 생성일: 2026-07-16 | 수정일: 2026-10-27
package npuctl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// nodeExcludedLabel·nodeExcludedReasonLabel 은 internal/controller/node_exclusion.go 가
// 발행하는 라벨 키의 사본이다. 판정은 operator 한 곳(nodeExclusion)에서만 하고, 여기서는
// 그 결과를 읽기만 한다 — 값을 다시 계산하지 않는다.
const (
	nodeExcludedLabel       = "kcloud.ai/excluded"
	nodeExcludedReasonLabel = "kcloud.ai/excluded-reason"
	// labelValueTrue 는 노드 라벨의 참 값이다(internal/controller 의 같은 이름 상수와 동일 관례).
	labelValueTrue = "true"
)

// CollectStatus는 클러스터의 NCP/DIP/NDR/DUS/Node를 조회하여 ClusterStatus로 집약합니다.
// 개별 리소스 조회 실패는 서로 독립적으로 처리하되(부분 결과라도 반환), 전부 실패하면 에러를 반환합니다.
func (c *Client) CollectStatus(ctx context.Context) (*ClusterStatus, error) {
	var ncps npuv1alpha1.NPUClusterPolicyList
	ncpErr := c.c.List(ctx, &ncps)

	var dips npuv1alpha1.DriverInstallPolicyList
	dipErr := c.c.List(ctx, &dips)

	var ndrs npuv1alpha1.NodeDeviceReportList
	ndrErr := c.c.List(ctx, &ndrs)

	var duses npuv1alpha1.DriverUpgradeStateList
	dusErr := c.c.List(ctx, &duses)

	var nodes corev1.NodeList
	nodeErr := c.c.List(ctx, &nodes)

	if ncpErr != nil && dipErr != nil && ndrErr != nil && dusErr != nil && nodeErr != nil {
		return nil, fmt.Errorf("모든 리소스 조회 실패: ncp=%v dip=%v ndr=%v dus=%v node=%v",
			ncpErr, dipErr, ndrErr, dusErr, nodeErr)
	}

	status := &ClusterStatus{}

	vendorIndex := buildVendorIndex(ncps.Items)
	attachDIPs(vendorIndex, dips.Items)
	status.Vendors = flattenVendorIndex(vendorIndex)

	status.ClusterPolicies = buildClusterPolicyStatuses(ncps.Items)

	status.Nodes = buildNodeStatuses(ndrs.Items, duses.Items, nodes.Items)

	return status, nil
}

// buildVendorIndex는 NPUClusterPolicy(보통 단일 인스턴스, 다중이면 첫 값 우선)로부터
// 벤더별(nvidia/furiosa/rngd/rebellions/tenstorrent) enabled/이미지 정보를 채운 인덱스를 만듭니다.
func buildVendorIndex(ncps []npuv1alpha1.NPUClusterPolicy) map[string]*VendorStatus {
	idx := map[string]*VendorStatus{
		VendorNvidia:      {Name: VendorNvidia},
		VendorFuriosa:     {Name: VendorFuriosa},
		VendorRngd:        {Name: VendorRngd, SubVendorOf: VendorFuriosa},
		VendorRebellions:  {Name: VendorRebellions},
		VendorTenstorrent: {Name: VendorTenstorrent},
	}
	if len(ncps) == 0 {
		return idx
	}
	// 여러 NPUClusterPolicy가 존재할 수 있으나(이론상), 관리 API 1단계는 클러스터에 하나만
	// 배포되는 현재 운영 관행을 따라 첫 항목을 채택한다. 2단계(REST)에서 다중 정책 지원 시 재검토.
	spec := ncps[0].Spec

	idx[VendorNvidia].Enabled = spec.Nvidia.Enabled
	idx[VendorNvidia].DevicePluginImage = spec.Nvidia.DevicePluginImage

	idx[VendorFuriosa].Enabled = spec.Furiosa.Enabled
	idx[VendorFuriosa].DevicePluginImage = spec.Furiosa.DevicePluginImage

	idx[VendorRngd].Enabled = spec.Furiosa.Rngd.Enabled
	idx[VendorRngd].DevicePluginImage = spec.Furiosa.Rngd.DevicePluginImage

	idx[VendorRebellions].Enabled = spec.Rebellions.Enabled
	idx[VendorRebellions].DevicePluginImage = spec.Rebellions.DevicePluginImage

	idx[VendorTenstorrent].Enabled = spec.Tenstorrent.Enabled
	idx[VendorTenstorrent].DevicePluginImage = spec.Tenstorrent.DevicePluginImage

	return idx
}

// vendorKeyForDIP는 DriverInstallPolicy.spec.vendor/model로부터 vendorIndex 키를 결정합니다.
// furiosa 벤더 + model=="rngd" 는 별도 "rngd" 서브벤더로 분류합니다(NCP.spec.furiosa.rngd와 대응).
func vendorKeyForDIP(spec npuv1alpha1.DriverInstallPolicySpec) string {
	if spec.Vendor == VendorFuriosa && spec.Model == VendorRngd {
		return VendorRngd
	}
	return spec.Vendor
}

func attachDIPs(idx map[string]*VendorStatus, dips []npuv1alpha1.DriverInstallPolicy) {
	for i := range dips {
		dip := dips[i]
		key := vendorKeyForDIP(dip.Spec)
		entry, ok := idx[key]
		if !ok {
			// NCP에 없는(미지원/미구성) 벤더의 DIP는 별도 항목으로 노출한다(누락 방지).
			entry = &VendorStatus{Name: key}
			idx[key] = entry
		}
		var up npuv1alpha1.UpgradePolicy
		if dip.Spec.UpgradePolicy != nil {
			up = *dip.Spec.UpgradePolicy
		}
		entry.DriverInstallPolicies = append(entry.DriverInstallPolicies, DIPStatus{
			Name:             dip.Name,
			Vendor:           dip.Spec.Vendor,
			Model:            dip.Spec.Model,
			DesiredVersion:   dip.Spec.Driver.Version,
			Installer:        dip.Spec.Driver.Installer,
			VerifiedVersions: dip.Spec.VerifiedVersions,
			AllowDowngrade:   dip.Spec.Driver.AllowDowngrade,
			VersionSource:    dip.Spec.Driver.VersionSource,
			AutoUpgrade:      up.AutoUpgrade,
			DrainEnabled:     up.DrainEnabled,
			TrackOnly:        dip.Spec.Driver.TrackOnly,
		})
	}
}

func flattenVendorIndex(idx map[string]*VendorStatus) []VendorStatus {
	names := make([]string, 0, len(idx))
	for name := range idx {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]VendorStatus, 0, len(names))
	for _, name := range names {
		entry := *idx[name]
		sort.Slice(entry.DriverInstallPolicies, func(i, j int) bool {
			return entry.DriverInstallPolicies[i].Name < entry.DriverInstallPolicies[j].Name
		})
		out = append(out, entry)
	}
	return out
}

func buildClusterPolicyStatuses(ncps []npuv1alpha1.NPUClusterPolicy) []ClusterPolicyStatus {
	const (
		vendorOperatorCondSuffix     = "OperatorReady"
		vendorDevicePluginCondSuffix = "DevicePluginReady"
		readyCondType                = "Ready"
	)
	out := make([]ClusterPolicyStatus, 0, len(ncps))
	for i := range ncps {
		ncp := ncps[i]
		cp := ClusterPolicyStatus{
			Namespace: ncp.Namespace,
			Name:      ncp.Name,
			Phase:     ncp.Status.Phase,
		}
		for _, cond := range ncp.Status.Conditions {
			if cond.Type == readyCondType {
				cp.Ready = string(cond.Status)
				cp.ReadyReason = cond.Reason
				cp.ReadyMessage = cond.Message
				continue
			}
			if strings.HasSuffix(cond.Type, vendorDevicePluginCondSuffix) {
				cp.DevicePlugins = append(cp.DevicePlugins, VendorConditionStatus{
					Vendor:  strings.ToLower(strings.TrimSuffix(cond.Type, vendorDevicePluginCondSuffix)),
					Status:  string(cond.Status),
					Reason:  cond.Reason,
					Message: cond.Message,
				})
				continue
			}
			if strings.HasSuffix(cond.Type, vendorOperatorCondSuffix) {
				cp.VendorOperators = append(cp.VendorOperators, VendorOperatorStatus{
					Vendor:  strings.ToLower(strings.TrimSuffix(cond.Type, vendorOperatorCondSuffix)),
					Status:  string(cond.Status),
					Reason:  cond.Reason,
					Message: cond.Message,
				})
			}
		}
		sort.Slice(cp.VendorOperators, func(i, j int) bool {
			return cp.VendorOperators[i].Vendor < cp.VendorOperators[j].Vendor
		})
		sort.Slice(cp.DevicePlugins, func(i, j int) bool {
			return cp.DevicePlugins[i].Vendor < cp.DevicePlugins[j].Vendor
		})
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func buildNodeStatuses(
	ndrs []npuv1alpha1.NodeDeviceReport,
	duses []npuv1alpha1.DriverUpgradeState,
	nodes []corev1.Node,
) []NodeStatus {
	byName := map[string]*NodeStatus{}
	order := []string{}

	ensure := func(name string) *NodeStatus {
		if ns, ok := byName[name]; ok {
			return ns
		}
		ns := &NodeStatus{Name: name}
		byName[name] = ns
		order = append(order, name)
		return ns
	}

	for i := range ndrs {
		ndr := ndrs[i]
		ns := ensure(ndr.Spec.NodeName)
		ns.PassthroughReserved = ndr.Status.PassthroughReserved
		for _, d := range ndr.Status.Devices {
			// 모델 미판정은 감추지 않고 "generic" 으로 드러낸다.
			product := d.Vendor + "/" + d.Model
			if d.Model == "" {
				product = d.Vendor + "/generic"
			}
			ns.Devices = append(ns.Devices, DeviceStatus{
				Vendor:        d.Vendor,
				Model:         d.Model,
				Count:         d.Count,
				DriverLoaded:  d.DriverLoaded,
				DriverVersion: d.DriverVersion,
				DriverBinding: d.DriverBinding,
				NeedsReboot:   d.NeedsReboot,
				PCIeAddress:   d.PCIeAddress,
				Product:       product,
			})
		}
	}

	for i := range duses {
		dus := duses[i]
		ns := ensure(dus.Spec.NodeName)
		ns.Upgrades = append(ns.Upgrades, UpgradeStatus{
			Name:              dus.Name,
			Vendor:            dus.Spec.Vendor,
			Model:             dus.Spec.Model,
			State:             dus.Status.State,
			CurrentVersion:    dus.Status.CurrentVersion,
			DesiredVersion:    dus.Status.DesiredVersion,
			Message:           dus.Status.Message,
			Retries:           dus.Status.Retries,
			RolledBackVersion: dus.Status.RolledBackVersion,
			Models:            dus.Status.ObservedModels,
		})
	}

	for i := range nodes {
		node := nodes[i]
		ns := ensure(node.Name)
		if node.Labels[nodeExcludedLabel] == labelValueTrue {
			ns.Excluded = true
			ns.ExcludedReason = node.Labels[nodeExcludedReasonLabel]
		}
		if len(node.Status.Allocatable) == 0 {
			continue
		}
		ns.Allocatable = make(map[string]string, len(node.Status.Allocatable))
		for res, qty := range node.Status.Allocatable {
			ns.Allocatable[string(res)] = qty.String()
		}
	}

	sort.Strings(order)
	out := make([]NodeStatus, 0, len(order))
	for _, name := range order {
		ns := *byName[name]
		sort.Slice(ns.Upgrades, func(i, j int) bool { return ns.Upgrades[i].Name < ns.Upgrades[j].Name })
		out = append(out, ns)
	}
	return out
}
