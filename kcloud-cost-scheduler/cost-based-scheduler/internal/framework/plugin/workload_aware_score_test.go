package plugin

import (
	"context"
	"testing"

	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWorkloadAwareNPUReadsForwardedAnnotations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := utils.NewCache(ctx)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "npu-node"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:                       resource.MustParse("16"),
			corev1.ResourceName("furiosa.ai/warboy"): resource.MustParse("2"),
		}},
	}
	if err := cache.AddNode(node, nil); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "npu-inference",
		Annotations: map[string]string{
			"sched.ai/acc-type":            "npu",
			"sched.ai/acc-vendor":          "furiosa",
			"sched.ai/npu-count":           "1",
			"sched.ai/predicted-cost-npu":  "0.0072",
			"sched.ai/predicted-power-npu": "60",
		},
	}}

	costScore, costStatus := NewWorkloadAwareCost(cache).Score(ctx, pod, node.Name)
	if !costStatus.IsSuccess() {
		t.Fatalf("cost status = %s", costStatus.Message())
	}
	if costScore <= 5 {
		t.Errorf("cost score = %d, want forwarded prediction score greater than neutral 5", costScore)
	}

	powerScore, powerStatus := NewWorkloadAwarePower(cache).Score(ctx, pod, node.Name)
	if !powerStatus.IsSuccess() {
		t.Fatalf("power status = %s", powerStatus.Message())
	}
	if powerScore <= 5 {
		t.Errorf("power score = %d, want forwarded prediction score greater than neutral 5", powerScore)
	}
}

func TestNodeDeviceTypeGenericNPUFromLabel(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "generic-npu-node",
		Labels: map[string]string{
			"accelerator.type":  "npu",
			"accelerator.model": "generic",
		},
	}}
	if got := nodeDeviceType(node); got != "npu" {
		t.Errorf("nodeDeviceType() = %q, want npu", got)
	}
}

func TestNodeDeviceTypeRecognizesFuriosaRNGD(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-node"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceName("furiosa.ai/rngd"): resource.MustParse("2"),
		}},
	}
	if got := nodeDeviceType(node); got != "npu" {
		t.Errorf("nodeDeviceType() = %q, want npu", got)
	}
	models := nodeAcceleratorModels(node, "npu")
	if len(models) != 1 || models[0] != "rngd" {
		t.Errorf("nodeAcceleratorModels() = %v, want [rngd]", models)
	}
}

func TestWorkloadAwareMixedReturnsNeutralUntilTuned(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := utils.NewCache(ctx)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-npu-node"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceName("nvidia.com/gpu"):    resource.MustParse("1"),
			corev1.ResourceName("furiosa.ai/warboy"): resource.MustParse("2"),
		}},
	}
	if err := cache.AddNode(node, nil); err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "mixed-workload",
		Annotations: map[string]string{
			"sched.ai/acc-type":             "mixed",
			"sched.ai/resource-composition": "cpu+gpu+npu",
			"sched.ai/predicted-cost-gpu":   "0.02",
			"sched.ai/predicted-cost-npu":   "0.01",
			"sched.ai/predicted-power-gpu":  "250",
			"sched.ai/predicted-power-npu":  "60",
		},
	}}

	if got := nodeDeviceType(node); got != "mixed" {
		t.Fatalf("nodeDeviceType() = %q, want mixed", got)
	}
	costScore, _ := NewWorkloadAwareCost(cache).Score(ctx, pod, node.Name)
	powerScore, _ := NewWorkloadAwarePower(cache).Score(ctx, pod, node.Name)
	if costScore != 5 || powerScore != 5 {
		t.Errorf("mixed scores = cost:%d power:%d, want neutral 5/5", costScore, powerScore)
	}
}
