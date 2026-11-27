package plugin

import (
	"context"
	"testing"

	backendlogger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestSinglePodAnnotationFilterScoreBindFlow(t *testing.T) {
	backendlogger.Init(backendlogger.NewDefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cache := utils.NewCache(ctx)
	nodes := []*v1.Node{
		newAcceleratorTestNode("cpu-only", "", ""),
		newAcceleratorTestNode("gpu-h100", "h100", "1"),
		newAcceleratorTestNode("gpu-a100", "NVIDIA-A100-SXM4-80GB", "1"),
		newAcceleratorTestNode("gpu-l4", "NVIDIA-L4", "1"),
	}
	for _, node := range nodes {
		if err := cache.AddNode(node, nil); err != nil {
			t.Fatalf("AddNode(%s) error = %v", node.Name, err)
		}
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-single-flow",
			Namespace: "default",
			UID:       types.UID("gpu-single-flow-uid"),
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
	binder := NewDefaultBinder(fakeClient)
	fwk := framework.NewFramework(
		nil,
		[]framework.FilterPlugin{NewNodeResourcesFit(), NewAcceleratorModelFit()},
		[]framework.ScorePlugin{NewWorkloadAwareCost(cache), NewWorkloadAwarePower(cache)},
		nil,
		binder,
	)

	feasible := make([]*v1.Node, 0, 2)
	for _, node := range nodes {
		result := fwk.RunFilterPlugins(ctx, pod, cache.Nodes()[node.Name])[node.Name]
		switch node.Name {
		case "gpu-a100", "gpu-l4":
			if result.IsFiltered {
				t.Errorf("node %s was filtered, want feasible", node.Name)
			} else {
				feasible = append(feasible, node)
			}
		default:
			if !result.IsFiltered {
				t.Errorf("node %s was feasible, want filtered", node.Name)
			}
		}
	}
	if len(feasible) != 2 {
		t.Fatalf("feasible node count = %d, want 2", len(feasible))
	}
	t.Logf("Filter result: feasible=[gpu-a100 gpu-l4], filtered=[cpu-only gpu-h100]")

	scores, status := fwk.RunScorePlugins(ctx, pod, feasible)
	if !status.IsSuccess() {
		t.Fatalf("RunScorePlugins() status = %s", status.Message())
	}
	if scores["gpu-l4"].TotalNodeScore <= scores["gpu-a100"].TotalNodeScore {
		t.Fatalf("scores: l4=%d a100=%d, want L4 greater than A100",
			scores["gpu-l4"].TotalNodeScore, scores["gpu-a100"].TotalNodeScore)
	}
	t.Logf("Score result: gpu-a100=%d gpu-l4=%d", scores["gpu-a100"].TotalNodeScore, scores["gpu-l4"].TotalNodeScore)

	selectedNode := "gpu-l4"
	if bindStatus := fwk.RunBindPlugin(ctx, pod, selectedNode); !bindStatus.IsSuccess() {
		t.Fatalf("RunBindPlugin() status = %s", bindStatus.Message())
	}

	assertBindingAction(t, fakeClient.Actions(), pod, selectedNode)
	t.Logf("Bind result: %s/%s -> %s", pod.Namespace, pod.Name, selectedNode)
}

func newAcceleratorTestNode(name, gpuModel, gpuCount string) *v1.Node {
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

func assertBindingAction(t *testing.T, actions []clienttesting.Action, pod *v1.Pod, nodeName string) {
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
