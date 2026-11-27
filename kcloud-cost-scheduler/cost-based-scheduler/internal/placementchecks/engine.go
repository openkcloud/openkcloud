package placementchecks

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

// Engine runs required checks against an immutable snapshot (no I/O).
type Engine struct {
	Config Config
}

// NewEngine builds an engine from a validated config.
func NewEngine(cfg Config) *Engine {
	return &Engine{Config: cfg}
}

// EvalInput is everything needed for one node evaluation in an attempt.
type EvalInput struct {
	Meta     AttemptMeta
	Pod      *v1.Pod
	NodeName string
	Node     *v1.Node // may be nil when only telemetry exists (then 1/7 UNEVALUATED)
	Snap     Snapshot
	Resources NodeResourceView
	Now      time.Time
}

// EvaluateNode runs checks 1–7 for one node.
// Checks 1 and 7 share acceleratorfit helpers with Filter plugins.
// Checks 3–5 are local (runtime / topology / thermal-power).
func (e *Engine) EvaluateNode(in EvalInput) CandidateReport {
	report := CandidateReport{NodeName: in.NodeName, Checks: make([]CheckResult, 0, 7)}
	nodeTel, ok := in.Snap.Nodes[in.NodeName]
	if !ok {
		nodeTel = NodeTelemetry{NodeName: in.NodeName}
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	freshCfg := e.Config.Checks[CheckFreshness]
	memCfg := e.Config.Checks[CheckMemory]
	rtCfg := e.Config.Checks[CheckRuntime]
	topoCfg := e.Config.Checks[CheckTopology]
	thermCfg := e.Config.Checks[CheckThermalPower]
	req := ParseRequest(in.Pod)

	var accelCR, policyCR CheckResult

	for _, id := range AllCheckIDs {
		cc := e.Config.Checks[id]
		var cr CheckResult
		switch id {
		case CheckAccelerator:
			cr = EvaluateAccelerator(in.Pod, in.Node, in.Resources, cc, now)
			accelCR = cr
		case CheckMemory:
			cr = EvaluateMemory(nodeTel, req, memCfg, freshCfg, now)
		case CheckRuntime:
			cr = EvaluateRuntime(in.Pod, in.Node, req, rtCfg, now)
		case CheckTopology:
			cr = EvaluateTopology(in.Pod, in.Node, req, in.Resources, topoCfg, now)
		case CheckThermalPower:
			cr = EvaluateThermalPower(nodeTel, thermCfg, freshCfg, now)
		case CheckFreshness:
			cr = EvaluateFreshness(nodeTel, cc, now)
		case CheckPolicy:
			cr = EvaluatePolicy(in.Pod, in.Node, cc, now)
			policyCR = cr
		default:
			cr = unimplementedCheck(id, cc, now)
		}
		report.Checks = append(report.Checks, cr)
		if cr.Enforced && cr.Result == ResultREJECT {
			report.RejectionReasons = append(report.RejectionReasons, RejectionReason{
				Stage:  "required_check",
				Check:  string(id),
				Code:   cr.Code,
				Detail: cr.Detail,
			})
		}
	}

	// Always preserve legacy-equivalent reject reasons (even in record mode).
	AppendLegacyRejectionReasons(&report, accelCR, policyCR)

	if len(report.RejectionReasons) > 0 {
		// IsFiltered for required-check enforce path only when an Enforced REJECT exists.
		for _, cr := range report.Checks {
			if cr.Enforced && cr.Result == ResultREJECT {
				report.IsFiltered = true
				break
			}
		}
		if report.Representative == nil {
			r := report.RejectionReasons[0]
			report.Representative = &r
		}
	}
	_ = in.Meta
	return report
}

func unimplementedCheck(id CheckID, cfg CheckConfig, now time.Time) CheckResult {
	if cfg.Mode == ModeOff {
		return CheckResult{
			Check: id, Result: ResultUNEVALUATED, Code: "check_disabled",
			Mode: cfg.Mode, EvaluatedAt: now.UTC(),
		}
	}
	return CheckResult{
		Check: id, Result: ResultUNEVALUATED, Code: "not_implemented",
		Detail: "planned for a later phase", Mode: cfg.Mode, EvaluatedAt: now.UTC(),
	}
}

// ShouldExclude reports whether required-check enforce excluded the node.
func ShouldExclude(report CandidateReport) bool {
	return report.IsFiltered
}
