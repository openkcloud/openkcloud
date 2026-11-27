// driver_daemonset_controller.go: Driver DaemonSet 컨트롤러
// 상세: DriverInstallPolicy.spec.driver.mode="daemonset"인 정책에 대해
//       컨테이너화 드라이버 DaemonSet을 생성/업데이트합니다.
// 생성일: 2026-04-13 | 수정일: 2026-10-14

package controller

import (
	"context"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driveradapter"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
)

// DriverDaemonSetReconciler는 Mode="daemonset"인 DriverInstallPolicy에 대해
// 드라이버 DaemonSet을 관리합니다.
type DriverDaemonSetReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=npu.ai,resources=driverinstallpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=npu.ai,resources=driverupgradestates,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *DriverDaemonSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	metrics.RecordReconcile() // reconcile 호출 시각 기록 (liveness probe 용)
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling DriverDaemonSet", "name", req.NamespacedName)

	// DriverInstallPolicy 목록 조회
	var pols npuv1alpha1.DriverInstallPolicyList
	if err := r.List(ctx, &pols); err != nil {
		return ctrl.Result{}, err
	}

	var states npuv1alpha1.DriverUpgradeStateList
	if err := r.List(ctx, &states); err != nil {
		return ctrl.Result{}, err
	}

	for i := range pols.Items {
		pol := &pols.Items[i]
		if pol.Spec.Driver.Mode != "daemonset" {
			continue
		}
		// TrackOnly 정책은 설치 DaemonSet 을 생성하지 않는다(DUS 추적만).
		// installer 이미지 미비 벤더(예: Tenstorrent, 실제 설치는 DKMS 2차)를 버전 관리에 편입.
		if pol.Spec.Driver.TrackOnly {
			logger.Info("TrackOnly 정책 — driver DaemonSet 생성 skip", "policy", pol.Name, "vendor", pol.Spec.Vendor)
			continue
		}
		if err := r.createOrUpdateDriverDS(ctx, pol, rollbackHeld(pols.Items, states.Items, pol)); err != nil {
			logger.Error(err, "failed to ensure driver DaemonSet", "policy", pol.Name)
			r.Recorder.Eventf(pol, corev1.EventTypeWarning, "ReconcileFailed",
				"Failed to ensure driver DaemonSet for policy %s: %v", pol.Name, err)
			return ctrl.Result{}, err
		}
		logger.Info("Driver DaemonSet ensured", "policy", pol.Name, "vendor", pol.Spec.Vendor)
	}

	return ctrl.Result{}, nil
}

// rollbackHeld 는 이 정책이 맡는 노드 중 하나라도 현재 정책 generation 에서 롤백을 기록했는지다
// (롤백 진행 중 또는 Idle 보류). 그동안 DS 이미지는 상태 기계가 롤백으로 패치한 값이다.
// 정책 매칭은 상태 기계 쪽과 같은 findPolicy 다 — 규칙을 복제하면 한쪽만 고쳐진다.
func rollbackHeld(pols []npuv1alpha1.DriverInstallPolicy, states []npuv1alpha1.DriverUpgradeState,
	pol *npuv1alpha1.DriverInstallPolicy) bool {
	for i := range states {
		s := &states[i]
		if s.Status.RolledBackVersion == "" || s.Status.RolledBackGeneration != pol.Generation {
			continue
		}
		if owner := findPolicy(pols, s.Spec.Vendor, s.Spec.Model); owner != nil && owner.Name == pol.Name {
			return true
		}
	}
	return false
}

// createOrUpdateDriverDS는 DriverInstallPolicy에 맞는 드라이버 DaemonSet을 생성하거나 업데이트합니다.
// keepImage 면 드라이버 컨테이너 이미지를 현재 DS 값으로 둔다(롤백 보류 중, rollbackHeld).
func (r *DriverDaemonSetReconciler) createOrUpdateDriverDS(ctx context.Context, pol *npuv1alpha1.DriverInstallPolicy,
	keepImage bool) error {
	ds := renderDriverDaemonSet(pol)
	if keepImage {
		var cur appsv1.DaemonSet
		err := r.Get(ctx, types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, &cur)
		if client.IgnoreNotFound(err) != nil {
			return err
		}
		if err == nil && len(cur.Spec.Template.Spec.Containers) > 0 && len(ds.Spec.Template.Spec.Containers) > 0 {
			ds.Spec.Template.Spec.Containers[0].Image = cur.Spec.Template.Spec.Containers[0].Image
			logf.FromContext(ctx).Info("롤백 보류 중 — 드라이버 DS 이미지 유지", "policy", pol.Name,
				"image", ds.Spec.Template.Spec.Containers[0].Image)
		}
	}
	return r.createOrUpdateDS(ctx, ds)
}

// createOrUpdateDS는 DaemonSet을 생성하거나 스펙/레이블/어노테이션 변경 시 업데이트합니다.
func (r *DriverDaemonSetReconciler) createOrUpdateDS(ctx context.Context, desired *appsv1.DaemonSet) error {
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
		// OwnerReferences 동기화 — 기존(ownerRef 없이 생성된) DS 도 업그레이드 시 owner 부여.
		cur.OwnerReferences = desired.OwnerReferences
		return r.Update(ctx, &cur)
	}
	return nil
}

// renderDriverDaemonSet은 DriverInstallPolicy 스펙을 기반으로 드라이버 DaemonSet을 빌드합니다.
func renderDriverDaemonSet(pol *npuv1alpha1.DriverInstallPolicy) *appsv1.DaemonSet {
	name := naming.DriverDSName(pol.Spec.Vendor, pol.Spec.Model)
	labels := map[string]string{
		"app.kubernetes.io/name":      "kcloud-driver",
		"app.kubernetes.io/component": "driver",
		"npu.ai/vendor":               strings.ToLower(pol.Spec.Vendor),
	}

	// 벤더별 nodeSelector (model 포함, RNGD 분기 지원)
	nodeSelector := driverNodeSelector(pol.Spec.Vendor, pol.Spec.Model)
	// DriverInstallPolicy에 nodeSelector가 지정된 경우 우선 사용
	if len(pol.Spec.NodeSelector) > 0 {
		nodeSelector = pol.Spec.NodeSelector
	}

	// 벤더별 rmmod 명령 (model 포함, RNGD 분기 지원)
	rmmodCmd := "true" // 미등록 벤더 방어 기본값 — 내릴 모듈 없음(셸 no-op)
	if a, ok := driveradapter.ForModel(pol.Spec.Vendor, pol.Spec.Model); ok {
		rmmodCmd = a.RmmodCommand()
	}

	image := pol.Spec.Driver.Image

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: naming.OperatorNamespace(),
			Labels:    labels,
			// DIP(cluster-scoped) 를 owner 로 지정 → DIP 삭제 시 K8s GC 가
			// driver DaemonSet 을 cascade 삭제(orphan 방지). cluster-scoped owner +
			// namespaced dependent 조합은 허용됨. BlockOwnerDeletion 은 생략하여
			// driverinstallpolicies/finalizers RBAC 의존을 피한다.
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "npu.ai/v1alpha1",
				Kind:       "DriverInstallPolicy",
				Name:       pol.Name,
				UID:        pol.UID,
				Controller: boolPtr(true),
			}},
		},
		Spec: appsv1.DaemonSetSpec{
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.OnDeleteDaemonSetStrategyType,
			},
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					HostPID:      true,
					HostNetwork:  true,
					NodeSelector: nodeSelector,
					Tolerations:  []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					// PreStop 의 rmmod 가 GPU 워크로드 잔재로 hang 될 가능성에 대비한 grace period.
					// kubelet 은 grace 만료 시 SIGKILL 로 종료하므로 PreStop timeout(30s)+여유 30s = 60s.
					TerminationGracePeriodSeconds: int64Ptr(60),
					// operator pod 와 같은 노드에 driver pod 가 spawn 되지 않도록 유도.
					// 단일 노드 클러스터 호환을 위해 required 대신 preferred 사용.
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
								{
									Weight: 100,
									PodAffinityTerm: corev1.PodAffinityTerm{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												// 차트 이름이 라벨 값이다(kcloud-operator.name). 이 값이 차트와 어긋나면
												// 셀렉터가 아무 pod 도 못 집어 anti-affinity 가 무동작이 된다.
												"app.kubernetes.io/name":      "kcloud-operator",
												"app.kubernetes.io/component": "controller",
											},
										},
										TopologyKey: "kubernetes.io/hostname",
									},
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:            "driver-manager",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/usr/local/bin/driver-manager.sh"},
							Env: []corev1.EnvVar{
								{Name: "DRIVER_VERSION", Value: pol.Spec.Driver.Version},
								{Name: "REBOOT_STRATEGY", Value: pol.Spec.RebootStrategy},
								{Name: "VENDOR", Value: pol.Spec.Vendor},
								{Name: "ALLOW_DOWNGRADE", Value: strconv.FormatBool(pol.Spec.Driver.AllowDowngrade)},
								{Name: "VERSION_SOURCE", Value: versionSourceOrDefault(pol.Spec.Driver.VersionSource)},
								{Name: "SKIP_ON_PASSTHROUGH", Value: strconv.FormatBool(pol.Spec.Driver.SkipOnPassthrough)},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: boolPtr(true),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "host-modules", MountPath: "/lib/modules"},
								{Name: "host-var", MountPath: "/var/lib/kcloud-operator"},
							},
						},
						{
							Name:            "check-kernel-headers",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"/usr/local/bin/check-kernel-headers.sh",
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: boolPtr(true),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "host-modules", MountPath: "/lib/modules"},
								{Name: "host-src", MountPath: "/usr/src"},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "driver",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{Name: "DRIVER_VERSION", Value: pol.Spec.Driver.Version},
								{Name: "VENDOR", Value: pol.Spec.Vendor},
								{Name: "ALLOW_DOWNGRADE", Value: strconv.FormatBool(pol.Spec.Driver.AllowDowngrade)},
								{Name: "VERSION_SOURCE", Value: versionSourceOrDefault(pol.Spec.Driver.VersionSource)},
								{Name: "SKIP_ON_PASSTHROUGH", Value: strconv.FormatBool(pol.Spec.Driver.SkipOnPassthrough)},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: boolPtr(true),
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"cat", "/var/lib/kcloud-operator/driver.ready"},
									},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       10,
								FailureThreshold:    180,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"/usr/local/bin/healthcheck.sh"},
									},
								},
								PeriodSeconds:    30,
								FailureThreshold: 3,
							},
							Lifecycle: &corev1.Lifecycle{
								// PreStop 은 timeout 으로 강제 종료 한도를 두어 rmmod hang 시에도
								// kubelet grace(60s) 안에 반드시 종료되도록 한다. timeout 종료(124)
								// 이후에도 컨테이너 SIGTERM 처리 시간이 남아야 하므로 timeout 30s.
								PreStop: &corev1.LifecycleHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"/bin/sh", "-c",
											"timeout 30 sh -c 'rm -f /var/lib/kcloud-operator/driver.ready /tmp/driver-ready; " + rmmodCmd + "' || true"},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "host-modules", MountPath: "/lib/modules"},
								{Name: "host-src", MountPath: "/usr/src"},
								{Name: "host-etc", MountPath: "/etc"},
								{Name: "host-var", MountPath: "/var/lib/kcloud-operator"},
								{Name: "device-plugins", MountPath: "/var/lib/kubelet/device-plugins"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "host-modules",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/lib/modules"},
							},
						},
						{
							Name: "host-src",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/usr/src"},
							},
						},
						{
							Name: "host-etc",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/etc"},
							},
						},
						{
							Name: "host-var",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kcloud-operator"},
							},
						},
						{
							Name: "device-plugins",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"},
							},
						},
					},
				},
			},
		},
	}

	// Furiosa APT 인증 Secret 마운트 (Warboy 전용).
	// Warboy 는 사설 archive.furiosa.ai 인증(furiosa.conf)이 필수라 secret 부재 시 fail-fast 가 맞다.
	// 반면 RNGD 는 공개 repo 를 사용하여 entrypoint 가 /secrets 를 전혀 참조하지 않으므로, secret
	// 오브젝트(furiosa-apt-auth)를 하드코딩 마운트하면 공개 repo 클러스터에서 secret 부재 시 파드가
	// 스케줄되지 못한다. → RNGD 는 마운트를 부착하지 않는다.
	if a, ok := driveradapter.ForModel(pol.Spec.Vendor, pol.Spec.Model); ok && a.AptAuthRequired() {
		ds.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			ds.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "furiosa-auth", MountPath: "/secrets", ReadOnly: true},
		)
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "furiosa-auth",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: driveradapter.AptAuthSecretName},
				},
			},
		)
	}

	// private 레지스트리 pull 을 위해 policy 레벨 imagePullSecrets 를 부착.
	// 빈 목록(미지정)이면 no-op — imagePullPolicy(IfNotPresent) 는 건드리지 않는다.
	applyImagePullSecrets(&ds.Spec.Template.Spec, pol.Spec.ImagePullSecrets)

	// control-plane/master 제외 — DS 모드 driver install 도 제어 평면은 건드리지 않는다
	// (Job 경로는 driver_upgrade_controller 가 이미 control-plane/master 를 skip).
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)

	return ds
}

// driverNodeSelector 는 벤더 장치가 있는 노드를 고르는 셀렉터를 어댑터에서 가져온다.
// 미등록 벤더와 셀렉터 없는 벤더(tenstorrent)는 빈 맵을 돌려준다 — 현재 default 동작이고,
// nodeSelector 필드에 nil 대신 빈 맵을 유지해 회귀를 막는다.
func driverNodeSelector(vendor, model string) map[string]string {
	if a, ok := driveradapter.ForModel(vendor, model); ok {
		if sel := a.PresentNodeSelector(); sel != nil {
			return sel
		}
	}
	return map[string]string{}
}

// versionSourceOrDefault 는 VersionSource 가 빈 값이면 기존 동작인 "Policy" 를 반환합니다.
// CRD default 가 적용되기 전(구 CR)에도 회귀 없이 Policy 로 동작하도록 보장합니다.
func versionSourceOrDefault(vs string) string {
	if vs == "" {
		return "Policy"
	}
	return vs
}

// SetupWithManager는 DriverDaemonSetReconciler를 Manager에 등록합니다.
func (r *DriverDaemonSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.DriverInstallPolicy{}).
		Owns(&appsv1.DaemonSet{}). // owner=DIP 인 driver DS 변경 감시 → 수동 삭제 시 재생성
		Named("driverdaemonset").
		Complete(r)
}
