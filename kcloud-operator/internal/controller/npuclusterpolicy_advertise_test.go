// ============================================================
// npuclusterpolicy_advertise_test.go: 광고 주체 선택 스위치(advertiseBy) 동작 검증
// 상세: dra 로 선언한 벤더의 대상 노드에 라벨이 붙고 되돌리면 떨어지는 것, 그 라벨이 있는
//       노드에서 벤더 device-plugin DaemonSet 이 물러나는 것을 단정한다.
// 생성일: 2026-08-06 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

// advNode 는 벤더 present 라벨을 가진 노드다.
func advNode(name string, labels map[string]string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

// advReconciler 는 pod 인덱스·status 서브리소스를 갖춘 fake client 기반 reconciler 다.
// 전환 판정이 pod 조회와 status 기록을 하므로 둘 다 필요하다.
func advReconciler(objs ...client.Object) *NPUClusterPolicyReconciler {
	s := newTestScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = resourcev1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).
		WithStatusSubresource(&npuv1alpha1.NPUClusterPolicy{}).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	return &NPUClusterPolicyReconciler{Client: c, Scheme: s, Recorder: record.NewFakeRecorder(64)}
}

func nodeHasLabel(t *testing.T, r *NPUClusterPolicyReconciler, node, key string) bool {
	t.Helper()
	var n corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: node}, &n); err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	_, ok := n.Labels[key]
	return ok
}

// TestReconcileDRAOwnedLabels_AppliesToVendorNodes 는 selector 미지정이면 그 벤더 장치가 있는
// 모든 노드에 라벨이 붙는 것을 단정한다.
func TestReconcileDRAOwnedLabels_AppliesToVendorNodes(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled:       true,
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDRA},
			},
		},
	}
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true"}),
		advNode("gpu-2", map[string]string{"kcloud.ai/nvidia.present": "true"}),
		advNode("npu-1", map[string]string{"kcloud.ai/tenstorrent.present": "true"}),
		advSlice("s1", "gpu-1", draDriverNameNvidia), advSlice("s2", "gpu-2", draDriverNameNvidia))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	key := npuv1alpha1.DRAOwnedNodeLabel("nvidia")
	for _, n := range []string{"gpu-1", "gpu-2"} {
		if !nodeHasLabel(t, r, n, key) {
			t.Errorf("%s 에 %s 라벨이 없음", n, key)
		}
	}
	if nodeHasLabel(t, r, "npu-1", key) {
		t.Error("다른 벤더 노드에 nvidia 라벨이 붙음")
	}
}

// TestReconcileDRAOwnedLabels_Rebellions 는 Rebellions 도 다른 벤더와 같은 전환 경로를
// 타는 것을 단정한다.
func TestReconcileDRAOwnedLabels_Rebellions(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Rebellions: npuv1alpha1.RebellionsSpec{
				Enabled:       true,
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDRA},
			},
		},
	}
	r := advReconciler(policy,
		advNode("rbln-1", map[string]string{"kcloud.ai/rebellions.present": "true"}),
		advSlice("s1", "rbln-1", draDriverNameRebellions))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	key := npuv1alpha1.DRAOwnedNodeLabel(vendorRebellions)
	if !nodeHasLabel(t, r, "rbln-1", key) {
		t.Errorf("rbln-1 에 %s 라벨이 없음", key)
	}
}

// TestReconcileDRAOwnedLabels_HonorsNodeSelector 는 selector 가 대상 노드를 좁히는 것을 단정한다.
func TestReconcileDRAOwnedLabels_HonorsNodeSelector(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled: true,
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{
					AdvertiseBy: npuv1alpha1.AdvertiseByDRA,
					AdvertiseByNodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"kcloud.ai/dra-pilot": "true"},
					},
				},
			},
		},
	}
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true", "kcloud.ai/dra-pilot": "true"}),
		advNode("gpu-2", map[string]string{"kcloud.ai/nvidia.present": "true"}),
		advSlice("s1", "gpu-1", draDriverNameNvidia), advSlice("s2", "gpu-2", draDriverNameNvidia))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	key := npuv1alpha1.DRAOwnedNodeLabel("nvidia")
	if !nodeHasLabel(t, r, "gpu-1", key) {
		t.Error("selector 에 맞는 노드에 라벨이 없음")
	}
	if nodeHasLabel(t, r, "gpu-2", key) {
		t.Error("selector 밖 노드에 라벨이 붙음")
	}
}

// TestReconcileDRAOwnedLabels_RevokesWhenReverted 는 spec 을 되돌리면 라벨이 떨어지는 것을
// 단정한다. 되돌아오지 않는 스위치는 스위치가 아니다 — 고아 라벨이 남으면 그 노드의
// device-plugin 이 영원히 돌아오지 못한다.
func TestReconcileDRAOwnedLabels_RevokesWhenReverted(t *testing.T) {
	key := npuv1alpha1.DRAOwnedNodeLabel("nvidia")
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{Enabled: true},
		},
	}
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true", key: "true"}))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if nodeHasLabel(t, r, "gpu-1", key) {
		t.Error("advertiseBy 를 되돌렸는데 라벨이 남음")
	}
}

// TestReconcileDRAOwnedLabels_RetiresNodeOnRevert 는 dra-owned 를 회수하는 바로 그
// reconcile 에서, 그 벤더의 dra.enabled 가 true 면 retired 라벨을 부여하는 것을 단정한다.
// 이 라벨이 없으면 DRA 드라이버 파드가 되돌린 노드에서 물러나지 않아 ResourceSlice 가
// 계속 남고 이중광고가 이어진다. 두 번째 reconcile 에서도 남아 있는지(sticky) 함께 본다.
func TestReconcileDRAOwnedLabels_RetiresNodeOnRevert(t *testing.T) {
	key := npuv1alpha1.DRAOwnedNodeLabel("nvidia")
	retiredKey := npuv1alpha1.DRARetiredNodeLabel("nvidia")
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled: true,
				DRA:     &npuv1alpha1.DRASpec{Enabled: true},
			},
		},
	}
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true", key: "true"}))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if nodeHasLabel(t, r, "gpu-1", key) {
		t.Error("advertiseBy 를 되돌렸는데 dra-owned 라벨이 남음")
	}
	if !nodeHasLabel(t, r, "gpu-1", retiredKey) {
		t.Fatal("회수 순간 retired 라벨이 붙지 않음")
	}

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("2회차 reconcileDRAOwnedLabels 오류: %v", err)
	}
	if !nodeHasLabel(t, r, "gpu-1", retiredKey) {
		t.Error("2회차에 retired 라벨이 사라짐 — want=false·dra.enabled=true 인 동안은 남아야 한다")
	}
}

// TestReconcileDRAOwnedLabels_ClearsRetiredWhenWantAgain 는 advertiseBy 를 dra 로 다시
// 돌리면, 그 전환 자체가(발행자 미확인으로) 거절되더라도 retired 라벨은 먼저 지워지는 것을
// 단정한다. 라벨을 먼저 지워야 DRA 드라이버 파드가 그 노드에 다시 스케줄돼 발행을 시작할 수
// 있다 — 지우지 않으면 발행자가 영영 돌아오지 못해 refuseAdvertiseSwitch 게이트를 통과할
// 길이 없다.
func TestReconcileDRAOwnedLabels_ClearsRetiredWhenWantAgain(t *testing.T) {
	retiredKey := npuv1alpha1.DRARetiredNodeLabel("nvidia")
	policy := draPolicy()
	policy.Spec.Nvidia.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true", retiredKey: "true"}))
	// ResourceSlice 를 두지 않는다 — dra-owned 전환 자체는 거절돼야 한다(이 시험의 목적은
	// 그 거절과 무관하게 retired 가 먼저 지워지는지 확인하는 것).

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if nodeHasLabel(t, r, "gpu-1", retiredKey) {
		t.Error("want=true 로 돌아왔는데 retired 라벨이 안 지워짐")
	}
	if nodeHasLabel(t, r, "gpu-1", npuv1alpha1.DRAOwnedNodeLabel("nvidia")) {
		t.Error("발행자 미확인인데 dra-owned 가 붙음(이 시험의 전제가 깨짐)")
	}
}

// TestReconcileDRAOwnedLabels_ClearsRetiredWhenDRADisabled 는 dra.enabled 가 꺼져 있으면
// (또는 애초에 켠 적이 없으면) want=false 로 머물러도 retired 라벨이 지워지는 것을 단정한다 —
// 드라이버 DaemonSet 자체가 배포되지 않으므로 그 라벨로 스케줄을 막아 둘 이유가 없다.
func TestReconcileDRAOwnedLabels_ClearsRetiredWhenDRADisabled(t *testing.T) {
	retiredKey := npuv1alpha1.DRARetiredNodeLabel("nvidia")
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			// advertiseBy 는 기본값(devicePlugin), DRA 는 nil(=disabled).
			Nvidia: npuv1alpha1.NvidiaSpec{Enabled: true},
		},
	}
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true", retiredKey: "true"}))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if nodeHasLabel(t, r, "gpu-1", retiredKey) {
		t.Error("dra.enabled=false 인데 retired 라벨이 남음")
	}
}

// TestReconcileDRARetiredLabel_ClearsOnlyAfterDaemonSetGone 는 dra.enabled=false 로
// 내려도 그 벤더의 DRA DaemonSet 이 아직 클러스터에 있으면 retired 라벨을 지우지 않고,
// DaemonSet 이 실제로 사라진 뒤에야 지우는 것을 단정한다. 라벨을 DaemonSet 삭제보다
// 먼저 지우면 그 순간 노드가 다시 스케줄 후보가 되어, DaemonSet 컨트롤러가 새 파드를
// 잠깐 올렸다가 뒤이은 DaemonSet 삭제와 함께 사라지는 경합이 생긴다.
func TestReconcileDRARetiredLabel_ClearsOnlyAfterDaemonSetGone(t *testing.T) {
	retiredKey := npuv1alpha1.DRARetiredNodeLabel("nvidia")
	newPolicy := func() *npuv1alpha1.NPUClusterPolicy {
		return &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				// advertiseBy 는 기본값(devicePlugin) — dra.enabled 만 false 로 내리는 시나리오.
				Nvidia: npuv1alpha1.NvidiaSpec{Enabled: true, DRA: &npuv1alpha1.DRASpec{Enabled: false}},
			},
		}
	}

	t.Run("DaemonSet이_아직_있으면_유지", func(t *testing.T) {
		policy := newPolicy()
		ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: draDSNameNvidia, Namespace: draNamespace}}
		r := advReconciler(policy, advNode("gpu-1", map[string]string{retiredKey: "true"}), ds)

		if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
			t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
		}
		if !nodeHasLabel(t, r, "gpu-1", retiredKey) {
			t.Error("DaemonSet 이 아직 있는데 retired 라벨이 지워짐 — 파드 재스케줄 경합 위험")
		}
	})

	t.Run("DaemonSet이_사라지면_지운다", func(t *testing.T) {
		policy := newPolicy()
		r := advReconciler(policy, advNode("gpu-1", map[string]string{retiredKey: "true"})) // DaemonSet 없음

		if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
			t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
		}
		if nodeHasLabel(t, r, "gpu-1", retiredKey) {
			t.Error("DaemonSet 이 사라진 뒤에도 retired 라벨이 남음")
		}
	})
}

// dsExcludesDRAOwned 는 렌더된 PodSpec 이 해당 벤더의 dra-owned 라벨 부재를 요구하는지 본다.
func dsExcludesDRAOwned(ds *appsv1.DaemonSet, vendor string) bool {
	key := npuv1alpha1.DRAOwnedNodeLabel(vendor)
	na := ds.Spec.Template.Spec.Affinity
	if na == nil || na.NodeAffinity == nil ||
		na.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return false
	}
	for _, term := range na.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, e := range term.MatchExpressions {
			if e.Key == key && e.Operator == corev1.NodeSelectorOpDoesNotExist {
				return true
			}
		}
	}
	return false
}

// TestNvidiaDevicePluginExcludesDRAOwnedNodes 는 NVIDIA 의 두 DaemonSet(mixed·flat) 모두가
// dra-owned 노드를 피하는 것을 단정한다. 한쪽만 고치면 MIG 노드에서 계속 광고된다.
func TestNvidiaDevicePluginExcludesDRAOwnedNodes(t *testing.T) {
	r := &NPUClusterPolicyReconciler{}
	base := map[string]string{"kcloud.ai/nvidia.present": "true"}
	for _, tc := range []struct {
		name  string
		mixed bool
	}{{nvidia.DevicePluginNameMixed, true}, {nvidia.DevicePluginNameFlat, false}} {
		ds := r.buildNvidiaDevicePluginDS(&npuv1alpha1.NPUClusterPolicy{}, tc.name, base, tc.mixed)
		if !dsExcludesDRAOwned(ds, "nvidia") {
			t.Errorf("%s: dra-owned 노드를 제외하지 않음", tc.name)
		}
	}
}

// TestRebellionsDevicePluginExcludesDRAOwnedNodes 는 ensureRebellionsDevicePlugin 이 만든
// DaemonSet 도 dra-owned 노드를 피하는 것을 단정한다. 이 함수는 createOrUpdateDS 로 쓰므로
// build 함수 반환값이 아니라 fake client 에서 다시 조회해야 한다.
func TestRebellionsDevicePluginExcludesDRAOwnedNodes(t *testing.T) {
	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Rebellions: npuv1alpha1.RebellionsSpec{Enabled: true, DevicePluginImage: "img:1"},
	})
	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureRebellionsDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureRebellionsDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	key := types.NamespacedName{Name: rbllnsDaemonSetName, Namespace: rbllnsResolveNamespace(policy)}
	if err := r.Get(ctx, key, &ds); err != nil {
		t.Fatalf("DaemonSet 조회 실패: %v", err)
	}
	if !dsExcludesDRAOwned(&ds, vendorRebellions) {
		t.Error("rebellions device-plugin 이 dra-owned 노드를 제외하지 않음")
	}
}

// advPod 는 nvidia.com/gpu 를 쥔 pod 이다.
func advPod(name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team-a"},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "busybox",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// advSlice 는 노드가 driver 로 DRA 장치를 내놓고 있다는 사실이다. refuseAdvertiseSwitch 가
// 전환하려는 벤더 자신의 드라이버인지를 가리므로(다른 벤더의 드라이버는 게이트를 통과시키지
// 않는다), 호출부는 그 시험이 다루는 벤더의 실제 드라이버 이름을 넘겨야 한다.
func advSlice(name, node, driver string) *resourcev1.ResourceSlice {
	return &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.ResourceSliceSpec{
			NodeName: &node,
			Driver:   driver,
			Pool:     resourcev1.ResourcePool{Name: node, ResourceSliceCount: 1},
			Devices:  []resourcev1.Device{{Name: "gpu-0"}},
		},
	}
}

func draPolicy() *npuv1alpha1.NPUClusterPolicy {
	return &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled:       true,
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDRA},
			},
		},
	}
}

// TestReconcileDRAOwnedLabels_RefusesWhileDeviceInUse 는 장치를 쥔 pod 이 있는 노드에서는
// 전환을 거절하고 그 사실을 status 에 남기는 것을 단정한다. 조용히 끄면 광고 붕괴를 우리가
// 일부러 만드는 셈이다 — 돌던 pod 은 재시작되는 순간 스케줄되지 못한다.
func TestReconcileDRAOwnedLabels_RefusesWhileDeviceInUse(t *testing.T) {
	policy := draPolicy()
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true"}),
		advPod("train", "gpu-1"),
		advSlice("slice-1", "gpu-1", draDriverNameNvidia))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if nodeHasLabel(t, r, "gpu-1", npuv1alpha1.DRAOwnedNodeLabel("nvidia")) {
		t.Error("점유 중인데 라벨이 붙음")
	}
	var got npuv1alpha1.NPUClusterPolicy
	if err := r.Get(context.Background(), types.NamespacedName{Name: "p", Namespace: "kcloud"}, &got); err != nil {
		t.Fatalf("정책 조회 실패: %v", err)
	}
	if len(got.Status.AdvertiseSwitch) == 0 {
		t.Fatal("status 에 전환 결과가 없음")
	}
	e := got.Status.AdvertiseSwitch[0]
	if e.Applied || e.Node != "gpu-1" || len(e.BlockingPods) == 0 {
		t.Errorf("거절 사유가 노드·파드를 지목하지 않음: %+v", e)
	}
}

// TestReconcileDRAOwnedLabels_RefusesWithoutResourceSlice 는 DRA 가 아직 그 노드에서
// 발행하지 않으면 전환을 거절하는 것을 단정한다. 이걸 놓치면 device-plugin 은 껐는데 DRA 는
// 아직 없는 — 양쪽 광고가 다 0 인 노드가 만들어진다.
func TestReconcileDRAOwnedLabels_RefusesWithoutResourceSlice(t *testing.T) {
	policy := draPolicy()
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true"}))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if nodeHasLabel(t, r, "gpu-1", npuv1alpha1.DRAOwnedNodeLabel("nvidia")) {
		t.Error("ResourceSlice 가 없는데 라벨이 붙음")
	}
}

// TestReconcileDRAOwnedLabels_AppliesWhenIdleAndPublished 는 비어 있고 발행도 있으면
// 전환이 실제로 이뤄지는 것을 단정한다(거절만 하는 코드가 되지 않도록).
func TestReconcileDRAOwnedLabels_AppliesWhenIdleAndPublished(t *testing.T) {
	policy := draPolicy()
	r := advReconciler(policy,
		advNode("gpu-1", map[string]string{"kcloud.ai/nvidia.present": "true"}),
		advSlice("slice-1", "gpu-1", draDriverNameNvidia))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if !nodeHasLabel(t, r, "gpu-1", npuv1alpha1.DRAOwnedNodeLabel("nvidia")) {
		t.Error("비어 있고 발행도 있는데 라벨이 붙지 않음")
	}
}

// TestRefuseAdvertiseSwitch_RequiresOwnVendorDriverSlice 는 노드에 다른 벤더(nvidia)의
// 드라이버가 낸 ResourceSlice 만 있을 때는 그 벤더(tenstorrent)로 전환을 거절하는 것을
// 단정한다. ResourceSlice 유무만 보고 승인하면 다른 벤더가 이미 발행 중인 노드에도
// 전환이 열린다.
func TestRefuseAdvertiseSwitch_RequiresOwnVendorDriverSlice(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Tenstorrent: npuv1alpha1.TenstorrentSpec{
				Enabled:       true,
				AdvertiseSpec: npuv1alpha1.AdvertiseSpec{AdvertiseBy: npuv1alpha1.AdvertiseByDRA},
			},
		},
	}
	r := advReconciler(policy,
		advNode("n1", map[string]string{"kcloud.ai/tenstorrent.present": "true"}),
		advSlice("s1", "n1", draDriverNameNvidia))

	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	if nodeHasLabel(t, r, "n1", npuv1alpha1.DRAOwnedNodeLabel(vendorTenstorrent)) {
		t.Error("다른 벤더(nvidia) 드라이버의 슬라이스만 있는데 tenstorrent 로 전환됨")
	}
	var got npuv1alpha1.NPUClusterPolicy
	if err := r.Get(context.Background(), types.NamespacedName{Name: "p", Namespace: "kcloud"}, &got); err != nil {
		t.Fatalf("정책 조회 실패: %v", err)
	}
	if len(got.Status.AdvertiseSwitch) == 0 {
		t.Fatal("status 에 전환 결과가 없음")
	}
	e := got.Status.AdvertiseSwitch[0]
	if e.Applied || !strings.Contains(e.Reason, draDriverNameTenstorrent) {
		t.Errorf("거절 사유가 tenstorrent 드라이버를 지목하지 않는다: %+v", e)
	}
}

// TestAdvertiseVendorsNilPolicy 는 nil 정책을 넘겨도 다섯 벤더 키를 돌려주는지 본다 —
// 삭제 경로처럼 정책 객체가 없는 자리에서 불려도 panic 하지 않아야 한다.
func TestAdvertiseVendorsNilPolicy(t *testing.T) {
	got := advertiseVendors(nil)
	if len(got) != 5 {
		t.Fatalf("nil 정책의 벤더 키 %d 개, want 5: %v", len(got), got)
	}
	for _, v := range []string{vendorNvidia, vendorFuriosa, vendorRngd, vendorTenstorrent, vendorRebellions} {
		if _, ok := got[v]; !ok {
			t.Errorf("벤더 %s 키가 없다", v)
		}
	}
}

// TestReconcileDRAOwnedLabels_RecordsDevicePluginNodes 는 devicePlugin 으로 선언된 벤더의 장치
// 노드도 status.advertiseSwitch 에 항목을 갖는지 본다 — "항목 없음" 이 "광고 없음" 으로
// 읽히지 않아야 한다.
func TestReconcileDRAOwnedLabels_RecordsDevicePluginNodes(t *testing.T) {
	policy := draPolicy()
	policy.Spec.Nvidia.AdvertiseSpec = npuv1alpha1.AdvertiseSpec{} // 기본값 = devicePlugin
	r := advReconciler(policy, advNode("gpu-1", map[string]string{vendorPresentLabel(vendorNvidia): labelValueTrue}))
	if err := r.reconcileDRAOwnedLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileDRAOwnedLabels 오류: %v", err)
	}
	var got npuv1alpha1.NPUClusterPolicy
	if err := r.Get(context.Background(), types.NamespacedName{Name: "p", Namespace: "kcloud"}, &got); err != nil {
		t.Fatalf("정책 조회 실패: %v", err)
	}
	var entry *npuv1alpha1.AdvertiseSwitchStatus
	for i := range got.Status.AdvertiseSwitch {
		if got.Status.AdvertiseSwitch[i].Vendor == vendorNvidia && got.Status.AdvertiseSwitch[i].Node == "gpu-1" {
			entry = &got.Status.AdvertiseSwitch[i]
		}
	}
	if entry == nil {
		t.Fatalf("devicePlugin 벤더의 노드 항목이 없다: %+v", got.Status.AdvertiseSwitch)
	}
	if entry.Mode != "devicePlugin" || !entry.Applied || entry.Reason != "" {
		t.Errorf("항목이 선언 모드·적용 여부를 담지 않는다: %+v", *entry)
	}
}
