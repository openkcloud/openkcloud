/*
TaintToleration Filter Plugin

This plugin filters nodes that have taints the pod does not tolerate.
*/
package plugin

import (
	"context"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
)

const TaintTolerationName = "TaintToleration"

type TaintToleration struct{}

var _ framework.FilterPlugin = &TaintToleration{}

func NewTaintToleration() *TaintToleration {
	return &TaintToleration{}
}

func (t *TaintToleration) Name() string {
	return TaintTolerationName
}

func (t *TaintToleration) Filter(ctx context.Context, pod *v1.Pod, nodeInfo *utils.NodeInfo) *utils.Status {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return utils.NewStatus(utils.Error, "node not found")
	}
	node := nodeInfo.Node()

	if parts, ok := utils.CachedPolicyParts(ctx, node.Name); ok {
		if r, found := utils.FindPolicyPart(parts, "taint"); found {
			if !r.OK {
				return utils.NewStatus(utils.Unschedulable, r.Detail)
			}
			return utils.NewStatus(utils.Success, "")
		}
	}

	r := acceleratorfit.EvaluateTaints(pod, node)
	if !r.OK {
		return utils.NewStatus(utils.Unschedulable, r.Detail)
	}
	return utils.NewStatus(utils.Success, "")
}
