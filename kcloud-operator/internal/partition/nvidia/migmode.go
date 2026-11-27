// ============================================================
// migmode.go: MIG mode enable 단계 렌더 + reboot 필요 판정 (, parity 격차 ②)
// 상세: Ampere(A30)는 -mig 1 이 pending 으로만 반영되고 재부팅으로 확정된다. enable 은 "명령
// 실행 + pending 확인 + reboot + 재관측"의 4박자이며, 판정은 전부 fail-closed 다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package nvidia

// ModeEnableSteps 는 대상 PCI 각각에 nvidia-smi -mig 1 을 실행하는 단계다.
// post-check 는 하지 않는다 — Ampere 는 즉시 Enabled 로 바뀌지 않는다(pending).
func ModeEnableSteps(pcis []string) []CommandStep {
	steps := make([]CommandStep, 0, len(pcis))
	for _, p := range pcis {
		steps = append(steps, CommandStep{Argv: []string{"nvidia-smi", "-i", p, "-mig", "1"}})
	}
	return steps
}

// NeedsModeEnable 은 MIG mode 가 확실히 Disabled 인 장치가 있는지다.
// "Enabled 가 아님"이 아니라 "Disabled 임"으로 판정한다 — NA(MIG 미지원 GPU)나 Unknown(관측 실패)에
// -mig 1 을 쏘면 안 되기 때문이다(fail-closed).
func NeedsModeEnable(devs []MigDevice) bool {
	return len(ModeEnableTargets(devs)) > 0
}

// ModeEnableTargets 는 -mig 1 을 실제로 실행할 PCI 목록이다(ModeCurrent 가 Disabled 인 장치만).
func ModeEnableTargets(devs []MigDevice) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		if d.ModeCurrent == modeDisabled && d.PCI != "" {
			out = append(out, d.PCI)
		}
	}
	return out
}

// NeedsReboot 은 pending 이 Enabled 인데 current 가 아직 아닌 장치가 있는지다(재부팅으로만 확정).
func NeedsReboot(devs []MigDevice) bool {
	for _, d := range devs {
		if d.ModePending == modeEnabled && d.ModeCurrent != modeEnabled {
			return true
		}
	}
	return false
}

// ModeDisableSteps 는 대상 PCI 각각에 nvidia-smi -mig 0 을 실행하는 단계다.
// post-check 는 하지 않는다 — enable 과 대칭으로, 확정은 재부팅 뒤 재관측이 판정한다.
func ModeDisableSteps(pcis []string) []CommandStep {
	steps := make([]CommandStep, 0, len(pcis))
	for _, p := range pcis {
		steps = append(steps, CommandStep{Argv: []string{"nvidia-smi", "-i", p, "-mig", "0"}})
	}
	return steps
}

// ModeDisableTargets 는 -mig 0 을 실제로 실행할 PCI 목록이다(ModeCurrent 가 Enabled 인 장치만).
// "Disabled 가 아님" 이 아니라 "Enabled 임" 으로 판정한다 — NA(MIG 미지원)나 Unknown(관측 실패)에
// 명령을 쏘면 안 된다(fail-closed, ModeEnableTargets 와 같은 규율).
func ModeDisableTargets(devs []MigDevice) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		if d.ModeCurrent == modeEnabled && d.PCI != "" {
			out = append(out, d.PCI)
		}
	}
	return out
}

// NeedsModeDisable 은 MIG mode 가 확실히 Enabled 인 장치가 있는지다.
func NeedsModeDisable(devs []MigDevice) bool { return len(ModeDisableTargets(devs)) > 0 }

// ModeObservable 은 mode 전환 판단에 쓸 만큼 관측이 신뢰 가능한지다(fail-closed 게이트).
// 관측 에러·Unknown·빈 값이 하나라도 있으면 전환을 시작하지 않는다 — 노드를 cordon 하고
// 재부팅까지 하는 절차를, 상태를 모르는 채로 시작하면 안 된다.
func ModeObservable(devs []MigDevice) bool {
	for _, d := range devs {
		if d.ObsError != "" || d.ModeCurrent == "" || d.ModeCurrent == modeUnknown {
			return false
		}
	}
	return true
}
