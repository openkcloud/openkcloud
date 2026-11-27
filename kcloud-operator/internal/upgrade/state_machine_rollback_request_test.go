// ============================================================
// state_machine_rollback_request_test.go: 관리자 롤백 요청 어노테이션의 수용 시험
// 상세: Idle 에서 어노테이션을 보면 Rollback 으로 전이하며 어노테이션을 지우고, 전이 중 상태는
// 요청을 보류한다.
// 생성일: 2026-09-30 | 수정일: 2026-10-11
// ============================================================
package upgrade

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"kcloud-operator/api/v1alpha1"
)

// rollbackRequestTestVendor 는 이 파일의 시험들이 공유하는 벤더 리터럴이다(goconst).
const rollbackRequestTestVendor = "nvidia"

func TestTransitionStateHonorsRollbackRequestInIdle(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	state := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "n1-nvidia",
			Annotations: map[string]string{v1alpha1.RollbackRequestedAnnotation: "2026-09-30T00:00:00Z"}},
		Spec:   v1alpha1.DriverUpgradeStateSpec{NodeName: "n1", Vendor: rollbackRequestTestVendor, Model: "a30"},
		Status: v1alpha1.DriverUpgradeStateStatus{State: v1alpha1.UpgradeStateIdle, PreviousVersion: "575.64.03"},
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(state).WithStatusSubresource(state).Build()
	m := &UpgradeStateMachine{Client: fc, Recorder: record.NewFakeRecorder(4)}
	policy := &v1alpha1.DriverInstallPolicy{}
	policy.Spec.Vendor = rollbackRequestTestVendor
	policy.Spec.Driver.Version = verOld
	if _, _, err := m.TransitionState(context.Background(), state, policy); err != nil {
		t.Fatalf("TransitionState: %v", err)
	}
	if state.Status.State != v1alpha1.UpgradeStateRollback {
		t.Fatalf("state = %s, want Rollback", state.Status.State)
	}
	var got v1alpha1.DriverUpgradeState
	if err := fc.Get(context.Background(), types.NamespacedName{Name: "n1-nvidia"}, &got); err != nil {
		t.Fatal(err)
	}
	if _, still := got.Annotations[v1alpha1.RollbackRequestedAnnotation]; still {
		t.Error("요청 어노테이션이 지워지지 않았다")
	}
}

// TestTransitionStateRollbackRequestRetriesAfterStatusPersistFailure 는 상태 전이(Rollback)
// 영속이 실패하면 어노테이션이 지워지지 않고 남아, 다음 reconcile(새 Get 으로 시작)이 같은
// 요청을 다시 전이 시도하는지 본다 — 어노테이션을 먼저 지우고 상태 영속을 나중에 하면, 영속이
// 실패했을 때 요청 신호가 사라진 채 상태만 Idle 로 멈춰 재시도할 방법이 없어진다.
func TestTransitionStateRollbackRequestRetriesAfterStatusPersistFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	seed := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "n1-nvidia",
			Annotations: map[string]string{v1alpha1.RollbackRequestedAnnotation: "2026-09-30T00:00:00Z"}},
		Spec:   v1alpha1.DriverUpgradeStateSpec{NodeName: "n1", Vendor: rollbackRequestTestVendor, Model: "a30"},
		Status: v1alpha1.DriverUpgradeStateStatus{State: v1alpha1.UpgradeStateIdle, PreviousVersion: "575.64.03"},
	}
	var statusUpdateCalls int
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(seed).WithStatusSubresource(seed).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string,
				obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					statusUpdateCalls++
					if statusUpdateCalls == 1 {
						return errors.New("simulated status persist failure")
					}
				}
				return cl.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).Build()
	m := &UpgradeStateMachine{Client: fc, Recorder: record.NewFakeRecorder(4)}
	policy := &v1alpha1.DriverInstallPolicy{}
	policy.Spec.Vendor = rollbackRequestTestVendor
	policy.Spec.Driver.Version = verOld
	ctx := context.Background()

	// 1차 reconcile: 상태 영속(Status().Update)이 실패한다.
	var first v1alpha1.DriverUpgradeState
	if err := fc.Get(ctx, types.NamespacedName{Name: "n1-nvidia"}, &first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.TransitionState(ctx, &first, policy); err == nil {
		t.Fatal("상태 영속 실패가 TransitionState 에러로 전파되지 않았다")
	}

	var afterFail v1alpha1.DriverUpgradeState
	if err := fc.Get(ctx, types.NamespacedName{Name: "n1-nvidia"}, &afterFail); err != nil {
		t.Fatal(err)
	}
	if afterFail.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("영속 실패했는데 저장된 state 가 바뀜: got=%s", afterFail.Status.State)
	}
	if _, has := afterFail.Annotations[v1alpha1.RollbackRequestedAnnotation]; !has {
		t.Fatal("상태 영속 실패했는데 요청 어노테이션이 지워졌다 — 재시도 트리거 소실")
	}

	// 2차 reconcile: 새로 Get 한 객체로 다시 시도 — 이번엔 통과한다.
	var second v1alpha1.DriverUpgradeState
	if err := fc.Get(ctx, types.NamespacedName{Name: "n1-nvidia"}, &second); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.TransitionState(ctx, &second, policy); err != nil {
		t.Fatalf("재시도 TransitionState: %v", err)
	}

	var final v1alpha1.DriverUpgradeState
	if err := fc.Get(ctx, types.NamespacedName{Name: "n1-nvidia"}, &final); err != nil {
		t.Fatal(err)
	}
	if final.Status.State != v1alpha1.UpgradeStateRollback {
		t.Fatalf("재시도 후 state = %s, want Rollback", final.Status.State)
	}
	if _, still := final.Annotations[v1alpha1.RollbackRequestedAnnotation]; still {
		t.Error("재시도가 성공했는데 요청 어노테이션이 남아있다")
	}
}

// TestTransitionStateDefersRollbackRequestDuringTransition 은 전이 중(Idle·Failed 가 아닌)
// 상태에서는 롤백 요청 어노테이션이 있어도 즉시 Rollback 으로 전이하지 않고, 어노테이션도
// 지우지 않은 채 원래 진행 중이던 전이(switch 분기)를 그대로 흘려보내는지 본다. CLI 가 먼저
// 거절하지만, kubectl 로 어노테이션을 직접 붙이는 경로에서는 이 분기가 유일한 방어선이다.
func TestTransitionStateDefersRollbackRequestDuringTransition(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	state := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "n1-nvidia",
			Annotations: map[string]string{v1alpha1.RollbackRequestedAnnotation: "2026-09-30T00:00:00Z"}},
		Spec:   v1alpha1.DriverUpgradeStateSpec{NodeName: "n1", Vendor: rollbackRequestTestVendor, Model: "a30"},
		Status: v1alpha1.DriverUpgradeStateStatus{State: v1alpha1.UpgradeStateDraining},
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(state).WithStatusSubresource(state).Build()
	m := &UpgradeStateMachine{Client: fc, Recorder: record.NewFakeRecorder(4)}
	// UpgradePolicy 가 nil 이면 handleDraining 이 drain 없이 바로 Upgrading 으로 넘어간다 —
	// 이 시험은 어노테이션 분기만 보면 되므로 나머지 경로는 최소 구성으로 둔다.
	policy := &v1alpha1.DriverInstallPolicy{}
	policy.Spec.Vendor = rollbackRequestTestVendor

	if _, _, err := m.TransitionState(context.Background(), state, policy); err != nil {
		t.Fatalf("TransitionState: %v", err)
	}
	if state.Status.State == v1alpha1.UpgradeStateRollback {
		t.Fatal("전이 중인데 롤백 요청이 즉시 Rollback 으로 전이시켰다")
	}
	if state.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Fatalf("어노테이션 분기가 기존 전이(Draining→Upgrading)를 막았다: got=%s", state.Status.State)
	}

	var got v1alpha1.DriverUpgradeState
	if err := fc.Get(context.Background(), types.NamespacedName{Name: "n1-nvidia"}, &got); err != nil {
		t.Fatal(err)
	}
	if _, has := got.Annotations[v1alpha1.RollbackRequestedAnnotation]; !has {
		t.Error("전이 중인데 요청 어노테이션이 지워졌다")
	}
}

func TestRollbackRequestAcceptedStates(t *testing.T) {
	for state, want := range map[string]bool{
		"": true, v1alpha1.UpgradeStateIdle: true, v1alpha1.UpgradeStateFailed: true,
		v1alpha1.UpgradeStateUpgrading: false, v1alpha1.UpgradeStateDraining: false, v1alpha1.UpgradeStateRollback: false,
	} {
		if got := v1alpha1.RollbackRequestAccepted(state); got != want {
			t.Errorf("RollbackRequestAccepted(%q) = %v, want %v", state, got, want)
		}
	}
}
