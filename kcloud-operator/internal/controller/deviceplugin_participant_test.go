// ============================================================
// deviceplugin_participant_test.go: 재시작 작업 본체 envtest
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
)

func dpRestartOp(name, node string) *npuv1alpha1.AcceleratorOperation {
	return &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: "DevicePluginRestart", NodeName: node, TransactionID: name,
			Owner: npuv1alpha1.OperationOwner{Kind: healthOwnerKind, Name: node},
		},
	}
}

var _ = Describe("DevicePluginRestart participant", func() {
	// 증명: Apply 는 대상 노드의 device-plugin pod 만 지운다.
	// 깨는 뮤테이션: 노드 필터를 지우면 다른 노드 pod 까지 사라져 실패한다.
	It("restarts only the target node's device-plugin pod", func() {
		seedHealthDevicePluginPod("dp-target-node", true)
		seedHealthDevicePluginPod("dp-other-node", true)
		DeferCleanup(func() {
			for _, n := range []string{"dp-target-node", "dp-other-node"} {
				_ = k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
					Name: "dp-" + n, Namespace: "default"}}, client.GracePeriodSeconds(0))
			}
		})

		p := NewDevicePluginParticipant(nvidiaReconciler())
		out, err := p.Apply(ctx, dpRestartOp("dp-restart-op", "dp-target-node"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyDone))

		var gone corev1.Pod
		err = k8sClient.Get(ctx, client.ObjectKey{Name: "dp-dp-target-node", Namespace: "default"}, &gone)
		Expect(err != nil || gone.DeletionTimestamp != nil).To(BeTrue(), "대상 pod 이 그대로 남았다")

		var kept corev1.Pod
		Expect(k8sClient.Get(ctx,
			client.ObjectKey{Name: "dp-dp-other-node", Namespace: "default"}, &kept)).To(Succeed())
		Expect(kept.DeletionTimestamp).To(BeNil(), "다른 노드 pod 까지 지웠다")
	})

	// 증명: pod 이 이미 없어도 오류가 아니다(멱등).
	It("is idempotent when the pod is already gone", func() {
		p := NewDevicePluginParticipant(nvidiaReconciler())
		op := dpRestartOp("dp-restart-op-2", "dp-empty-node")
		_, err := p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
	})

	// 증명: Rollback 은 하드웨어를 건드리지 않고 보상 완료만 알린다.
	It("has a no-op rollback", func() {
		p := NewDevicePluginParticipant(nvidiaReconciler())
		out, err := p.Rollback(ctx, dpRestartOp("dp-restart-op-3", "dp-empty-node"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventCompensated))
	})

	// 증명: health 가 만든 재시작은 검증을 요구하지 않는다. 요구하면 정책 기대값이 없어 검증이
	// 구조적으로 실패하고, 작업이 항상 RolledBack 으로 끝나 세 번이면 멀쩡한 노드가 격리된다.
	It("skips verification for a health-owned restart", func() {
		p := NewDevicePluginParticipant(nvidiaReconciler())
		_, want, err := p.VerifyRequest(ctx, dpRestartOp("dp-restart-op-4", "dp-empty-node"))
		Expect(err).NotTo(HaveOccurred())
		Expect(want).To(BeFalse(), "정책 기대값이 없는데 검증을 요구하면 이 작업은 영원히 되돌려진다")
	})
})

var _ = Describe("Revalidate participant with a non-policy owner", func() {
	// 증명: health 가 만든 재검증은 검증 대상이 아니다. 정책이 없으면 장치 관측을 띄우는 주체도
	// 없어 검증이 언제나 "관측 전무" 로 실패하고, health 재검증이 **항상** RolledBack 으로 끝나
	// 세 번이면 멀쩡한 노드가 격리된다.
	It("skips verification when the owner is not an ACPP", func() {
		p := NewRevalidateParticipant(nvidiaReconciler())
		op := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "health-revalidate-op"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: "Revalidate", NodeName: "revalidate-node", Vendor: "nvidia",
				TransactionID: "health-revalidate-op",
				Owner:         npuv1alpha1.OperationOwner{Kind: healthOwnerKind, Name: "revalidate-node"},
			},
		}
		_, want, err := p.VerifyRequest(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(want).To(BeFalse(),
			"정책 기대값이 없는데 검증을 요구하면 이 작업은 영원히 되돌려진다")
	})
})
