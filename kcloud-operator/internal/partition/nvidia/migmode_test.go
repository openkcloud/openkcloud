// ============================================================
// migmode_test.go: MIG mode enable 단계·판정 테스트
// 상세: -mig 1 시퀀스와 pending→reboot 판정. 실제 실행은 JobExecutor seam.
// 생성일: 2026-07-29 | 수정일: 2026-07-29
// ============================================================
package nvidia

import (
	"strings"
	"testing"
)

func TestModeEnableStepsPerPCI(t *testing.T) {
	steps := ModeEnableSteps([]string{"0000:18:00.0", "0000:3b:00.0"})
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	joined := strings.Join(steps[0].Argv, " ")
	if !strings.Contains(joined, "-i 0000:18:00.0") || !strings.Contains(joined, "-mig 1") {
		t.Fatalf("step 0 argv = %v", steps[0].Argv)
	}
	if steps[0].Optional {
		t.Fatalf("mode enable step must not be optional (fail-closed)")
	}
	// post-check 를 걸면 Ampere 는 즉시 Enabled 가 아니라 pending 이라 항상 실패한다.
	if len(steps[0].ExpectOneOf) != 0 || steps[0].ExpectEmpty {
		t.Fatalf("mode enable step must not assert immediate state: %+v", steps[0])
	}
}

func TestNeedsModeEnable(t *testing.T) {
	if !NeedsModeEnable([]MigDevice{{PCI: "a", ModeCurrent: "Disabled"}}) {
		t.Fatal("Disabled must need enable")
	}
	if NeedsModeEnable([]MigDevice{{PCI: "a", ModeCurrent: "Enabled"}}) {
		t.Fatal("Enabled must not need enable")
	}
}

// TestNeedsModeEnableIgnoresUnobservableDevices 는 fail-closed 를 검증한다 — MIG 미지원(NA)이나
// 관측 실패(Unknown) 장치에 -mig 1 을 쏘면 안 된다("!= Enabled" 로 판정하면 여기서 깨진다).
func TestNeedsModeEnableIgnoresUnobservableDevices(t *testing.T) {
	if NeedsModeEnable([]MigDevice{{PCI: "a", ModeCurrent: "NA"}}) {
		t.Fatal("non-MIG device (NA) must not trigger mode enable")
	}
	if NeedsModeEnable([]MigDevice{{PCI: "a", ModeCurrent: "Unknown"}}) {
		t.Fatal("unobserved device must not trigger mode enable")
	}
}

func TestNeedsRebootOnlyWhenPendingDiffersFromCurrent(t *testing.T) {
	if !NeedsReboot([]MigDevice{{PCI: "a", ModeCurrent: "Disabled", ModePending: "Enabled"}}) {
		t.Fatal("pending Enabled + current Disabled must need reboot")
	}
	if NeedsReboot([]MigDevice{{PCI: "a", ModeCurrent: "Enabled", ModePending: "Enabled"}}) {
		t.Fatal("already enabled must not need reboot")
	}
	if NeedsReboot([]MigDevice{{PCI: "a", ModeCurrent: "Disabled", ModePending: "Disabled"}}) {
		t.Fatal("no pending change must not need reboot")
	}
}

func TestModeEnableTargetsSelectsOnlyDisabled(t *testing.T) {
	got := ModeEnableTargets([]MigDevice{
		{PCI: "a", ModeCurrent: "Disabled"},
		{PCI: "b", ModeCurrent: "Enabled"},
		{PCI: "c", ModeCurrent: "NA"},
	})
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("targets = %v, want [a]", got)
	}
}

// TestModeObservableIsFailClosed 는 관측이 신뢰 불가하면 mode 전환을 시작하지 않음을 검증한다.
func TestModeObservableIsFailClosed(t *testing.T) {
	if ModeObservable([]MigDevice{{PCI: "a", ModeCurrent: "Disabled", ObsError: "job failed"}}) {
		t.Fatal("ObsError must block")
	}
	if ModeObservable([]MigDevice{{PCI: "a", ModeCurrent: "Unknown"}}) {
		t.Fatal("Unknown must block")
	}
	if ModeObservable([]MigDevice{{PCI: "a", ModeCurrent: ""}}) {
		t.Fatal("empty mode must block")
	}
	if !ModeObservable([]MigDevice{{PCI: "a", ModeCurrent: "Disabled", ModePending: "Disabled"}}) {
		t.Fatal("clean observation must pass")
	}
}
