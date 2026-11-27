// ============================================================
// quiesce_test.go: quiesce 오케스트레이션 테스트
// 상세: cordon → 장치 점유 pod 삭제 → 잔여 0 검증 → uncordon. DaemonSet pod 은 삭제하지 않는다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package partition

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func gpuPod(name, node string, owner *metav1.OwnerReference) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{
			Name: "c", Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if owner != nil {
		p.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	return p
}

func TestCordonMarksNodeUnschedulable(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1"}}
	c := fake.NewClientBuilder().WithObjects(node).Build()
	q := NvidiaQuiescer(c)
	if err := q.Cordon(context.Background(), "worker1"); err != nil {
		t.Fatal(err)
	}
	var got corev1.Node
	if err := c.Get(context.Background(), types.NamespacedName{Name: "worker1"}, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Spec.Unschedulable {
		t.Fatalf("node not cordoned")
	}
}

func TestEvictDeviceWorkloadsDeletesGPUPodsButNotDaemonSetPods(t *testing.T) {
	dsOwner := &metav1.OwnerReference{Kind: "DaemonSet", Name: "nvidia-device-plugin"}
	c := fake.NewClientBuilder().
		WithObjects(gpuPod("user-job", "worker1", nil), gpuPod("dp", "worker1", dsOwner)).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	q := NvidiaQuiescer(c)
	remaining, err := q.EvictDeviceWorkloads(context.Background(), "worker1")
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1 (daemonset pod stays)", remaining)
	}
	var left corev1.PodList
	if err := c.List(context.Background(), &left); err != nil {
		t.Fatal(err)
	}
	if len(left.Items) != 1 || left.Items[0].Name != "dp" {
		t.Fatalf("wrong pods left: %+v", left.Items)
	}
}

func TestAssertQuiescedFailsWhenNotCordoned(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1"}}
	c := fake.NewClientBuilder().WithObjects(node).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	if err := NvidiaQuiescer(c).AssertQuiesced(context.Background(), "worker1"); err == nil {
		t.Fatalf("expected error for uncordoned node")
	}
}

// TestAssertQuiescedFailsOnNonExemptPod 는 노드가 cordon 됐어도 장치를 점유한 일반(비면제) pod 이
// 남아 있으면 실패해야 함을 검증한다 — 이 조건을 빼면 통과해 버리는 회귀를 막는다.
func TestAssertQuiescedFailsOnNonExemptPod(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker2"}, Spec: corev1.NodeSpec{Unschedulable: true}}
	c := fake.NewClientBuilder().
		WithObjects(node, gpuPod("user-job", "worker2", nil)).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	if err := NvidiaQuiescer(c).AssertQuiesced(context.Background(), "worker2"); err == nil {
		t.Fatalf("expected error: non-exempt pod still holds device")
	}
}

// TestAssertQuiescedPassesWhenOnlyExemptPodsHoldDevice 는 cordon 된 노드에 DaemonSet 소유 pod 과
// static/mirror pod 만 장치를 점유하고 있으면 quiesced 로 간주해야 함을 검증한다.
func TestAssertQuiescedPassesWhenOnlyExemptPodsHoldDevice(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker3"}, Spec: corev1.NodeSpec{Unschedulable: true}}
	dsOwner := &metav1.OwnerReference{Kind: "DaemonSet", Name: "nvidia-device-plugin"}
	dsPod := gpuPod("dp", "worker3", dsOwner)
	mirrorPod := gpuPod("static-pod", "worker3", &metav1.OwnerReference{Kind: "Node", Name: "worker3"})
	mirrorPod.Annotations = map[string]string{"kubernetes.io/config.mirror": "hash"}
	c := fake.NewClientBuilder().
		WithObjects(node, dsPod, mirrorPod).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	if err := NvidiaQuiescer(c).AssertQuiesced(context.Background(), "worker3"); err != nil {
		t.Fatalf("expected nil (only exempt pods hold device), got: %v", err)
	}
}

func TestUncordonRestoresSchedulable(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1"}, Spec: corev1.NodeSpec{Unschedulable: true}}
	c := fake.NewClientBuilder().WithObjects(node).Build()
	if err := NvidiaQuiescer(c).Uncordon(context.Background(), "worker1"); err != nil {
		t.Fatal(err)
	}
	var got corev1.Node
	if err := c.Get(context.Background(), types.NamespacedName{Name: "worker1"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Unschedulable {
		t.Fatalf("node still cordoned")
	}
}
