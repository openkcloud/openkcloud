package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// fakeCatalog answers profile lookups by accelerator: the NPU profile is the
// cheaper one, so a correct implementation must recommend the NPU node.
type fakeCatalog struct {
	mu        sync.Mutex
	decisions []map[string]any
	profiles  int
}

func (f *fakeCatalog) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, key string, energy float64) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"found":true,"match_type":"exact","profile_key":%q,"environment_key":%q,"confidence":"MEASURED","predicted_p99_ms":1500,`+
				`"predicted_duration_s":20,"predicted_net_energy_j":%v,"source":"MEASURED_EXPLORATORY","sla_pass":true}`,
			key, key, energy))
	}
	mux.HandleFunc("/v1/profile", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("profile_key")
		f.mu.Lock()
		f.profiles++
		f.mu.Unlock()

		energy := 900.0
		if strings.Contains(key, ":npu:") || strings.Contains(key, ":npu:") {
			energy = 300.0
		}
		// Also key environment strings that include accelerator=npu
		if strings.Contains(key, ":npu:") || strings.Contains(r.URL.RawQuery, "npu") {
			energy = 300.0
		}
		respond(w, key, energy)
	})
	mux.HandleFunc("/v1/env_lookup", func(w http.ResponseWriter, r *http.Request) {
		env := r.URL.Query().Get("environment_key")
		f.mu.Lock()
		f.profiles++
		f.mu.Unlock()
		energy := 900.0
		if strings.Contains(env, ":npu:") {
			energy = 300.0
		}
		respond(w, env, energy)
	})
	mux.HandleFunc("/v1/placement_decision", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode decision: %v", err)
		}
		f.mu.Lock()
		f.decisions = append(f.decisions, body)
		f.mu.Unlock()
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"decision_id":"d"}`)
	})
	return mux
}

func (f *fakeCatalog) snapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.decisions))
	copy(out, f.decisions)
	return out
}

func newNPUTestNode(name, count string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: v1.NodeStatus{Allocatable: v1.ResourceList{
			v1.ResourceCPU:                     resource.MustParse("16"),
			v1.ResourceMemory:                  resource.MustParse("64Gi"),
			v1.ResourceName("furiosa.ai/rngd"): resource.MustParse(count),
		}},
	}
}

func newCatalogTestCache(t *testing.T, ctx context.Context) *utils.Cache {
	t.Helper()
	cache := utils.NewCache(ctx)
	for _, node := range []*v1.Node{
		newAcceleratorTestNode("gpu-node", "NVIDIA-L4", "1"),
		newNPUTestNode("npu-node", "1"),
	} {
		if err := cache.AddNode(node, nil); err != nil {
			t.Fatalf("AddNode(%s) error = %v", node.Name, err)
		}
	}
	return cache
}

func newCatalogTestPod(name string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name + "-uid"),
			Annotations: map[string]string{
				"sched.ai/trace-id":       name + "-trace",
				"sched.ai/routing-policy": "g3-npu-cap-overflow",
				"profile.ai/model-gpu":    "meta-llama/Llama-3.1-8B-Instruct",
				"profile.ai/model-npu":    "furiosa-ai/Llama-3.1-8B-Instruct",
				"profile.ai/runtime-gpu":  "vllm",
				"profile.ai/runtime-npu":  "furiosa-llm",
				"profile.ai/slo-p99-ms":   "2115",
				"profile.ai/concurrency":  "5",
			},
		},
	}
}

// constantScore gives every node a distinct, deterministic score so that a
// ranking change caused by the catalog plugin is detectable.
type constantScore struct{ scores map[string]int64 }

func (c *constantScore) Name() string { return "ConstantScore" }
func (c *constantScore) Score(_ context.Context, _ *v1.Pod, nodeName string) (int64, *utils.Status) {
	return c.scores[nodeName], utils.NewStatus(utils.Success, "")
}
func (c *constantScore) ScoreExtensions() framework.ScoreExtensions { return nil }

func testNodes() []*v1.Node {
	return []*v1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "npu-node"}},
	}
}

func TestCatalogAwareScoreShadowKeepsBindRanking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeCatalog{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	t.Setenv("SCHED_SHADOW_SCORE_WEIGHT", "0")
	t.Setenv("SCHED_CATALOG_TIMEOUT_MS", "2000")

	cache := newCatalogTestCache(t, ctx)
	pod := newCatalogTestPod("shadow-pod")
	baseline := &constantScore{scores: map[string]int64{"gpu-node": 40, "npu-node": 10}}

	withoutCatalog := framework.NewFramework(nil, nil,
		[]framework.ScorePlugin{baseline}, nil, nil)
	wantScores, status := withoutCatalog.RunScorePlugins(ctx, pod, testNodes())
	if !status.IsSuccess() {
		t.Fatalf("baseline RunScorePlugins status = %s", status.Message())
	}

	withCatalog := framework.NewFramework(nil, nil,
		[]framework.ScorePlugin{baseline, NewCatalogAwareScore(cache)}, nil, nil)
	gotScores, status := withCatalog.RunScorePlugins(ctx, pod, testNodes())
	if !status.IsSuccess() {
		t.Fatalf("catalog RunScorePlugins status = %s", status.Message())
	}
	host, err := utils.SelectPreferredNode(gotScores)
	withCatalog.CommitPlacementDecisions(pod, host, err)

	for _, node := range []string{"gpu-node", "npu-node"} {
		if gotScores[node].TotalNodeScore != wantScores[node].TotalNodeScore {
			t.Fatalf("node %s total = %d with catalog, %d without — shadow must not change Bind",
				node, gotScores[node].TotalNodeScore, wantScores[node].TotalNodeScore)
		}
	}
	if gotScores["gpu-node"].TotalNodeScore <= gotScores["npu-node"].TotalNodeScore {
		t.Fatalf("baseline ranking lost: gpu=%d npu=%d",
			gotScores["gpu-node"].TotalNodeScore, gotScores["npu-node"].TotalNodeScore)
	}

	decisions := fake.snapshot()
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want exactly 1 per scheduling cycle", len(decisions))
	}
	d := decisions[0]
	cands, ok := d["candidates_json"].([]any)
	if !ok || len(cands) != 2 {
		t.Fatalf("candidates_json = %v, want 2 candidates", d["candidates_json"])
	}
	// The NPU profile is cheaper, so shadow must recommend it even though Bind
	// still follows the baseline ranking that prefers the GPU node.
	if d["selected_shadow"] != "npu-node" {
		t.Fatalf("selected_shadow = %v, want npu-node", d["selected_shadow"])
	}
	if _, ok := d["selected_enforced"]; ok {
		t.Fatalf("selected_enforced present in shadow mode: %v", d["selected_enforced"])
	}
	if _, ok := d["decision_branch"]; ok {
		t.Fatal("decision_branch must stay unset in shadow mode")
	}
	if d["trace_id"] != "shadow-pod-trace" {
		t.Fatalf("trace_id = %v, want shadow-pod-trace", d["trace_id"])
	}
	if d["routing_policy"] != "g3-npu-cap-overflow" {
		t.Fatalf("routing_policy = %v", d["routing_policy"])
	}
}

func TestCatalogAwareScoreRawScoreComputedAtZeroWeight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeCatalog{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	t.Setenv("SCHED_SHADOW_SCORE_WEIGHT", "0")
	t.Setenv("SCHED_CATALOG_TIMEOUT_MS", "2000")

	plugin := NewCatalogAwareScore(newCatalogTestCache(t, ctx))
	pod := newCatalogTestPod("raw-pod")

	for _, node := range []string{"gpu-node", "npu-node"} {
		score, status := plugin.Score(ctx, pod, node)
		if !status.IsSuccess() {
			t.Fatalf("Score(%s) status = %s", node, status.Message())
		}
		if score != 0 {
			t.Fatalf("Score(%s) = %d, want 0 at weight=0", node, score)
		}
	}
	if status := plugin.NormalizeScore(ctx, pod, utils.PluginResultMap{}); !status.IsSuccess() {
		t.Fatalf("NormalizeScore status = %s", status.Message())
	}
	plugin.CommitPlacementDecision(pod, "", nil)

	d := fake.snapshot()[0]
	comps, _ := d["score_components_json"].(map[string]any)
	raw, _ := comps["raw_shadow_score"].(float64)
	if raw <= 0 {
		t.Fatalf("raw_shadow_score = %v, want > 0 even when returned score is 0", comps["raw_shadow_score"])
	}
}

func TestCatalogAwareScoreEnforceAppliesWeight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeCatalog{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "enforce")
	t.Setenv("SCHED_SHADOW_SCORE_WEIGHT", "1")
	t.Setenv("SCHED_CATALOG_TIMEOUT_MS", "2000")

	cache := newCatalogTestCache(t, ctx)
	pod := newCatalogTestPod("enforce-pod")
	baseline := &constantScore{scores: map[string]int64{"gpu-node": 40, "npu-node": 10}}
	fw := framework.NewFramework(nil, nil,
		[]framework.ScorePlugin{baseline, NewCatalogAwareScore(cache)}, nil, nil)
	got, status := fw.RunScorePlugins(ctx, pod, testNodes())
	if !status.IsSuccess() {
		t.Fatalf("RunScorePlugins: %s", status.Message())
	}
	// Cost-first: additive Catalog score stays 0; energy on PluginResult drives Bind.
	host, err := utils.SelectPreferredNode(got)
	fw.CommitPlacementDecisions(pod, host, err)
	if err != nil || host != "npu-node" {
		t.Fatalf("SelectPreferredNode = %q (%v), want npu-node (cheaper energy among SLA-pass)", host, err)
	}
	if d := fake.snapshot()[0]; d["selected_enforced"] != "npu-node" {
		t.Fatalf("selected_enforced = %v, want npu-node", d["selected_enforced"])
	}
	if d := fake.snapshot()[0]; d["selected_enforced"] != host {
		t.Fatalf("selected_enforced %v != SelectPreferredNode %s", d["selected_enforced"], host)
	}
}

func TestCatalogAwareScoreEnforceSkipsLowConfidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/profile" {
			_, _ = io.WriteString(w,
				`{"found":true,"confidence":"REFERENCE","predicted_net_energy_j":100}`)
			return
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"decision_id":"d"}`)
	}))
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "enforce")
	t.Setenv("SCHED_SHADOW_SCORE_WEIGHT", "1")
	t.Setenv("SCHED_PREDICTION_MIN_CONFIDENCE", "MEASURED")
	t.Setenv("SCHED_CATALOG_TIMEOUT_MS", "2000")

	plugin := NewCatalogAwareScore(newCatalogTestCache(t, ctx))
	score, _ := plugin.Score(ctx, newCatalogTestPod("lowconf-pod"), "gpu-node")
	if score != 0 {
		t.Fatalf("score = %d, want 0 for REFERENCE below MEASURED floor", score)
	}
}

func TestCatalogAwareScoreOffModeIsSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeCatalog{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "off")

	plugin := NewCatalogAwareScore(newCatalogTestCache(t, ctx))
	pod := newCatalogTestPod("off-pod")
	if score, _ := plugin.Score(ctx, pod, "gpu-node"); score != 0 {
		t.Fatalf("score = %d, want 0", score)
	}
	if status := plugin.NormalizeScore(ctx, pod, utils.PluginResultMap{}); !status.IsSuccess() {
		t.Fatal("NormalizeScore should succeed in off mode")
	}
	if got := fake.snapshot(); len(got) != 0 {
		t.Fatalf("decisions = %d, want 0 in off mode", len(got))
	}
}

func TestCatalogAwareScoreConcurrentCyclesStayIsolated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeCatalog{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	t.Setenv("SCHED_SHADOW_SCORE_WEIGHT", "0")
	t.Setenv("SCHED_CATALOG_TIMEOUT_MS", "2000")

	plugin := NewCatalogAwareScore(newCatalogTestCache(t, ctx))

	const cycles = 8
	var wg sync.WaitGroup
	for i := 0; i < cycles; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pod := newCatalogTestPod(fmt.Sprintf("pod-%d", i))
			for _, node := range []string{"gpu-node", "npu-node"} {
				if _, status := plugin.Score(ctx, pod, node); !status.IsSuccess() {
					t.Errorf("Score status = %s", status.Message())
				}
			}
			if status := plugin.NormalizeScore(ctx, pod, utils.PluginResultMap{}); !status.IsSuccess() {
				t.Errorf("NormalizeScore status = %s", status.Message())
			}
			plugin.CommitPlacementDecision(pod, "", nil)
		}(i)
	}
	wg.Wait()

	decisions := fake.snapshot()
	if len(decisions) != cycles {
		t.Fatalf("decisions = %d, want %d (one per cycle)", len(decisions), cycles)
	}
	seen := map[string]bool{}
	for _, d := range decisions {
		trace, _ := d["trace_id"].(string)
		if seen[trace] {
			t.Fatalf("duplicate trace_id %q — buffers leaked across cycles", trace)
		}
		seen[trace] = true
		if cands, ok := d["candidates_json"].([]any); !ok || len(cands) != 2 {
			t.Fatalf("trace %s candidates = %v, want exactly 2", trace, d["candidates_json"])
		}
	}

	plugin.mu.Lock()
	remaining := len(plugin.buffers)
	pendingLeft := len(plugin.pending)
	plugin.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("buffers remaining = %d, want 0 after cleanup", remaining)
	}
	if pendingLeft != 0 {
		t.Fatalf("pending remaining = %d, want 0 after commit", pendingLeft)
	}
}
