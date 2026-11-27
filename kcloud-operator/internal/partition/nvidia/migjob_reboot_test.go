// ============================================================
// migjob_reboot_test.go: 재부팅에 치인 MIG Job 의 탐지·재생성·상한 테스트
// 상세: mode 복원 재부팅이 GI rollback Job 을 Pending 에 가두면 결정론적 이름 탓에 모든 pass 가
// 같은 죽은 Job 을 기다린다. 여기서 고정: 죽은 Job 식별 / 건강한 Job 유지 / 재생성 상한.
// 생성일: 2026-08-06 | 수정일: 2026-10-11
// ============================================================
package nvidia

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	rebootTestNode = "k8s-worker1"
	bootBefore     = "boot-id-before"
	bootAfter      = "boot-id-after"
)

func rebootTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func rebootTestSteps() []CommandStep {
	return []CommandStep{{Argv: []string{"nvidia-smi", "mig", "-i", "0000:18:00.0", "-dgi"}, Optional: true}}
}

func nodeWithBootID(id string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: rebootTestNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{BootID: id}},
	}
}

// inFlightJob 은 실제 결함과 같은 Job 상태를 재현한다: 파드는 만들어졌지만 실행된 적이 없어
// Succeeded 도 Failed 도 0 이고, Job 은 영원히 그 상태에 머문다.
func inFlightJob(name, cmdHash, stampedBootID, restarts string) *batchv1.Job {
	job := renderMigJob(name, cmdHash, rebootTestNode, rebootTestSteps(), "kcloud-host-exec:v0.1.0")
	job.Annotations[NodeBootIDAnnotation] = stampedBootID
	if restarts != "" {
		job.Annotations[RebootRestartsAnnotation] = restarts
	}
	return job
}

// succeedOnCreate 는 새로 만들어진 Job 을 곧바로 Succeeded 로 기록한다 — 재생성이 일어났는지만
// 보려는 테스트가 wait 의 폴링에 붙잡히지 않게 하는 장치다(실 클러스터의 kubelet 역할).
func succeedOnCreate() interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if job, ok := obj.(*batchv1.Job); ok {
				job.Status.Succeeded = 1
			}
			return c.Create(ctx, obj, opts...)
		},
	}
}

// TestRunRecreatesJobStrandedByReboot 는 결함 3 의 본체다: 노드가 재부팅되어 Job 이 얹혔던 기계가
// 사라졌으면, 다음 pass 는 10분을 기다리는 대신 그 Job 을 버리고 다시 만들어야 한다.
func TestRunRecreatesJobStrandedByReboot(t *testing.T) {
	steps := rebootTestSteps()
	opID, cmdHash := OperationID("uid-1234", 1, rebootTestNode, "rollback", steps)
	stale := inFlightJob(opID, cmdHash, bootBefore, "")

	c := fake.NewClientBuilder().WithScheme(rebootTestScheme(t)).
		WithObjects(nodeWithBootID(bootAfter), stale). // 노드는 이미 다른 부팅 세대다
		WithInterceptorFuncs(succeedOnCreate()).Build()

	e := &JobExecutor{Client: c, Image: "kcloud-host-exec:v0.1.0", PollInterval: time.Millisecond, Timeout: 2 * time.Second}
	if err := e.Run(context.Background(), opID, cmdHash, rebootTestNode, steps); err != nil {
		t.Fatalf("재부팅에 치인 Job 은 재생성으로 복구돼야 한다: %v", err)
	}

	var got batchv1.Job
	if err := c.Get(context.Background(), types.NamespacedName{Name: opID, Namespace: Namespace}, &got); err != nil {
		t.Fatalf("재생성된 Job 이 없다: %v", err)
	}
	if got.Annotations[NodeBootIDAnnotation] != bootAfter {
		t.Errorf("재생성 Job 은 현재 부팅 세대를 새겨야 한다: %q", got.Annotations[NodeBootIDAnnotation])
	}
	if got.Annotations[RebootRestartsAnnotation] != "1" {
		t.Errorf("재생성 횟수가 이어져야 한다: %q", got.Annotations[RebootRestartsAnnotation])
	}
}

// TestRunLeavesHealthyInFlightJobAlone 은 과잉 수정을 잡는 쪽이다. 노드가 그대로면 아무리 오래
// 걸리는 Job 도 삭제·재생성 대상이 아니다 — 특권 Job 을 중복 실행하면 하드웨어를 두 번 만진다.
func TestRunLeavesHealthyInFlightJobAlone(t *testing.T) {
	steps := rebootTestSteps()
	opID, cmdHash := OperationID("uid-1234", 1, rebootTestNode, "rollback", steps)
	running := inFlightJob(opID, cmdHash, bootBefore, "")

	var creates, deletes int
	c := fake.NewClientBuilder().WithScheme(rebootTestScheme(t)).
		WithObjects(nodeWithBootID(bootBefore), running). // 재부팅 없음 — 같은 부팅 세대
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				creates++
				return cl.Create(ctx, obj, opts...)
			},
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				deletes++
				return cl.Delete(ctx, obj, opts...)
			},
		}).Build()

	// 이 Job 은 fake 클러스터에서 영원히 끝나지 않는다 — timeout 을 짧게 두고 "기다렸다" 만 확인한다.
	e := &JobExecutor{Client: c, Image: "kcloud-host-exec:v0.1.0", PollInterval: 5 * time.Millisecond, Timeout: 60 * time.Millisecond}
	err := e.Run(context.Background(), opID, cmdHash, rebootTestNode, steps)
	if err == nil || !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("건강한 in-flight Job 은 완료를 기다려야 한다(timeout 까지): %v", err)
	}
	if creates != 0 || deletes != 0 {
		t.Fatalf("건강한 Job 을 건드렸다: creates=%d deletes=%d", creates, deletes)
	}
	var got batchv1.Job
	if err := c.Get(context.Background(), types.NamespacedName{Name: opID, Namespace: Namespace}, &got); err != nil {
		t.Fatalf("원래 Job 이 사라졌다: %v", err)
	}
	if _, ok := got.Annotations[RebootRestartsAnnotation]; ok {
		t.Errorf("건강한 Job 에 재생성 횟수가 붙었다: %v", got.Annotations)
	}
}

// TestRunBoundsRebootRecreates 는 상한을 고정한다. 노드가 계속 재부팅해도 재생성은 무한하지 않고,
// 상한을 넘으면 하드 에러(terminal)로 올려 운영자를 부른다 — 조용한 무한 대기로 돌아가지 않는다.
func TestRunBoundsRebootRecreates(t *testing.T) {
	steps := rebootTestSteps()
	opID, cmdHash := OperationID("uid-1234", 1, rebootTestNode, "rollback", steps)
	exhausted := inFlightJob(opID, cmdHash, bootBefore, "2") // 재생성 횟수가 이미 상한에 도달한 상태

	var creates, deletes int
	c := fake.NewClientBuilder().WithScheme(rebootTestScheme(t)).
		WithObjects(nodeWithBootID(bootAfter), exhausted).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				creates++
				return cl.Create(ctx, obj, opts...)
			},
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				deletes++
				return cl.Delete(ctx, obj, opts...)
			},
		}).Build()

	e := &JobExecutor{Client: c, Image: "kcloud-host-exec:v0.1.0", PollInterval: time.Millisecond, Timeout: 2 * time.Second}
	err := e.Run(context.Background(), opID, cmdHash, rebootTestNode, steps)
	if err == nil || !strings.Contains(err.Error(), "manual intervention required") {
		t.Fatalf("상한 초과는 terminal 에러여야 한다: %v", err)
	}
	if creates != 0 || deletes != 0 {
		t.Fatalf("상한을 넘었는데 또 다시 만들었다: creates=%d deletes=%d", creates, deletes)
	}
}

// TestWaitBailsWhenNodeRebootsMidWait 는 조기 탈출을 고정한다. 대기 도중 노드가 재부팅되면 남은
// timeout(기본 10분)을 태울 이유가 없다.
func TestWaitBailsWhenNodeRebootsMidWait(t *testing.T) {
	steps := rebootTestSteps()
	opID, cmdHash := OperationID("uid-1234", 1, rebootTestNode, "rollback", steps)

	c := fake.NewClientBuilder().WithScheme(rebootTestScheme(t)).
		WithObjects(nodeWithBootID(bootBefore)).Build()

	e := &JobExecutor{Client: c, Image: "kcloud-host-exec:v0.1.0", PollInterval: 5 * time.Millisecond, Timeout: 10 * time.Second}

	// Job 을 새로 만든 직후(부팅 세대 bootBefore 스탬프) 노드를 재부팅시킨다.
	rebooted := make(chan error, 1)
	go func() {
		for {
			var job batchv1.Job
			if err := c.Get(context.Background(), types.NamespacedName{Name: opID, Namespace: Namespace}, &job); err == nil {
				var n corev1.Node
				if err := c.Get(context.Background(), types.NamespacedName{Name: rebootTestNode}, &n); err != nil {
					rebooted <- err
					return
				}
				n.Status.NodeInfo.BootID = bootAfter
				rebooted <- c.Status().Update(context.Background(), &n)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	start := time.Now()
	err := e.Run(context.Background(), opID, cmdHash, rebootTestNode, steps)
	if uerr := <-rebooted; uerr != nil {
		t.Fatalf("테스트 전제(노드 재부팅) 자체가 실패했다: %v", uerr)
	}
	if err == nil || !strings.Contains(err.Error(), "stranded by node reboot") {
		t.Fatalf("재부팅을 만난 대기는 즉시 빠져나와야 한다: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout 을 다 태웠다(조기 탈출 실패): %s", elapsed)
	}
}
