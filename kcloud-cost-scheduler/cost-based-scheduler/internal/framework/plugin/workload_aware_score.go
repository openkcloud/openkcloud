/*
WorkloadAware Score Plugin

This plugin leverages an external AI prediction engine to dynamically score nodes
based on expected workload characteristics.
*/
package plugin


import (
	"strconv"

	"context"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

func nodeDeviceType(node *v1.Node) string {
	if node == nil {
		return ""
	}
	if q, ok := node.Status.Allocatable["furiosa.ai/warboy"]; ok && q.Value() > 0 {
		return "npu"
	}
	if q, ok := node.Status.Allocatable["nvidia.com/gpu"]; ok && q.Value() > 0 {
		return "gpu"
	}
	if t, ok := node.Labels["accelerator.type"]; ok {
		return t
	}
	return ""
}

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

	var costUSD float64
	switch devType {
	case "gpu":
		costUSD = podAnnotationFloat(pod, "sched.ai/predicted-cost-gpu")
	case "npu":
		costUSD = podAnnotationFloat(pod, "sched.ai/predicted-cost-npu")
	default:
		return 0, utils.NewStatus(utils.Success, "")
	}

	if costUSD <= 0 {
		klog.V(4).InfoS("WorkloadAwareCost: predicted-cost-gpu/npu is missing or invalid, returning neutral score",
			"pod", pod.Name, "node", nodeName, "device", devType, "costUSD", costUSD)
		return 5, utils.NewStatus(utils.Success, "predicted cost missing or invalid")
	}

	const maxCostUSD = 1.0
	normalized := costUSD / maxCostUSD
	if normalized > 1.0 {
		normalized = 1.0
	}
	score := int64(25.0 * (1.0 - normalized))
	klog.V(4).InfoS("WorkloadAwareCost scored",
		"pod", pod.Name, "node", nodeName, "device", devType,
		"costUSD", costUSD, "score", score)
	return score, utils.NewStatus(utils.Success, "")
}

func (p *WorkloadAwareCost) ScoreExtensions() framework.ScoreExtensions { return p }
func (p *WorkloadAwareCost) NormalizeScore(_ context.Context, _ *v1.Pod, _ utils.PluginResult) *utils.Status {
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

	var powerW, tdp float64
	switch devType {
	case "gpu":
		powerW = podAnnotationFloat(pod, "sched.ai/predicted-power-gpu")
		tdp = 300.0
	case "npu":
		powerW = podAnnotationFloat(pod, "sched.ai/predicted-power-npu")
		tdp = 50.0
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
		"pod", pod.Name, "node", nodeName, "device", devType,
		"powerW", powerW, "tdp", tdp, "score", score)
	return score, utils.NewStatus(utils.Success, "")
}

func (p *WorkloadAwarePower) ScoreExtensions() framework.ScoreExtensions { return p }
func (p *WorkloadAwarePower) NormalizeScore(_ context.Context, _ *v1.Pod, _ utils.PluginResult) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}
func (p *WorkloadAwarePower) nodeInfo(name string) *utils.NodeInfo {
	if p.cache == nil {
		return nil
	}
	return p.cache.Nodes()[name]
}
