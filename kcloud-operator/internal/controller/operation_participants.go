// ============================================================
// operation_participants.go: participant 구현 (기존 적용 경로 위임)
// 상세: Coordinator 의 작업 본체를 기존 ACPP 경로로 위임한다. 적용 로직을 복제하지 않는 것이
//
//	이 파일의 존재 이유다 — 복제하면 두 경로가 갈라지고, 갈라진 순간 직렬화가 무의미해진다.
//
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/verification"
)

// participantRequeue 는 진행 중 상태에서 다음 확인까지 기다리는 간격이다. 기존 ACPP 진행-중
// phase(VerifyingAllocatable 등)가 이미 쓰는 값과 맞춘다 — 새 간격을 고를 이유가 없다.
const participantRequeue = 15 * time.Second

// revalidateParticipant 는 읽기 전용 작업이다. 되돌릴 것도 바꿀 것도 없고, 검증 단계만 의미가
// 있다 — 그래서 Apply 는 곧바로 완료를 알린다.
type revalidateParticipant struct {
	r *AcceleratorPartitionPolicyReconciler
}

// NewRevalidateParticipant 는 재검증 participant 를 만든다.
func NewRevalidateParticipant(r *AcceleratorPartitionPolicyReconciler) operation.Participant {
	return &revalidateParticipant{r: r}
}

func (p *revalidateParticipant) Apply(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	// 바꿀 것이 없다. 검증은 Coordinator 의 Verifying 단계가 공통으로 수행한다.
	return operation.Outcome{Event: operation.EventApplyDone, Message: "revalidate: nothing to mutate"}, nil
}

func (p *revalidateParticipant) Rollback(context.Context, *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	// 아무것도 바꾸지 않았으므로 되돌릴 것도 없다.
	return operation.Outcome{Event: operation.EventCompensated, Message: "revalidate: nothing to compensate"}, nil
}

func (p *revalidateParticipant) VerifyRequest(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return p.r.verifyRequestFor(ctx, op)
}

// acppOwnerKind 는 정책이 소유한 작업의 소유자 Kind 다.
const acppOwnerKind = "AcceleratorPartitionPolicy"

// verifyRequestFor 는 operation 의 소유 정책 저널에서 검증 기대값을 유도한다.
// 값을 새로 계산하지 않고 저널(ApplyRecord)에서 가져오는 이유는, 기대값을 그 자리에서 다시
// 계산하면 "적용 당시 무엇을 목표했는가" 가 아니라 "지금 무엇을 목표할 것인가" 를 검증하게 되기
// 때문이다. 이 둘은 spec 이 바뀐 순간 달라진다.
func (r *AcceleratorPartitionPolicyReconciler) verifyRequestFor(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	var acpp npuv1alpha1.AcceleratorPartitionPolicy
	// 소유자가 정책이 아닌 작업(예: health 가 만든 재검증)은 **검증 대상이 아니다.** 정책이 없으면
	// 장치 관측을 띄우는 주체도 없어 DeviceObservation 이 언제나 "관측 전무" 로 실패하고, health 가
	// 만든 재검증은 **항상** RolledBack 으로 끝나 세 번이면 멀쩡한 노드가 격리된다. 실제 재검증은
	// 정책 경로(ACPP reconcile)가 하고, 이 작업의 성공은 "재검증을 요청했다" 까지를 뜻한다.
	if op.Spec.Owner.Kind != "" && op.Spec.Owner.Kind != acppOwnerKind {
		return verification.Request{}, false, nil
	}
	if err := r.Get(ctx, types.NamespacedName{Name: op.Spec.Owner.Name}, &acpp); err != nil {
		if apierrors.IsNotFound(err) {
			return verification.Request{}, false, nil // 정책이 사라졌다 — 검증할 기대가 없다.
		}
		return verification.Request{}, false, fmt.Errorf("verify request: get owner %q: %w", op.Spec.Owner.Name, err)
	}
	rec := getApplyRecord(&acpp, op.Spec.NodeName)
	if rec.Profile == "" {
		return verification.Request{}, false, nil // 파티션 저널이 없다(공유 전용 등) — 기대 geometry 가 없다.
	}
	geom, errs, oerr := r.observeForNode(ctx, op.Spec.NodeName, rec.GPUPCIs)
	if oerr != nil {
		return verification.Request{}, false, oerr
	}
	return verification.Request{
		NodeName:     op.Spec.NodeName,
		Vendor:       acpp.Spec.Vendor,
		SourcePolicy: acpp.Name,
		Generation:   acpp.Generation,
		Expectation: verification.Expectation{
			Profile:        rec.Profile,
			CountPerDevice: rec.Count,
			Geometry:       nvidia.GeometrySummary(rec.Profile, rec.Count),
			Allocatable:    expectedAllocatable(rec),
			ProbeResource:  "nvidia.com/mig-" + rec.Profile,
		},
		ObservedGeometry:  geom,
		ObservationErrors: errs,
	}, true, nil
}

// observeForNode 는 노드의 PCI 목록을 관측해 geometry/오류 두 맵으로 나눈다.
// 관측기는 reconciler 의 seam 을 그대로 쓴다(envtest 는 fake 를 주입한다).
func (r *AcceleratorPartitionPolicyReconciler) observeForNode(ctx context.Context, node string, pcis []string) (map[string]string, map[string]string, error) {
	devs, err := r.observeDevicesForNode(ctx, node, pcis)
	if err != nil {
		return nil, nil, err
	}
	if len(devs) == 0 {
		return map[string]string{}, map[string]string{}, nil
	}
	geom, errs := observedGeometryOf(devs)
	return geom, errs, nil
}

// observeDevicesForNode 는 노드의 PCI 목록을 관측해 MigDevice 로 옮긴다.
// mode 판정(ModeCurrent/ModePending)이 필요한 호출자는 geometry 만 주는 observeForNode 대신
// 이것을 쓴다 — 두 호출자가 같은 변환을 공유해야 관측 해석이 갈라지지 않는다.
func (r *AcceleratorPartitionPolicyReconciler) observeDevicesForNode(ctx context.Context, node string, pcis []string) ([]nvidia.MigDevice, error) {
	if len(pcis) == 0 {
		return nil, nil
	}
	obs, err := r.nvidiaObserver().Observe(ctx, node, pcis)
	if err != nil {
		return nil, fmt.Errorf("observe %s: %w", node, err)
	}
	// 관측 결과를 보고서에도 남긴다. detector 는 비특권이라 MIG 를 못 보고, 그래서 NDR 의 MIG
	// 필드는 여기서 쓰지 않으면 영영 Unknown 이다 — 검증의 보고서 축이 그 장치에 대해 아무것도
	// 말하지 못해 근거 등급이 Observed 에서 멈춘다. 실패해도 적용을 막지 않는다(부수 기록이다).
	if perr := r.publishMigObservation(ctx, node, obs); perr != nil {
		logf.FromContext(ctx).Error(perr, "publishing the mig observation failed", "node", node)
	}
	devs := make([]nvidia.MigDevice, 0, len(obs))
	for _, o := range obs {
		devs = append(devs, nvidia.MigDevice{
			PCI: o.PCI, ModeCurrent: o.ModeCurrent, ModePending: o.ModePending,
			Geometry: o.Geometry, ObsError: o.Err,
		})
	}
	return devs, nil
}

// publishMigObservationTo 는 관측 결과를 보고서에 기록한다. 남의 객체(detector 소유)에 쓰는
// 것이라 규칙을 좁게 둔다: 관측에 성공한 PCI 항목만 쓰고, 값이 이미 같으면 쓰지 않는다(불필요한
// write 는 watch 이벤트를 만들고 그 이벤트가 같은 객체를 재큐잉해 핫루프가 된다). detector 쪽은
// 자기가 관측 못 했을 때 이 값을 덮지 않는다. 자유 함수로 두는 이유는 호출자가 둘(정책 경로·
// 정책 무관 관측 경로)이기 때문이다 — 메서드로 각자 구현하면 규칙이 갈라진다.
func publishMigObservationTo(ctx context.Context, c client.Client,
	node string, obs []nvidia.Observation) error {
	byPCI := make(map[string]nvidia.Observation, len(obs))
	for _, o := range obs {
		if o.PCI != "" && o.Err == "" {
			byPCI[o.PCI] = o
		}
	}
	if len(byPCI) == 0 {
		return nil
	}
	var ndr npuv1alpha1.NodeDeviceReport
	if err := c.Get(ctx, types.NamespacedName{Name: node}, &ndr); err != nil {
		return client.IgnoreNotFound(err)
	}
	base := ndr.DeepCopy()
	changed := false
	for i := range ndr.Status.Devices {
		d := &ndr.Status.Devices[i]
		o, ok := byPCI[d.PCIeAddress]
		if !ok {
			continue
		}
		if d.MigModeCurrent == o.ModeCurrent && d.MigModePending == o.ModePending &&
			d.MigCurrentGeometry == o.Geometry && d.MigObservationError == "" {
			continue
		}
		d.MigModeCurrent, d.MigModePending = o.ModeCurrent, o.ModePending
		d.MigCurrentGeometry, d.MigObservationError = o.Geometry, ""
		if o.LgipOutput != "" {
			d.MigLgipOutput = o.LgipOutput
		}
		changed = true
	}
	if !changed {
		return nil
	}
	// base 이후로 안 바뀐 필드(예: detector 가 쓴 빈 consumers 목록)는 patch 에 실리지 않는다 —
	// 전체 교체(Update)라면 그 "쥔 컨테이너 없음" 사실이 지워져 "모름"으로 퇴행한다.
	return c.Status().Patch(ctx, &ndr, client.MergeFrom(base))
}

// publishMigObservation 은 ACPP 적용 경로의 호출부다 — 규칙 본체는 publishMigObservationTo 하나뿐이다.
func (r *AcceleratorPartitionPolicyReconciler) publishMigObservation(ctx context.Context,
	node string, obs []nvidia.Observation) error {
	return publishMigObservationTo(ctx, r.Client, node, obs)
}

// acppParticipant 는 파티션·공유 작업의 본체다. 두 종류가 같은 구현을 쓰는 이유는 실행이 같기
// 때문이고, 그런데도 종류를 나누는 이유는 **선언하는 자원 키가 달라 충돌 판정이 달라지기**
// 때문이다.
type acppParticipant struct {
	r *AcceleratorPartitionPolicyReconciler
}

// NewACPPParticipant 는 기존 적용 경로에 위임하는 participant 를 만든다.
func NewACPPParticipant(r *AcceleratorPartitionPolicyReconciler) operation.Participant {
	return &acppParticipant{r: r}
}

// OperationTypeForACPP 는 정책이 요구하는 작업 종류다. layout 이 있으면 파티션 재구성,
// 없으면 공유 모드 변경이다(기존 runTarget 의 runSharingOnly 분기와 같은 기준).
func OperationTypeForACPP(acpp *npuv1alpha1.AcceleratorPartitionPolicy) operation.Type {
	if len(acpp.Spec.Layout) == 0 {
		return operation.SharingModeChange
	}
	return operation.PartitionReconfigure
}

// ResourceKeysForACPP 는 이 정책이 그 노드에서 건드리는 자원을 선언한다.
//
// 파티션은 장치 파티션 facet 과 노드 cordon 을 잡는다(적용 중 노드를 잠근다). 공유는 장치
// sharing facet 과 device-plugin 설정을 잡되 **노드 키는 잡지 않는다** — 공유 변경은 cordon 을
// 요구하지 않으므로, 노드 키를 선언하면 같은 노드의 무관한 작업까지 불필요하게 직렬화된다.
func ResourceKeysForACPP(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string, opType operation.Type) []string {
	rec := getApplyRecord(acpp, node)
	pcis := rec.GPUPCIs
	keys := make([]string, 0, len(pcis)+2)
	for _, pci := range pcis {
		switch opType {
		case operation.SharingModeChange:
			keys = append(keys, string(operation.DeviceSharingKey(pci)))
		default:
			keys = append(keys, string(operation.DevicePartitionKey(pci)))
		}
	}
	if opType == operation.SharingModeChange {
		keys = append(keys, string(operation.ClusterDPConfigKey(nvidia.DevicePluginNameMixed)))
		return keys
	}
	keys = append(keys, string(operation.NodeCordonKey(node)))
	return keys
}

// Apply 는 기존 적용 경로를 **한 번** 돌리고 결과 phase 를 사건으로 옮긴다.
// 적용 로직을 여기서 다시 쓰지 않는다 — 복제하면 두 경로가 갈라지고, 갈라진 순간 이 계층이
// 직렬화하는 대상이 실제로 도는 코드가 아니게 된다.
func (p *acppParticipant) Apply(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	ts, acpp, err := p.runOnce(ctx, op)
	if err != nil {
		return operation.Outcome{Event: operation.EventApplyFailed, Message: err.Error()}, nil
	}
	if acpp == nil {
		return operation.Outcome{Event: operation.EventApplyFailed, Message: "owner policy is gone"}, nil
	}
	// 재부팅 대기를 switch 보다 **먼저** 본다. 대기 중의 target phase 는 Applying 이라 아래
	// switch 로는 "진행 중" 과 구분되지 않는다. 대기 사실은 저널(ApplyRecord.MigPhase)에만 있다.
	rec := getApplyRecord(acpp, op.Spec.NodeName)
	if rec.MigPhase == npuv1alpha1.MigPhaseRebootRequested || rec.MigPhase == npuv1alpha1.MigPhaseRebootWaiting {
		// 재부팅 대기는 실패가 아니다. 노드가 돌아오면 다음 pass 가 이어간다.
		return operation.Outcome{Event: operation.EventRebootRequired, Message: "waiting for node reboot"}, nil
	}
	switch ts.Phase {
	case npuv1alpha1.ACPPPhaseReady, npuv1alpha1.ACPPPhaseDegraded:
		return operation.Outcome{Event: operation.EventApplyDone, Message: "apply path converged: " + ts.Phase}, nil
	case npuv1alpha1.ACPPPhaseFailed, npuv1alpha1.ACPPPhaseUnsupported,
		npuv1alpha1.ACPPPhaseRollingBack, npuv1alpha1.ACPPPhaseRollbackFailed, npuv1alpha1.ACPPPhaseRestored:
		return operation.Outcome{Event: operation.EventApplyFailed, Message: "apply path failed: " + ts.Phase}, nil
	}
	// 그 밖의 phase 는 진행 중이다(WaitingForDrain / Applying / VerifyingAllocatable 등).
	return operation.Outcome{Message: "in progress: " + ts.Phase, RequeueAfter: participantRequeue}, nil
}

// Rollback 은 기존 경로가 이미 수행하는 보상을 한 번 더 돌려 **종착을 확인**한다. 새 보상
// 로직을 만들지 않는다 — runTarget 이 apply/verify 실패 시 backend.Rollback 을 이미 돌리고
// 복원 종점으로 착지하기 때문이다. 이 계층이 더하는 것은 그 종착을 사건으로 옮기는 것과, RNGD
// 경로의 애매한 종착(아래 ACPPPhaseFailed 분기 참고)을 조건까지 읽어 정확히 가르는 것이다.
// NVIDIA 공유 경로는 이미 phase 만으로 구분되므로(Restored=성공, RollbackFailed=실패) 이
// 처리를 거치지 않는다.
func (p *acppParticipant) Rollback(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	ts, acpp, err := p.runOnce(ctx, op)
	if acpp == nil {
		return operation.Outcome{Message: "rollback pass failed", RequeueAfter: participantRequeue}, nil
	}
	// runErr(err) 를 여기서 먼저 걸러내지 않는다 — backend.Rollback 이 실제로 실패하는 경로는
	// runTarget 이 그 실패를 **바로 그 non-nil 리턴값**으로 알린다(acpp_sharing.go/controller.go
	// 의 rollback-fail 분기). err 만 보고 무조건 재시도로 돌리면 phase·condition 이 이미 담고
	// 있는 "복원 실패" 를 이 함수가 영원히 못 본다 — CompensationFailed 가 코드에는 있지만 실행
	// 경로가 없는 죽은 분기가 된다.
	switch ts.Phase {
	case npuv1alpha1.ACPPPhaseRollbackFailed:
		return operation.Outcome{Event: operation.EventCompensationFailed, Message: "rollback failed"}, nil
	case npuv1alpha1.ACPPPhaseRestored, npuv1alpha1.ACPPPhaseUnsupported:
		return operation.Outcome{Event: operation.EventCompensated, Message: "restored: " + ts.Phase}, nil
	case npuv1alpha1.ACPPPhaseFailed:
		// RNGD 경로(runTarget 의 furiosa 분기)는 복원 성공·실패를 **똑같이** ACPPPhaseFailed 로
		// 남긴다 — 실제 성패는 ACPPCondRolledBack condition 하나에만 있다. 그 condition 이 아예
		// 없으면 이 Failed 는 rollback 결과가 아니라 다른 사유(Discover 실패 등)다 — 그때는 무엇을
		// 되돌렸는지 알 수 없으므로 단정하지 않고 재시도로 넘긴다(안전한 쪽으로 떨어뜨린다).
		if cond := apimeta.FindStatusCondition(ts.Conditions, npuv1alpha1.ACPPCondRolledBack); cond != nil {
			if cond.Status == metav1.ConditionFalse {
				return operation.Outcome{Event: operation.EventCompensationFailed, Message: cond.Message}, nil
			}
			return operation.Outcome{Event: operation.EventCompensated, Message: "restored: " + ts.Phase}, nil
		}
	}
	msg := "rollback in progress: " + ts.Phase
	if err != nil {
		msg = "rollback pass failed: " + err.Error()
	}
	return operation.Outcome{Message: msg, RequeueAfter: participantRequeue}, nil
}

func (p *acppParticipant) VerifyRequest(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return p.r.verifyRequestFor(ctx, op)
}

// runOnce 는 소유 정책을 읽어 기존 적용 경로를 한 번 돌리고 status 까지 영속한다.
// status 를 함께 쓰는 이유는 정직성이다 — operation 만 알고 정책은 모르는 상태를 만들면
// 운영자가 kubectl get acpp 로 보는 것이 거짓이 된다.
func (p *acppParticipant) runOnce(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (npuv1alpha1.TargetStatus, *npuv1alpha1.AcceleratorPartitionPolicy, error) {
	var acpp npuv1alpha1.AcceleratorPartitionPolicy
	if err := p.r.Get(ctx, types.NamespacedName{Name: op.Spec.Owner.Name}, &acpp); err != nil {
		if apierrors.IsNotFound(err) {
			return npuv1alpha1.TargetStatus{}, nil, nil
		}
		return npuv1alpha1.TargetStatus{}, nil, err
	}
	if op.Spec.Owner.UID != "" && string(acpp.UID) != op.Spec.Owner.UID {
		// 같은 이름으로 재생성된 다른 정책이다 — 옛 operation 이 새 정책을 건드리면 안 된다.
		return npuv1alpha1.TargetStatus{}, nil, fmt.Errorf("owner uid mismatch for %q", op.Spec.Owner.Name)
	}
	backend, err := p.r.backendFor(acpp.Spec.Vendor)
	if err != nil {
		return npuv1alpha1.TargetStatus{}, &acpp, err
	}
	target := partition.Target{
		Ctx: ctx, NodeName: op.Spec.NodeName, Owner: acpp.Name,
		DaemonSetName: rngdUnifiedDSName, DaemonSetNamespace: rngdUnifiedDSNS(),
	}
	ec := &evidenceCtx{}
	ts, runErr := p.r.runTarget(&acpp, backend, target, ec)
	if gerr := p.r.gateReady(ctx, &acpp, &ts, target, ec); gerr != nil {
		return ts, &acpp, gerr
	}
	if werr := p.r.writeTargetStatus(ctx, &acpp, ts); werr != nil {
		return ts, &acpp, werr
	}
	return ts, &acpp, runErr
}
