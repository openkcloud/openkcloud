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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

// WorkloadOptimizerValidator validates WorkloadOptimizer resources
type WorkloadOptimizerValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// NewWorkloadOptimizerValidator creates a new WorkloadOptimizer validator
func NewWorkloadOptimizerValidator(client client.Client) *WorkloadOptimizerValidator {
	return &WorkloadOptimizerValidator{
		Client: client,
	}
}

// Handle handles WorkloadOptimizer validation requests
func (v *WorkloadOptimizerValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := log.FromContext(ctx)

	wo := &kcloudv1alpha1.WorkloadOptimizer{}
	if err := v.decoder.Decode(req, wo); err != nil {
		log.Error(err, "Failed to decode WorkloadOptimizer")
		return admission.Errored(400, err)
	}

	log.V(1).Info("Processing WorkloadOptimizer validation",
		"workloadOptimizer", wo.Name,
		"namespace", wo.Namespace)

	// Perform validation
	validationErrors := v.validateWorkloadOptimizer(ctx, wo)
	if len(validationErrors) > 0 {
		errorMsg := strings.Join(validationErrors, "; ")
		log.Info("WorkloadOptimizer validation failed",
			"workloadOptimizer", wo.Name,
			"errors", errorMsg)
		return admission.Denied(errorMsg)
	}

	log.V(1).Info("WorkloadOptimizer validation passed", "workloadOptimizer", wo.Name)
	return admission.Allowed("Validation passed")
}

// validateWorkloadOptimizer performs comprehensive validation of WorkloadOptimizer
func (v *WorkloadOptimizerValidator) validateWorkloadOptimizer(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	// Validate basic fields
	errors = append(errors, v.validateBasicFields(wo)...)

	// Validate workload type
	errors = append(errors, v.validateWorkloadType(wo)...)

	// Validate resources
	errors = append(errors, v.validateResources(wo)...)

	// Validate cost constraints
	errors = append(errors, v.validateCostConstraints(wo)...)

	// Validate power constraints
	errors = append(errors, v.validatePowerConstraints(wo)...)

	// Validate placement policy
	errors = append(errors, v.validatePlacementPolicy(wo)...)

	// Validate auto-scaling
	errors = append(errors, v.validateAutoScaling(wo)...)

	// Validate priority
	errors = append(errors, v.validatePriority(wo)...)

	// Validate namespace constraints
	errors = append(errors, v.validateNamespaceConstraints(ctx, wo)...)

	return errors
}

// validateBasicFields validates basic WorkloadOptimizer fields
func (v *WorkloadOptimizerValidator) validateBasicFields(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	if wo.Name == "" {
		errors = append(errors, "name is required")
	}

	if wo.Namespace == "" {
		errors = append(errors, "namespace is required")
	}

	if wo.Spec.WorkloadType == "" {
		errors = append(errors, "workloadType is required")
	}

	return errors
}

// validateWorkloadType validates the workload type
func (v *WorkloadOptimizerValidator) validateWorkloadType(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	validTypes := map[string]bool{
		"training":  true,
		"serving":   true,
		"inference": true,
		"batch":     true,
	}

	if !validTypes[wo.Spec.WorkloadType] {
		errors = append(errors, fmt.Sprintf("invalid workloadType '%s', must be one of: training, serving, inference, batch", wo.Spec.WorkloadType))
	}

	return errors
}

// validateResources validates resource requirements
func (v *WorkloadOptimizerValidator) validateResources(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	// Validate CPU
	if wo.Spec.Resources.CPU == "" {
		errors = append(errors, "resources.cpu is required")
	} else {
		if _, err := resource.ParseQuantity(wo.Spec.Resources.CPU); err != nil {
			errors = append(errors, fmt.Sprintf("invalid CPU resource format '%s': %v", wo.Spec.Resources.CPU, err))
		} else {
			cpuQuantity := resource.MustParse(wo.Spec.Resources.CPU)
			if cpuQuantity.Cmp(resource.MustParse("0")) <= 0 {
				errors = append(errors, "CPU resource must be greater than 0")
			}
			if cpuQuantity.Cmp(resource.MustParse("100")) > 0 {
				errors = append(errors, "CPU resource cannot exceed 100 cores")
			}
		}
	}

	// Validate Memory
	if wo.Spec.Resources.Memory == "" {
		errors = append(errors, "resources.memory is required")
	} else {
		if _, err := resource.ParseQuantity(wo.Spec.Resources.Memory); err != nil {
			errors = append(errors, fmt.Sprintf("invalid memory resource format '%s': %v", wo.Spec.Resources.Memory, err))
		} else {
			memoryQuantity := resource.MustParse(wo.Spec.Resources.Memory)
			if memoryQuantity.Cmp(resource.MustParse("0")) <= 0 {
				errors = append(errors, "memory resource must be greater than 0")
			}
			if memoryQuantity.Cmp(resource.MustParse("1Ti")) > 0 {
				errors = append(errors, "memory resource cannot exceed 1Ti")
			}
		}
	}

	// Validate GPU
	if wo.Spec.Resources.GPU < 0 {
		errors = append(errors, "GPU resource cannot be negative")
	}
	if wo.Spec.Resources.GPU > 16 {
		errors = append(errors, "GPU resource cannot exceed 16")
	}

	// Validate NPU
	if wo.Spec.Resources.NPU < 0 {
		errors = append(errors, "NPU resource cannot be negative")
	}
	if wo.Spec.Resources.NPU > 16 {
		errors = append(errors, "NPU resource cannot exceed 16")
	}

	// Validate GPU/NPU combination for different workload types
	switch wo.Spec.WorkloadType {
	case "training":
		if wo.Spec.Resources.GPU == 0 && wo.Spec.Resources.NPU == 0 {
			errors = append(errors, "training workloads typically require GPU or NPU acceleration")
		}
	case "inference":
		if wo.Spec.Resources.GPU == 0 && wo.Spec.Resources.NPU == 0 {
			errors = append(errors, "inference workloads typically require GPU or NPU acceleration")
		}
	}

	return errors
}

// validateCostConstraints validates cost constraints
func (v *WorkloadOptimizerValidator) validateCostConstraints(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	if wo.Spec.CostConstraints == nil {
		return errors
	}

	// Validate maxCostPerHour
	if wo.Spec.CostConstraints.MaxCostPerHour <= 0 {
		errors = append(errors, "costConstraints.maxCostPerHour must be greater than 0")
	}
	if wo.Spec.CostConstraints.MaxCostPerHour > 10000 {
		errors = append(errors, "costConstraints.maxCostPerHour cannot exceed $10,000/hour")
	}

	// Validate budgetLimit
	if wo.Spec.CostConstraints.BudgetLimit != nil {
		if *wo.Spec.CostConstraints.BudgetLimit <= 0 {
			errors = append(errors, "costConstraints.budgetLimit must be greater than 0")
		}
		if *wo.Spec.CostConstraints.BudgetLimit > 1000000 {
			errors = append(errors, "costConstraints.budgetLimit cannot exceed $1,000,000")
		}

		// Validate budget vs hourly cost relationship
		if *wo.Spec.CostConstraints.BudgetLimit < wo.Spec.CostConstraints.MaxCostPerHour*24 {
			errors = append(errors, "budgetLimit should be at least 24 times the maxCostPerHour")
		}
	}

	return errors
}

// validatePowerConstraints validates power constraints
func (v *WorkloadOptimizerValidator) validatePowerConstraints(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	if wo.Spec.PowerConstraints == nil {
		return errors
	}

	// Validate maxPowerUsage
	if wo.Spec.PowerConstraints.MaxPowerUsage <= 0 {
		errors = append(errors, "powerConstraints.maxPowerUsage must be greater than 0")
	}
	if wo.Spec.PowerConstraints.MaxPowerUsage > 10000 {
		errors = append(errors, "powerConstraints.maxPowerUsage cannot exceed 10,000W")
	}

	// Validate power vs resources relationship
	estimatedPower := v.estimatePowerFromResources(wo.Spec.Resources)
	if estimatedPower > wo.Spec.PowerConstraints.MaxPowerUsage {
		errors = append(errors, fmt.Sprintf("estimated power consumption (%.0fW) exceeds maxPowerUsage (%.0fW)", estimatedPower, wo.Spec.PowerConstraints.MaxPowerUsage))
	}

	return errors
}

// validatePlacementPolicy validates placement policy
func (v *WorkloadOptimizerValidator) validatePlacementPolicy(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	if wo.Spec.PlacementPolicy == nil {
		return errors
	}

	// Validate node selector
	if wo.Spec.PlacementPolicy.NodeSelector != nil {
		for key, value := range wo.Spec.PlacementPolicy.NodeSelector {
			if key == "" {
				errors = append(errors, "placementPolicy.nodeSelector keys cannot be empty")
			}
			if value == "" {
				errors = append(errors, "placementPolicy.nodeSelector values cannot be empty")
			}
		}
	}

	// Validate affinity rules
	for i, affinity := range wo.Spec.PlacementPolicy.Affinity {
		if affinity.Type == "" {
			errors = append(errors, fmt.Sprintf("placementPolicy.affinity[%d].type is required", i))
		} else {
			validTypes := map[string]bool{
				"gpu_workload":      true,
				"cpu_intensive":     true,
				"memory_intensive":  true,
				"storage_intensive": true,
			}
			if !validTypes[affinity.Type] {
				errors = append(errors, fmt.Sprintf("invalid affinity type '%s' at index %d", affinity.Type, i))
			}
		}

		if affinity.Key == "" {
			errors = append(errors, fmt.Sprintf("placementPolicy.affinity[%d].key is required", i))
		}

		if affinity.Value == "" {
			errors = append(errors, fmt.Sprintf("placementPolicy.affinity[%d].value is required", i))
		}

		if affinity.Weight < 0 || affinity.Weight > 100 {
			errors = append(errors, fmt.Sprintf("placementPolicy.affinity[%d].weight must be between 0 and 100", i))
		}
	}

	return errors
}

// validateAutoScaling validates auto-scaling configuration
func (v *WorkloadOptimizerValidator) validateAutoScaling(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	if wo.Spec.AutoScaling == nil {
		return errors
	}

	// Validate min/max replicas
	if wo.Spec.AutoScaling.MinReplicas <= 0 {
		errors = append(errors, "autoScaling.minReplicas must be greater than 0")
	}

	if wo.Spec.AutoScaling.MaxReplicas <= 0 {
		errors = append(errors, "autoScaling.maxReplicas must be greater than 0")
	}

	if wo.Spec.AutoScaling.MinReplicas > wo.Spec.AutoScaling.MaxReplicas {
		errors = append(errors, "autoScaling.minReplicas cannot be greater than maxReplicas")
	}

	if wo.Spec.AutoScaling.MaxReplicas > 1000 {
		errors = append(errors, "autoScaling.maxReplicas cannot exceed 1000")
	}

	// Validate metrics
	for i, metric := range wo.Spec.AutoScaling.Metrics {
		if metric.Type == "" {
			errors = append(errors, fmt.Sprintf("autoScaling.metrics[%d].type is required", i))
		} else {
			validTypes := map[string]bool{
				"cost":    true,
				"power":   true,
				"latency": true,
				"cpu":     true,
				"memory":  true,
				"gpu":     true,
			}
			if !validTypes[metric.Type] {
				errors = append(errors, fmt.Sprintf("invalid metric type '%s' at index %d", metric.Type, i))
			}
		}

		if metric.Threshold <= 0 {
			errors = append(errors, fmt.Sprintf("autoScaling.metrics[%d].threshold must be greater than 0", i))
		}
	}

	return errors
}

// validatePriority validates priority settings
func (v *WorkloadOptimizerValidator) validatePriority(wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	if wo.Spec.Priority < 0 || wo.Spec.Priority > 100 {
		errors = append(errors, "priority must be between 0 and 100")
	}

	return errors
}

// validateNamespaceConstraints validates namespace-specific constraints
func (v *WorkloadOptimizerValidator) validateNamespaceConstraints(ctx context.Context, wo *kcloudv1alpha1.WorkloadOptimizer) []string {
	var errors []string

	// Check if namespace exists
	var namespace corev1.Namespace
	err := v.Client.Get(ctx, client.ObjectKey{Name: wo.Namespace}, &namespace)
	if err != nil {
		errors = append(errors, fmt.Sprintf("namespace '%s' does not exist", wo.Namespace))
		return errors
	}

	// Check for namespace resource quotas
	var resourceQuotas corev1.ResourceQuotaList
	err = v.Client.List(ctx, &resourceQuotas, client.InNamespace(wo.Namespace))
	if err == nil {
		for _, quota := range resourceQuotas.Items {
			if quota.Status.Used != nil {
				// Check CPU quota
				if cpuUsed, exists := quota.Status.Used[corev1.ResourceCPU]; exists {
					if cpuHard, exists := quota.Spec.Hard[corev1.ResourceCPU]; exists {
						cpuRequested := resource.MustParse(wo.Spec.Resources.CPU)
						totalCPU := cpuUsed.DeepCopy()
						totalCPU.Add(cpuRequested)
						if totalCPU.Cmp(cpuHard) > 0 {
							errors = append(errors, fmt.Sprintf("CPU request would exceed namespace quota (used: %s, requested: %s, limit: %s)", cpuUsed.String(), cpuRequested.String(), cpuHard.String()))
						}
					}
				}

				// Check Memory quota
				if memoryUsed, exists := quota.Status.Used[corev1.ResourceMemory]; exists {
					if memoryHard, exists := quota.Spec.Hard[corev1.ResourceMemory]; exists {
						memoryRequested := resource.MustParse(wo.Spec.Resources.Memory)
						totalMemory := memoryUsed.DeepCopy()
						totalMemory.Add(memoryRequested)
						if totalMemory.Cmp(memoryHard) > 0 {
							errors = append(errors, fmt.Sprintf("Memory request would exceed namespace quota (used: %s, requested: %s, limit: %s)", memoryUsed.String(), memoryRequested.String(), memoryHard.String()))
						}
					}
				}
			}
		}
	}

	// TODO: Check for existing WorkloadOptimizers when CRD types are properly generated
	// For now, skip this validation

	return errors
}

// estimatePowerFromResources estimates power consumption from resource requirements
func (v *WorkloadOptimizerValidator) estimatePowerFromResources(resources kcloudv1alpha1.ResourceRequirements) float64 {
	// Base power consumption estimates
	cpuPowerPerCore := 15.0  // 15W per CPU core
	memoryPowerPerGB := 0.5  // 0.5W per GB memory
	gpuPowerPerUnit := 300.0 // 300W per GPU
	npuPowerPerUnit := 250.0 // 250W per NPU
	baseSystemPower := 50.0  // 50W base system power

	// Parse CPU cores
	cpuQuantity := resource.MustParse(resources.CPU)
	cpuCores := float64(cpuQuantity.MilliValue()) / 1000.0

	// Parse Memory GB
	memoryQuantity := resource.MustParse(resources.Memory)
	memoryGB := float64(memoryQuantity.Value()) / (1024 * 1024 * 1024)

	// Calculate total power
	totalPower := baseSystemPower +
		cpuCores*cpuPowerPerCore +
		memoryGB*memoryPowerPerGB +
		float64(resources.GPU)*gpuPowerPerUnit +
		float64(resources.NPU)*npuPowerPerUnit

	return totalPower
}

// InjectDecoder injects the decoder
func (v *WorkloadOptimizerValidator) InjectDecoder(d admission.Decoder) error {
	v.decoder = d
	return nil
}
