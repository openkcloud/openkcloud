package workloadcontext_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/workloadcontext"
)

type goldenFile struct {
	Cases []goldenCase `json:"cases"`
}

type goldenCase struct {
	ID             string         `json:"id"`
	Input          map[string]any `json:"input"`
	EnvironmentKey string         `json:"environment_key"`
	LookupC30      string         `json:"lookup_c30"`
	LookupC15      string         `json:"lookup_c15"`
}

func strField(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return workloadcontext.FormatUtil(t)
	default:
		return ""
	}
}

func intField(m map[string]any, k string) int {
	v, ok := m[k]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	default:
		return 0
	}
}

func TestBuildEnvironmentKeyGolden(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), "testdata", "environment_keys_golden.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var gf goldenFile
	if err := json.Unmarshal(raw, &gf); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(gf.Cases) == 0 {
		t.Fatal("no golden cases")
	}
	for _, c := range gf.Cases {
		in := c.Input
		got := workloadcontext.BuildEnvironmentKey(workloadcontext.EnvironmentKeyInput{
			Model:                strField(in, "model"),
			Revision:             strField(in, "revision"),
			Runtime:              strField(in, "runtime"),
			Accelerator:          strField(in, "accelerator"),
			SKU:                  strField(in, "sku"),
			Node:                 strField(in, "node"),
			Precision:            strField(in, "precision"),
			Protocol:             strField(in, "protocol"),
			InputTokens:          intField(in, "input_tokens"),
			OutputTokens:         intField(in, "output_tokens"),
			GPUMemoryUtilization: strField(in, "gpu_memory_utilization"),
			KVBlocks:             strField(in, "kv_blocks"),
			MaxNumSeqs:           strField(in, "max_num_seqs"),
			LSUQuota:             strField(in, "lsu_quota"),
			EngineCount:          strField(in, "engine_count"),
			AllocationMode:       strField(in, "allocation_mode"),
		})
		if got != c.EnvironmentKey {
			t.Fatalf("%s: environment_key\n got %s\nwant %s", c.ID, got, c.EnvironmentKey)
		}
		if c.LookupC30 != "" {
			lk := workloadcontext.BuildCatalogLookupKey(got, 30)
			if lk != c.LookupC30 {
				t.Fatalf("%s: lookup_c30\n got %s\nwant %s", c.ID, lk, c.LookupC30)
			}
		}
		if c.LookupC15 != "" {
			lk := workloadcontext.BuildCatalogLookupKey(got, 15)
			if lk != c.LookupC15 {
				t.Fatalf("%s: lookup_c15\n got %s\nwant %s", c.ID, lk, c.LookupC15)
			}
		}
	}
}
