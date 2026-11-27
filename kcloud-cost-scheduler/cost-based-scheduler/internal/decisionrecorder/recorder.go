package decisionrecorder

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"
)

// Event kinds for local JSONL (P6). Catalog DB is out of scope.
const (
	KindSchedulingAttempt    = "scheduling_attempt"
	KindCandidateCheckResult = "candidate_check_result"
	KindPlacementDecision    = "placement_decision"
	KindBindOutcome          = "bind_outcome"
	// KindPhaseTiming records attempt→select and Bind path clocks for scheduling-latency
	// baseline verification. Not a final comparison until default-scheduler
	// metric boundaries are confirmed equal.
	KindPhaseTiming = "phase_timing"
)

// Event is one JSONL line.
type Event struct {
	Kind      string         `json:"kind"`
	AttemptID string         `json:"attempt_id,omitempty"`
	NodeName  string         `json:"node_name,omitempty"`
	At        time.Time      `json:"at"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// Recorder writes scheduling decision events asynchronously with a bounded queue.
type Recorder struct {
	path     string
	queue    chan Event
	done     chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
	dropped  uint64
	written  uint64
	writeErr uint64
	stopOnce sync.Once
}

// New opens path for append and starts a single writer goroutine.
// queueSize <= 0 defaults to 256.
func New(path string, queueSize int) (*Recorder, error) {
	if path == "" {
		return nil, fmt.Errorf("decisionrecorder: path required")
	}
	if queueSize <= 0 {
		queueSize = 256
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	r := &Recorder{
		path:  path,
		queue: make(chan Event, queueSize),
		done:  make(chan struct{}),
	}
	r.wg.Add(1)
	go r.loop()
	return r, nil
}

// Enqueue non-blocking; drops when full and increments dropped counter.
// Returns false when closed or when the event is dropped.
// closed 확인과 큐 접수는 같은 잠금 안에서 Close와 직렬화된다.
func (r *Recorder) Enqueue(ev Event) bool {
	if r == nil {
		return false
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	select {
	case r.queue <- ev:
		return true
	default:
		r.dropped++
		return false
	}
}

// RecordCandidate writes the final candidate judgment (required checks + legacy/HAMi).
func (r *Recorder) RecordCandidate(attemptID, node string, rep placementchecks.CandidateReport) {
	r.RecordCandidateFinal(attemptID, node, map[string]any{
		"is_filtered":       rep.IsFiltered,
		"rejection_reasons": rep.RejectionReasons,
		"checks":            rep.Checks,
	})
}

// RecordCandidateFinal writes an arbitrary candidate payload (final IsFiltered/reasons).
func (r *Recorder) RecordCandidateFinal(attemptID, node string, payload map[string]any) {
	r.Enqueue(Event{
		Kind: KindCandidateCheckResult, AttemptID: attemptID, NodeName: node, Payload: payload,
	})
}

// Stats returns written/dropped counts.
func (r *Recorder) Stats() (written, dropped uint64) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.written, r.dropped
}

// WriteErrors returns how many encode/write failures were observed.
func (r *Recorder) WriteErrors() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeErr
}

// Close stops accepting events, drains the queue, then joins the writer.
// closed=true is set under the same mutex Enqueue uses for queue send, so a
// concurrent Enqueue cannot observe open then send after the writer has exited.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		close(r.done)
		r.wg.Wait()
	})
}

func (r *Recorder) loop() {
	defer r.wg.Done()
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	write := func(ev Event) {
		if err := enc.Encode(ev); err != nil {
			r.mu.Lock()
			r.writeErr++
			r.mu.Unlock()
			return
		}
		r.mu.Lock()
		r.written++
		r.mu.Unlock()
	}
	for {
		select {
		case <-r.done:
			for {
				select {
				case ev := <-r.queue:
					write(ev)
				default:
					return
				}
			}
		case ev := <-r.queue:
			write(ev)
		}
	}
}
