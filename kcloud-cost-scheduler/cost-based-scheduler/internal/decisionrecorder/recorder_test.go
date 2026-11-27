package decisionrecorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"
)

func TestRecorderEnqueueAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "decisions.jsonl")
	r, err := New(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	ok := r.Enqueue(Event{Kind: KindSchedulingAttempt, AttemptID: "a1", Payload: map[string]any{"pod": "p"}})
	if !ok {
		t.Fatal("enqueue failed")
	}
	r.RecordCandidate("a1", "n1", placementchecks.CandidateReport{NodeName: "n1"})
	time.Sleep(50 * time.Millisecond)
	r.Close()
	written, dropped := r.Stats()
	if written < 1 || dropped != 0 {
		t.Fatalf("written=%d dropped=%d", written, dropped)
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		t.Fatalf("file empty or err=%v", err)
	}
}

func TestRecorderDropsWhenFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "full.jsonl")
	r, err := New(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_ = r.Enqueue(Event{Kind: KindSchedulingAttempt, AttemptID: "a"})
	// Fill and overflow without waiting for writer.
	for i := 0; i < 20; i++ {
		_ = r.Enqueue(Event{Kind: KindSchedulingAttempt, AttemptID: "x"})
	}
	_, dropped := r.Stats()
	if dropped == 0 {
		t.Fatal("expected drops when queue saturated")
	}
}
