// ============================================================
// mapping.go: RNGD profile↔backendPolicy 매핑 테이블 + validation
// 상세: Furiosa 공식 profile 표기를 /main --policy 인자로 resolve. 숨은 등가 금지 — 명시 테이블.
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package rngd

import (
	"errors"
	"fmt"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

var (
	ErrProfileUnknown      = errors.New("rngd: profile unknown")
	ErrProfileNotSupported = errors.New("rngd: profile not supported (LegacyDocumented)")
	ErrCountMismatch       = errors.New("rngd: countPerDevice mismatch for fixed profile")
)

// RngdProfile 은 사용자-facing profile 과 /main backend policy 의 1:1 매핑이다.
type RngdProfile struct {
	Profile           string
	BackendPolicy     string
	CountPerDevice    int32
	CoresPerPartition int32
	SupportLevel      string
}

// rngdProfileTable 은 의 명시적 매핑이다. 코드에 숨은 등가 가정 금지.
// none(whole)/single-core 는 LegacyDocumented — validation 에서 거부(공식 표기 확인 전).
var rngdProfileTable = []RngdProfile{
	{Profile: "1core.6gb", BackendPolicy: "single-core", CountPerDevice: 8, CoresPerPartition: 1, SupportLevel: v1alpha1.SupportLegacyDocumented},
	{Profile: "2core.12gb", BackendPolicy: "dual-core", CountPerDevice: 4, CoresPerPartition: 2, SupportLevel: v1alpha1.SupportVerified},
	{Profile: "4core.24gb", BackendPolicy: "quad-core", CountPerDevice: 2, CoresPerPartition: 4, SupportLevel: v1alpha1.SupportVerified}, // <rngd-node> 실측 확인(allocatable 4→2→4)
}

// ResolveProfile 는 사용자 profile → RngdProfile.
func ResolveProfile(profile string) (RngdProfile, bool) {
	for _, p := range rngdProfileTable {
		if p.Profile == profile {
			return p, true
		}
	}
	return RngdProfile{}, false
}

// ResolvePolicy 는 backend policy → RngdProfile (역방향, discovery 용).
func ResolvePolicy(policy string) (RngdProfile, bool) {
	for _, p := range rngdProfileTable {
		if p.BackendPolicy == policy {
			return p, true
		}
	}
	return RngdProfile{}, false
}

// ValidateLayout 는 요청 profile·count 를 검증한다(RNGD).
// Verified/Documented 통과, LegacyDocumented 거부, count 지정 시 fixed count 와 일치해야 함.
// countPerDevice == 0 은 "미지정"(CR 의 omitempty zero value)으로 간주해 fixed-count 일치
// 검사를 건너뛴다. 이 함수는 요청(desired) count 검증 전용이다 — 실제 관측된 observed
// count(0 이 정상 상태일 수 있음)를 넘기면 0 을 미지정으로 오인해 불일치를 놓친다. 관측
// 검증은 reconciler 가 별도 수행한다.
func ValidateLayout(profile string, countPerDevice int32) error {
	p, ok := ResolveProfile(profile)
	if !ok {
		return fmt.Errorf("%w: %q", ErrProfileUnknown, profile)
	}
	if p.SupportLevel == v1alpha1.SupportLegacyDocumented {
		return fmt.Errorf("%w: %q", ErrProfileNotSupported, profile)
	}
	if countPerDevice != 0 && countPerDevice != p.CountPerDevice {
		return fmt.Errorf("%w: %q expects %d, got %d", ErrCountMismatch, profile, p.CountPerDevice, countPerDevice)
	}
	return nil
}
