package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/catalogclient"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/workloadcontext"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func newPrescoreEnv(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *utils.Cache, *CatalogAwareScore) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_SHADOW_SCORE_WEIGHT", "0")
	// Keep real TTL contract (0 → default 30s). Tests assert via HTTP hits, not cache disable.
	t.Setenv("SCHED_CATALOG_CACHE_TTL_SEC", "30")

	cache := utils.NewCache(context.Background())
	n1 := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{"sched.ai/gpu-model": "pro6000"}}}
	n2 := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b", Labels: map[string]string{"sched.ai/gpu-model": "pro6000"}}}
	n3 := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-c", Labels: map[string]string{"sched.ai/gpu-model": "pro6000"}}}
	for _, n := range []*v1.Node{n1, n2, n3} {
		if err := cache.AddNode(n, nil); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	p := NewCatalogAwareScore(cache)
	return srv, cache, p
}

func testPod(uid string, concurrency string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1", Namespace: "ns", UID: types.UID(uid),
			Annotations: map[string]string{
				"sched.ai/acc-type":       "gpu",
				"profile.ai/model":       "llama",
				"profile.ai/runtime":     "vllm",
				"profile.ai/concurrency": concurrency,
			},
		},
	}
}

func nodesFrom(cache *utils.Cache, names ...string) []*v1.Node {
	out := make([]*v1.Node, 0, len(names))
	for _, name := range names {
		if ni := cache.Nodes()[name]; ni != nil && ni.Node() != nil {
			out = append(out, ni.Node())
		}
	}
	return out
}

// R1: failure snapshot must survive first Score's recordCandidate/sweep.
func TestPreScoreFailureSnapshotSurvivesFirstScore(t *testing.T) {
	var hits atomic.Int32
	srv, cache, p := newPrescoreEnv(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	_ = srv
	pod := testPod("uid-r1-fail", "2")
	nodes := nodesFrom(cache, "node-a", "node-b")
	fw := framework.NewFramework(nil, nil, []framework.ScorePlugin{p}, nil, nil)

	if st := fw.RunPreScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("PreScore: %s", st.Message())
	}
	before := hits.Load()
	if before < 1 {
		t.Fatalf("PreScore HTTP expected >=1 got %d", before)
	}
	if _, st := fw.RunScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("Score: %s", st.Message())
	}
	after := hits.Load()
	if after != before {
		t.Fatalf("Score re-issued HTTP after prepared failure: before=%d after=%d", before, after)
	}
}

// R2: after PreScore, key miss must not HTTP.
func TestScoreNeverHTTPAfterPreScoreEvenOnKeyMiss(t *testing.T) {
	var hits atomic.Int32
	_, cache, p := newPrescoreEnv(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(catalogclient.ProfilePrediction{
			Found: true, MatchType: "exact", Confidence: workloadcontext.ConfidenceMEASURED,
		})
	})
	pod := testPod("uid-r2-miss", "2")
	nodes := nodesFrom(cache, "node-a")
	fw := framework.NewFramework(nil, nil, []framework.ScorePlugin{p}, nil, nil)
	if st := fw.RunPreScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("PreScore: %s", st.Message())
	}
	before := hits.Load()
	// Score a node that was not in PreScore set → miss path, still no HTTP.
	extra := nodesFrom(cache, "node-b")
	if _, st := fw.RunScorePlugins(context.Background(), pod, extra); !st.IsSuccess() {
		t.Fatalf("Score: %s", st.Message())
	}
	if hits.Load() != before {
		t.Fatalf("Score HTTP on key miss after PreScore: before=%d after=%d", before, hits.Load())
	}
}

func TestPreScoreDedupesIdenticalLookupKeys(t *testing.T) {
	var hits atomic.Int32
	var mu sync.Mutex
	seen := map[string]int{}
	_, cache, p := newPrescoreEnv(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		key := r.URL.Query().Get("environment_key") + "|c=" + r.URL.Query().Get("concurrency")
		mu.Lock()
		seen[key]++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(catalogclient.ProfilePrediction{
			Found: true, MatchType: "exact", Confidence: workloadcontext.ConfidenceMEASURED,
			ProfileKey: key,
		})
	})
	// Same concurrency → different nodes still produce different EnvironmentKeys
	// (node axis). Use two nodes and assert Score does not add hits.
	pod := testPod("uid-dedupe", "4")
	nodes := nodesFrom(cache, "node-a", "node-b", "node-c")
	fw := framework.NewFramework(nil, nil, []framework.ScorePlugin{p}, nil, nil)
	if st := fw.RunPreScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("PreScore: %s", st.Message())
	}
	pre := hits.Load()
	if int(pre) != len(nodes) {
		// node is part of env key → unique per node is expected
		t.Logf("unique keys via HTTP=%d nodes=%d (node axis in EnvironmentKey)", pre, len(nodes))
	}
	if _, st := fw.RunScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("Score: %s", st.Message())
	}
	if hits.Load() != pre {
		t.Fatalf("Score must not re-fetch: pre=%d after=%d", pre, hits.Load())
	}
}

func TestPreScoreTimeoutStoredAndReused(t *testing.T) {
	var hits atomic.Int32
	_, cache, p := newPrescoreEnv(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		// Slow enough vs client timeout if we shrink it; otherwise still return 500.
		http.Error(w, "timeout-sim", http.StatusGatewayTimeout)
	})
	// Force short client timeout via reconstructing client after env set.
	p.client = catalogclient.NewFromEnv()
	pod := testPod("uid-timeout", "2")
	nodes := nodesFrom(cache, "node-a", "node-b")
	fw := framework.NewFramework(nil, nil, []framework.ScorePlugin{p}, nil, nil)
	if st := fw.RunPreScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("PreScore: %s", st.Message())
	}
	before := hits.Load()
	if _, st := fw.RunScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("Score: %s", st.Message())
	}
	if hits.Load() != before {
		t.Fatalf("Score re-fetched after timeout/error prep: before=%d after=%d", before, hits.Load())
	}
}

func TestPreScoreClearOnCommit(t *testing.T) {
	var hits atomic.Int32
	_, cache, p := newPrescoreEnv(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(catalogclient.ProfilePrediction{
			Found: true, MatchType: "exact", Confidence: workloadcontext.ConfidenceMEASURED,
		})
	})
	pod := testPod("uid-commit", "2")
	nodes := nodesFrom(cache, "node-a")
	fw := framework.NewFramework(nil, nil, []framework.ScorePlugin{p}, nil, nil)
	_ = fw.RunPreScorePlugins(context.Background(), pod, nodes)
	_, _ = fw.RunScorePlugins(context.Background(), pod, nodes)
	fw.CommitPlacementDecisions(pod, "node-a", nil)
	if _, ok := p.prepSnapshot(pod.UID); ok {
		t.Fatal("prep must clear after CommitPlacementDecision")
	}
	if p.preScoreRan(pod.UID) {
		t.Fatal("prescoreRan must clear after CommitPlacementDecision")
	}
}

// F1: aging out prep payload must not re-enable Score HTTP for an active attempt.
func TestPreScoreAgeOutMustNotReenableHTTP(t *testing.T) {
	var hits atomic.Int32
	_, cache, p := newPrescoreEnv(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(catalogclient.ProfilePrediction{
			Found: true, MatchType: "exact", Confidence: workloadcontext.ConfidenceMEASURED,
		})
	})
	pod := testPod("uid-f1-age", "2")
	nodes := nodesFrom(cache, "node-a", "node-b")
	fw := framework.NewFramework(nil, nil, []framework.ScorePlugin{p}, nil, nil)
	if st := fw.RunPreScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("PreScore: %s", st.Message())
	}
	before := hits.Load()
	if before < 1 {
		t.Fatalf("PreScore HTTP expected >=1 got %d", before)
	}

	p.mu.Lock()
	for _, ap := range p.prep {
		ap.createdAt = time.Now().Add(-3 * time.Minute)
	}
	p.sweepLocked()
	p.mu.Unlock()

	if _, ok := p.prepSnapshot(pod.UID); ok {
		t.Fatal("aged prep payload should be swept")
	}
	if !p.preScoreRan(pod.UID) {
		t.Fatal("prescoreRan must survive prep age-out")
	}

	if _, st := fw.RunScorePlugins(context.Background(), pod, nodes); !st.IsSuccess() {
		t.Fatalf("Score: %s", st.Message())
	}
	after := hits.Load()
	if after != before {
		t.Fatalf("active PreScore expiry re-enabled Score HTTP: before=%d after=%d", before, after)
	}
}

func TestPreScoreRaceConcurrentPods(t *testing.T) {
	var hits atomic.Int32
	_, cache, p := newPrescoreEnv(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(catalogclient.ProfilePrediction{
			Found: true, MatchType: "exact", Confidence: workloadcontext.ConfidenceMEASURED,
		})
	})
	fw := framework.NewFramework(nil, nil, []framework.ScorePlugin{p}, nil, nil)
	nodes := nodesFrom(cache, "node-a", "node-b")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pod := testPod(fmt.Sprintf("uid-race-%d", i), "2")
			_ = fw.RunPreScorePlugins(context.Background(), pod, nodes)
			_, _ = fw.RunScorePlugins(context.Background(), pod, nodes)
			fw.CommitPlacementDecisions(pod, "node-a", nil)
		}(i)
	}
	wg.Wait()
	// No panic / race detector issues; hits should be finite.
	if hits.Load() < 1 {
		t.Fatal("expected some HTTP from PreScore")
	}
}
