package utils

import (
	"context"
	"sync"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"
)

type attemptKeyType struct{}

var attemptKey = attemptKeyType{}

// AttemptState holds per-scheduling-attempt inputs and node reports so required
// checks and legacy Filters share one evaluation (no double judgment).
type AttemptState struct {
	Meta   placementchecks.AttemptMeta
	Snap   placementchecks.Snapshot
	Req    placementchecks.ParsedRequest
	Config placementchecks.Config
	Engine *placementchecks.Engine

	mu      sync.Mutex
	reports map[string]placementchecks.CandidateReport
	// sharedEval counts EvaluateNode calls per node (tests assert ≤1).
	evalCount map[string]int
}

// NewAttemptState builds attempt-scoped state. Engine may be nil → checks skipped.
func NewAttemptState(meta placementchecks.AttemptMeta, snap placementchecks.Snapshot, req placementchecks.ParsedRequest, cfg placementchecks.Config) *AttemptState {
	return &AttemptState{
		Meta:      meta,
		Snap:      snap,
		Req:       req,
		Config:    cfg,
		Engine:    placementchecks.NewEngine(cfg),
		reports:   map[string]placementchecks.CandidateReport{},
		evalCount: map[string]int{},
	}
}

// WithAttempt attaches AttemptState to ctx.
func WithAttempt(ctx context.Context, st *AttemptState) context.Context {
	return context.WithValue(ctx, attemptKey, st)
}

// AttemptFrom returns AttemptState if present.
func AttemptFrom(ctx context.Context) *AttemptState {
	st, _ := ctx.Value(attemptKey).(*AttemptState)
	return st
}

// Report returns a cached CandidateReport for nodeName.
func (s *AttemptState) Report(nodeName string) (placementchecks.CandidateReport, bool) {
	if s == nil {
		return placementchecks.CandidateReport{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reports[nodeName]
	return r, ok
}

// EnsureEvaluated runs the required-check engine once per node and caches the report.
func (s *AttemptState) EnsureEvaluated(in placementchecks.EvalInput) placementchecks.CandidateReport {
	if s == nil || s.Engine == nil {
		return placementchecks.CandidateReport{NodeName: in.NodeName}
	}
	s.mu.Lock()
	if r, ok := s.reports[in.NodeName]; ok {
		s.mu.Unlock()
		return r
	}
	s.mu.Unlock()

	in.Meta = s.Meta
	in.Snap = s.Snap
	if in.Now.IsZero() && !s.Meta.StartedAt.IsZero() {
		in.Now = s.Meta.StartedAt
	}
	rep := s.Engine.EvaluateNode(in)

	s.mu.Lock()
	if r, ok := s.reports[in.NodeName]; ok {
		s.mu.Unlock()
		return r
	}
	s.evalCount[in.NodeName]++
	s.reports[in.NodeName] = rep
	s.mu.Unlock()
	return rep
}

// EvalCountForTest returns how many times EnsureEvaluated computed a node.
func (s *AttemptState) EvalCountForTest(nodeName string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evalCount[nodeName]
}
