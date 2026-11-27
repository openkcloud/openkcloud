// ============================================================
// migmode_disable_test.go: MIG 모드 해제 단계 렌더 테스트
// 상세: 관측이 확실한 장치에만 명령을 쏜다는 규율(enable 과 대칭)을 고정한다.
// 생성일: 2026-08-01
// ============================================================
package nvidia

import "testing"

// 증명: Enabled 로 관측된 장치에만 해제 명령이 나간다.
// 깨는 뮤테이션: 판정을 "Disabled 가 아님" 으로 바꾸면 미지원·관측실패 장치까지 대상이 되어 실패한다.
func TestModeDisableTargetsOnlyEnabledDevices(t *testing.T) {
	devs := []MigDevice{
		{PCI: "0000:41:00.0", ModeCurrent: modeEnabled},
		{PCI: "0000:81:00.0", ModeCurrent: modeDisabled},
		{PCI: "0000:a1:00.0", ModeCurrent: modeNA},
		{PCI: "0000:c1:00.0", ModeCurrent: modeUnknown},
		{PCI: "", ModeCurrent: modeEnabled},
	}
	got := ModeDisableTargets(devs)
	if len(got) != 1 || got[0] != "0000:41:00.0" {
		t.Fatalf("targets = %v", got)
	}
	if !NeedsModeDisable(devs) {
		t.Fatalf("Enabled 장치가 있는데 해제 불필요로 판정")
	}
	if NeedsModeDisable(devs[1:2]) {
		t.Fatalf("Disabled 장치만 있는데 해제 필요로 판정")
	}
}

// 증명: 렌더된 명령이 정확히 -mig 0 이다.
// 깨는 뮤테이션: 인자를 1 로 바꾸면(오타 한 글자) 모드를 되돌리는 대신 켜게 되어 실패한다.
func TestModeDisableStepsRenderMigZero(t *testing.T) {
	steps := ModeDisableSteps([]string{"0000:41:00.0"})
	if len(steps) != 1 {
		t.Fatalf("steps = %+v", steps)
	}
	want := []string{"nvidia-smi", "-i", "0000:41:00.0", "-mig", "0"}
	for i, a := range want {
		if steps[0].Argv[i] != a {
			t.Fatalf("argv = %v, want %v", steps[0].Argv, want)
		}
	}
}
