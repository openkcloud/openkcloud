/*
DurationAware Scheduling Plugin

This plugin categorizes workloads as short or long running and segregates them
to minimize fragmentation over time.
*/
package plugin


import (
	"context"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const (
	DurationAwareName         = "DurationAwareScheduling"
	podDurationClassAnnotation  = "sched.ai/duration-class"   // long | short | any
	nodeDurationClassLabel      = "node.kcloud/scheduling-class" // long | short | any
)

type DurationAwareScheduling struct {
	cache *utils.Cache
}

var _ framework.PreFilterPlugin = &DurationAwareScheduling{}
var _ framework.ScorePlugin = &DurationAwareScheduling{}

func NewDurationAwareScheduling(cache *utils.Cache) *DurationAwareScheduling {
	return &DurationAwareScheduling{cache: cache}
}

func (d *DurationAwareScheduling) Name() string { return DurationAwareName }

func (d *DurationAwareScheduling) PreFilter(ctx context.Context, pod *v1.Pod) *utils.Status {
	podClass := podDurationClass(pod)
	if podClass == "" || podClass == "any" {
		return utils.NewStatus(utils.Success, "")
	}

	nodes := d.cache.Nodes()
	for _, nodeInfo := range nodes {
		if nodeInfo == nil || nodeInfo.Node() == nil {
			continue
		}
		if nodeClass, ok := nodeInfo.Node().Labels[nodeDurationClassLabel]; ok {
			if nodeClass == podClass {
				klog.V(5).InfoS("DurationAware: matching node found",
					"pod", klog.KObj(pod), "class", podClass, "node", nodeInfo.Node().Name)
				return utils.NewStatus(utils.Success, "")
			}
		}
	}

	klog.V(3).InfoS("DurationAware: no matching-class node found, allowing any node",
		"pod", klog.KObj(pod), "requestedClass", podClass)
	return utils.NewStatus(utils.Success, "")
}

func (d *DurationAwareScheduling) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := d.nodeInfo(nodeName)
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return 0, utils.NewStatus(utils.Success, "")
	}

	podClass := podDurationClass(pod)
	if podClass == "" || podClass == "any" {
		return 10, utils.NewStatus(utils.Success, "") // 무관심 Pod는 균등 점수
	}

	nodeClass, hasLabel := nodeInfo.Node().Labels[nodeDurationClassLabel]
	if !hasLabel || nodeClass == "any" {
		klog.V(5).InfoS("DurationAware: node has no class label, neutral score",
			"pod", klog.KObj(pod), "node", nodeName)
		return 10, utils.NewStatus(utils.Success, "")
	}

	if nodeClass == podClass {
		klog.V(4).InfoS("DurationAware: class match, full score",
			"pod", klog.KObj(pod), "node", nodeName, "class", podClass)
		return 20, utils.NewStatus(utils.Success, "")
	}

	klog.V(4).InfoS("DurationAware: class mismatch, zero score",
		"pod", klog.KObj(pod), "node", nodeName,
		"podClass", podClass, "nodeClass", nodeClass)
	return 0, utils.NewStatus(utils.Success, "")
}

func (d *DurationAwareScheduling) ScoreExtensions() framework.ScoreExtensions { return d }
func (d *DurationAwareScheduling) NormalizeScore(_ context.Context, _ *v1.Pod, _ utils.PluginResult) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}

func (d *DurationAwareScheduling) nodeInfo(name string) *utils.NodeInfo {
	if d.cache == nil {
		return nil
	}
	return d.cache.Nodes()[name]
}

func podDurationClass(pod *v1.Pod) string {
	if cls, ok := pod.Annotations[podDurationClassAnnotation]; ok {
		return cls
	}
	switch pod.Annotations["sched.ai/intent"] {
	case "training":
		return "long"
	case "inference", "serving":
		return "short"
	default:
		return "any"
	}
}
