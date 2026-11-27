// ============================================================
// operation_driver_participant_test.go: 드라이버 작업 본체 envtest
// 상세: 드라이버 상태머신의 상태를 조정자 사건으로 옮기는 매핑과, 상태머신에 실제로 닿는지를 본다.
// 생성일: 2026-08-01 | 수정일: 2026-10-12
// ============================================================
package controller

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
)

// driverParticipantFixture 는 아래 두 plain-Go 테스트 전용 DIP+DUS 픽스처다. 이 파일의
// 다른 곳에서 쓰는 makeDIP/makeDUS(driver_upgrade_controller_test.go)를 그대로 쓰지 않는
// 이유는, 그 헬퍼들의 vendor/model 인자가 파일 전체에서 사실상 상수 값으로만 불려
// unparam 정적 분석이 오탐하기 때문이다(둘 중 하나를 바꿔도 다른 인자로 옮겨갈 뿐이다).
func driverParticipantFixture(dusName, state, current, desired string) (*npuv1alpha1.DriverInstallPolicy, *npuv1alpha1.DriverUpgradeState) {
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "dip-1"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa", Model: "warboy",
			Driver: npuv1alpha1.DriverSpec{Version: "2.0.0", Mode: "daemonset"},
		},
	}
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: dusName},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: "worker1", Vendor: "furiosa", Model: "warboy"},
		Status:     npuv1alpha1.DriverUpgradeStateStatus{State: state, CurrentVersion: current, DesiredVersion: desired},
	}
	return dip, dus
}

// 증명: Apply 한 번이 상태머신의 전이를 **클러스터에** 남긴다. TransitionState 는 실 Reconcile 과
// 마찬가지로 state 를 메모리에서만 전이시킨다 — 영속은 호출자 책임이다. 이 호출이 빠지면 Apply
// 를 몇 번을 불러도 재조회한 DriverUpgradeState.Status.State 가 절대 안 바뀐다 — cordon·drain·
// 설치 같은 부수효과만 매 pass 반복되고 진행은 기록되지 않는다. driverOutcomeFor 매핑 시험(위)은
// 순수 함수만 보므로 이 결함을 못 본다 — 이 시험이 advance()/TransitionState 연결을 실제로 구동한다.
// 깨는 뮤테이션: advance() 에서 p.r.Status().Update 호출을 빼면 실패한다.
func TestApplyPersistsStateMachineProgress(t *testing.T) {
	dip, dus := driverParticipantFixture("dus-1", npuv1alpha1.UpgradeStateRequired, "1.9.8", "2.0.0")
	r := newReconciler(dip, dus)
	p := NewDriverParticipant(r)
	op := &npuv1alpha1.AcceleratorOperation{
		Spec: npuv1alpha1.AcceleratorOperationSpec{Owner: npuv1alpha1.OperationOwner{Name: "dus-1"}},
	}

	if _, err := p.Apply(context.Background(), op); err != nil {
		t.Fatalf("Apply err: %v", err)
	}

	var got npuv1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: "dus-1"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.State != npuv1alpha1.UpgradeStatePreFlight {
		t.Fatalf("상태머신 전이가 영속되지 않았다: got.Status.State = %q, want %q",
			got.Status.State, npuv1alpha1.UpgradeStatePreFlight)
	}
}

// 증명: owner UID 가 실제 DriverUpgradeState 와 다르면(같은 이름으로 재생성된 다른 객체)
// 실행을 거부한다 — 옛 operation 이 새 사이클을 건드리면 안 된다.
// 깨는 뮤테이션: UID 비교를 빼면 실패한다.
func TestApplyRejectsOwnerUIDMismatch(t *testing.T) {
	dip, dus := driverParticipantFixture("dus-1", npuv1alpha1.UpgradeStateRequired, "1.9.8", "2.0.0")
	dus.UID = "current-uid"
	r := newReconciler(dip, dus)
	p := NewDriverParticipant(r)
	op := &npuv1alpha1.AcceleratorOperation{
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Owner: npuv1alpha1.OperationOwner{Name: "dus-1", UID: "stale-uid"},
		},
	}

	out, err := p.Apply(context.Background(), op)
	if err != nil {
		t.Fatalf("Apply 는 이 실패를 Outcome 으로 돌려줘야 한다(err=nil), got err=%v", err)
	}
	if out.Event != operation.EventApplyFailed {
		t.Fatalf("Event = %q, want %q — UID 불일치를 거르지 못했다", out.Event, operation.EventApplyFailed)
	}

	var got npuv1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: "dus-1"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.State != npuv1alpha1.UpgradeStateRequired {
		t.Fatalf("거부됐어야 할 호출이 상태머신을 실제로 전진시켰다: got.Status.State = %q", got.Status.State)
	}
}

var _ = Describe("driver participant outcome mapping", func() {
	// 증명: 사이클이 시작되기 전의 Idle 을 완료로 오인하지 않는다.
	//       (버전이 다른데 Idle 이면 아직 UpgradeRequired 로도 못 간 상태다.)
	// 깨는 뮤테이션: Idle 을 무조건 EventApplyDone 으로 매핑하면 실패한다.
	It("does not treat a not-yet-started Idle as completion", func() {
		out := driverOutcomeFor(&npuv1alpha1.DriverUpgradeStateStatus{
			State: npuv1alpha1.UpgradeStateIdle, CurrentVersion: "535.104.05", DesiredVersion: "570.12.01",
		})
		Expect(out.Event).To(BeEmpty(), "시작도 안 한 사이클을 완료로 보고했다")
	})

	// 증명: 버전이 수렴한 Idle 만 완료다.
	// 깨는 뮤테이션: 버전 비교를 빼면 위 스펙과 이 스펙 중 하나는 반드시 깨진다.
	It("treats a converged Idle as completion", func() {
		out := driverOutcomeFor(&npuv1alpha1.DriverUpgradeStateStatus{
			State: npuv1alpha1.UpgradeStateIdle, CurrentVersion: "570.12.01", DesiredVersion: "570.12.01",
		})
		Expect(out.Event).To(Equal(operation.EventApplyDone))
	})

	// 증명: 목표 버전이 아직 계산되지 않은 Idle(빈 DesiredVersion)도 완료로 본다 —
	//       업그레이드할 것이 없는 노드에 작업이 걸린 경우다.
	// 깨는 뮤테이션: 빈 문자열 처리를 빼면 이 경우가 영원히 진행 중으로 남아 상한에 걸린다.
	It("treats an Idle with no desired version as completion", func() {
		out := driverOutcomeFor(&npuv1alpha1.DriverUpgradeStateStatus{
			State: npuv1alpha1.UpgradeStateIdle, CurrentVersion: "570.12.01",
		})
		Expect(out.Event).To(Equal(operation.EventApplyDone))
	})

	// 증명: 세 터미널 상태가 모두 실패 사건이 된다.
	// 깨는 뮤테이션: UnverifiedVersion·HostAhead 를 빠뜨리면 그 상태가 진행 중으로 남아 20분 상한까지 매달린다.
	It("maps the terminal states to an apply failure", func() {
		for _, st := range []string{npuv1alpha1.UpgradeStateFailed, npuv1alpha1.UpgradeStateUnverifiedVersion,
			npuv1alpha1.UpgradeStateHostAhead} {
			out := driverOutcomeFor(&npuv1alpha1.DriverUpgradeStateStatus{State: st})
			Expect(out.Event).To(Equal(operation.EventApplyFailed), "state=%s", st)
		}
	})

	// 증명: 재부팅 관련 두 상태가 재부팅 사건이 된다(진행 중과 구분돼야 조정자가 대기 단계로 간다).
	// 깨는 뮤테이션: 둘 중 하나라도 빠지면 재부팅이 그냥 "진행 중" 으로 흘러 대기 단계를 안 밟는다.
	It("maps reboot states to the reboot event", func() {
		for _, st := range []string{npuv1alpha1.UpgradeStateRebootRequired, npuv1alpha1.UpgradeStateRebooting} {
			out := driverOutcomeFor(&npuv1alpha1.DriverUpgradeStateStatus{State: st})
			Expect(out.Event).To(Equal(operation.EventRebootRequired), "state=%s", st)
		}
	})

	// 증명: 상태머신이 스스로 보상에 들어가면 조정자에게는 적용 실패로 보고된다.
	// 깨는 뮤테이션: Rollback 을 진행 중으로 두면 조정자가 보상 단계에 진입하지 못해
	//       RollbackAttempts 상한이 세어질 기회조차 없어진다.
	It("maps the state machine's own rollback to an apply failure", func() {
		out := driverOutcomeFor(&npuv1alpha1.DriverUpgradeStateStatus{State: npuv1alpha1.UpgradeStateRollback})
		Expect(out.Event).To(Equal(operation.EventApplyFailed))
	})

	// 증명: 진행 중 상태들은 사건 없이 재큐잉 간격만 준다.
	// 깨는 뮤테이션: 아무 사건이나 돌려주면 조정자가 미정의 전이 오류로 떨어진다.
	It("reports progress without an event for in-flight states", func() {
		for _, st := range []string{
			npuv1alpha1.UpgradeStateRequired, npuv1alpha1.UpgradeStatePreFlight,
			npuv1alpha1.UpgradeStateCordoning, npuv1alpha1.UpgradeStateDraining,
			npuv1alpha1.UpgradeStateUpgrading, npuv1alpha1.UpgradeStateValidating,
			npuv1alpha1.UpgradeStateUncordoning,
		} {
			out := driverOutcomeFor(&npuv1alpha1.DriverUpgradeStateStatus{State: st})
			Expect(out.Event).To(BeEmpty(), "state=%s", st)
			Expect(out.RequeueAfter).To(BeNumerically(">", 0), "state=%s", st)
		}
	})
})

var _ = Describe("driver participant resource keys", func() {
	// 증명: 드라이버 작업이 장치 driver facet 과 노드 cordon·reboot 를 모두 선언한다.
	//       이 선언이 없으면 같은 노드의 파티션 작업과 충돌로 잡히지 않는다.
	// 깨는 뮤테이션: 노드 cordon 키를 빼면 파티션 작업과의 충돌이 사라져 직렬화 스펙이 깨진다.
	It("claims device driver facets plus the node cordon and reboot keys", func() {
		keys := ResourceKeysForDriver("worker1", []string{"0000:41:00.0", "0000:81:00.0"})
		Expect(keys).To(ContainElement("device/0000:41:00.0/driver"))
		Expect(keys).To(ContainElement("device/0000:81:00.0/driver"))
		Expect(keys).To(ContainElement("node/worker1/cordon"))
		Expect(keys).To(ContainElement("node/worker1/reboot"))
	})

	// 증명: 장치 목록이 비어도 노드 키는 남는다(장치를 모르는 상태에서도 노드 단위 충돌은 잡힌다).
	// 깨는 뮤테이션: 빈 목록에서 조기 반환하면 키가 전부 사라져 충돌 판정이 통과해 버린다.
	It("still claims node keys when the device list is empty", func() {
		keys := ResourceKeysForDriver("worker1", nil)
		Expect(keys).To(ConsistOf("node/worker1/cordon", "node/worker1/reboot"))
	})
})
