// ============================================================
// acceleratorevidence_crd_test.go: AcceleratorEvidence CRD envtest 왕복
// 상세: status 서브리소스로 level·fingerprint·advertisedResources 가 보존되는지, level enum 이
//
//	유효하지 않은 값을 거부하는지 확인한다.
//
// 생성일: 2026-07-31
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

var _ = Describe("AcceleratorEvidence CRD", func() {
	It("persists level, fingerprint and advertised resources through the status subresource", func() {
		ev := &npuv1alpha1.AcceleratorEvidence{
			ObjectMeta: metav1.ObjectMeta{Name: "aev-roundtrip-node"},
			Spec:       npuv1alpha1.AcceleratorEvidenceSpec{NodeName: "aev-roundtrip-node", Vendor: "nvidia"},
		}
		Expect(k8sClient.Create(ctx, ev)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ev, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		ev.Status = npuv1alpha1.AcceleratorEvidenceStatus{
			Level: npuv1alpha1.EvidenceLevelFunctionallyVerified,
			Fingerprint: npuv1alpha1.EnvironmentFingerprint{
				BootID: "boot-1", KernelVersion: "5.15.0-181-generic",
				DriverVersion: "535.104.05", Generation: 3,
			},
			Checks: []npuv1alpha1.EvidenceCheck{
				{Name: npuv1alpha1.EvidenceCheckDeviceObservation, Target: "0000:41:00.0", Passed: true},
			},
			AdvertisedResources: map[string]int32{"nvidia.com/mig-1g.6gb": 4},
			ObservedAt:          metav1.Now(),
			SourcePolicy:        "aev-roundtrip-acpp",
		}
		Expect(k8sClient.Status().Update(ctx, ev)).To(Succeed())

		var got npuv1alpha1.AcceleratorEvidence
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aev-roundtrip-node"}, &got)).To(Succeed())
		Expect(got.Status.Level).To(Equal(npuv1alpha1.EvidenceLevelFunctionallyVerified))
		Expect(got.Status.Fingerprint.BootID).To(Equal("boot-1"))
		Expect(got.Status.Fingerprint.Generation).To(BeNumerically("==", 3))
		Expect(got.Status.AdvertisedResources).To(HaveKeyWithValue("nvidia.com/mig-1g.6gb", int32(4)))
		Expect(got.Status.Checks).To(HaveLen(1))
	})

	It("rejects a level outside the enum", func() {
		ev := &npuv1alpha1.AcceleratorEvidence{
			ObjectMeta: metav1.ObjectMeta{Name: "aev-badlevel-node"},
			Spec:       npuv1alpha1.AcceleratorEvidenceSpec{NodeName: "aev-badlevel-node"},
		}
		Expect(k8sClient.Create(ctx, ev)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ev, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		ev.Status.Level = "MostlyFine"
		Expect(k8sClient.Status().Update(ctx, ev)).NotTo(Succeed())
	})
})
