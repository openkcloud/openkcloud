// ============================================================
// operation_participants_acpp_test.go: 적용 경로 위임 participant envtest
// 상세: participant 가 기존 적용 경로를 그대로 돌리고, 그 결과 phase 를 사건으로 옮기는지 본다.
//
//	재진입(같은 단계 재호출) 안전성과, RNGD 경로의 복원 성패를 실제로 구분하는지도 고정한다.
//
// 생성일: 2026-08-01
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/partition/nvidia"
)

// mkOperationFor 는 정책 하나에 대한 operation 을 프로덕션과 같은 모양으로 만든다.
func mkOperationFor(name string, acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string) *npuv1alpha1.AcceleratorOperation {
	opType := OperationTypeForACPP(acpp)
	return &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: string(opType), NodeName: node, Vendor: acpp.Spec.Vendor,
			TransactionID: name,
			Owner: npuv1alpha1.OperationOwner{
				Kind: "AcceleratorPartitionPolicy", Name: acpp.Name,
				UID: string(acpp.UID), Generation: acpp.Generation,
			},
			ResourceKeys: ResourceKeysForACPP(acpp, node, opType),
		},
	}
}

var _ = Describe("ACPP participant", func() {
	// 증명: participant 가 적용 경로를 실제로 돌려 하드웨어 명령이 나가고, 수렴하면 완료 사건을 준다.
	//       (도달 여부를 명령 수로 먼저 고정한다 — 경로에 못 갔는데 통과하는 사고를 막는다.)
	// 깨는 뮤테이션: Apply 가 runTarget 대신 곧바로 EventApplyDone 을 돌려주게 바꾸면 steps==0 이라 실패한다.
	It("runs the existing apply path and reports completion", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{"kcloud.ai/aop-part": "true"}
		seedNvidiaNode("aop-part-node", labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("aop-part-node") })

		acpp := mkNvidiaACPP("aop-part-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		op := mkOperationFor("aop-part-op", acpp, "aop-part-node")
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		steps := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return countingExec{steps: &steps} }
		p := NewACPPParticipant(r)

		var last operation.Outcome
		Eventually(func() operation.Event {
			out, err := p.Apply(ctx, op)
			Expect(err).NotTo(HaveOccurred())
			last = out
			return out.Event
		}, "20s", "300ms").Should(Equal(operation.EventApplyDone))
		Expect(steps).To(BeNumerically(">", 0), "적용 경로에 도달하지 못했다 — 이 스펙은 아무것도 검증하지 못한다")
		Expect(last.Message).NotTo(BeEmpty())

		// 정책 status 도 함께 갱신돼야 한다 — operation 만 알고 정책은 모르는 상태를 만들지 않는다.
		var got npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &got)).To(Succeed())
		Expect(got.Status.Targets).NotTo(BeEmpty())

		// 증명: 이미 수렴한 뒤 다시 Apply 를 불러도 하드웨어를 다시 건드리지 않는다(재진입 안전).
		// 이 보호는 shouldReverify(runTarget 최상단의 evidence 재사용 게이트)가 담당한다 —
		// 깨는 뮤테이션: shouldReverify 를 무조건 true 로 바꾸면 steps 가 6→12 로 늘어 이 Expect 가 잡는다.
		converged := steps
		out2, err2 := p.Apply(ctx, op)
		Expect(err2).NotTo(HaveOccurred())
		Expect(out2.Event).To(Equal(operation.EventApplyDone), "이미 끝난 작업을 다시 불러도 완료를 다시 보고해야 한다")
		Expect(steps).To(Equal(converged), "수렴한 뒤의 재호출이 하드웨어를 다시 건드렸다 — 재진입이 안전하지 않다")
	})

	// 증명: 자원 키가 작업 종류에 따라 갈린다. 이것이 partition 과 sharing 을 나누는 유일한 이유다.
	// 깨는 뮤테이션: 두 종류에 같은 키를 주면 실패한다.
	It("declares different resource keys per operation type", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		acpp := mkNvidiaACPP("aop-keys-acpp", map[string]string{"kcloud.ai/aop-keys": "true"})
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName: "aop-keys-node", GPUPCIs: []string{"0000:41:00.0"},
			OwnerUID: string(acpp.UID), Profile: "1g.6gb", Count: 4,
		}}
		Expect(k8sClient.Status().Update(ctx, acpp)).To(Succeed())

		partKeys := ResourceKeysForACPP(acpp, "aop-keys-node", operation.PartitionReconfigure)
		shareKeys := ResourceKeysForACPP(acpp, "aop-keys-node", operation.SharingModeChange)
		Expect(partKeys).To(ContainElement("device/0000:41:00.0/partition"))
		Expect(partKeys).To(ContainElement("node/aop-keys-node/cordon"))
		Expect(shareKeys).To(ContainElement("device/0000:41:00.0/sharing"))
		Expect(shareKeys).NotTo(ContainElement("node/aop-keys-node/cordon"),
			"공유 변경은 cordon 을 요구하지 않는다 — 노드 키를 선언하면 무관한 작업까지 직렬화된다")
	})

	// 증명: layout 이 없는 정책은 공유 작업으로 분류된다.
	// 깨는 뮤테이션: OperationTypeForACPP 를 상수 반환으로 바꾸면 실패한다.
	It("classifies a sharing-only policy as a sharing operation", func() {
		acpp := mkNvidiaACPP("aop-type-acpp", nil)
		Expect(OperationTypeForACPP(acpp)).To(Equal(operation.PartitionReconfigure))
		acpp.Spec.Layout = nil
		Expect(OperationTypeForACPP(acpp)).To(Equal(operation.SharingModeChange))
	})

	// 증명: RNGD 경로는 복원 성공/실패를 모두 ACPPPhaseFailed 로 남기고, 실제 성패는
	// ACPPCondRolledBack condition 에만 있다. Rollback 이 phase 만 보면 복원 실패를 성공으로
	// 오판한다 — condition 까지 읽어야 실제 결과와 일치한다.
	// 깨는 뮤테이션: condition 검사를 지우고 phase 만으로 판정하면, 복원-실패 케이스가
	// EventCompensated 로 잘못 보고돼 이 It 이 실패한다(아래 두 케이스 모두 같은 코드 경로를 탄다).
	It("distinguishes a genuine RNGD restore success from a restore failure", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)

		By("restore succeeds: reports Compensated")
		selOK := map[string]string{"kcloud.ai/aop-rb-ok": "true"}
		seedRngdNode("aop-rb-ok-node", selOK)
		upsertUnifiedDS(selOK) // 기본 env RNGD_PARTITION_POLICY=dual-core
		acppOK := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "aop-rb-ok-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   selOK,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "4core.24gb", CountPerDevice: 2}}, // DS 와 달라 diff.Changed
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acppOK)).To(Succeed())
		rOK := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: failVerifier{}}
		opOK := mkOperationFor("aop-rb-ok-op", acppOK, "aop-rb-ok-node")
		outOK, errOK := NewACPPParticipant(rOK).Rollback(ctx, opOK)
		Expect(errOK).NotTo(HaveOccurred())
		Expect(outOK.Event).To(Equal(operation.EventCompensated), "복원이 실제로 성공했는데 보상완료로 보고하지 않았다")

		By("restore itself fails (DS UID changed): reports CompensationFailed")
		selBad := map[string]string{"kcloud.ai/aop-rb-bad": "true"}
		seedRngdNode("aop-rb-bad-node", selBad)
		upsertUnifiedDS(selBad)
		acppBad := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "aop-rb-bad-acpp"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector:   selBad,
				Vendor:         "furiosa",
				Layout:         []npuv1alpha1.PartitionLayout{{Profile: "4core.24gb", CountPerDevice: 2}},
				DeletionPolicy: "Retain",
			},
		}
		Expect(k8sClient.Create(ctx, acppBad)).To(Succeed())
		rBad := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Verifier: uidBustingFailVerifier{}}
		opBad := mkOperationFor("aop-rb-bad-op", acppBad, "aop-rb-bad-node")
		outBad, errBad := NewACPPParticipant(rBad).Rollback(ctx, opBad)
		Expect(errBad).NotTo(HaveOccurred())
		Expect(outBad.Event).To(Equal(operation.EventCompensationFailed),
			"복원 자체가 실패했는데 보상완료로 보고했다 — 실제로 되돌아가지 않은 장치를 되돌아간 것으로 커밋하게 된다")
	})
})
