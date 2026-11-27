// ============================================================
// suite_test.go: 장애 주입 하네스 부트스트랩
// 상세: envtest 위에서 AcceleratorOperation 컨트롤러를 직접 구동하고, 작업 본체(participant)는
// 이 패키지가 정의한 가짜로 갈아 끼워 장애 지점을 정확한 순간에 주입한다. 증명 대상은
// 조정자의 제어 흐름뿐이고, 하드웨어 판정은 이 시험의 책임이 아니다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package fault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/controller"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/verification"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	// report 는 지표 수집기다. 각 스펙이 결과를 적고 AfterSuite 가 파일로 떨군다.
	report = newReport()
)

func TestFaultInjection(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fault Injection Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	Expect(npuv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := firstEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "kcloud"},
	})).To(Succeed())
})

var _ = AfterSuite(func() {
	// 판정표는 내부 docs/ 트리에 쓴다. 공개 저장소에는 docs/ 가 없으므로 디렉터리가 없으면
	// 건너뛴다 — 시험 결과 자체는 위의 단언이 판정한다.
	if _, err := os.Stat("../../docs/verification"); err == nil {
		Expect(report.write("../../docs/verification/stage5-metrics.md")).To(Succeed())
	}
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

func firstEnvTestBinaryDir() string {
	base := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(base, e.Name())
		}
	}
	return ""
}

// ---------- 가짜 작업 본체 ----------

// fakeParticipant 는 장애를 주입할 수 있는 작업 본체다. 각 훅이 nil 이면 정상 경로다.
type fakeParticipant struct {
	applyCalls    int
	rollbackCalls int
	// OnApply 는 Apply 호출마다 불린다. 반환값이 그대로 Outcome 이 된다.
	OnApply func(n int, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error)
	// OnRollback 도 같다.
	OnRollback func(n int, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error)
	// Verify 가 false 면 검증 단계를 건너뛴다.
	Verify bool
}

func (p *fakeParticipant) Apply(_ context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	p.applyCalls++
	if p.OnApply != nil {
		return p.OnApply(p.applyCalls, op)
	}
	return operation.Outcome{Event: operation.EventApplyDone, Message: "fake apply"}, nil
}

func (p *fakeParticipant) Rollback(_ context.Context, op *npuv1alpha1.AcceleratorOperation) (operation.Outcome, error) {
	p.rollbackCalls++
	if p.OnRollback != nil {
		return p.OnRollback(p.rollbackCalls, op)
	}
	return operation.Outcome{Event: operation.EventCompensated, Message: "fake rollback"}, nil
}

func (p *fakeParticipant) VerifyRequest(_ context.Context,
	op *npuv1alpha1.AcceleratorOperation) (verification.Request, bool, error) {
	if !p.Verify {
		return verification.Request{}, false, nil
	}
	return verification.Request{NodeName: op.Spec.NodeName, Vendor: op.Spec.Vendor, SourcePolicy: op.Name}, true, nil
}

// ---------- 하네스 ----------

// harness 는 한 시나리오가 쓰는 컨트롤러와 참가자다.
type harness struct {
	r      *controller.AcceleratorOperationReconciler
	fakes  map[operation.Type]*fakeParticipant
	leases *operation.LeaseManager
}

func newHarness(kinds ...operation.Type) *harness {
	if len(kinds) == 0 {
		kinds = []operation.Type{operation.PartitionReconfigure}
	}
	fakes := map[operation.Type]*fakeParticipant{}
	reg := operation.Registry{}
	for _, t := range kinds {
		f := &fakeParticipant{}
		fakes[t] = f
		reg[t] = f
	}
	leases := &operation.LeaseManager{
		Client: k8sClient, Namespace: "kcloud", Holder: "fault-harness", Duration: 60 * time.Second,
	}
	return &harness{
		r: &controller.AcceleratorOperationReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(256), Participants: reg, Leases: leases,
			// 검증기를 반드시 심는다 — nil 이면 컨트롤러가 검증 없이 통과시키므로(테스트 전용 경로)
			// F08 같은 "광고가 안 돌아왔는데 성공했는가" 를 물어볼 수 없다.
			Verification: &verification.Verifier{Client: k8sClient},
		},
		fakes:  fakes,
		leases: leases,
	}
}

// run 은 최대 n 번 reconcile 한다(종점이면 멈춘다). 컨트롤러가 스스로 requeue 하는 것을 흉내 낸다.
func (h *harness) run(name string, n int) *npuv1alpha1.AcceleratorOperation {
	var op npuv1alpha1.AcceleratorOperation
	for i := 0; i < n; i++ {
		_, err := h.r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &op)).To(Succeed())
		if operation.IsTerminal(op.Status.Phase) {
			break
		}
	}
	return &op
}

// mkOp 는 작업 객체를 만든다.
func mkOp(name, node string, t operation.Type, keys ...operation.ResourceKey) {
	ks := make([]string, 0, len(keys))
	for _, k := range keys {
		ks = append(ks, string(k))
	}
	if len(ks) == 0 {
		ks = []string{string(operation.NodeCordonKey(node))}
	}
	op := &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: string(t), NodeName: node, Vendor: "nvidia",
			TransactionID: name,
			Owner:         npuv1alpha1.OperationOwner{Kind: "FaultHarness", Name: name},
			ResourceKeys:  ks,
		},
	}
	Expect(k8sClient.Create(ctx, op)).To(Succeed())
}

// seedNode 는 Ready 노드 하나를 만든다(이미 있으면 그대로 쓴다).
func seedNode(name string) *corev1.Node {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8sClient.Create(ctx, node); err != nil && !strings.Contains(err.Error(), "already exists") {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, node)).To(Succeed())
	node.Status.Conditions = []corev1.NodeCondition{{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now(),
	}}
	node.Status.Allocatable = corev1.ResourceList{
		"nvidia.com/gpu": *resource.NewQuantity(2, resource.DecimalSI),
	}
	node.Status.NodeInfo.BootID = "boot-" + name
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
	return node
}

// cleanupOps 는 스펙 사이에 작업과 Lease 를 비운다 — 남으면 다음 스펙의 진입 판정이 오염된다.
func cleanupOps() {
	var list npuv1alpha1.AcceleratorOperationList
	Expect(k8sClient.List(ctx, &list)).To(Succeed())
	for i := range list.Items {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &list.Items[i]))).To(Succeed())
	}
}

// journalSteps 는 저널 단계 이름 목록이다(순서 검증용).
func journalSteps(op *npuv1alpha1.AcceleratorOperation) []string {
	out := make([]string, 0, len(op.Status.Journal))
	for _, j := range op.Status.Journal {
		out = append(out, j.Step)
	}
	return out
}
