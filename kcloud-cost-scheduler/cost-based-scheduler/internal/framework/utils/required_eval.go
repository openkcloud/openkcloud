package utils

import (
	"fmt"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"

	v1 "k8s.io/api/core/v1"
)

// NodeResourceViewFromNodeInfo builds the fit view for check 1 from cache NodeInfo.
func NodeResourceViewFromNodeInfo(ni *NodeInfo) placementchecks.NodeResourceView {
	out := placementchecks.NodeResourceView{AllocatedScalar: map[string]int64{}}
	if ni == nil {
		return out
	}
	if ni.Node() != nil {
		out.Allocatable = ni.Node().Status.Allocatable
	}
	if ni.Requested != nil {
		out.AllocatedMilliCPU = ni.Requested.MilliCPU
		out.AllocatedMemory = ni.Requested.Memory
		for k, v := range ni.Requested.ScalarResources {
			out.AllocatedScalar[string(k)] = v
		}
	}
	return out
}

// EvalInputFromAttempt fills EvalInput from attempt state + node.
func EvalInputFromAttempt(st *AttemptState, pod *v1.Pod, node *v1.Node, nodeName string, res placementchecks.NodeResourceView) placementchecks.EvalInput {
	in := placementchecks.EvalInput{
		Pod: pod, Node: node, NodeName: nodeName, Resources: res,
	}
	if st != nil {
		in.Meta = st.Meta
		in.Snap = st.Snap
		if !st.Meta.StartedAt.IsZero() {
			in.Now = st.Meta.StartedAt
		}
	}
	return in
}

// ApplyRequiredCheckExclusion sets IsFiltered when enforce REJECT exists.
func ApplyRequiredCheckExclusion(pr *PluginResult, rep placementchecks.CandidateReport) {
	if pr == nil {
		return
	}
	repCopy := rep
	pr.RequiredChecks = &repCopy
	if len(rep.RejectionReasons) > 0 {
		pr.RejectionReasons = append(pr.RejectionReasons, rep.RejectionReasons...)
	}
	if placementchecks.ShouldExclude(rep) {
		pr.IsFiltered = true
		detail := "required-check enforce reject"
		if rep.Representative != nil {
			detail = fmt.Sprintf("%s: %s", rep.Representative.Code, rep.Representative.Detail)
		}
		st := NewStatus(Unschedulable, detail)
		st.SetPlugin("RequiredChecks")
		pr.FilteredStatus = *st
	}
}

// LegacyFilterRejection builds a rejection reason for a legacy Filter plugin.
func LegacyFilterRejection(pluginName string, status *Status) placementchecks.RejectionReason {
	code := "legacy_filter"
	detail := ""
	if status != nil {
		detail = status.Message()
		if status.Code() != Success {
			code = status.Code().String()
		}
	}
	return placementchecks.RejectionReason{
		Stage:  "legacy_filter",
		Check:  pluginName,
		Code:   code,
		Detail: detail,
	}
}
