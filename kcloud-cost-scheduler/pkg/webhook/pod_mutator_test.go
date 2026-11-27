package webhook

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestPodMutator_GPUTraining은 GPU 학습 Pod에 비용 기반 Scheduler와
// 기본 스케줄링 Annotation이 설정되는지 확인한다.
func TestPodMutator_GPUTraining(t *testing.T) {
	pod := newTestPod("resnet-training-worker", "default", "nvidia.com/gpu", "2")
	mutator := &PodMutator{}

	mutator.applyCostSchedulerAnnotations(pod)

	if pod.Spec.SchedulerName != "keti-cost-scheduler" {
		t.Errorf("schedulerName = %q, want %q", pod.Spec.SchedulerName, "keti-cost-scheduler")
	}
	want := map[string]string{
		"sched.ai/acc-type":   "gpu",
		"sched.ai/acc-scale":  "2",
		"sched.ai/gpu-vendor": "nvidia",
		"sched.ai/gpu-model":  "generic",
		"sched.ai/intent":     "training",
		"sched.ai/preference": "throughput",
		"sched.ai/batch-size": "32",
	}
	assertAnnotations(t, pod.Annotations, want)
}

// TestPodMutator_NPUInference은 Furiosa NPU 추론 Pod가 npu/latency로
// 분류되고, 학습 전용 기본 batch-size가 추가되지 않는지 확인한다.
func TestPodMutator_NPUInference(t *testing.T) {
	pod := newTestPod("bert-inference", "serving", "furiosa.ai/warboy", "1")
	mutator := &PodMutator{}

	mutator.applyCostSchedulerAnnotations(pod)

	want := map[string]string{
		"sched.ai/acc-type":   "npu",
		"sched.ai/acc-scale":  "1",
		"sched.ai/npu-vendor": "furiosa",
		"sched.ai/npu-model":  "warboy",
		"sched.ai/intent":     "inference",
		"sched.ai/preference": "latency",
	}
	assertAnnotations(t, pod.Annotations, want)
	if _, exists := pod.Annotations["sched.ai/batch-size"]; exists {
		t.Errorf("inference pod unexpectedly received sched.ai/batch-size=%q", pod.Annotations["sched.ai/batch-size"])
	}
}

func TestPodMutator_GenericNPU(t *testing.T) {
	pod := newTestPodWithResources("generic-inference", "serving", map[string]string{
		"cpu":             "2",
		"example.com/npu": "2",
	})
	mutator := &PodMutator{}

	mutator.applyCostSchedulerAnnotations(pod)

	assertAnnotations(t, pod.Annotations, map[string]string{
		"sched.ai/acc-type":             "npu",
		"sched.ai/acc-scale":            "2",
		"sched.ai/npu-count":            "2",
		"sched.ai/gpu-count":            "0",
		"sched.ai/acc-vendor":           "example",
		"sched.ai/acc-model":            "generic",
		"sched.ai/acc-resource-counts":  "example.com/npu=2",
		"sched.ai/resource-composition": "cpu+npu",
	})
}

func TestPodMutator_GPUNPUMixedComposition(t *testing.T) {
	pod := newTestPodWithResources("mixed-inference", "serving", map[string]string{
		"cpu":               "4",
		"nvidia.com/gpu":    "1",
		"furiosa.ai/warboy": "2",
	})
	mutator := &PodMutator{}

	mutator.applyCostSchedulerAnnotations(pod)

	assertAnnotations(t, pod.Annotations, map[string]string{
		"sched.ai/acc-type":             "mixed",
		"sched.ai/acc-profile":          "mixed-untuned",
		"sched.ai/acc-scale":            "3",
		"sched.ai/gpu-count":            "1",
		"sched.ai/npu-count":            "2",
		"sched.ai/acc-vendor":           "furiosa,nvidia",
		"sched.ai/gpu-vendor":           "nvidia",
		"sched.ai/gpu-model":            "generic",
		"sched.ai/npu-vendor":           "furiosa",
		"sched.ai/npu-model":            "warboy",
		"sched.ai/acc-resource-counts":  "furiosa.ai/warboy=2,nvidia.com/gpu=1",
		"sched.ai/resource-composition": "cpu+gpu+npu",
	})
}

// TestWorkloadToPodNPUAnnotationFlow verifies the current Windows/WSL target:
// Workload NPU identity + mock prediction annotations are visible unchanged to
// the Pod admission layer, while per-Pod resource counts are re-derived.
func TestWorkloadToPodNPUAnnotationFlow(t *testing.T) {
	workload := newTestWorkloadWithResources("npu-flow", 1, map[string]string{
		"cpu":               "2",
		"furiosa.ai/warboy": "1",
	}, false)
	workloadMutator := &WorkloadMutator{}
	workloadMutator.mutateWorkload(workload)
	annotations := workload.GetAnnotations()
	annotations["sched.ai/predicted-power-npu"] = "60"
	annotations["sched.ai/predicted-cost-npu"] = "0.0072"
	annotations["sched.ai/prediction-provider"] = "mock"
	annotations["sched.ai/prediction-model-version"] = "mock-v1"
	annotations[annotationPredictedCostByModel] = "warboy=0.0072,rngd=0.014"
	annotations[annotationPredictedPowerByModel] = "warboy=60,rngd=110"
	workload.SetAnnotations(annotations)

	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(kueueWorkloadGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(
		kueueWorkloadGVK.GroupVersion().WithKind("WorkloadList"),
		&unstructured.UnstructuredList{},
	)
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(workload).Build()

	pod := newTestPodWithResources("npu-flow-worker", "kcp-workload", map[string]string{
		"cpu":               "2",
		"furiosa.ai/warboy": "1",
	})
	pod.Annotations = map[string]string{kueueWorkloadAnnotation: workload.GetName()}
	podMutator := &PodMutator{Client: client}
	if err := podMutator.propagateKueueWorkloadAnnotations(context.Background(), pod); err != nil {
		t.Fatalf("propagateKueueWorkloadAnnotations() error = %v", err)
	}
	podMutator.applyCostSchedulerAnnotations(pod)

	assertAnnotations(t, pod.Annotations, map[string]string{
		"sched.ai/acc-type":                 "npu",
		"sched.ai/acc-vendor":               "furiosa",
		"sched.ai/acc-model":                "warboy",
		"sched.ai/npu-vendor":               "furiosa",
		"sched.ai/npu-model":                "warboy",
		"sched.ai/acc-resource":             "furiosa.ai/warboy",
		"sched.ai/acc-resource-counts":      "furiosa.ai/warboy=1",
		"sched.ai/npu-count":                "1",
		"sched.ai/resource-composition":     "cpu+npu",
		"sched.ai/predicted-power-npu":      "60",
		"sched.ai/predicted-cost-npu":       "0.0072",
		"sched.ai/prediction-provider":      "mock",
		"sched.ai/prediction-model-version": "mock-v1",
		"sched.ai/predicted-cost-by-model":  "warboy=0.0072,rngd=0.014",
		"sched.ai/predicted-power-by-model": "warboy=60,rngd=110",
	})
	if pod.Spec.SchedulerName != "keti-cost-scheduler" {
		t.Errorf("schedulerName = %q, want keti-cost-scheduler", pod.Spec.SchedulerName)
	}
}

// TestPodMutator_PreservesUserIntent은 사용자가 이미 지정한 intent와
// preference를 Webhook이 덮어쓰지 않는지 확인한다.
func TestPodMutator_PreservesUserIntent(t *testing.T) {
	pod := newTestPod("custom-job", "default", "nvidia.com/gpu", "1")
	pod.Annotations = map[string]string{
		"sched.ai/intent":     "inference",
		"sched.ai/preference": "power",
	}
	mutator := &PodMutator{}

	mutator.applyCostSchedulerAnnotations(pod)

	if got := pod.Annotations["sched.ai/intent"]; got != "inference" {
		t.Errorf("intent = %q, want existing value inference", got)
	}
	if got := pod.Annotations["sched.ai/preference"]; got != "power" {
		t.Errorf("preference = %q, want existing value power", got)
	}
}

// TestPodMutator_ShouldOptimize는 시스템 Pod와 명시적으로 비활성화한
// Pod를 Webhook 대상에서 제외하는 조건을 검증한다.
func TestPodMutator_ShouldOptimize(t *testing.T) {
	mutator := &PodMutator{}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{name: "normal workload", pod: newTestPod("app", "default", "nvidia.com/gpu", "1"), want: true},
		{name: "system namespace", pod: newTestPod("system", "kube-system", "", ""), want: false},
		{name: "disabled annotation", pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Annotations: map[string]string{"kcloud.io/optimization-disabled": "true"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		}, want: false},
		{name: "no containers", pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mutator.shouldOptimizePod(tt.pod); got != tt.want {
				t.Errorf("shouldOptimizePod() = %v, want %v", got, tt.want)
			}
		})
	}
}

func newTestPod(name, namespace, resourceName, resourceQuantity string) *corev1.Pod {
	resources := map[string]string{}
	if resourceName != "" {
		resources[resourceName] = resourceQuantity
	}
	return newTestPodWithResources(name, namespace, resources)
}

func newTestPodWithResources(name, namespace string, values map[string]string) *corev1.Pod {
	requests := corev1.ResourceList{}
	for resourceName, resourceQuantity := range values {
		requests[corev1.ResourceName(resourceName)] = resource.MustParse(resourceQuantity)
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:      "worker",
				Resources: corev1.ResourceRequirements{Requests: requests},
			}},
		},
	}
}
