package catalogclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/workloadcontext"
)

type Mode string

const (
	ModeOff     Mode = "off"
	ModeShadow  Mode = "shadow"
	ModeEnforce Mode = "enforce"
)

type ProfilePrediction struct {
	Found               bool     `json:"found"`
	ProfileKey          string   `json:"profile_key"`
	EnvironmentKey      string   `json:"environment_key,omitempty"`
	MatchType           string   `json:"match_type,omitempty"`
	Confidence          string   `json:"confidence"`
	PredictedP99Ms      *float64 `json:"predicted_p99_ms"`
	PredictedDurationS  *float64 `json:"predicted_duration_s"`
	// PredictedNetEnergyJ is device net energy (historical Catalog field).
	PredictedNetEnergyJ *float64 `json:"predicted_net_energy_j"`
	// PredictedServerEnergyJ is server total energy (NETIO window) when measured.
	PredictedServerEnergyJ *float64 `json:"predicted_server_energy_j"`
	Source                 string   `json:"source"`
	SlaPass                *bool    `json:"sla_pass"`
	Reason                 string   `json:"reason,omitempty"`
}

type PlacementDecision struct {
	DecisionID          string         `json:"decision_id,omitempty"`
	TraceID             string         `json:"trace_id"`
	RunUUID             string         `json:"run_uuid,omitempty"`
	Mode                string         `json:"mode"`
	DecisionReason      string         `json:"decision_reason,omitempty"`
	RoutingPolicy       string         `json:"routing_policy,omitempty"`
	DecisionBranch      string         `json:"decision_branch,omitempty"`
	CandidatesJSON      any            `json:"candidates_json,omitempty"`
	SelectedShadow      string         `json:"selected_shadow,omitempty"`
	SelectedEnforced    string         `json:"selected_enforced,omitempty"`
	SelectedActual      string         `json:"selected_actual,omitempty"`
	PredictedP99Ms         *float64       `json:"predicted_p99_ms,omitempty"`
	PredictedDurationS     *float64       `json:"predicted_duration_s,omitempty"`
	PredictedNetEnergyJ    *float64       `json:"predicted_net_energy_j,omitempty"`
	PredictedServerEnergyJ *float64       `json:"predicted_server_energy_j,omitempty"`
	Confidence             string         `json:"confidence,omitempty"`
	ScoreComponentsJSON map[string]any `json:"score_components_json,omitempty"`
	RegretJSON          map[string]any `json:"regret_json,omitempty"`
	ModelVersion        string         `json:"model_version,omitempty"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	mode       Mode
	minConf    string
	weight     float64

	mu    sync.Mutex
	cache map[string]cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	at   time.Time
	pred ProfilePrediction
}

func ModeFromEnv() Mode {
	switch os.Getenv("SCHED_CATALOG_MODE") {
	case "shadow":
		return ModeShadow
	case "enforce":
		return ModeEnforce
	default:
		return ModeOff
	}
}

func NewFromEnv() *Client {
	timeoutMs, _ := strconv.Atoi(os.Getenv("SCHED_CATALOG_TIMEOUT_MS"))
	if timeoutMs <= 0 {
		timeoutMs = 100
	}
	ttlSec, _ := strconv.Atoi(os.Getenv("SCHED_CATALOG_CACHE_TTL_SEC"))
	if ttlSec <= 0 {
		ttlSec = 30
	}
	weight, _ := strconv.ParseFloat(os.Getenv("SCHED_SHADOW_SCORE_WEIGHT"), 64)
	minConf := os.Getenv("SCHED_PREDICTION_MIN_CONFIDENCE")
	if minConf == "" {
		minConf = "MEASURED"
	}
	return &Client{
		baseURL: os.Getenv("SCHED_CATALOG_URL"),
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
		},
		mode:    ModeFromEnv(),
		minConf: minConf,
		weight:  weight,
		cache:   map[string]cacheEntry{},
		ttl:     time.Duration(ttlSec) * time.Second,
	}
}

func (c *Client) Mode() Mode            { return c.mode }
func (c *Client) Weight() float64       { return c.weight }
func (c *Client) MinConfidence() string { return c.minConf }

// confidenceRank orders the WorkloadContext confidence ladder. Unknown values
// rank lowest so an unrecognised label can never satisfy a confidence floor.
func confidenceRank(s string) int {
	switch s {
	case "MEASURED":
		return 3
	case "REFERENCE":
		return 2
	case "SPEC":
		return 1
	default:
		return 0
	}
}

func MeetsMinConfidence(actual, min string) bool {
	return confidenceRank(actual) >= confidenceRank(min)
}

func (c *Client) LookupProfile(profileKey string) (ProfilePrediction, error) {
	fallback := ProfilePrediction{
		Found:      false,
		ProfileKey: profileKey,
		MatchType:  "profile_not_found",
		Confidence: "FALLBACK",
	}
	if c == nil || c.mode == ModeOff || c.baseURL == "" || profileKey == "" {
		return fallback, nil
	}

	c.mu.Lock()
	if e, ok := c.cache[profileKey]; ok && time.Since(e.at) < c.ttl {
		pred := e.pred
		c.mu.Unlock()
		return pred, nil
	}
	c.mu.Unlock()

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fallback, err
	}
	u.Path = "/v1/profile"
	q := u.Query()
	q.Set("profile_key", profileKey)
	u.RawQuery = q.Encode()

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return fallback, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fallback, fmt.Errorf("catalog status %d: %s", resp.StatusCode, string(body))
	}
	var pred ProfilePrediction
	if err := json.Unmarshal(body, &pred); err != nil {
		return fallback, err
	}
	c.mu.Lock()
	c.cache[profileKey] = cacheEntry{at: time.Now(), pred: pred}
	c.mu.Unlock()
	return pred, nil
}

// LookupEnvironment queries /v1/env_lookup (exact env+concurrency). Cross-env
// reuse is rejected by the Catalog API (similar/not_found).
func (c *Client) LookupEnvironment(environmentKey string, concurrency int) (ProfilePrediction, error) {
	lookupKey := workloadcontext.BuildCatalogLookupKey(environmentKey, concurrency)
	fallback := ProfilePrediction{
		Found:          false,
		ProfileKey:     lookupKey,
		EnvironmentKey: environmentKey,
		MatchType:      "profile_not_found",
		Confidence:     "FALLBACK",
		Reason:         "profile_not_found",
	}
	if c == nil || c.mode == ModeOff || c.baseURL == "" || environmentKey == "" {
		return fallback, nil
	}
	cacheKey := "env|" + lookupKey
	c.mu.Lock()
	if e, ok := c.cache[cacheKey]; ok && time.Since(e.at) < c.ttl {
		pred := e.pred
		c.mu.Unlock()
		return pred, nil
	}
	c.mu.Unlock()

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fallback, err
	}
	u.Path = "/v1/env_lookup"
	q := u.Query()
	q.Set("environment_key", environmentKey)
	q.Set("concurrency", strconv.Itoa(concurrency))
	u.RawQuery = q.Encode()

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return fallback, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fallback, fmt.Errorf("catalog env_lookup status %d: %s", resp.StatusCode, string(body))
	}
	var pred ProfilePrediction
	if err := json.Unmarshal(body, &pred); err != nil {
		return fallback, err
	}
	if pred.ProfileKey == "" {
		pred.ProfileKey = lookupKey
	}
	if pred.EnvironmentKey == "" {
		pred.EnvironmentKey = environmentKey
	}
	c.mu.Lock()
	c.cache[cacheKey] = cacheEntry{at: time.Now(), pred: pred}
	c.mu.Unlock()
	return pred, nil
}

func (c *Client) PostDecision(d PlacementDecision) error {
	if c == nil || c.mode == ModeOff || c.baseURL == "" {
		return nil
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Post(c.baseURL+"/v1/placement_decision", "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post decision %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
