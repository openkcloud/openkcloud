package acceleratorfit

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCanonicalModelL4VsL40S(t *testing.T) {
	if got := CanonicalModel("NVIDIA L40S"); got != "l40s" {
		t.Fatalf("L40S → %q, want l40s", got)
	}
	if got := CanonicalModel("NVIDIA L4"); got != "l4" {
		t.Fatalf("L4 → %q, want l4", got)
	}
	if got := CanonicalModel("l40s"); got == "l4" {
		t.Fatal("l40s must not collapse to l4")
	}
}

func TestModelFitL40SNotConfusedWithL4(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			GPUModelAnnotation: "l40s",
		}},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}}},
	}
	nodeL4 := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		GPUModelLabel: "l4",
	}}}
	nodeL40S := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		GPUModelLabel: "l40s",
	}}}
	for _, r := range EvaluateModelFit(pod, nodeL4) {
		if r.OK {
			t.Fatalf("l40s request must not match l4 node: %+v", r)
		}
	}
	for _, r := range EvaluateModelFit(pod, nodeL40S) {
		if !r.OK {
			t.Fatalf("l40s request must match l40s node: %+v", r)
		}
	}
}

func TestInitContainerAcceleratorRequestDetected(t *testing.T) {
	pod := &v1.Pod{Spec: v1.PodSpec{
		InitContainers: []v1.Container{{
			Name: "init",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}},
		Containers: []v1.Container{{Name: "c"}},
	}}
	if !PodRequestsAcceleratorType(pod, "gpu") {
		t.Fatal("init container GPU request must count")
	}
	req := ComputePodResourceRequest(pod)
	if req.ScalarResources["nvidia.com/gpu"] != 1 {
		t.Fatalf("init gpu scalar=%v", req.ScalarResources)
	}
}

func TestRestartableInitSumsWithMain(t *testing.T) {
	always := v1.ContainerRestartPolicyAlways
	pod := &v1.Pod{Spec: v1.PodSpec{
		InitContainers: []v1.Container{{
			Name:          "sidecar",
			RestartPolicy: &always,
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}},
		Containers: []v1.Container{{
			Name: "main",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}},
	}}
	got := ComputePodResourceRequest(pod).ScalarResources["nvidia.com/gpu"]
	if got != 2 {
		t.Fatalf("Always init + main must sum: got=%d want=2", got)
	}
	// Sequential init alone still maxes (not add) against empty main gpu.
	seq := &v1.Pod{Spec: v1.PodSpec{
		InitContainers: []v1.Container{{
			Name: "init",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("2")},
			},
		}},
		Containers: []v1.Container{{
			Name: "main",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}},
	}}
	if g := ComputePodResourceRequest(seq).ScalarResources["nvidia.com/gpu"]; g != 2 {
		t.Fatalf("sequential init max with main: got=%d want=2", g)
	}
}

func TestInitPhaseOrderAndOverhead(t *testing.T) {
	always := v1.ContainerRestartPolicyAlways
	sidecar := v1.Container{
		Name: "sidecar", RestartPolicy: &always,
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
		},
	}
	setup := v1.Container{
		Name: "setup",
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("2")},
		},
	}
	main := v1.Container{
		Name: "main",
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
		},
	}
	// Always starts first → overlaps sequential setup → peak 3.
	first := &v1.Pod{Spec: v1.PodSpec{
		InitContainers: []v1.Container{sidecar, setup},
		Containers:     []v1.Container{main},
	}}
	if g := ComputePodResourceRequest(first).ScalarResources["nvidia.com/gpu"]; g != 3 {
		t.Fatalf("sidecar then setup: got=%d want=3", g)
	}
	// Sequential first exits before Always → peak max(2, 1+1)=2.
	second := &v1.Pod{Spec: v1.PodSpec{
		InitContainers: []v1.Container{setup, sidecar},
		Containers:     []v1.Container{main},
	}}
	if g := ComputePodResourceRequest(second).ScalarResources["nvidia.com/gpu"]; g != 2 {
		t.Fatalf("setup then sidecar: got=%d want=2", g)
	}

	overhead := &v1.Pod{Spec: v1.PodSpec{
		Containers: []v1.Container{{
			Name: "main",
			Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("1"),
			}},
		}},
		InitContainers: []v1.Container{{
			Name: "setup",
			Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("2"),
			}},
		}},
		Overhead: v1.ResourceList{v1.ResourceCPU: resource.MustParse("500m")},
	}}
	if g := ComputePodResourceRequest(overhead).MilliCPU; g != 2500 {
		t.Fatalf("overhead after peak: got=%dm want=2500m", g)
	}
}

func TestCanonicalModelRTXPRO6000Alias(t *testing.T) {
	a := CanonicalModel("NVIDIA RTX PRO 6000")
	b := CanonicalModel("rtx-pro-6000")
	if a == "" || a != b {
		t.Fatalf("alias mismatch: %q vs %q", a, b)
	}
}

func TestPolicyRequireInvalidAndConflict(t *testing.T) {
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		"keti.re.kr/tier": "public",
	}}}
	bad := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		PolicyRequireAnnotation: "keti.re.kr/tier=public,keti.re.kr/region",
	}}}
	if r := EvaluateKetiPolicy(bad, node); r.OK || r.Code != "policy_require_invalid" {
		t.Fatalf("invalid fragment: %+v", r)
	}
	conflict := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		PolicyRequireAnnotation: "keti.re.kr/tier=secure,keti.re.kr/tier=public",
	}}}
	if r := EvaluateKetiPolicy(conflict, node); r.OK || r.Code != "policy_require_conflict" {
		t.Fatalf("conflict: %+v", r)
	}
}

func TestMissingAllocatableIsNotResourceAbsent(t *testing.T) {
	pod := &v1.Pod{Spec: v1.PodSpec{Containers: []v1.Container{{
		Name: "c",
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
		},
	}}}}
	counts := EvaluateAcceleratorCounts(pod, nil, nil)
	if len(counts) != 1 || counts[0].Code != "resource_view_missing" {
		t.Fatalf("%+v", counts)
	}
}
