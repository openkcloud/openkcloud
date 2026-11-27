// ============================================================
// operation_reboot.go: 노드 재부팅 작업 본체 (BootID 검증 + 시도 상한)
// 상세: 드라이버 업그레이드 경로가 이미 쓰는 규율(캡처-후-Job, BootID 변화로 완료 판정)을
// 드라이버에 묶이지 않는 형태로 담는다. 재부팅은 되돌릴 수 없는 유일한 작업이라, 이 파일에서
// 가장 중요한 코드는 실행이 아니라 상한이다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/verification"
)

// maxOperationReboots 는 한 작업이 노드를 재부팅할 수 있는 횟수다.
// ACPP MIG mode 전환 경로의 상한과 같은 값이다 — 두 번으로 안 되는 상태는 세 번째로도 안 된다.
const maxOperationReboots int32 = 2

// nodeRebooter 는 노드 하나를 재부팅하고 완료를 판정한다.
type nodeRebooter struct {
	Client client.Client
	// Image 는 nsenter 를 포함한 실행 이미지다(kcloud-host-exec / driver-installer).
	Image string
}

// RequestReboot 은 대상 노드의 재부팅 Job 을 만든다(이미 있으면 아무것도 하지 않는다).
//
// 이미 있는 Job 을 다시 만들지 않는 것이 핵심이다 — 재부팅 Job 은 노드와 함께 죽으므로 "완료"
// 신호가 없고, 존재만이 "이미 쐈다" 의 유일한 증거다.
func (rb *nodeRebooter) RequestReboot(ctx context.Context, node, capturedBootID string) error {
	if rb.Image == "" {
		// 빈 이미지로 Job 을 만들면 API 서버 validation 에러로 뒤늦게 깨진다 — 의도를 담아 먼저 막는다.
		return fmt.Errorf("reboot job for node %s: reboot image not configured", node)
	}
	name := naming.AcppRebootJobName(node)
	var existing batchv1.Job
	err := rb.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: driverjob.Namespace}, &existing)
	if err == nil {
		return nil // 이미 쐈다.
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	job := driverjob.RenderRebootJobFor(metav1.OwnerReference{}, nil, node, rb.Image)
	job.Name = name
	job.OwnerReferences = nil // 소유자 없는 one-shot — 소유자가 사라져도 재부팅은 이미 예약된 것이다.
	if err := rb.Client.Create(ctx, job); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// BootObserved 는 재부팅이 실제로 끝났는지와 그 사유를 돌려준다.
//
// 노드가 Ready 여도 BootID 가 캡처값과 같으면 아직 재부팅 전이다(kubelet 이 재부팅 직전 잠깐
// Ready 를 유지한다). 캡처값이 비어 있으면 비교할 것이 없으므로 Ready 만으로 보수 전진한다 —
// 캡처 실패가 영구 교착이 되지 않게 하는 fallback 이고, 드라이버 경로와 같은 판단이다.
func (rb *nodeRebooter) BootObserved(ctx context.Context, node, capturedBootID string) (bool, string, error) {
	var n corev1.Node
	if err := rb.Client.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		if apierrors.IsNotFound(err) {
			return false, "node not found", nil
		}
		// 재부팅 중에는 API 접근이 잠깐 막힐 수 있다 — 오류가 아니라 대기로 다룬다.
		return false, "node not readable: " + err.Error(), nil
	}
	if !nodeIsReady(&n) {
		return false, "node is not Ready yet", nil
	}
	if capturedBootID != "" && n.Status.NodeInfo.BootID == capturedBootID {
		return false, "node is Ready but the boot id has not changed yet", nil
	}
	return true, "", nil
}

// nodeIsReady 는 노드 Ready 조건이다.
func nodeIsReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// RebootAttemptsFrom 은 저널에 기록된 재부팅 요청 횟수다.
// 새 status 필드를 만들지 않는 이유는 저널이 이미 durable 하고 append-only 이기 때문이다 —
// 재부팅으로 프로세스가 죽어도 이 숫자는 남는다.
func RebootAttemptsFrom(journal []npuv1alpha1.OperationJournalEntry) int32 {
	var n int32
	for _, e := range journal {
		if e.Step == operation.StepRebootRequested {
			n++
		}
	}
	return n
}

// nodeRebootParticipant 는 재부팅 작업의 본체다.
type nodeRebootParticipant struct {
	rb *nodeRebooter
}

// NewNodeRebootParticipant 는 재부팅 participant 를 만든다.
func NewNodeRebootParticipant(c client.Client, image string) operation.Participant {
	return &nodeRebootParticipant{rb: &nodeRebooter{Client: c, Image: image}}
}

// Apply 는 재부팅을 요청하거나, 이미 요청했으면 완료를 판정한다.
//
// 캡처 BootID 는 스냅샷(조정자가 적용 전에 채운 status.snapshot.bootID)에서 읽는다 — 새 필드가
// 필요 없다. StepRebootObserved 가 이미 저널에 있으면 더 확인하지 않는다 — 이 가드가 없으면
// EventBootObserved 보고 후 되돌아온 다음 pass 에서도 BootObserved 가 여전히 true 라 같은
// 사건을 또 내게 되고, Applying 에는 그 전이가 없어 에러 루프가 된다.
func (p *nodeRebootParticipant) Apply(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	if operation.HasStep(op.Status.Journal, operation.StepRebootObserved) {
		return operation.Outcome{Event: operation.EventApplyDone, Message: "node reboot already observed"}, nil
	}

	captured := ""
	if op.Status.Snapshot != nil {
		captured = op.Status.Snapshot.BootID
	}
	attempts := RebootAttemptsFrom(op.Status.Journal)

	if attempts == 0 {
		if err := p.rb.RequestReboot(ctx, op.Spec.NodeName, captured); err != nil {
			return operation.Outcome{Event: operation.EventApplyFailed, Message: err.Error()}, nil
		}
		return operation.Outcome{Event: operation.EventRebootRequired, Message: "reboot requested"}, nil
	}

	// attempts > 0: 재부팅을 이미 요청했고 완료를 기다리는 중이다.
	done, reason, err := p.rb.BootObserved(ctx, op.Spec.NodeName, captured)
	if err != nil {
		return operation.Outcome{}, err
	}
	if done {
		return operation.Outcome{Event: operation.EventBootObserved, Message: "node came back from reboot"}, nil
	}
	if attempts >= maxOperationReboots {
		// 상한을 채웠는데도 안 돌아왔다 — 더 쏴봐야 노드만 왕복한다.
		return operation.Outcome{
			Event:   operation.EventApplyFailed,
			Message: fmt.Sprintf("node did not come back after %d reboots: %s", attempts, reason),
		}, nil
	}
	return operation.Outcome{Event: operation.EventRebootRequired, Message: reason}, nil
}

// Rollback 은 아직 노드가 내려가지 않았다면 예약된 재부팅을 취소한다.
// 이미 재부팅했다면 되돌릴 방법이 없다 — 그 사실을 숨기지 않고 보상 완료로 보고하되 메시지에 남긴다.
func (p *nodeRebootParticipant) Rollback(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: naming.AcppRebootJobName(op.Spec.NodeName), Namespace: driverjob.Namespace,
	}}
	if err := p.rb.Client.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !apierrors.IsNotFound(err) {
		return operation.Outcome{Message: "could not cancel the pending reboot: " + err.Error(), RequeueAfter: participantRequeue}, nil
	}
	return operation.Outcome{
		Event:   operation.EventCompensated,
		Message: "pending reboot cancelled; a reboot that already happened cannot be undone",
	}, nil
}

// VerifyRequest 는 재부팅 자체에 대한 검증 기대가 없음을 알린다.
// 재부팅은 수단이지 목적이 아니다 — 검증해야 할 것은 재부팅을 요구한 상위 작업의 결과다.
func (p *nodeRebootParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}
