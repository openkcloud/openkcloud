// ============================================================
// acpp_migmode_test.go: MIG mode enable 오케스트레이션 테스트
// 상세: Disabled 관측 → quiesce → enable → pending → reboot Job → 재관측 Enabled → done.
// 실패 시 노드 schedulable 복원, 관측 불가 시 fail-closed, 크래시 저널 재진입 회귀까지 고정한다.
// 생성일: 2026-07-29 | 수정일: 2026-10-13
// ============================================================
package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/partition/nvidia"
)

const migModePCI = "0000:18:00.0"

// failingExec 는 -mig 1 실행 실패를 흉내낸다(노드 복원 경로 검증용).
type failingExec struct{}

func (failingExec) Run(context.Context, string, string, string, []nvidia.CommandStep) error {
	return errors.New("nvidia-smi -mig 1 failed")
}

func migModeACPP() *npuv1alpha1.AcceleratorPartitionPolicy {
	return &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-migmode", UID: "acpp-uid-1"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			NodeSelector:   map[string]string{"kubernetes.io/hostname": "worker1"},
			Vendor:         "nvidia",
			Layout:         []npuv1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 4}},
			DeletionPolicy: "Retain",
		},
	}
}

func migModeTarget(ctx context.Context) partition.Target {
	return partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-migmode"}
}

// migModeFixture 는 노드(cordon 여부 지정) + ACPP 로 구성한 mode-enable 테스트 환경이다.
func migModeFixture(t *testing.T, acpp *npuv1alpha1.AcceleratorPartitionPolicy, cordoned bool, objs ...client.Object) (*AcceleratorPartitionPolicyReconciler, client.Client) {
	t.Helper()
	t.Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1") // 재부팅/enable Job 이 쓰는 nsenter 이미지
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "worker1",
			Labels:      map[string]string{"kubernetes.io/hostname": "worker1"},
			Annotations: map[string]string{migOwnerAnnotation: string(acpp.UID)},
		},
		Spec: corev1.NodeSpec{Unschedulable: cordoned},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceName(nvidiaGPUResource): *resource.NewQuantity(1, resource.DecimalSI),
		}},
	}
	all := append([]client.Object{node, acpp}, objs...)
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).
		WithObjects(all...).
		WithStatusSubresource(&npuv1alpha1.AcceleratorPartitionPolicy{}).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	return &AcceleratorPartitionPolicyReconciler{
		Client: c, Scheme: c.Scheme(), Verifier: allocVerifier{},
		NvidiaExecFactory: func(client.Client) nvidia.Executor { return fakeNvidiaExec{} },
	}, c
}

// migModePod 은 노드에서 GPU 를 점유한 일반(비면제) pod 이다. finalizer 를 붙이면 evict 후에도
// 남아 AssertQuiesced 를 계속 실패시킨다(배출 미완료 재현).
func migModePod(name string, withFinalizer bool) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{NodeName: "worker1", Containers: []corev1.Container{{
			Name: "c", Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceName(nvidiaGPUResource): resource.MustParse("1")},
			}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if withFinalizer {
		p.Finalizers = []string{"test/hold"}
	}
	return p
}

func nodeCordoned(t *testing.T, c client.Client) bool {
	t.Helper()
	var n corev1.Node
	if err := c.Get(context.Background(), types.NamespacedName{Name: "worker1"}, &n); err != nil {
		t.Fatal(err)
	}
	return n.Spec.Unschedulable
}

func migPhaseOf(t *testing.T, c client.Client, name string) string {
	t.Helper()
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.Get(context.Background(), types.NamespacedName{Name: name}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.ApplyRecords) == 0 {
		return ""
	}
	return got.Status.ApplyRecords[0].MigPhase
}

func TestEnsureMigModeEnabledCordonsBeforeEnabling(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false)
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ModePending: migModeDisabled}}

	done, ts, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatalf("must not be done before reboot")
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseApplying {
		t.Fatalf("phase = %q, want Applying", ts.Phase)
	}
	if !nodeCordoned(t, c) {
		t.Fatalf("node must be cordoned before mode enable")
	}
	if got := migPhaseOf(t, c, acpp.Name); got != npuv1alpha1.MigPhaseModeEnabling {
		t.Fatalf("journal migPhase = %q, want ModeEnabling", got)
	}
}

func TestEnsureMigModeEnabledCreatesRebootJobWhenPending(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, true)
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ModePending: migModeEnabled}}

	done, ts, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatalf("must wait for reboot")
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseApplying {
		t.Fatalf("phase = %q, want Applying", ts.Phase)
	}
	var jobs batchv1.JobList
	if err := c.List(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	var found bool
	for i := range jobs.Items {
		if jobs.Items[i].Labels["app.kubernetes.io/component"] == "node-reboot" {
			found = true
			if o := jobs.Items[i].OwnerReferences[0]; o.Kind != "AcceleratorPartitionPolicy" || o.UID != acpp.UID {
				t.Fatalf("reboot job owner = %+v, want owning ACPP", o)
			}
		}
	}
	if !found {
		t.Fatalf("reboot job not created: %+v", jobs.Items)
	}
	if got := migPhaseOf(t, c, acpp.Name); got != npuv1alpha1.MigPhaseRebootWaiting {
		t.Fatalf("journal migPhase = %q, want RebootWaiting", got)
	}

	// 재진입은 Job 을 새로 만들지 않는다(멱등) — 재부팅을 두 번 쏘면 노드가 왕복한다.
	if _, _, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.List(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("reboot job not idempotent: %d jobs", len(jobs.Items))
	}
}

func TestEnsureMigModeEnabledDoneWhenAlreadyEnabled(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false)
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeEnabled, ModePending: migModeEnabled}}

	done, _, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatalf("already-enabled must be done immediately")
	}
	// 이미 Enabled 인 노드는 건드리지 않는다 — 기존 라이브 경로(v0.5.66) 무회귀.
	if nodeCordoned(t, c) {
		t.Fatalf("must not cordon when nothing to do")
	}
	if got := migPhaseOf(t, c, acpp.Name); got != "" {
		t.Fatalf("journal written for no-op: %q", got)
	}
}

func TestEnsureMigModeEnabledBlocksWhenGPUPodRemains(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false, migModePod("tenant-job", true))
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ModePending: migModeDisabled}}

	done, ts, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatalf("must not proceed while GPU pod remains")
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseWaitingForDrain {
		t.Fatalf("phase = %q, want WaitingForWorkloadsToDrain", ts.Phase)
	}
	if got := migPhaseOf(t, c, acpp.Name); got != npuv1alpha1.MigPhaseQuiescing {
		t.Fatalf("journal migPhase = %q, want Quiescing", got)
	}
}

// TestEnsureMigModeEnabledFailsClosedOnUnobservedMode 는 관측이 불확실하면 cordon·전환을 시작조차
// 하지 않음을 검증한다(모르는 상태로 노드를 재부팅시키지 않는다).
func TestEnsureMigModeEnabledFailsClosedOnUnobservedMode(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false)
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ObsError: "observe job failed"}}

	done, ts, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatalf("unobservable mode must not be done")
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseWaitingForDrain {
		t.Fatalf("phase = %q", ts.Phase)
	}
	if reason := condReason(ts, npuv1alpha1.ACPPCondApplied); reason != npuv1alpha1.ReasonObservationUnavailable {
		t.Fatalf("reason = %q, want ObservationUnavailable", reason)
	}
	if nodeCordoned(t, c) {
		t.Fatalf("must not cordon on unobservable mode")
	}
}

// TestEnsureMigModeEnabledRestoresNodeOnEnableFailure 는 -mig 1 실패 후 노드를 다시 schedulable 로
// 되돌림을 검증한다 — cordon 된 채 방치된 GPU 노드는 그 자체로 장애다.
func TestEnsureMigModeEnabledRestoresNodeOnEnableFailure(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false)
	r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return failingExec{} }
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ModePending: migModeDisabled}}

	done, ts, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
	if err == nil {
		t.Fatalf("enable failure must propagate")
	}
	if done {
		t.Fatalf("failed enable must not be done")
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseFailed {
		t.Fatalf("phase = %q, want Failed", ts.Phase)
	}
	if nodeCordoned(t, c) {
		t.Fatalf("node left cordoned after failed mode enable")
	}
}

// TestEnsureMigModeEnabledKeepsOperatorCordonOnFailure 는 운영자가 먼저 cordon 해 둔 노드는
// 실패 복원에서 uncordon 하지 않음을 검증한다(정비 중인 노드를 정책이 되살리면 안 된다).
func TestEnsureMigModeEnabledKeepsOperatorCordonOnFailure(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, true)
	r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return failingExec{} }
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ModePending: migModeDisabled}}

	if _, _, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"}); err == nil {
		t.Fatalf("enable failure must propagate")
	}
	if !nodeCordoned(t, c) {
		t.Fatalf("pre-existing operator cordon must be preserved")
	}
}

// condReason 은 ts 의 지정 condition reason 을 반환한다(테스트 편의).
func condReason(ts npuv1alpha1.TargetStatus, ctype string) string {
	for _, c := range ts.Conditions {
		if c.Type == ctype {
			return c.Reason
		}
	}
	return ""
}

// recordingExec 는 실행된 step 을 모아 둔다 — GI 생성(-cgi)이 시도됐는지 판별용.
type recordingExec struct{ steps []nvidia.CommandStep }

func (e *recordingExec) Run(_ context.Context, _, _, _ string, steps []nvidia.CommandStep) error {
	e.steps = append(e.steps, steps...)
	return nil
}

func (e *recordingExec) ran(token string) bool {
	for _, s := range e.steps {
		for _, a := range s.Argv {
			if a == token {
				return true
			}
		}
	}
	return false
}

// stubObserver 는 테스트가 들고 있는 Observation 을 그대로 돌려준다 — 포인터라 패스 사이에
// 값을 바꿔 "재부팅 뒤 mode 가 켜졌다" 같은 관측 변화를 재현할 수 있다.
type stubObserver struct{ obs *nvidia.Observation }

func (o stubObserver) Observe(_ context.Context, _ string, pcis []string) ([]nvidia.Observation, error) {
	out := make([]nvidia.Observation, 0, len(pcis))
	for _, p := range pcis {
		cur := *o.obs
		cur.PCI = p
		out = append(out, cur)
	}
	return out, nil
}

// migModeNDR 은 detector 가 보고하는 수준의 nvidia NDR 이다(MIG 필드는 관측이 진실 소스).
func migModeNDR() *npuv1alpha1.NodeDeviceReport {
	return &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "worker1"},
		Status: npuv1alpha1.NodeDeviceReportStatus{Devices: []npuv1alpha1.DeviceEntry{{
			Vendor: "nvidia", Model: "generic", PCIeAddress: migModePCI, DriverVersion: "580.65.06",
		}}},
	}
}

// modeDisabledObs / modeEnabledObs 는 전환 전후 관측이다.
func modeDisabledObs() nvidia.Observation {
	return nvidia.Observation{ModeCurrent: migModeDisabled, ModePending: migModeDisabled, Geometry: "disabled"}
}

func modeEnabledObs() nvidia.Observation {
	return nvidia.Observation{ModeCurrent: migModeEnabled, ModePending: migModeEnabled, LgipOutput: lgipA30Test}
}

// TestUnsupportedProfileFailsValidationAfterModeEnableNotAtApply 는 mode 가 꺼져 있어 profile
// 목록을 못 볼 때의 Validate 완화가 영구 우회가 아님을 고정한다: mode enable 이 끝나 profile 이
// 관측되는 순간 재검증이 걸리고, 미지원 profile 은 -cgi 를 쏘기 전에 거절돼야 한다.
func TestUnsupportedProfileFailsValidationAfterModeEnableNotAtApply(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	acpp.Spec.Layout = []npuv1alpha1.PartitionLayout{{Profile: "7g.40gb", CountPerDevice: 1}} // A30 미지원(형식은 유효)
	r, c := migModeFixture(t, acpp, false, migModeNDR())
	exec := &recordingExec{}
	r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return exec }
	obs := modeDisabledObs()
	r.NvidiaObserverFactory = func(client.Client) nvidia.Observer { return stubObserver{&obs} }

	// 1) mode Disabled — profile 목록을 볼 수 없으니 형식 검증만 하고 mode enable 로 진행한다.
	ts, err := r.runTarget(acpp, nvidia.New(c).WithExecutor(exec), migModeTarget(ctx), &evidenceCtx{})
	if err != nil {
		t.Fatal(err)
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseApplying {
		t.Fatalf("phase = %q, want Applying (mode enable issued)", ts.Phase)
	}
	if !exec.ran("-mig") {
		t.Fatalf("mode enable was not issued: %+v", exec.steps)
	}

	// 2) 재부팅 후 mode Enabled — 이제 profile 이 보이므로 재검증에서 걸려야 한다.
	obs = modeEnabledObs()
	ts, err = r.runTarget(acpp, nvidia.New(c).WithExecutor(exec), migModeTarget(ctx), &evidenceCtx{})
	if err != nil {
		t.Fatal(err)
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseFailed {
		t.Fatalf("phase = %q, want Failed", ts.Phase)
	}
	if reason := condReason(ts, npuv1alpha1.ACPPCondValidated); reason != "ValidationFailed" {
		t.Fatalf("reason = %q, want ValidationFailed (not an apply failure)", reason)
	}
	if exec.ran("-cgi") {
		t.Fatalf("GI creation must never be attempted for an unsupported profile: %+v", exec.steps)
	}
	if nodeCordoned(t, c) {
		t.Fatalf("node left cordoned after validation rejection")
	}
}

// runModePass 는 production 과 같은 방식으로 reconcile 한 패스를 돈다 — backend 는 reconcile 마다
// 새로 만들어진다(backendFor 와 동일).
func runModePass(t *testing.T, r *AcceleratorPartitionPolicyReconciler, c client.Client,
	acpp *npuv1alpha1.AcceleratorPartitionPolicy, exec nvidia.Executor) npuv1alpha1.TargetStatus {
	t.Helper()
	ts, err := r.runTarget(acpp, nvidia.New(c).WithExecutor(exec).WithVerifier(r.Verifier), migModeTarget(context.Background()), &evidenceCtx{})
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// TestModeEnableFullCycleReachesReadyAndUncordonsNode 는 Disabled → -mig 1 → pending → 재부팅 →
// Enabled → GI 생성 → Ready 전 사이클을 돈다. (1) 성공 종점에서 정책이 잠근 노드가 반드시
// schedulable 로 복원돼야 한다 — computeBaseline 이 ApplyRecord 를 통째로 갈아치우며
// cordonedByPolicy 를 흘리면 노드가 영구 cordon 으로 남고, shouldReverify 가 재진입까지 막는다.
// (2) mode 전환 저널(RebootWaiting)을 든 채 재진입해도 Applying→Ready 로 수렴해야 한다 — 관측을
// 뒤집으면 실제로 도달하는 창이다.
func TestModeEnableFullCycleReachesReadyAndUncordonsNode(t *testing.T) {
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false, migModeNDR())
	exec := &recordingExec{}
	r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return exec }
	r.Verifier = allocVerifier{alloc: map[string]int32{"nvidia.com/mig-1g.6gb": 4, nvidiaGPUResource: 1}}
	obs := modeDisabledObs()
	r.NvidiaObserverFactory = func(client.Client) nvidia.Observer { return stubObserver{&obs} }

	// 1패스: mode Disabled → quiesce + -mig 1.
	if ts := runModePass(t, r, c, acpp, exec); ts.Phase != npuv1alpha1.ACPPPhaseApplying {
		t.Fatalf("pass1 phase = %q, want Applying", ts.Phase)
	}
	if !nodeCordoned(t, c) {
		t.Fatalf("pass1: node must be cordoned")
	}
	if !exec.ran("-mig") {
		t.Fatalf("pass1: mode enable not issued")
	}

	// 2패스: pending 이 Enabled 로 뜸 → 재부팅 요청.
	obs.ModePending = migModeEnabled
	if ts := runModePass(t, r, c, acpp, exec); ts.Phase != npuv1alpha1.ACPPPhaseApplying {
		t.Fatalf("pass2 phase = %q, want Applying", ts.Phase)
	}
	if got := migPhaseOf(t, c, acpp.Name); got != npuv1alpha1.MigPhaseRebootWaiting {
		t.Fatalf("pass2 journal = %q, want RebootWaiting", got)
	}

	// 3패스: 재부팅 완료 — mode Enabled, GI 는 아직 없음. 여기서 GI 를 만들고 Ready 로 간다.
	obs = modeEnabledObs()
	ts := runModePass(t, r, c, acpp, exec)
	if ts.Phase != npuv1alpha1.ACPPPhaseReady {
		t.Fatalf("pass3 phase = %q, want Ready", ts.Phase)
	}
	if !exec.ran("-cgi") {
		t.Fatalf("pass3: GI was never created: %+v", exec.steps)
	}
	if nodeCordoned(t, c) {
		t.Fatalf("node left cordoned after reaching Ready (cordonedByPolicy lost)")
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.Get(context.Background(), types.NamespacedName{Name: acpp.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ApplyRecords[0].MigPhase != npuv1alpha1.MigPhaseReady {
		t.Fatalf("journal = %q, want Ready", got.Status.ApplyRecords[0].MigPhase)
	}
	// 전환이 끝났으니 재부팅 예산도 원복돼야 한다(다음 전환이 1회 만에 상한에 걸리지 않도록).
	if got.Status.ApplyRecords[0].RebootAttempts != 0 {
		t.Fatalf("RebootAttempts = %d, want 0 after mode enable completed", got.Status.ApplyRecords[0].RebootAttempts)
	}
}

// TestRebootAttemptsAreCappedAndNodeRestored 는 재부팅해도 mode 가 확정되지 않는 노드를 무한히
// 재부팅시키지 않음을 검증한다. 재부팅 Job 은 노드와 함께 죽어 TTL 로 GC 되므로, 상한이 없으면
// "Job 없음 → 생성 → 재부팅" 이 TTL 주기로 영원히 반복된다.
func TestRebootAttemptsAreCappedAndNodeRestored(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false, migModeNDR())
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ModePending: migModeEnabled}}
	// 정책이 cordon 한 상태를 만든다(복원 대상임을 표시).
	if err := r.journalQuiescing(ctx, acpp, "worker1", devs); err != nil {
		t.Fatal(err)
	}
	if err := partition.NvidiaQuiescer(c).Cordon(ctx, "worker1"); err != nil {
		t.Fatal(err)
	}

	// 상한까지는 매번 재부팅 Job 을 만든다(TTL GC 로 사라진 상황을 Job 삭제로 재현).
	for i := int32(1); i <= maxMigRebootAttempts; i++ {
		done, ts, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
		if err != nil || done || ts.Phase != npuv1alpha1.ACPPPhaseApplying {
			t.Fatalf("attempt %d: done=%v phase=%q err=%v", i, done, ts.Phase, err)
		}
		var got npuv1alpha1.AcceleratorPartitionPolicy
		if err := c.Get(ctx, types.NamespacedName{Name: acpp.Name}, &got); err != nil {
			t.Fatal(err)
		}
		if got.Status.ApplyRecords[0].RebootAttempts != i {
			t.Fatalf("RebootAttempts = %d, want %d", got.Status.ApplyRecords[0].RebootAttempts, i)
		}
		var jobs batchv1.JobList
		if err := c.List(ctx, &jobs); err != nil {
			t.Fatal(err)
		}
		if len(jobs.Items) != 1 {
			t.Fatalf("attempt %d: want exactly 1 reboot job, got %d", i, len(jobs.Items))
		}
		// 노드 재부팅으로 Job 이 Failed → TTL GC 된 상태.
		if err := c.Delete(ctx, &jobs.Items[0]); err != nil {
			t.Fatal(err)
		}
	}

	// 상한 초과: Job 을 더 만들지 않고 Failed + 노드 복원.
	done, ts, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"})
	if err != nil {
		t.Fatalf("cap exhaustion must be terminal, not an error: %v", err)
	}
	if done {
		t.Fatalf("must not report done")
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseFailed {
		t.Fatalf("phase = %q, want Failed", ts.Phase)
	}
	var jobs batchv1.JobList
	if err := c.List(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("no further reboot job may be created past the cap: %+v", jobs.Items)
	}
	if nodeCordoned(t, c) {
		t.Fatalf("node left cordoned after giving up on mode enable")
	}
}

// TestAcppRebootJobNameDoesNotCollideWithDriverUpgrade 는 ACPP 재부팅 Job 이 드라이버 업그레이드
// 경로와 이름을 공유하지 않음을 고정한다 — 같으면 서로의 Job 을 자기 것으로 오인한다.
func TestAcppRebootJobNameDoesNotCollideWithDriverUpgrade(t *testing.T) {
	if naming.AcppRebootJobName("worker1") == naming.RebootJobName("worker1") {
		t.Fatalf("ACPP reboot job name must differ from the driver-upgrade reboot job name")
	}
	ctx := context.Background()
	acpp := migModeACPP()
	// 드라이버 업그레이드가 먼저 만들어 둔 Job(소유자 다름)이 이미 있는 상황.
	foreign := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: naming.RebootJobName("worker1"), Namespace: driverjob.Namespace,
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "npu.ai/v1alpha1", Kind: "DriverInstallPolicy", Name: "dip", UID: "dip-uid"}},
	}}
	r, c := migModeFixture(t, acpp, true, foreign)
	devs := []nvidia.MigDevice{{PCI: migModePCI, ModeCurrent: migModeDisabled, ModePending: migModeEnabled}}

	if _, _, err := r.ensureMigModeEnabled(acpp, migModeTarget(ctx), devs, npuv1alpha1.TargetStatus{NodeName: "worker1"}); err != nil {
		t.Fatal(err)
	}
	var own batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Name: naming.AcppRebootJobName("worker1"), Namespace: driverjob.Namespace}, &own); err != nil {
		t.Fatalf("ACPP must create its own reboot job instead of adopting a foreign one: %v", err)
	}
	if own.OwnerReferences[0].Kind != "AcceleratorPartitionPolicy" {
		t.Fatalf("owner = %+v", own.OwnerReferences[0])
	}
}

// TestModeEnableCompletionClearsTransitionJournal 은 전환이 끝나면 저널에서 mode-전환 phase 가
// 사라짐을 고정한다. 남겨두면 이후 분류(no-diff)가 이미 끝난 전환을 진행 중으로 읽어, geometry 가
// 우연히 일치할 때 apply 경로로 내려갔다가 MigUnsafe → WaitingForDrain 으로 되돌아오기를 30초마다
// 반복해 수렴도 실패도 없이 cordon 된 채 남는다. 전환 뒤의 분류는 apply 경로가 소유해야 하며,
// 우리가 만들지 않은 geometry 는 terminal 실패(ExistingMigConfiguration)로 떨어져야 한다. 저널은
// 손으로 심지 않고 실제 전환(Disabled → -mig 1 → pending → 재부팅 요청)으로 만든다.
func TestModeEnableCompletionClearsTransitionJournal(t *testing.T) {
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false, migModeNDR())
	exec := &recordingExec{}
	r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return exec }
	obs := modeDisabledObs()
	r.NvidiaObserverFactory = func(client.Client) nvidia.Observer { return stubObserver{&obs} }

	runModePass(t, r, c, acpp, exec) // Disabled → -mig 1
	obs.ModePending = migModeEnabled
	runModePass(t, r, c, acpp, exec) // pending → 재부팅 요청
	if got := migPhaseOf(t, c, acpp.Name); got != npuv1alpha1.MigPhaseRebootWaiting {
		t.Fatalf("journal = %q, want RebootWaiting", got)
	}

	// 재부팅 뒤 mode 는 켜졌는데 GI 까지 이미 요청과 똑같이 존재 → diff 없음 + 전환 저널.
	obs = nvidia.Observation{ModeCurrent: migModeEnabled, ModePending: migModeEnabled,
		Geometry: "1g.6gb x4", LgipOutput: lgipA30Test}
	ts := runModePass(t, r, c, acpp, exec)

	if got := migPhaseOf(t, c, acpp.Name); isMigModeEnablePhase(got) {
		t.Fatalf("journal = %q — 완료된 전환의 phase 가 남으면 이후 분류가 진행 중으로 오인한다", got)
	}
	if ts.Phase == npuv1alpha1.ACPPPhaseWaitingForDrain {
		t.Fatalf("완료된 전환이 WaitingForDrain 루프로 되돌아갔다(수렴도 실패도 없는 상태): %+v", ts.Conditions)
	}
	if reason := condReason(ts, npuv1alpha1.ACPPCondValidated); reason != npuv1alpha1.ReasonExistingMigConfiguration {
		t.Fatalf("우리가 만들지 않은 geometry 는 정직한 terminal 실패여야 한다: phase=%q reason=%q", ts.Phase, reason)
	}
}

// TestTerminalPhaseRestoresCordonedNodeAndCancelsReboot 은 mode enable 이 잠근 노드가 terminal
// 종점에서 반드시 풀리고, 예약해 둔 재부팅도 함께 취소됨을 고정한다. (1) 재시도해도 결과가 같은
// terminal 갈래에서 복원하지 않으면 노드는 사람이 알아챌 때까지 영구히 unschedulable 로 남는다 —
// 갈래마다 복원을 흩뿌리면 새 갈래가 생길 때 또 새므로 Reconcile 의 단일 choke-point 로 닫아야
// 한다. (2) uncordon 만 하고 재부팅 Job 을 남기면 방금 스케줄된 워크로드를 얹은 채 노드가 내려간다.
// 여기서 쓰는 terminal 갈래는 owner-lock 상실(TargetConflict) — 개별 복원 호출이 없던 자리다.
func TestTerminalPhaseRestoresCordonedNodeAndCancelsReboot(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false, migModeNDR())
	obs := modeEnabledObs()
	r.NvidiaObserverFactory = func(client.Client) nvidia.Observer { return stubObserver{&obs} }

	// 정책이 mode enable 을 위해 잠근 노드 + 예약된 재부팅 Job.
	if err := r.journalQuiescing(ctx, acpp, "worker1", []nvidia.MigDevice{{PCI: migModePCI}}); err != nil {
		t.Fatal(err)
	}
	if err := partition.NvidiaQuiescer(c).Cordon(ctx, "worker1"); err != nil {
		t.Fatal(err)
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: naming.AcppRebootJobName("worker1"), Namespace: driverjob.Namespace,
	}}
	if err := c.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	// 그 사이 다른 ACPP 가 노드 소유를 가져갔다 → TargetConflict(terminal).
	var n corev1.Node
	if err := c.Get(ctx, types.NamespacedName{Name: "worker1"}, &n); err != nil {
		t.Fatal(err)
	}
	n.Annotations[migOwnerAnnotation] = "someone-else-uid"
	if err := c.Update(ctx, &n); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Reconcile(ctx, reconcileReq(acpp.Name)); err != nil {
		t.Fatalf("terminal conflict must not error out: %v", err)
	}

	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: acpp.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != npuv1alpha1.ACPPPhaseFailed {
		t.Fatalf("phase = %q, want Failed(TargetConflict)", got.Status.Phase)
	}
	if nodeCordoned(t, c) {
		t.Fatalf("terminal 종점에서 정책이 잠근 노드가 영구 cordon 으로 남았다")
	}
	var left batchv1.Job
	err := c.Get(ctx, types.NamespacedName{Name: naming.AcppRebootJobName("worker1"), Namespace: driverjob.Namespace}, &left)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("uncordon 과 함께 예약된 재부팅이 취소돼야 한다, err=%v", err)
	}
}

// TestRestoreSchedulableKeepsOperatorCordonButCancelsReboot 은 운영자가 미리 cordon 해 둔 노드
// (CordonedByPolicy=false)는 계속 정비 중으로 두되, 우리가 예약한 재부팅은 거두는지 본다 —
// 정비 중인 노드를 정책이 되살리면 안 되고, 포기한 재부팅을 남겨도 안 된다.
func TestRestoreSchedulableKeepsOperatorCordonButCancelsReboot(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, true) // 운영자가 이미 cordon.
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: naming.AcppRebootJobName("worker1"), Namespace: driverjob.Namespace,
	}}
	if err := c.Create(ctx, job); err != nil {
		t.Fatal(err)
	}

	if err := r.restoreSchedulable(ctx, acpp, "worker1"); err != nil {
		t.Fatal(err)
	}
	if !nodeCordoned(t, c) {
		t.Fatalf("운영자 cordon 을 정책이 풀면 안 된다")
	}
	var left batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &left); !apierrors.IsNotFound(err) {
		t.Fatalf("포기한 재부팅 Job 이 남았다, err=%v", err)
	}
}

// TestApplyFailureRollbackKeepsCordonForRetry 는 재시도가 예약된 rollback 에서 노드를 풀지 않음을
// 고정한다. GI apply 는 cordon 을 전제로 하고(변경시-전제 NodeCordoned) 스스로 cordon 하지 않으므로,
// 여기서 uncordon 하면 다음 reconcile 이 NodeNotCordoned 로 영구 차단되고 CordonedByPolicy 까지
// 지워져 나중에 사람이 cordon 해 성공시켜도 되돌릴 주체가 없다.
func TestApplyFailureRollbackKeepsCordonForRetry(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false, migModeNDR())
	obs := modeEnabledObs() // mode 는 이미 켜졌고 GI 는 아직 없다 → 정상 apply 경로.
	r.NvidiaObserverFactory = func(client.Client) nvidia.Observer { return stubObserver{&obs} }
	r.NvidiaExecFactory = func(client.Client) nvidia.Executor { return failingExec{} }

	// mode enable 이 잠근 노드.
	if err := r.journalQuiescing(ctx, acpp, "worker1", []nvidia.MigDevice{{PCI: migModePCI}}); err != nil {
		t.Fatal(err)
	}
	if err := partition.NvidiaQuiescer(c).Cordon(ctx, "worker1"); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Reconcile(ctx, reconcileReq(acpp.Name)); err == nil {
		t.Fatal("apply 실패는 requeue 되도록 err 로 전파돼야 한다")
	}
	if !nodeCordoned(t, c) {
		t.Fatalf("재시도가 예약된 rollback 에서 cordon 을 풀면 다음 apply 가 NodeNotCordoned 로 막힌다")
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: acpp.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Status.ApplyRecords[0].CordonedByPolicy {
		t.Fatalf("cordon 소유권이 지워지면 성공 이후에도 노드를 되돌릴 주체가 없다")
	}
}

var _ = Describe("syncMigActiveLabel", func() {
	It("adds the label when active and removes it when inactive, idempotently", func() {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "sync-mig-active-node"}}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient}

		Expect(r.syncMigActiveLabel(ctx, node.Name, true)).To(Succeed())
		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &got)).To(Succeed())
		Expect(got.Labels[nvidia.MigActiveNodeLabel]).To(Equal("true"))

		// 멱등 — 이미 true 인 상태에 다시 true 를 걸어도 에러 없음, patch 재시도 없이 조용히 통과.
		Expect(r.syncMigActiveLabel(ctx, node.Name, true)).To(Succeed())

		Expect(r.syncMigActiveLabel(ctx, node.Name, false)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &got)).To(Succeed())
		_, present := got.Labels[nvidia.MigActiveNodeLabel]
		Expect(present).To(BeFalse())
	})

	// 증명: 조각이 0개여도 **MIG 모드가 켜진 GPU 가 남아 있으면** 라벨을 떼지 않는다. 그 GPU 는
	// `nvidia-smi -L` 에는 보이지만 CUDA 가 장치로 세지 않는다 — 라벨을 떼면 flat
	// device-plugin(전략 none)이 담당해 통짜 GPU 로 광고하고, 배정된 파드는 CUDA 초기화에서 죽는다.
	// 깨는 뮤테이션: migModeFullyRestored 가 항상 true 를 돌려주면 라벨이 떨어져 실패한다.
	It("keeps the label while a GPU still has MIG mode enabled", func() {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ghost-gpu-node",
			Labels: map[string]string{nvidia.MigActiveNodeLabel: "true"}}}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: node.Name},
			Spec: npuv1alpha1.NodeDeviceReportSpec{NodeName: node.Name}}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
		ndr.Status.Devices = []npuv1alpha1.DeviceEntry{
			// A30: 모드는 켜졌는데 조각이 없다 — 바로 그 유령이다.
			{Vendor: "nvidia", Count: 1, PCIeAddress: "0000:41:00.0",
				MigModeCurrent: "Enabled", MigModePending: "Enabled", MigCurrentGeometry: "disabled"},
			// A2: MIG 를 지원하지 않는다.
			{Vendor: "nvidia", Count: 1, PCIeAddress: "0000:81:00.0", MigModeCurrent: "NA"},
		}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, ndr) })

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient}
		Expect(r.syncMigActiveLabel(ctx, node.Name, false)).To(Succeed())

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &got)).To(Succeed())
		Expect(got.Labels).To(HaveKeyWithValue(nvidia.MigActiveNodeLabel, "true"),
			"모드가 켜진 GPU 가 남았는데 라벨을 뗐다 — flat plugin 이 유령 GPU 를 광고한다")

		// 모드까지 꺼지면 그때는 뗀다.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &corev1.Node{})).To(Succeed())
		var fresh npuv1alpha1.NodeDeviceReport
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &fresh)).To(Succeed())
		fresh.Status.Devices[0].MigModeCurrent = "Disabled"
		fresh.Status.Devices[0].MigModePending = "Disabled"
		Expect(k8sClient.Status().Update(ctx, &fresh)).To(Succeed())

		Expect(r.syncMigActiveLabel(ctx, node.Name, false)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &got)).To(Succeed())
		Expect(got.Labels).NotTo(HaveKey(nvidia.MigActiveNodeLabel),
			"모드까지 꺼졌는데 라벨이 남으면 이 노드는 영영 공유 모드를 못 쓴다")
	})

	// 증명: 관측이 실패한 보고서는 "꺼졌다" 로 읽지 않는다(fail-closed).
	// 깨는 뮤테이션: migModeFullyRestored 의 MigObservationError 분기를 지우면 라벨이 떨어져 실패한다.
	It("keeps the label when the report says the mig state could not be observed", func() {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "unobservable-mig-node",
			Labels: map[string]string{nvidia.MigActiveNodeLabel: "true"}}}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: node.Name},
			Spec: npuv1alpha1.NodeDeviceReportSpec{NodeName: node.Name}}
		Expect(k8sClient.Create(ctx, ndr)).To(Succeed())
		ndr.Status.Devices = []npuv1alpha1.DeviceEntry{{
			Vendor: "nvidia", Count: 1, PCIeAddress: "0000:41:00.0",
			MigModeCurrent: "Unknown", MigObservationError: "mode query failed: exit status 9",
		}}
		Expect(k8sClient.Status().Update(ctx, ndr)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, ndr) })

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient}
		Expect(r.syncMigActiveLabel(ctx, node.Name, false)).To(Succeed())
		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &got)).To(Succeed())
		Expect(got.Labels).To(HaveKeyWithValue(nvidia.MigActiveNodeLabel, "true"))
	})
})

var _ = Describe("reboot branch cordons before requesting a reboot", func() {
	It("cordons the node and journals ownership before setting MigPhaseRebootRequested", func() {
		GinkgoT().Setenv("HOST_EXEC_IMAGE", "harbor.local/kcloud/kcloud-host-exec:v1") // 재부팅 Job 이 쓰는 nsenter 이미지
		sel := map[string]string{"kcloud.ai/reboot-cordon-test": "true"}
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "reboot-cordon-node", Labels: sel}}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })
		// ensureMigModeEnabled 는 노드당 재부팅 Job 을 만든다 — 다른 spec 의 "전체 클러스터 node-reboot
		// Job 개수" 어서션과 격리하려면 이 spec 이 만든 Job 도 정리해야 한다. envtest 에는 GC 컨트롤러가
		// 없어 PropagationPolicy 없는 맨 Delete 는 orphan finalizer 를 영원히 못 걷어 Job 이 안 지워진다
		// (Terminating 에 stuck) — 프로덕션이 이미 이 문제를 피해간 deleteAcppRebootJob(Background 전파)
		// 을 그대로 재사용한다.
		cleanupR := &AcceleratorPartitionPolicyReconciler{Client: k8sClient}
		DeferCleanup(func() {
			Expect(cleanupR.deleteAcppRebootJob(ctx, node.Name)).To(Succeed())
			var job batchv1.Job
			err := k8sClient.Get(ctx, types.NamespacedName{Name: naming.AcppRebootJobName(node.Name), Namespace: driverjob.Namespace}, &job)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cleanup 후에도 재부팅 Job 이 남아 있으면 안 된다")
		})

		acpp := mkNvidiaACPP("reboot-cordon-acpp", sel)
		Expect(k8sClient.Create(ctx, acpp)).To(Succeed())
		DeferCleanup(func() { deleteACPPAndWait(acpp.Name) })

		r := &AcceleratorPartitionPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		// devs 는 NeedsReboot(devs)==true 를 만드는 pending=Enabled, current=Disabled 조합.
		devs := []nvidia.MigDevice{{PCI: "0000:aa:00.0", ModeCurrent: "Disabled", ModePending: "Enabled"}}
		t := partition.Target{Ctx: ctx, NodeName: node.Name}
		ts := npuv1alpha1.TargetStatus{NodeName: node.Name}

		_, _, err := r.ensureMigModeEnabled(acpp, t, devs, ts)
		Expect(err).NotTo(HaveOccurred())

		var gotNode corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &gotNode)).To(Succeed())
		Expect(gotNode.Spec.Unschedulable).To(BeTrue(), "reboot 요청 전에 노드가 cordon 돼 있어야 한다")

		var gotAcpp npuv1alpha1.AcceleratorPartitionPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "reboot-cordon-acpp"}, &gotAcpp)).To(Succeed())
		rec := getApplyRecord(&gotAcpp, node.Name)
		Expect(rec.OwnerUID).To(Equal(string(gotAcpp.UID)), "reboot 갈래도 소유권을 저널해야 한다")
		Expect(rec.CordonedByPolicy).To(BeTrue())
	})
})

// TestRunTarget_RefusesOnDRAOwnedNode 는 광고 주체가 DRA 로 넘어간 노드에서 ACPP 가 파티션을
// 건드리지 않는 것을 단정한다. NVIDIA DRA 드라이버는 claim 시점에 MIG 를 구성하므로
// (createMigDevice), 같은 GPU 를 둘이 재구성하면 재구성 중인 장치 위에 워크로드가 올라간다.
func TestRunTarget_RefusesOnDRAOwnedNode(t *testing.T) {
	ctx := context.Background()
	acpp := migModeACPP()
	r, c := migModeFixture(t, acpp, false)

	var node corev1.Node
	if err := c.Get(ctx, types.NamespacedName{Name: "worker1"}, &node); err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	node.Labels[npuv1alpha1.DRAOwnedNodeLabel("nvidia")] = labelValueTrue
	if err := c.Update(ctx, &node); err != nil {
		t.Fatalf("라벨 부여 실패: %v", err)
	}

	ts, err := r.runTarget(acpp, nvidia.New(c).WithExecutor(fakeNvidiaExec{}), migModeTarget(ctx), &evidenceCtx{})
	if err != nil {
		t.Fatalf("거절은 오류가 아니라 상태여야 함: %v", err)
	}
	if ts.Phase != npuv1alpha1.ACPPPhaseFailed {
		t.Errorf("phase = %q, want %q", ts.Phase, npuv1alpha1.ACPPPhaseFailed)
	}
	var found bool
	for _, cond := range ts.Conditions {
		if cond.Reason == "NodeOwnedByDRA" && strings.Contains(cond.Message, "advertiseBy") {
			found = true
		}
	}
	if !found {
		t.Errorf("거절 사유가 광고 주체 스위치를 지목하지 않음: %+v", ts.Conditions)
	}
}
