/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/partition/rngd"
	"kcloud-operator/internal/upgrade"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const finalizerName = "npu.ai/cleanup"

// ownerAnnotation is used to track ownership across namespaces (cross-namespace OwnerReference is not allowed).
const ownerAnnotation = "npu.ai/owner"

// vendorNvidia is the NVIDIA vendor identifier used across the controller package.
const vendorNvidia = "nvidia"

// nvidiaDevicePluginVendorLabel 은 mixed/flat 두 device-plugin DaemonSet 이 공통으로 다는 라벨이다.
// 두 DS 는 selector 충돌을 피하려 서로 다른 app.kubernetes.io/name 을 쓰므로,
// restartNvidiaDevicePlugin 이 어느 쪽 pod 이든 찾으려면 이 공통 라벨로 List 해야 한다.
const nvidiaDevicePluginVendorLabel = "kcloud.ai/dp-vendor"

// vendorFuriosa is the Furiosa vendor identifier used across the controller package.
const vendorFuriosa = "furiosa"

// Tenstorrent Blackhole device plugin 기본 상수.
// TT plugin 은 공식 벤더 plugin 이 없어 kcloud 가 자체 구현했으므로(3rd party 아님),
// 분류 정합상 namespace 는 operator 네임스페이스(kcloud, naming.OperatorNamespace())로 배치한다.
// DaemonSet 이름 `kcloud-tt-device-plugin`(자체 개발 표기). 타 벤더 plugin 은 3rd party 라 kube-system 유지.
// 이미지는 Harbor(global.registry) 경유로 조립되며,
// helm 미경유(직접 CR) 시를 위해 ttImageDefault 는 registry-relative 기본값을 둔다.
const (
	ttDaemonSetName   = "kcloud-tt-device-plugin"
	ttResourceDefault = "tenstorrent.com/blackhole"
	ttImageDefault    = "kcloud-tt-device-plugin:v0.1.0"
)

// Furiosa 통합 device-plugin 상수.
// 단일 DS 가 Warboy/RNGD 양 노드에 스케줄되며(공통 PCI vendor 라벨), entrypoint 가
// PCI device ID 로 모델을 감지해 해당 벤더 바이너리를 exec 한다.
const (
	// 모델별 device-plugin DS 이름. 벤더 공개 이미지를 그대로 실행하는 3rd-party 워크로드라
	// kube-system 에 kcloud- 접두 없이 둔다(nvidia-device-plugin·rbln-device-plugin 과 같은 규칙).
	// kcloud 네임스페이스는 kcloud 가 개발한 리소스만 담는다.
	furiosaWarboyDSName = "furiosa-warboy-device-plugin"
	furiosaRngdDSName   = "furiosa-rngd-device-plugin"
	// 개명 전 이름. 조정 때 있으면 지우는 정리 전용이다 — DS selector 가 immutable 이라
	// 이름을 바꾼 자리는 새로 만들고 옛 것을 지우는 수밖에 없다.
	//   - furiosa-device-plugin(kube-system): v0.7.26 이하의 Warboy DS
	//   - kcloud-furiosa-{warboy,rngd}-device-plugin(operator ns): v0.7.27 한 회차의 이름
	furiosaWarboyLegacyDSName = "furiosa-device-plugin"
	furiosaWarboyV0727DSName  = "kcloud-furiosa-warboy-device-plugin"
	furiosaRngdV0727DSName    = "kcloud-furiosa-rngd-device-plugin"
	// 통합 DS 와 모델별 DS 가 공유하는 device-plugin 컨테이너 이름. DS 이름과 달리 옛
	// 이름을 유지한다 — ACPP 가 살아 있는 DS 에서 이 컨테이너를 이름으로 집어 partition
	// env 를 읽고 쓴다(internal/partition/rngd).
	furiosaPluginContainerName = "furiosa-device-plugin"
	// 자체 구현이므로 operator 네임스페이스에 kcloud- 접두사로 둔다(TT plugin 과 같은 규칙).
	// 옛 이름·자리는 furiosaUnifiedLegacy* 로 남겨 한 번 정리한다.
	furiosaUnifiedDSName       = "kcloud-furiosa-device-plugin"
	furiosaUnifiedLegacyDSName = "furiosa-unified-device-plugin"
	furiosaUnifiedImgDefault   = "furiosa-unified-device-plugin:0.1.0"
	// 양 Furiosa 노드(Warboy/RNGD)가 공통으로 갖는 자립 라벨(node-manager 부여, PCI 0x1ed2).
	// NFD 비의존 — 통합 DS 공통 셀렉터.
	furiosaFamilyNodeLabel = npuv1alpha1.FuriosaFamilyPresentLabel
)

// furiosaPerModelDSRefs 는 모델별 Furiosa device-plugin DS 가 있을 수 있는 자리 전부다.
// 현재 자리는 kube-system 의 벤더식 이름, 옛 자리는 v0.7.26 의 kube-system 이름과
// v0.7.27 한 회차의 operator 네임스페이스 kcloud- 이름이다.
func furiosaPerModelDSRefs() []types.NamespacedName {
	return []types.NamespacedName{
		{Namespace: furiosaVendorDSNamespace(), Name: furiosaWarboyDSName},
		{Namespace: furiosaVendorDSNamespace(), Name: furiosaRngdDSName},
		{Namespace: naming.KubeSystemNamespace, Name: furiosaWarboyLegacyDSName},
		{Namespace: naming.OperatorNamespace(), Name: furiosaWarboyV0727DSName},
		{Namespace: naming.OperatorNamespace(), Name: furiosaRngdV0727DSName},
	}
}

// furiosaVendorDSNamespace 는 벤더 공개 이미지를 그대로 실행하는 모델별 Furiosa DS(Warboy·RNGD)와
// Warboy ConfigMap 이 사는 네임스페이스다. 3rd-party 워크로드 규칙대로 kube-system 이다.
func furiosaVendorDSNamespace() string { return naming.KubeSystemNamespace }

// furiosaUnifiedDSNamespace 는 우리가 빌드한 통합 Furiosa device-plugin DS 가 사는
// 네임스페이스다. 자체 이미지라 operator 네임스페이스를 따른다 — 모델별 벤더 DS(kube-system)와 다르다.
func furiosaUnifiedDSNamespace() string { return naming.OperatorNamespace() }

// NPUClusterPolicyReconciler reconciles a NPUClusterPolicy object
type NPUClusterPolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// mapper 는 시험이 주입하는 REST mapper 다. nil 이면 client 의 것(discovery 기반)을 쓴다.
	// DynamicRESTMapper 는 캐시 적중 시 재발견하지 않아 CRD 제거→미감지 전이를 시험이
	// 결정적으로 재현하기 어려워, 시험에서만 이 필드로 mapper 를 교체한다(실서비스는 nil).
	mapper apimeta.RESTMapper
	// APIReader 는 벤더 operator 감지 재확인(vendorOperatorCRDConfirmed)이 쓰는 캐시 없는 읽기
	// 경로다. 매니저 캐시(Client)로 CRD 를 GET 하면 클러스터 전체 CRD informer 가 새로 뜨므로
	// 쓰지 않는다. nil 이면 재확인을 건너뛰고 RESTMapper 캐시 판정만 쓴다(기존 동작 그대로).
	APIReader client.Reader
}

func (r *NPUClusterPolicyReconciler) createOrUpdateDS(ctx context.Context, desired *appsv1.DaemonSet) error {
	var cur appsv1.DaemonSet
	key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}
	if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	// 회수 삭제는 비동기라, 삭제가 끝나기 전 같은 이름으로 다시 켜면 Get 이 deletionTimestamp
	// 가 찍힌(삭제 진행 중) DaemonSet 을 돌려준다. 그 객체를 Update 하면 GC 가 곧 지워 광고가
	// 사라지므로, 오류를 돌려 다음 reconcile 로 backoff 재시도한다 — 그때는 GC 가 끝나 Create 로 재생성.
	if !cur.DeletionTimestamp.IsZero() {
		return fmt.Errorf("DaemonSet %s/%s 삭제 진행 중 — 재생성 전 GC 완료 대기", cur.Namespace, cur.Name)
	}
	if !equality.Semantic.DeepEqual(cur.Spec, desired.Spec) ||
		!equality.Semantic.DeepEqual(cur.Labels, desired.Labels) ||
		!equality.Semantic.DeepEqual(cur.Annotations, desired.Annotations) {
		cur.Spec = desired.Spec
		cur.Labels = desired.Labels
		cur.Annotations = desired.Annotations
		return r.Update(ctx, &cur)
	}
	return nil
}

// ConfigMap 공통 보장
func (r *NPUClusterPolicyReconciler) createOrUpdateCM(ctx context.Context, desired *corev1.ConfigMap) error {
	var cur corev1.ConfigMap
	key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}
	if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	if !equality.Semantic.DeepEqual(cur.Data, desired.Data) {
		cur.Data = desired.Data
		return r.Update(ctx, &cur)
	}
	return nil
}

// +kubebuilder:rbac:groups=npu.ai,resources=npuclusterpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=npu.ai,resources=npuclusterpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=npu.ai,resources=npuclusterpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=resource.k8s.io,resources=deviceclasses,verbs=get;list;watch;create;update;patch;delete
// DRA 드라이버에 줄 권한은 operator 자신이 먼저 갖고 있어야 한다 — 쿠버네티스가
// 자기가 없는 권한을 담은 ClusterRole 생성을 막는다(권한 상승 방지).
// +kubebuilder:rbac:groups=resource.k8s.io,resources=resourceclaims;resourceslices;resourceclaimtemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=resource.k8s.io,resources=resourceclaims/status;resourceslices/status;resourceclaimtemplates/status,verbs=update;patch
// +kubebuilder:rbac:groups=resource.k8s.io,resources=resourceclaims/driver,verbs={"associated-node:update","associated-node:patch"}
// 타 벤더 operator 감지 재확인(vendoroperator_detect.go 의 vendorOperatorCRDConfirmed)이
// CustomResourceDefinition 자체를 캐시 없는 APIReader 로 get 한다.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get
// 벤더 operator 상태 보고(vendoroperator_status.go 의 readVendorOperatorState)가 그 벤더의
// ClusterPolicy 류 CR 을 캐시 없는 APIReader 로 get·list 한다. 이 규칙이 빠지면 Forbidden 이
// Unknown(StatusProbeUnavailable)으로 보고되므로 조용히 "조회 불가" 상태로만 남는다.
// +kubebuilder:rbac:groups=nvidia.com,resources=clusterpolicies,verbs=get;list
// +kubebuilder:rbac:groups=rebellions.ai,resources=rblnclusterpolicies,verbs=get;list

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *NPUClusterPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	metrics.RecordReconcile() // reconcile 호출 시각 기록 (liveness probe 용)
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling NPUClusterPolicy", "name", req.NamespacedName)

	// -- Get CR
	var policy npuv1alpha1.NPUClusterPolicy
	if err := r.Get(ctx, req.NamespacedName, &policy); err != nil {
		logger.Error(err, "unable to fetch NPUClusterPolicy")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// -- Finalizer: handle deletion
	if !policy.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&policy, finalizerName) {
			if err := r.cleanupOwnedResources(ctx, &policy); err != nil {
				logger.Error(err, "failed to cleanup owned resources")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&policy, finalizerName)
			if err := r.Update(ctx, &policy); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// -- Finalizer: add if not present
	if !controllerutil.ContainsFinalizer(&policy, finalizerName) {
		controllerutil.AddFinalizer(&policy, finalizerName)
		if err := r.Update(ctx, &policy); err != nil {
			return ctrl.Result{}, err
		}
	}

	// -- node-manager(detector) — 리소스명은 kcloud-node-manager, 이미지/기능은 detector 그대로(이름만 변경)
	if err := r.ensureDetector(ctx, &policy); err != nil {
		logger.Error(err, "failed to ensure kcloud-node-manager")
		r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "kcloud-node-manager", err)
		r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "DetectorFailed", err.Error())
		return ctrl.Result{}, err
	}

	// -- 노드 라벨 동기화: DaemonSet 을 만들기 전에 광고 주체 전환·배제 노드 표시 두 축을
	// spec 과 맞춘다. 순서가 뒤바뀌면 라벨이 붙기 전 렌더가 한 박자 먼저 나가 두 광고가
	// 겹칠 수 있다. 두 축을 한 호출로 묶어 Reconcile 의 분기 수를 늘리지 않는다(gocyclo).
	if err := r.syncNodeLabels(ctx, &policy); err != nil {
		return ctrl.Result{}, err
	}

	// -- NVIDIA
	// delegated·external 이면 device-plugin 을 배포하지도(ensure) 회수하지도(delete) 않는다 —
	// kcloud 가 소유하지 않으므로 관측만 한다. 배포 여부는 IsOperatorOwned() 하나로 묻는다.
	if policy.Spec.Nvidia.Enabled && policy.Spec.Nvidia.IsOperatorOwned() {
		logger.Info("Ensuring NVIDIA Device Plugin DaemonSet")
		if err := r.ensureNvidiaDevicePlugin(ctx, &policy); err != nil {
			logger.Error(err, "failed to ensure NVIDIA Device Plugin")
			r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "NvidiaDevicePlugin", err)
			r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "NvidiaDevicePluginFailed", err.Error())
			return ctrl.Result{}, err
		}
	} else if policy.Spec.Nvidia.IsOperatorOwned() {
		// 토글 off 는 두 DaemonSet(mixed/flat)을 모두 회수해야 한다 — 하나만 지우면 남은 쪽이
		// 계속 광고한다. Furiosa·Tenstorrent·Rebellions 도 대칭이다: owner annotation 이 있는
		// DS 만 회수하고 3rd-party 동명 DS 는 보존한다. mixed 이름(nvidia.DevicePluginNameMixed)
		// 은 무중단 이관을 위해 기존 3rd-party 리소스와 일부러 같은 이름을 쓰므로
		// (timeslicing.go:148) 이름만 보고 지우면 한 번도 reconcile 하지 않은 DS 까지 지울 수
		// 있다 — enabled=true 로 한 번이라도 reconcile 되면 owner annotation 이 찍혀 정상 회수된다.
		for _, ds := range []struct{ name, reason string }{
			{nvidia.DevicePluginNameMixed, "NvidiaDevicePluginMixedRemoveFailed"},
			{nvidia.DevicePluginNameFlat, "NvidiaDevicePluginFlatRemoveFailed"},
		} {
			if err := r.deleteOwnedDaemonSetIfExists(ctx, ds.name, &policy); err != nil {
				logger.Error(err, "failed to remove NVIDIA Device Plugin", "daemonset", ds.name)
				r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to remove %s: %v", ds.name, err)
				r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, ds.reason, err.Error())
				return ctrl.Result{}, err
			}
		}
	}

	// -- 부가 operand(텔레메트리 exporter, DRA 드라이버). 둘 다 토글 off 면 회수하므로
	//    항상 호출한다. 광고 주체와 독립이라 실패해도 device-plugin 광고는 산다.
	if name, err := r.ensureSideOperands(ctx, &policy); err != nil {
		logger.Error(err, "failed to ensure side operand", "operand", name)
		r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", name, err)
		r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, name+"Failed", err.Error())
		return ctrl.Result{}, err
	}

	// -- Furiosa (warboy/rngd/unified). 분기가 많아 gocyclo 를 낮추려 별도 메서드로 분리.
	if err := r.reconcileFuriosaDevicePlugins(ctx, &policy); err != nil {
		return ctrl.Result{}, err
	}

	// -- Rebellions ATOM+ (기본 kube-system; 전용 네임스페이스 지정 시 생성 + PSA privileged; ClusterRole/Binding)
	// delegated·external 이면 배포하지도 회수하지도 않는다 — 관측만 한다.
	// 배포 여부는 여기서 한 번만 판정해 아래 준비 상태 조건 보고에도 쓴다 — 같은 식을 한 번 더 쓰면 gocyclo 한도를 넘는다.
	rbllnsManaged := policy.Spec.Rebellions.Enabled && policy.Spec.Rebellions.IsOperatorOwned()
	if rbllnsManaged {
		logger.Info("Ensuring Rebellions ATOM+ Device Plugin")
		for _, step := range []struct {
			name string
			fn   func(context.Context, *npuv1alpha1.NPUClusterPolicy) error
		}{
			{"RebellionsNamespace", r.ensureRbllnsNamespace},
			{"RebellionsServiceAccount", r.ensureRbllnsServiceAccount},
			{"RebellionsRBAC", r.ensureRbllnsRBAC},
			{"RebellionsConfigMap", r.ensureRbllnsConfigMap},
			{"RebellionsDevicePlugin", r.ensureRebellionsDevicePlugin},
		} {
			if err := step.fn(ctx, &policy); err != nil {
				logger.Error(err, "failed to ensure Rebellions step", "step", step.name)
				r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", step.name, err)
				r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, step.name+"Failed", err.Error())
				return ctrl.Result{}, err
			}
		}
	} else if policy.Spec.Rebellions.IsOperatorOwned() {
		// 비활성(managed): 소유 device-plugin DaemonSet 을 회수한다(광고 중단). SA/RBAC/CM/ns
		// 는 공유·저비용이라 남겨 둔다 — 광고를 멈추는 건 DaemonSet 회수로 충분하다.
		if err := r.deleteOwnedDaemonSetInIfExists(ctx, rbllnsResolveNamespace(&policy), rbllnsDaemonSetName, &policy); err != nil {
			logger.Error(err, "failed to remove Rebellions Device Plugin", "daemonset", rbllnsDaemonSetName)
			r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to remove %s: %v", rbllnsDaemonSetName, err)
			r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "RebellionsDevicePluginRemoveFailed", err.Error())
			return ctrl.Result{}, err
		}
	}

	// -- Tenstorrent Blackhole (single DaemonSet; nodeSelector=kcloud.ai/tenstorrent.present 자립 라벨, /sys hostPath 감지)
	// delegated·external 이면 배포하지도 회수하지도 않는다 — 관측만 한다.
	if policy.Spec.Tenstorrent.Enabled && policy.Spec.Tenstorrent.IsOperatorOwned() {
		logger.Info("Ensuring Tenstorrent Blackhole Device Plugin DaemonSet")
		if err := r.ensureTenstorrentDevicePlugin(ctx, &policy); err != nil {
			logger.Error(err, "failed to ensure Tenstorrent Device Plugin")
			r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "TenstorrentDevicePlugin", err)
			r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "TenstorrentDevicePluginFailed", err.Error())
			return ctrl.Result{}, err
		}
	} else if policy.Spec.Tenstorrent.IsOperatorOwned() {
		// 비활성(managed): 소유 DaemonSet 을 회수한다 — 안 지우면 광고가 계속 산다. DS 는
		// operator 네임스페이스에 살고, owner annotation 이 있는 것만 지운다(3rd-party 보존).
		if err := r.deleteOwnedDaemonSetInIfExists(ctx, naming.OperatorNamespace(), ttDaemonSetName, &policy); err != nil {
			logger.Error(err, "failed to remove Tenstorrent Device Plugin", "daemonset", ttDaemonSetName)
			r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to remove %s: %v", ttDaemonSetName, err)
			r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "TenstorrentDevicePluginRemoveFailed", err.Error())
			return ctrl.Result{}, err
		}
	}

	// -- All ensureXxx succeeded: set Ready=True and record success event.
	// external·delegated 벤더가 하나라도 있으면 "배포물이 없어도 정상"임을 사유로 가른다 —
	// 실패 경로는 모두 위에서 Ready=False 로 return 하므로, 여기 도달하면 관리 대상은 전부 준비된 상태다.
	readyReason, readyMessage := "AllResourcesReady", "All resources reconciled successfully"
	if ext, del := nonOperandVendorNames(&policy); len(ext)+len(del) > 0 {
		readyReason = "ExternalManaged"
		readyMessage = nonOperandReadyMessage(ext, del)
	}
	// managed 인 nvidia/rebellions/tenstorrent 옆에 그 벤더의 공개 operator 가 깔려 있으면
	// 조건·경고로 delegated 전환을 권고한다(자동 전환 없음, 노드만 관측하려면 external).
	// furiosa 는 감지 가능한 고유 CRD/라벨 시그니처가 없어 미확인(Unknown)
	// — vendoroperator_detect.go 상단 주석 참고.
	// 조건만 세팅하고 flush 는 아래 setReadyCondition 이 한 번에 한다.
	r.detectNvidiaGPUOperator(ctx, &policy)
	r.detectRebellionsOperator(ctx, &policy)
	r.detectTenstorrentOperator(ctx, &policy)
	// 벤더 5종 전부(rebellions 전용이던 것의 일반화) device-plugin·DRA 동시 광고 창을
	// top-level Condition 으로 남긴다 — advertiseBy·dra.enabled 값과 무관하게 실측만 본다.
	r.detectDualAdvertisement(ctx, &policy)
	// delegated 로 선언했는데 위임 대상(벤더 operator)이 감지되지 않으면(Absent) 경고한다 —
	// 조용히 managed 로 내려가 중복 배포하지 않고 그 상태를 드러낸다. Unknown 은 경고하지 않는다.
	r.warnDelegatedOperatorAbsent(ctx, &policy)
	// Rebellions device plugin 파드의 사전 점검·기동 상태를 조건으로 남긴다(조건만 세팅, flush 는 아래).
	// 꺼져 있거나 kcloud 가 배포하지 않는 모드면 조건을 없앤다.
	r.reportRebellionsDevicePlugin(ctx, &policy, rbllnsManaged)
	r.setReadyCondition(ctx, &policy, metav1.ConditionTrue, readyReason, readyMessage)
	r.Recorder.Eventf(&policy, corev1.EventTypeNormal, "Reconciled", "Successfully reconciled all resources")

	// DRA 드라이버가 아직 발행 전이면 다시 본다. 발행물은 드라이버가 뜬 뒤 몇 초 지나
	// 생기는데 이 컨트롤러는 ResourceSlice 를 watch 하지 않아, 재큐가 없으면 상태가
	// Installing 에 굳는다.
	return ctrl.Result{RequeueAfter: draRequeue(&policy)}, nil
}

// reconcileFuriosaDevicePlugins 는 Furiosa 의 device-plugin 을 spec 에 맞춘다.
// Unified=true 면 Warboy/RNGD 를 단일 통합 DS 로 전환하고 기존 2-DS 는 제거한다.
// Unified=false(기본) 면 기존 2-DS 경로 유지 + 통합 DS 제거(롤백).
// delegated·external 이면 warboy/rngd/unified 를 배포도 회수도 하지 않는다(RNGD 포함 —
// RngdSpec 에는 별도 관리 모드가 없고 Furiosa 의 모드를 따른다). 통합 전환·롤백 정리도 건너뛴다.
// 배포 여부는 IsOperatorOwned() 하나로 묻는다(managed 일 때만 참).
func (r *NPUClusterPolicyReconciler) reconcileFuriosaDevicePlugins(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	logger := logf.FromContext(ctx)
	furiosaManaged := policy.Spec.Furiosa.IsOperatorOwned()
	if furiosaManaged && policy.Spec.Furiosa.Enabled && policy.Spec.Furiosa.Unified {
		logger.Info("Ensuring Furiosa Unified Device Plugin DaemonSet")
		if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
			logger.Error(err, "failed to ensure Furiosa Unified Device Plugin")
			r.Recorder.Eventf(policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "FuriosaUnifiedDevicePlugin", err)
			r.setReadyCondition(ctx, policy, metav1.ConditionFalse, "FuriosaUnifiedDevicePluginFailed", err.Error())
			return err
		}
		// 전환: 기존 2-DS 제거(존재 시). 통합 DS 스케줄 후 정리해 순단 최소화.
		// 새 이름과 개명 전 이름을 모두 지우되, 이 policy 소유(owner annotation 일치)인 것만 지운다 —
		// 개명 전 이름(furiosa-device-plugin)은 벤더 일반 이름이라 3rd-party 가 같은 이름을 쓸 수 있다.
		for _, ref := range furiosaPerModelDSRefs() {
			if err := r.deleteOwnedDaemonSetInIfExists(ctx, ref.Namespace, ref.Name, policy); err != nil {
				logger.Error(err, "failed to delete per-model Furiosa DS during unified transition", "daemonset", ref.String())
			}
		}
		return nil
	}
	if !furiosaManaged {
		return nil
	}
	r.cleanupDisabledFuriosaLegacyDS(ctx, policy)
	if policy.Spec.Furiosa.Enabled {
		logger.Info("Ensuring Furiosa Device Plugin DaemonSet")
		if err := r.ensureFuriosaDevicePlugin(ctx, policy); err != nil {
			logger.Error(err, "failed to ensure Furiosa Device Plugin")
			r.Recorder.Eventf(policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "FuriosaDevicePlugin", err)
			r.setReadyCondition(ctx, policy, metav1.ConditionFalse, "FuriosaDevicePluginFailed", err.Error())
			return err
		}
	}
	// -- Furiosa RNGD (second-gen; separate DS, NFD-based node affinity)
	if policy.Spec.Furiosa.Rngd.Enabled {
		logger.Info("Ensuring Furiosa RNGD Device Plugin DaemonSet")
		if err := r.ensureFuriosaRngdDevicePlugin(ctx, policy, policy.Spec.Furiosa.Rngd.PartitionPolicy); err != nil {
			logger.Error(err, "failed to ensure Furiosa RNGD Device Plugin")
			r.Recorder.Eventf(policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "FuriosaRngdDevicePlugin", err)
			r.setReadyCondition(ctx, policy, metav1.ConditionFalse, "FuriosaRngdDevicePluginFailed", err.Error())
			return err
		}
	}
	r.rollbackFuriosaUnified(ctx, policy)
	return nil
}

// syncNodeLabels 는 DaemonSet 렌더링 전에 노드 라벨을 정책과 맞추는 두 축을 순서대로
// 적용한다: 광고 주체 전환(reconcileDRAOwnedLabels), 배제 노드 표시
// (reconcileNodeExclusionLabels, node_exclusion.go). 실패한 축에 맞는 이벤트·Ready 조건을
// 여기서 기록해, Reconcile 은 단일 분기로 두 축을 함께 처리하고 분기 수를 늘리지 않는다(gocyclo).
func (r *NPUClusterPolicyReconciler) syncNodeLabels(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	logger := logf.FromContext(ctx)
	if err := r.reconcileDRAOwnedLabels(ctx, policy); err != nil {
		logger.Error(err, "DRA 소유 라벨 동기화 실패")
		r.Recorder.Eventf(policy, corev1.EventTypeWarning, "AdvertiseSwitchFailed", "%v", err)
		r.setReadyCondition(ctx, policy, metav1.ConditionFalse, "AdvertiseSwitchFailed", err.Error())
		return err
	}
	if err := r.reconcileNodeExclusionLabels(ctx, policy); err != nil {
		logger.Error(err, "배제 노드 라벨 동기화 실패")
		r.Recorder.Eventf(policy, corev1.EventTypeWarning, "NodeExclusionLabelFailed", "%v", err)
		r.setReadyCondition(ctx, policy, metav1.ConditionFalse, "NodeExclusionLabelFailed", err.Error())
		return err
	}
	return nil
}

// draRequeue 는 발행 관측을 기다리는 DRA 드라이버가 있으면 재확인 간격을 준다.
// 0 이면 재큐하지 않는다. 이 컨트롤러는 ResourceSlice 를 watch 하지 않아, 재큐가
// 없으면 발행이 생겨도 상태가 Installing 에 굳는다.
func draRequeue(policy *npuv1alpha1.NPUClusterPolicy) time.Duration {
	for _, d := range policy.Status.DRADrivers {
		if d.Phase == npuv1alpha1.DRAPhaseInstalling {
			return 30 * time.Second
		}
	}
	return 0
}

// nonOperandVendorNames 는 Enabled 이면서 kcloud 가 데이터플레인을 배포하지 않는 모드
// (delegated 또는 external)인 벤더 이름을 모드별로 모은다. 두 모드 모두 배포물이 없어도
// 정상이라 Ready 사유를 ExternalManaged 로 가르는 근거다(사유 이름은 과거 릴리스 판정·
// 운영 문서가 그대로 인용하므로 유지한다). RNGD 는 Furiosa 의 모드를 따르므로 furiosa 로만
// 표기한다.
func nonOperandVendorNames(policy *npuv1alpha1.NPUClusterPolicy) (external, delegated []string) {
	s := policy.Spec
	classify := func(enabled, isExternal, isDelegated bool, name string) {
		switch {
		case !enabled:
		case isExternal:
			external = append(external, name)
		case isDelegated:
			delegated = append(delegated, name)
		}
	}
	classify(s.Nvidia.Enabled, s.Nvidia.IsExternal(), s.Nvidia.IsDelegated(), vendorNvidia)
	classify(s.Furiosa.Enabled || s.Furiosa.Rngd.Enabled, s.Furiosa.IsExternal(), s.Furiosa.IsDelegated(), vendorFuriosa)
	classify(s.Rebellions.Enabled, s.Rebellions.IsExternal(), s.Rebellions.IsDelegated(), vendorRebellions)
	classify(s.Tenstorrent.Enabled, s.Tenstorrent.IsExternal(), s.Tenstorrent.IsDelegated(), vendorTenstorrent)
	return external, delegated
}

// nonOperandReadyMessage 는 데이터플레인이 없는 벤더 명단을 external·delegated 로 갈라 한 줄로
// 알린다. 두 모드는 배포하지 않는다는 결과는 같지만 이유가 달라(external=kcloud 밖 설치를 관측만,
// delegated=벤더 operator 에 위임) 어느 벤더가 어느 쪽인지 이름으로 남긴다.
func nonOperandReadyMessage(external, delegated []string) string {
	var groups []string
	if len(external) > 0 {
		groups = append(groups, "external: "+strings.Join(external, ", "))
	}
	if len(delegated) > 0 {
		groups = append(groups, "delegated: "+strings.Join(delegated, ", "))
	}
	return fmt.Sprintf("no data plane deployed for vendors observed only (%s)", strings.Join(groups, "; "))
}

// setReadyCondition updates the Ready condition on the policy status.
func (r *NPUClusterPolicyReconciler) setReadyCondition(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: policy.Generation,
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, policy); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update NPUClusterPolicy status")
	}
}

// ownedScanNamespaces 는 owner-annotation 소유 리소스를 스캔할 네임스페이스 목록을 반환한다.
// device-plugin(kube-system) + operator 부속(detector, kcloud)이 서로 다른 ns 에 존재하므로,
// 이관 과도기·정상 모두 양쪽을 스캔해야 orphan 이 남지 않는다. env 미설정(둘이 동일)이면 1개.
func ownedScanNamespaces() []string {
	opNS := naming.OperatorNamespace()
	if opNS == naming.KubeSystemNamespace {
		return []string{naming.KubeSystemNamespace}
	}
	return []string{naming.KubeSystemNamespace, opNS}
}

// isDevicePluginResource 는 리소스 이름으로 3rd-party device-plugin(nvidia/furiosa/rngd/tt/rbln)을 식별한다.
// 5종 DS/CM 이름이 모두 "device-plugin" 을 포함(operator 관리 driver/toolkit/detector 는 미포함).
// ponytail: 이름 기반. 이름 규약이 갈라지면 생성 시 보존 어노테이션(npu.ai/preserve)으로 승격.
func isDevicePluginResource(name string) bool {
	return strings.Contains(name, "device-plugin")
}

// cleanupOwnedResources deletes all DaemonSets and ConfigMaps with the owner annotation matching this policy.
// device-plugin(3rd party)은 보존한다(무중단 이관 — isDevicePluginResource).
// 배제 라벨(kcloud.ai/excluded·excluded-reason)도 이 정책이 남긴 것이라 함께 회수한다.
func (r *NPUClusterPolicyReconciler) cleanupOwnedResources(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	ownerValue := fmt.Sprintf("%s/%s", policy.Namespace, policy.Name)

	for _, ns := range ownedScanNamespaces() {
		// Cleanup DaemonSets
		var dsList appsv1.DaemonSetList
		if err := r.List(ctx, &dsList, client.InNamespace(ns)); err != nil {
			return err
		}
		for i := range dsList.Items {
			ds := &dsList.Items[i]
			// device-plugin(3rd party, kube-system 고정)은 무중단 이관을 위해 보존 —
			// NCP 삭제(operator 이관/제거) 시 device-plugin 을 지우면 전벤더 allocatable 가 순단된다.
			if ds.Annotations[ownerAnnotation] == ownerValue && !isDevicePluginResource(ds.Name) {
				if err := r.Delete(ctx, ds); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
			}
		}

		// Cleanup ConfigMaps
		var cmList corev1.ConfigMapList
		if err := r.List(ctx, &cmList, client.InNamespace(ns)); err != nil {
			return err
		}
		for i := range cmList.Items {
			cm := &cmList.Items[i]
			// device-plugin CM 도 보존(DS 가 참조하므로 함께 남긴다).
			if cm.Annotations[ownerAnnotation] == ownerValue && !isDevicePluginResource(cm.Name) {
				if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
			}
		}
	}

	// DRA 소유·후퇴 라벨 회수. 정책이 사라지면 광고 주체도 없다.
	if err := r.reclaimDRANodeLabels(ctx, policy); err != nil {
		return err
	}
	// 배제 라벨 회수. 부여 로직을 복제하지 않고 policy=nil 로 같은 함수를 돌린다 —
	// nodeExclusion 이 nil 을 "배제 없음" 으로 판정하므로 control-plane 사유까지 지워진다.
	// 배포가 없으면 배제도 없다: 정책이 사라진 클러스터에서 "제외됨(control-plane)" 이
	// 남으면 CLI·콘솔이 없는 정책을 보고하고 detector 는 그 노드 검증을 계속 접는다.
	return r.reconcileNodeExclusionLabels(ctx, nil)
}

// ensureSideOperands 는 광고 경로 밖의 operand 를 순서대로 맞춘다. 실패한 operand
// 이름을 함께 돌려주어 호출부가 사유를 그대로 기록하게 한다.
func (r *NPUClusterPolicyReconciler) ensureSideOperands(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy,
) (string, error) {
	for _, op := range []struct {
		name   string
		ensure func(context.Context, *npuv1alpha1.NPUClusterPolicy) error
	}{
		{"DcgmExporter", r.ensureDcgmExporter},
		{"DRADriver", r.ensureDRADriver},
	} {
		if err := op.ensure(ctx, policy); err != nil {
			return op.name, err
		}
	}
	return "", nil
}

// setOwnerAnnotation sets the npu.ai/owner annotation on the given ObjectMeta.
func setOwnerAnnotation(obj *metav1.ObjectMeta, policy *npuv1alpha1.NPUClusterPolicy) {
	if obj.Annotations == nil {
		obj.Annotations = map[string]string{}
	}
	obj.Annotations[ownerAnnotation] = fmt.Sprintf("%s/%s", policy.Namespace, policy.Name)
}

// kcloud-node-manager ServiceAccount 상수. Namespace 는 operator 부속으로 kcloud 를 쓴다 —
// naming.OperatorNamespace()(OPERATOR_NAMESPACE env, 미설정 시 kube-system)를 SA/DS 에 공통 사용.
const detectorServiceAccountName = "kcloud-node-manager"

// applyImagePullSecrets 는 policy 레벨 imagePullSecrets 를 pod spec 에 부착한다.
// 빈 목록(미지정)이면 no-op — 노드레벨(containerd) 인증 경로(하위호환)를 유지한다.
// imagePullPolicy 는 건드리지 않는다(air-gap 프리로드용 IfNotPresent 불변식 보존).
func applyImagePullSecrets(spec *corev1.PodSpec, secrets []corev1.LocalObjectReference) {
	if len(secrets) > 0 {
		spec.ImagePullSecrets = secrets
	}
}

// -- ensureNvidiaDevicePlugin creates the mixed(MIG 관리 노드) 와 flat(그 외) device-plugin
// DaemonSet 두 개를 렌더한다. mixed 는 --mig-strategy=mixed 로 MIG 프로파일을, flat 은 그
// 플래그 없이 순정 GPU 를 광고하며 MigActiveNodeLabel 부재 노드만 맡는다.
func (r *NPUClusterPolicyReconciler) ensureNvidiaDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	// 기본 selector (자립 라벨 — node-manager 부여, NFD/gpu-operator 비의존)
	sel := map[string]string{vendorPresentLabel(vendorNvidia): labelValueTrue}
	if len(policy.Spec.Nvidia.NodeSelector) > 0 {
		sel = policy.Spec.Nvidia.NodeSelector
	}

	mixed := r.buildNvidiaDevicePluginDS(policy, nvidia.DevicePluginNameMixed, sel, true)
	flat := r.buildNvidiaDevicePluginDS(policy, nvidia.DevicePluginNameFlat, sel, false)

	for _, ds := range []*appsv1.DaemonSet{mixed, flat} {
		setOwnerAnnotation(&ds.ObjectMeta, policy)
		applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
		applyControlPlaneExclusion(&ds.Spec.Template.Spec)
		applyExcludeNodeSelector(&ds.Spec.Template.Spec, policy.Spec.Nvidia.ExcludeNodeSelector)
		applyImagePullSecrets(&ds.Spec.Template.Spec, policy.Spec.ImagePullSecrets)

		// 두 writer 조정: ACPP 가 sharing config 를 소유하면 live 배선을 보존한다.
		var live appsv1.DaemonSet
		if err := r.Get(ctx, types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, &live); err == nil {
			preserveNvidiaSharing(&live, ds)
		}
		if err := r.createOrUpdateDS(ctx, ds); err != nil {
			log.Error(err, "failed to ensure nvidia device plugin daemonset", "name", ds.Name)
			return err
		}
	}
	log.Info("NVIDIA device plugin daemonsets ensured (mixed + flat)")
	return nil
}

// buildNvidiaDevicePluginDS 는 mixed(MIG 관리 노드 전용) 또는 flat(그 외) device-plugin
// DaemonSet 정의를 만든다. selector 는 immutable 이라 mixed(라이브 기존 리소스와 같은 이름)의
// selector 는 과거 값 {app.kubernetes.io/name: nvidia-device-plugin} 그대로 둔다 — 바꾸면 기존
// 클러스터에서 422 로 영구 실패한다. flat 은 자기 이름을 selector 값으로 써 자동으로 비겹침이다.
// 두 DS 는 nvidiaDevicePluginVendorLabel 을 공통으로 달아 restartNvidiaDevicePlugin 의 노드별
// pod 삭제가 어느 쪽이든 찾는다.
func (r *NPUClusterPolicyReconciler) buildNvidiaDevicePluginDS(policy *npuv1alpha1.NPUClusterPolicy, name string, baseSel map[string]string, mixed bool) *appsv1.DaemonSet {
	labels := map[string]string{"app.kubernetes.io/name": name, nvidiaDevicePluginVendorLabel: "nvidia"}
	nvidiaRuntime := vendorNvidia
	args := []string{}
	if mixed {
		// <nvidia-node> 은 A30(MIG 가능)+A2(비 MIG) 혼재 노드라 single 전략은 무효.
		// mixed 는 A30 에 nvidia.com/mig-<profile> 을, A2 에는 기존 nvidia.com/gpu 를 광고한다.
		args = []string{"--mig-strategy=mixed"}
	}

	spec := corev1.PodSpec{
		RuntimeClassName: &nvidiaRuntime,
		Tolerations:      []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
		Containers: []corev1.Container{{
			Name:            "nvidia-device-plugin",
			Image:           policy.Spec.Nvidia.DevicePluginImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Args:            args,
			Env: []corev1.EnvVar{
				{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"},
				{Name: "NVIDIA_DRIVER_CAPABILITIES", Value: "all"},
				// MIG(mixed) 조각 메모리/용량 조회는 /dev/nvidia-caps 접근이 필요하다.
				// MIG_MONITOR_DEVICES=all 로 nvidia 런타임이 caps 디바이스를 주입하게 한다.
				{Name: "NVIDIA_MIG_MONITOR_DEVICES", Value: "all"},
			},
			// MIG 조각 조회는 특권이 필요하다("Insufficient Permissions" 회피). NVIDIA GPU
			// Operator 의 device-plugin 도 MIG 에서 privileged 로 동작한다.
			SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
			VolumeMounts:    []corev1.VolumeMount{{Name: "device-plugin", MountPath: "/var/lib/kubelet/device-plugins"}},
		}},
		Volumes: []corev1.Volume{{
			Name:         "device-plugin",
			VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"}},
		}},
	}

	if mixed {
		merged := map[string]string{nvidia.MigActiveNodeLabel: "true"}
		for k, v := range baseSel {
			merged[k] = v
		}
		spec.NodeSelector = merged
	} else {
		keys := make([]string, 0, len(baseSel))
		for k := range baseSel {
			keys = append(keys, k)
		}
		sort.Strings(keys) // map 순회는 무작위 — 정렬 없으면 매 reconcile 마다 렌더가 달라져 DS 가 롤링 재시작한다.
		var terms []corev1.NodeSelectorRequirement
		for _, k := range keys {
			terms = append(terms, corev1.NodeSelectorRequirement{Key: k, Operator: corev1.NodeSelectorOpIn, Values: []string{baseSel[k]}})
		}
		terms = append(terms, corev1.NodeSelectorRequirement{Key: nvidia.MigActiveNodeLabel, Operator: corev1.NodeSelectorOpDoesNotExist})
		spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: terms}},
				},
			},
		}
	}

	// mixed 는 nodeSelector 로, flat 은 affinity 로 노드를 고른다. dra-owned 제외는 둘 다
	// affinity 로 건다 — 한쪽만 빼면 그 DaemonSet 이 DRA 소유 노드에서 계속 광고한다.
	excludeDRAOwnedNodes(&spec, vendorNvidia)

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kube-system", Labels: labels},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       spec,
			},
		},
	}
}

// preserveNvidiaSharing 는 live DS 가 ACPP 소유(sharing owner annotation)면 desired 에 배선을 복원한다.
// RNGD 의 RNGD_PARTITION_POLICY 보존과 동일한 2-writer 조정 규칙이다.
func preserveNvidiaSharing(live, desired *appsv1.DaemonSet) {
	owner, owned := live.Annotations[nvidia.SharingOwnerAnnotation]
	if !owned {
		return
	}
	if desired.Annotations == nil {
		desired.Annotations = map[string]string{}
	}
	desired.Annotations[nvidia.SharingOwnerAnnotation] = owner
	mode := npuv1alpha1.SharingModeTimeSliced
	if nvidia.HasMPSVolume(live) {
		mode = npuv1alpha1.SharingModeMPS
	}
	nvidia.WireSharing(desired, mode, nvidia.SharingConfigMapNameOf(live))
}

// -- ensureFuriosaDevicePlugin creates a DaemonSet for Furiosa
func (r *NPUClusterPolicyReconciler) ensureFuriosaDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	// nodeSelector (자립 라벨 — node-manager 부여, NFD/수동 라벨 비의존)
	sel := map[string]string{vendorPresentLabel(vendorFuriosa): labelValueTrue}
	if len(policy.Spec.Furiosa.NodeSelector) > 0 {
		sel = policy.Spec.Furiosa.NodeSelector
	}

	// ConfigMap (옵션)
	if policy.Spec.Furiosa.ConfigMapName != "" {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      policy.Spec.Furiosa.ConfigMapName,
				Namespace: furiosaVendorDSNamespace(),
			},
		}
		setOwnerAnnotation(&cm.ObjectMeta, policy)
		cm.Data = map[string]string{
			"config.yaml": `defaultPe: Fusion
disabledDevices: []
interval: 10`,
		}
		if err := r.createOrUpdateCM(ctx, cm); err != nil {
			log.Error(err, "failed to ensure furiosa device plugin configmap")
			return err
		}
	}

	// 개명 정리: 같은 DS 의 옛 이름을 먼저 지운다. DS selector 가 immutable 이라 이름을
	// 바꾼 자리는 새로 만들 수밖에 없고, 옛 DS 가 남은 채 새 DS 가 뜨면 두 파드가 같은
	// 자원을 kubelet 에 중복 등록한다. 그래서 실패하면 새 DS 를 만들지 않고 물러난다.
	// 이 policy 소유(owner annotation 일치)인 것만 지운다 — 개명 전 이름(furiosa-device-plugin)은
	// 벤더 일반 이름이라 3rd-party 동명 DS 를 이름만으로 지우면 안 된다(그 경우 external 로 둔다).
	for _, ref := range []types.NamespacedName{
		{Namespace: naming.KubeSystemNamespace, Name: furiosaWarboyLegacyDSName},
		{Namespace: naming.OperatorNamespace(), Name: furiosaWarboyV0727DSName},
	} {
		if err := r.deleteOwnedDaemonSetInIfExists(ctx, ref.Namespace, ref.Name, policy); err != nil {
			log.Error(err, "failed to delete pre-rename Warboy DS before creating new one", "name", ref.Name)
			return err
		}
	}

	labels := map[string]string{"app.kubernetes.io/name": furiosaWarboyDSName}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      furiosaWarboyDSName,
			Namespace: furiosaVendorDSNamespace(),
			Labels:    labels,
		},
	}
	setOwnerAnnotation(&ds.ObjectMeta, policy)
	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				NodeSelector: sel,
				Tolerations:  []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
				Containers: []corev1.Container{{
					Name:            furiosaPluginContainerName,
					Image:           policy.Spec.Furiosa.DevicePluginImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"/usr/bin/k8s-device-plugin"},
					Args:            []string{"--config-file", "/etc/furiosa/config.yaml"},
					Env: []corev1.EnvVar{
						{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
						}},
						{Name: "RUST_LOG", Value: "info"},
					},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: boolPtr(false),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "sys", MountPath: "/sys"},
						{Name: "dev", MountPath: "/dev"},
						{Name: "dp", MountPath: "/var/lib/kubelet/device-plugins"},
						// ConfigMap이 있을 때만 마운트
						// (없으면 이 항목은 빼기)
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
					{Name: "dev", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
					{Name: "dp", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"}}},
				},
			},
		},
	}

	if policy.Spec.Furiosa.ConfigMapName != "" {
		// CM 마운트 추가
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: policy.Spec.Furiosa.ConfigMapName},
					},
				},
			},
		)
		ds.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			ds.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "config", MountPath: "/etc/furiosa"},
		)
	}

	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
	excludeDRAOwnedNodes(&ds.Spec.Template.Spec, vendorFuriosa)
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)
	applyExcludeNodeSelector(&ds.Spec.Template.Spec, policy.Spec.Furiosa.ExcludeNodeSelector)
	applyImagePullSecrets(&ds.Spec.Template.Spec, policy.Spec.ImagePullSecrets)

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure furiosa device plugin daemonset")
		return err
	}
	log.Info("Furiosa device plugin daemonset ensured")
	return nil
}

// rngdDevicePluginArgs returns the binary args for the Furiosa RNGD device plugin DaemonSet.
// 빈 문자열 또는 "none" 이면 --policy flag 를 안 붙인다. 그 외(single-core/dual-core/quad-core)
// 는 `--policy=<value>` 를 append 한다(libfuriosa-kubernetes PartitioningPolicy enum 과 1:1).
// 벤더 이미지 세대마다 받는 flag 가 다르다 — 내부 파티션 지원 빌드는 --debugMode·--policy 를
// 모두 받지만 공개 ghcr.io/furiosa-ai/furiosa-device-plugin 은 둘 다 unknown flag 로 거부해
// 죽는다. 그래서 둘 다 끄면 args 를 비운다 — 비-none 정책은 파티션 지원 이미지가 필요하다.
func rngdDevicePluginArgs(partitionPolicy string, debugMode *bool) []string {
	var args []string
	// debugMode 미지정(nil)은 켠 것으로 읽는다 — 기존 배포의 동작을 그대로 둔다.
	if debugMode == nil || *debugMode {
		args = append(args, "--debugMode")
	}
	if partitionPolicy != "" && partitionPolicy != "none" {
		args = append(args, "--policy="+partitionPolicy)
	}
	return args
}

// furiosaRngdDevicePluginImageDefault 는 spec.furiosa.rngd.devicePluginImage 가 비었을 때 쓰는 공개
// 벤더 이미지다. 차트 기본값(furiosa.rngd.devicePlugin*)과 같아야 한다(chart_default_images_test.go).
const furiosaRngdDevicePluginImageDefault = "docker.io/furiosaai/furiosa-device-plugin:2026.1.1"

// -- ensureFuriosaRngdDevicePlugin creates a DaemonSet for the Furiosa RNGD (2nd-gen) NPU device
// plugin. NodeSelector 는 자립 라벨 kcloud.ai/rngd.present=true 가 기본이며 override 는
// Spec.Furiosa.Rngd.NodeSelector 다. entrypoint ./main 은 PCI scan 으로 장치를 자동 인식해
// --resource-name 류 인자가 불필요하다(ResourceName/ConfigMapName CRD 필드는 공식 이미지가
// 자동 처리해 참조하지 않는다 — backward-compat). upstream 이미지(2026.1.x)는 --debugMode 만 받고
// --policy 는 unknown flag 로 거부해 기동이 실패하므로, partitionPolicy≠none 에는 custom 이미지가 필요하다.
func (r *NPUClusterPolicyReconciler) ensureFuriosaRngdDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, partitionPolicy string) error {
	log := logf.FromContext(ctx)

	rngdSpec := policy.Spec.Furiosa.Rngd

	// Image (default to upstream release tag when unset; registry path 은 `furiosaai`, 하이픈 없음)
	image := rngdSpec.DevicePluginImage
	if image == "" {
		image = furiosaRngdDevicePluginImageDefault
	}

	// nodeSelector: 자립 라벨(node-manager 부여, NFD 비의존); override via Spec.Furiosa.Rngd.NodeSelector
	sel := map[string]string{vendorPresentLabel(vendorRngd): labelValueTrue}
	if len(rngdSpec.NodeSelector) > 0 {
		sel = rngdSpec.NodeSelector
	}

	// 개명 정리: 같은 DS 의 옛 이름을 먼저 지운다. DS selector 가 immutable 이라 이름을
	// 바꾼 자리는 새로 만들 수밖에 없고, 옛 DS 가 남은 채 새 DS 가 뜨면 두 파드가 같은
	// 자원을 kubelet 에 중복 등록한다. 그래서 실패하면 새 DS 를 만들지 않고 물러난다.
	if err := r.deleteDaemonSetIn(ctx, naming.OperatorNamespace(), furiosaRngdV0727DSName); err != nil {
		log.Error(err, "failed to delete pre-rename RNGD DS before creating new one")
		return err
	}

	labels := map[string]string{"app.kubernetes.io/name": furiosaRngdDSName}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      furiosaRngdDSName,
			Namespace: furiosaVendorDSNamespace(),
			Labels:    labels,
		},
	}
	setOwnerAnnotation(&ds.ObjectMeta, policy)
	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				NodeSelector:      sel,
				Tolerations:       []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
				PriorityClassName: "system-node-critical",
				Containers: []corev1.Container{{
					Name:            furiosaPluginContainerName,
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"./main"},
					// --debugMode 가 없으면 기본 모드가 "couldn't recognize any furiosa devices" 로
					// 종료한다. partitionPolicy 가 비거나 "none" 이면 --policy 를 안 붙이고, 그 외는
					// partition-aware 이미지가 필요하다(ensureFuriosaRngdDevicePlugin 주석 참고).
					Args: rngdDevicePluginArgs(partitionPolicy, policy.Spec.Furiosa.Rngd.DebugMode),
					Env: []corev1.EnvVar{
						{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
						}},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged:               boolPtr(false),
						AllowPrivilegeEscalation: boolPtr(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "kubelet-socket", MountPath: "/var/lib/kubelet/device-plugins"},
						{Name: "dev-fs", MountPath: "/dev"},
						{Name: "sys-fs", MountPath: "/sys"},
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "kubelet-socket", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"}}},
					{Name: "dev-fs", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
					{Name: "sys-fs", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
				},
			},
		},
	}

	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
	excludeDRAOwnedNodes(&ds.Spec.Template.Spec, vendorRngd)
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)
	applyExcludeNodeSelector(&ds.Spec.Template.Spec, policy.Spec.Furiosa.Rngd.ExcludeNodeSelector)
	applyImagePullSecrets(&ds.Spec.Template.Spec, policy.Spec.ImagePullSecrets)

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure furiosa rngd device plugin daemonset")
		return err
	}
	log.Info("Furiosa RNGD device plugin daemonset ensured")
	return nil
}

// rollbackFuriosaUnified 는 furiosaUnified=false 로 되돌아간 정책이 남긴 통합 경로 자원을 회수한다.
// 레거시 2-DS 경로가 도는 중이므로 하나가 실패해도 나머지를 마저 지운다 — 실패를 올리면 그 회차의
// 2-DS 보장이 통째로 중단되고, 그 사이 광고가 비는 쪽이 더 나쁘다.
//
// Reconcile 안에 인라인으로 두면 이 함수의 분기가 그대로 Reconcile 의 순환 복잡도에 실린다
// (gocyclo 임계 30 을 이 블록이 넘겼다).
func (r *NPUClusterPolicyReconciler) rollbackFuriosaUnified(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) {
	logger := logf.FromContext(ctx)
	// 새 자리와 옛 자리 둘 다 확인한다 — 이름·네임스페이스를 바꾼 회차가 섞여 있다.
	if err := r.deleteDaemonSetIn(ctx, furiosaUnifiedDSNamespace(), furiosaUnifiedDSName); err != nil {
		logger.Error(err, "failed to delete unified Furiosa DS during rollback")
	}
	if err := r.deleteDaemonSetIn(ctx, naming.KubeSystemNamespace, furiosaUnifiedLegacyDSName); err != nil {
		logger.Error(err, "failed to delete legacy unified Furiosa DS during rollback")
	}
	// 통합 경로가 만든 ConfigMap 도 같이 회수 — 안 그러면 고아로 남는다. 모델별 Warboy 경로의
	// 동명 ConfigMap 은 kube-system 에 있어 여기서 지우는 대상이 아니다.
	cmName := policy.Spec.Furiosa.ConfigMapName
	if cmName == "" {
		return
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: furiosaUnifiedDSNamespace()}}
	if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "failed to delete unified Furiosa configmap during rollback")
	}
}

// cleanupDisabledFuriosaLegacyDS 는 Warboy·RNGD 가 꺼진 상태에서 개명 전 이름의 DS 를 지운다.
// ensure 경로는 켜진 쪽만 돌기 때문에 꺼진 쪽의 옛 DS 는 여기서만 정리된다. 실패는 기록만 한다.
func (r *NPUClusterPolicyReconciler) cleanupDisabledFuriosaLegacyDS(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) {
	logger := logf.FromContext(ctx)
	var refs []types.NamespacedName
	if !policy.Spec.Furiosa.Enabled {
		refs = append(refs,
			types.NamespacedName{Namespace: naming.KubeSystemNamespace, Name: furiosaWarboyLegacyDSName},
			types.NamespacedName{Namespace: naming.OperatorNamespace(), Name: furiosaWarboyV0727DSName})
	}
	if !policy.Spec.Furiosa.Rngd.Enabled {
		refs = append(refs, types.NamespacedName{Namespace: naming.OperatorNamespace(), Name: furiosaRngdV0727DSName})
	}
	for _, ref := range refs {
		if err := r.deleteOwnedDaemonSetInIfExists(ctx, ref.Namespace, ref.Name, policy); err != nil {
			logger.Error(err, "failed to delete pre-rename Furiosa DS while disabled", "daemonset", ref.String())
		}
	}
	// 현재 이름 DaemonSet 도 회수한다 — 비활성 벤더의 소유 DS 가 남으면 광고가 계속 산다.
	// legacy·현재 이름 모두 owner annotation 이 이 policy 와 맞는 것만 지운다 — 개명 전 이름
	// (furiosa-device-plugin)은 벤더 일반 이름이라 3rd-party 동명 DS 를 보존해야 한다.
	// external 은 이 함수에 오지 않는다(reconcileFuriosaDevicePlugins 가 먼저 건너뛴다).
	if !policy.Spec.Furiosa.Enabled {
		if err := r.deleteOwnedDaemonSetInIfExists(ctx, furiosaVendorDSNamespace(), furiosaWarboyDSName, policy); err != nil {
			logger.Error(err, "failed to remove disabled Furiosa warboy DS", "daemonset", furiosaWarboyDSName)
		}
	}
	if !policy.Spec.Furiosa.Rngd.Enabled {
		if err := r.deleteOwnedDaemonSetInIfExists(ctx, furiosaVendorDSNamespace(), furiosaRngdDSName, policy); err != nil {
			logger.Error(err, "failed to remove disabled Furiosa rngd DS", "daemonset", furiosaRngdDSName)
		}
	}
}

// deleteDaemonSetIn 은 지정한 네임스페이스의 DaemonSet 을 이름으로 지운다. 없으면 조용히 넘어간다.
func (r *NPUClusterPolicyReconciler) deleteDaemonSetIn(ctx context.Context, ns, name string) error {
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	if err := r.Delete(ctx, ds); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// deleteOwnedDaemonSetIfExists 는 owner annotation 이 이 policy 와 일치할 때만 kube-system 의
// DS 를 지운다. nvidia device-plugin·dcgm-exporter·furiosa-device-plugin 처럼 벤더 일반 이름을
// 쓰는 DS 전용 — 이 operator 가 한 번도 reconcile 하지 않아 owner annotation 이 없는 3rd-party
// DS 는 이름이 같아도 건드리지 않는다.
func (r *NPUClusterPolicyReconciler) deleteOwnedDaemonSetIfExists(ctx context.Context, name string, policy *npuv1alpha1.NPUClusterPolicy) error {
	return r.deleteOwnedDaemonSetInIfExists(ctx, "kube-system", name, policy)
}

// deleteOwnedDaemonSetInIfExists 는 주어진 네임스페이스의 DaemonSet 이 이 policy 소유
// (owner annotation 일치)일 때만 지운다. device-plugin 이 kube-system 밖(operator
// 네임스페이스·벤더 네임스페이스)에 사는 벤더도 회수하려 네임스페이스를 인자로 받는다.
// 소유 annotation 이 없는 3rd-party DS 는 이름이 같아도 보존한다.
func (r *NPUClusterPolicyReconciler) deleteOwnedDaemonSetInIfExists(ctx context.Context, namespace, name string, policy *npuv1alpha1.NPUClusterPolicy) error {
	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &ds); err != nil {
		return client.IgnoreNotFound(err)
	}
	if got := ds.Annotations[ownerAnnotation]; got != fmt.Sprintf("%s/%s", policy.Namespace, policy.Name) {
		// 소유 표시가 없거나 다른 DaemonSet 은 보존한다 — 안전 분기가 조용히 지나가지 않도록 기록한다.
		logf.FromContext(ctx).V(1).Info("소유 표시가 다른 DaemonSet 은 보존한다",
			"namespace", namespace, "name", name, "owner", got)
		return nil
	}
	if err := r.Delete(ctx, &ds); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// -- ensureFuriosaUnifiedDevicePlugin creates a single DaemonSet serving both Warboy and RNGD.
// 동봉 이미지 entrypoint 가 PCI vendor 0x1ed2 + device ID(0x0000=Warboy, 0x0001=RNGD)로 모델을 감지해
// 해당 벤더 바이너리를 exec 한다(proxy 없음, 노드당 한 모델). 리소스명은 각 바이너리가 광고하므로 불변
// (Warboy=beta.furiosa.ai/npu, RNGD=furiosa.ai/rngd). nodeSelector 는 양 노드 공통 PCI vendor 라벨.
// Warboy config 는 ConfigMapName(있으면 /etc/furiosa 마운트), RNGD partition 정책은 Rngd.PartitionPolicy
// 를 RNGD_PARTITION_POLICY env 로 전달한다.
func (r *NPUClusterPolicyReconciler) ensureFuriosaUnifiedDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	// 옛 자리(kube-system)의 통합 DS 를 먼저 치운다. 두 DS 가 같은 노드에서 같은 자원을
	// 등록하면 kubelet 등록이 충돌한다. 이름·네임스페이스를 바꾼 회차에만 실제로 지워진다.
	// 실패 시 여기서 return err 로 새 DS 생성을 막는다 — 옛 DS 를 못 지운 채 새 DS 를 만들면
	// 두 DS 가 같은 자원(beta.furiosa.ai/npu, furiosa.ai/rngd)을 중복 등록해 kubelet 이 충돌한다.
	if err := r.deleteDaemonSetIn(ctx, naming.KubeSystemNamespace, furiosaUnifiedLegacyDSName); err != nil {
		log.Error(err, "failed to delete legacy unified Furiosa DS before creating new one")
		return err
	}

	image := policy.Spec.Furiosa.UnifiedDevicePluginImage
	if image == "" {
		image = furiosaUnifiedImgDefault
	}

	// Warboy config ConfigMap (옵션) — 통합 이미지의 Warboy 바이너리가 --config-file 로 참조.
	cmName := policy.Spec.Furiosa.ConfigMapName
	if cmName != "" {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: furiosaUnifiedDSNamespace()},
		}
		setOwnerAnnotation(&cm.ObjectMeta, policy)
		cm.Data = map[string]string{
			"config.yaml": `defaultPe: Fusion
disabledDevices: []
interval: 10`,
		}
		if err := r.createOrUpdateCM(ctx, cm); err != nil {
			log.Error(err, "failed to ensure furiosa unified device plugin configmap")
			return err
		}
	}

	labels := map[string]string{"app.kubernetes.io/name": furiosaUnifiedDSName}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      furiosaUnifiedDSName,
			Namespace: furiosaUnifiedDSNamespace(),
			Labels:    labels,
		},
	}
	setOwnerAnnotation(&ds.ObjectMeta, policy)
	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				// 양 Furiosa 노드 공통 자립 라벨(node-manager 부여)로 스케줄. NFD 비의존.
				NodeSelector:      map[string]string{furiosaFamilyNodeLabel: "true"},
				Tolerations:       []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
				PriorityClassName: "system-node-critical",
				Containers: []corev1.Container{{
					Name:            furiosaPluginContainerName,
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					// Command 미지정 — 통합 이미지 ENTRYPOINT(entrypoint.sh)가 PCI 감지 후 exec.
					Env: []corev1.EnvVar{
						{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
						}},
						{Name: "RUST_LOG", Value: "info"},
						{Name: "RNGD_PARTITION_POLICY", Value: policy.Spec.Furiosa.Rngd.PartitionPolicy},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged:               boolPtr(false),
						AllowPrivilegeEscalation: boolPtr(false),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "sys", MountPath: "/sys"},
						{Name: "dev", MountPath: "/dev"},
						{Name: "dp", MountPath: "/var/lib/kubelet/device-plugins"},
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
					{Name: "dev", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
					{Name: "dp", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"}}},
				},
			},
		},
	}

	if cmName != "" {
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
					},
				},
			},
		)
		ds.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			ds.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "config", MountPath: "/etc/furiosa"},
		)
	}

	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
	// 통합 DS 는 두 제품을 함께 광고하므로 둘 중 하나라도 DRA 소유면 물러난다.
	excludeDRAOwnedNodes(&ds.Spec.Template.Spec, vendorFuriosa)
	excludeDRAOwnedNodes(&ds.Spec.Template.Spec, vendorRngd)
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)
	// 같은 이유로 둘 중 하나라도 정책 배제 대상이면 물러난다.
	applyExcludeNodeSelector(&ds.Spec.Template.Spec, policy.Spec.Furiosa.ExcludeNodeSelector)
	applyExcludeNodeSelector(&ds.Spec.Template.Spec, policy.Spec.Furiosa.Rngd.ExcludeNodeSelector)
	applyImagePullSecrets(&ds.Spec.Template.Spec, policy.Spec.ImagePullSecrets)

	// 두 writer 조정: ACPP 가 partition env 를 소유하면(owner 어노테이션) live 값을 보존한다.
	// (ACPP 가 RNGD_PARTITION_POLICY 의 권위. 어노테이션 없으면 기존 동작.)
	var live appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, &live); err == nil {
		if owner, owned := live.Annotations[rngd.PartitionOwnerAnnotation]; owned {
			if v := unifiedEnvValue(&live, "RNGD_PARTITION_POLICY"); v != "" {
				setUnifiedEnvValue(ds, "RNGD_PARTITION_POLICY", v)
			}
			if ds.Annotations == nil {
				ds.Annotations = map[string]string{}
			}
			ds.Annotations[rngd.PartitionOwnerAnnotation] = owner
		}
	}

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure furiosa unified device plugin daemonset")
		return err
	}
	log.Info("Furiosa unified device plugin daemonset ensured")
	return nil
}

// unifiedEnvValue 는 furiosa-device-plugin 컨테이너에서 name env 값을 읽는다(없으면 "").
func unifiedEnvValue(ds *appsv1.DaemonSet, name string) string {
	for _, ctr := range ds.Spec.Template.Spec.Containers {
		if ctr.Name != furiosaPluginContainerName {
			continue
		}
		for _, e := range ctr.Env {
			if e.Name == name {
				return e.Value
			}
		}
	}
	return ""
}

// setUnifiedEnvValue 는 furiosa-device-plugin 컨테이너의 name env 를 value 로 설정한다(없으면 append).
func setUnifiedEnvValue(ds *appsv1.DaemonSet, name, value string) {
	for ci := range ds.Spec.Template.Spec.Containers {
		ctr := &ds.Spec.Template.Spec.Containers[ci]
		if ctr.Name != furiosaPluginContainerName {
			continue
		}
		for ei := range ctr.Env {
			if ctr.Env[ei].Name == name {
				ctr.Env[ei].Value = value
				return
			}
		}
		ctr.Env = append(ctr.Env, corev1.EnvVar{Name: name, Value: value})
		return
	}
}

// ensureTenstorrentDevicePlugin creates a DaemonSet for the Tenstorrent Blackhole NPU device plugin.
// Pod spec: /dev + /sys(ReadOnly) + /var/lib/kubelet/device-plugins 마운트, privileged=true (PCIe 디바이스 직접 접근).
// /sys 는 plugin discovery 가 /sys/class/tenstorrent 를 참조하므로 ReadOnly 로 마운트해 장치 감지 가능하게 함.
// NodeSelector: 기본값 tenstorrent-blackhole=true (수동 라벨 전략); Spec.Tenstorrent.NodeSelector 로 재정의 가능.
// ResourceName: 기본값 "tenstorrent.com/blackhole"; Spec.Tenstorrent.ResourceName 으로 재정의 가능 (TT_RESOURCE_NAME env 로 주입).
func (r *NPUClusterPolicyReconciler) ensureTenstorrentDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	tt := policy.Spec.Tenstorrent

	// 이미지: spec 지정 없으면 registry-relative 기본값 사용 (helm 은 global.registry 로 조립)
	image := tt.DevicePluginImage
	if image == "" {
		image = ttImageDefault
	}

	// resourceName: spec 지정 없으면 기본값 사용 (device plugin 이 env 에서 읽음)
	resourceName := tt.ResourceName
	if resourceName == "" {
		resourceName = ttResourceDefault
	}

	// nodeSelector: 자립 라벨 기본값(node-manager 부여, 수동 라벨 비의존); spec 지정 시 override
	sel := map[string]string{vendorPresentLabel(vendorTenstorrent): labelValueTrue}
	if len(tt.NodeSelector) > 0 {
		sel = tt.NodeSelector
	}

	labels := map[string]string{"app.kubernetes.io/name": ttDaemonSetName}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ttDaemonSetName,
			Namespace: naming.OperatorNamespace(),
			Labels:    labels,
		},
	}
	setOwnerAnnotation(&ds.ObjectMeta, policy)
	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				NodeSelector:      sel,
				Tolerations:       []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
				PriorityClassName: "system-node-critical",
				Containers: []corev1.Container{{
					Name:            "tenstorrent-device-plugin",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env: []corev1.EnvVar{
						{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
						}},
						// device plugin 이 K8s 리소스 이름을 env 에서 읽을 수 있도록 주입
						{Name: "TT_RESOURCE_NAME", Value: resourceName},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: boolPtr(true),
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "kubelet-socket", MountPath: "/var/lib/kubelet/device-plugins"},
						{Name: "dev-fs", MountPath: "/dev"},
						// plugin discovery 가 /sys/class/tenstorrent 를 참조
						{Name: "sys-fs", MountPath: "/sys", ReadOnly: true},
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "kubelet-socket", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"}}},
					{Name: "dev-fs", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
					{Name: "sys-fs", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
				},
			},
		},
	}

	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
	excludeDRAOwnedNodes(&ds.Spec.Template.Spec, vendorTenstorrent)
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)
	applyExcludeNodeSelector(&ds.Spec.Template.Spec, policy.Spec.Tenstorrent.ExcludeNodeSelector)
	applyImagePullSecrets(&ds.Spec.Template.Spec, policy.Spec.ImagePullSecrets)

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure tenstorrent device plugin daemonset")
		return err
	}
	log.Info("Tenstorrent Blackhole device plugin daemonset ensured")
	return nil
}

// Rebellions ATOM+ default constants (Rebellions 공식 daemonset.yaml / configmap.yaml 스펙 준수)
// namespace/name 을 기존 `npu-op-*` 컨벤션 (kube-system) 으로 통일.
// Rebellions 공식 default `rbln-system/rbln-device-plugin` 대신 Warboy/RNGD/NVIDIA 와 동일 위치.
const (
	rbllnsNamespaceDefault     = "kube-system"
	rbllnsServiceAccountName   = "rbln-device-plugin"
	rbllnsClusterRoleName      = "rbln-device-plugin"
	rbllnsConfigMapNameDefault = "rbln-device-plugin-config"
	rbllnsDaemonSetName        = "rbln-device-plugin"
	rbllnsResourceNameDefault  = "ATOM"
	rbllnsResourcePrefixDfault = "rebellions.ai"
)

// rbllnsResolveNamespace returns the configured Rebellions namespace or default.
func rbllnsResolveNamespace(policy *npuv1alpha1.NPUClusterPolicy) string {
	if policy.Spec.Rebellions.Namespace != "" {
		return policy.Spec.Rebellions.Namespace
	}
	return rbllnsNamespaceDefault
}

// ensureRbllnsNamespace creates/ensures the Rebellions device plugin namespace with Pod Security
// Admission (PSA) privileged labels when a dedicated namespace is configured. kube-system 등
// 시스템 네임스페이스는 early-return 한다 — PSA 설정이 클러스터 운영자 관리이고 operator 가
// 덮어쓰면 보안 경계가 흔들리며, PSA 가 restricted 라도 kube-system DS 는 대개 예외 규칙
// (system-node-critical 등)으로 허용돼 별도 라벨 갱신이 필요 없다.
func (r *NPUClusterPolicyReconciler) ensureRbllnsNamespace(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)
	name := rbllnsResolveNamespace(policy)

	// Skip system namespaces — do not mutate their PSA labels or attempt Create.
	if name == "kube-system" || name == "kube-public" || name == "kube-node-lease" || name == "default" {
		log.V(1).Info("Rebellions namespace is a system namespace — skipping Create/PSA label mutation", "namespace", name)
		return nil
	}

	desiredLabels := map[string]string{
		"pod-security.kubernetes.io/enforce": "privileged",
		"pod-security.kubernetes.io/audit":   "privileged",
		"pod-security.kubernetes.io/warn":    "privileged",
	}

	var cur corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &cur); apierrors.IsNotFound(err) {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: desiredLabels,
			},
		}
		setOwnerAnnotation(&ns.ObjectMeta, policy)
		if err := r.Create(ctx, ns); err != nil {
			log.Error(err, "failed to create rebellions namespace")
			return err
		}
		return nil
	} else if err != nil {
		return err
	}

	// Merge PSA labels (do not overwrite user-added labels)
	if cur.Labels == nil {
		cur.Labels = map[string]string{}
	}
	changed := false
	for k, v := range desiredLabels {
		if cur.Labels[k] != v {
			cur.Labels[k] = v
			changed = true
		}
	}
	if changed {
		if err := r.Update(ctx, &cur); err != nil {
			log.Error(err, "failed to update rebellions namespace PSA labels")
			return err
		}
	}
	return nil
}

// ensureRbllnsServiceAccount creates the ServiceAccount used by the Rebellions
// device plugin DaemonSet.
func (r *NPUClusterPolicyReconciler) ensureRbllnsServiceAccount(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)
	ns := rbllnsResolveNamespace(policy)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbllnsServiceAccountName,
			Namespace: ns,
		},
		ImagePullSecrets: policy.Spec.ImagePullSecrets,
	}
	setOwnerAnnotation(&sa.ObjectMeta, policy)

	var cur corev1.ServiceAccount
	key := types.NamespacedName{Name: sa.Name, Namespace: sa.Namespace}
	if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, sa); err != nil {
			log.Error(err, "failed to create rebellions serviceaccount")
			return err
		}
		return nil
	} else if err != nil {
		return err
	}
	// imagePullSecrets 는 spec 변경으로 갱신될 수 있으므로 기존 SA 와 동기화한다.
	if !equality.Semantic.DeepEqual(cur.ImagePullSecrets, sa.ImagePullSecrets) {
		cur.ImagePullSecrets = sa.ImagePullSecrets
		if err := r.Update(ctx, &cur); err != nil {
			log.Error(err, "failed to update rebellions serviceaccount imagePullSecrets")
			return err
		}
	}
	return nil
}

// ensureRbllnsRBAC creates the ClusterRole and ClusterRoleBinding granting the
// Rebellions device plugin ServiceAccount permissions to read/patch nodes
// (kubelet socket management).
func (r *NPUClusterPolicyReconciler) ensureRbllnsRBAC(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)
	ns := rbllnsResolveNamespace(policy)

	desiredRules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs:     []string{"get", "list", "watch", "patch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"create", "patch"},
		},
	}

	// ClusterRole
	var curCR rbacv1.ClusterRole
	if err := r.Get(ctx, types.NamespacedName{Name: rbllnsClusterRoleName}, &curCR); apierrors.IsNotFound(err) {
		cr := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: rbllnsClusterRoleName},
			Rules:      desiredRules,
		}
		setOwnerAnnotation(&cr.ObjectMeta, policy)
		if err := r.Create(ctx, cr); err != nil {
			log.Error(err, "failed to create rebellions clusterrole")
			return err
		}
	} else if err != nil {
		return err
	} else if !equality.Semantic.DeepEqual(curCR.Rules, desiredRules) {
		curCR.Rules = desiredRules
		if err := r.Update(ctx, &curCR); err != nil {
			log.Error(err, "failed to update rebellions clusterrole")
			return err
		}
	}

	// ClusterRoleBinding
	desiredSubjects := []rbacv1.Subject{{
		Kind:      "ServiceAccount",
		Name:      rbllnsServiceAccountName,
		Namespace: ns,
	}}
	desiredRoleRef := rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     rbllnsClusterRoleName,
	}

	var curCRB rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, types.NamespacedName{Name: rbllnsClusterRoleName}, &curCRB); apierrors.IsNotFound(err) {
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: rbllnsClusterRoleName},
			RoleRef:    desiredRoleRef,
			Subjects:   desiredSubjects,
		}
		setOwnerAnnotation(&crb.ObjectMeta, policy)
		if err := r.Create(ctx, crb); err != nil {
			log.Error(err, "failed to create rebellions clusterrolebinding")
			return err
		}
		return nil
	} else if err != nil {
		return err
	}
	// RoleRef is immutable; only sync subjects (SA namespace may have changed via spec override).
	if !equality.Semantic.DeepEqual(curCRB.Subjects, desiredSubjects) {
		curCRB.Subjects = desiredSubjects
		if err := r.Update(ctx, &curCRB); err != nil {
			log.Error(err, "failed to update rebellions clusterrolebinding subjects")
			return err
		}
	}
	return nil
}

// ensureRbllnsConfigMap creates the ConfigMap consumed by the Rebellions device plugin
// (/etc/pcidp/config.json). Device IDs cover all 12 ATOM+ variants (1eff:0010~1251).
func (r *NPUClusterPolicyReconciler) ensureRbllnsConfigMap(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	// 신판(v0.4)은 /etc/pcidp 설정을 읽지 않는다. 이미 있는 ConfigMap 은 지우지 않는다(쓰는 곳이 없다).
	if gen, _ := rebellionsPluginGeneration(policy.Spec.Rebellions); gen == rbllnsGenV04 {
		return nil
	}
	ns := rbllnsResolveNamespace(policy)
	name := policy.Spec.Rebellions.ConfigMapName
	if name == "" {
		name = rbllnsConfigMapNameDefault
	}
	resourceName := policy.Spec.Rebellions.ResourceName
	if resourceName == "" {
		resourceName = rbllnsResourceNameDefault
	}
	resourcePrefix := policy.Spec.Rebellions.ResourcePrefix
	if resourcePrefix == "" {
		resourcePrefix = rbllnsResourcePrefixDfault
	}

	configJSON := fmt.Sprintf(
		`{"resourceList":[{"resourceName":"%s","resourcePrefix":"%s","deviceType":"accelerator","selectors":{"vendors":["1eff"],"devices":["0010","0011","1020","1021","1120","1121","1150","1151","1220","1221","1250","1251"],"drivers":["rebellions"]}}]}`,
		resourceName, resourcePrefix,
	)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Data: map[string]string{
			"config.json": configJSON,
		},
	}
	setOwnerAnnotation(&cm.ObjectMeta, policy)
	return r.createOrUpdateCM(ctx, cm)
}

// ensureRebellionsDevicePlugin creates the DaemonSet running the Rebellions device plugin.
// 컨테이너·볼륨은 판(rebellionsPluginGeneration)에 따라 rebellions_plugin_render.go 가 만든다.
// 두 판 모두 init container(rebellions_cdi_prep.go)가 rbln-smi 를 확인하고 CDI 이름 파일을 쓴다.
// v0.3.6 은 rbln-smi 를 /usr/bin/rbln-smi 다음에 PATH 에서만 찾으므로 PATH 로 호스트 경로를 알려 주고,
// 신판은 /host/usr/bin·/host/driver/usr/bin 을 스스로 찾는다. 호스트 /usr/bin 과 /usr/local/bin 을
// 읽기 전용으로 붙이는 것까지가 우리 몫이다.
func (r *NPUClusterPolicyReconciler) ensureRebellionsDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	ns := rbllnsResolveNamespace(policy)
	cmName := policy.Spec.Rebellions.ConfigMapName
	if cmName == "" {
		cmName = rbllnsConfigMapNameDefault
	}
	gen, _ := rebellionsPluginGeneration(policy.Spec.Rebellions)

	// 자립 라벨(node-manager 부여, 수동 라벨 비의존) + arch 게이트.
	sel := map[string]string{
		"kubernetes.io/arch":                 "amd64",
		vendorPresentLabel(vendorRebellions): labelValueTrue,
	}
	if len(policy.Spec.Rebellions.NodeSelector) > 0 {
		sel = policy.Spec.Rebellions.NodeSelector
	}

	labels := map[string]string{"app.kubernetes.io/name": rbllnsDaemonSetName}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbllnsDaemonSetName,
			Namespace: ns,
			Labels:    labels,
		},
	}
	setOwnerAnnotation(&ds.ObjectMeta, policy)
	podSpec := corev1.PodSpec{
		// 구판은 공식 daemonset.yaml 을 따라 hostNetwork 를 쓴다. 신판 벤더 차트는 쓰지 않는다 —
		// 상태 확인 gRPC 포트(51515)가 호스트 포트를 차지하지 않게 한다.
		HostNetwork:        gen == rbllnsGenV03,
		ServiceAccountName: rbllnsServiceAccountName,
		NodeSelector:       sel,
		Tolerations: []corev1.Toleration{
			{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
		},
		Containers: []corev1.Container{rbllnsPluginContainer(gen, policy.Spec.Rebellions.DevicePluginImage)},
		Volumes:    rbllnsPluginVolumes(gen, cmName),
	}
	if c := rbllnsPrepInitContainer(HostExecImage(), gen, rbllnsCDIKind(policy.Spec.Rebellions, gen)); c != nil {
		podSpec.InitContainers = []corev1.Container{*c}
	}
	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec:       podSpec,
		},
	}

	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
	excludeDRAOwnedNodes(&ds.Spec.Template.Spec, vendorRebellions)
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)
	applyExcludeNodeSelector(&ds.Spec.Template.Spec, policy.Spec.Rebellions.ExcludeNodeSelector)
	applyImagePullSecrets(&ds.Spec.Template.Spec, policy.Spec.ImagePullSecrets)

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure rebellions device plugin daemonset")
		return err
	}
	log.Info("Rebellions device plugin daemonset ensured", "generation", gen)
	return nil
}

// ensureDetectorServiceAccount creates the `kcloud-node-manager` ServiceAccount referenced by
// the detector DaemonSet. 차트는 kcloud-node-manager role/rolebinding 만 만들고 SA 객체는 만들지
// 않으므로(fresh 클러스터에서 detector pod 가 `serviceaccount not found` 로 기동 실패),
// 컨트롤러가 ensureRbllnsServiceAccount 와 대칭으로 SA 를 생성한다.
// policy.Spec.ImagePullSecrets 를 SA 에 부착하여 SA 경유 private pull 을 이중 보장한다.
func (r *NPUClusterPolicyReconciler) ensureDetectorServiceAccount(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      detectorServiceAccountName,
			Namespace: naming.OperatorNamespace(),
		},
		ImagePullSecrets: policy.Spec.ImagePullSecrets,
	}
	setOwnerAnnotation(&sa.ObjectMeta, policy)

	var cur corev1.ServiceAccount
	key := types.NamespacedName{Name: sa.Name, Namespace: sa.Namespace}
	if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, sa); err != nil {
			log.Error(err, "failed to create detector serviceaccount")
			return err
		}
		return nil
	} else if err != nil {
		return err
	}
	// imagePullSecrets 는 spec 변경으로 갱신될 수 있으므로 기존 SA 와 동기화한다.
	if !equality.Semantic.DeepEqual(cur.ImagePullSecrets, sa.ImagePullSecrets) {
		cur.ImagePullSecrets = sa.ImagePullSecrets
		if err := r.Update(ctx, &cur); err != nil {
			log.Error(err, "failed to update detector serviceaccount imagePullSecrets")
			return err
		}
	}
	return nil
}

func (r *NPUClusterPolicyReconciler) ensureDetector(ctx context.Context, pol *npuv1alpha1.NPUClusterPolicy) error {
	if pol.Spec.Detector == nil || pol.Spec.Detector.Image == "" {
		return fmt.Errorf("detector image must be specified in NPUClusterPolicy.spec.detector.image")
	}

	// detector DS 의 ServiceAccountName(kcloud-node-manager) 를 먼저 보장 — DS 보다 선행해야
	// fresh 클러스터에서 `serviceaccount not found` 로 pod 기동이 실패하지 않는다.
	if err := r.ensureDetectorServiceAccount(ctx, pol); err != nil {
		return err
	}

	image := pol.Spec.Detector.Image
	ds := renderDetectorDS(image, pol.Spec.ImagePullSecrets)
	setOwnerAnnotation(&ds.ObjectMeta, pol)
	return r.createOrUpdateDS(ctx, ds)
}

func renderDetectorDS(image string, pullSecrets []corev1.LocalObjectReference) *appsv1.DaemonSet {
	labels := map[string]string{"app.kubernetes.io/name": "kcloud-node-manager"}
	hostPathDirOrCreate := corev1.HostPathDirectoryOrCreate
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kcloud-node-manager",
			Namespace: naming.OperatorNamespace(),
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					// node-agent Metrics 능력: Prometheus 스크레이프 대상 표시(:9100/metrics).
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "9100",
						"prometheus.io/path":   "/metrics",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "kcloud-node-manager",
					Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{
						{
							Name:            "detector",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// node-agent Metrics 능력: /metrics 포트(비특권 로컬 서버).
							Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9100}},
							Env: []corev1.EnvVar{
								{
									Name:      "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}},
								},
								// NFD/GFD 라벨링 능력 활성화(node patch RBAC 는 kcloud-node-manager-role 에 부여됨).
								{Name: "NODEAGENT_ENABLE_LABELS", Value: "true"},
							},
							// Pod Security: 비특권 설계 확정. detector 이미지는 distroless/static:nonroot
							// (USER 65532)이며 드라이버 버전은 host /proc·/sys 읽기로 감지(root·device 불필요),
							// nvidia-smi 등 host CLI exec 은 best-effort(실패 무해)라 restricted 프로파일과 양립.
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             boolPtr(true),
								AllowPrivilegeEscalation: boolPtr(false),
								ReadOnlyRootFilesystem:   boolPtr(true),
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "host-proc", MountPath: "/host/proc", ReadOnly: true},
								{Name: "host-dev", MountPath: "/host/dev", ReadOnly: true},
								{Name: "host-var", MountPath: "/host/var", ReadOnly: true},
								{Name: "host-sys", MountPath: "/host/sys", ReadOnly: true},
								// detector binary 가 /usr/bin/nvidia-smi 검사로 nvidia userland 존재 판단.
								// /host/usr mount 없으면 nvidiaUserlandPresent()=false → driverVersion="" 보고.
								{Name: "host-usr", MountPath: "/host/usr", ReadOnly: true},
								// podresources sidecar 가 쓴 결과를 읽기 전용으로 받는다. detector 에는 kubelet
								// 소켓 자체를 마운트하지 않는다 — 읽을 수도 없고(root 전용) 권한 범위만 넓어진다.
								{Name: "podresources-share", MountPath: "/run/podresources", ReadOnly: true},
							},
						},
						{
							Name:            "podresources",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// Command 를 새 이미지에만 있는 실행 파일로 고정한다(node-manager 0.6.19 이상=
							// /podresources-writer → /detector 심볼릭 링크). 구 이미지는 이 경로가
							// 없어 이 컨테이너만 시작 실패로 남고 detector 본체는 영향받지 않는다 — 구 이미지가
							// --podresources-writer 인자를 몰라 detector 보고 루프를 uid 0 으로 한 번 더 돌리는
							// 이중 보고를 막는다.
							Command: []string{"/podresources-writer"},
							Args:    []string{"--podresources-writer"},
							// writer 모드는 NODE_NAME 을 읽지 않는다 — env 를 주지 않는다.
							// kubelet pod-resources 디렉터리는 root:root 0750, 소켓은 root 소유라 연결에 소켓 쓰기 권한이
							// 필요하다 — root 가 아니면 못 읽는다. 그래서 이 컨테이너만 uid 0 으로
							// 돌리고, 능력은 전부 버리고 rootfs 는 읽기 전용으로 둔다. 할 수 있는 일은 소켓 읽기와
							// /run/podresources 쓰기뿐이다. detector 본체의 비특권 설계는 그대로다.
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:                int64Ptr(0),
								RunAsNonRoot:             boolPtr(false),
								Privileged:               boolPtr(false),
								AllowPrivilegeEscalation: boolPtr(false),
								ReadOnlyRootFilesystem:   boolPtr(true),
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "kubelet-podresources", MountPath: hostPodResourcesDir, ReadOnly: true},
								{Name: "podresources-share", MountPath: "/run/podresources"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "host-proc", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/proc"}}},
						{Name: "host-dev", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
						{Name: "host-var", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var"}}},
						{Name: "host-sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
						{Name: "host-usr", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/usr"}}},
						// kubelet-podresources 는 사이드카 전용(root 만 연결 가능한 소켓). Type 을
						// DirectoryOrCreate 로 둬 디렉터리가 없는 노드(kubelet root 를 옮긴 호스트 등)
						// 에서도 파드가 뜬다 — 소켓 부재는 writer 가 공유 파일에 사유를 남겨 detector 가
						// consumersError 로 보고한다(파드 전체가 Pending 이 되어 detector 본체까지
						// 멈추는 것을 피한다). podresources-share 는 사이드카↔detector 전달용 메모리
						// emptyDir — 디스크에 닿지 않고 노드 재부팅/파드 재시작마다 비워진다.
						{Name: "kubelet-podresources", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: hostPodResourcesDir, Type: &hostPathDirOrCreate}}},
						{Name: "podresources-share", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: resource.NewQuantity(1<<20, resource.BinarySI)}}},
					},
				},
			},
		},
	}
	// detector는 /dev를 ReadOnly로 마운트하므로, 드라이버 업그레이드 중 rmmod 간섭을 막기 위해
	// device-plugin과 동일하게 업그레이드 라벨이 붙은 노드에는 스케줄되지 않도록 한다.
	// detector(node-manager)는 control-plane 포함 전 노드에 상주해야 하므로
	// applyControlPlaneExclusion 을 적용하지 않는다(감지·라벨·검증은 master 에서도 계속).
	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
	applyImagePullSecrets(&ds.Spec.Template.Spec, pullSecrets)
	return ds
}

// nodeEventFilter 는 노드 이벤트 중 reconcile 로 보낼 것을 고른다. 노드 Update 는 kubelet
// 하트비트로 매우 잦으므로 라벨이 바뀐 것만 통과시킨다 — 배제 축이 보는 것은 라벨뿐이다.
// LabelChangedPredicate 는 Update 만 걸러내고 Create·Delete·Generic 은 TypedFuncs 의 nil
// 기본값이라 그대로 통과한다(predicate.Or 로 감쌀 필요가 없다). 새 control-plane 노드 합류가
// Create 경로이므로 Create 통과는 필수다.
var nodeEventFilter predicate.Predicate = predicate.LabelChangedPredicate{}

// SetupWithManager sets up the controller with the Manager. 노드 watch 가 필요한 이유: 배제
// 라벨은 노드 라벨을 보고 붙는데 NCP 만 watch 하면 노드 합류·라벨 변경은 reconcile 을 깨우지
// 못한다. 유일한 안전망은 캐시 re-sync(cmd/main.go 가 SyncPeriod 를 60초로 줄여 둠)라 낡음
// 창은 최대 90초(60초+detector 스캔 30초)다 — 정확성 결함이 아니라 응답성 문제다. 그래도 라벨
// 즉시 반응이 이 축의 대표 사용법이라 predicate 하나 비용으로 watch 를 건다. 주기 재큐나
// SyncPeriod 변경은 하지 않는다 — re-sync 가 이미 안전망이다.
func (r *NPUClusterPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.NPUClusterPolicy{}).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.mapNodeToClusterPolicies),
			builder.WithPredicates(nodeEventFilter),
		).
		Named("npuclusterpolicy").
		Complete(r)
}

// mapNodeToClusterPolicies 는 노드 이벤트를 NCP 요청으로 옮긴다. 배제 판정은 클러스터 전체
// 노드를 훑으므로 어느 노드가 바뀌었는지는 쓰지 않는다(NCP 는 보통 1개).
func (r *NPUClusterPolicyReconciler) mapNodeToClusterPolicies(
	ctx context.Context, _ client.Object) []reconcile.Request {
	var list npuv1alpha1.NPUClusterPolicyList
	if err := r.List(ctx, &list); err != nil {
		logf.FromContext(ctx).Error(err, "노드 이벤트 매핑 중 NPUClusterPolicy 목록 조회 실패")
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: list.Items[i].Namespace, Name: list.Items[i].Name,
		}})
	}
	return reqs
}

// -- Add
func boolPtr(b bool) *bool {
	return &b
}

func int64Ptr(i int64) *int64 {
	return &i
}

// control-plane/master 노드를 식별하는 라벨. driver_upgrade_controller 의 노드 제외
// 로직(control-plane/master 무조건 skip)과 동일한 키를 사용해 정책 일관성을 유지한다.
const (
	controlPlaneNodeLabel = "node-role.kubernetes.io/control-plane"
	masterNodeLabel       = "node-role.kubernetes.io/master"
)

// appendNodeAffinityRequirements는 기존 Affinity를 보존하면서 required nodeAffinity
// term(들)에 match expression을 누적 추가한다. term이 없으면 하나 생성한다.
func appendNodeAffinityRequirements(spec *corev1.PodSpec, reqs ...corev1.NodeSelectorRequirement) {
	if spec.Affinity == nil {
		spec.Affinity = &corev1.Affinity{}
	}
	if spec.Affinity.NodeAffinity == nil {
		spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	ns := spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if ns == nil {
		ns = &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{}}}
	}
	if len(ns.NodeSelectorTerms) == 0 {
		ns.NodeSelectorTerms = append(ns.NodeSelectorTerms, corev1.NodeSelectorTerm{})
	}
	for i := range ns.NodeSelectorTerms {
		ns.NodeSelectorTerms[i].MatchExpressions = append(
			ns.NodeSelectorTerms[i].MatchExpressions, reqs...)
	}
	spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = ns
}

// applyDriverUpgradeAntiAffinity는 기존 Affinity를 보존하면서
// driver-upgrading-blocking 라벨이 없는 노드에만 스케줄되도록 제약을 추가한다 — 좁은
// lifecycle 의 blocking 라벨로 phase-aware 차단을 건다. Cordoning ~ Upgrading 단계는
// 라벨이 활성화돼 detector·device-plugin 을 차단(rmmod 보호)하고, Validating 단계는
// 라벨이 자동 제거돼 detector spawn → NDR 갱신 → Validator 통과로 이어진다.
func applyDriverUpgradeAntiAffinity(spec *corev1.PodSpec) {
	appendNodeAffinityRequirements(spec, corev1.NodeSelectorRequirement{
		Key:      upgrade.DriverUpgradingBlockingLabelKey,
		Operator: corev1.NodeSelectorOpDoesNotExist,
	})
}

// applyControlPlaneExclusion는 GPU/NPU 스택(device-plugin·toolkit·driver installer) 워크로드가
// control-plane/master 노드에 스케줄되지 않게 한다 — driver_upgrade_controller 가 이미 강제하는
// control-plane/master 라벨 제외와 동일 규칙이며 worker 노드는 두 라벨이 없어 영향 0 이다.
// detector(node-manager) DS 에는 적용하지 않는다(전 노드 상주). ponytail: 라벨 기반 제외라
// control-plane taint 를 제거하고 라벨만 유지하는 단일 노드 클러스터도 제외된다 — 그런 형상이
// 필요하면 values 토글을 추가한다.
func applyControlPlaneExclusion(spec *corev1.PodSpec) {
	appendNodeAffinityRequirements(spec,
		corev1.NodeSelectorRequirement{Key: controlPlaneNodeLabel, Operator: corev1.NodeSelectorOpDoesNotExist},
		corev1.NodeSelectorRequirement{Key: masterNodeLabel, Operator: corev1.NodeSelectorOpDoesNotExist},
	)
}

// applyExcludeNodeSelector 는 sel 이 고르는 노드를 이 벤더 배포 대상에서 뺀다 — 기존
// nodeSelector(양성 선택)의 반대 축이다. applyControlPlaneExclusion 과 별개로 위에 얹는다
// (control-plane 배제는 이 축과 무관하게 항상 걸린다). nodeExclusion(node_exclusion.go)이
// 같은 필드로 kcloud.ai/excluded 라벨의 사유를 판정하므로 두 경로가 갈리지 않는다. 배제
// 의미론은 "조건 하나라도 맞으면 뺀다" — 조건별 NOT 을 requiredDuringScheduling term 하나에
// AND 로 합성해 표현한다(유효하지 않은 조건은 exclusionConditions 가 이미 버렸다).
func applyExcludeNodeSelector(spec *corev1.PodSpec, sel *metav1.LabelSelector) {
	conds, _ := exclusionConditions(sel)
	reqs := make([]corev1.NodeSelectorRequirement, 0, len(conds))
	for _, e := range conds {
		switch e.Operator {
		case metav1.LabelSelectorOpIn:
			reqs = append(reqs, corev1.NodeSelectorRequirement{Key: e.Key, Operator: corev1.NodeSelectorOpNotIn, Values: e.Values})
		case metav1.LabelSelectorOpNotIn:
			reqs = append(reqs, corev1.NodeSelectorRequirement{Key: e.Key, Operator: corev1.NodeSelectorOpIn, Values: e.Values})
		case metav1.LabelSelectorOpExists:
			reqs = append(reqs, corev1.NodeSelectorRequirement{Key: e.Key, Operator: corev1.NodeSelectorOpDoesNotExist})
		case metav1.LabelSelectorOpDoesNotExist:
			reqs = append(reqs, corev1.NodeSelectorRequirement{Key: e.Key, Operator: corev1.NodeSelectorOpExists})
		}
	}
	if len(reqs) == 0 {
		return
	}
	appendNodeAffinityRequirements(spec, reqs...)
}
