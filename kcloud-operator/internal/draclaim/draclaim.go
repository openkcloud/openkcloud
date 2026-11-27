// ============================================================
// draclaim.go: DRA ResourceClaim 의 노드 할당 판정 (leaf 패키지)
// 상세: 장치를 쥔 claim 이 특정 노드에 남아 있는지 보는 안전 게이트다. partition 의 quiesce
// 경로와 upgrade 의 drain 경로가 같은 판정을 쓴다. partition 이 upgrade 를 단방향으로
// import 하므로, 판정을 partition 에 두면 upgrade 가 쓸 수 없어 우리 패키지를 하나도
// import 하지 않는 leaf 로 분리했다 — 복사본을 두면 게이트가 없는 쪽으로 흐른다.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================
package draclaim

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ErrClaimStillAllocated 는 "이 노드에 할당된 claim 이 아직 남았다" 를 "읽지 못했다" 와 가르는
// 센티넬이다. 두 호출자가 같은 반환을 다르게 해석하는 것이 정상이다 — upgrade 의 drain 게이트는
// 이것을 Pod 축과 같은 정상 재조정으로 돌리고(파드가 사라진 뒤 claim 이 deallocate 되기까지의
// 구간은 정상 drain 이 반드시 지난다), partition 의 AssertQuiesced 는 단언 함수이므로 그대로
// 오류로 올린다. 읽기 실패(List·Node Get)는 어느 쪽에서도 오류다.
var ErrClaimStillAllocated = errors.New("resourceclaim still allocated on node")

// AssertNoneAllocatedOnNode 는 nodeName 노드에 할당된(status.allocation 이 그 노드를 가리키는)
// ResourceClaim 이 없는지 검증한다. List 실패는 "없음" 이 아니라 "모른다" 다 — 안전 게이트이므로
// 읽지 못했으면 오류로 올린다(유일한 예외는 클러스터가 resource.k8s.io 를 서빙하지 않는 경우).
// 할당이 남은 경우만 ErrClaimStillAllocated 로 감싸 돌려줘 호출자가 errors.Is 로 가른다 — claim
// 이름은 메시지에 남겨 막은 주체를 특정한다. Node 는 라벨 매칭이 필요해질 때만 조회한다(실패해도
// 오류로 올린다 — 모르는 상태로 게이트를 열지 않는다).
func AssertNoneAllocatedOnNode(ctx context.Context, c client.Client, nodeName string) error {
	var claims resourcev1.ResourceClaimList
	if err := c.List(ctx, &claims); err != nil {
		if apimeta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("list resourceclaims: %w", err)
	}
	var node *corev1.Node
	for i := range claims.Items {
		claim := &claims.Items[i]
		if !pinnedToNode(claim) {
			continue
		}
		if node == nil {
			node = &corev1.Node{}
			if err := c.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
				return fmt.Errorf("get node %s: %w", nodeName, err)
			}
		}
		if AllocatedOnNode(claim, node) {
			return fmt.Errorf("node %s still has an allocated ResourceClaim %s/%s: %w",
				nodeName, claim.Namespace, claim.Name, ErrClaimStillAllocated)
		}
	}
	return nil
}

// AllocatedOnNode 는 claim 이 이 노드에 할당됐는지를 status.allocation.nodeSelector 로 판정한다.
func AllocatedOnNode(claim *resourcev1.ResourceClaim, node *corev1.Node) bool {
	if !pinnedToNode(claim) {
		return false
	}
	return nodeSelectorMatchesNode(claim.Status.Allocation.NodeSelector, node)
}

// pinnedToNode 는 claim 이 특정 노드에 박혔는지다. nodeSelector 가 nil 이면 "어디서든 쓸 수
// 있다"(network-attached, AllNodes)는 뜻이지 이 노드에 박혔다는 뜻이 아니다 — 그대로 매칭하면
// network 장치 claim 하나가 클러스터의 모든 노드 quiesce 를 영구히 막는다.
func pinnedToNode(claim *resourcev1.ResourceClaim) bool {
	alloc := claim.Status.Allocation
	return alloc != nil && alloc.NodeSelector != nil
}

// nodeSelectorOperators 는 corev1.NodeSelectorOperator → apimachinery selection.Operator 매핑이다.
// 값을 다시 옮기는 표일 뿐 매칭 로직 자체가 아니다 — 실제 In/NotIn/Exists/DoesNotExist/Gt/Lt 평가는
// labels.Requirement 가 이미 하므로 손으로 다시 구현하지 않는다.
var nodeSelectorOperators = map[corev1.NodeSelectorOperator]selection.Operator{
	corev1.NodeSelectorOpIn:           selection.In,
	corev1.NodeSelectorOpNotIn:        selection.NotIn,
	corev1.NodeSelectorOpExists:       selection.Exists,
	corev1.NodeSelectorOpDoesNotExist: selection.DoesNotExist,
	corev1.NodeSelectorOpGt:           selection.GreaterThan,
	corev1.NodeSelectorOpLt:           selection.LessThan,
}

// nodeSelectorMatchesNode 는 corev1.NodeSelector 의 의미론(term 은 OR, term 안 요구사항은 AND)을
// 평가한다. matchFields 는 DRA 가 노드-로컬 장치를 낼 때 쓰는 유일한 키인 metadata.name 을,
// matchExpressions 는 노드 라벨을 대상으로 한다(AllocationResult.NodeSelector 문서).
func nodeSelectorMatchesNode(sel *corev1.NodeSelector, node *corev1.Node) bool {
	fields := labels.Set{"metadata.name": node.Name}
	nodeLabels := labels.Set(node.Labels)
	for _, term := range sel.NodeSelectorTerms {
		if nodeSelectorRequirementsMatch(term.MatchFields, fields) &&
			nodeSelectorRequirementsMatch(term.MatchExpressions, nodeLabels) {
			return true
		}
	}
	return false
}

func nodeSelectorRequirementsMatch(reqs []corev1.NodeSelectorRequirement, set labels.Set) bool {
	for _, req := range reqs {
		op, ok := nodeSelectorOperators[req.Operator]
		if !ok {
			return false
		}
		r, err := labels.NewRequirement(req.Key, op, req.Values)
		if err != nil || !r.Matches(set) {
			return false
		}
	}
	return true
}
