/*
BinPacking Score Plugin

This plugin favors nodes that already have high resource utilization
to increase cluster density and avoid fragmentation.
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

const BinPackingName = "BinPacking"

type BinPacking struct {
	cache *utils.Cache
}

var _ framework.ScorePlugin = &BinPacking{}

func NewBinPacking(cache *utils.Cache) *BinPacking {
	return &BinPacking{
		cache: cache,
	}
}

func (b *BinPacking) Name() string {
	return BinPackingName
}

func (b *BinPacking) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := b.getNodeInfo(nodeName)
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return 0, utils.NewStatus(utils.Success, "")
	}

	totalCPU := nodeInfo.Allocatable.MilliCPU
	totalMem := nodeInfo.Allocatable.Memory

	usedCPU := nodeInfo.Requested.MilliCPU
	usedMem := nodeInfo.Requested.Memory

	if totalCPU == 0 || totalMem == 0 {
		return 0, utils.NewStatus(utils.Success, "")
	}

	podCPU := getPodCPURequest(pod)
	podMem := getPodMemRequest(pod)

	newCPUUtil := float64(usedCPU+podCPU) / float64(totalCPU)
	newMemUtil := float64(usedMem+podMem) / float64(totalMem)

	totalAccel := getNodeAccelCapacity(nodeInfo)
	usedAccel := getNodeAccelUsed(nodeInfo)
	podAccel := getPodAccelRequest(pod)

	var gpuUtil float64 = 0.0

	if totalAccel > 0 {
		gpuUtil = float64(usedAccel+podAccel) / float64(totalAccel)
	}

	score := (newCPUUtil + newMemUtil + gpuUtil) / 3.0

	if newCPUUtil > 0.9 || newMemUtil > 0.9 || gpuUtil > 0.9 {
		score *= 0.5
	}

	finalScore := int64(score * 100)

	klog.V(4).InfoS("BinPacking score",
		"node", nodeName,
		"cpuUtil", fmt.Sprintf("%.2f", newCPUUtil),
		"memUtil", fmt.Sprintf("%.2f", newMemUtil),
		"gpuUtil", fmt.Sprintf("%.2f", gpuUtil),
		"score", finalScore,
	)

	return finalScore, utils.NewStatus(utils.Success, "")
}

func (b *BinPacking) ScoreExtensions() framework.ScoreExtensions {
	return b
}

func (b *BinPacking) NormalizeScore(ctx context.Context, pod *v1.Pod, scores utils.PluginResult) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}


func (b *BinPacking) getNodeInfo(nodeName string) *utils.NodeInfo {
	if b.cache == nil {
		return nil
	}
	nodes := b.cache.Nodes()
	if nodes == nil {
		return nil
	}
	return nodes[nodeName]
}

func getPodCPURequest(pod *v1.Pod) int64 {
	var total int64
	for _, c := range pod.Spec.Containers {
		if req, ok := c.Resources.Requests[v1.ResourceCPU]; ok {
			total += req.MilliValue()
		}
	}
	return total
}

func getPodMemRequest(pod *v1.Pod) int64 {
	var total int64
	for _, c := range pod.Spec.Containers {
		if req, ok := c.Resources.Requests[v1.ResourceMemory]; ok {
			total += req.Value()
		}
	}
	return total
}
func getPodAccelRequest(pod *v1.Pod) int64 {
	var total int64
	for _, c := range pod.Spec.Containers {
		for _, res := range []v1.ResourceName{"nvidia.com/gpu", "furiosa.ai/warboy"} {
			if req, ok := c.Resources.Requests[res]; ok {
				total += req.Value()
			}
		}
	}
	return total
}

func getNodeAccelCapacity(nodeInfo *utils.NodeInfo) int64 {
	var total int64
	for _, res := range []v1.ResourceName{"nvidia.com/gpu", "furiosa.ai/warboy"} {
		if val, ok := nodeInfo.Allocatable.ScalarResources[res]; ok {
			total += val
		}
	}
	return total
}

func getNodeAccelUsed(nodeInfo *utils.NodeInfo) int64 {
	var total int64
	for _, res := range []v1.ResourceName{"nvidia.com/gpu", "furiosa.ai/warboy"} {
		if val, ok := nodeInfo.Requested.ScalarResources[res]; ok {
			total += val
		}
	}
	return total
}
