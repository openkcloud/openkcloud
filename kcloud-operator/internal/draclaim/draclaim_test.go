// ============================================================
// draclaim_test.go: 노드 단위 ResourceClaim 할당 판정 단위 테스트
// 상세: 두 호출자(upgrade·partition)의 시험이 모두 matchFields metadata.name 만
//   만들어 두어, 노드 라벨로 할당된 claim(matchExpressions)과 미지 연산자 분기를
//   어느 시험도 밟지 않는다. 라벨로 할당된 claim 을 놓치면 게이트가 조용히 열리므로
//   이 패키지 자체 시험으로 그 둘을 고정한다.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================

package draclaim

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nodeLabelClaim 은 matchExpressions 로 zone=a 라벨을 가진 노드에 할당된 claim 이다.
func nodeLabelClaim(op corev1.NodeSelectorOperator) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		Status: resourcev1.ResourceClaimStatus{
			Allocation: &resourcev1.AllocationResult{
				NodeSelector: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: []corev1.NodeSelectorRequirement{{
							Key: "zone", Operator: op, Values: []string{"a"},
						}},
					}},
				},
			},
		},
	}
}

// TestAllocatedOnNode_MatchExpressionsNodeLabel 은 노드 라벨로 할당된(matchExpressions)
// claim 을 놓치지 않는지, 그리고 라벨이 다른 노드까지 잘못 매칭하지 않는지를 고정한다.
func TestAllocatedOnNode_MatchExpressionsNodeLabel(t *testing.T) {
	claim := nodeLabelClaim(corev1.NodeSelectorOpIn)

	match := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"zone": "a"}}}
	if !AllocatedOnNode(claim, match) {
		t.Fatal("노드 라벨(matchExpressions)로 할당된 claim 을 놓쳤다")
	}

	other := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n2", Labels: map[string]string{"zone": "b"}}}
	if AllocatedOnNode(claim, other) {
		t.Fatal("라벨이 다른 노드인데 할당된 것으로 봤다")
	}
}

// TestAllocatedOnNode_UnknownOperatorDoesNotMatch 는 nodeSelectorOperators 에 없는
// 연산자를 가진 요구사항이 있으면 그 term 이 매칭되지 않는 현재 동작을 고정한다.
// 이 동작의 방향(과포착·과소포착)을 바꾸는 것은 이 시험의 범위 밖이다 — 이동 전부터
// 있던 공백을 시험으로 고정만 한다.
func TestAllocatedOnNode_UnknownOperatorDoesNotMatch(t *testing.T) {
	claim := nodeLabelClaim(corev1.NodeSelectorOperator("Bogus"))

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"zone": "a"}}}
	if AllocatedOnNode(claim, node) {
		t.Fatal("미지 연산자인 요구사항이 매칭된 것으로 처리됐다")
	}
}
