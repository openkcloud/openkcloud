// ============================================================
// nodedevicereport_notapplicable_test.go: ValidationStep.NotApplicable CRD 왕복 시험
// 상세: detector 가 JSON 으로 쓰는 notApplicable 필드가 구조적 스키마에 없으면 API 서버가
// 조용히 잘라낸다(pruning). 타입에 필드가 있다는 것만으로는 라이브에서 살아남는지 보장이
// 안 되므로, envtest(config/crd/bases 로 기동)에 실제로 쓰고 다시 읽어 값이 유지되는지 본다.
// 생성일: 2026-08-12 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

var _ = Describe("NodeDeviceReport ValidationStep.NotApplicable", func() {
	It("CRD 스키마에서 잘리지 않고 그대로 왕복한다", func() {
		name := "notapplicable-roundtrip"
		ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: npuv1alpha1.NodeDeviceReportSpec{NodeName: name}}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())

		ndr.Status.Validation = &npuv1alpha1.ValidationStatus{
			Passed: true,
			Steps: []npuv1alpha1.ValidationStep{
				{Name: "devicePlugin", Passed: true, NotApplicable: true, Message: "노드가 배포 대상에서 제외됨(control-plane)"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())

		got := &npuv1alpha1.NodeDeviceReport{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		Expect(got.Status.Validation).NotTo(BeNil())
		Expect(got.Status.Validation.Steps).To(HaveLen(1))
		Expect(got.Status.Validation.Steps[0].NotApplicable).To(BeTrue(),
			"API 서버가 notApplicable 을 잘라냈다 — CRD 스키마 누락")
	})
})
