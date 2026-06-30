/*
TaintToleration Filter Plugin

This plugin filters nodes that have taints the pod does not tolerate.
*/
package plugin

import (
	"context"
	"fmt"

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

	for _, taint := range node.Spec.Taints {
		if taint.Effect == v1.TaintEffectPreferNoSchedule {
			continue
		}

		if !tolerationsTolerateTaint(pod.Spec.Tolerations, taint) {
			return utils.NewStatus(utils.Unschedulable,
				fmt.Sprintf("node %s has taint %s=%s:%s that pod does not tolerate",
					node.Name, taint.Key, taint.Value, taint.Effect))
		}
	}

	return utils.NewStatus(utils.Success, "")
}

func tolerationsTolerateTaint(tolerations []v1.Toleration, taint v1.Taint) bool {
	for _, toleration := range tolerations {
		if tolerationTolerateTaint(toleration, taint) {
			return true
		}
	}
	return false
}

func tolerationTolerateTaint(toleration v1.Toleration, taint v1.Taint) bool {
	if toleration.Effect != "" && toleration.Effect != taint.Effect {
		return false
	}

	if toleration.Operator == v1.TolerationOpExists {
		return toleration.Key == "" || toleration.Key == taint.Key
	}

	return toleration.Key == taint.Key && toleration.Value == taint.Value
}
