package scheduler

import (
	"context"
	"testing"

	backendlogger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/plugin"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
)

// 시간차로 도착한 Pod가 Assume된 가속기 수량을 소진시키고, 자원이 부족해지면
// Pending으로 남고, 앞선 Pod가 종료되어 수량이 회복되면 다시 배치되는지 확인한다.
func TestSchedulerSequentialPodsExhaustAndRecoverAcceleratorCapacity(t *testing.T) {
	backendlogger.Init(backendlogger.NewDefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cache := utils.NewCache(ctx)
	for _, node := range []*v1.Node{
		newSchedulerFlowNode("gpu-a100", "NVIDIA-A100-SXM4-80GB", "1"),
		newSchedulerFlowNode("gpu-l4", "NVIDIA-L4", "1"),
	} {
		if err := cache.AddNode(node, nil); err != nil {
			t.Fatalf("AddNode(%s) error = %v", node.Name, err)
		}
	}

	podA := newSequentialFlowPod("pod-a")
	podB := newSequentialFlowPod("pod-b")
	podC := newSequentialFlowPod("pod-c")

	fakeClient := kubernetesfake.NewSimpleClientset(podA.DeepCopy(), podB.DeepCopy(), podC.DeepCopy())
	fwk := framework.NewFramework(
		nil,
		[]framework.FilterPlugin{plugin.NewNodeResourcesFit(), plugin.NewAcceleratorModelFit()},
		[]framework.ScorePlugin{plugin.NewWorkloadAwareCost(cache), plugin.NewWorkloadAwarePower(cache)},
		nil,
		plugin.NewDefaultBinder(fakeClient),
	)
	sched := &Scheduler{Cache: cache, hamiClient: plugin.NewHAMiExtenderClient()}
	klogger := klog.FromContext(ctx)

	// T0: 첫 Pod는 합계 점수가 높은 gpu-l4를 선택하고 수량을 1 점유한다.
	resultA, err := sched.schedulePod(ctx, fwk, podA)
	if err != nil {
		t.Fatalf("T0 schedulePod() error = %v", err)
	}
	if resultA.SuggestedHost != "gpu-l4" {
		t.Fatalf("T0 SuggestedHost = %q, want gpu-l4", resultA.SuggestedHost)
	}
	assumedA := podA.DeepCopy()
	if err := sched.assume(klogger, assumedA, resultA.SuggestedHost); err != nil {
		t.Fatalf("T0 assume() error = %v", err)
	}
	assertGPUUsed(t, cache, "T0", map[string]int64{"gpu-l4": 1, "gpu-a100": 0})

	// T1: gpu-l4가 소진되었으므로 남은 수량이 있는 gpu-a100이 선택된다.
	resultB, err := sched.schedulePod(ctx, fwk, podB)
	if err != nil {
		t.Fatalf("T1 schedulePod() error = %v", err)
	}
	if resultB.SuggestedHost != "gpu-a100" {
		t.Fatalf("T1 SuggestedHost = %q, want gpu-a100 (gpu-l4 should be exhausted)",
			resultB.SuggestedHost)
	}
	if !resultB.PluginResultMap["gpu-l4"].IsFiltered {
		t.Error("T1 gpu-l4 was not filtered even though its only GPU is assumed")
	}
	assumedB := podB.DeepCopy()
	if err := sched.assume(klogger, assumedB, resultB.SuggestedHost); err != nil {
		t.Fatalf("T1 assume() error = %v", err)
	}
	assertGPUUsed(t, cache, "T1", map[string]int64{"gpu-l4": 1, "gpu-a100": 1})

	// T2: 두 Node 모두 소진되어 Pending으로 남고 제외 사유가 기록된다.
	pendingResult, err := sched.schedulePod(ctx, fwk, podC)
	if err == nil {
		t.Fatal("T2 schedulePod() succeeded, want failure because every GPU is assumed")
	}
	if pendingResult.FeasibleNodes != 0 {
		t.Errorf("T2 FeasibleNodes = %d, want 0", pendingResult.FeasibleNodes)
	}
	for _, nodeName := range []string{"gpu-a100", "gpu-l4"} {
		pr, ok := pendingResult.PluginResultMap[nodeName]
		if !ok || !pr.IsFiltered {
			t.Errorf("T2 %s was not reported as filtered", nodeName)
			continue
		}
		status := pr.FilteredStatus
		if status.Plugin() != plugin.NodeResourcesFitName {
			t.Errorf("T2 %s filter plugin = %q, want %s",
				nodeName, status.Plugin(), plugin.NodeResourcesFitName)
		}
		if status.Message() == "" {
			t.Errorf("T2 %s filter reason is empty, want an insufficient-resource message", nodeName)
		}
		t.Logf("T2 pending reason: node=%s plugin=%s reason=%s",
			nodeName, status.Plugin(), status.Message())
	}

	// T3: 앞선 Pod가 종료되어 gpu-l4 수량이 회복된다.
	if err := cache.RemovePod(assumedA); err != nil {
		t.Fatalf("T3 RemovePod() error = %v", err)
	}
	assertGPUUsed(t, cache, "T3", map[string]int64{"gpu-l4": 0, "gpu-a100": 1})

	// T4: Pending이던 Pod가 회복된 gpu-l4에 배치된다.
	resultC, err := sched.schedulePod(ctx, fwk, podC)
	if err != nil {
		t.Fatalf("T4 schedulePod() error = %v", err)
	}
	if resultC.SuggestedHost != "gpu-l4" {
		t.Fatalf("T4 SuggestedHost = %q, want gpu-l4 after capacity was released",
			resultC.SuggestedHost)
	}
	assumedC := podC.DeepCopy()
	if err := sched.assume(klogger, assumedC, resultC.SuggestedHost); err != nil {
		t.Fatalf("T4 assume() error = %v", err)
	}
	if err := sched.runBindPlugin(ctx, fwk, assumedC, &resultC); err != nil {
		t.Fatalf("T4 runBindPlugin() error = %v", err)
	}
	assertSchedulerBindingAction(t, fakeClient.Actions(), podC, resultC.SuggestedHost)
	t.Logf("T4 rebound pending pod: %s/%s -> %s", podC.Namespace, podC.Name, resultC.SuggestedHost)
}

// Assume된 Pod가 Bind 이전 단계에서 실패해 cache에서 제거되면 점유했던 가속기
// 수량이 정확히 반환되어야 한다. bindingCycle은 비동기로 실행되므로 롤백이
// 누락되면 자원이 영구적으로 새어 나간다.
func TestSchedulerAssumeRollbackReleasesAcceleratorCapacity(t *testing.T) {
	backendlogger.Init(backendlogger.NewDefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cache := utils.NewCache(ctx)
	if err := cache.AddNode(newSchedulerFlowNode("gpu-l4", "NVIDIA-L4", "1"), nil); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}

	fwk := framework.NewFramework(
		nil,
		[]framework.FilterPlugin{plugin.NewNodeResourcesFit(), plugin.NewAcceleratorModelFit()},
		[]framework.ScorePlugin{plugin.NewWorkloadAwareCost(cache), plugin.NewWorkloadAwarePower(cache)},
		nil,
		plugin.NewDefaultBinder(kubernetesfake.NewSimpleClientset()),
	)
	sched := &Scheduler{Cache: cache, hamiClient: plugin.NewHAMiExtenderClient()}
	klogger := klog.FromContext(ctx)

	pod := newSequentialFlowPod("rollback-pod")
	result, err := sched.schedulePod(ctx, fwk, pod)
	if err != nil {
		t.Fatalf("schedulePod() error = %v", err)
	}
	assumed := pod.DeepCopy()
	if err := sched.assume(klogger, assumed, result.SuggestedHost); err != nil {
		t.Fatalf("assume() error = %v", err)
	}
	assertGPUUsed(t, cache, "after assume", map[string]int64{"gpu-l4": 1})

	if err := cache.RemovePod(assumed); err != nil {
		t.Fatalf("RemovePod() error = %v", err)
	}
	assertGPUUsed(t, cache, "after rollback", map[string]int64{"gpu-l4": 0})

	// 롤백 이후 같은 Node를 다시 후보로 쓸 수 있어야 한다.
	retry, err := sched.schedulePod(ctx, fwk, newSequentialFlowPod("retry-pod"))
	if err != nil {
		t.Fatalf("schedulePod() after rollback error = %v", err)
	}
	if retry.SuggestedHost != "gpu-l4" {
		t.Errorf("SuggestedHost after rollback = %q, want gpu-l4", retry.SuggestedHost)
	}
}

func newSequentialFlowPod(name string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name + "-uid"),
			Annotations: map[string]string{
				"sched.ai/gpu-model":                "a100,l4",
				"sched.ai/predicted-cost-by-model":  "a100=0.032,l4=0.010",
				"sched.ai/predicted-power-by-model": "a100=250,l4=75",
			},
		},
		Spec: v1.PodSpec{
			SchedulerName: "keti-cost-scheduler",
			Containers: []v1.Container{{
				Name: "worker",
				Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
					v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
				}},
			}},
		},
	}
}

func assertGPUUsed(t *testing.T, cache *utils.Cache, phase string, want map[string]int64) {
	t.Helper()
	nodes := cache.Nodes()
	for nodeName, wantUsed := range want {
		nodeInfo, ok := nodes[nodeName]
		if !ok {
			t.Fatalf("%s: node %s missing from cache", phase, nodeName)
		}
		var got int64
		if nodeInfo.Requested != nil {
			got = nodeInfo.Requested.ScalarResources[v1.ResourceName("nvidia.com/gpu")]
		}
		if got != wantUsed {
			t.Errorf("%s: node %s assumed nvidia.com/gpu = %d, want %d",
				phase, nodeName, got, wantUsed)
		}
	}
}
