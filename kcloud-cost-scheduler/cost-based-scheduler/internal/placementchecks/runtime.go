package placementchecks

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
)

// Pod annotations: minimum versions the workload requires (no defaults).
const (
	AnnotationMinCUDADriver  = "sched.ai/min-cuda-driver"
	AnnotationMinCUDARuntime = "sched.ai/min-cuda-runtime"
)

// Node labels — current GFD (driver-version / runtime-version.*) then legacy cuda.driver.* / cuda.runtime.*.
// runtime-version.* is the CUDA version the installed driver supports (NVML GetCudaDriverVersion),
// not proof that a container image's CUDA libraries or Toolkit/RuntimeClass will run.
const (
	NodeLabelCUDADriverFull         = "nvidia.com/cuda.driver-version.full"
	NodeLabelCUDADriverVersionMajor = "nvidia.com/cuda.driver-version.major"
	NodeLabelCUDADriverVersionMinor = "nvidia.com/cuda.driver-version.minor"
	NodeLabelCUDADriverVersionRev   = "nvidia.com/cuda.driver-version.revision"

	NodeLabelCUDARuntimeFull         = "nvidia.com/cuda.runtime-version.full"
	NodeLabelCUDARuntimeVersionMajor = "nvidia.com/cuda.runtime-version.major"
	NodeLabelCUDARuntimeVersionMinor = "nvidia.com/cuda.runtime-version.minor"

	// Legacy GFD keys (still emitted alongside current keys).
	NodeLabelCUDADriverMajor  = "nvidia.com/cuda.driver.major"
	NodeLabelCUDADriverMinor  = "nvidia.com/cuda.driver.minor"
	NodeLabelCUDADriverRev    = "nvidia.com/cuda.driver.rev"
	NodeLabelCUDARuntimeMajor = "nvidia.com/cuda.runtime.major"
	NodeLabelCUDARuntimeMinor = "nvidia.com/cuda.runtime.minor"
)

// VersionCompareMethod is recorded on every evaluated runtime check.
const VersionCompareMethod = "gte_numeric_dotted"

// RuntimeRequirement is one parsed min-version constraint from the Pod.
type RuntimeRequirement struct {
	Kind     string // cuda_driver | cuda_runtime (driver-supported CUDA level)
	Raw      string
	Parts    []int
	ParseErr string
}

type nodeVersionRead struct {
	Value      string
	Source     string
	Incomplete bool // some component labels present but set incomplete → UNEVALUATED
}

// EvaluateRuntime is check 3: Pod min driver / driver-supported CUDA vs node GFD labels.
// Scope: node driver release and driver-supported CUDA level only — not container CUDA,
// Toolkit, or RuntimeClass. Missing key → PASS; explicit empty / bad / incomplete → UNEVALUATED.
func EvaluateRuntime(pod *v1.Pod, node *v1.Node, req ParsedRequest, cfg CheckConfig, now time.Time) CheckResult {
	start := time.Now()
	cr := CheckResult{
		Check:       CheckRuntime,
		Mode:        cfg.Mode,
		EvaluatedAt: now.UTC(),
		InputValue: map[string]any{
			"scope":                       "node_driver_and_driver_supported_cuda",
			"container_runtime_verified":  false,
			"compare":                     VersionCompareMethod,
		},
		Threshold: map[string]any{
			"compare": VersionCompareMethod,
			"scope":   "node_driver_and_driver_supported_cuda",
		},
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

	reqs := req.RuntimeRequirements
	if len(reqs) == 0 {
		cr.Result = ResultPASS
		cr.Code = "runtime_not_required"
		cr.Detail = "no sched.ai/min-cuda-driver|min-cuda-runtime annotation key"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if node == nil {
		cr.Result = ResultUNEVALUATED
		cr.Code = "missing_node"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	nodeDriver := readNodeCUDADriverVersion(node)
	nodeRuntime := readNodeCUDARuntimeVersion(node)
	cr.InputValue["node_cuda_driver"] = nodeDriver.Value
	cr.InputValue["node_cuda_driver_source"] = nodeDriver.Source
	cr.InputValue["node_cuda_driver_incomplete"] = nodeDriver.Incomplete
	cr.InputValue["node_supported_cuda"] = nodeRuntime.Value
	cr.InputValue["node_supported_cuda_source"] = nodeRuntime.Source
	cr.InputValue["node_supported_cuda_incomplete"] = nodeRuntime.Incomplete

	var parts []map[string]any
	var firstReject, firstUneval *CheckResult

	for _, r := range reqs {
		part := map[string]any{
			"kind": r.Kind, "required_raw": r.Raw, "compare": VersionCompareMethod,
		}
		if r.ParseErr != "" {
			code := "version_parse_failed"
			if r.ParseErr == "empty_requirement" {
				code = "empty_requirement"
			}
			part["code"] = code
			part["detail"] = r.ParseErr
			parts = append(parts, part)
			if firstUneval == nil {
				u := cr
				u.Result = ResultUNEVALUATED
				u.Code = code
				u.Detail = r.ParseErr
				firstUneval = &u
			}
			continue
		}
		part["required_parts"] = r.Parts

		var nv nodeVersionRead
		switch r.Kind {
		case "cuda_driver":
			nv = nodeDriver
		case "cuda_runtime":
			nv = nodeRuntime
			part["means"] = "driver_supported_cuda_version"
		default:
			part["code"] = "unknown_runtime_kind"
			parts = append(parts, part)
			if firstUneval == nil {
				u := cr
				u.Result = ResultUNEVALUATED
				u.Code = "unknown_runtime_kind"
				u.Detail = r.Kind
				firstUneval = &u
			}
			continue
		}
		part["node_raw"] = nv.Value
		part["node_source"] = nv.Source
		if nv.Incomplete {
			part["code"] = "node_version_incomplete"
			parts = append(parts, part)
			if firstUneval == nil {
				u := cr
				u.Result = ResultUNEVALUATED
				u.Code = "node_version_incomplete"
				u.Detail = fmt.Sprintf("incomplete %s component labels (%s)", r.Kind, nv.Source)
				firstUneval = &u
			}
			continue
		}
		if strings.TrimSpace(nv.Value) == "" {
			part["code"] = "node_version_missing"
			parts = append(parts, part)
			if firstUneval == nil {
				u := cr
				u.Result = ResultUNEVALUATED
				u.Code = "node_version_missing"
				u.Detail = fmt.Sprintf("node has no confirmed %s label", r.Kind)
				firstUneval = &u
			}
			continue
		}
		haveParts, err := parseDottedVersion(nv.Value)
		if err != nil {
			part["code"] = "node_version_parse_failed"
			part["detail"] = err.Error()
			parts = append(parts, part)
			if firstUneval == nil {
				u := cr
				u.Result = ResultUNEVALUATED
				u.Code = "node_version_parse_failed"
				u.Detail = err.Error()
				firstUneval = &u
			}
			continue
		}
		part["node_parts"] = haveParts
		if versionGTE(haveParts, r.Parts) {
			part["code"] = "runtime_compatible"
			part["ok"] = true
			parts = append(parts, part)
			continue
		}
		part["code"] = "runtime_incompatible"
		part["ok"] = false
		parts = append(parts, part)
		if firstReject == nil {
			rej := cr
			rej.Result = ResultREJECT
			rej.Code = "runtime_incompatible"
			rej.Detail = fmt.Sprintf("%s node %s < required %s (%s); container runtime not verified",
				r.Kind, nv.Value, r.Raw, VersionCompareMethod)
			rej.Enforced = cfg.Mode == ModeEnforce
			firstReject = &rej
		}
	}

	cr.InputValue["parts"] = parts
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
		cr.Code = "runtime_compatible"
		cr.Detail = "node driver/supported-CUDA meets mins; container CUDA/Toolkit/RuntimeClass not verified"
	}
	cr.DurationNS = time.Since(start).Nanoseconds()
	return cr
}

func parseRuntimeRequirements(ann map[string]string) []RuntimeRequirement {
	if ann == nil {
		return nil
	}
	var out []RuntimeRequirement
	if raw, ok := ann[AnnotationMinCUDADriver]; ok {
		out = append(out, requirementFromAnnotation("cuda_driver", raw))
	}
	if raw, ok := ann[AnnotationMinCUDARuntime]; ok {
		out = append(out, requirementFromAnnotation("cuda_runtime", raw))
	}
	return out
}

func requirementFromAnnotation(kind, raw string) RuntimeRequirement {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return RuntimeRequirement{Kind: kind, Raw: raw, ParseErr: "empty_requirement"}
	}
	return makeRuntimeRequirement(kind, trimmed)
}

func makeRuntimeRequirement(kind, raw string) RuntimeRequirement {
	r := RuntimeRequirement{Kind: kind, Raw: raw}
	parts, err := parseDottedVersion(raw)
	if err != nil {
		r.ParseErr = err.Error()
		return r
	}
	r.Parts = parts
	return r
}

// parseDottedVersion accepts "535.104.05", "12.2", "550" — non-negative integer components.
// Leading '-' / non-numeric tokens are errors (not stripped into a valid version).
func parseDottedVersion(raw string) ([]int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("empty version")
	}
	lower := strings.ToLower(s)
	// Optional cosmetic prefixes only; never strip a leading minus into a positive version.
	if strings.HasPrefix(lower, "v") && len(lower) > 1 && lower[1] >= '0' && lower[1] <= '9' {
		s = s[1:]
		lower = strings.ToLower(s)
	}
	if strings.HasPrefix(lower, "cuda") {
		rest := strings.TrimSpace(s[4:])
		if rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			s = rest
		}
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("empty component in %q", raw)
		}
		if strings.HasPrefix(p, "-") || strings.HasPrefix(p, "+") {
			return nil, fmt.Errorf("signed component %q in %q", p, raw)
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("non-numeric component %q in %q", p, raw)
		}
		out = append(out, int(n))
	}
	return out, nil
}

// versionGTE reports whether have >= need using numeric dotted comparison
// (shorter explicit versions pad with 0 — only for fully observed version strings).
func versionGTE(have, need []int) bool {
	n := len(have)
	if len(need) > n {
		n = len(need)
	}
	for i := 0; i < n; i++ {
		h, nd := 0, 0
		if i < len(have) {
			h = have[i]
		}
		if i < len(need) {
			nd = need[i]
		}
		if h > nd {
			return true
		}
		if h < nd {
			return false
		}
	}
	return true
}

func readNodeCUDADriverVersion(node *v1.Node) nodeVersionRead {
	if node == nil || node.Labels == nil {
		return nodeVersionRead{}
	}
	labels := node.Labels
	if v := strings.TrimSpace(labels[NodeLabelCUDADriverFull]); v != "" {
		return nodeVersionRead{Value: v, Source: NodeLabelCUDADriverFull}
	}
	if r, ok := composeComponents(labels,
		[]string{NodeLabelCUDADriverVersionMajor, NodeLabelCUDADriverVersionMinor, NodeLabelCUDADriverVersionRev},
		"nvidia.com/cuda.driver-version.{major,minor,revision}",
	); ok {
		return r
	}
	if r, ok := composeComponents(labels,
		[]string{NodeLabelCUDADriverMajor, NodeLabelCUDADriverMinor, NodeLabelCUDADriverRev},
		"nvidia.com/cuda.driver.{major,minor,rev}",
	); ok {
		return r
	}
	return nodeVersionRead{}
}

func readNodeCUDARuntimeVersion(node *v1.Node) nodeVersionRead {
	if node == nil || node.Labels == nil {
		return nodeVersionRead{}
	}
	labels := node.Labels
	if v := strings.TrimSpace(labels[NodeLabelCUDARuntimeFull]); v != "" {
		return nodeVersionRead{Value: v, Source: NodeLabelCUDARuntimeFull}
	}
	if r, ok := composeComponents(labels,
		[]string{NodeLabelCUDARuntimeVersionMajor, NodeLabelCUDARuntimeVersionMinor},
		"nvidia.com/cuda.runtime-version.{major,minor}",
	); ok {
		return r
	}
	if r, ok := composeComponents(labels,
		[]string{NodeLabelCUDARuntimeMajor, NodeLabelCUDARuntimeMinor},
		"nvidia.com/cuda.runtime.{major,minor}",
	); ok {
		return r
	}
	return nodeVersionRead{}
}

// composeComponents requires every key in keys to be non-empty when any is present.
// Partial sets → Incomplete=true (caller UNEVALUATED). All empty → not ok (try next scheme).
func composeComponents(labels map[string]string, keys []string, source string) (nodeVersionRead, bool) {
	vals := make([]string, len(keys))
	any, all := false, true
	for i, k := range keys {
		v := strings.TrimSpace(labels[k])
		vals[i] = v
		if v != "" {
			any = true
		} else {
			all = false
		}
	}
	if !any {
		return nodeVersionRead{}, false
	}
	if !all {
		return nodeVersionRead{Incomplete: true, Source: source}, true
	}
	return nodeVersionRead{Value: strings.Join(vals, "."), Source: source}, true
}
