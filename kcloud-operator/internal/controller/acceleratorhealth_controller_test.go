// ============================================================
// acceleratorhealth_controller_test.go: health 감시 컨트롤러 envtest
// 상세: 정상 노드·관측 부재·플러그인 장애·격리 해제까지 상태 기록과 부작용(라벨·작업)을 고정한다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func newHealthReconciler() *AcceleratorHealthReconciler {
	return &AcceleratorHealthReconciler{
		Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(64),
	}
}

// seedHealthyNode 는 Ready 노드 + 방금 관측된 NDR + Ready 인 device-plugin pod 을 만든다.
func seedHealthyNode(name string) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name,
		Labels: map[string]string{"kcloud.ai/nvidia.present": "true"}}}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	node.Status.Conditions = []corev1.NodeCondition{{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now(),
	}}
	node.Status.Allocatable = corev1.ResourceList{
		"nvidia.com/gpu": *resource.NewQuantity(2, resource.DecimalSI),
	}
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

	ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.NodeDeviceReportSpec{NodeName: name}}
	Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
	now := metav1.Now()
	ndr.Status.Devices = []npuv1alpha1.DeviceEntry{{
		Vendor: "nvidia", Count: 1, DriverLoaded: true, PCIeAddress: "0000:18:00.0",
	}}
	ndr.Status.ObservedAt = &now
	Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())

	seedHealthDevicePluginPod(name, true)
}

// seedHealthDevicePluginPod 는 그 노드의 device-plugin pod 을 만든다(ready 여부 지정).
func seedHealthDevicePluginPod(node string, ready bool) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dp-" + node, Namespace: "default",
			Labels: map[string]string{nvidiaDevicePluginVendorLabel: "nvidia"},
		},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{
			Name: "dp", Image: "nvidia/k8s-device-plugin:v0.17.1",
		}}},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	pod.Status.Phase = corev1.PodRunning
	st := corev1.ConditionTrue
	if !ready {
		st = corev1.ConditionFalse
	}
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: st}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

// seedHealthyNodeWithoutDP 는 device-plugin pod 이 없는 가속기 노드다(예: Furiosa·Tenstorrent
// 노드처럼 nvidia 라벨을 안 다는 plugin 을 쓰거나, control-plane 처럼 광고 대상이 아닌 노드).
func seedHealthyNodeWithoutDP(name string) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name,
		Labels: map[string]string{"kcloud.ai/nvidia.present": "true"}}}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	node.Status.Conditions = []corev1.NodeCondition{{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now(),
	}}
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed()) // allocatable 비어 있음

	ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.NodeDeviceReportSpec{NodeName: name}}
	Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
	now := metav1.Now()
	ndr.Status.Devices = []npuv1alpha1.DeviceEntry{{Vendor: "nvidia", Count: 1, DriverLoaded: true}}
	ndr.Status.ObservedAt = &now
	Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())
}

// staleHealthNDR 는 보고서의 관측 시각을 과거로 민다.
func staleHealthNDR(node string, ago time.Duration) {
	var ndr npuv1alpha1.NodeDeviceReport
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ndr)).To(Succeed())
	old := metav1.NewTime(time.Now().Add(-ago))
	ndr.Status.ObservedAt = &old
	Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())
}

// cleanupHealth 는 노드·보고서·상태·pod·작업을 지운다.
func cleanupHealth(node string) {
	_ = k8sClient.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node}})
	_ = k8sClient.Delete(ctx, &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: node}})
	_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorHealth{ObjectMeta: metav1.ObjectMeta{Name: node}})
	_ = k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "dp-" + node, Namespace: "default"}}, client.GracePeriodSeconds(0))
	var ops npuv1alpha1.AcceleratorOperationList
	if err := k8sClient.List(ctx, &ops); err == nil {
		for i := range ops.Items {
			if ops.Items[i].Spec.NodeName == node {
				_ = k8sClient.Delete(ctx, &ops.Items[i])
			}
		}
	}
}

// healthOpsForNode 는 그 노드에 대해 health 가 만든 작업이다.
func healthOpsForNode(node string) []npuv1alpha1.AcceleratorOperation {
	var ops npuv1alpha1.AcceleratorOperationList
	Expect(k8sClient.List(ctx, &ops)).To(Succeed())
	var out []npuv1alpha1.AcceleratorOperation
	for i := range ops.Items {
		if ops.Items[i].Spec.NodeName == node && ops.Items[i].Spec.Owner.Kind == healthOwnerKind {
			out = append(out, ops.Items[i])
		}
	}
	return out
}

var _ = Describe("AcceleratorHealth", func() {
	// 증명: 이 노드를 맡는 관리 대상 device-plugin 이 없으면 장애로 부르지 않는다 — 없으면
	// NVIDIA 가 아닌 노드 전부가 Unhealthy 로 뜨고 복구 작업까지 생기는 회귀를 막는다.
	It("does not call a node unhealthy when no managed device-plugin targets it", func() {
		node := "health-nodp-node"
		seedHealthyNodeWithoutDP(node)
		DeferCleanup(func() { cleanupHealth(node) })

		r := newHealthReconciler()
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var ah npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ah)).To(Succeed())
		Expect(ah.Status.State).To(Equal("Healthy"),
			"맡는 plugin 이 없는 노드를 장애로 판정했다: %s", ah.Status.Reason)
		Expect(healthOpsForNode(node)).To(BeEmpty(), "대상 밖 노드에 복구 작업을 만들었다")
	})

	// 증명: 출처 정책이 사라진 근거의 광고 기준선은 비교에 쓰지 않는다 — 지워진 MPS 정책의
	// 기준선(gpu 4)이 남으면 정상 노드가 불일치로 잘못 판정된다.
	It("ignores an advertisement baseline whose source policy is gone", func() {
		node := "health-orphan-baseline-node"
		seedHealthyNode(node)
		DeferCleanup(func() { cleanupHealth(node) })

		ev := &npuv1alpha1.AcceleratorEvidence{ObjectMeta: metav1.ObjectMeta{Name: node},
			Spec: npuv1alpha1.AcceleratorEvidenceSpec{NodeName: node, Vendor: "nvidia"}}
		Expect(k8sClient.Create(ctx, ev)).To(Succeed())
		future := metav1.NewTime(time.Now().Add(time.Hour))
		ev.Status = npuv1alpha1.AcceleratorEvidenceStatus{
			Level: "FunctionallyVerified", ExpiresAt: &future,
			SourcePolicy:        "policy-that-was-deleted",
			AdvertisedResources: map[string]int32{"nvidia.com/gpu": 4},
		}
		Expect(k8sClient.Status().Update(ctx, ev)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, ev) })

		r := newHealthReconciler()
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var ah npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ah)).To(Succeed())
		Expect(ah.Status.Reason).NotTo(Equal("AdvertisementMismatch"),
			"사라진 정책의 기준선으로 불일치를 선언했다")
		Expect(healthOpsForNode(node)).To(BeEmpty(), "쓸모없는 재검증 작업을 만들었다")
	})

	// 증명: 신호가 전부 정상인 노드는 Healthy 로 기록되고 할당이 허용된다.
	// 깨는 뮤테이션: 판정 결과를 status 에 안 쓰면 State 가 비어 실패한다.
	It("records Healthy for a node whose signals all hold", func() {
		node := "health-ok-node"
		seedHealthyNode(node)
		DeferCleanup(func() { cleanupHealth(node) })

		r := newHealthReconciler()
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var ah npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ah)).To(Succeed())
		Expect(ah.Status.State).To(Equal("Healthy"))
		Expect(ah.Status.AllocationAllowed).To(BeTrue())
		Expect(ah.Status.Signals).NotTo(BeEmpty(), "신호 기록이 없으면 판정 근거를 볼 수 없다")
		Expect(healthOpsForNode(node)).To(BeEmpty(), "정상 노드에 복구 작업을 만들었다")
	})

	// 증명: 노드 에이전트 보고가 낡으면 Unknown 이고 할당이 막히며, 복구 작업은 만들지 않는다.
	It("records Unknown and creates no operation when the report is stale", func() {
		node := "health-stale-node"
		seedHealthyNode(node)
		staleHealthNDR(node, 20*time.Minute)
		DeferCleanup(func() { cleanupHealth(node) })

		r := newHealthReconciler()
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var ah npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ah)).To(Succeed())
		Expect(ah.Status.State).To(Equal("Unknown"))
		Expect(ah.Status.AllocationAllowed).To(BeFalse())
		Expect(healthOpsForNode(node)).To(BeEmpty(),
			"관측이 끊긴 것만으로 파괴적 복구를 만들면 안 된다")
	})

	// 증명: device-plugin 이 죽으면 복구 작업이 **정확히 하나** 만들어지고 반복 reconcile 로도 늘지 않는다.
	// 깨는 뮤테이션: 트랜잭션 ID 를 매번 다르게 만들면 5개가 생겨 실패한다.
	It("creates exactly one recovery operation for a persistent cause", func() {
		node := "health-dp-node"
		seedHealthyNode(node)
		// 실제 장애 모습을 만든다: plugin pod 이 Ready 가 아니고 **광고도 사라졌다**.
		// 광고가 남아 있는 동안에는 주체가 살아 있는 것으로 보는 것이 옳다.
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "dp-" + node, Namespace: "default"}, &pod)).To(Succeed())
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
		Expect(k8sClient.Status().Update(ctx, &pod)).To(Succeed())
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
		n.Status.Allocatable = corev1.ResourceList{}
		Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
		DeferCleanup(func() { cleanupHealth(node) })

		r := newHealthReconciler()
		// 첫 pass: 원인을 확정하고 복구를 요청한다.
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		var first npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &first)).To(Succeed())
		Expect(first.Status.State).To(Equal("Unhealthy"))
		Expect(first.Status.AllocationAllowed).To(BeFalse())

		// 이후 pass: 복구가 도는 동안에는 Recovering 이고, 새 작업을 또 만들지 않는다.
		for i := 0; i < 4; i++ {
			_, err = r.Reconcile(ctx, reconcileReq(node))
			Expect(err).NotTo(HaveOccurred())
		}

		ops := healthOpsForNode(node)
		Expect(ops).To(HaveLen(1), "같은 원인으로 작업이 여러 개 만들어졌다: %d", len(ops))
		Expect(ops[0].Spec.Type).To(Equal("DevicePluginRestart"))

		var ah npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ah)).To(Succeed())
		Expect(ah.Status.State).To(Equal("Recovering"), "복구가 도는 동안에는 복구 중이어야 한다")
		Expect(ah.Status.AllocationAllowed).To(BeFalse())
		Expect(ah.Status.RecoveryOperationRef).To(Equal(ops[0].Name))

		var labeled corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &labeled)).To(Succeed())
		Expect(labeled.Labels).To(HaveKeyWithValue(QuarantineLabel, "true"))
	})

	// 증명: 상태가 그대로면 시간이 흘러도 status 를 다시 쓰지 않는다(핫루프 방지). 시계를 pass
	// 마다 10초씩 민다 — 실제 시각으로 돌리면 세 pass 가 같은 초 안에 끝나 타임스탬프가 우연히
	// 같아져, 이 시험이 "안 썼다" 가 아니라 "우연히 같았다" 를 통과시킬 수 있다. 다만 보고서
	// 신선도 임계(90초)는 넘기지 않는다 — 넘기면 Unknown 으로 바뀌는 것이 정상이라 이 시험이
	// 다른 것을 재게 된다. 방벽이 둘이라(변화 없으면 write 생략 + status 의 바이트 안정성) 한쪽만
	// 망가져도 안 잡힐 수 있지만, 운영자가 겪는 것은 churn 유무이고 둘 다 무너지는 순간을 잡는다.
	It("does not rewrite the status when nothing changed", func() {
		node := "health-idle-node"
		seedHealthyNode(node)
		DeferCleanup(func() { cleanupHealth(node) })

		clock := time.Now()
		r := newHealthReconciler()
		r.Now = func() time.Time { return clock }

		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		var first npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &first)).To(Succeed())

		for i := 0; i < 3; i++ {
			clock = clock.Add(10 * time.Second)
			_, err = r.Reconcile(ctx, reconcileReq(node))
			Expect(err).NotTo(HaveOccurred())
		}
		var again npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &again)).To(Succeed())
		Expect(again.ResourceVersion).To(Equal(first.ResourceVersion),
			"상태가 그대로인데 status 를 다시 썼다(핫루프)")
		Expect(again.Status.DetectedAt).To(Equal(first.Status.DetectedAt),
			"상태가 안 바뀌었는데 감지 시각이 갱신됐다")
	})

	// 증명: 격리된 노드는 신호가 좋아져도 자동으로 풀리지 않는다.
	It("keeps a quarantined node quarantined even after signals recover", func() {
		node := "health-quarantine-node"
		seedHealthyNode(node)
		DeferCleanup(func() { cleanupHealth(node) })

		// 이미 격리 상태를 심는다.
		ah := &npuv1alpha1.AcceleratorHealth{ObjectMeta: metav1.ObjectMeta{Name: node},
			Spec: npuv1alpha1.AcceleratorHealthSpec{NodeName: node}}
		Expect(k8sClient.Create(ctx, ah)).To(Succeed())
		ah.Status = npuv1alpha1.AcceleratorHealthStatus{
			State: "Quarantined", Reason: "RepeatedRecoveryFailure", AllocationAllowed: false,
		}
		Expect(k8sClient.Status().Update(ctx, ah)).To(Succeed())

		r := newHealthReconciler()
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var after npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &after)).To(Succeed())
		Expect(after.Status.State).To(Equal("Quarantined"), "신호가 좋아졌다고 격리가 풀렸다")
		Expect(after.Status.AllocationAllowed).To(BeFalse())
	})

	// 증명: 사람이 해제를 표시하면 격리가 풀린다 — 표시가 없으면 나갈 길이 아예 없다. 이 시험이
	// 없으면 세 규칙(반복 실패 우선 판정 / 그 원인에는 작업 없음 / 격리에서 Recovering 으로만
	// 나감)이 합쳐져 **영구 격리**가 된다 — 실패 작업을 손으로 지워도 전이 게이트가 막아
	// 풀리지 않는다.
	// 깨는 뮤테이션: prevState 초기화(주석 해제 분기)를 지우면 Quarantined 에 머물러 실패한다.
	//               recoveryHistory 의 releasedAt 필터를 지우면 실패 3건이 계속 세어져 실패한다.
	It("releases a quarantined node once a human acknowledges the failures", func() {
		node := "health-release-node"
		seedHealthyNode(node)
		DeferCleanup(func() { cleanupHealth(node) })

		// health 가 만든 복구 작업 3건이 되돌리기로 끝난 상태 = 반복 실패 임계 도달.
		for i := 0; i < 3; i++ {
			op := &npuv1alpha1.AcceleratorOperation{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("health-release-fail-%d", i)},
				Spec: npuv1alpha1.AcceleratorOperationSpec{
					Type: "DevicePluginRestart", NodeName: node,
					TransactionID: fmt.Sprintf("health-release-%d", i),
					Owner:         npuv1alpha1.OperationOwner{Kind: healthOwnerKind, Name: node},
				},
			}
			Expect(k8sClient.Create(ctx, op)).To(Succeed())
			op.Status.Phase = npuv1alpha1.OpPhaseRolledBack
			Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())
		}

		base := time.Now()
		r := newHealthReconciler()
		r.Now = func() time.Time { return base }
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var quarantined npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &quarantined)).To(Succeed())
		Expect(quarantined.Status.State).To(Equal("Quarantined"),
			"반복 실패 3건인데 격리되지 않았다(전제가 안 선다)")
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
		Expect(n.Labels).To(HaveKeyWithValue(QuarantineLabel, "true"))

		// 사람이 "여기까지의 실패는 확인했다" 를 적는다.
		base2 := n.DeepCopy()
		n.Annotations = map[string]string{
			QuarantineReleaseAnnotation: base.Add(10 * time.Second).UTC().Format(time.RFC3339),
		}
		Expect(k8sClient.Patch(ctx, &n, client.MergeFrom(base2))).To(Succeed())

		r.Now = func() time.Time { return base.Add(20 * time.Second) }
		_, err = r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var released npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &released)).To(Succeed())
		Expect(released.Status.State).To(Equal("Healthy"), "해제를 표시했는데 격리가 안 풀렸다")
		Expect(released.Status.AllocationAllowed).To(BeTrue())
		Expect(released.Status.RecoveryFailures).To(BeNumerically("==", 0),
			"확인한 실패가 계속 세어진다 — 다음 실패 하나에 다시 격리된다")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
		Expect(n.Labels).NotTo(HaveKey(QuarantineLabel))
	})
})

var _ = Describe("AcceleratorHealth mixed-vendor node", func() {
	// 증명: 혼재 노드에서 다른 벤더 광고가 NVIDIA plugin 장애를 가리지 않는다 — worker1 처럼
	// Warboy 광고(beta.furiosa.ai/npu=1)가 살아 있으면 nvidia.com/gpu 가 0 인데도 Healthy 로
	// 읽힐 위험이 있다.
	It("does not let another vendor's advertisement mask a dead nvidia plugin", func() {
		node := "health-mixed-node"
		seedHealthyNode(node)
		DeferCleanup(func() { cleanupHealth(node) })

		// nvidia 광고만 사라지고 Furiosa 광고는 남은 상태.
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
		n.Status.Allocatable = corev1.ResourceList{
			"beta.furiosa.ai/npu": *resource.NewQuantity(1, resource.DecimalSI),
		}
		Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx,
			types.NamespacedName{Name: "dp-" + node, Namespace: "default"}, &pod)).To(Succeed())
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
		Expect(k8sClient.Status().Update(ctx, &pod)).To(Succeed())

		_, err := newHealthReconciler().Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var ah npuv1alpha1.AcceleratorHealth
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ah)).To(Succeed())
		Expect(ah.Status.Reason).To(Equal("DevicePluginDown"),
			"다른 벤더 광고가 NVIDIA plugin 장애를 가렸다: state=%s", ah.Status.State)
	})
})

var _ = Describe("AcceleratorHealth monitor interval", func() {
	// 증명: 정책이 감시 주기를 낮추면 컨트롤러가 그 간격으로 다시 본다.
	// 깨는 뮤테이션: requeueFor 가 상수를 돌려주면 30초가 나와 실패한다.
	It("requeues at the interval the policy asks for", func() {
		node := "health-interval-node"
		seedHealthyNode(node)
		pol := &npuv1alpha1.AcceleratorHealthPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "fast-interval"},
			Spec: npuv1alpha1.AcceleratorHealthPolicySpec{
				Checks: npuv1alpha1.HealthChecks{MonitorIntervalSeconds: 5},
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		DeferCleanup(func() { cleanupHealth(node); _ = k8sClient.Delete(ctx, pol) })

		res, err := newHealthReconciler().Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(5*time.Second), "정책이 정한 주기가 반영되지 않았다")
	})
})
