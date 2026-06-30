/*
Node state representation and caching

This module provides the NodeInfo structure, which tracks a specific node's
allocatable resources, requested resources, and currently scheduled pods
to facilitate rapid scheduling decisions in memory.
*/
package utils

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
)

type NodeInfo struct {
	node                         *corev1.Node
	Pods                         []*PodInfo
	PodsWithAffinity             []*PodInfo
	PodsWithRequiredAntiAffinity []*PodInfo
	UsedPorts                    HostPortInfo
	Requested                    *Resource
	Allocatable                  *Resource
	ImageStates                  map[string]*ImageStateSummary
	PVCRefCounts                 map[string]int
	GPUMap                       map[string]GPUInfo
	GPUMetricsUpdatedAt          time.Time
}

func NewNodeInfo(pods ...*corev1.Pod) *NodeInfo {
	ni := &NodeInfo{
		Requested:    &Resource{},
		Allocatable:  &Resource{},
		UsedPorts:    make(HostPortInfo),
		ImageStates:  make(map[string]*ImageStateSummary),
		PVCRefCounts: make(map[string]int),
		GPUMap:       make(map[string]GPUInfo),
	}
	for _, pod := range pods {
		ni.AddPod(pod)
	}
	return ni
}

func (n *NodeInfo) Node() *corev1.Node {
	if n == nil {
		return nil
	}
	return n.node
}

func (n *NodeInfo) AddPod(pods ...*corev1.Pod) {
	for _, pod := range pods {
		podInfo, _ := NewPodInfo(pod)
		n.AddPodInfo(podInfo)
	}
}

func (n *NodeInfo) AddPodInfo(podInfo *PodInfo) {
	n.Pods = append(n.Pods, podInfo)
	if podWithAffinity(podInfo.Pod) {
		n.PodsWithAffinity = append(n.PodsWithAffinity, podInfo)
	}
	if podWithRequiredAntiAffinity(podInfo.Pod) {
		n.PodsWithRequiredAntiAffinity = append(n.PodsWithRequiredAntiAffinity, podInfo)
	}
	n.update(podInfo.Pod, 1)
}

func (n *NodeInfo) RemovePod(pod *corev1.Pod) error {
	k, err := GetPodKey(pod)
	if err != nil {
		return err
	}
	if podWithAffinity(pod) {
		n.PodsWithAffinity, _ = removeFromSlice(n.PodsWithAffinity, k)
	}
	if podWithRequiredAntiAffinity(pod) {
		n.PodsWithRequiredAntiAffinity, _ = removeFromSlice(n.PodsWithRequiredAntiAffinity, k)
	}

	var removed bool
	if n.Pods, removed = removeFromSlice(n.Pods, k); removed {
		n.update(pod, -1)
		return nil
	}
	return fmt.Errorf("노드 %s에서 대상 Pod %s를 찾을 수 없음", n.node.Name, pod.Name)
}

func (n *NodeInfo) update(pod *corev1.Pod, sign int64) {
	res := calculateResource(pod)
	n.Requested.MilliCPU += sign * res.MilliCPU
	n.Requested.Memory += sign * res.Memory
	n.Requested.EphemeralStorage += sign * res.EphemeralStorage
	if n.Requested.ScalarResources == nil && len(res.ScalarResources) > 0 {
		n.Requested.ScalarResources = map[corev1.ResourceName]int64{}
	}
	for rName, rQuant := range res.ScalarResources {
		n.Requested.ScalarResources[rName] += sign * rQuant
	}

	n.updateUsedPorts(pod, sign > 0)
	n.updatePVCRefCounts(pod, sign > 0)
}

func calculateResource(pod *corev1.Pod) Resource {
	var res Resource
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return res
	}

	requests := PodRequests(pod)
	res.Add(requests)
	return res
}

func (n *NodeInfo) updateUsedPorts(pod *corev1.Pod, add bool) {
	for _, container := range pod.Spec.Containers {
		for _, podPort := range container.Ports {
			if add {
				n.UsedPorts.Add(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
			} else {
				n.UsedPorts.Remove(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
			}
		}
	}
}

func (n *NodeInfo) updatePVCRefCounts(pod *corev1.Pod, add bool) {
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim == nil {
			continue
		}

		key := GetNamespacedName(pod.Namespace, v.PersistentVolumeClaim.ClaimName)
		if add {
			n.PVCRefCounts[key] += 1
		} else {
			n.PVCRefCounts[key] -= 1
			if n.PVCRefCounts[key] <= 0 {
				delete(n.PVCRefCounts, key)
			}
		}
	}
}

func (n *NodeInfo) SetNode(node *corev1.Node) {
	n.node = node
	n.Allocatable = NewResource(node.Status.Allocatable)
}

func (n *NodeInfo) RemoveNode() {
	n.node = nil
}

func podWithAffinity(p *corev1.Pod) bool {
	affinity := p.Spec.Affinity
	return affinity != nil && (affinity.PodAffinity != nil || affinity.PodAntiAffinity != nil)
}

func podWithRequiredAntiAffinity(p *corev1.Pod) bool {
	affinity := p.Spec.Affinity
	return affinity != nil && affinity.PodAntiAffinity != nil &&
		len(affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0
}

type GPUInfo struct {
	uuid string
	arch string
}
