// ============================================================
// state_machine_failed_cordon_test.go: Failed 터미널 상태의 cordon 해제 단위 테스트
// 상세: Failed 터미널 상태에서 cordon 해제 조건(벤더 광고 0·소유권·진행 중 상태 여부)을
//       고정한다. 한 벤더의 드라이버 소실이 같은 노드의 다른 벤더까지 배치 불가로 만들지 않아야 한다.
// 생성일: 2026-08-05 | 수정일: 2026-10-11
// ============================================================

package upgrade

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// makeFailedDUS 는 Failed 터미널 상태의 DUS 를 반환한다(vendor 지정 가능).
func makeFailedDUS(vendor string) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: jDUSNm},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: jNode, Vendor: vendor, Model: jModel,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:              v1alpha1.UpgradeStateFailed,
			Message:            "롤백할 이전 버전 없음(job): 수동 조치 필요",
			LastTransitionTime: metav1.Now(),
		},
	}
}

// makeCordonTestDIP 는 daemonset 모드 DIP 를 반환한다. 이 파일의 시험은 버전도 autoUpgrade 도
// 보지 않는다 — cordon 해제 판정은 노드 상태와 DUS 소유권만으로 갈린다. DIP 는 상태머신 호출에
// 필요한 인자일 뿐이라 파라미터를 두지 않는다.
func makeCordonTestDIP() *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: jVendor},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: jVendor, Model: jModel,
			Driver:        v1alpha1.DriverSpec{Version: verNew}, // Mode="" = daemonset
			UpgradePolicy: &v1alpha1.UpgradePolicy{AutoUpgrade: true},
		},
	}
}

// makeCordonedNode 는 cordon 된 노드를 반환한다. owner 가 비어 있지 않으면 소유권 annotation 을
// 그 값으로 단다(빈 값 = 표식 없음 = 우리가 건 cordon 이 아님).
func makeCordonedNode(owner string, allocatable corev1.ResourceList) *corev1.Node {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Spec:       corev1.NodeSpec{Unschedulable: true},
		Status:     corev1.NodeStatus{Allocatable: allocatable},
	}
	if owner != "" {
		n.Annotations = map[string]string{cordonOwnerAnnotationKey: owner}
	}
	return n
}

func qty(v int64) resource.Quantity { return *resource.NewQuantity(v, resource.DecimalSI) }

// getNode 는 fake client 에서 노드를 다시 읽는다.
func getNode(t *testing.T, sm *UpgradeStateMachine) *corev1.Node {
	t.Helper()
	var n corev1.Node
	if err := sm.Get(context.Background(), types.NamespacedName{Name: jNode}, &n); err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	return &n
}

// ── 시험 1: Failed + 그 벤더 광고 0 → cordon 해제 ──────────────────────
// 드라이버가 빠져 device-plugin 이 그 벤더 광고를 0 으로 내리면, cordon 은 그 벤더에겐
// 중복이고 같은 노드의 다른 벤더에겐 순손해다.
func TestHandleFailed_ZeroAdvertisement_ReleasesCordon(t *testing.T) {
	dus := makeFailedDUS("nvidia")
	dip := makeCordonTestDIP()
	node := makeCordonedNode(jDUSNm, corev1.ResourceList{"nvidia.com/gpu": qty(0)})
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	requeue, _, err := sm.TransitionState(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if requeue {
		t.Error("해제 완료 후 requeue=true — 터미널 상태에서 더 볼 것이 없어야 한다")
	}
	got := getNode(t, sm)
	if got.Spec.Unschedulable {
		t.Error("cordon 이 유지됨 — 광고 0 이면 해제돼야 한다")
	}
	if _, ok := got.Annotations[cordonOwnerAnnotationKey]; ok {
		t.Error("소유권 annotation 잔류 — 해제와 함께 지워져야 재해제/재판정이 없다")
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Errorf("state=%q, want Failed — cordon 해제가 자동 복구를 재개해선 안 된다", dus.Status.State)
	}
}

// ── 시험 2: Failed 이지만 광고가 아직 살아 있음 → cordon 유지 ────────────
// 안전 쪽 절반이다. "드라이버가 없으면 광고가 떨어진다" 는 관측 하나일 뿐 보장이 아니다.
// 광고가 살아 있는데 풀면 드라이버 없는 장치 위로 워크로드가 떨어진다.
func TestHandleFailed_LiveAdvertisement_KeepsCordon(t *testing.T) {
	cases := []struct {
		name        string
		vendor      string
		allocatable corev1.ResourceList
	}{
		{
			name:        "whole-device 광고 잔존",
			vendor:      "nvidia",
			allocatable: corev1.ResourceList{"nvidia.com/gpu": qty(2)},
		},
		{
			// mixed MIG 는 프로파일마다 별도 리소스명으로 광고한다. nvidia.com/gpu 만 보면
			// MIG 장치가 살아 있는데도 0 으로 읽힌다.
			name:        "MIG 프로파일만 광고 중",
			vendor:      "nvidia",
			allocatable: corev1.ResourceList{"nvidia.com/gpu": qty(0), "nvidia.com/mig-1g.6gb": qty(7)},
		},
		{
			// 카탈로그에 없는 벤더는 어떤 리소스명을 봐야 하는지 모른다 — "광고 0" 을 증명할
			// 수 없으므로 해제 근거가 없다.
			name:        "카탈로그가 모르는 벤더",
			vendor:      "acme",
			allocatable: corev1.ResourceList{"acme.com/tpu": qty(4)},
		},
		{
			// furiosa 는 한 벤더 아래 제품이 둘(rngd/warboy)이다. 하나라도 살아 있으면 유지.
			name:        "furiosa 두 제품 중 하나만 살아 있음",
			vendor:      "furiosa",
			allocatable: corev1.ResourceList{"furiosa.ai/rngd": qty(0), "beta.furiosa.ai/npu": qty(1)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dus := makeFailedDUS(tc.vendor)
			dip := makeCordonTestDIP()
			node := makeCordonedNode(jDUSNm, tc.allocatable)
			sm := newUpgradeSMWithRecorder(dus, dip, node)

			requeue, after, err := sm.TransitionState(context.Background(), dus, dip)
			if err != nil {
				t.Fatalf("TransitionState 실패: %v", err)
			}
			got := getNode(t, sm)
			if !got.Spec.Unschedulable {
				t.Fatal("cordon 이 해제됨 — 광고를 0 으로 확인하지 못했으면 유지해야 한다")
			}
			if got.Annotations[cordonOwnerAnnotationKey] != jDUSNm {
				t.Error("소유권 annotation 이 사라짐 — 다음 회차에 다시 볼 근거를 잃는다")
			}
			// 보류했으면 다시 볼 기회가 있어야 한다(광고는 나중에 떨어질 수 있다).
			if !requeue || after != failedCordonRecheckInterval {
				t.Errorf("requeue=%v after=%v, want true/%v — 보류는 재진입이 있어야 의미가 있다",
					requeue, after, failedCordonRecheckInterval)
			}
		})
	}
}

// ── 시험 3: 다른 주체가 건 cordon 은 풀지 않는다 ────────────────────────
// ACPP quiesce(internal/partition/quiesce.go Cordon)와 운영자 수동 cordon 은 아무 표식도
// 남기지 않는다. 표식이 없거나 남의 것이면 그 cordon 은 우리 것이 아니다.
func TestHandleFailed_ForeignCordon_NotReleased(t *testing.T) {
	cases := []struct {
		name  string
		owner string
	}{
		{name: "표식 없음(ACPP quiesce / 수동 cordon)", owner: ""},
		{name: "다른 DUS 소유", owner: "k8s-worker1-nvidia"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dus := makeFailedDUS("nvidia")
			dip := makeCordonTestDIP()
			// 광고는 0 이다 — 즉 해제 조건 중 (c) 만으로는 풀리면 안 된다는 것을 고정한다.
			node := makeCordonedNode(tc.owner, corev1.ResourceList{"nvidia.com/gpu": qty(0)})
			sm := newUpgradeSMWithRecorder(dus, dip, node)

			if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
				t.Fatalf("TransitionState 실패: %v", err)
			}
			got := getNode(t, sm)
			if !got.Spec.Unschedulable {
				t.Fatal("남이 건 cordon 을 해제함 — 그 주체의 의도를 지운다")
			}
			if got.Annotations[cordonOwnerAnnotationKey] != tc.owner {
				t.Errorf("owner annotation=%q, want %q — 남의 표식을 건드리면 안 된다",
					got.Annotations[cordonOwnerAnnotationKey], tc.owner)
			}
		})
	}
}

// ── 시험 4: 진행 중 상태에서는 cordon 을 건드리지 않는다 ─────────────────
// 사이클이 도는 중의 cordon 은 목적이 있는 것이다. 해제 경로는 Failed 에서만 열린다.
func TestTransitionState_InProgressStates_LeaveCordonAlone(t *testing.T) {
	for _, state := range []string{
		v1alpha1.UpgradeStateDraining,
		v1alpha1.UpgradeStateUpgrading,
		v1alpha1.UpgradeStateValidating,
	} {
		t.Run(state, func(t *testing.T) {
			dus := makeFailedDUS("nvidia")
			dus.Status.State = state
			dip := makeCordonTestDIP()
			// 광고 0 = Failed 였다면 해제됐을 입력. 상태만으로 갈리는지 본다.
			node := makeCordonedNode(jDUSNm, corev1.ResourceList{"nvidia.com/gpu": qty(0)})
			sm := newUpgradeSMWithRecorder(dus, dip, node)

			if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
				t.Fatalf("TransitionState 실패: %v", err)
			}
			if !getNode(t, sm).Spec.Unschedulable {
				t.Errorf("%s 에서 cordon 이 해제됨 — 진행 중 사이클의 cordon 은 목적이 있다", state)
			}
		})
	}
}

// ── 시험 5: 해제 사실이 status/event 에 남는다 ──────────────────────────
// "왜 cordon 이 풀렸는가" 를 나중에 되짚을 수 있어야 한다. 실패 사유도 함께 보존한다.
func TestHandleFailed_Release_RecordsEventAndMessage(t *testing.T) {
	dus := makeFailedDUS("nvidia")
	origMsg := dus.Status.Message
	dip := makeCordonTestDIP()
	node := makeCordonedNode(jDUSNm, corev1.ResourceList{"nvidia.com/gpu": qty(0)})
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if !hasEvent(drainEvents(sm), "FailedCordonReleased") {
		t.Error("FailedCordonReleased 이벤트 미발행 — 해제 사실을 되짚을 수 없다")
	}
	if !strings.Contains(dus.Status.Message, origMsg) {
		t.Errorf("Message=%q — 원래 실패 사유가 지워졌다", dus.Status.Message)
	}
	if !strings.Contains(dus.Status.Message, "cordon") {
		t.Errorf("Message=%q — 해제 사실이 남지 않았다", dus.Status.Message)
	}
}

// ── cordonNode 소유권 표식 ─────────────────────────────────────────────
// 해제 판정의 전제다. 우리가 직접 false→true 로 뒤집었을 때만 표식을 남긴다 —
// 이미 cordon 된 노드에 표식을 달면 남의 cordon 을 우리 것으로 착각하게 된다.
func TestCordonNode_MarksOwnerOnlyWhenItFlipsTheNode(t *testing.T) {
	t.Run("우리가 뒤집음", func(t *testing.T) {
		dus := makeFailedDUS("nvidia")
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: jNode}}
		sm := newUpgradeSMWithRecorder(dus, node)

		if err := sm.cordonNode(context.Background(), jNode, dus); err != nil {
			t.Fatalf("cordonNode 실패: %v", err)
		}
		got := getNode(t, sm)
		if !got.Spec.Unschedulable {
			t.Fatal("cordon 안 됨")
		}
		if got.Annotations[cordonOwnerAnnotationKey] != jDUSNm {
			t.Errorf("owner annotation=%q, want %q", got.Annotations[cordonOwnerAnnotationKey], jDUSNm)
		}
	})

	t.Run("이미 cordon 된 노드", func(t *testing.T) {
		dus := makeFailedDUS("nvidia")
		node := makeCordonedNode("", nil) // 남이 이미 cordon 함
		sm := newUpgradeSMWithRecorder(dus, node)

		if err := sm.cordonNode(context.Background(), jNode, dus); err != nil {
			t.Fatalf("cordonNode 실패: %v", err)
		}
		if _, ok := getNode(t, sm).Annotations[cordonOwnerAnnotationKey]; ok {
			t.Error("남이 건 cordon 에 우리 소유권 표식을 달았다 — 나중에 남의 cordon 을 푼다")
		}
	})
}

// uncordonNode 는 정상 종료 경로다 — 우리 표식을 남기고 가면 다음 사이클/Failed 판정이 오염된다.
func TestUncordonNode_ClearsOwnerAnnotation(t *testing.T) {
	dus := makeFailedDUS("nvidia")
	node := makeCordonedNode(jDUSNm, nil)
	sm := newUpgradeSMWithRecorder(dus, node)

	if err := sm.uncordonNode(context.Background(), jNode, dus); err != nil {
		t.Fatalf("uncordonNode 실패: %v", err)
	}
	got := getNode(t, sm)
	if got.Spec.Unschedulable {
		t.Fatal("uncordon 안 됨")
	}
	if _, ok := got.Annotations[cordonOwnerAnnotationKey]; ok {
		t.Error("소유권 annotation 잔류")
	}
}

// 참고: failedCordonRecheckInterval 이 0 이면 보류가 tight loop 가 된다.
func TestFailedCordonRecheckInterval_IsPositive(t *testing.T) {
	if failedCordonRecheckInterval <= 0 {
		t.Fatalf("failedCordonRecheckInterval=%v, want > 0", failedCordonRecheckInterval)
	}
	_ = time.Second
}

// ── 시험 6: 사람이 uncordon 한 뒤 남은 stale 소유권 annotation 을 reaper 가 정리 ──────
// 사람이 kubectl uncordon 하면 Unschedulable=false 가 되지만 소유권 annotation 은 그대로
// 남는다. reaper 가 이 stale annotation 을 지우지 않으면, 그 뒤 다른 주체가 같은 노드를
// cordon 했을 때 handleFailed 가 남의 cordon 을 우리 것으로 오인해 풀 위험이 있다.
func TestHandleFailed_ReapsStaleClaimOnHumanUncordon(t *testing.T) {
	dus := makeFailedDUS("nvidia")
	dip := makeCordonTestDIP()
	// 1. 우리 cordon: Unschedulable=true, 소유권 annotation=이 DUS.
	node := makeCordonedNode(jDUSNm, corev1.ResourceList{"nvidia.com/gpu": qty(0)})
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	// 2. 사람 uncordon 시뮬: Unschedulable=false 로 뒤집되 annotation 은 그대로 둔다.
	uncordoned := getNode(t, sm)
	uncordoned.Spec.Unschedulable = false
	if err := sm.Update(context.Background(), uncordoned); err != nil {
		t.Fatalf("사람 uncordon 시뮬 실패: %v", err)
	}

	// 3. handleFailed 1회 → reaper 가 stale annotation 을 지워야 한다.
	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	afterReap := getNode(t, sm)
	if afterReap.Spec.Unschedulable {
		t.Fatal("사람이 uncordon 한 노드가 다시 cordon 됨")
	}
	if _, ok := afterReap.Annotations[cordonOwnerAnnotationKey]; ok {
		t.Error("stale 소유권 annotation 잔류 — reaper 가 정리하지 않았다")
	}

	// 4. foreign 재cordon: 다른 주체가 같은 노드를 cordon(표식 없음).
	afterReap.Spec.Unschedulable = true
	if err := sm.Update(context.Background(), afterReap); err != nil {
		t.Fatalf("foreign 재cordon 시뮬 실패: %v", err)
	}

	// 5. handleFailed → annotation 이 이미 정리됐으므로 남의 cordon 을 오인 해제하지 않는다.
	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if !getNode(t, sm).Spec.Unschedulable {
		t.Error("남의 cordon 을 오인 해제함 — stale annotation reaper 미작동")
	}
}
