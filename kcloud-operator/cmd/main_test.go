// ============================================================
// main_test.go: --version 플래그 출력(versionLine)의 단위 시험
// 상세: ldflags 로 박힌 buildCommit 이 그대로 나오고, 비면 unknown 으로 대신한다.
// 생성일: 2026-09-21 | 수정일: 2026-10-11
// ============================================================
package main

import "testing"

func TestVersionLine(t *testing.T) {
	saved := buildCommit
	t.Cleanup(func() { buildCommit = saved })

	buildCommit = "abc1234"
	if got := versionLine(); got != "abc1234" {
		t.Fatalf("versionLine() = %q, want abc1234", got)
	}
	buildCommit = ""
	if got := versionLine(); got != "unknown" {
		t.Fatalf("빈 buildCommit 은 unknown 이어야 한다: %q", got)
	}
}
