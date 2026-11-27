package scheduler

import (
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/decisionrecorder"
)

// PhaseTiming holds per-attempt clocks. Instants are taken with time.Now() so
// Sub() keeps the monotonic reading; UTC conversion happens only when emitting
// JSONL wall-clock strings.
//
// PhaseTiming clocks include PreScore (unique-key Profile prep) before Score.
type PhaseTiming struct {
	AttemptStartedAt time.Time
	AttemptEndedAt   time.Time // set on every terminal path; never invent BindEnd
	PreFilterStart   time.Time
	PreFilterEnd     time.Time
	FilterStart      time.Time
	FilterEnd        time.Time
	HamiStart        time.Time
	HamiEnd          time.Time
	PreScoreStart    time.Time
	PreScoreEnd      time.Time
	ScoreStart       time.Time
	ScoreEnd         time.Time
	SelectStart      time.Time
	SelectEnd        time.Time
	BindStart        time.Time
	BindEnd          time.Time
	SuggestedHost    string
	Outcome          string // select_ok | success | fail_*
	FailStage        string
	// EventRole: interim_select (judgment path only) vs terminal (final).
	EventRole string
}

func msBetween(a, b time.Time) any {
	if a.IsZero() || b.IsZero() {
		return nil
	}
	d := b.Sub(a)
	if d < 0 {
		return map[string]any{
			"status": "measurement_error",
			"reason": "clock_regression",
			"raw_ms": float64(d) / float64(time.Millisecond),
		}
	}
	return float64(d) / float64(time.Millisecond)
}

func boundaryNotes() map[string]any {
	return map[string]any{
		"filter_ms":            "runFilterPlugin only; excludes subsequent HAMi filter",
		"hami_filter_ms":       "HAMi extender Filter only; null when HAMi path not entered",
		"prescore_ms":          "RunPreScorePlugins: unique full-lookup-key Catalog prep; Score should not re-fetch prepared keys",
		"score_ms":             "RunScorePlugins scoring from prepared/shared state (legacy Score-only tests may still lookup)",
		"select_ms":            "selectResource including CommitPlacementDecisions/PostDecision",
		"attempt_to_select_ms": "attempt start → after selectResource return",
		"bind_ms":              "from before Permit through binder + cache/queue cleanup; not binder-API-only",
		"attempt_to_terminal_ms": "attempt start → AttemptEndedAt on every terminal path; BindEnd is not invented on pre-bind failures",
		"unclassified":         "gaps between phase sums and attempt_to_terminal_ms include HAMi (if any), candidate JSONL, and schedulingCycle glue",
	}
}

// markTerminal sets the attempt end clock. Does not set BindEnd.
func (t *PhaseTiming) markTerminal(outcome, failStage string) {
	if t == nil {
		return
	}
	t.AttemptEndedAt = time.Now()
	t.Outcome = outcome
	t.FailStage = failStage
	t.EventRole = "terminal"
}

func (t *PhaseTiming) markInterimSelect() {
	if t == nil {
		return
	}
	t.Outcome = "select_ok"
	t.FailStage = ""
	t.EventRole = "interim_select"
}

// Payload builds the JSONL body. metric_class marks verification-only clocks.
func (t *PhaseTiming) Payload() map[string]any {
	if t == nil {
		return nil
	}
	p := map[string]any{
		"metric_class":              "verification_not_official_kpi7",
		"boundary_note":             "compare with default scheduler only after Bind inclusion is confirmed",
		"boundaries":                boundaryNotes(),
		"event_role":                t.EventRole,
		"outcome":                   t.Outcome,
		"fail_stage":                t.FailStage,
		"suggested_host":            t.SuggestedHost,
		"attempt_started_at":        rfc3339UTC(t.AttemptStartedAt),
		"attempt_ended_at":          rfc3339UTC(t.AttemptEndedAt),
		"prefilter_ms":              msBetween(t.PreFilterStart, t.PreFilterEnd),
		"filter_ms":                 msBetween(t.FilterStart, t.FilterEnd),
		"hami_filter_ms":            msBetween(t.HamiStart, t.HamiEnd),
		"prescore_ms":               msBetween(t.PreScoreStart, t.PreScoreEnd),
		"score_ms":                  msBetween(t.ScoreStart, t.ScoreEnd),
		"select_ms":                 msBetween(t.SelectStart, t.SelectEnd),
		"attempt_to_select_ms":      msBetween(t.AttemptStartedAt, t.SelectEnd),
		"bind_ms":                   msBetween(t.BindStart, t.BindEnd),
		"attempt_to_bind_result_ms": msBetween(t.AttemptStartedAt, t.BindEnd),
		"select_to_bind_start_ms":   msBetween(t.SelectEnd, t.BindStart),
		// Primary total for success and pre-bind failure alike.
		"attempt_to_terminal_ms": msBetween(t.AttemptStartedAt, t.AttemptEndedAt),
	}
	return p
}

func rfc3339UTC(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	// Wall-clock string only; do not use the returned Time for Sub().
	return t.UTC().Format(time.RFC3339Nano)
}

func (sched *Scheduler) recordPhaseTiming(attemptID string, timing *PhaseTiming) {
	if sched.DecisionRecorder == nil || timing == nil || attemptID == "" {
		return
	}
	sched.DecisionRecorder.Enqueue(decisionrecorder.Event{
		Kind:      decisionrecorder.KindPhaseTiming,
		AttemptID: attemptID,
		NodeName:  timing.SuggestedHost,
		Payload:   timing.Payload(),
	})
}
