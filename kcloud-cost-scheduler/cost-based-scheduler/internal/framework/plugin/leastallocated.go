/*
LeastAllocated Score Plugin

This plugin favors nodes with fewer requested resources, distributing load evenly.
*/
package plugin

import (
	"context"
	"fmt"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const LeastAllocatedName = "LeastAllocated"

type LeastAllocated struct {
	cache *utils.Cache
}

var _ framework.ScorePlugin = &LeastAllocated{}

func NewLeastAllocated(cache *utils.Cache) *LeastAllocated {
	return &LeastAllocated{cache: cache}
}

func (l *LeastAllocated) Name() string {
	return LeastAllocatedName
}

func (l *LeastAllocated) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := l.getNodeInfo(nodeName)
	if nodeInfo == nil || nodeInfo.Node() == nil {
		klog.V(4).InfoS("LeastAllocated: node not found", "node", nodeName)
		return 0, utils.NewStatus(utils.Success, "")
	}

	allocatable := nodeInfo.Node().Status.Allocatable
	requested := nodeInfo.Requested

	cpuAllocatable := allocatable.Cpu().MilliValue()
	cpuRequested := requested.MilliCPU
	
	memAllocatable := allocatable.Memory().Value()
	memRequested := requested.Memory

	// Prevent division by zero
	if cpuAllocatable == 0 || memAllocatable == 0 {
		return 0, utils.NewStatus(utils.Success, "node has zero allocatable CPU or Memory")
	}

	cpuFree := float64(cpuAllocatable-cpuRequested) / float64(cpuAllocatable)
	memFree := float64(memAllocatable-memRequested) / float64(memAllocatable)

	// Combine scores and normalize to 0-100 range
	score := int64(((cpuFree + memFree) / 2.0) * 100)

	klog.V(4).InfoS("LeastAllocated score",
		"node", nodeName,
		"cpuFree", fmt.Sprintf("%.2f", cpuFree),
		"memFree", fmt.Sprintf("%.2f", memFree),
		"score", score)

	return score, utils.NewStatus(utils.Success, "")
}

func (l *LeastAllocated) ScoreExtensions() framework.ScoreExtensions {
	return l
}

func (l *LeastAllocated) NormalizeScore(ctx context.Context, pod *v1.Pod, scores utils.PluginResult) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}

func (l *LeastAllocated) getNodeInfo(nodeName string) *utils.NodeInfo {
	if l.cache == nil {
		return nil
	}
	nodes := l.cache.Nodes()
	if nodes == nil {
		return nil
	}
	return nodes[nodeName]
}
