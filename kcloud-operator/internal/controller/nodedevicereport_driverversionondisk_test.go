// ============================================================
// nodedevicereport_driverversionondisk_test.go: DeviceEntry.DriverVersionOnDisk CRD 왕복 시험
// 상세: detector 가 채우는 driverVersionOnDisk(디스크에 설치된 드라이버 버전, 드리프트 규명용
// 필드)가 구조적 스키마에 없으면 API 서버가 조용히 잘라낸다(pruning). 단위시험은 in-memory
// map 만 봐서 이 형태를 못 잡으므로, envtest(config/crd/bases 로 기동)에 실제로 쓰고 다시
// 읽어 값이 유지되는지 본다.
// 생성일: 2026-09-18 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

var _ = Describe("NodeDeviceReport DeviceEntry.DriverVersionOnDisk", func() {
	It("CRD 스키마에서 잘리지 않고 그대로 왕복한다", func() {
		name := "driverversionondisk-roundtrip"
		ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: npuv1alpha1.NodeDeviceReportSpec{NodeName: name}}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())

		ndr.Status.Devices = []npuv1alpha1.DeviceEntry{
			{Vendor: "nvidia", DriverVersion: "595.91.07", DriverVersionOnDisk: "580.178.04"},
		}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())

		got := &npuv1alpha1.NodeDeviceReport{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		Expect(got.Status.Devices).To(HaveLen(1))
		Expect(got.Status.Devices[0].DriverVersionOnDisk).To(Equal("580.178.04"),
			"API 서버가 driverVersionOnDisk 를 잘라냈다 — CRD 스키마 누락")
	})
})
