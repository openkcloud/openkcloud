// ============================================================
// driverinstallpolicy_cel_test.go: DriverSpec 커널 이미지 필드의 CEL 검증을 envtest 로 고정한다.
// 상세: kernelImages·kernelImageBuildRevision 은 mode=job 에서만 유효하다(CRD
// x-kubernetes-validations) — mode=daemonset 로 주면 생성이 거절된다. 빌드 개정 값은 태그 접미사 모양(…-vN)이어야 한다.
// trackOnly 의 image 생략은 trackOnly=true 일 때만 통과하고, 그 외에는 image 누락이 거절된다.
// 생성일: 2026-09-22 | 수정일: 2026-10-27
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// dipWithKernelImages 는 kernelImages 를 담은 최소 DIP 다. mode 만 케이스별로 다르다.
func dipWithKernelImages(name, mode string) *npuv1alpha1.DriverInstallPolicy {
	return &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "tenstorrent",
			Driver: npuv1alpha1.DriverSpec{
				Image: "reg/kcloud/tenstorrent-driver-ds:2.11.0-v1",
				Mode:  mode,
				KernelImages: map[string]npuv1alpha1.KernelImageRef{
					"5.15.0-190-generic": "reg/kcloud/tenstorrent-driver-ds:2.11.0-k5.15.0-190-generic-v1",
				},
			},
		},
	}
}

var _ = Describe("DriverInstallPolicy kernelImages CEL 검증", func() {
	It("mode=daemonset 로 kernelImages 를 주면 생성이 거절된다", func() {
		err := k8sClient.Create(ctx, dipWithKernelImages("cel-ki-daemonset", "daemonset"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("driver.kernelImages requires driver.mode=job"))
	})

	It("mode=job 이면 kernelImages 를 담은 DIP 생성이 통과한다", func() {
		dip := dipWithKernelImages("cel-ki-job", "job")
		Expect(k8sClient.Create(ctx, dip)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dip) })
	})
})

// dipWithBuildRevision 은 kernelImageBuildRevision 만 담은 최소 DIP 다(맵은 비운다 —
// 조립 경로를 켜는 것이 이 값 하나임을 CEL 수준에서도 분리해 둔다).
func dipWithBuildRevision(name, mode, revision string) *npuv1alpha1.DriverInstallPolicy {
	return &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "tenstorrent",
			Driver: npuv1alpha1.DriverSpec{
				Image:                    "reg/kcloud/tenstorrent-driver-ds:2.11.0-v1",
				Mode:                     mode,
				Version:                  "2.11.0",
				KernelImageBuildRevision: revision,
			},
		},
	}
}

var _ = Describe("DriverInstallPolicy kernelImageBuildRevision CEL 검증", func() {
	It("mode=daemonset 로 빌드 개정을 주면 생성이 거절된다", func() {
		err := k8sClient.Create(ctx, dipWithBuildRevision("cel-kibr-daemonset", "daemonset", "v4"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("driver.kernelImageBuildRevision requires driver.mode=job"))
	})

	It("mode=job 이면 빌드 개정을 담은 DIP 생성이 통과한다", func() {
		dip := dipWithBuildRevision("cel-kibr-job", "job", "v4")
		Expect(k8sClient.Create(ctx, dip)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dip) })
	})

	It("mode=daemonset 에 빈 빌드 개정은 거절되지 않는다", func() {
		dip := dipWithBuildRevision("cel-kibr-empty", "daemonset", "")
		Expect(k8sClient.Create(ctx, dip)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dip) })
	})

	It("태그 접미사 모양이 아닌 빌드 개정은 거절된다", func() {
		err := k8sClient.Create(ctx, dipWithBuildRevision("cel-kibr-shape", "job", "4"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kernelImageBuildRevision"))
	})
})

var _ = Describe("DriverInstallPolicy 조립 값 CEL 검증", func() {
	It("빌드 개정만 주고 version 을 비우면 생성이 거절된다", func() {
		dip := dipWithBuildRevision("cel-kibr-nover", "job", "v4")
		dip.Spec.Driver.Version = ""
		err := k8sClient.Create(ctx, dip)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requires a non-empty driver.version"))
	})

	It("mode=daemonset 에 variant 를 주면 생성이 거절된다", func() {
		dip := dipWithBuildRevision("cel-kiv-daemonset", "daemonset", "")
		dip.Spec.Driver.KernelImageVariant = "rngd"
		err := k8sClient.Create(ctx, dip)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("driver.kernelImageVariant requires driver.mode=job"))
	})

	It("mode=job 이면 variant 를 담은 DIP 생성이 통과한다", func() {
		dip := dipWithBuildRevision("cel-kiv-job", "job", "v4")
		dip.Spec.Driver.Version = "2026.3.1"
		dip.Spec.Driver.KernelImageVariant = "rngd"
		Expect(k8sClient.Create(ctx, dip)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dip) })
	})

	// 숫자로 시작하는 variant 는 조립을 조용히 망가뜨린다 — 태그 앞머리에서 버전 토큰을 떼는
	// 정규식이 variant 첫 자까지 버전으로 읽어(version "2026.3.1" + variant "2x" →
	// "2026.3.1-2x-k…" 의 버전 토큰이 "2026.3.1-2") spec.driver.version 과 달라지고,
	// 조립한 이미지가 거절돼 DKMS 이미지로 떨어진다. 켠 기능이 안 먹는 상태라 CRD 가 막는다.
	It("숫자로 시작하는 variant 는 생성이 거절된다", func() {
		for _, variant := range []string{"2x", "4pe"} {
			dip := dipWithBuildRevision("cel-kiv-digit-"+variant, "job", "v4")
			dip.Spec.Driver.Version = "2026.3.1"
			dip.Spec.Driver.KernelImageVariant = variant
			err := k8sClient.Create(ctx, dip)
			Expect(err).To(HaveOccurred(), "variant %q 가 통과했다", variant)
			Expect(err.Error()).To(ContainSubstring("kernelImageVariant"))
		}
	})
})

// dipTrackOnlyNoImage 는 설치기 이미지가 없는 최소 DIP 다. trackOnly 만 케이스별로 다르다.
func dipTrackOnlyNoImage(name string, trackOnly bool) *npuv1alpha1.DriverInstallPolicy {
	return &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "rebellions",
			Model:  "atom",
			Driver: npuv1alpha1.DriverSpec{Mode: "job", TrackOnly: trackOnly, VersionSource: "Host"},
		},
	}
}

var _ = Describe("DriverInstallPolicy trackOnly 의 image 생략 CEL 검증", func() {
	It("trackOnly=true 면 image 없이 생성이 통과한다", func() {
		dip := dipTrackOnlyNoImage("cel-trackonly-noimage", true)
		Expect(k8sClient.Create(ctx, dip)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dip) })
	})

	It("trackOnly=false 에 image 가 없으면 생성이 거절된다", func() {
		err := k8sClient.Create(ctx, dipTrackOnlyNoImage("cel-install-noimage", false))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("driver.image is required unless driver.trackOnly=true"))
	})
})
