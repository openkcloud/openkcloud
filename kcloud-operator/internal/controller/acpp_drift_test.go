// ============================================================
// acpp_drift_test.go: 검증 통과 후 광고 붕괴 감지 envtest
// 상세: 광고를 걷으면 Degraded 로 내려가고, 되돌리면 Ready 로 복귀하며, 그 사이 하드웨어를
//
//	건드리지 않는지(재적용 0회) 확인한다. 정상 롤아웃 중에는 Degraded 로 가지 않는다.
//
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/verification"
)

// withdrawMig 는 device-plugin 이 죽어 MIG 조각 광고가 사라진 상태를 만든다.
func withdrawMig(node string) {
	var n corev1.Node
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
	delete(n.Status.Allocatable, corev1.ResourceName("nvidia.com/mig-1g.6gb"))
	Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
}

// seedRollingDevicePlugin 은 그 노드를 맡는 device-plugin DaemonSet 을 "롤아웃 중" 상태로 만든다.
func seedRollingDevicePlugin(name string, rolling bool) *appsv1.DaemonSet {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kube-system"},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "dp", Image: "dp:1"}}},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ds)).To(Succeed())
	ds.Status = appsv1.DaemonSetStatus{
		ObservedGeneration: ds.Generation, DesiredNumberScheduled: 1,
		UpdatedNumberScheduled: 1, NumberReady: 1,
	}
	if rolling {
		ds.Status.NumberUnavailable = 1
		ds.Status.NumberReady = 0
	}
	Expect(k8sClient.Status().Update(ctx, ds)).To(Succeed())
	return ds
}

func degradedReason(name string) string {
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got); err != nil {
		return ""
	}
	if len(got.Status.Targets) == 0 {
		return ""
	}
	c := apimeta.FindStatusCondition(got.Status.Targets[0].Conditions, npuv1alpha1.ACPPCondDegraded)
	if c == nil {
		return ""
	}
	return string(c.Status) + "/" + c.Reason
}

// TestMonitorDriftSkipsDRAOwnedNode 는 DRA 소유 라벨이 붙은 노드를 monitorDrift 가 드리프트로
// 오판하지 않는지 본다. DRA 로 전환한 노드는 자원을 ResourceSlice 로 광고해 allocatable 이 0 이다
// — draOwnedNode 게이트 없이 그 0 을 그대로 재면 항상 "장치 다 사라짐" 으로 읽는다. 이 게이트는
// runTarget 의 적용 경로에만 있고, 재검증 경로(shouldReverify 가 false 인 pass)는 그보다 먼저
// return 해 그 경로를 타지 않으므로 monitorDrift 자신도 같은 판정을 걸어야 한다.
func TestMonitorDriftSkipsDRAOwnedNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n1",
			Labels: map[string]string{npuv1alpha1.DRAOwnedNodeLabel(vendorNvidia): labelValueTrue},
		},
		// Status.Allocatable 비움 — DRA 소유 노드는 실제로 이렇다(ResourceSlice 로만 광고).
	}
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).WithObjects(node).Build()
	r := &AcceleratorPartitionPolicyReconciler{Client: c, Scheme: c.Scheme(), Verification: &verification.Verifier{Client: c}}
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-dra"},
		Spec:       npuv1alpha1.AcceleratorPartitionPolicySpec{Vendor: vendorNvidia},
	}
	in := npuv1alpha1.TargetStatus{NodeName: "n1"}

	got, err := r.monitorDrift(partition.Target{Ctx: context.Background(), NodeName: "n1"}, acpp, in)
	if err != nil {
		t.Fatalf("monitorDrift 실패: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("DRA 소유 노드의 allocatable=0 을 드리프트로 오판했다 — got %+v, want 입력 그대로 %+v", got, in)
	}
}

// TestMonitorDriftStillWatchesNonDRANode 는 DRA 라벨이 없는 일반 노드에서는 monitorDrift 가
// 여전히 기존 감시 경로를 타는지 본다 — draOwnedNode 확인이 모든 노드를 건너뛰게 하지 않는지
// 보는 회귀 가드다.
func TestMonitorDriftStillWatchesNonDRANode(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}} // DRA 라벨 없음
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).WithObjects(node).Build()
	r := &AcceleratorPartitionPolicyReconciler{Client: c, Scheme: c.Scheme(), Verification: &verification.Verifier{Client: c}}
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-normal"},
		Spec:       npuv1alpha1.AcceleratorPartitionPolicySpec{Vendor: vendorNvidia},
	}
	in := npuv1alpha1.TargetStatus{NodeName: "n1"}

	got, err := r.monitorDrift(partition.Target{Ctx: context.Background(), NodeName: "n1"}, acpp, in)
	if err != nil {
		t.Fatalf("monitorDrift 실패: %v", err)
	}
	if reflect.DeepEqual(got, in) {
		t.Error("일반 노드인데 ts 가 입력 그대로다 — DRA skip 경로를 잘못 탄 것으로 보인다")
	}
}

var _ = Describe("ACPP advertisement drift", func() {
	It("leaves Ready when the advertisement collapses, and returns without re-applying", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		// MigActiveNodeLabel 을 심는다 — DevicePluginRolling 이 이 라벨로 담당 DaemonSet(mixed/flat)
		// 을 고르므로(MIG·MPS 노드 역할 규약), 라벨이 없으면 존재하지 않는 flat DS 를 찾아
		// 조용히 "안 돈다" 로 읽는다. 이 노드는 MIG 를 쓰므로 실제로도 mixed 가 맞다.
		labels := map[string]string{"kcloud.ai/nv-drift": "true", nvidia.MigActiveNodeLabel: "true"}
		geom := nvidia.GeometrySummary("1g.6gb", 4)
		seedNvidiaNode("nv-drift-node", labels, true, a30Device(geom, "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-drift-node") })
		advertiseMig("nv-drift-node", 4)
		ds := seedRollingDevicePlugin(nvidia.DevicePluginNameMixed, false)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ds, client.PropagationPolicy(metav1.DeletePropagationBackground))
			_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorEvidence{
				ObjectMeta: metav1.ObjectMeta{Name: "nv-drift-node"},
			}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		acpp := mkNvidiaACPP("nv-drift-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		// 이 노드는 처음부터 목표 geometry+광고를 갖고 있다 — 저널 없이 첫 reconcile 을 돌리면
		// routeNvidiaNoDiff 가 "geometry 는 맞지만 이 ACPP 가 만든 게 아니다" 로 읽어
		// ExistingMigConfiguration(Failed, terminal)로 거절한다(acpp_evidence_gate_test.go 와 같은 함정).
		// 이미 이 ACPP 가 소유·적용 완료로 기록된 저널을 심어 managed no-diff 재검증 경로로 들어가게 한다.
		acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName: "nv-drift-node", GPUPCIs: []string{"0000:41:00.0"}, OwnerUID: string(acpp.UID),
			BaselineGPUCount: 1, ExpectedMigCount: 4, ExpectedFullGPUCount: 1,
			Profile: "1g.6gb", Count: 4, MigPhase: npuv1alpha1.MigPhaseReady,
		}}
		Expect(k8sClient.Status().Update(ctx, acpp)).To(Succeed())
		steps := 0
		r := evidenceReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return countingExec{steps: &steps} }

		By("첫 수렴")
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-drift-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-drift-acpp"}, &got)
			return got.Status.Phase
		}, "20s", "300ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))
		stepsAtReady := steps

		By("광고가 사라지면 먼저 의심으로 표시된다")
		withdrawMig("nv-drift-node")
		_, _ = r.Reconcile(ctx, reconcileReq("nv-drift-acpp"))
		Expect(degradedReason("nv-drift-acpp")).To(Equal("Unknown/" + npuv1alpha1.ReasonAdvertisementDriftSuspected))
		var mid npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-drift-acpp"}, &mid)).To(Succeed())
		Expect(mid.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady))

		By("유예 시간이 지나면 Degraded 로 확정된다")
		// 시계를 앞당기는 대신 first-seen 을 과거로 밀어 유예 경과를 만든다(프로덕션이 만드는 것과
		// 같은 상태: condition 이 유예 이전부터 Unknown 이었던 상태).
		var cur npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-drift-acpp"}, &cur)).To(Succeed())
		for i := range cur.Status.Targets[0].Conditions {
			if cur.Status.Targets[0].Conditions[i].Type == npuv1alpha1.ACPPCondDegraded {
				cur.Status.Targets[0].Conditions[i].LastTransitionTime =
					metav1.NewTime(metav1.Now().Add(-2 * time.Minute))
			}
		}
		Expect(k8sClient.Status().Update(ctx, &cur)).To(Succeed())

		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-drift-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-drift-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseDegraded))
		Expect(degradedReason("nv-drift-acpp")).To(Equal("True/" + npuv1alpha1.ReasonAdvertisementDrift))

		By("Degraded 인 동안 하드웨어를 건드리지 않는다")
		for i := 0; i < 5; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-drift-acpp"))
		}
		Expect(steps).To(Equal(stepsAtReady), "Degraded 상태에서 재적용이 일어났다")

		By("광고가 돌아오면 즉시 Ready 로 복귀한다")
		advertiseMig("nv-drift-node", 4)
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-drift-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-drift-acpp"}, &got)
			return got.Status.Phase
		}, "10s", "200ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(degradedReason("nv-drift-acpp")).To(Equal("False/" + npuv1alpha1.ReasonAdvertisementConsistent))
	})

	It("does not flip to Degraded while the device plugin is rolling out", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		// 위와 같은 이유로 MigActiveNodeLabel 을 심는다.
		labels := map[string]string{"kcloud.ai/nv-drift-roll": "true", nvidia.MigActiveNodeLabel: "true"}
		geom := nvidia.GeometrySummary("1g.6gb", 4)
		seedNvidiaNode("nv-drift-roll-node", labels, true, a30Device(geom, "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("nv-drift-roll-node") })
		advertiseMig("nv-drift-roll-node", 4)
		ds := seedRollingDevicePlugin(nvidia.DevicePluginNameMixed, false)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ds, client.PropagationPolicy(metav1.DeletePropagationBackground))
			_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorEvidence{
				ObjectMeta: metav1.ObjectMeta{Name: "nv-drift-roll-node"},
			}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		rollACPP := mkNvidiaACPP("nv-drift-roll-acpp", labels)
		Expect(k8sClient.Create(ctx, rollACPP)).To(Succeed())
		// 위와 같은 이유로 소유·적용 완료 저널을 먼저 심는다(managed no-diff 재검증 경로).
		rollACPP.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName: "nv-drift-roll-node", GPUPCIs: []string{"0000:41:00.0"}, OwnerUID: string(rollACPP.UID),
			BaselineGPUCount: 1, ExpectedMigCount: 4, ExpectedFullGPUCount: 1,
			Profile: "1g.6gb", Count: 4, MigPhase: npuv1alpha1.MigPhaseReady,
		}}
		Expect(k8sClient.Status().Update(ctx, rollACPP)).To(Succeed())
		r := evidenceReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-drift-roll-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nv-drift-roll-acpp"}, &got)
			return got.Status.Phase
		}, "20s", "300ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		By("롤아웃 중 광고가 잠깐 비는 것은 장애가 아니다")
		withdrawMig("nv-drift-roll-node")
		var rolling appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"}, &rolling)).To(Succeed())
		rolling.Status.NumberUnavailable = 1
		rolling.Status.NumberReady = 0
		Expect(k8sClient.Status().Update(ctx, &rolling)).To(Succeed())

		for i := 0; i < 6; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("nv-drift-roll-acpp"))
		}
		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nv-drift-roll-acpp"}, &got)).To(Succeed())
		Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady), "정상 롤아웃을 장애로 오인했다")
		Expect(degradedReason("nv-drift-roll-acpp")).To(Equal("False/" + npuv1alpha1.ReasonAdvertisementCheckSuppressed))
	})
})
