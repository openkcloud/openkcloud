/*
NodeResourcesFit Filter Plugin

This plugin ensures the node has sufficient available resources to host the pod.
*/
package plugin

import (
	"context"
	"fmt"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"
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

	node := nodeInfo.Node()
	allocatable := node.Status.Allocatable
	allocated := nodeInfo.Requested
	if allocated == nil {
		allocated = &utils.Resource{}
	}

	host := acceleratorfit.EvaluateHostResources(pod, allocatable, allocated.MilliCPU, allocated.Memory)
	if !host.OK {
		return utils.NewStatus(utils.Unschedulable, host.Detail)
	}

	// Reuse only conclusive count results. resource_view_missing is UNEVALUATED
	// for required-checks, but legacy NodeResourcesFit must still reject missing GPUs.
	if counts, ok := utils.CachedAcceleratorCounts(ctx, node.Name); ok {
		reuseOK := true
		for _, c := range counts {
			if c.Code == "resource_view_missing" {
				reuseOK = false
				break
			}
			if !c.OK {
				detail := c.Detail
				if detail == "" {
					detail = fmt.Sprintf("Insufficient %s: requested %d, available %d", c.Resource, c.Requested, c.Available)
				}
				return utils.NewStatus(utils.Unschedulable, detail)
			}
		}
		if reuseOK {
			return checkNonAcceleratorScalars(pod, allocatable, allocated)
		}
		// fall through to live accelerator + scalar path
	}

	podRequests := computePodResourceRequest(pod)
	for resourceName, podRequest := range podRequests.ScalarResources {
		if podRequest <= 0 {
			continue
		}
		nodeAllocatable, ok := allocatable[resourceName]
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

func checkNonAcceleratorScalars(pod *v1.Pod, allocatable v1.ResourceList, allocated *utils.Resource) *utils.Status {
	podRequests := computePodResourceRequest(pod)
	for resourceName, podRequest := range podRequests.ScalarResources {
		if podRequest <= 0 {
			continue
		}
		if acceleratorfit.AcceleratorResourceType(resourceName) != "" {
			continue
		}
		nodeAllocatable, ok := allocatable[resourceName]
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
	// Shared with placementchecks check 1 — includes init-container accelerator scalars.
	fit := acceleratorfit.ComputePodResourceRequest(pod)
	result := &utils.Resource{
		MilliCPU:        fit.MilliCPU,
		Memory:          fit.Memory,
		ScalarResources: map[v1.ResourceName]int64{},
	}
	for k, v := range fit.ScalarResources {
		result.ScalarResources[v1.ResourceName(k)] = v
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
