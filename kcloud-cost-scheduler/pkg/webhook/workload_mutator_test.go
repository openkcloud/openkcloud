package webhook

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestWorkloadMutator_GPUTraining은 다음 입력을 검증한다.
//
//   - Kueue PodSet 수: 2개
//   - PodSet당 Pod 수: 1개
//   - Pod당 GPU 수: 2개
//
// 따라서 전체 가속기 수(acc-scale)는 2 × 1 × 2 = 4가 되어야 한다.
func TestWorkloadMutator_GPUTraining(t *testing.T) {
	workload := newTestWorkload("resnet-training", 1, "nvidia.com/gpu", "2", false)
	mutator := &WorkloadMutator{}

	if changed := mutator.mutateWorkload(workload); !changed {
		t.Fatal("mutateWorkload() = false, want true")
	}

	want := map[string]string{
		"sched.ai/intent":           "training",
		"sched.ai/preference":       "throughput",
		"sched.ai/acc-type":         "gpu",
		"sched.ai/gpu-vendor":       "nvidia",
		"sched.ai/gpu-model":        "generic",
		"sched.ai/acc-profile":      "small",
		"sched.ai/acc-scale":        "4",
		"sched.ai/rescheduling":     "free",
		"sched.ai/data-sensitivity": "low",
		"kcloud.io/optimized":       "true",
	}
	assertAnnotations(t, workload.GetAnnotations(), want)
}

// TestWorkloadMutator_PVCForbidsRescheduling은 PVC가 연결된 워크로드가
// 데이터 지역성 문제로 재배치 금지 상태가 되는지 확인한다.
func TestWorkloadMutator_PVCForbidsRescheduling(t *testing.T) {
	workload := newTestWorkload("llm-training", 1, "nvidia.com/gpu", "1", true)
	mutator := &WorkloadMutator{}

	mutator.mutateWorkload(workload)

	want := map[string]string{
		"sched.ai/rescheduling":     "forbidden",
		"sched.ai/data-sensitivity": "very-high",
	}
	assertAnnotations(t, workload.GetAnnotations(), want)
}

// TestWorkloadMutator_NPUInference은 NPU가 npu로 분류되고,
// 이름에 infer가 포함된 워크로드가 serving/latency로 분류되는지 확인한다.
func TestWorkloadMutator_NPUInference(t *testing.T) {
	workload := newTestWorkload("bert-inference", 1, "npu.com/npu", "1", false)
	mutator := &WorkloadMutator{}

	mutator.mutateWorkload(workload)

	want := map[string]string{
		"sched.ai/intent":      "serving",
		"sched.ai/preference":  "latency",
		"sched.ai/acc-type":    "npu",
		"sched.ai/acc-profile": "npu-single",
		"sched.ai/acc-scale":   "2",
	}
	assertAnnotations(t, workload.GetAnnotations(), want)
}

// TestWorkloadMutator_FuriosaNPU는 이름에 npu가 없는 Furiosa resource도
// 제조사, 모델, 자원명과 수량을 포함해 NPU로 전달되는지 검증한다.
func TestWorkloadMutator_FuriosaNPU(t *testing.T) {
	workload := newTestWorkload("bert-inference", 1, "furiosa.ai/warboy", "1", false)
	mutator := &WorkloadMutator{}
	mutator.mutateWorkload(workload)

	assertAnnotations(t, workload.GetAnnotations(), map[string]string{
		"sched.ai/acc-type":             "npu",
		"sched.ai/acc-scale":            "2",
		"sched.ai/npu-count":            "2",
		"sched.ai/gpu-count":            "0",
		"sched.ai/acc-vendor":           "furiosa",
		"sched.ai/acc-model":            "warboy",
		"sched.ai/npu-vendor":           "furiosa",
		"sched.ai/npu-model":            "warboy",
		"sched.ai/acc-resource":         "furiosa.ai/warboy",
		"sched.ai/acc-resource-counts":  "furiosa.ai/warboy=2",
		"sched.ai/resource-composition": "npu",
	})
}

func TestWorkloadMutator_FuriosaRNGDIdentity(t *testing.T) {
	workload := newTestWorkload("rngd-inference", 1, "furiosa.ai/rngd", "1", false)
	mutator := &WorkloadMutator{}
	mutator.mutateWorkload(workload)

	assertAnnotations(t, workload.GetAnnotations(), map[string]string{
		"sched.ai/acc-type":            "npu",
		"sched.ai/acc-vendor":          "furiosa",
		"sched.ai/acc-model":           "rngd",
		"sched.ai/npu-vendor":          "furiosa",
		"sched.ai/npu-model":           "rngd",
		"sched.ai/acc-resource":        "furiosa.ai/rngd",
		"sched.ai/acc-resource-counts": "furiosa.ai/rngd=2",
	})
}

func TestWorkloadMutator_PreservesExplicitGPUModels(t *testing.T) {
	workload := newTestWorkload("gpu-model-choice", 1, "nvidia.com/gpu", "1", false)
	workload.SetAnnotations(map[string]string{
		annotationGPUModel:              "a100,l4",
		annotationPredictedCostByModel:  "a100=0.032,l4=0.010",
		annotationPredictedPowerByModel: "a100=250,l4=75",
	})
	mutator := &WorkloadMutator{}
	mutator.mutateWorkload(workload)

	assertAnnotations(t, workload.GetAnnotations(), map[string]string{
		"sched.ai/gpu-vendor":               "nvidia",
		"sched.ai/gpu-model":                "a100,l4",
		"sched.ai/predicted-cost-by-model":  "a100=0.032,l4=0.010",
		"sched.ai/predicted-power-by-model": "a100=250,l4=75",
	})
}

func TestWorkloadMutator_GenericNPU(t *testing.T) {
	workload := newTestWorkload("generic-inference", 1, "example.com/npu", "2", false)
	mutator := &WorkloadMutator{}
	mutator.mutateWorkload(workload)

	assertAnnotations(t, workload.GetAnnotations(), map[string]string{
		"sched.ai/acc-type":             "npu",
		"sched.ai/acc-scale":            "4",
		"sched.ai/npu-count":            "4",
		"sched.ai/acc-vendor":           "example",
		"sched.ai/acc-model":            "generic",
		"sched.ai/acc-resource":         "example.com/npu",
		"sched.ai/acc-resource-counts":  "example.com/npu=4",
		"sched.ai/resource-composition": "npu",
	})
}

// CPU+NPU는 일반적인 NPU Pod 구성이므로 acc-type은 npu로 유지하고,
// CPU 포함 여부는 별도의 resource-composition에 기록한다.
func TestWorkloadMutator_CPUNPUComposition(t *testing.T) {
	workload := newTestWorkloadWithResources("cpu-npu-inference", 1, map[string]string{
		"cpu":               "2",
		"furiosa.ai/warboy": "1",
	}, false)
	mutator := &WorkloadMutator{}
	mutator.mutateWorkload(workload)

	assertAnnotations(t, workload.GetAnnotations(), map[string]string{
		"sched.ai/acc-type":             "npu",
		"sched.ai/npu-count":            "2",
		"sched.ai/resource-composition": "cpu+npu",
	})
}

// GPU+NPU 동시 요청은 아직 점수 튜닝 대상이 아니지만, 향후 구현이
// 연쇄적으로 바뀌지 않도록 mixed와 장치별 수량을 지금부터 기록한다.
func TestWorkloadMutator_GPUNPUMixedComposition(t *testing.T) {
	workload := newTestWorkloadWithResources("gpu-npu-mixed", 1, map[string]string{
		"cpu":               "4",
		"nvidia.com/gpu":    "1",
		"furiosa.ai/warboy": "2",
	}, false)
	mutator := &WorkloadMutator{}
	mutator.mutateWorkload(workload)

	assertAnnotations(t, workload.GetAnnotations(), map[string]string{
		"sched.ai/acc-type":             "mixed",
		"sched.ai/acc-profile":          "mixed-untuned",
		"sched.ai/acc-scale":            "6",
		"sched.ai/gpu-count":            "2",
		"sched.ai/npu-count":            "4",
		"sched.ai/acc-vendor":           "furiosa,nvidia",
		"sched.ai/gpu-vendor":           "nvidia",
		"sched.ai/gpu-model":            "generic",
		"sched.ai/npu-vendor":           "furiosa",
		"sched.ai/npu-model":            "warboy",
		"sched.ai/acc-resource-counts":  "furiosa.ai/warboy=4,nvidia.com/gpu=2",
		"sched.ai/resource-composition": "cpu+gpu+npu",
	})
}

// newTestWorkload는 테스트용 Kueue Workload 구조를 만든다.
// 실제 코드가 읽는 spec.podSets[].template.spec 형식만 최소한으로 구성한다.
func newTestWorkload(name string, podCount int64, resourceName, resourceQuantity string, withPVC bool) *unstructured.Unstructured {
	return newTestWorkloadWithResources(name, podCount, map[string]string{
		resourceName: resourceQuantity,
	}, withPVC)
}

func newTestWorkloadWithResources(name string, podCount int64, resources map[string]string, withPVC bool) *unstructured.Unstructured {
	limits := map[string]interface{}{}
	for resourceName, resourceQuantity := range resources {
		limits[resourceName] = resourceQuantity
	}
	podSpec := map[string]interface{}{
		"containers": []interface{}{
			map[string]interface{}{
				"name": "worker",
				"resources": map[string]interface{}{
					"limits": limits,
				},
			},
		},
	}
	if withPVC {
		podSpec["volumes"] = []interface{}{
			map[string]interface{}{
				"name": "training-data",
				"persistentVolumeClaim": map[string]interface{}{
					"claimName": "dataset-pvc",
				},
			},
		}
	}

	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "kueue.x-k8s.io/v1beta1",
		"kind":       "Workload",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "kcp-workload",
		},
		"spec": map[string]interface{}{
			"podSets": []interface{}{
				map[string]interface{}{
					"name":  "workers-a",
					"count": podCount,
					"template": map[string]interface{}{
						"spec": podSpec,
					},
				},
				map[string]interface{}{
					"name":  "workers-b",
					"count": podCount,
					"template": map[string]interface{}{
						"spec": podSpec,
					},
				},
			},
		},
	}}
}

func assertAnnotations(t *testing.T, got, want map[string]string) {
	t.Helper()
	for key, wantValue := range want {
		if gotValue := got[key]; gotValue != wantValue {
			t.Errorf("annotation %q = %q, want %q", key, gotValue, wantValue)
		}
	}
}
