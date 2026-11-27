// ============================================================
// operation_epoch_test.go: 노드 잠금 재획득과 세대 인상 envtest
// 상세: 잠금을 잃은 작업이 되찾으러 돌아가는지, 되찾을 때만 세대가 오르는지 본다.
//
//	재부팅은 잠금 유효기간보다 오래 걸리므로 이 경로가 실제로 발화한다.
//
// 생성일: 2026-08-01
// ============================================================
package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
)

// deleteNodeLease 는 노드 Lease 를 치운다(envtest 는 GC 가 없다). operation_lease_test.go 의
// cleanupLease 와 같은 방식이다 — 그쪽은 Describe 안의 클로저라 여기서 못 부른다.
func deleteNodeLease(node string) {
	_ = k8sClient.Delete(ctx, &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Name: operation.NodeLeaseName(node), Namespace: naming.OperatorNamespace(),
	}}, client.PropagationPolicy(metav1.DeletePropagationBackground))
}

// seedEpochOp 는 노드와 operation 을 함께 시드한다. 노드가 없으면 stepSnapshot 이 스냅샷을 못
// 찍는다며 RollingBack 으로 보내서 Applying 에 닿지도 못한다 — 그러면 두 스펙 다 세대가 아니라
// 엉뚱한 이유로 깨진다.
func seedEpochOp(name, node string, keys []string) {
	n := seedOpNode(node)
	op := mkOp(name, node, keys)
	Expect(k8sClient.Create(ctx, op)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		_ = k8sClient.Delete(ctx, n)
		deleteNodeLease(node)
	})
}

var _ = Describe("operation epoch and lease re-acquisition", func() {
	// 증명: 정상 진행 중에는 세대가 오르지 않는다(갱신은 재획득이 아니다).
	// 깨는 뮤테이션: Reconcile 의 갱신 블록에서 세대를 올리면 Applying 에 닿을 때 2 이상이 되어
	//                실패한다.
	// 안 잡히는 것: stepAcquire 의 인상 조건을 "항상" 으로 바꾸는 뮤테이션. stepAcquire 는 계획
	//              단계에서만 돌고 이 스펙에서는 한 번만 불리므로 관측상 구분되지 않는다.
	It("does not bump the epoch while merely renewing", func() {
		seedEpochOp("epoch-renew", "epoch-renew-node", []string{"device/0000:41:00.0/partition"})
		r := opReconciler(scriptedParticipant{apply: operation.Outcome{RequeueAfter: time.Second}})
		for i := 0; i < 6; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("epoch-renew"))
		}
		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "epoch-renew"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.OpPhaseApplying), "적용 단계에 도달하지 못했다")
		Expect(got.Status.Epoch).To(BeNumerically("==", 1), "갱신인데 세대가 올랐다")
	})

	// 증명: 잠금을 남에게 뺏기면 계획 단계로 돌아가고, 되찾을 때 세대가 오른다.
	//       이 경로가 없으면 작업이 "잠금을 잃었다" 메시지만 남기고 영원히 같은 자리를 돈다.
	// 깨는 뮤테이션: 갱신 실패 시 단계를 되돌리지 않으면 Applying 에 머물러 실패한다.
	//                세대 인상 조건을 되돌리면 Epoch 가 1 에 머물러 실패한다.
	It("returns to planning and bumps the epoch after losing and regaining the lease", func() {
		node := "epoch-steal-node"
		seedEpochOp("epoch-steal", node, []string{"device/0000:81:00.0/partition"})
		r := opReconciler(scriptedParticipant{apply: operation.Outcome{RequeueAfter: time.Second}})
		for i := 0; i < 4; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("epoch-steal"))
		}
		var mid npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "epoch-steal"}, &mid)).To(Succeed())
		Expect(mid.Status.Phase).To(Equal(npuv1alpha1.OpPhaseApplying))
		Expect(mid.Status.Epoch).To(BeNumerically("==", 1))

		By("다른 프로세스가 잠금을 가져간다")
		// 만료 처리로 회수되도록 시계를 미래로 둔 관리자를 쓴다 — 프로덕션이 만들 수 있는 상태다
		// (재부팅으로 우리 갱신이 밀리는 동안 잠금이 만료되고 다른 작업이 회수한다).
		future := time.Now().Add(10 * time.Minute)
		thief := &operation.LeaseManager{
			Client: k8sClient, Namespace: naming.OperatorNamespace(),
			Holder: "other-op", Duration: 60 * time.Second, Now: func() time.Time { return future },
		}
		ok, _, err := thief.Acquire(ctx, node, "other-op")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "만료된 잠금을 회수하지 못했다 — 이 스펙은 아무것도 검증하지 못한다")

		By("우리 작업은 계획 단계로 돌아간다")
		_, _ = r.Reconcile(ctx, reconcileReq("epoch-steal"))
		var lost npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "epoch-steal"}, &lost)).To(Succeed())
		Expect(lost.Status.Phase).To(Equal(npuv1alpha1.OpPhasePlanning), "잠금을 잃고도 적용 단계에 머물렀다")
		Expect(lost.Status.LeaseHolder).To(BeEmpty())

		By("남이 놓으면 되찾으면서 세대가 오른다")
		Expect(thief.Release(ctx, node, "other-op")).To(Succeed())
		for i := 0; i < 3; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("epoch-steal"))
		}
		var back npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "epoch-steal"}, &back)).To(Succeed())
		Expect(back.Status.Epoch).To(BeNumerically("==", 2), "재획득인데 세대가 그대로다")
	})
})
