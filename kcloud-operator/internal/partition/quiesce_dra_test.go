// ============================================================
// quiesce_dra_test.go: DRA 로 장치를 쥔 워크로드의 quiesce 판정 테스트
// 상세: DRA pod 은 resources.limits 가 비어 있고 ResourceClaim 이 장치를 쥔다 — 배출(EvictDeviceWorkloads)과
// 완료 판정(AssertQuiesced) 양쪽이 그 축을 봐야 apply 경로가 워크로드 밑에서 GPU 를 재구성하지 않는다.
// 생성일: 2026-08-05
// ============================================================
package partition

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// draPod 는 AcceleratorWorkload 의 DRA 경로가 실제로 렌더하는 모양이다(renderDeployment):
// resources.limits 는 비어 있고, pod.spec.resourceClaims + 컨테이너 resources.claims 로만 장치를 쥔다.
func draPod(name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName:       node,
			ResourceClaims: []corev1.PodResourceClaim{{Name: "accel", ResourceClaimTemplateName: ptr.To("img-accel")}},
			Containers: []corev1.Container{{
				Name:      "workload",
				Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "accel"}}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// allocatedClaim 은 노드-로컬 장치를 쥔 claim 이다. DRA 드라이버는 matchFields metadata.name 으로
// 그 노드를 지정한다(AllocationResult.NodeSelector).
func allocatedClaim(name, node string) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{
			NodeSelector: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchFields: []corev1.NodeSelectorRequirement{{
					Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{node},
				}},
			}}},
		}},
	}
}

func draClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().WithObjects(objs...).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
}

// TestAssertQuiescedFailsOnDRAWorkload 는 apply 경로(acpp_migmode 가 -mig 1 직전에 부르는 바로 그
// 함수)가 DRA 워크로드를 정지로 오판하지 않음을 고정한다.
func TestAssertQuiescedFailsOnDRAWorkload(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w1"}, Spec: corev1.NodeSpec{Unschedulable: true}}
	c := draClient(node, draPod("dra-job", "w1"), allocatedClaim("dra-job-accel", "w1"))
	if err := NvidiaQuiescer(c).AssertQuiesced(context.Background(), "w1"); err == nil {
		t.Fatal("quiesce must fail: a DRA workload still holds a device on this node")
	}
}

// TestAssertQuiescedFailsOnAllocatedClaimWithoutPod 는 claim 축이 pod 축과 별개로 살아 있는지를
// 고정한다 — Pod 이 사라져도 claim 이 deallocate 되기 전에는 장치가 여전히 잡혀 있다.
func TestAssertQuiescedFailsOnAllocatedClaimWithoutPod(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w1"}, Spec: corev1.NodeSpec{Unschedulable: true}}
	c := draClient(node, allocatedClaim("orphan", "w1"))
	if err := NvidiaQuiescer(c).AssertQuiesced(context.Background(), "w1"); err == nil {
		t.Fatal("quiesce must fail: a ResourceClaim is still allocated on this node")
	}
}

// TestAssertQuiescedIgnoresClaimWithoutNodeSelector 는 nodeSelector 가 없는 claim
// (network-attached·AllNodes 풀)이 노드를 묶지 않음을 고정한다 — 매칭하면 그런 claim 하나가
// 클러스터의 모든 노드 quiesce 를 영구히 막는다.
func TestAssertQuiescedIgnoresClaimWithoutNodeSelector(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w1"}, Spec: corev1.NodeSpec{Unschedulable: true}}
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "net", Namespace: "default"},
		Status:     resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{}},
	}
	if err := NvidiaQuiescer(draClient(node, claim)).AssertQuiesced(context.Background(), "w1"); err != nil {
		t.Fatalf("a claim usable on any node must not block quiescing: %v", err)
	}
}

// TestEvictDeviceWorkloadsEvictsDRAPod 는 배출 대상 판정도 같은 축을 보는지 고정한다. 판정만
// 고치면 apply 경로는 영원히 WaitingForDrain 에 머문다 — 아무도 그 pod 에게 나가라고 하지 않으므로.
func TestEvictDeviceWorkloadsEvictsDRAPod(t *testing.T) {
	c := draClient(draPod("dra-job", "w1"))
	remaining, err := NvidiaQuiescer(c).EvictDeviceWorkloads(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0 (the DRA pod is evictable)", remaining)
	}
	var left corev1.PodList
	if err := c.List(context.Background(), &left); err != nil {
		t.Fatal(err)
	}
	if len(left.Items) != 0 {
		t.Fatalf("DRA pod was not evicted: %+v", left.Items)
	}
}
