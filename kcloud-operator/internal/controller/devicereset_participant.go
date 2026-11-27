// ============================================================
// devicereset_participant.go: DeviceReset 작업 본체 — descriptor 의 resetCommand 를 노드에서 실행
// 상세: AcceleratorDescriptor.spec.cleanup.resetCommand 를 노드에서 실행하는 유일한 경로다.
//
//	관리자가 만든 AcceleratorOperation(DeviceReset)에서만 돌며, 되돌릴 수 없어 Compensated 로만 보고한다.
//
// 생성일: 2026-09-30 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/descriptor"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/vendorcap"
	"kcloud-operator/internal/verification"
)

// maxDeviceResets 는 한 작업이 리셋 명령을 실행할 수 있는 횟수다. 1 이다 — 리셋은 되돌릴 수
// 없으므로 "한 번 더" 가 없다. 재부팅 작업의 상한(maxOperationReboots)과 같은 부류이되 재부팅은
// 두 번까지 허용하는 것과 다르다: 재부팅은 같은 상태를 다시 시도하는 것이고 리셋은 장치 상태를
// 또 한 번 파괴하는 것이다.
const maxDeviceResets = 1

type deviceResetParticipant struct {
	client.Client
	image string
}

// NewDeviceResetParticipant 는 DeviceReset 작업 본체다. image 는 nsenter 를 가진 host-exec 이미지다.
func NewDeviceResetParticipant(c client.Client, image string) operation.Participant {
	return &deviceResetParticipant{Client: c, image: image}
}

// resetTarget 은 게이트 하나(descriptor 조회)가 확정한 실행 대상이다. 세 조회가 쓰는 벤더 어휘가
// 서로 다르므로(작업 enum·detector vendorKey·능력 레지스트리 키) 변환을 여기 한 곳에서 끝내고
// 나머지 게이트는 이 값만 쓴다. 어휘를 조회마다 따로 맞추면 한쪽만 고쳐질 때 게이트가 조용히
// 빗나간다 — furiosa 작업이 rngd descriptor 를 못 찾거나 furiosa.ai/rngd 파드를 놓치는 식이다.
type resetTarget struct {
	// command 는 실행할 리셋 명령이다(descriptor 선언 그대로).
	command []string
	// quiesceVendor 는 점유 판정기를 고르는 어휘다(detector vendorKey — nvidia·rngd·…).
	quiesceVendor string
	// capVendor 는 능력 레지스트리 어휘다(nvidia·furiosa·…).
	capVendor string
	// capProduct 는 능력 레지스트리의 제품 키다(a30·rngd·… — 제품을 가르지 않는 벤더는 "").
	// 등급 조회는 이 값까지 써야 한다 — 벤더만 물으면 한 벤더의 두 제품 등급이 갈릴 때
	// Unsupported 제품에도 통과가 난다.
	capProduct string
	// products 는 이 명령이 덮는, 그 노드에서 관측된 제품들이다(거절 사유 문장용).
	products []string
}

func (p *deviceResetParticipant) Apply(ctx context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	node, vendor := op.Spec.NodeName, op.Spec.Vendor

	// 게이트 1 — 그 노드가 관측한 제품의 descriptor 가 리셋 명령을 선언했는가.
	target, refusal, err := p.resetTargetFor(ctx, node, vendor)
	if err != nil {
		return operation.Outcome{}, err
	}
	if refusal != "" {
		return operation.Outcome{Event: operation.EventApplyFailed, Message: refusal}, nil
	}

	// 게이트 2 — 그 벤더·제품의 장치 리셋 능력 등급이 실행을 허용하는가.
	if refusal := resetGradeRefusal(target); refusal != "" {
		return operation.Outcome{Event: operation.EventApplyFailed, Message: refusal}, nil
	}

	// 게이트 3 — 그 노드에서 그 벤더 장치를 쓰는 파드가 0인가.
	pods, err := p.holdingPods(ctx, node, target)
	if err != nil {
		return operation.Outcome{}, err
	}
	if len(pods) > 0 {
		return operation.Outcome{Event: operation.EventApplyFailed,
			Message: fmt.Sprintf("노드 %s 에 %s 장치를 쓰는 파드가 있다: %v", node, vendor, pods)}, nil
	}
	if p.image == "" {
		return operation.Outcome{Event: operation.EventApplyFailed, Message: "host-exec 이미지가 설정되지 않았다"}, nil
	}

	name := naming.DeviceResetJobName(node, vendor, op.Spec.TransactionID)
	var job batchv1.Job
	err = p.Get(ctx, types.NamespacedName{Name: name, Namespace: driverjob.Namespace}, &job)
	switch {
	case apierrors.IsNotFound(err):
		// 저널이 이미 요청을 기록했는데 Job 이 없다 = TTL 이 걷은 뒤의 재진입이다. 다시 만들면
		// 같은 장치를 두 번 리셋한다. 결과를 확인할 수 없으므로 성공으로도 보고하지 않는다.
		if attempts := deviceResetAttemptsFrom(op.Status.Journal); attempts >= maxDeviceResets {
			return operation.Outcome{Event: operation.EventApplyFailed,
				Message: fmt.Sprintf("리셋 Job %s 을 이미 %d 번 만들었고 지금은 남아 있지 않다 — 두 번 리셋하지 않는다, 결과는 Job 로그로 확인하라", name, attempts)}, nil
		}
		created := driverjob.RenderDeviceResetJobFor(nil, node, p.image, name, target.command)
		if err := p.Create(ctx, created); err != nil && !apierrors.IsAlreadyExists(err) {
			return operation.Outcome{}, err
		}
		// 되돌릴 수 없는 행동을 했다는 사실을 저널에 남긴다(append-only·(단계,epoch) 중복 제거).
		op.Status.Journal = operation.AppendJournal(op.Status.Journal, operation.StepDeviceResetRequested,
			op.Status.Epoch, metav1.Now(), strings.Join(target.command, " "))
		return operation.Outcome{Message: "리셋 Job 생성: " + name, RequeueAfter: 10 * time.Second}, nil
	case err != nil:
		return operation.Outcome{}, err
	}
	switch {
	case job.Status.Succeeded > 0:
		return operation.Outcome{Event: operation.EventApplyDone, Message: "리셋 명령 종료 코드 0"}, nil
	case job.Status.Failed > 0:
		return operation.Outcome{Event: operation.EventApplyFailed, Message: "리셋 명령 실패 — Job " + name + " 로그 확인"}, nil
	}
	return operation.Outcome{Message: "리셋 Job 실행 중", RequeueAfter: 10 * time.Second}, nil
}

// resetGradeRefusal 은 장치 리셋 능력 등급이 실행을 막을 때의 거절 사유다(허용하면 빈
// 문자열). 게이트 1 이 확정한 제품까지 넘겨 Lookup 으로 조회한다 — LookupVendor 는 그
// 벤더의 아무 제품 값이나 물려주므로, 한 벤더의 두 제품 등급이 갈리면 Unsupported 제품에도
// 통과가 난다. 제품이 비면 거절한다: 어느 제품의 등급을 본 것인지 모르는 채로 되돌릴 수
// 없는 명령을 실행할 수 없다. tenstorrent 의 tt-smi -r 처럼 Unsupported 명령은 호스트를
// 멈출 수 있어, 등급 게이트가 그 실행을 막는다.
func resetGradeRefusal(t resetTarget) string {
	if strings.TrimSpace(t.capProduct) == "" {
		return fmt.Sprintf("%s descriptor 가 제품을 비워 둬 장치 리셋 등급을 확정할 수 없다", t.capVendor)
	}
	s := registryCapSupport(t.capVendor, t.capProduct, vendorcap.CapReset)
	if s.State == vendorcap.StateNative || s.State == vendorcap.StateSoftware {
		return ""
	}
	return fmt.Sprintf("%s/%s 의 장치 리셋 등급이 %s 라 실행하지 않는다", t.capVendor, t.capProduct, s.State)
}

// holdingPods 는 그 노드에서 장치를 쥔 파드들이다. 판정기 선택은 target.quiesceVendor 를 쓴다 —
// 작업 vendor("furiosa")를 그대로 넘기면 rngd 판정기를 못 골라 furiosa.ai/rngd 파드를 놓친다.
func (p *deviceResetParticipant) holdingPods(ctx context.Context, node string, target resetTarget) ([]string, error) {
	return advertiseQuiescer(p.Client, target.quiesceVendor).DeviceHoldingPods(ctx, node)
}

// deviceResetAttemptsFrom 은 저널에 남은 리셋 Job 생성 횟수다. status 에 새 필드를 만들지 않는
// 이유는 재부팅 참가자와 같다 — 저널은 이미 durable 하고 append-only 다.
func deviceResetAttemptsFrom(journal []npuv1alpha1.OperationJournalEntry) int {
	n := 0
	for _, e := range journal {
		if e.Step == operation.StepDeviceResetRequested {
			n++
		}
	}
	return n
}

// resetTargetFor 는 그 노드가 관측한 제품에 맞는 리셋 명령을 찾는다. 없거나 모호하면 거절 사유를
// 돌려준다(두 번째 반환값). 벤더만 보고 목록의 첫 일치를 쓰면 혼재 노드에서 그 노드에 없는
// 제품의 명령이 나온다 — 명령에는 장치 인덱스가 들어가므로 다른 장치를 리셋하게 된다. 관측
// 출처는 NodeDeviceReport 다 — detector 가 직접 쓰는 기록이고 노드 이름으로 바로 읽히며,
// 드라이버 정책이 없는 벤더에도 존재한다(DriverUpgradeState.status.observedModels 는 이 값에서
// 파생되고 드라이버 관리 대상 벤더에만 있다).
func (p *deviceResetParticipant) resetTargetFor(ctx context.Context, node, vendor string) (resetTarget, string, error) {
	var ndrs npuv1alpha1.NodeDeviceReportList
	if err := p.List(ctx, &ndrs); err != nil {
		return resetTarget{}, "", fmt.Errorf("노드 %s 장치 관측 조회 실패: %w", node, err)
	}
	devices, found := nodeDeviceReportDevices(ndrs.Items, node)
	if !found {
		return resetTarget{}, fmt.Sprintf("노드 %s 의 장치 관측 기록(NodeDeviceReport)이 없다 — 어느 제품의 리셋 명령을 쓸지 알 수 없다", node), nil
	}
	cards := make([]npuv1alpha1.DeviceEntry, 0, len(devices))
	for _, d := range devices {
		if strings.EqualFold(d.Vendor, vendor) {
			cards = append(cards, d)
		}
	}
	if len(cards) == 0 {
		return resetTarget{}, fmt.Sprintf("노드 %s 에서 %s 장치가 관측되지 않았다", node, vendor), nil
	}

	var descs npuv1alpha1.AcceleratorDescriptorList
	if err := p.List(ctx, &descs); err != nil {
		return resetTarget{}, "", fmt.Errorf("descriptor 목록 조회 실패: %w", err)
	}
	// 같은 명령을 선언한 descriptor 가 여럿인 것은 모호하지 않다(혼재 노드의 제품별 descriptor 가
	// 같은 명령을 적는 경우). 서로 다른 명령이 나오는 것만 모호하다.
	byCommand := map[string]resetTarget{}
	for i := range descs.Items {
		spec := descs.Items[i].Spec
		if !spec.Cleanup.RequiresDeviceReset || len(spec.Cleanup.ResetCommand) == 0 {
			continue
		}
		// 정합이 깨진 descriptor 는 AcceleratorDescriptor 컨트롤러가 전량 거절한다
		// (Reconcile 이 status.refusals 에 backend "*" 로 적는다). 같은 판정 함수를 여기서
		// 직접 불러 그 descriptor 의 명령을 root 로 실행하지 않는다 — status 사본을 읽으면
		// 아직 조정되지 않은 descriptor(status 가 비어 있다)를 막지 못한다.
		if errs := descriptor.Validate(spec); len(errs) > 0 {
			continue
		}
		for _, d := range cards {
			if !matchesDescriptor(d, spec) {
				continue
			}
			capVendor, capProduct := descriptorRegistryKey(spec.Vendor, spec.Product)
			key := strings.Join(spec.Cleanup.ResetCommand, "\x00")
			t := byCommand[key]
			t.command, t.quiesceVendor = spec.Cleanup.ResetCommand, spec.Vendor
			t.capVendor, t.capProduct = capVendor, capProduct
			t.products = append(t.products, d.Model)
			byCommand[key] = t
			break
		}
	}
	switch len(byCommand) {
	case 0:
		return resetTarget{}, fmt.Sprintf("노드 %s 의 %s 장치에 맞는 descriptor 가 requiresDeviceReset 과 resetCommand 를 선언하지 않았다", node, vendor), nil
	case 1:
		for _, t := range byCommand {
			sort.Strings(t.products)
			return t, "", nil
		}
	}
	return resetTarget{}, fmt.Sprintf("노드 %s 의 %s 장치에 서로 다른 리셋 명령이 여러 개 선언돼 있다(%s) — 어느 것도 고르지 않는다", node, vendor, strings.Join(ambiguousCommands(byCommand), " / ")), nil
}

// nodeDeviceReportDevices 는 그 노드의 관측 카드 목록이다. spec.nodeName 을 먼저 보고
// 비었을 때만 metadata.name 으로 폴백한다 — 같은 패키지의 드라이버 컨트롤러가 쓰는 방식과
// 같게 두어 같은 리소스를 두 방식으로 되짚지 않는다.
func nodeDeviceReportDevices(items []npuv1alpha1.NodeDeviceReport, node string) ([]npuv1alpha1.DeviceEntry, bool) {
	for i := range items {
		name := items[i].Spec.NodeName
		if name == "" {
			name = items[i].Name
		}
		if name == node {
			return items[i].Status.Devices, true
		}
	}
	return nil, false
}

// ambiguousCommands 는 거절 문장에 넣을 명령 목록이다(정렬해 문장을 결정적으로 만든다).
func ambiguousCommands(byCommand map[string]resetTarget) []string {
	out := make([]string, 0, len(byCommand))
	for _, t := range byCommand {
		out = append(out, strings.Join(t.command, " "))
	}
	sort.Strings(out)
	return out
}

// Rollback 은 되돌릴 것이 없다 — 리셋은 되돌릴 수 없는 행동이라 보상 없음을 기록만 한다.
//
// 앞선 메시지를 덮지 않고 이어 적는다: 조정자(stepRollback)가 op.Status.Message 를 이 반환값으로
// 갈아치우므로, 덮어쓰면 어느 게이트가 막았는지가 종점 상태에서 사라진다. 벤더 넷 가운데 셋이
// 등급 거절이라 거절이 이 작업의 지배적 경로다.
func (p *deviceResetParticipant) Rollback(_ context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	msg := "장치 리셋은 되돌릴 수 없다 — 보상 없음"
	if prev := strings.TrimSpace(op.Status.Message); prev != "" {
		msg = prev + " / " + msg
	}
	return operation.Outcome{Event: operation.EventCompensated, Message: msg}, nil
}

// VerifyRequest 는 검증 대상이 아니다 — 리셋 뒤 상태는 health 가 다음 관측에서 본다.
func (p *deviceResetParticipant) VerifyRequest(context.Context, *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}
