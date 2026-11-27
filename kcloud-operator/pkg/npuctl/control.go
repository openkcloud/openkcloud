// control.go: pkg/npuctl 제어(control) 로직 — 모두 CR patch 로 귀결된다
// 상세: 드라이버 버전 변경·벤더 enabled 토글·롤백 요청 어노테이션 patch 를 제공한다.
// status 는 컨트롤러만 쓰므로 CLI 는 metadata/spec 만 patch 하고 실제 작업은 operator
// 재조정이 한다. 라이브 클러스터 실행 검증은 금지(가드레일).
// 생성일: 2026-07-16 | 수정일: 2026-10-27
package npuctl

import (
	"context"
	"fmt"
	"sort"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// upgradeFieldManager 는 DIP spec 을 바꾸는 이 패키지의 managedFields 이름이다.
const upgradeFieldManager = "kcloud-operator-upgrade"

// UpgradeOptions는 UpgradeVendor 호출 시 부가 동작을 제어합니다.
type UpgradeOptions struct {
	// Force는 true면 upgradePolicy.forceUpgrade(+autoUpgrade/drainEnabled)를 함께 설정합니다(drain --force 동등).
	Force bool
	// Auto는 true면 upgradePolicy.autoUpgrade + drainEnabled를 설정합니다.
	Auto bool
	// Model은 동일 벤더에 DIP가 둘 이상 있을 때(예: furiosa=warboy/rngd) 대상을 특정합니다.
	// 비우면 해당 벤더의 DIP가 정확히 하나일 때만 허용되고, 둘 이상이면 에러로 후보를 안내합니다.
	Model string
}

// UpgradeResult는 UpgradeVendor의 결과 요약입니다.
type UpgradeResult struct {
	PolicyName      string `json:"policyName"`
	Vendor          string `json:"vendor"`
	Model           string `json:"model,omitempty"`
	PreviousVersion string `json:"previousVersion,omitempty"`
	NewVersion      string `json:"newVersion"`
	NoChange        bool   `json:"noChange"`
}

// PreviewVendorUpgrade 는 UpgradeVendor 와 같은 해석(DIP 특정·TrackOnly 거절·NoChange 판정)을 거쳐 같은
// 요약을 돌려주되 아무것도 patch 하지 않는다 — 확인 프롬프트가 무엇을 바꾸는지 보여 주는 용도다.
func (c *Client) PreviewVendorUpgrade(
	ctx context.Context, vendor, version string, opts UpgradeOptions,
) (*UpgradeResult, error) {
	if version == "" {
		return nil, fmt.Errorf("version은 비워둘 수 없습니다")
	}
	dip, err := c.resolveDIP(ctx, vendor, opts.Model)
	if err != nil {
		return nil, err
	}
	if dip.Spec.Driver.TrackOnly {
		return nil, trackOnlyUpgradeError(dip)
	}
	return &UpgradeResult{
		PolicyName: dip.Name, Vendor: dip.Spec.Vendor, Model: dip.Spec.Model,
		PreviousVersion: dip.Spec.Driver.Version, NewVersion: version,
		NoChange: dip.Spec.Driver.Version == version,
	}, nil
}

// UpgradeVendor는 vendor(+옵션 model)에 대응하는 단일 DriverInstallPolicy.spec.driver.version을
// version으로 patch합니다. 매칭 DIP가 없거나 둘 이상(모델 미지정)이거나 TrackOnly 정책이면 에러를
// 반환합니다. 모든 쓰기는 DIP patch로 귀결됩니다.
func (c *Client) UpgradeVendor(
	ctx context.Context,
	vendor, version string,
	opts UpgradeOptions,
) (*UpgradeResult, error) {
	if version == "" {
		return nil, fmt.Errorf("version은 비워둘 수 없습니다")
	}

	dip, err := c.resolveDIP(ctx, vendor, opts.Model)
	if err != nil {
		return nil, err
	}
	if dip.Spec.Driver.TrackOnly {
		return nil, trackOnlyUpgradeError(dip)
	}

	prevVersion := dip.Spec.Driver.Version
	result := &UpgradeResult{
		PolicyName:      dip.Name,
		Vendor:          dip.Spec.Vendor,
		Model:           dip.Spec.Model,
		PreviousVersion: prevVersion,
		NewVersion:      version,
	}

	if prevVersion == version {
		result.NoChange = true
		return result, nil
	}

	// MergeFrom(obj)는 obj를 deep-copy하지 않고 그대로 참조로 저장하므로(controller-runtime
	// client.mergeFromPatch.from), 반드시 mutate 이전에 DeepCopy()로 스냅샷을 떠야 diff가 발생한다.
	patch := client.MergeFrom(dip.DeepCopy())
	dip.Spec.Driver.Version = version
	if opts.Auto || opts.Force {
		up := dip.Spec.UpgradePolicy
		if up == nil {
			up = &npuv1alpha1.UpgradePolicy{}
		}
		up.AutoUpgrade = true
		up.DrainEnabled = true
		if opts.Force {
			up.ForceUpgrade = true
		}
		dip.Spec.UpgradePolicy = up
	}
	// FieldManager 를 명시한다 — 지정하지 않으면 API 서버가 User-Agent(= 바이너리 파일명)에서
	// 이름을 유도해 CLI 는 kubectl-npu, operator 는 manager 로 서로 다른 항목이 생긴다.
	// 이름을 고정하면 managedFields 에서 이 제어 경로를 식별할 수 있고, kcloud-operator 접두라
	// 공동 소유 경고(foreignSpecManagers)에서 제품 자신으로 제외된다.
	if err := c.c.Patch(ctx, dip, patch, client.FieldOwner(upgradeFieldManager)); err != nil {
		return nil, fmt.Errorf("DriverInstallPolicy %q patch 실패: %w", dip.Name, err)
	}

	return result, nil
}

// trackOnlyUpgradeError 는 TrackOnly 정책(설치 경로 없이 버전만 추적한다)에 대한 upgrade 를 거절하는 오류다.
// PreviewVendorUpgrade 와 UpgradeVendor 가 함께 써서 확인 프롬프트 앞뒤의 거절 문구가 같다.
func trackOnlyUpgradeError(dip *npuv1alpha1.DriverInstallPolicy) error {
	return fmt.Errorf(
		"vendor=%q(model=%q)는 DriverInstallPolicy %q 가 TrackOnly 라 설치 경로가 없습니다 — "+
			"드라이버는 호스트에서 직접 설치하고 이 정책은 버전만 추적합니다",
		dip.Spec.Vendor, dip.Spec.Model, dip.Name)
}

// resolveDIP는 vendor(+model)에 대응하는 단일 DriverInstallPolicy를 찾습니다.
// vendorKeyForDIP와 동일 규칙으로 furiosa+model=="rngd"는 vendor="rngd" 조회에 매칭됩니다.
func (c *Client) resolveDIP(ctx context.Context, vendor, model string) (*npuv1alpha1.DriverInstallPolicy, error) {
	var dips npuv1alpha1.DriverInstallPolicyList
	if err := c.c.List(ctx, &dips); err != nil {
		return nil, fmt.Errorf("DriverInstallPolicy 목록 조회 실패: %w", err)
	}

	candidates := make([]npuv1alpha1.DriverInstallPolicy, 0, len(dips.Items))
	for i := range dips.Items {
		dip := dips.Items[i]
		if vendorKeyForDIP(dip.Spec) != vendor {
			continue
		}
		if model != "" && dip.Spec.Model != model {
			continue
		}
		candidates = append(candidates, dip)
	}

	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("vendor=%q(model=%q)에 매칭되는 DriverInstallPolicy가 없습니다", vendor, model)
	case 1:
		return &candidates[0], nil
	default:
		names := make([]string, 0, len(candidates))
		for _, d := range candidates {
			names = append(names, fmt.Sprintf("%s(model=%s)", d.Name, d.Spec.Model))
		}
		return nil, fmt.Errorf(
			"vendor=%q에 매칭되는 DriverInstallPolicy가 %d개 있습니다(%v) — --model로 특정하세요",
			vendor, len(candidates), names)
	}
}

// ToggleResult는 SetVendorEnabled의 결과 요약입니다.
type ToggleResult struct {
	Vendor          string `json:"vendor"`
	PreviousEnabled bool   `json:"previousEnabled"`
	NewEnabled      bool   `json:"newEnabled"`
	NoChange        bool   `json:"noChange"`
}

// vendorEnabledField 는 NPUClusterPolicy 에서 그 벤더의 enabled 필드 포인터를 돌려준다 —
// 미리보기(읽기)와 토글(쓰기)이 같은 분기를 쓴다.
func vendorEnabledField(ncp *npuv1alpha1.NPUClusterPolicy, vendor string) (*bool, error) {
	switch vendor {
	case VendorNvidia:
		return &ncp.Spec.Nvidia.Enabled, nil
	case VendorFuriosa:
		return &ncp.Spec.Furiosa.Enabled, nil
	case VendorRngd:
		return &ncp.Spec.Furiosa.Rngd.Enabled, nil
	case VendorRebellions:
		return &ncp.Spec.Rebellions.Enabled, nil
	case VendorTenstorrent:
		return &ncp.Spec.Tenstorrent.Enabled, nil
	}
	return nil, fmt.Errorf("알 수 없는 vendor %q (nvidia|furiosa|rngd|rebellions|tenstorrent 중 하나)", vendor)
}

// firstClusterPolicy 는 클러스터의 첫 NPUClusterPolicy 다(관리 API 1단계 관례).
func (c *Client) firstClusterPolicy(ctx context.Context) (*npuv1alpha1.NPUClusterPolicy, error) {
	var ncps npuv1alpha1.NPUClusterPolicyList
	if err := c.c.List(ctx, &ncps); err != nil {
		return nil, fmt.Errorf("NPUClusterPolicy 목록 조회 실패: %w", err)
	}
	if len(ncps.Items) == 0 {
		return nil, fmt.Errorf("NPUClusterPolicy가 클러스터에 없습니다")
	}
	return &ncps.Items[0], nil
}

// PreviewVendorEnabled 는 SetVendorEnabled 와 같은 판정을 거쳐 요약만 돌려준다(patch 없음).
func (c *Client) PreviewVendorEnabled(ctx context.Context, vendor string, enabled bool) (*ToggleResult, error) {
	ncp, err := c.firstClusterPolicy(ctx)
	if err != nil {
		return nil, err
	}
	field, err := vendorEnabledField(ncp, vendor)
	if err != nil {
		return nil, err
	}
	return &ToggleResult{Vendor: vendor, PreviousEnabled: *field, NewEnabled: enabled, NoChange: *field == enabled}, nil
}

// SetVendorEnabled는 (첫 번째) NPUClusterPolicy.spec.<vendor>.enabled를 patch합니다.
// vendor: nvidia | furiosa | rngd(=furiosa.rngd) | rebellions | tenstorrent.
func (c *Client) SetVendorEnabled(ctx context.Context, vendor string, enabled bool) (*ToggleResult, error) {
	ncp, err := c.firstClusterPolicy(ctx)
	if err != nil {
		return nil, err
	}
	patch := client.MergeFrom(ncp.DeepCopy())
	field, err := vendorEnabledField(ncp, vendor)
	if err != nil {
		return nil, err
	}
	previous := *field
	result := &ToggleResult{Vendor: vendor, PreviousEnabled: previous, NewEnabled: enabled}
	if previous == enabled {
		result.NoChange = true
		return result, nil
	}
	*field = enabled
	if err := c.c.Patch(ctx, ncp, patch); err != nil {
		return nil, fmt.Errorf("NPUClusterPolicy %q patch 실패: %w", ncp.Name, err)
	}
	return result, nil
}

// RollbackResult는 TriggerDriverRollback의 결과 요약입니다.
type RollbackResult struct {
	DUSName string `json:"dusName"`
	Node    string `json:"node"`
	Vendor  string `json:"vendor"`
	Model   string `json:"model,omitempty"`
	// PreviousState는 patch 이전에 DriverUpgradeState.status.state에 있던 값입니다.
	PreviousState string `json:"previousState,omitempty"`
	// PreviousVersion은 상태머신이 롤백 시 되돌릴 버전입니다. 비어 있으면(한 번도
	// 업그레이드된 적 없는 등) handleRollback이 다음 reconcile에서 곧바로 Failed로
	// 전이합니다 — 이 함수는 그 판정을 대신하지 않고 값만 그대로 옮깁니다.
	PreviousVersion string `json:"previousVersion,omitempty"`
}

// resolveRollbackTarget은 TriggerDriverRollback·PreviewDriverRollback이 공유하는 해석
// 단계다 — DUS 를 찾고 TrackOnly 정책이면 거절하고 결과 요약을 만든다(쓰지 않는다,
// 미리보기·실제 트리거가 같은 판정을 쓰도록). TrackOnly 벤더는 설치 경로가 없어
// ensureTrackOnlyState 가 매 reconcile 마다 status.state 를 Idle 로 되돌리므로, 거르지
// 않으면 patch 는 조용히 취소돼 거짓 신호가 된다. resolveDIP 조회는 vendorKeyForDIP 의
// 다섯 벤더 키로 한다 — raw vendor 를 쓰면 furiosa 혼재 시 rngd DUS 가 warboy 정책에 잘못 매칭된다.
func (c *Client) resolveRollbackTarget(ctx context.Context, node, vendor, model string) (
	*npuv1alpha1.DriverUpgradeState, *RollbackResult, error,
) {
	dus, err := c.resolveDUS(ctx, node, vendor, model)
	if err != nil {
		return nil, nil, err
	}

	// DriverModelUnknown(v1alpha1)이면 이 노드는 같은 벤더 아래 서로 다른 product 장치가
	// 섞여 있어(예: furiosa Warboy+RNGD 혼재) 컨트롤러 자신도 대표 model 을 하나로 못
	// 정한 것이다. 그 값을 vendorKeyForDIP 에 넘기면 furiosa 키로 떨어져 warboy 정책에
	// 잘못 매칭될 수 있다 — 장치·라벨을 다시 읽어 알아내려 하지 않고(컨트롤러가 이미
	// 내린 "모른다"는 판단을 재구성하는 것과 같다), 여기서 바로 거절한다.
	if dus.Spec.Model == npuv1alpha1.DriverModelUnknown {
		return nil, nil, fmt.Errorf(
			"node=%q vendor=%q 는 같은 벤더 아래 서로 다른 product 장치가 섞여 있어 "+
				"이 CLI 로는 어느 DriverInstallPolicy 가 적용되는지 판정할 수 없습니다 — "+
				"kubectl 로 DriverUpgradeState 를 직접 다루세요 (관측된 제품: %v)",
			dus.Spec.NodeName, dus.Spec.Vendor, dus.Status.ObservedModels)
	}

	if !npuv1alpha1.RollbackRequestAccepted(dus.Status.State) {
		return nil, nil, fmt.Errorf(
			"node=%q vendor=%q 의 현재 상태 %q 에서는 롤백 요청을 받지 않습니다 — Idle·Failed 에서만 요청할 수 있습니다",
			dus.Spec.NodeName, dus.Spec.Vendor, dus.Status.State)
	}

	key := vendorKeyForDIP(npuv1alpha1.DriverInstallPolicySpec{Vendor: dus.Spec.Vendor, Model: dus.Spec.Model})
	dip, err := c.resolveDIP(ctx, key, "")
	if err != nil {
		return nil, nil, fmt.Errorf("롤백 대상의 DriverInstallPolicy 를 확인할 수 없습니다: %w", err)
	}
	if dip.Spec.Driver.TrackOnly {
		return nil, nil, fmt.Errorf(
			"vendor=%q(model=%q)는 DriverInstallPolicy %q 가 TrackOnly 라 설치 경로가 없습니다 — "+
				"상태머신이 롤백을 수행할 수 없고, 지금 patch 해도 다음 reconcile 에서 상태가 Idle 로 되돌아갑니다",
			dus.Spec.Vendor, dus.Spec.Model, dip.Name)
	}

	result := &RollbackResult{
		DUSName:         dus.Name,
		Node:            dus.Spec.NodeName,
		Vendor:          dus.Spec.Vendor,
		Model:           dus.Spec.Model,
		PreviousState:   dus.Status.State,
		PreviousVersion: dus.Status.PreviousVersion,
	}
	return dus, result, nil
}

// PreviewDriverRollback은 TriggerDriverRollback과 같은 판정(대상 해석 + TrackOnly 거절)을
// 거쳐 같은 요약을 돌려주되 아무것도 patch하지 않습니다. 확인 프롬프트가 사람에게 "무엇을
// 되돌리는지"(현재 상태·이전 버전)를 묻기 전에 보여주는 용도입니다.
func (c *Client) PreviewDriverRollback(ctx context.Context, node, vendor, model string) (*RollbackResult, error) {
	_, result, err := c.resolveRollbackTarget(ctx, node, vendor, model)
	return result, err
}

// TriggerDriverRollback은 (node[,vendor[,model]])에 대응하는 단일 DriverUpgradeState의
// metadata 에 롤백 요청 어노테이션(RollbackRequestedAnnotation)을 patch합니다. 이전
// 버전으로의 실제 복구는 이 함수가 하지 않습니다 — CLI 는 요청 사실만 기록하고,
// 상태 기계(internal/upgrade.UpgradeStateMachine.TransitionState)가 다음 reconcile 에서
// 이 어노테이션을 읽어 Rollback 으로 전이한 뒤 지웁니다.
func (c *Client) TriggerDriverRollback(ctx context.Context, node, vendor, model string) (*RollbackResult, error) {
	dus, result, err := c.resolveRollbackTarget(ctx, node, vendor, model)
	if err != nil {
		return nil, err
	}

	// status 는 컨트롤러만 쓴다 — CLI 는 요청 사실만 metadata 에 남기고, 상태 기계가 다음
	// reconcile 에서 transitionTo(Rollback) 로 전이하며 이 어노테이션을 지운다.
	patch := client.MergeFrom(dus.DeepCopy())
	if dus.Annotations == nil {
		dus.Annotations = map[string]string{}
	}
	dus.Annotations[npuv1alpha1.RollbackRequestedAnnotation] = time.Now().UTC().Format(time.RFC3339)
	if err := c.c.Patch(ctx, dus, patch); err != nil {
		return nil, fmt.Errorf("DriverUpgradeState %q 롤백 요청 기록 실패: %w", dus.Name, err)
	}
	return result, nil
}

// resolveDUS는 node(+선택적 vendor,model)에 대응하는 단일 DriverUpgradeState를 찾습니다.
// DriverUpgradeState는 (node,vendor)로만 유일합니다 — DriverInstallPolicy와 달리 model
// 별로 나뉘지 않습니다. vendor를 생략하면 그 노드의 DUS 전체에서 정확히 하나만 찾고
// (둘 이상이면 하이브리드 노드이므로 에러로 후보를 안내), model이 주어지면 찾은
// DUS.spec.model과 대조만 합니다(선별 기준이 아니라 오타 방지용 확인).
func (c *Client) resolveDUS(ctx context.Context, node, vendor, model string) (*npuv1alpha1.DriverUpgradeState, error) {
	var duses npuv1alpha1.DriverUpgradeStateList
	if err := c.c.List(ctx, &duses); err != nil {
		return nil, fmt.Errorf("DriverUpgradeState 목록 조회 실패: %w", err)
	}

	candidates := make([]npuv1alpha1.DriverUpgradeState, 0, len(duses.Items))
	for i := range duses.Items {
		d := duses.Items[i]
		if d.Spec.NodeName != node {
			continue
		}
		if vendor != "" && d.Spec.Vendor != vendor {
			continue
		}
		candidates = append(candidates, d)
	}

	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("node=%q(vendor=%q)에 매칭되는 DriverUpgradeState가 없습니다", node, vendor)
	case 1:
		d := candidates[0]
		if model != "" && d.Spec.Model != model {
			return nil, fmt.Errorf(
				"node=%q vendor=%q의 DriverUpgradeState는 model=%q인데 --model=%q을 지정했습니다",
				node, d.Spec.Vendor, d.Spec.Model, model)
		}
		return &d, nil
	default:
		vendors := make([]string, 0, len(candidates))
		for _, d := range candidates {
			vendors = append(vendors, d.Spec.Vendor)
		}
		sort.Strings(vendors)
		return nil, fmt.Errorf(
			"node=%q에 매칭되는 DriverUpgradeState가 %d개(vendor=%v) 있습니다 — --vendor로 특정하세요",
			node, len(candidates), vendors)
	}
}
