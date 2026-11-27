/*
Core interfaces for the scheduling framework plugins

This module defines the essential interfaces such as Filter, Score, and Permit
that plugins must implement to participate in the scheduling process.
*/
package framework

import (
	"context"
	"time"

	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	v1 "k8s.io/api/core/v1"
)

type Plugin interface {
	Name() string
}

type FilterPlugin interface {
	Plugin
	Filter(ctx context.Context, pod *v1.Pod, nodeInfo *utils.NodeInfo) *utils.Status
}

type ScorePlugin interface {
	Plugin
	Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status)
	ScoreExtensions() ScoreExtensions
}

// PreScorePlugin prepares Profile predictions for Filter-passed nodes before
// Score. Lookups belong here; Score must compute from shared state only.
type PreScorePlugin interface {
	Plugin
	PreScore(ctx context.Context, pod *v1.Pod, nodes []*v1.Node) *utils.Status
}

// ScoreExtensions runs once per plugin after every candidate node has been
// scored, so it receives the whole candidate map rather than a single node.
type ScoreExtensions interface {
	NormalizeScore(ctx context.Context, pod *v1.Pod, scores utils.PluginResultMap) *utils.Status
}

type BindPlugin interface {
	Plugin
	Bind(ctx context.Context, pod *v1.Pod, nodeName string) *utils.Status
}

type PreFilterPlugin interface {
	Plugin
	PreFilter(ctx context.Context, pod *v1.Pod) *utils.Status
}

type PermitPlugin interface {
	Plugin
	Permit(ctx context.Context, pod *v1.Pod, nodeName string) (PermitStatus, time.Duration)
}

type PermitStatus int

const (
	PermitSuccess PermitStatus = iota
	PermitWait
	PermitDeny
)

// PlacementDecisionCommitter posts catalog placement_decision after the final
// node selection is known (not during NormalizeScore).
type PlacementDecisionCommitter interface {
	CommitPlacementDecision(pod *v1.Pod, selectedHost string, selectErr error)
}

type Framework interface {
	RunPreFilterPlugins(ctx context.Context, pod *v1.Pod) *utils.Status
	RunFilterPlugins(ctx context.Context, pod *v1.Pod, nodeInfo *utils.NodeInfo) utils.PluginResultMap
	RunPreScorePlugins(ctx context.Context, pod *v1.Pod, nodes []*v1.Node) *utils.Status
	RunScorePlugins(ctx context.Context, pod *v1.Pod, nodes []*v1.Node) (utils.PluginResultMap, *utils.Status)
	CommitPlacementDecisions(pod *v1.Pod, selectedHost string, selectErr error)
	RunPermitPlugins(ctx context.Context, pod *v1.Pod, nodeName string) *utils.Status
	RunBindPlugin(ctx context.Context, pod *v1.Pod, nodeName string) *utils.Status
}
