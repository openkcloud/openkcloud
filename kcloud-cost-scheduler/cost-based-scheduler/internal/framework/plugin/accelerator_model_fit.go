package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
)

const AcceleratorModelFitName = "AcceleratorModelFit"

type AcceleratorModelFit struct{}

var _ framework.FilterPlugin = &AcceleratorModelFit{}

func NewAcceleratorModelFit() *AcceleratorModelFit {
	return &AcceleratorModelFit{}
}

func (p *AcceleratorModelFit) Name() string {
	return AcceleratorModelFitName
}

func (p *AcceleratorModelFit) Filter(ctx context.Context, pod *v1.Pod, nodeInfo *utils.NodeInfo) *utils.Status {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return utils.NewStatus(utils.Error, "node not found")
	}
	node := nodeInfo.Node()

	models, ok := utils.CachedModelFit(ctx, node.Name)
	if !ok {
		models = acceleratorfit.EvaluateModelFit(pod, node)
	}
	for _, r := range models {
		if !r.OK {
			return utils.NewStatus(utils.Unschedulable, fmt.Sprintf(
				"node %s %s models [%s] do not match requested models [%s]",
				node.Name,
				strings.ToUpper(r.Kind),
				strings.Join(r.NodeModels, ","),
				strings.Join(r.RequestedModels, ","),
			))
		}
	}
	return utils.NewStatus(utils.Success, "")
}
