// ============================================================
// participant.go: 작업 본체 계약
// 상세: Coordinator 는 허가·저널·펜싱만 책임지고, 실제 mutation 은 participant 가 한다. 구현은
// internal/controller 에 산다 — 이 패키지는 controller/partition 을 import 하지 않기 때문이다(verification 은 예외).
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"context"
	"time"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/verification"
)

// Outcome 은 participant 호출 한 번의 결과다.
// Event 가 빈 문자열이면 아직 진행 중이라는 뜻이고, 상태는 그대로 두고 재큐잉한다.
type Outcome struct {
	Event        Event
	Message      string
	RequeueAfter time.Duration
}

// Participant 는 한 종류의 작업 본체다. 세 메서드 모두 **멱등**이어야 한다 — Coordinator 는
// 같은 phase 에서 여러 번 부를 수 있고(재큐잉·재시작), 그때마다 하드웨어를 다시 건드리면 안 된다.
type Participant interface {
	// Apply 는 작업을 한 번 전진시킨다.
	Apply(ctx context.Context, op *v1alpha1.AcceleratorOperation) (Outcome, error)
	// Rollback 은 스냅샷 상태로 되돌린다.
	Rollback(ctx context.Context, op *v1alpha1.AcceleratorOperation) (Outcome, error)
	// VerifyRequest 는 검증 단계가 쓸 요청을 만든다. 두 번째 값이 false 면 검증 대상이 아니다.
	VerifyRequest(ctx context.Context, op *v1alpha1.AcceleratorOperation) (verification.Request, bool, error)
}

// Registry 는 작업 종류별 participant 다.
type Registry map[Type]Participant

// For 는 종류에 맞는 participant 를 찾는다. 없으면 ok=false — 호출자는 이 경우를 반드시
// 오류로 다뤄야 한다. 미등록 종류를 조용히 통과시키면 아무도 mutation 을 책임지지 않은 채
// 상태만 Succeeded 로 전진한다.
func (r Registry) For(t Type) (Participant, bool) {
	p, ok := r[t]
	return p, ok
}
