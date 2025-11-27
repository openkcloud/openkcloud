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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "npu-operator/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NPUClusterPolicyReconciler reconciles a NPUClusterPolicy object
type NPUClusterPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=npu.ai,resources=npuclusterpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=npu.ai,resources=npuclusterpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=npu.ai,resources=npuclusterpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NPUClusterPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *NPUClusterPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling NPUClusterPolicy", "name", req.NamespacedName)

	//-- Get CR
	var policy npuv1alpha1.NPUClusterPolicy
	if err := r.Get(ctx, req.NamespacedName, &policy); err != nil {
		logger.Error(err, "unable to fetch NPUClusterPolicy")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	//-- NVIDIA
	if policy.Spec.Nvidia.Enabled {
		logger.Info("Ensuring NVIDIA Device Plugin DaemonSet")
		if err := r.ensureNvidiaDevicePlugin(ctx, &policy); err != nil {
			logger.Error(err, "failed to ensure NVIDIA Device Plugin")
			return ctrl.Result{}, err
		}
	}

	//-- Furiosa
	if policy.Spec.Furiosa.Enabled {
		logger.Info("Ensuring Furiosa Device Plugin DaemonSet")
		if err := r.ensureFuriosaDevicePlugin(ctx, &policy); err != nil {
			logger.Error(err, "failed to ensure Furiosa Device Plugin")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// -- ensureNvidiaDevicePlugin creates a DaemonSet for NVIDIA
func (r *NPUClusterPolicyReconciler) ensureNvidiaDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	labels := map[string]string{
		"app.kubernetes.io/name": "nvidia-device-plugin",
	}
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
					NodeSelector: map[string]string{"nvidia.com/gpu.present": "true"},
					Containers: []corev1.Container{
						{
							Name:            "nvidia-device-plugin",
							Image:           policy.Spec.Nvidia.DevicePluginImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "device-plugin", MountPath: "/var/lib/kubelet/device-plugins"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "device-plugin",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"},
							},
						},
					},
				},
			},
		},
	}

	if err := r.Client.Create(ctx, ds); err != nil && !apierrors.IsAlreadyExists(err) {
		log.Error(err, "failed to create nvidia device plugin daemonset")
		return err
	}

	log.Info("NVIDIA device plugin daemonset ensured")
	return nil
}

// -- ensureFuriosaDevicePlugin creates a DaemonSet for Furiosa
func (r *NPUClusterPolicyReconciler) ensureFuriosaDevicePlugin(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	log := logf.FromContext(ctx)

	// 1. Create ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policy.Spec.Furiosa.ConfigMapName,
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"config.yaml": `defaultPe: Fusion
disabledDevices: []
interval: 10`,
		},
	}
	if err := r.Client.Create(ctx, configMap); err != nil && !apierrors.IsAlreadyExists(err) {
		log.Error(err, "failed to create furiosa device plugin configmap")
		return err
	}

	// 2. Create DaemonSet
	labels := map[string]string{
		"app.kubernetes.io/name": "furiosa-device-plugin",
	}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "furiosa-device-plugin",
			Namespace: "kube-system",
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{"furiosa": "true"},
					Containers: []corev1.Container{
						{
							Name:            "furiosa-device-plugin",
							Image:           policy.Spec.Furiosa.DevicePluginImage,
							ImagePullPolicy: corev1.PullAlways,
							Command:         []string{"/usr/bin/k8s-device-plugin"},
							Args:            []string{"--config-file", "/etc/furiosa/config.yaml"},
							Env: []corev1.EnvVar{
								{
									Name: "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
									},
								},
								{Name: "RUST_LOG", Value: "info"},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "sys", MountPath: "/sys"},
								{Name: "dev", MountPath: "/dev"},
								{Name: "dp", MountPath: "/var/lib/kubelet/device-plugins"},
								{Name: "config", MountPath: "/etc/furiosa"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "sys",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/sys"},
							},
						},
						{
							Name: "dev",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/dev"},
							},
						},
						{
							Name: "dp",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/device-plugins"},
							},
						},
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: policy.Spec.Furiosa.ConfigMapName,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := r.Client.Create(ctx, ds); err != nil && !apierrors.IsAlreadyExists(err) {
		log.Error(err, "failed to create furiosa device plugin daemonset")
		return err
	}

	log.Info("Furiosa device plugin daemonset ensured")
	return nil
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
