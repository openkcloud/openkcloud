// ============================================================
// acceleratorclass_types_test.go: AcceleratorClass 타입 회귀 테스트
// 상세: scheme 등록, JSON 태그, DeepCopy 가 mappings/minimumMemory 를 공유하지 않는지 확인.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestAcceleratorClassSchemeRegistered(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	kinds, _, err := s.ObjectKinds(&AcceleratorClass{})
	if err != nil {
		t.Fatalf("ObjectKinds: %v", err)
	}
	if kinds[0].Kind != "AcceleratorClass" || kinds[0].Group != "npu.ai" {
		t.Fatalf("unexpected gvk %v", kinds[0])
	}
	if _, _, err := s.ObjectKinds(&AcceleratorClassList{}); err != nil {
		t.Fatalf("list ObjectKinds: %v", err)
	}
}

func TestAcceleratorClassJSONTags(t *testing.T) {
	mem := resource.MustParse("8Gi")
	ac := AcceleratorClass{Spec: AcceleratorClassSpec{
		Class:        "medium",
		Requirements: AcceleratorRequirements{MinimumIsolation: IsolationSubdevice, MinimumMemory: &mem},
		Mappings: []AcceleratorMapping{
			{Vendor: "nvidia", Product: "A30", NativeProfile: "2g.12gb"},
			{Vendor: "furiosa", Product: "rngd", NativeProfile: "2core.12gb"},
		},
	}}
	b, err := json.Marshal(ac.Spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	want := `{"class":"medium","requirements":{"minimumIsolation":"subdevice","minimumMemory":"8Gi"},"mappings":[{"vendor":"nvidia","product":"A30","nativeProfile":"2g.12gb"},{"vendor":"furiosa","product":"rngd","nativeProfile":"2core.12gb"}]}`
	if got != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestAcceleratorClassDeepCopyIsIndependent(t *testing.T) {
	mem := resource.MustParse("8Gi")
	src := &AcceleratorClass{Spec: AcceleratorClassSpec{
		Requirements: AcceleratorRequirements{MinimumMemory: &mem},
		Mappings:     []AcceleratorMapping{{Vendor: "nvidia"}},
	}}
	dst := src.DeepCopy()
	dst.Spec.Mappings[0].Vendor = "furiosa"
	if src.Spec.Mappings[0].Vendor != "nvidia" {
		t.Fatal("DeepCopy shares the mappings slice")
	}
	if dst.Spec.Requirements.MinimumMemory == src.Spec.Requirements.MinimumMemory {
		t.Fatal("DeepCopy shares the minimumMemory pointer")
	}
}
