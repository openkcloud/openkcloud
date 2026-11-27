// ============================================================
// driver_upgrade_controller.go: Driver Upgrade 컨트롤러 (Reconciler)
// 상세: DriverUpgradeState CRD를 감시하고 UpgradeStateMachine을 호출하여
//       노드별 드라이버 업그레이드 상태 전이를 수행합니다.
//       ensureUpgradeStates()로 NodeDeviceReport 기반 DUS CR을 자동 생성합니다.
// 생성일: 2026-04-13 | 수정일: 2026-10-28
// ============================================================

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/upgrade"
	"kcloud-operator/internal/vendorcap"
	"kcloud-operator/internal/verification"
)

// +kubebuilder:rbac:groups=npu.ai,resources=driverupgradestates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=npu.ai,resources=driverupgradestates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=npu.ai,resources=driverinstallpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=npu.ai,resources=nodedevicereports,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// DriverUpgradeReconciler는 DriverUpgradeState CR을 감시하고 업그레이드 상태 머신을 구동합니다.
type DriverUpgradeReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
	StateMachine *upgrade.UpgradeStateMachine
}

func (r *DriverUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	metrics.RecordReconcile() // reconcile 호출 시각 기록 (liveness probe 용)
	logger := logf.FromContext(ctx).WithValues("driverupgradestate", req.Name)

	// 0. Stuck-label sweep — 6일 stuck invariant root cause 재발 방지.
	// 사이클이 비정상 종료(panic, ctx cancel, controller 재시작 도중 crash)되어
	// `npu.ai/driver-upgrading` 라벨이 노드에 남으면 detector DS nodeAffinity 가
	// 영구 차단 → NDR stale → 모든 사이클 zombie. 매 reconcile 진입마다 점검.
	if err := r.sweepStuckUpgradingLabels(ctx); err != nil {
		// 비치명적 오류: 본 reconcile 흐름은 계속 진행
		logger.Error(err, "stuck driver-upgrading 라벨 sweep 실패")
	}

	// 1. NodeDeviceReport 기반 DUS 자동 생성/동기화 (부트스트랩 포함 — Get 이전에 실행)
	if err := r.ensureUpgradeStates(ctx); err != nil {
		logger.Error(err, "UpgradeState 동기화 실패")
		// 비치명적 오류: 계속 진행
	}

	// 2. DriverUpgradeState CR 조회 (부트스트랩 더미 요청이면 NotFound → 재시도)
	var state v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, req.NamespacedName, &state); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	// Defer cleanup — Reconcile 함수가 panic 으로 중단되거나 ctx 가 cancel 된 직후라도
	// 사이클 종료 상태(UpgradeCycleClosed)에서는 라벨이 남지 않도록 한다. 같은 노드의 다른 DUS 가
	// 사이클 중이면 그 사이클의 라벨이라 남긴다(아래 otherCycleOnNode).
	// reconcile ctx 와 분리된 background ctx 를 사용해야 ctx cancel 시에도 cleanup 이 실행됨.
	defer func() {
		// 패닉 자체는 그대로 전파하되, 라벨 cleanup 은 시도한다.
		if state.Spec.NodeName == "" {
			return
		}
		if v1alpha1.UpgradeCycleClosed(state.Status.State) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 라벨은 노드 단위다. 같은 노드의 다른 DUS 가 사이클 중이면 그 사이클의 라벨이라 지우지 않는다 —
			// 지우면 그 사이클의 health 복구 억제와 device-plugin 차단이 풀린다(혼재 노드). 목록을 못 읽어도
			// 지우지 않는다. 남은 라벨은 sweep 이 노드의 모든 DUS 종료를 확인한 뒤 지운다.
			if busy, err := r.otherCycleOnNode(cleanupCtx, &state); err != nil || busy {
				return
			}
			if err := r.StateMachine.EnsureUpgradingLabelRemoved(cleanupCtx, state.Spec.NodeName); err != nil {
				logger.Error(err, "defer cleanup: driver-upgrading 라벨 제거 실패",
					"node", state.Spec.NodeName, "state", state.Status.State)
			}
			// 두 라벨 모두 제거하는 것이 invariant 다 — defer 도 동일하게 처리한다.
			// blocking 라벨이 잔류하면 detector 가 spawn 되지 않아 sweep 까지 window(15s)가
			// 생긴다. 이 호출로 그 window 를 0 에 가깝게 줄인다.
			if err := r.StateMachine.EnsureUpgradingBlockingLabelRemoved(cleanupCtx, state.Spec.NodeName); err != nil {
				logger.Error(err, "defer cleanup: driver-upgrading-blocking 라벨 제거 실패",
					"node", state.Spec.NodeName, "state", state.Status.State)
			}
		}
	}()

	// 3. 매칭 DriverInstallPolicy 조회 (vendor/model 기준)
	policy, err := r.findMatchingPolicy(ctx, state.Spec.Vendor, state.Spec.Model)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("DriverInstallPolicy 조회 실패: %w", err)
	}
	if policy == nil {
		logger.Info("매칭 DriverInstallPolicy 없음, 스킵", "vendor", state.Spec.Vendor)
		return ctrl.Result{}, nil
	}

	// 3.5. 위임 모드 — 상태머신을 여기서 직접 돌리는 대신 트랜잭션을 만든다(분기가 아니라
	// 호출자의 이동이므로 조정자와 이 컨트롤러가 동시에 상태머신을 돌릴 수 없다).
	if delegateModeEnabled() {
		if handled, res, derr := r.delegateDriverUpgrade(ctx, &state, policy); handled {
			return res, derr
		}
	}

	// 4. 상태 머신 실행
	requeue, requeueAfter, smErr := r.StateMachine.TransitionState(ctx, &state, policy)

	// 5. DriverUpgradeState 상태 업데이트
	if err := r.Status().Update(ctx, &state); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("상태 업데이트 실패: %w", err)
	}

	if smErr != nil {
		logger.Error(smErr, "상태 머신 오류")
		return ctrl.Result{}, smErr
	}

	if requeue {
		if requeueAfter > 0 {
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{Requeue: true}, nil
	}
	// Furiosa DUS 가 watch event 를 받지 못하는 경우를 위한 주기적 재체크(30초).
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// otherCycleOnNode 는 같은 노드의 다른 DUS 가 사이클 중(UpgradeCycleClosed 가 아님)인지다. 자기 자신은
// 캐시 사본이 이번 pass 의 전이보다 늦을 수 있어 이름으로 뺀다.
func (r *DriverUpgradeReconciler) otherCycleOnNode(ctx context.Context, state *v1alpha1.DriverUpgradeState) (bool, error) {
	var list v1alpha1.DriverUpgradeStateList
	if err := r.List(ctx, &list); err != nil {
		return false, err
	}
	for _, d := range list.Items {
		if d.Name != state.Name && d.Spec.NodeName == state.Spec.NodeName && !v1alpha1.UpgradeCycleClosed(d.Status.State) {
			return true, nil
		}
	}
	return false, nil
}

// desiredVersionChangeMessage 는 Idle DUS 의 목표 버전이 바뀔 때 status.message 를 만든다.
// 이전 값이 비어 있으면(새 DUS 가 로드 버전을 처음 채택한 직후) 화살표 문구 대신 기록 문구를 쓴다.
func desiredVersionChangeMessage(oldDesired, desired string) string {
	if oldDesired == "" {
		return fmt.Sprintf("목표 버전 %s 기록(새 DriverUpgradeState)", desired)
	}
	return fmt.Sprintf("정책 버전 변경: %s → %s", oldDesired, desired)
}

// ensureUpgradeStates는 NodeDeviceReport 기반으로 DriverUpgradeState CR을 자동 생성합니다.
// 버전 불일치 감지 시 State를 UpgradeRequired로 설정합니다.
func (r *DriverUpgradeReconciler) ensureUpgradeStates(ctx context.Context) error {
	logger := logf.FromContext(ctx)

	var ndrList v1alpha1.NodeDeviceReportList
	if err := r.List(ctx, &ndrList); err != nil {
		return err
	}

	// 벤더별 NodeDeviceReport 노드 수 게이지. 지원 벤더 전량을 매 pass 다시 써서(없으면 0)
	// 벤더가 사라졌을 때 옛 값이 남지 않게 한다.
	nodeCounts := map[string]int{}
	for _, ndr := range ndrList.Items {
		seen := map[string]bool{}
		for _, d := range ndr.Status.Devices {
			v := strings.ToLower(d.Vendor)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			nodeCounts[v]++
		}
	}
	for _, v := range vendorcap.Vendors() {
		metrics.SetNPUNodes(v, nodeCounts[v])
	}

	var dipList v1alpha1.DriverInstallPolicyList
	if err := r.List(ctx, &dipList); err != nil {
		return err
	}

	for _, ndr := range ndrList.Items {
		nodeName := ndr.Spec.NodeName
		if nodeName == "" {
			nodeName = ndr.Name
		}

		// Control plane/master 노드 제외
		var node corev1.Node
		if err := r.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
			continue
		}
		if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
			continue
		}
		if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
			continue
		}

		// DUS 는 (노드, 벤더) 당 하나인데 이 루프는 장치마다 돈다 — spec.model 은 장치 단위가
		// 아니라 벤더 단위 값이어야 한다. 정책 선택(findPolicy)은 계속 장치별 model 을 쓴다.
		vendorModels := vendorRepresentativeModels(ndr.Status.Devices)
		observedModels := vendorObservedModels(ndr.Status.Devices)

		for _, device := range ndr.Status.Devices {
			// 매칭 DIP 찾기
			policy := findPolicy(dipList.Items, device.Vendor, device.Model)
			if policy == nil {
				continue
			}
			// nodeSelector 매칭 확인
			if !nodeMatchesSelector(&node, policy.Spec.NodeSelector) {
				continue
			}

			// TrackOnly 정책: DUS 를 Idle 로 추적만 한다(설치/업그레이드 전이 없음).
			// installer 이미지 미비 벤더(예: Tenstorrent, 실제 설치는 DKMS 2차)의 버전 관측용.
			if policy.Spec.Driver.TrackOnly {
				if err := r.ensureTrackOnlyState(ctx, nodeName, device, policy, vendorModels[device.Vendor], observedModels[device.Vendor]); err != nil {
					logger.Error(err, "TrackOnly DUS 동기화 실패", "node", nodeName, "vendor", device.Vendor)
				}
				continue
			}

			// 호스트 버전은 장치 하나가 아니라 NDR 전체에서 상태 기계와 같은 규칙으로 구한다.
			loaded := upgrade.LoadedHostVersion(ndr.Status.Devices, device.Vendor, vendorModels[device.Vendor])
			r.syncUpgradeState(ctx, nodeName, device, policy, vendorModels[device.Vendor],
				observedModels[device.Vendor], loaded)
		}
	}
	return nil
}

// modelUnknown 은 제품명을 특정할 수 없다는 표시다. 이름이 아니라 "모른다" 라서
// upgrade.DeviceModelMatches 가 양방향 와일드카드로 취급한다. 값은
// v1alpha1.DriverModelUnknown 하나뿐이다 — pkg/npuctl 도 그 상수를 그대로 쓴다.
const modelUnknown = v1alpha1.DriverModelUnknown

// vendorRepresentativeModels 는 벤더별 대표 model 이다. DriverUpgradeState 는 (노드, 벤더) 당 하나인데
// 장치는 벤더당 여럿일 수 있어(라이브 <nvidia-node> = nvidia a30 + a2) 장치마다 spec.model 을 쓰면 두 장치가
// 같은 DUS 를 겨눠 매 reconcile 마다 서로 덮어쓰는 영구 쓰기 루프가 된다. 벤더 안에서 model 이 갈리면
// "generic" 이다. generic 은 이름이 아니라 "모른다" 는 표시이고, 혼재 노드는 벤더 단위로 정말 단일
// 제품명이 없다(detector 도 같은 이유로 벤더 단위 라벨을 generic 으로 둔다). upgrade.DeviceModelMatches
// 가 generic 을 양방향 와일드카드로 보므로 재부팅·자가복구 게이트는 그대로 걸려 기능 손실이 없다.
func vendorRepresentativeModels(devices []v1alpha1.DeviceEntry) map[string]string {
	models := make(map[string]string, len(devices))
	for _, d := range devices {
		prev, seen := models[d.Vendor]
		switch {
		case !seen:
			models[d.Vendor] = d.Model
		case prev != d.Model:
			models[d.Vendor] = modelUnknown
		}
	}
	return models
}

// vendorObservedModels 는 벤더별로 관측된 장치 모델을 정렬·중복 제거해 모은다 —
// vendorRepresentativeModels 가 대표값 하나로 뭉갤 때 잃는 정보를 status 에 남기기 위해서다.
func vendorObservedModels(devices []v1alpha1.DeviceEntry) map[string][]string {
	seen := map[string]map[string]bool{}
	for _, d := range devices {
		if d.Model == "" {
			continue
		}
		if seen[d.Vendor] == nil {
			seen[d.Vendor] = map[string]bool{}
		}
		seen[d.Vendor][d.Model] = true
	}
	out := make(map[string][]string, len(seen))
	for vendor, models := range seen {
		list := make([]string, 0, len(models))
		for m := range models {
			list = append(list, m)
		}
		sort.Strings(list)
		out[vendor] = list
	}
	return out
}

// syncDUSObservedModels 는 status.observedModels 를 관측값과 맞춘다(같으면 쓰지 않는다).
func (r *DriverUpgradeReconciler) syncDUSObservedModels(
	ctx context.Context, existing *v1alpha1.DriverUpgradeState, observed []string) {
	if reflect.DeepEqual(existing.Status.ObservedModels, observed) {
		return
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Status.ObservedModels = observed
	if err := r.Status().Patch(ctx, existing, patch); err != nil {
		logf.FromContext(ctx).Error(err, "DriverUpgradeState status.observedModels 동기화 실패", "name", existing.Name)
	}
}

// resetRebootTracking 은 새 업그레이드 사이클 진입 시 이전 사이클의 재부팅 추적 상태를 초기화한다.
// RebootAttempts 가 stale 이면 Validating→RebootRequired 게이트(need && RebootAttempts==0)가 막혀
// 모듈 교체 재부팅이 발화하지 않는다. RequestedTime/BootID 는 handleRebootRequired 가
// 재부팅 트리거 시 덮어쓰지만 사이클 경계에서 함께 리셋해 stale 관측을 없앤다.
func resetRebootTracking(s *v1alpha1.DriverUpgradeStateStatus) {
	s.RebootAttempts = 0
	s.RebootRequestedTime = metav1.Time{}
	s.RebootBootID = ""
}

// defaultDriftGrace 는 버전 드리프트를 확정하기까지 기다리는 유예 시간이다(EvaluateVersionDrift 입력).
const defaultDriftGrace = 10 * time.Minute

// driftBaseline 은 드리프트 감지가 호스트에 있어야 한다고 보는 버전이다. 롤백 뒤 보류 중(목표가
// rolledBackVersion)이면 롤백이 복원한 previousVersion 이 기준이다. 관측(observed)이 목표와 같으면
// 보류 중이라도 목표가 기준이다(호스트에서 직접 설치 등, 상태 기계가 보류를 풀기 전 reconcile).
// 정책 목표만 쓰면 보류 중 거짓 경고가 나고, 경고를 끄면 보류 중의 실제 호스트 변경을 놓친다.
func driftBaseline(s *v1alpha1.DriverUpgradeState, desired, observed string) string {
	if observed != desired && s.Status.RolledBackVersion != "" && s.Status.RolledBackVersion == desired &&
		s.Status.PreviousVersion != "" {
		return s.Status.PreviousVersion
	}
	return desired
}

// timeOrZero 는 *metav1.Time 이 nil 이면 zero time.Time 을 돌려준다(EvaluateVersionDrift 입력 변환용).
func timeOrZero(t *metav1.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time
}

// syncUpgradeState 는 설치형(비-TrackOnly) 정책에 대해 device 하나의 DUS 를 생성/전이합니다.
// 신규 생성(버전 비교로 초기 State), 정책 버전 변경, 버전 불일치→UpgradeRequired,
// currentVersion 동기화를 처리합니다. 에러는 로깅 후 skip(기존 동작 보존).
// loaded 는 versionSource=Host 의 목표가 되는 호스트 버전(upgrade.LoadedHostVersion)이다.
func (r *DriverUpgradeReconciler) syncUpgradeState(ctx context.Context, nodeName string,
	device v1alpha1.DeviceEntry, policy *v1alpha1.DriverInstallPolicy, vendorModel string, observed []string,
	loaded string) {
	logger := logf.FromContext(ctx)
	dusName := driverUpgradeStateName(nodeName, device.Vendor)

	var existing v1alpha1.DriverUpgradeState
	err := r.Get(ctx, types.NamespacedName{Name: dusName}, &existing)
	if apierrors.IsNotFound(err) {
		// 신규 DUS 는 항상 Idle 로 만든다. 버전이 어긋났는지는 여기서 판단하지 않는다 —
		// 업그레이드로 넘어갈지는 상태기계 handleIdle 이 autoUpgrade·verifiedVersions·cooldown
		// 게이트를 걸어 정한다. 생성 시점에 UpgradeRequired 로 박으면 그 게이트를 지날 길이 없다.
		initialState := v1alpha1.UpgradeStateIdle
		dus := v1alpha1.DriverUpgradeState{
			ObjectMeta: metav1.ObjectMeta{
				Name: dusName,
			},
			Spec: v1alpha1.DriverUpgradeStateSpec{
				NodeName: nodeName,
				Vendor:   device.Vendor,
				Model:    vendorModel,
			},
			Status: v1alpha1.DriverUpgradeStateStatus{
				State:              initialState,
				CurrentVersion:     device.DriverVersion,
				DesiredVersion:     upgrade.EffectiveDesiredVersion(policy, loaded),
				LastTransitionTime: metav1.Now(),
				ObservedModels:     observed,
			},
		}
		if err := r.Create(ctx, &dus); err != nil && !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "DriverUpgradeState 생성 실패", "name", dusName)
		}
		return
	}
	if err != nil {
		logger.Error(err, "DriverUpgradeState 조회 실패", "name", dusName)
		return
	}

	// 아래 두 분기가 조건부로 조기 return 하므로 model 동기화를 먼저 한다.
	r.syncDUSModel(ctx, &existing, vendorModel)
	r.syncDUSObservedModels(ctx, &existing, observed)

	// 대역외 드라이버 변경 감지(감지 전용) — 기준과 NDR 관측값이 어긋난 채 유예를 넘기면 Warning 이벤트로만
	// 알린다. 기준은 driftBaseline(보류 중이고 관측이 목표와 다르면 previousVersion, 그 밖은
	// EffectiveDesiredVersion — Host 모드는 로드된 호스트 버전)이다. CurrentVersion 은 아래 동기화가
	// 덮어써 못 쓴다. 상태 집합은 VersionObservationAllowed 의 셋(Idle·UnverifiedVersion·HostAhead)이다.
	// Idle 만 보면 허용 목록 밖 버전에 머문 노드에서 감지가 꺼진다. HostAhead 에서는 같은 어긋남을
	// 상태 기계의 HostDriverAhead 이벤트가 이미 알리므로 확정돼도 경고를 내지 않는다.
	if v1alpha1.VersionObservationAllowed(existing.Status.State) && device.DriverVersion != "" {
		verdict, reason := verification.EvaluateVersionDrift(verification.VersionDriftInput{
			State:           existing.Status.State,
			DeclaredVersion: driftBaseline(&existing, upgrade.EffectiveDesiredVersion(policy, loaded), device.DriverVersion),
			ObservedVersion: device.DriverVersion,
			ObservedAt:      time.Now(),
			Since:           timeOrZero(existing.Status.DriftFirstSeen),
			Grace:           defaultDriftGrace,
		})
		next := existing.Status.DriftFirstSeen
		switch verdict {
		case verification.VersionDriftConfirmed:
			if r.Recorder != nil && existing.Status.State != v1alpha1.UpgradeStateHostAhead {
				r.Recorder.Event(&existing, corev1.EventTypeWarning, "DriverVersionDrift", reason)
			}
		case verification.VersionDriftPending:
			if next == nil {
				now := metav1.Now()
				next = &now
			}
		case verification.VersionDriftNone:
			next = nil
		}
		if (next == nil) != (existing.Status.DriftFirstSeen == nil) ||
			(next != nil && existing.Status.DriftFirstSeen != nil && !next.Equal(existing.Status.DriftFirstSeen)) {
			patch := client.MergeFrom(existing.DeepCopy())
			existing.Status.DriftFirstSeen = next
			if err := r.Status().Patch(ctx, &existing, patch); err != nil {
				logger.Error(err, "DriftFirstSeen 갱신 실패", "name", dusName)
			}
		}
	}

	// desiredVersion 이 정책과 다르면 update 한다. 단 State==Idle 에서만 — mid-cycle 은
	// 상태기계가 DesiredVersion 을 소유하고, 롤백은 그것을 PreviousVersion 으로 놓는다.
	// 여기서 되돌리면 롤백을 깨고 매 롤백마다 재부팅 예산이 리셋돼 MaxReboots 상한이 무력화된다.
	// Idle 새 사이클 리셋은 handleIdle 이 백업한다.
	desiredVersion := upgrade.EffectiveDesiredVersion(policy, loaded)
	if desiredVersion != "" && existing.Status.DesiredVersion != desiredVersion &&
		existing.Status.State == v1alpha1.UpgradeStateIdle {
		oldDesired := existing.Status.DesiredVersion
		patch := client.MergeFrom(existing.DeepCopy())
		existing.Status.DesiredVersion = desiredVersion
		existing.Status.CurrentVersion = device.DriverVersion
		if existing.Status.State == v1alpha1.UpgradeStateIdle {
			// 새 사이클의 입력만 정리한다. Idle→UpgradeRequired 전이는 상태기계 handleIdle 이
			// 게이트(autoUpgrade·verifiedVersions·cooldown)를 걸어 결정한다.
			existing.Status.PreviousImage = ""
		}
		// 새 사이클 진입: 이전 사이클의 재부팅 카운터/마커를 리셋(durable persist).
		// 모듈 교체 재부팅 게이트(need && RebootAttempts==0)가 stale 값에 막히지 않게 한다.
		resetRebootTracking(&existing.Status)
		existing.Status.LastTransitionTime = metav1.Now()
		existing.Status.Message = desiredVersionChangeMessage(oldDesired, desiredVersion)
		if err := r.Status().Patch(ctx, &existing, patch); err != nil {
			logger.Error(err, "DriverUpgradeState 상태 패치 실패 (desiredVersion 변경)", "name", dusName)
		}
		return
	}

	// 버전 불일치를 여기서 UpgradeRequired 로 전이시키지 않는다 — 그러면 handleIdle 의 게이트
	// (autoUpgrade / verifiedVersions / IdleCooldown)를 통째로 지나친다. 전이 결정은 handleIdle
	// 한 곳이 갖는다. 불일치 상황의 관측값 갱신은 아래 currentVersion 동기화가 이미 담당하므로
	// 여기에 별도 분기를 두지 않는다 — 두면 매 reconcile 마다 LastTransitionTime 이 갱신돼
	// IdleCooldown 이 영원히 차지 않는다.

	// currentVersion 동기화: NDR 이 갱신되었지만 버전이 일치하는 경우
	// (desiredVersion == device.DriverVersion 이나 existing.Status.CurrentVersion이 stale)
	// 위 감지 블록과 같은 상태 집합이다 — 주차된 노드의 보고 버전이 전이 시점 값에 멈추면
	// `kubectl get dus` 와 지표가 노드의 실제 드라이버 버전을 더는 따라가지 않는다.
	if v1alpha1.VersionObservationAllowed(existing.Status.State) &&
		device.DriverVersion != "" &&
		existing.Status.CurrentVersion != device.DriverVersion {

		patch := client.MergeFrom(existing.DeepCopy())
		existing.Status.CurrentVersion = device.DriverVersion
		existing.Status.LastTransitionTime = metav1.Now()
		existing.Status.Message = fmt.Sprintf("NDR 버전 동기화: %s", device.DriverVersion)
		if err := r.Status().Patch(ctx, &existing, patch); err != nil {
			logger.Error(err, "DriverUpgradeState currentVersion 동기화 실패", "name", dusName)
		}
	}
}

// ensureTrackOnlyState 는 TrackOnly 정책에 대해 DUS 를 Idle 상태로만 유지한다. 설치·업그레이드 전이 없이
// currentVersion(NDR)과 desiredVersion 만 동기화한다. desired 는 설치 정책과 같은 규칙으로 고른다 —
// versionSource=Host 면 호스트에 올라온 버전이 곧 목표다(설치기 없는 벤더는 사이트마다 버전이 다르다).
func (r *DriverUpgradeReconciler) ensureTrackOnlyState(ctx context.Context, nodeName string,
	device v1alpha1.DeviceEntry, policy *v1alpha1.DriverInstallPolicy, vendorModel string, observed []string) error {
	const trackMsg = "TrackOnly: 버전 관측 전용(설치 경로 없음)"
	dusName := driverUpgradeStateName(nodeName, device.Vendor)
	desired := upgrade.EffectiveDesiredVersion(policy, device.DriverVersion)

	var existing v1alpha1.DriverUpgradeState
	err := r.Get(ctx, types.NamespacedName{Name: dusName}, &existing)
	if apierrors.IsNotFound(err) {
		dus := v1alpha1.DriverUpgradeState{
			ObjectMeta: metav1.ObjectMeta{Name: dusName},
			Spec: v1alpha1.DriverUpgradeStateSpec{
				NodeName: nodeName,
				Vendor:   device.Vendor,
				Model:    vendorModel,
			},
			Status: v1alpha1.DriverUpgradeStateStatus{
				State:              v1alpha1.UpgradeStateIdle,
				CurrentVersion:     device.DriverVersion,
				DesiredVersion:     desired,
				LastTransitionTime: metav1.Now(),
				Message:            trackMsg,
				ObservedModels:     observed,
			},
		}
		if createErr := r.Create(ctx, &dus); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
		return nil
	}
	if err != nil {
		return err
	}

	r.syncDUSModel(ctx, &existing, vendorModel)
	r.syncDUSObservedModels(ctx, &existing, observed)

	// 기존 DUS: currentVersion/desiredVersion 동기화, State 는 항상 Idle 로 강제.
	if existing.Status.State != v1alpha1.UpgradeStateIdle ||
		existing.Status.CurrentVersion != device.DriverVersion ||
		existing.Status.DesiredVersion != desired {
		patch := client.MergeFrom(existing.DeepCopy())
		existing.Status.State = v1alpha1.UpgradeStateIdle
		existing.Status.CurrentVersion = device.DriverVersion
		existing.Status.DesiredVersion = desired
		existing.Status.LastTransitionTime = metav1.Now()
		existing.Status.Message = trackMsg
		return r.Status().Patch(ctx, &existing, patch)
	}
	return nil
}

// syncDUSModel 은 DriverUpgradeState.spec.model 을 NodeDeviceReport 가 보고한 장치 모델과 맞춘다.
// spec.model 은 생성 시점 값이라 갱신되지 않아 라이브에서 이미 어긋나 있을 수 있다. 접두어·와일드카드
// 규칙(upgrade.DeviceModelMatches)은 이름이 구체화되는 방향만 덮는다 — `update-pciids` 를 돌리면
// 표기가 통째로 바뀔 수 있고(`a100-pcie-40gb` → `a100-sxm4-40gb`), 그때 두 값은 서로 접두어도
// 아니고 어느 쪽도 "generic" 이 아니라 매칭이 끊긴다. spec 갱신은 CRD·webhook 제약이 없고 값이
// 같으면 patch 가 나가지 않아 reconcile 마다 쓰기가 늘지 않는다.
func (r *DriverUpgradeReconciler) syncDUSModel(
	ctx context.Context, existing *v1alpha1.DriverUpgradeState, deviceModel string) {
	if deviceModel == "" || existing.Spec.Model == deviceModel {
		return
	}
	patch := client.MergeFrom(existing.DeepCopy())
	old := existing.Spec.Model
	existing.Spec.Model = deviceModel
	if err := r.Patch(ctx, existing, patch); err != nil {
		logf.FromContext(ctx).Error(err, "DriverUpgradeState spec.model 동기화 실패",
			"name", existing.Name, "from", old, "to", deviceModel)
		return
	}
	logf.FromContext(ctx).Info("DriverUpgradeState spec.model 동기화",
		"name", existing.Name, "from", old, "to", deviceModel)
}

// findMatchingPolicy는 vendor/model에 맞는 DriverInstallPolicy를 반환합니다.
func (r *DriverUpgradeReconciler) findMatchingPolicy(ctx context.Context, vendor, model string) (*v1alpha1.DriverInstallPolicy, error) {
	var list v1alpha1.DriverInstallPolicyList
	if err := r.List(ctx, &list); err != nil {
		return nil, err
	}
	return findPolicy(list.Items, vendor, model), nil
}

// SetupWithManager는 컨트롤러를 manager에 등록합니다.
// Primary: DriverUpgradeState, Secondary: DriverInstallPolicy + NodeDeviceReport
func (r *DriverUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DriverUpgradeState{}).
		Watches(
			&v1alpha1.DriverInstallPolicy{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				// DriverInstallPolicy 변경 시 해당 vendor의 모든 DUS enqueue
				pol, ok := obj.(*v1alpha1.DriverInstallPolicy)
				if !ok {
					return nil
				}
				var dusList v1alpha1.DriverUpgradeStateList
				if err := mgr.GetClient().List(ctx, &dusList); err != nil {
					return nil
				}
				var reqs []reconcile.Request
				for _, dus := range dusList.Items {
					if dus.Spec.Vendor == pol.Spec.Vendor {
						reqs = append(reqs, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: dus.Name},
						})
					}
				}
				return reqs
			}),
		).
		// NDR watch — DUS가 없을 때 부트스트랩 트리거
		Watches(
			&v1alpha1.NodeDeviceReport{},
			handler.EnqueueRequestsFromMapFunc(r.mapNDRToUpgradeStates),
		).
		Named("driverupgradestate").
		Complete(r)
}

// mapNDRToUpgradeStates는 NDR 변경 시 해당 노드의 DUS를 enqueue합니다.
// DUS가 없으면 부트스트랩 더미 요청을 생성하여 ensureUpgradeStates()를 트리거합니다.
func (r *DriverUpgradeReconciler) mapNDRToUpgradeStates(ctx context.Context, obj client.Object) []reconcile.Request {
	ndr, ok := obj.(*v1alpha1.NodeDeviceReport)
	if !ok {
		return nil
	}

	nodeName := ndr.Spec.NodeName
	if nodeName == "" {
		nodeName = ndr.Name
	}

	var dusList v1alpha1.DriverUpgradeStateList
	if err := r.List(ctx, &dusList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	found := false
	for _, dus := range dusList.Items {
		if dus.Spec.NodeName == nodeName {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dus.Name},
			})
			found = true
		}
	}

	// DUS가 없으면 더미 request로 Reconcile 트리거 (ensureUpgradeStates가 DUS 생성)
	if !found {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: nodeName + "-bootstrap"},
		})
	}

	return requests
}

// ─────────────────────────────────────────────
// 패키지 내 헬퍼
// ─────────────────────────────────────────────

// driverUpgradeStateName은 노드+벤더 기반 DUS 이름을 생성합니다.
func driverUpgradeStateName(nodeName, vendor string) string {
	return fmt.Sprintf("%s-%s", nodeName, vendor)
}

// nodeMatchesSelector는 노드 라벨이 selector 조건을 모두 만족하는지 확인합니다.
// selector가 비어있으면 모든 노드에 매칭됩니다.
func nodeMatchesSelector(node *corev1.Node, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}
	for k, v := range selector {
		if node.Labels[k] != v {
			return false
		}
	}
	return true
}

// stuckLabelGracePeriod 는 driver-upgrading 라벨 (사이클 추적용) 의 stuck grace 마진이다.
// 정상 사이클의 transient 상태 변화에서 잘못 sweep 하는 것을 방지.
const stuckLabelGracePeriod = 30 * time.Second

// stuckBlockingLabelGracePeriod 는 driver-upgrading-blocking 라벨 (Validating 진입 시 자동 제거되는
// 좁은 lifecycle) 의 grace. 일반 라벨보다 짧음 — Validating 진입 후에는 빨리 풀려야 detector
// 차단 해제까지 시간이 줄어듦.
const stuckBlockingLabelGracePeriod = 15 * time.Second

// sweepStuckUpgradingLabels 는 사이클이 비정상 종료된 채 driver-upgrading(-blocking) 라벨만
// 남은 노드에서 그 라벨을 제거한다. 전부 만족할 때만 cleanup 한다: (1) 라벨이 있다 (2) 해당
// 노드+vendor 의 DUS state 가 종료 상태(UpgradeCycleClosed) (3) LastTransitionTime 이 grace 이상
// 경과(transient 보호) (4) 매칭 DUS 가 하나도 없으면 라벨을 보존한다(다른 컨트롤러 소유 가능성).
// mid-cycle(PreFlight~Uncordoning) 라벨은 절대 건드리지 않는다. blocking 라벨은 lifecycle 이
// 좁아(Cordoning~Validating 진입) grace 를 15s 로 더 짧게 둔다.
func (r *DriverUpgradeReconciler) sweepStuckUpgradingLabels(ctx context.Context) error {
	logger := logf.FromContext(ctx)

	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return fmt.Errorf("노드 리스트 조회 실패: %w", err)
	}

	var dusList v1alpha1.DriverUpgradeStateList
	if err := r.List(ctx, &dusList); err != nil {
		return fmt.Errorf("DriverUpgradeState 리스트 조회 실패: %w", err)
	}

	dusByNode := map[string][]v1alpha1.DriverUpgradeState{}
	for _, dus := range dusList.Items {
		dusByNode[dus.Spec.NodeName] = append(dusByNode[dus.Spec.NodeName], dus)
	}

	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		_, hasMain := node.Labels[upgrade.DriverUpgradingLabelKey]
		_, hasBlocking := node.Labels[upgrade.DriverUpgradingBlockingLabelKey]
		if !hasMain && !hasBlocking {
			continue
		}

		nodeDUS, found := dusByNode[node.Name]
		if !found || len(nodeDUS) == 0 {
			// DUS 부재: 다른 컨트롤러/벤더 소유 라벨일 수 있음 → 보존
			continue
		}

		// 모든 DUS 가 종료 상태이고 grace 경과한 경우에만 stuck 으로 판정
		allTerminal := true
		oldestTransition := time.Now()
		for _, dus := range nodeDUS {
			if !v1alpha1.UpgradeCycleClosed(dus.Status.State) {
				// mid-cycle — 절대 건드리지 않음
				allTerminal = false
			}
			if !dus.Status.LastTransitionTime.IsZero() &&
				dus.Status.LastTransitionTime.Time.Before(oldestTransition) {
				oldestTransition = dus.Status.LastTransitionTime.Time
			}
		}
		if !allTerminal {
			continue
		}
		age := time.Since(oldestTransition)

		// blocking 라벨: 더 짧은 grace 로 빠르게 sweep
		if hasBlocking && age >= stuckBlockingLabelGracePeriod {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := r.StateMachine.EnsureUpgradingBlockingLabelRemoved(cleanupCtx, node.Name)
			cancel()
			if err != nil {
				logger.Error(err, "stuck driver-upgrading-blocking 라벨 sweep 실패", "node", node.Name)
			} else {
				logger.Info("stuck driver-upgrading-blocking 라벨 자동 제거", "node", node.Name,
					"dusCount", len(nodeDUS), "ageSeconds", age.Seconds())
				if r.Recorder != nil && len(nodeDUS) > 0 {
					r.Recorder.Eventf(&nodeDUS[0], corev1.EventTypeWarning, "StuckBlockingLabelSwept",
						"노드 %s 의 stuck driver-upgrading-blocking 라벨 자동 제거 (DUS 종료 + %ds 경과)",
						node.Name, int(age.Seconds()))
				}
			}
		}

		// 메인 라벨: 기존 grace (30s)
		if hasMain && age >= stuckLabelGracePeriod {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := r.StateMachine.EnsureUpgradingLabelRemoved(cleanupCtx, node.Name)
			cancel()
			if err != nil {
				logger.Error(err, "stuck driver-upgrading 라벨 sweep 실패", "node", node.Name)
				continue
			}
			logger.Info("stuck driver-upgrading 라벨 자동 제거", "node", node.Name,
				"dusCount", len(nodeDUS), "ageSeconds", age.Seconds())
			if r.Recorder != nil && len(nodeDUS) > 0 {
				r.Recorder.Eventf(&nodeDUS[0], corev1.EventTypeWarning, "StuckUpgradeLabelSwept",
					"노드 %s 의 stuck npu.ai/driver-upgrading 라벨 자동 제거 (DUS state 종료 + %ds 경과)",
					node.Name, int(age.Seconds()))
			}
		}
	}
	return nil
}

// findPolicy는 vendor/model이 일치하는 정책을 찾습니다.
//
// 매칭 규칙은 upgrade.DeviceModelMatches 가 정본이다. 규칙을 여기에 복제하면 한쪽만 고쳐질
// 수 있다 — detector 가 제품명을 구체화하면 `spec.model: a100` 정책이 장치
// `a100-pcie-40gb` 에 안 걸려 nil 이 나갈 수 있다. 정확일치 우선은 유지한다 — 벤더에 정책이
// 여럿이면 구체적인 것이 제품군 정책을 이겨야 한다.
func findPolicy(policies []v1alpha1.DriverInstallPolicy, vendor, model string) *v1alpha1.DriverInstallPolicy {
	var fallback *v1alpha1.DriverInstallPolicy
	for i := range policies {
		p := &policies[i]
		if !strings.EqualFold(p.Spec.Vendor, vendor) {
			continue
		}
		if strings.EqualFold(p.Spec.Model, model) {
			return p
		}
		// 와일드카드("generic"·빈값)와 제품군 접두어는 fallback (처음 찾은 것만 사용)
		if fallback == nil && upgrade.DeviceModelMatches(model, p.Spec.Model) {
			fallback = p
		}
	}
	return fallback
}

// TransactionIDForDriver 는 드라이버 작업의 요청 정체성이다.
// 목표 버전이 바뀌면 다른 트랜잭션이다 — 같은 노드의 다음 업그레이드가 이전 작업을 재사용하면
// 저널이 두 사이클을 섞어 담는다.
func TransactionIDForDriver(state *v1alpha1.DriverUpgradeState) string {
	return fmt.Sprintf("%s-%s-%s", state.UID, state.Status.DesiredVersion, state.Spec.NodeName)
}

// OperationNameForDriver 는 드라이버 작업 객체 이름이다. 결정론적이라 같은 의도는 같은 객체를
// 재사용한다 — 시각이나 난수를 넣으면 reconcile 마다 작업이 쌓인다.
func OperationNameForDriver(state *v1alpha1.DriverUpgradeState) string {
	name := fmt.Sprintf("drv-%s-%s", state.Name, state.Status.DesiredVersion)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// delegateDriverUpgrade 는 위임 모드에서 드라이버 상태머신을 직접 돌리는 대신 작업을 만든다.
// handled=true 면 호출자는 상태머신을 부르지 않고 여기서 pass 를 끝낸다 — 그래야 조정자와
// 컨트롤러가 같은 상태머신을 동시에 돌리지 않는다(분기가 아니라 호출자의 이동이다).
// 유휴 상태에서는 위임하지 않는다(handled=false) — 그 시점엔 상태머신이 아직 아무것도
// 건드리지 않았고 목표 버전도 계산 전일 수 있어, "업그레이드가 필요한가" 의 판정은
// 상태머신 몫으로 남기고 여기서 다시 구현하지 않는다.
func (r *DriverUpgradeReconciler) delegateDriverUpgrade(ctx context.Context, state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy) (bool, ctrl.Result, error) {
	if state.Status.State != v1alpha1.UpgradeStateRequired {
		// 유휴·진행 중·터미널 — 진행 중이면 이미 만들어 둔 작업이 상태머신을 돌리고 있으므로
		// 여기서는 아무것도 하지 않고 넘긴다.
		if isDriverOperationInFlight(state.Status.State) {
			return true, ctrl.Result{RequeueAfter: opRequeue}, nil
		}
		return false, ctrl.Result{}, nil
	}
	// 설치 Secret 이 없으면 작업을 만들지 않고 DUS 를 Idle 로 되돌린다(대기는 Idle 이 맡는다). 작업을
	// 만들면 노드 잠금 키를 쥔 채 대기해 같은 노드의 다른 벤더 작업이 Blocked 에 묶인다. 상태머신에 넘기지도
	// 않는다 — 그쪽 재확인 사이에 Secret 이 생기면 작업 없이 PreFlight 로 가고, 그 DUS 는 아무도 진행시키지
	// 않은 채 슬롯을 쥔다.
	if back, err := r.StateMachine.BackToIdleWithoutInstallSecret(ctx, state, policy); err != nil {
		return true, ctrl.Result{}, err
	} else if back {
		if err := r.Status().Update(ctx, state); err != nil {
			if apierrors.IsConflict(err) {
				return true, ctrl.Result{Requeue: true}, nil
			}
			return true, ctrl.Result{}, err
		}
		return true, ctrl.Result{RequeueAfter: upgrade.InstallSecretRecheck}, nil
	}

	pcis, err := r.devicePCIsForNode(ctx, state.Spec.NodeName, state.Spec.Vendor)
	if err != nil {
		return true, ctrl.Result{}, err
	}
	op := &v1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: OperationNameForDriver(state)},
		Spec: v1alpha1.AcceleratorOperationSpec{
			Type: string(operation.DriverUpgrade), NodeName: state.Spec.NodeName,
			Vendor:        state.Spec.Vendor,
			TransactionID: TransactionIDForDriver(state),
			Owner: v1alpha1.OperationOwner{
				Kind: "DriverUpgradeState", Name: state.Name,
				UID: string(state.UID), Generation: state.Generation,
			},
			ResourceKeys: ResourceKeysForDriver(state.Spec.NodeName, pcis),
		},
	}
	if err := r.Create(ctx, op); err != nil && !apierrors.IsAlreadyExists(err) {
		return true, ctrl.Result{}, err
	}
	return true, ctrl.Result{RequeueAfter: opRequeue}, nil
}

// isDriverOperationInFlight 는 이미 만들어진 작업이 상태머신을 돌리고 있는 상태인지다.
func isDriverOperationInFlight(state string) bool {
	switch state {
	case v1alpha1.UpgradeStatePreFlight, v1alpha1.UpgradeStateCordoning,
		v1alpha1.UpgradeStateDraining, v1alpha1.UpgradeStateUpgrading,
		v1alpha1.UpgradeStateValidating, v1alpha1.UpgradeStateRebootRequired,
		v1alpha1.UpgradeStateRebooting, v1alpha1.UpgradeStateUncordoning,
		v1alpha1.UpgradeStateRollback:
		return true
	}
	return false
}

// devicePCIsForNode 는 그 노드의 해당 벤더 장치 PCI 목록이다(자원 키 재료).
// 조회 실패는 오류로 올린다 — 장치를 모르는 채 만든 작업은 장치 단위 충돌을 못 잡는다.
func (r *DriverUpgradeReconciler) devicePCIsForNode(ctx context.Context, node, vendor string) ([]string, error) {
	var ndr v1alpha1.NodeDeviceReport
	if err := r.Get(ctx, types.NamespacedName{Name: node}, &ndr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil // 보고가 없으면 노드 키만으로 간다(ResourceKeysForDriver 가 처리한다).
		}
		return nil, err
	}
	pcis := make([]string, 0, len(ndr.Status.Devices))
	for _, d := range ndr.Status.Devices {
		if !strings.EqualFold(d.Vendor, vendor) || d.PCIeAddress == "" {
			continue
		}
		pcis = append(pcis, d.PCIeAddress)
	}
	return pcis, nil
}
