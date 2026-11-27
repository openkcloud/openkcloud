// ============================================================
// npuclusterpolicy_rebellions_envtest_test.go: Rebellions device plugin 판 필드 CRD 왕복과 조건 연결 envtest
// 상세: devicePluginGeneration 이 구조적 스키마에 있어 저장 뒤 남는지, 허용값 밖은 거절되는지 본다.
//       단위 시험은 in-memory 구조체만 보므로 API 서버의 pruning·enum 검증은 envtest 로만 확인된다.
//       Reconcile 이 RebellionsDevicePluginReady 조건을 세팅·제거하는지도 본다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

var _ = Describe("NPUClusterPolicy spec.rebellions.devicePluginGeneration", func() {
	It("허용값은 저장 뒤에도 남는다", func() {
		p := basePolicy("rbln-gen-roundtrip")
		p.Spec.Rebellions = npuv1alpha1.RebellionsSpec{
			DevicePluginImage: "docker.io/rebellions/k8s-device-plugin:v0.4.3", DevicePluginGeneration: "v0.4"}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		DeferCleanup(func() { deleteNCPAndWait(p.Name, p.Namespace) })

		got := &npuv1alpha1.NPUClusterPolicy{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: p.Name, Namespace: p.Namespace}, got)).To(Succeed())
		Expect(got.Spec.Rebellions.DevicePluginGeneration).To(Equal("v0.4"),
			"API 서버가 devicePluginGeneration 을 잘라냈다 — CRD 스키마 누락")
	})

	It("허용값이 아니면 거절한다", func() {
		p := basePolicy("rbln-gen-invalid")
		p.Spec.Rebellions = npuv1alpha1.RebellionsSpec{DevicePluginImage: "x", DevicePluginGeneration: "v0.9"}
		err := k8sClient.Create(ctx, p)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("devicePluginGeneration"))
	})
})

var _ = Describe("NPUClusterPolicy RebellionsDevicePluginReady 조건 연결", func() {
	reconcileOnce := func(p *npuv1alpha1.NPUClusterPolicy) *npuv1alpha1.NPUClusterPolicy {
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		DeferCleanup(func() {
			deleteNCPAndWait(p.Name, p.Namespace)
			// 소유 표시는 annotation 이라 GC 가 지우지 않는다. 같은 이름을 만드는 다른 시험과 겹치지 않게 지운다.
			for _, obj := range []client.Object{
				&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: rbllnsDaemonSetName, Namespace: "kube-system"}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: rbllnsConfigMapNameDefault, Namespace: "kube-system"}},
			} {
				_ = client.IgnoreNotFound(k8sClient.Delete(ctx, obj))
			}
		})
		r := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(100)}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: p.Name, Namespace: p.Namespace}})
		Expect(err).NotTo(HaveOccurred())
		got := &npuv1alpha1.NPUClusterPolicy{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: p.Name, Namespace: p.Namespace}, got)).To(Succeed())
		return got
	}

	It("Rebellions 를 켜면 판 메모와 함께 조건을 남긴다", func() {
		prev, had := os.LookupEnv(HostExecImageEnv)
		Expect(os.Setenv(HostExecImageEnv, "registry.example.com/kcloud-host-exec:test")).To(Succeed())
		DeferCleanup(func() {
			if had {
				_ = os.Setenv(HostExecImageEnv, prev)
			} else {
				_ = os.Unsetenv(HostExecImageEnv)
			}
		})
		p := basePolicy("rbln-cond-on")
		p.Spec.Rebellions = npuv1alpha1.RebellionsSpec{Enabled: true,
			DevicePluginImage: "docker.io/rebellions/k8s-device-plugin:v0.3.6", Namespace: "kube-system"}
		got := reconcileOnce(p)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, npuv1alpha1.ConditionRebellionsDevicePluginReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(Equal("판: v0.3 형식(태그 v0.3.6)"))
	})

	It("Rebellions 를 끄면 조건이 없다", func() {
		got := reconcileOnce(basePolicy("rbln-cond-off"))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions,
			npuv1alpha1.ConditionRebellionsDevicePluginReady)).To(BeNil())
	})
})
