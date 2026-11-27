// ============================================================
// policy.go: AcceleratorHealthPolicy 목록 → 실효 정책
// 상세: 정책이 하나도 없어도 감시가 돌도록 기본값을 내장한다. 여러 정책이 맞으면 이름 사전순으로
//
//	뒤에 오는 값이 이긴다(internal/verification 의 Resolve 와 같은 규율).
//
// 생성일: 2026-08-04
// ============================================================
package health

import (
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// EffectivePolicy 는 한 노드에 실제로 적용되는 임계와 조치다.
type EffectivePolicy struct {
	NDRFreshness             time.Duration
	DriverFailureThreshold   int32
	AdvertisementGrace       time.Duration
	RepeatedFailureThreshold int32
	RecoveryCooldown         time.Duration
	MonitorInterval          time.Duration
	disabled                 map[string]bool
	remediation              map[string]v1alpha1.RemediationRule
}

// Remediation 은 한 원인에 대해 무엇을 할지다.
type Remediation struct {
	StopAllocation  bool
	CreateOperation bool
	Quarantine      bool
}

// DefaultPolicy 는 CRD 가 없을 때의 값이다. 숫자의 근거:
// NDR 은 30초 주기로 갱신되므로 3주기(90초)를 못 받으면 관측이 끊긴 것으로 본다.
// 광고 유예 30초는 Stage 1 drift 감시와 같은 값이라 두 축이 서로 다른 순간에 흔들리지 않는다.
func DefaultPolicy() EffectivePolicy {
	return EffectivePolicy{
		NDRFreshness:             90 * time.Second,
		DriverFailureThreshold:   3,
		AdvertisementGrace:       30 * time.Second,
		RepeatedFailureThreshold: 3,
		RecoveryCooldown:         10 * time.Minute,
		MonitorInterval:          30 * time.Second,
		disabled:                 map[string]bool{},
		remediation:              map[string]v1alpha1.RemediationRule{},
	}
}

// Resolve 는 노드 라벨에 맞는 정책만 골라 기본값 위에 덮는다.
func Resolve(list []v1alpha1.AcceleratorHealthPolicy, nodeLabels map[string]string) EffectivePolicy {
	eff := DefaultPolicy()
	sorted := make([]v1alpha1.AcceleratorHealthPolicy, len(list))
	copy(sorted, list)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, p := range sorted {
		if !matches(p.Spec.Selector, nodeLabels) {
			continue
		}
		apply(&eff, p.Spec)
	}
	return eff
}

func matches(sel *metav1.LabelSelector, nodeLabels map[string]string) bool {
	if sel == nil {
		return true // selector 부재 = 전 노드
	}
	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		// 잘못된 selector 는 아무 노드에도 적용하지 않는다. 조용히 전 노드에 거는 방향으로
		// 틀리면 오타 하나가 클러스터 전체의 임계를 바꾼다.
		return false
	}
	return s.Matches(labels.Set(nodeLabels))
}

func apply(eff *EffectivePolicy, spec v1alpha1.AcceleratorHealthPolicySpec) {
	if v := spec.Checks.NDRFreshnessSeconds; v > 0 {
		eff.NDRFreshness = time.Duration(v) * time.Second
	}
	if v := spec.Checks.DriverHeartbeatFailureThreshold; v > 0 {
		eff.DriverFailureThreshold = v
	}
	if v := spec.Checks.AdvertisementGraceSeconds; v > 0 {
		eff.AdvertisementGrace = time.Duration(v) * time.Second
	}
	if v := spec.Checks.RepeatedFailureThreshold; v > 0 {
		eff.RepeatedFailureThreshold = v
	}
	if v := spec.Checks.MonitorIntervalSeconds; v > 0 {
		eff.MonitorInterval = time.Duration(v) * time.Second
	}
	for _, d := range spec.Checks.Disabled {
		eff.disabled[d] = true
	}
	for k, v := range spec.Remediation {
		eff.remediation[k] = v
		if v.RecoveryCooldownSeconds > 0 {
			eff.RecoveryCooldown = time.Duration(v.RecoveryCooldownSeconds) * time.Second
		}
	}
}

// Enabled 는 이 신호를 볼지다.
func (p EffectivePolicy) Enabled(signal string) bool { return !p.disabled[signal] }

// RemediationFor 는 원인별 조치다. 정책이 그 원인을 안 적었으면 원인의 기본 조치를 쓴다.
func (p EffectivePolicy) RemediationFor(reason string) Remediation {
	if rule, ok := p.remediation[reason]; ok {
		return remediationFromAction(rule.Action)
	}
	return defaultRemediation(reason)
}

func remediationFromAction(a v1alpha1.RemediationAction) Remediation {
	switch a {
	case v1alpha1.RemediationStopNewAllocation:
		return Remediation{StopAllocation: true}
	case v1alpha1.RemediationRequestRecovery:
		return Remediation{StopAllocation: true, CreateOperation: true}
	case v1alpha1.RemediationQuarantine:
		return Remediation{StopAllocation: true, Quarantine: true}
	default:
		return Remediation{}
	}
}

// defaultRemediation 은 원인별 기본 조치다(표).
//
// 관측이 없거나 노드가 죽은 경우는 배치만 막고 하드웨어를 건드리지 않는다 — 계약 이
// "stale NDR 만으로 파괴적 복구를 즉시 실행하지 않는다" 를 요구한다.
func defaultRemediation(reason string) Remediation {
	switch reason {
	case ReasonDevicePluginDown, ReasonAdvertisementMismatch:
		return Remediation{StopAllocation: true, CreateOperation: true}
	case ReasonDriverNotLoaded:
		// 배치를 막고 **복구를 한 번 요청한다**(RecoverDevice = 드라이버를 올리는 주체 재시작).
		// 여기서 곧바로 격리하지 않는 이유: 격리는 되돌리는 데 사람이 필요하고, 흔한 원인(설치
		// Job·DS pod 이 죽어 있음)은 재시작으로 낫는다. 복구가 반복 실패하면 그때 ReasonRepeatedRecoveryFail
		// 이 격리로 올린다 — 격리는 자동 복구가 통하지 않는다는 것이 드러난 뒤의 결론이어야 한다.
		return Remediation{StopAllocation: true, CreateOperation: true}
	case ReasonRepeatedRecoveryFail:
		return Remediation{StopAllocation: true, Quarantine: true}
	case ReasonNodeNotReady, ReasonObservationStale:
		return Remediation{StopAllocation: true}
	default:
		return Remediation{}
	}
}
