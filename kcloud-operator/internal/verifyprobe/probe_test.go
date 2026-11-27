// ============================================================
// probe_test.go: 검증 probe 표의 조회 시험
// 생성일: 2026-09-30
// ============================================================
package verifyprobe

import "testing"

func TestLookupKnownVendor(t *testing.T) {
	p, ok := Lookup("rebellions")
	if !ok || p.Command != "rbln-stat" || !p.Privileged || !p.HostPID {
		t.Fatalf("rebellions probe = %+v, ok=%v", p, ok)
	}
}

func TestLookupUnknownVendor(t *testing.T) {
	if _, ok := Lookup("unknown"); ok {
		t.Fatal("미등록 벤더는 false 여야 한다")
	}
}

func TestVendorsSorted(t *testing.T) {
	got := Vendors()
	want := []string{"furiosa", "nvidia", "rebellions", "tenstorrent"}
	if len(got) != len(want) {
		t.Fatalf("Vendors() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Vendors() = %v, want %v", got, want)
		}
	}
}
