// ============================================================
// translate.go: 추상 사용자 의도 → 벤더 device-plugin 리소스 번역
// 상세: 유일한 번역 함수다 — admission webhook 과 컨트롤러가 같은 함수를 부른다. 순수 함수이므로
// 클러스터 조회는 호출부(Load)가 미리 한다. 실패하면 항상 *Reject 를 돌려주고, 조용히 갈아끼우지 않는다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"fmt"
	"sort"
	"strings"

	"kcloud-operator/api/v1alpha1"
)

// Request 는 번역 입력이다(클래스와 워크로드를 합쳐 평평하게 만든 것).
type Request struct {
	ClassName        string
	Mappings         []v1alpha1.AcceleratorMapping
	Access           v1alpha1.AccessSpec
	Requirements     v1alpha1.AcceleratorRequirements
	VendorPreference []string
	AllocationAPI    string
}

// Result 는 번역 결과다. 이것이 그대로 Pod 의 resource limit + nodeAffinity 가 되고,
// AcceleratorWorkload.status.resolved 에 그대로 실린다 — 그래서 별도 타입을 만들지 않고
// CRD 의 ResolvedAllocation 을 그대로 쓴다(필드 복사 계층이 하나 줄고, 어긋날 일도 없다).
type Result = v1alpha1.ResolvedAllocation

// BuildRequest 는 워크로드와 클래스를 번역 입력으로 접는다.
// 워크로드 요구사항은 클래스를 완화하지 못한다(MergeRequirements 가 강한 쪽을 남긴다).
func BuildRequest(aw *v1alpha1.AcceleratorWorkload, class *v1alpha1.AcceleratorClass) Request {
	acc := aw.Spec.Accelerator
	req := Request{
		ClassName:     class.Name,
		Mappings:      class.Spec.Mappings,
		Access:        acc.Access,
		Requirements:  MergeRequirements(class.Spec.Requirements, acc.Requirements),
		AllocationAPI: v1alpha1.AllocationAPIAuto,
	}
	if acc.Preferences != nil {
		req.VendorPreference = acc.Preferences.Vendors
		if acc.Preferences.AllocationAPI != "" {
			req.AllocationAPI = acc.Preferences.AllocationAPI
		}
	}
	return req
}

// Translate 는 요청을 벤더 리소스로 옮긴다. 실패 시 error 는 항상 *Reject 다.
// DRA 가용성을 모르는 호출부(아직 Load 전환 전)를 위해 빈 DRACapability 로 위임한다.
func Translate(req Request, snap []NodeCapability) (*Result, error) {
	return TranslateWithDRA(req, snap, DRACapability{})
}

// TranslateWithDRA 는 Translate 와 같지만 DRA 가용성을 함께 받는다 — webhook·컨트롤러·미리보기가
// 같은 DRACapability 를 넘겨야 세 경로가 같은 판정을 한다.
func TranslateWithDRA(req Request, snap []NodeCapability, dra DRACapability) (*Result, error) {
	api := req.AllocationAPI
	if api == "" || api == v1alpha1.AllocationAPIAuto {
		api = v1alpha1.AllocationAPIDevicePlugin
	}
	if api == v1alpha1.AllocationAPIDRA {
		return translateDRA(req, snap, dra)
	}
	if api != v1alpha1.AllocationAPIDevicePlugin {
		// dra 사유를 아무 값에나 붙이지 않는다 — CRD enum 이 오늘 막아 주지만 그 enum 은 다른
		// 파일에 있고 이 분기와 이어진 것이 없다.
		return nil, &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonBackendUnsupported,
			Message: fmt.Sprintf("allocationAPI %q is not a known allocation API; use devicePlugin or auto", api)}
	}

	mappings := orderMappings(req.Mappings, req.VendorPreference)
	if len(mappings) == 0 {
		return nil, &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonNoVendorMapping,
			Message: fmt.Sprintf("AcceleratorClass %s declares no vendor mapping", req.ClassName)}
	}
	// admission webhook 이 이미 벤더 중복을 막지만 failurePolicy=Ignore 라 웹훅이 죽으면
	// 통과할 수 있다 — 여기서도 걸러야 "선택이 비결정적" 인 클래스가 조용히 첫 매핑으로
	// 정해지는 일이 없다.
	if dup := duplicateVendor(mappings); dup != "" {
		return nil, &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonNoVendorMapping,
			Message: fmt.Sprintf("AcceleratorClass %s: vendor %q appears more than once, which would make the vendor choice non-deterministic", req.ClassName, dup)}
	}

	var firstReject *Reject
	for _, m := range mappings {
		name, err := ResourceFor(m, req.Access.Mode)
		if err != nil {
			// 리소스명을 정할 수 없는 매핑이다(furiosa product 모호). 노드를 하나도 못 보므로
			// 노드 루프 밖에서 잡고, 고칠 필드를 그대로 지목한다.
			if firstReject == nil {
				firstReject = &Reject{Axis: AxisProduct, Reason: v1alpha1.AWReasonNoVendorMapping, Message: err.Error()}
			}
			continue
		}
		if name == "" {
			// 카탈로그에 없는 벤더다(CRD enum 은 통과했지만 productResource 에 없음). 거절을
			// 남기지 않으면 이 매핑이 조용히 0개 노드를 내고 사용자는 클러스터를 가리키는
			// NoCandidateNodes 를 받는다 — 고칠 곳은 클래스다.
			if firstReject == nil {
				firstReject = &Reject{Axis: AxisProduct, Reason: v1alpha1.AWReasonNoVendorMapping,
					Message: fmt.Sprintf("vendor %q has no known device-plugin resource name", m.Vendor)}
			}
			continue
		}
		nodes := make([]string, 0, len(snap))
		for _, nc := range snap {
			if !nodeIsCandidateFor(nc, m, name) {
				continue
			}
			// 두 게이트를 모두 통과해야 후보다 — 한쪽 통과는 판정이 아니다.
			if rj := CheckMode(nc, req.Access, m); rj != nil {
				if firstReject == nil {
					firstReject = rj
				}
				continue
			}
			if rj := CheckRequirements(nc, req.Requirements, req.Access, m); rj != nil {
				if firstReject == nil {
					firstReject = rj
				}
				continue
			}
			nodes = append(nodes, nc.NodeName)
		}
		if len(nodes) == 0 {
			continue
		}
		sort.Strings(nodes)
		vendor := strings.ToLower(m.Vendor)
		return &Result{
			Vendor:       vendor,
			Mode:         req.Access.Mode,
			ResourceName: name,
			// 시분할 replica 는 독립 장치가 아니다 — 공유 모드에서도 Pod 이 요청하는 것은 몫 1개다.
			// replicas 는 장치를 몇 갈래로 나눴는지일 뿐이므로 Explanation 이 그 사실을 말한다.
			Quantity:      1,
			AllocationAPI: api,
			Nodes:         nodes,
			Explanation:   explain(vendor, m, req.Access, nodes),
		}, nil
	}
	// 후보가 없으면 "노드가 없다" 로 뭉개지 말고 실제로 실패한 축을 그대로 올린다.
	if firstReject != nil {
		return nil, firstReject
	}
	return nil, &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonNoCandidateNodes,
		Message: fmt.Sprintf("no schedulable node advertises an accelerator for any vendor mapped by AcceleratorClass %s (cordoned nodes are not candidates — check whether every candidate is under a driver upgrade)", req.ClassName)}
}

// nodeIsCandidateFor 는 이 노드가 이 매핑의 후보일 수 있는지 본다. 벤더만 보고 거르면 같은 벤더
// 아래 다른 제품의 노드까지 들어와, 그 노드의 사소한 거절이 firstReject 를 선점해 진짜 후보의
// 거절을 가린다(예: furiosa 아래 rngd 요청에 warboy 노드가 섞이면 <nvidia-node> 이 <rngd-node> 의 사유를 가림).
// 그래서 벤더가 아니라 리소스로 거른다: (a) 이미 이 리소스를 광고하거나, (b) 아직 광고하지 않지만
// (governed-pending) 전체 장치 리소스는 광고한다 — 이 경우 CheckMode 의 ProfileNotApplied 같은 구체적
// 축이 열리도록 남긴다. 전체 장치조차 광고하지 않는 노드의 거절은 이 매핑에서 볼 가치가 없다.
func nodeIsCandidateFor(nc NodeCapability, m v1alpha1.AcceleratorMapping, resolved string) bool {
	if nc.Advertised[resolved] > 0 {
		return true
	}
	whole, err := WholeDeviceResource(m.Vendor, m.Product)
	return err == nil && whole != "" && nc.Advertised[whole] > 0
}

// duplicateVendor 는 두 번 나오는 첫 벤더를 돌려준다(없으면 ""). 대소문자를 구분하지 않는다
// (webhook 의 validateAcceleratorClass 와 동일한 판정).
func duplicateVendor(mappings []v1alpha1.AcceleratorMapping) string {
	seen := make(map[string]bool, len(mappings))
	for _, m := range mappings {
		vendor := strings.ToLower(strings.TrimSpace(m.Vendor))
		if seen[vendor] {
			return vendor
		}
		seen[vendor] = true
	}
	return ""
}

// orderMappings 는 선호 벤더를 앞으로 당긴다. 선호에만 있고 매핑에 없는 벤더는 무시하고,
// 선호에 없는 매핑도 뒤에 남긴다 — 선호는 요구가 아니다.
func orderMappings(mappings []v1alpha1.AcceleratorMapping, preference []string) []v1alpha1.AcceleratorMapping {
	out := make([]v1alpha1.AcceleratorMapping, 0, len(mappings))
	taken := make(map[int]bool, len(mappings))
	for _, want := range preference {
		for i, m := range mappings {
			if taken[i] || !strings.EqualFold(m.Vendor, want) {
				continue
			}
			out = append(out, m)
			taken[i] = true
		}
	}
	for i, m := range mappings {
		if !taken[i] {
			out = append(out, m)
		}
	}
	return out
}

// explain 은 사람이 읽는 근거다. 공유 결과는 격리가 없다는 사실을 반드시 말한다(정직성 원칙).
// shared 분기가 "time-sliced" 라고 단정할 수 있는 것은 checkSharing 이 SharingMode 를
// timeSliced 로 못박아 통과시키기 때문이다(mode.go:132). 그 게이트가 multiProcess/brokered 로
// 넓어지는 날 이 문구도 access.Implementation 기준으로 좁혀야 한다.
func explain(vendor string, m v1alpha1.AcceleratorMapping, access v1alpha1.AccessSpec, nodes []string) string {
	switch access.Mode {
	case v1alpha1.AccessModeShared:
		return fmt.Sprintf("%s whole device on %v, shared by %d time-sliced replicas; replicas contend for compute and memory and are not isolated from each other",
			vendor, nodes, access.Replicas)
	case v1alpha1.AccessModePartitioned:
		return fmt.Sprintf("%s partition %s on %v", vendor, m.NativeProfile, nodes)
	case v1alpha1.AccessModePartitionedShared:
		return fmt.Sprintf("%s partition %s on %v, shared by %d replicas; replicas inside one partition are not isolated from each other",
			vendor, m.NativeProfile, nodes, access.Replicas)
	default:
		return fmt.Sprintf("%s whole device on %v, exclusive", vendor, nodes)
	}
}

// translateDRA 는 DRA 경로 번역이다. device-plugin 경로와 달리 extended resource 를 쓰지 않고
// DeviceClass 를 가리킨다 — 둘을 동시에 채우면 렌더러가 같은 장치를 두 번 요청한다.
func translateDRA(req Request, snap []NodeCapability, dra DRACapability) (*Result, error) {
	if !dra.APIServed {
		return nil, &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonDRANotEnabled,
			Message: "allocationAPI \"dra\" requires resource.k8s.io on this cluster; use devicePlugin or auto"}
	}
	// device-plugin 경로가 CheckMode·CheckRequirements 로 보는 축을 이 경로는 하나도 보지
	// 않는다. 그대로 통과시키면 status.resolved.mode 가 요청대로 배치된 것처럼 보고하고,
	// webhook 의 공유 경고(격리 없음)도 사실이 아닌 DRA Explanation 으로 대체된다 —
	// 이 파일이 금지하는 조용한 대체 그 자체다. 구현하지 않은 축은 구현하지 않았다고 말한다.
	if rj := rejectUnsupportedDRAAxes(req); rj != nil {
		return nil, rj
	}
	mappings := orderMappings(req.Mappings, req.VendorPreference)
	if len(mappings) == 0 {
		return nil, &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonNoVendorMapping,
			Message: fmt.Sprintf("AcceleratorClass %s declares no vendor mapping", req.ClassName)}
	}
	var firstReject *Reject
	for _, m := range mappings {
		if err := ValidateDRAMapping(m); err != nil {
			return nil, &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonDRAMappingMissing, Message: err.Error()}
		}
		if m.DeviceClassName == "" {
			if firstReject == nil {
				firstReject = &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonDRAMappingMissing,
					Message: fmt.Sprintf("AcceleratorClass %s vendor %q has no deviceClassName; DRA is not configured for this vendor", req.ClassName, m.Vendor)}
			}
			continue
		}
		if !dra.DeviceClasses[m.DeviceClassName] {
			if firstReject == nil {
				firstReject = &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonDRADriverMissing,
					Message: fmt.Sprintf("DeviceClass %q does not exist; install the DRA driver for vendor %q", m.DeviceClassName, m.Vendor)}
			}
			continue
		}
		// device-plugin 경로와 같은 규율: Pod 이 요청하는 것은 항상 몫 1개다.
		// 공유 replica 는 장치를 몇 갈래로 나눴는지일 뿐 독립 장치가 아니다.
		const want = int32(1)
		// 이 벤더가 device-plugin 으로 광고할 전체 장치 리소스명이다 — 알아야 이중 광고 노드를
		// 걸러낼 수 있다. 못 정하면(furiosa 처럼 product 가 모호) 빈 문자열로 넘기지 않는다 —
		// 그러면 이중 광고 배제가 통째로 꺼진 채 노드가 후보가 된다(fail-open). device-plugin
		// 경로(위 ResourceFor 실패 분기)와 같은 축·사유로 거절하고 다음 매핑으로 넘어간다.
		resourceName, err := WholeDeviceResource(m.Vendor, m.Product)
		if err != nil {
			if firstReject == nil {
				firstReject = &Reject{Axis: AxisProduct, Reason: v1alpha1.AWReasonNoVendorMapping, Message: err.Error()}
			}
			continue
		}
		nodes, doubled := draCandidateNodes(snap, m.DRADriver, resourceName, want)
		if len(nodes) == 0 {
			// 이중 광고로 전멸한 경우까지 NoCandidateNodes 로 말하면 거짓말이다 — 그 노드는 장치를 내는 중이다
			// (<rngd-node> 이 draDevices 1개를 내는 중에 이 메시지를 받은 사례가 있다). 사용자는 멀쩡한 DRA 드라이버를
			// 다시 깔러 가고, 진짜 고칠 곳(그 노드의 device-plugin 광고)은 그대로 남는다. 이 사유만은 이미 쌓인
			// NoCandidateNodes 를 덮는다 — 첫 매치 유지가 원칙이지만, 노드 이름·충돌 리소스·할 일을 다 가진
			// 쪽이 더 구체적이다. 다른 구체적 사유(DRADriverMissing 등)는 덮지 않는다 — 그쪽도 이미 고칠 곳을 지목한다.
			if len(doubled) > 0 {
				if firstReject == nil || firstReject.Reason == v1alpha1.AWReasonNoCandidateNodes {
					firstReject = &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonDRADoubleAdvertised,
						Message: fmt.Sprintf("DRA driver %q publishes devices on node(s) %s, but the vendor device plugin advertises the same devices there (conflicting extended resource in parentheses); the scheduler would see one physical device as two independent resources, so those nodes are not DRA candidates — hand those nodes to DRA by setting the vendor block's advertiseBy to dra in NPUClusterPolicy (the operator then labels the nodes and the device plugin steps back), or set spec.accelerator.preferences.allocationAPI to devicePlugin",
							m.DRADriver, strings.Join(doubled, ", "))}
				}
				continue
			}
			if firstReject == nil {
				firstReject = &Reject{Axis: AxisCandidates, Reason: v1alpha1.AWReasonNoCandidateNodes,
					Message: fmt.Sprintf("no node publishes at least %d device(s) for DRA driver %q", want, m.DRADriver)}
			}
			continue
		}
		return &Result{
			Vendor:          m.Vendor,
			Mode:            req.Access.Mode,
			AllocationAPI:   v1alpha1.AllocationAPIDRA,
			DeviceClassName: m.DeviceClassName,
			Quantity:        want, // 항상 1 — AccessSpec 에 개수 축은 없다
			Nodes:           nodes,
			Explanation:     fmt.Sprintf("DRA: DeviceClass %q, driver %q, %d device(s)", m.DeviceClassName, m.DRADriver, want),
		}, nil
	}
	return nil, firstReject
}

// rejectUnsupportedDRAAxes 는 DRA 경로가 구현하지 않은 요청 축을 거절한다. 노드가 아니라
// 요청만 보므로 매핑 루프 밖에서 한 번만 판정한다.
// 모드와 requirements 를 다른 메시지로 나누는 이유는 다음에 할 일이 서로 다르기 때문이다 —
// 공유는 "격리 없음" 이라는 사실 자체가 검증되지 않은 채 배치되는 것이 문제이고, 분할은
// nativeProfile 이 통째로 무시되어 사용자가 요청한 것과 다른 크기의 장치를 받는 것이 문제다.
func rejectUnsupportedDRAAxes(req Request) *Reject {
	const remedy = "set spec.accelerator.preferences.allocationAPI to devicePlugin (or auto)"
	switch req.Access.Mode {
	case v1alpha1.AccessModePartitioned, v1alpha1.AccessModePartitionedShared:
		// 이것이 가장 위험한 조용한 대체다: DeviceClass 는 자기가 고른 장치를 줄 뿐
		// nativeProfile 을 요청하지 않는데, 통과시키면 status 는 partitioned 라고 보고한다.
		return &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonDRAUnsupportedRequest,
			Message: fmt.Sprintf("allocationAPI %q does not implement partitioning: it requests a device from the DeviceClass and never asks for mappings[].nativeProfile, so mode %q would hand out whatever device the DeviceClass selects; %s, or point the class at a DeviceClass that selects the partition itself",
				v1alpha1.AllocationAPIDRA, req.Access.Mode, remedy)}
	case v1alpha1.AccessModeShared:
		return &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonDRAUnsupportedRequest,
			Message: fmt.Sprintf("allocationAPI %q does not implement sharing: access.replicas and the device's verified sharing capability are not evaluated on this path, so mode %q cannot be confirmed and the workload would get no warning that replicas are not isolated from each other; %s",
				v1alpha1.AllocationAPIDRA, req.Access.Mode, remedy)}
	}
	if fields := requestedRequirements(req.Requirements); len(fields) > 0 {
		return &Reject{Axis: AxisAllocationAPI, Reason: v1alpha1.AWReasonDRAUnsupportedRequest,
			Message: fmt.Sprintf("allocationAPI %q does not evaluate spec.accelerator.requirements (%s): the DRA path picks nodes by DeviceClass and device count only, so these requirements would be silently unenforced; %s, or drop the requirements",
				v1alpha1.AllocationAPIDRA, strings.Join(fields, ", "), remedy)}
	}
	return nil
}

// requestedRequirements 는 실제로 채워진 requirements 필드를 사람이 읽는 형태로 나열한다
// (없으면 빈 슬라이스). 클래스에서 상속된 값도 포함된다 — MergeRequirements 가 이미 접어 둔 값이기 때문이다.
func requestedRequirements(req v1alpha1.AcceleratorRequirements) []string {
	var out []string
	if req.MinimumIsolation != "" {
		out = append(out, fmt.Sprintf("minimumIsolation=%s", req.MinimumIsolation))
	}
	if req.MinimumMemory != nil {
		out = append(out, fmt.Sprintf("minimumMemory=%s", req.MinimumMemory))
	}
	return out
}

// draCandidateNodes 는 그 드라이버로 요청 수 이상을 내놓는 노드다(stale 제외, 정렬). resourceName 으로
// 벤더를 되짚을 수 있으면 이중 광고 노드(같은 벤더를 device-plugin 으로도 광고 중)도 뺀다.
// 둘째 반환값은 그렇게 빠진 노드를 "node (충돌 리소스)" 로 적은 목록이다(정렬) — 필터 결과만 돌려주면
// 호출부가 "0개" 와 "0개인데 이유가 있다" 를 구분 못 하고 사실과 반대인 NoCandidateNodes 를 낸다.
// 노드 이름·충돌 리소스는 여기서만 알 수 있어 여기서 짓는다 — 구조체를 새로 만들 만큼의 데이터는 아니다.
func draCandidateNodes(snap []NodeCapability, driver string, resourceName string, want int32) (nodes []string, doubleAdvertised []string) {
	// 전체 장치 리소스명 하나만 보면 두 곳이 샌다: MIG 로 쪼갠 노드는 nvidia.com/gpu 를 광고하지
	// 않고 nvidia.com/mig-* 만 광고하며, 광고량 0(플러그인 재시작·전 장치 unhealthy)인 노드는
	// 키가 살아 있는데도 통과한다. 벤더로 되짚어 "이 벤더의 device-plugin 이 이 노드에서
	// 광고하고 있는가" 를 묻는다 — 그것이 같은 실리콘을 두 번 나눠 주게 되는 조건이다.
	vendor := VendorForResource(resourceName)
	for i := range snap {
		if snap[i].Stale {
			continue
		}
		// 장치 수를 먼저 본다 — 애초에 이 드라이버로 장치를 안 내는 노드까지 이중 광고로
		// 세면 "장치를 내는데 배제됐다" 는 메시지가 거짓이 된다.
		if snap[i].DRADevices[driver] < want {
			continue
		}
		// 같은 벤더 장치가 device-plugin 으로도 광고 중이면 스케줄러가 같은 하드웨어를 두 자원으로 본다.
		// 배타 요청 두 개가 장치 하나에 겹칠 수 있으므로 후보에서 뺀다. 광고 주체가 이미 DRA 로 넘어간
		// 벤더라면 device-plugin 은 그 노드에서 물러나 있다 — allocatable 의 키는 0 으로 굳은 잔재이므로
		// 이중 광고가 아니다. 이 갈래가 없으면 스위치를 켜도 그 노드는 영원히 DRA 후보가 되지 못한다.
		conflict := ""
		if vendor != "" && !snap[i].DRAOwnedVendors[vendor] {
			conflict = vendorResourceOn(snap[i].Advertised, vendor)
		}
		if conflict != "" {
			doubleAdvertised = append(doubleAdvertised, fmt.Sprintf("%s (%s)", snap[i].NodeName, conflict))
			continue
		}
		nodes = append(nodes, snap[i].NodeName)
	}
	sort.Strings(nodes)
	sort.Strings(doubleAdvertised)
	return nodes, doubleAdvertised
}

// vendorResourceOn 은 그 벤더의 device-plugin 리소스 중 이 노드에 광고된 첫 이름이다(없으면 "").
// 개수가 아니라 키의 존재를 본다 — 0 은 "장치가 없다" 가 아니라 "플러그인이 지금 0 을
// 보고 있다" 이고, 그 플러그인은 복구되면 같은 실리콘을 다시 광고한다.
// 조각 리소스(nvidia.com/mig-*)도 VendorForResource 가 같은 벤더로 되짚으므로 함께 걸린다.
// 여러 개면 사전순 첫 이름만 말한다 — 사용자가 어느 플러그인을 꺼야 하는지 알기엔 충분하고,
// map 순회 순서가 메시지를 실행마다 흔들지 않게 한다.
func vendorResourceOn(advertised map[string]int32, vendor string) string {
	out := ""
	for name := range advertised {
		if VendorForResource(name) == vendor && (out == "" || name < out) {
			out = name
		}
	}
	return out
}
