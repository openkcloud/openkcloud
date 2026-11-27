// ============================================================
// operation_controller_test.go: Operation 컨트롤러 envtest
// 상세: 상태머신 구동·선행 기록·충돌 직렬화·보상 상한·펜싱을 본다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/verification"
)

// scriptedParticipant 는 정해진 사건을 돌려주는 participant 다 — 컨트롤러의 상태머신 구동만
// 떼어 보기 위한 seam 이고, 적용 경로 위임은 별도 스펙이 본다.
type scriptedParticipant struct {
	apply     operation.Outcome
	rollback  operation.Outcome
	applyHits *int
	// sawApplyStartedOnEntry 가 non-nil 이면, 이 participant 가 실제로 불릴 때 서버(재조회)에
	// StepApplyStarted 가 이미 기록돼 있었는지를 담는다 — write-ahead 가 "저널 상대 순서" 가 아니라
	// "참여자를 부르는 시점에 이미 durable 하게 남아 있는가" 를 뜻한다는 것을 직접 확인한다.
	sawApplyStartedOnEntry *bool
}

func (s scriptedParticipant) Apply(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	if s.applyHits != nil {
		*s.applyHits++
	}
	if s.sawApplyStartedOnEntry != nil {
		var fresh npuv1alpha1.AcceleratorOperation
		_ = k8sClient.Get(ctx, types.NamespacedName{Name: op.Name}, &fresh)
		*s.sawApplyStartedOnEntry = operation.HasStep(fresh.Status.Journal, operation.StepApplyStarted)
	}
	return s.apply, nil
}
func (s scriptedParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return s.rollback, nil
}
func (s scriptedParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}

// epochRacingParticipant 는 자신이 호출되는 동안 "다른 프로세스가 이미 Lease 를 다시 잡아
// epoch 을 앞서 올렸다" 는 상태를 서버에 직접 만든다. 실제로는 참여자 호출이 오래 걸리는 동안
// (Job 생성·대기 등) 오래된 Lease 가 만료되고 다른 프로세스가 새로 잡는 것과 같은 모양이다 —
// writeStatus 의 펜싱이 이 낡은 pass 의 계산 결과를 실제로 버리는지 검증한다.
type epochRacingParticipant struct{ name string }

func (p epochRacingParticipant) Apply(ctx context.Context, _ *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	var live npuv1alpha1.AcceleratorOperation
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: p.name}, &live); err != nil {
		return operation.Outcome{}, err
	}
	live.Status.Epoch++
	if err := k8sClient.Status().Update(ctx, &live); err != nil {
		return operation.Outcome{}, err
	}
	return operation.Outcome{Event: operation.EventApplyDone}, nil
}
func (epochRacingParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated}, nil
}
func (epochRacingParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}

// rebootingParticipant 는 참여자가 재부팅 대기를 N번 반복 보고한 뒤 적용을 마치는 실제
// acppParticipant.Apply 의 모양을 흉내낸다(rec.MigPhase 가 RebootRequested/RebootWaiting 인
// 동안 매 호출마다 EventRebootRequired 를 돌려준다). EventBootObserved 를 만드는 코드가
// 저장소 전체에 없으므로, 이 픽스처가 실제로 밟는 유일한 탈출 경로는 참여자가 재부팅 대기
// 이외의 사건으로 돌아오는 것이다.
type rebootingParticipant struct {
	remaining *int
	applyHits *int
}

func (p rebootingParticipant) Apply(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	if p.applyHits != nil {
		*p.applyHits++
	}
	if *p.remaining > 0 {
		*p.remaining--
		return operation.Outcome{Event: operation.EventRebootRequired, Message: "waiting for node reboot"}, nil
	}
	return operation.Outcome{Event: operation.EventApplyDone}, nil
}
func (rebootingParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated}, nil
}
func (rebootingParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}

func opReconciler(p operation.Participant) *AcceleratorOperationReconciler {
	return &AcceleratorOperationReconciler{
		Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(100),
		Participants: operation.Registry{
			operation.PartitionReconfigure: p,
			operation.Revalidate:           p,
		},
		Leases: &operation.LeaseManager{
			Client: k8sClient, Namespace: naming.OperatorNamespace(),
			Holder: "test", Duration: 60 * time.Second,
		},
	}
}

func mkOp(name, node string, keys []string) *npuv1alpha1.AcceleratorOperation {
	return &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: string(operation.PartitionReconfigure), NodeName: node, Vendor: "nvidia",
			TransactionID: name,
			Owner:         npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "p-" + name},
			ResourceKeys:  keys,
		},
	}
}

// seedOpNode 는 stepSnapshot 이 스냅샷을 찍을 노드를 만든다. 이 시드가 없으면 노드가 없다고
// 정직하게 판정해 RollingBack 으로 빠진다(stepSnapshot 주석 참조) — 그건 결함이 아니라 그
// 판정이 실제로 작동한다는 뜻이다. 여기서는 정상 경로를 보려는 것이므로 노드를 실제로 시드한다.
func seedOpNode(name string) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	Expect(k8sClient.Create(ctx, n)).To(Succeed())
	return n
}

// Eventually 루프가 그렇게 쓴다. 여러 걸음을 한 번에 몰아 보는 스펙이 생기면 값이 달라진다.
//
//nolint:unparam // times 는 오늘 모든 호출에서 1이다 — 매 폴링마다 한 걸음씩 보려는
func driveOp(r *AcceleratorOperationReconciler, name string, times int) string {
	for i := 0; i < times; i++ {
		_, _ = r.Reconcile(ctx, reconcileReq(name))
	}
	var got npuv1alpha1.AcceleratorOperation
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got); err != nil {
		return ""
	}
	return got.Status.Phase
}

var _ = Describe("operation controller", func() {
	// 증명: 정상 경로가 끝까지 굴러가고, 되돌릴 수 없는 행동 이전에 저널이 남는다.
	// 깨는 뮤테이션: ApplyStarted 기록을 participant 호출 뒤로 옮기면 저널 순서 어서션이 실패한다.
	It("drives an operation to Succeeded and journals before acting", func() {
		node := seedOpNode("aop-happy-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-happy", "aop-happy-node", []string{"device/0000:41:00.0/partition"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		sawApplyStarted := false
		r := opReconciler(scriptedParticipant{
			apply: operation.Outcome{Event: operation.EventApplyDone}, sawApplyStartedOnEntry: &sawApplyStarted,
		})

		Eventually(func() string { return driveOp(r, "aop-happy", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseSucceeded))

		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-happy"}, &got)).To(Succeed())
		Expect(operation.HasStep(got.Status.Journal, operation.StepApplyStarted)).To(BeTrue())
		Expect(operation.HasStep(got.Status.Journal, operation.StepCommitted)).To(BeTrue())
		// write-ahead: 참여자가 실제로 불린 순간, 서버(재조회)에 ApplyStarted 가 이미 있어야 한다 —
		// "저널 안의 상대 순서" 가 아니라 "그 write 가 참여자 호출 이전 pass 에서 이미 커밋됐는가" 다.
		Expect(sawApplyStarted).To(BeTrue(), "참여자 호출 시점에 ApplyStarted 가 서버에 없었다(write-ahead 위반)")
		// 선행 기록: 적용 시작이 완료보다 앞에 있어야 한다.
		var startIdx, finishIdx = -1, -1
		for i, e := range got.Status.Journal {
			if e.Step == operation.StepApplyStarted {
				startIdx = i
			}
			if e.Step == operation.StepApplyFinished {
				finishIdx = i
			}
		}
		Expect(startIdx).To(BeNumerically(">=", 0))
		Expect(finishIdx).To(BeNumerically(">", startIdx), "완료가 시작보다 먼저 기록됐다")
		Expect(got.Status.Epoch).To(BeNumerically(">=", 1))
	})

	// 증명: 같은 자원을 다투는 두 operation 이 동시에 적용되지 않는다.
	// 깨는 뮤테이션: Admit 호출을 빼면 두 번째도 Applying 으로 올라가 실패한다.
	It("serializes two operations that claim the same device", func() {
		node := seedOpNode("aop-conflict-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		keys := []string{"device/0000:41:00.0/partition", "node/aop-conflict-node/cordon"}
		first := mkOp("aop-conflict-a", "aop-conflict-node", keys)
		second := mkOp("aop-conflict-b", "aop-conflict-node", keys)
		Expect(k8sClient.Create(ctx, first)).To(Succeed())
		Expect(k8sClient.Create(ctx, second)).To(Succeed())
		DeferCleanup(func() {
			for _, o := range []*npuv1alpha1.AcceleratorOperation{first, second} {
				_ = k8sClient.Delete(ctx, o, client.PropagationPolicy(metav1.DeletePropagationBackground))
			}
		})

		hits := 0
		// 적용이 끝나지 않는 participant — 첫 operation 이 계속 Applying 에 머문다.
		r := opReconciler(scriptedParticipant{apply: operation.Outcome{RequeueAfter: time.Second}, applyHits: &hits})

		for i := 0; i < 6; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("aop-conflict-a"))
			_, _ = r.Reconcile(ctx, reconcileReq("aop-conflict-b"))
		}
		var a, b npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-conflict-a"}, &a)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-conflict-b"}, &b)).To(Succeed())
		Expect(a.Status.Phase).To(Equal(npuv1alpha1.OpPhaseApplying))
		Expect(b.Status.Phase).To(Equal(npuv1alpha1.OpPhaseBlocked), "두 번째 작업이 동시에 적용에 들어갔다")
		Expect(b.Status.BlockedBy).To(Equal("aop-conflict-a"))
		Expect(operation.HasStep(b.Status.Journal, operation.StepApplyStarted)).To(BeFalse(),
			"막힌 작업이 적용을 시작한 기록을 남겼다")
	})

	// 증명: 보상이 계속 실패해도 무한 재시도하지 않고 수동 복구 종점에 도달한다. 참여자가 끝까지
	// Event 없이(진행 중이라고만) 응답하는 이 픽스처는 실제 결함과 같은 모양이다 — runNvidiaTarget
	// 이 backend.Rollback 오류를 삼키고 ACPPCondRolledBack 을 안 남기면 acppParticipant.Rollback
	// 도 Event 없는 Outcome 만 돌려준다. 여기서 확인하는 것은 그 결함을 고치는 것이 아니라,
	// Coordinator 가 참여자 신호와 무관하게 자체 상한으로 종점에 반드시 도달한다는 것이다.
	// 깨는 뮤테이션: RollbackAttempts 증가나 상한 비교를 빼면 영원히 RollingBack 이라 실패한다.
	It("stops compensating after the attempt limit and lands in ManualRecoveryRequired", func() {
		node := seedOpNode("aop-manual-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-manual", "aop-manual-node", []string{"device/0000:81:00.0/partition"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		r := opReconciler(scriptedParticipant{
			apply:    operation.Outcome{Event: operation.EventApplyFailed},
			rollback: operation.Outcome{RequeueAfter: time.Millisecond}, // 영원히 안 끝나는 보상
		})
		Eventually(func() string { return driveOp(r, "aop-manual", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseManualRecoveryRequired))

		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-manual"}, &got)).To(Succeed())
		Expect(got.Status.RollbackAttempts).To(BeNumerically(">=", 3))
	})

	// 증명: 종점에 도달하면 노드 Lease 를 놓는다(다음 작업이 영원히 대기하지 않는다).
	// 깨는 뮤테이션: 종점의 Release 호출을 빼면 후속 operation 이 Lease 를 못 잡아 실패한다.
	It("releases the node lease at a terminal phase", func() {
		node := seedOpNode("aop-release-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-release", "aop-release-node", []string{"device/0000:af:00.0/partition"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		r := opReconciler(scriptedParticipant{apply: operation.Outcome{Event: operation.EventApplyDone}})
		Eventually(func() string { return driveOp(r, "aop-release", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseSucceeded))

		ok, _, err := r.Leases.Acquire(ctx, "aop-release-node", "someone-else")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "종점인데 Lease 가 안 풀렸다")
		Expect(r.Leases.Release(ctx, "aop-release-node", "someone-else")).To(Succeed())
	})

	// 증명: 종점 operation 은 더 이상 participant 를 부르지 않고, status 도 다시 쓰지 않는다.
	// step() 의 switch 에는 애초에 종점 phase 의 case 가 없어 participant 호출은 조기 반환 없이도
	// 막힌다. writeStatus 의 최종 write 도 apiserver 의 객체-동일성 no-op 최적화로 resourceVersion
	// 을 안 바꾼다. 이 조기 반환의 실제 역할은 매 reconcile 마다 불필요한 Get+Update 왕복을 막는
	// 것이다(정확성이 아니라 효율성) — 5번 더 돌아도 resourceVersion 이 그대로임을 확인해 고정한다.
	It("does nothing once terminal", func() {
		node := seedOpNode("aop-terminal-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-terminal", "aop-terminal-node", nil)
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		hits := 0
		r := opReconciler(scriptedParticipant{apply: operation.Outcome{Event: operation.EventApplyDone}, applyHits: &hits})
		Eventually(func() string { return driveOp(r, "aop-terminal", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseSucceeded))
		at := hits
		var before npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-terminal"}, &before)).To(Succeed())
		for i := 0; i < 5; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("aop-terminal"))
		}
		Expect(hits).To(Equal(at))
		// step() 의 switch 에는 애초에 종점 phase 의 case 가 없어(하나의 방어선이 이미 있다), 그것만으로도
		// participant 는 다시 안 불린다 — 이 조기 반환이 실제로 막는 것은 그게 아니라 매 reconcile 마다
		// 내용이 똑같은 status 를 또 write 하는 것이다(ACPP writeTargetStatus 의 no-op write 규율과
		// 같은 이유 — 안 그러면 write 가 자기 자신을 재큐잉해 종점에서도 핫루프가 생긴다).
		var after npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-terminal"}, &after)).To(Succeed())
		Expect(after.ResourceVersion).To(Equal(before.ResourceVersion),
			"종점에서 5번을 더 돌았는데 resourceVersion 이 바뀌었다 — 불필요한 write 가 일어났다")
	})

	// 증명: 낡은 epoch 을 기준으로 계산된 결과는 status 에 반영되지 않는다 — 펜싱이 participant
	// 호출 전체를 감싸는지를 여기서 검증한다. participant 호출이 진행되는 동안(Job 생성·대기처럼
	// 실제로 시간이 걸리는 동안) 다른 프로세스가 Lease 를 다시 잡아 epoch 을 이미 올렸다면, 이
	// pass 가 그 위에 자신의(낡은) 계산 결과를 얹으면 안 된다.
	// 깨는 뮤테이션: writeStatus 의 epoch 비교를 빼면 낡은 결과가 새 epoch 위에 그대로 올라간다.
	It("discards a stale-epoch result when a newer epoch has already been recorded", func() {
		node := seedOpNode("aop-stale-epoch-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-stale-epoch", "aop-stale-epoch-node", []string{"device/0000:99:00.0/partition"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		r := opReconciler(epochRacingParticipant{name: "aop-stale-epoch"})

		// Pending→Planning→Prepared→Quiescing(ApplyStarted 기록)→Applying, 아직 participant 호출 전.
		for i := 0; i < 4; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("aop-stale-epoch"))
		}
		var before npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-stale-epoch"}, &before)).To(Succeed())
		Expect(before.Status.Phase).To(Equal(npuv1alpha1.OpPhaseApplying))
		epochBefore := before.Status.Epoch

		// 이 pass 가 stepApply 로 들어가 participant.Apply 를 부른다 — 그 안에서 "경쟁 프로세스가
		// 이미 epoch 을 올렸다" 를 시뮬레이트한다. 이 pass 자신의 결과(Verifying 전이)는 버려져야 한다.
		_, _ = r.Reconcile(ctx, reconcileReq("aop-stale-epoch"))

		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-stale-epoch"}, &got)).To(Succeed())
		Expect(got.Status.Epoch).To(BeNumerically(">", epochBefore), "경쟁 프로세스의 epoch 갱신이 반영돼 있어야 한다")
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.OpPhaseApplying), "낡은 epoch 의 계산 결과가 새 epoch 위에 얹혔다")
		Expect(operation.HasStep(got.Status.Journal, operation.StepApplyFinished)).To(BeFalse(),
			"낡은 pass 의 저널 기록이 반영됐다")
	})

	// 증명: WaitingForReboot 에서 재부팅 대기가 반복 보고돼도 미정의 전이 오류 루프에 빠지지
	// 않고, 참여자가 더는 재부팅 대기를 보고하지 않으면 계속 진행해 Succeeded 에 도달한다.
	// 깨는 뮤테이션: WaitingForReboot 재진입 처리를 빼면 두 번째 RebootRequired 부터
	// "not a transition from" 오류가 반복돼 phase 가 영원히 WaitingForReboot 에 멈춘다.
	It("advances past a repeated reboot wait instead of an undefined-transition error loop", func() {
		node := seedOpNode("aop-reboot-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-reboot", "aop-reboot-node", []string{"device/0000:aa:00.0/partition"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		remaining := 3
		r := opReconciler(rebootingParticipant{remaining: &remaining})

		Eventually(func() string { return driveOp(r, "aop-reboot", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseWaitingForReboot))

		// 아직 재부팅 중이다 — 몇 번을 더 돌아도 같은 자리에 머물러야 한다(오류 루프가 아니다).
		for i := 0; i < 2; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("aop-reboot"))
		}
		var mid npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-reboot"}, &mid)).To(Succeed())
		Expect(mid.Status.Phase).To(Equal(npuv1alpha1.OpPhaseWaitingForReboot),
			"재부팅 대기 중에 엉뚱한 phase 로 빠졌다")

		// 재부팅이 끝난다 — 참여자가 이제 ApplyDone 을 보고한다. 여기서 타임아웃하면 미정의
		// 전이 오류 루프에 빠진 것이다.
		Eventually(func() string { return driveOp(r, "aop-reboot", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseSucceeded))
	})

	// 증명: 적용이 Lease 유효기간을 넘겨도 컨트롤러가 스스로 갱신해 소유를 유지한다.
	// 깨는 뮤테이션: 갱신 호출을 빼면 만료 후 다른 프로세스가 Lease 를 가져갈 수 있다.
	It("renews the node lease while an operation is still applying", func() {
		node := seedOpNode("aop-lease-renew-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-lease-renew", "aop-lease-renew-node", []string{"device/0000:bb:00.0/partition"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		now := time.Now()
		clock := func() time.Time { return now }
		// 끝나지 않는 적용 — 실 하드웨어에서 60초를 넘기는 경로와 같은 모양이다.
		r := opReconciler(scriptedParticipant{apply: operation.Outcome{RequeueAfter: time.Second}})
		r.Now = clock
		r.Leases.Now = clock

		Eventually(func() string { return driveOp(r, "aop-lease-renew", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseApplying))

		// Lease 유효기간(60초)을 훌쩍 넘겨 시계를 앞당긴 뒤 한 번 더 돈다.
		now = now.Add(90 * time.Second)
		_, _ = r.Reconcile(ctx, reconcileReq("aop-lease-renew"))

		ok, _, err := r.Leases.Acquire(ctx, "aop-lease-renew-node", "someone-else")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "Lease 가 갱신되지 않아 만료된 것처럼 남에게 넘어갔다")
	})

	// 증명: 참여자가 "진행 중" 만 계속 보고해도(WaitingForDrain 고착 등) 상한 시간을 넘기면
	// 수동 복구로 수렴한다.
	// 깨는 뮤테이션: 경과 시간 비교를 빼면 영원히 requeue 한다.
	It("gives up and requires manual recovery when apply stalls past the duration cap", func() {
		node := seedOpNode("aop-stall-node")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		op := mkOp("aop-stall", "aop-stall-node", []string{"device/0000:cc:00.0/partition"})
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		now := time.Now()
		clock := func() time.Time { return now }
		// 절대 끝나지 않는 참여자 — WaitingForDrain 고착과 같은 모양이다.
		r := opReconciler(scriptedParticipant{apply: operation.Outcome{RequeueAfter: time.Second}})
		r.Now = clock

		Eventually(func() string { return driveOp(r, "aop-stall", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseApplying))

		now = now.Add(21 * time.Minute) // maxApplyDuration(20분) 초과
		Eventually(func() string { return driveOp(r, "aop-stall", 1) }, "10s", "100ms").
			Should(Equal(npuv1alpha1.OpPhaseManualRecoveryRequired))
	})
})
