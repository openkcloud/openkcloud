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

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// PodMutator mutates pods to apply optimization policies
type PodMutator struct {
	Client  client.Client
	decoder admission.Decoder
}

// NewPodMutator creates a new pod mutator
func NewPodMutator(client client.Client) *PodMutator {
	return &PodMutator{
		Client: client,
	}
}

// Handle handles pod mutation requests
func (m *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := log.FromContext(ctx)

	pod := &corev1.Pod{}
	if err := m.decoder.Decode(req, pod); err != nil {
		logger.Error(err, "Failed to decode pod")
		return admission.Errored(400, err)
	}

	logger.V(1).Info("Processing pod mutation",
		"pod", pod.Name,
		"namespace", pod.Namespace)

	// Check if pod should be optimized
	if !m.shouldOptimizePod(pod) {
		logger.V(1).Info("Pod does not need optimization", "pod", pod.Name)
		return admission.Allowed("No optimization needed")
	}

	// Find applicable WorkloadOptimizer
	wo, err := m.findApplicableWorkloadOptimizer(ctx, pod)
	if err != nil {
		logger.Error(err, "Failed to find applicable WorkloadOptimizer")
		return admission.Allowed("No WorkloadOptimizer found")
	}

	if wo == nil {
		logger.V(1).Info("No applicable WorkloadOptimizer found", "pod", pod.Name)
		return admission.Allowed("No applicable WorkloadOptimizer")
	}

	// Apply optimization to pod
	originalPod := pod.DeepCopy()
	if err := m.applyOptimizationToPod(pod, wo); err != nil {
		logger.Error(err, "Failed to apply optimization to pod")
		return admission.Errored(500, err)
	}

	logger.Info("Pod optimization applied",
		"pod", pod.Name,
		"workloadOptimizer", wo.Name,
		"changes", m.getChanges(originalPod, pod))

	// Create patch response
	patch, err := createPatch(originalPod, pod)
	if err != nil {
		logger.Error(err, "Failed to create patch")
		return admission.Errored(500, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, patch)
}

// createPatch creates a JSON patch between two pods
func createPatch(original, modified *corev1.Pod) ([]byte, error) {
	// Simple implementation - in production, use proper JSON patch library
	_, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}

	modifiedBytes, err := json.Marshal(modified)
	if err != nil {
		return nil, err
	}

	// For now, return the modified pod as patch
	return modifiedBytes, nil
}

// shouldOptimizePod determines if a pod should be optimized
func (m *PodMutator) shouldOptimizePod(pod *corev1.Pod) bool {
	// Skip system pods
	if pod.Namespace == "kube-system" || pod.Namespace == "kube-public" || pod.Namespace == "kube-node-lease" {
		return false
	}

	// Skip pods with optimization disabled annotation
	if pod.Annotations != nil {
		if disabled, exists := pod.Annotations["kcloud.io/optimization-disabled"]; exists && disabled == "true" {
			return false
		}
	}

	// Skip pods that are already optimized
	if pod.Annotations != nil {
		if optimized, exists := pod.Annotations["kcloud.io/optimized"]; exists && optimized == "true" {
			return false
		}
	}

	// Only optimize pods with containers
	if len(pod.Spec.Containers) == 0 {
		return false
	}

	return true
}

// findApplicableWorkloadOptimizer finds the WorkloadOptimizer that applies to this pod
func (m *PodMutator) findApplicableWorkloadOptimizer(ctx context.Context, pod *corev1.Pod) (*kcloudv1alpha1.WorkloadOptimizer, error) {
	logger := log.FromContext(ctx)

	// First, check if pod has explicit WorkloadOptimizer reference
	if pod.Annotations != nil {
		if woName, exists := pod.Annotations["kcloud.io/workload-optimizer"]; exists {
			// TODO: Implement when CRD types are properly generated
			logger.V(1).Info("WorkloadOptimizer reference found but CRD not available", "name", woName)
			return nil, nil
		}
	}

	// Try to infer WorkloadOptimizer based on pod characteristics
	wo, err := m.inferWorkloadOptimizer(ctx, pod)
	if err != nil {
		logger.Error(err, "Failed to infer WorkloadOptimizer")
		return nil, err
	}

	return wo, nil
}

// inferWorkloadOptimizer infers the appropriate WorkloadOptimizer based on pod characteristics
func (m *PodMutator) inferWorkloadOptimizer(ctx context.Context, pod *corev1.Pod) (*kcloudv1alpha1.WorkloadOptimizer, error) {
	_ = log.FromContext(ctx)

	// TODO: Implement when CRD types are properly generated
	// For now, return nil to indicate no WorkloadOptimizer found
	return nil, nil
}

// calculateMatchScore calculates how well a WorkloadOptimizer matches a pod
func (m *PodMutator) calculateMatchScore(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) float64 {
	score := 0.0

	// Check workload type match
	podWorkloadType := m.inferPodWorkloadType(pod)
	if podWorkloadType == wo.Spec.WorkloadType {
		score += 0.4
	}

	// Check resource requirements match
	if m.resourcesMatch(pod, wo) {
		score += 0.3
	}

	// Check labels/annotations match
	if m.labelsMatch(pod, wo) {
		score += 0.2
	}

	// Check namespace match
	if pod.Namespace == wo.Namespace {
		score += 0.1
	}

	return score
}

// inferPodWorkloadType infers the workload type from pod characteristics
func (m *PodMutator) inferPodWorkloadType(pod *corev1.Pod) string {
	// Check annotations first
	if pod.Annotations != nil {
		if workloadType, exists := pod.Annotations["kcloud.io/workload-type"]; exists {
			return workloadType
		}
	}

	// Check labels
	if pod.Labels != nil {
		if workloadType, exists := pod.Labels["workload-type"]; exists {
			return workloadType
		}
	}

	// Infer from pod name and characteristics
	podName := strings.ToLower(pod.Name)

	if strings.Contains(podName, "training") || strings.Contains(podName, "train") {
		return "training"
	}
	if strings.Contains(podName, "serving") || strings.Contains(podName, "serve") {
		return "serving"
	}
	if strings.Contains(podName, "inference") || strings.Contains(podName, "infer") {
		return "inference"
	}
	if strings.Contains(podName, "batch") {
		return "batch"
	}

	// Default to serving for stateless workloads
	return "serving"
}

// resourcesMatch checks if pod resource requirements match WorkloadOptimizer
func (m *PodMutator) resourcesMatch(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) bool {
	// Calculate total pod resource requirements
	totalCPU := resource.MustParse("0")
	totalMemory := resource.MustParse("0")
	totalGPU := int32(0)
	totalNPU := int32(0)

	for _, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if cpu, exists := container.Resources.Requests[corev1.ResourceCPU]; exists {
				totalCPU.Add(cpu)
			}
			if memory, exists := container.Resources.Requests[corev1.ResourceMemory]; exists {
				totalMemory.Add(memory)
			}
			if gpu, exists := container.Resources.Requests["nvidia.com/gpu"]; exists {
				gpuValue, _ := strconv.ParseInt(gpu.String(), 10, 32)
				totalGPU += int32(gpuValue)
			}
			if npu, exists := container.Resources.Requests["npu.com/npu"]; exists {
				npuValue, _ := strconv.ParseInt(npu.String(), 10, 32)
				totalNPU += int32(npuValue)
			}
		}
	}

	// Compare with WorkloadOptimizer requirements
	woCPU := resource.MustParse(wo.Spec.Resources.CPU)
	woMemory := resource.MustParse(wo.Spec.Resources.Memory)

	// Allow some tolerance (within 20%)
	cpuTolerance := resource.MustParse("0.2")
	memoryTolerance := resource.MustParse("200Mi")

	cpuUpperBound := woCPU.DeepCopy()
	cpuUpperBound.Add(cpuTolerance)
	cpuLowerBound := woCPU.DeepCopy()
	cpuLowerBound.Sub(cpuTolerance)

	memoryUpperBound := woMemory.DeepCopy()
	memoryUpperBound.Add(memoryTolerance)
	memoryLowerBound := woMemory.DeepCopy()
	memoryLowerBound.Sub(memoryTolerance)

	cpuMatch := totalCPU.Cmp(cpuUpperBound) <= 0 && totalCPU.Cmp(cpuLowerBound) >= 0
	memoryMatch := totalMemory.Cmp(memoryUpperBound) <= 0 && totalMemory.Cmp(memoryLowerBound) >= 0
	gpuMatch := totalGPU == wo.Spec.Resources.GPU
	npuMatch := totalNPU == wo.Spec.Resources.NPU

	return cpuMatch && memoryMatch && gpuMatch && npuMatch
}

// labelsMatch checks if pod labels match WorkloadOptimizer selector
func (m *PodMutator) labelsMatch(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) bool {
	// For now, return true as we don't have selector in WorkloadOptimizer
	// In the future, WorkloadOptimizer could have a selector field
	return true
}

// applyOptimizationToPod applies optimization policies to the pod
func (m *PodMutator) applyOptimizationToPod(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) error {
	logger := log.FromContext(context.Background())

	// Add optimization annotations
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["kcloud.io/optimized"] = "true"
	pod.Annotations["kcloud.io/workload-optimizer"] = wo.Name
	pod.Annotations["kcloud.io/workload-type"] = wo.Spec.WorkloadType

	// Apply resource optimization
	if err := m.applyResourceOptimization(pod, wo); err != nil {
		logger.Error(err, "Failed to apply resource optimization")
		return err
	}

	// Apply node selection optimization
	if err := m.applyNodeSelectionOptimization(pod, wo); err != nil {
		logger.Error(err, "Failed to apply node selection optimization")
		return err
	}

	// Apply cost optimization
	if err := m.applyCostOptimization(pod, wo); err != nil {
		logger.Error(err, "Failed to apply cost optimization")
		return err
	}

	// Apply power optimization
	if err := m.applyPowerOptimization(pod, wo); err != nil {
		logger.Error(err, "Failed to apply power optimization")
		return err
	}

	logger.Info("Optimization applied to pod",
		"pod", pod.Name,
		"workloadOptimizer", wo.Name)

	return nil
}

// applyResourceOptimization applies resource-related optimizations
func (m *PodMutator) applyResourceOptimization(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) error {
	// Set resource requests and limits based on WorkloadOptimizer
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]

		if container.Resources.Requests == nil {
			container.Resources.Requests = make(corev1.ResourceList)
		}
		if container.Resources.Limits == nil {
			container.Resources.Limits = make(corev1.ResourceList)
		}

		// Set CPU
		container.Resources.Requests[corev1.ResourceCPU] = resource.MustParse(wo.Spec.Resources.CPU)
		container.Resources.Limits[corev1.ResourceCPU] = resource.MustParse(wo.Spec.Resources.CPU)

		// Set Memory
		container.Resources.Requests[corev1.ResourceMemory] = resource.MustParse(wo.Spec.Resources.Memory)
		container.Resources.Limits[corev1.ResourceMemory] = resource.MustParse(wo.Spec.Resources.Memory)

		// Set GPU if specified
		if wo.Spec.Resources.GPU > 0 {
			gpuResource := resource.MustParse(strconv.FormatInt(int64(wo.Spec.Resources.GPU), 10))
			container.Resources.Requests["nvidia.com/gpu"] = gpuResource
			container.Resources.Limits["nvidia.com/gpu"] = gpuResource
		}

		// Set NPU if specified
		if wo.Spec.Resources.NPU > 0 {
			npuResource := resource.MustParse(strconv.FormatInt(int64(wo.Spec.Resources.NPU), 10))
			container.Resources.Requests["npu.com/npu"] = npuResource
			container.Resources.Limits["npu.com/npu"] = npuResource
		}
	}

	return nil
}

// applyNodeSelectionOptimization applies node selection optimizations
func (m *PodMutator) applyNodeSelectionOptimization(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) error {
	if wo.Spec.PlacementPolicy == nil {
		return nil
	}

	// Apply node selector
	if wo.Spec.PlacementPolicy.NodeSelector != nil {
		if pod.Spec.NodeSelector == nil {
			pod.Spec.NodeSelector = make(map[string]string)
		}
		for key, value := range wo.Spec.PlacementPolicy.NodeSelector {
			pod.Spec.NodeSelector[key] = value
		}
	}

	// Apply affinity rules
	if len(wo.Spec.PlacementPolicy.Affinity) > 0 {
		if pod.Spec.Affinity == nil {
			pod.Spec.Affinity = &corev1.Affinity{}
		}
		if pod.Spec.Affinity.NodeAffinity == nil {
			pod.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
		}

		// Convert WorkloadOptimizer affinity to Kubernetes affinity
		for _, affinityRule := range wo.Spec.PlacementPolicy.Affinity {
			requirement := corev1.NodeSelectorRequirement{
				Key:      affinityRule.Key,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{affinityRule.Value},
			}

			if pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
				pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{}
			}

			pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(
				pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms,
				corev1.NodeSelectorTerm{
					MatchExpressions: []corev1.NodeSelectorRequirement{requirement},
				},
			)
		}
	}

	return nil
}

// applyCostOptimization applies cost-related optimizations
func (m *PodMutator) applyCostOptimization(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) error {
	if wo.Spec.CostConstraints == nil {
		return nil
	}

	// Add cost optimization annotations
	if wo.Spec.CostConstraints.PreferSpot {
		pod.Annotations["kcloud.io/prefer-spot-instances"] = "true"
	}

	if wo.Spec.CostConstraints.BudgetLimit != nil {
		pod.Annotations["kcloud.io/budget-limit"] = fmt.Sprintf("%.2f", *wo.Spec.CostConstraints.BudgetLimit)
	}

	// Add cost-related labels for node selection
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}

	if wo.Spec.CostConstraints.PreferSpot {
		pod.Labels["kcloud.io/cost-tier"] = "spot"
	} else {
		pod.Labels["kcloud.io/cost-tier"] = "on-demand"
	}

	return nil
}

// applyPowerOptimization applies power-related optimizations
func (m *PodMutator) applyPowerOptimization(pod *corev1.Pod, wo *kcloudv1alpha1.WorkloadOptimizer) error {
	if wo.Spec.PowerConstraints == nil {
		return nil
	}

	// Add power optimization annotations
	if wo.Spec.PowerConstraints.PreferGreen {
		pod.Annotations["kcloud.io/prefer-green-energy"] = "true"
	}

	pod.Annotations["kcloud.io/max-power-usage"] = fmt.Sprintf("%.2f", wo.Spec.PowerConstraints.MaxPowerUsage)

	// Add power-related labels for node selection
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}

	if wo.Spec.PowerConstraints.PreferGreen {
		pod.Labels["kcloud.io/energy-source"] = "renewable"
	} else {
		pod.Labels["kcloud.io/energy-source"] = "mixed"
	}

	return nil
}

// getChanges returns a summary of changes made to the pod
func (m *PodMutator) getChanges(original, modified *corev1.Pod) []string {
	var changes []string

	// Check annotations
	if len(modified.Annotations) > len(original.Annotations) {
		changes = append(changes, "Added optimization annotations")
	}

	// Check labels
	if len(modified.Labels) > len(original.Labels) {
		changes = append(changes, "Added optimization labels")
	}

	// Check node selector
	if len(modified.Spec.NodeSelector) > len(original.Spec.NodeSelector) {
		changes = append(changes, "Applied node selector")
	}

	// Check affinity
	if modified.Spec.Affinity != nil && original.Spec.Affinity == nil {
		changes = append(changes, "Applied node affinity")
	}

	// Check resources
	for i, container := range modified.Spec.Containers {
		if i < len(original.Spec.Containers) {
			originalContainer := original.Spec.Containers[i]
			if len(container.Resources.Requests) > len(originalContainer.Resources.Requests) {
				changes = append(changes, "Updated resource requests")
			}
			if len(container.Resources.Limits) > len(originalContainer.Resources.Limits) {
				changes = append(changes, "Updated resource limits")
			}
		}
	}

	return changes
}

// InjectDecoder injects the decoder
func (m *PodMutator) InjectDecoder(d admission.Decoder) error {
	m.decoder = d
	return nil
}
