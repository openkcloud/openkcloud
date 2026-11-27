// ============================================================
// acceleratorpartitionpolicy_controller_test.go: ACPP reconciler envtest
// 상세: happy-path(no-diff→Ready) — unified DS(dual-core)+NDR+RNGD 노드 시드.
// 생성일: 2026-07-23 | 수정일: 2026-10-13
// ============================================================
package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/partition/rngd"
)

// 테스트 전용 상수 — DS env 이름/기본 policy 값 반복 리터럴 정리(goconst).
const (
	testPartitionPolicyEnv = "RNGD_PARTITION_POLICY"
	testDualCorePolicy     = "dual-core"
)

// reconcileReq 는 name 만으로 cluster-scoped 리소스용 reconcile 요청을 만든다.
func reconcileReq(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}
}

// fakeVerifier 는 하드웨어 의존 검증을 항상 성공 처리한다(envtest seam).
type fakeVerifier struct{}

func (fakeVerifier) VerifyAllocatable(partition.Target, map[string]int32) (*partition.VerifyResult, error) {
	return &partition.VerifyResult{AllocatableConverged: true}, nil
}
func (fakeVerifier) VerifyAllocation(partition.Target, string) (*partition.VerifyResult, error) {
	return &partition.VerifyResult{TestPodAllocated: true}, nil
}

var _ = Describe("ACPP reconciler happy path", func() {
	It("reaches Ready on no-diff RNGD policy", func() {
		By("seeding unified DS (dual-core) + NDR + RNGD node")
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "rngd-1",
				Labels: map[string]string{rngdPresentNodeLabel: "true"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		// unified DS 는 클러스터에 singleton 이므로(다른 spec 도 재사용) get-or-create+nodeSelector 리셋으로 실행 순서와 무관하게 만든다.
		upsertUnifiedDS(nil)

		ndr := &npuv1alpha1.NodeDeviceReport{
			ObjectMeta: metav1.ObjectMeta{Name: "rngd-1"},
			Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "rngd-1"},
		}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
		ndr.Status = npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{{
				Vendor: "furiosa", Model: "RNGD", Count: 1,
				DriverLoaded: true, DriverVersion: "1.0.0", PCIeAddress: "0000:01:00.0",
			}},
		}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())

		By("creating ACPP 2core.12gb")
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "rngd-four-way"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   map[string]string{rngdPresentNodeLabel: "true"},
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
			NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} }}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("rngd-four-way"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "rngd-four-way"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "rngd-four-way"}, &got)).To(Succeed())
		Expect(got.Status.Targets).To(HaveLen(1))
		Expect(got.Status.Targets[0].ResolvedLayout[0].BackendPolicy).To(Equal(testDualCorePolicy))
		Expect(got.Status.Targets[0].Advertisement.Mode).To(Equal("Flat"))
	})

	It("sets ec.Vendor and ec.Expectation.ProbeResource on the rngd Ready path", func() {
		wipeACPPs()
		sel := map[string]string{"kcloud.ai/probe-wiring-target": "true"}
		seedRngdNode("probe-wiring-node-1", sel)
		upsertUnifiedDS(sel)

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "probe-wiring-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{}}
		backend, err := r.backendFor(acpp.Spec.Vendor)
		Expect(err).NotTo(HaveOccurred())
		target := partition.Target{Ctx: ctx, NodeName: firstNodeName(ctx, r.Client, acpp.Spec.NodeSelector), Owner: acpp.Name,
			Vendor: acpp.Spec.Vendor, DaemonSetName: rngdUnifiedDSName, DaemonSetNamespace: rngdUnifiedDSNS()}

		ec := &evidenceCtx{}
		ts, err := r.runTarget(acpp, backend, target, ec)
		Expect(err).NotTo(HaveOccurred())
		Expect(ts.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(ec.Vendor).To(Equal(vendorFuriosa))
		Expect(ec.Expectation.ProbeResource).To(Equal(rngd.RngdResourceName))
	})
})

// deleteACPPAndWait 은 ACPP 하나를 finalizer 까지 제거하며 지우고, 실제로 없어질 때까지
// Eventually 로 기다린다. ACPP 를 만드는 스펙의 cleanup 은 이 helper 로 통일한다 — 맨
// k8sClient.Delete 만 하면 finalizer 가 남아 DeletionTimestamp 만 찍힌 채 Terminating 으로
// 남고, nodeHasOwningPolicy 는 그 상태를 가리지 않아 다른 스펙의 오판 재료가 된다.
func deleteACPPAndWait(name string) {
	_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorPartitionPolicy{ObjectMeta: metav1.ObjectMeta{Name: name}})
	Eventually(func() bool {
		var got npuv1alpha1.AcceleratorPartitionPolicy
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got)
		if apierrors.IsNotFound(err) {
			return true
		}
		if err != nil {
			return false
		}
		if controllerutil.RemoveFinalizer(&got, acppFinalizer) {
			_ = k8sClient.Update(ctx, &got)
		}
		return false
	}, "5s", "200ms").Should(BeTrue(), fmt.Sprintf("ACPP %s 가 finalizer 정리 후에도 삭제되지 않았다", name))
}

// terminatingACPPs 는 DeletionTimestamp 가 찍힌 채 남은 AcceleratorPartitionPolicy 이름 목록이다.
// AfterSuite 가 이것으로 맨 Delete 누수를 스펙 이름 대신 객체 이름으로 드러낸다.
func terminatingACPPs() []string {
	var list npuv1alpha1.AcceleratorPartitionPolicyList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())
	var out []string
	for i := range list.Items {
		if !list.Items[i].DeletionTimestamp.IsZero() {
			out = append(out, list.Items[i].Name)
		}
	}
	return out
}

// wipeACPPs 는 남은 ACPP 를 모두 finalizer 제거 후 완전히 삭제한다(충돌 테스트 간 contender 목록 격리).
func wipeACPPs() {
	var list npuv1alpha1.AcceleratorPartitionPolicyList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())
	for i := range list.Items {
		deleteACPPAndWait(list.Items[i].Name)
	}
}

// upsertUnifiedDS 는 singleton unified DS 를 주어진 pod-template nodeSelector 로 생성/갱신한다.
func upsertUnifiedDS(nodeSelector map[string]string) {
	var ds appsv1.DaemonSet
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds); err != nil {
		ds = appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": rngdUnifiedDSName}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": rngdUnifiedDSName}},
					Spec: corev1.PodSpec{
						NodeSelector: nodeSelector,
						Containers: []corev1.Container{{
							Name:  "furiosa-device-plugin",
							Image: "furiosa-unified-device-plugin:0.1.0",
							Env:   []corev1.EnvVar{{Name: testPartitionPolicyEnv, Value: testDualCorePolicy}},
						}},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, &ds)).To(Succeed())
		return
	}
	ds.Spec.Template.Spec.NodeSelector = nodeSelector
	// 다른 spec 의 Apply(또는 실패한 Rollback)가 env 를 dual-core 가 아닌 값으로 남겼을 수 있으므로
	// singleton DS 를 재사용할 때마다 baseline env 로 리셋(nodeSelector 와 마찬가지로 결정적 시작 상태 보장).
	for ci := range ds.Spec.Template.Spec.Containers {
		ctr := &ds.Spec.Template.Spec.Containers[ci]
		if ctr.Name != "furiosa-device-plugin" {
			continue
		}
		for ei := range ctr.Env {
			if ctr.Env[ei].Name == testPartitionPolicyEnv {
				ctr.Env[ei].Value = testDualCorePolicy
			}
		}
	}
	Expect(k8sClient.Update(ctx, &ds)).To(Succeed())
}

// seedRngdNode 는 라벨 붙은 노드 + furiosa NDR 을 시드한다(vendor 는 항상 furiosa — 유일 호출값).
func seedRngdNode(name string, labels map[string]string) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: name},
		Status: npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{{
				Vendor: vendorFuriosa, Model: "RNGD", Count: 1,
				DriverLoaded: true, DriverVersion: "1.0.0", PCIeAddress: "0000:01:00.0",
			}},
		},
	}
	Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
	ndr.Status = npuv1alpha1.NodeDeviceReportStatus{
		Devices: []npuv1alpha1.DeviceEntry{{
			Vendor: vendorFuriosa, Model: "RNGD", Count: 1,
			DriverLoaded: true, DriverVersion: "1.0.0", PCIeAddress: "0000:01:00.0",
		}},
	}
	Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed()) // status subresource — Create 만으론 반영 안됨
}

// appliedReason 은 target[0] 의 Applied condition reason 을 반환한다.
func appliedReason(acpp *npuv1alpha1.AcceleratorPartitionPolicy) string {
	if len(acpp.Status.Targets) == 0 {
		return ""
	}
	c := apimeta.FindStatusCondition(acpp.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondApplied)
	if c == nil {
		return ""
	}
	return c.Reason
}

var _ = Describe("ACPP conflict", func() {
	It("only the deterministic winner applies when two ACPPs target the same DaemonSet", func() {
		wipeACPPs()
		sel := map[string]string{"kcloud.ai/conflict-target": "true"}
		seedRngdNode("tc-node-1", sel)
		upsertUnifiedDS(sel)

		mkACPP := func(name string) *npuv1alpha1.AcceleratorPartitionPolicy {
			return &npuv1alpha1.AcceleratorPartitionPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
					NodeSelector:   sel,
					Vendor:         "furiosa",
					Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
					DeletionPolicy: "Retain",
				},
			}
		}

		By("creating the earlier ACPP first (alphabetically later name — timestamp must decide, not name)")
		earlier := mkACPP("zzz-created-first")
		Expect(k8sClient.Create(ctx, earlier)).To(Succeed())

		time.Sleep(1100 * time.Millisecond) // creationTimestamp 는 1s 해상도 — 확실히 분리

		later := mkACPP("aaa-created-second")
		Expect(k8sClient.Create(ctx, later)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{}}

		By("reconciling the earlier-created ACPP — it wins and reaches Ready")
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("zzz-created-first"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "zzz-created-first"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		By("reconciling the later-created ACPP — it loses with TargetConflict")
		_, _ = r.Reconcile(ctx, reconcileReq("aaa-created-second"))
		var loser npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aaa-created-second"}, &loser)).To(Succeed())
		Expect(loser.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseFailed))
		Expect(appliedReason(&loser)).To(Equal(npuv1alpha1.ReasonTargetConflict))
	})

	It("marks BackendScopeMismatch when selector matches only a subset of DS-managed nodes", func() {
		wipeACPPs()
		dsSel := map[string]string{"kcloud.ai/conflict-scope": "true"}
		seedRngdNode("scope-node-a", map[string]string{"kcloud.ai/conflict-scope": "true", "zone": "a", rngdPresentNodeLabel: "true"})
		Expect(k8sClient.Create(ctx, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "scope-node-b", Labels: map[string]string{"kcloud.ai/conflict-scope": "true", "zone": "b", rngdPresentNodeLabel: "true"}},
		})).To(Succeed())
		upsertUnifiedDS(dsSel) // DS 는 두 노드(a,b) 모두 관리

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "scope-mismatch"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   map[string]string{"zone": "a"}, // node a 만 매칭 — DS 관리 노드의 부분집합
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
			NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} }}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("scope-mismatch"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "scope-mismatch"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "scope-mismatch"}, &got)).To(Succeed())
		Expect(appliedReason(&got)).To(Equal(npuv1alpha1.ReasonBackendScopeMismatch))
	})

	It("marks VendorMismatch when spec.vendor != actual node vendor", func() {
		wipeACPPs()
		sel := map[string]string{"kcloud.ai/conflict-vendor": "true"}
		seedRngdNode("vendor-node-1", sel) // 실제 노드 벤더 = furiosa

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "vendor-mismatch"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "nvidia", // spec.vendor != 실제 노드 벤더(furiosa)
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 1}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
			NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} }}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("vendor-mismatch"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "vendor-mismatch"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "vendor-mismatch"}, &got)).To(Succeed())
		Expect(appliedReason(&got)).To(Equal(npuv1alpha1.ReasonVendorMismatch))
	})
})

// failVerifier 는 VerifyAllocation 을 항상 실패(all-false)로 반환한다(rollback 유발 seam).
type failVerifier struct{}

func (failVerifier) VerifyAllocatable(partition.Target, map[string]int32) (*partition.VerifyResult, error) {
	return &partition.VerifyResult{}, nil
}
func (failVerifier) VerifyAllocation(partition.Target, string) (*partition.VerifyResult, error) {
	return &partition.VerifyResult{TestPodAllocated: false, AllocatableConverged: false}, nil
}

// uidBustingFailVerifier 는 실패 검증과 함께, 검증 도중 DS 를 삭제·재생성해 UID 를 바꾼다
// (rollback 시도가 저장된 UID 와 불일치해 실패하는 경로를 재현하는 seam).
type uidBustingFailVerifier struct{}

func (uidBustingFailVerifier) VerifyAllocatable(partition.Target, map[string]int32) (*partition.VerifyResult, error) {
	return &partition.VerifyResult{}, nil
}
func (uidBustingFailVerifier) VerifyAllocation(partition.Target, string) (*partition.VerifyResult, error) {
	var ds appsv1.DaemonSet
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds); err == nil {
		fresh := ds.DeepCopy()
		fresh.ResourceVersion = ""
		fresh.UID = ""
		Expect(k8sClient.Delete(ctx, &ds)).To(Succeed())
		Expect(k8sClient.Create(ctx, fresh)).To(Succeed())
	}
	return &partition.VerifyResult{TestPodAllocated: false, AllocatableConverged: false}, nil
}

var _ = Describe("ACPP rollback on failure", func() {
	It("rolls back to previous policy and ends in Failed(PreviousConfigurationRestored) when verify fails after apply", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/rollback-a": "true"}
		seedRngdNode("rollback-node-1", sel)
		upsertUnifiedDS(sel) // 기본 env RNGD_PARTITION_POLICY=dual-core

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "rollback-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "4core.24gb", CountPerDevice: 2}}, // → quad-core, DS 와 다름(diff.Changed)
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: failVerifier{}}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("rollback-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "rollback-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "rollback-acpp"}, &got)).To(Succeed())
		rb := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondRolledBack)
		Expect(rb).NotTo(BeNil())
		Expect(rb.Status).To(Equal(metav1.ConditionTrue))
		Expect(rb.Reason).To(Equal("RollbackSucceeded"))

		var ds appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds)).To(Succeed())
		Expect(envValueOf(&ds)).To(Equal(testDualCorePolicy), "DS env must be restored to the pre-apply value")
	})

	It("marks RollbackFailed when restore itself fails (DS UID changed)", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/rollback-b": "true"}
		seedRngdNode("rollback-node-2", sel)
		upsertUnifiedDS(sel) // 기본 env RNGD_PARTITION_POLICY=dual-core

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "rollback-fail-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "4core.24gb", CountPerDevice: 2}}, // → quad-core, DS 와 다름(diff.Changed)
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: uidBustingFailVerifier{}}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("rollback-fail-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "rollback-fail-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "rollback-fail-acpp"}, &got)).To(Succeed())
		rb := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondRolledBack)
		Expect(rb).NotTo(BeNil())
		Expect(rb.Status).To(Equal(metav1.ConditionFalse))
		Expect(rb.Reason).To(Equal(npuv1alpha1.ACPPPhaseRollbackFailed))
	})

	// 회귀: no-diff(DS 이미 목표값) + verify 실패(all-false, error 없음) 는 Ready 로 승격되면 안 된다.
	// (RollbackFailed 후 requeue 시 DS 가 applied 값이라 no-diff 진입 → all-false 를 Ready 로 오승격하면
	//  수동개입 신호가 사라지는 escape hatch. verifySucceeded 로 양 분기 통일해 차단.)
	It("does NOT reach Ready on no-diff when verify reports failure without an error", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nodiff-fail": "true"}
		seedRngdNode("nodiff-fail-node", sel)
		upsertUnifiedDS(sel) // env=dual-core

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "nodiff-fail-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}}, // → dual-core, DS 와 동일(no-diff)
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: failVerifier{}}
		// 여러 번 reconcile 해도 Ready 로 새지 않고 Failed 를 유지해야 한다.
		Consistently(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nodiff-fail-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nodiff-fail-acpp"}, &got)
			return got.Status.Phase
		}, "2s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nodiff-fail-acpp"}, &got)).To(Succeed())
		v := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondVerified)
		Expect(v).NotTo(BeNil())
		Expect(v.Status).To(Equal(metav1.ConditionFalse))
	})
})

// envValueOf 는 DS 의 plugin 컨테이너 RNGD_PARTITION_POLICY env 값을 반환한다(테스트 assertion 용).
func envValueOf(ds *appsv1.DaemonSet) string {
	for _, ctr := range ds.Spec.Template.Spec.Containers {
		if ctr.Name != "furiosa-device-plugin" {
			continue
		}
		for _, e := range ctr.Env {
			if e.Name == testPartitionPolicyEnv {
				return e.Value
			}
		}
	}
	return ""
}

// countingVerifier 는 fakeVerifier 를 감싸 VerifyAllocation 호출 횟수를 센다
// (evidence-reuse 게이트로 재검증이 스킵되는지 확인하는 용도).
type countingVerifier struct {
	fakeVerifier
	calls int
}

func (c *countingVerifier) VerifyAllocation(t partition.Target, resourceName string) (*partition.VerifyResult, error) {
	c.calls++
	return c.fakeVerifier.VerifyAllocation(t, resourceName)
}

var _ = Describe("ACPP no-diff and idempotency", func() {
	It("skips Apply on no-diff but still reaches Ready with changed=false", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs) // 다른 spec(예: happy-path 의 vendor=furiosa contender 판정)으로 새지 않게 정리.
		sel := map[string]string{"kcloud.ai/no-diff-a": "true"}
		seedRngdNode("no-diff-node-1", sel)
		upsertUnifiedDS(sel) // 기본 env RNGD_PARTITION_POLICY=dual-core

		var dsBefore appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsBefore)).To(Succeed())

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "no-diff-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}}, // → dual-core, DS 와 이미 동일(no-diff)
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
			NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} }}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("no-diff-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "no-diff-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "no-diff-acpp"}, &got)).To(Succeed())
		c := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondApplied)
		Expect(c).NotTo(BeNil())
		Expect(c.Message).To(ContainSubstring("changed=false"))

		var dsAfter appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsAfter)).To(Succeed())
		// no-diff 여도 env(RNGD_PARTITION_POLICY) 자체는 절대 재기록되면 안 된다(Apply skip 은 여전히 유효).
		Expect(envValueOf(&dsAfter)).To(Equal(testDualCorePolicy), "Apply must be skipped on no-diff — env must not be rewritten")
		// no-diff 로 Ready 에 도달한 ACPP 도 owner-lock 을 주장해야 한다(ensureOwnerLock 이
		// annotation 을 새로 찍으므로, dsBefore 대비 DS 는 한 번 갱신된다 — 그래서 env 값으로만 no-write 를 확인).
		Expect(dsAfter.Annotations[rngd.PartitionOwnerAnnotation]).To(Equal("no-diff-acpp"), "no-diff Ready must still claim the owner lock")
	})

	It("is idempotent across repeated reconciles: no DS write, no re-verify, stays Ready", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/no-diff-b": "true"}
		seedRngdNode("idem-node-1", sel)
		upsertUnifiedDS(sel)

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "idem-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		cv := &countingVerifier{}
		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: cv}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("idem-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "idem-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(cv.calls).To(Equal(1), "first reconcile must verify once")

		var dsAfterFirst appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsAfterFirst)).To(Succeed())

		By("reconciling again with no spec change — must not rewrite DS or re-verify")
		_, err := r.Reconcile(ctx, reconcileReq("idem-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "idem-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(cv.calls).To(Equal(1), "evidence-reuse gate must skip re-verify on unchanged generation/config")

		var dsAfterSecond appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsAfterSecond)).To(Succeed())
		Expect(dsAfterSecond.ResourceVersion).To(Equal(dsAfterFirst.ResourceVersion), "repeated reconcile must not rewrite the DS")
	})
})

// ACPP validation rejection — criterion 9: 잘못된 layout(profile) 은 apply 전 거부되어야 하며
// DS env 는 절대 mutate 되면 안 된다(runTarget Validate 게이트가 Diff/Apply 이전에 return).
var _ = Describe("ACPP validation rejection", func() {
	assertRejectedBeforeApply := func(name string, layout []npuv1alpha1.PartitionLayout) {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/validation-reject-" + name: "true"} // per-spec selector — 노드/DS 명 충돌 방지
		seedRngdNode("validation-reject-node-"+name, sel)
		upsertUnifiedDS(sel) // 기본 env RNGD_PARTITION_POLICY=dual-core

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         layout,
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
			NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} }}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq(name))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got)).To(Succeed())
		Expect(got.Status.Targets).To(HaveLen(1))
		validated := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondValidated)
		Expect(validated).NotTo(BeNil())
		Expect(validated.Status).To(Equal(metav1.ConditionFalse))
		Expect(validated.Reason).To(Equal("ValidationFailed"))
		// Apply 가 아예 실행되지 않았다는 증거 — Applied condition 부재.
		Expect(apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondApplied)).To(BeNil())

		// DS env 는 apply 이전 상태(dual-core)로 절대 mutate 되지 않아야 한다(criterion 9).
		Consistently(func() string {
			var ds appsv1.DaemonSet
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds)).To(Succeed())
			return envValueOf(&ds)
		}, "2s", "200ms").Should(Equal(testDualCorePolicy))
	}

	It("rejects LegacyDocumented profile before any apply", func() {
		assertRejectedBeforeApply("reject-legacy-documented", []npuv1alpha1.PartitionLayout{{Profile: "1core.6gb", CountPerDevice: 8}})
	})

	It("rejects unknown profile before any apply", func() {
		assertRejectedBeforeApply("reject-unknown-profile", []npuv1alpha1.PartitionLayout{{Profile: "9core.99gb", CountPerDevice: 1}})
	})

	It("rejects mismatched countPerDevice before any apply", func() {
		assertRejectedBeforeApply("reject-count-mismatch", []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 3}})
	})
})

// NVIDIA ACPP 는 RNGD DS 기반 충돌/scope 가드를 우회해 자기 벤더 흐름(runTarget nvidia)으로 진입해야 한다.
// (regression: 혼합 벤더 클러스터에서 furiosa DS 가 있어도 NVIDIA ACPP 가 BackendScopeMismatch 로 오분류되면 안 됨)
// MIG-capable A30 은 apply 지원이므로 Unsupported 가 아니라 벤더 흐름으로 진입한다 —
// 여기선 count=0(무효 layout)로 Validate 단계까지 도달함을 확인해 우회를 증명한다(BackendScopeMismatch 아님).
var _ = Describe("ACPP NVIDIA bypasses RNGD conflict guard", func() {
	It("enters the nvidia vendor flow (not BackendScopeMismatch) even when the furiosa unified DS is present", func() {
		wipeACPPs()
		upsertUnifiedDS(map[string]string{furiosaFamilyNodeLabel: "true"}) // NVIDIA selector 로는 못 덮는 scope

		// furiosa-family 노드를 실제 시드 — dsManagedNodes 가 비어있지 않고 NVIDIA selector 로 못 덮이게 해야
		// pre-fix 코드에서 scope 체크가 BackendScopeMismatch 로 발동한다(테스트 실효성 보장).
		fuNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "rngd-scope-node", Labels: map[string]string{furiosaFamilyNodeLabel: "true", rngdPresentNodeLabel: "true"}}}
		Expect(k8sClient.Create(ctx, fuNode)).To(Succeed())

		nvNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-1", Labels: map[string]string{"kcloud.ai/nvidia.present": "true"}}}
		Expect(k8sClient.Create(ctx, nvNode)).To(Succeed())
		ndr := &npuv1alpha1.NodeDeviceReport{
			ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-1"},
			Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "gpu-node-1"},
		}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
		ndr.Status = npuv1alpha1.NodeDeviceReportStatus{Devices: []npuv1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "NVIDIA-A30", PCIeAddress: "0000:41:00.0"},
		}}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "nv-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector: map[string]string{"kcloud.ai/nvidia.present": "true"},
				Vendor:       "nvidia",
				Layout:       []npuv1alpha1.PartitionLayout{{Profile: "2g.12gb"}},
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		// 자기 정리 — 실패해도 seeded 노드/NDR/ACPP 가 타 spec(랜덤 순서)으로 새지 않게 한다.
		// 아래 Eventually 가 Reconcile 을 돌려 finalizer 를 붙이므로, ACPP 는 맨 Delete 만으로는
		// Terminating 으로 남는다 — deleteACPPAndWait 로 finalizer 까지 걷어야 실제로 사라진다.
		DeferCleanup(func() {
			deleteACPPAndWait(acpp.Name)
			_ = k8sClient.Delete(ctx, ndr)
			_ = k8sClient.Delete(ctx, nvNode)
			_ = k8sClient.Delete(ctx, fuNode)
		})
		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
			NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} }}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-acpp"}, &got)).To(Succeed())
		// 우회 증명: RNGD scope 가드에 걸렸다면 Applied=BackendScopeMismatch 였을 것. 대신 벤더 흐름의
		// Validate(count=0 무효)까지 진입해 ValidationFailed 로 실패한다.
		validated := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondValidated)
		Expect(validated).NotTo(BeNil())
		Expect(validated.Status).To(Equal(metav1.ConditionFalse))
		Expect(validated.Reason).To(Equal("ValidationFailed"))
		Expect(appliedReason(&got)).NotTo(Equal(npuv1alpha1.ReasonBackendScopeMismatch))
	})
})

// ACPP deletion(Retain, ) — owner lock 은 해제하되 파티션(env) 은 유지한다(자동 rollback 위험 회피).
var _ = Describe("ACPP deletion (Retain)", func() {
	It("removes owner lock and finalizer but keeps partition on delete", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/deletion-retain": "true"}
		seedRngdNode("deletion-retain-node", sel)
		upsertUnifiedDS(sel) // env=dual-core

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "deletion-retain-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector: sel,
				Vendor:       "furiosa",
				// dual-core(DS 기본값)과 다른 profile → changed apply 유발 → 백엔드가 owner annotation 을 찍는다.
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "4core.24gb", CountPerDevice: 2}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
			NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} }}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("deletion-retain-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "deletion-retain-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var dsReady appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsReady)).To(Succeed())
		Expect(envValueOf(&dsReady)).To(Equal("quad-core"))
		Expect(dsReady.Annotations[rngd.PartitionOwnerAnnotation]).To(Equal("deletion-retain-acpp"), "changed apply must stamp the owner lock with the ACPP's own name")

		By("deleting the ACPP")
		Expect(k8sClient.Delete(ctx, acpp)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("deletion-retain-acpp"))
		Expect(err).NotTo(HaveOccurred())

		By("ACPP is actually deleted (finalizer removed)")
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "deletion-retain-acpp"}, &got))
		}, "5s", "200ms").Should(BeTrue())

		var dsAfter appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsAfter)).To(Succeed())
		Expect(envValueOf(&dsAfter)).To(Equal("quad-core"), "Retain: partition must be kept, not rolled back")
		_, hasOwnerAfter := dsAfter.Annotations[rngd.PartitionOwnerAnnotation]
		Expect(hasOwnerAfter).To(BeFalse(), "owner lock must be released on delete")
	})

	// DS 가 이미 삭제된(NotFound) 상태에서 삭제해도 finalizer 는 제거되어 ACPP 가 stuck 되지 않는다.
	It("removes finalizer gracefully when the unified DS is absent", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/deletion-absent": "true"}
		seedRngdNode("deletion-absent-node", sel)
		upsertUnifiedDS(sel)

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "deletion-absent-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{}}
		// 한 번 reconcile 해 finalizer 를 붙인다.
		_, _ = r.Reconcile(ctx, reconcileReq("deletion-absent-acpp"))

		By("deleting the unified DS, then the ACPP")
		var ds appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &ds)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &ds)).To(Succeed())
		Expect(k8sClient.Delete(ctx, acpp)).To(Succeed())

		_, err := r.Reconcile(ctx, reconcileReq("deletion-absent-acpp"))
		Expect(err).NotTo(HaveOccurred(), "DS-absent must not error the deletion")
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "deletion-absent-acpp"}, &got))
		}, "5s", "200ms").Should(BeTrue(), "finalizer must be removed even when DS is gone")
	})

	// TargetConflict 패자도 finalizer 를 갖는다(Reconcile 이 충돌 판정 전에 finalizer 를 붙이므로).
	// 패자 삭제가 승자의 owner-lock(annotation 값 = 승자 이름)을 지우면 안 된다.
	It("deleting a TargetConflict loser does not release the winner's owner lock", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/owner-lock-conflict": "true"}
		seedRngdNode("owner-lock-node", sel)
		upsertUnifiedDS(sel) // env=dual-core

		mk := func(name string) *npuv1alpha1.AcceleratorPartitionPolicy {
			return &npuv1alpha1.AcceleratorPartitionPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
					NodeSelector: sel,
					Vendor:       "furiosa",
					// dual-core(DS 기본값)과 동일 → no-diff 경로로 owner-lock 을 주장해야 이 테스트가 위 결함을 검증한다.
					Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
					DeletionPolicy: "Retain",
				},
			}
		}

		By("creating winner first, loser second (creationTimestamp decides)")
		winner := mk("owner-lock-winner")
		Expect(k8sClient.Create(ctx, winner)).To(Succeed())
		time.Sleep(1100 * time.Millisecond) // creationTimestamp 1s 해상도 — 확실히 분리
		loser := mk("owner-lock-loser")
		Expect(k8sClient.Create(ctx, loser)).To(Succeed())

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{}}

		By("winner reconciles to Ready via no-diff and claims the owner lock")
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("owner-lock-winner"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "owner-lock-winner"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var dsAfterWinner appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsAfterWinner)).To(Succeed())
		Expect(dsAfterWinner.Annotations[rngd.PartitionOwnerAnnotation]).To(Equal("owner-lock-winner"))

		By("loser reconciles — gets a finalizer but loses with TargetConflict")
		_, _ = r.Reconcile(ctx, reconcileReq("owner-lock-loser"))
		var loserGot npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "owner-lock-loser"}, &loserGot)).To(Succeed())
		Expect(loserGot.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseFailed))
		Expect(appliedReason(&loserGot)).To(Equal(npuv1alpha1.ReasonTargetConflict))

		By("deleting the loser — the winner's owner lock must survive")
		Expect(k8sClient.Delete(ctx, &loserGot)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("owner-lock-loser"))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "owner-lock-loser"}, &got))
		}, "5s", "200ms").Should(BeTrue())

		var dsAfterLoserDelete appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rngdUnifiedDSName, Namespace: rngdUnifiedDSNS()}, &dsAfterLoserDelete)).To(Succeed())
		Expect(dsAfterLoserDelete.Annotations[rngd.PartitionOwnerAnnotation]).To(Equal("owner-lock-winner"), "loser deletion must not release the winner's owner lock")
	})
})

// ---- nvidia MIG durable-journal 상태머신 ----

// lgipA30Test 는 A30 MIG profile 목록(1g.6gb/2g.12gb/4g.24gb) 원문 — supported 채우기용.
const lgipA30Test = `|   0  MIG 1g.6gb   19  4/4  5.75 |
|   0  MIG 2g.12gb  14  2/2  11.62 |
|   0  MIG 4g.24gb   5  1/1  23.37 |`

// fakeNvidiaExec 는 nvidia.Executor 를 구현해 실제 Job 없이 apply/rollback 을 성공 처리한다(envtest seam).
type fakeNvidiaExec struct{}

func (fakeNvidiaExec) Run(context.Context, string, string, string, []nvidia.CommandStep) error {
	return nil
}

// fakeNvidiaObserver 는 nvidia.Observer 를 구현해 실제 Job 없이 MIG 관측을 반환한다(envtest seam, ).
// 기본은 seeded NDR 의 MIG 필드에서 관측을 파생해 기존 스펙(NDR 로 상태 주입)이 그대로 동작하게 한다.
// override 가 non-nil 이면 그 관측을, oerr 이 non-nil 이면 그 에러를 반환한다(특정 시나리오용).
type fakeNvidiaObserver struct {
	c        client.Client
	override []nvidia.Observation
	oerr     error
}

func (o fakeNvidiaObserver) Observe(ctx context.Context, nodeName string, pcis []string) ([]nvidia.Observation, error) {
	if o.oerr != nil {
		return nil, o.oerr
	}
	if o.override != nil {
		return o.override, nil
	}
	var ndr npuv1alpha1.NodeDeviceReport
	if err := o.c.Get(ctx, types.NamespacedName{Name: nodeName}, &ndr); err != nil {
		return nil, err
	}
	out := make([]nvidia.Observation, 0, len(pcis))
	for _, pci := range pcis {
		for _, d := range ndr.Status.Devices {
			if d.PCIeAddress != pci {
				continue
			}
			out = append(out, nvidia.Observation{
				PCI: pci, ModeCurrent: d.MigModeCurrent, ModePending: d.MigModePending,
				Geometry: d.MigCurrentGeometry, LgipOutput: d.MigLgipOutput, Err: d.MigObservationError,
			})
		}
	}
	return out, nil
}

// a30Device 는 A30 DeviceEntry 를 관측 파라미터로 만든다(driver/PCI/lgip 는 고정).
func a30Device(geometry, modeCurrent, modePending, obsErr string) npuv1alpha1.DeviceEntry {
	return npuv1alpha1.DeviceEntry{
		Vendor: "nvidia", Model: "NVIDIA A30", Count: 1,
		DriverLoaded: true, DriverVersion: "535.104.05", PCIeAddress: "0000:41:00.0",
		MigModeCurrent: modeCurrent, MigModePending: modePending, MigCurrentGeometry: geometry,
		MigObservationError: obsErr, MigLgipOutput: lgipA30Test,
	}
}

// seedNvidiaNode 는 라벨/cordon 을 가진 노드(allocatable nvidia.com/gpu=1) + nvidia NDR 을 시드한다.
// devs 가 여럿이면 다중 GPU 노드다(라이브 worker1 = MIG A30 + 비-MIG A2).
func seedNvidiaNode(name string, labels map[string]string, cordoned bool, devs ...npuv1alpha1.DeviceEntry) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
	}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	node.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceName("nvidia.com/gpu"): *resource.NewQuantity(1, resource.DecimalSI),
	}
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: name},
	}
	Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
	ndr.Status = npuv1alpha1.NodeDeviceReportStatus{Devices: devs}
	Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())
}

// setNodeGPUAllocatable 은 노드의 nvidia.com/gpu 광고량을 덮어쓴다.
//
// MIG mode 가 Enabled 인 GPU 는 조각이 없어도 full GPU 로 광고되지 않는다(A30 2장 실측:
// mode Enabled + GI 0 → nvidia.com/gpu=0). seedNvidiaNode 의 기본값 1 은 mode Disabled 를
// 전제한 값이라, MIG Enabled 로 시드하는 삭제 시험은 이 헬퍼로 0 을 명시해야 프로덕션이
// 실제로 만들 수 있는 상태가 된다.
func setNodeGPUAllocatable(name string, n int64) {
	var node corev1.Node
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &node)).To(Succeed())
	node.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceName("nvidia.com/gpu"): *resource.NewQuantity(n, resource.DecimalSI),
	}
	Expect(k8sClient.Status().Update(ctx, &node)).To(Succeed())
}

// nvidiaReconciler 는 fake executor(실 Job 우회) + 항상-성공 verifier 로 구성한 reconciler 다.
func nvidiaReconciler() *AcceleratorPartitionPolicyReconciler {
	return &AcceleratorPartitionPolicyReconciler{
		Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: fakeVerifier{},
		NvidiaExecFactory:     func(client.Client) nvidia.Executor { return fakeNvidiaExec{} },
		NvidiaObserverFactory: func(c client.Client) nvidia.Observer { return fakeNvidiaObserver{c: c} },
	}
}

// mkNvidiaACPP 는 nvidia ACPP(1g.6gb×4)를 만든다.
func mkNvidiaACPP(name string, sel map[string]string) *npuv1alpha1.AcceleratorPartitionPolicy {
	return &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			NodeSelector:   sel,
			Vendor:         "nvidia",
			Layout:         []npuv1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 4}},
			DeletionPolicy: "Retain",
		},
	}
}

// condReasonOf 는 target[0] 의 지정 condition reason 을 반환한다.
func condReasonOf(acpp *npuv1alpha1.AcceleratorPartitionPolicy, ctype string) string {
	if len(acpp.Status.Targets) == 0 {
		return ""
	}
	c := apimeta.FindStatusCondition(acpp.Status.Targets[0].Conditions, ctype)
	if c == nil {
		return ""
	}
	return c.Reason
}

// countingExec 는 실 Job 없이 성공 처리하면서 실행한 command step 수를 센다 — 재적용 왕복
// (GI 생성·파괴 반복)을 시각이 아니라 "하드웨어를 몇 번 건드렸나" 로 관측하기 위한 seam 이다.
type countingExec struct{ steps *int }

func (c countingExec) Run(_ context.Context, _, _, _ string, steps []nvidia.CommandStep) error {
	*c.steps += len(steps)
	return nil
}

// gpuPodOn 은 노드의 GPU 를 점유한 pod 이다 — GPUBusy(=quiesce 미완) 전제를 실제로 만든다.
func gpuPodOn(name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{
			Name: "app", Image: "busybox",
			Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
				corev1.ResourceName(nvidiaGPUResource): *resource.NewQuantity(1, resource.DecimalSI),
			}},
		}}},
	}
}

// condTimes 는 target[0] 의 "condition=전이시각" 목록이다 — 전체 status 덤프는 Gomega 가 잘라내
// 무엇이 달라졌는지 안 보이므로, 타임스탬프만 뽑아 비교한다.
func condTimes(acpp *npuv1alpha1.AcceleratorPartitionPolicy) []string {
	if len(acpp.Status.Targets) == 0 {
		return nil
	}
	out := make([]string, 0, len(acpp.Status.Targets[0].Conditions))
	for _, c := range acpp.Status.Targets[0].Conditions {
		out = append(out, c.Type+"="+c.LastTransitionTime.UTC().Format(time.RFC3339))
	}
	return out
}

var _ = Describe("ACPP nvidia MIG journal", func() {
	// runTarget 이 매 reconcile 마다 TargetStatus 를 새로 만들면(Conditions 빈 슬라이스)
	// SetStatusCondition 이 LastTransitionTime=now 를 다시 찍어, 아무것도 바뀌지 않은 reconcile 조차
	// 타임스탬프만 다른 status write 가 된다. 그 write 의 watch 이벤트가 자기 자신을 즉시 재큐잉하고
	// (informer 캐시가 낡으면 resourceVersion conflict 까지) 정책은 영원히 같은 자리를 돈다.
	// 따라서 비-Ready 정상상태에서 두 번째 reconcile 은 apiserver 에 아무것도 쓰지 않아야 한다.
	It("does not write status on an unchanged reconcile in a non-Ready steady state (no hot loop)", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-hotloop": "true"}
		// 외부 cordon(정책 소유 아님) + GPU 점유 pod = 정책이 진행도 실패도 못 하는 정상상태(QuiesceRequired).
		seedNvidiaNode("nv-hotloop-node", sel, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-hotloop-node") })
		pod := gpuPodOn("nv-hotloop-pod", "nv-hotloop-node")
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod) })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-hotloop-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-hotloop-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-hotloop-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseWaitingForDrain))

		var blocked npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-hotloop-acpp"}, &blocked)).To(Succeed())
		Expect(condReasonOf(&blocked, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonQuiesceRequired))

		By("최초 진입의 부수효과(finalizer/owner-lock/저널)를 모두 흘려보낸다")
		for i := 0; i < 2; i++ {
			_, err := r.Reconcile(ctx, reconcileReq("nv-hotloop-acpp"))
			Expect(err).NotTo(HaveOccurred())
		}
		var before npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-hotloop-acpp"}, &before)).To(Succeed())

		// LastTransitionTime 은 초 단위로 직렬화되므로 같은 초 안의 연속 reconcile 은 우연히 같은
		// status 를 만든다 — 초를 넘겨야 "매번 now 를 다시 찍는" 결함이 확정적으로 드러난다.
		time.Sleep(1100 * time.Millisecond)

		By("아무것도 바뀌지 않은 reconcile 은 write 를 만들지 않아야 한다")
		_, err := r.Reconcile(ctx, reconcileReq("nv-hotloop-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var after npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-hotloop-acpp"}, &after)).To(Succeed())
		Expect(condTimes(&after)).To(Equal(condTimes(&before)),
			"lastTransitionTime must be preserved when a condition's status did not change")
		Expect(after.Status.Targets).To(Equal(before.Status.Targets),
			"unchanged reconcile must reproduce a byte-identical target status (including lastTransitionTime)")
		Expect(after.ResourceVersion).To(Equal(before.ResourceVersion),
			"unchanged reconcile must be a no-op write — a real write re-enqueues the object and hot-loops")
	})

	It("blocks with ObservationUnavailable when MIG observation failed (always-precondition)", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-obs": "true"}
		seedNvidiaNode("nv-obs-node", sel, true, a30Device("disabled", "Disabled", "Disabled", "ecc uncorrectable error"))
		DeferCleanup(func() { cleanupNvidia("nv-obs-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-obs-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-obs-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-obs-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseWaitingForDrain))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-obs-acpp"}, &got)).To(Succeed())
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonObservationUnavailable))
	})

	// MIG mode 가 이미 Enabled 인 노드에서 cordon 주체(ensureMigModeEnabled)를 건너뛰면, GI apply
	// 전제인 cordon 을 아무도 하지 않아 정책이 오지 않는 외부 주체를 기다리며
	// NodeNotCordoned/WaitingForDrain 을 영원히 반복한다. 정책이 저널(CordonedByPolicy)을 먼저
	// 영속한 뒤 스스로 cordon 하고, 남은 차단 이유만 정직하게 보고해야 한다.
	It("cordons the node itself for a GI apply when MIG mode is already enabled", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-cordon": "true"}
		// MIG mode 는 이미 Enabled(GI 없음) — mode 가 Disabled 면 mode enable
		// 오케스트레이션이 먼저 노드를 cordon 해 버려 이 결함 자체를 볼 수 없다.
		seedNvidiaNode("nv-cordon-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-cordon-node") })
		// GPU 점유 pod — cordon 은 배출까지 하지 않으므로 apply 는 계속 막히고, 덕분에 "정책이
		// 잠근 cordon" 을 관측할 수 있다(성공 종점은 곧바로 uncordon 한다).
		pod := gpuPodOn("nv-cordon-pod", "nv-cordon-node")
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod, client.GracePeriodSeconds(0)) })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-cordon-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		events := record.NewFakeRecorder(10)
		r.Recorder = events
		_, err := r.Reconcile(ctx, reconcileReq("nv-cordon-acpp"))
		Expect(err).NotTo(HaveOccurred())

		// 노드가 조용히 잠기면 안 된다 — 대기에 상한이 없으므로 운영자가 볼 신호가 있어야 한다.
		Expect(events.Events).To(Receive(ContainSubstring("NodeCordoned")))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-cordon-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseWaitingForDrain))
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonQuiesceRequired),
			"cordon 은 정책이 직접 한다 — 남을 수 있는 차단 이유는 배출 미완뿐이다")
		Expect(got.Status.ApplyRecords).To(HaveLen(1))
		Expect(got.Status.ApplyRecords[0].CordonedByPolicy).To(BeTrue(),
			"cordon 소유권은 노드 patch 이전에 저널돼야 한다(persist-before-mutate)")
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseQuiescing))

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-cordon-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeTrue(), "operator must cordon the node itself for a GI apply")

		By("배출이 끝나면 apply 가 진행되고, 종점에서 정책이 잠근 cordon 만 풀린다")
		Expect(k8sClient.Delete(ctx, pod, client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-cordon-acpp"))
			var g npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-cordon-acpp"}, &g)
			return g.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-cordon-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeFalse(), "복원 choke-point 는 정책이 잠근 cordon 을 반드시 풀어야 한다")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-cordon-acpp"}, &got)).To(Succeed())
		Expect(got.Status.ApplyRecords[0].CordonedByPolicy).To(BeFalse())
	})

	// liveVerifier 는 180s 미수렴을 에러가 아니라 (AllocatableConverged:false, nil) 로 돌려준다
	// (failVerifier 와 같은 모양). 그러면 rollback 종점의 runErr 이 nil 이라 rate-limited 재시도도
	// 안 걸리고, 복원 choke-point 가 노드를 풀어 준 뒤 다음 pass 가 다시 cordon → 재-Apply → 재-Verify
	// → 재-Rollback 으로 돈다. 매 사이클이 실물 GI 생성·파괴 + device-plugin 재시작이므로, 같은
	// generation 의 rollback 저널은 terminal 이어야 한다(입력이 그대로면 결과도 그대로다).
	It("does not re-apply after a rollback for the same generation (no partition thrash loop)", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-thrash": "true"}
		// uncordoned — 정책이 스스로 cordon 하고, 종점에서 풀어 준다(루프의 재-cordon 구간까지 포함).
		seedNvidiaNode("nv-thrash-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-thrash-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-thrash-acpp", sel))).To(Succeed())
		steps := 0
		r := nvidiaReconciler()
		r.Verifier = failVerifier{} // VerifyAllocatable → (미수렴, nil) = 라이브 180s 초과와 같은 반환
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return countingExec{&steps} }

		_, err := r.Reconcile(ctx, reconcileReq("nv-thrash-acpp")) // apply → 미수렴 → rollback
		Expect(err).NotTo(HaveOccurred())
		firstPass := steps
		Expect(firstPass).To(BeNumerically(">", 0), "첫 pass 는 실제로 GI apply/rollback 을 실행해야 한다")

		By("spec 이 그대로인 다음 pass 들은 하드웨어를 다시 건드리지 않아야 한다")
		for i := 0; i < 5; i++ {
			_, err := r.Reconcile(ctx, reconcileReq("nv-thrash-acpp"))
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(steps).To(Equal(firstPass),
			"같은 generation 의 rollback 이후 재적용은 GI 생성·파괴 왕복을 무한 반복한다")

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-thrash-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseFailed), "terminal 이어야 재시도 폭주가 멈춘다")
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).To(Equal("ApplyRolledBack"))
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseRolledBack))

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-thrash-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeFalse(), "terminal 종점은 정책이 잠근 노드를 풀어 준다")
	})

	It("rejects external unmanaged identical geometry with ExistingMigConfiguration", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-external": "true"}
		// 이미 1g.6gb x4 로 Enabled — 이 ACPP 가 만든 것이 아님(ApplyRecord 없음).
		seedNvidiaNode("nv-external-node", sel, true, a30Device("1g.6gb x4", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-external-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-external-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-external-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-external-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseFailed))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-external-acpp"}, &got)).To(Succeed())
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondValidated)).To(Equal(npuv1alpha1.ReasonExistingMigConfiguration))
	})

	// cordon 하지 않기로 한 노드(관리 밖 GI 존재)는 진짜 차단 사유를 보고해야 한다. 판정 순서가
	// !NodeCordoned 우선이면 "cordon 해라" 라고 적히는데, 정책은 그 노드를 절대 cordon 하지 않으므로
	// 도달 불가능한 행동을 지시하는 거짓말이 된다.
	It("reports the real blocker (not NodeNotCordoned) on an uncordoned node with unmanaged GPU instances", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-unsafe": "true"}
		// mode Enabled + 관리 밖 GI(요청과 다른 geometry) + uncordoned → 정책은 cordon 하지 않는다.
		seedNvidiaNode("nv-unsafe-node", sel, false, a30Device("2g.12gb x2", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-unsafe-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-unsafe-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("nv-unsafe-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var unsafe npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-unsafe-acpp"}, &unsafe)).To(Succeed())
		Expect(condReasonOf(&unsafe, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonExistingMigConfiguration))
		Expect(condReasonOf(&unsafe, npuv1alpha1.ACPPCondApplied)).NotTo(Equal(npuv1alpha1.ReasonNodeNotCordoned),
			"정책이 cordon 하지 않기로 한 노드에 NodeNotCordoned 를 적으면 도달 불가능한 조치를 지시한다")

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-unsafe-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeFalse(), "진행할 수 없는 노드는 잠그지 않는다")
	})

	// MIG mode 미enable 은 더 이상 "차단하고 운영자를 기다리는" 종점이 아니다 —
	// operator 가 quiesce → -mig 1 → (pending) → 재부팅 순으로 직접 전환한다.
	It("orchestrates mode enable instead of blocking when the target GPU MIG mode is disabled", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-notenabled": "true"}
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1") // 재부팅 Job 이 쓰는 nsenter 이미지
		seedNvidiaNode("nv-notenabled-node", sel, false, a30Device("disabled", "Disabled", "Disabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-notenabled-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-notenabled-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-notenabled-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-notenabled-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseApplying))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-notenabled-acpp"}, &got)).To(Succeed())
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonMigModeNotEnabled))
		Expect(got.Status.ApplyRecords).To(HaveLen(1))
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseModeEnabling))
		Expect(got.Status.ApplyRecords[0].CordonedByPolicy).To(BeTrue())

		By("quiescing the node before touching -mig 1")
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-notenabled-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeTrue())

		By("requesting a reboot once the pending state appears")
		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-notenabled-node"}, &ndr)).To(Succeed())
		ndr.Status.Devices = []npuv1alpha1.DeviceEntry{a30Device("disabled", "Disabled", "Enabled", "")}
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())
		_, rerr := r.Reconcile(ctx, reconcileReq("nv-notenabled-acpp"))
		Expect(rerr).NotTo(HaveOccurred())

		var jobs batchv1.JobList
		Expect(k8sClient.List(ctx, &jobs)).To(Succeed())
		var rebootJobs int
		for i := range jobs.Items {
			if jobs.Items[i].Labels["app.kubernetes.io/component"] == "node-reboot" {
				rebootJobs++
				Expect(jobs.Items[i].OwnerReferences[0].Kind).To(Equal("AcceleratorPartitionPolicy"))
			}
		}
		Expect(rebootJobs).To(Equal(1))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-notenabled-acpp"}, &got)).To(Succeed())
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseRebootWaiting))
	})

	It("reaches Ready on a fresh cordoned apply (MIG pre-enabled) and excludes the partitioned GPU from ExpectedFullGPUCount", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-happy": "true"}
		// MIG mode 는 사전 enable(Enabled) + GI 없음(geometry "").
		seedNvidiaNode("nv-happy-node", sel, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-happy-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-happy-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-happy-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-happy-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-happy-acpp"}, &got)).To(Succeed())
		Expect(got.Status.ApplyRecords).To(HaveLen(1))
		rec := got.Status.ApplyRecords[0]
		Expect(rec.MigPhase).To(Equal(npuv1alpha1.MigPhaseReady))
		Expect(rec.BaselineGPUCount).To(Equal(int32(1)))
		Expect(rec.ExpectedMigCount).To(Equal(int32(4)))
		// 이 노드의 유일한 GPU 가 조각 대상이므로 apply 후 온전한 GPU 광고는 0 이다. baseline(=1)을
		// 그대로 기대값으로 쓰면 mixed device-plugin 이 절대 낼 수 없는 수를 기다리게 되어, A30
		// 파티션이 매번 rollback 된다.
		Expect(rec.ExpectedFullGPUCount).To(Equal(int32(0)), "조각낸 GPU 는 온전한 GPU 로 광고되지 않는다")
		Expect(rec.OwnerUID).To(Equal(string(got.UID)))
	})

	It("stays Ready on a reused reconcile (managed no-diff) without requiring cordon", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-reuse": "true"}
		// MIG mode 사전 enable(Enabled) + GI 없음.
		seedNvidiaNode("nv-reuse-node", sel, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-reuse-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-reuse-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-reuse-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-reuse-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		By("hardware now reports the applied geometry; node uncordoned; force re-verify")
		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-reuse-node"}, &ndr)).To(Succeed())
		ndr.Status.Devices[0].MigModeCurrent = "Enabled"
		ndr.Status.Devices[0].MigCurrentGeometry = "1g.6gb x4"
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())

		var node corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-reuse-node"}, &node)).To(Succeed())
		node.Spec.Unschedulable = false // 관리 no-diff 경로는 cordon 을 요구하지 않아야 한다
		Expect(k8sClient.Update(ctx, &node)).To(Succeed())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-reuse-acpp"}, &got)).To(Succeed())
		got.Status.ObservedGeneration = 0 // shouldReverify 강제 → managed no-diff 경로 진입
		Expect(k8sClient.Status().Update(ctx, &got)).To(Succeed())

		By("reconciling again — must stay Ready via managed no-diff (no cordon demanded)")
		Consistently(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-reuse-acpp"))
			var g npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-reuse-acpp"}, &g)
			return g.Status.Phase
		}, "2s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))
	})

	// 크래시 복구 continuation: hardware mutation 이후(MigPhase=Verifying) 크래시로 재시작 →
	// geometry 는 이미 일치(no-diff)하고 allocatable 은 이미 drop 된 상태. computeBaseline 을 다시 돌리면
	// baseline(0) < targets(1) → BaselineInconsistent(Failed terminal deadlock). continuation 분기가
	// baseline 재계산 없이 영속 record 로 tail(Verify)만 재개해 Ready 로 회복해야 한다.
	It("recovers a crashed in-flight apply (MigPhase=Verifying) to Ready without recomputing baseline", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-recover": "true"}
		// hardware 는 이미 1g.6gb x4 Enabled(mutation 완료). allocatable 은 아래에서 0 으로 drop.
		seedNvidiaNode("nv-recover-node", sel, true, a30Device("1g.6gb x4", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-recover-node") })

		By("dropping nvidia.com/gpu allocatable to 0 (MIG enabled) — computeBaseline would fail here")
		var node corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-recover-node"}, &node)).To(Succeed())
		node.Status.Allocatable = corev1.ResourceList{
			corev1.ResourceName("nvidia.com/gpu"): *resource.NewQuantity(0, resource.DecimalSI),
		}
		Expect(k8sClient.Status().Update(ctx, &node)).To(Succeed())

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-recover-acpp", sel))).To(Succeed())

		By("seeding a durable ApplyRecord as if we crashed mid-Verify (our own OwnerUID)")
		var created npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-recover-acpp"}, &created)).To(Succeed())
		created.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName:             "nv-recover-node",
			GPUPCIs:              []string{"0000:41:00.0"},
			OwnerUID:             string(created.UID),
			BaselineGPUCount:     1,
			ExpectedMigCount:     4,
			ExpectedFullGPUCount: 0,
			Profile:              "1g.6gb",
			Count:                4,
			Generation:           created.Generation,
			MigPhase:             npuv1alpha1.MigPhaseVerifying,
		}}
		Expect(k8sClient.Status().Update(ctx, &created)).To(Succeed())

		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-recover-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-recover-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-recover-acpp"}, &got)).To(Succeed())
		Expect(got.Status.ApplyRecords).To(HaveLen(1))
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseReady))
		// BaselineInconsistent 로 새지 않았음을 명시적으로 확인(continuation 이 computeBaseline 을 우회).
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).NotTo(Equal(npuv1alpha1.ReasonBaselineInconsistent))
	})

	// operator-driven 관측이 per-PCI Err 를 내면(NDR 은 깨끗해도) fail-closed 로 ObservationUnavailable.
	// 이게 관측(NDR 아님)이 진실 소스임을 증명한다.
	It("blocks with ObservationUnavailable when the observer returns an Err observation (fail-closed)", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-obserr": "true"}
		// NDR 은 깨끗한 Disabled — 관측이 아니었다면 apply 로 진행됐을 상태.
		seedNvidiaNode("nv-obserr-node", sel, true, a30Device("disabled", "Disabled", "Disabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-obserr-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-obserr-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		// 관측기가 PCI 에 대해 Err 관측을 직접 반환하도록 override.
		r.NvidiaObserverFactory = func(client.Client) nvidia.Observer {
			return fakeNvidiaObserver{override: []nvidia.Observation{{
				PCI: nvPCI, ModeCurrent: "Disabled", ModePending: "Disabled",
				Geometry: "disabled", LgipOutput: lgipA30Test, Err: "xid 79 fell off the bus",
			}}}
		}
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-obserr-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-obserr-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseWaitingForDrain))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-obserr-acpp"}, &got)).To(Succeed())
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonObservationUnavailable))
	})
})

// nvPCI 는 a30Device 가 쓰는 고정 PCI(snapshot GPUPCIs·NDR 매칭 공용).
const nvPCI = "0000:41:00.0"

// mkFinalizedNvidiaACPP 는 finalizer 를 미리 붙인 nvidia ACPP 를 만들고 그 UID 를 반환한다
// (삭제 요청 후에도 finalizer 로 잔존 → handleNvidiaDeletion 이 돌 수 있게 한다).
func mkFinalizedNvidiaACPP(name string, sel map[string]string) string {
	acpp := mkNvidiaACPP(name, sel)
	acpp.Finalizers = []string{acppFinalizer}
	Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
	var got npuv1alpha1.AcceleratorPartitionPolicy
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got)).To(Succeed())
	return string(got.UID)
}

// setNodeOwner 는 노드 migOwnerAnnotation 을 지정 uid 로 찍는다(owner-lock 시뮬레이션).
func setNodeOwner(node, uid string) {
	var n corev1.Node
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
	if n.Annotations == nil {
		n.Annotations = map[string]string{}
	}
	n.Annotations[migOwnerAnnotation] = uid
	Expect(k8sClient.Update(ctx, &n)).To(Succeed())
}

// seedApplyRecord 는 삭제 snapshot 으로 쓸 단일 ApplyRecord 를 영속한다.
func seedApplyRecord(name string, rec npuv1alpha1.ApplyRecord) {
	var acpp npuv1alpha1.AcceleratorPartitionPolicy
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &acpp)).To(Succeed())
	acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{rec}
	Expect(k8sClient.Status().Update(ctx, &acpp)).To(Succeed())
}

// snapshotRec 는 nv 삭제 테스트 공용 snapshot(1g.6gb×4, baseline 1) 을 phase/owner 만 바꿔 만든다.
func snapshotRec(node, ownerUID, phase string) npuv1alpha1.ApplyRecord {
	return npuv1alpha1.ApplyRecord{
		NodeName: node, GPUPCIs: []string{nvPCI}, OwnerUID: ownerUID,
		BaselineGPUCount: 1, ExpectedMigCount: 4, ExpectedFullGPUCount: 0,
		Profile: "1g.6gb", Count: 4, Generation: 1, MigPhase: phase,
	}
}

var _ = Describe("ACPP nvidia MIG deletion (snapshot rollback)", func() {
	It("rolls back snapshot PCIs and removes finalizer once MIG disabled + gpu allocatable restored to baseline", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-ok": "true"}
		// 복원 = MIG Enabled + GI 없음(geometry "") + allocatable=1(=baseline) → post-rollback assertions pass.
		// uncordoned 로 시드한다 — 성공한 apply 는 종점에서 노드를 되돌려 놓으므로 실제 운영의 삭제는
		// 항상 이 상태에서 시작한다. 한 Reconcile 안에서 cordon 취득→rollback→uncordon 이 돈다.
		seedNvidiaNode("nv-del-ok-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
		setNodeGPUAllocatable("nv-del-ok-node", 0)
		DeferCleanup(func() { cleanupNvidia("nv-del-ok-node") })

		uid := mkFinalizedNvidiaACPP("nv-del-ok-acpp", sel)
		setNodeOwner("nv-del-ok-node", uid)
		seedApplyRecord("nv-del-ok-acpp", snapshotRec("nv-del-ok-node", uid, npuv1alpha1.MigPhaseReady))

		var acpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-ok-acpp"}, &acpp)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("nv-del-ok-acpp"))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-ok-acpp"}, &got))
		}, "5s", "200ms").Should(BeTrue(), "finalizer removed after verified restore → object gone")

		// owner lock 해제 확인.
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-ok-node"}, &n)).To(Succeed())
		_, has := n.Annotations[migOwnerAnnotation]
		Expect(has).To(BeFalse(), "owner lock released after cleanup")
		Expect(n.Spec.Unschedulable).To(BeFalse(), "삭제가 취득한 cordon 은 완주 후 되돌려져야 한다")
	})

	It("retains finalizer when gpu allocatable is NOT restored to baseline", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-fail": "true"}
		// MIG Enabled + GI 없음(mode/geometry assert 통과) 이지만 allocatable 미복원으로 baseline assert 실패.
		seedNvidiaNode("nv-del-fail-node", sel, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-del-fail-node") })

		By("nvidia.com/gpu 를 1 로 남겨 둠 — MIG Enabled 노드가 낼 수 없는 값이라 복원 확인이 실패해야 한다")
		// 기대값은 ExpectedFullGPUCount(=0, MIG 대상이 아닌 GPU 수)다. mode 가 Enabled 인데
		// full GPU 가 광고되고 있다는 것은 회수가 끝나지 않았다는 뜻이므로 finalizer 를 쥔다.
		setNodeGPUAllocatable("nv-del-fail-node", 1)

		uid := mkFinalizedNvidiaACPP("nv-del-fail-acpp", sel)
		setNodeOwner("nv-del-fail-node", uid)
		seedApplyRecord("nv-del-fail-acpp", snapshotRec("nv-del-fail-node", uid, npuv1alpha1.MigPhaseReady))

		var acpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-fail-acpp"}, &acpp)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("nv-del-fail-acpp"))
		Expect(err).To(HaveOccurred(), "unverified restore must error → finalizer retained")
		Consistently(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-fail-acpp"}, &got))
		}, "1s", "200ms").Should(BeFalse(), "object must still exist (finalizer retained)")
	})

	It("blocks cleanup (CleanupBlocked) when hardware changed but owner lock is lost", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-blocked": "true"}
		// 하드웨어에 GI 가 여전히 존재(geometry "1g.6gb", 미복원) 이어야 진짜 conflict → 차단.
		// 복원(Enabled+GI 없음) 이면 restore-before-block 규칙에 따라 skip 되므로 GI 존재로 시드한다.
		seedNvidiaNode("nv-del-blocked-node", sel, true, a30Device("1g.6gb", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-del-blocked-node") })

		uid := mkFinalizedNvidiaACPP("nv-del-blocked-acpp", sel)
		setNodeOwner("nv-del-blocked-node", "someone-else-uid") // 소유권 상실
		seedApplyRecord("nv-del-blocked-acpp", snapshotRec("nv-del-blocked-node", uid, npuv1alpha1.MigPhaseApplied))

		var acpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-blocked-acpp"}, &acpp)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("nv-del-blocked-acpp"))
		Expect(err).To(HaveOccurred(), "ownership conflict with mutated hardware must block")

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-blocked-acpp"}, &got)).To(Succeed(), "object retained")
		Expect(got.Status.ApplyRecords).To(HaveLen(1))
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseCleanupBlocked))
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonOwnershipConflict))
	})

	It("safely skips a lock-lost record that never changed hardware (Prepared) and removes finalizer", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-skip": "true"}
		seedNvidiaNode("nv-del-skip-node", sel, true, a30Device("disabled", "Disabled", "Disabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-del-skip-node") })

		uid := mkFinalizedNvidiaACPP("nv-del-skip-acpp", sel)
		setNodeOwner("nv-del-skip-node", "someone-else-uid")
		seedApplyRecord("nv-del-skip-acpp", snapshotRec("nv-del-skip-node", uid, npuv1alpha1.MigPhasePrepared))

		var acpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-skip-acpp"}, &acpp)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("nv-del-skip-acpp"))
		Expect(err).NotTo(HaveOccurred(), "no hardware change → safe skip, no block")
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-skip-acpp"}, &got))
		}, "5s", "200ms").Should(BeTrue(), "finalizer removed → object gone")

		// 타 소유자 lock 은 건드리지 않았어야 한다.
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-skip-node"}, &n)).To(Succeed())
		Expect(n.Annotations[migOwnerAnnotation]).To(Equal("someone-else-uid"), "other owner's lock untouched")
	})

	// 멀티-record 재진입: nodeA 를 먼저 정리(lock 해제)한 뒤 nodeB assert 실패로 requeue 되면,
	// 다음 pass 에서 lock 이 사라진 nodeA 를 재처리한다. 이미 baseline 이면 차단 대신 skip 해야 한다
	// (restore-before-block). nodeA 가 CleanupBlocked 로 오탐되지 않고 삭제가 완주함을 검증한다.
	It("does not falsely block an already-cleaned record when a sibling record requeues the deletion", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-multi": "true"}
		// nodeA: 처음부터 복원됨(Enabled+GI 없음) + allocatable=1(baseline) → assert 항상 통과(정리 완료 record).
		seedNvidiaNode("nv-del-multi-a", sel, true, a30Device("", "Enabled", "Enabled", ""))
		setNodeGPUAllocatable("nv-del-multi-a", 0)
		DeferCleanup(func() { cleanupNvidia("nv-del-multi-a") })
		// nodeB: pass 1 에는 GI 존재(미복원) → assert 실패로 err/requeue. pass 2 에 복원(GI 없음)으로 갱신.
		seedNvidiaNode("nv-del-multi-b", sel, true, a30Device("1g.6gb", "Enabled", "Enabled", ""))
		setNodeGPUAllocatable("nv-del-multi-b", 0)
		DeferCleanup(func() { cleanupNvidia("nv-del-multi-b") })

		uid := mkFinalizedNvidiaACPP("nv-del-multi-acpp", sel)
		setNodeOwner("nv-del-multi-a", uid)
		setNodeOwner("nv-del-multi-b", uid)
		// 두 record(둘 다 Ready·owned) 를 snapshot 으로 영속. nodeA 가 먼저 처리된다.
		var seed npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-multi-acpp"}, &seed)).To(Succeed())
		seed.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{
			snapshotRec("nv-del-multi-a", uid, npuv1alpha1.MigPhaseReady),
			snapshotRec("nv-del-multi-b", uid, npuv1alpha1.MigPhaseReady),
		}
		Expect(k8sClient.Status().Update(ctx, &seed)).To(Succeed())

		var acpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-multi-acpp"}, &acpp)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

		r := nvidiaReconciler()

		By("pass 1: nodeA cleaned (lock released) then nodeB assert fails → err/requeue")
		_, err := r.Reconcile(ctx, reconcileReq("nv-del-multi-acpp"))
		Expect(err).To(HaveOccurred(), "nodeB not yet restored → finalizer retained")

		// nodeA lock 해제됨(정리 완료), 그러나 record 는 여전히 Ready(차단 아님).
		var nA corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-multi-a"}, &nA)).To(Succeed())
		_, hasA := nA.Annotations[migOwnerAnnotation]
		Expect(hasA).To(BeFalse(), "nodeA lock released after its cleanup")
		var mid npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-multi-acpp"}, &mid)).To(Succeed())
		for _, rec := range mid.Status.ApplyRecords {
			Expect(rec.MigPhase).NotTo(Equal(npuv1alpha1.MigPhaseCleanupBlocked),
				"no record may be flipped to CleanupBlocked after pass 1")
		}

		By("pass 2: nodeB now restorable — cleaned nodeA must be skipped (restore-before-block), not blocked")
		var ndrB npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-multi-b"}, &ndrB)).To(Succeed())
		ndrB.Status.Devices[0] = a30Device("", "Enabled", "Enabled", "") // 복원 관측(Enabled+GI 없음)
		Expect(k8sClient.Status().Update(ctx, &ndrB)).To(Succeed())

		_, err = r.Reconcile(ctx, reconcileReq("nv-del-multi-acpp"))
		Expect(err).NotTo(HaveOccurred(), "both records now restored → deletion completes")
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-multi-acpp"}, &got))
		}, "5s", "200ms").Should(BeTrue(), "finalizer removed → object gone (nodeA never blocked)")
	})

	// owner-lock 은 Diff/Apply 이전에 선취득되나 ApplyRecord 는 mutation 직전에야 영속된다.
	// 그 사이 hardware-block(MigModeNotEnabled 등)으로 조기 반환되면 lock 만 남고 record 가 없다. 이 상태서
	// 삭제되면 record 순회만으로는 lock 을 못 지워 누수 → 재-ACPP 가 영구 TargetConflict. orphan lock 해제로 방지.
	It("releases an owner-lock held without any ApplyRecord (blocked before persist) and does not touch other owners", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-orphan": "true"}
		seedNvidiaNode("nv-del-orphan-node", sel, true, a30Device("", "Enabled", "Enabled", ""))
		setNodeGPUAllocatable("nv-del-orphan-node", 0)
		DeferCleanup(func() { cleanupNvidia("nv-del-orphan-node") })
		// 타 소유자 노드 — full-node 스캔이 UID 불일치로 건드리지 않아야 한다.
		seedNvidiaNode("nv-del-orphan-other", sel, true, a30Device("", "Enabled", "Enabled", ""))
		setNodeGPUAllocatable("nv-del-orphan-other", 0)
		DeferCleanup(func() { cleanupNvidia("nv-del-orphan-other") })

		uid := mkFinalizedNvidiaACPP("nv-del-orphan-acpp", sel)
		setNodeOwner("nv-del-orphan-node", uid) // lock 취득, seedApplyRecord 호출 안 함 → record 없음
		setNodeOwner("nv-del-orphan-other", "someone-else-uid")

		var acpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-orphan-acpp"}, &acpp)).To(Succeed())
		Expect(acpp.Status.ApplyRecords).To(BeEmpty(), "precondition: no ApplyRecord (blocked before persist)")
		Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("nv-del-orphan-acpp"))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-orphan-acpp"}, &got))
		}, "5s", "200ms").Should(BeTrue(), "finalizer removed → object gone")

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-orphan-node"}, &n)).To(Succeed())
		_, has := n.Annotations[migOwnerAnnotation]
		Expect(has).To(BeFalse(), "orphan owner-lock (no ApplyRecord) released on deletion")

		var other corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-orphan-other"}, &other)).To(Succeed())
		Expect(other.Annotations[migOwnerAnnotation]).To(Equal("someone-else-uid"), "other owner's lock untouched by orphan scan")
	})

	// 삭제 경로는 rollback 전제로 cordon 을 요구한다(assertNodeQuiesced). 성공한 apply 는 종점에서
	// 노드를 uncordon 하므로(restoreSchedulable), 삭제가 스스로 cordon 을 취득하지 않으면 그 뒤의
	// 모든 삭제가 "node not cordoned; unsafe to rollback MIG" 로 막혀 운영자의 수동 cordon 없이는
	// finalizer 가 영구히 남는다. 그래서 snapshot 을 손으로 시드하지 않고 실제 apply(→Ready→uncordon)
	// 를 태운 뒤 삭제해, 이 자가-cordon 경로를 실제로 거치게 한다.
	It("cordons the node itself to roll back MIG on deletion after a completed apply", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-cordon": "true"}
		// uncordoned + mode 이미 Enabled → apply 가 스스로 cordon 하고 Ready 종점에서 되돌린다.
		seedNvidiaNode("nv-del-cordon-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
		setNodeGPUAllocatable("nv-del-cordon-node", 0)
		DeferCleanup(func() { cleanupNvidia("nv-del-cordon-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("nv-del-cordon-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-del-cordon-acpp"))
			var g npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-acpp"}, &g)
			return g.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		// 전제 단정: 하드웨어는 바뀌었고(rollback 대상), 노드는 schedulable 이며 복원 소유권도 닫혀 있다.
		var applied npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-acpp"}, &applied)).To(Succeed())
		Expect(applied.Status.ApplyRecords).To(HaveLen(1))
		Expect(applied.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseReady),
			"precondition: hardware was actually mutated (rollback target)")
		Expect(applied.Status.ApplyRecords[0].CordonedByPolicy).To(BeFalse())
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeFalse(), "precondition: a completed apply leaves the node schedulable")

		By("pass 1: 복원 미확인(GI 잔존)으로 실패해도 삭제가 취득한 cordon 은 유지된다(멱등 재시도)")
		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-node"}, &ndr)).To(Succeed())
		ndr.Status.Devices[0] = a30Device("1g.6gb", "Enabled", "Enabled", "")
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &applied)).To(Succeed())

		_, err := r.Reconcile(ctx, reconcileReq("nv-del-cordon-acpp"))
		Expect(err).To(HaveOccurred(), "GI still present → unverified restore → finalizer retained")
		Expect(err.Error()).NotTo(ContainSubstring("unsafe to rollback MIG"),
			"cordon 은 삭제가 직접 취득한다 — 남을 수 있는 실패 이유는 복원 미확인뿐이다(삭제가 node not cordoned 로 막히던 증상이 재발하면 안 된다)")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeTrue(), "삭제 경로가 rollback 전제인 cordon 을 스스로 취득해야 한다")
		var mid npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-acpp"}, &mid)).To(Succeed())
		Expect(mid.Status.ApplyRecords[0].CordonedByPolicy).To(BeTrue(),
			"cordon 소유권은 노드 patch 이전에 영속돼야 한다(persist-before-mutate)")
		Expect(mid.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseReady),
			"삭제 경로는 rollback 근거(snapshot phase)를 Quiescing 으로 덮어써선 안 된다")

		By("pass 2: 복원이 관측되면 삭제가 완주하고, 우리가 잠근 노드도 되돌려진다")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-node"}, &ndr)).To(Succeed())
		ndr.Status.Devices[0] = a30Device("", "Enabled", "Enabled", "")
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())
		Eventually(func() bool {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-del-cordon-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-acpp"}, &got))
		}, "10s", "200ms").Should(BeTrue(), "삭제가 스스로 cordon 하고 완주해야 한다(운영자 수동 cordon 불필요)")

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-cordon-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeFalse(), "삭제가 잠근 cordon 은 복원돼야 한다(cordon 채로 방치 금지)")
		_, has := n.Annotations[migOwnerAnnotation]
		Expect(has).To(BeFalse(), "owner lock released after cleanup")
	})

	// 운영자가 미리 cordon 해 둔 노드(CordonedByPolicy=false)는 삭제가 소유권을 주장하지
	// 않으므로, 정리가 끝나도 uncordon 하지 않는다 — 정비 중인 노드를 정책이 되살리면 안 된다.
	It("does not uncordon a node that the operator had cordoned before deletion", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-del-precordon": "true"}
		seedNvidiaNode("nv-del-precordon-node", sel, true, a30Device("", "Enabled", "Enabled", ""))
		setNodeGPUAllocatable("nv-del-precordon-node", 0)
		DeferCleanup(func() { cleanupNvidia("nv-del-precordon-node") })

		uid := mkFinalizedNvidiaACPP("nv-del-precordon-acpp", sel)
		setNodeOwner("nv-del-precordon-node", uid)
		seedApplyRecord("nv-del-precordon-acpp", snapshotRec("nv-del-precordon-node", uid, npuv1alpha1.MigPhaseReady))

		var acpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-precordon-acpp"}, &acpp)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("nv-del-precordon-acpp"))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			var got npuv1alpha1.AcceleratorPartitionPolicy
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-precordon-acpp"}, &got))
		}, "5s", "200ms").Should(BeTrue(), "finalizer removed → object gone")

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-del-precordon-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeTrue(), "운영자 cordon 은 삭제가 되살리지 않는다")
	})
})

// rollback 이 assertNodeQuiesced/Rollback/assertMigEmptyAndBaseline 중 하나에서 막히면 신호 없이
// requeue 만 되는 대신, Warning 이벤트로 원인을 남긴다.
var _ = Describe("deletion rollback failure emits a Warning event", func() {
	It("emits DeletionRollbackBlocked without touching MigPhase when restore verification fails", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/m1-test": "true"}
		// 같은 준비 절차 재사용: uncordoned + mode 이미 Enabled 상태에서 apply 를 Ready 까지 태운다.
		seedNvidiaNode("m1-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("m1-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("m1-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("m1-acpp"))
			var g npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "m1-acpp"}, &g)
			return g.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var applied npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "m1-acpp"}, &applied)).To(Succeed())

		// GI 를 잔존시켜 assertMigEmptyAndBaseline 이 복원 미확인으로 막히게 한다(앞의 삭제 스펙과 동일).
		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "m1-node"}, &ndr)).To(Succeed())
		ndr.Status.Devices[0] = a30Device("1g.6gb", "Enabled", "Enabled", "")
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())

		events := record.NewFakeRecorder(10)
		r.Recorder = events
		Expect(k8sClient.Delete(ctx, &applied)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("m1-acpp"))
		Expect(err).To(HaveOccurred(), "GI still present → unverified restore → finalizer retained")

		// cordonForRollback 도 NodeCordoned 이벤트를 먼저 남기므로, 큐에서 매치될 때까지 드레인한다.
		Eventually(events.Events).Should(Receive(ContainSubstring("DeletionRollbackBlocked")))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "m1-acpp"}, &got)).To(Succeed())
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseReady),
			"이벤트만 남기고 삭제 저널(snapshot phase) 은 건드리지 않아야 한다")
	})

	// cordonForRollback 자체가 실패하면(예: nodeOwnerUID 조회와 cordon 조회 사이에 노드가 삭제되는
	// 레이스) 다른 실패 경로와 달리 이벤트 없이 그냥 return err 하면, 운영자에게는 이유 없는
	// Terminating 만 보인다.
	It("emits DeletionRollbackBlocked when cordonForRollback itself fails", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/m2-test": "true"}
		seedNvidiaNode("m2-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("m2-node") })

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("m2-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("m2-acpp"))
			var g npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "m2-acpp"}, &g)
			return g.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var applied npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "m2-acpp"}, &applied)).To(Succeed())

		events := record.NewFakeRecorder(10)
		r.Recorder = events
		// nodeOwnerUID(소유권 확인)의 첫 조회는 통과시키고, cordonForRollback 의 두 번째 조회부터
		// NotFound 를 주입한다 — 같은 노드를 순서대로 두 번 조회하는 실제 경로에서 cordon 실패만
		// 격리해 재현한다.
		r.Client = &cordonFailClient{Client: r.Client, node: "m2-node"}

		Expect(k8sClient.Delete(ctx, &applied)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("m2-acpp"))
		Expect(err).To(HaveOccurred(), "cordon 실패 시 삭제는 finalizer 를 유지해야 한다")

		Eventually(events.Events).Should(Receive(ContainSubstring("DeletionRollbackBlocked")),
			"cordon 실패 시 Warning 이벤트가 없다")

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "m2-acpp"}, &got)).To(Succeed())
		Expect(got.Status.ApplyRecords[0].MigPhase).To(Equal(npuv1alpha1.MigPhaseReady),
			"이벤트만 남기고 삭제 저널(snapshot phase) 은 건드리지 않아야 한다")
	})
})

// cordonFailClient 는 지정된 노드에 대한 두 번째 이후 Get 호출부터 NotFound 를 주입한다 —
// nodeOwnerUID(첫 Get)는 통과시키고 cordonForRollback(두 번째 Get)만 실패시켜, 두 함수가
// 같은 노드를 순서대로 조회하는 실제 경로에서 cordon 실패만 재현한다(두 조회 사이 노드 삭제 레이스).
type cordonFailClient struct {
	client.Client
	node  string
	calls int
}

func (c *cordonFailClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Node); ok && key.Name == c.node {
		c.calls++
		if c.calls > 1 {
			return apierrors.NewNotFound(corev1.Resource("nodes"), key.Name)
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// layout 없는 순수 시분할(sharing-only) 정책은 하드웨어를 바꾸지 않으므로(DP 설정만) cordon·MIG
// 경로 없이 Ready 에 도달해야 한다.
var _ = Describe("ACPP sharing-only (no layout)", func() {
	It("accepts a pure time-slicing policy and reaches Ready without cordoning the node", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-sharing-only": "true"}
		// A2 류 비-MIG GPU — MIG mode 는 N/A, lgip profile 없음 → 파티션 apply 미지원 노드.
		seedNvidiaNode("nv-sharing-only-node", sel, false, npuv1alpha1.DeviceEntry{
			Vendor: "nvidia", Model: "NVIDIA A2", Count: 1,
			DriverLoaded: true, DriverVersion: "535.104.05", PCIeAddress: "0000:86:00.0",
			// 관측기가 정규화한 뒤의 값이다("N/A" 원문이 아니라 "NA") — 프로덕션이 만들 수 있는 값으로 둔다.
			MigModeCurrent: migModeNA, MigModePending: migModeNA,
		})
		DeferCleanup(func() { cleanupNvidia("nv-sharing-only-node") })
		// sharing 배선 대상 device-plugin DS. 이 노드는 mig-active 라벨이 없다(MIG 없는 A2) —
		// DevicePluginTargetForNode 가 flat DS 를 대상화한다.
		dp := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameFlat, Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nvidia-device-plugin", Image: "x"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, dp)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dp) })
		// wipeACPPs 는 finalizer 를 강제 제거해 삭제 rollback 이 돌지 않는다 — backend 가 만든 sharing
		// ConfigMap 이 spec 밖으로 새면 다음 sharing spec 이 타 소유 CM 충돌로 오염된다.
		DeferCleanup(func() {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: nvidia.SharingConfigMapNameFlat, Namespace: "kube-system"}}
			_ = k8sClient.Delete(ctx, cm)
		})

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "nv-sharing-only-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "nvidia",
				DeletionPolicy: "Retain",
				Sharing: &npuv1alpha1.SharingSpec{
					Mode:        npuv1alpha1.SharingModeTimeSliced,
					TimeSlicing: &npuv1alpha1.TimeSlicingSpec{Replicas: 4},
				},
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed(),
			"layout 없는 sharing-only 정책은 CRD 스키마를 통과해야 한다")

		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-sharing-only-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-only-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-only-acpp"}, &got)).To(Succeed())
		validated := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondValidated)
		Expect(validated).NotTo(BeNil())
		Expect(validated.Status).To(Equal(metav1.ConditionTrue))
		// 공유 저널이 영속돼야 한다(원복 근거). MIG 저널 phase 부재 = MIG 경로 미진입 증거.
		Expect(got.Status.ApplyRecords).To(HaveLen(1))
		Expect(got.Status.ApplyRecords[0].SharingMode).To(Equal(npuv1alpha1.SharingModeTimeSliced))
		Expect(got.Status.ApplyRecords[0].SharingReplicas).To(Equal(int32(4)))
		Expect(got.Status.ApplyRecords[0].MigPhase).To(BeEmpty())

		// sharing ConfigMap 이 full-GPU 리소스로 렌더돼야 하고, 노드는 cordon 되지 않아야 한다.
		var cm corev1.ConfigMap
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nvidia.SharingConfigMapNameFlat, Namespace: "kube-system"}, &cm)).To(Succeed())
		Expect(cm.Data[nvidia.SharingConfigKey]).To(ContainSubstring("nvidia.com/gpu"))
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-only-node"}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeFalse(), "sharing-only 는 cordon 이 필요 없다(DP 재시작만 필요)")
	})

	// 공유가 이미 적용된 뒤의 라이브 allocatable 은 배수된 광고량이다 — 그것을 base 로 다시 읽으면
	// replicas 편집 재진입마다 expected 가 배수의 배수(4×8=32)로 부풀어 verify 실패 → rollback →
	// 재진입 무한 flap 이 된다. base 는 첫 적용 때 저널에 영속한 물리 수여야 한다.
	It("converges to the new replicas when a Ready sharing-only policy is edited", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		sel := map[string]string{"kcloud.ai/nv-sharing-edit": "true"}
		seedNvidiaNode("nv-sharing-edit-node", sel, false, npuv1alpha1.DeviceEntry{
			Vendor: "nvidia", Model: "NVIDIA A2", Count: 1,
			DriverLoaded: true, DriverVersion: "535.104.05", PCIeAddress: "0000:86:00.0",
			// 관측기가 정규화한 뒤의 값이다("N/A" 원문이 아니라 "NA") — 프로덕션이 만들 수 있는 값으로 둔다.
			MigModeCurrent: migModeNA, MigModePending: migModeNA,
		})
		DeferCleanup(func() { cleanupNvidia("nv-sharing-edit-node") })
		// 이 노드는 mig-active 라벨이 없다(MIG 없는 A2) — flat DS 를 대상화한다.
		dp := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameFlat, Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nvidia-device-plugin", Image: "x"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, dp)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, dp) })
		// wipeACPPs 는 finalizer 를 강제 제거해 삭제 rollback 이 돌지 않는다 — backend 가 만든 sharing
		// ConfigMap 이 spec 밖으로 새면 다음 sharing spec 이 타 소유 CM 충돌로 오염된다.
		DeferCleanup(func() {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: nvidia.SharingConfigMapNameFlat, Namespace: "kube-system"}}
			_ = k8sClient.Delete(ctx, cm)
		})

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "nv-sharing-edit-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "nvidia",
				DeletionPolicy: "Retain",
				Sharing: &npuv1alpha1.SharingSpec{
					Mode:        npuv1alpha1.SharingModeTimeSliced,
					TimeSlicing: &npuv1alpha1.TimeSlicingSpec{Replicas: 4},
				},
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		// 물리 1 GPU × replicas 4 → DP 는 4 를 광고한다(verifier stub 이 그 사실을 표현).
		r1 := nvidiaReconciler()
		r1.Verifier = allocVerifier{alloc: map[string]int32{"nvidia.com/gpu": 4}}
		Eventually(func() string {
			_, _ = r1.Reconcile(ctx, reconcileReq("nv-sharing-edit-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-edit-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		// 실 DP 처럼 노드 광고를 배수된 값(4)으로 갱신한다 — 재진입이 이 값을 base 로 읽으면 안 된다.
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-edit-node"}, &n)).To(Succeed())
		n.Status.Allocatable[corev1.ResourceName("nvidia.com/gpu")] = *resource.NewQuantity(4, resource.DecimalSI)
		Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())

		By("replicas 4 → 8 로 편집")
		var cur npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-edit-acpp"}, &cur)).To(Succeed())
		cur.Spec.Sharing.TimeSlicing.Replicas = 8
		Expect(k8sClient.Update(ctx, &cur)).To(Succeed())

		// 물리 1 × 새 replicas 8 → DP 광고는 8 이 된다. 올바른 expected 는 8 — base 를 배수된
		// 광고량(4)에서 읽으면 expected 32 가 되어 영원히 수렴하지 못한다.
		r2 := nvidiaReconciler()
		r2.Verifier = allocVerifier{alloc: map[string]int32{"nvidia.com/gpu": 8}}
		Eventually(func() string {
			_, _ = r2.Reconcile(ctx, reconcileReq("nv-sharing-edit-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-edit-acpp"}, &got)
			rec := getApplyRecord(&got, "nv-sharing-edit-node")
			return fmt.Sprintf("%s/replicas=%d", got.Status.Phase, rec.SharingReplicas)
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady+"/replicas=8"),
			"편집 재진입은 저널의 물리 base 로 새 replicas 에 수렴해야 한다(배수된 광고량 재배수 금지)")

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-sharing-edit-acpp"}, &got)).To(Succeed())
		rec := getApplyRecord(&got, "nv-sharing-edit-node")
		Expect(rec.BaselineGPUCount).To(Equal(int32(1)), "물리 수가 저널에 영속돼야 재진입이 재관측 없이 base 를 안다")
	})

	It("rejects a policy whose layout and sharing are both empty", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "nv-empty-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   map[string]string{"kcloud.ai/nv-empty": "true"},
				Vendor:         "nvidia",
				Layout:         []npuv1alpha1.PartitionLayout{},
				DeletionPolicy: "Retain",
			},
		}
		err := k8sClient.Create(ctx, acpp)
		Expect(err).To(HaveOccurred(), "layout 도 sharing 도 없는 정책은 아무것도 요청하지 않으므로 admission 에서 거부돼야 한다")
		Expect(err.Error()).To(ContainSubstring("cannot both be empty"))
	})
})

// d5A2PCI 는 비-MIG GPU(A2)의 PCI 다 — 같은 노드의 A30(nvPCI)과 구분한다.
const d5A2PCI = "0000:86:00.0"

// d5A2Device 는 MIG 하드웨어가 없는 A2 다(mode N/A, lgip 없음 → MIG target 아님, MPS 가능).
func d5A2Device() npuv1alpha1.DeviceEntry {
	return npuv1alpha1.DeviceEntry{
		Vendor: "nvidia", Model: "NVIDIA A2", Count: 1,
		DriverLoaded: true, DriverVersion: "535.104.05", PCIeAddress: d5A2PCI,
		// 관측기가 정규화한 뒤의 값이다("N/A" 원문이 아니라 "NA") — 프로덕션이 만들 수 있는 값으로 둔다.
		MigModeCurrent: migModeNA, MigModePending: migModeNA,
	}
}

// d5MpsACPP 는 A2 를 겨냥한 sharing-only MPS 정책이다(장치 셀렉터가 CRD 에 없어 노드 단위로만 겨눈다).
func d5MpsACPP(name string, sel map[string]string) *npuv1alpha1.AcceleratorPartitionPolicy {
	return &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			NodeSelector:   sel,
			Vendor:         "nvidia",
			DeletionPolicy: "Retain",
			Sharing:        &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeMPS, MPS: &npuv1alpha1.MPSSpec{Replicas: 4}},
		},
	}
}

// deleteMpsDaemon 은 reconcile 이 만든 mps control daemon 을 spec 밖으로 새지 않게 지운다.
func deleteMpsDaemon() {
	_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: mpsControlDaemonDSName, Namespace: "kube-system"}})
}

// MPS 게이트가 "이 정책이 실제로 다루는 장치" 대신 "이 노드의 같은 벤더 전체 장치" 를 검사하면,
// MIG 가 켜진 형제 장치(A30 — 같은 노드의 다른 ACPP 가 소유·Ready) 때문에 A2 만 겨냥한 MPS 정책이
// discovery 직후 거부되어 ensureMpsControlDaemon 이 전혀 호출되지 못한다 — 다중 장치 노드에서
// 노드 전체와 개별 장치를 혼동하는 결함이다.
var _ = Describe("ACPP MPS with a MIG-owned sibling device", func() {
	It("judges MPS capability only on devices no other policy owns", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		DeferCleanup(deleteMpsDaemon)
		sel := map[string]string{"kcloud.ai/d5-mps": "true"}
		// worker1 재현: MIG-capable A30 + 비-MIG A2 가 한 노드에 공존한다.
		seedNvidiaNode("d5-node", sel, false, a30Device("", "Enabled", "Enabled", ""), d5A2Device())
		DeferCleanup(func() { cleanupNvidia("d5-node") })

		r := nvidiaReconciler()

		By("A30 을 실제로 파티션해 Ready 소유 상태를 만든다(저널은 apply 경로가 쓴다)")
		Expect(k8sClient.Create(ctx, mkNvidiaACPP("d5-mig-acpp", sel))).To(Succeed())
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("d5-mig-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "d5-mig-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))
		var mig npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-mig-acpp"}, &mig)).To(Succeed())
		Expect(getApplyRecord(&mig, "d5-node").GPUPCIs).To(Equal([]string{nvPCI}),
			"MIG 정책은 A30 만 소유한다 — A2 는 MIG target 이 아니다(전제 확인)")

		// MPS 게이트는 노드 라벨을 입력으로 쓴다 — 게이트 결과만 보면 라벨 동기화가
		// 깨져도 spec 이 통과한다. 전제를 직접 고정한다.
		By("MIG 적용 노드에 mig-active 라벨이 붙는다")
		Eventually(func() string {
			var n corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-node"}, &n)).To(Succeed())
			return n.Labels[nvidia.MigActiveNodeLabel]
		}, "10s", "200ms").Should(Equal("true"))

		By("A2 를 겨냥한 MPS 정책을 적용한다 — 장치 단위 소유 필터(A30 제외)보다 노드 단위 mig-active 게이트가 먼저 이 노드의 MPS 를 막는다")
		Expect(k8sClient.Create(ctx, d5MpsACPP("d5-mps-acpp", sel))).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("d5-mps-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-mps-acpp"}, &got)).To(Succeed())
		// A30 가 방금 GI 를 받아 Ready 가 됐으므로 d5-node 는 syncMigActiveLabel 에 의해 이미
		// mig-active=true 다 — 그 노드 전체를 mixed DS 가 맡으므로 A2 겨냥 MPS 도 노드 단위로
		// 막혀야 한다. "남의 소유 장치라 무시됐다" 가 아니라 "이 노드가 MIG 관리 중" 이라는 더
		// 정확한 이유로 거절되는 것이 정상이다 — owned-device 필터링 자체는 다른 spec 이 계속 지킨다.
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseFailed),
			"★ d5-node 는 mig-active 라벨이 걸린 노드다 — MPS 게이트가 mutation 이전에 막아야 한다")
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondValidated)).To(Equal(npuv1alpha1.ReasonSharingUnsupported))
		cond := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondValidated)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).To(ContainSubstring("MigActiveNodeLabel=true"),
			"거절 사유는 노드의 mig-active 라벨을 근거로 들어야 한다(구 판정=전역 DS 존재, 신 판정=노드 라벨)")

		// mutation 이전 거절이므로 mps control daemon 은 생기지 않는다.
		var ds appsv1.DaemonSet
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx,
			types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &ds))).To(BeTrue(),
			"★ mps control daemon 배선 경로에 도달하면 안 된다 — 노드 게이트가 먼저 막아야 한다")

		By("형제 MIG 정책은 손대지 않아야 한다")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-mig-acpp"}, &mig)).To(Succeed())
		Expect(mig.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(getApplyRecord(&mig, "d5-node").GPUPCIs).To(Equal([]string{nvPCI}))

		By("MIG 정책을 삭제해 mig-active 라벨의 근거를 없앤다")
		Expect(k8sClient.Delete(ctx, &mig)).To(Succeed())
		_, err = r.Reconcile(ctx, reconcileReq("d5-mig-acpp"))
		Expect(err).NotTo(HaveOccurred())

		By("모드가 아직 켜져 있는 동안에는 라벨을 붙들어 둔다(모드만 켜진 GPU 를 flat plugin 이 통짜로 광고하지 못하게 한다)")
		Consistently(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("d5-mig-acpp"))
			var n corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-node"}, &n)).To(Succeed())
			return n.Labels[nvidia.MigActiveNodeLabel]
		}, "1s", "200ms").Should(Equal("true"),
			"모드가 켜진 채 조각만 사라진 GPU 는 CUDA 가 못 쓴다 — flat plugin 이 통짜로 광고하면 파드가 죽는다")

		// 모드가 복원된 뒤 라벨을 걷는 것은 **정책이 있는 경로**의 몫이다. 정책이 이미 사라진 뒤에는
		// 이 라벨을 다시 볼 주체가 없다 — 라이브에서 그 순서(RestoreMode 로 모드까지 끄고 나서
		// finalizer 완료)는 반대이므로 정상 경로에서는 이 창이 열리지 않지만, Retain 삭제나 수동
		// 모드 복원 뒤에는 라벨이 남는다. 남아서 생기는 손해는 "이 노드에서 공유 모드를 못 쓴다"
		// 이고, 그 사실은 위 MigModeStillEnabled 이벤트가 말해 준다. 걷어 내는 것은 운영자 몫이다.
		By("정책이 다시 생겨 모드까지 복원되면 그때 라벨이 걷힌다")
		var restored npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-node"}, &restored)).To(Succeed())
		for i := range restored.Status.Devices {
			if restored.Status.Devices[i].MigModeCurrent == migModeEnabled {
				restored.Status.Devices[i].MigModeCurrent = migModeDisabled
				restored.Status.Devices[i].MigModePending = migModeDisabled
			}
		}
		Expect(k8sClient.Status().Update(ctx, &restored)).To(Succeed())
		Expect(r.syncMigActiveLabel(ctx, "d5-node", false)).To(Succeed())
		var afterRestore corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-node"}, &afterRestore)).To(Succeed())
		Expect(afterRestore.Labels).NotTo(HaveKey(nvidia.MigActiveNodeLabel),
			"모드까지 꺼졌는데 라벨이 남으면 이 노드는 영영 공유 모드를 못 쓴다")
	})

	// 소유 주장(OwnerUID)은 cordon 단계의 journalQuiescing 이 쓰고, 장치 목록(GPUPCIs)은 그보다
	// 뒤인 computeBaseline 이 쓴다. 형제 MIG 정책이 그 사이에서 멈추면(MIG mode 이미 Enabled +
	// GPU 를 쥔 pod → WaitingForDrain, 상한 없음) 저널은 "내 것" 이라고만 하고 "어느 장치" 인지는
	// 말하지 않아, 장치 단위 소유 판정이 빈 집합을 보고 같은 결함이 재발한다.
	It("names the owned devices in the same journal write that claims ownership", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		DeferCleanup(deleteMpsDaemon)
		sel := map[string]string{"kcloud.ai/i1-mps": "true"}
		seedNvidiaNode("i1-node", sel, false, a30Device("", "Enabled", "Enabled", ""), d5A2Device())
		DeferCleanup(func() { cleanupNvidia("i1-node") })
		// GPU 를 쥔 pod 이 형제 정책을 단계 (4)에 붙잡아 둔다 — 배출은 이 spec 밖의 일이다.
		pod := gpuPodOn("i1-gpu-pod", "i1-node")
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod) })
		// MPS 정책이 apply 경로(sharing config → daemon 게이트)까지 가려면 배선 대상 DS 가 있어야 한다.
		seedFlatDevicePluginDS()

		r := nvidiaReconciler()

		By("형제 MIG 정책을 cordon 직후(단계 4)에 멈춘다 — baseline(단계 5)에는 도달하지 못한다")
		Expect(k8sClient.Create(ctx, mkNvidiaACPP("i1-mig-acpp", sel))).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("i1-mig-acpp"))
		Expect(err).NotTo(HaveOccurred())
		var mig npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "i1-mig-acpp"}, &mig)).To(Succeed())
		Expect(mig.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseWaitingForDrain))
		Expect(condReasonOf(&mig, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonQuiesceRequired))
		rec := getApplyRecord(&mig, "i1-node")
		Expect(rec.MigPhase).To(Equal(npuv1alpha1.MigPhaseQuiescing), "GI 이전 구간에서 멈춰야 한다(전제)")
		Expect(rec.OwnerUID).To(Equal(string(mig.UID)), "저널은 이 창에서 이미 노드 소유를 주장한다(전제)")
		Expect(rec.GPUPCIs).To(Equal([]string{nvPCI}),
			"★ 소유를 주장하면서 어느 장치인지 말하지 않는다 — 장치 단위 소유 판정이 이 창에서 눈이 먼다")

		By("A2 겨냥 MPS 정책은 형제가 아직 drain 대기 중이어도 거부되면 안 된다")
		Expect(k8sClient.Create(ctx, d5MpsACPP("i1-mps-acpp", sel))).To(Succeed())
		_, err = r.Reconcile(ctx, reconcileReq("i1-mps-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "i1-mps-acpp"}, &got)).To(Succeed())
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondValidated)).NotTo(Equal(npuv1alpha1.ReasonSharingUnsupported),
			"★ 형제가 Ready 이전(WaitingForDrain)이라는 이유만으로 A2 겨냥 MPS 정책이 SharingUnsupported 로 거부됐다")
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseApplying),
			"★ daemon 기동 대기(Applying)여야 한다 — Failed 면 apply 경로에 도달조차 못 한 것이다")
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondApplied)).To(Equal(npuv1alpha1.ReasonSharingDaemonNotReady))
	})

	// 게이트를 지우는 것이 아니라 좁히는 것이다: 아무도 소유하지 않은 MIG-Enabled 장치는 여전히
	// 이 정책의 판단 대상이고, MPS 불가 판정은 mutation 이전에 그대로 거절돼야 한다.
	It("still rejects MPS when the MIG-enabled device is owned by no one", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		DeferCleanup(deleteMpsDaemon)
		sel := map[string]string{"kcloud.ai/d5-nogate": "true"}
		seedNvidiaNode("d5-nogate-node", sel, false, a30Device("", "Enabled", "Enabled", ""), d5A2Device())
		DeferCleanup(func() { cleanupNvidia("d5-nogate-node") })

		Expect(k8sClient.Create(ctx, d5MpsACPP("d5-nogate-acpp", sel))).To(Succeed())
		r := nvidiaReconciler()
		_, err := r.Reconcile(ctx, reconcileReq("d5-nogate-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d5-nogate-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseFailed))
		cond := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondValidated)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(npuv1alpha1.ReasonSharingUnsupported))
		Expect(cond.Message).To(ContainSubstring(nvPCI), "거절 사유는 어느 장치가 막았는지 말해야 한다")
		// 거절은 mutation 이전이다 — daemon 도 뜨지 않는다.
		var ds appsv1.DaemonSet
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx,
			types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &ds))).To(BeTrue())
	})
})

// seedRenderedDevicePlugin 은 **이 operator 가 실제로 렌더하는** device-plugin DaemonSet 을 만든다.
// 손으로 조립한 DS 로는 판정의 입력(--mig-strategy=mixed)이 프로덕션 산출물이라는 사실을 고정할
// 수 없다 — 렌더러가 플래그를 바꾸면 픽스처만 초록으로 남는다.
func seedRenderedDevicePlugin(sel map[string]string) {
	nr := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "d8-ncp", Namespace: "default"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{Nvidia: npuv1alpha1.NvidiaSpec{
			Enabled:           true,
			DevicePluginImage: "registry.example.com/nvidia-device-plugin:test",
			NodeSelector:      sel,
		}},
	}
	Expect(nr.ensureNvidiaDevicePlugin(ctx, policy)).To(Succeed())
	var ds appsv1.DaemonSet
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nvidia-device-plugin", Namespace: "kube-system"}, &ds)).To(Succeed())
	Expect(ds.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--mig-strategy=mixed"),
		"전제: 렌더러는 mixed DS 에만 --mig-strategy=mixed 를 붙인다 — 이 전제가 깨지면 아래 판정의 의미가 달라진다")

	var flatDS appsv1.DaemonSet
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nvidia-device-plugin-flat", Namespace: "kube-system"}, &flatDS)).To(Succeed())
	Expect(flatDS.Spec.Template.Spec.Containers[0].Args).NotTo(ContainElement("--mig-strategy=mixed"),
		"flat DS 에는 이 플래그가 없어야 한다 — mixed DS 에만 붙는다는 전제의 반대편 절반")
}

// seedFlatDevicePluginDS 는 sharing 배선 대상 flat device-plugin DS 를 만든다. sharing apply 는
// 이 DS 를 소유 확인·배선 대상으로 열어보므로(mutation 이전 fail-closed), apply 경로까지 도달해야
// 하는 spec 은 실 클러스터처럼 이것이 존재해야 한다(NPUClusterPolicy 가 만드는 오브젝트).
func seedFlatDevicePluginDS() {
	dp := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameFlat, Namespace: "kube-system"},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nvidia-device-plugin", Image: "x"}}},
			},
		},
	}
	Expect(k8sClient.Create(ctx, dp)).To(Succeed())
	DeferCleanup(cleanupDevicePluginWiring)
}

// cleanupDevicePluginWiring 은 DP DS(mixed+flat) 와 sharing ConfigMap 을 타 spec 으로 누수시키지 않는다.
// 렌더러가 만드는 오브젝트가 늘면 이 정리도 같이 늘어야 한다.
func cleanupDevicePluginWiring() {
	_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin", Namespace: "kube-system"}})
	_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameFlat, Namespace: "kube-system"}})
	_ = k8sClient.Delete(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: nvidia.SharingConfigMapNameMixed, Namespace: "kube-system"}})
	_ = k8sClient.Delete(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: nvidia.SharingConfigMapNameFlat, Namespace: "kube-system"}})
}

// 업스트림 k8s-device-plugin 은 `--mig-strategy=mixed` 와 `sharing.mps` 를 함께 받으면 시작 시
// 플래그 검증에서 거부한다(프로세스 단위 배타 — 개별 장치 배타가 아니다). DP DaemonSet·ConfigMap
// 은 클러스터 전역 단일 객체라, 노드 하나의 A2 를 겨냥한 MPS 정책이 전 nvidia 노드의 DP 를
// CrashLoopBackOff 로 몰고 무관한 정책이 소유한 A30 의 MIG 광고까지(mig-1g.6gb 4→0) 떨어뜨릴 수
// 있다 — apply→verify 실패→rollback→재적용 flap 으로 반복되므로, 판정은 apply 이전(mutation
// 이전)이어야 한다.
var _ = Describe("ACPP MPS against a device-plugin running --mig-strategy=mixed", func() {
	It("rejects the MPS policy before touching the device-plugin", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		DeferCleanup(deleteMpsDaemon)
		DeferCleanup(cleanupDevicePluginWiring)
		sel := map[string]string{"kcloud.ai/d8-mps": "true"}
		// 라이브 worker1 재현: MIG-capable A30 + 비-MIG A2 가 한 노드에 공존한다.
		seedNvidiaNode("d8-node", sel, false, a30Device("", "Enabled", "Enabled", ""), d5A2Device())
		DeferCleanup(func() { cleanupNvidia("d8-node") })
		seedRenderedDevicePlugin(sel)

		r := nvidiaReconciler()

		By("A30 을 실제로 파티션해 Ready 소유 상태를 만든다(MPS 정책이 MIG 광고를 떨어뜨릴 수 있는 그 자원이다)")
		Expect(k8sClient.Create(ctx, mkNvidiaACPP("d8-mig-acpp", sel))).To(Succeed())
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("d8-mig-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "d8-mig-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		// MPS 근본해결 이후 게이트는 전역 DS 존재가 아니라 이 노드의 mig-active 라벨을 본다 —
		// syncMigActiveLabel 이 위 GI 적용의 부작용으로 이미 걸어 뒀어야 아래 거절이 의미가 있다.
		var d8node corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d8-node"}, &d8node)).To(Succeed())
		Expect(d8node.Labels[nvidia.MigActiveNodeLabel]).To(Equal("true"),
			"★ 전제 확인: A30 GI 적용이 이 노드를 mig-active 로 표시했어야 아래 MPS 거절이 새 게이트를 재현한다")

		By("A2 를 겨냥한 MPS 정책을 적용한다 — DP 를 건드리기 전에 거절돼야 한다")
		Expect(k8sClient.Create(ctx, d5MpsACPP("d8-mps-acpp", sel))).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("d8-mps-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d8-mps-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseFailed),
			"★ MPS 요청이 사전 거절되지 않았다 — 이 경로가 라이브에서 전역 DP 를 crash-loop 로 몰았다")
		cond := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondValidated)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(npuv1alpha1.ReasonSharingUnsupported))
		Expect(cond.Message).To(ContainSubstring("--mig-strategy=mixed"),
			"거절 사유는 진단 가능해야 한다 — 무엇이 막았는지 말하지 않으면 사용자는 DP 로그를 찾아야 한다")
		Expect(cond.Message).To(ContainSubstring("MigActiveNodeLabel=true"),
			"★ 판정 근거가 노드 라벨이어야 한다 — 전역 DS 존재만으로 거절한 구 판정으로 회귀하면 안 된다")

		By("거절은 mutation 이전이다 — DP 배선도 sharing ConfigMap 도 mps daemon 도 없어야 한다")
		var dp appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nvidia-device-plugin", Namespace: "kube-system"}, &dp)).To(Succeed())
		Expect(dp.Annotations).NotTo(HaveKey(nvidia.SharingOwnerAnnotation),
			"★ DP DaemonSet 이 이미 patch 됐다 — pod 은 재기동되고 플래그 검증에서 죽는다")
		Expect(nvidia.HasMPSVolume(&dp)).To(BeFalse())
		var cm corev1.ConfigMap
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx,
			types.NamespacedName{Name: nvidia.SharingConfigMapNameMixed, Namespace: "kube-system"}, &cm))).To(BeTrue(),
			"★ sharing.mps 블록이 전역 ConfigMap 에 쓰였다")
		var mps appsv1.DaemonSet
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx,
			types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &mps))).To(BeTrue())

		By("형제 MIG 정책의 파티션은 그대로여야 한다(라이브에서 광고가 0 으로 떨어진 그 자원)")
		var mig npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d8-mig-acpp"}, &mig)).To(Succeed())
		Expect(mig.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(getApplyRecord(&mig, "d8-node").GPUPCIs).To(Equal([]string{nvPCI}))
	})

	// 가드는 mps 에만 걸려야 한다: 업스트림은 SharingStrategyMPS 에만 이 규칙을 걸고, time-slicing 은
	// 같은 mixed DP 에서 정상 동작한다.
	// 가드가 sharing 전체로 넓어지면 이 spec 이 먼저 깨진다.
	It("still applies time-slicing next to a Ready MIG partition", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		DeferCleanup(cleanupDevicePluginWiring)
		sel := map[string]string{"kcloud.ai/d8-ts": "true"}
		seedNvidiaNode("d8-ts-node", sel, false, a30Device("", "Enabled", "Enabled", ""), d5A2Device())
		DeferCleanup(func() { cleanupNvidia("d8-ts-node") })
		seedRenderedDevicePlugin(sel)

		r := nvidiaReconciler()

		Expect(k8sClient.Create(ctx, mkNvidiaACPP("d8-ts-mig-acpp", sel))).To(Succeed())
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("d8-ts-mig-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "d8-ts-mig-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		By("같은 노드의 비-MIG A2 에 시분할 정책을 적용한다")
		ts := d5MpsACPP("d8-ts-acpp", sel)
		ts.Spec.Sharing = &npuv1alpha1.SharingSpec{
			Mode:        npuv1alpha1.SharingModeTimeSliced,
			TimeSlicing: &npuv1alpha1.TimeSlicingSpec{Replicas: 4},
		}
		Expect(k8sClient.Create(ctx, ts)).To(Succeed())
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("d8-ts-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "d8-ts-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady),
			"★ time-slicing 이 mps 전용 mixed 거부 가드에 걸렸다 — 업스트림은 mps 에만 mixed 를 거부한다")

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d8-ts-acpp"}, &got)).To(Succeed())
		Expect(getApplyRecord(&got, "d8-ts-node").SharingMode).To(Equal(npuv1alpha1.SharingModeTimeSliced))
		var cm corev1.ConfigMap
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nvidia.SharingConfigMapNameMixed, Namespace: "kube-system"}, &cm)).To(Succeed())
		Expect(cm.Data[nvidia.SharingConfigKey]).To(ContainSubstring("replicas: 4"))

		var mig npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d8-ts-mig-acpp"}, &mig)).To(Succeed())
		Expect(mig.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady))
	})
})

// "클러스터에 mixed DS 가 존재하는가" 가 아니라 "이 노드가
// mig-active 라벨을 달고 있는가" 만 봐야 한다 — 전역 DS 조회로 판정하면 mixed DS 가 존재하기만
// 해도 노드와 무관하게 모든 MPS 요청이 막힌다. 이 spec 은 mixed DS 를 실제로 렌더해 둔 채로
// (ensureNvidiaDevicePlugin 은 두 DS 를 무조건 만든다) mig-active 라벨이 없는 노드의 MPS 가
// 통과하는지로 그 회귀를 고정한다.
var _ = Describe("ACPP MPS after per-node device-plugin split", func() {
	It("allows MPS on a node without the mig-active label even though the cluster-wide mixed DS exists", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		DeferCleanup(deleteMpsDaemon)
		DeferCleanup(cleanupDevicePluginWiring)

		sel := map[string]string{"kcloud.ai/d8root-flat": "true"}
		seedNvidiaNode("d8root-flat-node", sel, false, d5A2Device())
		DeferCleanup(func() { cleanupNvidia("d8root-flat-node") })
		// mig-active 라벨을 걸지 않는다 — 이 노드는 flat DS 가 맡는다. mixed DS 는 그래도
		// 존재한다(렌더러가 항상 둘 다 만든다) — 전역 존재만으로 판정하면 이 노드까지 막힌다.
		seedRenderedDevicePlugin(sel)

		r := nvidiaReconciler()
		Expect(k8sClient.Create(ctx, d5MpsACPP("d8root-mps", sel))).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("d8root-mps"))
		Expect(err).NotTo(HaveOccurred())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d8root-mps"}, &got)).To(Succeed())
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondValidated)).NotTo(Equal(npuv1alpha1.ReasonSharingUnsupported),
			"★ mig-active 라벨이 없는 노드인데도 전역 mixed DS 존재만으로 막혔다(전역 DS 조회로 판정하던 방식으로 회귀)")
	})
})

// runTarget(layout+sharing 조합)의 거절청소 지점(precheckSharing 실패 분기 → rejectSharing)은
// sharing-only 지점과 별개 코드경로다(이 호출이 없어도 나머지 스위트는 green 이다) — 이 spec 이
// 그 지점을 처음으로 고정한다. layout 을 가진 MIG-capable 장치(A30, mode Enabled)에 sharing.mps
// 를 얹으면 deviceStatusFor 가 첫 reconcile 부터 항상 거절해 "이미 적용된 mps 배선" 을 만들 수
// 없지만, disableSharing 은 저널 상태와 무관하게 항상 먼저 mps-control-daemon 정리를 부르므로,
// leftover daemon 을 미리 심어 두고 거절이 그 daemon 을 걷어내는지로 이 호출 지점을 고정한다.
var _ = Describe("ACPP layout+MPS combination rejection cleanup", func() {
	It("cleans up a leftover mps control daemon when a layout+sharing policy is rejected by the MPS guard", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		DeferCleanup(deleteMpsDaemon)

		sel := map[string]string{"kcloud.ai/i4-test": "true"}
		seedNvidiaNode("i4-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("i4-node") })

		// 아무도 안 쓰는 leftover mps-control-daemon 을 미리 심는다 — 이 spec 이
		// 유일한 ACPP 이므로 anyPolicyStillUsesMPS 는 항상 false, 거절청소가 돌면 반드시 걷힌다.
		Expect(k8sClient.Create(ctx, renderMpsControlDaemonDS())).To(Succeed())

		r := nvidiaReconciler()

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "i4-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   sel,
				Vendor:         "nvidia",
				DeletionPolicy: "Retain",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 4}},
				Sharing:        &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeMPS, MPS: &npuv1alpha1.MPSSpec{Replicas: 4}},
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcileReq("i4-acpp"))
		Expect(err).NotTo(HaveOccurred())

		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "i4-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseFailed),
			"전제 확인: MIG 가 켜진 장치에 mps 를 얹은 layout+sharing 조합은 거절돼야 한다")
		Expect(condReasonOf(&got, npuv1alpha1.ACPPCondValidated)).To(Equal(npuv1alpha1.ReasonSharingUnsupported))

		// ★ runTarget(layout+sharing 조합)의 거절청소 지점이 disableSharing 을 실제로 부르는지 —
		// 안 부르면(되돌린 코드) 이 leftover daemon 은 그대로 남는다.
		var ds appsv1.DaemonSet
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx,
			types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &ds))).To(BeTrue(),
			"★ 거절된 정책이 leftover mps-control-daemon 을 걷어내지 않았다 — runTarget 의 거절청소 지점이 disableSharing 을 부르지 않는다")
	})
})

// cleanupNvidia 는 nvidia 테스트가 시드한 노드/NDR 을 제거한다(타 spec 으로 누수 방지).
func cleanupNvidia(name string) {
	var node corev1.Node
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &node); err == nil {
		_ = k8sClient.Delete(ctx, &node)
	}
	var ndr npuv1alpha1.NodeDeviceReport
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &ndr); err == nil {
		_ = k8sClient.Delete(ctx, &ndr)
	}
}
