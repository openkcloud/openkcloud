package placementchecks

import (
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"

	v1 "k8s.io/api/core/v1"
)

// EvaluatePolicy is check 7: taint, nodeName, nodeSelector, keti.re.kr policy labels.
func EvaluatePolicy(pod *v1.Pod, node *v1.Node, cfg CheckConfig, now time.Time) CheckResult {
	start := time.Now()
	cr := CheckResult{
		Check:       CheckPolicy,
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

	parts := []acceleratorfit.PolicyPartResult{
		acceleratorfit.EvaluateTaints(pod, node),
		acceleratorfit.EvaluateNodeName(pod, node),
		acceleratorfit.EvaluateNodeSelector(pod, node),
		acceleratorfit.EvaluateKetiPolicy(pod, node),
	}
	cr.InputValue["parts"] = parts

	for _, p := range parts {
		if !p.OK {
			cr.Result = ResultREJECT
			cr.Code = p.Code
			cr.Detail = p.Detail
			cr.Enforced = cfg.Mode == ModeEnforce
			cr.DurationNS = time.Since(start).Nanoseconds()
			return cr
		}
	}
	cr.Result = ResultPASS
	cr.Code = "policy_ok"
	cr.DurationNS = time.Since(start).Nanoseconds()
	return cr
}

// AppendLegacyRejectionReasons records Filter-equivalent failures only
// (taint / nodeName / nodeSelector / accelerator model·count).
// Annotation policy-require rejects stay on CheckResult; they become
// RejectionReasons via the enforce path in Engine, never as legacy_filter.
func AppendLegacyRejectionReasons(report *CandidateReport, accelerator, policy CheckResult) {
	if report == nil {
		return
	}
	if accelerator.Result == ResultREJECT {
		report.RejectionReasons = append(report.RejectionReasons, RejectionReason{
			Stage:  "legacy_filter",
			Check:  string(CheckAccelerator),
			Code:   accelerator.Code,
			Detail: accelerator.Detail,
		})
	}
	if policy.Result == ResultREJECT && acceleratorfit.IsLegacyPolicyCode(policy.Code) {
		report.RejectionReasons = append(report.RejectionReasons, RejectionReason{
			Stage:  "legacy_filter",
			Check:  string(CheckPolicy),
			Code:   policy.Code,
			Detail: policy.Detail,
		})
	}
	if report.Representative == nil && len(report.RejectionReasons) > 0 {
		r := report.RejectionReasons[0]
		report.Representative = &r
	}
}
