//go:build e2e

// Runs against a live catalog_api.py instance. Driven by
// scripts/e2e_shadow_score.sh, which seeds the DB and asserts the stored row.
package plugin

import (
	"context"
	"os"
	"strconv"
	"testing"

	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestShadowScoreE2E(t *testing.T) {
	url := os.Getenv("SCHED_CATALOG_URL")
	if url == "" {
		t.Skip("SCHED_CATALOG_URL not set")
	}
	// The E0 gate runs against a catalog holding only a GPU profile, so the
	// expected hit count and recommendation are caller-supplied.
	wantFound, err := strconv.Atoi(envOr("E2E_EXPECT_FOUND", "2"))
	if err != nil {
		t.Fatalf("E2E_EXPECT_FOUND: %v", err)
	}
	wantSelected := envOr("E2E_EXPECT_SELECTED", "npu-node")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	plugin := NewCatalogAwareScore(newCatalogTestCache(t, ctx))
	pod := newCatalogTestPod("e2e-pod")
	// Pointing the probe at a real run's trace lets record_regret.py join the
	// shadow recommendation to the measured outcome.
	if trace := os.Getenv("E2E_TRACE_ID"); trace != "" {
		pod.Annotations["sched.ai/trace-id"] = trace
	}

	for _, node := range []string{"gpu-node", "npu-node"} {
		score, status := plugin.Score(ctx, pod, node)
		if !status.IsSuccess() {
			t.Fatalf("Score(%s) status = %s", node, status.Message())
		}
		if score != 0 {
			t.Fatalf("Score(%s) = %d, want 0 in shadow weight=0", node, score)
		}
	}

	plugin.mu.Lock()
	buf := plugin.buffers[pod.UID]
	var found int
	var best string
	var bestScore float64 = -1
	if buf != nil {
		for _, c := range buf.candidates {
			if c.Found {
				found++
			}
			if c.RawShadowScore > bestScore {
				bestScore, best = c.RawShadowScore, c.Node
			}
		}
	}
	plugin.mu.Unlock()

	// A profile_key mismatch between the Python writer and the Go reader shows
	// up here as a missing hit, which is the whole point of this test.
	if found != wantFound {
		t.Fatalf("catalog hits = %d, want %d; Python and Go profile_key contracts disagree", found, wantFound)
	}
	if best != wantSelected {
		t.Fatalf("shadow recommendation = %s, want %s", best, wantSelected)
	}

	if status := plugin.NormalizeScore(ctx, pod, utils.PluginResultMap{}); !status.IsSuccess() {
		t.Fatalf("NormalizeScore status = %s", status.Message())
	}
	plugin.CommitPlacementDecision(pod, "", nil)
	t.Logf("shadow decision posted to live catalog API (hits=%d, recommended=%s)", found, best)
}
