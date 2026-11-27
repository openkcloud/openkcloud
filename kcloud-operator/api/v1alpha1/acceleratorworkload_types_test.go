// ============================================================
// acceleratorworkload_types_test.go: AcceleratorWorkload 타입 회귀 테스트
// 상세: scheme 등록, 추상 모드 상수 값 고정(문자열이 API 계약이다), status JSON 태그.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestAcceleratorWorkloadSchemeRegistered(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	kinds, _, err := s.ObjectKinds(&AcceleratorWorkload{})
	if err != nil {
		t.Fatalf("ObjectKinds: %v", err)
	}
	if kinds[0].Kind != "AcceleratorWorkload" || kinds[0].Group != "npu.ai" {
		t.Fatalf("unexpected gvk %v", kinds[0])
	}
	if _, _, err := s.ObjectKinds(&AcceleratorWorkloadList{}); err != nil {
		t.Fatalf("list ObjectKinds: %v", err)
	}
}

// 모드 문자열은 사용자 YAML 에 그대로 적히는 API 계약이다 — 오타 리팩터링을 막는다.
// (ImplementationAuto 와 AllocationAPIAuto 는 둘 다 값이 "auto" 라 map[string]string 리터럴
// 키로 함께 쓰면 중복 키 컴파일 에러가 나서 슬라이스로 비교한다.)
func TestAccessModeConstants(t *testing.T) {
	type pair struct{ got, want string }
	for _, p := range []pair{
		{AccessModeExclusive, "exclusive"},
		{AccessModeShared, "shared"},
		{AccessModePartitioned, "partitioned"},
		{AccessModePartitionedShared, "partitioned-shared"},
		{ImplementationAuto, "auto"},
		{ImplementationTimeSlicing, "timeSlicing"},
		{ImplementationMultiProcess, "multiProcess"},
		{ImplementationBrokered, "brokered"},
		{AllocationAPIAuto, "auto"},
	} {
		if p.got != p.want {
			t.Fatalf("constant %q != %q", p.got, p.want)
		}
	}
}

func TestAcceleratorWorkloadSpecJSONTags(t *testing.T) {
	reps := int32(2)
	aw := AcceleratorWorkload{Spec: AcceleratorWorkloadSpec{
		Accelerator: AcceleratorRequest{
			Class:       "inference-medium",
			Access:      AccessSpec{Mode: AccessModePartitionedShared, Implementation: ImplementationAuto, Replicas: 2},
			Preferences: &AcceleratorPreferences{Vendors: []string{"furiosa", "nvidia"}, AllocationAPI: AllocationAPIAuto},
		},
		Workload: WorkloadTemplate{Image: "registry.example.com/inference:v1", Replicas: &reps, Command: []string{"python", "inference.py"}},
	}}
	b, err := json.Marshal(aw.Spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"accelerator":{"class":"inference-medium","access":{"mode":"partitioned-shared","implementation":"auto","replicas":2},"preferences":{"vendors":["furiosa","nvidia"],"allocationAPI":"auto"}},"workload":{"image":"registry.example.com/inference:v1","replicas":2,"command":["python","inference.py"]}}`
	if got := string(b); got != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestResolvedAllocationJSONTags(t *testing.T) {
	st := AcceleratorWorkloadStatus{
		Phase: AWPhaseTranslated,
		Resolved: &ResolvedAllocation{
			Vendor: "nvidia", Mode: AccessModePartitioned, ResourceName: "nvidia.com/mig-1g.6gb",
			Quantity: 1, AllocationAPI: AllocationAPIDevicePlugin, Nodes: []string{"worker1"}, Explanation: "x",
		},
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"phase":"Translated","resolved":{"vendor":"nvidia","mode":"partitioned","resourceName":"nvidia.com/mig-1g.6gb","quantity":1,"allocationAPI":"devicePlugin","nodes":["worker1"],"explanation":"x"}}`
	if got := string(b); got != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
}
