package plugin

import (
	"context"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/catalogclient"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/workloadcontext"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

const CatalogAwareScoreName = "CatalogAwareScore"

const (
	// A scheduling cycle that aborts between Score and NormalizeScore would
	// otherwise leak its buffer, so stale entries are swept by age.
	candidateBufferTTL = 2 * time.Minute
	maxShadowScore     = 100.0
	energyScoreNumer   = 10000.0
)

type shadowCandidate struct {
	Node                string         `json:"node"`
	RawShadowScore      float64        `json:"raw_shadow_score"`
	ReturnedScore       int64          `json:"returned_score"`
	Found               bool           `json:"found"`
	Confidence          string         `json:"confidence"`
	MeetsMinConfidence  bool           `json:"meets_min_confidence"`
	PredictedP99Ms         *float64       `json:"predicted_p99_ms,omitempty"`
	PredictedDurationS     *float64       `json:"predicted_duration_s,omitempty"`
	PredictedNetEnergyJ    *float64       `json:"predicted_net_energy_j,omitempty"`
	PredictedServerEnergyJ *float64       `json:"predicted_server_energy_j,omitempty"`
	Extras                 map[string]any `json:"extras,omitempty"`
}

// candidateBuffer collects one scheduling cycle's candidates. Cycles for
// different pods run concurrently, so buffers are keyed by pod UID and every
// access is taken under CatalogAwareScore.mu.
type candidateBuffer struct {
	createdAt     time.Time
	traceID       string
	routingPolicy string
	candidates    []shadowCandidate
}

// pendingPlacement holds Score-cycle catalog state until the final node
// selection is known. placement_decision must not be posted from NormalizeScore
// when later Score plugins can still change totals.
type pendingPlacement struct {
	createdAt     time.Time
	traceID       string
	routingPolicy string
	candidates    []shadowCandidate
	shadowBest    shadowCandidate
	enforceOn     bool
}

// preparedPrediction is one PreScore lookup result, keyed by full catalog lookup key.
type preparedPrediction struct {
	pred catalogclient.ProfilePrediction
	err  error
}

// attemptCatalogPrep holds PreScore outputs for one scheduling attempt.
// Payload maps may be dropped by age sweep; whether PreScore *ran* is tracked
// separately in CatalogAwareScore.prescoreRan and is cleared only by clearPrepared
// (Commit / fail / cancel). Score must not fall back to HTTP after PreScore ran.
type attemptCatalogPrep struct {
	createdAt time.Time
	byKey     map[string]preparedPrediction
	nodeKey   map[string]string // nodeName → lookup key fixed at PreScore
}

// CatalogAwareScore looks up Catalog predictions per candidate node and records
// exactly one placement_decision per scheduling cycle.
//
// In shadow mode the raw score is still computed so that selected_shadow names
// a real recommendation, but the score returned to the framework is 0, which
// leaves Bind selection identical to a build without this plugin.
//
// PreScore prepares unique full-lookup-key predictions; once PreScore ran for a
// pod UID, Score never issues HTTP — it reuses prep (including failures) or
// marks an explicit miss / expired attempt as unevaluated.
type CatalogAwareScore struct {
	cache  *utils.Cache
	client *catalogclient.Client

	mu       sync.Mutex
	buffers  map[types.UID]*candidateBuffer
	pending  map[types.UID]*pendingPlacement
	prep     map[types.UID]*attemptCatalogPrep
	// prescoreRan survives prep age-out. Cleared only by clearPrepared.
	prescoreRan map[types.UID]time.Time
}

var _ framework.PlacementDecisionCommitter = &CatalogAwareScore{}

var (
	_ framework.PreScorePlugin  = &CatalogAwareScore{}
	_ framework.ScorePlugin     = &CatalogAwareScore{}
	_ framework.ScoreExtensions = &CatalogAwareScore{}
)

func NewCatalogAwareScore(cache *utils.Cache) *CatalogAwareScore {
	return &CatalogAwareScore{
		cache:       cache,
		client:      catalogclient.NewFromEnv(),
		buffers:     map[types.UID]*candidateBuffer{},
		pending:     map[types.UID]*pendingPlacement{},
		prep:        map[types.UID]*attemptCatalogPrep{},
		prescoreRan: map[types.UID]time.Time{},
	}
}

func (p *CatalogAwareScore) Name() string { return CatalogAwareScoreName }

func (p *CatalogAwareScore) ScoreExtensions() framework.ScoreExtensions { return p }

func lookupKeyFor(wc *workloadcontext.Context) string {
	if wc == nil {
		return ""
	}
	if wc.EnvironmentKey != "" {
		return workloadcontext.BuildCatalogLookupKey(wc.EnvironmentKey, wc.Concurrency)
	}
	return wc.ProfileKey
}

func unevaluatedPred(key, reason string) catalogclient.ProfilePrediction {
	return catalogclient.ProfilePrediction{
		Found:      false,
		ProfileKey: key,
		MatchType:  "profile_not_found",
		Confidence: workloadcontext.ConfidenceFALLBACK,
		Reason:     reason,
	}
}

// PreScore performs at most one Catalog lookup per unique full lookup key among
// Filter-passed nodes and stores results for Score (including failures).
func (p *CatalogAwareScore) PreScore(ctx context.Context, pod *v1.Pod, nodes []*v1.Node) *utils.Status {
	if p.client == nil || p.client.Mode() == catalogclient.ModeOff || pod == nil {
		return utils.NewStatus(utils.Success, "")
	}
	uid := pod.UID
	byKey := map[string]preparedPrediction{}
	nodeKey := map[string]string{}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		wc := p.contextForNode(pod, node.Name)
		key := lookupKeyFor(wc)
		if key == "" {
			continue
		}
		nodeKey[node.Name] = key
		if _, seen := byKey[key]; seen {
			continue
		}
		var pred catalogclient.ProfilePrediction
		var err error
		if wc.EnvironmentKey != "" {
			pred, err = p.client.LookupEnvironment(wc.EnvironmentKey, wc.Concurrency)
		} else {
			pred, err = p.client.LookupProfile(wc.ProfileKey)
		}
		if err != nil {
			klog.V(2).InfoS("catalog PreScore lookup failed", "err", err, "key", key)
			pred = unevaluatedPred(key, "prescore_lookup_error")
		}
		if pred.MatchType != "exact" {
			pred.SlaPass = nil
		}
		byKey[key] = preparedPrediction{pred: pred, err: err}
	}
	now := time.Now()
	p.mu.Lock()
	p.prep[uid] = &attemptCatalogPrep{
		createdAt: now,
		byKey:     byKey,
		nodeKey:   nodeKey,
	}
	p.prescoreRan[uid] = now
	p.mu.Unlock()
	klog.V(4).InfoS("CatalogAwareScore PreScore",
		"pod", pod.Name, "nodes", len(nodes), "unique_keys", len(byKey))
	return utils.NewStatus(utils.Success, "")
}

func (p *CatalogAwareScore) prepSnapshot(uid types.UID) (*attemptCatalogPrep, bool) {
	if p == nil {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ap, ok := p.prep[uid]
	return ap, ok
}

func (p *CatalogAwareScore) preScoreRan(uid types.UID) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.prescoreRan[uid]
	return ok
}

func (p *CatalogAwareScore) clearPrepared(uid types.UID) {
	p.mu.Lock()
	delete(p.prep, uid)
	delete(p.prescoreRan, uid)
	p.mu.Unlock()
}

func (p *CatalogAwareScore) Score(ctx context.Context, pod *v1.Pod, nodeName string) (int64, *utils.Status) {
	if p.client == nil || p.client.Mode() == catalogclient.ModeOff {
		return 0, utils.NewStatus(utils.Success, "")
	}

	wc := p.contextForNode(pod, nodeName)
	key := lookupKeyFor(wc)
	var pred catalogclient.ProfilePrediction
	var err error

	ran := p.preScoreRan(pod.UID)
	ap, hasPrep := p.prepSnapshot(pod.UID)
	if ran {
		// PreScore already ran for this attempt: never HTTP from Score,
		// even if age-sweep dropped the prep payload.
		if hasPrep && ap != nil {
			if fixed, has := ap.nodeKey[nodeName]; has {
				key = fixed
			}
			if e, hit := ap.byKey[key]; hit {
				pred = e.pred
			} else {
				klog.V(2).InfoS("catalog Score miss after PreScore", "node", nodeName, "key", key)
				pred = unevaluatedPred(key, "prescore_key_miss")
			}
		} else {
			klog.V(2).InfoS("catalog Score after PreScore prep expired/cleared", "node", nodeName, "key", key)
			pred = unevaluatedPred(key, "prescore_attempt_expired")
		}
	} else if wc.EnvironmentKey != "" {
		// Legacy: unit tests that call Score without PreScore.
		pred, err = p.client.LookupEnvironment(wc.EnvironmentKey, wc.Concurrency)
	} else {
		pred, err = p.client.LookupProfile(wc.ProfileKey)
	}
	if err != nil {
		klog.V(2).InfoS("catalog lookup failed", "err", err, "node", nodeName)
		pred = unevaluatedPred(key, "score_lookup_error")
	}
	// Unevaluated / non-exact must never be treated as SLA PASS.
	if pred.MatchType != "exact" {
		pred.SlaPass = nil
	}

	raw := rawShadowScore(pred)
	meetsConf := catalogclient.MeetsMinConfidence(pred.Confidence, p.client.MinConfidence())
	eligible := costEligible(pred, meetsConf)

	// Enforce cost-first does not add to TotalNodeScore via weight. Ranking is
	// applied in NormalizeScore + utils.SelectPreferredNode (energy, then totals).
	// Shadow still returns 0 so Bind ranking is unchanged.
	var returned int64
	reason := pred.Reason
	if p.client.Mode() == catalogclient.ModeEnforce && p.client.Weight() > 0 {
		returned = 0
		if !eligible {
			if reason == "" {
				reason = ineligibleReason(pred, meetsConf)
			}
		}
	}

	p.recordCandidate(pod, wc, shadowCandidate{
		Node:                nodeName,
		RawShadowScore:      raw,
		ReturnedScore:       returned,
		Found:               pred.Found,
		Confidence:          pred.Confidence,
		MeetsMinConfidence:  meetsConf,
		PredictedP99Ms:         pred.PredictedP99Ms,
		PredictedDurationS:     pred.PredictedDurationS,
		PredictedNetEnergyJ:    pred.PredictedNetEnergyJ,
		PredictedServerEnergyJ: pred.PredictedServerEnergyJ,
		Extras: map[string]any{
			"profile_key":               wc.ProfileKey,
			"environment_key":           wc.EnvironmentKey,
			"catalog_lookup_key":        pred.ProfileKey,
			"match_type":                pred.MatchType,
			"sla_pass":                  pred.SlaPass,
			"accelerator":               wc.Accelerator,
			"model":                     wc.Model,
			"runtime":                   wc.Runtime,
			"concurrency":               wc.Concurrency,
			"reason":                    reason,
			"cost_eligible":             eligible,
			"predicted_server_energy_j": pred.PredictedServerEnergyJ,
			"energy_rank_scope":         energyRankScope(pred),
		},
	})

	klog.V(4).InfoS("CatalogAwareScore",
		"pod", pod.Name, "node", nodeName, "returned", returned, "raw", raw,
		"mode", p.client.Mode(), "confidence", pred.Confidence, "costEligible", eligible)
	return returned, utils.NewStatus(utils.Success, "")
}

func hasCostEnergy(pred catalogclient.ProfilePrediction) bool {
	if pred.PredictedServerEnergyJ != nil && *pred.PredictedServerEnergyJ > 0 {
		return true
	}
	return pred.PredictedNetEnergyJ != nil && *pred.PredictedNetEnergyJ > 0
}

func energyRankScope(pred catalogclient.ProfilePrediction) string {
	if pred.PredictedServerEnergyJ != nil && *pred.PredictedServerEnergyJ > 0 {
		return "server_total"
	}
	if pred.PredictedNetEnergyJ != nil && *pred.PredictedNetEnergyJ > 0 {
		return "device_net"
	}
	return "none"
}

// costEligible is the Enforce cost-selection set: exact MEASURED (or above floor)
// prediction with explicit sla_pass=true and positive server total or device net energy.
func costEligible(pred catalogclient.ProfilePrediction, meetsConf bool) bool {
	if !pred.Found || !meetsConf || pred.MatchType != "exact" {
		return false
	}
	if pred.SlaPass == nil || !*pred.SlaPass {
		return false
	}
	return hasCostEnergy(pred)
}

func ineligibleReason(pred catalogclient.ProfilePrediction, meetsConf bool) string {
	if !pred.Found {
		return "catalog_lookup_failed_or_not_found"
	}
	if pred.MatchType != "exact" {
		return "match_type_not_exact"
	}
	if !meetsConf {
		return "confidence_below_floor"
	}
	if pred.SlaPass == nil {
		return "sla_pass_unevaluated"
	}
	if !*pred.SlaPass {
		return "sla_pass_false"
	}
	if !hasCostEnergy(pred) {
		return "invalid_predicted_energy"
	}
	return "cost_ineligible"
}

// NormalizeScore prepares cost-selection metadata only. placement_decision is
// posted later via CommitPlacementDecision after the final SelectPreferredNode.
func (p *CatalogAwareScore) NormalizeScore(ctx context.Context, pod *v1.Pod, result utils.PluginResultMap) *utils.Status {
	if p.client == nil || p.client.Mode() == catalogclient.ModeOff {
		return utils.NewStatus(utils.Success, "")
	}

	buf := p.takeBuffer(pod.UID)
	if buf == nil || len(buf.candidates) == 0 {
		return utils.NewStatus(utils.Success, "")
	}

	// Shadow recommendation: highest raw score (energy inverse), name tie-break.
	shadowOrder := append([]shadowCandidate(nil), buf.candidates...)
	sort.Slice(shadowOrder, func(i, j int) bool {
		if shadowOrder[i].RawShadowScore != shadowOrder[j].RawShadowScore {
			return shadowOrder[i].RawShadowScore > shadowOrder[j].RawShadowScore
		}
		return shadowOrder[i].Node < shadowOrder[j].Node
	})
	shadowBest := shadowOrder[0]

	enforceOn := p.client.Mode() == catalogclient.ModeEnforce && p.client.Weight() > 0
	if enforceOn && result != nil {
		byNode := map[string]shadowCandidate{}
		for _, c := range buf.candidates {
			byNode[c.Node] = c
		}
		for nodeName, pr := range result {
			c, ok := byNode[nodeName]
			elig := false
			if ok {
				elig, _ = c.Extras["cost_eligible"].(bool)
			}
			pr.CostSelectionActive = true
			pr.CostEligible = false
			pr.PredictedNetEnergyJ = nil
			pr.PredictedServerEnergyJ = nil
			if elig && (c.PredictedNetEnergyJ != nil || c.PredictedServerEnergyJ != nil) {
				pr.CostEligible = true
				if c.PredictedNetEnergyJ != nil {
					e := *c.PredictedNetEnergyJ
					pr.PredictedNetEnergyJ = &e
				}
				if c.PredictedServerEnergyJ != nil {
					s := *c.PredictedServerEnergyJ
					pr.PredictedServerEnergyJ = &s
				}
			} else {
				// Ineligible recorded candidate OR missing from catalog buffer:
				// exclude from Bind; do not fall back to TotalNodeScore.
				pr.IsFiltered = true
				reason := "cost_candidate_missing"
				if ok {
					reason = ineligibleReasonFromCandidate(c)
				}
				pr.FilteredStatus = *utils.NewStatus(utils.Unschedulable, reason)
			}
			for i := range pr.Scores {
				if pr.Scores[i].PluginName == CatalogAwareScoreName {
					pr.Scores[i].Score = 0
				}
			}
			result[nodeName] = pr
		}
	}

	p.storePending(pod.UID, &pendingPlacement{
		createdAt:     time.Now(),
		traceID:       buf.traceID,
		routingPolicy: buf.routingPolicy,
		candidates:    append([]shadowCandidate(nil), buf.candidates...),
		shadowBest:    shadowBest,
		enforceOn:     enforceOn,
	})

	klog.V(4).InfoS("CatalogAwareScore normalize ready for commit",
		"pod", pod.Name, "candidates", len(buf.candidates),
		"selectedShadow", shadowBest.Node, "enforceOn", enforceOn, "mode", p.client.Mode())
	return utils.NewStatus(utils.Success, "")
}

// CommitPlacementDecision posts one placement_decision using the final Bind
// selection (or empty selected_enforced when selection failed / shadow mode).
func (p *CatalogAwareScore) CommitPlacementDecision(pod *v1.Pod, selectedHost string, selectErr error) {
	if pod != nil {
		defer p.clearPrepared(pod.UID)
	}
	if p.client == nil || p.client.Mode() == catalogclient.ModeOff {
		return
	}
	pending := p.takePending(pod.UID)
	if pending == nil {
		return
	}

	decisionReason := "scheduler_catalog_lookup"
	selectedEnforced := ""
	var enforcedBest *shadowCandidate
	if pending.enforceOn {
		if selectErr != nil || selectedHost == "" {
			decisionReason = "no_valid_cost_candidate"
			selectedEnforced = ""
		} else {
			selectedEnforced = selectedHost
			decisionReason = "enforce_min_energy_among_sla_pass"
			for i := range pending.candidates {
				if pending.candidates[i].Node == selectedHost {
					cp := pending.candidates[i]
					enforcedBest = &cp
					break
				}
			}
		}
	}

	shadowBest := pending.shadowBest
	predP99, predDur, predE, predSrv, conf := shadowBest.PredictedP99Ms, shadowBest.PredictedDurationS, shadowBest.PredictedNetEnergyJ, shadowBest.PredictedServerEnergyJ, shadowBest.Confidence
	if enforcedBest != nil {
		predP99 = enforcedBest.PredictedP99Ms
		predDur = enforcedBest.PredictedDurationS
		predE = enforcedBest.PredictedNetEnergyJ
		predSrv = enforcedBest.PredictedServerEnergyJ
		conf = enforcedBest.Confidence
	}

	decision := catalogclient.PlacementDecision{
		TraceID:        pending.traceID,
		Mode:           string(p.client.Mode()),
		DecisionReason: decisionReason,
		RoutingPolicy:  pending.routingPolicy,
		// decision_branch stays empty: B0~B7 are scheduler judgements and are
		// never inferred from an experiment cohort.
		DecisionBranch:         "",
		CandidatesJSON:         pending.candidates,
		SelectedShadow:         shadowBest.Node,
		SelectedEnforced:       selectedEnforced,
		PredictedP99Ms:         predP99,
		PredictedDurationS:     predDur,
		PredictedNetEnergyJ:    predE,
		PredictedServerEnergyJ: predSrv,
		Confidence:             conf,
		ScoreComponentsJSON: map[string]any{
			"candidate_count":       len(pending.candidates),
			"weight":                p.client.Weight(),
			"min_confidence":        p.client.MinConfidence(),
			"raw_shadow_score":      shadowBest.RawShadowScore,
			"cost_selection_policy": "min_server_energy_among_sla_pass_then_total_score",
			"energy_rank_scope":     energyRankScope(catalogclient.ProfilePrediction{PredictedNetEnergyJ: predE, PredictedServerEnergyJ: predSrv}),
			"selected_enforced":     selectedEnforced,
		},
		ModelVersion: "catalog-lookup-v1",
	}

	if err := p.client.PostDecision(decision); err != nil {
		klog.V(2).InfoS("catalog decision post failed", "err", err, "pod", pod.Name)
	}

	klog.V(4).InfoS("CatalogAwareScore decision committed",
		"pod", pod.Name, "candidates", len(pending.candidates),
		"selectedShadow", shadowBest.Node, "selectedEnforced", selectedEnforced,
		"reason", decisionReason, "mode", p.client.Mode())
}

func ineligibleReasonFromCandidate(c shadowCandidate) string {
	if r, ok := c.Extras["reason"].(string); ok && r != "" {
		return r
	}
	return "cost_ineligible"
}

func rawShadowScore(pred catalogclient.ProfilePrediction) float64 {
	if !pred.Found {
		return 0
	}
	// Prefer server total for ranking; fall back to device net.
	var energy *float64
	if pred.PredictedServerEnergyJ != nil && *pred.PredictedServerEnergyJ > 0 {
		energy = pred.PredictedServerEnergyJ
	} else if pred.PredictedNetEnergyJ != nil && *pred.PredictedNetEnergyJ > 0 {
		energy = pred.PredictedNetEnergyJ
	}
	if energy == nil {
		return 0
	}
	score := energyScoreNumer / *energy
	if score > maxShadowScore {
		score = maxShadowScore
	}
	return score
}

func (p *CatalogAwareScore) recordCandidate(pod *v1.Pod, wc *workloadcontext.Context, c shadowCandidate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()

	buf := p.buffers[pod.UID]
	if buf == nil {
		buf = &candidateBuffer{
			createdAt:     time.Now(),
			traceID:       wc.TraceID,
			routingPolicy: wc.RoutingPolicy,
		}
		p.buffers[pod.UID] = buf
	}
	buf.candidates = append(buf.candidates, c)
}

func (p *CatalogAwareScore) takeBuffer(uid types.UID) *candidateBuffer {
	p.mu.Lock()
	defer p.mu.Unlock()
	buf := p.buffers[uid]
	delete(p.buffers, uid)
	return buf
}

func (p *CatalogAwareScore) storePending(uid types.UID, pend *pendingPlacement) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	p.pending[uid] = pend
}

func (p *CatalogAwareScore) takePending(uid types.UID) *pendingPlacement {
	p.mu.Lock()
	defer p.mu.Unlock()
	pend := p.pending[uid]
	delete(p.pending, uid)
	return pend
}

func (p *CatalogAwareScore) sweepLocked() {
	cutoff := time.Now().Add(-candidateBufferTTL)
	for uid, buf := range p.buffers {
		if buf.createdAt.Before(cutoff) {
			klog.V(2).InfoS("dropping stale catalog candidate buffer", "podUID", uid)
			delete(p.buffers, uid)
		}
	}
	for uid, pend := range p.pending {
		if pend.createdAt.Before(cutoff) {
			klog.V(2).InfoS("dropping stale catalog pending placement", "podUID", uid)
			delete(p.pending, uid)
		}
	}
	// Drop aged prep *payload* only. prescoreRan stays until clearPrepared so
	// Score cannot re-enable HTTP after an active attempt's prep expired (F1).
	for uid, ap := range p.prep {
		if ap == nil || ap.createdAt.Before(cutoff) {
			klog.V(2).InfoS("dropping stale catalog PreScore prep payload", "podUID", uid)
			delete(p.prep, uid)
		}
	}
}

// contextForNode resolves the WorkloadContext against a specific candidate.
// The accelerator/SKU/node come from the candidate node; util/KV/LSU/engines
// come from Pod annotations (or resource requests), so GPU vs NPU (or different
// LSU quotas) produce different environment_keys and predictions.
func (p *CatalogAwareScore) contextForNode(pod *v1.Pod, nodeName string) *workloadcontext.Context {
	ann := map[string]string{}
	if pod.Annotations != nil {
		for k, v := range pod.Annotations {
			ann[k] = v
		}
	}
	traceID := ann["sched.ai/trace-id"]
	if traceID == "" {
		traceID = string(pod.UID)
	}

	devType := ann["sched.ai/acc-type"]
	sku := ann["profile.ai/sku"]
	nodeLabelSKU := ""
	if p.cache != nil {
		if nodeInfo := p.cache.Nodes()[nodeName]; nodeInfo != nil && nodeInfo.Node() != nil {
			n := nodeInfo.Node()
			// Pod annotation wins when set (mixed GPU/NPU nodes otherwise force npu).
			if devType == "" {
				if t := nodeDeviceType(n); t != "" {
					devType = t
				}
			}
			if sku == "" {
				sku = n.Labels["sched.ai/gpu-model"]
				nodeLabelSKU = sku
			}
			_ = nodeLabelSKU
		}
	}
	if sku == "" {
		sku = ann["sched.ai/gpu-model"]
	}

	model := annForDevice(ann, "profile.ai/model", devType)
	if model == "" {
		model = ann["sched.ai/gpu-model"]
	}

	in := workloadcontext.Input{
		ExperimentID:  os.Getenv("SCHED_EXPERIMENT_ID"),
		WorkloadID:    string(pod.UID),
		TraceID:       traceID,
		Model:         model,
		Runtime:       annForDevice(ann, "profile.ai/runtime", devType),
		Accelerator:   nzStr(devType, "gpu"),
		Precision:     annForDevice(ann, "profile.ai/precision", devType),
		RoutingPolicy: ann["sched.ai/routing-policy"],
		InputTokens:   annInt(ann, "profile.ai/input-tokens", 256),
		OutputTokens:  annInt(ann, "profile.ai/output-tokens", 128),
		ContextLength: annInt(ann, "profile.ai/context-length", 4096),
		Concurrency:   annInt(ann, "profile.ai/concurrency", 1),
		Annotations:   ann,
	}
	ctx, err := workloadcontext.Parse(in)
	if err != nil {
		klog.V(2).InfoS("workload context parse failed", "err", err, "pod", pod.Name, "node", nodeName)
		return &workloadcontext.Context{TraceID: traceID, Confidence: workloadcontext.ConfidenceFALLBACK}
	}

	lsu := ann["profile.ai/lsu-quota"]
	if lsu == "" {
		lsu = podLSURequest(pod)
	}
	engines := ann["profile.ai/engine-count"]
	if engines == "" {
		engines = "1"
	}
	util := ann["profile.ai/gpu-memory-utilization"]
	kv := ann["profile.ai/kv-blocks"]
	if kv == "" {
		kv = ann["profile.ai/max-model-len"]
	}
	ctx.EnvironmentKey = workloadcontext.BuildEnvironmentKey(workloadcontext.EnvironmentKeyInput{
		Model:                ctx.Model,
		Revision:             annDefault(ann, "profile.ai/model-revision", "na"),
		Runtime:              nzStr(ctx.Runtime, "unknown"),
		Accelerator:          nzStr(ctx.Accelerator, "gpu"),
		SKU:                  nzStr(sku, "na"),
		Node:                 nodeName,
		Precision:            nzStr(ctx.Precision, "auto"),
		Protocol:             annDefault(ann, "profile.ai/protocol", "na"),
		InputTokens:          ctx.InputTokens,
		OutputTokens:         ctx.OutputTokens,
		GPUMemoryUtilization: nzStr(util, "na"),
		KVBlocks:             nzStr(kv, "na"),
		MaxNumSeqs:           annDefault(ann, "profile.ai/max-num-seqs", "na"),
		LSUQuota:             nzStr(lsu, "na"),
		EngineCount:          engines,
		AllocationMode:       annDefault(ann, "profile.ai/allocation-mode", nzStr(ctx.RoutingPolicy, "na")),
	})
	return ctx
}

func annDefault(ann map[string]string, key, def string) string {
	if v := ann[key]; v != "" {
		return v
	}
	return def
}

func nzStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func podLSURequest(pod *v1.Pod) string {
	if pod == nil {
		return ""
	}
	const lsuRes = "keti.re.kr/lsu"
	for _, c := range pod.Spec.Containers {
		if q, ok := c.Resources.Requests[v1.ResourceName(lsuRes)]; ok {
			return strconv.FormatInt(q.Value(), 10)
		}
		if q, ok := c.Resources.Limits[v1.ResourceName(lsuRes)]; ok {
			return strconv.FormatInt(q.Value(), 10)
		}
	}
	return ""
}

// annForDevice prefers a device-scoped annotation (profile.ai/model-npu) over
// the generic one, since the GPU and NPU builds serve different model ids.
func annForDevice(ann map[string]string, base, devType string) string {
	if devType != "" {
		if v := ann[base+"-"+devType]; v != "" {
			return v
		}
	}
	return ann[base]
}

func annInt(ann map[string]string, key string, def int) int {
	v, ok := ann[key]
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
