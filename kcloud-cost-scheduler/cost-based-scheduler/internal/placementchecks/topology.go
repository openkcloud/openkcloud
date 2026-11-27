package placementchecks

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
)

// Topology / gang annotations (check 4). Distinct from Permit GangScheduling wait.
const (
	AnnotationTopologyRequire = "sched.ai/topology-require"
	AnnotationGangSize        = "sched.ai/gang-size" // shared with Permit plugin
	NodeLabelTopologyZone     = "topology.kubernetes.io/zone"
	NodeLabelHostname         = "kubernetes.io/hostname"
)

// Supported topology-require keys for P5. Anything else is recorded as unsupported.
var supportedTopologyKeys = map[string]struct{}{
	"zone":     {},
	"hostname": {},
}

// TopologyRequest is the attempt-scoped topology / gang view.
type TopologyRequest struct {
	RequirePresent bool
	Zone           string
	Hostname       string
	GangSize       int
	GangSizeRaw    string
	GangSizeErr    string
	Unsupported    []string
	EmptyRequire   bool     // key present, whitespace-only or only empty commas
	EmptyValues    []string // supported keys with empty values (zone=)
	Malformed      []string
	Conflict       string // conflicting duplicate supported key
}

// EvaluateTopology is check 4: zone/hostname label match.
// Gang-size alone does not imply same-node packing (UNEVALUATED until colocation
// and member occupancy are explicit). PASS ≠ Permit atomic co-scheduling.
func EvaluateTopology(pod *v1.Pod, node *v1.Node, req ParsedRequest, res NodeResourceView, cfg CheckConfig, now time.Time) CheckResult {
	start := time.Now()
	cr := CheckResult{
		Check:       CheckTopology,
		Mode:        cfg.Mode,
		EvaluatedAt: now.UTC(),
		InputValue: map[string]any{
			"atomic_placement_guaranteed": false,
			"permit_gang_wait":            false,
		},
		Threshold: map[string]any{},
	}
	if cfg.Mode == ModeOff {
		cr.Result = ResultUNEVALUATED
		cr.Code = "check_disabled"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if pod == nil {
		cr.Result = ResultUNEVALUATED
		cr.Code = "missing_pod"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if node != nil {
		cr.InputValue["node"] = node.Name
	}

	tr := req.Topology
	cr.InputValue["zone_required"] = tr.Zone
	cr.InputValue["hostname_required"] = tr.Hostname
	cr.InputValue["gang_size"] = tr.GangSize
	cr.InputValue["unsupported_keys"] = tr.Unsupported

	if tr.EmptyRequire {
		cr.Result = ResultUNEVALUATED
		cr.Code = "empty_requirement"
		cr.Detail = "sched.ai/topology-require present but no interpretable constraint"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if len(tr.EmptyValues) > 0 {
		cr.Result = ResultUNEVALUATED
		cr.Code = "empty_topology_value"
		cr.Detail = fmt.Sprintf("empty values for keys %v", tr.EmptyValues)
		cr.InputValue["empty_values"] = tr.EmptyValues
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if tr.Conflict != "" {
		cr.Result = ResultUNEVALUATED
		cr.Code = "topology_require_conflict"
		cr.Detail = tr.Conflict
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if len(tr.Malformed) > 0 {
		cr.Result = ResultUNEVALUATED
		cr.Code = "topology_require_invalid"
		cr.Detail = fmt.Sprintf("malformed fragments: %v", tr.Malformed)
		cr.InputValue["malformed"] = tr.Malformed
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if tr.GangSizeErr != "" {
		cr.Result = ResultUNEVALUATED
		cr.Code = "gang_size_invalid"
		cr.Detail = tr.GangSizeErr
		cr.InputValue["gang_size_raw"] = tr.GangSizeRaw
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	needTopo := tr.Zone != "" || tr.Hostname != ""
	needGang := tr.GangSize > 1
	if !needTopo && !needGang && len(tr.Unsupported) == 0 {
		cr.Result = ResultPASS
		cr.Code = "topology_not_required"
		cr.Detail = "no topology/gang constraint"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if node == nil && (needTopo || needGang || len(tr.Unsupported) > 0) {
		cr.Result = ResultUNEVALUATED
		cr.Code = "missing_node"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	var firstReject, firstUneval *CheckResult

	// Known zone/hostname judgments first — must not be hidden by unsupported keys.
	if tr.Zone != "" {
		got := ""
		ok := false
		if node.Labels != nil {
			got, ok = node.Labels[NodeLabelTopologyZone]
		}
		cr.InputValue["node_zone"] = got
		if !ok || strings.TrimSpace(got) == "" {
			u := cr
			u.Result = ResultUNEVALUATED
			u.Code = "node_zone_missing"
			u.Detail = "node missing topology.kubernetes.io/zone"
			firstUneval = &u
		} else if got != tr.Zone {
			rej := cr
			rej.Result = ResultREJECT
			rej.Code = "zone_mismatch"
			rej.Detail = fmt.Sprintf("required zone %s, node has %s", tr.Zone, got)
			rej.Enforced = cfg.Mode == ModeEnforce
			firstReject = &rej
		}
	}

	if tr.Hostname != "" && firstReject == nil {
		got := ""
		if node.Labels != nil {
			got = strings.TrimSpace(node.Labels[NodeLabelHostname])
		}
		cr.InputValue["node_hostname"] = got
		cr.InputValue["hostname_source"] = NodeLabelHostname
		if got == "" {
			u := cr
			u.Result = ResultUNEVALUATED
			u.Code = "node_hostname_missing"
			u.Detail = "kubernetes.io/hostname label missing; Node.Name is not a substitute"
			if firstUneval == nil {
				firstUneval = &u
			}
		} else if got != tr.Hostname {
			rej := cr
			rej.Result = ResultREJECT
			rej.Code = "hostname_mismatch"
			rej.Detail = fmt.Sprintf("required hostname %s, label has %s", tr.Hostname, got)
			rej.Enforced = cfg.Mode == ModeEnforce
			firstReject = &rej
		}
	}

	if len(tr.Unsupported) > 0 && firstReject == nil {
		u := cr
		u.Result = ResultUNEVALUATED
		u.Code = "topology_unsupported"
		u.Detail = fmt.Sprintf("unsupported topology keys %v", tr.Unsupported)
		if firstUneval == nil {
			firstUneval = &u
		} else {
			// Preserve unsupported alongside earlier uneval (e.g. missing zone).
			cr.InputValue["also_unsupported"] = tr.Unsupported
		}
	}

	if needGang && firstReject == nil {
		// gang-size alone ≠ same-node requirement (matches Permit semantics).
		// Without explicit colocation + member occupancy, do not REJECT/PASS on capacity.
		gangNote := noteGangCapacity(req, res, tr.GangSize)
		cr.InputValue["gang"] = gangNote
		u := cr
		u.Result = ResultUNEVALUATED
		u.Code = "gang_placement_unevaluated"
		u.Detail = "gang-size does not imply same-node packing; member occupancy unknown — not used to exclude"
		if firstUneval == nil {
			firstUneval = &u
		}
	}

	switch {
	case firstReject != nil:
		cr.Result = firstReject.Result
		cr.Code = firstReject.Code
		cr.Detail = firstReject.Detail
		cr.Enforced = firstReject.Enforced
	case firstUneval != nil:
		cr.Result = firstUneval.Result
		cr.Code = firstUneval.Code
		cr.Detail = firstUneval.Detail
	default:
		cr.Result = ResultPASS
		cr.Code = "topology_match"
		cr.Detail = "zone/hostname match; not an atomic multi-pod guarantee"
	}
	cr.DurationNS = time.Since(start).Nanoseconds()
	return cr
}

// noteGangCapacity records optional arithmetic for diagnostics only (never drives REJECT).
func noteGangCapacity(req ParsedRequest, res NodeResourceView, gangSize int) map[string]any {
	gang := map[string]any{
		"size":                        gangSize,
		"atomic_placement_guaranteed": false,
		"same_node_required":          false,
		"decision":                    "reference_only",
	}
	if res.Allocatable == nil {
		gang["note"] = "no allocatable view"
		return gang
	}
	var resName string
	var perPod int64
	switch {
	case req.GPUCount > 0 && req.NPUCount == 0:
		resName, perPod = "nvidia.com/gpu", req.GPUCount
	case req.NPUCount > 0 && req.GPUCount == 0:
		resName, perPod = "furiosa.ai/rngd", req.NPUCount
	default:
		gang["note"] = "accelerator request ambiguous"
		return gang
	}
	need, ok := checkedMulInt64(perPod, int64(gangSize))
	if !ok {
		gang["note"] = "overflow"
		return gang
	}
	used := int64(0)
	if res.AllocatedScalar != nil {
		used = res.AllocatedScalar[resName]
	}
	alloc := int64(0)
	if q, ok := res.Allocatable[v1.ResourceName(resName)]; ok {
		alloc = q.Value()
	}
	gang["resource"] = resName
	gang["per_pod"] = perPod
	gang["if_colocated_need"] = need
	gang["available"] = alloc - used
	return gang
}

func checkedMulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > 0 && b > 0 && a > (1<<63-1)/b {
		return 0, false
	}
	if a < 0 || b < 0 {
		return 0, false
	}
	return a * b, true
}

func parseTopologyRequest(ann map[string]string) TopologyRequest {
	tr := TopologyRequest{}
	if ann == nil {
		return tr
	}
	if raw, ok := ann[AnnotationTopologyRequire]; ok {
		tr.RequirePresent = true
		if strings.TrimSpace(raw) == "" {
			tr.EmptyRequire = true
		} else {
			seen := map[string]string{}
			anyFragment := false
			for _, part := range strings.Split(raw, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				anyFragment = true
				key, val, cut := strings.Cut(part, "=")
				key = strings.TrimSpace(key)
				val = strings.TrimSpace(val)
				if !cut || key == "" {
					tr.Malformed = append(tr.Malformed, part)
					continue
				}
				if _, supp := supportedTopologyKeys[key]; !supp {
					tr.Unsupported = append(tr.Unsupported, key)
					continue
				}
				if val == "" {
					tr.EmptyValues = append(tr.EmptyValues, key)
					continue
				}
				if prev, exists := seen[key]; exists && prev != val {
					tr.Conflict = fmt.Sprintf("conflicting values for %s: %q vs %q", key, prev, val)
					continue
				}
				seen[key] = val
				switch key {
				case "zone":
					tr.Zone = val
				case "hostname":
					tr.Hostname = val
				}
			}
			if !anyFragment && tr.Conflict == "" && len(tr.Malformed) == 0 {
				tr.EmptyRequire = true
			}
		}
	}
	if raw, ok := ann[AnnotationGangSize]; ok {
		tr.GangSizeRaw = raw
		s := strings.TrimSpace(raw)
		if s == "" {
			tr.GangSizeErr = "empty_gang_size"
		} else {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				tr.GangSizeErr = fmt.Sprintf("invalid gang-size %q", raw)
			} else {
				tr.GangSize = n
			}
		}
	}
	return tr
}
