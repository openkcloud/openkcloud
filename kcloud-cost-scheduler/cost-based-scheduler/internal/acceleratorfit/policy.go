package acceleratorfit

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
)

// PolicyRequireAnnotation lists required node labels as key=value pairs (comma-separated).
// Only keys under keti.re.kr/ are enforced by EvaluateKetiPolicy.
const PolicyRequireAnnotation = "sched.ai/policy-require"

type PolicyPartResult struct {
	OK     bool
	Code   string
	Detail string
	Input  map[string]any
}

func EvaluateNodeName(pod *v1.Pod, node *v1.Node) PolicyPartResult {
	r := PolicyPartResult{OK: true, Code: "node_name_ok", Input: map[string]any{}}
	if pod == nil || node == nil {
		r.OK = false
		r.Code = "missing_pod_or_node"
		return r
	}
	r.Input["pod_node_name"] = pod.Spec.NodeName
	r.Input["node"] = node.Name
	if pod.Spec.NodeName != "" && pod.Spec.NodeName != node.Name {
		r.OK = false
		r.Code = "node_name_mismatch"
		r.Detail = fmt.Sprintf("pod requires node %s but got %s", pod.Spec.NodeName, node.Name)
	}
	return r
}

func EvaluateNodeSelector(pod *v1.Pod, node *v1.Node) PolicyPartResult {
	r := PolicyPartResult{OK: true, Code: "node_selector_ok", Input: map[string]any{}}
	if pod == nil || node == nil {
		r.OK = false
		r.Code = "missing_pod_or_node"
		return r
	}
	if len(pod.Spec.NodeSelector) == 0 {
		r.Code = "node_selector_absent"
		return r
	}
	r.Input["node_selector"] = pod.Spec.NodeSelector
	for key, val := range pod.Spec.NodeSelector {
		nodeVal, ok := node.Labels[key]
		if !ok || nodeVal != val {
			r.OK = false
			r.Code = "node_selector_mismatch"
			r.Detail = fmt.Sprintf("node %s does not match nodeSelector %s=%s", node.Name, key, val)
			r.Input["failed_key"] = key
			r.Input["wanted"] = val
			r.Input["got"] = nodeVal
			return r
		}
	}
	return r
}

func EvaluateTaints(pod *v1.Pod, node *v1.Node) PolicyPartResult {
	r := PolicyPartResult{OK: true, Code: "taint_ok", Input: map[string]any{}}
	if pod == nil || node == nil {
		r.OK = false
		r.Code = "missing_pod_or_node"
		return r
	}
	for _, taint := range node.Spec.Taints {
		if taint.Effect == v1.TaintEffectPreferNoSchedule {
			continue
		}
		if !tolerationsTolerateTaint(pod.Spec.Tolerations, taint) {
			r.OK = false
			r.Code = "taint_untolerated"
			r.Detail = fmt.Sprintf("node %s has taint %s=%s:%s that pod does not tolerate",
				node.Name, taint.Key, taint.Value, taint.Effect)
			r.Input["taint_key"] = taint.Key
			r.Input["taint_value"] = taint.Value
			r.Input["taint_effect"] = string(taint.Effect)
			return r
		}
	}
	return r
}

func tolerationsTolerateTaint(tolerations []v1.Toleration, taint v1.Taint) bool {
	for _, toleration := range tolerations {
		if tolerationTolerateTaint(toleration, taint) {
			return true
		}
	}
	return false
}

func tolerationTolerateTaint(toleration v1.Toleration, taint v1.Taint) bool {
	if toleration.Effect != "" && toleration.Effect != taint.Effect {
		return false
	}
	if toleration.Operator == v1.TolerationOpExists {
		return toleration.Key == "" || toleration.Key == taint.Key
	}
	return toleration.Key == taint.Key && toleration.Value == taint.Value
}

// EvaluateKetiPolicy checks sched.ai/policy-require pairs whose keys are under keti.re.kr/.
// No annotation → not applicable (OK with code policy_not_required).
// Malformed fragments or conflicting duplicate keys → not OK (never silent PASS).
func EvaluateKetiPolicy(pod *v1.Pod, node *v1.Node) PolicyPartResult {
	r := PolicyPartResult{OK: true, Code: "policy_not_required", Input: map[string]any{}}
	if pod == nil || node == nil {
		r.OK = false
		r.Code = "missing_pod_or_node"
		return r
	}
	raw := ""
	if pod.Annotations != nil {
		raw = strings.TrimSpace(pod.Annotations[PolicyRequireAnnotation])
	}
	if raw == "" {
		return r
	}
	r.Input["policy_require_raw"] = raw
	parsed := parsePolicyRequire(raw)
	r.Input["requirements"] = parsed.Requirements
	if !parsed.OK {
		r.OK = false
		r.Code = parsed.Code
		r.Detail = parsed.Detail
		return r
	}
	if len(parsed.Requirements) == 0 {
		r.OK = false
		r.Code = "policy_require_invalid"
		r.Detail = "policy-require annotation present but no keti.re.kr/ key=value pairs"
		return r
	}
	for key, want := range parsed.Requirements {
		got, ok := node.Labels[key]
		if !ok || got != want {
			r.OK = false
			r.Code = "policy_label_mismatch"
			r.Detail = fmt.Sprintf("node missing required policy label %s=%s", key, want)
			r.Input["failed_key"] = key
			r.Input["wanted"] = want
			r.Input["got"] = got
			return r
		}
	}
	r.Code = "policy_ok"
	return r
}

type policyParseResult struct {
	Requirements map[string]string
	OK           bool
	Code         string
	Detail       string
}

func parsePolicyRequire(raw string) policyParseResult {
	out := policyParseResult{OK: true, Requirements: map[string]string{}}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if !ok || key == "" {
			out.OK = false
			out.Code = "policy_require_invalid"
			out.Detail = fmt.Sprintf("malformed policy fragment %q (want key=value)", part)
			return out
		}
		if !strings.HasPrefix(key, "keti.re.kr/") {
			out.OK = false
			out.Code = "policy_require_invalid"
			out.Detail = fmt.Sprintf("policy key %q must use keti.re.kr/ prefix", key)
			return out
		}
		if prev, exists := out.Requirements[key]; exists && prev != val {
			out.OK = false
			out.Code = "policy_require_conflict"
			out.Detail = fmt.Sprintf("conflicting values for %s: %q vs %q", key, prev, val)
			return out
		}
		out.Requirements[key] = val
	}
	return out
}

// IsLegacyPolicyCode reports rejection codes that mirror existing Filter plugins
// (TaintToleration / NodeSelector). Annotation policy-require is not legacy.
func IsLegacyPolicyCode(code string) bool {
	switch code {
	case "taint_untolerated", "node_name_mismatch", "node_selector_mismatch":
		return true
	default:
		return false
	}
}
