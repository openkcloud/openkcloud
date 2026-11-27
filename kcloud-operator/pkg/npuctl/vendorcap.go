// vendorcap.go: 벤더 능력 뷰(명세서 층 + 성적표 층) — CLI·REST 공유
// 상세: 명세서(internal/vendorcap)에 evidence·ACPP 를 조인해 벤더×능력 Verification 을
// 유도한다. 조인 규칙은 이 정본이다. 순수 함수(Derive)는 클러스터 없이
// 시험하고, 수집 함수(CollectWith)는 client.Reader 로 CLI/REST 가 함께 쓴다.
// 생성일: 2026-09-19 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/vendorcap"
	"kcloud-operator/internal/verification"
)

// VerificationDegraded 는 검증 뒤 광고가 무너진 상태다(v1alpha1 의 세 값에 하나 추가).
const VerificationDegraded = "degraded"

// productWarboy 는 Furiosa 의 두 product 중 하나다. RNGD 와 달리 pkg/npuctl/types.go 에
// vendor-key 상수(VendorRngd 같은)가 없어 여기 하나 둔다.
const productWarboy = "warboy"

// VendorCapabilityRow 는 한 벤더×제품×능력 칸이다.
type VendorCapabilityRow struct {
	Vendor     string `json:"vendor"`
	Product    string `json:"product,omitempty"`
	Capability string `json:"capability"`
	Declared   string `json:"declared"`
	// Assurance 는 이 선언을 얼마나 확인했는지의 등급이다(none|unit|cluster|hardware,
	// internal/vendorcap.Support.Assurance()). Verified 는 evidence·ACPP 를 조인해 유도한
	// 실측 성적표라 기준이 다르다 — 둘 다 있어야 "구현했다"와 "확인했다"가 섞이지 않는다.
	Assurance string `json:"assurance"`
	Verified  string `json:"verified"`
	Reason    string `json:"reason,omitempty"`
	Source    string `json:"source,omitempty"`
}

// furiosaProduct 는 노드 라벨만으로 Furiosa product 를 정한다 — furiosa.present 자체가
// Warboy 노드 라벨이다(api/v1alpha1.PresentNodeLabel 이 이 라벨 문자열의 단일 출처이며
// furiosa(Warboy)·rngd 를 별도 키로 쓴다고 명시한다). rngd.present 가 있으면 "rngd",
// furiosa.present 가 있으면 "warboy", 그 둘 다 없이 furiosa-family.present 만 있으면
// ""(product 미상 — 모든 product 에 적용). "kcloud.ai/warboy.present" 는
// node-device-detector 가 쓰지 않는 라벨이라 보지 않는다.
func furiosaProduct(n corev1.Node) string {
	switch {
	case n.Labels[v1alpha1.PresentNodeLabel(VendorRngd)] == labelValueTrue:
		return VendorRngd
	case n.Labels[v1alpha1.PresentNodeLabel(VendorFuriosa)] == labelValueTrue:
		return productWarboy
	}
	return ""
}

// nodeVendorProduct 는 노드 라벨"만"으로 (vendor, product) 를 정한다 — evidence 의
// Spec.Vendor 가 비어 있을 때만 쓰는 폴백이다(evidenceVendorProduct). 한 노드에 여러 벤더
// present 라벨이 같이 실리면(예 <nvidia-node> 의 nvidia+furiosa) 먼저 매치되는 벤더로
// 떨어지는 모호함이 있다 — 그래서 evidence 는 이 폴백보다 Spec.Vendor 를 우선한다.
// kcloud.ai/accelerator.present(옛 gpu.present) 는 여러 벤더가 함께 쓰는 범용 라벨이라 판정에 쓰지 않는다.
func nodeVendorProduct(n corev1.Node) (vendor, product string) {
	has := func(v string) bool { return n.Labels[v1alpha1.PresentNodeLabel(v)] == labelValueTrue }
	familyOnly := n.Labels[v1alpha1.FuriosaFamilyPresentLabel] == labelValueTrue
	switch {
	case has(VendorNvidia):
		return VendorNvidia, ""
	case has(VendorRngd), has(VendorFuriosa), familyOnly:
		return VendorFuriosa, furiosaProduct(n)
	case has(VendorRebellions):
		return VendorRebellions, ""
	case has(VendorTenstorrent):
		return VendorTenstorrent, ""
	}
	return "", ""
}

// evidenceVendorProduct 는 이 evidence 가 말하는 (vendor,product) 다.
// AcceleratorEvidenceSpec.Vendor 를 컨트롤러가 채워 두면 그 값을 그대로 믿고, 비어
// 있을 때만 노드 라벨 폴백(nodeVendorProduct)으로 떨어진다. product 는 항상 라벨
// 기반이다(furiosaProduct) — Spec.Vendor 에는 product 축이 없다.
func evidenceVendorProduct(ev *v1alpha1.AcceleratorEvidence, n corev1.Node) (vendor, product string) {
	if ev.Spec.Vendor == "" {
		return nodeVendorProduct(n)
	}
	vendor = ev.Spec.Vendor
	if vendor == VendorFuriosa {
		product = furiosaProduct(n)
	}
	return vendor, product
}

// nodeDRAOwned 는 (vendor,product) 의 DRA-owned 광고 라벨을 본다. furiosa 는 product 에
// 따라 advertise 벤더 키가 다르다 — internal/controller/npuclusterpolicy_advertise.go 의
// advertiseVendors 에 furiosa(Warboy)·rngd(RNGD) 가 따로 있어, Warboy 노드엔
// kcloud.ai/furiosa.dra-owned, RNGD 노드엔 kcloud.ai/rngd.dra-owned 가 붙는다. product 를
// 모르면("") 둘 중 아무거나로 판정한다.
func nodeDRAOwned(n corev1.Node, vendor, product string) bool {
	if vendor != VendorFuriosa {
		return n.Labels[v1alpha1.DRAOwnedNodeLabel(vendor)] == labelValueTrue
	}
	rngdOwned := n.Labels[v1alpha1.DRAOwnedNodeLabel(VendorRngd)] == labelValueTrue
	furiosaOwned := n.Labels[v1alpha1.DRAOwnedNodeLabel(VendorFuriosa)] == labelValueTrue
	switch product {
	case VendorRngd:
		return rngdOwned
	case productWarboy:
		return furiosaOwned
	default:
		return rngdOwned || furiosaOwned
	}
}

// nodeDRARetired 는 (vendor,product) 의 dra-retired 라벨을 본다 — nodeDRAOwned 와 같은
// furiosa/rngd 분기(라벨 키가 갈린다). 되돌린 뒤에도 DRA 드라이버 파드가
// 아직 그 노드를 떠나지 않았거나 막 떠난 직후라는 뜻이라 읽기 전용으로 표시만 한다.
func nodeDRARetired(n corev1.Node, vendor, product string) bool {
	if vendor != VendorFuriosa {
		return n.Labels[v1alpha1.DRARetiredNodeLabel(vendor)] == labelValueTrue
	}
	rngdRetired := n.Labels[v1alpha1.DRARetiredNodeLabel(VendorRngd)] == labelValueTrue
	furiosaRetired := n.Labels[v1alpha1.DRARetiredNodeLabel(VendorFuriosa)] == labelValueTrue
	switch product {
	case VendorRngd:
		return rngdRetired
	case productWarboy:
		return furiosaRetired
	default:
		return rngdRetired || furiosaRetired
	}
}

// findCond 는 타입이 일치하는 첫 조건을 돌려준다(없으면 ok=false).
func findCond(conds []metav1.Condition, t string) (metav1.Condition, bool) {
	for _, c := range conds {
		if c.Type == t {
			return c, true
		}
	}
	return metav1.Condition{}, false
}

func condTrue(conds []metav1.Condition, t string) bool {
	c, ok := findCond(conds, t)
	return ok && c.Status == metav1.ConditionTrue
}

// vk 는 성적표 집계 키다. product=="" 는 "이 벤더의 모든 product 에 적용" 을 뜻하고, 최종
// 조회에서 (vendor,product,cap) 과 (vendor,"",cap) 를 OR 로 합친다.
type vk struct{ vendor, product, cap string }

// DeriveVendorCapabilities 는 순수 조인이다. fresh 는 evidence 를 지금 믿어도 되는지 판정한다.
func DeriveVendorCapabilities(
	evs []v1alpha1.AcceleratorEvidence, acpps []v1alpha1.AcceleratorPartitionPolicy,
	nodes []corev1.Node, fresh func(*v1alpha1.AcceleratorEvidence, corev1.Node) bool,
) []VendorCapabilityRow {
	nodeByName := map[string]corev1.Node{}
	for _, n := range nodes {
		nodeByName[n.Name] = n
	}
	verified := map[vk]bool{}
	degraded := map[vk]bool{}
	applyEvidence(evs, nodeByName, fresh, verified)
	for _, p := range acpps {
		applyACPP(p, verified, degraded)
	}
	return buildCapabilityRows(verified, degraded)
}

// applyEvidence 는 evidence 로 devicePlugin/dra/discovery/driver 를 채운다.
func applyEvidence(
	evs []v1alpha1.AcceleratorEvidence, nodeByName map[string]corev1.Node,
	fresh func(*v1alpha1.AcceleratorEvidence, corev1.Node) bool, verified map[vk]bool,
) {
	for i := range evs {
		ev := &evs[i]
		n, ok := nodeByName[ev.Spec.NodeName]
		if !ok {
			n, ok = nodeByName[ev.Name]
		}
		if !ok || !fresh(ev, n) {
			continue
		}
		vendor, product := evidenceVendorProduct(ev, n)
		if vendor == "" {
			continue
		}
		switch ev.Status.Level {
		case v1alpha1.EvidenceLevelAllocationVerified:
			if nodeDRAOwned(n, vendor, product) {
				verified[vk{vendor, product, string(vendorcap.CapDRA)}] = true
			} else {
				verified[vk{vendor, product, string(vendorcap.CapDevicePlugin)}] = true
			}
			fallthrough
		case v1alpha1.EvidenceLevelFunctionallyVerified:
			verified[vk{vendor, product, string(vendorcap.CapDiscovery)}] = true
			verified[vk{vendor, product, string(vendorcap.CapDriver)}] = true
		}
	}
}

// applyACPP 는 한 ACPP 가 요청한 partition/sharing 능력을 target 조건에 따라 채운다.
// Degraded 는 광고 붕괴 감시라 파티션·공유를 구분하지 않고 요청된 능력 전부에 적용한다.
// Verified=True·layout 있음은 항상 partition Verified 다 — 컨트롤러는 partition 이 Ready
// 인 뒤에만 sharing 을 얹으므로, reason=ReasonSharingVerified 도 partition 검증을
// 함의한다. 그 reason 일 때만 sharing 능력도 Verified 로 본다(SetStatusCondition 이
// 자리를 덮어써 reason 하나만 남는다) — "sharing 검증"은 "partition 검증"을 포함한다.
func applyACPP(p v1alpha1.AcceleratorPartitionPolicy, verified, degraded map[vk]bool) {
	vendor := p.Spec.Vendor
	hasLayout := len(p.Spec.Layout) > 0
	sharingCap, hasSharing := acppSharingCapability(p.Spec.Sharing)
	if !hasLayout && !hasSharing {
		return
	}
	// 조건은 오브젝트 최상위가 아니라 노드별 target 의 Conditions 에 실린다(controller 의
	// setCond, acceleratorpartitionpolicy_controller.go:1401). 여러 target 중 하나라도
	// Degraded 면 그 능력은 Degraded, 그 외 하나라도 Verified 조건에 맞으면 Verified 다
	// (buildCapabilityRows 가 조회 시 Degraded 를 우선한다).
	for _, ts := range p.Status.Targets {
		if condTrue(ts.Conditions, v1alpha1.ACPPCondDegraded) {
			if hasLayout {
				degraded[vk{vendor, "", string(vendorcap.CapPartition)}] = true
			}
			if hasSharing {
				degraded[vk{vendor, "", string(sharingCap)}] = true
			}
		}
		c, ok := findCond(ts.Conditions, v1alpha1.ACPPCondVerified)
		if !ok || c.Status != metav1.ConditionTrue {
			continue
		}
		if hasLayout {
			verified[vk{vendor, "", string(vendorcap.CapPartition)}] = true
		}
		if hasSharing && c.Reason == v1alpha1.ReasonSharingVerified {
			verified[vk{vendor, "", string(sharingCap)}] = true
		}
	}
}

// acppSharingCapability 는 spec.sharing.mode 를 vendorcap 능력으로 옮긴다(없거나 모르는
// mode 면 ok=false).
func acppSharingCapability(s *v1alpha1.SharingSpec) (vendorcap.Capability, bool) {
	if s == nil {
		return "", false
	}
	switch strings.ToLower(s.Mode) {
	case strings.ToLower(v1alpha1.SharingModeTimeSliced):
		return vendorcap.CapTimeSlicing, true
	case strings.ToLower(v1alpha1.SharingModeMPS):
		return vendorcap.CapMultiProcess, true
	}
	return "", false
}

// buildCapabilityRows 는 명세서 층(vendorcap.Keys/Capabilities/Lookup) 위에 verified/degraded
// 집계를 얹어 정렬된 행을 만든다(Unsupported/Unknown·그 외).
func buildCapabilityRows(verified, degraded map[vk]bool) []VendorCapabilityRow {
	var rows []VendorCapabilityRow
	for _, k := range vendorcap.Keys() {
		for _, c := range vendorcap.Capabilities() {
			s, ok := vendorcap.Lookup(k.Vendor, k.Product, c)
			if !ok {
				continue
			}
			row := VendorCapabilityRow{Vendor: k.Vendor, Product: k.Product, Capability: string(c),
				Declared: string(s.State), Assurance: string(s.Assurance()), Reason: s.Reason, Source: s.Source}
			exact := vk{k.Vendor, k.Product, string(c)}
			anyProduct := vk{k.Vendor, "", string(c)}
			switch {
			case s.State == vendorcap.StateUnsupported || s.State == vendorcap.StateUnknown:
				row.Verified = v1alpha1.VerificationNotApplicable
			case degraded[exact] || degraded[anyProduct]:
				row.Verified = VerificationDegraded
			case verified[exact] || verified[anyProduct]:
				row.Verified = v1alpha1.VerificationVerified
			default:
				row.Verified = v1alpha1.VerificationRequired
			}
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Vendor != rows[j].Vendor {
			return rows[i].Vendor < rows[j].Vendor
		}
		if rows[i].Product != rows[j].Product {
			return rows[i].Product < rows[j].Product
		}
		return rows[i].Capability < rows[j].Capability
	})
	return rows
}

// CollectVendorCapabilitiesWith 는 클러스터를 읽어 뷰를 만든다. CLI 와 REST 가 같이 쓴다.
// freshness 는 기존 verification.CheckFreshness 를 그대로 재사용하고, 감시축은
// AcceleratorVerificationPolicy 를 노드 라벨로 해석해 정한다(verification.Resolve) — 운영자가
// 설치한 정책을 이 뷰만 무시하면 같은 evidence 를 컨트롤러는 신선하다고, 이 뷰는 낡았다고
// 보게 된다. 현재 환경 지문은 internal/verification/fingerprint.go 의 빌더로 만든다.
func CollectVendorCapabilitiesWith(ctx context.Context, r client.Reader) ([]VendorCapabilityRow, error) {
	var evs v1alpha1.AcceleratorEvidenceList
	if err := r.List(ctx, &evs); err != nil && !meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("list acceleratorevidences: %w", err)
	}
	var acpps v1alpha1.AcceleratorPartitionPolicyList
	if err := r.List(ctx, &acpps); err != nil && !meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("list acceleratorpartitionpolicies: %w", err)
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	var ndrs v1alpha1.NodeDeviceReportList
	if err := r.List(ctx, &ndrs); err != nil && !meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("list nodedevicereports: %w", err)
	}
	// 실효 검증 정책은 노드마다 다르다 — AcceleratorVerificationPolicy 의 nodeSelector 가
	// 노드를 고른다. 목록은 여기서 한 번만 읽고 노드별 해석은 아래 fresh 클로저가 한다.
	var policies v1alpha1.AcceleratorVerificationPolicyList
	if err := r.List(ctx, &policies); err != nil && !meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("list acceleratorverificationpolicies: %w", err)
	}
	// NDR 은 Spec.NodeName·ObjectMeta.Name 어느 쪽으로 조회해도 찾도록 두 키 모두에 등록한다
	// — 관례상 둘은 같은 값이지만, 어긋난 데이터가 와도 안전하게 찾는다.
	ndrByNode := map[string]*v1alpha1.NodeDeviceReport{}
	for i := range ndrs.Items {
		n := &ndrs.Items[i]
		if n.Spec.NodeName != "" {
			ndrByNode[n.Spec.NodeName] = n
		}
		if n.Name != "" {
			ndrByNode[n.Name] = n
		}
	}
	now := time.Now()
	fresh := func(ev *v1alpha1.AcceleratorEvidence, n corev1.Node) bool {
		ndr := ndrByNode[n.Name]
		if ndr == nil {
			ndr = &v1alpha1.NodeDeviceReport{}
		}
		// evidenceVendorProduct 로 vendor 를 정한다 — 다중 벤더 노드에서 라벨 순서만으로
		// 고르면 엉뚱한 벤더 장치의 driver/firmware 버전과 비교하게 된다. product 는 이
		// 지문 계산엔 쓰지 않는다.
		vendor, _ := evidenceVendorProduct(ev, n)
		// Resolve + Compute + CheckFreshness 는 ACPP monitorDrift
		// (acceleratorpartitionpolicy_controller.go:808-815)와 같은 호출이다 — 감시축에서
		// Generation 을 빼는 것 하나만 다르다(withoutGeneration 주석).
		pol := verification.Resolve(policies.Items, n.Labels)
		cur := verification.Compute(&n, ndr, vendor, ev.Status.Fingerprint.Generation)
		verdict, _ := verification.CheckFreshness(ev, now, cur, withoutGeneration(pol.InvalidateOn))
		return verification.IsFresh(verdict)
	}
	return DeriveVendorCapabilities(evs.Items, acpps.Items, nodes.Items, fresh), nil
}

// withoutGeneration 은 감시축에서 Generation 을 뺀다 — 위 Compute 호출에 evidence 자신의
// generation(ev.Status.Fingerprint.Generation)을 그대로 넣으므로 cur.Generation 은 항상 old 와
// 같아져 이 축으로는 무효화가 트리거될 수 없다. 그 사실을 감시 목록에서 빼서 의도를 명시한다.
// monitorDrift 는 ACPP 의 generation 을 넣으므로 이 제외가 없다 — 두 호출의 유일한 차이다.
func withoutGeneration(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != verification.FieldGeneration {
			out = append(out, f)
		}
	}
	return out
}

// CollectVendorCapabilities 는 Client 편의 메서드다.
func (c *Client) CollectVendorCapabilities(ctx context.Context) ([]VendorCapabilityRow, error) {
	return CollectVendorCapabilitiesWith(ctx, c.c)
}

// capabilityRefusal 은 명세서 층 선언(s)이 Unsupported·Unknown 이면 거절 에러를 낸다 —
// partition apply·sharing apply·allocation switch 가 patch 전에 공유하는 판정이다(팀리더
// 지시: vendorcap 레지스트리를 직접 판정하지 말고 이미 있는 등급을 따른다). s 는 순수값
// 이라 클러스터 없이 시험할 수 있다 — LookupVendor 호출부(각 명령)와 이 판정을 분리해
// "레지스트리가 어떻게 채워졌든 이 판정 자체는 옳은가" 를 따로 확인한다.
func capabilityRefusal(vendor, action string, s vendorcap.Support) error {
	if s.State == vendorcap.StateUnsupported || s.State == vendorcap.StateUnknown {
		return fmt.Errorf("vendor=%q 는 %s 를 지원하지 않습니다(등급=%s): %s", vendor, action, s.State, s.Reason)
	}
	return nil
}
