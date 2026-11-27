package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
)

type fixtureFile struct {
	Cases []fixtureCase `json:"cases"`
}

type fixtureCase struct {
	ID                       string             `json:"id"`
	Requirement              string             `json:"requirement"`
	BaselineScores           map[string]int64   `json:"baseline_scores"`
	Candidates               []fixtureCandidate `json:"candidates"`
	WantSelectedNode         string             `json:"want_selected_node"`
	WantSelectedEnforcedEmpty bool              `json:"want_selected_enforced_empty"`
	WantSelectError          bool               `json:"want_select_error"`
	WantFailedNode           string             `json:"want_failed_node"`
	WantFailedSLAPassNotTrue bool               `json:"want_failed_sla_pass_not_true"`
	WantFailedPluginScoreZero bool              `json:"want_failed_plugin_score_zero"`
	WantGPUSlaPassCleared    bool               `json:"want_gpu_sla_pass_cleared"`
}

type fixtureCandidate struct {
	Node        string  `json:"node"`
	EnergyJ     float64 `json:"energy_j"`
	SlaPass     *bool   `json:"sla_pass"`
	Confidence  string  `json:"confidence"`
	MatchType   string  `json:"match_type"`
	LookupError bool    `json:"lookup_error"`
}

type programmableCatalog struct {
	byNode map[string]fixtureCandidate
	posted []map[string]any
}

func (f *programmableCatalog) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, nodeHint string) {
		cand, ok := f.byNode[nodeHint]
		if !ok {
			// Fall back: map accelerator token in key to node name used in fixtures.
			for name, c := range f.byNode {
				if (nodeHint == "npu" && name == "npu-node") || (nodeHint == "gpu" && name == "gpu-node") {
					cand, ok = c, true
					break
				}
			}
		}
		if !ok {
			http.Error(w, "unknown candidate", 404)
			return
		}
		if cand.LookupError {
			http.Error(w, "catalog unavailable", http.StatusBadGateway)
			return
		}
		key := fmt.Sprintf("fixture-%s", cand.Node)
		body := map[string]any{
			"found":                  true,
			"match_type":             cand.MatchType,
			"profile_key":            key,
			"environment_key":        key,
			"confidence":             cand.Confidence,
			"predicted_p99_ms":       1500.0,
			"predicted_duration_s":   20.0,
			"predicted_net_energy_j": cand.EnergyJ,
			"source":                 "MEASURED_EXPLORATORY",
		}
		if cand.SlaPass != nil {
			body["sla_pass"] = *cand.SlaPass
		}
		raw, _ := json.Marshal(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}
	accelHint := func(s string) string {
		if stringIndex(s, ":npu:") >= 0 {
			return "npu"
		}
		return "gpu"
	}
	mux.HandleFunc("/v1/env_lookup", func(w http.ResponseWriter, r *http.Request) {
		reply(w, accelHint(r.URL.Query().Get("environment_key")))
	})
	mux.HandleFunc("/v1/profile", func(w http.ResponseWriter, r *http.Request) {
		reply(w, accelHint(r.URL.Query().Get("profile_key")))
	})
	mux.HandleFunc("/v1/placement_decision", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.posted = append(f.posted, body)
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"decision_id":"d"}`)
	})
	return mux
}

func stringIndex(s, tok string) int {
	for i := 0; i+len(tok) <= len(s); i++ {
		if s[i:i+len(tok)] == tok {
			return i
		}
	}
	return -1
}

func loadCostFixtures(t *testing.T) fixtureFile {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "cost_selection_fixtures.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc fixtureFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("fixture cases empty")
	}
	return doc
}

func enforceEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("SCHED_CATALOG_URL", url)
	t.Setenv("SCHED_CATALOG_MODE", "enforce")
	t.Setenv("SCHED_SHADOW_SCORE_WEIGHT", "1")
	t.Setenv("SCHED_PREDICTION_MIN_CONFIDENCE", "MEASURED")
	t.Setenv("SCHED_CATALOG_TIMEOUT_MS", "2000")
}

func candidateExtrasSLA(dec map[string]any, node string) (any, bool) {
	cands, _ := dec["candidates_json"].([]any)
	for _, c := range cands {
		m, _ := c.(map[string]any)
		if m["node"] != node {
			continue
		}
		extras, _ := m["extras"].(map[string]any)
		return extras["sla_pass"], true
	}
	return nil, false
}

func catalogPluginScore(pr utils.PluginResult) int64 {
	for _, s := range pr.Scores {
		if s.PluginName == CatalogAwareScoreName {
			return s.Score
		}
	}
	return 0
}

func runFixtureCase(t *testing.T, fc fixtureCase) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	byNode := map[string]fixtureCandidate{}
	for _, c := range fc.Candidates {
		byNode[c.Node] = c
	}
	fake := &programmableCatalog{byNode: byNode}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	enforceEnv(t, srv.URL)

	cache := newCatalogTestCache(t, ctx)
	pod := newCatalogTestPod(fc.ID)
	baseline := &constantScore{scores: fc.BaselineScores}
	if baseline.scores == nil {
		baseline.scores = map[string]int64{"gpu-node": 10, "npu-node": 10}
	}
	fw := framework.NewFramework(nil, nil,
		[]framework.ScorePlugin{baseline, NewCatalogAwareScore(cache)}, nil, nil)
	got, status := fw.RunScorePlugins(ctx, pod, testNodes())
	if !status.IsSuccess() {
		t.Fatalf("RunScorePlugins: %s", status.Message())
	}

	host, err := utils.SelectPreferredNode(got)
	fw.CommitPlacementDecisions(pod, host, err)
	if fc.WantSelectError {
		if err == nil {
			t.Fatalf("%s: SelectPreferredNode succeeded with %q, want error (배치 보류)", fc.ID, host)
		}
	} else {
		if err != nil {
			t.Fatalf("%s: SelectPreferredNode error: %v", fc.ID, err)
		}
		if host != fc.WantSelectedNode {
			t.Fatalf("%s: SelectPreferredNode=%q want %q (gpu total=%d npu total=%d eligible gpu=%v npu=%v)",
				fc.ID, host, fc.WantSelectedNode,
				got["gpu-node"].TotalNodeScore, got["npu-node"].TotalNodeScore,
				got["gpu-node"].CostEligible, got["npu-node"].CostEligible)
		}
	}

	if len(fake.posted) != 1 {
		t.Fatalf("%s: decisions=%d want 1", fc.ID, len(fake.posted))
	}
	dec := fake.posted[0]
	se, _ := dec["selected_enforced"].(string)
	if fc.WantSelectedEnforcedEmpty {
		if se != "" {
			t.Fatalf("%s: selected_enforced=%q want empty", fc.ID, se)
		}
	} else if fc.WantSelectedNode != "" {
		if se != fc.WantSelectedNode {
			t.Fatalf("%s: selected_enforced=%q want %q", fc.ID, se, fc.WantSelectedNode)
		}
		if !fc.WantSelectError && se != host {
			t.Fatalf("%s: selected_enforced %q != SelectPreferredNode %q", fc.ID, se, host)
		}
	}

	if fc.WantFailedSLAPassNotTrue && fc.WantFailedNode != "" {
		sla, ok := candidateExtrasSLA(dec, fc.WantFailedNode)
		if !ok {
			t.Fatalf("%s: missing failed node %s in candidates", fc.ID, fc.WantFailedNode)
		}
		if sla == true {
			t.Fatalf("%s: failed node sla_pass=true", fc.ID)
		}
	}
	if fc.WantFailedPluginScoreZero && fc.WantFailedNode != "" {
		if catalogPluginScore(got[fc.WantFailedNode]) != 0 {
			t.Fatalf("%s: catalog plugin score on %s = %d want 0",
				fc.ID, fc.WantFailedNode, catalogPluginScore(got[fc.WantFailedNode]))
		}
	}
	if fc.WantGPUSlaPassCleared {
		sla, ok := candidateExtrasSLA(dec, "gpu-node")
		if !ok {
			t.Fatal("missing gpu candidate")
		}
		if sla != nil {
			t.Fatalf("%s: non-exact sla_pass=%v want nil", fc.ID, sla)
		}
	}
}

func TestCostSelectionFixturesFromJSON(t *testing.T) {
	doc := loadCostFixtures(t)
	for _, fc := range doc.Cases {
		fc := fc
		t.Run(fc.ID, func(t *testing.T) {
			runFixtureCase(t, fc)
		})
	}
}

func TestFixtureFilePresent(t *testing.T) {
	doc := loadCostFixtures(t)
	if len(doc.Cases) < 7 {
		t.Fatalf("cases = %d, want >= 7", len(doc.Cases))
	}
}

// C8: Catalog Normalize must not freeze selected_enforced before later Score plugins.
func TestCostSelectionLaterPluginMatchesRecord(t *testing.T) {
	doc := loadCostFixtures(t)
	var candidates []fixtureCandidate
	for _, fc := range doc.Cases {
		if fc.ID == "C7b_equal_energy_uses_total_score" {
			candidates = fc.Candidates
		}
	}
	if len(candidates) != 2 {
		t.Fatal("fixture missing C7b candidates")
	}
	fake := &programmableCatalog{byNode: map[string]fixtureCandidate{}}
	for _, c := range candidates {
		fake.byNode[c.Node] = c
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	enforceEnv(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := newCatalogTestCache(t, ctx)
	pod := newCatalogTestPod("c8-later-plugin")
	baseline := &constantScore{scores: map[string]int64{"gpu-node": 10, "npu-node": 10}}
	later := &constantScore{scores: map[string]int64{"gpu-node": 0, "npu-node": 40}}
	fw := framework.NewFramework(nil, nil,
		[]framework.ScorePlugin{baseline, NewCatalogAwareScore(cache), later}, nil, nil)
	results, status := fw.RunScorePlugins(ctx, pod, testNodes())
	if !status.IsSuccess() {
		t.Fatal(status)
	}
	selected, err := utils.SelectPreferredNode(results)
	if err != nil {
		t.Fatal(err)
	}
	fw.CommitPlacementDecisions(pod, selected, err)
	if selected != "npu-node" {
		t.Fatalf("final=%q want npu-node (gpu_total=%d npu_total=%d)",
			selected, results["gpu-node"].TotalNodeScore, results["npu-node"].TotalNodeScore)
	}
	if len(fake.posted) != 1 {
		t.Fatalf("decisions=%d want 1", len(fake.posted))
	}
	recorded, _ := fake.posted[0]["selected_enforced"].(string)
	if recorded != selected {
		t.Fatalf("late plugin: recorded=%q final=%q", recorded, selected)
	}
}

// C9: Enforce with missing candidate buffer entry must not fall back to TotalScore.
func TestCostSelectionMissingCandidateNoFallback(t *testing.T) {
	doc := loadCostFixtures(t)
	var invalid fixtureCandidate
	for _, fc := range doc.Cases {
		if fc.ID == "C2_cheap_slo_fail_excluded" {
			for _, c := range fc.Candidates {
				if c.Node == "npu-node" {
					invalid = c
				}
			}
		}
	}
	if invalid.SlaPass == nil || *invalid.SlaPass {
		t.Fatal("invalid fixture missing")
	}
	fake := &programmableCatalog{byNode: map[string]fixtureCandidate{"npu-node": invalid}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	enforceEnv(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plugin := NewCatalogAwareScore(newCatalogTestCache(t, ctx))
	pod := newCatalogTestPod("c9-partial-cost-buffer")
	if _, status := plugin.Score(ctx, pod, "npu-node"); !status.IsSuccess() {
		t.Fatal(status)
	}
	results := utils.PluginResultMap{
		"gpu-node": {TotalNodeScore: 40},
		"npu-node": {TotalNodeScore: 10},
	}
	if status := plugin.NormalizeScore(ctx, pod, results); !status.IsSuccess() {
		t.Fatal(status)
	}
	selected, err := utils.SelectPreferredNode(results)
	plugin.CommitPlacementDecision(pod, selected, err)
	if err == nil {
		t.Fatalf("no eligible candidate: final=%q; want selection error", selected)
	}
	if len(fake.posted) != 1 {
		t.Fatalf("decisions=%d want 1", len(fake.posted))
	}
	recorded, _ := fake.posted[0]["selected_enforced"].(string)
	if recorded != "" {
		t.Fatalf("recorded=%q want empty", recorded)
	}
	if results["gpu-node"].CostSelectionActive != true || !results["gpu-node"].IsFiltered {
		t.Fatalf("missing candidate gpu must be cost-active and filtered: %+v", results["gpu-node"])
	}
}
