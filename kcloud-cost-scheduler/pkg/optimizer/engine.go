package optimizer

import (
	"context"
	"math"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
)

type Engine struct {
	CostCalculator  *CostCalculator
	PowerCalculator *PowerCalculator
}

type WorkloadState struct {
	WorkloadOptimizer *kcloudv1alpha1.WorkloadOptimizer
	Pods              []corev1.Pod
	AvailableNodes    []corev1.Node
}

type OptimizationResult struct {
	EstimatedCost        float64
	EstimatedPower       float64
	Score                float64
	RequiresRescheduling bool
	AssignedNode         string
	RecommendedReplicas  int32
}

func NewEngine() *Engine {
	return &Engine{
		CostCalculator:  NewCostCalculator(),
		PowerCalculator: NewPowerCalculator(),
	}
}

func (e *Engine) Optimize(ctx context.Context, state *WorkloadState) *OptimizationResult {
	log := log.FromContext(ctx)
	wo := state.WorkloadOptimizer
	result := &OptimizationResult{RequiresRescheduling: false, RecommendedReplicas: 1}
	cpuCores := e.parseCPU(wo.Spec.Resources.CPU)
	memoryGB := e.parseMemory(wo.Spec.Resources.Memory)
	baseCost := e.CostCalculator.CalculateCost(cpuCores, memoryGB, wo.Spec.Resources.GPU, wo.Spec.Resources.NPU)
	if wo.Spec.CostConstraints != nil && wo.Spec.CostConstraints.PreferSpot {
		baseCost *= 0.7
	}
	result.EstimatedCost = baseCost
	basePower := e.PowerCalculator.CalculatePower(cpuCores, memoryGB, wo.Spec.Resources.GPU, wo.Spec.Resources.NPU)
	if wo.Spec.PowerConstraints != nil && wo.Spec.PowerConstraints.PreferGreen {
		basePower *= 0.9
	}
	result.EstimatedPower = basePower
	result.Score = e.calculateScore(wo, result)
	result.RequiresRescheduling = result.Score < 0.5
	if wo.Spec.AutoScaling != nil {
		result.RecommendedReplicas = wo.Spec.AutoScaling.MinReplicas
	}
	log.Info("Optimization completed", "cost", result.EstimatedCost)
	return result
}

func (e *Engine) calculateScore(wo *kcloudv1alpha1.WorkloadOptimizer, result *OptimizationResult) float64 {
	score := 1.0
	if wo.Spec.CostConstraints != nil {
		ratio := result.EstimatedCost / wo.Spec.CostConstraints.MaxCostPerHour
		if ratio > 1.0 {
			score -= (ratio - 1.0) * 0.5
		}
	}
	if score < 0 {
		score = 0
	}
	return math.Round(score*100) / 100
}

func (e *Engine) parseCPU(cpu string) float64 {
	if strings.HasSuffix(cpu, "m") {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(cpu, "m"), 64)
		return v / 1000.0
	}
	v, _ := strconv.ParseFloat(cpu, 64)
	return v
}

func (e *Engine) parseMemory(memory string) float64 {
	value, multiplier := memory, 1.0
	if strings.HasSuffix(memory, "Gi") {
		value = strings.TrimSuffix(memory, "Gi")
	} else if strings.HasSuffix(memory, "Mi") {
		value = strings.TrimSuffix(memory, "Mi")
		multiplier = 1.0 / 1024.0
	}
	v, _ := strconv.ParseFloat(value, 64)
	return v * multiplier
}
