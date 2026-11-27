/*
WorkloadAware Score Plugin

This plugin leverages an external AI prediction engine to dynamically score nodes
based on expected workload characteristics.
*/
package plugin

import (
	"strconv"
	"strings"

	"context"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

func podAnnotationFloat(pod *v1.Pod, key string) float64 {
	if pod.Annotations == nil {
		return 0
	}
	v, ok := pod.Annotations[key]
	if !ok || v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}

func podAnnotationFloatByModel(pod *v1.Pod, key, model string) float64 {
	if pod == nil || pod.Annotations == nil || model == "" {
		return 0
	}
	model = canonicalAcceleratorModel(model)
	for _, entry := range strings.Split(pod.Annotations[key], ",") {
		name, rawValue, found := strings.Cut(entry, "=")
		if !found || canonicalAcceleratorModel(name) != model {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(rawValue), 64)
		if err == nil {
			return value
		}
	}
	return 0
}

func nodeScoringModel(node *v1.Node, acceleratorType string) string {
	models := nodeAcceleratorModels(node, acceleratorType)
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

const WorkloadAwareCostName = "WorkloadAwareCost"

type WorkloadAwareCost struct {
	cache *utils.Cache
}

var _ framework.ScorePlugin = &WorkloadAwareCost{}

func NewWorkloadAwareCost(cache *utils.Cache) *WorkloadAwareCost {
	return &WorkloadAwareCost{cache: cache}
}

func (p *WorkloadAwareCost) Name() string { return WorkloadAwareCostName }

func (p *WorkloadAwareCost) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := p.nodeInfo(nodeName)
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return 0, utils.NewStatus(utils.Success, "")
	}
	devType := nodeDeviceType(nodeInfo.Node())
	model := nodeScoringModel(nodeInfo.Node(), devType)

	var costUSD float64
	switch devType {
	case "gpu":
		costUSD = podAnnotationFloatByModel(pod, "sched.ai/predicted-cost-by-model", model)
		if costUSD <= 0 {
			costUSD = podAnnotationFloat(pod, "sched.ai/predicted-cost-gpu")
		}
	case "npu":
		costUSD = podAnnotationFloatByModel(pod, "sched.ai/predicted-cost-by-model", model)
		if costUSD <= 0 {
			costUSD = podAnnotationFloat(pod, "sched.ai/predicted-cost-npu")
		}
	case "mixed":
		// TODO(mixed-scheduling): GPU/NPU 단계별 비용을 합산하는 모델로 교체.
		return 5, utils.NewStatus(utils.Success, "mixed GPU/NPU cost scoring is not tuned")
	default:
		return 0, utils.NewStatus(utils.Success, "")
	}

	if costUSD <= 0 {
		klog.V(4).InfoS("WorkloadAwareCost: predicted-cost-gpu/npu is missing or invalid, returning neutral score",
			"pod", pod.Name, "node", nodeName, "device", devType, "costUSD", costUSD)
		return 5, utils.NewStatus(utils.Success, "predicted cost missing or invalid")
	}

	normalized := costUSD / maxCostUSD
	if normalized > 1.0 {
		normalized = 1.0
	}
	score := int64(25.0 * (1.0 - normalized))
	klog.V(4).InfoS("WorkloadAwareCost scored",
		"pod", pod.Name, "node", nodeName, "device", devType, "model", model,
		"costUSD", costUSD, "maxCostUSD", maxCostUSD, "score", score)
	return score, utils.NewStatus(utils.Success, "")
}

func (p *WorkloadAwareCost) ScoreExtensions() framework.ScoreExtensions { return p }
func (p *WorkloadAwareCost) NormalizeScore(_ context.Context, _ *v1.Pod, _ utils.PluginResultMap) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}
func (p *WorkloadAwareCost) nodeInfo(name string) *utils.NodeInfo {
	if p.cache == nil {
		return nil
	}
	return p.cache.Nodes()[name]
}

const WorkloadAwarePowerName = "WorkloadAwarePower"

type WorkloadAwarePower struct {
	cache *utils.Cache
}

var _ framework.ScorePlugin = &WorkloadAwarePower{}

func NewWorkloadAwarePower(cache *utils.Cache) *WorkloadAwarePower {
	return &WorkloadAwarePower{cache: cache}
}

func (p *WorkloadAwarePower) Name() string { return WorkloadAwarePowerName }

func (p *WorkloadAwarePower) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := p.nodeInfo(nodeName)
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return 0, utils.NewStatus(utils.Success, "")
	}
	devType := nodeDeviceType(nodeInfo.Node())
	model := nodeScoringModel(nodeInfo.Node(), devType)

	var powerW, tdp float64
	switch devType {
	case "gpu":
		powerW = podAnnotationFloatByModel(pod, "sched.ai/predicted-power-by-model", model)
		if powerW <= 0 {
			powerW = podAnnotationFloat(pod, "sched.ai/predicted-power-gpu")
		}
		tdp = gpuTDPWatts
	case "npu":
		powerW = podAnnotationFloatByModel(pod, "sched.ai/predicted-power-by-model", model)
		if powerW <= 0 {
			powerW = podAnnotationFloat(pod, "sched.ai/predicted-power-npu")
		}
		tdp = npuTDPWatts
	case "mixed":
		// TODO(mixed-scheduling): 동시 사용률과 구간별 energy를 반영한 뒤
		// GPU/NPU 전력 예측을 결합한다. 현재는 안전한 중립 점수를 반환한다.
		return 5, utils.NewStatus(utils.Success, "mixed GPU/NPU power scoring is not tuned")
	default:
		return 0, utils.NewStatus(utils.Success, "")
	}

	if powerW <= 0 {
		klog.V(4).InfoS("WorkloadAwarePower: predicted-power-gpu/npu is missing or invalid, returning neutral score",
			"pod", pod.Name, "node", nodeName, "device", devType, "powerW", powerW)
		return 5, utils.NewStatus(utils.Success, "predicted power missing or invalid")
	}

	ratio := powerW / tdp
	if ratio > 1.0 {
		ratio = 1.0
	}
	score := int64(15.0 * (1.0 - ratio*0.5))
	klog.V(4).InfoS("WorkloadAwarePower scored",
		"pod", pod.Name, "node", nodeName, "device", devType, "model", model,
		"powerW", powerW, "tdp", tdp, "score", score)
	return score, utils.NewStatus(utils.Success, "")
}

func (p *WorkloadAwarePower) ScoreExtensions() framework.ScoreExtensions { return p }
func (p *WorkloadAwarePower) NormalizeScore(_ context.Context, _ *v1.Pod, _ utils.PluginResultMap) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}
func (p *WorkloadAwarePower) nodeInfo(name string) *utils.NodeInfo {
	if p.cache == nil {
		return nil
	}
	return p.cache.Nodes()[name]
}
