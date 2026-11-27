// ============================================================
// compare_test.go: 비교군 B0~B3 측정
// 상세: 같은 장애를 네 구성에 주입해 "조정자가 없었으면 어땠는가" 를 수치로 답한다. 옛 코드를
// 되살리는 대신 축별 게이트(internal/operation/gates.go)를 끄는 방식이라, 되살린 코드가
// 정말 B0 인지 증명하는 문제가 새로 생기지 않는다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package fault

import (
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
)

// probe 는 비교군을 가르는 장애 하나다. 축을 하나씩 겨냥한 것만 골랐다 — 모든 구성에서 같은
// 결과가 나오는 시나리오는 비교표에서 아무것도 말해 주지 않는다.
const staleMarker = "낡은 프로세스가 쓴 값"

type probe struct {
	id     string
	title  string
	axis   string
	run    func(g operation.Gates) (bad bool, observed string)
	expect string
}

// compareProbes 는 축별 대표 장애다.
func compareProbes() []probe {
	return []probe{
		{
			id: "P1", title: "같은 자원에 경합하는 두 작업", axis: "충돌 행렬",
			expect: "둘이 동시에 적용 구간에 있지 않다",
			run:    probeConcurrentApply,
		},
		{
			id: "P2", title: "옛 leader 의 늦은 status 기록", axis: "세대 펜싱",
			expect: "낡은 세대의 기록이 반영되지 않는다",
			run:    probeStaleWriter,
		},
		{
			id: "P3", title: "적용 시작 직후 프로세스 종료", axis: "선행 저널",
			expect: "무엇을 시작했는지 기록이 남는다",
			run:    probeJournalSurvives,
		},
		{
			id: "P4", title: "광고가 돌아오지 않은 채 확정 시도", axis: "검증 게이트",
			expect: "근거 없이 Succeeded 가 되지 않는다",
			run:    probeCommitWithoutEvidence,
		},
	}
}

// probeConcurrentApply 는 같은 자원 키를 선언한 두 작업을 번갈아 굴려 **동시에** 적용 구간에
// 들어가는지 본다. 충돌 행렬이 없으면 둘 다 들어간다.
func probeConcurrentApply(g operation.Gates) (bool, string) {
	defer cleanupOps()
	seedNode("cmp-conflict-node")
	mkOp("cmp-a", "cmp-conflict-node", operation.PartitionReconfigure)
	mkOp("cmp-b", "cmp-conflict-node", operation.PartitionReconfigure)

	h := newHarness()
	h.r.Gates = g
	// 적용을 끝내지 않고 붙잡아 둔다 — 두 작업이 겹치는 창을 만든다.
	h.fakes[operation.PartitionReconfigure].OnApply = func(int,
		*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
		return operation.Outcome{RequeueAfter: time.Second}, nil
	}
	for i := 0; i < 6; i++ {
		_, _ = h.r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "cmp-a"}})
		_, _ = h.r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "cmp-b"}})
	}
	a, b := phaseOf("cmp-a"), phaseOf("cmp-b")
	both := inApplyRegion(a) && inApplyRegion(b)
	return both, fmt.Sprintf("cmp-a=%s cmp-b=%s", a, b)
}

// probeStaleWriter 는 세대가 앞선 뒤 낡은 사본이 status 를 쓰려 할 때 그것이 반영되는지 본다.
func probeStaleWriter(g operation.Gates) (bool, string) {
	defer cleanupOps()
	seedNode("cmp-stale-node")
	mkOp("cmp-stale", "cmp-stale-node", operation.PartitionReconfigure)

	h := newHarness()
	h.r.Gates = g
	h.run("cmp-stale", 4) // 잠금 취득까지 굴린다

	var cur npuv1alpha1.AcceleratorOperation
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "cmp-stale"}, &cur)).To(Succeed())
	stale := cur.DeepCopy() // 낡은 사본 — 이 시점의 세대를 들고 있다

	// 서버 쪽 세대를 올린다(다른 프로세스가 잠금을 새로 잡은 상황).
	cur.Status.Epoch += 5
	Expect(k8sClient.Status().Update(ctx, &cur)).To(Succeed())

	// 낡은 사본이 자기 결과를 쓴다.
	stale.Status.Message = staleMarker
	// 컨트롤러의 status 기록 경로를 그대로 태운다 — 펜싱은 거기 있다.
	_ = h.r.WriteStatusForTest(ctx, stale)

	var after npuv1alpha1.AcceleratorOperation
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "cmp-stale"}, &after)).To(Succeed())
	landed := after.Status.Message == staleMarker
	return landed, fmt.Sprintf("낡은 기록 반영=%v", landed)
}

// probeJournalSurvives 는 적용을 시작한 뒤 저널에 그 사실이 남는지 본다.
func probeJournalSurvives(g operation.Gates) (bool, string) {
	defer cleanupOps()
	seedNode("cmp-journal-node")
	mkOp("cmp-journal", "cmp-journal-node", operation.PartitionReconfigure)

	h := newHarness()
	h.r.Gates = g
	h.fakes[operation.PartitionReconfigure].OnApply = func(int,
		*npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
		return operation.Outcome{RequeueAfter: time.Second}, nil
	}
	op := h.run("cmp-journal", 6)
	has := false
	for _, e := range op.Status.Journal {
		if e.Step == operation.StepApplyStarted {
			has = true
		}
	}
	return !has, fmt.Sprintf("ApplyStarted 기록=%v", has)
}

// probeCommitWithoutEvidence 는 근거를 못 만드는 상황에서 Succeeded 로 가는지 본다.
func probeCommitWithoutEvidence(g operation.Gates) (bool, string) {
	defer cleanupOps()
	seedNode("cmp-verify-node")
	mkOp("cmp-verify", "cmp-verify-node", operation.PartitionReconfigure)

	h := newHarness()
	h.r.Gates = g
	h.fakes[operation.PartitionReconfigure].Verify = true
	op := h.run("cmp-verify", 8)
	bad := op.Status.Phase == npuv1alpha1.OpPhaseSucceeded
	return bad, "phase=" + op.Status.Phase
}

func inApplyRegion(phase string) bool {
	switch phase {
	case npuv1alpha1.OpPhasePrepared, npuv1alpha1.OpPhaseQuiescing, npuv1alpha1.OpPhaseApplying:
		return true
	}
	return false
}

func phaseOf(name string) string {
	var op npuv1alpha1.AcceleratorOperation
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &op); err != nil {
		return ""
	}
	return op.Status.Phase
}

var _ = Describe("비교군 B0~B3", func() {
	It("measures the same faults under each configuration", func() {
		for _, b := range operation.Baselines() {
			for _, p := range compareProbes() {
				bad, observed := p.run(b.Gates)
				report.addCompare(compareResult{
					Baseline: b.Name, BaselineDesc: b.Desc,
					ProbeID: p.id, Probe: p.title, Axis: p.axis,
					Expect: p.expect, Observed: observed, Bad: bad,
				})
			}
		}
		// B2 는 네 기능이 전부 켜진 구성이다 — 여기서 하나라도 잘못된 판정이 나오면 회귀다.
		for _, c := range report.compares {
			if c.Baseline == "B2" {
				Expect(c.Bad).To(BeFalse(),
					"B2(%s)에서 잘못된 판정: %s", c.ProbeID, c.Observed)
			}
		}
	})
})
