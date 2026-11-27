// ============================================================
// participant_test.go: participant 레지스트리 테스트
// 상세: 등록되지 않은 작업 종류가 조용히 통과하지 않는다는 것만 고정한다(계약 자체는 얇다).
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"context"
	"testing"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/verification"
)

type stubParticipant struct{}

func (stubParticipant) Apply(context.Context, *v1alpha1.AcceleratorOperation) (Outcome, error) {
	return Outcome{Event: EventApplyDone}, nil
}
func (stubParticipant) Rollback(context.Context, *v1alpha1.AcceleratorOperation) (Outcome, error) {
	return Outcome{Event: EventCompensated}, nil
}
func (stubParticipant) VerifyRequest(context.Context, *v1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	return verification.Request{}, false, nil
}

// 증명: 등록된 종류만 찾아진다. 미등록 종류를 실행하면 아무도 mutation 을 책임지지 않은 채
// 상태만 전진하므로, 레지스트리 조회 실패는 반드시 드러나야 한다.
// 깨는 뮤테이션: For 가 항상 (nil, true) 를 돌려주게 바꾸면 실패한다.
func TestRegistryOnlyResolvesRegisteredTypes(t *testing.T) {
	reg := Registry{PartitionReconfigure: stubParticipant{}}
	if p, ok := reg.For(PartitionReconfigure); !ok || p == nil {
		t.Fatalf("등록된 종류를 못 찾았다")
	}
	if _, ok := reg.For(DriverUpgrade); ok {
		t.Fatalf("등록하지 않은 종류가 조회됐다")
	}
}

// 증명: Outcome 의 빈 Event 가 "아직 진행 중" 을 뜻한다(전이 없음).
// 깨는 뮤테이션: 빈 Event 를 성공으로 해석하는 코드가 생기면 상태머신 테스트가 잡는다.
func TestEmptyOutcomeEventMeansInProgress(t *testing.T) {
	var o Outcome
	if o.Event != "" {
		t.Fatalf("기본 Outcome 이 사건을 갖고 있다: %q", o.Event)
	}
	if _, ok := Next(v1alpha1.OpPhaseApplying, o.Event); ok {
		t.Fatalf("빈 사건으로 전이가 열렸다")
	}
}
