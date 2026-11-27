package placementchecks

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Store holds measured node telemetry. This is the sole source of truth for
// freshness and measured values used by required checks. Cache.GPUMetricsUpdatedAt
// must not be used for freshness.
type Store struct {
	mu      sync.RWMutex
	nodes   map[string]NodeTelemetry
	version uint64
}

// NewStore creates an empty telemetry store.
func NewStore() *Store {
	return &Store{nodes: map[string]NodeTelemetry{}}
}

// UpsertNode replaces one node's telemetry after validation.
// Invalid payloads (no devices identity, negative memory) return an error and
// do not bump version or clocks.
func (s *Store) UpsertNode(n NodeTelemetry) error {
	if err := validateNodeTelemetry(n); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	n.Version = s.version
	// Deep copy devices slice.
	cp := n
	cp.Devices = append([]DeviceTelemetry(nil), n.Devices...)
	if n.ServerPower != nil {
		sp := *n.ServerPower
		cp.ServerPower = &sp
	}
	s.nodes[n.NodeName] = cp
	return nil
}

// DeleteNode removes telemetry for a node.
func (s *Store) DeleteNode(nodeName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodes, nodeName)
	s.version++
}

// Snapshot returns an immutable deep copy for one scheduling attempt.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{
		ID:      uuid.NewString(),
		TakenAt: time.Now().UTC(),
		Version: s.version,
		Nodes:   make(map[string]NodeTelemetry, len(s.nodes)),
	}
	for k, n := range s.nodes {
		cp := n
		cp.Devices = append([]DeviceTelemetry(nil), n.Devices...)
		if n.ServerPower != nil {
			sp := *n.ServerPower
			cp.ServerPower = &sp
		}
		out.Nodes[k] = cp
	}
	return out
}

// Version returns the current store generation.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

func validateNodeTelemetry(n NodeTelemetry) error {
	if n.NodeName == "" {
		return fmt.Errorf("node_name required")
	}
	seen := map[string]struct{}{}
	for i, d := range n.Devices {
		if d.UUID == "" {
			return fmt.Errorf("devices[%d]: uuid required", i)
		}
		if _, dup := seen[d.UUID]; dup {
			return fmt.Errorf("devices[%d]: duplicate uuid %s", i, d.UUID)
		}
		seen[d.UUID] = struct{}{}
		if d.Kind != "gpu" && d.Kind != "npu" {
			return fmt.Errorf("devices[%d]: kind must be gpu|npu", i)
		}
		if d.NodeName != "" && d.NodeName != n.NodeName {
			return fmt.Errorf("devices[%d]: node_name %q does not match parent %q", i, d.NodeName, n.NodeName)
		}
		if err := validateMemoryField(d.MemoryTotalMiB, i, "memory_total_mib"); err != nil {
			return err
		}
		if err := validateMemoryField(d.MemoryUsedMiB, i, "memory_used_mib"); err != nil {
			return err
		}
		if d.MemoryTotalMiB.Present && d.MemoryUsedMiB.Present &&
			d.MemoryUsedMiB.Value > d.MemoryTotalMiB.Value {
			return fmt.Errorf("devices[%d]: memory_used_mib > memory_total_mib", i)
		}
		if err := validateFiniteMeasurement(d.TemperatureC, i, "temperature_c", true); err != nil {
			return err
		}
		if err := validateFiniteMeasurement(d.PowerW, i, "power_w", false); err != nil {
			return err
		}
	}
	if n.ServerPower != nil {
		if n.ServerPower.NodeName != "" && n.ServerPower.NodeName != n.NodeName {
			return fmt.Errorf("server_power.node_name %q does not match parent %q", n.ServerPower.NodeName, n.NodeName)
		}
		if err := validateFiniteMeasurement(n.ServerPower.PowerW, -1, "server_power_w", false); err != nil {
			return err
		}
	}
	return nil
}

func validateMemoryField(tv TimedValue, idx int, name string) error {
	if !tv.Present {
		return nil
	}
	if !floatIsFinite(tv.Value) {
		return fmt.Errorf("devices[%d]: %s non-finite", idx, name)
	}
	if tv.Value < 0 {
		return fmt.Errorf("devices[%d]: %s < 0", idx, name)
	}
	if code := memoryUnitCode(tv); code != "" {
		return fmt.Errorf("devices[%d]: %s unit %q not MiB", idx, name, tv.Unit)
	}
	return nil
}

// validateFiniteMeasurement rejects NaN/Inf. allowNegative is for temperature (°C).
func validateFiniteMeasurement(tv TimedValue, idx int, name string, allowNegative bool) error {
	if !tv.Present {
		return nil
	}
	prefix := name
	if idx >= 0 {
		prefix = fmt.Sprintf("devices[%d]: %s", idx, name)
	}
	if !floatIsFinite(tv.Value) {
		return fmt.Errorf("%s non-finite", prefix)
	}
	if !allowNegative && tv.Value < 0 {
		return fmt.Errorf("%s < 0", prefix)
	}
	return nil
}

// --- P2 fixture / file injection (test and offline only; not live NVML) ---

// InjectFileJSON loads a NodeTelemetry JSON document into the store.
// Intended for fixture/file injection in P2; live collectors are P8.
func (s *Store) InjectFileJSON(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return s.InjectJSON(raw)
}

// InjectJSON unmarshals NodeTelemetry or a wrapper {"nodes":[...]} into the store.
func (s *Store) InjectJSON(raw []byte) error {
	var wrap struct {
		Nodes []NodeTelemetry `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &wrap); err == nil && len(wrap.Nodes) > 0 {
		for _, n := range wrap.Nodes {
			if err := s.UpsertNode(n); err != nil {
				return err
			}
		}
		return nil
	}
	var one NodeTelemetry
	if err := json.Unmarshal(raw, &one); err != nil {
		return fmt.Errorf("inject json: %w", err)
	}
	return s.UpsertNode(one)
}

// AttemptID generates a new scheduling-attempt identifier.
func AttemptID() string {
	return uuid.NewString()
}

// monoSeq is an optional dense id helper for tests.
var monoSeq atomic.Uint64

// AttemptIDSequential is test-friendly incremental IDs (not for production uniqueness alone).
func AttemptIDSequential(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, monoSeq.Add(1))
}
