// ============================================================
// acceleratoroperation_crd_test.go: AcceleratorOperation CRD envtest 왕복
// 상세: status 서브리소스로 저널·스냅샷·epoch 이 보존되는지, phase enum 이 임의 값을 거부하는지.
// 생성일: 2026-08-01
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

var _ = Describe("AcceleratorOperation CRD", func() {
	// 증명: 저널·스냅샷·epoch 이 status 서브리소스를 왕복해도 보존된다.
	// 깨는 뮤테이션: Journal 필드에서 json 태그를 지우면 실패한다.
	It("persists journal, snapshot and epoch", func() {
		op := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "aop-roundtrip"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: "PartitionReconfigure", NodeName: "aop-node", Vendor: "nvidia",
				TransactionID: "tx-1",
				Owner:         npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "p1", UID: "u1", Generation: 3},
				ResourceKeys:  []string{"device/0000:41:00.0/partition", "node/aop-node/cordon"},
			},
		}
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		op.Status = npuv1alpha1.AcceleratorOperationStatus{
			Phase: npuv1alpha1.OpPhaseApplying,
			Epoch: 2,
			Journal: []npuv1alpha1.OperationJournalEntry{
				{Step: "SnapshotTaken", Epoch: 2, At: metav1.Now()},
				{Step: "ApplyStarted", Epoch: 2, At: metav1.Now(), Detail: "1g.6gb x4"},
			},
			Snapshot: &npuv1alpha1.OperationSnapshot{
				Allocatable: map[string]int32{"nvidia.com/gpu": 1},
				Geometry:    map[string]string{"0000:41:00.0": ""},
				BootID:      "boot-1",
			},
		}
		Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())

		var got npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-roundtrip"}, &got)).To(Succeed())
		Expect(got.Status.Epoch).To(BeNumerically("==", 2))
		Expect(got.Status.Journal).To(HaveLen(2))
		Expect(got.Status.Journal[1].Step).To(Equal("ApplyStarted"))
		Expect(got.Status.Snapshot.BootID).To(Equal("boot-1"))
		Expect(got.Spec.ResourceKeys).To(ContainElement("device/0000:41:00.0/partition"))
	})

	// 증명: 상태머신 밖의 phase 는 apiserver 가 거부한다.
	// 깨는 뮤테이션: Phase 필드의 kubebuilder enum 마커를 지우면 실패한다.
	It("rejects a phase outside the state machine", func() {
		op := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "aop-badphase"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: "Revalidate", NodeName: "aop-node", TransactionID: "tx-2",
				Owner: npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "p1"},
			},
		}
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		op.Status.Phase = "AlmostDone"
		Expect(k8sClient.Status().Update(ctx, op)).NotTo(Succeed())
	})

	// 증명: 빈 transactionID 는 apiserver 가 거부한다. 빈 값이 들어오면 DeviceReset 의 Job
	// 이름이 (노드, 벤더)로 붕괴해 앞선 작업의 성공 Job 을 자기 성공으로 오인한다.
	// 깨는 뮤테이션: TransactionID 의 MinLength 마커를 지우면 실패한다.
	It("rejects an empty transactionID", func() {
		op := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "aop-emptytx"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: "DeviceReset", NodeName: "aop-node", Vendor: "nvidia", TransactionID: "",
				Owner: npuv1alpha1.OperationOwner{Kind: "Administrator", Name: "admin"},
			},
		}
		Expect(k8sClient.Create(ctx, op)).NotTo(Succeed())
	})
})
