// ============================================================
// npuclusterpolicy_delegated_test.go: managementMode=delegated 의 컨트롤러 동작 고정
// 상세: delegated 는 kcloud 가 배포하지 않고 벤더 operator 에 위임한다. device-plugin·
// dcgm-exporter·DRA 드라이버를 만들지 않고, 위임 대상을 감지하지 못하면(Absent) 경고하며,
// 판정 수단이 없으면(Unknown) 경고하지 않는 것을 고정한다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

var _ = Describe("NPUClusterPolicy managementMode=delegated", func() {
	ctx := context.Background()

	// reconcileDelegated 는 정책을 만들고 한 번 조정한다. 감지 결과를 결정론적으로 고정하려
	// 빈 RESTMapper 를 주입한다 — 시그니처 CRD 가 하나도 서빙되지 않으므로 시그니처가 있는
	// 벤더는 Absent, 없는 벤더는 Unknown 이 된다. 조정 중 쌓인 이벤트를 함께 돌려준다.
	reconcileDelegated := func(policy *npuv1alpha1.NPUClusterPolicy) *record.FakeRecorder {
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		DeferCleanup(func() { deleteNCPAndWait(policy.Name, policy.Namespace) })
		rec := record.NewFakeRecorder(100)
		r := &NPUClusterPolicyReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: rec,
			mapper:   apimeta.NewDefaultRESTMapper(nil),
		}
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace},
		})
		Expect(err).NotTo(HaveOccurred())
		return rec
	}

	It("does not deploy device-plugin, dcgm-exporter, or the DRA driver even when enabled", func() {
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: draNamespace}})

		// enabled=true 인데도 배포하지 않아야 한다 — managed 였다면 세 operand 를 모두 만든다.
		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-delegated-nvidia", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:           true,
					DevicePluginImage: "img:v1",
					ManagementSpec:    npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
					DcgmExporter:      &npuv1alpha1.DcgmExporterSpec{Enabled: true},
					DRA:               &npuv1alpha1.DRASpec{Enabled: true},
				},
			},
		}
		reconcileDelegated(policy)

		for _, name := range []string{nvidia.DevicePluginNameMixed, nvidia.DevicePluginNameFlat} {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "kube-system"}, &appsv1.DaemonSet{})
			Expect(errors.IsNotFound(err)).To(BeTrue(), "delegated must not create %s", name)
		}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: dcgmExporterDSName, Namespace: "kube-system"}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "delegated must not create dcgm-exporter")
		err = k8sClient.Get(ctx, types.NamespacedName{Name: draDSNameNvidia, Namespace: draNamespace}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "delegated must not create the nvidia DRA driver")

		var got npuv1alpha1.NPUClusterPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, &got)).To(Succeed())
		cond := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("leaves a pre-existing DaemonSet of the same name (no owner annotation) untouched", func() {
		existing := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "vendor-operator"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "vendor-operator"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "vendor:v1"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"}})
		})

		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-delegated-preexisting", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:           true,
					DevicePluginImage: "img:v1",
					ManagementSpec:    npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				},
			},
		}
		reconcileDelegated(policy)

		var survived appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"}, &survived)).To(Succeed())
		Expect(survived.Spec.Template.Spec.Containers[0].Image).To(Equal("vendor:v1"))
	})

	It("warns when the vendor operator is not detected (Absent)", func() {
		// nvidia 시그니처(clusterpolicies.nvidia.com)는 빈 mapper 에 없으므로 Absent 다 —
		// delegated 로 선언했는데 위임 대상이 없다는 뜻이라 경고해야 한다.
		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-delegated-absent", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:           true,
					DevicePluginImage: "img:v1",
					ManagementSpec:    npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				},
			},
		}
		rec := reconcileDelegated(policy)
		Expect(hasRecorderEventReason(rec, "DelegatedOperatorAbsent")).To(BeTrue(),
			"delegated 인데 operator 를 감지하지 못하면 경고해야 한다")
	})

	It("does not warn when detection is Unknown (vendor has no signature)", func() {
		// furiosa 는 감지 시그니처가 없어 Unknown 이다. Unknown 에 경고하면 시그니처 없는
		// 벤더는 delegated 를 쓸 때마다 오경고한다.
		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-delegated-unknown", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
				Furiosa: npuv1alpha1.FuriosaSpec{
					Enabled:           true,
					DevicePluginImage: "img:v1",
					ManagementSpec:    npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				},
			},
		}
		rec := reconcileDelegated(policy)
		Expect(hasRecorderEventReason(rec, "DelegatedOperatorAbsent")).To(BeFalse(),
			"Unknown 에는 경고하지 않아야 한다")
	})
})
