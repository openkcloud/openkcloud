package placementchecks

import (
	"math"
	"time"
)

// EvaluateMemory is check 2: per-device VRAM headroom.
// PASS only when at least req.Count *distinct* devices each have
// (total - used) >= requestMiB + safetyMargin.
// Node-sum free memory is never used as a substitute.
func EvaluateMemory(node NodeTelemetry, req ParsedRequest, cfg CheckConfig, freshCfg CheckConfig, now time.Time) CheckResult {
	start := time.Now()
	cr := CheckResult{
		Check:       CheckMemory,
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

	if req.GPUCount > 0 && req.NPUCount > 0 {
		cr.Result = ResultUNEVALUATED
		cr.Code = "mixed_accelerator_request"
		cr.Detail = "GPU+NPU in one request is out of P2 scope; not evaluated as GPU-only PASS"
		cr.InputValue["gpu_count"] = req.GPUCount
		cr.InputValue["npu_count"] = req.NPUCount
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	kind := ""
	var needCount int64
	var budget *int64
	var budgetErr string
	switch {
	case req.GPUCount > 0:
		kind = "gpu"
		budget, needCount, budgetErr = req.MemoryBudgetFor("gpu")
	case req.NPUCount > 0:
		kind = "npu"
		budget, needCount, budgetErr = req.MemoryBudgetFor("npu")
	default:
		cr.Result = ResultUNEVALUATED
		cr.Code = "no_accelerator_request"
		cr.Detail = "no positive gpu/npu device count on pod"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	cr.InputValue["kind"] = kind
	cr.InputValue["device_count_requested"] = needCount

	if budgetErr != "" {
		cr.Result = ResultUNEVALUATED
		cr.Code = budgetErr
		cr.Detail = "memory annotation invalid"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if budget == nil {
		cr.Result = ResultUNEVALUATED
		cr.Code = "memory_annotation_missing"
		cr.Detail = "sched.ai/*-memory-mib not set; no default"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	cr.InputValue["requested_memory_mib"] = *budget

	if cfg.MemorySafetyMarginMiB == nil {
		cr.Result = ResultUNEVALUATED
		cr.Code = "safety_margin_unset"
		cr.Detail = "memory_safety_margin_mib is null"
		cr.Threshold["safety_margin_mib"] = nil
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	margin := *cfg.MemorySafetyMarginMiB
	cr.Threshold["safety_margin_mib"] = margin

	needFree, okAdd := checkedAddInt64(*budget, margin)
	if !okAdd {
		cr.Result = ResultUNEVALUATED
		cr.Code = "request_margin_overflow"
		cr.Detail = "requested_memory_mib + safety_margin_mib overflows int64"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	cr.InputValue["required_free_mib"] = needFree

	var devices []map[string]any
	fit := 0
	uneval := 0
	seenUUID := map[string]int{}
	duplicateUUID := false
	crossNode := false

	for _, d := range node.Devices {
		if d.Kind != kind {
			continue
		}
		entry := map[string]any{
			"device_uuid": d.UUID,
			"model":       d.Model,
			"source_total": d.MemoryTotalMiB.Source,
			"source_used":  d.MemoryUsedMiB.Source,
		}
		if !d.MemoryTotalMiB.CollectedAt.IsZero() {
			entry["collected_at_total"] = d.MemoryTotalMiB.CollectedAt.UTC().Format(time.RFC3339Nano)
		}
		if !d.MemoryUsedMiB.CollectedAt.IsZero() {
			entry["collected_at_used"] = d.MemoryUsedMiB.CollectedAt.UTC().Format(time.RFC3339Nano)
		}

		if d.NodeName != "" && d.NodeName != node.NodeName {
			crossNode = true
			uneval++
			entry["fresh"] = false
			entry["fresh_code"] = "device_node_mismatch"
			devices = append(devices, entry)
			continue
		}
		seenUUID[d.UUID]++
		if seenUUID[d.UUID] > 1 {
			duplicateUUID = true
			uneval++
			entry["fresh"] = false
			entry["fresh_code"] = "duplicate_device_uuid"
			entry["fits"] = false
			devices = append(devices, entry)
			continue // do not count the same UUID twice
		}

		ok, code := MemoryFieldsFresh(d, freshCfg, now)
		if !ok {
			uneval++
			entry["fresh"] = false
			entry["fresh_code"] = code
			devices = append(devices, entry)
			continue
		}
		total, okT := floatToInt64MiB(d.MemoryTotalMiB.Value)
		used, okU := floatToInt64MiB(d.MemoryUsedMiB.Value)
		if !okT || !okU {
			uneval++
			entry["fresh"] = false
			entry["fresh_code"] = "memory_value_out_of_range"
			devices = append(devices, entry)
			continue
		}
		if used > total {
			uneval++
			entry["fresh"] = false
			entry["fresh_code"] = "used_exceeds_total"
			entry["memory_total_mib"] = total
			entry["memory_used_mib"] = used
			devices = append(devices, entry)
			continue
		}
		free := total - used
		entry["memory_total_mib"] = total
		entry["memory_used_mib"] = used
		entry["memory_free_mib"] = free
		entry["fresh"] = true
		if d.UtilizationPCT.Present {
			entry["utilization_pct"] = d.UtilizationPCT.Value
		} else {
			entry["utilization_pct"] = nil
		}
		if free >= needFree {
			fit++
			entry["fits"] = true
		} else {
			entry["fits"] = false
		}
		devices = append(devices, entry)
	}
	cr.InputValue["devices"] = devices
	cr.InputValue["fit_device_count"] = fit

	if duplicateUUID {
		cr.Result = ResultUNEVALUATED
		cr.Code = "duplicate_device_uuid"
		cr.Detail = "same device UUID appears more than once; not counted as multiple devices"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if crossNode {
		cr.Result = ResultUNEVALUATED
		cr.Code = "device_node_mismatch"
		cr.Detail = "device.node_name does not match parent node"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if len(devices) == 0 {
		cr.Result = ResultUNEVALUATED
		cr.Code = "no_devices_of_kind"
		cr.Detail = "telemetry has no devices matching requested kind"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}
	if uneval > 0 && fit < int(needCount) {
		cr.Result = ResultUNEVALUATED
		cr.Code = "telemetry_not_fresh"
		cr.Detail = "one or more devices lack fresh usable memory totals/used"
		cr.DurationNS = time.Since(start).Nanoseconds()
		return cr
	}

	if fit >= int(needCount) {
		cr.Result = ResultPASS
		cr.Code = "memory_headroom_ok"
	} else {
		cr.Result = ResultREJECT
		cr.Code = "memory-headroom"
		cr.Detail = "insufficient per-device free memory for requested count"
	}
	cr.Enforced = cr.Result == ResultREJECT && cfg.Mode == ModeEnforce
	cr.DurationNS = time.Since(start).Nanoseconds()
	if ca := firstCollected(node, kind); ca != nil {
		cr.CollectedAt = ca
	}
	return cr
}

func checkedAddInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func floatToInt64MiB(v float64) (int64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if v < 0 || v > float64(math.MaxInt64) {
		return 0, false
	}
	n := int64(v)
	if float64(n) != v {
		// Non-integer MiB readings are not accepted as exact budgets.
		return 0, false
	}
	return n, true
}

func firstCollected(node NodeTelemetry, kind string) *time.Time {
	for _, d := range node.Devices {
		if d.Kind != kind {
			continue
		}
		if !d.MemoryUsedMiB.CollectedAt.IsZero() {
			t := d.MemoryUsedMiB.CollectedAt.UTC()
			return &t
		}
	}
	return nil
}
