// ============================================================
// migobservation_controller.go: 정책과 무관한 MIG 관측 컨트롤러
// 상세: 관측이 정책 주도면 정책 없는 노드는 아무도 보지 않아 고아 라벨·빈 프로파일 목록·침묵
//
//	근거 세 문제가 생긴다 — 이 컨트롤러가 그 사슬을 끊는다. 특권은 그 순간·그 노드에만 쓴다.
//
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

// migObservationInterval 은 정책과 무관한 관측 주기다.
//
// 이 값이 답하는 질문은 "사람이 손으로 MIG 모드를 되돌린 것을 언제까지 몰라도 되는가" 이고,
// 모르는 동안의 손해는 "그 노드에서 공유 모드를 못 쓴다" 뿐이다. 장애 감지 주기(30초)와 같은
// 급일 이유가 없고, 특권 Job 을 그 빈도로 띄우는 비용이 훨씬 크다.
const migObservationInterval = 10 * time.Minute

// migObservationAttemptedAtAnnotation 은 이 컨트롤러가 마지막으로 관측 Job 을 **띄운** 시각
// (RFC3339)이다 — 성공 여부와 무관하다. 이 값 하나가 "다시 띄워도 되는가" 를 막는 유일한
// 문(rate limiter)이다. 성공 시각으로 답하면 안 된다: 관측이 계속 실패하는 노드에서는 성공
// 시각이 영영 안 남아 노드 watch 이벤트(kubelet 하트비트)마다 특권 Job 이 다시 뜬다.
// "데이터가 신선한가" 는 다른 질문이고 NDR 의 장치별 필드가 이미 답을 담는다 — 뭉개면 안
// 된다. NDR 에 필드를 추가하지 않는 이유는 그 보존 필드 목록을 다른 저장소가 관리해서다.
const migObservationAttemptedAtAnnotation = "kcloud.ai/mig.observation-attempted-at"

// MigObservationReconciler 는 NVIDIA 노드의 MIG 상태를 정책과 무관하게 관측한다.
type MigObservationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// ObserverFactory 는 관측기 seam 이다(nil 이면 실 특권 Job).
	ObserverFactory func() nvidia.Observer
	// Now 는 시계 seam 이다(nil 이면 time.Now) — 신선도 판정을 시험이 밀어볼 수 있어야 한다.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=npu.ai,resources=nodedevicereports,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=npu.ai,resources=nodedevicereports/status,verbs=get;update;patch

func (r *MigObservationReconciler) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *MigObservationReconciler) observer() nvidia.Observer {
	if r.ObserverFactory != nil {
		return r.ObserverFactory()
	}
	return nvidia.NewMigObserver(r.Client, nvidia.Namespace, HostExecImage(), nvidia.StreamStanding)
}

func (r *MigObservationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: req.Name}, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if node.Labels[vendorPresentLabel(vendorNvidia)] != labelValueTrue {
		return ctrl.Result{}, nil // MIG 개념이 없는 노드다
	}

	var ndr npuv1alpha1.NodeDeviceReport
	if err := r.Get(ctx, types.NamespacedName{Name: node.Name}, &ndr); err != nil {
		return ctrl.Result{RequeueAfter: migObservationInterval}, client.IgnoreNotFound(err)
	}

	pcis := nvidiaPCIsOf(&ndr)
	if len(pcis) == 0 {
		return ctrl.Result{RequeueAfter: migObservationInterval}, nil
	}
	if !needsObservation(&node, r.now()) {
		return ctrl.Result{RequeueAfter: migObservationInterval}, nil
	}

	// 시도 시각을 Job 을 띄우기 **전에**, 성공·실패와 무관하게 찍는다 — 이것이 "다시 띄워도
	// 되는가" 를 막는 유일한 문이다. 장치 상태는 이 판단에 영향을 주지 않는다: 노드 watch
	// 이벤트가 몇 초마다 와도(kubelet 하트비트), 이 시각이 최근이면 needsObservation 이 장치가
	// 아무리 더러워도 false 를 낸다. 성공 여부에 시각을 걸면 관측이 계속 실패하는 노드에서
	// 이 문이 영영 안 잠겨 몇 초 간격으로 Job 이 재생성된다.
	if merr := r.markObservationAttempted(ctx, &node, r.now()); merr != nil {
		return ctrl.Result{}, merr
	}

	obs, err := r.observer().Observe(ctx, node.Name, pcis)
	if err != nil {
		logf.FromContext(ctx).Error(err, "standing mig observation failed", "node", node.Name)
	}
	if len(obs) > 0 {
		// 오류가 났어도 부분 관측을 받았으면 기록한다 — 조용히 버리지 않는다. publishMigObservationTo
		// 는 관측 성공 항목만 쓰므로, 전부 실패인 관측을 넘겨도 안전하게 no-op 이다.
		if perr := publishMigObservationTo(ctx, r.Client, node.Name, obs); perr != nil {
			return ctrl.Result{}, perr
		}
	}
	if err != nil {
		return ctrl.Result{RequeueAfter: migObservationInterval}, nil
	}
	if err := r.dropOrphanMigLabel(ctx, &node, len(obs), len(pcis)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: migObservationInterval}, nil
}

// markObservationAttempted 는 이번 관측 시도 시각을 노드 주석에 남긴다. 값이 같은 초면 patch 를
// 생략한다(publishMigObservationTo 와 같은 원칙 — 불필요한 write 는 watch 이벤트를 만든다).
func (r *MigObservationReconciler) markObservationAttempted(ctx context.Context, node *corev1.Node, now time.Time) error {
	val := now.UTC().Format(time.RFC3339)
	if node.Annotations[migObservationAttemptedAtAnnotation] == val {
		return nil
	}
	base := node.DeepCopy()
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[migObservationAttemptedAtAnnotation] = val
	return r.Patch(ctx, node, client.MergeFrom(base))
}

// nvidiaPCIsOf 는 보고서가 아는 NVIDIA 장치 중 MIG 를 관측할 수 있는 PCI 목록이다.
// passthrough(vfio-pci) 장치는 뺀다 — host 의 nvidia-smi 가 그 GPU 를 보지 못해 관측은 매번
// fail-closed 로 끝나고, 그 재시도가 폭주의 또 다른 씨앗이 된다. 드라이버 설치 경로의
// SkipOnPassthrough 는 정책이 켜야 도는 opt-in 필드이고 판정도 설치 스크립트 안에서 난다 —
// 여기는 공유 규칙을 물려받은 것이 아니라 독립적으로 무조건 뺀다: 정책이 뭐라 하든 host
// nvidia-smi 는 vfio GPU 를 못 보므로, 관측에는 opt-in 이 아니라 무조건이 유일하게 옳다.
func nvidiaPCIsOf(ndr *npuv1alpha1.NodeDeviceReport) []string {
	if ndr.Status.PassthroughReserved {
		return nil // 노드의 GPU 가 전량 vfio-pci 예약 — MIG 관측 대상이 없다
	}
	out := make([]string, 0, len(ndr.Status.Devices))
	for i := range ndr.Status.Devices {
		d := &ndr.Status.Devices[i]
		if !strings.EqualFold(d.Vendor, "nvidia") || d.PCIeAddress == "" {
			continue
		}
		if d.DriverBinding == "vfio-pci" {
			continue
		}
		out = append(out, d.PCIeAddress)
	}
	return out
}

// needsObservation 은 관측 Job 을 다시 띄워도 되는지다 — 유일한 rate limiter다. 마지막
// **시도**(성공·실패 무관) 이후 주기(migObservationInterval)가 안 지났으면 장치 상태와
// 무관하게 false 다. 장치가 더럽다고 이 판정을 건너뛰면 관측이 계속 실패하는 노드에서
// 다음 watch 이벤트(kubelet 하트비트, ~10초 간격)마다 Job 이 다시 떠 무한 루프가 된다. 시도
// 시각이 없거나(첫 관측) 주기를 넘겼으면 다시 본다 — 장치가 깨끗해도 본다: 사람이 손으로
// 되돌린 모드를 잡으려면 깨끗한 노드도 주기적으로 다시 봐야 한다.
func needsObservation(node *corev1.Node, now time.Time) bool {
	last, ok := parseMigObservationAttemptedAt(node)
	if !ok {
		return true
	}
	return now.Sub(last) >= migObservationInterval
}

// parseMigObservationAttemptedAt 은 노드 주석의 마지막 관측 시도 시각을 읽는다. 주석이
// 없거나 파싱할 수 없으면 "모른다" 다 — needsObservation 은 그것을 "다시 봐야 한다" 로
// 해석한다(fail-open — 이 컨트롤러가 처음 보는 노드는 관측해야 안다).
func parseMigObservationAttemptedAt(node *corev1.Node) (time.Time, bool) {
	val := node.Annotations[migObservationAttemptedAtAnnotation]
	if val == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// dropOrphanMigLabel 은 소유 정책이 없고 모드까지 꺼진 노드의 mig-active 라벨을 뗀다.
//
// 정책이 있는 노드는 건드리지 않는다 — 그 라벨은 정책 경로가 소유한다. 모드가 하나라도
// 켜져 있거나 모르면 두 손 든다: 모드가 켜진 채 조각이 0인 GPU 를 통짜로 광고하면 배정된
// 파드가 CUDA 초기화에서 죽는다. observedCount != wantCount(일부 PCI 만 관측됨)도 같은
// 이유로 두 손 든다 — 불완전한 근거로 라벨을 떼는 것이 유령 GPU 광고를 만든 바로 그 실패다.
func (r *MigObservationReconciler) dropOrphanMigLabel(ctx context.Context,
	node *corev1.Node, observedCount, wantCount int) error {
	if node.Labels[nvidia.MigActiveNodeLabel] != labelValueTrue {
		return nil
	}
	if observedCount != wantCount {
		return nil
	}
	owned, err := r.nodeHasOwningPolicy(ctx, node)
	if err != nil || owned {
		return err
	}
	// "모드가 다 꺼졌는가" 는 acpp_migmode.go 의 판정 하나로 통일한다 — 같은 질문을 여기서
	// 다시 답하면 두 판정이 갈라지고, 갈라진 순간 한쪽만 고치고 고쳤다고 믿게 된다.
	if !migModeFullyRestoredFor(ctx, r.Client, node.Name) {
		return nil
	}
	base := node.DeepCopy()
	delete(node.Labels, nvidia.MigActiveNodeLabel)
	if r.Recorder != nil {
		r.Recorder.Eventf(node, corev1.EventTypeNormal, "MigActiveLabelCleared",
			"removed %s from node %s: no policy owns it and every GPU has MIG mode off",
			nvidia.MigActiveNodeLabel, node.Name)
	}
	return r.Patch(ctx, node, client.MergeFrom(base))
}

// nodeHasOwningPolicy 는 이 노드를 겨냥한 AcceleratorPartitionPolicy 가 있는지다.
// 매칭 규칙은 nodeMatchesSelector 하나로 통일한다(빈 셀렉터 = 전체 노드 매칭, 이 저장소
// 전역의 표준 k8s 의미 — 직접 다시 구현하면 세 번째 갈래가 생긴다).
func (r *MigObservationReconciler) nodeHasOwningPolicy(ctx context.Context, node *corev1.Node) (bool, error) {
	var list npuv1alpha1.AcceleratorPartitionPolicyList
	if err := r.List(ctx, &list); err != nil {
		return false, err
	}
	for i := range list.Items {
		if nodeMatchesSelector(node, list.Items[i].Spec.NodeSelector) {
			return true, nil
		}
	}
	return false, nil
}

func (r *MigObservationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(r)
}
