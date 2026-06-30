/*
PodAffinity Filter and Score Plugin

This plugin evaluates pod affinity and anti-affinity rules to co-locate or
disperse pods accordingly.
*/
package plugin

import (
	"context"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const PodAffinityName = "PodAffinity"

type PodAffinity struct {
	cache *utils.Cache
}

var _ framework.ScorePlugin = &PodAffinity{}

func NewPodAffinity(cache *utils.Cache) *PodAffinity {
	return &PodAffinity{
		cache: cache,
	}
}

func (p *PodAffinity) Name() string {
	return PodAffinityName
}

func (p *PodAffinity) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	nodeInfo := p.getNodeInfo(nodeName)
	if nodeInfo == nil {
		klog.V(4).InfoS("PodAffinity: node not found", "node", nodeName)
		return 10, utils.NewStatus(utils.Success, "") // neutral score
	}

	affinityMode := "preferred"
	if pod.Annotations != nil {
		if mode, ok := pod.Annotations["scheduler.affinity-mode"]; ok {
			affinityMode = mode
		}
	}

	if affinityMode == "none" {
		klog.V(4).InfoS("PodAffinity: mode is none, returning neutral score",
			"node", nodeName,
			"score", 10)
		return 10, utils.NewStatus(utils.Success, "")
	}

	podJobName, podQueueName := getPodJobAndQueueNames(pod)

	sameJobCount := 0
	sameQueueCount := 0

	for _, existingPodInfo := range nodeInfo.Pods {
		if existingPodInfo == nil || existingPodInfo.Pod == nil {
			continue
		}
		existingPod := existingPodInfo.Pod

		if existingPod.Status.Phase == v1.PodSucceeded || existingPod.Status.Phase == v1.PodFailed {
			continue
		}

		if existingPod.UID == pod.UID {
			continue
		}

		existingJob, existingQueue := getPodJobAndQueueNames(existingPod)

		if podJobName != "" && existingJob == podJobName {
			sameJobCount++
		} else if podQueueName != "" && existingQueue == podQueueName {
			sameQueueCount++
		}
	}

	baseScore := 10.0
	var finalScore float64

	switch affinityMode {
	case "preferred":
		if sameJobCount > 0 {
			bonus := float64(sameJobCount) * 2.0
			finalScore = baseScore + bonus
			if finalScore > 20.0 {
				finalScore = 20.0 // cap at max
			}
			klog.V(4).InfoS("PodAffinity: preferred mode with same-job pods",
				"node", nodeName,
				"sameJobCount", sameJobCount,
				"jobName", podJobName,
				"bonus", bonus,
				"score", finalScore)
		} else if sameQueueCount > 0 {
			bonus := float64(sameQueueCount) * 1.0
			finalScore = baseScore + bonus
			if finalScore > 20.0 {
				finalScore = 20.0 // cap at max
			}
			klog.V(4).InfoS("PodAffinity: preferred mode with same-queue pods",
				"node", nodeName,
				"sameQueueCount", sameQueueCount,
				"queueName", podQueueName,
				"bonus", bonus,
				"score", finalScore)
		} else {
			finalScore = baseScore
			klog.V(4).InfoS("PodAffinity: preferred mode with no related pods",
				"node", nodeName,
				"score", finalScore)
		}

	case "anti":
		if sameJobCount > 0 {
			penalty := float64(sameJobCount) * -5.0
			finalScore = baseScore + penalty
			if finalScore < 0.0 {
				finalScore = 0.0 // floor at 0
			}
			klog.V(4).InfoS("PodAffinity: anti mode with same-job pods",
				"node", nodeName,
				"sameJobCount", sameJobCount,
				"jobName", podJobName,
				"penalty", penalty,
				"score", finalScore)
		} else if sameQueueCount > 0 {
			penalty := float64(sameQueueCount) * -2.0
			finalScore = baseScore + penalty
			if finalScore < 0.0 {
				finalScore = 0.0 // floor at 0
			}
			klog.V(4).InfoS("PodAffinity: anti mode with same-queue pods",
				"node", nodeName,
				"sameQueueCount", sameQueueCount,
				"queueName", podQueueName,
				"penalty", penalty,
				"score", finalScore)
		} else {
			finalScore = 20.0
			klog.V(4).InfoS("PodAffinity: anti mode with no related pods (perfect spreading)",
				"node", nodeName,
				"score", finalScore)
		}

	default:
		finalScore = baseScore
		klog.V(4).InfoS("PodAffinity: unknown mode, using neutral score",
			"node", nodeName,
			"mode", affinityMode,
			"score", finalScore)
	}

	return int64(finalScore), utils.NewStatus(utils.Success, "")
}

func (p *PodAffinity) ScoreExtensions() framework.ScoreExtensions {
	return p
}

func (p *PodAffinity) NormalizeScore(ctx context.Context, pod *v1.Pod, scores utils.PluginResult) *utils.Status {
	return utils.NewStatus(utils.Success, "")
}

func (p *PodAffinity) getNodeInfo(nodeName string) *utils.NodeInfo {
	if p.cache == nil {
		return nil
	}
	nodes := p.cache.Nodes()
	if nodes == nil {
		return nil
	}
	return nodes[nodeName]
}

// getPodJobAndQueueNames extracts job and queue names from pod labels.
func getPodJobAndQueueNames(pod *v1.Pod) (jobName string, queueName string) {
	if pod.Labels == nil {
		return "", ""
	}
	if v := pod.Labels["batch.kubernetes.io/job-name"]; v != "" {
		jobName = v
	} else if v := pod.Labels["job-name"]; v != "" {
		jobName = v
	}
	if v := pod.Labels["kueue.x-k8s.io/queue-name"]; v != "" {
		queueName = v
	} else if v := pod.Labels["queue-name"]; v != "" {
		queueName = v
	}
	return jobName, queueName
}
