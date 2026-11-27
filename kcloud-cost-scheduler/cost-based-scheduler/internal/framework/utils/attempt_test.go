package utils

import (
	"testing"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAttemptStateEvaluatesOncePerNode(t *testing.T) {
	cfg := placementchecks.DefaultConfig()
	st := NewAttemptState(placementchecks.AttemptMeta{
		AttemptID: "a1", StartedAt: time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC),
	}, placementchecks.Snapshot{Nodes: map[string]placementchecks.NodeTelemetry{}}, placementchecks.ParsedRequest{}, cfg)

	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Spec: v1.PodSpec{
		Containers: []v1.Container{{Name: "c", Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
		}}},
	}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: v1.NodeStatus{
		Allocatable: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
	}}
	in := EvalInputFromAttempt(st, pod, node, "n1", NodeResourceViewFromNodeInfo(NewNodeInfo()))
	in.Resources = NodeResourceViewFromNodeInfo(&NodeInfo{
		Requested: &Resource{},
	})
	// Allocatable from node status for count checks:
	in.Resources.Allocatable = node.Status.Allocatable

	_ = st.EnsureEvaluated(in)
	_ = st.EnsureEvaluated(in)
	if st.EvalCountForTest("n1") != 1 {
		t.Fatalf("eval count=%d want 1", st.EvalCountForTest("n1"))
	}
	rep, ok := st.Report("n1")
	if !ok || len(rep.Checks) != 7 {
		t.Fatalf("expected 7 checks cached: ok=%v checks=%d", ok, len(rep.Checks))
	}
}
