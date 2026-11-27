package placementchecks

import (
	"math"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEvaluateTopologyNotRequired(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}}
	cr := EvaluateTopology(pod, node, ParseRequest(pod), NodeResourceView{}, DefaultConfig().Checks[CheckTopology], now)
	if cr.Result != ResultPASS || cr.Code != "topology_not_required" {
		t.Fatalf("%+v", cr)
	}
	if cr.InputValue["atomic_placement_guaranteed"] != false {
		t.Fatal("must not claim atomic placement")
	}
}

func TestEvaluateTopologyZoneMatchAndMismatch(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig().Checks[CheckTopology]
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		AnnotationTopologyRequire: "zone=rack-a",
	}}}
	ok := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		NodeLabelTopologyZone: "rack-a",
	}}}
	cr := EvaluateTopology(pod, ok, ParseRequest(pod), NodeResourceView{}, cfg, now)
	if cr.Result != ResultPASS {
		t.Fatalf("match: %+v", cr)
	}
	bad := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		NodeLabelTopologyZone: "rack-b",
	}}}
	cr2 := EvaluateTopology(pod, bad, ParseRequest(pod), NodeResourceView{}, cfg, now)
	if cr2.Result != ResultREJECT || cr2.Code != "zone_mismatch" {
		t.Fatalf("mismatch: %+v", cr2)
	}
}

func TestEvaluateTopologyUnsupportedKey(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		AnnotationTopologyRequire: "nvlink=required",
	}}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}}
	cr := EvaluateTopology(pod, node, ParseRequest(pod), NodeResourceView{}, DefaultConfig().Checks[CheckTopology], now)
	if cr.Result != ResultUNEVALUATED || cr.Code != "topology_unsupported" {
		t.Fatalf("%+v", cr)
	}
}

func TestEvaluateTopologyGangSizeAloneIsUnevaluated(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig().Checks[CheckTopology]
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			AnnotationGangSize: "2",
		}},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}}},
	}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}}
	res := NodeResourceView{
		Allocatable:     v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
		AllocatedScalar: map[string]int64{},
	}
	cr := EvaluateTopology(pod, node, ParseRequest(pod), res, cfg, now)
	if cr.Result != ResultUNEVALUATED || cr.Code != "gang_placement_unevaluated" {
		t.Fatalf("gang-size alone must not REJECT/PASS on single-node capacity: %+v", cr)
	}
	if cr.InputValue["atomic_placement_guaranteed"] != false {
		t.Fatal("must not claim atomic placement")
	}
}

func TestEvaluateTopologyEmptyRequire(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		AnnotationTopologyRequire: "  ",
	}}}
	cr := EvaluateTopology(pod, &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}}, ParseRequest(pod), NodeResourceView{}, DefaultConfig().Checks[CheckTopology], now)
	if cr.Code != "empty_requirement" {
		t.Fatalf("%+v", cr)
	}
}

// Formalized from REVIEW_P5.md P5R1–P5R9.
func TestReviewP5Boundaries(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	topoCfg := cfg.Checks[CheckTopology]
	makeNode := func() *v1.Node {
		return &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{
			NodeLabelTopologyZone: "rack-b",
		}}}
	}
	evalTopo := func(raw string, node *v1.Node) CheckResult {
		p := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			AnnotationTopologyRequire: raw,
		}}}
		return EvaluateTopology(p, node, ParseRequest(p), NodeResourceView{}, topoCfg, now)
	}

	t.Run("P5R1_empty_topology_value_is_not_no_constraint", func(t *testing.T) {
		for _, raw := range []string{"zone=", "hostname=", ",,,"} {
			got := evalTopo(raw, makeNode())
			if got.Result != ResultUNEVALUATED {
				t.Errorf("%q: got %+v; want UNEVALUATED", raw, got)
			}
		}
	})
	t.Run("P5R2_conflicting_topology_values", func(t *testing.T) {
		got := evalTopo("zone=rack-a,zone=rack-b", makeNode())
		if got.Result == ResultPASS {
			t.Fatalf("conflicting requirements silently overwritten: %+v", got)
		}
	})
	t.Run("P5R3_unsupported_key_must_not_hide_known_mismatch", func(t *testing.T) {
		got := evalTopo("zone=rack-a,nvlink=required", makeNode())
		if got.Result != ResultREJECT {
			t.Fatalf("known rack-a != rack-b hidden by unsupported key: %+v", got)
		}
	})
	t.Run("P5R4_missing_hostname_label_is_not_node_name", func(t *testing.T) {
		got := evalTopo("hostname=node-a", makeNode())
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("hostname label missing but node name substituted: %+v", got)
		}
	})

	gangPod := func() *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "member", Namespace: "review", UID: "member-1",
				OwnerReferences: []metav1.OwnerReference{{
					UID: "same-gang", Name: "job", Kind: "Job", APIVersion: "batch/v1",
				}},
				Annotations: map[string]string{AnnotationGangSize: "2"},
			},
			Spec: v1.PodSpec{Containers: []v1.Container{{
				Name: "c",
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
				},
			}}},
		}
	}
	t.Run("P5R5_gang_size_does_not_require_colocation", func(t *testing.T) {
		p := gangPod()
		res := NodeResourceView{
			Allocatable:     v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			AllocatedScalar: map[string]int64{},
		}
		c := topoCfg
		c.Mode = ModeEnforce
		got := EvaluateTopology(p, makeNode(), ParseRequest(p), res, c, now)
		if got.Result == ResultREJECT {
			t.Fatalf("distributed gang discarded solely for not fitting on one node: %+v", got)
		}
	})
	t.Run("P5R6_gang_member_already_assumed_is_not_new_demand", func(t *testing.T) {
		p := gangPod()
		res := NodeResourceView{
			Allocatable:     v1.ResourceList{"nvidia.com/gpu": resource.MustParse("2")},
			AllocatedScalar: map[string]int64{},
		}
		c := topoCfg
		c.Mode = ModeEnforce
		first := EvaluateTopology(p, makeNode(), ParseRequest(p), res, c, now)
		if first.Result == ResultREJECT {
			t.Fatalf("control: initial group capacity is not proven infeasible: %+v", first)
		}
		res.AllocatedScalar["nvidia.com/gpu"] = 1
		p.Name = "second-member"
		p.UID = "member-2"
		second := EvaluateTopology(p, makeNode(), ParseRequest(p), res, c, now)
		if second.Result == ResultREJECT {
			t.Fatalf("whole gang required again instead of remaining members: %+v", second)
		}
	})

	powerNode := func(value float64) NodeTelemetry {
		return NodeTelemetry{
			NodeName: "node-a",
			Devices: []DeviceTelemetry{{
				UUID: "gpu-0", NodeName: "node-a", Kind: "gpu",
				PowerW: TimedValue{
					Present: true, Value: value, Unit: "W", Source: "fixture",
					CollectedAt: now.Add(-time.Second), ReceivedAt: now,
				},
			}},
		}
	}
	t.Run("P5R7_invalid_numeric_power_must_not_PASS", func(t *testing.T) {
		limit := 300.0
		c := cfg.Checks[CheckThermalPower]
		c.MaxGPUPowerW = &limit
		for _, value := range []float64{math.NaN(), -1, math.Inf(-1)} {
			n := powerNode(value)
			store := NewStore()
			if err := store.UpsertNode(n); err != nil {
				continue // store rejection is also acceptable
			}
			got := EvaluateThermalPower(store.Snapshot().Nodes[n.NodeName], c, cfg.Checks[CheckFreshness], now)
			if got.Result != ResultUNEVALUATED {
				t.Errorf("invalid power %v: %+v", value, got)
			}
		}
		// Direct evaluate path (bypass store) must also refuse.
		got := EvaluateThermalPower(powerNode(math.NaN()), c, cfg.Checks[CheckFreshness], now)
		if got.Result == ResultPASS {
			t.Fatalf("NaN power must not PASS: %+v", got)
		}
	})
	t.Run("P5R8_nonfinite_limit_must_be_invalid_config", func(t *testing.T) {
		for _, raw := range []string{".nan", ".inf"} {
			_, err := ParseConfigYAML([]byte("version: review\nchecks:\n  thermal-power:\n    max_gpu_power_w: " + raw + "\n"))
			if err == nil {
				t.Errorf("nonfinite configured limit %s accepted", raw)
			}
		}
	})
	t.Run("P5R9_server_power_belongs_to_another_node", func(t *testing.T) {
		limit := 500.0
		c := cfg.Checks[CheckThermalPower]
		c.MaxServerPowerW = &limit
		n := NodeTelemetry{
			NodeName: "node-a",
			ServerPower: &ServerPowerTelemetry{
				NodeName: "node-b", AgentTimeOK: true,
				PowerW: TimedValue{
					Present: true, Value: 400, Unit: "W", Source: "netio",
					CollectedAt: now.Add(-time.Second), ReceivedAt: now,
				},
			},
		}
		store := NewStore()
		if err := store.UpsertNode(n); err != nil {
			return // store rejection OK
		}
		got := EvaluateThermalPower(store.Snapshot().Nodes[n.NodeName], c, cfg.Checks[CheckFreshness], now)
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("node-b reading evaluated for node-a: %+v", got)
		}
	})
}
