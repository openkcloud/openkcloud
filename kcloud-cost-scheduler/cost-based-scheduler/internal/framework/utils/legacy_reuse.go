package utils

import (
	"context"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"
)

func checkFromReport(rep placementchecks.CandidateReport, id placementchecks.CheckID) (placementchecks.CheckResult, bool) {
	for _, cr := range rep.Checks {
		if cr.Check == id {
			return cr, true
		}
	}
	return placementchecks.CheckResult{}, false
}

// CachedModelFit returns model_results from the attempt's check-1 report when present.
func CachedModelFit(ctx context.Context, nodeName string) ([]acceleratorfit.ModelFitResult, bool) {
	st := AttemptFrom(ctx)
	if st == nil {
		return nil, false
	}
	rep, ok := st.Report(nodeName)
	if !ok {
		return nil, false
	}
	cr, ok := checkFromReport(rep, placementchecks.CheckAccelerator)
	if !ok || cr.InputValue == nil {
		return nil, false
	}
	models, ok := cr.InputValue["model_results"].([]acceleratorfit.ModelFitResult)
	return models, ok
}

// CachedAcceleratorCounts returns count_results from check 1 when present.
func CachedAcceleratorCounts(ctx context.Context, nodeName string) ([]acceleratorfit.CountFitResult, bool) {
	st := AttemptFrom(ctx)
	if st == nil {
		return nil, false
	}
	rep, ok := st.Report(nodeName)
	if !ok {
		return nil, false
	}
	cr, ok := checkFromReport(rep, placementchecks.CheckAccelerator)
	if !ok || cr.InputValue == nil {
		return nil, false
	}
	counts, ok := cr.InputValue["count_results"].([]acceleratorfit.CountFitResult)
	return counts, ok
}

// CachedPolicyParts returns policy part results from check 7 when present.
func CachedPolicyParts(ctx context.Context, nodeName string) ([]acceleratorfit.PolicyPartResult, bool) {
	st := AttemptFrom(ctx)
	if st == nil {
		return nil, false
	}
	rep, ok := st.Report(nodeName)
	if !ok {
		return nil, false
	}
	cr, ok := checkFromReport(rep, placementchecks.CheckPolicy)
	if !ok || cr.InputValue == nil {
		return nil, false
	}
	parts, ok := cr.InputValue["parts"].([]acceleratorfit.PolicyPartResult)
	return parts, ok
}

// FindPolicyPart returns the part for a legacy plugin family (taint|node_name|node_selector).
func FindPolicyPart(parts []acceleratorfit.PolicyPartResult, family string) (acceleratorfit.PolicyPartResult, bool) {
	var codes []string
	switch family {
	case "taint":
		codes = []string{"taint_ok", "taint_untolerated", "missing_pod_or_node"}
	case "node_name":
		codes = []string{"node_name_ok", "node_name_mismatch", "missing_pod_or_node"}
	case "node_selector":
		codes = []string{"node_selector_ok", "node_selector_absent", "node_selector_mismatch", "missing_pod_or_node"}
	default:
		return acceleratorfit.PolicyPartResult{}, false
	}
	for _, p := range parts {
		for _, c := range codes {
			if p.Code == c {
				return p, true
			}
		}
	}
	return acceleratorfit.PolicyPartResult{}, false
}
