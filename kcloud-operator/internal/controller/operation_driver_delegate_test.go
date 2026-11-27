// ============================================================
// operation_driver_delegate_test.go: 드라이버 위임 모드 envtest
// 상세: 스위치가 꺼진 기본 상태의 회귀 0, 켰을 때의 작업 생성, 그리고 같은 노드의 파티션
// 작업과 동시에 적용되지 않는지를 본다. 직렬화를 이루는 두 기제(자원 키 충돌 판정, 노드
// Lease)를 각각 격리해서도 확인한다.
// 생성일: 2026-08-01 | 수정일: 2026-10-22
// ============================================================
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driveradapter"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/upgrade"
)

// nvidiaDriverPolicy 는 Reconcile 의 findMatchingPolicy 가 찾아야 하는 최소 DriverInstallPolicy
// 다 — 이게 없으면 Reconcile 이 위임 분기에 도달하기 전에 조기 반환한다(driver_upgrade_controller.go
// 의 "매칭 DriverInstallPolicy 없음, 스킵").
func nvidiaDriverPolicy(name string) *npuv1alpha1.DriverInstallPolicy {
	return &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "nvidia",
			Driver: npuv1alpha1.DriverSpec{Version: "570.12.01", Mode: "daemonset", Image: "example.invalid/nvidia-installer:570.12.01"},
		},
	}
}

// bareOpNode 는 stepSnapshot 이 요구하는, 딱 이름만 있는 노드다(operation_controller.go 의
// stepSnapshot 은 노드가 없으면 "되돌릴 목표가 없다" 며 즉시 RollingBack 으로 보낸다 — 이
// 시험들이 실제로 Applying/Planning 까지 가려면 노드가 존재해야 한다).
func bareOpNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// driverReconcilerForTest 는 envtest 공유 클라이언트로 구성한 드라이버 reconciler 다.
// 기존 스위트(driver_upgrade_controller_test.go)의 newReconciler 는 fake 클라이언트를 새로
// 만들어 이 파일의 전역 k8sClient/ctx 와 맞지 않는다 — 같은 모양을 envtest 클라이언트로 옮긴다.
func driverReconcilerForTest() *DriverUpgradeReconciler {
	return &DriverUpgradeReconciler{
		Client: k8sClient, Scheme: k8sClient.Scheme(),
		StateMachine: &upgrade.UpgradeStateMachine{Client: k8sClient},
	}
}

// seedUpgradeState 는 상태머신이 "업그레이드 필요" 로 판정한 뒤의 DUS 를 만든다.
// 이 시드가 없으면 위임 분기에 **도달조차 못 한다** — 그러면 이 스펙은 아무것도 검증하지 못한다.
func seedUpgradeState(name, node, state, cur, desired string) *npuv1alpha1.DriverUpgradeState {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: node, Vendor: "nvidia"},
	}
	Expect(k8sClient.Create(ctx, dus)).To(Succeed())
	dus.Status.State = state
	dus.Status.CurrentVersion = cur
	dus.Status.DesiredVersion = desired
	Expect(k8sClient.Status().Update(ctx, dus)).To(Succeed())
	return dus
}

// driverOperationsFor 는 그 DUS 를 소유자로 하는 작업 목록이다.
func driverOperationsFor(owner string) []npuv1alpha1.AcceleratorOperation {
	var list npuv1alpha1.AcceleratorOperationList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())
	var out []npuv1alpha1.AcceleratorOperation
	for _, o := range list.Items {
		if o.Spec.Owner.Kind == "DriverUpgradeState" && o.Spec.Owner.Name == owner {
			out = append(out, o)
		}
	}
	return out
}

// opReconcilerWithDriver 는 opReconciler 가 등록하지 않는 DriverUpgrade 종류를 같은
// participant 로 추가 등록한다. opReconciler 하나만 쓰면 드라이버 작업이 "no participant
// registered" 로 즉시 ManualRecoveryRequired 에 떨어져(operation_controller.go stepApply)
// 직렬화 시험이 적용 단계에 도달하기도 전에 무의미해진다.
func opReconcilerWithDriver(p operation.Participant) *AcceleratorOperationReconciler {
	r := opReconciler(p)
	r.Participants[operation.DriverUpgrade] = p
	return r
}

// cleanupNodeLease 는 직렬화 시험이 남긴 노드 Lease 를 지운다(타 spec 으로 누수 방지).
func cleanupNodeLease(node string) {
	var lease coordv1.Lease
	key := types.NamespacedName{Name: operation.NodeLeaseName(node), Namespace: naming.OperatorNamespace()}
	if err := k8sClient.Get(ctx, key, &lease); err == nil {
		_ = k8sClient.Delete(ctx, &lease, client.PropagationPolicy(metav1.DeletePropagationBackground))
	}
}

var _ = Describe("driver delegate mode", func() {
	// 증명: 스위치가 꺼져 있으면 드라이버 작업이 만들어지지 않는다(기본 동작 보존). 이 위임
	// 분기는 Reconcile 안에만 있으므로, 상태만 시드하고 Reconcile 을 부르지 않으면 스위치
	// 자체를 시험한 게 아니다 — 전체 Reconcile 을 불러 진짜 분기를 태운다.
	// 깨는 뮤테이션: delegateModeEnabled 를 기본 true 로 바꾸면 작업이 생겨 실패한다.
	It("creates no operation while the switch is off", func() {
		dip := nvidiaDriverPolicy("drv-off-dip")
		Expect(k8sClient.Create(ctx, dip)).To(Succeed())
		dus := seedUpgradeState("drv-off", "drv-off-node",
			npuv1alpha1.UpgradeStateRequired, "535.104.05", "570.12.01")
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, dus, client.PropagationPolicy(metav1.DeletePropagationBackground))
			_ = k8sClient.Delete(ctx, dip, client.PropagationPolicy(metav1.DeletePropagationBackground))
		})
		_, _ = driverReconcilerForTest().Reconcile(ctx, reconcileReq(dus.Name))
		Expect(driverOperationsFor("drv-off")).To(BeEmpty())
	})

	// 증명: 스위치를 켜면 업그레이드 필요 상태에서 작업이 정확히 하나 생긴다.
	// 깨는 뮤테이션: 작업 이름에 시각·난수를 넣으면 pass 마다 작업이 늘어 실패한다.
	It("creates exactly one operation for an upgrade that is required", func() {
		withDelegateMode(func() {
			dus := seedUpgradeState("drv-on", "drv-on-node",
				npuv1alpha1.UpgradeStateRequired, "535.104.05", "570.12.01")
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, dus, client.PropagationPolicy(metav1.DeletePropagationBackground))
			})
			r := driverReconcilerForTest()
			for i := 0; i < 4; i++ {
				handled, _, err := r.delegateDriverUpgrade(ctx, dus, nvidiaDriverPolicy("drv-on-dip"))
				Expect(err).NotTo(HaveOccurred())
				Expect(handled).To(BeTrue(), "위임 분기에 도달하지 못했다")
			}
			ops := driverOperationsFor("drv-on")
			Expect(ops).To(HaveLen(1))
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &ops[0], client.PropagationPolicy(metav1.DeletePropagationBackground))
			})
			Expect(ops[0].Spec.Type).To(Equal(string(operation.DriverUpgrade)))
			Expect(ops[0].Spec.ResourceKeys).To(ContainElement("node/drv-on-node/cordon"))
			Expect(ops[0].Spec.ResourceKeys).To(ContainElement("node/drv-on-node/reboot"))
		})
	})

	// 증명: 설치 Secret 이 없으면 작업을 만들지 않고 DUS 를 Idle 로 되돌린다(대기는 Idle 이 맡는다).
	//       작업을 만들면 노드 잠금 키를 쥔 채 대기해 같은 노드의 다른 벤더(NVIDIA 등) 작업이 Blocked 에
	//       묶이고, 상태머신에 넘기면 그쪽 재확인 사이에 Secret 이 생길 때 작업 없이 PreFlight 로 간다.
	//       Secret 이 생긴 뒤 다시 UpgradeRequired 가 되면 작업을 만든다.
	// 깨는 뮤테이션: delegateDriverUpgrade 의 Secret 확인을 빼면 첫 호출에서 작업이 생겨 실패한다.
	It("returns to idle without an operation while the install secret is missing", func() {
		withDelegateMode(func() {
			dus := &npuv1alpha1.DriverUpgradeState{
				ObjectMeta: metav1.ObjectMeta{Name: "drv-nosecret"},
				Spec: npuv1alpha1.DriverUpgradeStateSpec{
					NodeName: "drv-nosecret-node", Vendor: "furiosa", Model: "warboy"},
			}
			Expect(k8sClient.Create(ctx, dus)).To(Succeed())
			dus.Status.State = npuv1alpha1.UpgradeStateRequired
			dus.Status.DesiredVersion = "1.9.9-3"
			Expect(k8sClient.Status().Update(ctx, dus)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, dus, client.PropagationPolicy(metav1.DeletePropagationBackground))
				for _, o := range driverOperationsFor("drv-nosecret") {
					_ = k8sClient.Delete(ctx, &o, client.PropagationPolicy(metav1.DeletePropagationBackground))
				}
			})
			pol := &npuv1alpha1.DriverInstallPolicy{Spec: npuv1alpha1.DriverInstallPolicySpec{
				Vendor: "furiosa", Model: "warboy"}}
			r := driverReconcilerForTest()
			r.StateMachine.APIReader = k8sClient

			handled, res, err := r.delegateDriverUpgrade(ctx, dus, pol)
			Expect(err).NotTo(HaveOccurred())
			Expect(handled).To(BeTrue(), "Secret 이 없는데 상태머신으로 넘겼다")
			Expect(res.RequeueAfter).To(Equal(upgrade.InstallSecretRecheck))
			Expect(driverOperationsFor("drv-nosecret")).To(BeEmpty())
			var got npuv1alpha1.DriverUpgradeState
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dus.Name}, &got)).To(Succeed())
			Expect(got.Status.State).To(Equal(npuv1alpha1.UpgradeStateIdle))
			Expect(got.Status.Message).To(ContainSubstring(driveradapter.AptAuthSecretName))

			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: driveradapter.AptAuthSecretName, Namespace: naming.OperatorNamespace()}}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secret)
			})
			got.Status.State = npuv1alpha1.UpgradeStateRequired
			Expect(k8sClient.Status().Update(ctx, &got)).To(Succeed())
			handled, _, err = r.delegateDriverUpgrade(ctx, &got, pol)
			Expect(err).NotTo(HaveOccurred())
			Expect(handled).To(BeTrue())
			Expect(driverOperationsFor("drv-nosecret")).To(HaveLen(1))
		})
	})

	// 증명: 유휴 상태에서는 위임하지 않는다 — 상태머신이 필요를 판정할 기회를 뺏지 않는다.
	//       (이 가드가 없으면 목표 버전도 모르는 채 작업이 생긴다.)
	// 깨는 뮤테이션: 상태 검사를 빼면 유휴 DUS 에도 작업이 생겨 실패한다.
	It("does not delegate while the state machine is idle", func() {
		withDelegateMode(func() {
			dus := seedUpgradeState("drv-idle", "drv-idle-node",
				npuv1alpha1.UpgradeStateIdle, "535.104.05", "535.104.05")
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, dus, client.PropagationPolicy(metav1.DeletePropagationBackground))
			})
			handled, _, err := driverReconcilerForTest().delegateDriverUpgrade(ctx, dus, nvidiaDriverPolicy("drv-idle-dip"))
			Expect(err).NotTo(HaveOccurred())
			Expect(handled).To(BeFalse(), "유휴 상태를 위임이 가로챘다")
			Expect(driverOperationsFor("drv-idle")).To(BeEmpty())
		})
	})

	// 증명: 같은 노드의 드라이버 작업과 파티션 작업이 동시에 적용 단계에 들어가지 않는다 —
	// 자원 키 충돌 판정에서 가장 위험한 쌍이다.
	// 깨는 뮤테이션: ResourceKeysForDriver 에서 노드 잠금 키를 빼면 둘 다 적용에 들어가 실패한다.
	It("serializes a driver upgrade against a partition change on the same node", func() {
		node := "drv-conflict-node"
		driverOp := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "drv-conflict-driver"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.DriverUpgrade), NodeName: node, Vendor: "nvidia",
				TransactionID: "tx-drv",
				Owner:         npuv1alpha1.OperationOwner{Kind: "DriverUpgradeState", Name: "drv-conflict-dus"},
				ResourceKeys:  ResourceKeysForDriver(node, []string{"0000:41:00.0"}),
			},
		}
		partOp := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "drv-conflict-part"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.PartitionReconfigure), NodeName: node, Vendor: "nvidia",
				TransactionID: "tx-part",
				Owner:         npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "drv-conflict-acpp"},
				ResourceKeys: []string{
					string(operation.DevicePartitionKey("0000:81:00.0")),
					string(operation.NodeCordonKey(node)),
				},
			},
		}
		n := bareOpNode(node)
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
		Expect(k8sClient.Create(ctx, driverOp)).To(Succeed())
		Expect(k8sClient.Create(ctx, partOp)).To(Succeed())
		DeferCleanup(func() {
			for _, o := range []*npuv1alpha1.AcceleratorOperation{driverOp, partOp} {
				_ = k8sClient.Delete(ctx, o, client.PropagationPolicy(metav1.DeletePropagationBackground))
			}
			_ = k8sClient.Delete(ctx, n, client.PropagationPolicy(metav1.DeletePropagationBackground))
			cleanupNodeLease(node)
		})

		// 끝나지 않는 본체로 첫 작업을 적용 단계에 묶어 둔다. opReconciler 는 DriverUpgrade 를
		// 등록하지 않으므로 opReconcilerWithDriver 로 보강한다(위 주석 참고).
		r := opReconcilerWithDriver(scriptedParticipant{apply: operation.Outcome{RequeueAfter: opRequeue}})
		for i := 0; i < 8; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("drv-conflict-driver"))
			_, _ = r.Reconcile(ctx, reconcileReq("drv-conflict-part"))
		}
		var a, b npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "drv-conflict-driver"}, &a)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "drv-conflict-part"}, &b)).To(Succeed())
		applying := 0
		for _, p := range []string{a.Status.Phase, b.Status.Phase} {
			if p == npuv1alpha1.OpPhaseApplying {
				applying++
			}
		}
		Expect(applying).To(Equal(1), "드라이버 교체와 파티션 재구성이 같은 노드에서 동시에 적용됐다")
		blocked := b.Status.Phase == npuv1alpha1.OpPhaseBlocked || a.Status.Phase == npuv1alpha1.OpPhaseBlocked
		Expect(blocked).To(BeTrue(), "둘 중 하나는 대기 상태여야 한다")
	})

	// 증명(격리 1/2 — 자원 키 충돌 판정): operation.Admit 은 Lease 를 전혀 참조하지 않는 순수
	// 함수다. Lease 객체를 하나도 만들지 않고 이 함수만 불러도 드라이버/파티션 쌍이 자원 키
	// 겹침만으로 Blocked 가 나오는지 본다 — 위 직렬화 시험이 Lease 없이도 성립함을 별도로 증명한다.
	// 깨는 뮤테이션: ResourceKeysForDriver 가 node 키를 안 만들면 AdmitNow 로 바뀌어 실패한다.
	It("blocks the driver/partition pair by resource-key overlap alone, independent of the node lease", func() {
		node := "drv-admit-only-node"
		driverClaim := operation.Claim{
			Name: "drv-admit-driver", Type: operation.DriverUpgrade,
			Keys: toResourceKeys(ResourceKeysForDriver(node, []string{"0000:41:00.0"})),
		}
		partClaim := operation.Claim{
			Name: "drv-admit-part", Type: operation.PartitionReconfigure,
			Keys: toResourceKeys([]string{
				string(operation.DevicePartitionKey("0000:81:00.0")),
				string(operation.NodeCordonKey(node)),
			}),
		}
		verdict, blockedBy := operation.Admit(partClaim, []operation.Claim{driverClaim})
		Expect(verdict).To(Equal(operation.AdmitBlocked))
		Expect(blockedBy).To(Equal(driverClaim.Name))
	})

	// 증명(격리 2/2 — 노드 Lease): 자원 키가 전혀 겹치지 않는 두 작업도 같은 노드를 노리면
	// Lease 하나가 직렬화한다. Admit 은 이 쌍을 막지 않는다는 것부터 스스로 확인해, 이 시험이
	// 자원 키 충돌 판정의 재확인이 아니라 Lease 만의 효과임을 증명한다.
	It("still serializes two same-node operations via the node lease when resource keys don't overlap", func() {
		node := "drv-lease-only-node"
		opA := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "drv-lease-a"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.PartitionReconfigure), NodeName: node, Vendor: "nvidia",
				TransactionID: "tx-lease-a",
				Owner:         npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "drv-lease-a-acpp"},
				ResourceKeys:  []string{string(operation.DevicePartitionKey("0000:aa:00.0"))},
			},
		}
		opB := &npuv1alpha1.AcceleratorOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "drv-lease-b"},
			Spec: npuv1alpha1.AcceleratorOperationSpec{
				Type: string(operation.PartitionReconfigure), NodeName: node, Vendor: "nvidia",
				TransactionID: "tx-lease-b",
				Owner:         npuv1alpha1.OperationOwner{Kind: "AcceleratorPartitionPolicy", Name: "drv-lease-b-acpp"},
				ResourceKeys:  []string{string(operation.DevicePartitionKey("0000:bb:00.0"))},
			},
		}
		n := bareOpNode(node)
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
		Expect(k8sClient.Create(ctx, opA)).To(Succeed())
		Expect(k8sClient.Create(ctx, opB)).To(Succeed())
		DeferCleanup(func() {
			for _, o := range []*npuv1alpha1.AcceleratorOperation{opA, opB} {
				_ = k8sClient.Delete(ctx, o, client.PropagationPolicy(metav1.DeletePropagationBackground))
			}
			_ = k8sClient.Delete(ctx, n, client.PropagationPolicy(metav1.DeletePropagationBackground))
			cleanupNodeLease(node)
		})

		verdict, _ := operation.Admit(operation.ClaimOf(opA), []operation.Claim{operation.ClaimOf(opB)})
		Expect(verdict).To(Equal(operation.AdmitNow), "이 시험의 전제(키가 안 겹친다)가 깨졌다 — Admit 이 이미 막고 있으면 Lease 효과를 분리해 볼 수 없다")

		r := opReconciler(scriptedParticipant{apply: operation.Outcome{RequeueAfter: opRequeue}})
		for i := 0; i < 8; i++ {
			_, _ = r.Reconcile(ctx, reconcileReq("drv-lease-a"))
			_, _ = r.Reconcile(ctx, reconcileReq("drv-lease-b"))
		}
		var a, b npuv1alpha1.AcceleratorOperation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "drv-lease-a"}, &a)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "drv-lease-b"}, &b)).To(Succeed())
		applying, planning := 0, 0
		for _, p := range []string{a.Status.Phase, b.Status.Phase} {
			switch p {
			case npuv1alpha1.OpPhaseApplying:
				applying++
			case npuv1alpha1.OpPhasePlanning:
				planning++
			}
		}
		Expect(applying).To(Equal(1), "자원 키가 안 겹치는 같은 노드 작업 둘이 동시에 적용됐다 — 노드 Lease 가 직렬화하지 않았다")
		Expect(planning).To(Equal(1), "Lease 를 못 받은 쪽은 Blocked 가 아니라 Planning 에 남아 대기해야 한다")
	})
})

// toResourceKeys 는 []string 자원 키를 operation.Claim 이 쓰는 타입으로 바꾼다.
func toResourceKeys(ss []string) []operation.ResourceKey {
	out := make([]operation.ResourceKey, 0, len(ss))
	for _, s := range ss {
		out = append(out, operation.ResourceKey(s))
	}
	return out
}
