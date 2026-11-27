// ============================================================
// operation_lease_test.go: 노드 Lease 획득·만료 envtest
// 상세: 같은 노드를 두 소유자가 동시에 쥐지 못하고, 만료된 Lease 는 회수된다.
// 생성일: 2026-08-01
// ============================================================
package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
)

var _ = Describe("node lease", func() {
	newManager := func(holder string, now func() time.Time) *operation.LeaseManager {
		return &operation.LeaseManager{
			Client: k8sClient, Namespace: naming.OperatorNamespace(),
			Holder: holder, Duration: 60 * time.Second, Now: now,
		}
	}
	cleanupLease := func(node string) {
		_ = k8sClient.Delete(ctx, &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
			Name: operation.NodeLeaseName(node), Namespace: naming.OperatorNamespace(),
		}}, client.PropagationPolicy(metav1.DeletePropagationBackground))
	}

	// 증명: 두 번째 소유자는 살아 있는 Lease 를 뺏지 못한다. 이것이 동시 mutation 을 막는 마지막 벽이다.
	// 깨는 뮤테이션: Acquire 에서 소유자 비교를 빼면 실패한다.
	It("refuses a second holder while the lease is live", func() {
		node := "lease-node-a"
		DeferCleanup(func() { cleanupLease(node) })
		now := time.Now()
		clock := func() time.Time { return now }

		ok, _, err := newManager("op-first", clock).Acquire(ctx, node, "op-first")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		ok2, _, err := newManager("op-second", clock).Acquire(ctx, node, "op-second")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok2).To(BeFalse(), "살아 있는 Lease 를 두 번째 소유자가 가져갔다")
	})

	// 증명: 같은 소유자의 재획득은 갱신이다(재진입 시 자기 Lease 에 막히면 안 된다).
	// 깨는 뮤테이션: 소유자 일치 분기를 지우면 실패한다.
	It("renews for the same holder", func() {
		node := "lease-node-b"
		DeferCleanup(func() { cleanupLease(node) })
		now := time.Now()
		m := newManager("op-same", func() time.Time { return now })
		ok, first, err := m.Acquire(ctx, node, "op-same")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		now = now.Add(30 * time.Second)
		ok2, second, err := m.Acquire(ctx, node, "op-same")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok2).To(BeTrue())
		Expect(second.Time.After(first.Time)).To(BeTrue(), "갱신인데 만료 시각이 안 밀렸다")
	})

	// 증명: 만료된 Lease 는 다른 소유자가 회수한다(소유자 프로세스가 사라져도 노드가 안 잠긴다).
	// 깨는 뮤테이션: 만료 검사를 빼면 실패한다.
	It("lets another holder take over an expired lease", func() {
		node := "lease-node-c"
		DeferCleanup(func() { cleanupLease(node) })
		now := time.Now()
		ok, _, err := newManager("op-dead", func() time.Time { return now }).Acquire(ctx, node, "op-dead")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		later := now.Add(120 * time.Second)
		ok2, _, err := newManager("op-live", func() time.Time { return later }).Acquire(ctx, node, "op-live")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok2).To(BeTrue(), "만료된 Lease 를 회수하지 못했다")

		var lease coordv1.Lease
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: operation.NodeLeaseName(node), Namespace: naming.OperatorNamespace(),
		}, &lease)).To(Succeed())
		Expect(*lease.Spec.HolderIdentity).To(Equal("op-live"))
	})

	// 증명: 소유자가 아니면 Release 가 남의 Lease 를 지우지 않는다.
	// 깨는 뮤테이션: Release 의 소유자 검사를 빼면 실패한다.
	It("does not release someone else's lease", func() {
		node := "lease-node-d"
		DeferCleanup(func() { cleanupLease(node) })
		now := time.Now()
		clock := func() time.Time { return now }
		_, _, err := newManager("op-owner", clock).Acquire(ctx, node, "op-owner")
		Expect(err).NotTo(HaveOccurred())

		Expect(newManager("op-stranger", clock).Release(ctx, node, "op-stranger")).To(Succeed())

		var lease coordv1.Lease
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: operation.NodeLeaseName(node), Namespace: naming.OperatorNamespace(),
		}, &lease)).To(Succeed())
		Expect(*lease.Spec.HolderIdentity).To(Equal("op-owner"))
	})
})
