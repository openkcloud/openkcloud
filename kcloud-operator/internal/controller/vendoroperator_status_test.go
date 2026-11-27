// ============================================================
// vendoroperator_status_test.go: 벤더 operator CR 상태 조회·Ready 조건 시험
// 상세: readVendorOperatorState 단위 시험 + detectVendorOperator 가 그 결과로
//
//	<Vendor>OperatorReady 조건을 세우는지 통합 시험(clusterPolicyObj 는 unstructured 로 만든다).
//
// 생성일: 2026-10-09 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// clusterPolicyObj 는 nvidia.com/v1 ClusterPolicy 를 unstructured 로 만든다.
func clusterPolicyObj(name, state string) *unstructured.Unstructured {
	cp := &unstructured.Unstructured{}
	cp.SetAPIVersion("nvidia.com/v1")
	cp.SetKind("ClusterPolicy")
	cp.SetName(name)
	_ = unstructured.SetNestedField(cp.Object, state, "status", "state")
	return cp
}

func TestReadVendorOperatorState_Nvidia(t *testing.T) {
	reader := fake.NewClientBuilder().WithRuntimeObjects(clusterPolicyObj("cluster-policy", "ready")).Build()
	r := &NPUClusterPolicyReconciler{APIReader: reader}
	st, err := r.readVendorOperatorState(context.Background(), "nvidia")
	if err != nil || !st.found || st.state != "ready" || !st.ready || st.name != "cluster-policy" {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestReadVendorOperatorState_NoProbeIsUnknown(t *testing.T) {
	r := &NPUClusterPolicyReconciler{APIReader: fake.NewClientBuilder().Build()}
	st, err := r.readVendorOperatorState(context.Background(), "tenstorrent")
	if err != nil || st.found || st.probe {
		t.Fatalf("probe 없는 벤더는 unknown 이어야 한다: %+v", st)
	}
}

// TestReadVendorOperatorState_NoClusterPolicy: 표에 있는 벤더(nvidia)인데 ClusterPolicy CR 이
// 하나도 없으면(List 성공·0개) probe=true, found=false, blocked=false 다 — blocked(CRD·권한
// 문제로 조회 자체가 막힘)와는 다른 경로라 호출부가 NoClusterPolicy 로 가른다. 빈 fake
// 클라이언트에 GVK 가 찍힌 *unstructured.UnstructuredList 로 List 하면 NoMatch 가 아니라 빈
// 목록이 돌아온다 — fakeClient.List 가 List Kind 를 즉석 등록한 뒤 tracker.List 를 부르기
// 때문이다(TestReadVendorOperatorState_Nvidia 와 같은 메커니즘).
func TestReadVendorOperatorState_NoClusterPolicy(t *testing.T) {
	r := &NPUClusterPolicyReconciler{APIReader: fake.NewClientBuilder().Build()}
	st, err := r.readVendorOperatorState(context.Background(), "nvidia")
	if err != nil || !st.probe || st.found || st.blocked {
		t.Fatalf("CR 없음은 probe=true, found=false, blocked=false 여야 한다: st=%+v err=%v", st, err)
	}
}

// forbiddenListReader 는 List 호출마다 Forbidden 오류를 돌려주는 client.Reader 스텁이다 — RBAC
// 가 빠진 상황을 흉내낸다(readVendorOperatorState 의 blocked 분기 시험용). Get 은 쓰지 않아
// 비워 둔다(embedded nil client.Reader — 호출되면 panic 하지만 이 시험 경로는 List 만 쓴다).
type forbiddenListReader struct{ client.Reader }

func (forbiddenListReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return apierrors.NewForbidden(schema.GroupResource{Group: "nvidia.com", Resource: "clusterpolicies"}, "", nil)
}

// noMatchListReader 는 List 호출마다 RESTMapper NoMatch 오류를 돌려주는 client.Reader 스텁이다.
type noMatchListReader struct{ client.Reader }

func (noMatchListReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "nvidia.com", Kind: "ClusterPolicy"}, SearchedVersions: []string{"v1"}}
}

// TestReadVendorOperatorState_Blocked: List 가 Forbidden 또는 NoMatch 로 실패하면 둘 다
// probe=true, found=false, blocked=true, err=nil 이어야 한다(CRD 없음·권한 없음은 Unknown 으로
// 보고 — vendoroperator_status.go 주석).
func TestReadVendorOperatorState_Blocked(t *testing.T) {
	t.Run("Forbidden", func(t *testing.T) {
		r := &NPUClusterPolicyReconciler{APIReader: forbiddenListReader{}}
		st, err := r.readVendorOperatorState(context.Background(), "nvidia")
		if err != nil || !st.probe || st.found || !st.blocked {
			t.Fatalf("Forbidden 은 probe=true, found=false, blocked=true 여야 한다: st=%+v err=%v", st, err)
		}
	})
	t.Run("NoMatch", func(t *testing.T) {
		r := &NPUClusterPolicyReconciler{APIReader: noMatchListReader{}}
		st, err := r.readVendorOperatorState(context.Background(), "nvidia")
		if err != nil || !st.probe || st.found || !st.blocked {
			t.Fatalf("NoMatch 은 probe=true, found=false, blocked=true 여야 한다: st=%+v err=%v", st, err)
		}
	})
}

// TestSetVendorOperatorReadyCondition_NoClusterPolicy 와
// TestSetVendorOperatorReadyCondition_StatusProbeUnavailable 은 readVendorOperatorState 의
// 결과가 조건 reason 까지 올바르게 올라가는지 본다(detectNvidiaGPUOperator 의 mapper·CRD 재확인
// 기구 없이 setVendorOperatorReadyCondition 을 직접 호출 — present=true 를 주면 그 아래만 본다).
func TestSetVendorOperatorReadyCondition_NoClusterPolicy(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "np-ready-nocp"}}
	r := &NPUClusterPolicyReconciler{Recorder: record.NewFakeRecorder(10), APIReader: fake.NewClientBuilder().Build()}

	r.setVendorOperatorReadyCondition(context.Background(), p, "nvidia", true)

	cond := meta.FindStatusCondition(p.Status.Conditions, "NvidiaOperatorReady")
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != reasonNoClusterPolicy {
		t.Fatalf("조건이 Unknown/NoClusterPolicy 여야 한다: %+v", cond)
	}
}

func TestSetVendorOperatorReadyCondition_StatusProbeUnavailable(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "np-ready-blocked"}}
	r := &NPUClusterPolicyReconciler{Recorder: record.NewFakeRecorder(10), APIReader: forbiddenListReader{}}

	r.setVendorOperatorReadyCondition(context.Background(), p, "nvidia", true)

	cond := meta.FindStatusCondition(p.Status.Conditions, "NvidiaOperatorReady")
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != reasonStatusProbeUnavailable {
		t.Fatalf("조건이 Unknown/StatusProbeUnavailable 이어야 한다: %+v", cond)
	}
}

// TestDetectVendorOperator_SetsReadyCondition 은 vendoroperator_detect_test.go 의 재확인
// fixture(newVendorRecheckFixture)를 그대로 쓴다 — nvidia 시그니처를 서빙하는 mapper + CRD 가
// 있는 reader 에 ClusterPolicy(state=notReady)를 더하면 NvidiaOperatorReady=False(reason
// NotReady, message 에 state 값)가 세워져야 한다.
func TestDetectVendorOperator_SetsReadyCondition(t *testing.T) {
	p, served, crdName := newVendorRecheckFixture(t, metav1.ConditionFalse)
	s := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(s)
	reader := fake.NewClientBuilder().WithScheme(s).
		WithObjects(&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: crdName}}).
		WithRuntimeObjects(clusterPolicyObj("cluster-policy", "notReady")).Build()
	rec := record.NewFakeRecorder(10)
	r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served, APIReader: reader}

	r.detectNvidiaGPUOperator(context.Background(), p)

	cond := meta.FindStatusCondition(p.Status.Conditions, "NvidiaOperatorReady")
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "NotReady" || !strings.Contains(cond.Message, "notReady") {
		t.Fatalf("Ready 조건이 틀리다: %+v", cond)
	}
}
