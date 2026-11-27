// client_test.go: CLI client 의 managedFields 관리자 이름(FieldOwner) 단위 테스트
// 상세: 호출마다 FieldOwner 를 넘기지 않은 쓰기는 kubectl-npu 가, 넘긴 쓰기는 그 값이 우선한다.
// 생성일: 2026-10-13 | 수정일: 2026-10-13
package npuctl

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// patchedFieldManager 는 withCLIFieldOwner 로 감싼 client 로 Patch 한 번을 보내고, 하위 client 가
// 받은 FieldManager 를 돌려준다.
func patchedFieldManager(t *testing.T, opts ...client.PatchOption) string {
	t.Helper()
	var got string
	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(_ context.Context, _ client.WithWatch, _ client.Object, _ client.Patch, o ...client.PatchOption) error {
			po := &client.PatchOptions{}
			po.ApplyOptions(o)
			got = po.FieldManager
			return nil
		},
	}).Build()
	c := withCLIFieldOwner(base)
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "y"}}
	if err := c.Patch(context.Background(), obj, client.MergeFrom(&corev1.ConfigMap{}), opts...); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	return got
}

func TestWithCLIFieldOwner_SetsKubectlNpu(t *testing.T) {
	if got := patchedFieldManager(t); got != "kubectl-npu" {
		t.Fatalf("FieldManager=%q, kubectl-npu 기대", got)
	}
}

func TestWithCLIFieldOwner_PerCallFieldOwnerWins(t *testing.T) {
	if got := patchedFieldManager(t, client.FieldOwner("x")); got != "x" {
		t.Fatalf("FieldManager=%q, 호출별 FieldOwner x 가 우선해야 한다", got)
	}
}
