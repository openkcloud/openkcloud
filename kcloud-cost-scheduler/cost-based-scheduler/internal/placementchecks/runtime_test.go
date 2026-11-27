package placementchecks

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseDottedVersionAndGTE(t *testing.T) {
	parts, err := parseDottedVersion("535.104.05")
	if err != nil || len(parts) != 3 || parts[2] != 5 {
		t.Fatalf("parts=%v err=%v", parts, err)
	}
	if !versionGTE([]int{535, 104, 5}, []int{535, 104}) {
		t.Fatal("trailing zero pad: have more components must still GTE shorter need")
	}
	if versionGTE([]int{535, 100}, []int{535, 104}) {
		t.Fatal("535.100 must not satisfy min 535.104")
	}
	if !versionGTE([]int{12, 2}, []int{12, 2}) {
		t.Fatal("equal must GTE")
	}
	if _, err := parseDottedVersion("12.2rc1"); err == nil {
		t.Fatal("non-numeric must fail")
	}
	if _, err := parseDottedVersion("-535.104"); err == nil {
		t.Fatal("leading minus must fail, not strip to PASS")
	}
}

func TestEvaluateRuntimeNotRequired(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}}
	cr := EvaluateRuntime(pod, node, ParseRequest(pod), DefaultConfig().Checks[CheckRuntime], now)
	if cr.Result != ResultPASS || cr.Code != "runtime_not_required" {
		t.Fatalf("%+v", cr)
	}
}

func TestEvaluateRuntimeCompatibleAndReject(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	cfg := DefaultConfig().Checks[CheckRuntime]
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{
			AnnotationMinCUDADriver:  "535.104",
			AnnotationMinCUDARuntime: "12.2",
		},
	}}
	okNode := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ok", Labels: map[string]string{
		NodeLabelCUDADriverFull:   "550.54.15",
		NodeLabelCUDARuntimeMajor: "12",
		NodeLabelCUDARuntimeMinor: "4",
	}}}
	cr := EvaluateRuntime(pod, okNode, ParseRequest(pod), cfg, now)
	if cr.Result != ResultPASS || cr.Code != "runtime_compatible" {
		t.Fatalf("compatible: %+v", cr)
	}
	if cr.Threshold["compare"] != VersionCompareMethod {
		t.Fatalf("compare method missing: %+v", cr.Threshold)
	}

	oldNode := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "old", Labels: map[string]string{
		NodeLabelCUDADriverMajor:  "525",
		NodeLabelCUDADriverMinor:  "60",
		NodeLabelCUDADriverRev:    "13",
		NodeLabelCUDARuntimeMajor: "12",
		NodeLabelCUDARuntimeMinor: "0",
	}}}
	cr2 := EvaluateRuntime(pod, oldNode, ParseRequest(pod), cfg, now)
	if cr2.Result != ResultREJECT || cr2.Code != "runtime_incompatible" {
		t.Fatalf("incompatible: %+v", cr2)
	}
	if cr2.Enforced {
		t.Fatal("record mode must not enforce")
	}
}

func TestEvaluateRuntimeMissingNodeLabel(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationMinCUDADriver: "535.0"},
	}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}} // no cuda labels
	cr := EvaluateRuntime(pod, node, ParseRequest(pod), DefaultConfig().Checks[CheckRuntime], now)
	if cr.Result != ResultUNEVALUATED || cr.Code != "node_version_missing" {
		t.Fatalf("%+v", cr)
	}
}

func TestEvaluateRuntimeInvalidRequirement(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationMinCUDADriver: "not-a-version"},
	}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		NodeLabelCUDADriverFull: "550.54.15",
	}}}
	cr := EvaluateRuntime(pod, node, ParseRequest(pod), DefaultConfig().Checks[CheckRuntime], now)
	if cr.Result != ResultUNEVALUATED || cr.Code != "version_parse_failed" {
		t.Fatalf("%+v", cr)
	}
}

func TestEvaluateRuntimeEnforceExcludes(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	c := cfg.Checks[CheckRuntime]
	c.Mode = ModeEnforce
	cfg.Checks[CheckRuntime] = c
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationMinCUDADriver: "600.0"},
	}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		NodeLabelCUDADriverFull: "550.54.15",
	}}}
	rep := NewEngine(cfg).EvaluateNode(EvalInput{Pod: pod, Node: node, NodeName: "n", Now: now})
	var found bool
	for _, cr := range rep.Checks {
		if cr.Check == CheckRuntime {
			found = true
			if cr.Result != ResultREJECT || !cr.Enforced {
				t.Fatalf("%+v", cr)
			}
		}
	}
	if !found || !ShouldExclude(rep) {
		t.Fatalf("enforce must filter: %+v", rep)
	}
}

func TestEvaluateRuntimeComposedDriverLabels(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationMinCUDADriver: "535.104.05"},
	}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		NodeLabelCUDADriverMajor: "535",
		NodeLabelCUDADriverMinor: "104",
		NodeLabelCUDADriverRev:   "05",
	}}}
	cr := EvaluateRuntime(pod, node, ParseRequest(pod), DefaultConfig().Checks[CheckRuntime], now)
	if cr.Result != ResultPASS {
		t.Fatalf("composed labels: %+v", cr)
	}
}

// Formalized from REVIEW_P4.md P4R1–P4R6 (+ controls).
func TestReviewP4VersionBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	cfg := DefaultConfig().Checks[CheckRuntime]
	evaluate := func(ann, labels map[string]string) CheckResult {
		p := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Annotations: ann}}
		n := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: labels}}
		return EvaluateRuntime(p, n, ParseRequest(p), cfg, now)
	}

	t.Run("P4R1_negative_requirement_must_not_PASS", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDADriver: "-535.104"},
			map[string]string{NodeLabelCUDADriverFull: "550.54.15"},
		)
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("negative requirement normalized into valid version: %+v", got)
		}
	})
	t.Run("P4R2_explicit_empty_requirement_is_not_absent", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDADriver: "   "},
			map[string]string{NodeLabelCUDADriverFull: "550.54.15"},
		)
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("present-but-empty requirement silently removed: %+v", got)
		}
		if got.Code != "empty_requirement" {
			t.Fatalf("code=%s want empty_requirement", got.Code)
		}
	})
	t.Run("P4R3_partial_driver_components_are_unknown", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDADriver: "535.104.05"},
			map[string]string{NodeLabelCUDADriverMajor: "535"},
		)
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("missing minor/revision treated as zero: %+v", got)
		}
		if got.Code != "node_version_incomplete" {
			t.Fatalf("code=%s want node_version_incomplete", got.Code)
		}
	})
	t.Run("P4R4_partial_runtime_components_are_unknown", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDARuntime: "12.2"},
			map[string]string{NodeLabelCUDARuntimeMajor: "12"},
		)
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("missing minor treated as zero: %+v", got)
		}
		if got.Code != "node_version_incomplete" {
			t.Fatalf("code=%s want node_version_incomplete", got.Code)
		}
	})
	t.Run("P4R5_current_runtime_full_label", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDARuntime: "12.2"},
			map[string]string{NodeLabelCUDARuntimeFull: "12.4"},
		)
		if got.Result != ResultPASS {
			t.Fatalf("current NVIDIA full label ignored: %+v", got)
		}
	})
	t.Run("P4R6_current_driver_component_labels", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDADriver: "535.104.05"},
			map[string]string{
				NodeLabelCUDADriverVersionMajor: "550",
				NodeLabelCUDADriverVersionMinor: "54",
				NodeLabelCUDADriverVersionRev:   "15",
			},
		)
		if got.Result != ResultPASS {
			t.Fatalf("current NVIDIA component labels ignored: %+v", got)
		}
	})
	t.Run("control_numeric_minor_comparison", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDARuntime: "12.9"},
			map[string]string{NodeLabelCUDARuntimeMajor: "12", NodeLabelCUDARuntimeMinor: "10"},
		)
		if got.Result != ResultPASS {
			t.Fatalf("12.10 must exceed 12.9 numerically: %+v", got)
		}
	})
	t.Run("control_absent_constraint_is_not_required", func(t *testing.T) {
		got := evaluate(nil, nil)
		if got.Result != ResultPASS || got.Code != "runtime_not_required" {
			t.Fatalf("no declared constraint: %+v", got)
		}
	})
	t.Run("scope_does_not_claim_container_verification", func(t *testing.T) {
		got := evaluate(
			map[string]string{AnnotationMinCUDARuntime: "12.2"},
			map[string]string{NodeLabelCUDARuntimeFull: "12.4"},
		)
		if got.InputValue["container_runtime_verified"] != false {
			t.Fatalf("must not claim container runtime verified: %+v", got.InputValue)
		}
		if got.Threshold["scope"] != "node_driver_and_driver_supported_cuda" {
			t.Fatalf("scope=%v", got.Threshold["scope"])
		}
	})
}
