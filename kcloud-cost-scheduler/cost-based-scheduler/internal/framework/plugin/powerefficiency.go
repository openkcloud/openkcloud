/*
PowerEfficiency Score Plugin

This plugin favors nodes with higher performance-per-watt ratios, promoting green computing.
*/
package plugin

import (
	"context"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const PowerEfficiencyName = "PowerEfficiency"

type PowerEfficiency struct {
	cache           *utils.Cache
	metricsProvider utils.AcceleratorMetricsProvider
}

var _ framework.ScorePlugin = &PowerEfficiency{}

func NewPowerEfficiency(cache *utils.Cache, metricsProvider utils.AcceleratorMetricsProvider) *PowerEfficiency {
	return &PowerEfficiency{
		cache:           cache,
		metricsProvider: metricsProvider,
	}
}

func (p *PowerEfficiency) Name() string {
	return PowerEfficiencyName
}

func (p *PowerEfficiency) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := p.getNodeInfo(nodeName)
	if nodeInfo == nil || nodeInfo.Node() == nil {
		klog.V(4).InfoS("PowerEfficiency: node not found", "node", nodeName)
		return 0, utils.NewStatus(utils.Success, "")
	}

	node := nodeInfo.Node()

	metrics := p.metricsProvider.GetMetrics(node)

	if !metrics.Found {
		klog.V(4).InfoS("PowerEfficiency: no accelerator metrics found",
			"node", nodeName)
		return 0, utils.NewStatus(utils.Success, "")
	}

	perfPerWatt := metrics.PerfPerWatt
	if perfPerWatt == 0 && metrics.PowerWatts > 0 {
		perfPerWatt = metrics.PerfScore / metrics.PowerWatts
	}

	if perfPerWatt == 0 {
		klog.V(4).InfoS("PowerEfficiency: perfPerWatt is 0, returning 0",
			"node", nodeName)
		return 0, utils.NewStatus(utils.Success, "")
	}

	normalizedPerfPerWatt := perfPerWatt / 20.0
	if normalizedPerfPerWatt > 1.0 {
		normalizedPerfPerWatt = 1.0
	}

	score := int64(30.0 * normalizedPerfPerWatt)

	klog.V(4).InfoS("PowerEfficiency score calculated",
		"node", nodeName,
		"perfScore", metrics.PerfScore,
		"powerWatts", metrics.PowerWatts,
		"perfPerWatt", perfPerWatt,
		"score", score)

	return score, utils.NewStatus(utils.Success, "")
}

func (p *PowerEfficiency) ScoreExtensions() framework.ScoreExtensions {
	return p
}

func (p *PowerEfficiency) NormalizeScore(ctx context.Context, pod *v1.Pod, scores utils.PluginResult) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}

func (p *PowerEfficiency) getNodeInfo(nodeName string) *utils.NodeInfo {
	if p.cache == nil {
		return nil
	}
	nodes := p.cache.Nodes()
	if nodes == nil {
		return nil
	}
	return nodes[nodeName]
}
