package placementchecks

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Formalized from REVIEW_P1P2.md R1–R11 (overlay counterexamples).
func TestReviewP1P2Boundaries(t *testing.T) {
	now := time.Date(2026, 10, 26, 2, 15, 30, 0, time.UTC)
	cfg := testCfgMargin(0)
	loadNode := func(t *testing.T) NodeTelemetry {
		t.Helper()
		s := NewStore()
		if err := s.InjectFileJSON(fixturePath("node_pro6000_occupied.json")); err != nil {
			t.Fatal(err)
		}
		return s.Snapshot().Nodes["gpu-node-01"]
	}

	t.Run("R1_old_source_new_receive_must_not_be_fresh", func(t *testing.T) {
		tv := TimedValue{Present: true, Value: 1000, CollectedAt: now.Add(-2 * time.Hour), ReceivedAt: now}
		got := EvaluateTimedValue(tv, "memory_used_mib", cfg.Checks[CheckFreshness], now)
		if got.OK {
			t.Fatalf("stale source accepted as fresh: %+v", got)
		}
		if got.Code != "expired" {
			t.Fatalf("code=%s want expired", got.Code)
		}
	})

	t.Run("R2_missing_source_time_must_not_use_receive", func(t *testing.T) {
		tv := TimedValue{Present: true, Value: 1000, ReceivedAt: now}
		got := EvaluateTimedValue(tv, "memory_used_mib", cfg.Checks[CheckFreshness], now)
		if got.OK {
			t.Fatalf("missing source time accepted as fresh: %+v", got)
		}
		if got.Code != "missing_source_time" {
			t.Fatalf("code=%s", got.Code)
		}
	})

	t.Run("R3_empty_server_sample_is_not_PASS", func(t *testing.T) {
		got := EvaluateFreshness(NodeTelemetry{
			NodeName:    "n",
			ServerPower: &ServerPowerTelemetry{NodeName: "n"},
		}, cfg.Checks[CheckFreshness], now)
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("missing server measurement: %+v", got)
		}
	})

	t.Run("R4_duplicate_device_uuid_must_not_satisfy_count_two", func(t *testing.T) {
		n := loadNode(t)
		n.Devices = append(n.Devices, n.Devices[0])
		s := NewStore()
		if err := s.UpsertNode(n); err == nil {
			t.Fatal("store must reject duplicate uuid")
		}
		got := EvaluateMemory(n, ParseRequest(gpuPod("4096", "2")), cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], now)
		if got.Result == ResultPASS {
			t.Fatalf("same physical device counted twice: %+v", got)
		}
		if got.Code != "duplicate_device_uuid" {
			t.Fatalf("code=%s", got.Code)
		}
	})

	t.Run("R5_cross_node_device_identity_must_not_PASS", func(t *testing.T) {
		n := loadNode(t)
		n.Devices[0].NodeName = "another-node"
		s := NewStore()
		if err := s.UpsertNode(n); err == nil {
			t.Fatal("store must reject cross-node device")
		}
		got := EvaluateMemory(n, ParseRequest(gpuPod("4096", "1")), cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], now)
		if got.Result == ResultPASS {
			t.Fatalf("device belonging to another node accepted: %+v", got)
		}
		if got.Code != "device_node_mismatch" {
			t.Fatalf("code=%s", got.Code)
		}
	})

	t.Run("R6_memory_units_must_match", func(t *testing.T) {
		n := loadNode(t)
		n.Devices[0].MemoryTotalMiB.Unit = "KiB"
		n.Devices[0].MemoryUsedMiB.Unit = "KiB"
		s := NewStore()
		if err := s.UpsertNode(n); err == nil {
			t.Fatal("store must reject non-MiB unit")
		}
		got := EvaluateMemory(n, ParseRequest(gpuPod("4096", "1")), cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], now)
		if got.Result != ResultUNEVALUATED {
			t.Fatalf("KiB treated as MiB: %+v", got)
		}
	})

	t.Run("R7_GPU_and_NPU_requests_must_not_ignore_NPU", func(t *testing.T) {
		n := loadNode(t)
		pod := gpuPod("4096", "1")
		pod.Spec.Containers[0].Resources.Requests["furiosa.ai/rngd"] = resource.MustParse("1")
		got := EvaluateMemory(n, ParseRequest(pod), cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], now)
		if got.Result == ResultPASS {
			t.Fatalf("NPU ignored under GPU PASS: %+v", got)
		}
		if got.Code != "mixed_accelerator_request" {
			t.Fatalf("code=%s", got.Code)
		}
	})

	t.Run("R8_request_plus_margin_overflow_must_not_PASS", func(t *testing.T) {
		n := loadNode(t)
		c := testCfgMargin(1)
		got := EvaluateMemory(n, ParseRequest(gpuPod("9223372036854775807", "1")), c.Checks[CheckMemory], c.Checks[CheckFreshness], now)
		if got.Result == ResultPASS {
			t.Fatalf("overflow produced PASS: %+v", got)
		}
		if got.Code != "request_margin_overflow" {
			t.Fatalf("code=%s", got.Code)
		}
	})

	t.Run("R9_freshness_null_threshold_must_stay_null", func(t *testing.T) {
		c, err := ParseConfigYAML([]byte("version: review\nchecks:\n  data-freshness:\n    max_age_seconds: null\n"))
		if err != nil {
			t.Fatal(err)
		}
		if c.Checks[CheckFreshness].MaxAgeSeconds != nil {
			t.Fatalf("null replaced by %d", *c.Checks[CheckFreshness].MaxAgeSeconds)
		}
	})

	t.Run("R10_unknown_config_key_must_fail", func(t *testing.T) {
		_, err := ParseConfigYAML([]byte("version: review\nchecks:\n  memory-headroom:\n    memory_safety_margin_mib_typo: 0\n"))
		if err == nil {
			t.Fatal("unknown threshold key silently ignored")
		}
	})

	t.Run("R11_empty_path_load_must_respect_mode_env", func(t *testing.T) {
		t.Setenv("SCHED_REQUIRED_CHECK_2_MODE", "off")
		c, err := LoadConfigFile("")
		if err != nil {
			t.Fatal(err)
		}
		if c.ModeOf(CheckMemory) != ModeOff {
			t.Fatalf("env ignored on default load: %s", c.ModeOf(CheckMemory))
		}
	})
}
