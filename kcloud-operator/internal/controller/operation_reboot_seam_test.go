// ============================================================
// operation_reboot_seam_test.go: 재부팅 왕복과 삭제 복원의 이음매
// 상세: 각 단계가 개별로 검증돼도 단계 사이 전이는 따로 보지 않으면 빠진다. 대기 상태를
// 빠져나오는 경로가 둘(명시적 완료 사건 / 대기 이외 사건)이므로 둘 다 여기서 밟는다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/verification"
)

// rebootThenDoneParticipant 는 첫 호출에 재부팅을 요구하고, 그 다음부터 완료 사건을 낸다 —
// 재부팅 완료를 **명시적으로 알리지 않는** 참여자(드라이버 경로)의 관용구다.
type rebootThenDoneParticipant struct{ calls *int }

func (p rebootThenDoneParticipant) Apply(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	*p.calls++
	if *p.calls == 1 {
		return operation.Outcome{Event: operation.EventRebootRequired, Message: "reboot needed"}, nil
	}
	return operation.Outcome{Event: operation.EventApplyDone, Message: "converged after reboot"}, nil
}
func (p rebootThenDoneParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated}, nil
}
func (p rebootThenDoneParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}

// rebootThenObservedParticipant 는 재부팅 완료를 명시적으로 알리는 참여자(재부팅 작업)의 관용구다.
type rebootThenObservedParticipant struct{ calls *int }

func (p rebootThenObservedParticipant) Apply(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	*p.calls++
	if *p.calls == 1 {
		return operation.Outcome{Event: operation.EventRebootRequired, Message: "reboot requested"}, nil
	}
	if *p.calls == 2 {
		return operation.Outcome{Event: operation.EventBootObserved, Message: "node came back"}, nil
	}
	return operation.Outcome{Event: operation.EventApplyDone, Message: "done after boot"}, nil
}
func (p rebootThenObservedParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated}, nil
}
func (p rebootThenObservedParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}

var _ = Describe("reboot round trip seam", func() {
	// 증명: 재부팅 완료를 명시적으로 알리는 참여자가 대기 상태를 빠져나와 종점에 도달한다.
	// 깨는 뮤테이션: stepApply 의 EventBootObserved 처리를 지우면 미정의 전이 오류로 떨어져 실패한다.
	It("completes when the participant announces the boot explicitly", func() {
		calls := 0
		// stepSnapshot 은 대상 노드가 없으면 "되돌릴 목표가 없다" 며 곧바로 RollingBack 으로
		// 보낸다(정직한 판정이지 결함이 아니다) — 노드를 시드하지 않으면 이 스펙이 재부팅 참여자에
		// 도달하기도 전에 끝나 아무것도 증명하지 못한다.
		node := seedOpNode("seam-observed-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("seam-observed", "seam-observed-node", []string{"node/seam-observed-node/reboot"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
			cleanupNodeLease("seam-observed-node")
		})
		r := opReconciler(rebootThenObservedParticipant{calls: &calls})

		Eventually(func() string { return driveOp(r, "seam-observed", 1) }, "15s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseSucceeded))

		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "seam-observed"}, &got)).To(Succeed())
		Expect(operation.HasStep(got.Status.Journal, operation.StepRebootRequested)).To(BeTrue())
		Expect(operation.HasStep(got.Status.Journal, operation.StepRebootObserved)).To(BeTrue(),
			"재부팅 완료가 이력에 남지 않았다")
		Expect(calls).To(BeNumerically(">=", 3), "참여자가 왕복을 다 돌지 않았다")
	})

	// 증명: 재부팅 완료를 알리지 않는 참여자도 대기 상태를 빠져나온다(대기 이외 사건이 곧 신호다).
	// 깨는 뮤테이션: stepApply 의 phase 되돌림을 지우면 WaitingForReboot 에서 ApplyDone 이 도착해
	//                미정의 전이 오류로 떨어져 실패한다.
	It("completes when the participant merely stops asking for a reboot", func() {
		calls := 0
		node := seedOpNode("seam-implicit-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("seam-implicit", "seam-implicit-node", []string{"node/seam-implicit-node/reboot"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
			cleanupNodeLease("seam-implicit-node")
		})
		r := opReconciler(rebootThenDoneParticipant{calls: &calls})

		Eventually(func() string { return driveOp(r, "seam-implicit", 1) }, "15s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseSucceeded))
		Expect(calls).To(BeNumerically(">=", 2))
	})

	// 증명: 대기 상태가 오류 루프가 아니다 — 참여자가 계속 재부팅을 요구해도 조정자는 오류 없이
	//       절제된 간격으로 다시 확인하고, 결국 적용 상한에 걸려 종점에 도달한다.
	// 깨는 뮤테이션: 대기 중 자기 전이를 만들면 즉시 재큐잉으로 나가 busy-loop 이 되고,
	//                적용 상한 검사를 지우면 영원히 종점에 도달하지 못해 실패한다.
	It("does not spin while the reboot never completes", func() {
		calls := 0
		node := seedOpNode("seam-stuck-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("seam-stuck", "seam-stuck-node", []string{"node/seam-stuck-node/reboot"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
			cleanupNodeLease("seam-stuck-node")
		})
		stuck := alwaysRebootingParticipant{calls: &calls}
		r := opReconciler(stuck)
		// 시계를 앞당겨 적용 상한을 넘긴다 — 실제로 20분을 기다리지 않고 상한 경로를 밟는다.
		base := time.Now()
		r.Now = func() time.Time { return base }

		// 먼저 대기 상태까지 몰아간다. Pending→Planning→Prepared→Quiescing→Applying 각 단계는
		// 전이할 때마다 정당하게 Requeue:true 를 낸다(operation_controller.go Reconcile 의
		// "전이했으면 곧바로 다음 단계를 본다") — 이 사실을 놓치고 매 Reconcile 호출에서 바로
		// Requeue==false 를 기대하면 대기 상태에 도달하기도 전에 이 스펙이 실패한다(이 시험이
		// 잡으려는 이음매 결함과는 무관한 오탐).
		Eventually(func() string { return driveOp(r, "seam-stuck", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseWaitingForReboot))

		// 이제부터가 실제 스펙이다: 이미 대기 중일 때 반복 호출해도 busy-loop 이 아니어야 한다.
		for i := 0; i < 3; i++ {
			res, err := r.Reconcile(ctx, reconcileReq("seam-stuck"))
			Expect(err).NotTo(HaveOccurred(), "대기 중에 오류가 났다 — 오류 루프의 증상이다")
			//nolint:staticcheck // Requeue 는 deprecated 지만 operation_controller.go 의 Reconcile 이
			// 지금도 이 필드로 즉시 재큐잉(busy-loop)을 신호한다(line 130) — 이 스펙이 직접
			// 증명하려는 대상이 그 필드 자체이므로 그대로 확인한다.
			Expect(res.Requeue).To(BeFalse(), "대기 중에 즉시 재큐잉으로 나갔다(busy-loop)")
			Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		}
		var mid npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "seam-stuck"}, &mid)).To(Succeed())
		Expect(mid.Status.Phase).To(Equal(npuv1alpha1.OpPhaseWaitingForReboot))

		r.Now = func() time.Time { return base.Add(maxApplyDuration + time.Minute) }
		Eventually(func() string { return driveOp(r, "seam-stuck", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseManualRecoveryRequired))
	})
})

// alwaysRebootingParticipant 는 영원히 재부팅만 요구한다.
type alwaysRebootingParticipant struct{ calls *int }

func (p alwaysRebootingParticipant) Apply(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	*p.calls++
	return operation.Outcome{Event: operation.EventRebootRequired, Message: "still rebooting"}, nil
}
func (p alwaysRebootingParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated}, nil
}
func (p alwaysRebootingParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}
