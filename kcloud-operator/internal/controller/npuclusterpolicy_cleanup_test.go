// ============================================================
// npuclusterpolicy_cleanup_test.go: envtest 스펙용 NPUClusterPolicy 삭제 대기 헬퍼
// 상세: 이 스위트는 매니저를 띄우지 않아 Reconcile 이 붙인 finalizer(npu.ai/cleanup)를
//       아무도 지우지 않는다. 맨 Delete 는 객체를 Terminating 으로 영영 남긴다 —
//       deleteACPPAndWait(acceleratorpartitionpolicy_controller_test.go)와 같은 규율로
//       테스트가 finalizer 를 직접 떼고 NotFound 까지 기다린다.
// 생성일: 2026-09-21 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"fmt"

	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// deleteNCPAndWait 은 NPUClusterPolicy 하나를 finalizer 까지 제거하며 지우고 실제로
// 없어질 때까지 기다린다. Reconcile 을 호출한 스펙의 cleanup 은 맨 Delete 대신 이것을 쓴다.
func deleteNCPAndWait(name, namespace string) {
	key := types.NamespacedName{Name: name, Namespace: namespace}
	_ = k8sClient.Delete(ctx, &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	})
	Eventually(func() bool {
		var got npuv1alpha1.NPUClusterPolicy
		err := k8sClient.Get(ctx, key, &got)
		if apierrors.IsNotFound(err) {
			return true
		}
		if err != nil {
			return false
		}
		if controllerutil.RemoveFinalizer(&got, finalizerName) {
			_ = k8sClient.Update(ctx, &got)
		}
		return false
	}, "5s", "200ms").Should(BeTrue(),
		fmt.Sprintf("NPUClusterPolicy %s/%s 가 finalizer 정리 후에도 삭제되지 않았다", namespace, name))
}

// terminatingNCPs 는 DeletionTimestamp 가 찍힌 채 남은 NPUClusterPolicy 의 "ns/name" 목록이다.
func terminatingNCPs() []string {
	var list npuv1alpha1.NPUClusterPolicyList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())
	var out []string
	for i := range list.Items {
		if !list.Items[i].DeletionTimestamp.IsZero() {
			out = append(out, list.Items[i].Namespace+"/"+list.Items[i].Name)
		}
	}
	return out
}
