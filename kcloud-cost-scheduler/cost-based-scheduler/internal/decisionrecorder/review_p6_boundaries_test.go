package decisionrecorder

import (
	"path/filepath"
	"testing"
)

func TestReviewP6R7ClosedRecorderDoesNotAcceptEvents(t *testing.T) {
	r, err := New(filepath.Join(t.TempDir(), "events.jsonl"), 4)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if r.Enqueue(Event{Kind: KindSchedulingAttempt, AttemptID: "after-close"}) {
		t.Fatal("Enqueue returned true after writer exited; acknowledged event is never written")
	}
}
