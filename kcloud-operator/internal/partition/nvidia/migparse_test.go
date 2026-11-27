// migparse_test.go: nvidia-smi mig -lgip 파서 단위 테스트
package nvidia

import (
	"os"
	"testing"
)

func TestParseMigProfiles(t *testing.T) {
	raw, err := os.ReadFile("testdata/lgip_a30.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := ParseMigProfiles(string(raw))
	want := map[string]int32{"1g.6gb": 4, "2g.12gb": 2, "4g.24gb": 1}
	wantMem := map[string]int32{"1g.6gb": 6, "2g.12gb": 12, "4g.24gb": 24}
	if len(got) != len(want) {
		t.Fatalf("got %d profiles, want %d: %+v", len(got), len(want), got)
	}
	for _, p := range got {
		if want[p.Name] != p.MaxInstances {
			t.Errorf("%s maxInstances=%d, want %d", p.Name, p.MaxInstances, want[p.Name])
		}
		if wantMem[p.Name] != p.MemoryGB {
			t.Errorf("%s memoryGB=%d, want %d", p.Name, p.MemoryGB, wantMem[p.Name])
		}
	}
}

func TestParseMigProfiles_Empty(t *testing.T) {
	if got := ParseMigProfiles("No MIG-capable devices found."); len(got) != 0 {
		t.Errorf("expected 0 profiles, got %+v", got)
	}
}
