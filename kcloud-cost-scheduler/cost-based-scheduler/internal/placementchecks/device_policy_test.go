package placementchecks

import (
	"testing"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCheck1ModelMismatchReject(t *testing.T) {
	now := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			acceleratorfit.GPUModelAnnotation: "l40s",
		}},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}}},
	}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{
			acceleratorfit.GPUModelLabel: "l4",
		}},
		Status: v1.NodeStatus{Allocatable: v1.ResourceList{
			"nvidia.com/gpu": resource.MustParse("1"),
		}},
	}
	res := NodeResourceView{Allocatable: node.Status.Allocatable, AllocatedScalar: map[string]int64{}}
	cr := EvaluateAccelerator(pod, node, res, cfg.Checks[CheckAccelerator], now)
	if cr.Result != ResultREJECT || cr.Code != "model_mismatch" {
		t.Fatalf("%+v", cr)
	}
}

func TestCheck1InitGPUCountUsesSharedFit(t *testing.T) {
	now := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	pod := &v1.Pod{Spec: v1.PodSpec{
		InitContainers: []v1.Container{{
			Name: "init",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}},
		Containers: []v1.Container{{Name: "c"}},
	}}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: v1.NodeStatus{Allocatable: v1.ResourceList{
			"nvidia.com/gpu": resource.MustParse("1"),
		}},
	}
	res := NodeResourceView{Allocatable: node.Status.Allocatable, AllocatedScalar: map[string]int64{}}
	cr := EvaluateAccelerator(pod, node, res, cfg.Checks[CheckAccelerator], now)
	if cr.Result != ResultPASS {
		t.Fatalf("init gpu should PASS with free device: %+v", cr)
	}
	res.AllocatedScalar["nvidia.com/gpu"] = 1
	cr2 := EvaluateAccelerator(pod, node, res, cfg.Checks[CheckAccelerator], now)
	if cr2.Result != ResultREJECT {
		t.Fatalf("no free gpu must REJECT: %+v", cr2)
	}
}

func TestCheck1PartitionUnsupported(t *testing.T) {
	now := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	pod := &v1.Pod{Spec: v1.PodSpec{Containers: []v1.Container{{
		Name: "c",
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{"nvidia.com/mig-1g.5gb": resource.MustParse("1")},
		},
	}}}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	cr := EvaluateAccelerator(pod, node, NodeResourceView{}, DefaultConfig().Checks[CheckAccelerator], now)
	if cr.Code != "partition_unsupported" {
		t.Fatalf("%+v", cr)
	}
}

func TestCheck7TaintAndPolicy(t *testing.T) {
	now := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec: v1.NodeSpec{Taints: []v1.Taint{{
			Key: "dedicated", Value: "gpu", Effect: v1.TaintEffectNoSchedule,
		}}},
	}
	cr := EvaluatePolicy(pod, node, cfg.Checks[CheckPolicy], now)
	if cr.Result != ResultREJECT || cr.Code != "taint_untolerated" {
		t.Fatalf("%+v", cr)
	}

	pod2 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{
			acceleratorfit.PolicyRequireAnnotation: "keti.re.kr/tier=gpu",
		},
	}}
	node2 := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n2", Labels: map[string]string{
		"keti.re.kr/tier": "cpu",
	}}}
	cr2 := EvaluatePolicy(pod2, node2, cfg.Checks[CheckPolicy], now)
	if cr2.Code != "policy_label_mismatch" {
		t.Fatalf("%+v", cr2)
	}
	// Node having keti label without pod requirement must not invent a policy PASS requirement.
	pod3 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p3"}}
	cr3 := EvaluatePolicy(pod3, node2, cfg.Checks[CheckPolicy], now)
	if cr3.Result != ResultPASS {
		t.Fatalf("no policy-require should PASS: %+v", cr3)
	}
}

func TestEnginePreservesLegacyReasonsInRecordMode(t *testing.T) {
	now := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	cfg := DefaultConfig() // record
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			acceleratorfit.GPUModelAnnotation: "l40s",
		}},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}}},
	}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{
			acceleratorfit.GPUModelLabel: "l4",
		}},
		Status: v1.NodeStatus{Allocatable: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")}},
	}
	rep := NewEngine(cfg).EvaluateNode(EvalInput{
		Pod: pod, Node: node, NodeName: "n1",
		Resources: NodeResourceView{Allocatable: node.Status.Allocatable, AllocatedScalar: map[string]int64{}},
		Now:       now,
	})
	if ShouldExclude(rep) {
		t.Fatal("record mode must not set IsFiltered via required-check enforce")
	}
	found := false
	for _, r := range rep.RejectionReasons {
		if r.Stage == "legacy_filter" && r.Code == "model_mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy reason missing: %+v", rep.RejectionReasons)
	}
}

func TestFilterAndCheck1ShareModelJudgment(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			acceleratorfit.GPUModelAnnotation: "l40s",
		}},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}}},
	}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
		acceleratorfit.GPUModelLabel: "l4",
	}}}
	shared := acceleratorfit.EvaluateModelFit(pod, node)
	if len(shared) != 1 || shared[0].OK {
		t.Fatalf("shared=%+v", shared)
	}
	cr := EvaluateAccelerator(pod, node, NodeResourceView{
		Allocatable: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")}, AllocatedScalar: map[string]int64{},
	}, DefaultConfig().Checks[CheckAccelerator], time.Now().UTC())
	if cr.Code != shared[0].Code {
		t.Fatalf("check1 code %s != shared %s", cr.Code, shared[0].Code)
	}
}

// Formalized from REVIEW_P3.md P3R1–P3R7 (overlay counterexamples).
func TestReviewP3Boundaries(t *testing.T) {
	now := time.Date(2026, 10, 27, 0, 30, 0, 0, time.UTC)
	cfg := DefaultConfig()
	gpuPod := func() *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "review"},
			Spec: v1.PodSpec{Containers: []v1.Container{{
				Name: "main",
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
				},
			}}},
		}
	}
	gpuNode := func() *v1.Node {
		return &v1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{
				acceleratorfit.GPUModelLabel: "rtx-pro-6000",
				"keti.re.kr/tier":           "public",
			}},
			Status: v1.NodeStatus{Allocatable: v1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("1"),
			}},
		}
	}

	t.Run("P3R1_restartable_init_and_main_are_concurrent", func(t *testing.T) {
		p := gpuPod()
		always := v1.ContainerRestartPolicyAlways
		p.Spec.InitContainers = []v1.Container{{
			Name:          "sidecar",
			RestartPolicy: &always,
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
		}}
		got := acceleratorfit.ComputePodResourceRequest(p).ScalarResources["nvidia.com/gpu"]
		if got != 2 {
			t.Fatalf("restartable init GPU 1 + concurrent main GPU 1: got=%d want=2", got)
		}
		if ParseRequest(p).GPUCount != 2 {
			t.Fatalf("ParseRequest must match shared sum: %d", ParseRequest(p).GPUCount)
		}
	})

	t.Run("P3R2_invalid_policy_fragment_must_not_be_ignored", func(t *testing.T) {
		p := gpuPod()
		p.Annotations = map[string]string{
			acceleratorfit.PolicyRequireAnnotation: "keti.re.kr/tier=public,keti.re.kr/region",
		}
		got := acceleratorfit.EvaluateKetiPolicy(p, gpuNode())
		if got.OK {
			t.Fatalf("malformed second requirement silently ignored: %+v", got)
		}
		if got.Code != "policy_require_invalid" {
			t.Fatalf("code=%s want policy_require_invalid", got.Code)
		}
	})

	t.Run("P3R3_conflicting_duplicate_policy_keys", func(t *testing.T) {
		p := gpuPod()
		p.Annotations = map[string]string{
			acceleratorfit.PolicyRequireAnnotation: "keti.re.kr/tier=secure,keti.re.kr/tier=public",
		}
		got := acceleratorfit.EvaluateKetiPolicy(p, gpuNode())
		if got.OK {
			t.Fatalf("conflicting policy requirements reduced to last value: %+v", got)
		}
		if got.Code != "policy_require_conflict" {
			t.Fatalf("code=%s want policy_require_conflict", got.Code)
		}
	})

	t.Run("P3R4_new_policy_must_not_be_labeled_legacy", func(t *testing.T) {
		p, n := gpuPod(), gpuNode()
		p.Annotations = map[string]string{
			acceleratorfit.PolicyRequireAnnotation: "keti.re.kr/tier=secure",
		}
		cr := EvaluatePolicy(p, n, cfg.Checks[CheckPolicy], now)
		if cr.Result != ResultREJECT {
			t.Fatalf("control: expected policy mismatch, got %+v", cr)
		}
		report := CandidateReport{}
		AppendLegacyRejectionReasons(&report, CheckResult{}, cr)
		for _, r := range report.RejectionReasons {
			if r.Stage == "legacy_filter" {
				t.Fatalf("new annotation policy is not checked by any legacy Filter: %+v", r)
			}
		}
	})

	t.Run("P3R5_MIG_unknown_must_not_hide_known_GPU_shortage", func(t *testing.T) {
		p, n := gpuPod(), gpuNode()
		p.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"] = resource.MustParse("2")
		p.Spec.Containers[0].Resources.Requests["nvidia.com/mig-1g.5gb"] = resource.MustParse("1")
		got := EvaluateAccelerator(p, n, NodeResourceView{Allocatable: n.Status.Allocatable}, cfg.Checks[CheckAccelerator], now)
		if got.Result != ResultREJECT {
			t.Fatalf("known 2>1 GPU shortage hidden by partition_unsupported: %+v", got)
		}
		if got.Code != "insufficient_count" {
			t.Fatalf("code=%s want insufficient_count", got.Code)
		}
	})

	t.Run("P3R6_product_name_spacing_alias", func(t *testing.T) {
		p, n := gpuPod(), gpuNode()
		p.Annotations = map[string]string{
			acceleratorfit.GPUModelAnnotation: "NVIDIA RTX PRO 6000",
		}
		got := acceleratorfit.EvaluateModelFit(p, n)
		if len(got) != 1 || !got[0].OK {
			t.Fatalf("same product, display annotation vs canonical node label: %+v", got)
		}
	})

	t.Run("P3R7_missing_resource_view_is_not_proven_shortage", func(t *testing.T) {
		p, n := gpuPod(), gpuNode()
		got := EvaluateAccelerator(p, n, NodeResourceView{}, cfg.Checks[CheckAccelerator], now)
		if got.Result == ResultREJECT {
			t.Fatalf("missing resource snapshot treated as measured shortage despite node GPU=1: %+v", got)
		}
		if got.Result != ResultUNEVALUATED || got.Code != "resource_view_missing" {
			t.Fatalf("want UNEVALUATED/resource_view_missing, got %+v", got)
		}
	})
}

// Formalized from REVIEW_P3_AFTER_FIX.md — init-phase overlap and overhead order.
func TestReviewP3InitPhaseBoundaries(t *testing.T) {
	makePod := func(sidecarFirst bool) *v1.Pod {
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
		init := []v1.Container{setup, sidecar}
		if sidecarFirst {
			init = []v1.Container{sidecar, setup}
		}
		return &v1.Pod{Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name: "main",
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
				},
			}},
			InitContainers: init,
		}}
	}

	t.Run("sidecar_before_normal_init_peak_is_three_GPUs", func(t *testing.T) {
		p := makePod(true)
		got := acceleratorfit.ComputePodResourceRequest(p).ScalarResources["nvidia.com/gpu"]
		parsed := ParseRequest(p).GPUCount
		if got != 3 || parsed != 3 {
			t.Fatalf("sidecar(1) remains during setup(2); fit=%d parsed=%d want=3", got, parsed)
		}
	})

	t.Run("normal_init_before_sidecar_peak_is_two_GPUs", func(t *testing.T) {
		p := makePod(false)
		got := acceleratorfit.ComputePodResourceRequest(p).ScalarResources["nvidia.com/gpu"]
		if got != 2 {
			t.Fatalf("setup(2) exits before sidecar(1)+main(1); got=%d want=2", got)
		}
	})

	t.Run("overhead_is_added_after_phase_peak", func(t *testing.T) {
		p := &v1.Pod{Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name: "main",
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("1"),
						v1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			}},
			InitContainers: []v1.Container{{
				Name: "setup",
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			}},
			Overhead: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("500m"),
				v1.ResourceMemory: resource.MustParse("128Mi"),
			},
		}}
		got := acceleratorfit.ComputePodResourceRequest(p)
		wantMem := int64(2176 * 1024 * 1024)
		if got.MilliCPU != 2500 || got.Memory != wantMem {
			t.Fatalf("max(app,setup)+overhead: got cpu=%dm memory=%d; want cpu=2500m memory=%d",
				got.MilliCPU, got.Memory, wantMem)
		}
	})
}
