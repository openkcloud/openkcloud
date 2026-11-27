/*
NodeSelector Filter Plugin

This plugin filters nodes based on the Pod's nodeName and nodeSelector.
Affinity is not evaluated here (comment historically mentioned it).
*/
package plugin

import (
	"context"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
)

const NodeSelectorName = "NodeSelector"

type NodeSelector struct{}

var _ framework.FilterPlugin = &NodeSelector{}

func NewNodeSelector() *NodeSelector {
	return &NodeSelector{}
}

func (n *NodeSelector) Name() string {
	return NodeSelectorName
}

func (n *NodeSelector) Filter(ctx context.Context, pod *v1.Pod, nodeInfo *utils.NodeInfo) *utils.Status {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return utils.NewStatus(utils.Error, "node not found")
	}
	node := nodeInfo.Node()

	if parts, ok := utils.CachedPolicyParts(ctx, node.Name); ok {
		if r, found := utils.FindPolicyPart(parts, "node_name"); found && !r.OK {
			return utils.NewStatus(utils.Unschedulable, r.Detail)
		}
		if r, found := utils.FindPolicyPart(parts, "node_selector"); found && !r.OK {
			return utils.NewStatus(utils.Unschedulable, r.Detail)
		}
		if _, foundName := utils.FindPolicyPart(parts, "node_name"); foundName {
			if _, foundSel := utils.FindPolicyPart(parts, "node_selector"); foundSel {
				return utils.NewStatus(utils.Success, "")
			}
		}
	}

	if r := acceleratorfit.EvaluateNodeName(pod, node); !r.OK {
		return utils.NewStatus(utils.Unschedulable, r.Detail)
	}
	if r := acceleratorfit.EvaluateNodeSelector(pod, node); !r.OK {
		return utils.NewStatus(utils.Unschedulable, r.Detail)
	}
	return utils.NewStatus(utils.Success, "")
}
