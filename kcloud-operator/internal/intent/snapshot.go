// ============================================================
// snapshot.go: 노드별 capability 스냅샷
// 상세: 번역의 유일한 입력. 이미 있는 네 소스 — allocatable(실제 광고), ACPP status(적용 상태·
// capability), NDR(장치 메모리), DRA(ResourceSlice) — 를 접는다. ACPP 가 수렴하지 않은 노드는 stale 로 모든 모드에서 뺀다(fail closed).
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
)

// NodeCapability 는 한 노드가 "지금" 무엇을 줄 수 있는가다.
type NodeCapability struct {
	NodeName string
	// Vendor 는 광고 중인 리소스명에서 되짚은 벤더다(NDR 이 아니라 allocatable 이 진실이다).
	// 단일 벤더 노드용 편의 필드일 뿐이다 — 멀티벤더 노드(예: NVIDIA + Furiosa 카드가 같은
	// 노드에 공존)에서는 리소스명 사전순으로 결정된 값 하나만 담기므로 안전하지 않다. 전체
	// 그림이 필요하면 Advertised 를 VendorForResource 와 함께 순회하라.
	Vendor string
	// Advertised 는 Node.status.allocatable 중 가속기 리소스만이다.
	Advertised map[string]int32
	// MemoryMiB 는 NDR 이 보고한 장치 메모리 중 최솟값이다(0 = 미관측).
	MemoryMiB int64
	// Devices 는 ACPP 가 관측한 장치별 capability 3축이다. ACPP 가 없으면 비어 있다 — 다만
	// ACPP 가 존재하지만 아직 이 노드에 대한 Status.Targets 를 하나도 게시하지 않은 경우도
	// 똑같이 비고 Stale=false 로 남는다(둘은 이 스냅샷에서 구분되지 않는, 의도된 동치다).
	// 이 값이 비어 있다고 "비관리 노드"로 단정하지 말 것 — Advertised 는 그런 경우에도 살아있는
	// Node.status.allocatable 에서 오므로 계속 신뢰할 수 있다.
	Devices []v1alpha1.DeviceStatus
	// SharingMode/SharingReplicas 는 ACPP ApplyRecord 저널이 기록한 "실제로 적용된" 공유 상태다.
	SharingMode     string
	SharingReplicas int32
	// PartitionProfiles 는 ACPP resolvedLayout 이 확정한 profile 이다.
	PartitionProfiles []string
	// Stale 이면 이 노드는 어떤 모드에서도 후보가 아니다.
	Stale       bool
	StaleReason string
	// DRAOwnedVendors 는 이 노드에서 광고 주체가 DRA 로 넘어간 벤더들이다. 그 벤더의
	// device-plugin 은 이 노드에서 물러났으므로, allocatable 에 키가 남아 있어도(0 으로
	// 굳은 잔재) 이중 광고로 보지 않는다.
	DRAOwnedVendors map[string]bool
	// DRADevices 는 이 노드가 DRA 로 내놓는 드라이버별 장치 수다(빈 map = DRA 광고 없음).
	DRADevices map[string]int32
}

// BuildSnapshot 은 노드·ACPP·NDR·DRA 목록을 노드별 capability 로 접는다(순수 함수).
// 가속기를 하나도 광고하지 않는 노드는 후보가 될 수 없으므로 제외한다 — 단, DRA 로만
// 광고하는 노드는 Advertised 가 비어도 DRADevices 가 있으면 살려 둔다(DRA-only 노드가
// 여기서 탈락하면 뒤따르는 모든 DRA 경로가 영원히 후보 0을 본다).
func BuildSnapshot(nodes []corev1.Node, acpps []v1alpha1.AcceleratorPartitionPolicy, ndrs []v1alpha1.NodeDeviceReport, dra DRACapability) []NodeCapability {
	out := make([]NodeCapability, 0, len(nodes))
	for i := range nodes {
		// cordon 된 노드는 여전히 allocatable 을 광고하지만 스케줄러가 Pod 을 올리지 않는다.
		// 후보로 남겨 두면 렌더한 nodeAffinity 가 드라이버 업그레이드로 rmmod 중인 노드에
		// Pod 을 계속 박는다 — 후보에서 뺀다(그 결과 NoCandidateNodes 가 정직한 답이다).
		if nodes[i].Spec.Unschedulable {
			continue
		}
		nc := NodeCapability{NodeName: nodes[i].Name, Advertised: map[string]int32{},
			SharingMode: v1alpha1.SharingModeExclusive, DRAOwnedVendors: draOwnedVendors(nodes[i].Labels)}
		// map 순회 순서는 무작위다 — 리소스명을 정렬해 멀티벤더 노드에서도 Vendor 선택이
		// 실행마다 안정되게 한다.
		names := make([]string, 0, len(nodes[i].Status.Allocatable))
		for name := range nodes[i].Status.Allocatable {
			names = append(names, string(name))
		}
		sort.Strings(names)
		for _, name := range names {
			vendor := VendorForResource(name)
			if vendor == "" {
				continue
			}
			q := nodes[i].Status.Allocatable[corev1.ResourceName(name)]
			nc.Advertised[name] = int32(q.Value())
			if nc.Vendor == "" {
				nc.Vendor = vendor
			}
		}
		nc.DRADevices = dra.SlicesByNodeDriver[nc.NodeName]
		if len(nc.Advertised) == 0 && len(nc.DRADevices) == 0 {
			continue
		}
		nc.MemoryMiB = smallestDeviceMemoryMiB(ndrs, nc.NodeName)
		applyACPPStatus(&nc, acpps)
		applyObservedSharing(&nc, ndrs)
		out = append(out, nc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeName < out[j].NodeName })
	return out
}

// applyACPPStatus 는 이 노드를 타깃하는 ACPP 전체의 상태를 스냅샷에 반영한다. 이름 사전순으로
// 먼저 매치되는 정책만 보면 안 된다 — 사전순 앞의 Failed 정책이 같은 노드의 Ready 정책 관측을
// 가리고 노드 전체가 후보에서 빠질 수 있다(Translate 의 firstReject/nodeHasVendor 와 같은 함정).
// 채택 규칙: 수렴(observedGeneration==generation)+Ready 인 정책이 있으면 그 관측을 쓴다 — 여럿이면
// 이름 사전순 첫 번째(결정적 선택일 뿐 소유권 판정이 아니다). 다른 정책의 실패·미수렴은 채택된
// 관측을 가리지 않는다. Ready 가 하나도 없으면 사전순 첫 비수렴 정책을 사유로 stale 표시한다(fail closed).
func applyACPPStatus(nc *NodeCapability, acpps []v1alpha1.AcceleratorPartitionPolicy) {
	sorted := make([]v1alpha1.AcceleratorPartitionPolicy, len(acpps))
	copy(sorted, acpps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var adopted *v1alpha1.AcceleratorPartitionPolicy
	var adoptedTarget *v1alpha1.TargetStatus
	staleReason := ""
	for i := range sorted {
		a := &sorted[i]
		for j := range a.Status.Targets {
			t := &a.Status.Targets[j]
			if t.NodeName != nc.NodeName {
				continue
			}
			switch {
			case a.Status.ObservedGeneration != a.Generation:
				if staleReason == "" {
					staleReason = fmt.Sprintf("AcceleratorPartitionPolicy %s has not converged (status.observedGeneration=%d, metadata.generation=%d)",
						a.Name, a.Status.ObservedGeneration, a.Generation)
				}
			case t.Phase != v1alpha1.ACPPPhaseReady:
				if staleReason == "" {
					staleReason = fmt.Sprintf("AcceleratorPartitionPolicy %s target %s is in phase %q (not %q)",
						a.Name, t.NodeName, t.Phase, v1alpha1.ACPPPhaseReady)
				}
			default:
				if adopted == nil {
					adopted, adoptedTarget = a, t
				}
			}
		}
	}
	if adopted == nil {
		if staleReason != "" {
			nc.Stale = true
			nc.StaleReason = staleReason
		}
		return
	}
	nc.Devices = adoptedTarget.Devices
	for _, e := range adoptedTarget.ResolvedLayout {
		if e.Profile != "" {
			nc.PartitionProfiles = append(nc.PartitionProfiles, e.Profile)
		}
	}
	for k := range adopted.Status.ApplyRecords {
		rec := &adopted.Status.ApplyRecords[k]
		if rec.NodeName != nc.NodeName || rec.SharingMode == "" {
			continue
		}
		nc.SharingMode = rec.SharingMode
		nc.SharingReplicas = rec.SharingReplicas
	}
}

// applyObservedSharing 은 ACPP 저널이 아니라 실제 광고량으로 "광고 수 > 물리 장치 수" 를 검출한다 —
// 저널만 읽으면 손으로 고친 sharing ConfigMap 이나 ACPP 가 소유하지 않는 기존 설정을 놓쳐 exclusive
// 워크로드가 복제된 장치에 떨어질 수 있다(정직성 원칙 위반). 이 관측은 광고가 물리 장치 수를 넘는다는
// 것만 증명하고 원인(device-plugin 복제 vs 벤더 하드웨어 서브유닛, 예: RNGD 1대가 PE 4개로 광고)은
// 증명하지 못하므로 SharingModeTimeSliced 로 단정하지 않고 Oversubscribed 로만 표시한다 — CheckMode 는
// 그래도 fail-closed 로 exclusive 를 거절한다. 파티션 적용 노드는 대상이 아니다(조각 리소스라 비교 불가).
func applyObservedSharing(nc *NodeCapability, ndrs []v1alpha1.NodeDeviceReport) {
	if nc.Stale || len(nc.PartitionProfiles) > 0 || nc.SharingMode == v1alpha1.SharingModeTimeSliced {
		return
	}
	names := make([]string, 0, len(nc.Advertised))
	for name := range nc.Advertised {
		names = append(names, name)
	}
	// 멀티벤더 노드에서 어느 리소스가 먼저 걸리는지가 SharingReplicas 를 정하므로 순서를 고정한다.
	sort.Strings(names)
	for _, name := range names {
		count := nc.Advertised[name]
		vendor := VendorForResource(name)
		// mig-* 는 조각 리소스라 물리 장치 수와 비교할 대상이 아니다.
		if vendor == "" || strings.HasPrefix(name, migResourcePrefix) {
			continue
		}
		physical := physicalDeviceCount(ndrs, nc.NodeName, vendor)
		// NDR 이 그 벤더 장치를 하나도 보고하지 않으면(detector 미관측 등) 비교할 근거가 없다.
		if physical <= 0 || count <= physical {
			continue
		}
		nc.SharingMode = v1alpha1.SharingModeOversubscribed
		// 나눠떨어지지 않으면 어떤 배수도 실제 장치가 주는 값이 아니다 — 내림한 수를 발표하면
		// 뒤 게이트가 그 값과 비교해 통과시킨다. 미상(0)으로 두면 shared 도 함께 거절된다.
		if count%physical == 0 {
			nc.SharingReplicas = count / physical
		}
		return
	}
}

// physicalDeviceCount 는 NDR 이 보고한 그 벤더의 물리 장치 수다(0 = 관측 없음).
// DeviceEntry 는 (vendor, model) 별 집계 행이므로 Count 를 합산한다.
func physicalDeviceCount(ndrs []v1alpha1.NodeDeviceReport, nodeName, vendor string) int32 {
	var total int32
	for i := range ndrs {
		if ndrs[i].Spec.NodeName != nodeName {
			continue
		}
		for _, d := range ndrs[i].Status.Devices {
			if d.Count > 0 && strings.EqualFold(d.Vendor, vendor) {
				total += d.Count
			}
		}
	}
	return total
}

// smallestDeviceMemoryMiB 는 노드에서 가장 작은 장치 메모리다. 워크로드가 어느 장치에
// 떨어질지 모르므로 최솟값이 보장할 수 있는 유일한 값이다. 0 = 관측 없음.
func smallestDeviceMemoryMiB(ndrs []v1alpha1.NodeDeviceReport, nodeName string) int64 {
	var smallest int64
	for i := range ndrs {
		if ndrs[i].Spec.NodeName != nodeName {
			continue
		}
		for _, d := range ndrs[i].Status.Devices {
			if d.MemoryMiB <= 0 {
				continue
			}
			if smallest == 0 || d.MemoryMiB < smallest {
				smallest = d.MemoryMiB
			}
		}
	}
	return smallest
}

// ApplyHealth 는 배치 후보에서 "지금 쓰면 안 되는" 노드를 뺀다.
//
// health 축은 ACPP 판정 뒤에 얹는다. ACPP 가 준 사유가 더 구체적이므로 먼저 쓰고, 그것이 없을
// 때만 health 사유를 쓴다 — 순서를 뒤집으면 "왜 배치가 안 되는가" 의 답이 항상 뭉뚱그린 health
// 사유로 덮인다. 상태가 비어 있는(아직 판정 전) 노드는 건드리지 않는다: 감시가 아직 안 돈 것을
// "쓰면 안 되는 노드" 로 읽으면 기능 도입이 곧 전면 차단이 된다.
func ApplyHealth(snap []NodeCapability, healths []v1alpha1.AcceleratorHealth) []NodeCapability {
	if len(healths) == 0 {
		return snap
	}
	blocked := make(map[string]v1alpha1.AcceleratorHealthStatus, len(healths))
	for _, h := range healths {
		if h.Status.State != "" && !h.Status.AllocationAllowed {
			blocked[h.Name] = h.Status
		}
	}
	for i := range snap {
		h, ok := blocked[snap[i].NodeName]
		if !ok {
			continue
		}
		snap[i].Stale = true
		if snap[i].StaleReason == "" {
			snap[i].StaleReason = fmt.Sprintf("health %s: %s", h.State, h.Reason)
		}
	}
	// 장치 단위: 고장이 확인된(Unhealthy) 장치만 후보에서 뺀다. 관측 못 한 장치(Unknown)는
	// 빼지 않는다 — 관측이 잠깐 끊긴 순간마다 용량이 출렁이고, 그 구간은 위 노드 축이 이미 막는다.
	// PCI 주소가 없는 장치는 여기서도 걸러낼 수 없다: deviceInputsFromNDR(acceleratorhealth_controller.go)가
	// PCI 없는 장치를 판정 입력에서부터 제외하므로 그런 장치의 고장은 애초에 관측되지 않는다.
	// 회귀는 아니다(기존 노드 단위 driver 신호도 OR 라 이미 이런 장치를 못 봤다) — 다만 이 필터가
	// 메꾸지 못하는 구멍이니, 장치 단위 제외가 전체 커버리지라고 오해하면 안 된다.
	unhealthy := make(map[string]map[string]bool, len(healths))
	for _, h := range healths {
		for _, d := range h.Status.Devices {
			if d.State == "Unhealthy" && d.PCIAddress != "" {
				if unhealthy[h.Name] == nil {
					unhealthy[h.Name] = map[string]bool{}
				}
				unhealthy[h.Name][d.PCIAddress] = true
			}
		}
	}
	for i := range snap {
		bad := unhealthy[snap[i].NodeName]
		if len(bad) == 0 {
			continue
		}
		kept := make([]v1alpha1.DeviceStatus, 0, len(snap[i].Devices))
		for _, d := range snap[i].Devices {
			if !bad[d.PCIAddress] {
				kept = append(kept, d)
			}
		}
		snap[i].Devices = kept
	}
	return snap
}

// Load 는 스냅샷과 함께 그것을 만드는 데 쓴 DRACapability 도 돌려준다 — 호출부가 DRA 번역에도
// 같은 가용성 판정을 그대로 넘겨야(TranslateWithDRA) webhook·컨트롤러·미리보기가 일치한다.
// webhook 과 컨트롤러가 같은 입력으로 같은 판정을 하도록 조회 지점을 여기 하나로 묶는다. 매개변수가
// client.Reader 인 것은 List 만 쓰기 때문이고, apiserver 의 비캐시 직접 reader(internal/apiserver/server.go
// 의 Reader 필드)도 그대로 넘길 수 있어야 한다.
func Load(ctx context.Context, c client.Reader) ([]NodeCapability, DRACapability, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return nil, DRACapability{}, fmt.Errorf("list nodes: %w", err)
	}
	var acpps v1alpha1.AcceleratorPartitionPolicyList
	if err := c.List(ctx, &acpps); err != nil {
		return nil, DRACapability{}, fmt.Errorf("list acceleratorpartitionpolicies: %w", err)
	}
	var ndrs v1alpha1.NodeDeviceReportList
	if err := c.List(ctx, &ndrs); err != nil {
		return nil, DRACapability{}, fmt.Errorf("list nodedevicereports: %w", err)
	}
	// health 는 없을 수도 있다(구버전 배포·CRD 미적용). 그 경우 조용히 건너뛴다 —
	// 감시가 없다는 이유로 배치를 막으면 기능 도입이 곧 장애가 된다.
	var healths v1alpha1.AcceleratorHealthList
	// resource.k8s.io 가 없는 클러스터에서도 device-plugin 경로는 계속 동작해야 한다.
	// List 실패를 오류로 올리면 DRA 미지원 클러스터에서 번역 전체가 죽는다.
	dra, _ := LoadDRACapability(ctx, c)
	if err := c.List(ctx, &healths); err != nil {
		return BuildSnapshot(nodes.Items, acpps.Items, ndrs.Items, dra), dra, nil
	}
	return ApplyHealth(BuildSnapshot(nodes.Items, acpps.Items, ndrs.Items, dra), healths.Items), dra, nil
}

// LoadDRACapability 는 DeviceClass·ResourceSlice 를 읽어 DRA 발행 현황으로 접는다.
// resource.k8s.io 가 없는 클러스터에서도 device-plugin 경로는 계속 동작해야 하므로,
// 조회 실패는 오류가 아니라 "DRA 없음" 이다(APIServed=false).
func LoadDRACapability(ctx context.Context, c client.Reader) (DRACapability, error) {
	empty := DRACapability{
		DeviceClasses: map[string]bool{}, SlicesByNodeDriver: map[string]map[string]int32{},
		PoolsByNodeDriver: map[string]map[string][]string{},
	}
	var classes resourcev1.DeviceClassList
	if err := c.List(ctx, &classes); err != nil {
		return empty, nil
	}
	var slices resourcev1.ResourceSliceList
	if err := c.List(ctx, &slices); err != nil {
		return empty, nil
	}
	return BuildDRACapability(true, classes.Items, slices.Items), nil
}

// draOwnedVendors 는 이 노드에서 광고를 DRA 에 넘긴 벤더들이다. NPUClusterPolicy 의
// advertiseBy 에서 파생된 노드 라벨(kcloud.ai/<vendor>.dra-owned)을 되짚는다.
func draOwnedVendors(nodeLabels map[string]string) map[string]bool {
	const prefix, suffix = "kcloud.ai/", ".dra-owned"
	out := map[string]bool{}
	for k, v := range nodeLabels {
		if v != "true" || !strings.HasPrefix(k, prefix) || !strings.HasSuffix(k, suffix) {
			continue
		}
		out[strings.TrimSuffix(strings.TrimPrefix(k, prefix), suffix)] = true
	}
	return out
}
