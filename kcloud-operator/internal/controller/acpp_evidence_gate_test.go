// ============================================================
// acpp_evidence_gate_test.go: 근거 불일치 시 commit 거부 envtest
// 상세: 광고가 실제로 수렴한 노드(프로덕션이 만들 수 있는 상태)에서 Ready + 근거 기록을 확인하고,
// 장치 관측을 spec 과 어긋나게 주입하면 Ready 로 승격되지 않음을 확인한다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/verification"
)

// evidenceReconciler 는 근거 게이트를 켠 reconciler 다(기존 스펙은 nil 이라 영향 없음).
func evidenceReconciler() *AcceleratorPartitionPolicyReconciler {
	r := nvidiaReconciler()
	r.Verification = &verification.Verifier{Client: k8sClient}
	return r
}

// advertiseMig 는 device-plugin 이 MIG 조각을 광고한 뒤의 노드 상태를 만든다.
//
// 그렇다. 다른 layout 을 쓰는 스펙이 생기면 자연히 값이 달라진다.
//
//nolint:unparam // count 는 오늘 모든 호출에서 4다 — 이 스펙들이 공유하는 layout(1g.6gb×4) 값이라
func advertiseMig(node string, count int64) {
	var n corev1.Node
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
	n.Status.Allocatable[corev1.ResourceName("nvidia.com/mig-1g.6gb")] = *resource.NewQuantity(count, resource.DecimalSI)
	Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
}

func evidenceFor(node string) *npuv1alpha1.AcceleratorEvidence {
	var ev npuv1alpha1.AcceleratorEvidence
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ev); err != nil {
		return nil
	}
	return &ev
}

var _ = Describe("ACPP evidence commit gate", func() {
	const sel = "kcloud.ai/nv-evidence"

	It("records evidence and reaches Ready when every observation agrees", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{sel: "true"}
		geom := nvidia.GeometrySummary("1g.6gb", 4)
		seedNvidiaNode("nv-evidence-node", labels, true, a30Device(geom, "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-evidence-node") })
		advertiseMig("nv-evidence-node", 4)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorEvidence{
				ObjectMeta: metav1.ObjectMeta{Name: "nv-evidence-node"},
			}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		acpp := mkNvidiaACPP("nv-evidence-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		// 이 node/NDR 는 처음부터 목표 geometry+광고를 갖고 있지만 이 ACPP 는 아직 그 GI 를 소유했다는
		// 저널이 없다 — 그 상태로 첫 reconcile 을 돌리면 routeNvidiaNoDiff 가 "geometry 는 맞지만 이
		// ACPP 가 만든 게 아니다" 로 읽어 ExistingMigConfiguration(Failed, terminal)로 거절한다(의도된
		// 안전장치이지 이 스펙의 표적이 아니다). 그래서 이미 소유·적용했다는 저널을 먼저 심어 managed
		// no-diff 재검증 경로로 들어간다 — 근거 게이트가 실제로 지켜야 할 상태는 이미 적용된 것으로
		// 기록된 파티션이 지금도 여전히 맞는지 재확인하는 이 pass 다.
		acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName: "nv-evidence-node", GPUPCIs: []string{"0000:41:00.0"}, OwnerUID: string(acpp.UID),
			BaselineGPUCount: 1, ExpectedMigCount: 4, ExpectedFullGPUCount: 1,
			Profile: "1g.6gb", Count: 4, MigPhase: npuv1alpha1.MigPhaseReady,
		}}
		Expect(k8sClient.Status().Update(ctx, acpp)).To(Succeed())

		r := evidenceReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-evidence-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-evidence-acpp"}, &got)
			return got.Status.Phase
		}, "20s", "300ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		ev := evidenceFor("nv-evidence-node")
		Expect(ev).NotTo(BeNil())
		Expect(ev.Status.Level).To(Equal(npuv1alpha1.EvidenceLevelFunctionallyVerified))
		Expect(ev.Status.AdvertisedResources).To(HaveKeyWithValue("nvidia.com/mig-1g.6gb", int32(4)))
		Expect(ev.Status.Fingerprint.Generation).To(BeNumerically(">", 0))
		Expect(ev.Status.SourcePolicy).To(Equal("nv-evidence-acpp"))
	})

	It("refuses to reach Ready when the device disagrees with the spec", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{sel + "-bad": "true"}
		// 장치 geometry 는 spec 과 어긋나게(2g.12gb x2 관측, 1g.6gb x4 요구) 시드한다. 이 시드로
		// 바로 reconcile 하면 이 게이트보다 먼저 nvidiaHWBlock 의 MigUnsafe 에 걸려 WaitingForDrain
		// 에 멈춰, 게이트 없이도 스펙이 통과해 버린다(뮤테이션으로 확인). positive 스펙과 같은
		// 저널(이미 소유·Ready, geometry 일치)을 심어 MigUnsafe 를 피하고 **광고만 비워 두면**,
		// 광고만 보는 fakeVerifier/기존 verifySucceeded 는 그래도 통과시킨다 — 실제 allocatable 을
		// 읽는 새 게이트만이 이 불일치를 잡는다.
		seedNvidiaNode("nv-evidence-bad", labels, true,
			a30Device(nvidia.GeometrySummary("1g.6gb", 4), "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-evidence-bad") })
		// advertiseMig 를 부르지 않는다 — device-plugin 이 아직 mig-1g.6gb 를 광고하지 않은 상태.
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorEvidence{
				ObjectMeta: metav1.ObjectMeta{Name: "nv-evidence-bad"},
			}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		acpp := mkNvidiaACPP("nv-evidence-bad-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName: "nv-evidence-bad", GPUPCIs: []string{"0000:41:00.0"}, OwnerUID: string(acpp.UID),
			BaselineGPUCount: 1, ExpectedMigCount: 4, ExpectedFullGPUCount: 1,
			Profile: "1g.6gb", Count: 4, MigPhase: npuv1alpha1.MigPhaseReady,
		}}
		Expect(k8sClient.Status().Update(ctx, acpp)).To(Succeed())

		r := evidenceReconciler()
		for i := 0; i < 8; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-evidence-bad-acpp"))
		}
		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-evidence-bad-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).NotTo(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondVerified)).To(Equal(npuv1alpha1.ReasonEvidenceDisagreement))

		if ev := evidenceFor("nv-evidence-bad"); ev != nil {
			Expect(ev.Status.Level).To(BeEmpty())
			Expect(ev.Status.AdvertisedResources).To(BeEmpty())
		}
	})
})

// flatDevicePluginDS 는 sharing-only 경로가 대상화하는 flat nvidia device-plugin DS 다
// (다른 sharing 스펙의 시드와 동일 — 새 fixture 를 만들지 않는다).
func flatDevicePluginDS() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameFlat, Namespace: "kube-system"},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nvidia-device-plugin", Image: "x"}}},
			},
		},
	}
}

// setGPUAllocatable 은 device-plugin 이 (공유 적용 후) 노드에 광고하는 nvidia.com/gpu 수량을 흉내낸다.
func setGPUAllocatable(node string, n int64) {
	var got corev1.Node
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
	got.Status.Allocatable[corev1.ResourceName(nvidiaGPUResource)] = *resource.NewQuantity(n, resource.DecimalSI)
	Expect(k8sClient.Status().Update(ctx, &got)).To(Succeed())
}

// timeSlicedACPP 는 layout 없는(순수 공유) 정책이다 — runSharingOnly 경로 전용.
func timeSlicedACPP(name string, sel map[string]string, replicas int32) *npuv1alpha1.AcceleratorPartitionPolicy {
	return &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			NodeSelector:   sel,
			Vendor:         "nvidia",
			DeletionPolicy: "Retain",
			Sharing: &npuv1alpha1.SharingSpec{
				Mode:        npuv1alpha1.SharingModeTimeSliced,
				TimeSlicing: &npuv1alpha1.TimeSlicingSpec{Replicas: replicas},
			},
		},
	}
}

// runSharingOnly(layout 없는 순수 공유 정책)도 근거 게이트를 피해 가는 구멍이 없어야 한다
// (ec.Verified 가 세워져야 한다). 이 두 스펙은 그 구멍이 막혀 있음을 양방향으로 고정한다.
var _ = Describe("ACPP sharing-only evidence gate", func() {
	const sel = "kcloud.ai/nv-sharing-evidence"

	It("records evidence and reaches Ready once the shared allocatable actually converges", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{sel: "true"}
		// A2 처럼 MIG 불가 장치는 nvidia backend 의 target 목록(MigDevice)에 아예 안 잡혀
		// ec.ObservedGeometry 가 항상 비어 DeviceObservation 체크가 무조건 실패한다 — 장치 관측
		// 축과 무관한 픽스처 문제다. MIG 가능(A30, mode Enabled) 장치를 써야 공유 전용 정책이라도
		// 관측이 존재한다.
		seedNvidiaNode("nv-sharing-node", labels, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-sharing-node") })
		dp := flatDevicePluginDS()
		Expect(k8sClient.Create(ctx, dp)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dp) })
		DeferCleanup(func() {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: nvidia.SharingConfigMapNameFlat, Namespace: "kube-system"}}
			_ = k8sClient.Delete(ctx, cm)
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorEvidence{
				ObjectMeta: metav1.ObjectMeta{Name: "nv-sharing-node"},
			}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		Expect(k8sClient.Create(ctx, timeSlicedACPP("nv-sharing-acpp", labels, 4))).To(Succeed())
		r := evidenceReconciler()

		// pass 1: layout 이 없으므로 runSharingOnly 로 들어간다. fakeVerifier 는 실제 allocatable 을
		// 보지 않고 항상 수렴을 보고하므로, 게이트가 없었다면 device-plugin(이 envtest 엔 없다)이
		// 아직 4배 광고를 하기도 전에 여기서 바로 Ready 가 됐을 것이다.
		_, err := r.Reconcile(ctx, reconcileReq("nv-sharing-acpp"))
		Expect(err).NotTo(HaveOccurred())
		var afterFirst npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-acpp"}, &afterFirst)).To(Succeed())
		Expect(afterFirst.Status.Phase).NotTo(Equal(npuv1alpha1.ACPPPhaseReady),
			"device-plugin 이 아직 4배 광고를 하기 전인데 게이트 없이 Ready 로 승격됐다")

		// device-plugin 이 이제 baseline(1) × replicas(4) = 4 를 광고한다.
		setGPUAllocatable("nv-sharing-node", 4)

		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-sharing-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		ev := evidenceFor("nv-sharing-node")
		Expect(ev).NotTo(BeNil())
		Expect(ev.Status.Level).To(Equal(npuv1alpha1.EvidenceLevelFunctionallyVerified))
		Expect(ev.Status.AdvertisedResources).To(HaveKeyWithValue(nvidiaGPUResource, int32(4)))
	})

	It("refuses to reach Ready when the shared allocatable never converges", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{sel + "-bad": "true"}
		seedNvidiaNode("nv-sharing-bad-node", labels, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-sharing-bad-node") })
		dp := flatDevicePluginDS()
		Expect(k8sClient.Create(ctx, dp)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dp) })
		DeferCleanup(func() {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: nvidia.SharingConfigMapNameFlat, Namespace: "kube-system"}}
			_ = k8sClient.Delete(ctx, cm)
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorEvidence{
				ObjectMeta: metav1.ObjectMeta{Name: "nv-sharing-bad-node"},
			}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		Expect(k8sClient.Create(ctx, timeSlicedACPP("nv-sharing-bad-acpp", labels, 4))).To(Succeed())
		r := evidenceReconciler()
		// device-plugin 은 끝까지 4배 광고를 하지 않는다(라이브라면 DP 가 죽었거나 설정을 못 읽은
		// 상태). fakeVerifier 는 그래도 항상 수렴을 보고하므로, 게이트가 없으면 이 정책은 광고가
		// 1(baseline)에 머문 채로도 Ready 로 승격됐을 것이다.
		for i := 0; i < 5; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-sharing-bad-acpp"))
		}
		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-bad-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).NotTo(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondVerified)).To(Equal(npuv1alpha1.ReasonEvidenceDisagreement))

		ev := evidenceFor("nv-sharing-bad-node")
		Expect(ev).NotTo(BeNil())
		Expect(ev.Status.Level).To(BeEmpty())
		// 사유가 반드시 광고 축이어야 한다 — 장치 관측이나 다른 축이 우연히 대신 막았다면 이
		// 스펙은 원래 잡으려던 것(공유 전용 정책의 광고 미수렴)을 증명하지 못한다.
		Expect(ev.Status.Reason).To(ContainSubstring("Advertisement"))
	})
})
