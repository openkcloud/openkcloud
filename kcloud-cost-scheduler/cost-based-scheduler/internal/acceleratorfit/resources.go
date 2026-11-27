package acceleratorfit

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
)

// ResourceRequest is a simplified pod request view for fit checks.
type ResourceRequest struct {
	MilliCPU        int64
	Memory          int64
	ScalarResources map[string]int64
}

// ComputePodResourceRequest builds effective requests matching Kubernetes
// AggregateContainerRequests (+ overhead), for the inputs we support:
//
//  1. sum(regular containers)
//  2. walk init in order: Always init accumulates and stays; each sequential
//     init peaks as (that init + Always inits started so far)
//  3. max(app+all Always, init-phase peak)
//  4. add Pod overhead last (never lost under a larger init peak)
func ComputePodResourceRequest(pod *v1.Pod) ResourceRequest {
	result := ResourceRequest{ScalarResources: map[string]int64{}}
	if pod == nil {
		return result
	}
	for _, container := range pod.Spec.Containers {
		addRequests(&result, container.Resources.Requests)
	}

	restartableAccum := ResourceRequest{ScalarResources: map[string]int64{}}
	initPeak := ResourceRequest{ScalarResources: map[string]int64{}}

	for _, container := range pod.Spec.InitContainers {
		var phase ResourceRequest
		if isRestartableInit(container) {
			addRequests(&result, container.Resources.Requests)
			addRequests(&restartableAccum, container.Resources.Requests)
			phase = cloneRequest(restartableAccum)
		} else {
			phase = cloneRequest(restartableAccum)
			addRequests(&phase, container.Resources.Requests)
		}
		maxRequest(&initPeak, phase)
	}
	maxRequest(&result, initPeak)

	if pod.Spec.Overhead != nil {
		addRequests(&result, pod.Spec.Overhead)
	}
	return result
}

func isRestartableInit(c v1.Container) bool {
	return c.RestartPolicy != nil && *c.RestartPolicy == v1.ContainerRestartPolicyAlways
}

func addRequests(result *ResourceRequest, requests v1.ResourceList) {
	if result.ScalarResources == nil {
		result.ScalarResources = map[string]int64{}
	}
	for name, qty := range requests {
		switch name {
		case v1.ResourceCPU:
			result.MilliCPU += qty.MilliValue()
		case v1.ResourceMemory:
			result.Memory += qty.Value()
		default:
			result.ScalarResources[string(name)] += qty.Value()
		}
	}
}

func cloneRequest(in ResourceRequest) ResourceRequest {
	out := ResourceRequest{
		MilliCPU:        in.MilliCPU,
		Memory:          in.Memory,
		ScalarResources: map[string]int64{},
	}
	for k, v := range in.ScalarResources {
		out.ScalarResources[k] = v
	}
	return out
}

func maxRequest(dst *ResourceRequest, src ResourceRequest) {
	if src.MilliCPU > dst.MilliCPU {
		dst.MilliCPU = src.MilliCPU
	}
	if src.Memory > dst.Memory {
		dst.Memory = src.Memory
	}
	if dst.ScalarResources == nil {
		dst.ScalarResources = map[string]int64{}
	}
	for k, v := range src.ScalarResources {
		if v > dst.ScalarResources[k] {
			dst.ScalarResources[k] = v
		}
	}
}

// CountFitResult is one accelerator resource count judgment.
type CountFitResult struct {
	OK          bool
	Code        string
	Detail      string
	Resource    string
	Requested   int64
	Available   int64
	Allocatable int64
	Used        int64
}

// EvaluateAcceleratorCounts checks positive accelerator scalar requests against
// node allocatable minus already allocated. allocatable==nil means the view was
// not supplied (caller should treat as unknown, not shortage).
func EvaluateAcceleratorCounts(pod *v1.Pod, allocatable v1.ResourceList, allocatedScalar map[string]int64) []CountFitResult {
	req := ComputePodResourceRequest(pod)
	var out []CountFitResult
	if allocatedScalar == nil {
		allocatedScalar = map[string]int64{}
	}
	for resourceName, podRequest := range req.ScalarResources {
		if podRequest <= 0 {
			continue
		}
		if AcceleratorResourceType(v1.ResourceName(resourceName)) == "" {
			continue
		}
		r := CountFitResult{Resource: resourceName, Requested: podRequest}
		if allocatable == nil {
			r.OK = false
			r.Code = "resource_view_missing"
			r.Detail = "NodeResourceView.Allocatable not provided"
			out = append(out, r)
			continue
		}
		nodeAllocatable, ok := allocatable[v1.ResourceName(resourceName)]
		if !ok || nodeAllocatable.Value() == 0 {
			r.OK = false
			r.Code = "resource_absent"
			r.Detail = fmt.Sprintf("node does not have resource %s", resourceName)
			out = append(out, r)
			continue
		}
		r.Allocatable = nodeAllocatable.Value()
		r.Used = allocatedScalar[resourceName]
		r.Available = r.Allocatable - r.Used
		if podRequest > r.Available {
			r.OK = false
			r.Code = "insufficient_count"
			r.Detail = fmt.Sprintf("Insufficient %s: requested %d, available %d", resourceName, podRequest, r.Available)
		} else {
			r.OK = true
			r.Code = "count_ok"
		}
		out = append(out, r)
	}
	return out
}

// HostFitResult covers CPU and host memory (legacy NodeResourcesFit core).
type HostFitResult struct {
	OK           bool
	Code         string
	Detail       string
	RequestedCPU int64
	AvailableCPU int64
	RequestedMem int64
	AvailableMem int64
}

func EvaluateHostResources(pod *v1.Pod, allocatable v1.ResourceList, allocatedMilliCPU, allocatedMemory int64) HostFitResult {
	req := ComputePodResourceRequest(pod)
	r := HostFitResult{
		OK: true, Code: "host_ok",
		RequestedCPU: req.MilliCPU, RequestedMem: req.Memory,
	}
	availCPU := allocatable.Cpu().MilliValue() - allocatedMilliCPU
	availMem := allocatable.Memory().Value() - allocatedMemory
	r.AvailableCPU = availCPU
	r.AvailableMem = availMem
	if req.MilliCPU > availCPU {
		r.OK = false
		r.Code = "insufficient_cpu"
		r.Detail = fmt.Sprintf("Insufficient cpu: requested %dm, available %dm", req.MilliCPU, availCPU)
		return r
	}
	if req.Memory > availMem {
		r.OK = false
		r.Code = "insufficient_memory"
		r.Detail = fmt.Sprintf("Insufficient memory: requested %d, available %d", req.Memory, availMem)
		return r
	}
	return r
}
