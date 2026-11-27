// partition.go: pkg/npuctl 파티션(layout)·공유(sharing) 조회 + 적용
// 상세: AcceleratorPartitionPolicy 하나가 파티션·공유 두 축을 함께 담는다. 조회는
// status.targets 로 이미 노드가 해석된 것만 낸다. 적용(apply)은 node+vendor 로 기존
// 정책을 찾아(있으면 그 축만 patch, 없으면 생성) policy.go 의 번역 규칙을 재사용한다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/vendorcap"
)

// PartitionRow 는 한 노드×벤더의 파티션 레이아웃 한 항목이다. profile 이 여럿이면
// 항목마다 한 행이다.
type PartitionRow struct {
	Node           string `json:"node"`
	Vendor         string `json:"vendor"`
	Profile        string `json:"profile,omitempty"`
	CountPerDevice int32  `json:"countPerDevice,omitempty"`
	Phase          string `json:"phase,omitempty"`
}

// SharingRow 는 한 노드×벤더의 공유 상태 한 행이다.
type SharingRow struct {
	Node     string `json:"node"`
	Vendor   string `json:"vendor"`
	Mode     string `json:"mode,omitempty"`
	Replicas int32  `json:"replicas,omitempty"`
	Phase    string `json:"phase,omitempty"`
}

// CollectPartitionStatusWith 는 클러스터를 읽어 파티션 뷰를 만든다. CLI 와 REST 가 같이 쓴다.
// spec.layout 이 있는 ACPP 만 보고, status.targets 로 이미 해석된 노드만 낸다 — 아직
// reconcile 되지 않은 정책은 대상 노드를 이 함수가 직접 풀지 않는다(컨트롤러의 판정을
// 다시 하지 않는다).
func CollectPartitionStatusWith(ctx context.Context, r client.Reader) ([]PartitionRow, error) {
	var acpps npuv1alpha1.AcceleratorPartitionPolicyList
	if err := r.List(ctx, &acpps); err != nil {
		return nil, fmt.Errorf("list acceleratorpartitionpolicies: %w", err)
	}
	var rows []PartitionRow
	for _, p := range acpps.Items {
		if len(p.Spec.Layout) == 0 {
			continue
		}
		for _, ts := range p.Status.Targets {
			for _, l := range p.Spec.Layout {
				rows = append(rows, PartitionRow{
					Node: ts.NodeName, Vendor: p.Spec.Vendor,
					Profile: l.Profile, CountPerDevice: l.CountPerDevice, Phase: ts.Phase,
				})
			}
		}
	}
	return rows, nil
}

// CollectPartitionStatus 는 Client 편의 메서드다.
func (c *Client) CollectPartitionStatus(ctx context.Context) ([]PartitionRow, error) {
	return CollectPartitionStatusWith(ctx, c.c)
}

// CollectSharingStatusWith 는 spec.sharing 이 있는 ACPP 만 본다. 규칙은
// CollectPartitionStatusWith 와 같다.
func CollectSharingStatusWith(ctx context.Context, r client.Reader) ([]SharingRow, error) {
	var acpps npuv1alpha1.AcceleratorPartitionPolicyList
	if err := r.List(ctx, &acpps); err != nil {
		return nil, fmt.Errorf("list acceleratorpartitionpolicies: %w", err)
	}
	var rows []SharingRow
	for _, p := range acpps.Items {
		if p.Spec.Sharing == nil {
			continue
		}
		mode, replicas := sharingModeReplicas(p.Spec.Sharing)
		for _, ts := range p.Status.Targets {
			rows = append(rows, SharingRow{
				Node: ts.NodeName, Vendor: p.Spec.Vendor, Mode: mode, Replicas: replicas, Phase: ts.Phase,
			})
		}
	}
	return rows, nil
}

// CollectSharingStatus 는 Client 편의 메서드다.
func (c *Client) CollectSharingStatus(ctx context.Context) ([]SharingRow, error) {
	return CollectSharingStatusWith(ctx, c.c)
}

// sharingModeReplicas 는 SharingSpec 하나를 (mode, replicas) 로 편다. nil 은
// EffectiveSharingMode() 와 같은 규칙으로 exclusive 다 — 그 메서드를 그대로 불러
// nil-safe 판정을 두 곳에서 따로 두지 않는다.
func sharingModeReplicas(s *npuv1alpha1.SharingSpec) (mode string, replicas int32) {
	mode = (npuv1alpha1.AcceleratorPartitionPolicySpec{Sharing: s}).EffectiveSharingMode()
	if s == nil {
		return mode, 0
	}
	switch mode {
	case npuv1alpha1.SharingModeTimeSliced:
		if s.TimeSlicing != nil {
			replicas = s.TimeSlicing.Replicas
		}
	case npuv1alpha1.SharingModeMPS:
		if s.MPS != nil {
			replicas = s.MPS.Replicas
		}
	}
	return mode, replicas
}

// resolveNodeACPP 는 node·vendor 를 겨냥한 단일 AcceleratorPartitionPolicy 를 찾는다
// (status.targets 로 이미 해석된 것만 — resolveDUS 와 같은 원칙). 0개면 (nil,nil) —
// apply 가 새로 만들 신호다. 2개 이상이면 webhook 의 target-conflict 방지가 정상
// 동작하면 생기지 않지만 방어적으로 모호 에러를 낸다(resolveDUS·resolveDIP 와 같은
// fail-closed 관례). 삭제 처리 중인 정책은 거절한다 — partition·sharing apply 가 검사를
// 각자 복제해 두면 한쪽이 비어도 드러나지 않으므로, 검사를 이 한 곳에 모은다.
func (c *Client) resolveNodeACPP(ctx context.Context, node, vendor string) (
	*npuv1alpha1.AcceleratorPartitionPolicy, error,
) {
	var acpps npuv1alpha1.AcceleratorPartitionPolicyList
	if err := c.c.List(ctx, &acpps); err != nil {
		return nil, fmt.Errorf("list acceleratorpartitionpolicies: %w", err)
	}
	var matches []npuv1alpha1.AcceleratorPartitionPolicy
	for i := range acpps.Items {
		p := acpps.Items[i]
		if p.Spec.Vendor != vendor {
			continue
		}
		for _, ts := range p.Status.Targets {
			if ts.NodeName == node {
				matches = append(matches, p)
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		if matches[0].DeletionTimestamp != nil {
			return nil, fmt.Errorf(
				"policy %q 는 삭제 처리 중이라 patch 가 적용되지 않습니다 — 삭제가 끝난 뒤 다시 시도하세요",
				matches[0].Name)
		}
		return &matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}
		return nil, fmt.Errorf(
			"node=%q vendor=%q 를 겨냥한 AcceleratorPartitionPolicy 가 %d개(%v) 있습니다 — kubectl 로 직접 정리하세요",
			node, vendor, len(matches), names)
	}
}

// checkNodeExists 는 apply 대상 노드가 클러스터에 실제로 있는지 본다 — 없는 노드를
// 겨냥한 정책을 만들지 않는다.
func (c *Client) checkNodeExists(ctx context.Context, node string) error {
	var n corev1.Node
	if err := c.c.Get(ctx, client.ObjectKey{Name: node}, &n); err != nil {
		return fmt.Errorf("node=%q 를 클러스터에서 찾지 못했습니다: %w", node, err)
	}
	return nil
}

// PartitionApplyResult 는 ApplyPartition/PreviewPartitionApply 의 결과 요약이다.
type PartitionApplyResult struct {
	Policy                string `json:"policy"`
	Node                  string `json:"node"`
	Vendor                string `json:"vendor"`
	Profile               string `json:"profile"`
	CountPerDevice        int32  `json:"countPerDevice,omitempty"`
	CurrentProfile        string `json:"currentProfile,omitempty"`
	CurrentCountPerDevice int32  `json:"currentCountPerDevice,omitempty"`
	Created               bool   `json:"created,omitempty"`
	NoChange              bool   `json:"noChange,omitempty"`
}

// resolvePartitionApply 는 PreviewPartitionApply·ApplyPartition 이 공유하는 해석
// 단계다(control.go resolveRollbackTarget 과 같은 원칙) — 대상을 찾고, vendorcap
// 레지스트리로 지원 여부를 보고, 결과 요약을 만든다. 아무것도 쓰지 않는다.
// existing 이 nil 이면 apply 가 새로 만들어야 한다는 뜻이다.
func (c *Client) resolvePartitionApply(ctx context.Context, node, vendor, profile string, countPerDevice int32) (
	existing *npuv1alpha1.AcceleratorPartitionPolicy, result *PartitionApplyResult, err error,
) {
	if profile == "" {
		return nil, nil, fmt.Errorf("--profile 은 비워둘 수 없습니다")
	}
	if err := c.checkNodeExists(ctx, node); err != nil {
		return nil, nil, err
	}
	if err := capabilityRefusal(vendor, "partition", vendorcap.LookupVendor(vendor, vendorcap.CapPartition)); err != nil {
		return nil, nil, err
	}
	existing, err = c.resolveNodeACPP(ctx, node, vendor)
	if err != nil {
		return nil, nil, err
	}
	result = &PartitionApplyResult{Node: node, Vendor: vendor, Profile: profile, CountPerDevice: countPerDevice}
	if existing == nil {
		result.Policy = partitionPolicyName(node, vendor)
		return nil, result, nil
	}
	result.Policy = existing.Name
	if len(existing.Spec.Layout) == 1 {
		result.CurrentProfile = existing.Spec.Layout[0].Profile
		result.CurrentCountPerDevice = existing.Spec.Layout[0].CountPerDevice
	}
	result.NoChange = len(existing.Spec.Layout) == 1 &&
		result.CurrentProfile == profile && result.CurrentCountPerDevice == countPerDevice
	return existing, result, nil
}

// partitionPolicyName 은 apply 가 새로 만들 때 쓰는 결정적 이름이다. resolveNodeACPP 는
// 이름이 아니라 (vendor,status.targets) 로 찾으므로, 다른 이름으로 이미 있는 정책도
// 정상 발견된다 — 이 이름은 "처음 만들 때"만 쓰인다.
func partitionPolicyName(node, vendor string) string {
	return fmt.Sprintf("%s-%s", node, vendor)
}

// PreviewPartitionApply 는 ApplyPartition 과 같은 판정을 거쳐 같은 요약을 돌려주되
// 아무것도 쓰지 않는다 — 확인 프롬프트가 대상·현재값·요청값을 보여주는 용도다.
func (c *Client) PreviewPartitionApply(ctx context.Context, node, vendor, profile string, countPerDevice int32) (
	*PartitionApplyResult, error,
) {
	_, result, err := c.resolvePartitionApply(ctx, node, vendor, profile, countPerDevice)
	return result, err
}

// ApplyPartition 은 node+vendor 의 파티션 레이아웃을 profile/countPerDevice 로 맞춘다.
// 기존 정책이 있으면 그 spec.layout 만 patch 하고(spec.sharing 은 손대지 않는다), 없으면
// 새로 만든다. 레이아웃 번역·검증은 BuildPartitionPolicy 를 그대로 재사용한다.
func (c *Client) ApplyPartition(ctx context.Context, node, vendor, profile string, countPerDevice int32) (
	*PartitionApplyResult, error,
) {
	existing, result, err := c.resolvePartitionApply(ctx, node, vendor, profile, countPerDevice)
	if err != nil {
		return nil, err
	}
	if result.NoChange {
		return result, nil
	}
	built, err := BuildPartitionPolicy(PolicyRequest{
		Name: result.Policy, Vendor: vendor, NodeName: node, Profile: profile, CountPerDevice: countPerDevice,
	})
	if err != nil {
		return nil, err
	}
	if existing == nil {
		if err := c.c.Create(ctx, built); err != nil {
			return nil, fmt.Errorf("AcceleratorPartitionPolicy %q 생성 실패: %w", built.Name, err)
		}
		result.Created = true
		return result, nil
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec.Layout = built.Spec.Layout
	if err := c.c.Patch(ctx, existing, patch); err != nil {
		return nil, fmt.Errorf("AcceleratorPartitionPolicy %q patch 실패: %w", existing.Name, err)
	}
	return result, nil
}

// SharingApplyResult 는 ApplySharing/PreviewSharingApply 의 결과 요약이다.
type SharingApplyResult struct {
	Policy          string `json:"policy"`
	Node            string `json:"node"`
	Vendor          string `json:"vendor"`
	Mode            string `json:"mode"`
	Replicas        int32  `json:"replicas,omitempty"`
	CurrentMode     string `json:"currentMode,omitempty"`
	CurrentReplicas int32  `json:"currentReplicas,omitempty"`
	Created         bool   `json:"created,omitempty"`
	NoChange        bool   `json:"noChange,omitempty"`
}

// sharingCapability 는 apply 가 받는 mode 문자열(timeSliced|mps|exclusive)을 vendorcap
// 능력으로 옮긴다. acppSharingCapability(vendorcap.go, capabilities 명령이 이미 씀)를
// 그대로 재사용한다 — 같은 번역표를 두 곳에 두지 않는다. exclusive 는 능력이 아니라
// "공유 끄기" 라 ok=false 로 돌아오고, 이때는 지원 여부를 보지 않는다(모든 벤더가 항상
// 지원하는 상태다).
func sharingCapability(mode string) (vendorcap.Capability, bool) {
	return acppSharingCapability(&npuv1alpha1.SharingSpec{Mode: mode})
}

// resolveSharingApply 는 PreviewSharingApply·ApplySharing 이 공유하는 해석 단계다.
func (c *Client) resolveSharingApply(ctx context.Context, node, vendor, mode string, replicas int32) (
	existing *npuv1alpha1.AcceleratorPartitionPolicy, result *SharingApplyResult, err error,
) {
	switch mode {
	case npuv1alpha1.SharingModeExclusive, npuv1alpha1.SharingModeTimeSliced, npuv1alpha1.SharingModeMPS:
	default:
		return nil, nil, fmt.Errorf("알 수 없는 공유 방식 %q(timeSliced|mps|exclusive 중 하나)", mode)
	}
	if err := c.checkNodeExists(ctx, node); err != nil {
		return nil, nil, err
	}
	if cap, ok := sharingCapability(mode); ok {
		if err := capabilityRefusal(vendor, mode, vendorcap.LookupVendor(vendor, cap)); err != nil {
			return nil, nil, err
		}
	}
	existing, err = c.resolveNodeACPP(ctx, node, vendor)
	if err != nil {
		return nil, nil, err
	}
	result = &SharingApplyResult{Node: node, Vendor: vendor, Mode: mode, Replicas: replicas}
	if existing == nil {
		result.Policy = partitionPolicyName(node, vendor)
		return nil, result, nil
	}
	result.Policy = existing.Name
	result.CurrentMode, result.CurrentReplicas = sharingModeReplicas(existing.Spec.Sharing)
	result.NoChange = result.CurrentMode == mode && result.CurrentReplicas == replicas
	return existing, result, nil
}

// PreviewSharingApply 는 ApplySharing 과 같은 판정을 거쳐 같은 요약을 돌려주되
// 아무것도 쓰지 않는다.
func (c *Client) PreviewSharingApply(ctx context.Context, node, vendor, mode string, replicas int32) (
	*SharingApplyResult, error,
) {
	_, result, err := c.resolveSharingApply(ctx, node, vendor, mode, replicas)
	return result, err
}

// ApplySharing 은 node+vendor 의 공유 방식을 mode/replicas 로 맞춘다. 기존 정책이
// 있으면 spec.sharing 만 patch 하고(spec.layout 은 손대지 않는다), 없으면 새로 만든다.
// mode="exclusive" 는 공유를 끈다 — BuildPartitionPolicy 의 기존 규칙(Intent="exclusive"
// 는 layout 이 없을 때만 sharing.mode=exclusive 를 만든다)을 그대로 따른다.
func (c *Client) ApplySharing(ctx context.Context, node, vendor, mode string, replicas int32) (
	*SharingApplyResult, error,
) {
	existing, result, err := c.resolveSharingApply(ctx, node, vendor, mode, replicas)
	if err != nil {
		return nil, err
	}
	if result.NoChange {
		return result, nil
	}
	req := PolicyRequest{Name: result.Policy, Vendor: vendor, NodeName: node, Replicas: replicas}
	if mode == npuv1alpha1.SharingModeExclusive {
		req.Intent = "exclusive"
	} else {
		req.Sharing = mode
	}
	built, err := BuildPartitionPolicy(req)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		if err := c.c.Create(ctx, built); err != nil {
			return nil, fmt.Errorf("AcceleratorPartitionPolicy %q 생성 실패: %w", built.Name, err)
		}
		result.Created = true
		return result, nil
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec.Sharing = built.Spec.Sharing
	if err := c.c.Patch(ctx, existing, patch); err != nil {
		return nil, fmt.Errorf("AcceleratorPartitionPolicy %q patch 실패: %w", existing.Name, err)
	}
	return result, nil
}
