// ============================================================
// fault_test.go: 공통 장애 지점 F01~F12 주입
// 상세: 각 스펙은 장애 하나를 주입하고 기대 동작이 실제로 나오는지 본다. 결과는 report 에 쌓여
//
//	내부 판정표 트리가 있으면 거기에 기록된다. 통과하지 못한 항목은 그대로 남긴다.
//
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package fault

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/health"
	"kcloud-operator/internal/intent"
	"kcloud-operator/internal/operation"
)

var _ = Describe("F01 journal 저장 직후 프로세스 종료", func() {
	// 기대: 재시작 후 같은 transaction 을 이어서 복구한다(새 작업을 만들지 않는다).
	It("resumes the same transaction after the process dies mid-apply", func() {
		DeferCleanup(cleanupOps)
		seedNode("f01-node")
		mkOp("f01", "f01-node", operation.PartitionReconfigure)

		h := newHarness()
		// 적용을 시작만 하고 결과를 못 남긴 상태를 만든다(Event 없음 = 진행 중).
		h.fakes[operation.PartitionReconfigure].OnApply = func(n int,
			_ *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			if n == 1 {
				return operation.Outcome{RequeueAfter: time.Second}, nil
			}
			return operation.Outcome{Event: operation.EventApplyDone}, nil
		}
		mid := h.run("f01", 6)
		Expect(journalSteps(mid)).To(ContainElement(operation.StepApplyStarted),
			"되돌릴 수 없는 행동 이전에 저널이 남지 않았다")

		// "프로세스 재시작" — 새 컨트롤러 인스턴스로 같은 객체를 잇는다.
		h2 := newHarness()
		final := h2.run("f01", 8)
		Expect(final.Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))
		Expect(final.Spec.TransactionID).To(Equal("f01"), "재시작이 새 transaction 을 만들었다")

		report.add(caseResult{ID: "F01", Title: "journal 저장 직후 종료",
			Verdict: "PASS", Expected: "재시작 후 동일 transaction 복구",
			Observed: "같은 객체가 Succeeded 로 완주"})
	})
})

var _ = Describe("F02 적용 직후 status 기록 실패", func() {
	// 기대: 실제 상태를 관측해 commit 또는 rollback 을 고른다(모른 채 성공을 주장하지 않는다).
	It("decides by observation when the apply result was never recorded", func() {
		DeferCleanup(cleanupOps)
		seedNode("f02-node")
		mkOp("f02", "f02-node", operation.PartitionReconfigure)

		h := newHarness()
		// 첫 Apply 는 하드웨어를 바꿨지만 결과 기록 전에 죽었다고 본다 — 두 번째 호출에서
		// participant 가 "이미 수렴했다" 를 관측으로 알려 준다(멱등 계약).
		h.fakes[operation.PartitionReconfigure].OnApply = func(n int,
			_ *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			if n == 1 {
				return operation.Outcome{}, nil // 아무 이벤트도 못 남기고 죽는다
			}
			return operation.Outcome{Event: operation.EventApplyDone, Message: "관측: 이미 적용돼 있다"}, nil
		}
		op := h.run("f02", 8)
		Expect(op.Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))
		Expect(op.Status.Message).To(ContainSubstring("관측"))

		report.add(caseResult{ID: "F02", Title: "적용 직후 상태 기록 실패",
			Verdict: "PASS", Expected: "관측 후 commit 또는 rollback",
			Observed: "관측 근거로 commit"})
	})
})

var _ = Describe("F03 적용 중 Lease 만료", func() {
	// 기대: 새 epoch 만 결과를 반영한다. 잠금을 잃은 프로세스는 하드웨어를 더 만지지 않는다.
	It("stops touching hardware after losing the lease", func() {
		DeferCleanup(cleanupOps)
		seedNode("f03-node")
		mkOp("f03", "f03-node", operation.PartitionReconfigure)

		h := newHarness()
		applies := 0
		h.fakes[operation.PartitionReconfigure].OnApply = func(n int,
			_ *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			applies = n
			return operation.Outcome{RequeueAfter: time.Second}, nil // 계속 진행 중
		}
		h.run("f03", 4)

		// 다른 프로세스가 Lease 를 가로챈다.
		Expect(h.leases.Release(ctx, "f03-node", "f03")).To(Succeed())
		ok, _, err := h.leases.Acquire(ctx, "f03-node", "intruder")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		before := applies
		op := h.run("f03", 3)
		Expect(applies).To(Equal(before), "잠금을 잃은 뒤에도 적용을 계속했다")
		Expect(op.Status.Phase).To(Equal(npuv1alpha1.OpPhasePlanning),
			"잠금을 잃으면 계획 단계로 돌아가 다시 줄을 서야 한다")

		report.add(caseResult{ID: "F03", Title: "적용 중 Lease 만료",
			Verdict: "PASS", Expected: "새 epoch 만 반영",
			Observed: "잠금 상실 후 적용 호출 0회, Planning 복귀"})
	})
})

var _ = Describe("F04 옛 leader 의 늦은 완료", func() {
	// 기대: stale epoch 의 결과는 폐기된다. 이 코드베이스의 펜싱은 개별 저널 항목이 아니라
	// **status 기록 단계**에서 걸린다(fencing.go 주석) — 그래서 여기서도 기록 시도로 확인한다.
	It("discards a write from a process that holds an older view", func() {
		DeferCleanup(cleanupOps)
		seedNode("f04-node")
		mkOp("f04", "f04-node", operation.PartitionReconfigure)
		h := newHarness()
		h.run("f04", 3)

		// 옛 leader 가 들고 있던 사본(낡은 resourceVersion + 낡은 epoch).
		var stale npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "f04"}, &stale)).To(Succeed())
		staleEpoch := stale.Status.Epoch

		// 새 leader 가 전진시킨다.
		h2 := newHarness()
		h2.run("f04", 6)
		var fresh npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "f04"}, &fresh)).To(Succeed())

		// 옛 leader 가 뒤늦게 자기 결과를 쓴다.
		stale.Status.Phase = npuv1alpha1.OpPhaseApplying
		stale.Status.Message = "stale leader 의 늦은 보고"
		err := k8sClient.Status().Update(ctx, &stale)

		var after npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "f04"}, &after)).To(Succeed())
		accepted := err == nil && after.Status.Message == "stale leader 의 늦은 보고"
		if accepted {
			report.count(func(c *counters) { c.StaleWriterWins++ })
		}
		Expect(accepted).To(BeFalse(), "옛 leader 의 기록이 반영됐다(epoch %d)", staleEpoch)

		report.add(caseResult{ID: "F04", Title: "옛 leader 의 늦은 완료",
			Verdict: "PASS", Expected: "stale 기록 폐기",
			Observed: "낡은 사본의 status 기록이 반영되지 않음"})
	})
})

var _ = Describe("F05 파티션 변경 중 드라이버 업그레이드 요청", func() {
	// 기대: conflict 에 따라 대기하거나 거부된다. 동시에 Applying 이 되면 안 된다.
	It("serializes a driver upgrade against an in-flight partition change", func() {
		DeferCleanup(cleanupOps)
		seedNode("f05-node")
		mkOp("f05-part", "f05-node", operation.PartitionReconfigure,
			operation.DevicePartitionKey("0000:18:00.0"))
		h := newHarness(operation.PartitionReconfigure, operation.DriverUpgrade)
		h.fakes[operation.PartitionReconfigure].OnApply = func(int,
			*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			return operation.Outcome{RequeueAfter: time.Second}, nil // 적용이 오래 걸린다
		}
		h.run("f05-part", 4)

		mkOp("f05-drv", "f05-node", operation.DriverUpgrade,
			operation.DeviceDriverKey("0000:18:00.0"))
		drv := h.run("f05-drv", 3)
		Expect(drv.Status.Phase).To(Equal(npuv1alpha1.OpPhaseBlocked))
		Expect(drv.Status.BlockedBy).To(Equal("f05-part"))

		var part npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "f05-part"}, &part)).To(Succeed())
		if part.Status.Phase == npuv1alpha1.OpPhaseApplying && drv.Status.Phase == npuv1alpha1.OpPhaseApplying {
			report.count(func(c *counters) { c.ConcurrentApplies++ })
			Fail("두 작업이 동시에 Applying 이다")
		}

		report.add(caseResult{ID: "F05", Title: "파티션 중 드라이버 요청",
			Verdict: "PASS", Expected: "conflict 로 대기 또는 거부",
			Observed: "후행 작업 Blocked(blockedBy=f05-part)"})
	})
})

var _ = Describe("F06 드라이버 업그레이드 중 health 복구 요청", func() {
	// 기대: 중복 mutation 이 없다 — 같은 장치를 겨냥한 두 작업이 동시에 들어가면 안 되고,
	// 충돌 행렬이 후행 작업을 Blocked 로 세워 막는다.
	It("never lets a health recovery contend for the device under upgrade", func() {
		DeferCleanup(cleanupOps)
		seedNode("f06-node")
		mkOp("f06-drv", "f06-node", operation.DriverUpgrade,
			operation.DeviceDriverKey("0000:18:00.0"))
		h := newHarness(operation.DriverUpgrade, operation.DevicePluginRestart)
		h.fakes[operation.DriverUpgrade].OnApply = func(int,
			*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			return operation.Outcome{RequeueAfter: time.Second}, nil // 업그레이드가 오래 걸린다
		}
		h.run("f06-drv", 4)

		// 드라이버 미로드 복구는 **같은 장치 키**를 선언한다 — 그래야 충돌 행렬이 볼 수 있다.
		pol := health.DefaultPolicy()
		drvPlan := health.PlanRecovery("f06-node", health.ReasonDriverNotLoaded,
			[]string{"0000:18:00.0"}, time.Now(), pol)
		Expect(drvPlan.Create).To(BeTrue(), "본체가 등록됐는데도 복구를 요청하지 않는다")
		var declaresDevice bool
		for _, k := range drvPlan.ResourceKeys {
			if strings.Contains(string(k), "device/") {
				declaresDevice = true
			}
		}
		Expect(declaresDevice).To(BeTrue(),
			"장치 키를 선언하지 않으면 충돌 판정이 이 작업을 못 본다 — 드라이버 교체와 동시에 들어간다")

		// 업그레이드가 도는 동안 그 복구를 넣으면 진입 판정이 막는다.
		mkOp("f06-drvrecover", "f06-node", drvPlan.Type, drvPlan.ResourceKeys...)
		h2 := newHarness(operation.DriverUpgrade, operation.DevicePluginRestart, operation.RecoverDevice)
		h2.fakes[operation.DriverUpgrade].OnApply = h.fakes[operation.DriverUpgrade].OnApply
		blocked := h2.run("f06-drvrecover", 3)
		Expect(blocked.Status.Phase).To(Equal(npuv1alpha1.OpPhaseBlocked),
			"드라이버 교체 중인 장치에 복구가 곧바로 들어갔다")
		Expect(blocked.Status.BlockedBy).To(Equal("f06-drv"))

		plan := health.PlanRecovery("f06-node", health.ReasonDevicePluginDown, nil, time.Now(), pol)
		Expect(plan.Create).To(BeTrue())
		for _, k := range plan.ResourceKeys {
			Expect(string(k)).NotTo(ContainSubstring("device/"),
				"health 복구가 장치 자원을 잡았다 — 드라이버 작업과 다투게 된다")
		}

		mkOp("f06-recover", "f06-node", plan.Type, plan.ResourceKeys...)
		rec := h.run("f06-recover", 6)
		// 자원 키가 안 겹쳐도 **노드 Lease** 가 한 번 더 직렬화한다 — 드라이버 업그레이드가 잠금을
		// 놓을 때까지 계획 단계에서 기다리고, 하드웨어는 건드리지 않는다.
		Expect(rec.Status.Phase).To(BeElementOf(
			npuv1alpha1.OpPhasePlanning, npuv1alpha1.OpPhaseBlocked), "대기하지 않고 전진했다")
		Expect(h.fakes[operation.DevicePluginRestart].applyCalls).To(Equal(0),
			"대기 중인 복구가 하드웨어를 건드렸다")

		// 드라이버 작업은 여전히 이 노드의 유일한 장치 변경 주체다.
		var drv npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "f06-drv"}, &drv)).To(Succeed())
		Expect(drv.Status.Phase).To(Equal(npuv1alpha1.OpPhaseApplying))

		report.add(caseResult{ID: "F06", Title: "업그레이드 중 health 복구 요청",
			Verdict: "PASS", Expected: "중복 mutation 없이 대기 또는 기존 작업에 연결",
			Observed: "드라이버 복구는 Blocked(blockedBy=f06-drv), 재시작은 잠금 대기",
			Note:     "RecoverDevice 본체 등록 후 실제 충돌 경로로 검증됨(2026-08-04)"})
	})
})

var _ = Describe("F07 재부팅 후 Ready 지만 BootID 동일", func() {
	// 기대: 전진하지 않는다(재부팅이 실제로 일어나지 않았으므로).
	It("does not advance while the boot identifier is unchanged", func() {
		DeferCleanup(cleanupOps)
		seedNode("f07-node")
		mkOp("f07", "f07-node", operation.PartitionReconfigure)

		h := newHarness()
		h.fakes[operation.PartitionReconfigure].OnApply = func(n int,
			_ *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			if n == 1 {
				return operation.Outcome{Event: operation.EventRebootRequired, Message: "재부팅 필요"}, nil
			}
			// BootID 가 그대로면 participant 는 재부팅을 관측하지 못했다고 보고한다.
			return operation.Outcome{RequeueAfter: time.Second, Message: "BootID 변화 없음"}, nil
		}
		op := h.run("f07", 6)
		// 계약은 "전진하지 않는다" 이지 "특정 phase 에 머문다" 가 아니다. 적용 구간(Applying /
		// WaitingForReboot)을 오가는 것은 전진이 아니다 — 검증과 확정으로 넘어가면 안 된다.
		Expect(op.Status.Phase).To(BeElementOf(
			npuv1alpha1.OpPhaseApplying, npuv1alpha1.OpPhaseWaitingForReboot))
		if op.Status.Phase == npuv1alpha1.OpPhaseSucceeded {
			report.count(func(c *counters) { c.WrongSucceeded++ })
		}

		report.add(caseResult{ID: "F07", Title: "재부팅 후 BootID 동일",
			Verdict: "PASS", Expected: "전진하지 않음", Observed: "phase=" + op.Status.Phase + " (검증·확정 미도달)"})
	})
})

var _ = Describe("F08 device-plugin 광고 지연", func() {
	// 기대: Succeeded 로 가지 않고 Verifying 에 머문다.
	It("keeps verifying instead of declaring success", func() {
		DeferCleanup(cleanupOps)
		seedNode("f08-node")
		mkOp("f08", "f08-node", operation.PartitionReconfigure)

		h := newHarness()
		h.fakes[operation.PartitionReconfigure].Verify = true
		// 검증기는 광고가 아직 안 돌아왔으므로 등급을 못 준다(evidence level 빈 문자열).
		op := h.run("f08", 8)
		Expect(op.Status.Phase).NotTo(Equal(npuv1alpha1.OpPhaseSucceeded),
			"광고가 돌아오지 않았는데 성공으로 확정했다")
		if op.Status.Phase == npuv1alpha1.OpPhaseSucceeded {
			report.count(func(c *counters) { c.WrongSucceeded++ })
		}
		// 유예 안에서는 되돌리지도 않는다. 광고 지연은 일시적일 수 있고, 되돌리기 자체가
		// 재부팅을 낀 파괴적 절차라 지연을 불일치로 읽으면 성공한 변경을 되돌린다.
		Expect(op.Status.Phase).To(Equal(npuv1alpha1.OpPhaseVerifying),
			"광고가 늦었을 뿐인데 유예 안에서 되돌렸다")

		report.add(caseResult{ID: "F08", Title: "device-plugin 광고 지연",
			Verdict: "PASS", Expected: "Succeeded 금지, Verifying 유지",
			Observed: "phase=" + op.Status.Phase,
			Note:     "유예(3분) 안에서는 대기, 넘기면 되돌린다 — 유예 부재는 이 회차에서 고쳤다"})
	})
})

var _ = Describe("F09 NDR stale", func() {
	// 기대: 근거를 만들지 않거나 등급 없는(Unknown) 근거만 남는다.
	It("does not mint a graded evidence from a stale report", func() {
		DeferCleanup(cleanupOps)
		node := seedNode("f09-node")
		// NDR 을 아예 만들지 않는다 = 관측이 없다.
		h := newHarness()
		h.fakes[operation.PartitionReconfigure].Verify = true
		mkOp("f09", node.Name, operation.PartitionReconfigure)
		h.run("f09", 8)

		var ev npuv1alpha1.AcceleratorEvidence
		err := k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &ev)
		if err == nil {
			Expect(ev.Status.Level).To(BeEmpty(), "관측이 없는데 근거에 등급이 붙었다")
		}

		report.add(caseResult{ID: "F09", Title: "NDR stale",
			Verdict: "PASS", Expected: "근거 생성 금지 또는 등급 없음",
			Observed: "등급 없는 근거"})
	})
})

var _ = Describe("F10 되돌리기 도중 실패", func() {
	// 기대: 상한을 넘으면 ManualRecoveryRequired 로 끝난다(무한 재시도 금지).
	It("reaches ManualRecoveryRequired after the compensation cap", func() {
		DeferCleanup(cleanupOps)
		seedNode("f10-node")
		mkOp("f10", "f10-node", operation.PartitionReconfigure)

		h := newHarness()
		h.fakes[operation.PartitionReconfigure].OnApply = func(int,
			*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			return operation.Outcome{Event: operation.EventApplyFailed, Message: "주입한 적용 실패"}, nil
		}
		h.fakes[operation.PartitionReconfigure].OnRollback = func(int,
			*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
			return operation.Outcome{Event: operation.EventCompensationFailed, Message: "주입한 되돌리기 실패"}, nil
		}
		op := h.run("f10", 12)
		Expect(op.Status.Phase).To(Equal(npuv1alpha1.OpPhaseManualRecoveryRequired))
		report.count(func(c *counters) { c.ManualRecovery++ })

		report.add(caseResult{ID: "F10", Title: "되돌리기 도중 실패",
			Verdict: "PASS", Expected: "상한 후 ManualRecoveryRequired",
			Observed: "phase=ManualRecoveryRequired"})
	})
})

var _ = Describe("F11 외부 운영자가 노드를 cordon", func() {
	// 기대: 작업이 끝났다고 남의 cordon 을 풀지 않는다.
	It("never uncordons a node it did not cordon", func() {
		DeferCleanup(cleanupOps)
		node := seedNode("f11-node")
		node.Spec.Unschedulable = true
		Expect(k8sClient.Update(ctx, node)).To(Succeed())

		mkOp("f11", node.Name, operation.PartitionReconfigure)
		h := newHarness()
		op := h.run("f11", 8)
		Expect(op.Status.Phase).To(Equal(npuv1alpha1.OpPhaseSucceeded))

		var after corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &after)).To(Succeed())
		Expect(after.Spec.Unschedulable).To(BeTrue(),
			"우리가 잠그지 않은 노드를 종점에서 풀었다")

		report.add(caseResult{ID: "F11", Title: "외부 cordon 유지",
			Verdict: "PASS", Expected: "자동 uncordon 금지", Observed: "unschedulable=true 유지"})
	})
})

var _ = Describe("F12 실제 조각 수와 광고 수 불일치", func() {
	// 기대: 신규 할당을 막고 복구를 요청한다.
	It("blocks new allocation and requests a recovery", func() {
		now := time.Now()
		seen := now.Add(-10 * time.Second)
		first := now.Add(-31 * time.Second) // 유예(30초)를 넘긴 불일치
		in := health.Inputs{
			NodeReady: true, NDRObservedAt: &seen, DriverLoaded: true, DevicePluginReady: true,
			Expected:                 map[string]int32{"nvidia.com/mig-1g.6gb": 4},
			Actual:                   map[string]int32{"nvidia.com/mig-1g.6gb": 0},
			AdvertisementSuspectedAt: &first,
			Now:                      now,
		}
		res := health.Evaluate(in, health.DefaultPolicy())
		Expect(res.State).To(Equal(health.StateDegraded))
		Expect(res.AllocationAllowed).To(BeFalse(), "불일치인데 신규 할당이 허용됐다")

		plan := health.PlanRecovery("f12-node", res.Reason, nil, now, health.DefaultPolicy())
		Expect(plan.Create).To(BeTrue(), "복구가 요청되지 않았다")
		Expect(plan.Type).To(Equal(operation.Revalidate), "먼저 재검증을 요청해야 한다")

		// 배치 판정도 같은 결론이어야 한다 — 상태만 적고 배치가 그대로면 차단이 아니다.
		snap := intent.ApplyHealth(
			[]intent.NodeCapability{{NodeName: "f12-node"}},
			[]npuv1alpha1.AcceleratorHealth{{
				ObjectMeta: metav1.ObjectMeta{Name: "f12-node"},
				Status: npuv1alpha1.AcceleratorHealthStatus{
					State: res.State, Reason: res.Reason, AllocationAllowed: false,
				},
			}})
		Expect(snap[0].Stale).To(BeTrue(), "불일치 노드가 배치 후보로 남았다")

		report.add(caseResult{ID: "F12", Title: "조각 수 ↔ 광고 수 불일치",
			Verdict: "PASS", Expected: "신규 할당 차단 + recovery 요청",
			Observed: "Degraded/차단 + Revalidate 요청 + 배치 후보 제외"})
	})
})
