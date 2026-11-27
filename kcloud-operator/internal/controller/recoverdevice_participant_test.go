// ============================================================
// recoverdevice_participant_test.go: RecoverDevice 본체 envtest
// 상세: 드라이버 pod 을 지우는지, 지울 대상이 없을 때 조용히 성공하지 않는지 고정한다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/health"
	"kcloud-operator/internal/operation"
)

// TestDriverRecoveryVendorNarrowsToTheBrokenVendorOnly 는 driverRecoveryVendor 가 고장난
// 벤더 하나만 골라내는지, 두 벤더가 같이 고장이면 비우는지(=참가자가 전부 대상) 본다. enum
// 밖 벤더도 비운다 — 좁혔다가 op.Spec.Vendor 가 CRD enum 검증에 걸리면 AcceleratorOperation
// 생성 자체가 거절돼 그 노드의 health 감시가 통째로 멈춘다.
// 깨는 뮤테이션: vendor 비교 갈래를 지우면 두 벤더 케이스에서 첫 벤더만 남고, enum 화이트리스트
// 체크를 지우면 enum 밖 벤더가 새 나가 실패한다.
func TestDriverRecoveryVendorNarrowsToTheBrokenVendorOnly(t *testing.T) {
	cases := []struct {
		name    string
		devices []health.DeviceResult
		want    string
	}{
		{"한 벤더만 고장", []health.DeviceResult{
			{Vendor: "nvidia", State: health.StateHealthy},
			{Vendor: "furiosa", State: health.StateUnhealthy, Reason: health.ReasonDriverNotLoaded},
		}, "furiosa"},
		{"두 벤더가 같이 고장", []health.DeviceResult{
			{Vendor: "nvidia", State: health.StateUnhealthy, Reason: health.ReasonDriverNotLoaded},
			{Vendor: "furiosa", State: health.StateUnhealthy, Reason: health.ReasonDriverNotLoaded},
		}, ""},
		{"장치 판정 없음(노드 단위 조기 종료 경로)", nil, ""},
		{"enum 밖 벤더", []health.DeviceResult{
			{Vendor: "unknown-vendor", State: health.StateUnhealthy, Reason: health.ReasonDriverNotLoaded},
		}, ""},
	}
	for _, c := range cases {
		if got := driverRecoveryVendor(operation.RecoverDevice, c.devices); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// RecoverDevice 가 아닌 작업 타입은 벤더를 매기지 않는다(다른 타입은 이 필드를 안 쓴다).
	if got := driverRecoveryVendor(operation.DevicePluginRestart,
		[]health.DeviceResult{{Vendor: "nvidia", State: health.StateUnhealthy, Reason: health.ReasonDriverNotLoaded}}); got != "" {
		t.Errorf("RecoverDevice 가 아닌데 벤더를 매겼다: %q", got)
	}
}

// seedDriverPod 는 그 노드의 드라이버 pod 을 만든다(component 라벨로 식별).
func seedDriverPod(name, node, component string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "default",
			Labels: map[string]string{driverComponentLabel: component},
		},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{
			Name: "driver", Image: "harbor.local/kcloud/driver-installer:v1",
		}}},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

func recoverDeviceOp(name, node string) *npuv1alpha1.AcceleratorOperation {
	return &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: string(operation.RecoverDevice), NodeName: node, TransactionID: name,
			Owner: npuv1alpha1.OperationOwner{Kind: "AcceleratorHealth", Name: node},
		},
	}
}

// seedVendorDriverPod 는 벤더 라벨이 달린 드라이버 pod 을 만든다(벤더 범위 시험용).
func seedVendorDriverPod(name, node, component, vendor string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "default",
			Labels: map[string]string{driverComponentLabel: component, driverVendorLabel: vendor},
		},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{
			Name: "driver", Image: "harbor.local/kcloud/driver-installer:v1",
		}}},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

var _ = Describe("RecoverDevice participant", func() {
	// 증명: 그 노드의 드라이버 pod 을 지운다(설치 Job·DS 둘 다).
	// 깨는 뮤테이션: driverPodComponents 에서 "driver-install" 을 빼면 DS pod 만 지워져 실패한다.
	It("restarts every driver pod on the node", func() {
		node := "recover-device-node"
		dsPod := seedDriverPod("drv-ds-"+node, node, "driver")
		jobPod := seedDriverPod("drv-job-"+node, node, "driver-install")
		// 다른 노드의 pod 은 건드리지 않는다.
		other := seedDriverPod("drv-ds-other", "some-other-node", "driver")
		DeferCleanup(func() {
			for _, p := range []*corev1.Pod{dsPod, jobPod, other} {
				_ = k8sClient.Delete(ctx, p, client.GracePeriodSeconds(0))
			}
		})

		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		out, err := p.Apply(ctx, recoverDeviceOp("recover-ok", node))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyDone))

		for _, p := range []*corev1.Pod{dsPod, jobPod} {
			var got corev1.Pod
			err := k8sClient.Get(ctx, types.NamespacedName{Name: p.Name, Namespace: "default"}, &got)
			Expect(err != nil || got.DeletionTimestamp != nil).To(BeTrue(),
				"드라이버 pod %s 을 지우지 않았다", p.Name)
		}
		var untouched corev1.Pod
		Expect(k8sClient.Get(ctx,
			types.NamespacedName{Name: other.Name, Namespace: "default"}, &untouched)).To(Succeed())
		Expect(untouched.DeletionTimestamp).To(BeNil(), "다른 노드의 드라이버 pod 을 지웠다")
	})

	// 증명: 되살릴 대상이 없으면 **성공했다고 말하지 않는다.**
	//
	// 조용히 성공하면 health 는 복구가 된 줄 알고 같은 요청을 쿨다운마다 영원히 되풀이한다.
	// 실패로 끝나야 반복 실패가 세어지고 세 번이면 격리로 올라가 사람을 부른다.
	//
	// 깨는 뮤테이션: deleted == 0 갈래를 지우고 항상 ApplyDone 을 돌려주면 실패한다.
	It("reports failure when there is no driver pod to restart", func() {
		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		out, err := p.Apply(ctx, recoverDeviceOp("recover-empty", "node-without-driver-pods"))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyFailed),
			"되살릴 대상이 없는데 성공을 보고하면 같은 요청이 영원히 반복된다")
	})

	// 증명: op.Spec.Vendor 가 지정되면 다른 벤더의 드라이버 pod 은 건드리지 않는다 —
	// 혼재 노드에서 한 벤더의 드라이버 고장이 멀쩡한 다른 벤더의 드라이버 pod 까지 지우면 안 된다.
	// 깨는 뮤테이션: restartDriverPods 의 vendor 필터(lbls[driverVendorLabel] = vendor)를 지우면
	// 다른 벤더 pod 도 지워져 실패한다.
	It("does not restart a different vendor's driver pod", func() {
		node := "recover-vendor-node"
		nvidiaPod := seedVendorDriverPod("drv-nvidia-"+node, node, "driver", "nvidia")
		furiosaPod := seedVendorDriverPod("drv-furiosa-"+node, node, "driver", "furiosa")
		DeferCleanup(func() {
			for _, p := range []*corev1.Pod{nvidiaPod, furiosaPod} {
				_ = k8sClient.Delete(ctx, p, client.GracePeriodSeconds(0))
			}
		})

		op := recoverDeviceOp("recover-vendor", node)
		op.Spec.Vendor = "nvidia"
		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		out, err := p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyDone))

		var gotNvidia corev1.Pod
		err = k8sClient.Get(ctx, types.NamespacedName{Name: nvidiaPod.Name, Namespace: "default"}, &gotNvidia)
		Expect(err != nil || gotNvidia.DeletionTimestamp != nil).To(BeTrue(),
			"대상 벤더의 드라이버 pod 을 지우지 않았다")

		var gotFuriosa corev1.Pod
		Expect(k8sClient.Get(ctx,
			types.NamespacedName{Name: furiosaPod.Name, Namespace: "default"}, &gotFuriosa)).To(Succeed())
		Expect(gotFuriosa.DeletionTimestamp).To(BeNil(), "다른 벤더의 드라이버 pod 을 지웠다")
	})

	// 증명: 벤더 필터가 라벨과 어긋나 0건이면, 필터 없이 한 번 더 찾아서라도 지운다.
	// NDR 이 준 벤더값(detector 소문자화)과 pod 라벨(DIP Spec.Vendor 소문자)은 서로 다른
	// 주체가 만들어 어긋날 수 있다 — 어긋난 채로 조용히 deleted==0 을 내면 ApplyFailed → 3회 →
	// 영구 격리(Recovering 으로만 나가는데 그 상태가 다시 안 온다)로 간다.
	// 깨는 뮤테이션: driverPodsFor 의 fallback(len(pods)==0 이면 vendor 없이 재조회)을 지우면
	// deleted==0 이 되어 ApplyFailed 로 실패한다.
	It("still restarts the pod when its vendor label does not match the operation's vendor", func() {
		node := "recover-mismatched-vendor-node"
		// 라벨이 비어 있거나(구버전) 다른 값이면 벤더 필터만으로는 못 찾는다.
		pod := seedDriverPod("drv-nolabel-"+node, node, "driver")
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod, client.GracePeriodSeconds(0)) })

		op := recoverDeviceOp("recover-mismatched-vendor", node)
		op.Spec.Vendor = "furiosa" // pod 은 이 라벨을 안 달고 있다.
		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		out, err := p.Apply(ctx, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Event).To(Equal(operation.EventApplyDone),
			"벤더 라벨이 어긋난다고 되살릴 대상이 없다고 보고했다 — 반복 실패로 노드가 격리된다")

		var got corev1.Pod
		err = k8sClient.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: "default"}, &got)
		Expect(err != nil || got.DeletionTimestamp != nil).To(BeTrue(), "라벨이 어긋난 pod 을 못 찾았다")
	})

	// 증명: health 가 만든 작업은 검증을 요구하지 않는다.
	// 정책이 없으면 장치 관측 주체도 없어 검증이 구조적으로 실패하고, 그러면 이 작업은 항상
	// RolledBack 이 되어 세 번 만에 멀쩡한 노드가 격리된다.
	It("does not demand verification for a health-owned operation", func() {
		p := NewRecoverDeviceParticipant(&AcceleratorPartitionPolicyReconciler{Client: k8sClient})
		_, applicable, err := p.VerifyRequest(ctx, recoverDeviceOp("recover-verify", "any-node"))
		Expect(err).NotTo(HaveOccurred())
		Expect(applicable).To(BeFalse())
	})
})
