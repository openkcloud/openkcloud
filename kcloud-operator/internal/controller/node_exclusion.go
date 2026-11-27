// ============================================================
// node_exclusion.go: 배제 노드 판정을 kcloud.ai/* 라벨로 발행
// 상세: 검증·CLI·콘솔은 allocatable=0 같은 "결과"만 보고 "의도"(배포 대상 제외)를 모른다.
// nodeExclusion 이 배제 판정의 정본이고, applyControlPlaneExclusion(npuclusterpolicy_
// controller.go)이 같은 조건을 렌더 시점 affinity 로 걸 때 쓰는 상수를 그대로 공유한다.
// detector·npuctl 은 이 라벨을 읽기만 하고 판정 규칙을 복제하지 않는다.
// 생성일: 2026-08-12 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

const (
	// nodeExcludedLabel 은 이 노드가 배포 대상에서 제외됐는지다. 값은 항상 "true" — 배제가
	// 풀리면 라벨 자체를 지운다(상태를 남기지 않는다).
	nodeExcludedLabel = "kcloud.ai/excluded"
	// nodeExcludedReasonLabel 의 값은 고정 어휘: exclusionReasonControlPlane·exclusionReasonPolicy.
	nodeExcludedReasonLabel = "kcloud.ai/excluded-reason"

	// exclusionReasonControlPlane 은 control-plane/master 배제다. 항상 켜져 있고 정책으로
	// 끌 수 없다(applyControlPlaneExclusion 과 동일 조건).
	exclusionReasonControlPlane = "control-plane"
	// exclusionReasonPolicy 는 벤더별 excludeNodeSelector 배제 사유다.
	exclusionReasonPolicy = "policy"
)

// nodeExclusion 은 배제 판정의 정본이다. control-plane 이 policy 보다 강하다 — 둘 다 걸리면
// control-plane 을 돌려준다(정책으로 안전 규칙을 못 끄게 한다). ncp 가 nil 이면 배제가
// 아니다 — reconcile 자체가 NCP 로 트리거되므로 정책이 없으면 이 함수가 아예 안 불린다.
// 정책 삭제·미생성을 다르게 다루면 같은 클러스터 상태가 이력에 따라 다르게 보이고, 라벨을
// 회수하지 않으면 CLI·콘솔이 없는 정책을 계속 "제외됨"으로 보고한다. 그 대가로 NCP 생성 전
// control-plane 노드는 검증 실패로 남지만, "정책 부재" 를 검증 쪽에서 표현하는 게 더 싸다.
func nodeExclusion(node *corev1.Node, ncp *npuv1alpha1.NPUClusterPolicy) (bool, string) {
	if ncp == nil {
		return false, ""
	}
	if _, ok := node.Labels[controlPlaneNodeLabel]; ok {
		return true, exclusionReasonControlPlane
	}
	if _, ok := node.Labels[masterNodeLabel]; ok {
		return true, exclusionReasonControlPlane
	}
	if matchesAnyExcludeNodeSelector(node, ncp) {
		return true, exclusionReasonPolicy
	}
	return false, ""
}

// vendorExcludeSelectors 는 벤더별 excludeNodeSelector 전부다. 라벨 경로·경고 로그가 같은
// 목록을 봐야 벤더 하나가 조용히 빠지지 않는다.
func vendorExcludeSelectors(ncp *npuv1alpha1.NPUClusterPolicy) []*metav1.LabelSelector {
	if ncp == nil {
		return nil
	}
	return []*metav1.LabelSelector{
		ncp.Spec.Nvidia.ExcludeNodeSelector,
		ncp.Spec.Furiosa.ExcludeNodeSelector,
		ncp.Spec.Furiosa.Rngd.ExcludeNodeSelector,
		ncp.Spec.Tenstorrent.ExcludeNodeSelector,
		ncp.Spec.Rebellions.ExcludeNodeSelector,
	}
}

// exclusionConditions 는 셀렉터를 조건 목록으로 펼친다. 렌더 경로와 라벨 경로가 같은 목록을
// 소비해야 같은 노드를 뺀다 — 의미론은 "조건 하나라도 맞으면 뺀다" 다. 유효하지 않은 조건
// (예: In 인데 values 없음)을 그대로 렌더하면 API 서버가 거부해 관계없는 벤더까지 Reconcile
// 이 멈춘다(webhook·CRD enum 이 없어 이런 입력이 실제로 저장된다) — 그런 조건은 kept 대신
// dropped 로 걸러낸다. matchLabels 키는 정렬해 순서를 고정한다(비결정적이면 nodeAffinity 가
// 매번 달라져 불필요한 갱신이 반복된다); Exists·DoesNotExist 의 Values 는 렌더와 맞춰 지운다.
func exclusionConditions(sel *metav1.LabelSelector) (kept, dropped []metav1.LabelSelectorRequirement) {
	if sel == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(sel.MatchLabels))
	for k := range sel.MatchLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	all := make([]metav1.LabelSelectorRequirement, 0, len(keys)+len(sel.MatchExpressions))
	for _, k := range keys {
		all = append(all, metav1.LabelSelectorRequirement{
			Key: k, Operator: metav1.LabelSelectorOpIn, Values: []string{sel.MatchLabels[k]},
		})
	}
	for _, e := range sel.MatchExpressions {
		if e.Operator == metav1.LabelSelectorOpExists || e.Operator == metav1.LabelSelectorOpDoesNotExist {
			e.Values = nil
		}
		all = append(all, e)
	}

	for _, c := range all {
		// 유효성은 metav1 에 맡긴다 — 알 수 없는 operator, values 없는 In/NotIn, 잘못된
		// 라벨 키가 한 검사로 걸린다(각각을 따로 열거하면 규칙이 갈라진다).
		if _, err := conditionSelector(c); err != nil {
			dropped = append(dropped, c)
			continue
		}
		kept = append(kept, c)
	}
	return kept, dropped
}

// conditionSelector 는 조건 하나를 labels.Selector 로 만든다. 렌더 쪽 NOT 변환과 같은
// 규칙(metav1 의 operator 해석)을 쓰기 위해 조건을 셀렉터 하나로 감싼다.
func conditionSelector(c metav1.LabelSelectorRequirement) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{c},
	})
}

// matchesAnyExcludeNodeSelector 는 이 노드가 어느 벤더든 excludeNodeSelector 조건 하나에라도
// 걸리는지 본다. 조건 목록은 exclusionConditions 가 만들어 렌더 경로와 공유한다.
func matchesAnyExcludeNodeSelector(node *corev1.Node, ncp *npuv1alpha1.NPUClusterPolicy) bool {
	for _, sel := range vendorExcludeSelectors(ncp) {
		kept, _ := exclusionConditions(sel)
		for _, c := range kept {
			s, err := conditionSelector(c)
			if err != nil {
				continue // exclusionConditions 가 이미 걸렀다.
			}
			if s.Matches(labels.Set(node.Labels)) {
				return true
			}
		}
	}
	return false
}

// logDroppedExclusionConditions 는 버린 excludeNodeSelector 조건을 알린다. 조용히 무시하면
// 사용자는 배제가 걸린 줄 안다. 노드 수와 무관하게 정책당 한 번만 남긴다.
func logDroppedExclusionConditions(ctx context.Context, ncp *npuv1alpha1.NPUClusterPolicy) {
	logger := logf.FromContext(ctx)
	for _, sel := range vendorExcludeSelectors(ncp) {
		_, dropped := exclusionConditions(sel)
		for _, c := range dropped {
			logger.Info("excludeNodeSelector 조건이 유효하지 않아 무시함 — 이 조건으로는 노드가 배제되지 않는다",
				"key", c.Key, "operator", c.Operator, "values", c.Values)
		}
	}
}

// reconcileNodeExclusionLabels 는 nodeExclusion 판정과 노드 라벨을 맞춘다. 부여와 회수가
// 같은 함수에 있어야 고아 라벨이 남지 않는다(reconcileDRAOwnedLabels 와 같은 이유).
func (r *NPUClusterPolicyReconciler) reconcileNodeExclusionLabels(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	logger := logf.FromContext(ctx)
	logDroppedExclusionConditions(ctx, policy)

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return fmt.Errorf("노드 목록 조회 실패: %w", err)
	}

	for i := range nodes.Items {
		node := &nodes.Items[i]
		excluded, reason := nodeExclusion(node, policy)
		// 라벨 존재가 아니라 값을 본다. 존재만 보면 손으로 붙인 kcloud.ai/excluded=false 가
		// 배제 노드에서 "이미 맞다" 로 판정돼(존재==배제) 값이 영구히 false 로 남고,
		// npuctl·detector 는 "true" 만 배제로 인정하므로 화면은 배제가 아니다.
		// 비배제 노드의 목표값은 빈 문자열이라 그 라벨은 그대로 회수된다.
		want := ""
		if excluded {
			want = labelValueTrue
		}
		if node.Labels[nodeExcludedLabel] == want && node.Labels[nodeExcludedReasonLabel] == reason {
			continue
		}

		patch := client.MergeFrom(node.DeepCopy())
		if excluded {
			if node.Labels == nil {
				node.Labels = map[string]string{}
			}
			node.Labels[nodeExcludedLabel] = labelValueTrue
			node.Labels[nodeExcludedReasonLabel] = reason
		} else {
			delete(node.Labels, nodeExcludedLabel)
			delete(node.Labels, nodeExcludedReasonLabel)
		}
		if err := r.Patch(ctx, node, patch); err != nil {
			return fmt.Errorf("노드 %s 배제 라벨 갱신 실패: %w", node.Name, err)
		}
		logger.Info("노드 배제 라벨 갱신", "node", node.Name, "excluded", excluded, "reason", reason)
	}

	return nil
}
