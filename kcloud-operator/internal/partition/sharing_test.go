// ============================================================
// sharing_test.go: 벤더 중립 sharing 계약 테스트
// 상세: 요청 검증 규칙(replicas 경계·모드 정합)과 미지원 backend 처리.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package partition

import (
	"errors"
	"testing"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

func TestValidateSharingLayoutRejectsBadReplicas(t *testing.T) {
	for _, tc := range []struct {
		name string
		l    SharingLayout
		ok   bool
	}{
		{"exclusive ignores replicas", SharingLayout{Mode: "exclusive"}, true},
		{"timeSliced replicas 1 is pointless", SharingLayout{Mode: "timeSliced", Replicas: 1}, false},
		{"timeSliced replicas 4 ok", SharingLayout{Mode: "timeSliced", Replicas: 4}, true},
		{"timeSliced replicas 0 missing", SharingLayout{Mode: "timeSliced"}, false},
		{"timeSliced over cap", SharingLayout{Mode: "timeSliced", Replicas: 17}, false},
		{"unknown mode", SharingLayout{Mode: "brokered"}, false},
	} {
		err := ValidateSharingLayout(tc.l)
		if tc.ok && err != nil {
			t.Fatalf("%s: unexpected err %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

// mps 는 timeSliced 와 같은 replica 규칙을 쓴다(2 이상, cap 이하). 모드를 몰라서 통과시키면
// DP 가 기동 실패한다 — 어떤 mutation 보다 먼저 fail-closed 해야 한다.
func TestValidateSharingLayout_MPS(t *testing.T) {
	if err := ValidateSharingLayout(SharingLayout{Mode: v1alpha1.SharingModeMPS, Replicas: 4}); err != nil {
		t.Errorf("유효한 mps 를 거부했다: %v", err)
	}
	if err := ValidateSharingLayout(SharingLayout{Mode: v1alpha1.SharingModeMPS, Replicas: 1}); err == nil {
		t.Error("replicas=1 은 공유가 아니다 — 거부해야 한다")
	}
	if err := ValidateSharingLayout(SharingLayout{Mode: v1alpha1.SharingModeMPS, Replicas: MaxSharingReplicas + 1}); err == nil {
		t.Error("cap 초과를 거부해야 한다")
	}
}

// mps 요청은 spec.Sharing.MPS.Replicas 를 읽어야 한다. 안 읽으면 Replicas 가 0으로 남아
// ValidateSharingLayout 이 "requires replicas >= 2, got 0" 로 항상 거부한다 — daemon 유무와
// 무관하게 mps 요청 자체가 apply 단계에 절대 도달 못 한다.
func TestSharingLayoutFrom_MPS(t *testing.T) {
	spec := v1alpha1.AcceleratorPartitionPolicySpec{
		Sharing: &v1alpha1.SharingSpec{Mode: v1alpha1.SharingModeMPS, MPS: &v1alpha1.MPSSpec{Replicas: 4}},
	}
	l := SharingLayoutFrom(spec)
	if l.Mode != v1alpha1.SharingModeMPS {
		t.Fatalf("mode = %q, want mps", l.Mode)
	}
	if l.Replicas != 4 {
		t.Fatalf("replicas = %d, want 4 (MPSSpec.Replicas 가 전달되지 않음)", l.Replicas)
	}
}

// SharingLayoutFrom 의 두 if 가 spec.Sharing.Mode 와 무관하게 독립으로 평가되면, CRD/webhook
// 어느 쪽도 TimeSlicing/MPS 상호배타를 막지 않으므로(둘 다 있어도 스키마 통과) mode 는
// timeSliced 인데 MPS.Replicas 가 조용히 TimeSlicing.Replicas 를 덮어쓸 수 있다 — 에러 없이,
// cap 이내라 ValidateSharingLayout 도 못 잡는다.
func TestSharingLayoutFrom_IgnoresFieldForDeclaredOtherMode(t *testing.T) {
	spec := v1alpha1.AcceleratorPartitionPolicySpec{
		Sharing: &v1alpha1.SharingSpec{
			Mode:        v1alpha1.SharingModeTimeSliced,
			TimeSlicing: &v1alpha1.TimeSlicingSpec{Replicas: 4, FailRequestsGreaterThanOne: true},
			MPS:         &v1alpha1.MPSSpec{Replicas: 16}, // 정리 안 된 잔여 필드 — mode 는 timeSliced.
		},
	}
	l := SharingLayoutFrom(spec)
	if l.Replicas != 4 {
		t.Fatalf("mode=timeSliced 인데 replicas=%d — MPS.Replicas(16)가 새어 들어왔다", l.Replicas)
	}
	if !l.FailRequestsGreaterThanOne {
		t.Fatalf("timeSlicing 필드가 안 읽혔다: %+v", l)
	}
}

func TestSharingUnsupportedErrorIsIdentifiable(t *testing.T) {
	err := fmtSharingUnsupported("furiosa")
	if !errors.Is(err, ErrSharingUnsupported) {
		t.Fatalf("err %v must wrap ErrSharingUnsupported", err)
	}
}
