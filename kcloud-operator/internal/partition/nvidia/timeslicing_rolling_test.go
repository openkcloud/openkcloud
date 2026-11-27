// ============================================================
// timeslicing_rolling_test.go: device-plugin 롤아웃 진행 판정 테스트
// 상세: 광고 공백을 장애로 오인하지 않으려면 "지금 롤링 중인가" 를 정확히 답해야 한다.
// 생성일: 2026-07-31
// ============================================================
package nvidia

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// scheme() 은 backend_test.go 가 정의한 공용 헬퍼를 그대로 쓴다 — 같은 패키지에 동명 함수를
// 새로 두면 충돌한다.

// 이 파일의 모든 테스트가 노드 하나("w1")·desired=2 인 DaemonSet 만 다룬다 — 두 값은 상수로
// 고정한다(unparam: 값이 안 변하는 파라미터를 두면 무엇이 실제로 바뀌는지 흐려진다).
func nodeWithLabel(mig bool) *corev1.Node {
	labels := map[string]string{}
	if mig {
		labels[MigActiveNodeLabel] = "true"
	}
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w1", Labels: labels}}
}

func dsWith(name string, gen, observed int64, updated, ready, unavailable int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kube-system", Generation: gen},
		Status: appsv1.DaemonSetStatus{
			ObservedGeneration:     observed,
			DesiredNumberScheduled: 2,
			UpdatedNumberScheduled: updated,
			NumberReady:            ready,
			NumberUnavailable:      unavailable,
		},
	}
}

func TestDevicePluginRollingIsFalseWhenSettled(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(nodeWithLabel(true), dsWith(DevicePluginNameMixed, 3, 3, 2, 2, 0)).Build()
	got, err := DevicePluginRolling(context.Background(), c, "w1")
	if err != nil || got {
		t.Fatalf("rolling=%v err=%v", got, err)
	}
}

func TestDevicePluginRollingDetectsStaleObservedGeneration(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(nodeWithLabel(true), dsWith(DevicePluginNameMixed, 4, 3, 2, 2, 0)).Build()
	if got, _ := DevicePluginRolling(context.Background(), c, "w1"); !got {
		t.Fatalf("generation 이 아직 반영되지 않았는데 정착으로 판정")
	}
}

func TestDevicePluginRollingDetectsUnavailableAndPartialUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		ds   *appsv1.DaemonSet
	}{
		{"unavailable", dsWith(DevicePluginNameMixed, 3, 3, 2, 1, 1)},
		{"partially updated", dsWith(DevicePluginNameMixed, 3, 3, 1, 2, 0)},
		// ready == desired 이지만 minReadySeconds 유예로 아직 available 로 못 올라온 창 —
		// NumberUnavailable 단독으로 롤아웃을 잡아야 한다(ready 조건과 섞이지 않게 고립).
		{"ready but not yet available", dsWith(DevicePluginNameMixed, 3, 3, 2, 2, 1)},
	} {
		c := fake.NewClientBuilder().WithScheme(scheme()).
			WithObjects(nodeWithLabel(true), tc.ds).Build()
		if got, _ := DevicePluginRolling(context.Background(), c, "w1"); !got {
			t.Fatalf("%s: 롤아웃 중인데 정착으로 판정", tc.name)
		}
	}
}

func TestDevicePluginRollingFollowsTheNodeLabel(t *testing.T) {
	// mig-active 라벨이 없는 노드는 flat DaemonSet 이 맡는다 — mixed 가 롤링 중이어도 무관하다.
	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(nodeWithLabel(false),
			dsWith(DevicePluginNameMixed, 9, 1, 0, 0, 2),
			dsWith(DevicePluginNameFlat, 3, 3, 2, 2, 0)).Build()
	if got, _ := DevicePluginRolling(context.Background(), c, "w1"); got {
		t.Fatalf("이 노드를 맡지 않는 DaemonSet 의 롤아웃을 봤다")
	}
}

func TestDevicePluginRollingIsFalseWhenDaemonSetAbsent(t *testing.T) {
	// DaemonSet 이 아예 없으면 롤아웃 중이 아니다 — 그 상태의 광고 부재는 억제 대상이 아니라
	// 진짜 문제다(억제하면 영원히 조용해진다).
	c := fake.NewClientBuilder().WithScheme(scheme()).
		WithObjects(nodeWithLabel(true)).Build()
	got, err := DevicePluginRolling(context.Background(), c, "w1")
	if err != nil || got {
		t.Fatalf("rolling=%v err=%v", got, err)
	}
}
