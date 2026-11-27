package placementchecks

import (
	"testing"
	"time"
)

func freshTV(v float64, unit string, now time.Time) TimedValue {
	return TimedValue{
		Present: true, Value: v, Unit: unit,
		Source: "fixture", CollectedAt: now.Add(-10 * time.Second), ReceivedAt: now,
	}
}

func TestEvaluateThermalLimitsUnset(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cr := EvaluateThermalPower(NodeTelemetry{NodeName: "n"}, cfg.Checks[CheckThermalPower], cfg.Checks[CheckFreshness], now)
	if cr.Result != ResultUNEVALUATED || cr.Code != "limits_unset" {
		t.Fatalf("%+v", cr)
	}
}

func TestEvaluateThermalGPUTempPassAndReject(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lim := 80.0
	tc := cfg.Checks[CheckThermalPower]
	tc.MaxGPUTempC = &lim
	cfg.Checks[CheckThermalPower] = tc
	fresh := cfg.Checks[CheckFreshness]

	okNode := NodeTelemetry{
		NodeName: "n",
		Devices: []DeviceTelemetry{{
			UUID: "gpu-0", Kind: "gpu", NodeName: "n",
			TemperatureC: freshTV(70, "C", now),
		}},
	}
	cr := EvaluateThermalPower(okNode, cfg.Checks[CheckThermalPower], fresh, now)
	if cr.Result != ResultPASS || cr.Code != "within_limits" {
		t.Fatalf("pass: %+v", cr)
	}

	hot := okNode
	hot.Devices = []DeviceTelemetry{{
		UUID: "gpu-0", Kind: "gpu", NodeName: "n",
		TemperatureC: freshTV(85, "C", now),
	}}
	cr2 := EvaluateThermalPower(hot, cfg.Checks[CheckThermalPower], fresh, now)
	if cr2.Result != ResultREJECT || cr2.Code != "gpu_temp_exceeded" {
		t.Fatalf("reject: %+v", cr2)
	}
	if cr2.Enforced {
		t.Fatal("record must not enforce")
	}
}

func TestEvaluateThermalStaleIsUnevaluated(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lim := 80.0
	tc := cfg.Checks[CheckThermalPower]
	tc.MaxGPUTempC = &lim
	cfg.Checks[CheckThermalPower] = tc
	node := NodeTelemetry{
		NodeName: "n",
		Devices: []DeviceTelemetry{{
			UUID: "gpu-0", Kind: "gpu",
			TemperatureC: TimedValue{
				Present: true, Value: 90, Unit: "C",
				CollectedAt: now.Add(-2 * time.Hour), ReceivedAt: now,
			},
		}},
	}
	cr := EvaluateThermalPower(node, cfg.Checks[CheckThermalPower], cfg.Checks[CheckFreshness], now)
	if cr.Result != ResultUNEVALUATED || cr.Code != "expired" {
		t.Fatalf("stale must not REJECT: %+v", cr)
	}
}

func TestEvaluateThermalServerPowerSeparateFromGPU(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	srv := 1500.0
	tc := cfg.Checks[CheckThermalPower]
	tc.MaxServerPowerW = &srv
	// GPU power limit intentionally unset — server-only check.
	cfg.Checks[CheckThermalPower] = tc

	node := NodeTelemetry{
		NodeName: "n",
		Devices: []DeviceTelemetry{{
			UUID: "gpu-0", Kind: "gpu",
			PowerW: freshTV(400, "W", now), // high GPU W must not use server limit
		}},
		ServerPower: &ServerPowerTelemetry{
			NodeName: "n", AgentTimeOK: true,
			PowerW: freshTV(1200, "W", now),
		},
	}
	cr := EvaluateThermalPower(node, cfg.Checks[CheckThermalPower], cfg.Checks[CheckFreshness], now)
	if cr.Result != ResultPASS {
		t.Fatalf("server within limit: %+v", cr)
	}

	node.ServerPower.PowerW = freshTV(1600, "W", now)
	cr2 := EvaluateThermalPower(node, cfg.Checks[CheckThermalPower], cfg.Checks[CheckFreshness], now)
	if cr2.Result != ResultREJECT || cr2.Code != "server_power_exceeded" {
		t.Fatalf("server exceed: %+v", cr2)
	}
}

func TestEvaluateThermalNoGPUTelemetry(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lim := 80.0
	tc := cfg.Checks[CheckThermalPower]
	tc.MaxGPUTempC = &lim
	cfg.Checks[CheckThermalPower] = tc
	cr := EvaluateThermalPower(NodeTelemetry{NodeName: "n"}, cfg.Checks[CheckThermalPower], cfg.Checks[CheckFreshness], now)
	if cr.Code != "no_gpu_telemetry" {
		t.Fatalf("%+v", cr)
	}
}

func TestEvaluateThermalEnforce(t *testing.T) {
	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	lim := 80.0
	tc := cfg.Checks[CheckThermalPower]
	tc.Mode = ModeEnforce
	tc.MaxGPUTempC = &lim
	cfg.Checks[CheckThermalPower] = tc
	node := NodeTelemetry{
		NodeName: "n",
		Devices: []DeviceTelemetry{{
			UUID: "gpu-0", Kind: "gpu", TemperatureC: freshTV(90, "C", now),
		}},
	}
	rep := NewEngine(cfg).EvaluateNode(EvalInput{
		NodeName: "n", Snap: Snapshot{Nodes: map[string]NodeTelemetry{"n": node}}, Now: now,
	})
	if !ShouldExclude(rep) {
		t.Fatalf("enforce must filter: %+v", rep)
	}
}
