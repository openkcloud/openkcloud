// ============================================================
// deviceplugin_participant.go: DevicePluginRestart 작업 본체
// 상세: 대상 노드의 device-plugin pod 을 지워 DaemonSet 이 새로 띄우게 한다. ConfigMap·DS 스펙은
//
//	건드리지 않는다 — 광고 설정을 바꾸는 것은 다른 작업 종류의 몫이다.
//
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/verification"
)

// devicePluginParticipant 는 새 경로를 만들지 않고 기존 restartNvidiaDevicePlugin 절차를 작업
// 본체로 감싼다.
type devicePluginParticipant struct {
	r *AcceleratorPartitionPolicyReconciler
}

// NewDevicePluginParticipant 는 재시작 작업 본체를 만든다.
func NewDevicePluginParticipant(r *AcceleratorPartitionPolicyReconciler) operation.Participant {
	return &devicePluginParticipant{r: r}
}

// Apply 는 그 노드의 device-plugin pod 을 지운다. pod 이 이미 없으면 아무 일도 하지 않는다(멱등).
func (p *devicePluginParticipant) Apply(ctx context.Context,
	op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	if err := p.r.restartNvidiaDevicePlugin(ctx, op.Spec.NodeName); err != nil {
		return operation.Outcome{}, err
	}
	return operation.Outcome{Event: operation.EventApplyDone, Message: "device-plugin 재시작 요청"}, nil
}

// Rollback 은 되돌릴 것이 없다 — 재시작은 상태를 남기지 않는다. 보상 완료를 그대로 알린다.
func (p *devicePluginParticipant) Rollback(context.Context,
	*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated, Message: "재시작은 되돌릴 대상이 없다"}, nil
}

// VerifyRequest 는 소유 정책의 기대값으로 검증을 요청한다.
//
// 소유자가 정책이 아니면(예: health 가 만든 재시작) **검증하지 않는다.** 정책이 없으면 장치 관측을
// 띄우는 주체도 없어 검증이 구조적으로 실패하고, 그러면 이 작업은 항상 RolledBack 으로 끝나
// 복구 실패로 세어져 세 번이면 멀쩡한 노드가 격리된다. 판정은 verifyRequestFor 한 곳에 모아
// 두 경로가 갈라지지 않게 한다.
func (p *devicePluginParticipant) VerifyRequest(ctx context.Context,
	op *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return p.r.verifyRequestFor(ctx, op)
}
