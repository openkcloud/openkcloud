// ============================================================
// acceleratorpartitionpolicy_sharing_test.go: spec.sharing 요청 축 테스트
// 상세: 모드 상수 고정 + replicas 경계. CRD marker 검증은 envtest 에서 별도.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import "testing"

func TestSharingModesAreTwo(t *testing.T) {
	if SharingModeExclusive != "exclusive" || SharingModeTimeSliced != "timeSliced" {
		t.Fatalf("modes = %q/%q", SharingModeExclusive, SharingModeTimeSliced)
	}
}

func TestSpecSharingIsOptionalAndNilMeansExclusive(t *testing.T) {
	var s AcceleratorPartitionPolicySpec
	if s.Sharing != nil {
		t.Fatalf("sharing must default to nil (exclusive), got %+v", s.Sharing)
	}
	if s.EffectiveSharingMode() != SharingModeExclusive {
		t.Fatalf("EffectiveSharingMode() = %q, want exclusive", s.EffectiveSharingMode())
	}
}

func TestEffectiveSharingModeReadsSpec(t *testing.T) {
	s := AcceleratorPartitionPolicySpec{Sharing: &SharingSpec{Mode: SharingModeTimeSliced, TimeSlicing: &TimeSlicingSpec{Replicas: 4}}}
	if s.EffectiveSharingMode() != SharingModeTimeSliced {
		t.Fatalf("mode = %q", s.EffectiveSharingMode())
	}
}
