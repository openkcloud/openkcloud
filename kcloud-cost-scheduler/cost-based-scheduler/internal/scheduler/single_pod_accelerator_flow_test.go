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
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"
)

func TestSchedulerSinglePodAnnotationFilterScoreBindFlow(t *testing.T) {
	backendlogger.Init(backendlogger.NewDefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cache := utils.NewCache(ctx)
	for _, node := range []*v1.Node{
		newSchedulerFlowNode("cpu-only", "", ""),
		newSchedulerFlowNode("gpu-h100", "h100", "1"),
		newSchedulerFlowNode("gpu-a100", "NVIDIA-A100-SXM4-80GB", "1"),
		newSchedulerFlowNode("gpu-l4", "NVIDIA-L4", "1"),
	} {
		if err := cache.AddNode(node, nil); err != nil {
			t.Fatalf("AddNode(%s) error = %v", node.Name, err)
		}
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-scheduler-flow",
			Namespace: "default",
			UID:       types.UID("gpu-scheduler-flow-uid"),
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

	fakeClient := kubernetesfake.NewSimpleClientset(pod.DeepCopy())
	fwk := framework.NewFramework(
		nil,
		[]framework.FilterPlugin{plugin.NewNodeResourcesFit(), plugin.NewAcceleratorModelFit()},
		[]framework.ScorePlugin{plugin.NewWorkloadAwareCost(cache), plugin.NewWorkloadAwarePower(cache)},
		nil,
		plugin.NewDefaultBinder(fakeClient),
	)
	sched := &Scheduler{
		Cache:      cache,
		hamiClient: plugin.NewHAMiExtenderClient(),
	}

	result, err := sched.schedulePod(ctx, fwk, pod)
	if err != nil {
		t.Fatalf("schedulePod() error = %v", err)
	}
	if result.FeasibleNodes != 2 {
		t.Errorf("FeasibleNodes = %d, want 2", result.FeasibleNodes)
	}
	if result.SuggestedHost != "gpu-l4" {
		t.Fatalf("SuggestedHost = %q, want gpu-l4", result.SuggestedHost)
	}
	if result.PluginResultMap["gpu-l4"].TotalNodeScore <= result.PluginResultMap["gpu-a100"].TotalNodeScore {
		t.Fatalf("scores: l4=%d a100=%d, want L4 greater than A100",
			result.PluginResultMap["gpu-l4"].TotalNodeScore,
			result.PluginResultMap["gpu-a100"].TotalNodeScore)
	}
	t.Logf("Scheduler result: feasible=2 scores[a100]=%d scores[l4]=%d selected=%s",
		result.PluginResultMap["gpu-a100"].TotalNodeScore,
		result.PluginResultMap["gpu-l4"].TotalNodeScore,
		result.SuggestedHost)

	assumedPod := pod.DeepCopy()
	if err := sched.assume(klog.FromContext(ctx), assumedPod, result.SuggestedHost); err != nil {
		t.Fatalf("assume() error = %v", err)
	}
	if err := sched.runBindPlugin(ctx, fwk, assumedPod, &result); err != nil {
		t.Fatalf("runBindPlugin() error = %v", err)
	}
	assertSchedulerBindingAction(t, fakeClient.Actions(), pod, result.SuggestedHost)
	t.Logf("Binding action: %s/%s -> %s", pod.Namespace, pod.Name, result.SuggestedHost)
}

func newSchedulerFlowNode(name, gpuModel, gpuCount string) *v1.Node {
	allocatable := v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("16"),
		v1.ResourceMemory: resource.MustParse("64Gi"),
	}
	labels := map[string]string{}
	if gpuCount != "" {
		allocatable[v1.ResourceName("nvidia.com/gpu")] = resource.MustParse(gpuCount)
		labels["nvidia.com/gpu.product"] = gpuModel
	}
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status:     v1.NodeStatus{Allocatable: allocatable},
	}
}

func assertSchedulerBindingAction(t *testing.T, actions []clienttesting.Action, pod *v1.Pod, nodeName string) {
	t.Helper()
	for _, action := range actions {
		if action.GetVerb() != "create" || action.GetResource().Resource != "pods" || action.GetSubresource() != "binding" {
			continue
		}
		createAction, ok := action.(clienttesting.CreateAction)
		if !ok {
			t.Fatalf("binding action type = %T, want CreateAction", action)
		}
		binding, ok := createAction.GetObject().(*v1.Binding)
		if !ok {
			t.Fatalf("binding object type = %T, want *v1.Binding", createAction.GetObject())
		}
		if binding.Name != pod.Name || binding.Namespace != pod.Namespace || binding.Target.Name != nodeName {
			t.Fatalf("binding = %s/%s -> %s, want %s/%s -> %s",
				binding.Namespace, binding.Name, binding.Target.Name,
				pod.Namespace, pod.Name, nodeName)
		}
		return
	}
	t.Fatal("Pod binding action was not recorded")
}
