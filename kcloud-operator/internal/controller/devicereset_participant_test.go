// ============================================================
// devicereset_participant_test.go: DeviceReset participant 의 게이트·Job 생성·결과 판정 시험
// 상세: fake client 로 descriptor 부재·등급 Unsupported·파드 점유 거절과 Job 생성→성공 보고를 본다.
// 생성일: 2026-09-30 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	resource2 "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/operation"
	"kcloud-operator/internal/vendorcap"
)

func deviceResetOp(name, node, vendor string) *npuv1alpha1.AcceleratorOperation {
	return &npuv1alpha1.AcceleratorOperation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kcloud"},
		Spec: npuv1alpha1.AcceleratorOperationSpec{
			Type: string(operation.DeviceReset), NodeName: node, Vendor: vendor, TransactionID: name,
		},
	}
}

// resetDescriptor 는 리셋 명령을 선언한 descriptor 다. 정합 필드를 모두 채운다 — 게이트 1 이
// descriptor.Validate 로 거절된 descriptor 를 건너뛰므로, 정합하지 않은 픽스처는 프로덕션이
// 실행하지 않는 입력이 된다.
func resetDescriptor(name, vendor, product string, cmd []string) *npuv1alpha1.AcceleratorDescriptor {
	return &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor:         vendor,
			Product:        product,
			AllocationUnit: npuv1alpha1.AllocationUnitDevice,
			Identity:       npuv1alpha1.DescriptorIdentity{Source: npuv1alpha1.IdentitySourcePCIAddress},
			DeviceNodes:    []string{"/dev/" + product},
			Backends:       []string{npuv1alpha1.BackendDevicePlugin},
			Cleanup:        npuv1alpha1.DescriptorCleanup{RequiresDeviceReset: true, ResetCommand: cmd},
		},
	}
}

// resetNDR 은 노드가 관측한 카드 목록이다. vendor/model 은 detector 어휘를 그대로 쓴다
// (furiosa RNGD 는 vendor="furiosa", model="rngd").
func resetNDR(node string, cards ...[2]string) *npuv1alpha1.NodeDeviceReport {
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: node},
	}
	for _, c := range cards {
		ndr.Status.Devices = append(ndr.Status.Devices,
			npuv1alpha1.DeviceEntry{Vendor: c[0], Model: c[1], Count: 1})
	}
	return ndr
}

// nvidiaCard·rngdCard 는 NDR 카드 표기를 짧게 쓰기 위한 것이다.
func nvidiaCard(model string) [2]string { return [2]string{"nvidia", model} }
func rngdCard() [2]string               { return [2]string{"furiosa", "rngd"} }

// devicePod 은 extended resource 하나를 limits 로 요구하는 파드다.
func devicePod(ns, name, node, resource string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name:  "main",
				Image: "busybox",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{corev1.ResourceName(resource): resource2.MustParse("1")},
				},
			}},
		},
	}
}

func deviceResetClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(objs...).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
}

func TestDeviceResetParticipant_RefusesWithoutDescriptorCommand(t *testing.T) {
	// 노드 관측은 있으나 그 제품의 descriptor 에 리셋 명령이 없다.
	c := deviceResetClient(resetNDR("n1", nvidiaCard("a30")))
	p := NewDeviceResetParticipant(c, "host-exec:test")
	out, err := p.Apply(context.Background(), deviceResetOp("op1", "n1", "nvidia"))
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "resetCommand") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestDeviceResetParticipant_RefusesUnsupportedGrade(t *testing.T) {
	// tenstorrent 는 레지스트리 CapReset 이 Unsupported 다 — descriptor 가 있어도 실행하지 않는다.
	c := deviceResetClient(resetNDR("n1", [2]string{"tenstorrent", "blackhole-p150"}),
		resetDescriptor("tt", "tenstorrent", "blackhole-p150", []string{"tt-smi", "-r", "0"}))
	out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op2", "n1", "tenstorrent"))
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "Unsupported") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	var jobs batchv1.JobList
	_ = c.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("거절했는데 Job 을 만들었다")
	}
}

func TestDeviceResetParticipant_RefusesWhilePodHoldsDevice(t *testing.T) {
	// 게이트 셋 중 파드 점유만 남겨 둔다 — descriptor 와 등급은 통과 상태다.
	holder := devicePod("team", "trainer", "n1", "nvidia.com/gpu")
	c := deviceResetClient(resetNDR("n1", nvidiaCard("a30")),
		resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"}), holder)
	out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op4", "n1", "nvidia"))
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "team/trainer") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	var jobs batchv1.JobList
	_ = c.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("거절했는데 Job 을 만들었다")
	}
}

func TestDeviceResetParticipant_CreatesJobThenReportsResult(t *testing.T) {
	c := deviceResetClient(resetNDR("n1", nvidiaCard("a30")),
		resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"}))
	p := NewDeviceResetParticipant(c, "host-exec:test")
	op := deviceResetOp("op3", "n1", "nvidia")
	ctx := context.Background()

	out, err := p.Apply(ctx, op)
	if err != nil || out.Event != "" {
		t.Fatalf("첫 Apply 는 Job 을 만들고 진행 중이어야 한다: out=%+v err=%v", out, err)
	}
	name := naming.DeviceResetJobName("n1", "nvidia", op.Spec.TransactionID)
	var job batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: driverjob.Namespace}, &job); err != nil {
		t.Fatalf("Job 이 없다: %v", err)
	}
	if cmd := strings.Join(job.Spec.Template.Spec.Containers[0].Command, " "); !strings.HasSuffix(cmd, "-- nvidia-smi -r") {
		t.Fatalf("command = %q", cmd)
	}
	if out, _ := p.Apply(ctx, op); out.Event != "" {
		t.Fatalf("Job 이 끝나기 전에는 진행 중이어야 한다: %+v", out)
	}
	// Job status 는 subresource 라 Status().Update 로만 남는다(fake client 도 실물과 같다).
	job.Status.Succeeded = 1
	if err := c.Status().Update(ctx, &job); err != nil {
		t.Fatal(err)
	}
	if out, _ := p.Apply(ctx, op); out.Event != operation.EventApplyDone {
		t.Fatalf("성공한 Job 뒤에는 ApplyDone: %+v", out)
	}
}

func TestDeviceResetParticipant_ReportsFailedJob(t *testing.T) {
	// 실패한 Job 을 성공으로 오인하면 리셋되지 않은 장치가 정상으로 보고된다.
	c := deviceResetClient(resetNDR("n2", nvidiaCard("a30")),
		resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"}))
	p := NewDeviceResetParticipant(c, "host-exec:test")
	op := deviceResetOp("op5", "n2", "nvidia")
	ctx := context.Background()

	if out, err := p.Apply(ctx, op); err != nil || out.Event != "" {
		t.Fatalf("첫 Apply 는 진행 중이어야 한다: out=%+v err=%v", out, err)
	}
	var job batchv1.Job
	key := types.NamespacedName{Name: naming.DeviceResetJobName("n2", "nvidia", op.Spec.TransactionID), Namespace: driverjob.Namespace}
	if err := c.Get(ctx, key, &job); err != nil {
		t.Fatalf("Job 이 없다: %v", err)
	}
	job.Status.Failed = 1
	if err := c.Status().Update(ctx, &job); err != nil {
		t.Fatal(err)
	}
	out, _ := p.Apply(ctx, op)
	if out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, job.Name) {
		t.Fatalf("실패한 Job 뒤에는 ApplyFailed + Job 이름: %+v", out)
	}
}

func TestDeviceResetParticipant_RefusesWithoutNodeObservation(t *testing.T) {
	// 노드 관측 기록이 없으면 어느 제품의 명령을 쓸지 알 수 없다 — 추측하지 않고 거절한다.
	c := deviceResetClient(resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"}))
	out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op6", "n9", "nvidia"))
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "n9") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	var jobs batchv1.JobList
	_ = c.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("거절했는데 Job 을 만들었다")
	}
}

func TestDeviceResetParticipant_PicksCommandForObservedProduct(t *testing.T) {
	// 노드에 a2 만 있다 — 이름이 앞서는 a30 descriptor 의 명령을 쓰면 없는 장치를 리셋한다.
	c := deviceResetClient(
		resetNDR("n1", nvidiaCard("a2")),
		resetDescriptor("aaa-a30", "nvidia", "a30", []string{"nvidia-smi", "-r", "-i", "0"}),
		resetDescriptor("zzz-a2", "nvidia", "a2", []string{"nvidia-smi", "-r", "-i", "1"}),
	)
	op := deviceResetOp("op7", "n1", "nvidia")
	ctx := context.Background()
	if out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(ctx, op); err != nil || out.Event != "" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	var job batchv1.Job
	key := types.NamespacedName{Name: naming.DeviceResetJobName("n1", "nvidia", op.Spec.TransactionID), Namespace: driverjob.Namespace}
	if err := c.Get(ctx, key, &job); err != nil {
		t.Fatalf("Job 이 없다: %v", err)
	}
	if cmd := strings.Join(job.Spec.Template.Spec.Containers[0].Command, " "); !strings.HasSuffix(cmd, "-- nvidia-smi -r -i 1") {
		t.Fatalf("관측된 제품(a2)의 명령이어야 한다: %q", cmd)
	}
}

func TestDeviceResetParticipant_RefusesAmbiguousMixedNode(t *testing.T) {
	// 혼재 노드에서 제품마다 다른 명령이 나오면 어느 것도 고르지 않는다.
	c := deviceResetClient(
		resetNDR("n1", nvidiaCard("a30"), nvidiaCard("a2")),
		resetDescriptor("aaa-a30", "nvidia", "a30", []string{"nvidia-smi", "-r", "-i", "0"}),
		resetDescriptor("zzz-a2", "nvidia", "a2", []string{"nvidia-smi", "-r", "-i", "1"}),
	)
	out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op8", "n1", "nvidia"))
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "여러") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	var jobs batchv1.JobList
	_ = c.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("거절했는데 Job 을 만들었다")
	}
}

func TestDeviceResetParticipant_MixedNodeWithOneCommandRuns(t *testing.T) {
	// 제품이 둘이어도 선언된 명령이 같으면 모호하지 않다.
	c := deviceResetClient(
		resetNDR("n1", nvidiaCard("a30"), nvidiaCard("a2")),
		resetDescriptor("aaa-a30", "nvidia", "a30", []string{"nvidia-smi", "-r"}),
		resetDescriptor("zzz-a2", "nvidia", "a2", []string{"nvidia-smi", "-r"}),
	)
	if out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op9", "n1", "nvidia")); err != nil || out.Event != "" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestDeviceResetParticipant_IgnoresJobOfAnotherOperation(t *testing.T) {
	// 앞선 작업이 남긴 성공 Job 을 새 작업의 성공으로 읽으면 리셋 없이 성공이 보고된다.
	c := deviceResetClient(resetNDR("n1", nvidiaCard("a30")),
		resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"}))
	p := NewDeviceResetParticipant(c, "host-exec:test")
	ctx := context.Background()

	first := deviceResetOp("op10", "n1", "nvidia")
	if out, err := p.Apply(ctx, first); err != nil || out.Event != "" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	firstKey := types.NamespacedName{Name: naming.DeviceResetJobName("n1", "nvidia", first.Spec.TransactionID), Namespace: driverjob.Namespace}
	var firstJob batchv1.Job
	if err := c.Get(ctx, firstKey, &firstJob); err != nil {
		t.Fatalf("첫 작업의 Job 이 없다: %v", err)
	}
	firstJob.Status.Succeeded = 1
	if err := c.Status().Update(ctx, &firstJob); err != nil {
		t.Fatal(err)
	}

	second := deviceResetOp("op11", "n1", "nvidia")
	out, err := p.Apply(ctx, second)
	if err != nil || out.Event != "" {
		t.Fatalf("다른 작업의 성공 Job 을 자기 성공으로 읽었다: out=%+v err=%v", out, err)
	}
	secondKey := types.NamespacedName{Name: naming.DeviceResetJobName("n1", "nvidia", second.Spec.TransactionID), Namespace: driverjob.Namespace}
	if secondKey.Name == firstKey.Name {
		t.Fatal("작업마다 Job 이름이 갈려야 한다")
	}
	if err := c.Get(ctx, secondKey, &firstJob); err != nil {
		t.Fatalf("두 번째 작업이 자기 Job 을 만들지 않았다: %v", err)
	}
}

func TestDeviceResetParticipant_RefusesSecondResetAfterJobGone(t *testing.T) {
	// TTL 이 Job 을 걷은 뒤 같은 작업이 다시 들어오면 리셋을 두 번 실행하게 된다.
	c := deviceResetClient(resetNDR("n1", nvidiaCard("a30")),
		resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"}))
	p := NewDeviceResetParticipant(c, "host-exec:test")
	op := deviceResetOp("op12", "n1", "nvidia")
	ctx := context.Background()

	if out, err := p.Apply(ctx, op); err != nil || out.Event != "" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if !operation.HasStep(op.Status.Journal, operation.StepDeviceResetRequested) {
		t.Fatal("Job 을 만들었으면 저널에 그 사실이 남아야 한다")
	}
	name := naming.DeviceResetJobName("n1", "nvidia", op.Spec.TransactionID)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: driverjob.Namespace}}
	if err := c.Delete(ctx, job); err != nil {
		t.Fatal(err)
	}

	out, err := p.Apply(ctx, op)
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "두 번") {
		t.Fatalf("사라진 Job 을 다시 만들지 않고 실패로 끝내야 한다: out=%+v err=%v", out, err)
	}
	var jobs batchv1.JobList
	_ = c.List(ctx, &jobs)
	if len(jobs.Items) != 0 {
		t.Fatalf("Job 을 다시 만들었다: %d 개", len(jobs.Items))
	}
}

func TestDeviceResetParticipant_RollbackKeepsGateRefusal(t *testing.T) {
	// 게이트 거절은 rollback 을 지나 종점에 남는다 — 조정자가 Rollback 반환값으로 Message 를 덮는다.
	c := deviceResetClient(resetNDR("n1", [2]string{"tenstorrent", "blackhole-p150"}),
		resetDescriptor("tt", "tenstorrent", "blackhole-p150", []string{"tt-smi", "-r", "0"}))
	p := NewDeviceResetParticipant(c, "host-exec:test")
	op := deviceResetOp("op13", "n1", "tenstorrent")
	ctx := context.Background()

	out, _ := p.Apply(ctx, op)
	if out.Event != operation.EventApplyFailed {
		t.Fatalf("등급 거절이어야 한다: %+v", out)
	}
	op.Status.Message = out.Message // 조정자 stepApply 가 하는 일

	rolled, err := p.Rollback(ctx, op)
	if err != nil || rolled.Event != operation.EventCompensated {
		t.Fatalf("rolled=%+v err=%v", rolled, err)
	}
	if !strings.Contains(rolled.Message, "Unsupported") {
		t.Fatalf("거절 사유가 종점에서 사라졌다: %q", rolled.Message)
	}
}

func TestDeviceResetParticipant_FuriosaUsesRngdVocabulary(t *testing.T) {
	// 작업 vendor 는 "furiosa" 인데 descriptor 와 점유 판정기는 "rngd" 어휘를 쓴다.
	// 세 조회가 같은 어휘를 쓰지 않으면 descriptor 를 못 찾거나 점유 파드를 놓친다.
	holder := devicePod("team", "rngd-trainer", "n1", "furiosa.ai/rngd")
	c := deviceResetClient(resetNDR("n1", rngdCard()),
		resetDescriptor("rngd", "rngd", "rngd", []string{"furiosa-smi", "reset"}), holder)
	p := NewDeviceResetParticipant(c, "host-exec:test").(*deviceResetParticipant)
	ctx := context.Background()

	target, refusal, err := p.resetTargetFor(ctx, "n1", "furiosa")
	if err != nil || refusal != "" {
		t.Fatalf("furiosa 작업이 rngd descriptor 를 찾아야 한다: refusal=%q err=%v", refusal, err)
	}
	if strings.Join(target.command, " ") != "furiosa-smi reset" {
		t.Fatalf("command = %v", target.command)
	}

	pods, err := p.holdingPods(ctx, "n1", target)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0] != "team/rngd-trainer" {
		t.Fatalf("furiosa.ai/rngd 파드를 봐야 한다: %v", pods)
	}
}

func TestDeviceResetParticipant_RefusesUnsupportedProductOfSplitGradeVendor(t *testing.T) {
	// 한 벤더의 두 제품 등급이 갈린 상황이다: furiosa/warboy 만 Software 로 갈아 끼우고
	// rngd 는 Unsupported 그대로 둔다. 등급을 벤더로 물으면 warboy 의 Software 가
	// rngd 작업을 통과시킨다 — 제품을 아는 자리이므로 그 제품의 등급으로 판정해야 한다.
	t.Cleanup(vendorcap.SetForTest("furiosa", "warboy", vendorcap.CapReset,
		vendorcap.Support{State: vendorcap.StateSoftware, Source: "시험 전용 선언"}))
	c := deviceResetClient(resetNDR("n1", rngdCard()),
		resetDescriptor("rngd", "rngd", "rngd", []string{"furiosa-smi", "reset"}))
	out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op14", "n1", "furiosa"))
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "Unsupported") {
		t.Fatalf("rngd 의 등급으로 거절해야 한다: out=%+v err=%v", out, err)
	}
	var jobs batchv1.JobList
	_ = c.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("거절했는데 Job 을 만들었다")
	}
}

func TestDeviceResetParticipant_RefusesProductlessDescriptorGrade(t *testing.T) {
	// 제품이 비면 어느 제품의 등급을 본 것인지도 알 수 없다 — 되돌릴 수 없는 명령을
	// 그 상태로 실행하지 않는다. 게이트 1 이 정합 오류 descriptor 를 이미 건너뛰므로
	// Apply 로는 닿지 않는 상태이고, 등급 게이트 자체의 판정을 여기서 고정한다.
	if refusal := resetGradeRefusal(resetTarget{capVendor: "nvidia"}); refusal == "" {
		t.Fatal("제품이 빈 대상은 거절해야 한다")
	}
	if refusal := resetGradeRefusal(resetTarget{capVendor: "nvidia", capProduct: "a30"}); refusal != "" {
		t.Fatalf("nvidia 는 CapReset 이 Native 라 통과해야 한다: %q", refusal)
	}
}

func TestDeviceResetParticipant_SkipsRefusedDescriptor(t *testing.T) {
	// 정합이 깨진 descriptor 는 컨트롤러가 전량 거절한다(backend 선언 없음). 그 상태로도
	// resetCommand 는 root 로 실행되던 결함이다 — 거절된 descriptor 만 있으면 거절한다.
	bad := resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"})
	bad.Spec.Backends = nil
	c := deviceResetClient(resetNDR("n1", nvidiaCard("a30")), bad)
	out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op15", "n1", "nvidia"))
	if err != nil || out.Event != operation.EventApplyFailed || !strings.Contains(out.Message, "resetCommand") {
		t.Fatalf("거절된 descriptor 의 명령을 실행하지 않아야 한다: out=%+v err=%v", out, err)
	}
	var jobs batchv1.JobList
	_ = c.List(context.Background(), &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("거절했는데 Job 을 만들었다")
	}
}

func TestDeviceResetParticipant_FindsReportByNodeNameField(t *testing.T) {
	// NodeDeviceReport 의 metadata.name 이 노드 이름과 다를 수 있다 — 같은 패키지의
	// 드라이버 컨트롤러는 spec.nodeName 을 먼저 보고 비었을 때만 metadata.name 으로
	// 폴백한다. 같은 리소스를 두 방식으로 되짚지 않는다.
	ndr := resetNDR("n1", nvidiaCard("a30"))
	ndr.Name = "report-n1"
	c := deviceResetClient(ndr, resetDescriptor("nv", "nvidia", "a30", []string{"nvidia-smi", "-r"}))
	out, err := NewDeviceResetParticipant(c, "host-exec:test").Apply(context.Background(), deviceResetOp("op16", "n1", "nvidia"))
	if err != nil || out.Event != "" {
		t.Fatalf("spec.nodeName 으로 관측 기록을 찾아야 한다: out=%+v err=%v", out, err)
	}
}
