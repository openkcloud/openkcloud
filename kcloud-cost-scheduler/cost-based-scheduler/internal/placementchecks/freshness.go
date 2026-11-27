package placementchecks

import (
	"time"
)

// Default freshness floor when config leaves EarliestValidUnix unset.
// Keep in sync with DefaultConfig(); EvaluateTimedValue must not invent a
// different floor when the config pointer is non-nil.
const defaultEarliestValidUnix int64 = 946684800 // 2000-01-01 UTC

// FieldFreshness evaluates one TimedValue against freshness config.
type FieldFreshness struct {
	Field  string
	OK     bool
	Code   string
	Detail string
}

// EvaluateTimedValue checks presence, source clock, future skew, and max age.
// Age and validity use CollectedAt (instrument/source). ReceivedAt is never a
// substitute for a missing or stale CollectedAt.
func EvaluateTimedValue(tv TimedValue, field string, cfg CheckConfig, now time.Time) FieldFreshness {
	if !tv.Present {
		return FieldFreshness{Field: field, OK: false, Code: "missing_value", Detail: "value not present"}
	}
	if tv.CollectedAt.IsZero() {
		return FieldFreshness{
			Field:  field,
			OK:     false,
			Code:   "missing_source_time",
			Detail: "collected_at required; received_at is not a source substitute",
		}
	}
	src := tv.CollectedAt

	earliestUnix := defaultEarliestValidUnix
	if cfg.EarliestValidUnix != nil {
		earliestUnix = *cfg.EarliestValidUnix
	}
	earliest := time.Unix(earliestUnix, 0).UTC()
	if !src.After(earliest) {
		return FieldFreshness{
			Field:  field,
			OK:     false,
			Code:   "invalid_source_time",
			Detail: "source clock at or before earliest_valid (e.g. NETIO 1970)",
		}
	}

	skewSec := int64(300)
	if cfg.MaxFutureSkewSeconds != nil {
		skewSec = *cfg.MaxFutureSkewSeconds
	}
	if src.After(now.Add(time.Duration(skewSec) * time.Second)) {
		return FieldFreshness{Field: field, OK: false, Code: "future_timestamp", Detail: "source time too far ahead of now"}
	}

	if cfg.MaxAgeSeconds == nil {
		return FieldFreshness{Field: field, OK: false, Code: "max_age_unset", Detail: "max_age_seconds is null"}
	}
	age := now.Sub(src)
	if age > time.Duration(*cfg.MaxAgeSeconds)*time.Second {
		return FieldFreshness{Field: field, OK: false, Code: "expired", Detail: "collected_at older than max_age_seconds"}
	}
	if age < -time.Duration(skewSec)*time.Second {
		return FieldFreshness{Field: field, OK: false, Code: "future_timestamp", Detail: "collected_at in the future"}
	}
	return FieldFreshness{Field: field, OK: true, Code: "fresh"}
}

// EvaluateFreshness is check 6 for a node: field-level validity of memory (and
// optional server power) used by dependent checks.
func EvaluateFreshness(node NodeTelemetry, cfg CheckConfig, now time.Time) CheckResult {
	start := time.Now()
	cr := CheckResult{
		Check:       CheckFreshness,
		Mode:        cfg.Mode,
		EvaluatedAt: now.UTC(),
		InputValue:  map[string]any{"node": node.NodeName, "device_count": len(node.Devices)},
		Threshold:   map[string]any{},
	}
	if cfg.MaxAgeSeconds != nil {
		cr.Threshold["max_age_seconds"] = *cfg.MaxAgeSeconds
	} else {
		cr.Threshold["max_age_seconds"] = nil
	}
	if cfg.Mode == ModeOff {
		cr.Result = ResultUNEVALUATED
		cr.Code = "check_disabled"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	var failures []map[string]any
	allOK := true
	anyField := false

	for _, d := range node.Devices {
		pairs := []struct {
			name string
			tv   TimedValue
		}{
			{"memory_total_mib", d.MemoryTotalMiB},
			{"memory_used_mib", d.MemoryUsedMiB},
		}
		// Include thermal/power fields when present so check 6 covers inputs used by check 5.
		if d.TemperatureC.Present {
			pairs = append(pairs, struct {
				name string
				tv   TimedValue
			}{"temperature_c", d.TemperatureC})
		}
		if d.PowerW.Present {
			pairs = append(pairs, struct {
				name string
				tv   TimedValue
			}{"power_w", d.PowerW})
		}
		for _, pair := range pairs {
			anyField = true
			fr := EvaluateTimedValue(pair.tv, d.UUID+"."+pair.name, cfg, now)
			if !fr.OK {
				allOK = false
				failures = append(failures, map[string]any{
					"device_uuid": d.UUID, "field": pair.name, "code": fr.Code, "detail": fr.Detail,
				})
			}
		}
	}
	if node.ServerPower != nil {
		anyField = true
		if !node.ServerPower.AgentTimeOK && !node.ServerPower.AgentTime.IsZero() {
			allOK = false
			failures = append(failures, map[string]any{
				"field": "server_agent_time", "code": "invalid_source_time",
				"detail": "Agent.Time marked invalid (e.g. 1970)",
			})
		}
		// ServerPower present ⇒ PowerW must be a real measurement (Present=true + fresh).
		fr := EvaluateTimedValue(node.ServerPower.PowerW, "server_power_w", cfg, now)
		if !fr.OK {
			allOK = false
			failures = append(failures, map[string]any{
				"field": "server_power_w", "code": fr.Code, "detail": fr.Detail,
			})
		}
	}

	if !anyField {
		cr.Result = ResultUNEVALUATED
		cr.Code = "no_telemetry_fields"
		cr.Detail = "node has no device/server fields to validate"
	} else if allOK {
		cr.Result = ResultPASS
		cr.Code = "fresh"
	} else {
		cr.Result = ResultUNEVALUATED
		cr.Code = "stale_or_invalid"
		cr.InputValue["failures"] = failures
		if len(failures) > 0 {
			if c, ok := failures[0]["code"].(string); ok {
				cr.Code = c
			}
		}
	}
	cr.DurationNS = time.Since(start).Nanoseconds()
	cr.Enforced = false
	return cr
}

// MemoryFieldsFresh reports whether device memory total/used are usable for check 2.
func MemoryFieldsFresh(d DeviceTelemetry, cfg CheckConfig, now time.Time) (ok bool, code string) {
	if code := memoryUnitCode(d.MemoryTotalMiB); code != "" {
		return false, code
	}
	if code := memoryUnitCode(d.MemoryUsedMiB); code != "" {
		return false, code
	}
	t := EvaluateTimedValue(d.MemoryTotalMiB, "memory_total_mib", cfg, now)
	u := EvaluateTimedValue(d.MemoryUsedMiB, "memory_used_mib", cfg, now)
	if !t.OK {
		return false, t.Code
	}
	if !u.OK {
		return false, u.Code
	}
	return true, "fresh"
}

// memoryUnitCode returns a non-empty code when Unit is set and not MiB.
// Empty Unit is treated as MiB (schema field name).
func memoryUnitCode(tv TimedValue) string {
	if !tv.Present {
		return ""
	}
	u := tv.Unit
	if u == "" || u == "MiB" || u == "mib" || u == "MIB" {
		return ""
	}
	return "memory_unit_mismatch"
}
