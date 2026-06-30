/*
Scheduling calculation helpers

This module provides utility functions for resource request calculation,
normalization, and basic Kubernetes API interactions.
*/
package utils

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	SkipPodLevelResources = true
	ExcludeOverhead       = true
	UseStatusResources    = true
)

func PodRequests(pod *v1.Pod) v1.ResourceList {
	reqs := AggregateContainerRequests(pod)

	if !SkipPodLevelResources && IsPodLevelRequestsSet(pod) {
		for resourceName, quantity := range pod.Spec.Resources.Requests {
			if IsSupportedPodLevelResource(resourceName) {
				reqs[resourceName] = quantity
			}
		}
	}

	if !ExcludeOverhead && pod.Spec.Overhead != nil {
		addResourceList(reqs, pod.Spec.Overhead)
	}

	return reqs
}

func AggregateContainerRequests(pod *v1.Pod) v1.ResourceList {
	reqs := make(v1.ResourceList, 4)
	var containerStatuses map[string]*v1.ContainerStatus

	if UseStatusResources {
		containerStatuses = make(map[string]*v1.ContainerStatus, len(pod.Status.ContainerStatuses))
		for i := range pod.Status.ContainerStatuses {
			containerStatuses[pod.Status.ContainerStatuses[i].Name] = &pod.Status.ContainerStatuses[i]
		}
	}

	for _, container := range pod.Spec.Containers {
		containerReqs := container.Resources.Requests
		if UseStatusResources {
			cs, found := containerStatuses[container.Name]
			if found && cs.Resources != nil {
				if pod.Status.Resize == v1.PodResizeStatusInfeasible {
					containerReqs = cs.Resources.Requests.DeepCopy()
				} else {
					containerReqs = rmax(container.Resources.Requests, cs.Resources.Requests)
				}
			}
		}

		addResourceList(reqs, containerReqs)
	}

	restartableInitContainerReqs := v1.ResourceList{}
	initContainerReqs := v1.ResourceList{}

	for _, container := range pod.Spec.InitContainers {
		containerReqs := container.Resources.Requests

		if container.RestartPolicy != nil && *container.RestartPolicy == v1.ContainerRestartPolicyAlways {
			addResourceList(reqs, containerReqs)
			addResourceList(restartableInitContainerReqs, containerReqs)
			containerReqs = restartableInitContainerReqs
		} else {
			tmp := v1.ResourceList{}
			addResourceList(tmp, containerReqs)
			addResourceList(tmp, restartableInitContainerReqs)
			containerReqs = tmp
		}

		maxResourceList(initContainerReqs, containerReqs)
	}

	maxResourceList(reqs, initContainerReqs)
	return reqs
}

func addResourceList(list, newList v1.ResourceList) {
	for name, quantity := range newList {
		if value, ok := list[name]; !ok {
			list[name] = quantity.DeepCopy()
		} else {
			value.Add(quantity)
			list[name] = value
		}
	}
}

var supportedPodLevelResources = sets.New(v1.ResourceCPU, v1.ResourceMemory)

func IsSupportedPodLevelResource(name v1.ResourceName) bool {
	return supportedPodLevelResources.Has(name)
}

func IsPodLevelResourcesSet(pod *v1.Pod) bool {
	if pod.Spec.Resources == nil {
		return false
	}

	if (len(pod.Spec.Resources.Requests) + len(pod.Spec.Resources.Limits)) == 0 {
		return false
	}

	for resourceName := range pod.Spec.Resources.Requests {
		if IsSupportedPodLevelResource(resourceName) {
			return true
		}
	}

	for resourceName := range pod.Spec.Resources.Limits {
		if IsSupportedPodLevelResource(resourceName) {
			return true
		}
	}

	return false
}

func IsPodLevelRequestsSet(pod *v1.Pod) bool {
	if pod.Spec.Resources == nil {
		return false
	}

	if len(pod.Spec.Resources.Requests) == 0 {
		return false
	}

	for resourceName := range pod.Spec.Resources.Requests {
		if IsSupportedPodLevelResource(resourceName) {
			return true
		}
	}

	return false
}

func rmax(a v1.ResourceList, b v1.ResourceList) v1.ResourceList {
	result := v1.ResourceList{}
	for key, value := range a {
		if other, found := b[key]; found {
			if value.Cmp(other) <= 0 {
				result[key] = other.DeepCopy()
				continue
			}
		}
		result[key] = value.DeepCopy()
	}
	for key, value := range b {
		if _, found := result[key]; !found {
			result[key] = value.DeepCopy()
		}
	}
	return result
}

func maxResourceList(list, newList v1.ResourceList) {
	for name, quantity := range newList {
		if value, ok := list[name]; !ok || quantity.Cmp(value) > 0 {
			list[name] = quantity.DeepCopy()
		}
	}
}

func GetPodFullName(pod *v1.Pod) string {
	return pod.Name + "_" + pod.Namespace
}
