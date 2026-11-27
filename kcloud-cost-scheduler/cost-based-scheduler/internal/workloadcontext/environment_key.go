package workloadcontext

import (
	"fmt"
	"strconv"
	"strings"
)

// EnvironmentKeyInput is the shared Catalog environment axis set (Python/Go).
// Concurrency is NOT part of EnvironmentKey; use BuildCatalogLookupKey.
type EnvironmentKeyInput struct {
	Model                 string
	Revision              string
	Runtime               string
	Accelerator           string
	SKU                   string
	Node                  string
	Precision             string
	Protocol              string
	InputTokens           int
	OutputTokens          int
	GPUMemoryUtilization  string // pre-canonical string preferred (e.g. "0.9")
	KVBlocks              string // e.g. "4096" or "kvblocks2048" without prefix
	MaxNumSeqs            string
	LSUQuota              string
	EngineCount           string // default "1"
	AllocationMode        string
}

func axis(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func axisNum(v int) string {
	return strconv.Itoa(v)
}

// FormatUtil matches Python format(v, "g") / Go FormatFloat 'g'.
func FormatUtil(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// BuildEnvironmentKey builds the Catalog environment key (no concurrency).
// Order must match scripts/catalog/workload_context.py::build_environment_key.
func BuildEnvironmentKey(in EnvironmentKeyInput) string {
	model := nz(in.Model, "unknown")
	runtime := nz(in.Runtime, "unknown")
	accel := nz(in.Accelerator, "unknown")
	precision := nz(in.Precision, "unknown")
	engines := axis(in.EngineCount, "1")
	parts := []string{
		model,
		axis(in.Revision, "na"),
		runtime,
		accel,
		axis(in.SKU, "na"),
		axis(in.Node, "na"),
		precision,
		axis(in.Protocol, "na"),
		"in" + axisNum(in.InputTokens),
		"out" + axisNum(in.OutputTokens),
		"util" + axis(in.GPUMemoryUtilization, "na"),
		"kv" + axis(in.KVBlocks, "na"),
		"mns" + axis(in.MaxNumSeqs, "na"),
		"lsu" + axis(in.LSUQuota, "na"),
		"engines" + engines,
		nz(in.AllocationMode, "na"),
	}
	return strings.Join(parts, ":")
}

// BuildCatalogLookupKey appends :cN for measured Score/Catalog lookup.
func BuildCatalogLookupKey(environmentKey string, concurrency int) string {
	if concurrency < 1 {
		concurrency = 1
	}
	return fmt.Sprintf("%s:c%d", environmentKey, concurrency)
}
