package placementchecks

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"

	v1 "k8s.io/api/core/v1"
)

// ParsedRequest is the attempt-scoped pod requirement view (parsed once).
type ParsedRequest struct {
	GPUCount            int64
	NPUCount            int64
	GPUMemoryMiB        *int64 // nil = missing/invalid → memory check UNEVALUATED
	NPUMemoryMiB        *int64
	GPUMemoryError      string // detail when annotation present but invalid
	NPUMemoryError      string
	RuntimeRequirements []RuntimeRequirement
	Topology            TopologyRequest
	RawAnnotations      map[string]string
}

// ParseRequest extracts accelerator counts, memory, runtime mins, and topology from a Pod.
// Device resource quantities are counts only; memory comes solely from annotations.
// Counts use the same Always-init sum / sequential-init max as NodeResourcesFit.
func ParseRequest(pod *v1.Pod) ParsedRequest {
	out := ParsedRequest{RawAnnotations: map[string]string{}}
	if pod == nil {
		return out
	}
	if pod.Annotations != nil {
		for k, v := range pod.Annotations {
			out.RawAnnotations[k] = v
		}
	}
	out.GPUCount = acceleratorCount(pod, "nvidia.com/gpu")
	out.NPUCount = acceleratorCount(pod, "furiosa.ai/rngd")
	if out.NPUCount == 0 {
		// Also count keti NPU PE style if present as positive request.
		out.NPUCount = acceleratorCount(pod, "keti.re.kr/npu-pe")
	}

	out.GPUMemoryMiB, out.GPUMemoryError = parseMemoryAnnotation(out.RawAnnotations[AnnotationGPUMemoryMiB])
	out.NPUMemoryMiB, out.NPUMemoryError = parseMemoryAnnotation(out.RawAnnotations[AnnotationNPUMemoryMiB])
	out.RuntimeRequirements = parseRuntimeRequirements(out.RawAnnotations)
	out.Topology = parseTopologyRequest(out.RawAnnotations)
	return out
}

func acceleratorCount(pod *v1.Pod, resName v1.ResourceName) int64 {
	return acceleratorfit.ComputePodResourceRequest(pod).ScalarResources[string(resName)]
}

// parseMemoryAnnotation returns (*value, "") on success.
// Missing empty string → (nil, ""); invalid → (nil, reason).
func parseMemoryAnnotation(raw string) (*int64, string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, ""
	}
	if strings.ContainsAny(s, ".eE") {
		// Reject floats explicitly (e.g. "1.5").
		if _, err := strconv.ParseFloat(s, 64); err == nil && !isIntString(s) {
			return nil, "non_integer_mib"
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, "parse_failed"
	}
	if n <= 0 {
		return nil, "non_positive_mib"
	}
	return &n, ""
}

func isIntString(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// MemoryBudgetFor returns the per-device MiB budget for kind "gpu"|"npu".
func (r ParsedRequest) MemoryBudgetFor(kind string) (budget *int64, count int64, errDetail string) {
	switch strings.ToLower(kind) {
	case "gpu":
		return r.GPUMemoryMiB, r.GPUCount, r.GPUMemoryError
	case "npu":
		return r.NPUMemoryMiB, r.NPUCount, r.NPUMemoryError
	default:
		return nil, 0, fmt.Sprintf("unknown_kind_%s", kind)
	}
}
