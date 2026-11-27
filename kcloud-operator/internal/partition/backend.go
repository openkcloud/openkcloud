// ============================================================
// backend.go: 벤더 중립 파티션 backend 계약
// 상세: Discover/Validate/Diff/Apply/Verify/Rollback. 미지원 op 는 ErrUnsupported 반환.
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package partition

import (
	"context"
	"errors"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// ErrUnsupported 는 backend 가 지원하지 않는 op 를 나타낸다(NVIDIA apply/rollback 등).
// reconciler 는 이를 operations.<op>.supported=false 로 정직 보고한다.
var ErrUnsupported = errors.New("partition: operation unsupported by backend")

// Layout 은 요청 레이아웃 한 항목이다(v1alpha1.PartitionLayout 미러).
type Layout struct {
	Profile        string
	CountPerDevice int32
}

// ResolvedEntry 는 profile → backendPolicy resolve 결과다.
type ResolvedEntry struct {
	Profile                string
	BackendPolicy          string
	ExpectedCountPerDevice int32
}

// Target 은 nodeSelector 가 매칭한 한 대상의 컨텍스트다.
type Target struct {
	Ctx      context.Context
	NodeName string
	// DaemonSet 식별(RNGD apply/충돌 판정 기준).
	DaemonSetName      string
	DaemonSetNamespace string
	// Owner 는 이 target 을 요청한 ACPP 의 .metadata.name — owner-lock annotation 값(두 writer 조정).
	Owner string
	// Generation 은 요청 ACPP 의 .metadata.generation — Job operation-ID 재료(nvidia MIG).
	Generation int64
	// Vendor 는 할당 probe 가 어느 벤더 명령으로 장치를 확인할지 고르는 키다(비면 pause probe).
	Vendor string
}

// DiscoverResult 는 discovery 산출 — status 4축을 채운다.
type DiscoverResult struct {
	Backend       v1alpha1.BackendRef
	DriverVersion string
	Devices       []v1alpha1.DeviceStatus
	Resolved      []v1alpha1.ResolvedLayoutEntry
	Observed      []v1alpha1.ObservedLayoutEntry
	Operations    v1alpha1.OperationsStatus
	Advertisement v1alpha1.AdvertisementStatus
}

// DiffResult 는 현재 vs 목표 backend policy 차이다.
type DiffResult struct {
	Changed    bool
	FromPolicy string
	ToPolicy   string
}

// RollbackState 는 apply 전 저장하는 복원 기준.
type RollbackState struct {
	DaemonSetUID    string
	PrevPolicy      string
	Generation      int64
	ResourceVersion string
	TemplateHash    string
}

// VerifyResult 는 allocatable 수렴 + 테스트 Pod 할당 검증 결과다.
type VerifyResult struct {
	AllocatableConverged bool
	TestPodAllocated     bool
	Snapshot             map[string]int32
	// ProbeExitCode 는 벤더 기능 probe 의 종료코드다. pause probe(스케줄만 확인)는 nil 로 남는다.
	ProbeExitCode *int32
	// ProbeOutput 은 probe 파드의 마지막 로그다(판정 근거로 evidence 에 남긴다).
	ProbeOutput string
}

// Verifier 는 하드웨어 의존 검증 seam 이다(envtest 는 fake 주입).
type Verifier interface {
	VerifyAllocatable(t Target, expected map[string]int32) (*VerifyResult, error)
	VerifyAllocation(t Target, resourceName string) (*VerifyResult, error)
}

// Backend 는 벤더별 파티션 backend 계약이다.
type Backend interface {
	Vendor() string
	Discover(t Target) (*DiscoverResult, error)
	Validate(layout []Layout) error
	Diff(t Target, resolved []ResolvedEntry) (DiffResult, error)
	Apply(t Target, resolved []ResolvedEntry) (*RollbackState, error)
	Verify(t Target) (*VerifyResult, error)
	Rollback(t Target, s RollbackState) error
}
