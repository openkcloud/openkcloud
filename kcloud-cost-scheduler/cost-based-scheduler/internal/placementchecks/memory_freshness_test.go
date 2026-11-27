package placementchecks

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testCfgMargin(margin int64) Config {
	cfg := DefaultConfig()
	m := margin
	age := int64(3600)
	c := cfg.Checks[CheckMemory]
	c.MemorySafetyMarginMiB = &m
	cfg.Checks[CheckMemory] = c
	f := cfg.Checks[CheckFreshness]
	f.MaxAgeSeconds = &age
	cfg.Checks[CheckFreshness] = f
	return cfg
}

func gpuPod(memMiB string, count string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "ns", UID: "uid-1",
			Annotations: map[string]string{AnnotationGPUMemoryMiB: memMiB},
		},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse(count)},
			},
		}}},
	}
}

func TestMemoryRejectOccupancyNotUtilAlone(t *testing.T) {
	// free = 98304-36047 = 62257; request 65536 + margin 1024 = 66560 > 62257 → REJECT
	// util 100% is recorded but not the reject criterion by itself.
	s := NewStore()
	if err := s.InjectFileJSON(fixturePath("node_pro6000_occupied.json")); err != nil {
		t.Fatal(err)
	}
	cfg := testCfgMargin(1024)
	eng := NewEngine(cfg)
	now := time.Date(2026, 10, 26, 2, 15, 30, 0, time.UTC)
	rep := eng.EvaluateNode(EvalInput{Meta: AttemptMeta{AttemptID: "a1"}, Pod: gpuPod("65536", "1"), NodeName: "gpu-node-01", Snap: s.Snapshot(), Now: now})
	var mem CheckResult
	for _, c := range rep.Checks {
		if c.Check == CheckMemory {
			mem = c
		}
	}
	if mem.Result != ResultREJECT || mem.Code != "memory-headroom" {
		t.Fatalf("memory=%+v", mem)
	}
	// Same occupancy, smaller request → PASS (util still 100%).
	rep2 := eng.EvaluateNode(EvalInput{Meta: AttemptMeta{AttemptID: "a2"}, Pod: gpuPod("4096", "1"), NodeName: "gpu-node-01", Snap: s.Snapshot(), Now: now})
	for _, c := range rep2.Checks {
		if c.Check == CheckMemory && c.Result != ResultPASS {
			t.Fatalf("expected PASS for small request, got %+v", c)
		}
	}
}

func TestMemoryUnevaluatedWhenAnnotationMissing(t *testing.T) {
	s := NewStore()
	_ = s.InjectFileJSON(fixturePath("node_pro6000_occupied.json"))
	cfg := testCfgMargin(1024)
	pod := gpuPod("65536", "1")
	delete(pod.Annotations, AnnotationGPUMemoryMiB)
	req := ParseRequest(pod)
	cr := EvaluateMemory(s.Snapshot().Nodes["gpu-node-01"], req, cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], time.Date(2026, 10, 26, 2, 15, 30, 0, time.UTC))
	if cr.Result != ResultUNEVALUATED || cr.Code != "memory_annotation_missing" {
		t.Fatalf("%+v", cr)
	}
}

func TestMemoryUnevaluatedWhenMarginNull(t *testing.T) {
	s := NewStore()
	_ = s.InjectFileJSON(fixturePath("node_pro6000_occupied.json"))
	cfg := DefaultConfig()
	age := int64(3600)
	f := cfg.Checks[CheckFreshness]
	f.MaxAgeSeconds = &age
	cfg.Checks[CheckFreshness] = f
	req := ParseRequest(gpuPod("4096", "1"))
	cr := EvaluateMemory(s.Snapshot().Nodes["gpu-node-01"], req, cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], time.Date(2026, 10, 26, 2, 15, 30, 0, time.UTC))
	if cr.Code != "safety_margin_unset" {
		t.Fatalf("%+v", cr)
	}
}

func TestMemoryPerDeviceNotSum(t *testing.T) {
	s := NewStore()
	_ = s.InjectFileJSON(fixturePath("node_dual_gpu_asymmetric.json"))
	cfg := testCfgMargin(0)
	now := time.Date(2026, 10, 26, 2, 15, 30, 0, time.UTC)
	// request 30000 on 1 GPU: only large fits (free ~45068); small free ~4576 → PASS
	cr := EvaluateMemory(s.Snapshot().Nodes["dual-gpu-node"], ParseRequest(gpuPod("30000", "1")), cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], now)
	if cr.Result != ResultPASS {
		t.Fatalf("1x30000: %+v", cr)
	}
	// 2 GPUs each needing 30000: only one device fits → REJECT (sum would be enough)
	cr2 := EvaluateMemory(s.Snapshot().Nodes["dual-gpu-node"], ParseRequest(gpuPod("30000", "2")), cfg.Checks[CheckMemory], cfg.Checks[CheckFreshness], now)
	if cr2.Result != ResultREJECT {
		t.Fatalf("2x30000 should REJECT on per-device rule: %+v", cr2)
	}
}

func TestFreshness1970AndExpired(t *testing.T) {
	s := NewStore()
	_ = s.InjectFileJSON(fixturePath("node_netio_1970.json"))
	cfg := testCfgMargin(1024)
	now := time.Date(2026, 10, 26, 2, 15, 30, 0, time.UTC)
	cr := EvaluateFreshness(s.Snapshot().Nodes["gpu-node-01"], cfg.Checks[CheckFreshness], now)
	if cr.Result != ResultUNEVALUATED {
		t.Fatalf("1970 server power should UNEVALUATED freshness: %+v", cr)
	}
	// Device memory is fresh; expired case:
	node := s.Snapshot().Nodes["gpu-node-01"]
	old := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	node.Devices[0].MemoryUsedMiB.CollectedAt = old
	node.Devices[0].MemoryUsedMiB.ReceivedAt = old
	node.Devices[0].MemoryTotalMiB.CollectedAt = old
	node.Devices[0].MemoryTotalMiB.ReceivedAt = old
	node.ServerPower = nil
	age := int64(60)
	fc := cfg.Checks[CheckFreshness]
	fc.MaxAgeSeconds = &age
	later := time.Date(2026, 10, 26, 0, 5, 0, 0, time.UTC)
	cr2 := EvaluateFreshness(node, fc, later)
	if cr2.Code != "expired" && cr2.Result != ResultUNEVALUATED {
		t.Fatalf("expired: %+v", cr2)
	}
	// Memory check must not REJECT/PASS on stale data.
	mem := EvaluateMemory(node, ParseRequest(gpuPod("4096", "1")), cfg.Checks[CheckMemory], fc, later)
	if mem.Result != ResultUNEVALUATED {
		t.Fatalf("stale memory must UNEVALUATED: %+v", mem)
	}
}

func TestRecordModeDoesNotEnforce(t *testing.T) {
	s := NewStore()
	_ = s.InjectFileJSON(fixturePath("node_pro6000_occupied.json"))
	cfg := testCfgMargin(1024)
	cfg.Checks[CheckMemory] = func() CheckConfig {
		c := cfg.Checks[CheckMemory]
		c.Mode = ModeRecord
		return c
	}()
	now := time.Date(2026, 10, 26, 2, 15, 30, 0, time.UTC)
	rep := NewEngine(cfg).EvaluateNode(EvalInput{Pod: gpuPod("65536", "1"), NodeName: "gpu-node-01", Snap: s.Snapshot(), Now: now})
	if ShouldExclude(rep) {
		t.Fatal("record mode must not exclude")
	}
	cfg.Checks[CheckMemory] = func() CheckConfig {
		c := cfg.Checks[CheckMemory]
		c.Mode = ModeEnforce
		return c
	}()
	rep2 := NewEngine(cfg).EvaluateNode(EvalInput{Pod: gpuPod("65536", "1"), NodeName: "gpu-node-01", Snap: s.Snapshot(), Now: now})
	if !ShouldExclude(rep2) {
		t.Fatal("enforce mode must exclude on REJECT")
	}
}

func TestFutureTimestamp(t *testing.T) {
	cfg := testCfgMargin(0)
	now := time.Date(2026, 10, 26, 2, 0, 0, 0, time.UTC)
	tv := TimedValue{
		Present: true, Value: 1, CollectedAt: now.Add(time.Hour), ReceivedAt: now.Add(time.Hour),
	}
	fr := EvaluateTimedValue(tv, "x", cfg.Checks[CheckFreshness], now)
	if fr.OK || fr.Code != "future_timestamp" {
		t.Fatalf("%+v", fr)
	}
}
