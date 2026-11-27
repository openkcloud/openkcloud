// ============================================================
// policy.go: 검증 정책 해석
// 상세: AcceleratorVerificationPolicy 목록과 노드 라벨로 실효 정책을 만든다. CRD 가 하나도
// 없어도 DefaultPolicy 로 동작한다 — 검증은 정책 설치 여부와 무관하게 돌아야 한다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"sort"
	"time"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// 내장 기본값. allocation probe 는 꺼 둔다 — 매 검증마다 테스트 Pod 를 띄우면 노드가 흔들리고,
// 기존 ACPP 도 shouldReverify 로 프로브를 아껴 쓴다.
const (
	defaultTTL              = 30 * time.Minute
	defaultDriftGracePeriod = 30 * time.Second
)

// EffectivePolicy 는 한 노드에 실제로 적용되는 검증 설정이다.
type EffectivePolicy struct {
	Checks           map[string]bool
	TTL              time.Duration
	InvalidateOn     []string
	DriftGracePeriod time.Duration
}

// Enabled 는 이 체크를 돌려야 하는지다.
func (p EffectivePolicy) Enabled(check string) bool { return p.Checks[check] }

// DefaultPolicy 는 CRD 가 없을 때 쓰는 내장 기본값이다.
func DefaultPolicy() EffectivePolicy {
	return EffectivePolicy{
		Checks: map[string]bool{
			v1alpha1.EvidenceCheckDeviceObservation: true,
			v1alpha1.EvidenceCheckNodeDeviceReport:  true,
			v1alpha1.EvidenceCheckAdvertisement:     true,
			v1alpha1.EvidenceCheckAllocationProbe:   false,
		},
		TTL: defaultTTL,
		// FirmwareVersion 은 기본 감시축에서 뺀다 — 의도된 선택이다. 지문(fingerprint.go)에는
		// 담겨 ChangedFields 가 변화를 보고하므로, 감시가 필요한 노드는 정책의 invalidateOn 에
		// FirmwareVersion 을 적어 켠다.
		InvalidateOn:     []string{FieldBootID, FieldDriverVersion, FieldKernelVersion, FieldGeneration},
		DriftGracePeriod: defaultDriftGracePeriod,
	}
}

// Resolve 는 노드 라벨에 맞는 정책 하나를 골라 기본값 위에 얹는다.
//
// 여러 정책이 맞으면 이름 사전순 첫 번째가 이긴다. 병합하지 않는 이유는 추적 가능성이다 —
// 세 정책의 필드를 섞어 놓으면 "이 노드의 TTL 이 왜 5분인가" 에 답할 수 없다.
// 정책이 명시한 필드만 덮어쓰고, 비워 둔 필드는 기본값을 그대로 이어받는다.
func Resolve(list []v1alpha1.AcceleratorVerificationPolicy, nodeLabels map[string]string) EffectivePolicy {
	eff := DefaultPolicy()
	sorted := make([]v1alpha1.AcceleratorVerificationPolicy, len(list))
	copy(sorted, list)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i := range sorted {
		if !selectorMatches(sorted[i].Spec.NodeSelector, nodeLabels) {
			continue
		}
		apply(&eff, sorted[i].Spec)
		return eff
	}
	return eff
}

func apply(eff *EffectivePolicy, spec v1alpha1.AcceleratorVerificationPolicySpec) {
	if len(spec.Checks) > 0 {
		// 명시하면 그 목록이 전부다 — 기본 체크를 남기면 "이 체크만 돌려라" 를 표현할 수 없다.
		checks := make(map[string]bool, len(spec.Checks))
		for _, c := range spec.Checks {
			checks[c] = true
		}
		eff.Checks = checks
	}
	if spec.TTL != nil && spec.TTL.Duration > 0 {
		eff.TTL = spec.TTL.Duration
	}
	if len(spec.InvalidateOn) > 0 {
		eff.InvalidateOn = append([]string(nil), spec.InvalidateOn...)
	}
	if spec.DriftGracePeriod != nil && spec.DriftGracePeriod.Duration > 0 {
		eff.DriftGracePeriod = spec.DriftGracePeriod.Duration
	}
}

// selectorMatches 는 빈 selector 를 "모든 노드" 로 본다(라벨 부분집합 매칭).
func selectorMatches(sel, labels map[string]string) bool {
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}
