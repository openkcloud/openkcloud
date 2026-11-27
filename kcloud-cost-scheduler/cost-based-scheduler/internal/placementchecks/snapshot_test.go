package placementchecks

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func fixturePath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "required_checks", name)
}

func TestInjectFileAndSnapshotImmutable(t *testing.T) {
	s := NewStore()
	if err := s.InjectFileJSON(fixturePath("node_pro6000_occupied.json")); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	n := snap.Nodes["gpu-node-01"]
	if len(n.Devices) != 1 {
		t.Fatalf("devices=%d", len(n.Devices))
	}
	// Mutate snapshot copy — store must be unchanged.
	n.Devices[0].MemoryUsedMiB.Value = 1
	snap.Nodes["gpu-node-01"] = n
	snap2 := s.Snapshot()
	if snap2.Nodes["gpu-node-01"].Devices[0].MemoryUsedMiB.Value != 36047 {
		t.Fatal("store mutated via snapshot")
	}
}

func TestUpsertNilRejectDoesNotBumpOnInvalid(t *testing.T) {
	s := NewStore()
	v0 := s.Version()
	err := s.UpsertNode(NodeTelemetry{NodeName: "x", Devices: []DeviceTelemetry{{Kind: "gpu"}}})
	if err == nil {
		t.Fatal("expected uuid required")
	}
	if s.Version() != v0 {
		t.Fatal("version bumped on invalid upsert")
	}
}

func TestConcurrentSnapshot(t *testing.T) {
	s := NewStore()
	_ = s.InjectFileJSON(fixturePath("node_pro6000_occupied.json"))
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_ = s.Snapshot()
			} else {
				n := NodeTelemetry{
					NodeName: "gpu-node-01",
					Devices: []DeviceTelemetry{{
						UUID: "GPU-00000000-fixture", NodeName: "gpu-node-01", Kind: "gpu",
						MemoryTotalMiB: TimedValue{Present: true, Value: 98304, CollectedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC()},
						MemoryUsedMiB:  TimedValue{Present: true, Value: float64(36000 + i), CollectedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC()},
					}},
				}
				_ = s.UpsertNode(n)
			}
		}(i)
	}
	wg.Wait()
}
