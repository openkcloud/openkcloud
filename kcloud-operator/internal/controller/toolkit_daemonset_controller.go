// ============================================================
// toolkit_daemonset_controller.go: Container Toolkit DaemonSet 컨트롤러
// 상세: DriverInstallPolicy.spec.toolkit.enabled=true 인 정책에 대해 vendor 별
// container-toolkit DaemonSet 을 생성/업데이트합니다. NVIDIA 는 nvidia-container-toolkit
// 이미지로 containerd 에 nvidia runtime 을 등록하며, driver Ready 이후 시작하도록 init
// container 로 host driver 존재를 대기합니다(nvidia-ctk 는 이미 등록된 노드에서 no-op).
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driveradapter"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
)

// toolkit 기본 상수. 이미지는 vendor 제공 container-toolkit(vendor 레지스트리 참조).
// DriverInstallPolicy.spec.toolkit.image 로 override 가능(미지정 시 이 기본값).
const (
	nvidiaToolkitImageDefault = "nvcr.io/nvidia/k8s/container-toolkit:v1.17.8-ubuntu20.04"
	// toolkit 이 patch 하는 host containerd 경로.
	hostContainerdConfigDir = "/etc/containerd"
	hostContainerdSockDir   = "/run/containerd"
	// toolkit 바이너리 설치 host 경로(NVIDIA gpu-operator 규약과 동일).
	hostToolkitInstallDir = "/usr/local/nvidia"
)

// ToolkitDaemonSetReconciler 는 DriverInstallPolicy.spec.toolkit.enabled=true 인
// 정책에 대해 container-toolkit DaemonSet 을 관리합니다.
type ToolkitDaemonSetReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=npu.ai,resources=driverinstallpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ToolkitDaemonSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	metrics.RecordReconcile()
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling ToolkitDaemonSet", "name", req.NamespacedName)

	var pols npuv1alpha1.DriverInstallPolicyList
	if err := r.List(ctx, &pols); err != nil {
		return ctrl.Result{}, err
	}

	for i := range pols.Items {
		pol := &pols.Items[i]
		if pol.Spec.Toolkit == nil || !pol.Spec.Toolkit.Enabled {
			continue
		}
		if !toolkitVendorSupported(pol.Spec.Vendor) {
			// Warboy/RNGD/Atom+/Tenstorrent 는 별도 containerd runtime 이 불필요
			// (device-plugin 만으로 리소스 노출). 미지원 vendor 는 skip(회귀 0).
			logger.V(1).Info("toolkit vendor not supported — skipping", "vendor", pol.Spec.Vendor)
			continue
		}
		if err := r.createOrUpdateToolkitDS(ctx, pol); err != nil {
			logger.Error(err, "failed to ensure toolkit DaemonSet", "policy", pol.Name)
			r.Recorder.Eventf(pol, corev1.EventTypeWarning, "ReconcileFailed",
				"Failed to ensure toolkit DaemonSet for policy %s: %v", pol.Name, err)
			return ctrl.Result{}, err
		}
		logger.Info("Toolkit DaemonSet ensured", "policy", pol.Name, "vendor", pol.Spec.Vendor)
	}

	return ctrl.Result{}, nil
}

// toolkitVendorSupported 는 container-toolkit reconciler 가 DS 를 생성하는 vendor 인지
// 판정한다. 현재 NVIDIA 만 container-toolkit(containerd runtime 등록)이 필요하며, 그 판정은
// driveradapter 가 담당한다 — 이 함수는 조회 결과를 그대로 전한다.
func toolkitVendorSupported(vendor string) bool {
	a, ok := driveradapter.ForModel(vendor, "")
	return ok && a.ToolkitRequired()
}

func (r *ToolkitDaemonSetReconciler) createOrUpdateToolkitDS(ctx context.Context, pol *npuv1alpha1.DriverInstallPolicy) error {
	if err := r.deleteLegacyToolkitDS(ctx); err != nil {
		return err
	}
	ds := renderToolkitDaemonSet(pol)
	return r.createOrUpdateDS(ctx, ds)
}

// deleteLegacyToolkitDS 는 접두사를 떼기 전 이름(naming.ToolkitLegacyDSName)의 DS 를
// 새 DS ensure 전에 지운다. 두 DS 가 같은 노드에서 containerd 설정을 서로 덮으면
// 유효 설정이 어느 쪽인지 알 수 없게 되므로, 접두사를 뗀 회차엔 실제로 지워져야 한다.
func (r *ToolkitDaemonSetReconciler) deleteLegacyToolkitDS(ctx context.Context) error {
	legacy := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{
		Name: naming.ToolkitLegacyDSName, Namespace: naming.KubeSystemNamespace}}
	if err := r.Delete(ctx, legacy); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// createOrUpdateDS 는 DaemonSet 을 생성하거나 스펙/레이블/어노테이션/ownerRef 변경 시
// 업데이트하는 idempotent upsert (driver reconciler 와 동일 규약).
func (r *ToolkitDaemonSetReconciler) createOrUpdateDS(ctx context.Context, desired *appsv1.DaemonSet) error {
	// 모든 DIP 트리거가 all-DIP List 후 동일 nvidia-toolkit DS 를 ensure 하므로
	// 동시 reconcile 의 Get→Update 가 stale resourceVersion 으로 409 Conflict 가 날 수 있다.
	// RetryOnConflict 가 Conflict 시 아래 함수를 재실행(=fresh Get→Update)하여 흡수한다.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur appsv1.DaemonSet
		key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}
		if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
			return r.Create(ctx, desired)
		} else if err != nil {
			return err
		}
		if !equality.Semantic.DeepEqual(cur.Spec, desired.Spec) ||
			!equality.Semantic.DeepEqual(cur.Labels, desired.Labels) ||
			!equality.Semantic.DeepEqual(cur.Annotations, desired.Annotations) ||
			!equality.Semantic.DeepEqual(cur.OwnerReferences, desired.OwnerReferences) {
			cur.Spec = desired.Spec
			cur.Labels = desired.Labels
			cur.Annotations = desired.Annotations
			cur.OwnerReferences = desired.OwnerReferences
			return r.Update(ctx, &cur)
		}
		return nil
	})
}

// renderToolkitDaemonSet 는 vendor 별 container-toolkit DaemonSet 을 만든다.
// 현재 NVIDIA 만 지원(toolkitVendorSupported 로 진입 게이트). driver 와 동일 노드에
// 뜨되, init container 로 host nvidia driver(/dev/nvidia0) 준비를 대기하여 driver Ready
// 이후 toolkit 설치가 시작되도록 한다.
func renderToolkitDaemonSet(pol *npuv1alpha1.DriverInstallPolicy) *appsv1.DaemonSet {
	name := naming.ToolkitDSName(pol.Spec.Vendor, pol.Spec.Model)
	labels := map[string]string{
		"app.kubernetes.io/name":      "kcloud-toolkit",
		"app.kubernetes.io/component": "toolkit",
		"npu.ai/vendor":               strings.ToLower(pol.Spec.Vendor),
	}

	nodeSelector := driverNodeSelector(pol.Spec.Vendor, pol.Spec.Model)
	if len(pol.Spec.NodeSelector) > 0 {
		nodeSelector = pol.Spec.NodeSelector
	}

	image := nvidiaToolkitImageDefault
	if pol.Spec.Toolkit != nil && pol.Spec.Toolkit.Image != "" {
		image = pol.Spec.Toolkit.Image
	}

	privileged := boolPtr(true)
	hostPathDir := corev1.HostPathDirectory
	hostPathDirOrCreate := corev1.HostPathDirectoryOrCreate

	// toolkit main container 의 기본 env. entrypoint(nvidia-ctk)는 이 env 로
	// containerd 경로/런타임/driver-root 를 결정한다.
	toolkitEnv := []corev1.EnvVar{
		{Name: "RUNTIME", Value: "containerd"},
		{Name: "CONTAINERD_RUNTIME_CLASS", Value: "nvidia"},
		{Name: "CONTAINERD_CONFIG", Value: "/runtime/config-dir/config.toml"},
		{Name: "CONTAINERD_SOCKET", Value: "/runtime/sock-dir/containerd.sock"},
		// 이미 default runtime 을 바꾸지 않는다(기존 워크로드 무영향).
		{Name: "CONTAINERD_SET_AS_DEFAULT", Value: "false"},
		{Name: "NVIDIA_VISIBLE_DEVICES", Value: "void"},
		// driver root = host root("/"). 이 operator 의 드라이버 DS 는 항상 드라이버를
		// 호스트에 설치하므로, nvidia-container-cli 가 host 의 드라이버를 사용하도록
		// 한다. 이미지 기본값 "/run/nvidia/driver"(컨테이너 드라이버 전용)는 호스트
		// 설치 노드에서 "change root failed: no such file or directory" 를 유발한다.
		{Name: "NVIDIA_DRIVER_ROOT", Value: "/"},
	}
	// DriverInstallPolicy.spec.toolkit.env passthrough. 기본 env 뒤에 append 하므로
	// 사용자가 values 로 override 가능(예: 컨테이너 드라이버 노드에서 driver-root 변경).
	if pol.Spec.Toolkit != nil {
		for _, e := range pol.Spec.Toolkit.Env {
			toolkitEnv = append(toolkitEnv, corev1.EnvVar{Name: e.Name, Value: e.Value})
		}
	}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			// nvidia-container-toolkit 는 3rd party 공식 이미지(nvidia/container-toolkit)라
			// device-plugin 과 동일하게 kube-system 고정(분류 정합: 3rd party SW→kube-system).
			Namespace: naming.KubeSystemNamespace,
			Labels:    labels,
			// DIP(cluster-scoped) 를 owner 로 → DIP 삭제 시 K8s GC 가 toolkit DS 를
			// cascade 삭제. driver DS 와 동일 규약(BlockOwnerDeletion 생략).
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "npu.ai/v1alpha1",
				Kind:       "DriverInstallPolicy",
				Name:       pol.Name,
				UID:        pol.UID,
				Controller: boolPtr(true),
			}},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					HostPID:           true,
					NodeSelector:      nodeSelector,
					PriorityClassName: "system-node-critical",
					Tolerations:       []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					// driver Ready 대기: host nvidia device node(/dev/nvidia0)가 나타날 때까지
					// 블록. driver DS 가 모듈을 적재하면 device node 가 생성된다.
					InitContainers: []corev1.Container{{
						Name:            "driver-validation",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"sh", "-c"},
						Args: []string{
							"until [ -e /host-dev/nvidia0 ] || [ -e /host-dev/nvidiactl ]; do " +
								"echo 'waiting for nvidia driver (device node) ...'; sleep 5; done; " +
								"echo 'nvidia driver ready'",
						},
						SecurityContext: &corev1.SecurityContext{Privileged: privileged},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "host-dev", MountPath: "/host-dev", ReadOnly: true},
						},
					}},
					Containers: []corev1.Container{{
						Name:            "nvidia-container-toolkit",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						// toolkit 이미지 entrypoint(/work/nvidia-toolkit)는 필수 positional
						// 인자 DESTINATION(설치 host 경로)을 요구한다 — 미지정 시
						// "the install root must be specified" 로 즉시 종료. toolkit 은
						// ${DESTINATION}/toolkit 에 설치되며, 이 경로는 toolkit-install-dir
						// 볼륨(hostToolkitInstallDir)으로 host 에 마운트된다.
						Args: []string{hostToolkitInstallDir},
						// entrypoint 는 nvidia-ctk 로 containerd config 를 idempotent 하게
						// patch 후 상주. env(toolkitEnv)로 containerd 경로/런타임/driver-root 지정.
						Env:             toolkitEnv,
						SecurityContext: &corev1.SecurityContext{Privileged: privileged},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "toolkit-install-dir", MountPath: hostToolkitInstallDir},
							{Name: "containerd-config", MountPath: "/runtime/config-dir"},
							{Name: "containerd-socket", MountPath: "/runtime/sock-dir"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "host-dev", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
						{Name: "toolkit-install-dir", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: hostToolkitInstallDir, Type: &hostPathDirOrCreate}}},
						{Name: "containerd-config", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: hostContainerdConfigDir, Type: &hostPathDir}}},
						{Name: "containerd-socket", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: hostContainerdSockDir, Type: &hostPathDir}}},
					},
				},
			},
		},
	}

	// toolkit 은 driver 와 짝이므로 driver 와 동일한 upgrade-blocking 회피를 적용하지 않는다:
	// driver 가 rmmod 중이면 device node 가 사라져 init container 가 자연히 재대기한다.
	// control-plane/master 는 제외 — 제어 평면에는 nvidia runtime/toolkit 을 두지 않는다.
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)
	return ds
}

// SetupWithManager sets up the controller with the Manager.
func (r *ToolkitDaemonSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.DriverInstallPolicy{}).
		Named("toolkitdaemonset").
		Complete(r)
}
