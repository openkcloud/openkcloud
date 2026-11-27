// ============================================================
// operation_recovery_test.go: 적용 실패 후 복구 판정 배선 envtest
// 상세: 실패 보고가 곧바로 보상으로 가지 않고, 관측 결과에 따라 갈리는지 본다.
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
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/verification"
)

// observingParticipant 는 적용 실패를 보고하면서 지정한 관측을 검증 요청으로 준다.
type observingParticipant struct {
	observed map[string]string
	errs     map[string]string
	expected string
}

func (o observingParticipant) Apply(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventApplyFailed, Message: "apply reported failure"}, nil
}
func (o observingParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated}, nil
}
func (o observingParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{
		ObservedGeometry:  o.observed,
		ObservationErrors: o.errs,
		Expectation:       verification.Expectation{Geometry: o.expected},
	}, true, nil
}

// seedRecoveryOp 는 노드와 operation 을 함께 시드한다. 노드가 없으면 stepSnapshot 이 "스냅샷을
// 못 찍는다" 며 RollingBack 으로 보내서 Applying 에 닿지도 못한다 — 그러면 rollback 을 기대하는
// 스펙이 엉뚱한 이유로 통과한다.
func seedRecoveryOp(name string) {
	node := seedOpNode(name + "-node")
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
	op := mkOp(name, name+"-node", []string{"device/0000:41:00.0/partition"})
	Expect(k8sClient.Create(ctx, op)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
	})
}

// verifyPendingParticipant 는 적용은 성공하지만 검증 근거가 아직 안 모인 상태를 만든다
// (기대 geometry 는 있는데 노드 보고가 없다 = 등급을 못 준다).
type verifyPendingParticipant struct{}

func (verifyPendingParticipant) Apply(context.Context,
	*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventApplyDone, Message: "적용 완료"}, nil
}
func (verifyPendingParticipant) Rollback(context.Context,
	*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated}, nil
}

// NodeName 을 반드시 채운다 — 비우면 검증기가 노드를 못 읽어 **오류**로 끝나고, 그러면 이
// 스펙이 유예가 아니라 오류 재큐잉 때문에 Verifying 에 머물러 통과해 버린다.
func (verifyPendingParticipant) VerifyRequest(_ context.Context,
	op *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{
		NodeName:    op.Spec.NodeName,
		Vendor:      "nvidia",
		Expectation: verification.Expectation{Geometry: nvidia.GeometrySummary("1g.6gb", 4)},
	}, true, nil
}

var _ = Describe("operation verification grace", func() {
	// 증명: 근거가 아직 안 모였다는 이유로 성공한 변경을 되돌리지 않는다. 유예를 넘겨야 되돌린다.
	//
	// 관측 갱신은 node-agent 주기(30초)와 재광고만큼 늦을 수 있다 — 그 지연을 불일치로 읽으면
	// 재부팅을 낀 되돌리기가 헛돌게 된다.
	//
	// 깨는 뮤테이션: stepVerify 의 유예 분기를 지우면 첫 검증에서 RolledBack 이 되어 실패한다.
	It("waits for the evidence instead of rolling back a good change", func() {
		name := "aop-verify-grace"
		seedRecoveryOp(name)
		r := opReconciler(verifyPendingParticipant{})
		r.Verification = &verification.Verifier{Client: k8sClient}
		base := time.Now()
		r.Now = func() time.Time { return base }

		for i := 0; i < 8; i++ {
			_, err := r.Reconcile(ctx, reconcileReq(name))
			Expect(err).NotTo(HaveOccurred())
		}
		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.OpPhaseVerifying),
			"근거가 아직 안 모였는데 되돌렸다(또는 확정했다)")

		// 유예를 넘기면 더 기다리지 않는다 — 영원히 Verifying 에 머무는 것도 종점 부재다.
		r.Now = func() time.Time { return base.Add(maxVerifyGrace + time.Minute) }
		Eventually(func() string { return driveOp(r, name, 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseRolledBack))
	})
})

var _ = Describe("operation recovery after a reported apply failure", func() {
	drive := func(name string, p operation.Participant, want string) {
		seedRecoveryOp(name)
		r := opReconciler(p)
		Eventually(func() string { return driveOp(r, name, 1) }, "10s", "100ms").Should(Equal(want))
	}

	// 증명: 장치가 이미 목표라면 실패 보고에도 불구하고 검증으로 넘어가 성공한다.
	// 깨는 뮤테이션: decideAfterApplyFailure 를 곧바로 RollingBack 으로 바꾸면 실패한다.
	It("commits when the device already matches the target", func() {
		geom := nvidia.GeometrySummary("1g.6gb", 4)
		drive("aop-recover-commit", observingParticipant{
			observed: map[string]string{"0000:41:00.0": geom}, errs: map[string]string{}, expected: geom,
		}, npuv1alpha1.OpPhaseSucceeded)
	})

	// 증명: 장치가 목표와 다르면 보상으로 간다.
	// 깨는 뮤테이션: geometry 비교를 빼면 commit 으로 새어 실패한다.
	It("rolls back when the device does not match", func() {
		drive("aop-recover-rollback", observingParticipant{
			observed: map[string]string{"0000:41:00.0": nvidia.GeometrySummary("2g.12gb", 2)},
			errs:     map[string]string{}, expected: nvidia.GeometrySummary("1g.6gb", 4),
		}, npuv1alpha1.OpPhaseRolledBack)
	})

	// 증명: 관측이 안 되면 보상하지 않고 기다리며, 관측 실패 횟수가 쌓인다.
	// 깨는 뮤테이션: ObservationOK 판정을 빼면 곧바로 RolledBack 이 되어 실패한다.
	It("withholds judgement while the device cannot be observed", func() {
		name := "aop-recover-wait"
		seedRecoveryOp(name)
		r := opReconciler(observingParticipant{
			observed: map[string]string{},
			errs:     map[string]string{"0000:41:00.0": "nvidia-smi failed"},
			expected: nvidia.GeometrySummary("1g.6gb", 4),
		})
		// 앞의 네 걸음은 Pending→Planning→Prepared→Quiescing→Applying 이라 판정이 돌지 않는다.
		// 그 뒤 세 걸음이 실제 판정이고, 상한(5)에 닿지 않아 Applying 에 머물러야 한다.
		for i := 0; i < 7; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq(name))
		}
		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.OpPhaseApplying), "관측도 못 했는데 되돌렸다")
		Expect(got.Status.ObserveFailures).To(BeNumerically(">", 0))
	})

	// 증명: 관측 개념이 없는 참여자(검증 대상 아님)의 적용 실패는 곧바로 보상으로 간다.
	//       "관측을 못 하는 것" 과 "관측이 실패한 것" 은 다르다 — 섞으면 기대 geometry 를 애초에
	//       안 만드는 작업이 전부 대기만 하다 사람을 부르고, 보상 상한이 세어 볼 기회도 없어진다.
	// 깨는 뮤테이션: ObservationOK 기본값을 false 로 되돌리면 대기만 하다 수동 복구로 새어 실패한다.
	It("compensates when the participant has no observation to offer", func() {
		name := "aop-recover-noobs"
		seedRecoveryOp(name)
		r := opReconciler(scriptedParticipant{
			apply:    operation.Outcome{Event: operation.EventApplyFailed},
			rollback: operation.Outcome{Event: operation.EventCompensated},
		})
		Eventually(func() string { return driveOp(r, name, 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseRolledBack))
	})

	// 증명: 관측 실패가 상한을 넘으면 사람에게 넘긴다 — 영원히 Applying 에 머물지 않는다.
	// 깨는 뮤테이션: RecoverManual 갈래를 대기로 접으면 Applying 에 머물러 실패한다.
	It("escalates to manual recovery once observation keeps failing", func() {
		name := "aop-recover-manual"
		seedRecoveryOp(name)
		r := opReconciler(observingParticipant{
			observed: map[string]string{},
			errs:     map[string]string{"0000:41:00.0": "nvidia-smi failed"},
			expected: nvidia.GeometrySummary("1g.6gb", 4),
		})
		Eventually(func() string { return driveOp(r, name, 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseManualRecoveryRequired))
	})
})
