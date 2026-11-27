// ============================================================
// acceleratorpartitionpolicy_controller.go: ACPP reconciler — 상태머신·phase 집계
// 상세: 벤더 backend 위임(RNGD full / NVIDIA discovery-only). RNGD apply = DS env 전역.
// 생성일: 2026-07-23 | 수정일: 2026-10-14
// ============================================================
package controller

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/partition/rngd"
	"kcloud-operator/internal/verification"
)

const acppFinalizer = "npu.ai/acpp-cleanup"

// migOwnerAnnotation 은 nvidia MIG 소유 노드에 찍는 owner-lock 키다(값 = 소유 ACPP 의 UID, ).
const migOwnerAnnotation = "npu.ai/mig-partition-owner"

// nvidiaGPUResource 는 노드 allocatable 의 풀-GPU 리소스명(baseline 계산·expected 구성 공용, goconst 회피).
const nvidiaGPUResource = "nvidia.com/gpu"

// MVP-1: RNGD 는 DS 전역이므로 target = DaemonSet 고정.
// 이름·네임스페이스 모두 npuclusterpolicy_controller.go 의 값을 그대로 따른다 —
// 두 컨트롤러가 같은 DS 를 가리켜야 파티션 적용이 대상을 찾는다.
const rngdUnifiedDSName = furiosaUnifiedDSName

func rngdUnifiedDSNS() string { return furiosaUnifiedDSNamespace() }

// AcceleratorPartitionPolicyReconciler reconciles a AcceleratorPartitionPolicy object.
type AcceleratorPartitionPolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Verifier partition.Verifier // 하드웨어 검증 seam(nil 이면 verify skip)
	// NvidiaExecFactory 는 nvidia MIG apply 의 Executor 를 만드는 seam 이다(nil → 실 JobExecutor).
	// envtest 는 fake executor 를 주입해 실제 Job 없이 apply 경로를 검증한다.
	NvidiaExecFactory func(client.Client) nvidia.Executor
	// NvidiaObserverFactory 는 operator-driven MIG 관측 seam 이다(nil → 실 MigObserver Job, ).
	// envtest 는 fake observer 를 주입해 실제 Job 없이 관측 경로를 검증한다.
	NvidiaObserverFactory func(client.Client) nvidia.Observer
	// Verification 은 근거 기반 commit gate 다(nil 이면 게이트를 걸지 않는다).
	// 게이트가 켜지면 Ready 승격 전에 spec·장치·NodeDeviceReport·광고가 서로 맞는지 확인하고,
	// 어긋나면 Ready 로 올리지 않는다 — 기존 verifySucceeded 는 backend 왕복 성공만 보므로
	// 관측원이 서로 다른 말을 하는 상태를 잡지 못한다.
	Verification *verification.Verifier
	// Leases 는 위임 모드에서 삭제 경로가 노드를 되돌리기 전에 잡는 노드 Lease seam 이다(nil 이면
	// 잠그지 않는다 — 게이트가 꺼진 기존 배포는 이 필드를 아예 안 심으므로 동작이 그대로다).
	// 삭제는 Reconcile 의 위임 분기보다 먼저 처리되므로(핸들러 진입 시점에 이미 결정) 조정자를
	// 거치지 않는다 — 대신 하드웨어를 되돌리기 직전에 조정자와 같은 Lease 를 잡아 "삭제가 진행
	// 중인 트랜잭션과 동시에 같은 노드를 만지는" 창을 없앤다.
	Leases *operation.LeaseManager
}

// evidenceCtx 는 한 번의 reconcile pass 가 모은 검증 근거 입력이다. runTarget 이 채우고
// Reconcile 의 게이트가 읽는다 — 관측 Job 을 두 번 띄우지 않기 위해 pass 안에서 실어 나른다.
type evidenceCtx struct {
	Vendor            string
	Expectation       verification.Expectation
	ObservedGeometry  map[string]string
	ObservationErrors map[string]string
	// TargetDevices 는 이 정책이 건드린 장치(PCI)다 — 보고서 대조를 비대상 장치까지 확대하지 않는다.
	TargetDevices []string
	// Ready 로 승격할 자격이 있는 pass 인지. 재검증을 건너뛴 pass(evidence 재사용)는 false 다.
	Verified bool
}

// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorpartitionpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorpartitionpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorpartitionpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=npu.ai,resources=nodedevicereports,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=pods/eviction,verbs=create

func (r *AcceleratorPartitionPolicyReconciler) backendFor(vendor string) (partition.Backend, error) {
	switch vendor {
	case vendorFuriosa:
		return rngd.New(r.Client).WithVerifier(r.Verifier), nil
	case vendorNvidia:
		return nvidia.New(r.Client).WithExecutor(r.nvidiaExecutor()).WithVerifier(r.Verifier), nil
	}
	return nil, fmt.Errorf("unknown vendor %q", vendor)
}

// nvidiaExecutor 는 apply/rollback 공용 Executor 를 만든다(seam 우선, 없으면 실 JobExecutor).
func (r *AcceleratorPartitionPolicyReconciler) nvidiaExecutor() nvidia.Executor {
	if r.NvidiaExecFactory != nil {
		return r.NvidiaExecFactory(r.Client)
	}
	return nvidia.NewJobExecutor(r.Client, HostExecImage())
}

// nvidiaObserver 는 operator-driven MIG 관측기를 만든다(seam 우선, 없으면 실 MigObserver Job, ).
func (r *AcceleratorPartitionPolicyReconciler) nvidiaObserver() nvidia.Observer {
	if r.NvidiaObserverFactory != nil {
		return r.NvidiaObserverFactory(r.Client)
	}
	return nvidia.NewMigObserver(r.Client, nvidia.Namespace, HostExecImage(), nvidia.StreamApply)
}

// Reconcile 은 discover→validate→diff→(apply)→verify→Ready 상태머신을 target 별로 실행하고 phase 를 집계한다.
func (r *AcceleratorPartitionPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	var acpp npuv1alpha1.AcceleratorPartitionPolicy
	if err := r.Get(ctx, req.NamespacedName, &acpp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// deletion — Retain: 파티션은 유지, owner lock 해제 + finalizer 제거(handleDeletion, ).
	if !acpp.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &acpp)
	}
	if !controllerutil.ContainsFinalizer(&acpp, acppFinalizer) {
		controllerutil.AddFinalizer(&acpp, acppFinalizer)
		if err := r.Update(ctx, &acpp); err != nil {
			return ctrl.Result{}, err
		}
	}

	backend, err := r.backendFor(acpp.Spec.Vendor)
	if err != nil {
		return ctrl.Result{}, r.fail(ctx, &acpp, err.Error())
	}

	// 충돌 판정 — MVP-1 RNGD 단일 DS target: DS-UID 충돌/scope 부분집합/vendor 불일치.
	target := partition.Target{Ctx: ctx, NodeName: firstNodeName(ctx, r.Client, acpp.Spec.NodeSelector), Owner: acpp.Name,
		Vendor:        acpp.Spec.Vendor,
		DaemonSetName: rngdUnifiedDSName, DaemonSetNamespace: rngdUnifiedDSNS()}

	if win, reason := r.resolveConflict(ctx, &acpp); !win {
		ts := npuv1alpha1.TargetStatus{NodeName: target.NodeName, Phase: npuv1alpha1.ACPPPhaseFailed}
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, reason, "conflict/scope guard", acpp.Generation)
		return ctrl.Result{}, r.writeTargetStatus(ctx, &acpp, ts)
	}

	if delegateModeEnabled() {
		// 위임 모드: 정책은 요청만 하고 실행은 Coordinator 가 한다.
		return r.delegateToOperation(ctx, &acpp, target)
	}

	ec := &evidenceCtx{}
	ts, runErr := r.runTarget(&acpp, backend, target, ec)
	if runErr != nil {
		logger.Error(runErr, "runTarget failed")
	}
	// 근거 게이트 — Ready 로 승격하려는 pass 에서만 돈다. 관측원이 서로 다른 말을 하면
	// 여기서 Ready 를 취소한다(하드웨어는 건드리지 않는다 — 되돌리는 것은 Stage 2 의 몫).
	if gerr := r.gateReady(ctx, &acpp, &ts, target, ec); gerr != nil {
		logger.Error(gerr, "evidence gate failed", "node", target.NodeName)
	}
	// cordon 복원 단일 choke-point — mode enable 이 잠근 노드는 종점에서 반드시 풀려야 한다. 개별
	// return 마다 복원을 흩뿌리면 새 terminal 갈래가 생길 때마다 노드가 unschedulable 로 남는다.
	// restoreSchedulable 은 멱등이라 여기서 한 번에 닫는다. 단, rollback + non-nil err(재시도 예약)
	// 은 예외다 — GI apply 는 cordon 을 전제하고 스스로 cordon 하지 않으므로 여기서 풀면 다음
	// reconcile 이 NodeNotCordoned 로 영구 차단되고, CordonedByPolicy 기록도 지워져 이후 수동
	// 개입으로 풀려도 복원 주체가 없다. verify 실패 rollback 은 err 가 nil 이라 복원 대상으로 남는다.
	if runErr == nil || (ts.Phase != npuv1alpha1.ACPPPhaseRollingBack && ts.Phase != npuv1alpha1.ACPPPhaseRollbackFailed) {
		switch ts.Phase {
		case npuv1alpha1.ACPPPhaseFailed, npuv1alpha1.ACPPPhaseUnsupported, npuv1alpha1.ACPPPhaseRollingBack,
			npuv1alpha1.ACPPPhaseRollbackFailed, npuv1alpha1.ACPPPhaseRestored:
			if err := r.restoreSchedulable(ctx, &acpp, target.NodeName); err != nil {
				logger.Error(err, "restore schedulable failed", "node", target.NodeName)
			}
		}
	}
	if err := r.writeTargetStatus(ctx, &acpp, ts); err != nil {
		return ctrl.Result{}, err
	}
	// runTarget 의 non-nil err = transient 실패(Discover/Diff/Apply) → 전파해 requeue(자가치유).
	// Validate 거부·Unsupported 등 terminal 결과는 runTarget 이 nil err + phase 로 반환하므로 requeue 안 함.
	// 단, transient 블록 상태(WaitingForDrain: 외부 cordon/drain 대기, VerifyingAllocatable: 재시도 대기)는
	// runErr 이 nil 이라 기본 resync(~10h)까지 잠들어버린다 → 짧은 RequeueAfter 로 능동 재확인한다(자가치유).
	switch acpp.Status.Phase {
	case npuv1alpha1.ACPPPhaseReady, npuv1alpha1.ACPPPhaseDegraded:
		// 수렴한 정책도 광고를 계속 지켜봐야 한다. 지금은 Ready 에 requeue 가 없어 기본
		// resync(~10시간)까지 잠들고, 그 사이 광고가 무너져도 아무도 모른다.
		// 15초는 유예(기본 30초)와 합쳐 최악 45초 — 완료 기준의 60초 안에 든다.
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	case npuv1alpha1.ACPPPhaseWaitingForDrain:
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	case npuv1alpha1.ACPPPhaseVerifyingAllocatable:
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	case npuv1alpha1.ACPPPhaseApplying:
		// MIG mode 전환 대기(-mig 1 → pending → 재부팅 → 재관측). runErr 가 nil 이라
		// 능동 재확인이 없으면 기본 resync(~10h)까지 잠들어 재부팅 후에도 수렴하지 못한다.
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	case npuv1alpha1.ACPPPhaseRestored:
		// 미수렴 sharing 이 원복된 종점. verifier 는 미수렴을 에러가 아니라 결과로 돌려주므로 runErr
		// 이 nil 이고, 재확인이 없으면 기본 resync(~10h)까지 여기 눌러앉는다. 간격이 긴 이유: 저널이
		// 원복돼 있어 재진입은 멱등 게이트를 못 타고 처음부터 재적용한다(ConfigMap+DS patch → 전
		// nvidia 노드 rollout → DP 재시작 → 180s 검증 → 실패 → 원복 rollout) — 한 번의 재시도가
		// 클러스터 전역 rollout 2회다. MIG 경로(VerifyingAllocatable)는 재검증만 하므로 15s 로
		// 짧아도 되지만 여기는 재-mutation 이라 대칭이 성립하지 않는다.
		return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
	}
	return ctrl.Result{}, runErr
}

// shouldReverify 는 재검증 필요 여부를 판정한다(evidence 재사용 게이트). observedGeneration·
// resolved config 가 직전과 같고 직전 phase 가 Ready 면 false(재검증 skip) — 하나라도 다르면(또는
// prev 가 없으면) true 다. Degraded 도 Ready 와 같이 정상상태로 본다 — 광고 붕괴는 감지 전용이라
// 재적용으로 이어지면 안 되는데, Degraded 를 비정상으로 보면 여기서 true 가 나와 다음 pass 가
// 처음부터 다시 적용한다(GI 재생성·device-plugin 재시작). 되돌리는 것은 Stage 4 remediation 의
// 몫이다.
func (r *AcceleratorPartitionPolicyReconciler) shouldReverify(acpp *npuv1alpha1.AcceleratorPartitionPolicy, prev *npuv1alpha1.TargetStatus) bool {
	if prev == nil {
		return true
	}
	if prev.Phase != npuv1alpha1.ACPPPhaseReady && prev.Phase != npuv1alpha1.ACPPPhaseDegraded {
		return true
	}
	if acpp.Status.ObservedGeneration != acpp.Generation {
		return true
	}
	// backend template hash 변화 감지는 Discover 의 backend.version/observed 로 근사(MVP-1).
	return false
}

// runTarget 은 한 target 에 대해 discover→validate→diff→apply→verify→Ready 상태머신을 실행한다.
// ec 는 이 pass 가 모은 검증 근거 입력을 실어 나른다(Reconcile 의 게이트가 소비한다).
func (r *AcceleratorPartitionPolicyReconciler) runTarget(acpp *npuv1alpha1.AcceleratorPartitionPolicy, backend partition.Backend, t partition.Target, ec *evidenceCtx) (npuv1alpha1.TargetStatus, error) {
	var prev *npuv1alpha1.TargetStatus
	if len(acpp.Status.Targets) > 0 {
		prev = &acpp.Status.Targets[0]
	}
	if !r.shouldReverify(acpp, prev) {
		// evidence 재사용 — 적용 경로는 전부 skip 한다. 다만 "아무것도 안 본다" 는 아니다:
		// 검증 뒤에 광고가 무너졌는지만 확인한다(감지 전용, mutation 없음).
		return r.monitorDrift(t, acpp, *prev)
	}

	ts := npuv1alpha1.TargetStatus{NodeName: t.NodeName, RequestedLayout: acpp.Spec.Layout}

	// 0. 소유권 — 이 노드의 이 벤더 장치를 DRA 가 광고하기로 선언됐다면 우리가 파티션을
	// 건드리면 안 된다. DRA 드라이버는 claim 시점에 MIG 를 구성한다(NVIDIA k8s-dra-driver-gpu
	// 의 createMigDevice). 같은 GPU 를 둘이 재구성하면 재구성 중인 장치 위에 워크로드가 올라간다.
	// 적용 경로 넷이 모두 runTarget 을 지나므로 판정은 여기 한 곳에 둔다.
	if owned, oerr := r.draOwnedNode(t.Ctx, t.NodeName, acpp.Spec.Vendor); oerr != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(&ts, npuv1alpha1.ACPPCondCapabilitiesDiscovered, metav1.ConditionFalse,
			"OwnershipCheckFailed", oerr.Error(), acpp.Generation)
		return ts, oerr
	} else if owned {
		msg := fmt.Sprintf("노드 %s 의 %s 장치는 DRA 가 광고한다(NPUClusterPolicy 의 advertiseBy: dra) — "+
			"파티션 구성 주체가 둘이 되지 않도록 이 노드에는 적용하지 않는다. 되돌리려면 advertiseBy 를 devicePlugin 으로 두라",
			t.NodeName, acpp.Spec.Vendor)
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(&ts, npuv1alpha1.ACPPCondCapabilitiesDiscovered, metav1.ConditionFalse,
			"NodeOwnedByDRA", msg, acpp.Generation)
		return ts, nil
	}

	// 1. Discover
	dres, err := backend.Discover(t)
	if err != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(&ts, npuv1alpha1.ACPPCondCapabilitiesDiscovered, metav1.ConditionFalse, "DiscoveryFailed", err.Error(), acpp.Generation)
		return ts, err
	}
	ts.Backend, ts.DriverVersion, ts.Devices = dres.Backend, dres.DriverVersion, dres.Devices
	ts.Operations, ts.Advertisement, ts.ObservedLayout = dres.Operations, dres.Advertisement, dres.Observed
	setCond(&ts, npuv1alpha1.ACPPCondCapabilitiesDiscovered, metav1.ConditionTrue, "DiscoverySucceeded", "capabilities discovered", acpp.Generation)

	// 1.5 nvidia MIG 관측 — detector 가 못 채우는 MIG mode/geometry/lgip 를 operator
	// observe Job 으로 채워 backend 에 주입·재Discover 한다. Validate 가 관측 기반 supported 를 쓰도록
	// 반드시 Validate 전에 수행한다(관측 실패/미충족은 stop=true 로 조기 반환).
	if acpp.Spec.Vendor == vendorNvidia {
		if nvb, ok := backend.(*nvidia.Backend); ok {
			if stop, oerr := r.observeNvidiaMIG(acpp, backend, nvb, t, &ts); stop {
				return ts, oerr
			}
			ec.Vendor = vendorNvidia
			ec.ObservedGeometry, ec.ObservationErrors = observedGeometryOf(nvb.Targets())
			ec.TargetDevices = targetPCIs(nvb.Targets())
		}
	}

	// 2. Validate — 매 reconcile 마다 방금 관측한 supported 로 다시 판정한다. mode enable 이 끝나
	// profile 목록이 보이기 시작하면 형식-only 완화가 자동으로 풀리고, 지원되지 않는
	// profile 요청은 여기서(GI 생성 전에) 거절된다 — -cgi 를 쏴보고 실패하는 경로로 새지 않는다.
	layouts := toPartitionLayouts(acpp.Spec.Layout)
	if len(layouts) == 0 {
		return r.runSharingOnly(acpp, backend, t, ts, ec)
	}
	if err := backend.Validate(layouts); err != nil {
		// mode enable 때문에 우리가 잠근 노드라면 되돌린다 — 만들다 만 GI 는 없고, 검증 거부는
		// 재시도해도 같은 결과라 노드를 계속 묶어둘 이유가 없다(MIG mode 는 켠 채 유지).
		if rerr := r.restoreSchedulable(t.Ctx, acpp, t.NodeName); rerr != nil {
			return ts, rerr
		}
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(&ts, npuv1alpha1.ACPPCondValidated, metav1.ConditionFalse, "ValidationFailed", err.Error(), acpp.Generation)
		return ts, nil // 검증 거부는 적용 전 종료(criterion 9)
	}
	setCond(&ts, npuv1alpha1.ACPPCondValidated, metav1.ConditionTrue, "ValidationSucceeded", "layout valid", acpp.Generation)

	// 공유 요청의 결함(미지원 backend / replicas 형식)도 적용 전에 거절한다 — 파티션을 바꿔놓고
	// 나서 거절하면 되돌릴 것만 늘어난다(criterion 9: 검증 거부는 적용 전 종료).
	if _, rejected, ok, perr := r.precheckSharing(t.Ctx, acpp, backend, ts); perr != nil {
		return ts, perr
	} else if !ok {
		return r.rejectSharing(acpp, backend, t, rejected)
	}

	// NVIDIA discovery-only: apply 미지원 → Unsupported(status 만 기록).
	if !ts.Operations.Apply.Supported {
		ts.Phase = npuv1alpha1.ACPPPhaseUnsupported
		return ts, nil
	}

	// nvidia MIG: durable-journal 상태머신(전제 2종 분리·소유 no-diff·baseline). RNGD 는 아래 DS-env 흐름 유지.
	if acpp.Spec.Vendor == vendorNvidia {
		nts, nerr := r.runNvidiaTarget(acpp, backend, t, ts)
		if nerr != nil || nts.Phase != npuv1alpha1.ACPPPhaseReady {
			return nts, nerr
		}
		// 파티션이 Ready 면 mode enable 을 위해 우리가 잠근 노드를 되돌린다. GI apply 는
		// cordon 을 요구하므로 여기(성공 종점)가 유일하게 안전한 복원 지점이다. 외부 cordon 은 유지.
		if err := r.restoreSchedulable(t.Ctx, acpp, t.NodeName); err != nil {
			return nts, err
		}
		// 파티션이 Ready 인 뒤에만 공유를 얹는다. MIG Ready 로 가는 세 경로(첫 apply/
		// managed no-diff/크래시 복구) 모두 여기를 통과하므로 spec.sharing 만 바뀌어도 반영된다.
		rec := getApplyRecord(acpp, t.NodeName)
		ec.Expectation = verification.Expectation{
			Profile:        rec.Profile,
			CountPerDevice: rec.Count,
			Geometry:       nvidia.GeometrySummary(rec.Profile, rec.Count),
			Allocatable:    expectedAllocatable(rec),
			ProbeResource:  "nvidia.com/mig-" + rec.Profile,
		}
		ec.Verified = true
		return r.runSharing(acpp, backend, t, nts, expectedAllocatable(rec))
	}

	// 3. Resolve + Diff
	resolved, resolvedStatus := resolveRngd(acpp.Spec.Layout)
	ts.ResolvedLayout = resolvedStatus
	diff, err := backend.Diff(t, resolved)
	if err != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		return ts, err
	}

	// 4. Apply (no-diff 면 skip — 재사용/멱등 정교화는 후속 과제). happy-path 는 no-diff.
	if diff.Changed {
		rbState, err := backend.Apply(t, resolved)
		if err != nil {
			ts.Phase = npuv1alpha1.ACPPPhaseFailed
			setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, "ApplyFailed", err.Error(), acpp.Generation)
			return ts, err
		}
		ts.Backend.Rollback = npuv1alpha1.RollbackInfo{
			PrevPolicy: rbState.PrevPolicy, Generation: rbState.Generation,
			ResourceVersion: rbState.ResourceVersion, TemplateHash: rbState.TemplateHash,
		}
		msg := fmt.Sprintf("policy %s applied (changed=%v)", diff.ToPolicy, diff.Changed)
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionTrue, "DevicePluginConfigured", msg, acpp.Generation)

		// 5. Verify — 실패/에러 시 rollback(RollingBack → Failed/PreviousConfigurationRestored, Ready 로 승격 안 함).
		vr, verr := backend.Verify(t)
		if !verifySucceeded(vr, verr) {
			ts.Phase = npuv1alpha1.ACPPPhaseRollingBack
			if rerr := backend.Rollback(t, *rbState); rerr != nil {
				ts.Phase = npuv1alpha1.ACPPPhaseFailed
				setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionFalse, npuv1alpha1.ACPPPhaseRollbackFailed, rerr.Error(), acpp.Generation)
				return ts, rerr
			}
			ts.Phase = npuv1alpha1.ACPPPhaseFailed // PreviousConfigurationRestored = Failed(Ready 아님)
			setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionTrue, "RollbackSucceeded", "restored previous policy", acpp.Generation)
			return ts, nil
		}
	} else {
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionTrue, "DevicePluginConfigured", "no-diff; policy already applied (changed=false)", acpp.Generation)
		// no-diff 여도 verify 는 수행하며, error 뿐 아니라 all-false 결과도 실패로 처리한다.
		// (RollbackFailed 후 requeue 시 DS 가 이미 applied 값이라 no-diff 로 진입 → 여기서 all-false 를
		//  Ready 로 잘못 승격하면 수동개입 필요 신호가 사라진다. rollback state 는 없으므로 Failed 로만 표시.)
		vr, verr := backend.Verify(t)
		if !verifySucceeded(vr, verr) {
			ts.Phase = npuv1alpha1.ACPPPhaseFailed
			setCond(&ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionFalse, "VerificationFailed", "no-diff verify failed; manual intervention may be required", acpp.Generation)
			return ts, verr
		}
	}
	setCond(&ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionTrue, "AllocationTestSucceeded", "test pod allocated", acpp.Generation)
	ts.Phase = npuv1alpha1.ACPPPhaseReady
	// owner-lock 확정 — changed-apply 경로는 backend.Apply 가 이미 찍지만, no-diff 채택 경로는
	// 여기서만 찍힌다. no-diff happy-path 도 소유권을 주장해야 NCP 재탈환을 막는다.
	if acpp.Spec.Vendor == vendorFuriosa {
		r.ensureOwnerLock(t.Ctx, acpp)
	}
	// RNGD 는 per-PCI geometry 관측 축이 없다(설계 문서의 알려진 한계 항목) — Expectation.Geometry
	// 는 비워 둔다. 그래도 장치 관측 체크 자체는 통과시켜야 한다: ObservedGeometry 를 비워 두면
	// "관측이 아예 없다" 로 읽혀(evalDeviceObservation) 매 pass 마다 등급 없는 근거가 되고, 이 상태로
	// cmd/main.go 가 게이트를 전역으로 켜면 RNGD 는 영원히 Ready 를 못 받는다 — 그래서 관측된 PCI 를
	// 값 없이(geometry 비교 없음, 존재·무오류만) 채운다.
	ec.Vendor = vendorFuriosa
	ec.ObservedGeometry = rngdObservedPCIs(ts.Devices)
	ec.Expectation = verification.Expectation{ProbeResource: rngd.RngdResourceName}
	ec.Verified = true
	// 공유 요청은 여기서도 통과시킨다 — 미지원 backend 는 조용히 무시하지 않고 Unsupported 로 보고한다.
	return r.runSharing(acpp, backend, t, ts, ts.Advertisement.AdvertisedResources)
}

// runNvidiaTarget 은 nvidia MIG apply 의 durable-journal 상태머신이다(전제분리 + journal).
// 전제는 2종(항상: PCI·관측·driver / 변경시: cordon·idle·MIG-safe)으로 분리되며, 소유+Ready+geometry 일치
// 는 no-diff 로 재검증만 하고(변경시-전제 불요구), mutation 은 반드시 ApplyRecord(Applying) 영속 후에만 한다.
func (r *AcceleratorPartitionPolicyReconciler) runNvidiaTarget(acpp *npuv1alpha1.AcceleratorPartitionPolicy, backend partition.Backend, t partition.Target, ts npuv1alpha1.TargetStatus) (npuv1alpha1.TargetStatus, error) {
	nvb, ok := backend.(*nvidia.Backend)
	if !ok {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, "ApplyFailed", "nvidia backend type assertion failed", acpp.Generation)
		return ts, nil
	}
	// MIG 관측은 runTarget 이 Validate 전에 이미 수행해 backend 에 주입·재Discover 한다.

	// (1) 항상-전제(어느 상태서나).
	if reason, msg, blocked := nvidiaAlwaysBlock(nvidia.CheckAlways(nvb.Targets(), ts.DriverVersion != "")); blocked {
		ts.Phase = npuv1alpha1.ACPPPhaseWaitingForDrain
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, reason, msg, acpp.Generation)
		return ts, nil
	}

	// (2) owner-lock 선취득(UID) — Diff/Apply 이전에 소유를 확정한다.
	owned, err := r.ensureNodeOwnerLockUID(t.Ctx, t.NodeName, string(acpp.UID))
	if err != nil {
		return ts, err
	}
	if !owned {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, npuv1alpha1.ReasonTargetConflict, "node MIG owned by another ACPP", acpp.Generation)
		return ts, nil
	}

	// (2.5) MIG mode enable — mode 가 꺼져 있으면 GI 생성 전에 quiesce→-mig 1→reboot 를 완주한다.
	// Diff 이전에 두는 이유: mode 가 Disabled 인 동안의 geometry 비교는 의미가 없고, 무엇보다 전환
	// 중(mode-enable phase) 저널이 아래 !diff.Changed 분류를 통과하면 ExistingMigConfiguration 으로
	// 떨어져 자가치유 없이 정책이 굳는다. 이미 Enabled 인 노드는 즉시 done 이라 기존 경로는 무회귀.
	if done, out, merr := r.ensureMigModeEnabled(acpp, t, nvb.Targets(), ts); merr != nil || !done {
		return out, merr
	}
	// 저널은 ensureMigModeEnabled 가 방금 갱신했을 수 있다(전환 완료 시 mode-전환 phase 제거).
	// 그 이전 스냅샷으로 아래 no-diff 를 분류하면 이미 끝난 전환을 진행 중으로 오인해, geometry 가
	// 일치하는 경우 수렴도 실패도 없이 WaitingForDrain 을 반복한다(노드는 cordon 된 채).
	rec := getApplyRecord(acpp, t.NodeName)

	// (3) Resolve + Diff + 소유 no-diff 술어.
	resolved, resolvedStatus := resolveNvidia(acpp.Spec.Layout)
	ts.ResolvedLayout = resolvedStatus
	t.Generation = acpp.Generation
	diff, derr := backend.Diff(t, resolved)
	if derr != nil {
		return ts, derr
	}
	if handled, out, herr := r.routeNvidiaNoDiff(acpp, backend, nvb, t, ts, rec, resolved, diff.Changed); handled {
		return out, herr
	}

	// (3.5) 같은 generation 의 rollback 은 terminal 이다. verify 미수렴은 에러가 아니라 결과로
	// 돌아오므로(liveVerifier: 180s 초과 → converged=false, err=nil) rollback 종점의 runErr 이 nil 이고
	// rate-limited 재시도도 걸리지 않는다. 그 상태로 재진입하면 아래 cordon 게이트가 노드를 다시 잠그고
	// GI 를 다시 만들어 또 부순다 — 실물 파티션 생성·파괴와 device-plugin 재시작이 무한 왕복한다.
	// 입력(spec)이 그대로면 결과도 그대로이므로 여기서 끝내고 spec 변경을 기다린다(노드는 종점
	// choke-point 가 풀어 준다). 재시도 경로는 generation 증가뿐이다.
	if rec.MigPhase == npuv1alpha1.MigPhaseRolledBack && rec.Generation == acpp.Generation {
		ts.Phase = carryRollbackFailure(acpp, t.NodeName, &ts)
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, "ApplyRolledBack",
			"apply rolled back for this generation; update spec to retry", acpp.Generation)
		return ts, nil
	}

	// (4) hardware 변경 전제 — 진행 중(applyInFlight) 복구는 우리가 만든 Enabled MIG 상태로 차단하면 안 되므로 skip.
	// mode 전환 저널만 남은 경우는 skip 하지 않는다 — GI 를 만들기 전이므로 cordon·idle 을 다시 확인해야 한다.
	if !isApplyInFlightPhase(rec.MigPhase) {
		hw, herr := nvidia.CheckHardwareChange(t.Ctx, r.Client, t.NodeName, nvb.Targets())
		if herr != nil {
			return ts, herr
		}
		// cordon 주체는 정책이다. mode 가 이미 Enabled 인 노드는 유일한 cordon 주체인
		// ensureMigModeEnabled 를 건너뛰므로, 여기서 cordon 을 남에게 맡기면 오지 않는 외부 주체를
		// 기다리며 WaitingForDrain 을 영원히 반복한다. 진행 가능한 상태(mode Enabled + 관리 밖
		// GI 없음)에서 cordon 만 없다면 스스로 잠근다. 배출은 하지 않으므로 GPU 를 쥔 pod 가 남아
		// 있으면 아래 idle 전제가 그대로 걸러 "배출 대기" 로 정직하게 보고된다 — 달라지는 것은
		// "누군가 cordon 해 주기를 기다리는" 상태가 더는 없다는 점이다.
		if !hw.NodeCordoned && !hw.MigModeNotEnabled && !hw.MigUnsafe {
			if cerr := r.cordonForApply(t.Ctx, acpp, t.NodeName, nvb.Targets()); cerr != nil {
				return ts, cerr
			}
			hw.NodeCordoned = true
		}
		if reason, msg, blocked := nvidiaHWBlock(hw); blocked {
			ts.Phase = npuv1alpha1.ACPPPhaseWaitingForDrain
			setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, reason, msg, acpp.Generation)
			return ts, nil
		}
	}

	// (5) persist-before-mutate: baseline 계산 → ApplyRecord(Applying) 을 mutation 이전에 영속(crash-safety).
	newRec, berr := r.computeBaseline(t.Ctx, t.NodeName, acpp, resolved, nvb.Targets())
	if berr != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, npuv1alpha1.ReasonBaselineInconsistent, berr.Error(), acpp.Generation)
		return ts, nil
	}
	newRec.MigPhase = npuv1alpha1.MigPhaseApplying
	if err := r.patchApplyRecord(t.Ctx, acpp, newRec); err != nil {
		return ts, err // 영속 실패 시 mutation 하지 않는다(복원 대상 PCI 가 durable 하지 않으면 위험).
	}
	if err := r.syncMigActiveLabel(t.Ctx, t.NodeName, true); err != nil {
		return ts, err
	}

	// (6) Apply → 실패 시 rollback(rb 는 부분적용 후에도 non-nil).
	nvb.WithExpected(expectedAllocatable(newRec))
	rb, aerr := backend.Apply(t, resolved)
	if aerr != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseRollingBack
		// generation 당 한 번 — 재진입은 (3.5) MigPhaseRolledBack 게이트가 먼저 걸러 여기 못 온다.
		metrics.RecordACPPApply(acpp.Spec.Vendor, acpp.Spec.EffectiveSharingMode(), "rollback")
		if rb != nil {
			// 되돌리기 오류를 버리면 실패가 상태에 남지 않는다 — 그러면 되돌아가지 않은 장치를
			// 되돌아간 것으로 처리하게 되고, 조정자의 보상 실패 종점도 영영 열리지 않는다.
			if rbErr := backend.Rollback(t, *rb); rbErr != nil {
				ts.Phase = npuv1alpha1.ACPPPhaseRollbackFailed
				setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionFalse,
					"RollbackFailed", rbErr.Error(), acpp.Generation)
			} else {
				setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionTrue,
					"RollbackSucceeded", "partition restored after apply failure", acpp.Generation)
			}
		}
		_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseRolledBack)
		_ = r.syncMigActiveLabel(t.Ctx, t.NodeName, false)
		setCond(&ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, "ApplyFailed", aerr.Error(), acpp.Generation)
		return ts, aerr
	}
	_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseApplied)

	// (7) device-plugin 재시작 → Verify → Ready.
	ts.Phase = npuv1alpha1.ACPPPhaseRestartingDevicePlugin
	if err := r.restartNvidiaDevicePlugin(t.Ctx, t.NodeName); err != nil {
		return ts, err
	}
	_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseVerifying)
	vr, verr := backend.Verify(t)
	if !verifySucceeded(vr, verr) {
		ts.Phase = npuv1alpha1.ACPPPhaseRollingBack
		// generation 당 한 번 — 재진입은 (3.5) MigPhaseRolledBack 게이트가 먼저 걸러 여기 못 온다.
		metrics.RecordACPPApply(acpp.Spec.Vendor, acpp.Spec.EffectiveSharingMode(), "rollback")
		if rb != nil {
			if rbErr := backend.Rollback(t, *rb); rbErr != nil {
				ts.Phase = npuv1alpha1.ACPPPhaseRollbackFailed
				setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionFalse,
					"RollbackFailed", rbErr.Error(), acpp.Generation)
			} else {
				setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionTrue,
					"RollbackSucceeded", "partition restored after verify failure", acpp.Generation)
			}
		}
		_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseRolledBack)
		_ = r.syncMigActiveLabel(t.Ctx, t.NodeName, false)
		setCond(&ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionFalse, "VerificationFailed", "mig verify failed", acpp.Generation)
		return ts, verr
	}
	_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseReady)
	setNvidiaReady(&ts, acpp)
	return ts, nil
}

// carryRollbackFailure 는 "같은 generation 의 rollback 은 terminal 이다" 종점에서, 직전 pass 가
// 남긴 되돌리기 결과(ACPPCondRolledBack)를 그대로 이어 붙인다. 이 종점은 매 pass 마다 새
// TargetStatus 를 만들므로, 여기서 조건을 다시 세우지 않으면 되돌리기 실패로 남긴 신호가 다음
// pass 부터 사라진다(첫 pass 에서만 보이는 되돌리기 실패는 표면화가 아니다). 되돌리기를 다시
// 실행하지는 않는다 — 기본 phase(Failed)에 직전 결과만 반영해 돌려준다.
func carryRollbackFailure(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string, ts *npuv1alpha1.TargetStatus) string {
	prev := findTargetStatus(acpp, node)
	if prev == nil {
		return npuv1alpha1.ACPPPhaseFailed
	}
	rb := apimeta.FindStatusCondition(prev.Conditions, npuv1alpha1.ACPPCondRolledBack)
	if rb == nil {
		return npuv1alpha1.ACPPPhaseFailed
	}
	setCond(ts, npuv1alpha1.ACPPCondRolledBack, rb.Status, rb.Reason, rb.Message, acpp.Generation)
	if rb.Status == metav1.ConditionFalse {
		return npuv1alpha1.ACPPPhaseRollbackFailed
	}
	return npuv1alpha1.ACPPPhaseFailed
}

// isApplyInFlightPhase 는 GI mutation 이 실제로 진행 중인 저널 phase 인지다(baseline 영속 이후).
// 변경시-전제(cordon·idle) skip 은 이 구간에만 허용한다 — 우리가 만든 Enabled MIG 상태로 복구를
// 차단하면 안 되기 때문이다. mode 전환 구간은 GI 이전이라 여기 포함되지 않는다(전제 재확인 필요).
func isApplyInFlightPhase(phase string) bool {
	switch phase {
	case npuv1alpha1.MigPhaseApplying, npuv1alpha1.MigPhaseApplied,
		npuv1alpha1.MigPhaseRestartingDP, npuv1alpha1.MigPhaseVerifying:
		return true
	}
	return false
}

// routeNvidiaNoDiff 는 geometry 일치(no-diff) 상태의 세 갈래를 라우팅한다(소유 술어).
// handled=false 면 호출자가 정상 apply 경로를 계속한다.
func (r *AcceleratorPartitionPolicyReconciler) routeNvidiaNoDiff(acpp *npuv1alpha1.AcceleratorPartitionPolicy, backend partition.Backend, nvb *nvidia.Backend, t partition.Target, ts npuv1alpha1.TargetStatus, rec npuv1alpha1.ApplyRecord, resolved []partition.ResolvedEntry, changed bool) (bool, npuv1alpha1.TargetStatus, error) {
	if changed {
		return false, ts, nil
	}
	owned := rec.OwnerUID == string(acpp.UID)
	if rec.MigPhase == npuv1alpha1.MigPhaseReady && owned && recMatches(rec, resolved, nvb.Targets()) {
		// 이미 적용 완료·소유·geometry 일치 → 재검증만(변경시-전제 불요구, cordon/Disabled 요구 안 함).
		out, err := r.verifyManagedNvidia(acpp, backend, nvb, t, ts, rec)
		return true, out, err
	}
	if owned && isApplyInFlightPhase(rec.MigPhase) {
		// crash-recovery continuation — 우리(this ACPP)가 만든 in-flight apply 인데 hardware mutation 은 이미
		// 끝나 geometry 가 일치(!diff.Changed)한다. baseline 재계산 시 drop 된 allocatable 로 BaselineInconsistent
		// (terminal deadlock)가 되므로, 영속 record 로 상태머신 tail 만 재개한다(resumeNvidiaRecovery).
		out, err := r.resumeNvidiaRecovery(acpp, backend, nvb, t, ts, rec)
		return true, out, err
	}
	if isMigModeEnablePhase(rec.MigPhase) {
		// GI 이전 저널(mode 전환 또는 apply 직전 cordon)이 남은 채 geometry 만 일치 — baseline 이
		// 없어 recovery tail 을 탈 수 없다.
		// 여기서 아래 ExistingMigConfiguration 으로 떨어뜨리면 자가치유 없는 terminal 실패가 되므로
		// (그 분류는 ts.Phase 를 ACPPPhaseFailed 로 못 박는다), 정상 경로로 내려보내 변경시-전제부터
		// 다시 밟게 한다(전부 transient·requeue).
		return false, ts, nil
	}
	if rec.MigPhase == npuv1alpha1.MigPhaseRolledBack {
		// 되돌리기가 실패해 GI 가 그대로 남으면 geometry 가 여전히 목표와 같아 여기로 들어온다.
		// 아래 ExistingMigConfiguration 으로 떨어뜨리면 runNvidiaTarget 의 "같은 generation 의
		// rollback 은 terminal 이다"(carryRollbackFailure)가 이 상태를 다시는 못 보고, 표면화한
		// RollbackFailed/RolledBack 조건이 다음 pass 부터 사라진다 — 그 종점이 이 상태를 소유하게
		// 내려보낸다(전이·mutation 없음, 표면화만 이어 붙인다).
		return false, ts, nil
	}
	// 외부 수동 설정과 geometry 만 우연히 일치 — 이 ACPP 가 만든 것이 아니므로 채택 거부.
	ts.Phase = npuv1alpha1.ACPPPhaseFailed
	setCond(&ts, npuv1alpha1.ACPPCondValidated, metav1.ConditionFalse, npuv1alpha1.ReasonExistingMigConfiguration, "geometry matches but not managed by this ACPP", acpp.Generation)
	return true, ts, nil
}

// observeNvidiaMIG 는 detector 가 못 채우는 MIG mode/geometry/lgip 를 operator observe Job 으로 fresh
// 관측해 backend 에 주입하고 Discover 를 재실행한다. 캐시 없이 매 reconcile 관측(MVP) —
// apply 직후 다음 reconcile 이 새 geometry 를 즉시 반영해 no-diff 수렴. 관측 Job 실패는 fail-closed
// (WaitingForDrain). 반환 stop=true 면 호출자는 ts(+err) 로 즉시 종료한다.
func (r *AcceleratorPartitionPolicyReconciler) observeNvidiaMIG(acpp *npuv1alpha1.AcceleratorPartitionPolicy, backend partition.Backend, nvb *nvidia.Backend, t partition.Target, ts *npuv1alpha1.TargetStatus) (bool, error) {
	// 모든 nvidia 장치 PCI 에서 관측한다 — detector 가 model 을 "generic" 으로만 보고해
	// migCapable(model) 로는 A30 을 못 가리므로, MIG-capability 는 관측(lgip profiles)으로 판정한다.
	// ts.Devices 는 첫 Discover 가 채운 전체 nvidia 장치 목록이다.
	var pcis []string
	for _, d := range ts.Devices {
		if d.PCIAddress != "" {
			pcis = append(pcis, d.PCIAddress)
		}
	}
	if len(pcis) == 0 {
		return false, nil
	}
	obs, oerr := r.nvidiaObserver().Observe(t.Ctx, t.NodeName, pcis)
	if oerr != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseWaitingForDrain
		setCond(ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse, npuv1alpha1.ReasonObservationUnavailable, "mig observation unavailable: "+oerr.Error(), acpp.Generation)
		return true, nil
	}
	obsMap := make(map[string]nvidia.Observation, len(obs))
	for _, o := range obs {
		obsMap[o.PCI] = o
	}
	nvb.WithObservations(obsMap)
	dres, derr := backend.Discover(t)
	if derr != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed
		setCond(ts, npuv1alpha1.ACPPCondCapabilitiesDiscovered, metav1.ConditionFalse, "DiscoveryFailed", derr.Error(), acpp.Generation)
		return true, derr
	}
	ts.DriverVersion, ts.Devices = dres.DriverVersion, dres.Devices
	ts.Operations, ts.Advertisement = dres.Operations, dres.Advertisement
	return false, nil
}

// observedGeometryOf 는 backend 가 들고 있는 관측 장치 목록을 PCI→geometry / PCI→오류 두 맵으로
// 나눈다. 오류가 있는 장치는 geometry 를 신뢰할 수 없으므로 오류 쪽에만 넣는다.
func observedGeometryOf(devs []nvidia.MigDevice) (map[string]string, map[string]string) {
	geom := make(map[string]string, len(devs))
	errs := make(map[string]string, len(devs))
	for _, d := range devs {
		if d.ObsError != "" {
			errs[d.PCI] = d.ObsError
			continue
		}
		geom[d.PCI] = d.Geometry
	}
	return geom, errs
}

// rngdObservedPCIs 는 RNGD 장치 관측의 "관측이 있었다" 만을 기록한다(PCI→빈 문자열). RNGD 는 nvidia
// 처럼 PCI 별 geometry 축이 없어 값은 비교 대상이 아니다(Expectation.Geometry 도 비어 있어 무시된다) —
// 그래도 관측 자체가 없으면 evalDeviceObservation 이 "관측 전무" 로 읽어 등급 없는 근거가 되므로,
// Discover 가 이미 찾은 장치 PCI 존재만 여기서 알려준다.
// 키는 PCI 가 있으면 PCI, 없으면 장치 ID 다 — Furiosa 는 detector 가 PCI 를 채우지 않아
// PCI 만 받으면 관측이 통째로 사라진다.
func rngdObservedPCIs(devs []npuv1alpha1.DeviceStatus) map[string]string {
	out := make(map[string]string, len(devs))
	for _, d := range devs {
		if key := deviceKey(d.PCIAddress, d.ID); key != "" {
			out[key] = ""
		}
	}
	return out
}

// deviceKey 는 장치 관측·보고를 맞대 놓을 식별자다(PCI 우선, 없으면 벤더가 준 ID).
func deviceKey(pci, id string) string {
	if pci != "" {
		return pci
	}
	return id
}

// gateReady 는 Ready 로 승격하려는 status 를 근거로 재확인한다. 근거가 어긋나면 Ready 를 취소하고
// 사유를 condition 으로 남긴다. 장치·DaemonSet 은 건드리지 않는다 — 감지와 기록만 한다.
//
// 게이트가 꺼져 있거나(Verification==nil), 이 pass 가 Ready 로 가지 않거나, 검증을 건너뛴
// pass 면 아무것도 하지 않는다.
func (r *AcceleratorPartitionPolicyReconciler) gateReady(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy,
	ts *npuv1alpha1.TargetStatus, t partition.Target, ec *evidenceCtx) error {
	if r.Verification == nil || !ec.Verified || ts.Phase != npuv1alpha1.ACPPPhaseReady {
		return nil
	}
	ev, err := r.Verification.Verify(ctx, verification.Request{
		NodeName:          t.NodeName,
		Vendor:            acpp.Spec.Vendor,
		SourcePolicy:      acpp.Name,
		Generation:        acpp.Generation,
		Expectation:       ec.Expectation,
		ObservedGeometry:  ec.ObservedGeometry,
		ObservationErrors: ec.ObservationErrors,
		TargetDevices:     ec.TargetDevices,
	})
	if err != nil {
		return err
	}
	if ev.Status.Level == "" {
		ts.Phase = npuv1alpha1.ACPPPhaseVerifyingAllocatable
		setCond(ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionFalse,
			npuv1alpha1.ReasonEvidenceDisagreement, ev.Status.Reason, acpp.Generation)
	}
	return nil
}

// monitorDrift 는 이미 수렴한 target 의 광고가 검증 시점 기준선을 유지하는지 확인하고 status 만
// 갱신한다. 장치·DaemonSet·ConfigMap 을 건드리지 않는다.
//
// 판단 근거는 evidence 가 저장한 광고량이다 — 여기서 기대값을 다시 계산하면 "검증 뒤에 무너졌다"
// 가 아니라 "지금 계산한 값과 다르다" 를 말하게 되고, 그 둘은 다른 주장이다.
func (r *AcceleratorPartitionPolicyReconciler) monitorDrift(t partition.Target, acpp *npuv1alpha1.AcceleratorPartitionPolicy,
	ts npuv1alpha1.TargetStatus) (npuv1alpha1.TargetStatus, error) {
	if r.Verification == nil || t.NodeName == "" {
		return ts, nil
	}
	// DRA 로 광고하는 노드는 자원을 ResourceSlice 로 내보내 allocatable 이 0 이다. allocatable
	// 드리프트로 보면 항상 "장치 0" 으로 오독한다. draOwnedNode 게이트는 runTarget 의 적용 경로
	// (:270)에만 있고 이 재검증 경로는 그 전에 return 하므로, 여기서 같은 판정을 다시 건다.
	if owned, oerr := r.draOwnedNode(t.Ctx, t.NodeName, acpp.Spec.Vendor); oerr == nil && owned {
		return ts, nil
	}
	ev, err := r.Verification.Load(t.Ctx, t.NodeName)
	if err != nil {
		return ts, err
	}
	var node corev1.Node
	if err := r.Get(t.Ctx, types.NamespacedName{Name: t.NodeName}, &node); err != nil {
		return ts, client.IgnoreNotFound(err)
	}
	var ndr npuv1alpha1.NodeDeviceReport
	if err := r.Get(t.Ctx, types.NamespacedName{Name: t.NodeName}, &ndr); err != nil && !apierrors.IsNotFound(err) {
		return ts, err
	}

	policy := verification.DefaultPolicy()
	var policies npuv1alpha1.AcceleratorVerificationPolicyList
	if err := r.List(t.Ctx, &policies); err == nil {
		policy = verification.Resolve(policies.Items, node.Labels)
	}
	cur := verification.Compute(&node, &ndr, acpp.Spec.Vendor, acpp.Generation)
	freshVerdict, _ := verification.CheckFreshness(ev, time.Now(), cur, policy.InvalidateOn)

	rolling := false
	if acpp.Spec.Vendor == vendorNvidia {
		if v, rerr := nvidia.DevicePluginRolling(t.Ctx, r.Client, t.NodeName); rerr == nil {
			rolling = v
		} else {
			// 롤아웃 여부를 모르면 억제한다 — 모르는 상태에서 장애를 선언하지 않는다.
			rolling = true
		}
	}
	rec := getApplyRecord(acpp, t.NodeName)
	in := verification.DriftInput{
		Actual:            allocatableSnapshot(&node),
		RolloutInProgress: rolling,
		NodeReady:         isNodeReadyForDrift(&node),
		ApplyInFlight:     isApplyInFlightPhase(rec.MigPhase) || isMigModeEnablePhase(rec.MigPhase),
		Terminating:       !acpp.DeletionTimestamp.IsZero() || !node.DeletionTimestamp.IsZero(),
		EvidenceFresh:     verification.IsFresh(freshVerdict),
		FirstSuspectedAt:  driftFirstSeen(&ts),
		AlreadyConfirmed:  driftConfirmed(&ts),
		Now:               metav1.Now(),
		Grace:             policy.DriftGracePeriod,
	}
	if ev != nil {
		in.Expected = ev.Status.AdvertisedResources
	}

	verdict, reason := verification.EvaluateDrift(in)
	applyDriftVerdict(&ts, acpp, verdict, reason)
	return ts, nil
}

// driftConfirmed 는 직전 pass 가 이미 광고 붕괴를 확정했는지다. 확정 조건은 status=True 이면서
// 사유가 광고 붕괴인 것 — 다른 이유의 Degraded(예: 하드웨어 실패)를 광고 확정으로 읽지 않는다.
func driftConfirmed(ts *npuv1alpha1.TargetStatus) bool {
	c := apimeta.FindStatusCondition(ts.Conditions, npuv1alpha1.ACPPCondDegraded)
	return c != nil && c.Status == metav1.ConditionTrue && c.Reason == npuv1alpha1.ReasonAdvertisementDrift
}

// driftFirstSeen 은 의심을 처음 본 시각이다 — Degraded condition 이 Unknown 인 동안의
// LastTransitionTime 이 그 값이다(별도 status 필드를 만들지 않는다).
func driftFirstSeen(ts *npuv1alpha1.TargetStatus) *metav1.Time {
	c := apimeta.FindStatusCondition(ts.Conditions, npuv1alpha1.ACPPCondDegraded)
	if c == nil || c.Status != metav1.ConditionUnknown {
		return nil
	}
	t := c.LastTransitionTime
	return &t
}

// applyDriftVerdict 는 판정을 condition 과 phase 로 옮긴다. phase 는 Ready 와 Degraded 사이만
// 오간다 — 다른 phase 로 내리면 재적용 경로가 열린다.
func applyDriftVerdict(ts *npuv1alpha1.TargetStatus, acpp *npuv1alpha1.AcceleratorPartitionPolicy,
	verdict verification.DriftVerdict, reason string) {
	switch verdict {
	case verification.DriftConfirmed:
		ts.Phase = npuv1alpha1.ACPPPhaseDegraded
		setCond(ts, npuv1alpha1.ACPPCondDegraded, metav1.ConditionTrue,
			npuv1alpha1.ReasonAdvertisementDrift, reason, acpp.Generation)
	case verification.DriftSuspected:
		// 아직 확정이 아니다 — phase 는 Ready 로 둔다. 의심만으로 내리면 롤아웃마다 흔들린다.
		ts.Phase = npuv1alpha1.ACPPPhaseReady
		setCond(ts, npuv1alpha1.ACPPCondDegraded, metav1.ConditionUnknown,
			npuv1alpha1.ReasonAdvertisementDriftSuspected, reason, acpp.Generation)
	case verification.DriftSuppressed:
		ts.Phase = npuv1alpha1.ACPPPhaseReady
		setCond(ts, npuv1alpha1.ACPPCondDegraded, metav1.ConditionFalse,
			npuv1alpha1.ReasonAdvertisementCheckSuppressed, reason, acpp.Generation)
	default:
		ts.Phase = npuv1alpha1.ACPPPhaseReady
		setCond(ts, npuv1alpha1.ACPPCondDegraded, metav1.ConditionFalse,
			npuv1alpha1.ReasonAdvertisementConsistent, "advertisement matches the verified baseline", acpp.Generation)
	}
}

// allocatableSnapshot 은 노드 allocatable 을 판정 입력 형식으로 옮긴다.
func allocatableSnapshot(node *corev1.Node) map[string]int32 {
	out := make(map[string]int32, len(node.Status.Allocatable))
	for k, q := range node.Status.Allocatable {
		out[string(k)] = int32(q.Value())
	}
	return out
}

// isNodeReadyForDrift 는 노드가 Ready 인지다. Ready 가 아니면 광고가 비어도 그것은 노드 문제이지
// 이 정책의 광고 붕괴가 아니다.
func isNodeReadyForDrift(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	// Ready condition 이 아직 없는 노드(envtest 시드 직후)는 판단 대상으로 본다 — 여기서 false 를
	// 주면 모든 감시가 억제로 새 나가 기능이 죽는다.
	return true
}

// verifyManagedNvidia 는 소유·Ready·geometry 일치(managed no-diff)의 재검증만 수행한다. 실 에러는 전파,
// non-convergence(에러 아님)는 hard-fail 대신 transient(VerifyingAllocatable)로 두고 requeue 해 self-heal 한다.
func (r *AcceleratorPartitionPolicyReconciler) verifyManagedNvidia(acpp *npuv1alpha1.AcceleratorPartitionPolicy, backend partition.Backend, nvb *nvidia.Backend, t partition.Target, ts npuv1alpha1.TargetStatus, rec npuv1alpha1.ApplyRecord) (npuv1alpha1.TargetStatus, error) {
	nvb.WithExpected(expectedAllocatable(rec))
	vr, verr := backend.Verify(t)
	if verr != nil {
		ts.Phase = npuv1alpha1.ACPPPhaseFailed // 실 에러는 rate-limited retry 로 전파.
		setCond(&ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionFalse, "VerificationFailed", "managed mig verify failed", acpp.Generation)
		return ts, verr
	}
	if !verifySucceeded(vr, nil) {
		// non-convergence — plugin 이 잠시 재시작 중일 수 있다. Ready 였던 노드가 mid-restart 로 stuck 되지 않게 재시도.
		ts.Phase = npuv1alpha1.ACPPPhaseVerifyingAllocatable
		setCond(&ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionFalse, "VerificationPending", "managed mig allocatable not yet converged; retrying", acpp.Generation)
		return ts, nil
	}
	setNvidiaReady(&ts, acpp)
	return ts, nil
}

// resumeNvidiaRecovery 는 우리(this ACPP)의 in-flight apply 가 hardware mutation 이후 크래시했을 때
// (geometry 이미 일치) baseline 재계산·재-Apply 없이 영속 record 로 상태머신 tail(DP 재시작→Verify)만
// 재개한다. computeBaseline 을 우회해 drop 된 allocatable 로 인한 BaselineInconsistent 데드락을 막는다.
func (r *AcceleratorPartitionPolicyReconciler) resumeNvidiaRecovery(acpp *npuv1alpha1.AcceleratorPartitionPolicy, backend partition.Backend, nvb *nvidia.Backend, t partition.Target, ts npuv1alpha1.TargetStatus, rec npuv1alpha1.ApplyRecord) (npuv1alpha1.TargetStatus, error) {
	nvb.WithExpected(expectedAllocatable(rec))
	ts.Phase = npuv1alpha1.ACPPPhaseRestartingDevicePlugin
	if rec.MigPhase == npuv1alpha1.MigPhaseApplied {
		// Applied 에서 크래시 → DP 재시작이 아직 안 됐을 수 있다(멱등: 이미 됐어도 안전).
		if err := r.restartNvidiaDevicePlugin(t.Ctx, t.NodeName); err != nil {
			return ts, err
		}
	}
	_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseVerifying)
	vr, verr := backend.Verify(t)
	if !verifySucceeded(vr, verr) {
		ts.Phase = npuv1alpha1.ACPPPhaseRollingBack
		// Rollback 은 target PCI 로 disable 한다(state 무시).
		if rbErr := backend.Rollback(t, partition.RollbackState{}); rbErr != nil {
			ts.Phase = npuv1alpha1.ACPPPhaseRollbackFailed
			setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionFalse,
				"RollbackFailed", rbErr.Error(), acpp.Generation)
		} else {
			setCond(&ts, npuv1alpha1.ACPPCondRolledBack, metav1.ConditionTrue,
				"RollbackSucceeded", "partition restored after recovery verify failure", acpp.Generation)
		}
		_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseRolledBack)
		setCond(&ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionFalse, "VerificationFailed", "recovery verify failed", acpp.Generation)
		return ts, verr
	}
	_ = r.setRecordPhase(t.Ctx, acpp, t.NodeName, npuv1alpha1.MigPhaseReady)
	setNvidiaReady(&ts, acpp)
	return ts, nil
}

// resolveNvidia 는 nvidia layout 을 resolve 한다 — profile 은 그대로, BackendPolicy 는 없음(ProfileNamed).
func resolveNvidia(ls []npuv1alpha1.PartitionLayout) ([]partition.ResolvedEntry, []npuv1alpha1.ResolvedLayoutEntry) {
	re := make([]partition.ResolvedEntry, 0, len(ls))
	rs := make([]npuv1alpha1.ResolvedLayoutEntry, 0, len(ls))
	for _, l := range ls {
		re = append(re, partition.ResolvedEntry{Profile: l.Profile, ExpectedCountPerDevice: l.CountPerDevice})
		rs = append(rs, npuv1alpha1.ResolvedLayoutEntry{Profile: l.Profile, ExpectedCountPerDevice: l.CountPerDevice})
	}
	return re, rs
}

// nvidiaAlwaysBlock 은 항상-전제(PCI·관측·driver) 위반을 (reason,msg,blocked) 로 매핑한다.
func nvidiaAlwaysBlock(a nvidia.AlwaysResult) (string, string, bool) {
	switch {
	case a.ObservationFailed:
		return npuv1alpha1.ReasonObservationUnavailable, "mig observation unavailable", true
	case a.PCIMissing:
		return npuv1alpha1.ReasonUnstableGpuIdentity, "gpu pci address missing", true
	case !a.DriverReady:
		return npuv1alpha1.ReasonDriverNotReady, "nvidia driver not ready", true
	}
	return "", "", false
}

// nvidiaHWBlock 은 hardware 변경시-전제(cordon·MIG-safe·idle) 위반을 (reason,msg,blocked) 로 매핑한다.
// 순서 주의: 장치 상태(mode·관리 밖 GI)를 cordon 보다 먼저 본다. 정책은 그 두 경우에 노드를 cordon
// 하지 않기로 되어 있으므로(진행 불가한 노드를 잠글 이유가 없다), cordon 을 먼저 보고하면 아무도 하지
// 않을 조치를 지시하는 거짓 이유가 된다. cordon 은 정책이 스스로 취득하므로 !NodeCordoned 는
// "cordon 이 먹지 않았다" 는 좁은 뜻으로만 남는다.
func nvidiaHWBlock(h nvidia.HWResult) (string, string, bool) {
	switch {
	case h.MigModeNotEnabled:
		// MIG mode 는 외부 사전조건. 미enable 이면 차단·requeue(노드 enable 후 자가치유).
		return npuv1alpha1.ReasonMigModeNotEnabled, "MIG mode must be pre-enabled on the target GPU", true
	case h.MigUnsafe:
		return npuv1alpha1.ReasonExistingMigConfiguration, "existing/unsafe mig state present", true
	case !h.NodeCordoned:
		return npuv1alpha1.ReasonNodeNotCordoned, "node not cordoned", true
	case h.GPUBusy:
		return npuv1alpha1.ReasonQuiesceRequired, "gpu workloads still running", true
	}
	return "", "", false
}

// ensureNodeOwnerLockUID 는 노드 annotation(migOwnerAnnotation)=uid 를 멱등 취득한다.
// 이미 다른 uid 로 잠겨 있으면 (false,nil), 취득/재확인 성공은 (true,nil). patch 충돌은 재시도한다.
func (r *AcceleratorPartitionPolicyReconciler) ensureNodeOwnerLockUID(ctx context.Context, node, uid string) (bool, error) {
	for i := 0; i < 3; i++ {
		var n corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: node}, &n); err != nil {
			return false, err
		}
		switch cur := n.Annotations[migOwnerAnnotation]; {
		case cur == uid:
			return true, nil
		case cur != "":
			return false, nil
		}
		// annotation 하나만 merge-patch — 동시 Node writer(라벨/컨디션 등)를 clobber 하지 않는다.
		base := n.DeepCopy()
		if n.Annotations == nil {
			n.Annotations = map[string]string{}
		}
		n.Annotations[migOwnerAnnotation] = uid
		if err := r.Patch(ctx, &n, client.MergeFrom(base)); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("owner lock: conflict retries exhausted for node %s", node)
}

// restartNvidiaDevicePlugin 은 대상 노드의 nvidia-device-plugin pod 를 삭제해 재-스캔을 유도한다.
func (r *AcceleratorPartitionPolicyReconciler) restartNvidiaDevicePlugin(ctx context.Context, node string) error {
	var pods corev1.PodList
	if err := r.List(ctx, &pods,
		client.MatchingLabels{nvidiaDevicePluginVendorLabel: "nvidia"},
		client.MatchingFields{"spec.nodeName": node}); err != nil {
		return err
	}
	for i := range pods.Items {
		if err := r.Delete(ctx, &pods.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// computeBaseline 은 apply 전 노드 상태에서 baseline·기대 allocatable 을 계산해 ApplyRecord 를
// 만든다. ExpectedFullGPUCount 는 apply 후 mixed device-plugin 이 광고할 온전한 GPU
// 수다 — 물리 GPU 수에서 이 정책이 조각낼 대상 수를 뺀 값이다. apply 직전 allocatable
// (BaselineGPUCount)을 그대로 기대값으로 쓰면 안 된다: mode 가 Enabled 여도 GI 가 없는 동안은
// flat device-plugin 이 그 GPU 를 온전한 GPU 로 계속 광고해 baseline 이 물리 수와 같아지고, 조각낸
// 뒤에는 mixed 가 그 GPU 를 빼고 광고해 기대값이 영원히 충족되지 않아 GI 생성·파괴만 반복한다.
func (r *AcceleratorPartitionPolicyReconciler) computeBaseline(ctx context.Context, node string, acpp *npuv1alpha1.AcceleratorPartitionPolicy, resolved []partition.ResolvedEntry, targets []nvidia.MigDevice) (npuv1alpha1.ApplyRecord, error) {
	var rec npuv1alpha1.ApplyRecord
	if len(resolved) != 1 {
		return rec, fmt.Errorf("baseline: expected single resolved layout, got %d", len(resolved))
	}
	var n corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: node}, &n); err != nil {
		return rec, err
	}
	q := n.Status.Allocatable[corev1.ResourceName(nvidiaGPUResource)]
	baseline := int32(q.Value())
	nt := int32(len(targets))
	var ndr npuv1alpha1.NodeDeviceReport
	if err := r.Get(ctx, client.ObjectKey{Name: node}, &ndr); err != nil {
		return rec, fmt.Errorf("baseline: get NodeDeviceReport %q: %w", node, err)
	}
	physical := nvidiaPhysicalGPUs(&ndr)
	if physical < nt {
		return rec, fmt.Errorf("baseline: node %s reports %d nvidia GPUs but %d are partition targets", node, physical, nt)
	}
	r0 := resolved[0]
	// 기존 저널을 승계한 뒤 baseline 항목만 덮어쓴다 — 통째로 새 record 를 만들면 baseline 과
	// 무관한 필드(cordonedByPolicy, sharingMode/Replicas)가 조용히 사라진다. cordon 소유권을
	// 잃으면 mode enable 로 잠근 노드를 Ready 이후에도 삭제 시에도 되돌리지 못한다.
	rec = getApplyRecord(acpp, node)
	rec.NodeName = node
	rec.GPUPCIs = targetPCIs(targets)
	rec.OwnerUID = string(acpp.UID)
	rec.BaselineGPUCount = baseline
	rec.ExpectedMigCount = r0.ExpectedCountPerDevice * nt
	rec.ExpectedFullGPUCount = physical - nt
	rec.Profile = r0.Profile
	rec.Count = r0.ExpectedCountPerDevice
	rec.Generation = acpp.Generation
	return rec, nil
}

// nvidiaPhysicalGPUs 는 NodeDeviceReport 가 보고한 그 노드의 nvidia 물리 GPU 수다. allocatable 이
// 아니라 인벤토리를 쓰는 이유는, allocatable 이 "지금 어느 device-plugin 이 떠 있는가" 에 따라
// 달라져 apply 후 기대값의 기준이 될 수 없기 때문이다.
func nvidiaPhysicalGPUs(ndr *npuv1alpha1.NodeDeviceReport) int32 {
	var n int32
	for _, d := range ndr.Status.Devices {
		if !strings.EqualFold(d.Vendor, vendorNvidia) {
			continue
		}
		c := d.Count
		if c <= 0 {
			c = 1
		}
		n += c
	}
	return n
}

// targetPCIs 는 target 장치들의 PCI 목록이다 — 저널의 GPUPCIs 는 소유 주장 시점(journalQuiescing)과
// baseline 시점(computeBaseline)에 각각 쓰이므로, 같은 유도식을 공유해야 두 write 가 같은 값을 낸다.
func targetPCIs(targets []nvidia.MigDevice) []string {
	pcis := make([]string, 0, len(targets))
	for _, d := range targets {
		pcis = append(pcis, d.PCI)
	}
	return pcis
}

// expectedAllocatable 은 Verify 가 검증할 기대 allocatable 맵(mig + full-gpu)을 만든다.
func expectedAllocatable(rec npuv1alpha1.ApplyRecord) map[string]int32 {
	return map[string]int32{
		"nvidia.com/mig-" + rec.Profile: rec.ExpectedMigCount,
		nvidiaGPUResource:               rec.ExpectedFullGPUCount,
	}
}

// recMatches 는 저장된 ApplyRecord 가 요청 resolved + 현 targets(PCI 집합)와 일치하는지(소유 no-diff 술어)다.
func recMatches(rec npuv1alpha1.ApplyRecord, resolved []partition.ResolvedEntry, targets []nvidia.MigDevice) bool {
	if len(resolved) != 1 {
		return false
	}
	if rec.Profile != resolved[0].Profile || rec.Count != resolved[0].ExpectedCountPerDevice {
		return false
	}
	if len(rec.GPUPCIs) != len(targets) {
		return false
	}
	have := make(map[string]bool, len(rec.GPUPCIs))
	for _, p := range rec.GPUPCIs {
		have[p] = true
	}
	for _, d := range targets {
		if !have[d.PCI] {
			return false
		}
	}
	return true
}

// getApplyRecord 는 node 의 ApplyRecord 를 반환한다(없으면 zero value).
func getApplyRecord(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string) npuv1alpha1.ApplyRecord {
	for _, r := range acpp.Status.ApplyRecords {
		if r.NodeName == node {
			return r
		}
	}
	return npuv1alpha1.ApplyRecord{}
}

// patchApplyRecord 는 rec 를 status.ApplyRecords 에 upsert 하고 Status().Patch 로 영속한다(durable journal).
func (r *AcceleratorPartitionPolicyReconciler) patchApplyRecord(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy, rec npuv1alpha1.ApplyRecord) error {
	base := acpp.DeepCopy()
	found := false
	for i := range acpp.Status.ApplyRecords {
		if acpp.Status.ApplyRecords[i].NodeName == rec.NodeName {
			acpp.Status.ApplyRecords[i] = rec
			found = true
			break
		}
	}
	if !found {
		acpp.Status.ApplyRecords = append(acpp.Status.ApplyRecords, rec)
	}
	return r.Status().Patch(ctx, acpp, client.MergeFrom(base))
}

// setRecordPhase 는 node 의 ApplyRecord.MigPhase 만 갱신해 영속한다(상태 진행 저널링).
func (r *AcceleratorPartitionPolicyReconciler) setRecordPhase(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy, node, phase string) error {
	rec := getApplyRecord(acpp, node)
	rec.NodeName = node
	rec.MigPhase = phase
	return r.patchApplyRecord(ctx, acpp, rec)
}

// setNvidiaReady 는 nvidia MIG apply 성공 종점(Ready + Applied/Verified True)을 찍는다.
func setNvidiaReady(ts *npuv1alpha1.TargetStatus, acpp *npuv1alpha1.AcceleratorPartitionPolicy) {
	ts.Phase = npuv1alpha1.ACPPPhaseReady
	setCond(ts, npuv1alpha1.ACPPCondApplied, metav1.ConditionTrue, "DevicePluginConfigured", "mig geometry applied", acpp.Generation)
	setCond(ts, npuv1alpha1.ACPPCondVerified, metav1.ConditionTrue, "AllocatableConverged", "mig allocatable converged", acpp.Generation)
}

// ensureOwnerLock 은 unified DS 의 owner-lock annotation 이 이 acpp 소유로 찍혀 있음을 보장하는 멱등 헬퍼다
// (두 writer 조정). DS 부재(NotFound)는 조용히 건너뛴다(관리할 lock 이 없음). 그 외 에러는
// reconcile 자체를 실패시키지 않고 로그만 남긴다 — status 는 이미 Ready 로 확정된 뒤이므로.
func (r *AcceleratorPartitionPolicyReconciler) ensureOwnerLock(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy) {
	logger := logf.FromContext(ctx)
	var ds appsv1.DaemonSet
	if err := r.Get(ctx, client.ObjectKey{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "ensureOwnerLock: get DS failed")
		}
		return
	}
	if ds.Annotations[rngd.PartitionOwnerAnnotation] == acpp.Name {
		return
	}
	if ds.Annotations == nil {
		ds.Annotations = map[string]string{}
	}
	ds.Annotations[rngd.PartitionOwnerAnnotation] = acpp.Name
	if err := r.Update(ctx, &ds); err != nil {
		logger.Error(err, "ensureOwnerLock: update DS failed")
	}
}

// verifySucceeded 는 backend.Verify 결과를 성공/실패로 판별한다(양 분기 공통 규칙).
// error → 실패. nil result(verifier 미주입 skip 신호) → 성공(검증 불요).
// non-nil result → AllocatableConverged 또는 TestPodAllocated 중 하나라도 true 면 성공, 모두 false 면 실패.
func verifySucceeded(vr *partition.VerifyResult, err error) bool {
	if err != nil {
		return false
	}
	if vr == nil {
		return true
	}
	return vr.AllocatableConverged || vr.TestPodAllocated
}

// resolveConflict 는 이 acpp 가 target(DS) 에 적용해도 되는지 판정한다.
// 순서: 1) vendor 불일치(NDR 실제 벤더≠spec.vendor) 2) scope 부분집합(selector 가 DS 관리 노드 전체를 못 덮음)
// 3) 같은 vendor 로 같은(MVP-1 단일) DS 를 노리는 ACPP 중 결정적 승자(creationTimestamp 최소 → 동률 시 name 사전순)인지.
// 비-승자/불일치는 (false, reason) 을 반환하고 runTarget 은 호출되지 않는다.
func (r *AcceleratorPartitionPolicyReconciler) resolveConflict(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy) (bool, string) {
	nodeName := firstNodeName(ctx, r.Client, acpp.Spec.NodeSelector)
	if nodeName != "" {
		var ndr npuv1alpha1.NodeDeviceReport
		if err := r.Get(ctx, client.ObjectKey{Name: nodeName}, &ndr); err == nil && len(ndr.Status.Devices) > 0 {
			// 멀티벤더 노드(예: <nvidia-node> = nvidia A30/A2 + Warboy) 지원: 첫 장치가 아니라
			// spec.vendor 장치가 하나라도 존재하는지로 판정한다(Devices[0] 단독 검사는 오분류).
			found := false
			for _, d := range ndr.Status.Devices {
				if d.Vendor == acpp.Spec.Vendor {
					found = true
					break
				}
			}
			if !found {
				return false, npuv1alpha1.ReasonVendorMismatch
			}
		}
	}

	// DS 기반 scope/충돌 판정은 RNGD(furiosa) 전용 — DS 에 apply 하는 backend 만 해당.
	// NVIDIA 등 discovery-only 벤더는 대상 DS 가 없어 scope·충돌 개념이 없다 → 통과시켜
	// runTarget 이 Unsupported 로 처리하게 한다(동작 보존, 혼합 벤더 오분류 방지).
	if acpp.Spec.Vendor != vendorFuriosa {
		return true, ""
	}

	if dsNodes, err := r.dsManagedNodes(ctx); err == nil && !selectorCoversAll(ctx, r.Client, acpp.Spec.NodeSelector, dsNodes) {
		return false, npuv1alpha1.ReasonBackendScopeMismatch
	}

	var all npuv1alpha1.AcceleratorPartitionPolicyList
	if err := r.List(ctx, &all); err != nil {
		return true, "" // 목록 실패 시 보수적으로 진행
	}
	var contenders []npuv1alpha1.AcceleratorPartitionPolicy
	for _, o := range all.Items {
		if o.Spec.Vendor == acpp.Spec.Vendor && o.DeletionTimestamp.IsZero() {
			contenders = append(contenders, o)
		}
	}
	sort.Slice(contenders, func(i, j int) bool {
		a, b := contenders[i], contenders[j]
		if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
			return a.CreationTimestamp.Before(&b.CreationTimestamp)
		}
		return a.Name < b.Name
	})
	if len(contenders) > 0 && contenders[0].Name != acpp.Name {
		return false, npuv1alpha1.ReasonTargetConflict
	}
	return true, ""
}

// rngdPresentNodeLabel 은 node-manager 가 RNGD 하드웨어 보유 노드에 부여하는 자립 라벨이다.
var rngdPresentNodeLabel = vendorPresentLabel(vendorRngd)

// labelValueTrue 는 노드 라벨의 참 값 "true"(goconst 회피용 공용 상수).
const labelValueTrue = "true"

// dsManagedNodes 는 RNGD 파티션 scope 판정 대상 노드명 목록을 반환한다. unified DS 는 Warboy·RNGD
// 를 한 DaemonSet 으로 관리하므로 pod nodeSelector(furiosa-family)만으로는 Warboy 노드까지
// 포함된다. 그러나 RNGD_PARTITION_POLICY 는 RNGD 노드에만 영향(Warboy 바이너리는 무시)하므로,
// scope 는 DS 관리 노드 중 **RNGD 노드(kcloud.ai/rngd.present=true)**로 한정한다(mixed unified DS
// 오분류 방지). DS 에 nodeSelector 가 없으면 scope 제약을 걸지 않는다(빈 슬라이스 →
// selectorCoversAll 무조건 통과).
func (r *AcceleratorPartitionPolicyReconciler) dsManagedNodes(ctx context.Context) ([]string, error) {
	var ds appsv1.DaemonSet
	if err := r.Get(ctx, client.ObjectKey{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds); err != nil {
		return nil, err
	}
	if len(ds.Spec.Template.Spec.NodeSelector) == 0 {
		return nil, nil
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(ds.Spec.Template.Spec.NodeSelector)}); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		if n.Labels[rngdPresentNodeLabel] == labelValueTrue {
			names = append(names, n.Name)
		}
	}
	return names, nil
}

// selectorCoversAll 은 sel 매칭 노드 집합이 dsNodes 전체를 덮는지(부분집합이 아닌지) 확인한다.
func selectorCoversAll(ctx context.Context, c client.Client, sel map[string]string, dsNodes []string) bool {
	if len(dsNodes) == 0 {
		return true
	}
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(sel)}); err != nil {
		return false
	}
	matched := make(map[string]bool, len(nodes.Items))
	for _, n := range nodes.Items {
		matched[n.Name] = true
	}
	for _, name := range dsNodes {
		if !matched[name] {
			return false
		}
	}
	return true
}

// aggregatePhase 는 전 target 최저 수렴 phase 를 반환한다 — 첫 비-Ready target 이 우선.
func aggregatePhase(targets []npuv1alpha1.TargetStatus) string {
	if len(targets) == 0 {
		return npuv1alpha1.ACPPPhasePending
	}
	for _, t := range targets {
		if t.Phase != npuv1alpha1.ACPPPhaseReady {
			return t.Phase
		}
	}
	return npuv1alpha1.ACPPPhaseReady
}

func (r *AcceleratorPartitionPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// nvidia.CheckHardwareChange / restartNvidiaDevicePlugin 이 pod 를 노드명으로 필터하므로 인덱스 등록.
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
		return []string{o.(*corev1.Pod).Spec.NodeName}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.AcceleratorPartitionPolicy{}).
		Owns(&appsv1.DaemonSet{}).
		Complete(r)
}

func toPartitionLayouts(ls []npuv1alpha1.PartitionLayout) []partition.Layout {
	out := make([]partition.Layout, len(ls))
	for i, l := range ls {
		out[i] = partition.Layout{Profile: l.Profile, CountPerDevice: l.CountPerDevice}
	}
	return out
}

// resolveRngd 는 요청 layout 을 backendPolicy 로 resolve 한다(mapping 테이블).
func resolveRngd(ls []npuv1alpha1.PartitionLayout) ([]partition.ResolvedEntry, []npuv1alpha1.ResolvedLayoutEntry) {
	re := make([]partition.ResolvedEntry, 0, len(ls))
	rs := make([]npuv1alpha1.ResolvedLayoutEntry, 0, len(ls))
	for _, l := range ls {
		p, ok := rngd.ResolveProfile(l.Profile)
		if !ok {
			continue
		}
		re = append(re, partition.ResolvedEntry{Profile: p.Profile, BackendPolicy: p.BackendPolicy, ExpectedCountPerDevice: p.CountPerDevice})
		rs = append(rs, npuv1alpha1.ResolvedLayoutEntry{Profile: p.Profile, BackendPolicy: p.BackendPolicy, ExpectedCountPerDevice: p.CountPerDevice})
	}
	return re, rs
}

func setCond(ts *npuv1alpha1.TargetStatus, ctype string, status metav1.ConditionStatus, reason, msg string, gen int64) {
	apimeta.SetStatusCondition(&ts.Conditions, metav1.Condition{
		Type: ctype, Status: status, Reason: reason, Message: msg, ObservedGeneration: gen,
	})
}

// writeTargetStatus 는 target status 1개(MVP-1 단일 target)를 영속하는 유일한 지점이다. 기록 전에
// 반드시 LastTransitionTime 을 직전 status 에서 승계한다 — runTarget 이 매 reconcile 마다 빈
// Conditions 로 TargetStatus 를 새로 만들면 apimeta.SetStatusCondition 이 모든 condition 을
// "전이"로 보아 아무것도 안 바뀐 reconcile 도 매번 실제 write 를 낸다. 그 write 의 watch 이벤트가
// 재큐잉하고 낡은 캐시로 resourceVersion conflict 가 나면 정책이 영원히 같은 자리를 돈다. 바이트
// 단위로 같은 status 를 만들어 apiserver 의 write 자체를 no-op 시키는 것이 유일한 차단점이다.
func (r *AcceleratorPartitionPolicyReconciler) writeTargetStatus(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy, ts npuv1alpha1.TargetStatus) error {
	carryCondTimes(findTargetStatus(acpp, ts.NodeName), &ts)
	acpp.Status.Targets = []npuv1alpha1.TargetStatus{ts}
	acpp.Status.ObservedGeneration = acpp.Generation
	acpp.Status.Phase = aggregatePhase(acpp.Status.Targets)
	return r.Status().Update(ctx, acpp)
}

// findTargetStatus 는 노드의 직전 target status 를 반환한다(없으면 nil).
func findTargetStatus(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string) *npuv1alpha1.TargetStatus {
	for i := range acpp.Status.Targets {
		if acpp.Status.Targets[i].NodeName == node {
			return &acpp.Status.Targets[i]
		}
	}
	return nil
}

// carryCondTimes 는 status 가 변하지 않은 condition 의 LastTransitionTime 을 직전 값으로 되돌린다
// (= SetStatusCondition 이 Conditions 를 보존했을 때의 의미). status 가 바뀐 condition 은 실제 전이라
// 새 시각을 유지한다. prev.Conditions 를 통째로 seeding 하지 않는 이유는, 그러면 이번 pass 가 도달하지
// 못한 단계의 condition(예: 성공 후 실패한 reconcile 의 Verified=True, 재적용 성공 후의 RolledBack=True)이
// 지워지지 않고 남아 status 가 거짓을 말하기 때문이다 — 고쳐야 할 것은 타임스탬프뿐이다.
func carryCondTimes(prev *npuv1alpha1.TargetStatus, ts *npuv1alpha1.TargetStatus) {
	if prev == nil {
		return
	}
	for i := range ts.Conditions {
		old := apimeta.FindStatusCondition(prev.Conditions, ts.Conditions[i].Type)
		if old != nil && old.Status == ts.Conditions[i].Status {
			ts.Conditions[i].LastTransitionTime = old.LastTransitionTime
		}
	}
}

func (r *AcceleratorPartitionPolicyReconciler) fail(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy, msg string) error {
	acpp.Status.Phase = npuv1alpha1.ACPPPhaseFailed
	_ = msg // 실패 사유는 target 부재로 조건에 기록할 곳이 없음(vendor 판정 이전) — phase 로만 표시.
	return r.Status().Update(ctx, acpp)
}

// firstNodeName 은 selector 매칭 첫 노드명을 반환한다(MVP-1 단일 target; 정렬로 결정적).
func firstNodeName(ctx context.Context, c client.Client, sel map[string]string) string {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(sel)}); err != nil {
		return ""
	}
	names := make([]string, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// handleDeletion 은 deletionPolicy Retain(MVP-1 유일)을 구현한다: owner lock(annotation) 해제 +
// 임시 검증 Pod 정리 후 finalizer 제거. DS 의 RNGD_PARTITION_POLICY env 는 건드리지 않는다 —
// 현재 partition 을 유지(자동 rollback 은 위험, ).
func (r *AcceleratorPartitionPolicyReconciler) handleDeletion(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(acpp, acppFinalizer) {
		return ctrl.Result{}, nil
	}

	// nvidia MIG 는 하드웨어를 실제로 바꿨으므로 snapshot 기반 rollback + 복원 검증을 해야 finalizer 를
	// 지운다. cleanup 실패/차단 시 err 전파 → finalizer 유지(rate-limited requeue).
	if acpp.Spec.Vendor == vendorNvidia {
		if err := r.handleNvidiaDeletion(ctx, acpp); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(acpp, acppFinalizer)
		if err := r.Update(ctx, acpp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	{
		var ds appsv1.DaemonSet
		if err := r.Get(ctx, client.ObjectKey{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds); err == nil {
			// 이 ACPP 가 실제 소유자일 때만 lock 해제 — TargetConflict 패자 삭제가 승자의 lock 을
			// 지우면 안 된다. value 는 소유 ACPP 의 .metadata.name.
			if ds.Annotations[rngd.PartitionOwnerAnnotation] == acpp.Name {
				delete(ds.Annotations, rngd.PartitionOwnerAnnotation)
				if err := r.Update(ctx, &ds); err != nil {
					return ctrl.Result{}, err
				}
			}
		} else if !apierrors.IsNotFound(err) {
			// DS 가 진짜 부재(NotFound)면 해제할 lock 이 없으니 진행. 그 외(transient) 에러는
			// owner lock 을 못 지운 채 ACPP 를 지우면 annotation 이 누수되므로 requeue 한다(Retain 규범).
			return ctrl.Result{}, err
		}
		// 검증 Pod 정리: MVP-1 은 verify Pod 를 생성하지 않으므로 no-op(향후 Pod 기반 verify 도입 시 라벨 셀렉터로 정리).
		controllerutil.RemoveFinalizer(acpp, acppFinalizer)
		if err := r.Update(ctx, acpp); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// handleNvidiaDeletion 은 저장된 ApplyRecords(snapshot only — nodeSelector 재평가 금지, ) 만 보고
// 노드별 MIG 복원을 수행한다. 하드웨어를 바꾼(phase≥Applying) 노드는 소유권을 잃었으면 CleanupBlocked
// (finalizer 유지, 운영자 개입). 소유 중이면 quiesce→snapshot PCI rollback→DP 재시작→fresh NDR 로
// MIG Disabled + baseline gpu 복원 확인→lock 해제. 어느 단계든 err 면 finalizer 유지(requeue).
func (r *AcceleratorPartitionPolicyReconciler) handleNvidiaDeletion(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy) error {
	// 삭제가 끝까지 간 뒤에는 우리가 잡은 노드 Lease 를 돌려준다. 안 돌려주면 정책은 사라졌는데
	// Lease 객체에는 죽은 소유자가 남아, 다음 작업이 만료(60초)를 기다려야 하고 운영자에게는
	// 유령 소유자로 보인다. 중간에 실패하면 돌려주지 않는다 — 되돌리기가 아직 진행 중이므로
	// 잠금을 유지하는 것이 맞다.
	var held []string
	for _, rec := range acpp.Status.ApplyRecords {
		// 삭제는 Reconcile 의 위임 분기보다 먼저 처리돼(:123-125) 위임 모드에서도 여기까지 그대로
		// 온다 — 조정자가 같은 노드에서 operation 을 Applying 중일 수 있다. 되돌리기 전에 같은
		// Lease 를 잡아 두 주체가 동시에 하드웨어를 만지는 창을 없앤다. 위임이 꺼져 있거나
		// Leases 가 안 심어진 배포(기존 동작)는 이 블록이 전부 no-op 이다.
		took, lerr := r.takeDeletionLease(ctx, acpp, rec.NodeName)
		if lerr != nil {
			return lerr
		}
		if took {
			held = append(held, rec.NodeName)
		}
		// 공유 원복은 소유권 판정보다 **위**다 — 공유는 하드웨어를 안 바꿔 MIG 노드 lock 을 잡지
		// 않으므로(layout 없는 공유 정책), 소유권 분기 뒤에 두면 그런 정책은 항상 continue 로
		// 건너뛰어 ConfigMap·배선·소유 표시가 영구히 남아 daemon 없는 배수 광고가 계속 나가고,
		// 다른 이름의 새 정책마저 소유 검사에서 막힌다. 안전하다: RollbackSharing 은 DS/CM 의
		// 소유 표시가 남의 것이면 곧바로 반환하므로 소유하지 않은 노드를 돌아도 건드리지 않는다.
		// GI 제거보다 앞서야 하는 이유도 같다 — 광고 배수가 남으면 baseline 복원 확인이 성립하지 않는다.
		if rec.SharingMode == npuv1alpha1.SharingModeTimeSliced || rec.SharingMode == npuv1alpha1.SharingModeMPS {
			st := partition.Target{Ctx: ctx, NodeName: rec.NodeName, Owner: acpp.Name}
			if err := nvidia.New(r.Client).RollbackSharing(st, partition.SharingRollbackState{}); err != nil {
				return err
			}
			if err := r.restartNvidiaDevicePlugin(ctx, rec.NodeName); err != nil {
				// 재시작 실패로 정리를 끝내지 않는다(공유 해제 경로들과 같은 규율). 배선을 걷는
				// RollbackSharing 이 DP DaemonSet 의 pod 템플릿을 patch 하므로 DaemonSet 컨트롤러가
				// 스스로 롤아웃한다 — 이 호출은 그것을 앞당기는 가속기일 뿐이라 실패해도 광고는
				// 결국 돌아온다. 반대로 여기서 반환하면 소유권 충돌 판정(CleanupBlocked)조차
				// 기록되지 못해 운영자가 진짜 이유를 못 본다.
				logf.FromContext(ctx).Error(err, "deletion: device-plugin restart failed", "node", rec.NodeName)
			}
		}
		// phase≥Applying 이면 하드웨어가 실제로 mutate 됐다(Prepared/LockAcquired/미기록 은 복원 대상 아님).
		// mode 전환 구간도 복원 대상이 아니다 — GI 를 만들기 전이라 되돌릴 GI 가 없고, 그럼에도
		// rollback 을 태우면 BuildDisableSteps 의 "current==Enabled" assert 가 전환 도중(Disabled)에
		// 실패해 finalizer 가 영구히 남는다. mode 자체는 켜진 채 유지한다.
		hardwareChanged := rec.MigPhase != "" && rec.MigPhase != npuv1alpha1.MigPhasePrepared &&
			rec.MigPhase != npuv1alpha1.MigPhaseLockAcquired && !isMigModeEnablePhase(rec.MigPhase)
		lockUID, err := r.nodeOwnerUID(ctx, rec.NodeName)
		if err != nil {
			return err // owner 확인 실패 → finalizer 유지, requeue.
		}
		if lockUID != string(acpp.UID) {
			if !hardwareChanged {
				continue // 하드웨어 미변경 + 소유권 없음 → undo 할 게 없다(타 소유자 노드 미터치).
			}
			// lock 은 사라졌지만 하드웨어가 이미 baseline 일 수 있다 — 예: THIS record 를 이전 pass 에서
			// 완전 정리(lock 해제)했고, 다른 record 의 requeue 로 재진입한 경우. 이미 복원된 하드웨어는
			// 완료로 간주(멱등)하고, 진짜 미복원일 때만 차단한다.
			if err := r.assertMigEmptyAndBaseline(ctx, rec, acpp.Spec.DeletionPolicy == npuv1alpha1.DeletionPolicyRestoreMode); err == nil {
				continue
			}
			// 하드웨어를 바꿨는데 lock 을 잃었고 복원도 증명 못 함 → 차단(운영자 개입).
			r.setDeletionBlocked(ctx, acpp, rec.NodeName, "ownership conflict during cleanup")
			return fmt.Errorf("cleanup blocked on node %s: ownership conflict (phase=%s)", rec.NodeName, rec.MigPhase)
		}
		if !hardwareChanged {
			// mode 전환 도중 삭제되면 우리가 잠근 노드를 되돌려 준다(cordon 된 채 방치 금지).
			if err := r.restoreSchedulable(ctx, acpp, rec.NodeName); err != nil {
				return err
			}
			if err := r.releaseNodeOwnerLockUID(ctx, rec.NodeName, string(acpp.UID)); err != nil {
				return err
			}
			continue
		}
		// cordon 주체는 정책이다. 성공한 apply 는 종점에서 노드를 되돌려 놓으므로 삭제 시점의
		// 노드는 schedulable 이고, 여기서 cordon 을 남에게 맡기면 assertNodeQuiesced 가 오지 않는
		// 외부 주체를 기다리며 영구히 막는다 — finalizer 가 빠지지 않아 ACPP 가 영원히 Terminating 이다.
		// 배출은 하지 않으므로 GPU 를 쥔 pod 이 남아 있으면 아래 quiesce 전제가 그대로 걸러 낸다.
		if err := r.cordonForRollback(ctx, acpp, rec); err != nil {
			// 다른 차단 경로와 같은 신호를 준다 — 이벤트가 없으면 운영자에게는 이유 없는
			// Terminating 으로만 보인다.
			r.emitDeletionBlockedEvent(acpp, rec.NodeName, err)
			return err
		}
		// cordonForRollback 은 기록을 값으로 받아 자기 복사본만 바꾼다. 저장된 기록을 다시 읽어야
		// 이후 단계가 CordonedByPolicy=true 를 보고, 옛 복사본으로 소유 표시를 지우지 않는다.
		rec = getApplyRecord(acpp, rec.NodeName)
		if err := r.assertNodeQuiesced(ctx, rec.NodeName); err != nil {
			r.emitDeletionBlockedEvent(acpp, rec.NodeName, err)
			return err
		}
		b := nvidia.New(r.Client).WithExecutor(r.nvidiaExecutor()).WithTargetPCIs(rec.GPUPCIs)
		if err := b.Rollback(partition.Target{Ctx: ctx, NodeName: rec.NodeName, Owner: string(acpp.UID), Generation: rec.Generation},
			partition.RollbackState{PrevPolicy: "disabled"}); err != nil {
			r.emitDeletionBlockedEvent(acpp, rec.NodeName, err)
			return err
		}
		if err := r.restartNvidiaDevicePlugin(ctx, rec.NodeName); err != nil {
			return err
		}
		if err := r.finishNvidiaDeletionRollback(ctx, acpp, rec); err != nil {
			return err
		}
		if err := r.syncMigActiveLabel(ctx, rec.NodeName, false); err != nil {
			return err
		}
		// 복원이 증명된 뒤에만 노드를 되돌린다 — 그 전에 uncordon 하면 rollback 도중 워크로드가 들어온다.
		if err := r.restoreSchedulable(ctx, acpp, rec.NodeName); err != nil {
			return err
		}
		if err := r.releaseNodeOwnerLockUID(ctx, rec.NodeName, string(acpp.UID)); err != nil {
			return err
		}
	}
	// control daemon 정리는 저널 상태·소유권과 무관하게 멱등이다 — 참조 카운트(anyPolicyStillUsesMPS)가
	// "다른 정책이 아직 쓰는가" 를 이미 지키므로 무조건 불러도 안전하다. 그래서 루프 밖에서 정확히
	// 한 번 부른다 — 루프 안에 두면 sharing-only 정책은 MIG 노드 lock 을 잡지 않아 소유권 분기에서
	// 항상 continue 하고, ApplyRecords 가 비면 루프 자체가 돌지 않아 daemon 이 영구히 남는다.
	// 위치가 루프 뒤인 이유는 순서다 — DP 배선(RollbackSharing)을 먼저 걷어야 클라이언트가 죽은
	// daemon 을 물고 남지 않는다. node 인자는 exclusive 경로에서 쓰이지 않으므로 빈 값이면 된다.
	if err := r.ensureMpsControlDaemon(ctx, npuv1alpha1.SharingModeExclusive, acpp.Name, ""); err != nil {
		return err
	}
	// owner-lock 은 Diff/Apply 이전에 선취득(runNvidiaTarget)되나 ApplyRecord 는 mutation 직전에야
	// 영속된다. 그 사이 hardware-block(MigModeNotEnabled/NotCordoned/GPUBusy)으로 조기 반환되면
	// lock 만 남고 record 가 없어, record 순회만으로는 이 노드 lock 을 해제하지 못해 누수된다(삭제 후
	// 재-ACPP 영구 TargetConflict). record 없는 lock 은 hardware 미변경이므로 rollback 없이 해제한다.
	if err := r.releaseOrphanOwnerLocks(ctx, string(acpp.UID)); err != nil {
		return err
	}
	return r.releaseDeletionLeases(ctx, acpp, held)
}

// takeDeletionLease 는 삭제 경로가 노드 Lease 를 잡는 지점이다. 위임이 꺼져 있거나 Lease 관리자가
// 없으면(기존 배포) 아무것도 하지 않고 false 를 돌려준다 — 그 경우 돌려줄 것도 없다.
func (r *AcceleratorPartitionPolicyReconciler) takeDeletionLease(ctx context.Context,
	acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string) (bool, error) {
	if !delegateModeEnabled() || r.Leases == nil {
		return false, nil
	}
	ok, _, err := r.Leases.Acquire(ctx, node, deletionLeaseHolder(acpp))
	if err != nil {
		return false, err
	}
	if !ok {
		// 조정자가 이 노드를 쥐고 있다 — 지금 되돌리면 동시 mutation 이 된다. finalizer 를
		// 유지하고 다음 pass 에 다시 시도한다(Lease 는 만료가 있으므로 영구 대기가 아니다).
		return false, fmt.Errorf("cleanup waiting for node lease on %s: coordinator busy", node)
	}
	return true, nil
}

// releaseDeletionLeases 는 삭제가 끝까지 간 뒤 잡았던 노드 Lease 를 돌려준다. 중간 실패 경로에서는
// 부르지 않는다 — 되돌리기가 아직 진행 중이면 잠금을 유지하는 것이 맞다.
func (r *AcceleratorPartitionPolicyReconciler) releaseDeletionLeases(ctx context.Context,
	acpp *npuv1alpha1.AcceleratorPartitionPolicy, nodes []string) error {
	for _, node := range nodes {
		if err := r.Leases.Release(ctx, node, deletionLeaseHolder(acpp)); err != nil {
			return err
		}
	}
	return nil
}

// releaseOrphanOwnerLocks 는 이 ACPP UID 로 잠긴 노드 중 ApplyRecord 로 정리되지 않은 lock 을 스캔·해제한다.
// nodeSelector 재평가가 아니라 UID annotation 매칭이므로 (snapshot only)을 위반하지 않고,
// releaseNodeOwnerLockUID 가 값 일치 시에만 지우므로 타 소유자 노드는 건드리지 않는다. 이미 해제된 노드는 no-op.
func (r *AcceleratorPartitionPolicyReconciler) releaseOrphanOwnerLocks(ctx context.Context, uid string) error {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return err
	}
	for i := range nodes.Items {
		if nodes.Items[i].Annotations[migOwnerAnnotation] == uid {
			if err := r.releaseNodeOwnerLockUID(ctx, nodes.Items[i].Name, uid); err != nil {
				return err
			}
		}
	}
	return nil
}

// nodeOwnerUID 는 노드의 migOwnerAnnotation 값(소유 ACPP UID)을 읽는다. 부재면 "".
func (r *AcceleratorPartitionPolicyReconciler) nodeOwnerUID(ctx context.Context, node string) (string, error) {
	var n corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: node}, &n); err != nil {
		return "", err
	}
	return n.Annotations[migOwnerAnnotation], nil
}

// releaseNodeOwnerLockUID 는 annotation 값이 uid 와 일치할 때만 owner-lock 을 지운다(merge-patch, 타 소유자 미터치).
func (r *AcceleratorPartitionPolicyReconciler) releaseNodeOwnerLockUID(ctx context.Context, node, uid string) error {
	var n corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: node}, &n); err != nil {
		return err
	}
	if n.Annotations[migOwnerAnnotation] != uid {
		return nil
	}
	base := n.DeepCopy()
	delete(n.Annotations, migOwnerAnnotation)
	return r.Patch(ctx, &n, client.MergeFrom(base))
}

// migModeDisableAction 은 mode disable Job 의 결정론적 이름에 들어가는 action 토큰이다(GI apply/enable 과 구분).
const migModeDisableAction = "mig-mode-disable"

// finishNvidiaDeletionRollback 은 GI rollback 뒤 baseline 확인과(deletionPolicy 에 따라) MIG
// mode 복원까지를 한 단계로 묶는다. handleNvidiaDeletion 밖으로 뽑은 이유는 그 함수의 복잡도가
// 상한에 가깝기 때문이다(gocyclo) — 로직은 그대로이고 나누기만 한다.
func (r *AcceleratorPartitionPolicyReconciler) finishNvidiaDeletionRollback(ctx context.Context,
	acpp *npuv1alpha1.AcceleratorPartitionPolicy, rec npuv1alpha1.ApplyRecord) error {
	restoreMode := acpp.Spec.DeletionPolicy == npuv1alpha1.DeletionPolicyRestoreMode
	// RestoreMode 는 mode 복원을 baseline 확인보다 **먼저** 한다. mixed 전략에서 MIG mode 가 켜진
	// 채 조각이 없는 GPU 는 아무것도 광고하지 않으므로, mode 를 끄기 전의 allocatable 은 baseline
	// 보다 반드시 적다 — 순서가 반대면 baseline 확인이 mode 복원을 막고 mode 복원만이 baseline 을
	// 만들 수 있어 서로를 기다린다. mode 복원이 끝난 뒤의 pass 에서 아래 baseline 확인이 그대로
	// 판정한다 — 확인을 건너뛰지 않는다.
	if restoreMode {
		if err := r.enforceRestoreModeOnDeletion(ctx, acpp, rec); err != nil {
			// 차단 이벤트는 enforceRestoreModeOnDeletion 이 실패 시에만 남긴다 — 재부팅 대기는
			// 정상 진행 상태라 매 pass 이벤트를 남기면 소음이 된다.
			return err
		}
	}
	if err := r.assertMigEmptyAndBaseline(ctx, rec, restoreMode); err != nil {
		r.emitDeletionBlockedEvent(acpp, rec.NodeName, err)
		return err // 복원 미확인 → finalizer 유지(never remove on unverified restore).
	}
	return nil
}

// enforceRestoreModeOnDeletion 은 deletionPolicy=RestoreMode 일 때만 mode 복원을 밀어붙인다.
func (r *AcceleratorPartitionPolicyReconciler) enforceRestoreModeOnDeletion(ctx context.Context,
	acpp *npuv1alpha1.AcceleratorPartitionPolicy, rec npuv1alpha1.ApplyRecord) error {
	if acpp.Spec.DeletionPolicy != npuv1alpha1.DeletionPolicyRestoreMode {
		return nil
	}
	done, merr := r.restoreMigModeDisabled(ctx, acpp, rec)
	if merr != nil {
		r.emitDeletionBlockedEvent(acpp, rec.NodeName, merr)
		return merr
	}
	if !done {
		// 아직 mode 가 Disabled 로 관측되지 않는다 — finalizer 를 유지해 다음 pass 에 이어간다.
		// 여기서 통과시키면 mode 가 켜진 채 정책만 사라져, 그 노드는 공유를 못 쓰는데 이유를
		// 알려줄 객체도 없는 상태가 된다.
		return fmt.Errorf("cleanup waiting for MIG mode restore on node %s", rec.NodeName)
	}
	return nil
}

// restoreMigModeDisabled 는 GI 회수 뒤 MIG mode 를 Disabled 로 되돌린다(deletionPolicy=RestoreMode).
// 완료 판정은 재관측이다 — 재부팅 성공만으로는 mode 가 실제로 내려갔는지 알 수 없다(MIG mode
// enable 경로와 같은 이유). 재부팅 횟수는 저널의 DisableRebootAttempts 로 제한한다 — enable 경로의
// RebootAttempts 와는 별도 예산이다(공유하면 enable 이 상한을 소진하고 Failed 로 끝난 정책에서
// disable 이 한 번도 못 쏴 보고 막힌다). 재부팅 Job 이 이미 있으면 이번 사이클은 이미 쏜 것이다 —
// 존재 여부를 먼저 봐야 완료 전에 명령과 카운터가 매 pass 다시 오르지 않는다.
func (r *AcceleratorPartitionPolicyReconciler) restoreMigModeDisabled(ctx context.Context,
	acpp *npuv1alpha1.AcceleratorPartitionPolicy, rec npuv1alpha1.ApplyRecord) (bool, error) {
	devs, oerr := r.observeDevicesForNode(ctx, rec.NodeName, rec.GPUPCIs)
	if oerr != nil {
		return false, oerr
	}
	// 관측을 신뢰할 수 없으면 아무것도 하지 않는다 — 상태를 모르는 채 재부팅을 예약하는 것이
	// 이 경로에서 가장 위험한 행동이다(fail-closed, ModeObservable 과 같은 게이트).
	if !nvidia.ModeObservable(devs) {
		return false, fmt.Errorf("mig mode not observable on node %s; refusing to restore", rec.NodeName)
	}
	if !nvidia.NeedsModeDisable(devs) {
		return true, nil // 이미 Disabled — 멱등하게 완료.
	}

	var existingJob batchv1.Job
	jobErr := r.Get(ctx, types.NamespacedName{Name: naming.AcppRebootJobName(rec.NodeName), Namespace: driverjob.Namespace}, &existingJob)
	if jobErr == nil {
		return false, nil // 이미 예약했다 — 다음 재관측을 기다린다(명령·카운터 재실행 금지).
	}
	if !apierrors.IsNotFound(jobErr) {
		return false, jobErr
	}

	if rec.DisableRebootAttempts >= maxMigRebootAttempts {
		return false, fmt.Errorf("mig mode still enabled on node %s after %d reboots; manual intervention required",
			rec.NodeName, rec.DisableRebootAttempts)
	}

	pcis := nvidia.ModeDisableTargets(devs)
	steps := nvidia.ModeDisableSteps(pcis)
	opID, cmdHash := nvidia.OperationID(string(acpp.UID), acpp.Generation, rec.NodeName, migModeDisableAction, steps)
	if err := r.nvidiaExecutor().Run(ctx, opID, cmdHash, rec.NodeName, steps); err != nil {
		return false, err
	}
	// 시도 횟수를 Job 생성 **이전에** 영속한다 — 재부팅이 프로세스를 죽이므로 카운터가 먼저
	// durable 해야 상한이 의미를 갖는다(enable 경로와 같은 규율).
	// 저장된 최신 기록에서 시작한다 — 호출자가 들고 있는 복사본으로 통째 저장하면 그 뒤에 바뀐
	// 필드(삭제가 잡은 CordonedByPolicy)를 옛 값으로 되돌린다.
	next := getApplyRecord(acpp, rec.NodeName)
	next.NodeName = rec.NodeName
	next.DisableRebootAttempts++
	if err := r.patchApplyRecord(ctx, acpp, next); err != nil {
		return false, err
	}
	rb := &nodeRebooter{Client: r.Client, Image: HostExecImage()}
	if err := rb.RequestReboot(ctx, rec.NodeName, ""); err != nil {
		return false, err
	}
	return false, nil
}

// assertNodeQuiesced 는 rollback 안전(노드 cordon + 장치 점유 워크로드 없음)을 검증한다.
// 판정은 전적으로 partition.Quiescer 에 있다 — 배출 대상 규칙과 완료 판정 규칙이 갈라지면
// "배출은 안 하면서 정지했다고 보는" 구멍이 생기므로(DRA pod 이 실제로 그랬다) 한 곳에 둔다.
func (r *AcceleratorPartitionPolicyReconciler) assertNodeQuiesced(ctx context.Context, node string) error {
	if err := partition.NvidiaQuiescer(r.Client).AssertQuiesced(ctx, node); err != nil {
		return fmt.Errorf("%w; unsafe to rollback MIG", err)
	}
	return nil
}

// emitDeletionBlockedEvent 는 삭제 rollback 이 막힌 이유를 Warning 이벤트로 남긴다.
// MigPhase/status 는 건드리지 않는다 — 삭제 경로의 저널은 "무엇을 되돌려야 하는가"의 유일한
// 근거라 phase 를 덮으면 소유권 충돌 차단과 같은 종류의 자기유발 상태 오염이 재발한다.
func (r *AcceleratorPartitionPolicyReconciler) emitDeletionBlockedEvent(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string, err error) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(acpp, corev1.EventTypeWarning, "DeletionRollbackBlocked",
		"node %s: MIG rollback during deletion is blocked: %v (finalizer held until resolved)", node, err)
}

// assertMigEmptyAndBaseline 은 rollback 후 fresh NDR 로 대상 PCI 전부 MIG Enabled + GI 없음(geometry
// 빈 문자열)이고 노드 nvidia.com/gpu allocatable 이 baseline 으로 복원됐는지 확인한다.
// mode 는 끄지 않는다(GI 만 제거) — 하나라도 아니면 err. allowModeDisabled 는 deletionPolicy=
// RestoreMode 에서만 true 다 — 그 정책은 이 확인 뒤에 mode 를 Disabled 로 되돌리므로, 이후 재진입은
// mode 가 이미 Disabled 로 관측된다. "반드시 Enabled" 를 그대로 두면 mode 복원이 성공한 순간부터
// 이 확인이 영구 거절돼 정리가 못 끝나는 역설이 된다. Retain 은 mode 를 안 건드려 Enabled 만 받는다.
func (r *AcceleratorPartitionPolicyReconciler) assertMigEmptyAndBaseline(ctx context.Context, rec npuv1alpha1.ApplyRecord, allowModeDisabled bool) error {
	// MIG 상태는 detector 가 못 채우므로 apply 경로와 동일하게 observer(nsenter Job)로 관측한다.
	obs, err := r.nvidiaObserver().Observe(ctx, rec.NodeName, rec.GPUPCIs)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(obs))
	for _, o := range obs {
		seen[o.PCI] = true
		if o.Err != "" {
			return fmt.Errorf("node %s pci %s observation failed after rollback: %s", rec.NodeName, o.PCI, o.Err)
		}
		modeOK := o.ModeCurrent == migModeEnabled || (allowModeDisabled && o.ModeCurrent == migModeDisabled)
		if !modeOK {
			return fmt.Errorf("node %s pci %s MIG not enabled after rollback (current=%s)", rec.NodeName, o.PCI, o.ModeCurrent)
		}
		if !migGeometryEmpty(o.Geometry) {
			return fmt.Errorf("node %s pci %s still has GPU instances after rollback (geometry=%s)", rec.NodeName, o.PCI, o.Geometry)
		}
	}
	for _, p := range rec.GPUPCIs {
		if !seen[p] {
			return fmt.Errorf("node %s: snapshot MIG pci %s not observed after rollback", rec.NodeName, p)
		}
	}
	var n corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: rec.NodeName}, &n); err != nil {
		return err
	}
	q := n.Status.Allocatable[corev1.ResourceName(nvidiaGPUResource)]
	got := int32(q.Value())
	// mode 까지 되돌리는 경로(RestoreMode)는 baseline 보다 **많이** 돌아온다 — mode 를 끄면 조각
	// 대상이던 GPU 가 온전한 GPU 로 다시 광고되기 때문이다. 정확 일치를 요구하면 복원이 성공한
	// 그 사실 때문에 정리가 막힌다. 부족한 것만 실패로 본다.
	if allowModeDisabled {
		if got < rec.BaselineGPUCount {
			return fmt.Errorf("node %s allocatable %s=%d not restored to baseline %d",
				rec.NodeName, nvidiaGPUResource, got, rec.BaselineGPUCount)
		}
		return nil
	}
	// Retain 은 MIG mode 를 켠 채 둔다 — mode 가 Enabled 인 GPU 는 조각을 모두 회수해도 full GPU 로
	// 광고되지 않으므로, 돌아올 수 있는 값은 MIG 대상이 아닌 GPU 수(ExpectedFullGPUCount)다. 여기서
	// BaselineGPUCount 를 요구하면 안 된다 — 그 값은 baseline 을 잡던 시점의 광고량이라 mode 가
	// Disabled 인 노드에서 시작하면 조각 대상 GPU 까지 포함한 수다. mode 를 켠 뒤에는 그 수가
	// 원리상 다시 나올 수 없어 finalizer 가 영구 보유되고 노드가 cordon 된 채 남는다.
	if got != rec.ExpectedFullGPUCount {
		return fmt.Errorf("node %s allocatable %s=%d not restored to expected full-gpu count %d",
			rec.NodeName, nvidiaGPUResource, got, rec.ExpectedFullGPUCount)
	}
	return nil
}

// migGeometryEmpty 는 "조각이 없다" 의 두 표기를 함께 받는다. 관측기는 mode 가 꺼진 GPU 를
// geometry "disabled" 로 보고하는데(NodeDeviceReport 계약과 같은 어휘), 빈 문자열만 받으면
// mode 복원이 끝난 노드가 "아직 조각이 남았다" 로 읽힌다.
func migGeometryEmpty(g string) bool {
	return g == "" || strings.EqualFold(g, "disabled")
}

// setDeletionBlocked 는 cleanup 차단 상태를 영속한다: 대상 rec.MigPhase=CleanupBlocked + target
// OwnershipConflict condition. finalizer 는 유지되므로 운영자가 수동 복원 후 개입해야 한다.
func (r *AcceleratorPartitionPolicyReconciler) setDeletionBlocked(ctx context.Context, acpp *npuv1alpha1.AcceleratorPartitionPolicy, node, msg string) {
	base := acpp.DeepCopy()
	for i := range acpp.Status.ApplyRecords {
		if acpp.Status.ApplyRecords[i].NodeName == node {
			acpp.Status.ApplyRecords[i].MigPhase = npuv1alpha1.MigPhaseCleanupBlocked
		}
	}
	idx := -1
	for i := range acpp.Status.Targets {
		if acpp.Status.Targets[i].NodeName == node {
			idx = i
			break
		}
	}
	if idx < 0 {
		acpp.Status.Targets = append(acpp.Status.Targets, npuv1alpha1.TargetStatus{NodeName: node})
		idx = len(acpp.Status.Targets) - 1
	}
	setCond(&acpp.Status.Targets[idx], npuv1alpha1.ACPPCondApplied, metav1.ConditionFalse,
		npuv1alpha1.ReasonOwnershipConflict, msg, acpp.Generation)
	logger := logf.FromContext(ctx)
	if err := r.Status().Patch(ctx, acpp, client.MergeFrom(base)); err != nil {
		logger.Error(err, "setDeletionBlocked: status patch failed")
	}
}

// delegateModeEnabled 는 위임 모드가 켜졌는지다.
//
// 값을 캐시하지 않는다 — 캐시하면 "껐는데 안 꺼진다" 가 생긴다. Getenv 는 프로세스 메모리
// 조회라 매 reconcile 마다 불러도 비용이 없다. 유효값은 "delegate" 하나이고 그 외/미설정은
// 전부 꺼짐이다(값이 없으면 꺼진 것으로 읽히는 방향이 안전하다).
func delegateModeEnabled() bool {
	return os.Getenv("KCLOUD_OPERATION_COORDINATOR") == "delegate"
}

// deletionLeaseHolder 는 삭제 경로가 노드 Lease 를 잡을 때 쓰는 소유자 식별자다. 정책 이름
// 기준으로 고정해 같은 삭제 pass 의 재시도가 자기 자신의 Lease 를 다시 잡을 수 있게 한다.
func deletionLeaseHolder(acpp *npuv1alpha1.AcceleratorPartitionPolicy) string {
	return "acpp-delete-" + acpp.Name
}

// TransactionIDFor 는 같은 의도의 재요청이 같은 값을 갖도록 만든 요청 정체성이다.
// (uid, generation, node) — spec 이 바뀌면 generation 이 오르므로 새 트랜잭션이 된다.
func TransactionIDFor(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string) string {
	return fmt.Sprintf("%s-%d-%s", acpp.UID, acpp.Generation, node)
}

// OperationNameFor 는 트랜잭션 객체 이름이다. 결정론적이라 같은 의도는 같은 객체를 재사용한다 —
// 시각이나 난수를 넣으면 reconcile 마다 트랜잭션이 쌓인다.
func OperationNameFor(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string) string {
	// UID 접미사가 없으면 삭제 후 같은 이름으로 재생성된 정책(generation 이 1로 되돌아간다)이
	// 옛 정책의(고아로 남은, 대개 이미 종점인) operation 과 이름이 겹친다 — Create 가 돌려주는
	// AlreadyExists 를 그대로 재사용으로 읽어, 이미 끝난 옛 객체를 새 정책이 영원히 기다리게
	// 된다. UID 는 정책 인스턴스마다 달라 이 충돌 자체가 성립하지 않는다.
	uid := string(acpp.UID)
	if len(uid) > 8 {
		uid = uid[:8]
	}
	suffix := "-" + uid
	name := fmt.Sprintf("acpp-%s-%d-%s", acpp.Name, acpp.Generation, node)
	if max := 63 - len(suffix); len(name) > max {
		name = name[:max]
	}
	return name + suffix
}

// delegateToOperation 은 위임 모드에서 정책 대신 트랜잭션을 만든다. 여기서 runTarget 을 부르지
// 않는다 — 위임 모드의 유일한 mutation 호출자는 participant 이고, 그래서 두 경로가 동시에
// 존재하지 않는다(분기가 아니라 호출자의 이동이다). 이미 수렴한 정책에는 트랜잭션을 만들지
// 않는다 — 이 가드가 없으면 gate 를 켜는 순간 클러스터의 모든 Ready 정책이 재적용된다(이 기능에서
// 폭발 반경이 가장 큰 지점이다).
func (r *AcceleratorPartitionPolicyReconciler) delegateToOperation(ctx context.Context,
	acpp *npuv1alpha1.AcceleratorPartitionPolicy, t partition.Target) (ctrl.Result, error) {
	var prev *npuv1alpha1.TargetStatus
	if len(acpp.Status.Targets) > 0 {
		prev = &acpp.Status.Targets[0]
	}
	if !r.shouldReverify(acpp, prev) {
		// 수렴 상태다 — 트랜잭션 없이 기존 광고 감시만 돈다.
		ts, err := r.monitorDrift(t, acpp, *prev)
		if err != nil {
			return ctrl.Result{}, err
		}
		if werr := r.writeTargetStatus(ctx, acpp, ts); werr != nil {
			return ctrl.Result{}, werr
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	if t.NodeName == "" {
		return ctrl.Result{}, nil // 대상 노드가 없다 — 만들 트랜잭션이 없다.
	}

	opType := OperationTypeForACPP(acpp)
	op := &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: OperationNameFor(acpp, t.NodeName)},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: string(opType), NodeName: t.NodeName, Vendor: acpp.Spec.Vendor,
			TransactionID: TransactionIDFor(acpp, t.NodeName),
			Owner: npuv1alpha1.OperationOwner{
				Kind: "AcceleratorPartitionPolicy", Name: acpp.Name,
				UID: string(acpp.UID), Generation: acpp.Generation,
			},
			ResourceKeys: ResourceKeysForACPP(acpp, t.NodeName, opType),
		},
	}
	// ownerRef 를 심어 정책이 지워지면 K8s GC 가 이 트랜잭션도 회수하게 한다(둘 다 cluster-scoped
	// 라 유효하다) — 그러지 않으면 종점(Succeeded 등)에 도달한 operation 객체가 영구히 쌓인다.
	if err := ctrl.SetControllerReference(acpp, op, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, op); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// draOwnedNode 는 이 노드의 이 벤더 광고가 DRA 로 넘어갔는지다. 라벨은 NPUClusterPolicy 의
// advertiseBy 에서 파생된다(operator 가 붙인다). 노드를 못 읽으면 "모른다" 이므로 오류다 —
// 안전 게이트에서 조회 실패를 "아니다" 로 흘리면 두 소유자가 붙는다.
func (r *AcceleratorPartitionPolicyReconciler) draOwnedNode(ctx context.Context, node, vendor string) (bool, error) {
	if node == "" || vendor == "" {
		return false, nil
	}
	var n corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return false, fmt.Errorf("노드 %s 조회 실패(DRA 소유 확인): %w", node, err)
	}
	return n.Labels[npuv1alpha1.DRAOwnedNodeLabel(vendor)] == labelValueTrue, nil
}
