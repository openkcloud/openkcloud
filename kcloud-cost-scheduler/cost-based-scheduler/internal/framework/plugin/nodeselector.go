/*
NodeSelector Filter Plugin

This plugin filters nodes based on the Pod's node selector and node affinity terms.
*/
package plugin

import (
	"context"
	"fmt"

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

	if pod.Spec.NodeName != "" && pod.Spec.NodeName != node.Name {
		return utils.NewStatus(utils.Unschedulable,
			fmt.Sprintf("pod requires node %s but got %s", pod.Spec.NodeName, node.Name))
	}

	for key, val := range pod.Spec.NodeSelector {
		nodeVal, ok := node.Labels[key]
		if !ok || nodeVal != val {
			return utils.NewStatus(utils.Unschedulable,
				fmt.Sprintf("node %s does not match nodeSelector %s=%s", node.Name, key, val))
		}
	}

	return utils.NewStatus(utils.Success, "")
}
