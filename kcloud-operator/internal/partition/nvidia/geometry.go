// ============================================================
// geometry.go: MIG 목표 → typed CommandStep 시퀀스(GI 생성만, mode 전환 없음)
// 상세: 균일 단일 프로파일. PCI 선택자 + profile name. MIG mode Enabled 는 외부 사전조건이라
// operator 는 -mig 를 만지지 않고 GI 만 생성/제거한다.
// 생성일: 2026-07-24 | 수정일: 2026-10-11
// ============================================================
package nvidia

import "strings"

type CommandStep struct {
	Argv        []string
	ExpectEmpty bool
	ExpectOneOf []string
	// Optional 이면 non-zero 종료를 무시한다(best-effort). 기존 GI/CI 제거(-dci/-dgi)는 대상이
	// 없을 때 "No GPU instances found: Not Found" 로 non-zero 종료하므로 이를 관용해야 한다.
	Optional bool
}

type GeometrySpec struct {
	GPUSelector string
	ProfileName string
	Count       int32
}

func q(sel string, field string) []string {
	return []string{"nvidia-smi", "-i", sel, "--query-gpu=" + field, "--format=csv,noheader"}
}

// BuildApplySteps 는 apply 시퀀스를 만든다: MIG mode Enabled 는 사전조건이므로
// preflight 로 current==Enabled 를 assert 만 하고(-mig 전환 없음), 기존 GI/CI 제거 후 GI 를 생성한다.
func BuildApplySteps(specs []GeometrySpec) []CommandStep {
	steps := make([]CommandStep, 0, len(specs)*6) // spec 당 6 스텝
	for _, s := range specs {
		names := make([]string, 0, s.Count)
		for i := int32(0); i < s.Count; i++ {
			names = append(names, s.ProfileName)
		}
		steps = append(steps,
			CommandStep{Argv: []string{"nvidia-smi", "-i", s.GPUSelector, "--query-compute-apps=pid", "--format=csv,noheader"}, ExpectEmpty: true},
			CommandStep{Argv: q(s.GPUSelector, "mig.mode.current"), ExpectOneOf: []string{"Enabled"}},
			CommandStep{Argv: q(s.GPUSelector, "mig.mode.pending"), ExpectOneOf: []string{"Enabled", "Disabled", "N/A", "[N/A]"}},
			CommandStep{Argv: []string{"nvidia-smi", "mig", "-i", s.GPUSelector, "-dci"}, Optional: true},
			CommandStep{Argv: []string{"nvidia-smi", "mig", "-i", s.GPUSelector, "-dgi"}, Optional: true},
			CommandStep{Argv: []string{"nvidia-smi", "mig", "-i", s.GPUSelector, "-cgi", strings.Join(names, ","), "-C"}},
		)
	}
	return steps
}

// BuildDisableSteps 는 rollback 시퀀스를 만든다: mode 는 끄지 않고 GI/CI 만 제거한 뒤
// mode 가 정상 판독되는지 확인한다.
// 사후 확인은 Enabled 만 받으면 안 된다 — RestoreMode 삭제 경로는 mode 를 먼저 끄고(재부팅) 나서
// 이 함수를 부르므로 그 경로에서는 Disabled 가 정상이다. Enabled 만 받으면 이미 되돌아간 노드에서
// job 이 실패해 finalizer 가 영원히 안 빠진다(7분 넘게 Terminating, 노드는 cordon 된 채 방치된다).
// 확인 목적은 "조각이 사라졌다" 이지 "mode 가 켜져 있다" 가 아니므로 두 값 다 받는다(판독 불가는 여전히 실패).
func BuildDisableSteps(selectors []string) []CommandStep {
	steps := make([]CommandStep, 0, len(selectors)*3) // selector 당 3 스텝
	for _, sel := range selectors {
		steps = append(steps,
			CommandStep{Argv: []string{"nvidia-smi", "mig", "-i", sel, "-dci"}, Optional: true},
			CommandStep{Argv: []string{"nvidia-smi", "mig", "-i", sel, "-dgi"}, Optional: true},
			CommandStep{Argv: q(sel, "mig.mode.current"), ExpectOneOf: []string{"Enabled", "Disabled"}},
		)
	}
	return steps
}
