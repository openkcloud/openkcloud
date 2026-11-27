// ============================================================
// recoverdevice_participant.go: RecoverDevice 작업 본체
// 상세: 드라이버가 안 올라온 노드에서 **드라이버를 올리는 주체**(설치 Job pod / 드라이버 DS
// pod)를 지워 다시 뜨게 한다. 커널 모듈을 직접 건드리지도, 노드를 재부팅하지도 않는다 — 그
// 둘은 되돌릴 수 없고, health 는 되돌릴 수 없는 행동을 스스로 하지 않는다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/upgrade"
	"kcloud-operator/internal/verification"
)

// driverPodComponents 는 "드라이버를 올리는 주체" 로 인정하는 pod 들이다. 설치 Job(Mode=job)과
// 드라이버 DaemonSet(Mode=daemonset) 둘 다 대상이다 — 어느 모드로 운영하는지는 정책이 정하고,
// 복구는 그 선택을 몰라도 되어야 한다.
var driverPodComponents = []string{"driver", "driver-install"}

const driverComponentLabel = "app.kubernetes.io/component"

// driverVendorLabel 은 드라이버 pod 이 소속 벤더를 알리는 라벨이다(설치 Job·드라이버 DS 둘 다
// 이 라벨을 단다 — driver_daemonset_controller.go, driverjob/job.go).
const driverVendorLabel = "npu.ai/vendor"

// recoverDeviceParticipant 는 장치 복구 작업의 본체다.
//
// **무엇이 복구인가**: 이 단계의 답은 "드라이버를 올리는 주체를 다시 띄운다" 하나다. 운영에서
// driverLoaded=false 의 흔한 원인은 그 주체(설치 Job·DS pod)가 죽어 있는 것이고, 다시 띄우면
// 낫는다. 커널 모듈 재적재(rmmod/modprobe)와 재부팅은 **하지 않는다** — 전자는 프로세스 점유 시
// 실패하거나 커널을 불안정하게 만들고, 후자는 되돌릴 수 없어 둘 다 사람이 결정할 일이다.
type recoverDeviceParticipant struct {
	r *AcceleratorPartitionPolicyReconciler
}

// NewRecoverDeviceParticipant 는 장치 복구 본체를 만든다.
func NewRecoverDeviceParticipant(r *AcceleratorPartitionPolicyReconciler) operation.Participant {
	return &recoverDeviceParticipant{r: r}
}

// Apply 는 그 노드의 드라이버 pod 을 지운다.
//
// 지울 pod 이 하나도 없으면 **실패로 보고한다.** 이 노드에는 드라이버를 올릴 주체가 아예 없다는
// 뜻이고, 그렇다면 재시도해도 아무 일도 일어나지 않는다 — 조용히 성공했다고 말하면 health 는
// 복구가 됐다고 믿고 같은 요청을 쿨다운마다 영원히 되풀이한다. 실패로 끝나야 반복 실패가 세어지고,
// 세 번이면 격리로 올라가 사람을 부른다.
func (p *recoverDeviceParticipant) Apply(ctx context.Context,
	op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	// 드라이버 업그레이드 사이클 안이면 아무것도 지우지 않는다. 그 구간의 driverLoaded=false 는
	// 고장이 아니라 설치가 스스로 구 모듈을 내린 예정된 상태다. 여기서 설치 주체를 지우면 노드의
	// apt 가 DKMS 빌드 도중 죽어 패키지가 half-configured 로 남고, 버전 보고가 막혀 검증이 영원히
	// 실패하는 자기강화 루프가 된다.
	//
	// 실패가 아니라 완료로 보고한다 — ApplyFailed 는 세 번 쌓이면 멀쩡한 노드를 격리로 보낸다.
	if p.upgradingDriver(ctx, op.Spec.NodeName) {
		return operation.Outcome{
			Event:   operation.EventApplyDone,
			Message: "드라이버 업그레이드 중인 노드; 드라이버 pod 을 건드리지 않는다",
		}, nil
	}
	deleted, err := p.restartDriverPods(ctx, op.Spec.NodeName, op.Spec.Vendor)
	if err != nil {
		return operation.Outcome{}, err
	}
	if deleted == 0 {
		return operation.Outcome{
			Event:   operation.EventApplyFailed,
			Message: "이 노드에 드라이버를 올리는 pod 이 없다; 자동으로 되살릴 대상이 없음",
		}, nil
	}
	return operation.Outcome{Event: operation.EventApplyDone, Message: "드라이버 pod 재시작 요청"}, nil
}

// upgradingDriver 는 그 노드가 드라이버 업그레이드 사이클 안인지다. 상태기계가 Cordoning 에서
// `npu.ai/driver-upgrading` 라벨을 붙이고 Uncordoning 에서 뗀다.
//
// 노드를 못 읽으면 **업그레이드 중이 아닌 것으로 본다.** 못 읽었다고 복구를 건너뛰면 노드 객체가
// 잠시 안 보이는 순간마다 복구가 조용히 사라진다 — 그쪽이 더 나쁜 실패다.
func (p *recoverDeviceParticipant) upgradingDriver(ctx context.Context, node string) bool {
	var n corev1.Node
	if err := p.r.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return false
	}
	_, upgrading := n.Labels[upgrade.DriverUpgradingLabelKey]
	return upgrading
}

// restartDriverPods 는 그 노드의 드라이버 pod 을 지우고 지운 개수를 돌려준다(멱등). vendor 가
// 비어 있지 않으면 그 벤더의 pod 만 대상으로 한다 — 안 그러면 혼재 노드에서 한 벤더의 드라이버
// 고장이 멀쩡한 다른 벤더의 드라이버 pod 까지 쿨다운마다 지운다.
func (p *recoverDeviceParticipant) restartDriverPods(ctx context.Context, node, vendor string) (int, error) {
	deleted := 0
	for _, comp := range driverPodComponents {
		pods, err := p.driverPodsFor(ctx, node, comp, vendor)
		if err != nil {
			return deleted, err
		}
		for i := range pods {
			if err := p.r.Delete(ctx, &pods[i]); err != nil && !apierrors.IsNotFound(err) {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

// driverPodsFor 는 그 노드·component 의 드라이버 pod 목록이다. vendor 가 있으면 먼저 그 벤더로
// 좁혀 찾고, 0건이면 벤더 필터 없이 한 번 더 찾는다 — NDR 이 준 벤더값(detector 소문자화)과 pod
// 라벨(DIP Spec.Vendor 소문자)이 어긋나는 잔여 케이스에서, 좁혔다가 대상을 하나도 못 찾아
// deleted==0 → ApplyFailed → 3회 → 영구 격리로 가는 것을 막는 벨트다(주 방어는 driverRecoveryVendor
// 의 enum 화이트리스트).
func (p *recoverDeviceParticipant) driverPodsFor(ctx context.Context, node, comp, vendor string) ([]corev1.Pod, error) {
	if vendor != "" {
		pods, err := p.listDriverPods(ctx, node, comp, vendor)
		if err != nil {
			return nil, err
		}
		if len(pods) > 0 {
			return pods, nil
		}
	}
	return p.listDriverPods(ctx, node, comp, "")
}

func (p *recoverDeviceParticipant) listDriverPods(ctx context.Context, node, comp, vendor string) ([]corev1.Pod, error) {
	lbls := client.MatchingLabels{driverComponentLabel: comp}
	if vendor != "" {
		lbls[driverVendorLabel] = vendor
	}
	var pods corev1.PodList
	if err := p.r.List(ctx, &pods, lbls, client.MatchingFields{"spec.nodeName": node}); err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// Rollback 은 되돌릴 것이 없다 — 재시작은 상태를 남기지 않는다.
func (p *recoverDeviceParticipant) Rollback(context.Context,
	*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	return operation.Outcome{Event: operation.EventCompensated, Message: "재시작은 되돌릴 대상이 없다"}, nil
}

// VerifyRequest 는 판정을 verifyRequestFor 한 곳에 맡긴다.
//
// health 가 만든 작업은 소유 정책이 없어 검증이 구조적으로 통과할 수 없다 — 여기서 따로 판단하면
// 그 사실을 잊은 채 "정직하게" 검증을 요구하게 되고, 그러면 이 작업은 항상 RolledBack 으로 끝나
// 멀쩡한 노드가 세 번 만에 격리된다.
func (p *recoverDeviceParticipant) VerifyRequest(ctx context.Context,
	op *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return p.r.verifyRequestFor(ctx, op)
}
