// ============================================================
// migobservation_controller_test.go: 정책 독립 MIG 관측 컨트롤러 envtest
// 상세: 정책이 없어도 관측이 돌아 보고서가 채워지는지, 고아 라벨이 정리되는지 고정한다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

// fakeObserver 는 지정한 관측 결과를 그대로 돌려준다(특권 Job 대체). err 를 채우면 실 Job
// 생성·대기 실패 같은 인프라 오류(파싱 가능한 fail-closed 결과가 아니라 진짜 Go error)를 흉내낸다.
type fakeObserver struct {
	obs   []nvidia.Observation
	err   error
	calls int
}

func (f *fakeObserver) Observe(context.Context, string, []string) ([]nvidia.Observation, error) {
	f.calls++
	return f.obs, f.err
}

// errObserveJobFailed 는 관측 Job 자체가 못 뜬 것 같은 인프라 오류를 흉내낸다(fail-closed 파싱
// 결과가 아니라 실 Go error).
var errObserveJobFailed = errors.New("observe job create: timed out")

// seedMigNode 는 NVIDIA 노드 + 관측 실패 상태의 보고서를 만든다(detector 가 만드는 그대로).
func seedMigNode(name string, labels map[string]string) {
	l := map[string]string{"kcloud.ai/nvidia.present": "true"}
	for k, v := range labels {
		l[k] = v
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: l}}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())

	ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.NodeDeviceReportSpec{NodeName: name}}
	Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
	now := metav1.Now()
	ndr.Status.ObservedAt = &now
	ndr.Status.Devices = []npuv1alpha1.DeviceEntry{{
		Vendor: "nvidia", Model: "NVIDIA A30", Count: 1, DriverLoaded: true,
		PCIeAddress: "0000:18:00.0", MigModeCurrent: "Unknown",
		MigObservationError: "mode query failed: exit status 9",
	}}
	Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())
}

func cleanupMigNode(name string) {
	_ = k8sClient.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})
	_ = k8sClient.Delete(ctx, &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: name}})
}

// owningACPPNames 는 이 노드를 겨냥하는 ACPP 이름을 모두 돌려준다 — nodeHasOwningPolicy 와 같은
// nodeMatchesSelector 판정을 그대로 재사용한다(판정을 여기서 다시 구현하면 두 곳이 갈릴 수 있다).
// terminating(DeletionTimestamp 설정) 여부는 가리지 않는다 — nodeHasOwningPolicy 도 안 가리고,
// 그 격차가 바로 이 진단이 잡으려는 누수의 정의다. 오염원이 재발했을 때 이름으로 바로 지목하기 위한
// 시험 전용 진단이다.
func owningACPPNames(node *corev1.Node) []string {
	var list npuv1alpha1.AcceleratorPartitionPolicyList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())
	var names []string
	for i := range list.Items {
		if nodeMatchesSelector(node, list.Items[i].Spec.NodeSelector) {
			names = append(names, list.Items[i].Name)
		}
	}
	return names
}

// patchMigObservationAttemptedAt 은 노드의 마지막 관측 시도 시각 주석을 직접 심는다
// (rate limit 판정 시험용).
func patchMigObservationAttemptedAt(name string, t time.Time) {
	var node corev1.Node
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &node)).To(Succeed())
	base := node.DeepCopy()
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[migObservationAttemptedAtAnnotation] = t.UTC().Format(time.RFC3339)
	Expect(k8sClient.Patch(ctx, &node, client.MergeFrom(base))).To(Succeed())
}

var _ = Describe("MigObservation controller", func() {
	// 증명: 정책이 하나도 없어도 관측이 돌아 보고서의 MIG 필드가 실값으로 채워진다.
	// 깨는 뮤테이션: Reconcile 에서 관측 호출을 지우면 Unknown 이 남아 실패한다.
	It("observes and publishes even when no policy targets the node", func() {
		node := "migobs-node"
		seedMigNode(node, nil)
		DeferCleanup(func() { cleanupMigNode(node) })

		obs := &fakeObserver{obs: []nvidia.Observation{{
			PCI: "0000:18:00.0", ModeCurrent: migModeDisabled, ModePending: migModeDisabled,
			Geometry: "disabled", LgipOutput: "profiles...",
		}}}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.calls).To(Equal(1))

		var got npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
		Expect(got.Status.Devices[0].MigModeCurrent).To(Equal(migModeDisabled))
		Expect(got.Status.Devices[0].MigObservationError).To(BeEmpty())
		Expect(got.Status.Devices[0].MigLgipOutput).To(Equal("profiles..."),
			"프로파일 목록이 없으면 유효한 MIG 정책을 만들 수 없다 — 이 필드가 닭과 달걀을 끊는다")
	})

	// 증명: 관측 Job 을 띄우기 전에 시도 시각을 찍는다 — 성공·실패와 무관하게. 이것이 "다시
	// 띄워도 되는가" 를 막는 유일한 문(rate limiter)이다. 성공 시각에만 걸면 관측이 계속
	// 실패하는 노드에서 그 문이 영영 안 잠겨, watch 이벤트(kubelet 하트비트, ~10초 간격)마다
	// 특권 Job 이 다시 뜬다.
	// 깨는 뮤테이션: markObservationAttempted 호출을 Observe 성공 이후로 옮기거나 err==nil 로
	// 게이트하면, 이 시험(전부 fail-closed)에서 주석이 안 찍혀 실패한다.
	It("marks the attempt time even when every observation is fail-closed", func() {
		node := "migobs-allfail-node"
		seedMigNode(node, nil)
		DeferCleanup(func() { cleanupMigNode(node) })

		obs := &fakeObserver{obs: []nvidia.Observation{{
			PCI: "0000:18:00.0", ModeCurrent: "Unknown", ModePending: "Unknown",
			Err: "observe job timed out",
		}}}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.calls).To(Equal(1))

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
		Expect(got.Annotations).To(HaveKey(migObservationAttemptedAtAnnotation),
			"관측이 전부 실패했는데 시도 시각을 안 찍었다 — 다음 watch 이벤트마다 다시 뜬다")
	})

	// 증명: Observe 가 진짜 Go error(Job 생성·대기 실패 등, fail-closed 파싱 결과가 아니라)를
	// 돌려줘도 시도 시각은 찍힌다. 위 시험(fail-closed, err==nil)만으로는 "Observe 이후 err==nil
	// 검사를 통과해야만 시각을 찍는" 잘못된 배치도 우연히 통과한다 — MigObserver.Observe 가
	// fail-closed 결과에서는 err 를 nil 로 돌려주기 때문이다. 이 시험은 err != nil 경로를 직접
	// 겨냥해 "Job 을 띄우기 전에 찍는다" 는 배치 자체를 고정한다. 깨는 뮤테이션:
	// markObservationAttempted 호출을 Observe 뒤 "err != nil 이면 return" 다음으로 옮기면 실패한다.
	It("marks the attempt time even when Observe itself returns a real error", func() {
		node := "migobs-infra-error-node"
		seedMigNode(node, nil)
		DeferCleanup(func() { cleanupMigNode(node) })

		obs := &fakeObserver{err: errObserveJobFailed}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.calls).To(Equal(1))

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
		Expect(got.Annotations).To(HaveKey(migObservationAttemptedAtAnnotation),
			"Observe 가 진짜 오류를 냈는데 시도 시각을 안 찍었다")
	})

	// 증명(핵심 불변식): 마지막 시도가 주기 안이면, 장치 상태가 아무리 더러워도(seedMigNode 의
	// 기본 상태 — MigObservationError 있음) 다시 띄우지 않는다. 장치 상태로 이 판단을 뒤집으면
	// 핫루프가 된다 — 노드 watch 이벤트는 kubelet 하트비트로 몇 초마다 오는데, RequeueAfter 는
	// 그 이벤트를 막지 못한다. 막는 것은 이 시도-시각 문 하나뿐이다.
	// 깨는 뮤테이션: needsObservation 이 시도 시각보다 장치 상태를 먼저 보게 되돌리면, 더러운
	// 장치 때문에 다시 관측해 calls 가 1이 되어 실패한다.
	It("does not launch a new job while the last attempt is recent, even though the devices are still dirty", func() {
		node := "migobs-dirty-but-recent-node"
		seedMigNode(node, nil) // 기본 상태: MigObservationError 있음(더러움).
		DeferCleanup(func() { cleanupMigNode(node) })

		fixedNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
		patchMigObservationAttemptedAt(node, fixedNow.Add(-1*time.Minute)) // 주기(10분) 안.

		obs := &fakeObserver{}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
			Now:             func() time.Time { return fixedNow },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.calls).To(Equal(0),
			"장치가 더러운데 최근 시도를 무시하고 다시 띄웠다 — 이것이 라이브 핫루프였다")
	})

	// 증명: 이미 신선한 관측이 있으면(기본 실시간 clock, Now seam 미주입) 특권 Job 을 다시
	// 띄우지 않는다 — 시험 전용 clock 이 아니라 실제 배포가 쓰는 기본 경로(r.now())도 본다.
	// 깨는 뮤테이션: needsObservation 판정을 지우면 calls 가 1이 되어 실패한다.
	It("does not re-run the privileged job when the last attempt is already fresh", func() {
		node := "migobs-fresh-node"
		seedMigNode(node, nil)
		DeferCleanup(func() { cleanupMigNode(node) })

		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ndr)).To(Succeed())
		ndr.Status.Devices[0].MigModeCurrent = migModeDisabled
		ndr.Status.Devices[0].MigObservationError = ""
		now := metav1.Now()
		ndr.Status.ObservedAt = &now
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())
		patchMigObservationAttemptedAt(node, time.Now())

		obs := &fakeObserver{}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.calls).To(Equal(0), "이미 아는 것을 다시 물으려고 특권 Job 을 띄웠다")
	})

	// 증명: 소유 정책이 없고 모드까지 꺼진 노드의 고아 라벨을 정리한다.
	// 깨는 뮤테이션: 라벨 정리 분기를 지우면 라벨이 남아 실패한다.
	It("drops an orphaned mig-active label once the mode is confirmed off", func() {
		node := "migobs-orphan-node"
		seedMigNode(node, map[string]string{nvidia.MigActiveNodeLabel: "true"})
		DeferCleanup(func() { cleanupMigNode(node) })

		// 전제: 이 노드를 소유하는 ACPP 가 하나도 없어야 한다. 있으면(다른 스펙이 cleanup 에서
		// finalizer 를 못 지워 terminating 으로 남긴 ACPP 포함 — nodeHasOwningPolicy 는 그것을
		// 가리지 않는다) 아래 라벨 정리가 조용히 스킵되고, 이 스펙만 봐서는 원인을 못 찾는다.
		// 재발 시 오염원을 이름으로 바로 지목한다(단독·직렬 실행 1회 FAIL 재발 방지).
		var seeded corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &seeded)).To(Succeed())
		Eventually(func() []string { return owningACPPNames(&seeded) }, "5s", "200ms").Should(BeEmpty(),
			"이 노드를 소유하는 ACPP 가 남아 있다 — 위 이름의 스펙이 cleanup 에서 finalizer 를 제거하지 않았다")

		obs := &fakeObserver{obs: []nvidia.Observation{{
			PCI: "0000:18:00.0", ModeCurrent: migModeDisabled, ModePending: migModeDisabled, Geometry: "disabled",
		}}}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
		Expect(got.Labels).NotTo(HaveKey(nvidia.MigActiveNodeLabel))
	})

	// 증명: 모드가 아직 켜져 있으면 라벨을 떼지 않는다(유령 GPU 광고 차단 유지).
	// 깨는 뮤테이션: 모드 확인을 지우고 무조건 떼면 실패한다.
	It("keeps the label while a GPU still has MIG mode enabled", func() {
		node := "migobs-enabled-node"
		seedMigNode(node, map[string]string{nvidia.MigActiveNodeLabel: "true"})
		DeferCleanup(func() { cleanupMigNode(node) })

		obs := &fakeObserver{obs: []nvidia.Observation{{
			PCI: "0000:18:00.0", ModeCurrent: "Enabled", ModePending: "Enabled", Geometry: "disabled",
		}}}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
		Expect(got.Labels).To(HaveKeyWithValue(nvidia.MigActiveNodeLabel, "true"))
	})

	// 증명: 소유 정책이 있는 노드의 라벨은 건드리지 않는다(소유권 존중).
	// 깨는 뮤테이션: 정책 존재 확인을 지우면 라벨이 떨어져 실패한다.
	It("never touches the label on a node a policy owns", func() {
		node := "migobs-owned-node"
		seedMigNode(node, map[string]string{
			nvidia.MigActiveNodeLabel: "true", "kcloud.ai/owned-test": "true"})
		DeferCleanup(func() { cleanupMigNode(node) })

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "migobs-owner"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				Vendor:       "nvidia",
				NodeSelector: map[string]string{"kcloud.ai/owned-test": "true"},
				// CRD 의 CEL 규칙(spec.layout/spec.sharing 중 하나는 필수)을 만족시키기 위한 최소값 —
				// 이 시험이 확인하는 것은 소유권 판정이지 layout 내용이 아니다.
				Layout: []npuv1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 1}},
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		DeferCleanup(func() { deleteACPPAndWait(acpp.Name) })

		obs := &fakeObserver{obs: []nvidia.Observation{{
			PCI: "0000:18:00.0", ModeCurrent: migModeDisabled, ModePending: migModeDisabled, Geometry: "disabled",
		}}}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
		Expect(got.Labels).To(HaveKeyWithValue(nvidia.MigActiveNodeLabel, "true"),
			"정책이 소유한 라벨을 남이 뗐다")
	})

	// 증명(wedge 회귀 고정): 장치 상태와 무관하게(깨끗해도), 마지막 시도 시각이 주기(10분)
	// 보다 오래되면 다시 관측한다 — 시도 시각이 유일한 rate limiter 라서, 이게 없으면 첫
	// 시도 이후 이 컨트롤러는 그 노드를 영영 다시 보지 않는다(사람이 손으로 되돌린 모드를
	// 절대 못 잡는다).
	// 깨는 뮤테이션: needsObservation 의 시간 분기를 지우면 calls 가 0으로 남아 실패한다.
	It("re-observes once the last attempt is older than the interval, even with clean devices", func() {
		node := "migobs-attempted-stale-node"
		seedMigNode(node, nil)
		DeferCleanup(func() { cleanupMigNode(node) })

		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ndr)).To(Succeed())
		ndr.Status.Devices[0].MigModeCurrent = migModeDisabled
		ndr.Status.Devices[0].MigObservationError = ""
		nowMeta := metav1.Now()
		ndr.Status.ObservedAt = &nowMeta
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())

		fixedNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
		patchMigObservationAttemptedAt(node, fixedNow.Add(-migObservationInterval)) // 주기를 정확히 넘겼다.

		obs := &fakeObserver{obs: []nvidia.Observation{{
			PCI: "0000:18:00.0", ModeCurrent: migModeDisabled, ModePending: migModeDisabled, Geometry: "disabled",
		}}}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
			Now:             func() time.Time { return fixedNow },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.calls).To(Equal(1), "시도 시각이 오래됐는데도 재관측하지 않았다 — 사람이 되돌린 모드를 영영 못 잡는다")
	})

	// 증명: NodeSelector 를 생략한 ACPP 는 이 저장소의 표준 k8s 의미대로 "모든 노드" 를
	// 겨냥한다. 그런 정책이 있으면, 셀렉터로 직접 지목되지 않은 노드라도 고아가 아니다.
	// 깨는 뮤테이션: nodeHasOwningPolicy 가 빈 셀렉터를 "매칭 없음" 으로 되돌리면(예전 버그)
	// 이 시험이 실패한다 — 라벨이 떨어진다.
	It("does not touch the label when an ACPP with no NodeSelector exists (matches every node)", func() {
		node := "migobs-cluster-owned-node"
		seedMigNode(node, map[string]string{nvidia.MigActiveNodeLabel: "true"})
		DeferCleanup(func() { cleanupMigNode(node) })

		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "migobs-cluster-owner"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				Vendor: "nvidia",
				// NodeSelector 미설정 — 표준 k8s 의미로 전체 노드 매칭(nodeMatchesSelector).
				Layout: []npuv1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 1}},
			},
		}
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		DeferCleanup(func() { deleteACPPAndWait(acpp.Name) })

		obs := &fakeObserver{obs: []nvidia.Observation{{
			PCI: "0000:18:00.0", ModeCurrent: migModeDisabled, ModePending: migModeDisabled, Geometry: "disabled",
		}}}
		r := &MigObservationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ObserverFactory: func() nvidia.Observer { return obs },
		}
		_, err := r.Reconcile(ctx, reconcileReq(node))
		Expect(err).NotTo(HaveOccurred())

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &got)).To(Succeed())
		Expect(got.Labels).To(HaveKeyWithValue(nvidia.MigActiveNodeLabel, "true"),
			"셀렉터 없는 정책이 소유한 노드의 라벨을 남이 뗐다")
	})
})

// TestNvidiaPCIsOfSkipsPassthroughDevices 는 vfio-pci 바인딩 장치를 관측 대상에서 빼는지 본다.
// host 의 nvidia-smi 가 passthrough GPU 를 못 보므로 관측은 매번 fail-closed 로 끝나고, 그
// 재시도가 폭주의 또 다른 씨앗이 된다.
// 깨는 뮤테이션: DriverBinding == "vfio-pci" 분기를 지우면 그 PCI 가 섞여 나와 실패한다.
func TestNvidiaPCIsOfSkipsPassthroughDevices(t *testing.T) {
	ndr := &npuv1alpha1.NodeDeviceReport{Status: npuv1alpha1.NodeDeviceReportStatus{
		Devices: []npuv1alpha1.DeviceEntry{
			{Vendor: "nvidia", PCIeAddress: "0000:18:00.0", DriverBinding: "nvidia"},
			{Vendor: "nvidia", PCIeAddress: "0000:86:00.0", DriverBinding: "vfio-pci"},
		},
	}}
	got := nvidiaPCIsOf(ndr)
	if len(got) != 1 || got[0] != "0000:18:00.0" {
		t.Fatalf("vfio-pci 장치를 걸러내지 못했다: %v", got)
	}
}

// TestNvidiaPCIsOfSkipsPassthroughReservedNode 는 전량 vfio-pci 예약 노드에서 아무 PCI 도
// 돌려주지 않는지 본다.
// 깨는 뮤테이션: PassthroughReserved 조기 반환을 지우면 PCI 가 나와 실패한다.
func TestNvidiaPCIsOfSkipsPassthroughReservedNode(t *testing.T) {
	ndr := &npuv1alpha1.NodeDeviceReport{Status: npuv1alpha1.NodeDeviceReportStatus{
		PassthroughReserved: true,
		Devices:             []npuv1alpha1.DeviceEntry{{Vendor: "nvidia", PCIeAddress: "0000:18:00.0"}},
	}}
	if got := nvidiaPCIsOf(ndr); got != nil {
		t.Fatalf("passthrough 예약 노드에서 PCI 를 돌려줬다: %v", got)
	}
}
