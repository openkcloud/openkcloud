// ============================================================
// migjob_test.go: JobExecutor OperationID/renderMigJob/scriptFor 단위 테스트
// 상세: 이름/cmd-hash 결정론성 + 63자 상한 + assert 스크립트 생성 검증.
// 생성일: 2026-07-24
// ============================================================
package nvidia

import (
	"strings"
	"testing"
)

func TestOperationID_StableAndDistinct(t *testing.T) {
	steps := []CommandStep{
		{Argv: []string{"nvidia-smi", "-i", "0", "-mig", "1"}},
	}
	name1, hash1 := OperationID("11112222-uid", 3, "worker1", "apply", steps)
	name2, hash2 := OperationID("11112222-uid", 3, "worker1", "apply", steps)
	if name1 != name2 || hash1 != hash2 {
		t.Fatalf("same inputs must be stable: (%q,%q) vs (%q,%q)", name1, hash1, name2, hash2)
	}

	nameGen, hashGen := OperationID("11112222-uid", 4, "worker1", "apply", steps)
	if nameGen == name1 {
		t.Fatalf("generation 변경 시 name 이 달라져야 함: %q", nameGen)
	}
	if hashGen != hash1 {
		t.Fatalf("generation 변경은 cmdHash 에 영향 없어야 함: %q vs %q", hashGen, hash1)
	}

	otherSteps := []CommandStep{
		{Argv: []string{"nvidia-smi", "-i", "0", "-mig", "0"}},
	}
	nameOther, hashOther := OperationID("11112222-uid", 3, "worker1", "apply", otherSteps)
	if hashOther == hash1 {
		t.Fatalf("steps 변경 시 cmdHash 가 달라져야 함")
	}
	if nameOther == name1 {
		t.Fatalf("cmdHash 변경은 name(suffix) 에도 반영되어야 함")
	}

	for _, tc := range []struct{ name, hash string }{{name1, hash1}, {nameGen, hashGen}, {nameOther, hashOther}} {
		if len(tc.name) > 63 {
			t.Errorf("name %q exceeds 63 chars (%d)", tc.name, len(tc.name))
		}
		if !strings.HasSuffix(tc.name, "-"+tc.hash[:8]) {
			t.Errorf("name %q must end with the 8-char cmdHash suffix %q", tc.name, tc.hash[:8])
		}
	}

	// 매우 긴 노드명/action 으로 63자 초과 유도 — truncate 되어도 hash suffix 는 보존.
	longNode := strings.Repeat("very-long-node-name-", 5)
	longAction := strings.Repeat("action", 10)
	nameLong, hashLong := OperationID("11112222-uid", 999999, longNode, longAction, steps)
	if len(nameLong) > 63 {
		t.Errorf("truncated name %q still exceeds 63 chars (%d)", nameLong, len(nameLong))
	}
	if !strings.HasSuffix(nameLong, "-"+hashLong[:8]) {
		t.Errorf("truncated name %q must preserve hash suffix %q", nameLong, hashLong[:8])
	}
}

func TestRenderMigJob_HostPidTTL(t *testing.T) {
	steps := []CommandStep{
		{Argv: []string{"nvidia-smi", "-i", "0", "-mig", "1"}},
	}
	name, cmdHash := OperationID("uid-abcdef", 1, "worker1", "apply", steps)
	job := renderMigJob(name, cmdHash, "worker1", steps, "example.com/mig-exec:latest")

	if !job.Spec.Template.Spec.HostPID {
		t.Error("HostPID must be true")
	}
	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Errorf("RestartPolicy must be Never, got %q", job.Spec.Template.Spec.RestartPolicy)
	}
	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Fatal("TTLSecondsAfterFinished must be set")
	}
	if got := job.Annotations[CmdHashAnnotation]; got != cmdHash {
		t.Errorf("cmd-hash annotation = %q, want %q", got, cmdHash)
	}
}

func TestScriptFor_AssertsExpect(t *testing.T) {
	sc := scriptFor([]CommandStep{
		{Argv: []string{"nvidia-smi", "-i", "P", "--query-gpu=mig.mode.current", "--format=csv,noheader"}, ExpectOneOf: []string{"Enabled"}},
	})
	if !strings.Contains(sc, "Enabled") || !strings.Contains(sc, "exit 1") {
		t.Fatal("ExpectOneOf assert(불일치 exit 1) 필요")
	}
}
