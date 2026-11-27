// ============================================================
// recoverdevice_upgrade_guard_test.go: 업그레이드 중 노드에서 RecoverDevice 가 설치 Job pod 을 죽이지 않는지 고정
// 상세: 드라이버 버전 전환 중에는 dpkg 가 스스로 구 DKMS 모듈을 내리는 구간이 있어
// driverLoaded=false 가 뜬다 — 고장이 아니라 예정된 상태다. 이때 RecoverDevice 가 설치 Job
// pod 을 지우면 apt-get install 이 DKMS 빌드 도중 죽어 패키지가 half-configured 로 남고,
// 버전 보고가 막혀 검증이 영원히 실패하는 자기강화 루프가 된다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/health"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/upgrade"
)

// seedUpgradingNode 는 `npu.ai/driver-upgrading` 라벨이 붙은 노드를 만든다.
// 이 라벨은 상태기계가 Cordoning 진입 시 붙이고 Uncordoning 에서 뗀다.
func seedUpgradingNode(name string) *corev1.Node {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{upgrade.DriverUpgradingLabelKey: "true"},
	}}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	return node
}

var _ = Describe("RecoverDevice 는 업그레이드 중인 노드를 건드리지 않는다", func() {
	// 증명: 업그레이드 라벨이 붙은 노드에서는 설치 Job pod 을 지우지 않는다. 설치가 스스로
	// 모듈을 내리는 구간을 health 가 고장으로 읽고 설치 주체를 죽이면 이 가드가 막아야 한다.
	// 깨는 뮤테이션: restartDriverPods 의 업그레이드 라벨 확인을 지우면 pod 이 지워져 실패한다.
	It("업그레이드 중이면 설치 Job pod 을 지우지 않는다", func() {
		node := "recover-guard-upgrading-node"
		n := seedUpgradingNode(node)
		jobPod := seedDriverPod("drv-job-"+node, node, "driver-install")
		dsPod := seedDriverPod("drv-ds-"+node, node, "driver")
		DeferCleanup(func() {
			for _, p := range []*corev1.Pod{jobPod, dsPod} {
				_ = k8sClient.Delete(ctx, p, client.GracePeriodSeconds(0))
			}
			_ = k8sClient.Delete(ctx, n)
		})

		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		out, err := p.Apply(ctx, recoverDeviceOp("recover-guard-upgrading", node))
		Expect(err).NotTo(HaveOccurred())

		for _, pod := range []*corev1.Pod{jobPod, dsPod} {
			var got corev1.Pod
			Expect(k8sClient.Get(ctx,
				types.NamespacedName{Name: pod.Name, Namespace: "default"}, &got)).To(Succeed())
			Expect(got.DeletionTimestamp).To(BeNil(),
				"업그레이드 중인 노드의 드라이버 pod %s 을 지웠다 — 설치가 도중에 죽는다", pod.Name)
		}

		// 실패로 보고하면 안 된다. ApplyFailed 는 세 번 쌓이면 멀쩡한 노드를 격리로 보낸다.
		// `NotTo(ApplyFailed)` 로는 아무 값이나 통과하므로 정확히 못박는다.
		Expect(out.Event).To(Equal(operation.EventApplyDone),
			"업그레이드 중 건너뛴 것을 실패로 보고하면 반복 실패가 세어져 노드가 격리된다")
	})

	// 증명: 업그레이드 라벨이 없으면 종전대로 지운다(가드가 정상 복구를 막지 않는다).
	//
	// 깨는 뮤테이션: 라벨 유무와 무관하게 항상 건너뛰게 만들면 이 시험이 실패한다.
	It("업그레이드 중이 아니면 종전대로 드라이버 pod 을 지운다", func() {
		node := "recover-guard-normal-node"
		n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node}}
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
		jobPod := seedDriverPod("drv-job-"+node, node, "driver-install")
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, jobPod, client.GracePeriodSeconds(0))
			_ = k8sClient.Delete(ctx, n)
		})

		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		out, err := p.Apply(ctx, recoverDeviceOp("recover-guard-normal", node))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyDone))

		var got corev1.Pod
		err = k8sClient.Get(ctx, types.NamespacedName{Name: jobPod.Name, Namespace: "default"}, &got)
		Expect(err != nil || got.DeletionTimestamp != nil).To(BeTrue(),
			"업그레이드 중이 아닌 노드의 드라이버 pod 을 되살리지 않았다")
	})

	// 증명: 노드가 아예 없어도 가드 조회 실패로 복구를 막지 않는다. 조회 실패로 복구를 건너뛰면
	// 노드 객체가 잠시 안 보이는 순간마다 복구가 조용히 사라진다 — 못 읽었으면 "업그레이드 중
	// 아님" 으로 본다.
	// 깨는 뮤테이션: `upgradingDriver` 의 조회 실패 갈래를 `return true` 로 바꾸면 실패한다(함수
	// 끝의 `return upgrading` 만 바꾸는 뮤테이션은 노드가 없으면 이미 조회 실패 갈래에서 돌아가
	// 닿지 못해 무의미하다). 이벤트만 보면 "진행" 과 "건너뜀" 이 구분 안 되므로 pod 삭제 여부로 가른다.
	It("노드를 못 읽으면 업그레이드 중이 아닌 것으로 보고 진행한다", func() {
		node := "recover-guard-missing-node"
		jobPod := seedDriverPod("drv-job-"+node, node, "driver-install")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, jobPod, client.GracePeriodSeconds(0)) })

		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		out, err := p.Apply(ctx, recoverDeviceOp("recover-guard-missing", node))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyDone))

		// 이벤트만 보면 안 된다 — 건너뛰기도 `EventApplyDone` 을 돌려준다.
		// pod 이 실제로 지워졌는지, 즉 관측 가능한 효과로 가른다.
		var got corev1.Pod
		err = k8sClient.Get(ctx, types.NamespacedName{Name: jobPod.Name, Namespace: "default"}, &got)
		Expect(err != nil || got.DeletionTimestamp != nil).To(BeTrue(),
			"노드를 못 읽었다고 복구를 건너뛰었다 — 노드가 잠시 안 보이는 순간마다 복구가 사라진다")
	})
})

var _ = Describe("health 는 업그레이드 중인 노드에 복구 작업을 만들지 않는다", func() {
	// 증명: 업그레이드 라벨이 붙은 노드에서 DriverNotLoaded 판정이 나와도 AcceleratorOperation 을
	// 만들지 않는다. 참가자 쪽 가드만으로는 부족하다 — 작업이 만들어지면 조정자·저널·검증이 다
	// 돌고, 업그레이드가 끝난 뒤 쿨다운 버킷에 남은 작업이 뒤늦게 실행될 여지도 생긴다. 애초에
	// 만들지 않는 것이 맞다.
	// 깨는 뮤테이션: ensureRecovery 의 업그레이드 라벨 확인을 지우면 작업이 만들어져 실패한다.
	It("업그레이드 중이면 RecoverDevice 를 만들지 않는다", func() {
		node := "health-guard-upgrading-node"
		n := seedUpgradingNode(node)
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })

		r := &AcceleratorHealthReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(64),
		}
		res := health.Result{
			State:  health.StateUnhealthy,
			Reason: health.ReasonDriverNotLoaded,
			Devices: []health.DeviceResult{{
				Vendor: "furiosa", State: health.StateUnhealthy,
				Reason: health.ReasonDriverNotLoaded,
			}},
		}
		opRef, err := r.ensureRecovery(ctx, n, res, health.DefaultPolicy())
		Expect(err).NotTo(HaveOccurred())
		Expect(opRef).To(BeEmpty(), "업그레이드 중인 노드에 복구 작업을 만들었다")

		var ops npuv1alpha1.AcceleratorOperationList
		Expect(k8sClient.List(ctx, &ops)).To(Succeed())
		for i := range ops.Items {
			Expect(ops.Items[i].Spec.NodeName).NotTo(Equal(node),
				"업그레이드 중인 노드 앞으로 작업 %s 이 만들어졌다", ops.Items[i].Name)
		}
	})

	// 증명: 업그레이드 라벨이 없으면 종전대로 복구 작업을 만든다.
	//
	// 깨는 뮤테이션: 라벨 유무와 무관하게 항상 건너뛰게 만들면 이 시험이 실패한다.
	It("업그레이드 중이 아니면 종전대로 RecoverDevice 를 만든다", func() {
		node := "health-guard-normal-node"
		n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node}}
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })

		r := &AcceleratorHealthReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(64),
		}
		res := health.Result{
			State:  health.StateUnhealthy,
			Reason: health.ReasonDriverNotLoaded,
			Devices: []health.DeviceResult{{
				Vendor: "furiosa", State: health.StateUnhealthy,
				Reason: health.ReasonDriverNotLoaded,
			}},
		}
		opRef, err := r.ensureRecovery(ctx, n, res, health.DefaultPolicy())
		Expect(err).NotTo(HaveOccurred())
		Expect(opRef).NotTo(BeEmpty(), "정상 노드인데 복구 작업을 만들지 않았다")
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorOperation{
				ObjectMeta: metav1.ObjectMeta{Name: opRef},
			})
		})
	})
})
