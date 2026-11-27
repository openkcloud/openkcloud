package placementchecks

import (
	"fmt"
	"strings"
	"time"
)

// EvaluateThermalPower is check 5: measured GPU temp/power and server input power
// against configured caps. Unset limits → UNEVALUATED. Stale/missing readings for
// a configured limit → UNEVALUATED (never PASS/REJECT on bad clocks).
// GPU device power and NETIO server power are never mixed into one threshold.
func EvaluateThermalPower(node NodeTelemetry, cfg CheckConfig, freshCfg CheckConfig, now time.Time) CheckResult {
	start := time.Now()
	cr := CheckResult{
		Check:       CheckThermalPower,
		Mode:        cfg.Mode,
		EvaluatedAt: now.UTC(),
		InputValue:  map[string]any{"node": node.NodeName},
		Threshold:   map[string]any{},
	}
	if cfg.Mode == ModeOff {
		cr.Result = ResultUNEVALUATED
		cr.Code = "check_disabled"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	tempLim := cfg.MaxGPUTempC
	gpuPowLim := cfg.MaxGPUPowerW
	srvPowLim := cfg.MaxServerPowerW
	cr.Threshold["max_gpu_temp_c"] = nilOrFloat(tempLim)
	cr.Threshold["max_gpu_power_w"] = nilOrFloat(gpuPowLim)
	cr.Threshold["max_server_power_w"] = nilOrFloat(srvPowLim)

	if tempLim == nil && gpuPowLim == nil && srvPowLim == nil {
		cr.Result = ResultUNEVALUATED
		cr.Code = "limits_unset"
		cr.Detail = "max_gpu_temp_c / max_gpu_power_w / max_server_power_w all null"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	var parts []map[string]any
	var firstReject, firstUneval *CheckResult
	checked := 0

	if tempLim != nil || gpuPowLim != nil {
		gpuCount := 0
		for _, d := range node.Devices {
			if d.Kind != "gpu" {
				continue
			}
			gpuCount++
			entry := map[string]any{"device_uuid": d.UUID, "kind": d.Kind}

			if tempLim != nil {
				checked++
				sub := evaluateDeviceLimit(d.TemperatureC, "temperature_c", "C", *tempLim, freshCfg, now)
				entry["temperature"] = sub
				applyLimitOutcome(&firstReject, &firstUneval, cr, cfg, sub, "gpu_temp_exceeded")
			}
			if gpuPowLim != nil {
				checked++
				sub := evaluateDeviceLimit(d.PowerW, "power_w", "W", *gpuPowLim, freshCfg, now)
				entry["gpu_power"] = sub
				applyLimitOutcome(&firstReject, &firstUneval, cr, cfg, sub, "gpu_power_exceeded")
			}
			parts = append(parts, entry)
		}
		cr.InputValue["gpu_device_count"] = gpuCount
		if gpuCount == 0 && (tempLim != nil || gpuPowLim != nil) {
			u := cr
			u.Result = ResultUNEVALUATED
			u.Code = "no_gpu_telemetry"
			u.Detail = "GPU temp/power limit set but node has no gpu devices in snapshot"
			if firstUneval == nil && firstReject == nil {
				firstUneval = &u
			}
		}
	}

	if srvPowLim != nil {
		checked++
		sp := map[string]any{"limit_w": *srvPowLim}
		if node.ServerPower == nil {
			sp["code"] = "server_power_missing"
			parts = append(parts, map[string]any{"server_power": sp})
			u := cr
			u.Result = ResultUNEVALUATED
			u.Code = "server_power_missing"
			u.Detail = "max_server_power_w set but no server_power telemetry"
			if firstUneval == nil {
				firstUneval = &u
			}
		} else if node.ServerPower.NodeName != "" && node.ServerPower.NodeName != node.NodeName {
			sp["code"] = "server_power_node_mismatch"
			sp["server_node"] = node.ServerPower.NodeName
			parts = append(parts, map[string]any{"server_power": sp})
			u := cr
			u.Result = ResultUNEVALUATED
			u.Code = "server_power_node_mismatch"
			u.Detail = fmt.Sprintf("server_power.node_name %q != parent %q", node.ServerPower.NodeName, node.NodeName)
			if firstUneval == nil {
				firstUneval = &u
			}
		} else if !node.ServerPower.AgentTimeOK && !node.ServerPower.AgentTime.IsZero() {
			sp["code"] = "invalid_source_time"
			sp["detail"] = "Agent.Time marked invalid (e.g. 1970)"
			parts = append(parts, map[string]any{"server_power": sp})
			u := cr
			u.Result = ResultUNEVALUATED
			u.Code = "invalid_source_time"
			u.Detail = "server power Agent.Time invalid; not compared to limit"
			if firstUneval == nil {
				firstUneval = &u
			}
		} else {
			sub := evaluateDeviceLimit(node.ServerPower.PowerW, "server_power_w", "W", *srvPowLim, freshCfg, now)
			sp["reading"] = sub
			parts = append(parts, map[string]any{"server_power": sp})
			applyLimitOutcome(&firstReject, &firstUneval, cr, cfg, sub, "server_power_exceeded")
		}
	}

	cr.InputValue["parts"] = parts
	cr.InputValue["limits_checked"] = checked
	_ = checked

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
		cr.Code = "within_limits"
		cr.Detail = "configured thermal/power readings within caps"
	}
	cr.DurationNS = time.Since(start).Nanoseconds()
	return cr
}

type limitReading struct {
	OK     bool
	Code   string
	Detail string
	Value  *float64
	Limit  float64
	Field  string
}

func evaluateDeviceLimit(tv TimedValue, field, unit string, limit float64, freshCfg CheckConfig, now time.Time) limitReading {
	out := limitReading{Field: field, Limit: limit}
	if !floatIsFinite(limit) {
		out.Code = "limit_nonfinite"
		out.Detail = "configured limit is NaN or Inf"
		return out
	}
	fr := EvaluateTimedValue(tv, field, freshCfg, now)
	if !fr.OK {
		out.Code = fr.Code
		out.Detail = fr.Detail
		return out
	}
	if unit != "" && tv.Unit != "" && !unitMatches(tv.Unit, unit) {
		out.Code = "unit_mismatch"
		out.Detail = fmt.Sprintf("unit %q incompatible with %s", tv.Unit, unit)
		return out
	}
	v := tv.Value
	if !floatIsFinite(v) {
		out.Code = "measurement_nonfinite"
		out.Detail = fmt.Sprintf("%s is NaN or Inf", field)
		return out
	}
	// Power must be non-negative; temperature may be negative °C.
	if isPowerField(field) && v < 0 {
		out.Code = "measurement_negative"
		out.Detail = fmt.Sprintf("%s=%v is negative", field, v)
		return out
	}
	out.Value = &v
	if v > limit {
		out.Code = "exceeded"
		out.Detail = fmt.Sprintf("%s=%v > limit %v", field, v, limit)
		return out
	}
	out.OK = true
	out.Code = "within_limit"
	return out
}

func isPowerField(field string) bool {
	switch field {
	case "power_w", "server_power_w", "gpu_power_w":
		return true
	default:
		return strings.HasSuffix(field, "power_w")
	}
}

func unitMatches(got, want string) bool {
	g := strings.ToLower(strings.TrimSpace(got))
	w := strings.ToLower(strings.TrimSpace(want))
	if g == w {
		return true
	}
	aliases := map[string]string{"celsius": "c", "watt": "w", "watts": "w"}
	if a, ok := aliases[g]; ok {
		g = a
	}
	if a, ok := aliases[w]; ok {
		w = a
	}
	return g == w
}

func applyLimitOutcome(firstReject, firstUneval **CheckResult, base CheckResult, cfg CheckConfig, sub limitReading, rejectCode string) {
	if sub.OK {
		return
	}
	if sub.Code == "exceeded" {
		if *firstReject != nil {
			return
		}
		rej := base
		rej.Result = ResultREJECT
		rej.Code = rejectCode
		rej.Detail = sub.Detail
		rej.Enforced = cfg.Mode == ModeEnforce
		*firstReject = &rej
		return
	}
	if *firstUneval != nil || *firstReject != nil {
		return
	}
	u := base
	u.Result = ResultUNEVALUATED
	u.Code = sub.Code
	u.Detail = sub.Detail
	*firstUneval = &u
}

func nilOrFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
