package nvidia

import (
	"strings"
	"testing"
)

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// preflight current==Enabled(사전조건), -mig 전환 없음, GI 생성.
func TestBuildApplySteps_PreEnabledNoModeSwitch(t *testing.T) {
	steps := BuildApplySteps([]GeometrySpec{{GPUSelector: "00000000:18:00.0", ProfileName: "1g.6gb", Count: 4}})
	var preEnabledOK, cgiOK, sawMigSwitch bool
	for _, s := range steps {
		j := strings.Join(s.Argv, " ")
		if strings.Contains(j, "mig.mode.current") && eq(s.ExpectOneOf, []string{"Enabled"}) {
			preEnabledOK = true
		}
		if strings.Contains(j, "-cgi 1g.6gb,1g.6gb,1g.6gb,1g.6gb") {
			cgiOK = true
		}
		if strings.Contains(j, "-mig 1") || strings.Contains(j, "-mig 0") {
			sawMigSwitch = true
		}
	}
	if !preEnabledOK || !cgiOK {
		t.Fatalf("preflight(current==Enabled)+cgi(name) 필요: pre=%v cgi=%v", preEnabledOK, cgiOK)
	}
	if sawMigSwitch {
		t.Fatal("apply 는 mode 전환(-mig)을 하지 않아야 함")
	}
}

func TestBuildDisableSteps_KeepsModeEnabled(t *testing.T) {
	steps := BuildDisableSteps([]string{"PCI"})
	var dgiOK, sawMigSwitch bool
	for _, s := range steps {
		j := strings.Join(s.Argv, " ")
		if strings.Contains(j, "-dgi") {
			dgiOK = true
		}
		if strings.Contains(j, "-mig 0") {
			sawMigSwitch = true
		}
	}
	if !dgiOK {
		t.Fatal("disable 은 GI 제거(-dgi)를 포함해야 함")
	}
	if sawMigSwitch {
		t.Fatal("rollback 은 mode 를 끄지 않아야 함(-mig 0 금지)")
	}
	last := steps[len(steps)-1]
	// 되돌리기가 mode 를 끄지는 않지만, RestoreMode 삭제 경로는 mode 를 먼저 끄고 이 시퀀스를
	// 부른다 — 그때 Disabled 를 실패로 보면 이미 복원된 노드에서 job 이 실패해 finalizer 가
	// 영원히 남는다.
	if !eq(last.ExpectOneOf, []string{"Enabled", "Disabled"}) {
		t.Fatalf("disable 마지막 step 은 mode 판독 확인이어야(Enabled/Disabled 허용): %+v", last)
	}
}
