package placementchecks

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func podWithGPU(memAnn string, gpu int64) *v1.Pod {
	p := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "t", Namespace: "ns",
			Annotations: map[string]string{},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name: "c",
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
			}},
		},
	}
	if gpu != 1 {
		p.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"] = resource.MustParse("2")
	}
	if memAnn != "" {
		p.Annotations[AnnotationGPUMemoryMiB] = memAnn
	}
	return p
}

func TestParseRequestMemoryMissing(t *testing.T) {
	r := ParseRequest(podWithGPU("", 1))
	if r.GPUCount != 1 {
		t.Fatalf("gpu count=%d", r.GPUCount)
	}
	if r.GPUMemoryMiB != nil {
		t.Fatalf("expected nil memory, got %v", *r.GPUMemoryMiB)
	}
}

func TestParseRequestMemoryValid(t *testing.T) {
	r := ParseRequest(podWithGPU("65536", 1))
	if r.GPUMemoryMiB == nil || *r.GPUMemoryMiB != 65536 {
		t.Fatalf("got %#v", r.GPUMemoryMiB)
	}
}

func TestParseRequestMemoryRejectsZeroFloat(t *testing.T) {
	for _, bad := range []string{"0", "-1", "1.5", "abc"} {
		r := ParseRequest(podWithGPU(bad, 1))
		if r.GPUMemoryMiB != nil {
			t.Fatalf("%q: expected nil budget", bad)
		}
		if r.GPUMemoryError == "" {
			t.Fatalf("%q: expected error detail", bad)
		}
	}
}
