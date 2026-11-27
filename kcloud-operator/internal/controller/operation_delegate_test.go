// ============================================================
// operation_delegate_test.go: delegate 모드 envtest (이음매 포함)
// 상세: gate 가 꺼진 기본 상태의 회귀 0, 켰을 때의 단일 mutation, 이미 수렴한 정책의 무동작,
// 그리고 정책부터 근거까지 전 구간을 한 번에 통과시키는 이음매 시험.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/partition/nvidia"
	"kcloud-operator/internal/verification"
)

// withDelegateMode 는 gate 를 켠 채 fn 을 돌리고 반드시 되돌린다.
func withDelegateMode(fn func()) {
	prev, had := os.LookupEnv("KCLOUD_OPERATION_COORDINATOR")
	Expect(os.Setenv("KCLOUD_OPERATION_COORDINATOR", "delegate")).To(Succeed())
	defer func() {
		if had {
			_ = os.Setenv("KCLOUD_OPERATION_COORDINATOR", prev)
			return
		}
		_ = os.Unsetenv("KCLOUD_OPERATION_COORDINATOR")
	}()
	fn()
}

func operationsFor(owner string) []npuv1alpha1.AcceleratorOperation {
	var list npuv1alpha1.AcceleratorOperationList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())
	var out []npuv1alpha1.AcceleratorOperation
	for _, o := range list.Items {
		if o.Spec.Owner.Name == owner {
			out = append(out, o)
		}
	}
	return out
}

var _ = Describe("delegate mode", func() {
	// 증명: 기본값은 꺼짐이다. 환경변수가 없으면 정책이 직접 적용하고 operation 을 만들지 않는다.
	// 깨는 뮤테이션: delegateModeEnabled 를 기본 true 로 바꾸면 steps==0 이 되어 실패한다.
	It("is off by default and keeps the direct apply path", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		Expect(os.Getenv("KCLOUD_OPERATION_COORDINATOR")).To(BeEmpty())
		labels := map[string]string{"kcloud.ai/aop-off": "true"}
		seedNvidiaNode("aop-off-node", labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("aop-off-node") })
		acpp := mkNvidiaACPP("aop-off-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		steps := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return countingExec{steps: &steps} }
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("aop-off-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "aop-off-acpp"}, &got)
			return got.Status.Phase
		}, "20s", "300ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))
		Expect(steps).To(BeNumerically(">", 0), "직접 적용 경로가 안 돌았다")
		Expect(operationsFor("aop-off-acpp")).To(BeEmpty(), "gate 가 꺼졌는데 트랜잭션이 생겼다")
	})

	// 증명: gate 를 켜면 정책은 직접 mutation 하지 않고 operation 하나만 만든다.
	//       이것이 "두 번 mutation 하지 않는다" 의 직접 증거다.
	// 깨는 뮤테이션: delegate 분기에서 return 을 빼고 runTarget 까지 흘려보내면 steps>0 이 되어 실패한다.
	It("creates exactly one operation and mutates nothing itself", func() {
		withDelegateMode(func() {
			wipeACPPs()
			DeferCleanup(wipeACPPs)
			labels := map[string]string{"kcloud.ai/aop-on": "true"}
			seedNvidiaNode("aop-on-node", labels, true, a30Device("", "Enabled", "Enabled", ""))
			DeferCleanup(func() { cleanupNvidia("aop-on-node") })
			acpp := mkNvidiaACPP("aop-on-acpp", labels)
			Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

			steps := 0
			r := nvidiaReconciler()
			r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return countingExec{steps: &steps} }
			for i := 0; i < 5; i++ {
				_, _ = r.Reconcile(ctx, reconcileReq("aop-on-acpp"))
			}
			ops := operationsFor("aop-on-acpp")
			Expect(ops).To(HaveLen(1), "reconcile 마다 트랜잭션이 늘어난다")
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &ops[0], client.PropagationPolicy(metav1.DeletePropagationBackground))
			})
			Expect(steps).To(Equal(0), "위임 모드인데 정책이 직접 하드웨어를 건드렸다")
			Expect(ops[0].Spec.Type).To(Equal(string(operation.PartitionReconfigure)))
			Expect(ops[0].Spec.NodeName).To(Equal("aop-on-node"))
			Expect(ops[0].Spec.TransactionID).NotTo(BeEmpty())
		})
	})

	// 증명: 이미 수렴한 정책은 gate 를 켜도 트랜잭션을 만들지 않는다(빠뜨리면 gate 를 켜는 순간
	// 클러스터의 모든 Ready 정책이 재적용된다 — 가장 큰 폭발 반경). 노드를 처음부터 목표
	// geometry 로 시드해 "이미 수렴" 을 흉내내면 안 된다 — ApplyRecords 없이 시작하면
	// routeNvidiaNoDiff 가 ExistingMigConfiguration(Failed, terminal)로 거절해 Ready 에 못
	// 도달한다. "aop-off" 처럼 geometry 를 비운 채 시작해 실제 apply 경로로 자연 수렴시켜야 한다.
	// 깨는 뮤테이션: delegateToOperation 의 shouldReverify 가드를 지우면 트랜잭션이 생겨 실패한다.
	It("does not create an operation for an already converged policy", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		labels := map[string]string{"kcloud.ai/aop-converged": "true"}
		seedNvidiaNode("aop-converged-node", labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia("aop-converged-node") })
		acpp := mkNvidiaACPP("aop-converged-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())

		r := nvidiaReconciler()
		Eventually(func() string {
			_, _ = r.Reconcile(ctx, reconcileReq("aop-converged-acpp"))
			var got npuv1alpha1.AcceleratorPartitionPolicy
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "aop-converged-acpp"}, &got)
			return got.Status.Phase
		}, "20s", "300ms").Should(Equal(npuv1alpha1.ACPPPhaseReady))

		withDelegateMode(func() {
			for i := 0; i < 5; i++ {
				_, _ = r.Reconcile(ctx, reconcileReq("aop-converged-acpp"))
			}
			Expect(operationsFor("aop-converged-acpp")).To(BeEmpty(),
				"수렴한 정책에 트랜잭션을 만들면 gate 를 켜는 순간 전 클러스터가 재적용된다")
		})
	})

	// 증명: gate 를 껐다 켜도 트랜잭션이 중복 생성되지 않는다(같은 의도는 같은 이름).
	// 깨는 뮤테이션: OperationNameFor 에 시각이나 난수를 넣으면 실패한다.
	It("reuses the same operation for the same intent", func() {
		withDelegateMode(func() {
			wipeACPPs()
			DeferCleanup(wipeACPPs)
			labels := map[string]string{"kcloud.ai/aop-reuse": "true"}
			seedNvidiaNode("aop-reuse-node", labels, true, a30Device("", "Enabled", "Enabled", ""))
			DeferCleanup(func() { cleanupNvidia("aop-reuse-node") })
			acpp := mkNvidiaACPP("aop-reuse-acpp", labels)
			Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
			r := nvidiaReconciler()
			for i := 0; i < 3; i++ {
				_, _ = r.Reconcile(ctx, reconcileReq("aop-reuse-acpp"))
			}
			first := operationsFor("aop-reuse-acpp")
			Expect(first).To(HaveLen(1))
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &first[0], client.PropagationPolicy(metav1.DeletePropagationBackground))
			})
			for i := 0; i < 3; i++ {
				_, _ = r.Reconcile(ctx, reconcileReq("aop-reuse-acpp"))
			}
			again := operationsFor("aop-reuse-acpp")
			Expect(again).To(HaveLen(1))
			Expect(again[0].Name).To(Equal(first[0].Name))
		})
	})

	// ==== 이음매 ====
	// 증명: 정책 → 트랜잭션 → 작업 본체 → 기존 적용 경로 → 검증기 → 근거 → 정책 status 가 한
	// 줄로 이어진다. 깨는 뮤테이션: participant 등록·gateReady 호출을 지우면(근거 미생성) 실패한다.
	// 노드/NDR 을 목표 geometry+광고로 미리 시드하고 ApplyRecords 는 비운 채 reconcile 하면 안
	// 된다 — routeNvidiaNoDiff 가 "이 ACPP 가 만든 게 아니다" 로 거절한다. 이미 이 레이아웃을 적용해
	// 소유하고 있다는 저널을 먼저 심어 managed no-diff 경로로 들어가게 한다.
	It("carries a policy through the coordinator to a recorded evidence", func() {
		withDelegateMode(func() {
			wipeACPPs()
			DeferCleanup(wipeACPPs)
			labels := map[string]string{"kcloud.ai/aop-seam": "true"}
			geom := nvidia.GeometrySummary("1g.6gb", 4)
			seedNvidiaNode("aop-seam-node", labels, true, a30Device(geom, "Enabled", "Enabled", ""))
			DeferCleanup(func() { cleanupNvidia("aop-seam-node") })
			advertiseMig("aop-seam-node", 4)
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorEvidence{
					ObjectMeta: metav1.ObjectMeta{Name: "aop-seam-node"},
				}, client.PropagationPolicy(metav1.DeletePropagationBackground))
			})

			acpp := mkNvidiaACPP("aop-seam-acpp", labels)
			Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
			// 이 ACPP 가 이 레이아웃을 이미 적용해 소유하고 있다는 저널을 심는다(위 코멘트 참조) —
			// 그래야 routeNvidiaNoDiff 가 managed no-diff 재검증 경로로 들어간다.
			acpp.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
				NodeName: "aop-seam-node", GPUPCIs: []string{"0000:41:00.0"}, OwnerUID: string(acpp.UID),
				BaselineGPUCount: 1, ExpectedMigCount: 4, ExpectedFullGPUCount: 1,
				Profile: "1g.6gb", Count: 4, MigPhase: npuv1alpha1.MigPhaseReady,
			}}
			Expect(k8sClient.Status().Update(ctx, acpp)).To(Succeed())

			// acppR 은 evidenceReconciler() 가 아니라 nvidiaReconciler() 다 — participant 의 runOnce 는
			// gateReady 를 통해 acppR.Verification 이 있으면 자체적으로도 근거를 쓴다. 이 스펙만으로는
			// 근거가 Coordinator 의 stepVerify 에서 왔는지 participant 내부의 낡은 ACPP-직접 게이트에서
			// 왔는지 구분할 수 없다(둘 중 하나만 없애도 통과하고, 둘 다 없애야 실패한다). acppR 에는
			// Verification 을 심지 않아 근거의 유일한 출처를 opR(Coordinator)로 고정한다.
			acppR := nvidiaReconciler()
			opR := opReconciler(NewACPPParticipant(acppR))
			opR.Verification = &verification.Verifier{Client: k8sClient}

			var opName string
			Eventually(func() string {
				_, _ = acppR.Reconcile(ctx, reconcileReq("aop-seam-acpp"))
				ops := operationsFor("aop-seam-acpp")
				if len(ops) == 0 {
					return ""
				}
				opName = ops[0].Name
				_, _ = opR.Reconcile(ctx, reconcileReq(opName))
				var got npuv1alpha1.AcceleratorOperation
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: opName}, &got)
				return got.Status.Phase
			}, "40s", "300ms").Should(Equal(npuv1alpha1.OpPhaseSucceeded))
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &npuv1alpha1.AcceleratorOperation{
					ObjectMeta: metav1.ObjectMeta{Name: opName},
				}, client.PropagationPolicy(metav1.DeletePropagationBackground))
			})

			By("트랜잭션 저널이 선행 기록 순서를 지켰다")
			var op npuv1alpha1.AcceleratorOperation
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: opName}, &op)).To(Succeed())
			Expect(operation.HasStep(op.Status.Journal, operation.StepApplyStarted)).To(BeTrue())
			Expect(operation.HasStep(op.Status.Journal, operation.StepCommitted)).To(BeTrue())

			By("정책 status 가 실제 결과를 말한다")
			var got npuv1alpha1.AcceleratorPartitionPolicy
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-seam-acpp"}, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(npuv1alpha1.ACPPPhaseReady))

			By("근거가 기록됐다")
			ev := evidenceFor("aop-seam-node")
			Expect(ev).NotTo(BeNil())
			Expect(ev.Status.Level).NotTo(BeEmpty())
			Expect(ev.Status.SourcePolicy).To(Equal("aop-seam-acpp"))
		})
	})

	// ==== 이음매: 충돌하는 두 정책 ====
	// 증명: 같은 노드를 노리는 두 정책이 delegate 모드에서 동시에 적용되지 않는다. ResourceKeysForACPP
	// 를 nil 로 고정해도 이 스펙은 통과한다 — 두 operation 이 같은 노드를 노리므로 Admit(자원 키
	// 충돌 판정)이 통과시켜도 stepAcquire 의 노드 Lease 가 독립적으로 두 번째 획득을 막는다. 이
	// 픽스처에서는 Lease 가 이미 충분한 방어선이고, ResourceKeys/Admit 고유 로직은
	// internal/operation/conflict_test.go 가 별도로 고정한다.
	It("serializes two policies that claim the same device", func() {
		withDelegateMode(func() {
			wipeACPPs()
			DeferCleanup(wipeACPPs)
			labels := map[string]string{"kcloud.ai/aop-two": "true"}
			seedNvidiaNode("aop-two-node", labels, true, a30Device("", "Enabled", "Enabled", ""))
			DeferCleanup(func() { cleanupNvidia("aop-two-node") })

			first := mkNvidiaACPP("aop-two-a", labels)
			second := mkNvidiaACPP("aop-two-b", labels)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			Expect(k8sClient.Create(ctx, second)).To(Succeed())

			// 두 정책 모두 같은 장치를 저널에 선언한 상태를 만든다(자원 키가 겹치게).
			for _, a := range []*npuv1alpha1.AcceleratorPartitionPolicy{first, second} {
				var fresh npuv1alpha1.AcceleratorPartitionPolicy
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: a.Name}, &fresh)).To(Succeed())
				fresh.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
					NodeName: "aop-two-node", GPUPCIs: []string{"0000:41:00.0"},
					OwnerUID: string(fresh.UID), Profile: "1g.6gb", Count: 4,
				}}
				Expect(k8sClient.Status().Update(ctx, &fresh)).To(Succeed())
			}

			acppR := nvidiaReconciler()
			for i := 0; i < 3; i++ {
				_, _ = acppR.Reconcile(ctx, reconcileReq("aop-two-a"))
				_, _ = acppR.Reconcile(ctx, reconcileReq("aop-two-b"))
			}
			opsA, opsB := operationsFor("aop-two-a"), operationsFor("aop-two-b")
			Expect(opsA).To(HaveLen(1))
			Expect(opsB).To(HaveLen(1))
			DeferCleanup(func() {
				for _, o := range []npuv1alpha1.AcceleratorOperation{opsA[0], opsB[0]} {
					_ = k8sClient.Delete(ctx, &o, client.PropagationPolicy(metav1.DeletePropagationBackground))
				}
			})

			// 끝나지 않는 본체로 첫 작업을 적용에 묶어 둔다.
			opR := opReconciler(scriptedParticipant{apply: operation.Outcome{RequeueAfter: opRequeue}})
			for i := 0; i < 6; i++ {
				_, _ = opR.Reconcile(ctx, reconcileReq(opsA[0].Name))
				_, _ = opR.Reconcile(ctx, reconcileReq(opsB[0].Name))
			}
			var a, b npuv1alpha1.AcceleratorOperation
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: opsA[0].Name}, &a)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: opsB[0].Name}, &b)).To(Succeed())
			applying := 0
			for _, p := range []string{a.Status.Phase, b.Status.Phase} {
				if p == npuv1alpha1.OpPhaseApplying {
					applying++
				}
			}
			Expect(applying).To(Equal(1), "같은 장치를 두 정책이 동시에 적용 중이다")
		})
	})
})

// 작업 객체 이름에 UID 가 없으면 삭제 후 같은 이름으로 재생성된 정책이 고아로 남은 옛
// operation 과 이름이 충돌해 새 정책이 영원히 적용되지 않는다.
var _ = Describe("delegate mode operation identity", func() {
	// 증명: ACPP 를 지웠다가 같은 이름으로 다시 만들어도(generation 이 1로 되돌아간다) 새
	// 정책은 옛(고아, 이미 종점인) operation 과 이름이 겹치지 않고 자신만의 새 operation 을 만든다.
	// 깨는 뮤테이션: OperationNameFor 에서 UID 접미사를 빼면 실패한다 — Create 가 옛 이름과
	// 충돌해 AlreadyExists 를 돌려주고 그게 조용히 삼켜져 새 정책 소유의 operation 이 아예
	// 안 생긴다.
	It("does not collide with an orphaned operation after the policy is deleted and recreated under the same name", func() {
		withDelegateMode(func() {
			wipeACPPs()
			DeferCleanup(wipeACPPs)
			labels := map[string]string{"kcloud.ai/aop-recreate": "true"}
			seedNvidiaNode("aop-recreate-node", labels, true, a30Device("", "Enabled", "Enabled", ""))
			DeferCleanup(func() { cleanupNvidia("aop-recreate-node") })

			acppA := mkNvidiaACPP("aop-recreate-acpp", labels)
			Expect(k8sClient.Create(ctx, acppA)).To(Succeed())
			var gotA npuv1alpha1.AcceleratorPartitionPolicy
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-recreate-acpp"}, &gotA)).To(Succeed())
			uidA := string(gotA.UID)

			r := nvidiaReconciler()
			_, _ = r.Reconcile(ctx, reconcileReq("aop-recreate-acpp"))
			opsA := operationsFor("aop-recreate-acpp")
			Expect(opsA).To(HaveLen(1))
			opA := opsA[0]
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &opA, client.PropagationPolicy(metav1.DeletePropagationBackground))
			})
			// 옛 트랜잭션을 종점으로 만든다 — envtest 에는 GC 컨트롤러가 없어 ownerRef 로도 안
			// 지워지므로, 고아로 영구히 남는 실제 조건을 그대로 시뮬레이션한다.
			opA.Status.Phase = npuv1alpha1.OpPhaseSucceeded
			Expect(k8sClient.Status().Update(ctx, &opA)).To(Succeed())

			// A 를 지운다 — ApplyRecords 가 비어 있어 handleNvidiaDeletion 은 즉시 finalizer 를 뗀다.
			Expect(k8sClient.Delete(ctx, &gotA)).To(Succeed())
			_, _ = r.Reconcile(ctx, reconcileReq("aop-recreate-acpp"))
			Eventually(func() bool {
				var got npuv1alpha1.AcceleratorPartitionPolicy
				return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-recreate-acpp"}, &got))
			}, "5s", "200ms").Should(BeTrue(), "A 가 지워졌어야 한다")

			// 같은 이름으로 B 를 만든다 — generation 1, 다른 UID.
			acppB := mkNvidiaACPP("aop-recreate-acpp", labels)
			Expect(k8sClient.Create(ctx, acppB)).To(Succeed())
			var gotB npuv1alpha1.AcceleratorPartitionPolicy
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-recreate-acpp"}, &gotB)).To(Succeed())
			uidB := string(gotB.UID)
			Expect(uidB).NotTo(Equal(uidA))

			_, _ = r.Reconcile(ctx, reconcileReq("aop-recreate-acpp"))
			ops := operationsFor("aop-recreate-acpp")
			DeferCleanup(func() {
				for i := range ops {
					_ = k8sClient.Delete(ctx, &ops[i], client.PropagationPolicy(metav1.DeletePropagationBackground))
				}
			})
			var forB *npuv1alpha1.AcceleratorOperation
			for i := range ops {
				if ops[i].Spec.Owner.UID == uidB {
					forB = &ops[i]
				}
			}
			Expect(forB).NotTo(BeNil(), "재생성된 정책 소유의 operation 이 생기지 않았다 — 옛 operation 과 이름이 충돌했다")
			Expect(forB.Name).NotTo(Equal(opA.Name), "새 정책의 operation 이 옛 operation 과 이름이 같다")
		})
	})
})

// 위임 모드에서도 ACPP 삭제는 Reconcile 의 위임 검사보다 먼저 처리돼 조정자를 거치지 않는다.
// 조정자가 같은 노드를 Applying 중(Lease 보유)일 때 삭제가 그대로 진행되면 두 주체가 동시에
// 하드웨어를 만진다 — 최소한의 방어로 삭제 경로도 같은 노드 Lease 를 잡게 한다.
var _ = Describe("delegate mode deletion lease guard", func() {
	// 증명: 조정자가 노드의 Lease 를 쥐고 있으면 삭제 경로는 하드웨어를 되돌리지 않고 대기한다.
	// 깨는 뮤테이션: handleNvidiaDeletion 의 Lease 획득 검사를 지우면 실패한다 — Lease 가 남의
	// 것인데도 rollback 이 진행돼 finalizer 가 즉시 제거된다.
	It("waits for the node lease instead of rolling back hardware while the coordinator holds it", func() {
		withDelegateMode(func() {
			wipeACPPs()
			DeferCleanup(wipeACPPs)
			sel := map[string]string{"kcloud.ai/aop-del-lease": "true"}
			seedNvidiaNode("aop-del-lease-node", sel, false, a30Device("", "Enabled", "Enabled", ""))
			setNodeGPUAllocatable("aop-del-lease-node", 0)
			DeferCleanup(func() { cleanupNvidia("aop-del-lease-node") })

			uid := mkFinalizedNvidiaACPP("aop-del-lease-acpp", sel)
			setNodeOwner("aop-del-lease-node", uid)
			seedApplyRecord("aop-del-lease-acpp", snapshotRec("aop-del-lease-node", uid, npuv1alpha1.MigPhaseReady))

			leases := &operation.LeaseManager{
				Client: k8sClient, Namespace: naming.OperatorNamespace(),
				Holder: "coordinator-op", Duration: 60 * time.Second,
			}
			ok, _, err := leases.Acquire(ctx, "aop-del-lease-node", "coordinator-op")
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			DeferCleanup(func() { _ = leases.Release(ctx, "aop-del-lease-node", "coordinator-op") })

			var acpp npuv1alpha1.AcceleratorPartitionPolicy
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-del-lease-acpp"}, &acpp)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &acpp)).To(Succeed())

			r := nvidiaReconciler()
			r.Leases = leases
			_, err = r.Reconcile(ctx, reconcileReq("aop-del-lease-acpp"))
			Expect(err).To(HaveOccurred(), "조정자가 Lease 를 쥐고 있는데 삭제가 그냥 진행됐다")
			Consistently(func() bool {
				var got npuv1alpha1.AcceleratorPartitionPolicy
				return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-del-lease-acpp"}, &got))
			}, "1s", "200ms").Should(BeFalse(), "Lease 획득 실패인데 finalizer 가 제거됐다")

			// 조정자가 끝났다 — Lease 를 놓으면 삭제가 이어서 진행돼야 한다.
			Expect(leases.Release(ctx, "aop-del-lease-node", "coordinator-op")).To(Succeed())
			_, err = r.Reconcile(ctx, reconcileReq("aop-del-lease-acpp"))
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				var got npuv1alpha1.AcceleratorPartitionPolicy
				return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "aop-del-lease-acpp"}, &got))
			}, "5s", "200ms").Should(BeTrue(), "Lease 가 풀렸는데 삭제가 끝나지 않았다")
		})
	})
})
