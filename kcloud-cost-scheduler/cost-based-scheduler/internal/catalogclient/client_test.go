package catalogclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newProfileServer(t *testing.T, hits *int32, body string, delay time.Duration, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

func TestModeOffNeverCallsCatalog(t *testing.T) {
	var hits int32
	srv := newProfileServer(t, &hits, `{"found":true}`, 0, 200)
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "off")
	c := NewFromEnv()

	pred, err := c.LookupProfile("some-key")
	if err != nil {
		t.Fatalf("LookupProfile() error = %v", err)
	}
	if pred.Found || pred.Confidence != "FALLBACK" {
		t.Fatalf("pred = %+v, want not-found FALLBACK", pred)
	}
	if err := c.PostDecision(PlacementDecision{TraceID: "t"}); err != nil {
		t.Fatalf("PostDecision() error = %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("catalog hits = %d, want 0 in off mode", got)
	}
}

func TestLookupProfileCachesByKey(t *testing.T) {
	var hits int32
	srv := newProfileServer(t, &hits,
		`{"found":true,"profile_key":"k1","confidence":"MEASURED","predicted_net_energy_j":100}`, 0, 200)
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	c := NewFromEnv()

	for i := 0; i < 3; i++ {
		pred, err := c.LookupProfile("k1")
		if err != nil {
			t.Fatalf("LookupProfile() error = %v", err)
		}
		if !pred.Found || pred.PredictedNetEnergyJ == nil || *pred.PredictedNetEnergyJ != 100 {
			t.Fatalf("pred = %+v, want found with 100 J", pred)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("catalog hits = %d, want 1 (cached)", got)
	}

	if _, err := c.LookupProfile("k2"); err != nil {
		t.Fatalf("LookupProfile(k2) error = %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("catalog hits = %d, want 2 after new key", got)
	}
}

func TestLookupProfileTimeoutFallsBack(t *testing.T) {
	var hits int32
	srv := newProfileServer(t, &hits, `{"found":true}`, 300*time.Millisecond, 200)
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	t.Setenv("SCHED_CATALOG_TIMEOUT_MS", "30")
	c := NewFromEnv()

	pred, err := c.LookupProfile("slow")
	if err == nil {
		t.Fatal("LookupProfile() error = nil, want timeout")
	}
	if pred.Found || pred.Confidence != "FALLBACK" {
		t.Fatalf("pred = %+v, want FALLBACK on timeout", pred)
	}
}

func TestLookupProfileServerErrorFallsBack(t *testing.T) {
	var hits int32
	srv := newProfileServer(t, &hits, `{"error":"boom"}`, 0, 500)
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	c := NewFromEnv()

	pred, err := c.LookupProfile("k")
	if err == nil {
		t.Fatal("LookupProfile() error = nil, want status error")
	}
	if pred.Confidence != "FALLBACK" {
		t.Fatalf("pred = %+v, want FALLBACK", pred)
	}
}

func TestPostDecisionOmitsEmptyDecisionBranch(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"decision_id":"d1"}`)
	}))
	defer srv.Close()

	t.Setenv("SCHED_CATALOG_URL", srv.URL)
	t.Setenv("SCHED_CATALOG_MODE", "shadow")
	c := NewFromEnv()

	err := c.PostDecision(PlacementDecision{
		TraceID:        "trace-1",
		Mode:           "shadow",
		SelectedShadow: "node-a",
		CandidatesJSON: []map[string]any{{"node": "node-a"}, {"node": "node-b"}},
	})
	if err != nil {
		t.Fatalf("PostDecision() error = %v", err)
	}
	if _, ok := got["decision_branch"]; ok {
		t.Fatal("decision_branch present, want omitted so B0~B7 is never inferred")
	}
	if got["selected_shadow"] != "node-a" {
		t.Fatalf("selected_shadow = %v, want node-a", got["selected_shadow"])
	}
	cands, ok := got["candidates_json"].([]any)
	if !ok || len(cands) != 2 {
		t.Fatalf("candidates_json = %v, want 2 entries", got["candidates_json"])
	}
}

func TestMeetsMinConfidence(t *testing.T) {
	cases := []struct {
		actual, min string
		want        bool
	}{
		{"MEASURED", "MEASURED", true},
		{"REFERENCE", "MEASURED", false},
		{"SPEC", "REFERENCE", false},
		{"REFERENCE", "SPEC", true},
		{"FALLBACK", "SPEC", false},
		{"", "SPEC", false},
		{"bogus", "SPEC", false},
	}
	for _, c := range cases {
		if got := MeetsMinConfidence(c.actual, c.min); got != c.want {
			t.Errorf("MeetsMinConfidence(%q, %q) = %v, want %v", c.actual, c.min, got, c.want)
		}
	}
}
