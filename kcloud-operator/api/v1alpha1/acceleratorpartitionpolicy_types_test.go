// ============================================================
// acceleratorpartitionpolicy_types_test.go: AcceleratorPartitionPolicy spec 필드 단위 테스트
// 상세: Layout profile/countPerDevice 필드가 올바르게 설정되는지 확인.
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import "testing"

func TestACPPSpec_Fields(t *testing.T) {
	p := AcceleratorPartitionPolicy{
		Spec: AcceleratorPartitionPolicySpec{
			Vendor:         "furiosa",
			Layout:         []PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
			DeletionPolicy: "Retain",
		},
	}
	if p.Spec.Layout[0].Profile != "2core.12gb" {
		t.Fatalf("profile mismatch: %q", p.Spec.Layout[0].Profile)
	}
	if p.Spec.Layout[0].CountPerDevice != 4 {
		t.Fatalf("countPerDevice mismatch: %d", p.Spec.Layout[0].CountPerDevice)
	}
}
