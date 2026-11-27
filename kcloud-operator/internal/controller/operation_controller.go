// ============================================================
// operation_controller.go: AcceleratorOperation 상태머신 구동
// 상세: 허가(충돌 판정 + Lease) → 선행 기록 → participant 적용 → 검증 → commit/보상.
//
//	되돌릴 수 없는 행동 이전에 저널을 반드시 영속한다 — 그 순서가 크래시 복구의 유일한 근거다.
//
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/verification"
)

const (
	// maxRollbackAttempts 는 보상 시도 상한이다(드라이버 업그레이드 경로의 기본값과 같은 3).
	// 참여자가 성공·실패를 명확히 보고하지 못하는 경로(예: 아래 stepRollback 주석 참조)에서도
	// 이 상한은 참여자의 신호와 무관하게 적용된다 — Coordinator 의 유일한 자체 종점 보장이다.
	maxRollbackAttempts = 3
	// maxApplyDuration 은 stepApply 가 "진행 중" 만 계속 보고받아도 되는 최대 경과 시간이다.
	// rollback 과 달리 재부팅·모드 전환을 낀 정상 적용도 수 분씩 걸릴 수 있어 시도 횟수 대신
	// 넉넉한 절대 시간으로 상한을 둔다 — WaitingForDrain 처럼 외부 개입이 영원히 없는 고착
	// 상태만 여기에 걸리고, 정상적으로 느린 적용은 걸리지 않게 하려는 값이다.
	maxApplyDuration = 20 * time.Minute
	// maxVerifyGrace 는 검증기가 "아직 등급을 못 준다" 를 계속 말해도 되는 최대 시간이다.
	// 관측은 적용 직후 곧바로 갱신되지 않는다 — node-agent 는 30초 주기로 보고하고, device-plugin
	// 은 재광고까지 더 걸린다. 그 지연을 곧바로 불일치로 읽으면 **성공한 변경을 되돌린다** — 되돌리기
	// 자체가 재부팅을 낀 파괴적 절차라 기다리는 비용보다 훨씬 비싸다. health 쪽 광고 유예(기본
	// 30초)보다 넉넉히 잡되, 적용 상한(20분)보다는 훨씬 짧게 둔다.
	maxVerifyGrace = 3 * time.Minute
	// maxObserveFailures 는 복구 판정용 관측이 연속 실패해도 되는 횟수다. 넘으면 수동 복구로
	// 보낸다 — 관측 없이 rollback 을 실행하는 것이 이 계통에서 가장 위험한 행동이다. 상수만
	// 정의돼 있고, ObserveFailures 필드를 실제로 세고 비교하는 크래시 복구 판정 로직은 아직
	// 없다. CRD 계약(AcceleratorOperationStatus.ObserveFailures)과 이름을 맞춰 두는 것이 이
	// 상수의 유일한 역할이다.
	//nolint:unused // 다음 태스크(크래시 복구 판정)의 소비자를 기다리는 상수 — 위 설명 참조.
	maxObserveFailures = 5
	// opRequeue 는 진행 중 operation 재확인 간격이다.
	opRequeue = 10 * time.Second
)

// AcceleratorOperationReconciler 는 트랜잭션 상태머신을 굴린다.
type AcceleratorOperationReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
	Participants operation.Registry
	Leases       *operation.LeaseManager
	// Verification 은 Verifying 단계의 commit oracle 이다(nil 이면 검증 없이 통과 — 테스트 전용).
	Verification *verification.Verifier
	// Gates 는 안전 축을 하나씩 끄는 손잡이다. 제로값이 "전부 켬" 이므로 운영 배선은 채우지 않는다 —
	// 비교군을 재는 시험만 채운다(internal/operation/gates.go).
	Gates operation.Gates
	Now   func() time.Time
}

// +kubebuilder:rbac:groups=npu.ai,resources=acceleratoroperations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratoroperations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;delete

func (r *AcceleratorOperationReconciler) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *AcceleratorOperationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	var op npuv1alpha1.AcceleratorOperation
	if err := r.Get(ctx, req.NamespacedName, &op); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if op.Status.Phase == "" {
		op.Status.Phase = npuv1alpha1.OpPhasePending
	}
	if operation.IsTerminal(op.Status.Phase) {
		// 종점에서는 아무것도 하지 않는다. Lease 는 종점 진입 시 이미 놓은 상태다.
		return ctrl.Result{}, nil
	}

	// Lease 갱신 — stepAcquire 이후의 모든 pass 에서 갱신한다(적용이 Lease 유효기간을 넘기면
	// 자원 키가 안 겹치는 같은 노드 작업은 Lease 만으로 직렬화되므로 그 창이 열린다). Acquire 는
	// 이미 우리 것이면 RenewTime 을 갱신하는 함수이므로 재사용한다 — 새 경로가 아니다.
	if op.Status.LeaseHolder == op.Name && !r.Gates.DisableLease {
		ok, expiry, lerr := r.Leases.Acquire(ctx, op.Spec.NodeName, op.Name)
		if lerr != nil {
			logger.Error(lerr, "lease renewal failed", "node", op.Spec.NodeName)
			return ctrl.Result{RequeueAfter: opRequeue}, nil
		}
		if !ok {
			// 갱신이 밀려 다른 프로세스가 이미 가져갔다 — 더는 우리 것이 아니므로 하드웨어를
			// 건드리지 않는다. 계획 단계로 돌아가 다시 줄을 선다: 그러지 않으면 "잠금을 잃었다"
			// 메시지만 남긴 채 같은 자리를 영원히 돈다(되찾을 경로가 없다). 재부팅이 잠금
			// 유효기간보다 오래 걸리므로 이 경로는 실제로 발화한다.
			op.Status.LeaseHolder = ""
			op.Status.Phase = npuv1alpha1.OpPhasePlanning
			op.Status.Message = "lost the node lease to another process; re-queuing for it"
			if serr := r.writeStatus(ctx, &op); serr != nil {
				return ctrl.Result{}, serr
			}
			return ctrl.Result{RequeueAfter: opRequeue}, nil
		}
		op.Status.LeaseExpiry = &expiry
	}

	next, res, err := r.step(ctx, &op)
	if err != nil {
		logger.Error(err, "operation step failed", "phase", op.Status.Phase, "op", op.Name)
		return ctrl.Result{RequeueAfter: opRequeue}, nil
	}
	if next != "" {
		if operation.IsTerminal(next) && !r.Gates.DisableLease {
			if rerr := r.Leases.Release(ctx, op.Spec.NodeName, op.Name); rerr != nil {
				logger.Error(rerr, "lease release failed", "node", op.Spec.NodeName)
			}
		}
		op.Status.Phase = next
	}
	if serr := r.writeStatus(ctx, &op); serr != nil {
		return ctrl.Result{}, serr
	}
	if next != "" && !operation.IsTerminal(next) {
		return ctrl.Result{Requeue: true}, nil // 전이했으면 곧바로 다음 단계를 본다.
	}
	return res, nil
}

// step 은 현재 phase 에서 한 걸음 나아간다. 돌려주는 phase 가 빈 문자열이면 전이 없음이다.
func (r *AcceleratorOperationReconciler) step(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (string, ctrl.Result, error) {
	switch op.Status.Phase {
	case npuv1alpha1.OpPhasePending, npuv1alpha1.OpPhaseBlocked:
		return r.stepAdmit(ctx, op)
	case npuv1alpha1.OpPhasePlanning:
		return r.stepAcquire(ctx, op)
	case npuv1alpha1.OpPhasePrepared:
		return r.stepSnapshot(ctx, op)
	case npuv1alpha1.OpPhaseQuiescing:
		// 정지(cordon/배출)는 적용 경로 안에 이미 있다. 여기서는 선행 기록만 남기고 넘어간다.
		r.journal(op, operation.StepApplyStarted, "")
		return npuv1alpha1.OpPhaseApplying, ctrl.Result{}, nil
	case npuv1alpha1.OpPhaseApplying, npuv1alpha1.OpPhaseWaitingForReboot:
		return r.stepApply(ctx, op)
	case npuv1alpha1.OpPhaseVerifying:
		return r.stepVerify(ctx, op)
	case npuv1alpha1.OpPhaseRollingBack:
		return r.stepRollback(ctx, op)
	}
	return "", ctrl.Result{}, nil
}

// stepAdmit 은 활성 operation 전체에 대해 진입을 판정한다.
func (r *AcceleratorOperationReconciler) stepAdmit(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (string, ctrl.Result, error) {
	var list npuv1alpha1.AcceleratorOperationList
	if err := r.List(ctx, &list); err != nil {
		return "", ctrl.Result{}, err
	}
	active := make([]operation.Claim, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		if o.Name == op.Name || operation.IsTerminal(o.Status.Phase) || o.Status.Phase == npuv1alpha1.OpPhaseBlocked {
			continue
		}
		if o.Status.Phase == "" || o.Status.Phase == npuv1alpha1.OpPhasePending {
			continue // 아직 자원 청구가 없다.
		}
		active = append(active, operation.ClaimOf(o))
	}
	verdict, blockedBy := operation.Admit(operation.ClaimOf(op), active)
	if r.Gates.DisableConflict {
		verdict, blockedBy = operation.AdmitNow, ""
	}
	if verdict == operation.AdmitBlocked {
		op.Status.BlockedBy = blockedBy
		op.Status.Message = "waiting for " + blockedBy
		if op.Status.Phase == npuv1alpha1.OpPhaseBlocked {
			return "", ctrl.Result{RequeueAfter: opRequeue}, nil
		}
		return npuv1alpha1.OpPhaseBlocked, ctrl.Result{RequeueAfter: opRequeue}, nil
	}
	op.Status.BlockedBy = ""
	op.Status.Message = ""
	r.journal(op, operation.StepPlanned, verdictName(verdict))
	return npuv1alpha1.OpPhasePlanning, ctrl.Result{}, nil
}

// stepAcquire 는 노드 Lease 를 잡고 epoch 을 올린다.
func (r *AcceleratorOperationReconciler) stepAcquire(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (string, ctrl.Result, error) {
	// 이 pass 를 시작할 때 우리가 소유자였는지 — 세대 인상 판정의 유일한 기준이다.
	//
	// 오늘은 방어용이다: 이 함수는 계획 단계에서만 돌고, 그 단계에 이르는 경로(최초 진입, 잠금
	// 상실 복귀)는 둘 다 소유자를 비워 두므로 heldBefore 는 사실상 항상 거짓이다. 그래도 조건을
	// 두는 이유는 소유자를 쥔 채 계획 단계로 돌아오는 경로가 생기면 그때는 갱신을 재획득으로
	// 오인해 세대를 올리기 때문이다 — 그러면 진행 중인 자기 결과가 매 pass 낡은 것으로 걸러진다.
	if r.Gates.DisableLease {
		// 잠금 없이 곧바로 진행한다. 세대는 올리지 않는다 — 잠금이 없으면 세대라는 개념 자체가
		// 성립하지 않고(무엇을 기준으로 낡았다고 하겠는가), 펜싱 축은 따로 잰다.
		return npuv1alpha1.OpPhasePrepared, ctrl.Result{}, nil
	}
	heldBefore := op.Status.LeaseHolder == op.Name
	ok, expiry, err := r.Leases.Acquire(ctx, op.Spec.NodeName, op.Name)
	if err != nil {
		return "", ctrl.Result{}, err
	}
	if !ok {
		op.Status.Message = "waiting for the node lease"
		return "", ctrl.Result{RequeueAfter: opRequeue}, nil
	}
	op.Status.LeaseHolder = op.Name
	op.Status.LeaseExpiry = &expiry
	// epoch 은 **새로 잡을 때만** 오른다. 단순 갱신에도 올리면 진행 중인 자기 작업의 결과가 매번
	// 낡은 것으로 걸러진다. 반대로 재획득에 안 올리면(이전 구현: 저널에 LeaseAcquired 가 남으면
	// 두 번 다시 참이 안 되는 조건) 세대가 0→1 로만 가서 writeStatus 의 방어가 명목만 남는다 —
	// 남이 가져갔다 되찾은 사실이 숫자에 안 나타난다.
	if !heldBefore {
		op.Status.Epoch = operation.NextEpoch(op.Status.Epoch)
	}
	r.journal(op, operation.StepLeaseAcquired, fmt.Sprintf("epoch %d", op.Status.Epoch))
	return npuv1alpha1.OpPhasePrepared, ctrl.Result{}, nil
}

// stepSnapshot 은 mutation 이전 상태를 영속한다.
func (r *AcceleratorOperationReconciler) stepSnapshot(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (string, ctrl.Result, error) {
	var node corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: op.Spec.NodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			// 노드가 없으면 되돌릴 목표를 만들 수 없다 — 적용을 시작하지 않는다.
			op.Status.Message = "node not found; refusing to apply without a snapshot"
			return npuv1alpha1.OpPhaseRollingBack, ctrl.Result{}, nil
		}
		return "", ctrl.Result{}, err
	}
	if op.Status.Snapshot == nil {
		op.Status.Snapshot = operation.BuildSnapshot(&node, nil, "", false)
	}
	r.journal(op, operation.StepSnapshotTaken, "")
	r.journal(op, operation.StepQuiesced, "")
	return npuv1alpha1.OpPhaseQuiescing, ctrl.Result{}, nil
}

// stepApply 는 participant 를 한 번 부른다. 저널의 ApplyStarted 는 **이 호출 이전에** 이미
// 영속돼 있다(Quiescing 단계에서 기록 후 status write). 참여자 호출 자체를 펜싱으로 감싸지
// 않는 이유는 fencing.go 의 설계 그대로다 — 옛 프로세스가 참여자를 다시 부르는 것은 위험하지
// 않다(같은 의도의 하드웨어 작업은 이름·명령 해시가 같아 멱등하게 재사용된다). 위험한 것은
// 그 결과를 status 에 쓰는 것이고, 그 write 는 writeStatus 가 매 pass 마다 펜싱한다.
func (r *AcceleratorOperationReconciler) stepApply(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (string, ctrl.Result, error) {
	p, ok := r.Participants.For(operation.Type(op.Spec.Type))
	if !ok {
		op.Status.Message = "no participant registered for " + op.Spec.Type
		return npuv1alpha1.OpPhaseManualRecoveryRequired, ctrl.Result{}, nil
	}
	out, err := p.Apply(ctx, op)
	if err != nil {
		return "", ctrl.Result{}, err
	}
	op.Status.Message = out.Message

	if op.Status.Phase == npuv1alpha1.OpPhaseWaitingForReboot {
		if out.Event == operation.EventBootObserved {
			// 재부팅 participant 가 완료를 명시적으로 알렸다 — 정의된 전이를 그대로 탄다.
			r.journal(op, operation.StepRebootObserved, out.Message)
			next, ok := operation.Next(op.Status.Phase, out.Event)
			if !ok {
				return "", ctrl.Result{}, fmt.Errorf("participant returned %q which is not a transition from %q", out.Event, op.Status.Phase)
			}
			return next, ctrl.Result{}, nil
		}
		if out.Event == operation.EventRebootRequired {
			// 아직 재부팅 대기 중이다 — 전이 없이 같은 자리에서 다시 확인한다. 전이표에 자기 전이를
			// 추가하는 대신 여기서 처리하는 이유: Reconcile 은 전이가 있으면 곧바로 재큐잉으로
			// 나가버리는데, 자기 전이는 매 pass 마다 "전이가 있다" 이므로 10초 주기 대신 노드가
			// 돌아올 때까지 API 서버를 계속 두드리는 busy-loop 이 된다. 전이 없음(next="")으로 두면
			// 기존 in-progress 분기와 같은 절제된 requeue 를 쓴다. 상한 검사(Event=="" 일 때만
			// 도는 아래 분기)도 재부팅이 영원히 끝나지 않는 노드가 종점에 도달하도록 비켜 가지 않는다.
			if started := applyStartedAt(op.Status.Journal); started != nil && r.now().Sub(started.Time) >= maxApplyDuration {
				op.Status.Message = fmt.Sprintf("node did not come back within %s: %s", maxApplyDuration, out.Message)
				return npuv1alpha1.OpPhaseManualRecoveryRequired, ctrl.Result{}, nil
			}
			return "", ctrl.Result{RequeueAfter: opRequeue}, nil
		}
		// 재부팅 완료를 명시적으로 알리지 않는 참여자(드라이버 경로)는 대기 이외의 사건으로
		// 돌아온 것 자체가 그 대기가 끝났다는 신호다. phase 를 Applying 으로 되돌려야 아래
		// Next() 가 정의된 전이를 찾는다 — 그러지 않으면 WaitingForReboot 에 머문 채
		// ApplyDone/ApplyFailed 가 도착해 미정의 전이 오류로 떨어진다.
		op.Status.Phase = npuv1alpha1.OpPhaseApplying
	}

	if out.Event == "" {
		// 참여자가 계속 "진행 중" 만 보고하면(WaitingForDrain 고착 등) 상한 없이는 영원히
		// requeue 한다. 시도 "횟수" 가 아니라 저널에 이미 있는 ApplyStarted 시각으로부터의
		// 경과 시간을 기준으로 삼는다 — 폴링 간격이 바뀌어도(participantRequeue 등) 기준이
		// 흔들리지 않고, 새 상태 필드도 필요 없다.
		if started := applyStartedAt(op.Status.Journal); started != nil && r.now().Sub(started.Time) >= maxApplyDuration {
			op.Status.Message = fmt.Sprintf("apply stalled for over %s with no progress: %s", maxApplyDuration, out.Message)
			return npuv1alpha1.OpPhaseManualRecoveryRequired, ctrl.Result{}, nil
		}
		return "", ctrl.Result{RequeueAfter: requeueOr(out.RequeueAfter)}, nil
	}
	if out.Event == operation.EventApplyFailed {
		// 실패 보고가 곧 rollback 은 아니다 — 장치가 이미 목표에 도달했는데 프로세스만 죽었을 수
		// 있다. 관측으로 commit 과 rollback 중 하나를 고른다.
		return r.decideAfterApplyFailure(ctx, op, out.Message)
	}
	if out.Event == operation.EventApplyDone {
		r.journal(op, operation.StepApplyFinished, out.Message)
		r.journal(op, operation.StepVerifyStarted, "")
	}
	if out.Event == operation.EventRebootRequired {
		r.journal(op, operation.StepRebootRequested, out.Message)
	}
	next, ok := operation.Next(op.Status.Phase, out.Event)
	if !ok {
		return "", ctrl.Result{}, fmt.Errorf("participant returned %q which is not a transition from %q", out.Event, op.Status.Phase)
	}
	return next, ctrl.Result{}, nil
}

// applyStartedAt 는 저널에서 ApplyStarted 시각을 찾는다(없으면 nil). AppendJournal 이
// (step, epoch) 로 중복을 걸러내므로 정상 경로에서 한 번만 존재한다.
func applyStartedAt(entries []npuv1alpha1.OperationJournalEntry) *metav1.Time {
	for i := range entries {
		if entries[i].Step == operation.StepApplyStarted {
			return &entries[i].At
		}
	}
	return nil
}

// lastJournalAt 은 저널에서 그 단계의 **마지막** 기록 시각을 찾는다(없으면 nil).
// applyStartedAt 이 처음 것을 쓰는 것과 반대인 이유: 세대가 바뀌면 같은 단계가 다시 기록되고,
// 검증 유예가 재려는 것은 전체 경과가 아니라 **이번 검증 시도**의 경과이기 때문이다.
func lastJournalAt(entries []npuv1alpha1.OperationJournalEntry, step string) *metav1.Time {
	var out *metav1.Time
	for i := range entries {
		if entries[i].Step == step {
			out = &entries[i].At
		}
	}
	return out
}

// decideAfterApplyFailure 는 적용 실패 보고 뒤 commit·rollback·대기 중 하나를 고른다.
// 이 판정이 도는 유일한 지점이다 — 진행 중인 정상 적용에는 개입하지 않는다(개입하면 아직 안
// 끝난 적용을 실패로 오인해 되돌린다).
func (r *AcceleratorOperationReconciler) decideAfterApplyFailure(ctx context.Context, op *npuv1alpha1.AcceleratorOperation, msg string) (string, ctrl.Result, error) {
	in := operation.RecoveryInput{
		Journal:            op.Status.Journal,
		MaxObserveFailures: maxObserveFailures,
		ObserveFailures:    op.Status.ObserveFailures,
	}
	// 관측 개념이 없는 참여자(VerifyRequest 가 "대상 아님" 이거나 오류)는 **관측 실패가 아니다**.
	// 둘을 섞으면 기대 geometry 를 애초에 안 만드는 작업들이 전부 대기만 하다 사람을 부르게 되고,
	// 보상 상한이 세어 볼 기회조차 없어진다. 관측 실패는 참여자가 실제로 오류를 보고했을 때뿐이고,
	// 관측 근거가 없는 경우는 아래 판정에서 기대 geometry 가 비어 rollback 으로 간다(기존 동작).
	in.ObservationOK = true
	if p, ok := r.Participants.For(operation.Type(op.Spec.Type)); ok {
		if req, applicable, err := p.VerifyRequest(ctx, op); err == nil && applicable {
			in.ObservationOK = len(req.ObservationErrors) == 0
			in.ObservedGeometry = req.ObservedGeometry
			in.ExpectedGeometry = req.Expectation.Geometry
		}
	}
	decision, reason := operation.DecideRecovery(in)
	op.Status.Message = msg + " | recovery: " + reason
	switch decision {
	case operation.RecoverCommit:
		op.Status.ObserveFailures = 0
		r.journal(op, operation.StepApplyFinished, "recovered by observation")
		r.journal(op, operation.StepVerifyStarted, "")
		return npuv1alpha1.OpPhaseVerifying, ctrl.Result{}, nil
	case operation.RecoverWait:
		op.Status.ObserveFailures++
		return "", ctrl.Result{RequeueAfter: opRequeue}, nil
	case operation.RecoverManual:
		return npuv1alpha1.OpPhaseManualRecoveryRequired, ctrl.Result{}, nil
	case operation.RecoverRestart:
		// 저널에 적용 기록이 없다 — 되돌릴 것이 없으므로 다음 pass 가 다시 시도한다.
		// 오늘의 호출 경로에서는 닿지 않는다(ApplyStarted 가 Quiescing 에서 이미 기록된다).
		return "", ctrl.Result{RequeueAfter: opRequeue}, nil
	}
	op.Status.ObserveFailures = 0
	return npuv1alpha1.OpPhaseRollingBack, ctrl.Result{}, nil
}

// stepVerify 는 Stage 1 검증기를 commit oracle 로 쓴다.
func (r *AcceleratorOperationReconciler) stepVerify(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (string, ctrl.Result, error) {
	p, ok := r.Participants.For(operation.Type(op.Spec.Type))
	if !ok {
		return npuv1alpha1.OpPhaseManualRecoveryRequired, ctrl.Result{}, nil
	}
	req, applicable, err := p.VerifyRequest(ctx, op)
	if err != nil {
		return "", ctrl.Result{}, err
	}
	if !applicable || r.Verification == nil || r.Gates.DisableVerify {
		// 검증 대상이 아니거나 검증기가 없다 — 근거 없이 성공을 주장하지 않되, 진행은 시킨다.
		// (검증기 미주입은 테스트 구성이고, 운영 배선은 항상 주입한다.)
		r.journal(op, operation.StepCommitted, "verification not applicable")
		return npuv1alpha1.OpPhaseSucceeded, ctrl.Result{}, nil
	}
	ev, verr := r.Verification.Verify(ctx, req)
	if verr != nil {
		return "", ctrl.Result{}, verr
	}
	if ev.Status.Level == "" {
		// 등급이 없다는 것은 "틀렸다" 가 아니라 "아직 근거가 안 모였다" 일 수 있다. 유예 안에서는
		// 되돌리지 않고 다시 본다 — 되돌리기는 그 자체가 파괴적이라 되돌릴 수 없는 오판이 된다.
		if started := lastJournalAt(op.Status.Journal, operation.StepVerifyStarted); started != nil &&
			r.now().Sub(started.Time) < maxVerifyGrace {
			op.Status.Message = "verification not settled yet: " + ev.Status.Reason
			return "", ctrl.Result{RequeueAfter: opRequeue}, nil
		}
		op.Status.Message = fmt.Sprintf("verification disagreed after %s: %s", maxVerifyGrace, ev.Status.Reason)
		return npuv1alpha1.OpPhaseRollingBack, ctrl.Result{}, nil
	}
	r.journal(op, operation.StepCommitted, "evidence level "+ev.Status.Level)
	return npuv1alpha1.OpPhaseSucceeded, ctrl.Result{}, nil
}

// stepRollback 은 보상을 시도하고 횟수를 제한한다. 참여자가 명확한 성공(EventCompensated)·
// 실패(EventCompensationFailed) 어느 쪽도 보고하지 못하는 경로가 실제로 있다 — NVIDIA MIG
// rollback 은 runNvidiaTarget 이 backend.Rollback 오류를 삼키고 ACPPCondRolledBack condition
// 을 안 남기면 acppParticipant.Rollback 이 Event 없는 Outcome(진행 중 취급)만 돌려준다. 그
// 경로를 고치지 않고, RollbackAttempts 상한을 참여자 신호와 무관하게 세어 영원히 "진행 중"만
// 말해도 이 카운터가 종점을 강제하게 한다.
func (r *AcceleratorOperationReconciler) stepRollback(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (string, ctrl.Result, error) {
	p, ok := r.Participants.For(operation.Type(op.Spec.Type))
	if !ok {
		return npuv1alpha1.OpPhaseManualRecoveryRequired, ctrl.Result{}, nil
	}
	r.journal(op, operation.StepRollbackStarted, "")
	op.Status.RollbackAttempts++
	out, err := p.Rollback(ctx, op)
	if err != nil {
		return "", ctrl.Result{}, err
	}
	op.Status.Message = out.Message
	if out.Event == operation.EventCompensated {
		r.journal(op, operation.StepRollbackFinished, out.Message)
		return npuv1alpha1.OpPhaseRolledBack, ctrl.Result{}, nil
	}
	if out.Event == operation.EventCompensationFailed || op.Status.RollbackAttempts >= maxRollbackAttempts {
		op.Status.Message = fmt.Sprintf("compensation gave up after %d attempts: %s", op.Status.RollbackAttempts, out.Message)
		return npuv1alpha1.OpPhaseManualRecoveryRequired, ctrl.Result{}, nil
	}
	return "", ctrl.Result{RequeueAfter: requeueOr(out.RequeueAfter)}, nil
}

// journal 은 현재 epoch 으로 단계를 기록한다(멱등).
func (r *AcceleratorOperationReconciler) journal(op *npuv1alpha1.AcceleratorOperation, step, detail string) {
	if r.Gates.DisableJournal {
		return
	}
	op.Status.Journal = operation.AppendJournal(op.Status.Journal, step, op.Status.Epoch,
		metav1.NewTime(r.now()), detail)
}

// writeStatus 는 펜싱을 통과한 경우에만 status 를 쓴다. 서버의 세대가 이 pass 시작 시점의
// 세대보다 앞서 있으면 다른 프로세스가 이미 잠금을 새로 잡고 진행한 것이므로 우리(낡은)
// 결과를 버린다. 비교가 "정확히 같음" 이 아니라 방향 있는 비교(>)인 이유: stepAcquire 에서
// 방금 세대를 올렸지만 아직 한 번도 쓰지 않은 정상 케이스에서는 서버가 이전 세대를 들고
// 있다 — 물어야 할 질문은 "계산하는 동안 남이 앞서 갔는가" 다.
func (r *AcceleratorOperationReconciler) writeStatus(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) error {
	var fresh npuv1alpha1.AcceleratorOperation
	if err := r.Get(ctx, client.ObjectKey{Name: op.Name}, &fresh); err != nil {
		return client.IgnoreNotFound(err)
	}
	if fresh.Status.Epoch > op.Status.Epoch && !r.Gates.DisableFencing {
		// 낡은 프로세스다 — status 를 쓰지 않는다. 이것이 "이전 epoch 결과 폐기" 의 실행 지점이다.
		return nil
	}
	op.Status.ObservedGeneration = op.Generation
	op.ResourceVersion = fresh.ResourceVersion
	return r.Status().Update(ctx, op)
}

func requeueOr(d time.Duration) time.Duration {
	if d <= 0 {
		return opRequeue
	}
	return d
}

// verdictName 은 저널에 남길 판정 이름이다.
func verdictName(v operation.Admission) string {
	switch v {
	case operation.AdmitAfter:
		return "admitted after a dependency"
	default:
		return "admitted"
	}
}

func (r *AcceleratorOperationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.AcceleratorOperation{}).
		Complete(r)
}

// WriteStatusForTest 는 status 기록 경로(펜싱 포함)를 시험이 직접 태울 수 있게 연다.
// 비교군 측정이 "낡은 프로세스가 쓰면 어떻게 되는가" 를 묻는데, 그 판정은 writeStatus 안에 있고
// 그 경로를 우회하면 잰 값이 무의미해진다.
func (r *AcceleratorOperationReconciler) WriteStatusForTest(ctx context.Context,
	op *npuv1alpha1.AcceleratorOperation) error {
	return r.writeStatus(ctx, op)
}
