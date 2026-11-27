package webhook

import (
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// acceleratorResourceInfo is the hardware-neutral identity propagated through
// Workload -> Pod annotations.  The resource name remains the source of truth;
// vendor/model are hints that can later be replaced by a server-side catalog.
type acceleratorResourceInfo struct {
	ResourceName string
	Type         string
	Vendor       string
	Model        string
}

// resourceSummary separates accelerator type from full resource composition.
//
// A normal NPU Pod also requests CPU, so CPU+NPU must remain acc-type=npu while
// resource-composition records cpu+npu. GPU+NPU is the accelerator-mixed case;
// its scoring policy is intentionally left neutral until mixed-device tuning is
// implemented.
type resourceSummary struct {
	HasCPU         bool
	GPUCount       int64
	NPUCount       int64
	Resources      map[string]acceleratorResourceInfo
	ResourceCounts map[string]int64
}

func newResourceSummary() resourceSummary {
	return resourceSummary{
		Resources:      make(map[string]acceleratorResourceInfo),
		ResourceCounts: make(map[string]int64),
	}
}

func (s *resourceSummary) merge(other resourceSummary, multiplier int64) {
	if multiplier <= 0 {
		multiplier = 1
	}
	s.HasCPU = s.HasCPU || other.HasCPU
	s.GPUCount += other.GPUCount * multiplier
	s.NPUCount += other.NPUCount * multiplier
	if s.Resources == nil {
		s.Resources = make(map[string]acceleratorResourceInfo)
	}
	if s.ResourceCounts == nil {
		s.ResourceCounts = make(map[string]int64)
	}
	for name, info := range other.Resources {
		s.Resources[name] = info
		s.ResourceCounts[name] += other.ResourceCounts[name] * multiplier
	}
}

func (s resourceSummary) acceleratorType() string {
	switch {
	case s.GPUCount > 0 && s.NPUCount > 0:
		return "mixed"
	case s.GPUCount > 0:
		return "gpu"
	case s.NPUCount > 0:
		return "npu"
	default:
		return "cpu"
	}
}

func (s resourceSummary) acceleratorCount() int64 {
	return s.GPUCount + s.NPUCount
}

func (s resourceSummary) composition() string {
	parts := make([]string, 0, 3)
	if s.HasCPU {
		parts = append(parts, "cpu")
	}
	if s.GPUCount > 0 {
		parts = append(parts, "gpu")
	}
	if s.NPUCount > 0 {
		parts = append(parts, "npu")
	}
	if len(parts) == 0 {
		return "cpu"
	}
	return strings.Join(parts, "+")
}

func (s resourceSummary) identity() (resourceNames, vendor, model string) {
	if len(s.Resources) == 0 {
		return "", "", ""
	}
	names := make([]string, 0, len(s.Resources))
	vendors := map[string]struct{}{}
	models := map[string]struct{}{}
	for name, info := range s.Resources {
		names = append(names, name)
		if info.Vendor != "" {
			vendors[info.Vendor] = struct{}{}
		}
		if info.Model != "" {
			models[info.Model] = struct{}{}
		}
	}
	sort.Strings(names)
	resourceNames = strings.Join(names, ",")
	vendor = joinSortedValues(vendors)
	model = joinSortedValues(models)
	return resourceNames, vendor, model
}

// identityForType keeps GPU and NPU identity independently addressable. This
// avoids losing the model-to-device relation when a Pod requests both types.
func (s resourceSummary) identityForType(acceleratorType string) (vendor, model string) {
	vendors := map[string]struct{}{}
	models := map[string]struct{}{}
	for _, info := range s.Resources {
		if info.Type != acceleratorType {
			continue
		}
		if info.Vendor != "" {
			vendors[info.Vendor] = struct{}{}
		}
		if info.Model != "" {
			models[info.Model] = struct{}{}
		}
	}
	return joinSortedValues(vendors), joinSortedValues(models)
}

func setTypedAcceleratorAnnotations(annotations map[string]string, summary resourceSummary) {
	gpuVendor, gpuModel := summary.identityForType("gpu")
	if gpuVendor != "" {
		annotations[annotationGPUVendor] = gpuVendor
	}
	// nvidia.com/gpu does not identify the physical model. Preserve an explicit
	// Workload model choice instead of replacing it with "generic".
	if gpuModel != "" && (gpuModel != "generic" || annotations[annotationGPUModel] == "") {
		annotations[annotationGPUModel] = gpuModel
	}

	npuVendor, npuModel := summary.identityForType("npu")
	if npuVendor != "" {
		annotations[annotationNPUVendor] = npuVendor
	}
	if npuModel != "" && (npuModel != "generic" || annotations[annotationNPUModel] == "") {
		annotations[annotationNPUModel] = npuModel
	}
}

func joinSortedValues(values map[string]struct{}) string {
	if len(values) == 0 {
		return ""
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return strings.Join(result, ",")
}

func (s resourceSummary) resourceCountString() string {
	if len(s.ResourceCounts) == 0 {
		return ""
	}
	resources := make([]string, 0, len(s.ResourceCounts))
	for name, count := range s.ResourceCounts {
		resources = append(resources, name+"="+strconv.FormatInt(count, 10))
	}
	sort.Strings(resources)
	return strings.Join(resources, ",")
}

func summarizeContainerResources(containers []corev1.Container) resourceSummary {
	summary := newResourceSummary()
	for _, container := range containers {
		resources := effectiveResourceList(container)
		if cpu, ok := resources[corev1.ResourceCPU]; ok && cpu.Sign() > 0 {
			summary.HasCPU = true
		}
		for name, quantity := range resources {
			if quantity.Sign() <= 0 {
				continue
			}
			info, found := classifyAcceleratorResource(name)
			if !found {
				continue
			}
			summary.Resources[info.ResourceName] = info
			summary.ResourceCounts[info.ResourceName] += quantity.Value()
			switch info.Type {
			case "gpu":
				summary.GPUCount += quantity.Value()
			case "npu":
				summary.NPUCount += quantity.Value()
			}
		}
	}
	return summary
}

// effectiveResourceList uses requests as the scheduling source of truth and
// falls back to limits for older manifests/tests. Request values overwrite the
// same limit key so an extended resource is never counted twice.
func effectiveResourceList(container corev1.Container) corev1.ResourceList {
	result := corev1.ResourceList{}
	for name, quantity := range container.Resources.Limits {
		result[name] = quantity.DeepCopy()
	}
	for name, quantity := range container.Resources.Requests {
		result[name] = quantity.DeepCopy()
	}
	return result
}

// classifyAcceleratorResource provides a provisional generic classifier for
// Windows/WSL flow tests. Before hardware rollout this should be backed by a
// ConfigMap/ResourceSlice catalog, keeping this function as the fallback.
func classifyAcceleratorResource(resourceName corev1.ResourceName) (acceleratorResourceInfo, bool) {
	raw := strings.ToLower(string(resourceName))
	domain, model, hasSlash := strings.Cut(raw, "/")
	if !hasSlash || model == "" {
		return acceleratorResourceInfo{}, false
	}

	vendor := strings.Split(domain, ".")[0]
	info := acceleratorResourceInfo{
		ResourceName: string(resourceName),
		Vendor:       vendor,
		Model:        model,
	}

	switch {
	case domain == "nvidia.com" && (model == "gpu" || strings.Contains(model, "mig")):
		info.Type = "gpu"
	case domain == "furiosa.ai":
		// Furiosa resource names such as warboy/rngd do not contain "npu".
		info.Type = "npu"
	case strings.Contains(model, "npu") || strings.Contains(domain, "npu"):
		info.Type = "npu"
	case strings.Contains(model, "gpu"):
		info.Type = "gpu"
	default:
		return acceleratorResourceInfo{}, false
	}

	if model == "gpu" || model == "npu" {
		info.Model = "generic"
	}
	return info, true
}
