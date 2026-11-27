package decisionrecorder

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestReviewP6RecorderConcurrentClose checks the Close/Enqueue race:
// an event Enqueue accepted (true) must be written or counted as a write error
// after Close returns — never silently lost with written=0, dropped=0.
func TestReviewP6RecorderConcurrentClose(t *testing.T) {
	for round := 0; round < 200; round++ {
		path := filepath.Join(t.TempDir(), "events.jsonl")
		r, err := New(path, 64)
		if err != nil {
			t.Fatal(err)
		}

		var accepted atomic.Uint64
		var producers sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 32; i++ {
			producers.Add(1)
			go func(id int) {
				defer producers.Done()
				<-start
				if r.Enqueue(Event{Kind: KindSchedulingAttempt, AttemptID: "c"}) {
					accepted.Add(1)
				}
			}(i)
		}

		close(start)
		// Close while producers are still racing Enqueue.
		go r.Close()
		producers.Wait()
		r.Close() // idempotent join

		written, dropped := r.Stats()
		writeErr := r.WriteErrors()
		got := accepted.Load()
		if got != written+writeErr {
			t.Fatalf("round %d: accepted=%d written=%d dropped=%d write_errors=%d (accepted events must be written or write-failed)",
				round, got, written, dropped, writeErr)
		}
		if got > 0 && written == 0 && writeErr == 0 {
			t.Fatalf("round %d: accepted in-flight event lost after Close: accepted=%d written=0 dropped=%d write_errors=0",
				round, got, dropped)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := 0
		for _, c := range b {
			if c == '\n' {
				lines++
			}
		}
		if uint64(lines) != written {
			t.Fatalf("round %d: jsonl lines=%d written=%d", round, lines, written)
		}
	}
}
