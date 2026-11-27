package plugin

import (
	"strings"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"

	v1 "k8s.io/api/core/v1"
)

const (
	gpuModelAnnotation = acceleratorfit.GPUModelAnnotation
	npuModelAnnotation = acceleratorfit.NPUModelAnnotation
	gpuModelLabel      = acceleratorfit.GPUModelLabel
	npuModelLabel      = acceleratorfit.NPUModelLabel
)

func nodeDeviceType(node *v1.Node) string {
	if node == nil {
		return ""
	}

	hasGPU := false
	hasNPU := false
	for name, quantity := range node.Status.Allocatable {
		if quantity.Value() <= 0 {
			continue
		}
		switch acceleratorfit.AcceleratorResourceType(name) {
		case "gpu":
			hasGPU = true
		case "npu":
			hasNPU = true
		}
	}

	if hasGPU && hasNPU {
		return "mixed"
	}
	if hasNPU {
		return "npu"
	}
	if hasGPU {
		return "gpu"
	}
	if acceleratorType, ok := node.Labels["accelerator.type"]; ok {
		return strings.ToLower(strings.TrimSpace(acceleratorType))
	}
	return ""
}

func acceleratorResourceType(resourceName v1.ResourceName) string {
	return acceleratorfit.AcceleratorResourceType(resourceName)
}

func nodeAcceleratorModels(node *v1.Node, acceleratorType string) []string {
	return acceleratorfit.NodeAcceleratorModels(node, acceleratorType)
}

func canonicalAcceleratorModel(raw string) string {
	return acceleratorfit.CanonicalModel(raw)
}

func annotationModels(pod *v1.Pod, key string) []string {
	return acceleratorfit.AnnotationModels(pod, key)
}

func podRequestsAcceleratorType(pod *v1.Pod, acceleratorType string) bool {
	return acceleratorfit.PodRequestsAcceleratorType(pod, acceleratorType)
}

func intersects(left, right []string) bool {
	return acceleratorfit.Intersects(left, right)
}
