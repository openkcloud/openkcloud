// Package placementchecks implements the required placement checks (workshop 7).
// P1/P2: shared types, config, request parse, telemetry store, memory + freshness.
// Framework Filter wiring is deferred to later phases.
package placementchecks

import (
	"time"
)

// CheckID identifies one of the seven required checks.
type CheckID string

const (
	CheckAccelerator   CheckID = "accelerator"    // 1
	CheckMemory        CheckID = "memory-headroom" // 2
	CheckRuntime       CheckID = "runtime"         // 3
	CheckTopology      CheckID = "topology"        // 4
	CheckThermalPower  CheckID = "thermal-power"   // 5
	CheckFreshness     CheckID = "data-freshness"  // 6
	CheckPolicy        CheckID = "policy"          // 7
)

// AllCheckIDs is the fixed evaluation order (1→7).
var AllCheckIDs = []CheckID{
	CheckAccelerator,
	CheckMemory,
	CheckRuntime,
	CheckTopology,
	CheckThermalPower,
	CheckFreshness,
	CheckPolicy,
}

// CheckNumber returns 1..7 for env override keys; 0 if unknown.
func CheckNumber(id CheckID) int {
	for i, c := range AllCheckIDs {
		if c == id {
			return i + 1
		}
	}
	return 0
}

// Result is the structured outcome of one check.
type Result string

const (
	ResultPASS         Result = "PASS"
	ResultREJECT       Result = "REJECT"
	ResultUNEVALUATED  Result = "UNEVALUATED"
)

// Mode controls evaluation and candidate exclusion for new checks.
type Mode string

const (
	ModeOff     Mode = "off"
	ModeRecord  Mode = "record"
	ModeEnforce Mode = "enforce"
)

// Annotation keys for per-device memory budget (MiB). No defaults.
const (
	AnnotationGPUMemoryMiB = "sched.ai/gpu-memory-mib"
	AnnotationNPUMemoryMiB = "sched.ai/npu-memory-mib"
)

// Runtime annotation keys are defined in runtime.go (min-cuda-driver / min-cuda-runtime).

// TimedValue holds a measured numeric with source and clock metadata.
// Missing measurements use Present=false; measured zero uses Present=true and Value=0.
type TimedValue struct {
	Present     bool      `json:"present"`
	Value       float64   `json:"value,omitempty"`
	Unit        string    `json:"unit,omitempty"`
	Source      string    `json:"source,omitempty"`
	CollectedAt time.Time `json:"collected_at,omitempty"` // instrument/source clock
	ReceivedAt  time.Time `json:"received_at,omitempty"`  // scheduler receive clock
}

// DeviceTelemetry is measured state for one accelerator.
type DeviceTelemetry struct {
	UUID              string     `json:"uuid"`
	NodeName          string     `json:"node_name"`
	Kind              string     `json:"kind"` // gpu | npu
	Model             string     `json:"model,omitempty"`
	MemoryTotalMiB    TimedValue `json:"memory_total_mib"`
	MemoryUsedMiB     TimedValue `json:"memory_used_mib"`
	UtilizationPCT    TimedValue `json:"utilization_pct"`
	TemperatureC      TimedValue `json:"temperature_c"`
	PowerW            TimedValue `json:"power_w"`
}

// ServerPowerTelemetry is NETIO (or similar) server input power.
type ServerPowerTelemetry struct {
	NodeName    string     `json:"node_name"`
	PowerW      TimedValue `json:"power_w"`
	EnergyWh    TimedValue `json:"energy_wh"`
	AgentTime   time.Time  `json:"agent_time,omitempty"` // raw Agent.Time when known
	AgentTimeOK bool       `json:"agent_time_ok"`        // false if 1970/invalid
}

// NodeTelemetry aggregates devices and optional server power for one node.
type NodeTelemetry struct {
	NodeName    string                `json:"node_name"`
	Devices     []DeviceTelemetry     `json:"devices"`
	ServerPower *ServerPowerTelemetry `json:"server_power,omitempty"`
	Version     uint64                `json:"version"`
}

// Snapshot is an immutable copy of telemetry used for one scheduling attempt.
type Snapshot struct {
	ID        string                    `json:"snapshot_id"`
	TakenAt   time.Time                 `json:"taken_at"`
	Version   uint64                    `json:"store_version"`
	Nodes     map[string]NodeTelemetry  `json:"nodes"`
}

// CheckResult is one check outcome for one candidate node.
type CheckResult struct {
	Check       CheckID        `json:"check"`
	Result      Result         `json:"result"`
	Code        string         `json:"code"`
	Detail      string         `json:"detail,omitempty"`
	Mode        Mode           `json:"mode"`
	InputValue  map[string]any `json:"input_value,omitempty"`
	Threshold   map[string]any `json:"threshold,omitempty"`
	CollectedAt *time.Time     `json:"collected_at,omitempty"`
	EvaluatedAt time.Time      `json:"evaluated_at"`
	DurationNS  int64          `json:"duration_ns"`
	Enforced    bool           `json:"enforced"` // true only if this REJECT excludes the node
}

// RejectionReason is one exclusion cause (legacy filter or new check).
type RejectionReason struct {
	Stage  string `json:"stage"` // required_check | legacy_filter | hami_filter | catalog_cost
	Check  string `json:"check,omitempty"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// CandidateReport is the full structured report for one node in one attempt.
type CandidateReport struct {
	NodeName          string            `json:"node_name"`
	Checks            []CheckResult     `json:"checks"`
	RejectionReasons  []RejectionReason `json:"rejection_reasons,omitempty"`
	Representative    *RejectionReason  `json:"representative_reason,omitempty"`
	IsFiltered        bool              `json:"is_filtered"`
}

// AttemptMeta links an evaluation cycle.
type AttemptMeta struct {
	AttemptID     string    `json:"attempt_id"`
	TraceID       string    `json:"trace_id,omitempty"`
	ExperimentID  string    `json:"experiment_id,omitempty"`
	PodNamespace  string    `json:"pod_namespace"`
	PodName       string    `json:"pod_name"`
	PodUID        string    `json:"pod_uid"`
	SnapshotID    string    `json:"snapshot_id"`
	ConfigVersion string    `json:"config_version"`
	StartedAt     time.Time `json:"started_at"`
}
