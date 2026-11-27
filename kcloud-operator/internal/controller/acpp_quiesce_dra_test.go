// ============================================================
// acpp_quiesce_dra_test.go: assertNodeQuiesced 의 DRA ResourceClaim 스캔 테스트
// 상세: Pod 가 비어 있어도 ResourceClaim.status.allocation 이 노드를 물고 있으면 정지 판정은 실패해야 한다.
// 생성일: 2026-08-05
// ============================================================
package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// cordonedNode 는 이미 cordon 된(Pod 배출 전제가 이미 충족된) 노드다.
func cordonedNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: corev1.NodeSpec{Unschedulable: true}}
}

// nodeSelectorForHostname 은 DRA 드라이버가 노드-로컬 장치를 낼 때 쓰는 실제 관례(resourceslice
// tracker)를 재현한다 — matchFields metadata.name 로 정확히 그 노드를 지정한다.
func nodeSelectorForHostname(name string) *corev1.NodeSelector {
	return &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
		MatchFields: []corev1.NodeSelectorRequirement{{
			Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{name},
		}},
	}}}
}

// newTestReconciler 는 Pod spec.nodeName 인덱스까지 갖춘 fake client 기반 reconciler 다
// (assertNodeQuiesced 가 위임하는 partition.Quiescer.AssertQuiesced 가 그 인덱스로 Pod 를 찾는다).
func newTestReconciler(objs ...client.Object) *AcceleratorPartitionPolicyReconciler {
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).
		WithObjects(objs...).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	return &AcceleratorPartitionPolicyReconciler{Client: c, Scheme: c.Scheme()}
}

func TestAssertNodeQuiescedRejectsAllocatedResourceClaim(t *testing.T) {
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{
			NodeSelector: nodeSelectorForHostname("w1"),
		}},
	}
	r := newTestReconciler(cordonedNode("w1"), claim)
	if err := r.assertNodeQuiesced(context.Background(), "w1"); err == nil {
		t.Fatal("quiesce must fail while a ResourceClaim is allocated on the node")
	}
}

// TestAssertNodeQuiescedIgnoresClaimAllocatedElsewhere 는 다른 노드에 박힌 claim 이 이 노드의
// 정지 판정을 오염시키지 않음을 고정한다(오탐 방지 — draclaim.AllocatedOnNode 가 노드를 구분하는지).
func TestAssertNodeQuiescedIgnoresClaimAllocatedElsewhere(t *testing.T) {
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{
			NodeSelector: nodeSelectorForHostname("other-node"),
		}},
	}
	r := newTestReconciler(cordonedNode("w1"), claim)
	if err := r.assertNodeQuiesced(context.Background(), "w1"); err != nil {
		t.Fatalf("claim allocated on a different node must not block this node: %v", err)
	}
}

// TestAssertNodeQuiescedPassesWithoutClaims 는 claim 이 아예 없을 때(=DRA 미사용) 기존 동작이
// 그대로 통과함을 고정한다 — 새 스캔이 DRA 를 안 쓰는 클러스터를 회귀시키지 않는지 확인.
func TestAssertNodeQuiescedPassesWithoutClaims(t *testing.T) {
	r := newTestReconciler(cordonedNode("w1"))
	if err := r.assertNodeQuiesced(context.Background(), "w1"); err != nil {
		t.Fatalf("no pods, no claims: quiesce should pass, got %v", err)
	}
}
