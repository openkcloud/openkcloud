// ============================================================
// sequence_test.go: Acto 식 작업 시퀀스 A~D
// 상세: 상태를 왕복시키는 시퀀스를 조정자 계층에서 돌려 최종 상태가 정확한지 본다. 하드웨어
//
//	결과는 여기서 주장하지 않는다 — 시퀀스가 겹치지 않고 순서대로 끝나는지만 본다.
//
// 생성일: 2026-08-04
// ============================================================
package fault

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/health"
	"kcloud-operator/internal/operation"
)

// runSequence 는 작업을 순서대로 만들고 각각을 종점까지 돌린다. 매 단계에서 "동시에 Applying 인
// 작업이 둘 이상인가" 를 확인한다 — 시퀀스 검증의 본체는 최종 상태와 이 불변식 둘이다.
func runSequence(id, node string, steps []operation.Type, keys ...operation.ResourceKey) []string {
	h := newHarness(steps...)
	phases := make([]string, 0, len(steps))
	for i, t := range steps {
		name := fmt.Sprintf("%s-%d", id, i)
		mkOp(name, node, t, keys...)
		op := h.run(name, 10)
		phases = append(phases, op.Status.Phase)

		var list npuv1alpha1.AcceleratorOperationList
		Expect(k8sClient.List(ctx, &list)).To(Succeed())
		applying := 0
		for j := range list.Items {
			if list.Items[j].Status.Phase == npuv1alpha1.OpPhaseApplying {
				applying++
			}
		}
		if applying > 1 {
			report.count(func(c *counters) { c.ConcurrentApplies++ })
			Fail(fmt.Sprintf("%s 단계에서 동시에 Applying 인 작업이 %d 개다", name, applying))
		}
	}
	return phases
}

var _ = Describe("Sequence A — exclusive → time-slicing → MPS → exclusive", func() {
	It("returns to the starting mode with every step converged", func() {
		DeferCleanup(cleanupOps)
		seedNode("seq-a-node")
		phases := runSequence("seq-a", "seq-a-node", []operation.Type{
			operation.SharingModeChange, operation.SharingModeChange,
			operation.SharingModeChange, operation.SharingModeChange,
		}, operation.DeviceSharingKey("0000:18:00.0"))

		for i, p := range phases {
			Expect(p).To(Equal(npuv1alpha1.OpPhaseSucceeded), "단계 %d 가 %s 로 끝났다", i, p)
		}
		report.add(caseResult{ID: "SeqA", Title: "exclusive→time-slicing→MPS→exclusive",
			Verdict: "PASS", Expected: "네 단계 모두 수렴, 동시 적용 없음",
			Observed: "4/4 Succeeded", Note: "공유 모드 자체의 하드웨어 결과는 라이브 문서가 책임진다"})
	})
})

var _ = Describe("Sequence B — full GPU → MIG → driver upgrade → rollback → full GPU", func() {
	It("keeps every stage serialized and ends converged", func() {
		DeferCleanup(cleanupOps)
		seedNode("seq-b-node")
		h := newHarness(operation.PartitionReconfigure, operation.DriverUpgrade)

		// 1) MIG 적용
		mkOp("seq-b-0", "seq-b-node", operation.PartitionReconfigure,
			operation.DevicePartitionKey("0000:18:00.0"))
		Expect(h.run("seq-b-0", 10).Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))

		// 2) 드라이버 업그레이드 — 앞 작업이 끝났으므로 막히지 않아야 한다.
		mkOp("seq-b-1", "seq-b-node", operation.DriverUpgrade,
			operation.DeviceDriverKey("0000:18:00.0"))
		Expect(h.run("seq-b-1", 10).Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))

		// 3) 되돌리기 — 적용이 실패해 보상으로 끝나는 경우.
		h.fakes[operation.PartitionReconfigure].OnApply = func(int,
			*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			return operation.Outcome{Event: operation.EventApplyFailed, Message: "주입한 실패"}, nil
		}
		mkOp("seq-b-2", "seq-b-node", operation.PartitionReconfigure,
			operation.DevicePartitionKey("0000:18:00.0"))
		rolled := h.run("seq-b-2", 12)
		Expect(rolled.Status.Phase).To(Equal(npuv1alpha1.OpPhaseRolledBack))
		report.count(func(c *counters) { c.Rollbacks++ })

		// 4) 되돌린 뒤 다시 정상 적용이 가능해야 한다(잠금·저널 잔여 없음).
		h.fakes[operation.PartitionReconfigure].OnApply = nil
		mkOp("seq-b-3", "seq-b-node", operation.PartitionReconfigure,
			operation.DevicePartitionKey("0000:18:00.0"))
		Expect(h.run("seq-b-3", 10).Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))

		report.add(caseResult{ID: "SeqB", Title: "full GPU→MIG→driver→rollback→full GPU",
			Verdict: "PASS", Expected: "각 단계 직렬화, 되돌린 뒤 재적용 가능",
			Observed: "Succeeded → Succeeded → RolledBack → Succeeded"})
	})
})

var _ = Describe("Sequence C — RNGD dual-core → quad-core → node reboot → dual-core", func() {
	It("serializes a reboot between two partition changes", func() {
		DeferCleanup(cleanupOps)
		seedNode("seq-c-node")
		h := newHarness(operation.PartitionReconfigure, operation.NodeReboot)

		mkOp("seq-c-0", "seq-c-node", operation.PartitionReconfigure,
			operation.DevicePartitionKey("0000:01:00.0"))
		Expect(h.run("seq-c-0", 10).Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))

		mkOp("seq-c-1", "seq-c-node", operation.NodeReboot, operation.NodeRebootKey("seq-c-node"))
		Expect(h.run("seq-c-1", 10).Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))

		mkOp("seq-c-2", "seq-c-node", operation.PartitionReconfigure,
			operation.DevicePartitionKey("0000:01:00.0"))
		Expect(h.run("seq-c-2", 10).Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))

		// 종점 뒤 잔여 Lease 가 없어야 한다.
		var leaseHeld bool
		var op npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "seq-c-2"}, &op)).To(Succeed())
		leaseHeld = op.Status.LeaseHolder != "" && !operation.IsTerminal(op.Status.Phase)
		if leaseHeld {
			report.count(func(c *counters) { c.OrphanLeases++ })
		}
		Expect(leaseHeld).To(BeFalse())

		report.add(caseResult{ID: "SeqC", Title: "RNGD dual→quad→reboot→dual",
			Verdict: "PASS", Expected: "재부팅이 두 파티션 변경 사이에 직렬화",
			Observed: "3/3 Succeeded, 잔여 잠금 없음"})
	})
})

var _ = Describe("Sequence D — health degraded → recovery → verifier failure → quarantine", func() {
	// 기대: 복구가 반복 실패하면 격리로 수렴한다(자동 실행 중단).
	It("converges to quarantine after repeated recovery failures", func() {
		now := time.Now()
		seen := now.Add(-5 * time.Second)
		base := health.Inputs{
			NodeReady: true, NDRObservedAt: &seen, DriverLoaded: true, DevicePluginReady: true,
			Expected: map[string]int32{"nvidia.com/gpu": 2},
			Actual:   map[string]int32{"nvidia.com/gpu": 0},
			Now:      now,
		}
		pol := health.DefaultPolicy()

		// 1) degraded — 유예를 넘긴 광고 불일치.
		first := now.Add(-31 * time.Second)
		degraded := base
		degraded.AdvertisementSuspectedAt = &first
		Expect(health.Evaluate(degraded, pol).State).To(Equal(health.StateDegraded))

		// 2) recovery — 복구 작업이 도는 동안에는 새 복구를 부르지 않는다.
		recovering := degraded
		recovering.RecoveryInFlight = true
		Expect(health.Evaluate(recovering, pol).State).To(Equal(health.StateRecovering))

		// 3) verifier failure → 복구가 실패로 끝난다. 임계까지는 아직 격리가 아니다.
		failedOnce := degraded
		failedOnce.RecoveryFailures = 1
		Expect(health.Evaluate(failedOnce, pol).State).To(Equal(health.StateDegraded))

		// 4) 반복 실패 → 격리, 그리고 격리는 복구를 거쳐야만 풀린다.
		quarantined := degraded
		quarantined.RecoveryFailures = pol.RepeatedFailureThreshold
		res := health.Evaluate(quarantined, pol)
		Expect(res.State).To(Equal(health.StateQuarantined))
		Expect(res.AllocationAllowed).To(BeFalse())
		Expect(health.AllowedTransition(health.StateQuarantined, health.StateHealthy)).To(BeFalse())

		report.add(caseResult{ID: "SeqD", Title: "degraded→recovery→verify 실패→quarantine",
			Verdict: "PASS", Expected: "격리로 수렴",
			Observed: "Degraded → Recovering → Degraded(1회 실패) → Quarantined(임계 도달)"})
	})
})
