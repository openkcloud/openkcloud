// ============================================================
// operation_driver_participant.go: 드라이버 업그레이드 상태머신 어댑터
// 상세: UpgradeStateMachine 은 cordon·drain·설치·재부팅·검증·보상을 이미 완결적으로 수행한다.
// 이 파일은 그것을 한 번 전진시키고 결과 상태를 조정자 사건으로 옮기는 얇은 층이다 — 상태머신을
// 복제하면 두 경로가 갈라지고, 갈라진 순간 직렬화가 무의미해진다.
// 생성일: 2026-08-01 | 수정일: 2026-10-12
// ============================================================
package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/verification"
)

// driverParticipant 는 드라이버 작업(설치·업그레이드·롤백)의 본체다.
type driverParticipant struct {
	r *DriverUpgradeReconciler
}

// NewDriverParticipant 는 드라이버 상태머신에 위임하는 participant 를 만든다.
func NewDriverParticipant(r *DriverUpgradeReconciler) operation.Participant {
	return &driverParticipant{r: r}
}

// ResourceKeysForDriver 는 드라이버 작업이 건드리는 자원을 선언한다.
//
// 노드 cordon·reboot 를 반드시 포함한다 — 드라이버 교체는 노드를 잠그고 재부팅할 수 있으므로,
// 장치가 달라도 **같은 노드의** 파티션 작업과는 동시에 진행되면 안 된다. 장치 목록이 비어도
// 노드 키는 남긴다(장치를 아직 모르는 상태에서도 노드 단위 충돌은 잡혀야 한다).
func ResourceKeysForDriver(nodeName string, pcis []string) []string {
	keys := make([]string, 0, len(pcis)+2)
	for _, pci := range pcis {
		if pci == "" {
			continue
		}
		keys = append(keys, string(operation.DeviceDriverKey(pci)))
	}
	return append(keys,
		string(operation.NodeCordonKey(nodeName)),
		string(operation.NodeRebootKey(nodeName)))
}

// driverOutcomeFor 는 드라이버 상태를 조정자 사건으로 옮긴다(순수 — 클러스터를 읽지 않는다).
//
// Idle 은 "끝났다" 와 "아직 시작 안 했다" 를 같은 값으로 표현한다. 버전이 수렴했을 때만 완료로
// 본다 — 그러지 않으면 사이클이 시작되기 전 첫 pass 에서 작업이 곧바로 성공해 버린다.
func driverOutcomeFor(st *npuv1alpha1.DriverUpgradeStateStatus) operation.Outcome {
	switch st.State {
	case npuv1alpha1.UpgradeStateIdle, "":
		if st.DesiredVersion == "" || st.CurrentVersion == st.DesiredVersion {
			return operation.Outcome{Event: operation.EventApplyDone, Message: "driver cycle idle at " + st.CurrentVersion}
		}
		// 아직 사이클이 시작되지 않았다 — 상태머신이 다음 pass 에 UpgradeRequired 로 옮긴다.
		return operation.Outcome{Message: "driver cycle not started yet", RequeueAfter: participantRequeue}
	case npuv1alpha1.UpgradeStateFailed, npuv1alpha1.UpgradeStateUnverifiedVersion, npuv1alpha1.UpgradeStateHostAhead:
		return operation.Outcome{Event: operation.EventApplyFailed, Message: "driver state " + st.State + ": " + st.Message}
	case npuv1alpha1.UpgradeStateRebootRequired, npuv1alpha1.UpgradeStateRebooting:
		return operation.Outcome{Event: operation.EventRebootRequired, Message: "driver upgrade is rebooting the node"}
	case npuv1alpha1.UpgradeStateRollback:
		// 상태머신이 스스로 보상에 들어갔다 — 조정자에게는 적용 실패다. 조정자가 보상 단계로
		// 내려가야 RollbackAttempts 상한이 세어지고 수동 복구 종점이 열린다.
		return operation.Outcome{Event: operation.EventApplyFailed, Message: "driver upgrade entered rollback"}
	}
	return operation.Outcome{Message: "driver state " + st.State, RequeueAfter: participantRequeue}
}

// Apply 는 드라이버 상태머신을 한 번 전진시킨다.
func (p *driverParticipant) Apply(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	st, err := p.advance(ctx, op)
	if err != nil {
		return operation.Outcome{Event: operation.EventApplyFailed, Message: err.Error()}, nil
	}
	if st == nil {
		return operation.Outcome{Event: operation.EventApplyFailed, Message: "driver upgrade state is gone"}, nil
	}
	return driverOutcomeFor(st), nil
}

// Rollback 은 같은 상태머신을 계속 돌려 보상 종착을 확인한다.
//
// 새 보상 로직을 만들지 않는다 — 상태머신이 Rollback 상태에서 이미 이전 이미지로 되돌리고
// MaxRollbackAttempts 를 세며, 초과하면 Failed 로 착지한다. 이 층이 더하는 것은 그 종착을
// 사건으로 옮기는 것뿐이다.
func (p *driverParticipant) Rollback(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	st, err := p.advance(ctx, op)
	if err != nil || st == nil {
		return operation.Outcome{Message: "rollback pass failed", RequeueAfter: participantRequeue}, nil
	}
	switch st.State {
	case npuv1alpha1.UpgradeStateIdle:
		// 이전 버전으로 되돌아가 사이클이 닫혔다.
		return operation.Outcome{Event: operation.EventCompensated, Message: "driver restored to " + st.CurrentVersion}, nil
	case npuv1alpha1.UpgradeStateFailed, npuv1alpha1.UpgradeStateUnverifiedVersion, npuv1alpha1.UpgradeStateHostAhead:
		// 상태머신이 스스로 포기했다 — 되돌리기가 끝나지 않았다는 뜻이다.
		return operation.Outcome{Event: operation.EventCompensationFailed, Message: "driver rollback ended in " + st.State}, nil
	}
	return operation.Outcome{Message: "driver rollback in progress: " + st.State, RequeueAfter: participantRequeue}, nil
}

// VerifyRequest 는 드라이버 작업의 검증 요청이다.
//
// 드라이버 경로는 자체 validator 체인(Validating 상태)이 이미 실 드라이버 버전과 헬스를
// 확인하므로, 조정자 계층에서 파티션용 기대 geometry 를 만들어 다시 검증하지 않는다.
// 검증 대상 아님(false)을 정직하게 돌려준다 — 없는 기대를 지어내면 그 자체가 거짓 근거다.
func (p *driverParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}

// advance 는 소유 DriverUpgradeState 를 읽어 상태머신을 한 번 전진시키고 갱신된 status 를 준다.
//
// TransitionState 는 state 를 메모리에서만 전이시킨다 — 영속은 호출자 몫이다(Reconcile 이
// TransitionState 직후 Status().Update 를 부르는 것과 같은 이유). 빠뜨리면 이 pass 의
// cordon·drain·설치 같은 부수효과가 클러스터에 남지 않아 다음 pass 가 같은 상태를 또 읽어
// 무한 반복한다. 상태머신 오류 여부와 무관하게 먼저 영속한다(부분 전이도 재개의 근거다).
func (p *driverParticipant) advance(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (*npuv1alpha1.DriverUpgradeStateStatus, error) {
	var state npuv1alpha1.DriverUpgradeState
	if err := p.r.Get(ctx, types.NamespacedName{Name: op.Spec.Owner.Name}, &state); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if op.Spec.Owner.UID != "" && string(state.UID) != op.Spec.Owner.UID {
		// 같은 이름으로 재생성된 다른 객체다 — 옛 작업이 새 사이클을 건드리면 안 된다.
		return nil, fmt.Errorf("owner uid mismatch for %q", op.Spec.Owner.Name)
	}
	// 정책 조회는 실 컨트롤러(driver_upgrade_controller.go 의 Reconcile)가 쓰는 것과 같은
	// 함수를 그대로 재사용한다 — 조회 로직을 여기서 새로 쓰지 않는다.
	policy, err := p.r.findMatchingPolicy(ctx, state.Spec.Vendor, state.Spec.Model)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, fmt.Errorf("no driver install policy for node %q", state.Spec.NodeName)
	}
	_, _, terr := p.r.StateMachine.TransitionState(ctx, &state, policy)
	if uerr := p.r.Status().Update(ctx, &state); uerr != nil {
		return nil, uerr
	}
	if terr != nil {
		return nil, terr
	}
	return &state.Status, nil
}
