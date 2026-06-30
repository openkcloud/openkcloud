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

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/metrics"
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

// NPUClusterPolicyReconciler reconciles a NPUClusterPolicy object
type NPUClusterPolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *NPUClusterPolicyReconciler) createOrUpdateDS(ctx context.Context, desired *appsv1.DaemonSet) error {
	var cur appsv1.DaemonSet
	key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}
	if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete

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

	// -- Detector
	if err := r.ensureDetector(ctx, &policy); err != nil {
		logger.Error(err, "failed to ensure Detector")
		r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "Detector", err)
		r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "DetectorFailed", err.Error())
		return ctrl.Result{}, err
	}

	// -- NVIDIA
	if policy.Spec.Nvidia.Enabled {
		logger.Info("Ensuring NVIDIA Device Plugin DaemonSet")
		if err := r.ensureNvidiaDevicePlugin(ctx, &policy); err != nil {
			logger.Error(err, "failed to ensure NVIDIA Device Plugin")
			r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "NvidiaDevicePlugin", err)
			r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "NvidiaDevicePluginFailed", err.Error())
			return ctrl.Result{}, err
		}
	}

	// -- Furiosa
	if policy.Spec.Furiosa.Enabled {
		logger.Info("Ensuring Furiosa Device Plugin DaemonSet")
		if err := r.ensureFuriosaDevicePlugin(ctx, &policy); err != nil {
			logger.Error(err, "failed to ensure Furiosa Device Plugin")
			r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "FuriosaDevicePlugin", err)
			r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "FuriosaDevicePluginFailed", err.Error())
			return ctrl.Result{}, err
		}
	}

	// -- Furiosa RNGD (second-gen; separate DS, NFD-based node affinity)
	if policy.Spec.Furiosa.Rngd.Enabled {
		logger.Info("Ensuring Furiosa RNGD Device Plugin DaemonSet")
		if err := r.ensureFuriosaRngdDevicePlugin(ctx, &policy, policy.Spec.Furiosa.Rngd.PartitionPolicy); err != nil {
			logger.Error(err, "failed to ensure Furiosa RNGD Device Plugin")
			r.Recorder.Eventf(&policy, corev1.EventTypeWarning, "ReconcileFailed", "Failed to ensure %s: %v", "FuriosaRngdDevicePlugin", err)
			r.setReadyCondition(ctx, &policy, metav1.ConditionFalse, "FuriosaRngdDevicePluginFailed", err.Error())
			return ctrl.Result{}, err
		}
	}

	// -- Rebellions ATOM+ (separate namespace rbln-system + PSA privileged + ClusterRole/Binding)
	if policy.Spec.Rebellions.Enabled {
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
	}

	// -- All ensureXxx succeeded: set Ready=True and record success event
	r.setReadyCondition(ctx, &policy, metav1.ConditionTrue, "AllResourcesReady", "All resources reconciled successfully")
	r.Recorder.Eventf(&policy, corev1.EventTypeNormal, "Reconciled", "Successfully reconciled all resources")

	return ctrl.Result{}, nil
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

// cleanupOwnedResources deletes all DaemonSets and ConfigMaps with the owner annotation matching this policy.
func (r *NPUClusterPolicyReconciler) cleanupOwnedResources(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	ownerValue := fmt.Sprintf("%s/%s", policy.Namespace, policy.Name)

	// Cleanup DaemonSets
	var dsList appsv1.DaemonSetList
	if err := r.List(ctx, &dsList, client.InNamespace("kube-system")); err != nil {
		return err
	}
	for i := range dsList.Items {
		ds := &dsList.Items[i]
		if ds.Annotations[ownerAnnotation] == ownerValue {
			if err := r.Delete(ctx, ds); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	// Cleanup ConfigMaps
	var cmList corev1.ConfigMapList
	if err := r.List(ctx, &cmList, client.InNamespace("kube-system")); err != nil {
		return err
	}
	for i := range cmList.Items {
		cm := &cmList.Items[i]
		if cm.Annotations[ownerAnnotation] == ownerValue {
			if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

// setOwnerAnnotation sets the npu.ai/owner annotation on the given ObjectMeta.
func setOwnerAnnotation(obj *metav1.ObjectMeta, policy *npuv1alpha1.NPUClusterPolicy) {
	if obj.Annotations == nil {
		obj.Annotations = map[string]string{}
	}
	obj.Annotations[ownerAnnotation] = fmt.Sprintf("%s/%s", policy.Namespace, policy.Name)
}

// -- ensureNvidiaDevicePlugin creates a DaemonSet for NVIDIA
func (r *NPUClusterPolicyReconciler) ensureNvidiaDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	// 기본 selector (수동 라벨 전략)
	sel := map[string]string{"nvidia.com/gpu.present": "true"}
	if len(policy.Spec.Nvidia.NodeSelector) > 0 {
		sel = policy.Spec.Nvidia.NodeSelector
	}

	labels := map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}
	nvidiaRuntime := vendorNvidia
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nvidia-device-plugin",
			Namespace: "kube-system",
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeSelector:     sel,
					RuntimeClassName: &nvidiaRuntime,
					Tolerations:      []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{{
						Name:            "nvidia-device-plugin",
						Image:           policy.Spec.Nvidia.DevicePluginImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env: []corev1.EnvVar{
							{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"},
							{Name: "NVIDIA_DRIVER_CAPABILITIES", Value: "all"},
						},
						SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: boolPtr(false)},
						VolumeMounts:    []corev1.VolumeMount{{Name: "device-plugin", MountPath: "/var/lib/kubelet/device-plugins"}},
					}},
					Volumes: []corev1.Volume{{
						Name:         "device-plugin",
						VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"}},
					}},
				},
			},
		},
	}
	setOwnerAnnotation(&ds.ObjectMeta, policy)
	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure nvidia device plugin daemonset")
		return err
	}
	log.Info("NVIDIA device plugin daemonset ensured")
	return nil
}

// -- ensureFuriosaDevicePlugin creates a DaemonSet for Furiosa
func (r *NPUClusterPolicyReconciler) ensureFuriosaDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	// nodeSelector
	sel := map[string]string{"furiosa": "true"}
	if len(policy.Spec.Furiosa.NodeSelector) > 0 {
		sel = policy.Spec.Furiosa.NodeSelector
	}

	// ConfigMap (옵션)
	if policy.Spec.Furiosa.ConfigMapName != "" {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      policy.Spec.Furiosa.ConfigMapName,
				Namespace: "kube-system",
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

	labels := map[string]string{"app.kubernetes.io/name": "furiosa-device-plugin"}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "furiosa-device-plugin",
			Namespace: "kube-system",
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
					Name:            "furiosa-device-plugin",
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

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure furiosa device plugin daemonset")
		return err
	}
	log.Info("Furiosa device plugin daemonset ensured")
	return nil
}

// rngdDevicePluginArgs returns the binary args for the Furiosa RNGD device plugin DaemonSet.
// 빈 문자열 또는 "none" 이면 --policy flag 미추가 (회귀 0). 그 외 (single-core/dual-core/quad-core)
// 면 `--policy=<value>` 를 append 한다 (libfuriosa-kubernetes PartitioningPolicy enum 과 1:1).
// upstream v2026.1.0 image 는 --policy flag 를 노출하지 않으므로, 비-none 정책은
// partition-aware custom image (helm values.furiosa.rngd.devicePluginImage 로 override) 필요.
func rngdDevicePluginArgs(partitionPolicy string) []string {
	args := []string{"--debugMode"}
	if partitionPolicy != "" && partitionPolicy != "none" {
		args = append(args, "--policy="+partitionPolicy)
	}
	return args
}

// -- ensureFuriosaRngdDevicePlugin creates a DaemonSet for the Furiosa RNGD (2nd-gen) NPU device plugin.
// NodeSelector uses NFD PCI label feature.node.kubernetes.io/pci-1200_1ed2.present=true by default;
// override via Spec.Furiosa.Rngd.NodeSelector.
//
// Pod spec는 Furiosa 공식 helm chart (furiosa-device-plugin:2026.1.0) 의 DaemonSet 템플릿을 따른다:
//   - entrypoint: ./main (working dir 기준). 바이너리가 PCI scan 으로 RNGD 디바이스를 자동 인식하므로
//     --resource-name 등 인자는 불필요.
//   - /dev 전체 + /sys + device-plugin 소켓 디렉토리를 마운트.
//   - Privileged=false, drop=ALL capabilities, priorityClassName=system-node-critical.
//
// Spec.Furiosa.Rngd.ResourceName / ConfigMapName 필드는 CRD 에 남아 있지만, 현재 공식 이미지가
// 이를 자동 처리하므로 이 함수에서 참조하지 않는다 (backward-compat: 필드 존재는 허용).
//
// partitionPolicy: NPUClusterPolicy.Spec.Furiosa.Rngd.PartitionPolicy 의 string 값
// ("none"/"single-core"/"dual-core"/"quad-core"/"" 중 하나).
// 빈 문자열 또는 "none" 이면 args 변경 없이 기존 1:1 카드 동작 유지 (회귀 0).
// 그 외 값이면 `--policy=<value>` 가 binary args 에 append 된다.
//
// 주의 (2026-04-29 worker-pa 분석): upstream image `docker.io/furiosaai/furiosa-device-plugin:2026.1.0`
// 는 cobra binary 가 `--debugMode` flag 만 노출 — `--policy` flag 미지원. cobra 는 unknown flag 거부.
// 따라서 v2026.1.0 image 로 partitionPolicy="dual-core" 운영 시 binary 시작 자체 실패.
// dual-core/single-core/quad-core 운영을 위해서는 별도 빌드된 partition-aware device plugin image
// (helm values.furiosa.rngd.devicePluginImage 로 override) 가 필요하다.
func (r *NPUClusterPolicyReconciler) ensureFuriosaRngdDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, partitionPolicy string) error {
	log := logf.FromContext(ctx)

	rngd := policy.Spec.Furiosa.Rngd

	// Image (default to upstream release tag when unset; registry path 은 `furiosaai`, 하이픈 없음)
	image := rngd.DevicePluginImage
	if image == "" {
		image = "docker.io/furiosaai/furiosa-device-plugin:2026.1.0"
	}

	// nodeSelector: NFD PCI label by default; allow override via Spec.Furiosa.Rngd.NodeSelector
	sel := map[string]string{"feature.node.kubernetes.io/pci-1200_1ed2.present": "true"}
	if len(rngd.NodeSelector) > 0 {
		sel = rngd.NodeSelector
	}

	labels := map[string]string{"app.kubernetes.io/name": "furiosa-rngd-device-plugin"}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "furiosa-rngd-device-plugin",
			Namespace: "kube-system",
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
					Name:            "furiosa-device-plugin",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"./main"},
					// --debugMode 는 Furiosa device plugin 이 device 를 인식·등록하는 데
					// 필요 (기본 모드에서는 "couldn't recognize any furiosa devices" 출력
					// 후 종료됨. v1.5 follow-up F-1 반영.
					// partitionPolicy 가 비어있거나 "none" 이면 --policy flag 미추가 (기존 동작).
					// 그 외 (single-core/dual-core/quad-core) 면 partition-aware image 가
					// libfuriosa-kubernetes 의 PartitioningPolicy 와 1:1 매핑되는 flag 로 받는다.
					// (upstream v2026.1.0 image 는 미지원 — partition-aware custom image 필요)
					Args: rngdDevicePluginArgs(partitionPolicy),
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

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure furiosa rngd device plugin daemonset")
		return err
	}
	log.Info("Furiosa RNGD device plugin daemonset ensured")
	return nil
}

// Rebellions ATOM+ default constants (Rebellions 공식 daemonset.yaml / configmap.yaml 스펙 준수)
// 2026-04-22: namespace/name 을 기존 `npu-op-*` 컨벤션 (kube-system) 으로 통일.
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

// ensureRbllnsNamespace creates/ensures the Rebellions device plugin namespace with Pod
// Security Admission (PSA) privileged labels when a dedicated namespace is configured.
//
// 2026-04-22: `kube-system` 및 기타 시스템 네임스페이스는 early-return 한다. 이유:
//  1. kube-system 은 이미 존재하며 kubelet/kube-proxy 등 시스템 컴포넌트를 위한 PSA
//     설정이 클러스터 운영자에 의해 관리됨. operator 가 PSA 라벨을 덮어쓰면 전체
//     클러스터 보안 경계가 흔들림.
//  2. PSA 가 기본 `privileged` 가 아닌 `baseline`/`restricted` 로 설정된 환경이라도,
//     kube-system 에 배포되는 DS/DaemonSet 들은 대개 예외 규칙 (system-node-critical
//     priority, legitimate privileged) 으로 허용된다. 별도 namespace label 갱신이
//     필요하지 않다.
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

// ensureRebellionsDevicePlugin creates the DaemonSet running the Rebellions device
// plugin. Spec mirrors Rebellions 공식 daemonset.yaml (hostNetwork, privileged, 9 volumes).
// Note: host-driver-usr-bin mounts /usr/local/bin (Phase 0-A 실측: rbln-stat/rbln-smi 는 /usr/local/bin 에 위치).
func (r *NPUClusterPolicyReconciler) ensureRebellionsDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	ns := rbllnsResolveNamespace(policy)
	cmName := policy.Spec.Rebellions.ConfigMapName
	if cmName == "" {
		cmName = rbllnsConfigMapNameDefault
	}
	image := policy.Spec.Rebellions.DevicePluginImage

	sel := map[string]string{
		"kubernetes.io/arch": "amd64",
		"rebellions-atom":    "true",
	}
	if len(policy.Spec.Rebellions.NodeSelector) > 0 {
		sel = policy.Spec.Rebellions.NodeSelector
	}

	labels := map[string]string{"app.kubernetes.io/name": "rbln-device-plugin"}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbllnsDaemonSetName,
			Namespace: ns,
			Labels:    labels,
		},
	}
	setOwnerAnnotation(&ds.ObjectMeta, policy)
	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				HostNetwork:        true,
				ServiceAccountName: rbllnsServiceAccountName,
				NodeSelector:       sel,
				Tolerations: []corev1.Toleration{
					{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
					{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				},
				Containers: []corev1.Container{{
					Name:            "rbln-device-plugin",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Args:            []string{"--log-dir=device-plugin", "--log-level=10"},
					SecurityContext: &corev1.SecurityContext{
						Privileged: boolPtr(true),
						RunAsUser:  int64Ptr(0),
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("40Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("200Mi"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "devicesock", MountPath: "/var/lib/kubelet/device-plugins"},
						{Name: "plugins-registry", MountPath: "/var/lib/kubelet/plugins_registry"},
						{Name: "log", MountPath: "/var/log"},
						{Name: "config-volume", MountPath: "/etc/pcidp"},
						{Name: "device-info", MountPath: "/var/run/k8s.cni.cncf.io/devinfo/dp"},
						{Name: "host-usr-bin", MountPath: "/host/usr/bin", ReadOnly: true},
						{Name: "host-driver-usr-bin", MountPath: "/host/driver/usr/bin", ReadOnly: true},
						// F-A2 (2026-04-22): Rebellions device plugin 이미지는 `rbln-smi`
						// 를 /usr/bin/rbln-smi 에서 찾음. host 는 /usr/local/bin/rbln-smi
						// 이므로 single-file hostPath 로 경로 bridge. 없어도 동작 자체는
						// 되나 RSD group 생성 경로에서 "rbln-smi not found" 경고 제거.
						{Name: "host-rbln-smi", MountPath: "/usr/bin/rbln-smi", ReadOnly: true},
						{Name: "host-dev", MountPath: "/dev"},
						{Name: "host-sys", MountPath: "/sys"},
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "devicesock", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"}}},
					{Name: "plugins-registry", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins_registry"}}},
					{Name: "log", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/log"}}},
					{Name: "config-volume", VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
						},
					}},
					{Name: "device-info", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/run/k8s.cni.cncf.io/devinfo/dp"}}},
					{Name: "host-usr-bin", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/usr/bin"}}},
					{Name: "host-driver-usr-bin", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/usr/local/bin"}}},
					// F-A2: single-file hostPath (HostPathFile) for /usr/bin/rbln-smi bridge
					{Name: "host-rbln-smi", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
						Path: "/usr/local/bin/rbln-smi",
						Type: hostPathFilePtr(),
					}}},
					{Name: "host-dev", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
					{Name: "host-sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
				},
			},
		},
	}

	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)

	if err := r.createOrUpdateDS(ctx, ds); err != nil {
		log.Error(err, "failed to ensure rebellions device plugin daemonset")
		return err
	}
	log.Info("Rebellions device plugin daemonset ensured")
	return nil
}

func (r *NPUClusterPolicyReconciler) ensureDetector(ctx context.Context, pol *npuv1alpha1.NPUClusterPolicy) error {
	if pol.Spec.Detector == nil || pol.Spec.Detector.Image == "" {
		return fmt.Errorf("detector image must be specified in NPUClusterPolicy.spec.detector.image")
	}

	image := pol.Spec.Detector.Image
	ds := renderDetectorDS(image)
	setOwnerAnnotation(&ds.ObjectMeta, pol)
	return r.createOrUpdateDS(ctx, ds)
}

func renderDetectorDS(image string) *appsv1.DaemonSet {
	labels := map[string]string{"app.kubernetes.io/name": "kcloud-detector"}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kcloud-detector",
			Namespace: "kube-system",
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: "npu-detector",
					Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{{
						Name:            "detector",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env: []corev1.EnvVar{{
							Name:      "NODE_NAME",
							ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}},
						}},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "host-proc", MountPath: "/host/proc", ReadOnly: true},
							{Name: "host-dev", MountPath: "/host/dev", ReadOnly: true},
							{Name: "host-var", MountPath: "/host/var", ReadOnly: true},
							{Name: "host-sys", MountPath: "/host/sys", ReadOnly: true},
							// detector binary 가 /usr/bin/nvidia-smi 검사로 nvidia userland 존재 판단.
							// /host/usr mount 없으면 nvidiaUserlandPresent()=false → driverVersion="" 보고.
							{Name: "host-usr", MountPath: "/host/usr", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "host-proc", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/proc"}}},
						{Name: "host-dev", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev"}}},
						{Name: "host-var", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var"}}},
						{Name: "host-sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys"}}},
						{Name: "host-usr", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/usr"}}},
					},
				},
			},
		},
	}
	// detector는 /dev를 ReadOnly로 마운트하므로, 드라이버 업그레이드 중 rmmod 간섭을 막기 위해
	// device-plugin과 동일하게 업그레이드 라벨이 붙은 노드에는 스케줄되지 않도록 한다.
	applyDriverUpgradeAntiAffinity(&ds.Spec.Template.Spec)
	return ds
}

// SetupWithManager sets up the controller with the Manager.
func (r *NPUClusterPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.NPUClusterPolicy{}).
		Named("npuclusterpolicy").
		Complete(r)
}

// -- Add
func boolPtr(b bool) *bool {
	return &b
}

func int64Ptr(i int64) *int64 {
	return &i
}

// hostPathFilePtr returns a pointer to HostPathFile HostPathType for single-file hostPath volumes.
func hostPathFilePtr() *corev1.HostPathType {
	t := corev1.HostPathFile
	return &t
}

// applyDriverUpgradeAntiAffinity는 기존 Affinity를 보존하면서
// driver-upgrading-blocking 라벨이 없는 노드에만 스케줄되도록 제약을 추가한다.
// architectural plan §4.4 옵션 A: 좁은 lifecycle 의 blocking 라벨로 phase-aware 차단.
// Cordoning ~ Upgrading 단계: 라벨 활성 → detector / device-plugin 차단 (rmmod 보호).
// Validating 단계: 라벨 자동 제거 → detector spawn 가능 → NDR 갱신 → Validator 통과.
func applyDriverUpgradeAntiAffinity(spec *corev1.PodSpec) {
	req := corev1.NodeSelectorRequirement{
		Key:      upgrade.DriverUpgradingBlockingLabelKey,
		Operator: corev1.NodeSelectorOpDoesNotExist,
	}
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
			ns.NodeSelectorTerms[i].MatchExpressions, req)
	}
	spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = ns
}
