package utils

import (
	"context"
	"testing"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCachedModelFitReusesAttemptReport(t *testing.T) {
	cfg := placementchecks.DefaultConfig()
	st := NewAttemptState(placementchecks.AttemptMeta{
		AttemptID: "a1", StartedAt: time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC),
	}, placementchecks.Snapshot{Nodes: map[string]placementchecks.NodeTelemetry{}}, placementchecks.ParsedRequest{}, cfg)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p",
			Annotations: map[string]string{
				acceleratorfit.GPUModelAnnotation: "l40s",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "c", Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			}}},
		},
	}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "n1",
			Labels: map[string]string{acceleratorfit.GPUModelLabel: "l4"},
		},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
		},
	}
	in := EvalInputFromAttempt(st, pod, node, "n1", placementchecks.NodeResourceView{
		Allocatable: node.Status.Allocatable, AllocatedScalar: map[string]int64{},
	})
	_ = st.EnsureEvaluated(in)

	ctx := WithAttempt(context.Background(), st)
	models, ok := CachedModelFit(ctx, "n1")
	if !ok || len(models) == 0 {
		t.Fatalf("expected cached model results ok=%v n=%d", ok, len(models))
	}
	if models[0].OK {
		t.Fatalf("expected model mismatch on cached result")
	}
	if st.EvalCountForTest("n1") != 1 {
		t.Fatalf("eval count drifted: %d", st.EvalCountForTest("n1"))
	}
	// Second cache read must not re-evaluate.
	_, _ = CachedModelFit(ctx, "n1")
	if st.EvalCountForTest("n1") != 1 {
		t.Fatalf("cache read caused re-eval: %d", st.EvalCountForTest("n1"))
	}
}

func TestCachedPolicyPartsReusesAttemptReport(t *testing.T) {
	cfg := placementchecks.DefaultConfig()
	st := NewAttemptState(placementchecks.AttemptMeta{
		AttemptID: "a2", StartedAt: time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC),
	}, placementchecks.Snapshot{Nodes: map[string]placementchecks.NodeTelemetry{}}, placementchecks.ParsedRequest{}, cfg)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: v1.PodSpec{
			NodeName: "other",
			Containers: []v1.Container{{Name: "c", Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{"cpu": resource.MustParse("1")},
			}}},
		},
	}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	in := EvalInputFromAttempt(st, pod, node, "n1", placementchecks.NodeResourceView{})
	_ = st.EnsureEvaluated(in)

	ctx := WithAttempt(context.Background(), st)
	parts, ok := CachedPolicyParts(ctx, "n1")
	if !ok {
		t.Fatal("expected cached policy parts")
	}
	r, found := FindPolicyPart(parts, "node_name")
	if !found || r.OK {
		t.Fatalf("expected node_name mismatch: found=%v ok=%v", found, r.OK)
	}
}
