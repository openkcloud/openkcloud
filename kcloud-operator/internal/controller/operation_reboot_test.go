// ============================================================
// operation_reboot_test.go: 재부팅 작업 본체 envtest
// 상세: 재부팅은 되돌릴 수 없다 — 상한·BootID 검증·중복 방지가 이 스펙의 전부다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
)

// seedRebootNode 는 BootID 와 Ready 조건을 가진 노드를 만든다(프로덕션이 만드는 모양 그대로).
// seedRebootNode 는 항상 "boot-1" + Ready 로 시작한다 — 이 파일의 모든 fixture 가 같은 초기
// 상태를 쓴다(unparam: 값이 안 변하는 파라미터를 두면 무엇이 실제로 바뀌는지 흐려진다). Ready 가
// 아닌 상태를 보고 싶은 테스트는 setNodeBoot 로 이후에 바꾼다.
func seedRebootNode(name string) {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	Expect(k8sClient.Create(ctx, n)).To(Succeed())
	n.Status.NodeInfo.BootID = "boot-1"
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
}

// setNodeBoot 는 재부팅 후 상태를 시뮬레이션한다 — BootID 는 항상 "boot-2"(seedRebootNode 의
// "boot-1" 과 달라야 "바뀌었다" 를 검증할 수 있다).
func setNodeBoot(name string, ready bool) {
	var n corev1.Node
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &n)).To(Succeed())
	cond := corev1.ConditionFalse
	if ready {
		cond = corev1.ConditionTrue
	}
	n.Status.NodeInfo.BootID = "boot-2"
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: cond}}
	Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
}

func rebootJobFor(node string) (*batchv1.Job, error) {
	var job batchv1.Job
	err := k8sClient.Get(ctx, types.NamespacedName{
		Name: naming.AcppRebootJobName(node), Namespace: driverjob.Namespace,
	}, &job)
	return &job, err
}

var _ = Describe("node rebooter", func() {
	newRebooter := func() *nodeRebooter {
		return &nodeRebooter{Client: k8sClient, Image: "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0"}
	}

	// 증명: 재부팅 요청이 실제로 Job 을 만든다(대상 경로 도달 확인).
	// 깨는 뮤테이션: RequestReboot 이 Job 을 안 만들면 조회가 NotFound 라 실패한다.
	It("creates a reboot job for the node", func() {
		node := "reboot-node-a"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })

		Expect(newRebooter().RequestReboot(ctx, node, "boot-1")).To(Succeed())
		job, err := rebootJobFor(node)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Spec.Template.Spec.NodeName).To(Equal(node))
	})

	// 증명: 이미 Job 이 있으면 두 번 만들지 않는다(재부팅을 두 번 쏘지 않는다).
	// 깨는 뮤테이션: 존재 확인을 빼면 두 번째 호출이 AlreadyExists 로 에러를 내거나 Job 을 덮어쓴다.
	It("does not issue a second reboot job", func() {
		node := "reboot-node-b"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		rb := newRebooter()
		Expect(rb.RequestReboot(ctx, node, "boot-1")).To(Succeed())
		before, err := rebootJobFor(node)
		Expect(err).NotTo(HaveOccurred())
		Expect(rb.RequestReboot(ctx, node, "boot-1")).To(Succeed())
		after, err := rebootJobFor(node)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.UID).To(Equal(before.UID), "재부팅 Job 이 다시 만들어졌다")
	})

	// 증명: 노드가 Ready 여도 BootID 가 그대로면 재부팅 완료가 아니다.
	//       (kubelet 이 재부팅 직전 잠깐 Ready 를 유지하는 레이스를 잡는다.)
	// 깨는 뮤테이션: BootID 비교를 빼고 Ready 만 보면 이 스펙이 곧바로 실패한다.
	It("does not call it done while the boot id is unchanged", func() {
		node := "reboot-node-c"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		done, reason, err := newRebooter().BootObserved(ctx, node, "boot-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "재부팅 전인데 완료로 판정했다")
		Expect(reason).NotTo(BeEmpty())
	})

	// 증명: BootID 가 바뀌고 Ready 면 완료다.
	// 깨는 뮤테이션: 완료 조건을 뒤집으면 실패한다.
	It("calls it done once the boot id changed and the node is Ready", func() {
		node := "reboot-node-d"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		setNodeBoot(node, true)
		done, _, err := newRebooter().BootObserved(ctx, node, "boot-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	// 증명: BootID 가 바뀌었어도 노드가 아직 Ready 가 아니면 완료가 아니다.
	// 깨는 뮤테이션: Ready 검사를 빼면 재부팅 도중 노드를 완료로 보고해 후속 작업이 죽은 노드에 나간다.
	It("waits for Ready even after the boot id changed", func() {
		node := "reboot-node-e"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		setNodeBoot(node, false)
		done, _, err := newRebooter().BootObserved(ctx, node, "boot-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
	})

	// 증명: BootID 를 캡처하지 못한 경우(빈 값)에는 Ready 만으로 보수 전진한다 — 교착 방지.
	// 깨는 뮤테이션: 빈 값 fallback 을 빼면 캡처 실패한 재부팅이 영원히 대기한다.
	It("falls back to readiness when no boot id was captured", func() {
		node := "reboot-node-f"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		done, _, err := newRebooter().BootObserved(ctx, node, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})
})

var _ = Describe("node reboot participant", func() {
	mkRebootOp := func(name, node string, journal []npuv1alpha1.OperationJournalEntry) *npuv1alpha1.AcceleratorOperation {
		op := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.NodeReboot), NodeName: node, TransactionID: name,
				Owner:        npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "p-" + name},
				ResourceKeys: []string{"node/" + node + "/reboot", "node/" + node + "/cordon"},
			},
		}
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		if journal != nil {
			op.Status.Journal = journal
			op.Status.Snapshot = &npuv1alpha1.OperationSnapshot{BootID: "boot-1"}
			Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())
		}
		return op
	}

	// 증명: 첫 호출은 재부팅을 요청하고 대기 사건을 돌려준다.
	// 깨는 뮤테이션: 첫 호출에서 곧바로 완료를 돌려주면 Job 이 없는데 완료가 되어 실패한다.
	It("requests a reboot and reports waiting", func() {
		node := "reboot-part-a"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		op := mkRebootOp("reboot-part-op-a", node, nil)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		op.Status.Snapshot = &npuv1alpha1.OperationSnapshot{BootID: "boot-1"}

		p := NewNodeRebootParticipant(k8sClient, "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0")
		out, err := p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventRebootRequired))
		_, jerr := rebootJobFor(node)
		Expect(jerr).NotTo(HaveOccurred(), "재부팅을 보고했는데 Job 이 없다")
	})

	// 증명: 재부팅이 관측되면 완료 사건을 낸다. 이것이 EventBootObserved 의 유일한 생산자다.
	// 깨는 뮤테이션: BootObserved 결과를 무시하면 작업이 대기에서 영원히 못 빠져나온다.
	It("produces the boot observed event once the node came back", func() {
		node := "reboot-part-b"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		op := mkRebootOp("reboot-part-op-b", node, []npuv1alpha1.OperationJournalEntry{
			{Step: operation.StepRebootRequested, Epoch: 1, At: metav1.Now()},
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		setNodeBoot(node, true)

		p := NewNodeRebootParticipant(k8sClient, "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0")
		out, err := p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventBootObserved))
	})

	// 증명: 상한을 넘으면 재부팅을 더 쏘지 않고 실패로 보고한다.
	//       재부팅 루프에 빠진 노드를 사람이 손으로 꺼내야 하는 상황을 막는 유일한 장치다.
	// 깨는 뮤테이션: 상한 비교를 빼면 이 스펙에서 세 번째 Job 이 만들어져 실패한다.
	It("refuses to reboot beyond the attempt limit", func() {
		node := "reboot-part-c"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		journal := make([]npuv1alpha1.OperationJournalEntry, 0, maxOperationReboots)
		for i := int32(0); i < maxOperationReboots; i++ {
			journal = append(journal, npuv1alpha1.OperationJournalEntry{
				Step: operation.StepRebootRequested, Epoch: int64(i + 1), At: metav1.Now(),
			})
		}
		op := mkRebootOp("reboot-part-op-c", node, journal)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		p := NewNodeRebootParticipant(k8sClient, "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0")
		out, err := p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyFailed))
		_, jerr := rebootJobFor(node)
		Expect(jerr).To(HaveOccurred(), "상한을 넘었는데 재부팅 Job 이 만들어졌다")
	})

	// 증명: 시도 횟수를 저널에서 센다(새 status 필드 없이).
	// 깨는 뮤테이션: 다른 단계 이름을 세면 숫자가 어긋나 상한 스펙과 함께 깨진다.
	It("counts attempts from the journal", func() {
		Expect(RebootAttemptsFrom(nil)).To(BeNumerically("==", 0))
		Expect(RebootAttemptsFrom([]npuv1alpha1.OperationJournalEntry{
			{Step: operation.StepRebootRequested, Epoch: 1},
			{Step: operation.StepApplyStarted, Epoch: 1},
			{Step: operation.StepRebootRequested, Epoch: 2},
		})).To(BeNumerically("==", 2))
	})

	// 증명: 재진입 안전 — 조정자가 완료를 이미 저널(StepRebootObserved)에 남긴 뒤 이 participant
	// 를 다시 부르면(WaitingForReboot → Applying 전이 다음 pass), 노드 상태가 그대로라도
	// BootObserved 를 다시 판정해 EventBootObserved 를 또 내면 안 된다. Applying 에는 그 전이가
	// 없어(operation.Next 참고) "정의되지 않은 전이" 에러 루프가 된다 — ApplyDone 으로 마감해야 한다.
	// 깨는 뮤테이션: StepRebootObserved 가드를 지우면 이 테스트가 EventBootObserved 를 받고 실패한다.
	It("does not re-report boot observed once it is already journaled (reentrancy)", func() {
		node := "reboot-part-d"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		setNodeBoot(node, true) // 재부팅은 이미 끝났고 노드는 그 상태 그대로다.
		op := mkRebootOp("reboot-part-op-d", node, []npuv1alpha1.OperationJournalEntry{
			{Step: operation.StepRebootRequested, Epoch: 1, At: metav1.Now()},
			{Step: operation.StepRebootObserved, Epoch: 1, At: metav1.Now()},
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		p := NewNodeRebootParticipant(k8sClient, "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0")
		// 재진입 안전은 두 번째 호출만이 아니라 반복 호출에서도 유지돼야 한다.
		for i := 0; i < 3; i++ {
			out, err := p.Apply(ctx, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Event).To(Equal(operation.EventApplyDone), "call #%d", i+1)
		}
	})

	// 증명: Rollback 은 아직 실행 대기 중인 재부팅 Job 을 실제로 취소한다.
	// 깨는 뮤테이션: Delete 호출을 지우면 Job 이 남아 있어 이 테스트가 실패한다.
	It("cancels the pending reboot job on rollback", func() {
		node := "reboot-part-e"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		op := mkRebootOp("reboot-part-op-e", node, []npuv1alpha1.OperationJournalEntry{
			{Step: operation.StepRebootRequested, Epoch: 1, At: metav1.Now()},
		})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		rb := &nodeRebooter{Client: k8sClient, Image: "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0"}
		Expect(rb.RequestReboot(ctx, node, "boot-1")).To(Succeed())
		_, jerr := rebootJobFor(node)
		Expect(jerr).NotTo(HaveOccurred(), "픽스처 준비 실패: Job 이 없다")

		p := NewNodeRebootParticipant(k8sClient, "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0")
		out, err := p.Rollback(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventCompensated))
		_, jerr = rebootJobFor(node)
		Expect(jerr).To(HaveOccurred(), "롤백했는데 재부팅 Job 이 남아 있다")
	})

	// 증명: 취소할 Job 이 아예 없어도(재부팅을 요청하기 전 단계에서 롤백) Rollback 은 실패로 보고하지
	// 않는다 — NotFound 는 "이미 취소된 것" 과 구별할 수 없고, 어느 쪽이든 보상 완료다.
	It("tolerates rolling back when no reboot job was ever created", func() {
		node := "reboot-part-f"
		seedRebootNode(node)
		DeferCleanup(func() { cleanupRebootFixture(node) })
		op := mkRebootOp("reboot-part-op-f", node, nil)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		p := NewNodeRebootParticipant(k8sClient, "registry.example.com:5000/kcloud/kcloud-host-exec:v0.1.0")
		out, err := p.Rollback(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventCompensated))
	})
})

// cleanupRebootFixture 는 노드와 재부팅 Job 을 치운다(envtest 는 GC 가 없다).
func cleanupRebootFixture(node string) {
	_ = k8sClient.Delete(ctx, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: naming.AcppRebootJobName(node), Namespace: driverjob.Namespace,
	}}, client.PropagationPolicy(metav1.DeletePropagationBackground))
	_ = k8sClient.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node}},
		client.PropagationPolicy(metav1.DeletePropagationBackground))
}
