/*
NodeResourcesFit Filter Plugin

This plugin ensures the node has sufficient available resources to host the pod.
*/
package plugin

import (
	"context"
	"fmt"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
)

const NodeResourcesFitName = "NodeResourcesFit"

type NodeResourcesFit struct{}

var _ framework.FilterPlugin = &NodeResourcesFit{}

func NewNodeResourcesFit() *NodeResourcesFit {
	return &NodeResourcesFit{}
}

func (n *NodeResourcesFit) Name() string {
	return NodeResourcesFitName
}

func (n *NodeResourcesFit) Filter(ctx context.Context, pod *v1.Pod, nodeInfo *utils.NodeInfo) *utils.Status {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return utils.NewStatus(utils.Error, "node not found")
	}

	podRequests := computePodResourceRequest(pod)

	allocatable := nodeInfo.Node().Status.Allocatable

	allocated := nodeInfo.Requested

	availableCPU := allocatable.Cpu().MilliValue() - allocated.MilliCPU
	if podRequests.MilliCPU > availableCPU {
		return utils.NewStatus(utils.Unschedulable,
			fmt.Sprintf("Insufficient cpu: requested %dm, available %dm",
				podRequests.MilliCPU, availableCPU))
	}

	availableMemory := allocatable.Memory().Value() - allocated.Memory
	if podRequests.Memory > availableMemory {
		return utils.NewStatus(utils.Unschedulable,
			fmt.Sprintf("Insufficient memory: requested %d, available %d",
				podRequests.Memory, availableMemory))
	}

	for resourceName, podRequest := range podRequests.ScalarResources {
		if podRequest <= 0 {
			continue
		}
		nodeAllocatable, ok := allocatable[v1.ResourceName(resourceName)]
		if !ok || nodeAllocatable.Value() == 0 {
			return utils.NewStatus(utils.Unschedulable,
				fmt.Sprintf("node does not have resource %s", resourceName))
		}
		nodeUsed := allocated.ScalarResources[resourceName]
		available := nodeAllocatable.Value() - nodeUsed
		if podRequest > available {
			return utils.NewStatus(utils.Unschedulable,
				fmt.Sprintf("Insufficient %s: requested %d, available %d",
					resourceName, podRequest, available))
		}
	}

	return utils.NewStatus(utils.Success, "")
}

func computePodResourceRequest(pod *v1.Pod) *utils.Resource {
	result := &utils.Resource{}
	for _, container := range pod.Spec.Containers {
		result.Add(container.Resources.Requests)
	}

	for _, container := range pod.Spec.InitContainers {
		requests := container.Resources.Requests
		if cpu := requests[v1.ResourceCPU]; cpu.MilliValue() > result.MilliCPU {
			result.MilliCPU = cpu.MilliValue()
		}
		if memory := requests[v1.ResourceMemory]; memory.Value() > result.Memory {
			result.Memory = memory.Value()
		}
	}

	if pod.Spec.Overhead != nil {
		result.Add(pod.Spec.Overhead)
	}

	return result
}

func PodRequests(pod *v1.Pod) v1.ResourceList {
	result := v1.ResourceList{}
	for _, container := range pod.Spec.Containers {
		addResourceList(result, container.Resources.Requests)
	}

	for _, container := range pod.Spec.InitContainers {
		for rName, rQuantity := range container.Resources.Requests {
			if existing, ok := result[rName]; !ok || rQuantity.Cmp(existing) > 0 {
				if result == nil {
					result = v1.ResourceList{}
				}
				result[rName] = rQuantity.DeepCopy()
			}
		}
	}

	if pod.Spec.Overhead != nil {
		addResourceList(result, pod.Spec.Overhead)
	}

	return result
}

func addResourceList(list v1.ResourceList, new v1.ResourceList) {
	for name, quantity := range new {
		if value, ok := list[name]; !ok {
			list[name] = quantity.DeepCopy()
		} else {
			value.Add(quantity)
			list[name] = value
		}
	}
}
