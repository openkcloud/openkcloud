// Package acceleratorfit holds pure accelerator identity / fit helpers shared by
// Filter plugins and placementchecks (no double-logic forks).
package acceleratorfit

import (
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
)

const (
	GPUModelAnnotation = "sched.ai/gpu-model"
	NPUModelAnnotation = "sched.ai/npu-model"
	GPUModelLabel      = "sched.ai/gpu-model"
	NPUModelLabel      = "sched.ai/npu-model"
)

// CanonicalModel normalizes product names. Spaces/underscores become hyphens
// before alias matching so "NVIDIA RTX PRO 6000" ≡ "rtx-pro-6000".
// Longer tokens win (l40s before l4).
func CanonicalModel(raw string) string {
	model := strings.ToLower(strings.TrimSpace(raw))
	if model == "" {
		return ""
	}
	model = strings.NewReplacer(" ", "-", "_", "-").Replace(model)
	for strings.Contains(model, "--") {
		model = strings.ReplaceAll(model, "--", "-")
	}
	// Order matters: more specific / longer first.
	for _, known := range []string{"l40s", "rtx-pro-6000", "pro-6000", "warboy", "rngd", "a100", "h100", "l40", "l4"} {
		if strings.Contains(model, known) {
			return known
		}
	}
	return model
}

func AcceleratorResourceType(resourceName v1.ResourceName) string {
	raw := strings.ToLower(string(resourceName))
	domain, name, found := strings.Cut(raw, "/")
	if !found || name == "" {
		return ""
	}
	switch {
	case domain == "nvidia.com" && (name == "gpu" || strings.Contains(name, "mig")):
		return "gpu"
	case domain == "furiosa.ai":
		return "npu"
	case strings.Contains(domain, "npu") || strings.Contains(name, "npu"):
		return "npu"
	case strings.Contains(name, "gpu"):
		return "gpu"
	default:
		return ""
	}
}

// PodRequestsAcceleratorType is true if any container or init container requests
// a positive quantity of that accelerator kind.
func PodRequestsAcceleratorType(pod *v1.Pod, acceleratorType string) bool {
	if pod == nil {
		return false
	}
	check := func(containers []v1.Container) bool {
		for _, container := range containers {
			for resourceName, quantity := range container.Resources.Requests {
				if quantity.Value() > 0 && AcceleratorResourceType(resourceName) == acceleratorType {
					return true
				}
			}
			for resourceName, quantity := range container.Resources.Limits {
				if quantity.Value() > 0 && AcceleratorResourceType(resourceName) == acceleratorType {
					return true
				}
			}
		}
		return false
	}
	return check(pod.Spec.Containers) || check(pod.Spec.InitContainers)
}

func AnnotationModels(pod *v1.Pod, key string) []string {
	if pod == nil || pod.Annotations == nil {
		return nil
	}
	models := map[string]struct{}{}
	for _, raw := range strings.Split(pod.Annotations[key], ",") {
		model := CanonicalModel(raw)
		if model == "" || model == "generic" {
			continue
		}
		models[model] = struct{}{}
	}
	return sortedKeys(models)
}

func NodeAcceleratorModels(node *v1.Node, acceleratorType string) []string {
	if node == nil {
		return nil
	}
	models := map[string]struct{}{}
	addModels := func(raw string) {
		for _, item := range strings.Split(raw, ",") {
			if model := CanonicalModel(item); model != "" {
				models[model] = struct{}{}
			}
		}
	}

	switch acceleratorType {
	case "gpu":
		addModels(node.Labels[GPUModelLabel])
		addModels(node.Labels["nvidia.com/gpu.product"])
	case "npu":
		addModels(node.Labels[NPUModelLabel])
	}
	if node.Labels["accelerator.type"] == acceleratorType {
		addModels(node.Labels["accelerator.model"])
	}

	for resourceName, quantity := range node.Status.Allocatable {
		if quantity.Value() <= 0 || AcceleratorResourceType(resourceName) != acceleratorType {
			continue
		}
		_, resourceModel, found := strings.Cut(strings.ToLower(string(resourceName)), "/")
		if found && resourceModel != "gpu" && resourceModel != "npu" && !strings.Contains(resourceModel, "mig") {
			addModels(resourceModel)
		}
	}
	return sortedKeys(models)
}

func Intersects(left, right []string) bool {
	values := make(map[string]struct{}, len(left))
	for _, item := range left {
		values[item] = struct{}{}
	}
	for _, item := range right {
		if _, ok := values[item]; ok {
			return true
		}
	}
	return false
}

func sortedKeys(models map[string]struct{}) []string {
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

// ModelFitResult is the shared accelerator product-name judgment.
type ModelFitResult struct {
	OK              bool
	Code            string
	Detail          string
	Kind            string
	RequestedModels []string
	NodeModels      []string
}

// EvaluateModelFit checks sched.ai/*-model annotations against node labels.
// No requested models → OK (nothing to enforce). Partition/MIG is not decided here.
func EvaluateModelFit(pod *v1.Pod, node *v1.Node) []ModelFitResult {
	var out []ModelFitResult
	for _, accelerator := range []struct {
		kind          string
		annotationKey string
	}{
		{kind: "gpu", annotationKey: GPUModelAnnotation},
		{kind: "npu", annotationKey: NPUModelAnnotation},
	} {
		if !PodRequestsAcceleratorType(pod, accelerator.kind) {
			continue
		}
		requested := AnnotationModels(pod, accelerator.annotationKey)
		if len(requested) == 0 {
			continue
		}
		nodeModels := NodeAcceleratorModels(node, accelerator.kind)
		ok := Intersects(requested, nodeModels)
		r := ModelFitResult{
			OK: ok, Kind: accelerator.kind,
			RequestedModels: requested, NodeModels: nodeModels,
		}
		if ok {
			r.Code = "model_match"
		} else {
			r.Code = "model_mismatch"
			r.Detail = "node accelerator models do not intersect requested models"
		}
		out = append(out, r)
	}
	return out
}
