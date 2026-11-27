// ============================================================
// state_machine_claim_gate_test.go: drain 게이트 claim 축의 제어 흐름 계약 고정 시험
// 상세: claim 축에서 "아직 할당돼 있다"(정상 재조정)와 "읽지 못했다"(오류)를 가르고, 막은
//       주체가 Event 로 보이는지 고정한다. fake client 기반 — envtest 불필요.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================

package upgrade

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/draclaim"
)

// allocatedClaimOnDrainGateNode 는 drainGateNode 에 할당된 채 남은 claim 이다. 대응하는 Pod 는
// 만들지 않는다 — Pod 축 0건 + claim 축 1건이 고치려는 상태 그대로다.
func allocatedClaimOnDrainGateNode() *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: "default"},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{
			NodeSelector: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchFields: []corev1.NodeSelectorRequirement{{
					Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{drainGateNode},
				}},
			}}},
		}},
	}
}

// ── claim 축이 막을 때 Warning Event 로 이유가 밖에 보인다 ──────────────
// 막은 주체를 Event 가 이름으로 담지 않으면 운영자는 왜 멈췄는지 알 수 없고, 이 시험 자체도
// "아무 이유로든 멈췄다" 로 통과하는 시험이 된다.
func TestHandleDrainingAllocatedClaimEmitsWarningEvent(t *testing.T) {
	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: drainGateNode}}
	claim := allocatedClaimOnDrainGateNode()

	sm := newUpgradeSMWithRecorder(dus, dip, node, claim)
	if _, _, err := sm.handleDraining(context.Background(), dus, dip); err != nil {
		t.Fatalf("할당된 claim 이 남은 것은 오류가 아니라 정상 재조정이어야 한다: %v", err)
	}

	rec, ok := sm.Recorder.(*record.FakeRecorder)
	if !ok {
		t.Fatalf("FakeRecorder 가 아니다: %T", sm.Recorder)
	}
	var got []string
	for len(rec.Events) > 0 {
		got = append(got, <-rec.Events)
	}
	found := ""
	for _, e := range got {
		if strings.Contains(e, "DrainBlockedByClaim") {
			found = e
		}
	}
	if found == "" {
		t.Fatalf("claim 축이 막았는데 Event 가 없다: events=%v", got)
	}
	if !strings.HasPrefix(found, corev1.EventTypeWarning+" ") {
		t.Errorf("Event 가 Warning 이 아니다: %s", found)
	}
	if !strings.Contains(found, drainGateNode) || !strings.Contains(found, claim.Name) {
		t.Errorf("Event 가 노드 이름과 claim 이름을 담지 않았다: %s", found)
	}
}

// ── claim 을 읽지 못한 것은 여전히 오류다 ───────────────────────────────
// "할당 남음" 을 재조정으로 바꾼 변경이 "읽기 실패" 까지 삼키면 게이트는 모르는 상태로 열린다.
func TestHandleDrainingClaimReadFailureStaysError(t *testing.T) {
	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	readErr := errors.New("apiserver 응답 없음")
	sm := &UpgradeStateMachine{
		Client: fake.NewClientBuilder().WithScheme(newUpgradeTestScheme()).
			WithObjects(dus, dip).
			WithStatusSubresource(&v1alpha1.DriverUpgradeState{}).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList,
					opts ...client.ListOption) error {
					if _, isClaims := list.(*resourcev1.ResourceClaimList); isClaims {
						return readErr
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Recorder: record.NewFakeRecorder(64),
	}

	_, _, err := sm.handleDraining(context.Background(), dus, dip)
	if err == nil {
		t.Fatal("claim 축을 읽지 못했는데 drain 이 통과했다")
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("올라온 오류가 읽기 실패가 아니다: %v", err)
	}
	if errors.Is(err, draclaim.ErrClaimStillAllocated) {
		t.Fatalf("읽기 실패가 '할당 남음' 센티넬과 섞였다: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateDraining {
		t.Fatalf("claim 축을 읽지 못했는데 Draining 을 벗어났다: got=%s", dus.Status.State)
	}
}
