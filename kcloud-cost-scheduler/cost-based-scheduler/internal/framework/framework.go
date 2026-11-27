/*
Execution engine for the scheduling framework plugins

This module provides the framework implementation that manages the registration
and execution flow of various scheduling plugins during a scheduling cycle.
*/
package framework

import (
	"context"
	"fmt"
	"time"

	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
)

type frameworkImpl struct {
	preFilterPlugins []PreFilterPlugin
	filterPlugins    []FilterPlugin
	scorePlugins     []ScorePlugin
	permitPlugins    []PermitPlugin
	bindPlugin       BindPlugin
}

func NewFramework(
	preFilterPlugins []PreFilterPlugin,
	filterPlugins []FilterPlugin,
	scorePlugins []ScorePlugin,
	permitPlugins []PermitPlugin,
	bindPlugin BindPlugin,
) Framework {
	return &frameworkImpl{
		preFilterPlugins: preFilterPlugins,
		filterPlugins:    filterPlugins,
		scorePlugins:     scorePlugins,
		permitPlugins:    permitPlugins,
		bindPlugin:       bindPlugin,
	}
}

func (f *frameworkImpl) RunPreFilterPlugins(ctx context.Context, pod *v1.Pod) *utils.Status {
	for _, plugin := range f.preFilterPlugins {
		if status := plugin.PreFilter(ctx, pod); !status.IsSuccess() {
			logger.Info(fmt.Sprintf("[PreFilter] pod=%s/%s plugin=%s denied: %s",
				pod.Namespace, pod.Name, plugin.Name(), status.Message()))
			return status
		}
	}
	return utils.NewStatus(utils.Success, "")
}

func (f *frameworkImpl) RunPermitPlugins(ctx context.Context, pod *v1.Pod, nodeName string) *utils.Status {
	for _, plugin := range f.permitPlugins {
		decision, timeout := plugin.Permit(ctx, pod, nodeName)
		switch decision {
		case PermitDeny:
			logger.Info(fmt.Sprintf("[Permit] pod=%s/%s plugin=%s denied",
				pod.Namespace, pod.Name, plugin.Name()))
			return utils.NewStatus(utils.Unschedulable, fmt.Sprintf("Denied by Permit plugin %s", plugin.Name()))
		case PermitWait:
			logger.Info(fmt.Sprintf("[Permit] pod=%s/%s plugin=%s waiting timeout=%s",
				pod.Namespace, pod.Name, plugin.Name(), timeout))

			waitStatus := plugin.(interface {
				WaitForGang(ctx context.Context, pod *v1.Pod, timeout time.Duration) *utils.Status
			}).WaitForGang(ctx, pod, timeout)
			if !waitStatus.IsSuccess() {
				return waitStatus
			}
		}
	}
	return utils.NewStatus(utils.Success, "")
}

func (f *frameworkImpl) RunFilterPlugins(ctx context.Context, pod *v1.Pod, nodeInfo *utils.NodeInfo) utils.PluginResultMap {
	result := utils.PluginResultMap{}

	if nodeInfo == nil || nodeInfo.Node() == nil {
		return result
	}

	nodeName := nodeInfo.Node().Name
	node := nodeInfo.Node()

	pluginResult := utils.PluginResult{
		IsFiltered: false,
		Scores:     []utils.PluginScore{},
	}

	// P6: run required checks once per node before legacy Filters; keep full report
	// on filtered candidates (no silent drop of 7-check evidence).
	if st := utils.AttemptFrom(ctx); st != nil && st.Engine != nil {
		resView := utils.NodeResourceViewFromNodeInfo(nodeInfo)
		rep := st.EnsureEvaluated(utils.EvalInputFromAttempt(st, pod, node, nodeName, resView))
		utils.ApplyRequiredCheckExclusion(&pluginResult, rep)
	}

	if !pluginResult.IsFiltered {
		for _, plugin := range f.filterPlugins {
			status := plugin.Filter(ctx, pod, nodeInfo)
			if !status.IsSuccess() {
				logger.Info(fmt.Sprintf("[Filter] pod=%s/%s node=%s plugin=%s reason=%s",
					pod.Namespace, pod.Name, nodeName, plugin.Name(), status.Message()))

				status.SetPlugin(plugin.Name())
				pluginResult.IsFiltered = true
				pluginResult.FilteredStatus = *status
				pluginResult.RejectionReasons = append(pluginResult.RejectionReasons,
					utils.LegacyFilterRejection(plugin.Name(), status))
				break
			}
		}
	}

	result[nodeName] = pluginResult
	return result
}

// RunPreScorePlugins runs Score plugins that also implement PreScorePlugin.
// Catalog lookups happen here so Score can stay network-free for prepared keys.
func (f *frameworkImpl) RunPreScorePlugins(ctx context.Context, pod *v1.Pod, nodes []*v1.Node) *utils.Status {
	for _, plugin := range f.scorePlugins {
		ps, ok := plugin.(PreScorePlugin)
		if !ok {
			continue
		}
		if status := ps.PreScore(ctx, pod, nodes); !status.IsSuccess() {
			logger.Info(fmt.Sprintf("[PreScore] pod=%s/%s plugin=%s failed: %s",
				pod.Namespace, pod.Name, plugin.Name(), status.Message()))
			return status
		}
	}
	return utils.NewStatus(utils.Success, "")
}

func (f *frameworkImpl) RunScorePlugins(ctx context.Context, pod *v1.Pod, nodes []*v1.Node) (utils.PluginResultMap, *utils.Status) {
	result := utils.PluginResultMap{}

	for _, node := range nodes {
		result[node.Name] = utils.PluginResult{
			IsFiltered:     false,
			Scores:         make([]utils.PluginScore, 0),
			TotalNodeScore: 0,
		}
	}

	for _, plugin := range f.scorePlugins {
		for _, node := range nodes {
			score, status := plugin.Score(ctx, pod, node.Name)
			if !status.IsSuccess() {
				return nil, status
			}

			pluginResult := result[node.Name]
			pluginResult.Scores = append(pluginResult.Scores, utils.PluginScore{
				PluginName: plugin.Name(),
				Score:      score,
			})
			result[node.Name] = pluginResult
		}

		// NormalizeScore runs once per plugin after every candidate has been
		// scored. A plugin may only rewrite the PluginScore entry carrying its
		// own Name(); totals are always recomputed by the framework afterwards
		// so a normalized value cannot drift from TotalNodeScore.
		if ext := plugin.ScoreExtensions(); ext != nil {
			if status := ext.NormalizeScore(ctx, pod, result); !status.IsSuccess() {
				logger.Info(fmt.Sprintf("[NormalizeScore] pod=%s/%s plugin=%s failed: %s",
					pod.Namespace, pod.Name, plugin.Name(), status.Message()))
				return nil, status
			}
		}

		recomputeTotalNodeScores(result)
	}

	return result, utils.NewStatus(utils.Success, "")
}

func recomputeTotalNodeScores(result utils.PluginResultMap) {
	for nodeName, pluginResult := range result {
		total := 0
		for _, s := range pluginResult.Scores {
			total += int(s.Score)
		}
		pluginResult.TotalNodeScore = total
		result[nodeName] = pluginResult
	}
}

func (f *frameworkImpl) CommitPlacementDecisions(pod *v1.Pod, selectedHost string, selectErr error) {
	for _, plugin := range f.scorePlugins {
		if c, ok := plugin.(PlacementDecisionCommitter); ok {
			c.CommitPlacementDecision(pod, selectedHost, selectErr)
		}
	}
}

func (f *frameworkImpl) RunBindPlugin(ctx context.Context, pod *v1.Pod, nodeName string) *utils.Status {
	if f.bindPlugin == nil {
		return utils.NewStatus(utils.Error, "Bind plugin is not set")
	}

	status := f.bindPlugin.Bind(ctx, pod, nodeName)
	if !status.IsSuccess() {
		logger.Info(fmt.Sprintf("[Bind] pod=%s/%s node=%s plugin=%s failed: %s",
			pod.Namespace, pod.Name, nodeName, f.bindPlugin.Name(), status.Message()))
		return status
	}

	return utils.NewStatus(utils.Success, "")
}
