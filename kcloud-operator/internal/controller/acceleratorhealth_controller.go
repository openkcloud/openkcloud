// ============================================================
// acceleratorhealth_controller.go: 노드 health 감시 컨트롤러
// 상세: 노드마다 신호를 모아 상태를 판정해 AcceleratorHealth 에 기록한다. 부작용은 자기 CR
// status·격리 라벨·AcceleratorOperation 생성 셋뿐이다 — 장치·DaemonSet·ConfigMap·재부팅은
// 안 건드린다. 수집이 이 파일에 있는 이유: internal/health 는 internal/partition 을 import
// 할 수 없어, 이 컨트롤러가 mutation 금지 계약을 컴파일 그래프로 강제하며 수집을 맡는다.
// 생성일: 2026-08-04 | 수정일: 2026-11-16
// ============================================================
package controller

import (
	"context"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/health"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/upgrade"
	"kcloud-operator/internal/vendorcap"
)

// QuarantineLabel 은 격리 표시다. taint 가 아니라 라벨인 이유는 계약이 "신규 할당 차단" 이기
// 때문이다 — taint 는 이미 돌고 있는 워크로드를 쫓아낸다.
const QuarantineLabel = "kcloud.ai/health.quarantined"

// QuarantineReleaseAnnotation 은 **사람이 격리를 푸는 유일한 입구**다. 값은 RFC3339 시각이고,
// "이 시각 이전의 복구 실패는 확인했다" 는 뜻이다. 없으면 나갈 길이 없다 — Quarantined 의
// 원인에는 복구 작업이 안 붙어(자동 재시도 중단) Recovering 이 못 되고, AllowedTransition 은
// Recovering 말고는 못 나가게 막아 **영구 격리**가 된다. 전이 게이트는 손으로 못 풀므로:
//
//	kubectl annotate node <노드> kcloud.ai/health.quarantine-release=$(date -u +%FT%TZ) --overwrite
const QuarantineReleaseAnnotation = "kcloud.ai/health.quarantine-release"

// healthRequeue 는 정책이 주기를 안 정했을 때의 감시 주기다. NDR 갱신 주기(30초)와 맞춰 둔다.
// 정책(`AcceleratorHealthPolicy.spec.checks.monitorIntervalSeconds`)이 값을 주면 그것이 이긴다.
const healthRequeue = 30 * time.Second

// AcceleratorHealthReconciler 는 노드 하나를 감시한다.
type AcceleratorHealthReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// Now 는 시계 seam 이다(nil 이면 time.Now).
	Now func() time.Time
}

// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorhealths,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorhealths/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorhealthpolicies,verbs=get;list;watch

func (r *AcceleratorHealthReconciler) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *AcceleratorHealthReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: req.Name}, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !isAcceleratorNode(&node) {
		return ctrl.Result{}, nil // 가속기가 없는 노드는 감시 대상이 아니다
	}

	var policies npuv1alpha1.AcceleratorHealthPolicyList
	if err := r.List(ctx, &policies); err != nil {
		return ctrl.Result{}, err
	}
	pol := health.Resolve(policies.Items, node.Labels)

	prev, err := r.loadHealth(ctx, node.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	in, err := r.collectHealthInputs(ctx, &node, prev)
	if err != nil {
		return ctrl.Result{}, err
	}

	res := health.Evaluate(in, pol)
	// 사람이 지금 상태보다 나중 시각으로 해제를 표시했으면 이전 상태를 기억하지 않는다 —
	// 그래야 전이 게이트가 격리를 붙잡지 않는다.
	prevState := prev.Status.State
	if rel := quarantineReleasedAt(&node); rel != nil && rel.After(prev.Status.DetectedAt.Time) {
		logf.FromContext(ctx).Info("quarantine released by annotation",
			"node", node.Name, "at", rel.Format(time.RFC3339), "was", prevState)
		prevState = ""
	}
	if !health.AllowedTransition(prevState, res.State) {
		// 격리는 복구를 거쳐야 풀린다. 신호가 좋아졌다는 이유로 자동 해제하지 않는다.
		res.State, res.Reason, res.AllocationAllowed = prev.Status.State, prev.Status.Reason, false
	}

	// 판정은 노드 단위지만 지표는 벤더별로 쌓는다 — 노드에 있는 각 벤더에 같은 판정을 센다.
	// in.Devices 의 vendor 는 deviceInputsFromNDR 이 이미 소문자로 맞춰 둔다.
	seenVendor := map[string]bool{}
	for _, d := range in.Devices {
		if d.Vendor == "" || seenVendor[d.Vendor] {
			continue
		}
		seenVendor[d.Vendor] = true
		metrics.RecordHealthVerdict(d.Vendor, res.State)
	}

	opRef, err := r.ensureRecovery(ctx, &node, res, pol)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.writeHealth(ctx, &node, prev, res, opRef, in); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.syncQuarantineLabel(ctx, &node, !res.AllocationAllowed && quarantineWorthy(res.State)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueFor(pol)}, nil
}

// requeueFor 는 이 노드를 다시 볼 간격이다. 정책이 정한 값이 있으면 그것을 쓴다 — 자가치유가
// 빠른 장애는 기본 30초 주기로는 아예 관측되지 않는다.
func requeueFor(p health.EffectivePolicy) time.Duration {
	if p.MonitorInterval > 0 {
		return p.MonitorInterval
	}
	return healthRequeue
}

// quarantineWorthy 는 노드에 격리 라벨을 붙일 상태인지다. 규칙은 하나다: 할당이 막혔고 상태를
// 알고 있을 때만 붙인다. Unknown 을 빼는 이유는 관측이 잠깐 끊긴 것만으로 라벨을 붙였다 뗐다
// 하면 배치가 흔들리기 때문이고, 그 구간의 차단은 status 축(AllocationAllowed)이 이미 맡는다.
func quarantineWorthy(state string) bool {
	return state != health.StateUnknown && state != health.StateHealthy && state != ""
}

// acceleratorPresentLabels 는 node-device-detector 가 실제로 쓰는 present 라벨 전체 집합이다
// (node-device-detector/label.go computeLabels 가 정본). accelerator.present 와 그 옛 이름 gpu.present(0.9 에서
// 제거, 구 node-manager 혼용 대비)는 장치가 있으면 항상, furiosa-family.present 는 PCI 0x1ed2 를 공유하는
// Warboy·RNGD 공통으로 붙는다. 나머지는 등록된 벤더마다 vendorPresentLabel(vendor) 하나씩이다 — 단 furiosa
// 안에서 rngd 만 detector 의 vendorKey 가 별도 키("rngd")를 쓰는 유일한 product 라 따로 더한다(Warboy 는
// vendorKey 가 "furiosa" 그대로라 "kcloud.ai/warboy.present" 는 안 쓴다).
func acceleratorPresentLabels() map[string]bool {
	m := map[string]bool{
		npuv1alpha1.AcceleratorPresentLabel:   true,
		"kcloud.ai/gpu.present":               true,
		npuv1alpha1.FuriosaFamilyPresentLabel: true,
	}
	for _, v := range vendorcap.Vendors() {
		m[vendorPresentLabel(v)] = true
	}
	m[vendorPresentLabel(vendorRngd)] = true
	return m
}

// isAcceleratorNode 는 감시 대상 노드인지다. 라벨은 node-agent 가 붙인다.
func isAcceleratorNode(node *corev1.Node) bool {
	labels := acceleratorPresentLabels()
	for k := range node.Labels {
		if labels[k] {
			return true
		}
	}
	return false
}

// loadHealth 는 직전 상태를 읽는다. 없으면 빈 객체다(이름은 노드 이름).
func (r *AcceleratorHealthReconciler) loadHealth(ctx context.Context, node string) (*npuv1alpha1.AcceleratorHealth, error) {
	var ah npuv1alpha1.AcceleratorHealth
	if err := r.Get(ctx, types.NamespacedName{Name: node}, &ah); err != nil {
		if apierrors.IsNotFound(err) {
			return &npuv1alpha1.AcceleratorHealth{ObjectMeta: metav1.ObjectMeta{Name: node}}, nil
		}
		return nil, err
	}
	return &ah, nil
}

// collectHealthInputs 는 판정 입력을 모은다. **읽기 전용이다.**
func (r *AcceleratorHealthReconciler) collectHealthInputs(ctx context.Context, node *corev1.Node,
	prev *npuv1alpha1.AcceleratorHealth) (health.Inputs, error) {
	in := health.Inputs{Now: r.now(), NodeReady: nodeReadyForHealth(node), Actual: allocatableSnapshot(node)}

	var ndr npuv1alpha1.NodeDeviceReport
	if err := r.Get(ctx, types.NamespacedName{Name: node.Name}, &ndr); err == nil {
		if ndr.Status.ObservedAt != nil {
			t := ndr.Status.ObservedAt.Time
			in.NDRObservedAt = &t
		}
		in.DriverLoaded = anyDriverLoaded(ndr.Status.Devices)
		in.Devices = deviceInputsFromNDR(ndr.Status.Devices)
	} else if client.IgnoreNotFound(err) != nil {
		return in, err
	}

	var ev npuv1alpha1.AcceleratorEvidence
	if err := r.Get(ctx, types.NamespacedName{Name: node.Name}, &ev); err == nil {
		if r.usableBaseline(ctx, &ev, in.Now) {
			in.Expected = ev.Status.AdvertisedResources
		}
	} else if client.IgnoreNotFound(err) != nil {
		return in, err
	}

	ready, err := r.devicePluginReady(ctx, node.Name, in.Actual)
	if err != nil {
		return in, err
	}
	in.DevicePluginReady = ready
	if rolling, rerr := nvidia.DevicePluginRolling(ctx, r.Client, node.Name); rerr == nil {
		in.DevicePluginRolling = rolling
	} else {
		// 롤아웃 여부를 모르면 억제한다 — 모르는 상태에서 장애를 선언하지 않는다(Stage 1 과 같은 규율).
		in.DevicePluginRolling = true
	}

	if prev.Status.SuspectedAt != nil {
		t := prev.Status.SuspectedAt.Time
		in.AdvertisementSuspectedAt = &t
	}
	fails, inFlight, err := r.recoveryHistory(ctx, node.Name, quarantineReleasedAt(node))
	if err != nil {
		return in, err
	}
	in.RecoveryFailures, in.RecoveryInFlight = fails, inFlight
	return in, nil
}

// usableBaseline 은 근거의 광고 기준선을 지금도 쓸 수 있는지다. 정책이 지워져도 근거는
// 남는다 — 그 근거의 기준선(예: MPS 배수 광고 4)을 계속 비교하면 정책이 없는 정상 노드가
// 영영 "광고 불일치" 가 되고, 쓸모없는 재검증 작업이 반복 생성된다. 만료된 근거도 같은
// 이유로 거른다.
func (r *AcceleratorHealthReconciler) usableBaseline(ctx context.Context,
	ev *npuv1alpha1.AcceleratorEvidence, now time.Time) bool {
	if len(ev.Status.AdvertisedResources) == 0 {
		return false
	}
	if ev.Status.ExpiresAt != nil && now.After(ev.Status.ExpiresAt.Time) {
		return false
	}
	if ev.Status.SourcePolicy == "" {
		return false
	}
	var acpp npuv1alpha1.AcceleratorPartitionPolicy
	if err := r.Get(ctx, types.NamespacedName{Name: ev.Status.SourcePolicy}, &acpp); err != nil {
		return false // 출처 정책이 없으면 그 기준선은 지금 상태에 대한 주장이 아니다
	}
	return acpp.DeletionTimestamp.IsZero()
}

func nodeReadyForHealth(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// anyDriverLoaded 는 보고된 장치 중 하나라도 드라이버가 올라와 있는지다.
// 장치가 하나도 없으면 "드라이버 없음" 이 아니라 "볼 것이 없음" 이므로 true 로 둔다 —
// 그 상태는 NDR 신선도 축이 이미 걸러 낸다.
func anyDriverLoaded(devs []npuv1alpha1.DeviceEntry) bool {
	if len(devs) == 0 {
		return true
	}
	for _, d := range devs {
		if d.DriverLoaded {
			return true
		}
	}
	return false
}

// deviceInputsFromNDR 는 NDR 장치를 장치 단위 판정 입력으로 옮긴다. PCI 주소가 없는 장치는
// 뺀다 — 식별할 수 없는 장치는 지금 판정할 수도, 나중에 배치에서 뺄 수도 없다. 그런
// 항목까지 억지로 넣으면 빈 PCI 를 가진 여러 장치가 서로 뭉개져 오판정을 만든다.
func deviceInputsFromNDR(devs []npuv1alpha1.DeviceEntry) []health.DeviceInput {
	out := make([]health.DeviceInput, 0, len(devs))
	for _, d := range devs {
		if d.PCIeAddress == "" {
			continue
		}
		out = append(out, health.DeviceInput{
			PCI: d.PCIeAddress, Vendor: strings.ToLower(d.Vendor), Model: d.Model, DriverLoaded: d.DriverLoaded,
			MigObservationError: d.MigObservationError, MigModeCurrent: d.MigModeCurrent,
		})
	}
	return out
}

// devicePluginReady 는 "이 노드의 NVIDIA 광고 주체가 살아 있는가" 다. pod 라벨만 보면
// Furiosa·Tenstorrent plugin(그 라벨 없음)과 control-plane(GPU 광고 자체 제외)이 전부
// 장애로 잡힌다 — 맡는 pod 이 없으면 판정 대상이 아니다(없는 것은 고장이 아니다). "노드가
// 뭔가 광고하면 정상" 으로 두면 혼재 노드에서 반대 오판정이 난다 — NVIDIA+Warboy 노드는
// Furiosa 광고가 살아 있는 동안 NVIDIA plugin 이 죽어도 정상으로 읽힌다. 그래서 광고 판정을
// nvidia.com/ 축으로 좁힌다.
func (r *AcceleratorHealthReconciler) devicePluginReady(ctx context.Context, node string,
	advertised map[string]int32) (bool, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.MatchingLabels{nvidiaDevicePluginVendorLabel: "nvidia"}); err != nil {
		return false, err
	}
	found := false
	for i := range pods.Items {
		if pods.Items[i].Spec.NodeName != node {
			continue
		}
		found = true
		if podReady(&pods.Items[i]) {
			return true, nil
		}
	}
	if !found {
		// 이 노드를 맡는 관리 대상 plugin 이 없다 — 고장이 아니라 대상 밖이다.
		return true, nil
	}
	// pod 은 있는데 Ready 가 아니다. 그래도 해당 벤더 자원이 아직 광고되고 있으면 주체는 살아 있다
	// (kubelet 이 짧은 재등록을 견딘다). 광고까지 사라졌을 때만 장애로 부른다.
	for k, v := range advertised {
		if v > 0 && strings.HasPrefix(k, "nvidia.com/") {
			return true, nil
		}
	}
	return false, nil
}

func podReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// quarantineReleasedAt 은 사람이 격리 해제를 표시한 시각이다. 주석이 없거나 시각으로 못 읽으면
// nil 이다 — 오타를 "해제" 로 읽어 격리를 푸는 것보다 안 푸는 쪽이 안전하다.
func quarantineReleasedAt(node *corev1.Node) *time.Time {
	v := node.Annotations[QuarantineReleaseAnnotation]
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &t
}

// recoveryHistory 는 **health 가 만든** 작업만 센다. 사람이 건 작업의 실패를 여기 섞으면
// 엉뚱한 노드가 격리된다.
//
// releasedAt 이전에 만들어진 작업은 세지 않는다. 사람이 확인했다고 표시한 실패이므로 계속 세면
// 노드가 영영 임계 위에 머문다.
func (r *AcceleratorHealthReconciler) recoveryHistory(ctx context.Context, node string,
	releasedAt *time.Time) (int32, bool, error) {
	var ops npuv1alpha1.AcceleratorOperationList
	if err := r.List(ctx, &ops); err != nil {
		return 0, false, err
	}
	var fails int32
	inFlight := false
	for i := range ops.Items {
		op := &ops.Items[i]
		if op.Spec.NodeName != node || op.Spec.Owner.Kind != healthOwnerKind {
			continue
		}
		if releasedAt != nil && !op.CreationTimestamp.After(*releasedAt) {
			continue // 사람이 확인한 시각 이전의 작업이다
		}
		switch op.Status.Phase {
		case npuv1alpha1.OpPhaseRolledBack, npuv1alpha1.OpPhaseManualRecoveryRequired:
			fails++
		case npuv1alpha1.OpPhaseSucceeded:
			// 성공한 복구는 세지 않는다.
		default:
			inFlight = true
		}
	}
	return fails, inFlight, nil
}

// healthOwnerKind 는 health 가 만든 작업의 소유자 Kind 다.
const healthOwnerKind = "AcceleratorHealth"

// ensureRecovery 는 판정이 요구하면 복구 작업을 만든다. 이름이 트랜잭션 ID 에서 결정되므로
// 같은 원인·같은 쿨다운 구간에서는 같은 이름이 나와 **하나만** 생긴다.
func (r *AcceleratorHealthReconciler) ensureRecovery(ctx context.Context, node *corev1.Node,
	res health.Result, pol health.EffectivePolicy) (string, error) {
	if res.Reason == "" {
		return "", nil
	}
	plan := health.PlanRecovery(node.Name, res.Reason, r.devicePCIs(ctx, node.Name), r.now(), pol)
	if !plan.Create {
		return "", nil
	}
	// 드라이버 업그레이드 사이클 안이면 드라이버 복구를 요청하지 않는다. 그 구간의
	// driverLoaded=false 는 설치가 스스로 구 모듈을 내린 예정된 상태이지 고장이 아니다.
	// 참가자 쪽에도 같은 가드가 있지만(recoverdevice_participant.go) 애초에 만들지 않는 편이 낫다 —
	// 작업이 생기면 조정자·저널·검증이 모두 돌고, 쿨다운 버킷에 남은 작업이 업그레이드가 끝난
	// 뒤 뒤늦게 실행될 여지도 생긴다. 이 경로가 설치 Job pod 을 지우면 노드의 apt 가 깨진다.
	if plan.Type == operation.RecoverDevice {
		if _, upgrading := node.Labels[upgrade.DriverUpgradingLabelKey]; upgrading {
			logf.FromContext(ctx).Info("드라이버 업그레이드 중 — 복구 작업 생략",
				"node", node.Name, "reason", res.Reason)
			return "", nil
		}
	}
	name := healthOperationName(plan.TransactionID)
	keys := make([]string, 0, len(plan.ResourceKeys))
	for _, k := range plan.ResourceKeys {
		keys = append(keys, string(k))
	}
	op := &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: string(plan.Type), NodeName: node.Name,
			Vendor:        driverRecoveryVendor(plan.Type, res.Devices),
			TransactionID: plan.TransactionID,
			Owner:         npuv1alpha1.OperationOwner{Kind: healthOwnerKind, Name: node.Name},
			ResourceKeys:  keys,
		},
	}
	if err := r.Create(ctx, op); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return name, nil // 같은 원인의 작업이 이미 있다 — 중복 억제가 동작한 것이다.
		}
		return "", err
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(op, corev1.EventTypeWarning, "RecoveryRequested",
			"health %s(%s) 때문에 %s 작업을 요청했다", res.State, res.Reason, plan.Type)
	}
	logf.FromContext(ctx).Info("health recovery requested",
		"node", node.Name, "reason", res.Reason, "type", plan.Type, "op", name)
	return name, nil
}

// healthOperationName 은 트랜잭션 ID 에서 작업 이름을 만든다. 결정론적이라 같은 원인·같은
// 쿨다운 구간이면 같은 이름이 나오고, 그래서 Create 가 AlreadyExists 로 튕겨 중복이 막힌다.
// 63자 상한은 쿠버네티스 이름 규칙이다.
func healthOperationName(txID string) string {
	// 원인 이름이 대문자를 포함하므로 그대로 쓰면 객체 이름 규칙(RFC 1123)에 걸린다.
	// 트랜잭션 ID 자체는 사람이 읽는 값이라 원형을 유지하고, 이름만 소문자로 옮긴다.
	name := strings.ToLower(txID)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// driverRecoveryVendor 는 RecoverDevice 작업을 이 노드의 어느 벤더로 좁힐지다. 드라이버가 안
// 올라온 장치가 한 벤더뿐이면 그 벤더로 좁힌다 — 안 그러면 혼재 노드(예: NVIDIA+Furiosa)에서
// 한 벤더의 드라이버 고장이 멀쩡한 다른 벤더의 드라이버 pod 까지 쿨다운마다 지운다. 두 벤더가
// 같이 고장이거나(노드 단위 판정이라 원인 특정 불가) 장치 판정 자체가 없으면(res.Devices 가
// 빈 노드 단위 조기 종료 경로) 비워 둔다 — 비우면 참가자가 노드의 드라이버 pod 전부를
// 대상으로 한다(전부 고장일 때 옳은 동작).
func driverRecoveryVendor(t operation.Type, devices []health.DeviceResult) string {
	if t != operation.RecoverDevice {
		return ""
	}
	vendor := ""
	for _, d := range devices {
		if d.State == health.StateHealthy || d.Reason != health.ReasonDriverNotLoaded {
			continue
		}
		if vendor != "" && vendor != d.Vendor {
			return "" // 두 벤더가 같이 고장 — 좁히지 않는다
		}
		vendor = d.Vendor
	}
	if !acceleratorOperationVendors[vendor] {
		// NDR 이 소문자화한 벤더값이 CRD enum(api/v1alpha1 acceleratoroperation_types.go) 밖이면
		// 좁히지 않는다. 좁혔다가는 두 가지로 락아웃된다: ①필터 라벨이 어긋나면 pod 을 하나도
		// 못 찾아 deleted==0 → ApplyFailed → 3회 → Quarantined(사람만 해제 가능) ②op.Spec.Vendor
		// 자체가 CRD 검증에 걸려 AcceleratorOperation 생성이 거절돼 그 노드 health 를 영영 못 쓴다.
		return ""
	}
	return vendor
}

// acceleratorOperationVendors 는 AcceleratorOperationSpec.Vendor 의 CRD enum 과 같은 목록이다
// (api/v1alpha1/acceleratoroperation_types.go, kubebuilder:validation:Enum). 두 곳이 갈라지면
// 한쪽만 고치고 고쳤다고 믿게 되므로 값을 그대로 옮겨 적었다 — enum 이 바뀌면 여기도 바꿔야 한다.
var acceleratorOperationVendors = map[string]bool{
	"nvidia": true, "furiosa": true, "rebellions": true, "tenstorrent": true,
}

// devicePCIs 는 이 노드 장치의 PCI 목록이다(복구 작업의 자원 키 재료).
func (r *AcceleratorHealthReconciler) devicePCIs(ctx context.Context, node string) []string {
	var ndr npuv1alpha1.NodeDeviceReport
	if err := r.Get(ctx, types.NamespacedName{Name: node}, &ndr); err != nil {
		return nil
	}
	var out []string
	for _, d := range ndr.Status.Devices {
		if d.PCIeAddress != "" {
			out = append(out, d.PCIeAddress)
		}
	}
	return out
}

// writeHealth 는 판정을 영속한다. **상태가 그대로면 쓰지 않는다** — 타임스탬프만 다른 status 를
// 매번 쓰면 watch 이벤트가 자기 자신을 재큐잉해 핫루프가 된다.
func (r *AcceleratorHealthReconciler) writeHealth(ctx context.Context, node *corev1.Node,
	prev *npuv1alpha1.AcceleratorHealth, res health.Result, opRef string, in health.Inputs) error {
	ah := prev.DeepCopy()
	created := false
	if ah.CreationTimestamp.IsZero() {
		ah.Spec = npuv1alpha1.AcceleratorHealthSpec{NodeName: node.Name}
		if err := r.Create(ctx, ah); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return err
			}
			if err := r.Get(ctx, types.NamespacedName{Name: node.Name}, ah); err != nil {
				return err
			}
		}
		created = true
	}

	next := npuv1alpha1.AcceleratorHealthStatus{
		State:                res.State,
		Reason:               res.Reason,
		AllocationAllowed:    res.AllocationAllowed,
		Signals:              toHealthSignals(res.Signals),
		DetectedAt:           prev.Status.DetectedAt,
		RecoveryOperationRef: prev.Status.RecoveryOperationRef,
		RecoveryFailures:     in.RecoveryFailures,
		SuspectedAt:          nextSuspectedAt(prev, res, in),
		Devices:              toDeviceHealths(res.Devices),
	}
	if opRef != "" {
		next.RecoveryOperationRef = opRef
	}
	if prev.Status.State != res.State || next.DetectedAt.IsZero() {
		next.DetectedAt = metav1.NewTime(in.Now)
	}
	if !created && equalHealthStatus(prev.Status, next) {
		return nil
	}
	ah.Status = next
	return r.Status().Update(ctx, ah)
}

// nextSuspectedAt 은 광고 불일치를 처음 본 시각을 유지한다. 불일치가 사라지면 지운다 —
// 남겨 두면 다음 불일치가 유예 없이 곧바로 확정된다.
func nextSuspectedAt(prev *npuv1alpha1.AcceleratorHealth, res health.Result,
	in health.Inputs) *metav1.Time {
	suspect := false
	for _, s := range res.Signals {
		if s.Name == health.SignalAdvertisement && (!s.OK || s.Message == "유예 중") {
			suspect = true
		}
	}
	if !suspect {
		return nil
	}
	if prev.Status.SuspectedAt != nil {
		return prev.Status.SuspectedAt
	}
	t := metav1.NewTime(in.Now)
	return &t
}

func toHealthSignals(sigs []health.Signal) []npuv1alpha1.HealthSignal {
	out := make([]npuv1alpha1.HealthSignal, 0, len(sigs))
	for _, s := range sigs {
		out = append(out, npuv1alpha1.HealthSignal{
			Name: s.Name, Target: s.Target, OK: s.OK, Message: s.Message,
		})
	}
	return out
}

func toDeviceHealths(devs []health.DeviceResult) []npuv1alpha1.DeviceHealth {
	out := make([]npuv1alpha1.DeviceHealth, 0, len(devs))
	for _, d := range devs {
		out = append(out, npuv1alpha1.DeviceHealth{PCIAddress: d.PCI, State: d.State, Reason: d.Reason})
	}
	return out
}

// equalHealthStatus 는 의미 있는 필드가 같은지다. DetectedAt·SuspectedAt 은 상태가 같으면
// 승계되므로 비교에 넣어도 안전하다.
func equalHealthStatus(a, b npuv1alpha1.AcceleratorHealthStatus) bool {
	if a.State != b.State || a.Reason != b.Reason || a.AllocationAllowed != b.AllocationAllowed ||
		a.RecoveryOperationRef != b.RecoveryOperationRef || a.RecoveryFailures != b.RecoveryFailures ||
		len(a.Signals) != len(b.Signals) || len(a.Devices) != len(b.Devices) {
		return false
	}
	if (a.SuspectedAt == nil) != (b.SuspectedAt == nil) {
		return false
	}
	for i := range a.Signals {
		if a.Signals[i] != b.Signals[i] {
			return false
		}
	}
	for i := range a.Devices {
		if a.Devices[i] != b.Devices[i] {
			return false
		}
	}
	return true
}

// syncQuarantineLabel 은 라벨을 상태와 맞춘다. 값이 이미 같으면 patch 하지 않는다 —
// 불필요한 write 는 watch 이벤트를 만들고, 그 이벤트가 같은 객체를 재큐잉해 핫루프가 된다.
func (r *AcceleratorHealthReconciler) syncQuarantineLabel(ctx context.Context, node *corev1.Node,
	quarantined bool) error {
	cur := node.Labels[QuarantineLabel] == labelValueTrue
	if cur == quarantined {
		return nil
	}
	base := node.DeepCopy()
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	if quarantined {
		node.Labels[QuarantineLabel] = labelValueTrue
	} else {
		delete(node.Labels, QuarantineLabel)
	}
	return r.Patch(ctx, node, client.MergeFrom(base))
}

// SetupWithManager 는 노드 변화와 자기 CR 변화를 본다.
func (r *AcceleratorHealthReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Owns(&npuv1alpha1.AcceleratorHealth{}).
		Named("acceleratorhealth").
		Complete(r)
}
