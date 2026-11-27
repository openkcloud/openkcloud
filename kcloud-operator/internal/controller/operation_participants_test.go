// ============================================================
// operation_participants_test.go: participant 구현 envtest
// 상세: 재검증 participant 가 아무것도 바꾸지 않으면서 검증 요청은 정확히 만드는지 본다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/partition/nvidia"
)

var _ = Describe("revalidate participant", func() {
	// 증명: 소유 정책의 적용 저널에서 기대값이 유도되고, 그 기대 geometry 가 프로덕션 헬퍼가
	//       만드는 문자열과 같다.
	// 깨는 뮤테이션: verifyRequestFor 가 rec.Profile 대신 spec.Layout 을 읽게 바꾸면,
	//       저널과 spec 이 다른 아래 fixture 에서 실패한다.
	It("derives the expectation from the apply record, not from the current spec", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{"kcloud.ai/aop-reval": "true"}
		geom := nvidia.GeometrySummary("1g.6gb", 4)
		seedNvidiaNode("aop-reval-node", labels, true, a30Device(geom, "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("aop-reval-node") })

		acpp := mkNvidiaACPP("aop-reval-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		// spec 을 저널과 다른 값으로 바꿔 둔다 — mkNvidiaACPP 의 기본 Layout(1g.6gb×4)이 아래
		// 저널과 우연히 같으면, "spec 대신 rec 을 읽는다" 뮤테이션을 이 fixture 가 놓친다.
		acpp.Spec.Layout = []npuv1alpha1.PartitionLayout{{Profile: "1g.12gb", CountPerDevice: 2}}
		Expect(k8sClient.Update(ctx, acpp)).To(Succeed())
		// 저널을 프로덕션이 만드는 모양 그대로 시드한다 — OwnerUID 를 빠뜨리면 소유 판정이
		// 어긋나 이 spec 이 아무것도 검증하지 못한 채 통과한다.
		acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName: "aop-reval-node", GPUPCIs: []string{"0000:41:00.0"},
			OwnerUID: string(acpp.UID), Profile: "1g.6gb", Count: 4,
			ExpectedMigCount: 4, ExpectedFullGPUCount: 1, BaselineGPUCount: 1,
			Generation: acpp.Generation, MigPhase: npuv1alpha1.MigPhaseReady,
		}}
		Expect(k8sClient.Status().Update(ctx, acpp)).To(Succeed())

		op := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "aop-reval"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.Revalidate), NodeName: "aop-reval-node", Vendor: "nvidia",
				TransactionID: "tx-reval",
				Owner: npuv1alpha1.OperationOwner{
					Kind: "AcceleratorPartitionPolicy", Name: acpp.Name,
					UID: string(acpp.UID), Generation: acpp.Generation,
				},
			},
		}
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, op, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})

		p := NewRevalidateParticipant(nvidiaReconciler())
		req, ok, err := p.VerifyRequest(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "저널이 있는데 검증 요청을 만들지 못했다")
		// spec.Layout 은 1g.12gb×2 로 바꿔 뒀다 — 아래 값이 그게 아니라 저널의 1g.6gb×4 여야
		// "기대값을 spec 에서, 현재 저널이 아닌 데서" 유도하지 않는다는 게 실제로 고정된다.
		Expect(req.Expectation.Profile).To(Equal("1g.6gb"))
		Expect(req.Expectation.CountPerDevice).To(Equal(int32(4)))
		Expect(req.Expectation.Geometry).To(Equal(geom))
		Expect(req.Expectation.Allocatable).To(HaveKeyWithValue("nvidia.com/mig-1g.6gb", int32(4)))
		Expect(req.ObservedGeometry).To(HaveKeyWithValue("0000:41:00.0", geom))
		Expect(req.SourcePolicy).To(Equal(acpp.Name))
	})

	// 증명: 읽기 전용 작업은 하드웨어를 건드리지 않는다.
	// 깨는 뮤테이션: Apply 가 executor 를 부르게 바꾸면 steps 가 0 이 아니어서 실패한다.
	It("mutates nothing", func() {
		steps := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return countingExec{steps: &steps} }
		p := NewRevalidateParticipant(r)
		op := &npuv1alpha1.AcceleratorOperation{
			Spec: npuv1alpha1.AcceleratorOperationSpec{Type: string(operation.Revalidate), NodeName: "nope"},
		}
		out, err := p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyDone))
		Expect(steps).To(Equal(0))
	})

	// 증명: 저널이 없으면 검증 요청을 만들지 않는다(없는 기대를 지어내지 않는다).
	// 깨는 뮤테이션: rec.Profile 빈 값 가드를 지우면 빈 기대로 true 를 돌려줘 실패한다.
	It("declines to build a request without an apply record", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		acpp := mkNvidiaACPP("aop-reval-empty", map[string]string{"kcloud.ai/aop-reval-empty": "true"})
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		op := &npuv1alpha1.AcceleratorOperation{
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.Revalidate), NodeName: "no-node",
				Owner: npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: acpp.Name},
			},
		}
		_, ok, err := NewRevalidateParticipant(nvidiaReconciler()).VerifyRequest(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	// 증명: 소유 정책 자체가 없으면(삭제됨) 에러가 아니라 "검증 대상 아님" 으로 답한다 —
	// 정책이 사라진 걸 장애로 보고하면 이미 끝난 operation 이 영원히 실패로 남는다.
	// 깨는 뮤테이션: apierrors.IsNotFound 분기를 지우고 모든 Get 오류를 그대로 전파하면
	// 이 테스트가 "Expect(err).NotTo(HaveOccurred())" 에서 깨진다.
	It("declines to build a request when the owner policy is gone", func() {
		op := &npuv1alpha1.AcceleratorOperation{
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.Revalidate), NodeName: "no-node",
				Owner: npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "aop-reval-nonexistent"},
			},
		}
		_, ok, err := NewRevalidateParticipant(nvidiaReconciler()).VerifyRequest(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	// 증명: Rollback 도 read-only 다 — 계약은 되돌릴 것을 요구하지만 revalidate 는 애초에
	// 바꾼 게 없으므로 Compensated 를 즉시 보고하고 executor 를 부르지 않는다.
	// 깨는 뮤테이션: Rollback 의 Event 를 EventApplyDone 으로 바꾸면 실패한다.
	It("rolls back to Compensated without touching hardware", func() {
		steps := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return countingExec{steps: &steps} }
		p := NewRevalidateParticipant(r)
		op := &npuv1alpha1.AcceleratorOperation{
			Spec: npuv1alpha1.AcceleratorOperationSpec{Type: string(operation.Revalidate), NodeName: "nope"},
		}
		out, err := p.Rollback(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventCompensated))
		Expect(steps).To(Equal(0))
	})

	// 증명: 재진입 안전 — Coordinator 는 죽었다 살아나면 같은 phase 를 다시 부를 수 있으므로,
	// Apply/Rollback 을 여러 번 불러도 매번 같은 Outcome 이어야 한다. 세 번만 불러 확인하면
	// "네 번째부터 무너지는" 종류의 상태 누적 버그를 놓친다 — 다섯 번 반복한다.
	// 깨는 뮤테이션: Apply 안에 호출 횟수를 세는 은닉 상태를 넣어 4번째 호출부터 다른 Event 를
	// 돌려주게 하면(3회까지만 통과하는 실수를 재현), 이 테스트가 4·5번째 반복에서 잡는다.
	It("is safe to call repeatedly with identical inputs (reentrancy)", func() {
		r := nvidiaReconciler()
		p := NewRevalidateParticipant(r)
		op := &npuv1alpha1.AcceleratorOperation{
			Spec: npuv1alpha1.AcceleratorOperationSpec{Type: string(operation.Revalidate), NodeName: "nope"},
		}
		for i := 0; i < 5; i++ {
			out, err := p.Apply(ctx, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Event).To(Equal(operation.EventApplyDone), "call #%d", i+1)
		}
		for i := 0; i < 5; i++ {
			out, err := p.Rollback(ctx, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Event).To(Equal(operation.EventCompensated), "call #%d", i+1)
		}
	})

	// 증명: 관측이 실패하면 빈 근거를 지어내지 않고 에러를 그대로 올린다 — 관측 실패를
	// "관측할 게 없다"(ok=false, err=nil)로 뭉개면 진짜 장애가 검증 생략으로 둔갑한다.
	// 깨는 뮤테이션: observeForNode 의 에러 전파를 지우고 빈 맵을 돌려주면 Expect(err)에서 깨진다.
	It("propagates an observation failure instead of returning an empty request", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{"kcloud.ai/aop-reval-oerr": "true"}
		seedNvidiaNode("aop-reval-oerr-node", labels, true, a30Device("1g.6gb", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("aop-reval-oerr-node") })

		acpp := mkNvidiaACPP("aop-reval-oerr-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
			NodeName: "aop-reval-oerr-node", GPUPCIs: []string{"0000:41:00.0"},
			OwnerUID: string(acpp.UID), Profile: "1g.6gb", Count: 4,
			Generation: acpp.Generation, MigPhase: npuv1alpha1.MigPhaseReady,
		}}
		Expect(k8sClient.Status().Update(ctx, acpp)).To(Succeed())

		op := &npuv1alpha1.AcceleratorOperation{
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.Revalidate), NodeName: "aop-reval-oerr-node",
				Owner: npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: acpp.Name},
			},
		}
		r := nvidiaReconciler()
		r.NvidiaObserverFactory = func(client.Client) nvidia.Observer {
			return fakeNvidiaObserver{oerr: errors.New("boom")}
		}
		_, ok, err := NewRevalidateParticipant(r).VerifyRequest(ctx, op)
		Expect(err).To(HaveOccurred())
		Expect(ok).To(BeFalse())
	})
})

// TestPublishMigObservationTo_UpdatesMigFieldThroughPatch: Update → Patch 전환 뒤에도
// 정상 경로(관측값이 실제로 바뀌는 경우)가 여전히 기록되는지 fake client 로
// 확인한다. consumers 보존 자체는 아래 바이트 단언이 증명한다 — 이 fake client(controller-
// runtime v0.22)는 내부적으로 unstructured 왕복을 거쳐 빈 슬라이스(omitempty)를 seed 시점에
// 이미 nil 로 바꿔 버리므로(사전 확인), "저장된 값이 [] 로 남는가" 는 이 하네스로는 못 본다.
func TestPublishMigObservationTo_UpdatesMigFieldThroughPatch(t *testing.T) {
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{{PCIeAddress: "0000:18:00.0", MigModeCurrent: "Unknown"}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).
		WithObjects(ndr).
		WithStatusSubresource(&npuv1alpha1.NodeDeviceReport{}).Build()

	obs := []nvidia.Observation{{PCI: "0000:18:00.0", ModeCurrent: migModeDisabled, Geometry: "disabled"}}
	if err := publishMigObservationTo(context.Background(), c, "n1", obs); err != nil {
		t.Fatalf("publishMigObservationTo: %v", err)
	}

	var got npuv1alpha1.NodeDeviceReport
	if err := c.Get(context.Background(), types.NamespacedName{Name: "n1"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Devices[0].MigModeCurrent != migModeDisabled || got.Status.Devices[0].MigCurrentGeometry != "disabled" {
		t.Fatalf("MIG 필드가 patch 로 갱신되지 않았다: %+v", got.Status.Devices[0])
	}
}

// TestPublishMigObservationToPatch_LeavesUnchangedConsumersOutOfPatchBytes: detector 가
// 쓴 consumers: []("쥔 컨테이너 없음")는 MIG 필드만 바뀌는 patch
// 에 실리면 안 된다. publishMigObservationTo 와 같은 순서(base 를 변경 전에 뜨고, 바뀐
// 필드만 적용)로 patch 바이트를 직접 계산해 "consumers" 키 부재·"migModeCurrent" 존재를
// 고정한다(envtest·fake client 모두 불필요 — client.MergeFrom 의 순수 바이트 계산만 쓴다).
func TestPublishMigObservationToPatch_LeavesUnchangedConsumersOutOfPatchBytes(t *testing.T) {
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: npuv1alpha1.NodeDeviceReportStatus{
			Consumers: []npuv1alpha1.DeviceConsumer{},
			Devices:   []npuv1alpha1.DeviceEntry{{PCIeAddress: "0000:18:00.0", MigModeCurrent: "Unknown"}},
		},
	}
	base := ndr.DeepCopy()
	modified := ndr.DeepCopy()
	modified.Status.Devices[0].MigModeCurrent = migModeDisabled
	modified.Status.Devices[0].MigCurrentGeometry = "disabled"

	b, err := client.MergeFrom(base).Data(modified)
	if err != nil {
		t.Fatalf("패치 바이트 계산: %v", err)
	}
	if strings.Contains(string(b), "consumers") {
		t.Fatalf("패치가 안 바뀐 consumers 를 실어 보냈다: %s", b)
	}
	if !strings.Contains(string(b), "migModeCurrent") {
		t.Fatalf("패치에 바뀐 MIG 필드가 없다: %s", b)
	}
}

// TestPublishMigObservationTo_UsesPatchNotUpdate: 위 두 시험은 호출부가 Update() 로 되돌려져도
// 계속 통과한다 — fake client 가 seed 시점에 이미 빈 슬라이스를 nil 로 바꿔 버려(사전 확인) MIG
// 필드 갱신은 Update 로도 똑같이 성공하고, 패치 바이트 시험은 publishMigObservationTo 를 아예
// 부르지 않는다. 그래서 호출부 문자열 자체를 고정한다 — policywrite_test.go 의
// policyWriteSource 와 같은 종류의 회귀 가드다.
func TestPublishMigObservationTo_UsesPatchNotUpdate(t *testing.T) {
	b, err := os.ReadFile("operation_participants.go")
	if err != nil {
		t.Fatalf("operation_participants.go 읽기 실패: %v", err)
	}
	if src := string(b); !strings.Contains(src, "c.Status().Patch(ctx, &ndr, client.MergeFrom(base))") {
		t.Fatal("publishMigObservationTo 가 더 이상 MergeFrom patch 를 쓰지 않는다(images 리뷰 m-4 회귀)")
	}
}
