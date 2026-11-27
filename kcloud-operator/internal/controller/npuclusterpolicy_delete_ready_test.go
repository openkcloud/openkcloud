// ============================================================
// npuclusterpolicy_delete_ready_test.go: 소유 DaemonSet 회수 실패 시 Ready 조건 반영 검증
// 상세: NVIDIA(mixed/flat 중 하나)·Rebellions·Tenstorrent 각각의 delete-on-disable 경로에서
// 삭제가 실패하면 Ready=False + 식별 reason 이 남는지, error 가 반환(재큐)되는지를 fake
// client Delete interceptor 로 검증한다. envtest 는 불필요하다.
// 생성일: 2026-09-20 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/partition/nvidia"
)

// ownedDS 는 지정한 namespace/name 에 이 policy 소유 annotation 을 단 DaemonSet 을 만든다.
func ownedDS(namespace, name, policyNamespace, policyName string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				ownerAnnotation: policyNamespace + "/" + policyName,
			},
		},
	}
}

// reconcileWithFailingDelete 는 targetName 의 Delete 만 실패시키는 fake client 로 policy 를
// reconcile 하고, 재조회한 policy 와 Reconcile 의 반환 error 를 함께 돌려준다.
func reconcileWithFailingDelete(
	t *testing.T, policy *npuv1alpha1.NPUClusterPolicy, injected error, targetName string, existing ...client.Object,
) (*npuv1alpha1.NPUClusterPolicy, error) {
	t.Helper()
	s := newTestScheme()
	objs := append([]client.Object{policy}, existing...)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&npuv1alpha1.NPUClusterPolicy{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if obj.GetName() == targetName {
					return injected
				}
				return cl.Delete(ctx, obj, opts...)
			},
		}).
		Build()
	r := &NPUClusterPolicyReconciler{Client: c, Scheme: s, Recorder: record.NewFakeRecorder(10)}

	ctx := context.Background()
	_, reconcileErr := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace},
	})

	var got npuv1alpha1.NPUClusterPolicy
	if err := r.Get(ctx, types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, &got); err != nil {
		t.Fatalf("policy 재조회 실패: %v", err)
	}
	return &got, reconcileErr
}

// basePolicy 는 delete-on-disable 경로까지 오류 없이 도달하는 최소 spec 이다 —
// detector.image 만 채우고 나머지 벤더는 기본값(Enabled=false)으로 둔다.
func basePolicy(name string) *npuv1alpha1.NPUClusterPolicy {
	return makePolicy(name, npuv1alpha1.NPUClusterPolicySpec{
		Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
	})
}

// NVIDIA 토글 off 상태에서 mixed DS 회수가 실패하면 Ready=False(reason 에 mixed/flat 구분)로
// 남고 error 가 반환(재큐)되어야 한다. Ready.Reason 은 CRD 스키마 패턴(^[A-Za-z]([A-Za-z0-9_,:]*
// [A-Za-z0-9_])?$, config/crd/bases/npu.ai_npuclusterpolicies.yaml)이 하이픈을 금지해 DaemonSet
// 이름(예: nvidia-device-plugin)을 그대로 이어붙이지 않고 PascalCase 로 남긴다 — 실제 이름은
// Warning 이벤트 메시지에 남는다. 아래 Rebellions·Tenstorrent 시험도 같은 규칙을 따른다.
func TestNPUClusterPolicy_NvidiaRemoveFailure_SetsReadyFalse(t *testing.T) {
	policy := basePolicy("test-policy")
	injected := errors.New("injected nvidia delete failure")
	ds := ownedDS("kube-system", nvidia.DevicePluginNameMixed, policy.Namespace, policy.Name)

	got, err := reconcileWithFailingDelete(t, policy, injected, nvidia.DevicePluginNameMixed, ds)
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("삭제 오류가 반환값에 있어야 한다: got %v", err)
	}

	cond := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
	if cond == nil {
		t.Fatal("Ready 조건이 없다 — 회수 실패가 반영되지 않았다")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready.Status = %v, want False", cond.Status)
	}
	if cond.Reason != "NvidiaDevicePluginMixedRemoveFailed" {
		t.Errorf("Ready.Reason = %q, want NvidiaDevicePluginMixedRemoveFailed", cond.Reason)
	}
	if cond.Message != injected.Error() {
		t.Errorf("Ready.Message = %q, want %q", cond.Message, injected.Error())
	}
}

// Rebellions 토글 off 상태에서 소유 DS 회수가 실패하면 Ready=False 로 남고 error 가
// 반환(재큐)되어야 한다.
func TestNPUClusterPolicy_RebellionsRemoveFailure_SetsReadyFalse(t *testing.T) {
	policy := basePolicy("test-policy")
	injected := errors.New("injected rebellions delete failure")
	ds := ownedDS(rbllnsNamespaceDefault, rbllnsDaemonSetName, policy.Namespace, policy.Name)

	got, err := reconcileWithFailingDelete(t, policy, injected, rbllnsDaemonSetName, ds)
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("삭제 오류가 반환값에 있어야 한다: got %v", err)
	}

	cond := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
	if cond == nil {
		t.Fatal("Ready 조건이 없다 — 회수 실패가 반영되지 않았다")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready.Status = %v, want False", cond.Status)
	}
	if cond.Reason != "RebellionsDevicePluginRemoveFailed" {
		t.Errorf("Ready.Reason = %q, want RebellionsDevicePluginRemoveFailed", cond.Reason)
	}
}

// Tenstorrent 토글 off 상태에서 소유 DS 회수가 실패하면 Ready=False 로 남고 error 가
// 반환(재큐)되어야 한다.
func TestNPUClusterPolicy_TenstorrentRemoveFailure_SetsReadyFalse(t *testing.T) {
	policy := basePolicy("test-policy")
	injected := errors.New("injected tenstorrent delete failure")
	ds := ownedDS(naming.OperatorNamespace(), ttDaemonSetName, policy.Namespace, policy.Name)

	got, err := reconcileWithFailingDelete(t, policy, injected, ttDaemonSetName, ds)
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("삭제 오류가 반환값에 있어야 한다: got %v", err)
	}

	cond := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
	if cond == nil {
		t.Fatal("Ready 조건이 없다 — 회수 실패가 반영되지 않았다")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready.Status = %v, want False", cond.Status)
	}
	if cond.Reason != "TenstorrentDevicePluginRemoveFailed" {
		t.Errorf("Ready.Reason = %q, want TenstorrentDevicePluginRemoveFailed", cond.Reason)
	}
}
