// ============================================================
// sharing.go: 벤더 중립 sharing 계약
// 상세: SharingBackend 는 옵셔널 인터페이스다 — 미구현 backend 는 타입 assertion 실패로
// ErrSharingUnsupported 를 정직 보고한다(Backend 인터페이스 자체는 그대로 둔다).
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package partition

import (
	"errors"
	"fmt"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// MaxSharingReplicas 는 정책 상한이다(하드웨어 상한 아님).
const MaxSharingReplicas = 16

var ErrSharingUnsupported = errors.New("partition: sharing unsupported by backend")

// SharingLayout 은 해석된 공유 요청이다.
type SharingLayout struct {
	Mode                       string
	Replicas                   int32
	FailRequestsGreaterThanOne bool
}

// SharingRollbackState 는 공유 적용 전 상태 스냅샷이다.
// PrevDSHash 는 두지 않는다 — 어떤 consumer 도 사용하지 않는 필드다.
type SharingRollbackState struct {
	ConfigMapExisted bool
	PrevConfigYAML   string
}

// SharingBackend 는 공유를 지원하는 backend 만 구현하는 옵셔널 계약이다.
type SharingBackend interface {
	ValidateSharing(l SharingLayout) error
	ApplySharing(t Target, l SharingLayout, base map[string]int32) (*SharingRollbackState, error)
	// ApplySharingConfig 는 ApplySharing 의 config(ConfigMap) 단계만 수행한다 — device-plugin
	// 배선은 하지 않는다. MPS 는 control daemon 이 이 config 를 마운트해 기동하므로, 호출자가
	// daemon 을 띄우기 전에 config 만 먼저 놓을 수 있어야 한다.
	ApplySharingConfig(t Target, l SharingLayout, base map[string]int32) (*SharingRollbackState, error)
	RollbackSharing(t Target, s SharingRollbackState) error
	ExpectedSharedAllocatable(base map[string]int32, l SharingLayout) map[string]int32
}

// SharingLayoutFrom 은 CR spec 을 SharingLayout 으로 옮긴다.
func SharingLayoutFrom(spec v1alpha1.AcceleratorPartitionPolicySpec) SharingLayout {
	l := SharingLayout{Mode: spec.EffectiveSharingMode()}
	if spec.Sharing == nil {
		return l
	}
	// declared mode 로 게이트한다 — CRD/webhook 어느 쪽도 TimeSlicing/MPS 상호배타를 막지
	// 않으므로, 독립된 if 였다면 정리 안 된 잔여 필드가 조용히 다른 모드의 값을 덮어쓴다.
	switch l.Mode {
	case v1alpha1.SharingModeTimeSliced:
		if spec.Sharing.TimeSlicing != nil {
			l.Replicas = spec.Sharing.TimeSlicing.Replicas
			l.FailRequestsGreaterThanOne = spec.Sharing.TimeSlicing.FailRequestsGreaterThanOne
		}
	case v1alpha1.SharingModeMPS:
		if spec.Sharing.MPS != nil {
			l.Replicas = spec.Sharing.MPS.Replicas
		}
	}
	return l
}

// ValidateSharingLayout 은 벤더 무관 요청 규칙이다(벤더별 지원 여부는 backend 가 별도 판정).
func ValidateSharingLayout(l SharingLayout) error {
	switch l.Mode {
	case v1alpha1.SharingModeExclusive, "":
		return nil
	case v1alpha1.SharingModeTimeSliced, v1alpha1.SharingModeMPS:
		if l.Replicas < 2 {
			return fmt.Errorf("sharing: %s requires replicas >= 2, got %d", l.Mode, l.Replicas)
		}
		if l.Replicas > MaxSharingReplicas {
			return fmt.Errorf("sharing: replicas %d exceeds cap %d", l.Replicas, MaxSharingReplicas)
		}
		return nil
	default:
		return fmt.Errorf("sharing: unknown mode %q", l.Mode)
	}
}

// fmtSharingUnsupported 는 벤더명을 담은 미지원 에러다.
func fmtSharingUnsupported(vendor string) error {
	return fmt.Errorf("%w: vendor %s", ErrSharingUnsupported, vendor)
}

// SharingFor 는 backend 가 공유를 지원하면 SharingBackend 로, 아니면 미지원 에러를 준다.
func SharingFor(b Backend) (SharingBackend, error) {
	sb, ok := b.(SharingBackend)
	if !ok {
		return nil, fmtSharingUnsupported(b.Vendor())
	}
	return sb, nil
}
