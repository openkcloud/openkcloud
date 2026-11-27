// ============================================================
// acceleratordescriptor_controller_test.go: descriptor status 수렴 검증
// 생성일: 2026-09-15 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/verification"
)

// descriptorReconciler 는 시험용 reconciler 다. 이 패키지의 envtest 스위트는
// 매니저를 띄우지 않으므로(suite_test.go 에 ctrl.NewManager 가 없다) 시험이
// Reconcile 을 직접 부른다 — acpp_evidence_gate_test.go 와 같은 방식이다.
func descriptorReconciler() *AcceleratorDescriptorReconciler {
	return &AcceleratorDescriptorReconciler{
		Client: k8sClient, Scheme: k8sClient.Scheme(),
	}
}

var _ = Describe("AcceleratorDescriptor 컨트롤러", func() {
	It("생성 가능한 backend 를 status 에 기록한다", func() {
		desc := &npuv1alpha1.AcceleratorDescriptor{
			ObjectMeta: metav1.ObjectMeta{Name: "rngd-ok"},
			Spec: npuv1alpha1.AcceleratorDescriptorSpec{
				Vendor:         "rngd",
				Product:        "rngd",
				AllocationUnit: "device",
				Identity: npuv1alpha1.DescriptorIdentity{
					Source: "pciAddress", StableAcrossReboot: true,
				},
				DeviceNodes: []string{"/dev/rngd/{{id}}*"},
				Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			},
		}
		Expect(k8sClient.Create(ctx, desc)).To(Succeed())

		r := descriptorReconciler()
		Eventually(func() []string {
			_, _ = r.Reconcile(ctx, reconcileReq("rngd-ok"))
			var got npuv1alpha1.AcceleratorDescriptor
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "rngd-ok"}, &got); err != nil {
				return nil
			}
			return got.Status.FeasibleBackends
		}, "20s", "300ms").Should(ContainElement(npuv1alpha1.BackendDevicePlugin))
	})

	It("거절 사유를 삼키지 않고 status 에 남긴다", func() {
		desc := &npuv1alpha1.AcceleratorDescriptor{
			ObjectMeta: metav1.ObjectMeta{Name: "rngd-refused"},
			Spec: npuv1alpha1.AcceleratorDescriptorSpec{
				Vendor:         "rngd",
				Product:        "rngd",
				AllocationUnit: "device",
				Identity: npuv1alpha1.DescriptorIdentity{
					Source: "pciAddress", StableAcrossReboot: false,
				},
				DeviceNodes: []string{"/dev/rngd/{{id}}*"},
				Backends:    []string{npuv1alpha1.BackendDRA},
			},
		}
		Expect(k8sClient.Create(ctx, desc)).To(Succeed())

		r := descriptorReconciler()
		Eventually(func() int {
			_, _ = r.Reconcile(ctx, reconcileReq("rngd-refused"))
			var got npuv1alpha1.AcceleratorDescriptor
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "rngd-refused"}, &got); err != nil {
				return 0
			}
			return len(got.Status.Refusals)
		}, "20s", "300ms").Should(BeNumerically(">", 0))
	})

	It("NDR 카드별 PCI 를 resolve 해 status.ResolvedDevices 에 정렬해 싣는다", func() {
		ndr := &npuv1alpha1.NodeDeviceReport{
			ObjectMeta: metav1.ObjectMeta{Name: "d4a-node1"},
			Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "d4a-node1"},
		}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
		ndr.Status = npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{
				// 역순으로 심는다 — sort.Strings 가 빠지면 그대로 역순 출력되어 시험이 깨진다.
				{Vendor: "nvidia", Model: "a30", Count: 1, PCIeAddress: "0000:3b:00.0"},
				{Vendor: "nvidia", Model: "a30", Count: 1, PCIeAddress: "0000:18:00.0"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())

		desc := &npuv1alpha1.AcceleratorDescriptor{
			ObjectMeta: metav1.ObjectMeta{Name: "d4a-nvidia-a30"},
			Spec: npuv1alpha1.AcceleratorDescriptorSpec{
				Vendor:         "nvidia",
				Product:        "a30",
				AllocationUnit: "device",
				Identity: npuv1alpha1.DescriptorIdentity{
					Source: "pciAddress", StableAcrossReboot: true,
				},
				DeviceNodes: []string{"/dev/nvidia{{id}}"},
				Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			},
		}
		Expect(k8sClient.Create(ctx, desc)).To(Succeed())

		r := descriptorReconciler()
		// nvidia+a30 조합은 이 패키지의 다른 envtest 시험(공유 apiserver)과 안 섞임을 grep 으로
		// 확인했다 — 전체 슬라이스를 그대로 색인해 ID 순서를 직접 검증한다(order-insensitive
		// 조회를 쓰면 정렬이 빠져도 통과해 버린다).
		Eventually(func() []npuv1alpha1.NodeDeviceIDs {
			_, _ = r.Reconcile(ctx, reconcileReq("d4a-nvidia-a30"))
			var got npuv1alpha1.AcceleratorDescriptor
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "d4a-nvidia-a30"}, &got); err != nil {
				return nil
			}
			return got.Status.ResolvedDevices
		}, "20s", "300ms").Should(Equal([]npuv1alpha1.NodeDeviceIDs{
			{Node: "d4a-node1", IDs: []string{"PCI-0000:18:00.0", "PCI-0000:3b:00.0"}},
		}))
	})

	It("여러 노드의 카드를 노드명 순으로 정렬해 status 에 싣는다", func() {
		// envtest apiserver 의 List() 는 NDR 을 metadata.name(etcd key) 순으로 돌려준다. 이름과
		// 노드명이 같은 방향으로 어긋나면(예: "n1"/"n2") 정렬 없는 map 삽입 순서가 이미 정렬된
		// 것과 같아져 sort.Strings 를 지워도 우연히 통과한다. 그래서 NDR 이름과 노드명을 반대
		// 방향으로 둔다: "aa-ndr"(먼저 나옴)의 노드는 "znode", "zz-ndr"(나중 나옴)의 노드는
		// "anode" — 정렬 없는 삽입 순서는 [znode, anode] 로 기대값([anode, znode])과 반대가 되어,
		// sort.Strings(nodes) 가 실제로 뒤집어야만 첫 Reconcile 부터 통과한다.
		aaNdr := &npuv1alpha1.NodeDeviceReport{
			ObjectMeta: metav1.ObjectMeta{Name: "aa-ndr"},
			Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "znode"},
		}
		Expect(k8sClient.Create(ctx, aaNdr)).To(Succeed())
		aaNdr.Status = npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{
				{Vendor: "nvidia", Model: "d4a-sort-model", Count: 1, PCIeAddress: "0000:aa:00.0"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, aaNdr)).To(Succeed())

		zzNdr := &npuv1alpha1.NodeDeviceReport{
			ObjectMeta: metav1.ObjectMeta{Name: "zz-ndr"},
			Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "anode"},
		}
		Expect(k8sClient.Create(ctx, zzNdr)).To(Succeed())
		zzNdr.Status = npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{
				{Vendor: "nvidia", Model: "d4a-sort-model", Count: 1, PCIeAddress: "0000:bb:00.0"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, zzNdr)).To(Succeed())

		desc := &npuv1alpha1.AcceleratorDescriptor{
			ObjectMeta: metav1.ObjectMeta{Name: "d4a-multi-node"},
			Spec: npuv1alpha1.AcceleratorDescriptorSpec{
				Vendor:         "nvidia",
				Product:        "d4a-sort-model",
				AllocationUnit: "device",
				Identity: npuv1alpha1.DescriptorIdentity{
					Source: "pciAddress", StableAcrossReboot: true,
				},
				DeviceNodes: []string{"/dev/nvidia{{id}}"},
				Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			},
		}
		Expect(k8sClient.Create(ctx, desc)).To(Succeed())

		r := descriptorReconciler()
		// product 를 이 시험 전용 값으로 지어 다른 nvidia 시험과 안 섞이게 한다. Eventually 의
		// 재시도는 이 확인에 안 맞는다(map 순회가 매 호출 무작위라 재시도 예산 안에서 우연히
		// 한 번은 통과해 버릴 수 있다) — 직접 여러 번 Reconcile 해 매번 순서를 확인한다.
		for i := 0; i < 10; i++ {
			_, err := r.Reconcile(ctx, reconcileReq("d4a-multi-node"))
			Expect(err).NotTo(HaveOccurred())
			var got npuv1alpha1.AcceleratorDescriptor
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "d4a-multi-node"}, &got)).To(Succeed())
			Expect(got.Status.ResolvedDevices).To(Equal([]npuv1alpha1.NodeDeviceIDs{
				{Node: "anode", IDs: []string{"PCI-0000:bb:00.0"}},
				{Node: "znode", IDs: []string{"PCI-0000:aa:00.0"}},
			}), "attempt %d", i)
		}
	})

	It("NDR vendor furiosa/model rngd 를 descriptor vendor rngd 에 매칭한다", func() {
		ndr := &npuv1alpha1.NodeDeviceReport{
			ObjectMeta: metav1.ObjectMeta{Name: "d4a-node2"},
			Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "d4a-node2"},
		}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
		ndr.Status = npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{
				{Vendor: "furiosa", Model: "rngd", Count: 1, PCIeAddress: "0000:01:00.0"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())

		desc := &npuv1alpha1.AcceleratorDescriptor{
			ObjectMeta: metav1.ObjectMeta{Name: "d4a-rngd-resolve"},
			Spec: npuv1alpha1.AcceleratorDescriptorSpec{
				Vendor:         "rngd",
				Product:        "rngd",
				AllocationUnit: "device",
				Identity: npuv1alpha1.DescriptorIdentity{
					Source: "pciAddress", StableAcrossReboot: true,
				},
				DeviceNodes: []string{"/dev/rngd/{{id}}*"},
				Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			},
		}
		Expect(k8sClient.Create(ctx, desc)).To(Succeed())

		r := descriptorReconciler()
		Eventually(func() []string {
			_, _ = r.Reconcile(ctx, reconcileReq("d4a-rngd-resolve"))
			var got npuv1alpha1.AcceleratorDescriptor
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "d4a-rngd-resolve"}, &got); err != nil {
				return nil
			}
			return idsForNode(got.Status.ResolvedDevices, "d4a-node2")
		}, "20s", "300ms").Should(Equal([]string{"PCI-0000:01:00.0"}))
	})
})

// idsForNode 는 ResolvedDevices 에서 한 노드의 ID 목록만 꺼낸다. 이 패키지 envtest 는 suite
// 전체가 apiserver 를 공유해 다른 시험의 NDR 이 함께 조회되므로, 전체 슬라이스가 아니라
// 시험 전용 노드 하나만 골라 확인해야 교차 오염에 흔들리지 않는다.
func idsForNode(resolved []npuv1alpha1.NodeDeviceIDs, node string) []string {
	for _, nd := range resolved {
		if nd.Node == node {
			return nd.IDs
		}
	}
	return nil
}

// fakeProbeCall 은 fakeAllocationProber.Probe 호출 한 번의 인자다.
type fakeProbeCall struct {
	nodeName, resourceName, vendor string
}

// fakeAllocationProber 는 verification.AllocationProber 를 흉내 내며 호출 인자를 기록한다.
// 항상 성공(exit=0)으로 응답한다 — 이 시험은 게이팅(불렸는지 여부)만 본다.
type fakeAllocationProber struct {
	calls []fakeProbeCall
}

func (f *fakeAllocationProber) Probe(_ context.Context, nodeName, resourceName, vendor string) (verification.ProbeResult, error) {
	f.calls = append(f.calls, fakeProbeCall{nodeName: nodeName, resourceName: resourceName, vendor: vendor})
	exit := int32(0)
	return verification.ProbeResult{Allocated: true, ExitCode: &exit}, nil
}

// TestDescriptorProbeGatedByPolicy 는 acceleratordescriptor_apply_test.go 의 TestMain envtest
// (aaClient)를 그대로 쓴다 — DeviceClass 가 아니라 Node/AcceleratorVerificationPolicy/
// AcceleratorEvidence 만 필요하지만 이 패키지의 유일한 TestMain 이 그쪽이라 공유한다.
//
// 케이스(b) 정책 없음을 먼저 확인한다 — AllocationProbe 를 켠 정책을 나중에 추가하면 케이스(a)로
// 자연히 넘어가고, 거꾸로 하려면 정책을 지우는 단계가 하나 더 필요해진다.
func TestDescriptorProbeGatedByPolicy(t *testing.T) {
	ctx := context.Background()
	const node = "k8s-worker3"

	nodeObj := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: node, Labels: map[string]string{"kubernetes.io/hostname": node},
	}}
	if err := aaClient.Create(ctx, nodeObj); err != nil {
		t.Fatalf("Node 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, nodeObj) })

	// NDR 의 metadata.name 은 nodeName 과 동일하게 관리된다(validator.go 주석) — verifier 가
	// loadReport 로 nodeName 키로 조회하므로, 이름이 다르면 NodeDeviceReport 체크가 보고서를 못 찾는다.
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: node},
	}
	if err := aaClient.Create(ctx, ndr); err != nil {
		t.Fatalf("NodeDeviceReport 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, ndr) })
	ndr.Status = npuv1alpha1.NodeDeviceReportStatus{
		Devices: []npuv1alpha1.DeviceEntry{
			{Vendor: "tenstorrent", Model: "blackhole-p150", Count: 1, PCIeAddress: "0000:aa:00.0"},
		},
	}
	if err := aaClient.Status().Update(ctx, ndr); err != nil {
		t.Fatalf("NodeDeviceReport status 갱신 실패: %v", err)
	}

	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "probe-gate-desc"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "tenstorrent", Product: "blackhole-p150", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/tenstorrent/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
		},
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	prober := &fakeAllocationProber{}
	r := autoApplyReconciler()
	r.Verification = &verification.Verifier{Client: aaClient, Prober: prober}

	// 케이스(b): AVP 가 없다 — prober 는 안 불리지만 AcceleratorEvidence 는 upsert 된다.
	reconcileOnce(t, r, desc.Name)
	if len(prober.calls) != 0 {
		t.Fatalf("정책 없이 prober 가 불림: %+v", prober.calls)
	}
	var ev npuv1alpha1.AcceleratorEvidence
	if err := aaClient.Get(ctx, types.NamespacedName{Name: node}, &ev); err != nil {
		t.Fatalf("정책 없이도 있어야 할 AcceleratorEvidence(%s) 조회 실패: %v", node, err)
	}

	// 케이스(a): AllocationProbe 를 켠 AVP 를 이 노드에 매칭 — prober 가 정확히 한 번 불린다.
	avp := &npuv1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "probe-gate-avp"},
		Spec: npuv1alpha1.AcceleratorVerificationPolicySpec{
			NodeSelector: map[string]string{"kubernetes.io/hostname": node},
			Checks: []string{
				npuv1alpha1.EvidenceCheckDeviceObservation, npuv1alpha1.EvidenceCheckNodeDeviceReport,
				npuv1alpha1.EvidenceCheckAdvertisement, npuv1alpha1.EvidenceCheckAllocationProbe,
			},
		},
	}
	if err := aaClient.Create(ctx, avp); err != nil {
		t.Fatalf("AcceleratorVerificationPolicy 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, avp) })

	reconcileOnce(t, r, desc.Name)
	if len(prober.calls) != 1 {
		t.Fatalf("AllocationProbe 켠 정책 매칭인데 prober 호출 횟수=%d(기대 1): %+v", len(prober.calls), prober.calls)
	}
	got := prober.calls[0]
	if got.vendor != "tenstorrent" || got.resourceName != "tenstorrent/blackhole-p150" || got.nodeName != node {
		t.Fatalf("prober 호출 인자 불일치: %+v", got)
	}

	// probe 가 통과(exit=0)하면 evidence 는 등급을 받아야 한다 — Request 에 관측 geometry 가 없으면
	// DeviceObservation 이 "no device observation available" 로 실패해 Level 이 매 재조정마다
	// 빈 문자열로 남는 구조적 결함이 된다(descriptor 경로 evidence 전체에 영향).
	if err := aaClient.Get(ctx, types.NamespacedName{Name: node}, &ev); err != nil {
		t.Fatalf("AcceleratorEvidence(%s) 재조회 실패: %v", node, err)
	}
	if ev.Status.Level == "" {
		t.Fatalf("probe 통과인데 evidence 가 등급을 못 받았다(빈 Level): reason=%q checks=%+v",
			ev.Status.Reason, ev.Status.Checks)
	}
}

// TestDescriptorProbeErrorNonFatal 은 resolved 노드 중 하나가 Verify 를 실패시켜도(여기서는
// corev1.Node 오브젝트 자체가 없는 노드를 가리키는 낡은 NodeDeviceReport로 재현 — verifier.go 의
// Node 조회는 NotFound 를 관용하지 않는다) Reconcile 전체가 에러로 중단되지 않고, 다른 정상 노드는
// 여전히 Verify(및 prober 호출)를 받으며, descriptor status(FeasibleBackends/ResolvedDevices)도
// 정상적으로 기록되는지 확인한다.
func TestDescriptorProbeErrorNonFatal(t *testing.T) {
	ctx := context.Background()
	const okNode = "probe-err-ok-node"
	const missingNode = "probe-err-missing-node" // NDR 이 가리키지만 Node 오브젝트를 만들지 않는다

	okNodeObj := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: okNode, Labels: map[string]string{"kubernetes.io/hostname": okNode},
	}}
	if err := aaClient.Create(ctx, okNodeObj); err != nil {
		t.Fatalf("Node 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, okNodeObj) })

	ndrOK := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "probe-err-ndr-ok"},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: okNode},
	}
	if err := aaClient.Create(ctx, ndrOK); err != nil {
		t.Fatalf("NodeDeviceReport 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, ndrOK) })
	ndrOK.Status = npuv1alpha1.NodeDeviceReportStatus{
		Devices: []npuv1alpha1.DeviceEntry{
			{Vendor: "tenstorrent", Model: "blackhole-p150", Count: 1, PCIeAddress: "0000:bb:00.0"},
		},
	}
	if err := aaClient.Status().Update(ctx, ndrOK); err != nil {
		t.Fatalf("NodeDeviceReport status 갱신 실패: %v", err)
	}

	ndrMissing := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "probe-err-ndr-missing"},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: missingNode},
	}
	if err := aaClient.Create(ctx, ndrMissing); err != nil {
		t.Fatalf("NodeDeviceReport 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, ndrMissing) })
	ndrMissing.Status = npuv1alpha1.NodeDeviceReportStatus{
		Devices: []npuv1alpha1.DeviceEntry{
			{Vendor: "tenstorrent", Model: "blackhole-p150", Count: 1, PCIeAddress: "0000:cc:00.0"},
		},
	}
	if err := aaClient.Status().Update(ctx, ndrMissing); err != nil {
		t.Fatalf("NodeDeviceReport status 갱신 실패: %v", err)
	}

	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "probe-err-desc"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "tenstorrent", Product: "blackhole-p150", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/tenstorrent/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
		},
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	// 빈 nodeSelector = 전 노드 매칭. 정상 노드는 이 정책으로 prober 까지 불려야 한다.
	avp := &npuv1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "probe-err-avp"},
		Spec: npuv1alpha1.AcceleratorVerificationPolicySpec{
			Checks: []string{
				npuv1alpha1.EvidenceCheckDeviceObservation, npuv1alpha1.EvidenceCheckNodeDeviceReport,
				npuv1alpha1.EvidenceCheckAdvertisement, npuv1alpha1.EvidenceCheckAllocationProbe,
			},
		},
	}
	if err := aaClient.Create(ctx, avp); err != nil {
		t.Fatalf("AcceleratorVerificationPolicy 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, avp) })

	prober := &fakeAllocationProber{}
	r := autoApplyReconciler()
	r.Verification = &verification.Verifier{Client: aaClient, Prober: prober}

	if _, err := r.Reconcile(ctx, reconcileReq(desc.Name)); err != nil {
		t.Fatalf("노드 하나의 Verify 실패로 Reconcile 전체가 에러를 반환하면 안 된다: %v", err)
	}

	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if len(got.Status.FeasibleBackends) == 0 {
		t.Fatalf("FeasibleBackends 가 비어 있다(비치명 처리라면 채워져 있어야 한다): %+v", got.Status)
	}
	if len(got.Status.ResolvedDevices) != 2 {
		t.Fatalf("ResolvedDevices 개수=%d(기대 2): %+v", len(got.Status.ResolvedDevices), got.Status.ResolvedDevices)
	}

	// 정상 노드는 Verify 가 끝까지 돌아 evidence 가 upsert 되고 prober 도 호출됐어야 한다.
	var okEv npuv1alpha1.AcceleratorEvidence
	if err := aaClient.Get(ctx, types.NamespacedName{Name: okNode}, &okEv); err != nil {
		t.Fatalf("정상 노드(%s)의 AcceleratorEvidence 조회 실패: %v", okNode, err)
	}
	okCalled := false
	for _, c := range prober.calls {
		if c.nodeName == okNode {
			okCalled = true
		}
	}
	if !okCalled {
		t.Fatalf("실패 노드가 섞여 있어도 정상 노드(%s)는 prober 가 불려야 한다: %+v", okNode, prober.calls)
	}

	// 실패 노드는 corev1.Node 가 없어 Verify 가 애초에 evidence 를 만들지 못했어야 한다.
	var missingEv npuv1alpha1.AcceleratorEvidence
	err := aaClient.Get(ctx, types.NamespacedName{Name: missingNode}, &missingEv)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Node 오브젝트가 없는 노드(%s)에 evidence 가 생기면 안 된다(err=%v)", missingNode, err)
	}
}
