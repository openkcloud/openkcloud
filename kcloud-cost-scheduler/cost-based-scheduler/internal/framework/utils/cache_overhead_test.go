package utils

import (
	"testing"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/acceleratorfit"

	v1 "k8s.io/api/core/v1"
	apiResource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Formalized from REVIEW_P3_FINAL.md cache follow-up (P6 prerequisite).
func TestReviewP6CacheOverheadFollowup(t *testing.T) {
	p := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "overhead", Namespace: "review", UID: "overhead-review"},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name: "main",
				Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
					v1.ResourceCPU:    apiResource.MustParse("1"),
					v1.ResourceMemory: apiResource.MustParse("1Gi"),
				}},
			}},
			InitContainers: []v1.Container{{
				Name: "setup",
				Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
					v1.ResourceCPU:    apiResource.MustParse("2"),
					v1.ResourceMemory: apiResource.MustParse("2Gi"),
				}},
			}},
			Overhead: v1.ResourceList{
				v1.ResourceCPU:    apiResource.MustParse("500m"),
				v1.ResourceMemory: apiResource.MustParse("128Mi"),
			},
		},
	}
	want := acceleratorfit.ComputePodResourceRequest(p)
	nodeInfo := NewNodeInfo(p)
	got := nodeInfo.Requested
	if got.MilliCPU != want.MilliCPU || got.Memory != want.Memory {
		t.Fatalf("cache Requested=%dm/%d; ComputePodResourceRequest=%dm/%d",
			got.MilliCPU, got.Memory, want.MilliCPU, want.Memory)
	}
	if got.MilliCPU != 2500 || got.Memory != int64(2176*1024*1024) {
		t.Fatalf("cache allocated=%dm/%d bytes; want 2500m/2281701376", got.MilliCPU, got.Memory)
	}
}

func TestCacheAssumeRemoveIncludesOverhead(t *testing.T) {
	p := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", UID: "u1"},
		Spec: v1.PodSpec{
			NodeName: "n1",
			Containers: []v1.Container{{
				Name: "c",
				Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
					v1.ResourceCPU: apiResource.MustParse("1"),
				}},
			}},
			Overhead: v1.ResourceList{v1.ResourceCPU: apiResource.MustParse("500m")},
		},
	}
	n := NewNodeInfo()
	n.SetNode(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}})
	n.AddPod(p)
	if n.Requested.MilliCPU != 1500 {
		t.Fatalf("after add: %dm want 1500m", n.Requested.MilliCPU)
	}
	if err := n.RemovePod(p); err != nil {
		t.Fatal(err)
	}
	if n.Requested.MilliCPU != 0 {
		t.Fatalf("after remove: %dm want 0", n.Requested.MilliCPU)
	}
}
