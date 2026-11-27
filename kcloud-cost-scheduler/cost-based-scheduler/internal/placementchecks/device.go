package placementchecks

import (
	"strings"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"

	v1 "k8s.io/api/core/v1"
)

// NodeResourceView is the Kubernetes allocatable/allocated snapshot for check 1.
// Allocatable == nil means the view was not supplied (unknown), not "zero GPUs".
type NodeResourceView struct {
	Allocatable       v1.ResourceList
	AllocatedMilliCPU int64
	AllocatedMemory   int64
	AllocatedScalar   map[string]int64
}

// EvaluateAccelerator is check 1: kind/count/model (+ partition UNEVALUATED).
// Known REJECT (model/count) is preferred over partition_unsupported.
func EvaluateAccelerator(pod *v1.Pod, node *v1.Node, res NodeResourceView, cfg CheckConfig, now time.Time) CheckResult {
	start := time.Now()
	cr := CheckResult{
		Check:       CheckAccelerator,
		Mode:        cfg.Mode,
		EvaluatedAt: now.UTC(),
		InputValue:  map[string]any{},
		Threshold:   map[string]any{},
	}
	if cfg.Mode == ModeOff {
		cr.Result = ResultUNEVALUATED
		cr.Code = "check_disabled"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if pod == nil || node == nil {
		cr.Result = ResultUNEVALUATED
		cr.Code = "missing_pod_or_node"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	cr.InputValue["node"] = node.Name

	hasMIG := requestsMIG(pod)
	cr.InputValue["partition_requested"] = hasMIG

	models := acceleratorfit.EvaluateModelFit(pod, node)
	counts := acceleratorfit.EvaluateAcceleratorCounts(pod, res.Allocatable, res.AllocatedScalar)
	cr.InputValue["model_results"] = models
	cr.InputValue["count_results"] = counts

	anyAccel := acceleratorfit.PodRequestsAcceleratorType(pod, "gpu") ||
		acceleratorfit.PodRequestsAcceleratorType(pod, "npu")
	if !anyAccel && len(models) == 0 && len(counts) == 0 {
		cr.Result = ResultPASS
		cr.Code = "no_accelerator_constraint"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	for _, m := range models {
		if !m.OK {
			cr.Result = ResultREJECT
			cr.Code = m.Code
			cr.Detail = m.Detail
			cr.Enforced = cfg.Mode == ModeEnforce
			cr.DurationNS = time.Since(start).Nanoseconds()
			return cr
		}
	}

	viewMissing := false
	for _, c := range counts {
		if isMIGResourceName(c.Resource) {
			continue // partition handled after known non-MIG rejects
		}
		if c.Code == "resource_view_missing" {
			viewMissing = true
			continue
		}
		if !c.OK {
			cr.Result = ResultREJECT
			cr.Code = c.Code
			cr.Detail = c.Detail
			cr.Enforced = cfg.Mode == ModeEnforce
			cr.DurationNS = time.Since(start).Nanoseconds()
			return cr
		}
	}
	if viewMissing {
		cr.Result = ResultUNEVALUATED
		cr.Code = "resource_view_missing"
		cr.Detail = "NodeResourceView.Allocatable not provided; not treated as measured shortage"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	if hasMIG {
		cr.Result = ResultUNEVALUATED
		cr.Code = "partition_unsupported"
		cr.Detail = "MIG/partition requests are not evaluated yet; non-MIG checks already passed"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	cr.Result = ResultPASS
	cr.Code = "accelerator_ok"
	cr.DurationNS = time.Since(start).Nanoseconds()
	return cr
}

func requestsMIG(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}
	check := func(containers []v1.Container) bool {
		for _, c := range containers {
			for name, qty := range c.Resources.Requests {
				if qty.Value() <= 0 {
					continue
				}
				if isMIGResourceName(string(name)) {
					return true
				}
			}
		}
		return false
	}
	return check(pod.Spec.Containers) || check(pod.Spec.InitContainers)
}

func isMIGResourceName(name string) bool {
	return strings.Contains(strings.ToLower(name), "mig")
}
