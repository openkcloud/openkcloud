package decisionrecorder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderSaturationStatsMatchFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sat.jsonl")
	r, err := New(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for i := 0; i < 40; i++ {
		if r.Enqueue(Event{Kind: KindSchedulingAttempt, AttemptID: "s"}) {
			accepted++
		}
	}
	// Let writer catch some before close drain.
	time.Sleep(20 * time.Millisecond)
	r.Close()

	written, dropped := r.Stats()
	if accepted == 0 {
		t.Fatal("expected some accepts")
	}
	if dropped == 0 {
		t.Fatal("expected drops under saturation")
	}
	if written+r.WriteErrors() != uint64(accepted) {
		t.Fatalf("accepted=%d written=%d write_errors=%d dropped=%d", accepted, written, r.WriteErrors(), dropped)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	if uint64(lines) != written {
		t.Fatalf("file lines=%d written=%d", lines, written)
	}
}
