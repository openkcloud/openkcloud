// ============================================================
// acpp_restore_mode_test.go: 삭제 시 MIG 모드 복원 envtest
// 상세: 기본값에서는 아무것도 바뀌지 않고, 복원 정책에서만 모드 해제와 재부팅이 일어난다.
// 생성일: 2026-08-01 | 수정일: 2026-10-14
// ============================================================
package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/partition/nvidia"
)

// seedDeletableRecord 는 GI 가 이미 회수된(삭제 경로 후반) 저널을 만든다.
// OwnerUID 와 노드 lock 을 함께 시드하지 않으면 삭제 경로가 소유권 분기에서 건너뛰어
// 이 스펙이 아무것도 검증하지 못한다.
func seedDeletableRecord(acpp *npuv1alpha1.AcceleratorPartitionPolicy, node string) {
	var fresh npuv1alpha1.AcceleratorPartitionPolicy
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
	fresh.Status.ApplyRecords = []npuv1alpha1.ApplyRecord{{
		NodeName: node, GPUPCIs: []string{"0000:41:00.0"}, OwnerUID: string(fresh.UID),
		Profile: "1g.6gb", Count: 4, BaselineGPUCount: 1, ExpectedFullGPUCount: 1,
		MigPhase: npuv1alpha1.MigPhaseReady, CordonedByPolicy: true,
	}}
	Expect(k8sClient.Status().Update(ctx, &fresh)).To(Succeed())
	setNodeOwner(node, string(fresh.UID))
}

var _ = Describe("deletion restores MIG mode", func() {
	// 증명: 기본 정책에서는 모드 해제 명령도 재부팅도 없다(기존 동작 보존).
	// 깨는 뮤테이션: 복원 분기를 정책과 무관하게 실행하면 명령 수가 늘어 실패한다.
	It("does nothing extra under the default deletion policy", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		node := "restore-default-node"
		labels := map[string]string{"kcloud.ai/restore-default": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-default-acpp", labels)
		Expect(acpp.Spec.DeletionPolicy).To(Equal(npuv1alpha1.DeletionPolicyRetain))
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		// 이미지를 실제로 설정한다 — 미설정이면 RequestReboot 이 "이미지 없음" 으로 먼저 실패해
		// Job 이 안 생기고, 그러면 아래 rebootJobFor 부재 확인이 정책 분기가 아니라 그 우연
		// 때문에 통과해 버린다(뮤테이션으로 확인: 이미지를 안 설정하면 이 확인이 늘 통과한다).
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		// 총 실행 step 수가 아니라 -mig 0 명령 수만 센다 — 기존 삭제 경로도 GI 회수 명령을 같은
		// executor 로 실행하므로(hardwareChanged 분기의 b.Rollback), 총합으로 재면 그 정상적인
		// 기존 명령까지 "복원 분기가 실행됐다" 로 오판한다.
		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		_ = r.handleNvidiaDeletion(ctx, &fresh)

		Expect(disables).To(Equal(0), "기본 정책인데 모드 해제 명령이 실행됐다")
		_, jerr := rebootJobFor(node)
		Expect(jerr).To(HaveOccurred(), "기본 정책인데 재부팅 Job 이 만들어졌다")
	})

	// 증명: 복원 정책에서는 모드가 Enabled 로 관측되면 해제 명령이 나가고 재부팅이 예약된다.
	//       그리고 그때까지 정리가 끝나지 않는다(finalizer 유지).
	// 깨는 뮤테이션: 미완료를 오류 없이 통과시키면 finalizer 가 빠져 모드가 켜진 채 정책이 사라진다.
	It("issues a mode disable and schedules a reboot under the restore policy", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		// 이미지를 설정해야 한다 — 미설정이면 RequestReboot 이 Job 생성 전에 "이미지 미설정"
		// 오류로 먼저 실패해, 이 스펙의 두 단언(err 있음 + disables>0)이 실제 재부팅 예약과
		// 무관하게 통과해 버린다. 이미지를 설정해야 "재부팅이 실제로 예약됐다" 를 검증하는 스펙이 된다.
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-on-node"
		labels := map[string]string{"kcloud.ai/restore-on": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-on-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		err := r.handleNvidiaDeletion(ctx, &fresh)
		Expect(err).To(HaveOccurred(), "모드가 아직 켜져 있는데 정리를 완료로 처리했다")
		Expect(disables).To(BeNumerically(">", 0), "모드 해제 명령이 나가지 않았다")
		_, jerr := rebootJobFor(node)
		Expect(jerr).NotTo(HaveOccurred(), "재부팅이 실제로 예약되지 않았다")
	})

	// 증명: 모드가 이미 Disabled 로 관측되면 아무 명령도 재부팅도 없이 정리가 끝난다(멱등).
	// 깨는 뮤테이션: 관측 결과를 무시하고 무조건 해제하면 재부팅이 반복돼 실패한다.
	It("completes without a reboot once the mode is already disabled", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		// 이미지를 설정해야 한다 — 미설정이면 이미 Disabled 판정을 건너뛰는 뮤테이션이 들어와도
		// RequestReboot 이 "이미지 없음" 으로 먼저 실패해 아래 rebootJobFor 부재 확인이 우연히
		// 통과해 버린다.
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-done-node"
		labels := map[string]string{"kcloud.ai/restore-done": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Disabled", "Disabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-done-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		_ = r.handleNvidiaDeletion(ctx, &fresh)
		Expect(disables).To(Equal(0), "이미 해제된 모드에 해제 명령을 다시 쐈다")
		_, jerr := rebootJobFor(node)
		Expect(jerr).To(HaveOccurred(), "이미 해제된 모드인데 재부팅을 예약했다")
	})

	// 증명: 관측이 불확실하면(관측 오류) 모드를 건드리지 않는다 — 상태를 모르는 채 재부팅을
	// 예약하는 것이 이 경로에서 가장 위험한 행동이다. handleNvidiaDeletion 경유로는
	// assertMigEmptyAndBaseline 이 관측 오류를 먼저 거절해 이 함수의 ModeObservable 게이트에
	// 도달하지 못하므로, restoreMigModeDisabled 를 직접 불러 이 함수 자신의 fail-closed 규율을
	// 확인한다. 깨는 뮤테이션: ModeObservable 게이트를 빼면 관측 실패에도 명령이 나간다.
	It("refuses to touch the mode when observation is unreliable", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		node := "restore-blind-node"
		labels := map[string]string{"kcloud.ai/restore-blind": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Unknown", "Unknown", "nvidia-smi failed"))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-blind-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		rec := getApplyRecord(&fresh, node)

		done, err := r.restoreMigModeDisabled(ctx, &fresh, rec)
		Expect(err).To(HaveOccurred(), "관측이 불확실한데 정리를 진행시켰다")
		Expect(done).To(BeFalse())
		Expect(disables).To(Equal(0), "관측이 불확실한데 모드를 건드렸다")
		_, jerr := rebootJobFor(node)
		Expect(jerr).To(HaveOccurred(), "관측이 불확실한데 재부팅을 예약했다")
	})

	// 증명: 이미 예약된 재부팅 Job 이 있으면(아직 완료 전 재관측) 명령도 카운터도 다시 건드리지
	// 않는다 — enable 경로(ensureAcppRebootJob)와 같은 재진입 가드. 이게 없으면 재부팅 하나가
	// 끝나기도 전에 매 pass 마다 카운터가 올라 상한(2)을 두 번의 reconcile 만으로 소진한다.
	// 깨는 뮤테이션: Job 존재 확인을 빼면 재진입마다 해제 명령과 카운터가 다시 실행돼 실패한다.
	It("does not re-issue a mode disable while a reboot is already pending", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-pending-node"
		labels := map[string]string{"kcloud.ai/restore-pending": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-pending-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		var fresh npuv1alpha1.AcceleratorPartitionPolicy

		// pass 1: 실제로 해제 명령이 나가고 재부팅이 예약된다.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		Expect(r.handleNvidiaDeletion(ctx, &fresh)).To(HaveOccurred())
		Expect(disables).To(Equal(1), "첫 pass 에서 해제 명령이 정확히 한 번 나가야 한다")

		var afterFirst npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &afterFirst)).To(Succeed())
		firstAttempts := afterFirst.Status.ApplyRecords[0].DisableRebootAttempts
		Expect(firstAttempts).To(Equal(int32(1)))

		// pass 2, 3: 노드는 아직 재부팅 전이라 재관측은 여전히 Enabled 를 보고한다 — Job 이 이미
		// 있으므로 해제 명령도 카운터도 다시 움직이면 안 된다.
		for i := 0; i < 2; i++ {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
			Expect(r.handleNvidiaDeletion(ctx, &fresh)).To(HaveOccurred())
		}
		Expect(disables).To(Equal(1), "재부팅 대기 중에 해제 명령이 다시 나갔다")

		var afterMore npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &afterMore)).To(Succeed())
		Expect(afterMore.Status.ApplyRecords[0].DisableRebootAttempts).To(Equal(firstAttempts),
			"재부팅 대기 중에 시도 횟수가 다시 올랐다 — 재진입 가드가 없다")
	})

	// 증명: 재부팅 상한을 이미 다 썼는데도 모드가 안 내려갔으면 더 재부팅하지 않고 운영자 개입을
	// 요구하는 오류로 정리를 막는다 — 복원 실패가 조용히 삼켜지지 않고 finalizer 를 쥔 채로
	// 드러난다(핸들러 반환 오류 → Reconcile 이 finalizer 를 제거하지 않는다).
	// 깨는 뮤테이션: 상한 검사를 빼면 상한을 넘겨도 계속 재부팅을 쏴 실패한다.
	It("stops retrying and surfaces a blocked error once the reboot budget is exhausted", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-exhausted-node"
		labels := map[string]string{"kcloud.ai/restore-exhausted": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-exhausted-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)
		var withAttempts npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &withAttempts)).To(Succeed())
		withAttempts.Status.ApplyRecords[0].DisableRebootAttempts = maxMigRebootAttempts
		Expect(k8sClient.Status().Update(ctx, &withAttempts)).To(Succeed())

		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		err := r.handleNvidiaDeletion(ctx, &fresh)
		Expect(err).To(HaveOccurred(), "상한을 넘겼는데 정리가 조용히 완료됐다")
		Expect(err.Error()).To(ContainSubstring("manual intervention"))
		Expect(disables).To(Equal(0), "상한을 넘겼는데 해제 명령을 또 쐈다")
		_, jerr := rebootJobFor(node)
		Expect(jerr).To(HaveOccurred(), "상한을 넘겼는데 재부팅을 또 예약했다")
		// handleDeletion(전체 Reconcile 경로)은 이 오류가 있으면 finalizer 를 절대 제거하지
		// 않는다(internal/controller/acceleratorpartitionpolicy_controller.go:handleDeletion) —
		// 여기서 재확인하는 것은 err != nil 그 자체다. 이 테스트는 handleNvidiaDeletion 을 직접
		// 부르므로(기존 스펙 전부와 같은 방식) finalizer 를 이 fixture 가 붙이지 않아 별도로
		// 관측할 수는 없지만, 그 상위 호출자가 err 하나만 보고 결정한다는 사실은 코드로 고정돼 있다.
	})

	// 증명(자기봉쇄 결함 고정): RestoreMode 로 모드 해제가 성공하고 재부팅이 끝나 재관측이 Disabled
	// 를 보고하면, 바로 다음 pass 에서 정리가 끝나야 한다. assertMigEmptyAndBaseline 은 매 pass 먼저
	// 불리므로, allowModeDisabled 가 효과가 없으면 이 pass 도 "MIG not enabled after rollback" 으로
	// 다시 막혀 복원 성공 자체가 정리를 영원히 못 끝내는 역설이 된다. 앞의 스펙들은
	// handleNvidiaDeletion 을 한 번만 불러 이 두 번째 pass 를 만들지 않으므로 이 역설을 못 잡는다.
	// 깨는 뮤테이션: allowModeDisabled 를 강제로 false 로 두면 두 번째 pass 가 다시 오류를 낸다.
	It("completes cleanup on the pass after mode disable succeeds and reboot completes", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-followthrough-node"
		labels := map[string]string{"kcloud.ai/restore-followthrough": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-followthrough-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		r := nvidiaReconciler()
		var fresh npuv1alpha1.AcceleratorPartitionPolicy

		// pass 1: mode 가 아직 Enabled — 해제 명령이 나가고 재부팅이 예약된다. 아직 안 끝났으므로
		// 오류를 내고 finalizer 를 쥔다(기존 스펙과 같은 관측).
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		Expect(r.handleNvidiaDeletion(ctx, &fresh)).To(HaveOccurred())
		_, jerr := rebootJobFor(node)
		Expect(jerr).NotTo(HaveOccurred(), "1차 pass 에서 재부팅이 예약되지 않았다")

		// 재부팅이 실제로 끝나 노드가 mode Disabled 를 보고한다고 가정한다(재관측 결과) —
		// 기존 스펙들(예: "stays Ready on a reused reconcile")과 같은 방식으로 NDR 을 직접 갱신한다.
		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ndr)).To(Succeed())
		ndr.Status.Devices[0].MigModeCurrent = migModeDisabled
		ndr.Status.Devices[0].MigModePending = migModeDisabled
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())

		// pass 2: 이번엔 정리가 실제로 끝나야 한다.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		Expect(r.handleNvidiaDeletion(ctx, &fresh)).NotTo(HaveOccurred(),
			"모드 해제가 성공했는데도 다음 pass 가 정리를 끝내지 못했다(자기봉쇄)")
	})

	// 증명(예산 분리 고정): mode enable 이 자기 재부팅 상한을 이미 다 써 Failed 로 끝난 정책도
	// RestoreMode 로 지우면 해제 명령이 나가야 한다 — enable 카운터와 disable 카운터가 같은 필드를
	// 쓰면 여기서 첫 pass 부터 상한 초과로 막혀 finalizer 가 영구히 안 빠진다.
	// 깨는 뮤테이션: restoreMigModeDisabled 가 다시 RebootAttempts 를 읽게 되돌리면 이 pass 가
	// "manual intervention" 오류로 막힌다.
	It("still disables MIG mode when the enable path already exhausted its own reboot budget", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-enable-exhausted-node"
		labels := map[string]string{"kcloud.ai/restore-enable-exhausted": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-enable-exhausted-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)
		var withEnableAttempts npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &withEnableAttempts)).To(Succeed())
		// mode enable 경로가 예전에 상한까지 재부팅을 쓰고 Failed 로 끝난 이력을 시드한다.
		withEnableAttempts.Status.ApplyRecords[0].RebootAttempts = maxMigRebootAttempts
		Expect(k8sClient.Status().Update(ctx, &withEnableAttempts)).To(Succeed())

		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		err := r.handleNvidiaDeletion(ctx, &fresh)
		Expect(err).To(HaveOccurred(), "재부팅을 막 예약했으니 정리는 아직 안 끝나야 한다")
		Expect(err.Error()).NotTo(ContainSubstring("manual intervention"),
			"enable 예산과 disable 예산이 분리되지 않아 상한 초과로 막혔다")
		Expect(disables).To(Equal(1), "enable 상한 소진 이력 때문에 해제 명령이 나가지 않았다")

		var afterFirst npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &afterFirst)).To(Succeed())
		Expect(afterFirst.Status.ApplyRecords[0].DisableRebootAttempts).To(Equal(int32(1)),
			"disable 시도가 자기 카운터에 기록되지 않았다")
		Expect(afterFirst.Status.ApplyRecords[0].RebootAttempts).To(Equal(maxMigRebootAttempts),
			"enable 카운터가 disable 시도로 건드려졌다 — 예산이 분리되지 않았다")
	})
})

// migZeroCountingExec 는 -mig 0 명령만 센다.
type migZeroCountingExec struct{ n *int }

func (e migZeroCountingExec) Run(_ context.Context, _, _, _ string, steps []nvidia.CommandStep) error {
	for _, s := range steps {
		if len(s.Argv) >= 5 && s.Argv[3] == "-mig" && s.Argv[4] == "0" {
			*e.n++
		}
	}
	return nil
}

var _ = Describe("deletion restores MIG mode before checking the advertisement baseline", func() {
	// 증명: 조각이 회수됐지만 MIG mode 가 아직 켜져 있어 광고가 baseline 에 못 미치는 상태에서도
	// 모드 해제 명령이 나간다. mixed 전략에서 mode 가 켜진 채 조각이 없는 GPU 는 아무것도 광고하지
	// 않으므로 이 상태는 RestoreMode 삭제가 **반드시** 지나는 구간이다.
	// 깨는 뮤테이션: baseline 확인을 모드 복원보다 앞에 두면 baseline 미달로 먼저 반환해 해제
	// 명령이 영영 안 나가고 finalizer 가 영구히 남는다.
	It("issues the mode disable while the advertisement is still below baseline", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-below-baseline-node"
		labels := map[string]string{"kcloud.ai/restore-below-baseline": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-below-baseline-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		// 노드가 광고하는 GPU(1) 보다 baseline(2)을 크게 잡는다 — mode 를 끄기 전의 실제 상태다.
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		fresh.Status.ApplyRecords[0].BaselineGPUCount = 2
		Expect(k8sClient.Status().Update(ctx, &fresh)).To(Succeed())

		disables := 0
		r := nvidiaReconciler()
		r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return migZeroCountingExec{n: &disables} }
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		err := r.handleNvidiaDeletion(ctx, &fresh)

		Expect(err).To(HaveOccurred(), "모드가 아직 켜져 있는데 정리를 완료로 처리했다")
		Expect(disables).To(BeNumerically(">", 0),
			"광고가 baseline 에 못 미친다는 이유로 모드 해제가 막혔다 — 그 광고를 되돌리는 것이 모드 해제다")
		_, jerr := rebootJobFor(node)
		Expect(jerr).NotTo(HaveOccurred(), "재부팅이 실제로 예약되지 않았다")
	})
})

var _ = Describe("deletion keeps the cordon ownership it took for the mode restore reboot", func() {
	// schedulable 노드와 CordonedByPolicy=false 기록으로 RestoreMode 삭제 첫 pass 를 돌린 뒤의 상태를
	// 돌려준다. 성공한 apply 는 종점에서 노드를 되돌려 놓으므로 삭제 시점의 실제 상태가 이 조합이다.
	// 기존 시드는 cordon 된 노드와 CordonedByPolicy=true 라 이 경로를 지나지 않는다.
	firstPass := func(name string) (*AcceleratorPartitionPolicyReconciler, string, string) {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := name + "-node"
		labels := map[string]string{"kcloud.ai/" + name: "true"}
		seedNvidiaNode(node, labels, false, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP(name+"-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)
		var seeded npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &seeded)).To(Succeed())
		seeded.Status.ApplyRecords[0].CordonedByPolicy = false
		Expect(k8sClient.Status().Update(ctx, &seeded)).To(Succeed())

		r := nvidiaReconciler()
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		Expect(r.handleNvidiaDeletion(ctx, &fresh)).To(HaveOccurred(), "재부팅을 막 예약했으니 정리는 아직 안 끝나야 한다")
		return r, node, acpp.Name
	}

	// 증명: 삭제가 cordon 을 잡은 뒤 mode 복원 재부팅 예약이 저널을 다시 써도 cordon 소유 표시가 남는다.
	// 깨는 뮤테이션: 삭제 루프의 재조회와 restoreMigModeDisabled 의 최신 기록 시작을 모두 되돌리면
	// CordonedByPolicy 가 false 로 돌아가 이 단언이 실패한다(둘 중 하나만 되돌리면 통과한다).
	It("keeps CordonedByPolicy after scheduling the mode restore reboot", func() {
		_, node, name := firstPass("restore-cordon-owned")

		var after npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &after)).To(Succeed())
		rec := getApplyRecord(&after, node)
		Expect(rec.DisableRebootAttempts).To(Equal(int32(1)), "재부팅 예약이 기록되지 않았다")
		Expect(rec.CordonedByPolicy).To(BeTrue(),
			"삭제가 cordon 한 노드인데 소유 표시가 지워졌다 — 삭제가 끝나도 아무도 uncordon 하지 않는다")
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeTrue(), "재부팅 대기 중에는 노드가 cordon 상태여야 한다")
	})

	// 증명: mode 가 Disabled 로 관측되는 마지막 pass 에서 정책이 잡은 cordon 이 풀려 노드가 schedulable
	// 로 돌아온다. 소유 표시가 지워지면 정리는 끝나는데 cordon 만 남는다.
	// 깨는 뮤테이션: 위와 같이 두 수정을 모두 되돌리면 restoreSchedulable 이 uncordon 을 건너뛴다.
	It("returns the node to schedulable on the pass after the mode restore", func() {
		r, node, name := firstPass("restore-cordon-released")

		var ndr npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &ndr)).To(Succeed())
		ndr.Status.Devices[0].MigModeCurrent = migModeDisabled
		ndr.Status.Devices[0].MigModePending = migModeDisabled
		Expect(k8sClient.Status().Update(ctx, &ndr)).To(Succeed())

		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &fresh)).To(Succeed())
		Expect(r.handleNvidiaDeletion(ctx, &fresh)).To(Succeed())

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node}, &n)).To(Succeed())
		Expect(n.Spec.Unschedulable).To(BeFalse(), "삭제가 끝났는데 노드가 cordon 으로 남았다")
	})

	// 증명: 호출자가 들고 있는 기록이 옛 복사본(CordonedByPolicy=false)이어도 재부팅 예약은 저장된
	// 최신 기록(CordonedByPolicy=true)에서 시작해 그 표시를 지우지 않는다. 삭제 루프의 재조회와
	// 무관하게 이 함수 하나만 직접 불러 확인한다.
	// 깨는 뮤테이션: restoreMigModeDisabled 의 최신 기록 시작을 인자 복사본 시작으로 되돌리면
	// CordonedByPolicy 가 false 로 돌아가 이 단언이 실패한다.
	It("starts the reboot bookkeeping from the stored record, not the caller's stale copy", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		node := "restore-stale-copy-node"
		labels := map[string]string{"kcloud.ai/restore-stale-copy": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })

		acpp := mkNvidiaACPP("restore-stale-copy-acpp", labels)
		acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRestoreMode
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node) // 저장된 기록: CordonedByPolicy=true

		r := nvidiaReconciler()
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		stale := getApplyRecord(&fresh, node)
		Expect(stale.CordonedByPolicy).To(BeTrue(), "시드가 저장된 기록에 소유 표시를 두지 않았다")
		stale.CordonedByPolicy = false // 삭제 루프가 cordon 을 잡기 전에 복사한 값

		done, err := r.restoreMigModeDisabled(ctx, &fresh, stale)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "재부팅을 예약했으니 아직 완료가 아니다")

		var after npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &after)).To(Succeed())
		rec := getApplyRecord(&after, node)
		Expect(rec.DisableRebootAttempts).To(Equal(int32(1)), "재부팅 예약이 기록되지 않았다")
		Expect(rec.CordonedByPolicy).To(BeTrue(), "옛 복사본이 저장된 cordon 소유 표시를 지웠다")
	})
})

var _ = Describe("deletion releases the node lease it took", func() {
	// 증명: 위임 모드에서 삭제가 잡은 노드 Lease 를 끝에 돌려준다.
	// 깨는 뮤테이션: releaseHeld 호출을 지우면 Lease 소유자가 남아 실패한다.
	It("releases the deletion lease after cleanup succeeds", func() {
		wipeACPPs()
		DeferCleanup(wipeACPPs)
		node := "lease-release-node"
		labels := map[string]string{"kcloud.ai/lease-release": "true"}
		seedNvidiaNode(node, labels, true, a30Device("", "Enabled", "Enabled", ""))
		DeferCleanup(func() { cleanupNvidia(node); cleanupRebootFixture(node) })
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1")
		GinkgoT().Setenv("KCLOUD_OPERATION_COORDINATOR", "delegate")

		acpp := mkNvidiaACPP("lease-release-acpp", labels)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		seedDeletableRecord(acpp, node)

		r := nvidiaReconciler()
		r.Leases = &operation.LeaseManager{Client: k8sClient, Namespace: "default"}
		var fresh npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: acpp.Name}, &fresh)).To(Succeed())
		Expect(r.handleNvidiaDeletion(ctx, &fresh)).To(Succeed())

		var lease coordinationv1.Lease
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcloud-node-" + node, Namespace: "default"}, &lease)
		if err == nil && lease.Spec.HolderIdentity != nil {
			Expect(*lease.Spec.HolderIdentity).To(BeEmpty(),
				"삭제가 끝났는데 Lease 소유자가 남았다: %s", *lease.Spec.HolderIdentity)
		}
	})
})
