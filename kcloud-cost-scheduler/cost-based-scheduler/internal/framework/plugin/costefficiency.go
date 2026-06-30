/*
CostEfficiency Score Plugin

This plugin evaluates nodes based on their cost per hour and performance score,
favoring highly cost-efficient instances.
*/
package plugin

import (
	"context"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const CostEfficiencyName = "CostEfficiency"

type CostEfficiency struct {
	cache           *utils.Cache
	metricsProvider utils.AcceleratorMetricsProvider
}

var _ framework.ScorePlugin = &CostEfficiency{}

func NewCostEfficiency(cache *utils.Cache, metricsProvider utils.AcceleratorMetricsProvider) *CostEfficiency {
	return &CostEfficiency{
		cache:           cache,
		metricsProvider: metricsProvider,
	}
}

func (c *CostEfficiency) Name() string {
	return CostEfficiencyName
}

func (c *CostEfficiency) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := c.getNodeInfo(nodeName)
	if nodeInfo == nil || nodeInfo.Node() == nil {
		klog.V(4).InfoS("CostEfficiency: node not found", "node", nodeName)
		return 0, utils.NewStatus(utils.Success, "")
	}

	node := nodeInfo.Node()

	metrics := c.metricsProvider.GetMetrics(node)

	if !metrics.Found {
		klog.V(4).InfoS("CostEfficiency: no accelerator metrics found",
			"node", nodeName)
		return 0, utils.NewStatus(utils.Success, "")
	}

	costPerHour := metrics.CostPerHour
	perfScore := metrics.PerfScore

	if perfScore == 0 {
		klog.V(4).InfoS("CostEfficiency: perfScore is 0, returning 0",
			"node", nodeName)
		return 0, utils.NewStatus(utils.Success, "")
	}

	if costPerHour <= 0 {
		klog.V(4).InfoS("CostEfficiency: costPerHour is invalid, returning neutral score",
			"node", nodeName, "costPerHour", costPerHour)
		return 5, utils.NewStatus(utils.Success, "costPerHour missing or invalid")
	}

	costPerInference := costPerHour / perfScore

	// TODO: maxCostPerInference 값을 하드코딩 대신, 가속기 유형 또는 설정으로부터 동적으로 로드하도록 개선 필요
	const maxCostPerInference = 0.01
	normalizedCost := costPerInference / maxCostPerInference
	if normalizedCost > 1.0 {
		normalizedCost = 1.0
	}

	score := int64(50.0 * (1.0 - normalizedCost))

	klog.V(4).InfoS("CostEfficiency score calculated",
		"node", nodeName,
		"costPerHour", costPerHour,
		"perfScore", perfScore,
		"costPerInference", costPerInference,
		"score", score)

	return score, utils.NewStatus(utils.Success, "")
}

func (c *CostEfficiency) ScoreExtensions() framework.ScoreExtensions {
	return c
}

func (c *CostEfficiency) NormalizeScore(ctx context.Context, pod *v1.Pod, scores utils.PluginResult) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}

func (c *CostEfficiency) getNodeInfo(nodeName string) *utils.NodeInfo {
	if c.cache == nil {
		return nil
	}
	nodes := c.cache.Nodes()
	if nodes == nil {
		return nil
	}
	return nodes[nodeName]
}
