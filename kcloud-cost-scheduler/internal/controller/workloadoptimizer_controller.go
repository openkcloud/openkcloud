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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
	"github.com/KETI-Cloud-Platform/k8s-workload-operator/pkg/metrics"
	"github.com/KETI-Cloud-Platform/k8s-workload-operator/pkg/optimizer"
	"github.com/KETI-Cloud-Platform/k8s-workload-operator/pkg/scheduler"
)

// WorkloadOptimizerReconciler reconciles a WorkloadOptimizer object
type WorkloadOptimizerReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Optimizer *optimizer.Engine
	Scheduler *scheduler.Scheduler
	Metrics   *metrics.MetricsCollector
}

//+kubebuilder:rbac:groups=kcloud.io,resources=workloadoptimizers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kcloud.io,resources=workloadoptimizers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kcloud.io,resources=workloadoptimizers/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *WorkloadOptimizerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the WorkloadOptimizer instance
	var wo kcloudv1alpha1.WorkloadOptimizer
	if err := r.Get(ctx, req.NamespacedName, &wo); err != nil {
		if errors.IsNotFound(err) {
			log.Info("WorkloadOptimizer resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get WorkloadOptimizer")
		return ctrl.Result{}, err
	}

	// Check if the resource is being deleted
	if !wo.DeletionTimestamp.IsZero() {
		log.Info("WorkloadOptimizer is being deleted")
		// Record deletion metrics
		if r.Metrics != nil {
			r.Metrics.RecordWorkloadOptimizerDeleted(wo.Namespace, wo.Name, wo.Spec.WorkloadType)
		}
		return r.handleDeletion(ctx, &wo)
	}

	// Add finalizer if not present
	if err := r.addFinalizer(ctx, &wo); err != nil {
		return ctrl.Result{}, err
	}

	// Analyze current state
	currentState, err := r.analyzeCurrentState(ctx, &wo)
	if err != nil {
		log.Error(err, "Failed to analyze current state")
		return ctrl.Result{}, err
	}

	// Perform optimization
	optimizationResult, err := r.performOptimization(ctx, &wo, currentState)
	if err != nil {
		log.Error(err, "Failed to perform optimization")
		return ctrl.Result{}, err
	}

	// Update status
	if err := r.updateStatus(ctx, &wo, optimizationResult); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	// Record metrics
	if r.Metrics != nil {
		r.Metrics.RecordWorkloadOptimizerCreated(wo.Namespace, wo.Name, wo.Spec.WorkloadType)
		r.Metrics.RecordWorkloadOptimizerPhase(wo.Namespace, wo.Name, wo.Status.Phase, wo.Spec.WorkloadType)
		if optimizationResult.Score > 0 {
			r.Metrics.RecordWorkloadOptimizerScore(wo.Namespace, wo.Name, wo.Spec.WorkloadType, optimizationResult.Score)
		}
		if optimizationResult.EstimatedCost > 0 {
			r.Metrics.RecordWorkloadOptimizerCost(wo.Namespace, wo.Name, wo.Spec.WorkloadType, optimizationResult.EstimatedCost)
		}
		if optimizationResult.EstimatedPower > 0 {
			r.Metrics.RecordWorkloadOptimizerPower(wo.Namespace, wo.Name, wo.Spec.WorkloadType, optimizationResult.EstimatedPower)
		}
	}

	// Set requeue time based on optimization result
	requeueAfter := time.Minute * 5
	if optimizationResult.RequiresRescheduling {
		requeueAfter = time.Minute * 1
	}

	log.Info("Reconciliation completed successfully",
		"optimizationScore", optimizationResult.Score,
		"estimatedCost", optimizationResult.EstimatedCost,
		"estimatedPower", optimizationResult.EstimatedPower)

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// analyzeCurrentState analyzes the current state of the workload
func (r *WorkloadOptimizerReconciler) analyzeCurrentState(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer) (*optimizer.WorkloadState, error) {
	log := log.FromContext(ctx)

	// Get pods associated with this workload
	pods, err := r.getAssociatedPods(ctx, wo)
	if err != nil {
		return nil, fmt.Errorf("failed to get associated pods: %w", err)
	}

	// Get available nodes
	nodes, err := r.getAvailableNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get available nodes: %w", err)
	}

	log.Info("Current state analyzed",
		"podsCount", len(pods),
		"nodesCount", len(nodes))

	return &optimizer.WorkloadState{
		WorkloadOptimizer: wo,
		Pods:              pods,
		AvailableNodes:    nodes,
	}, nil
}

// performOptimization performs optimization using the optimizer engine
func (r *WorkloadOptimizerReconciler) performOptimization(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer, state *optimizer.WorkloadState) (*optimizer.OptimizationResult, error) {
	log := log.FromContext(ctx)

	// Use the optimizer engine to optimize the workload
	result := r.Optimizer.Optimize(ctx, state)

	log.Info("Optimization performed",
		"score", result.Score,
		"estimatedCost", result.EstimatedCost,
		"estimatedPower", result.EstimatedPower,
		"requiresRescheduling", result.RequiresRescheduling)

	return result, nil
}

// updateStatus updates the status of the WorkloadOptimizer
func (r *WorkloadOptimizerReconciler) updateStatus(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer, result *optimizer.OptimizationResult) error {
	log := log.FromContext(ctx)

	// Update status fields
	now := metav1.Now()
	wo.Status.Phase = r.determinePhase(result)
	wo.Status.CurrentCost = &result.EstimatedCost
	wo.Status.CurrentPower = &result.EstimatedPower
	wo.Status.AssignedNode = &result.AssignedNode
	wo.Status.OptimizationScore = &result.Score
	wo.Status.LastOptimizationTime = &now
	wo.Status.Replicas = &result.RecommendedReplicas

	// Update conditions
	r.updateConditions(wo, result)

	// Update the status
	if err := r.Status().Update(ctx, wo); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	log.Info("Status updated successfully", "phase", wo.Status.Phase)
	return nil
}

// determinePhase determines the current phase based on optimization result
func (r *WorkloadOptimizerReconciler) determinePhase(result *optimizer.OptimizationResult) string {
	if result.Score >= 0.8 {
		return "Optimized"
	} else if result.Score >= 0.5 {
		return "Optimizing"
	} else {
		return "Pending"
	}
}

// updateConditions updates the conditions based on optimization result
func (r *WorkloadOptimizerReconciler) updateConditions(wo *kcloudv1alpha1.WorkloadOptimizer, result *optimizer.OptimizationResult) {
	now := metav1.Now()

	// Update or add conditions
	conditions := []metav1.Condition{
		{
			Type:               "Available",
			Status:             metav1.ConditionTrue,
			Reason:             "OptimizationCompleted",
			Message:            fmt.Sprintf("Workload optimization completed with score %.2f", result.Score),
			LastTransitionTime: now,
		},
		{
			Type:               "CostOptimized",
			Status:             metav1.ConditionTrue,
			Reason:             "WithinBudget",
			Message:            fmt.Sprintf("Current cost $%.2f/hour is within constraints", result.EstimatedCost),
			LastTransitionTime: now,
		},
		{
			Type:               "PowerOptimized",
			Status:             metav1.ConditionTrue,
			Reason:             "WithinLimits",
			Message:            fmt.Sprintf("Current power %.2fW is within constraints", result.EstimatedPower),
			LastTransitionTime: now,
		},
	}

	// Update conditions in the status
	for _, condition := range conditions {
		metav1.SetMetaDataAnnotation(&wo.ObjectMeta, fmt.Sprintf("condition-%s", condition.Type), condition.Message)
	}
	wo.Status.Conditions = conditions
}

// getAssociatedPods gets pods associated with this workload optimizer
func (r *WorkloadOptimizerReconciler) getAssociatedPods(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer) ([]corev1.Pod, error) {
	var pods corev1.PodList
	err := r.List(ctx, &pods, client.InNamespace(wo.Namespace))
	if err != nil {
		return nil, err
	}

	// Filter pods that match this workload optimizer
	var associatedPods []corev1.Pod
	for _, pod := range pods.Items {
		// Check if pod has the workload optimizer label or annotation
		if pod.Labels["workload-optimizer"] == wo.Name ||
			pod.Annotations["workload-optimizer"] == wo.Name {
			associatedPods = append(associatedPods, pod)
		}
	}

	return associatedPods, nil
}

// getAvailableNodes gets all available nodes in the cluster
func (r *WorkloadOptimizerReconciler) getAvailableNodes(ctx context.Context) ([]corev1.Node, error) {
	var nodes corev1.NodeList
	err := r.List(ctx, &nodes)
	if err != nil {
		return nil, err
	}

	// Filter only ready nodes
	var availableNodes []corev1.Node
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				availableNodes = append(availableNodes, node)
				break
			}
		}
	}

	return availableNodes, nil
}

// addFinalizer adds finalizer to the WorkloadOptimizer if not present
func (r *WorkloadOptimizerReconciler) addFinalizer(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer) error {
	finalizerName := "workloadoptimizer.kcloud.io/finalizer"

	if !containsString(wo.ObjectMeta.Finalizers, finalizerName) {
		wo.ObjectMeta.Finalizers = append(wo.ObjectMeta.Finalizers, finalizerName)
		return r.Update(ctx, wo)
	}
	return nil
}

// handleDeletion handles the deletion of WorkloadOptimizer
func (r *WorkloadOptimizerReconciler) handleDeletion(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	finalizerName := "workloadoptimizer.kcloud.io/finalizer"

	if containsString(wo.ObjectMeta.Finalizers, finalizerName) {
		// Perform cleanup operations here
		log.Info("Performing cleanup operations")

		// Remove finalizer
		wo.ObjectMeta.Finalizers = removeString(wo.ObjectMeta.Finalizers, finalizerName)
		if err := r.Update(ctx, wo); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadOptimizerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kcloudv1alpha1.WorkloadOptimizer{}).
		Complete(r)
}

// Helper functions
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}
