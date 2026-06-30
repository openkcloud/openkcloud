// ============================================================
// npuclusterpolicy_partition_test.go: RngdSpec.PartitionPolicy enum 검증 단위 테스트
// 상세: kubebuilder Enum annotation 으로 정의된 RngdSpec.PartitionPolicy 의
//       valid/invalid 값 및 omitempty 기본 동작 검증
// 생성일: 2026-04-29
// ============================================================

package v1alpha1

import (
	"testing"
)

// rngdPartitionPolicyValidValues 는 npuclusterpolicy_types.go 의
// +kubebuilder:validation:Enum annotation 에 선언된 허용 값과 동일하게 유지한다.
// annotation 이 변경될 경우 이 슬라이스도 함께 갱신해야 한다.
var rngdPartitionPolicyValidValues = []string{
	"none",
	"single-core",
	"dual-core",
	"quad-core",
}

// isValidPartitionPolicy 는 kubebuilder Enum annotation 로직을 단위 테스트 내에서
// 재현한다 (CRD 미빌드 환경에서도 동작 가능).
func isValidPartitionPolicy(v string) bool {
	for _, valid := range rngdPartitionPolicyValidValues {
		if v == valid {
			return true
		}
	}
	return false
}

// TestRngdPartitionPolicy_ValidValues 는 kubebuilder Enum 허용 값이 모두 통과함을 검증한다.
func TestRngdPartitionPolicy_ValidValues(t *testing.T) {
	cases := []string{"none", "single-core", "dual-core", "quad-core"}
	for _, v := range cases {
		if !isValidPartitionPolicy(v) {
			t.Errorf("유효한 partitionPolicy 값이 거부됨: %q", v)
		}
	}
}

// TestRngdPartitionPolicy_InvalidValue 는 허용되지 않는 값이 거부됨을 검증한다.
func TestRngdPartitionPolicy_InvalidValue(t *testing.T) {
	invalidCases := []string{
		"invalid",
		"NONE",
		"Single-Core",
		"half-core",
		"8core",
		"any",
	}
	for _, v := range invalidCases {
		if isValidPartitionPolicy(v) {
			t.Errorf("invalid partitionPolicy 값이 허용됨: %q", v)
		}
	}
}

// TestRngdPartitionPolicy_EmptyIsOmitted 는 PartitionPolicy="" 일 때 RngdSpec 이
// 정상 초기화(zero value) 됨을 검증한다 (omitempty — 필드 생략 시 기본값 "none" 동작).
func TestRngdPartitionPolicy_EmptyIsOmitted(t *testing.T) {
	spec := RngdSpec{}
	if spec.PartitionPolicy != "" {
		t.Errorf("RngdSpec zero value 에서 PartitionPolicy 가 빈 문자열이어야 하나 %q 임", spec.PartitionPolicy)
	}
	// 빈 문자열은 enum 검증에서 제외 (omitempty: 필드 자체가 생략됨)
	// kubebuilder validation 은 값이 존재할 때만 enum 검사를 수행함
}

// TestRngdPartitionPolicy_DualCoreAccepted 는 "dual-core" 가 유효한 값임을 명시적으로 검증한다
// (PoC Phase B 요구사항 명시 테스트).
func TestRngdPartitionPolicy_DualCoreAccepted(t *testing.T) {
	spec := RngdSpec{PartitionPolicy: "dual-core"}
	if !isValidPartitionPolicy(spec.PartitionPolicy) {
		t.Errorf("dual-core 가 partitionPolicy enum 에서 거부됨")
	}
}
