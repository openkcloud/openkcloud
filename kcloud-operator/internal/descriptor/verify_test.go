// ============================================================
// verify_test.go: backend 실현 가능성 판정 시험
// 상세: 거절이 조용히 삼켜지지 않는지도 함께 본다 - 거절 사유가 비면 관리자가
//
//	왜 backend 가 안 나왔는지 되짚을 수 없다.
//
// 생성일: 2026-08-10
// ============================================================
package descriptor

import (
	"strings"
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func TestFeasibleBackendsAcceptsBoth(t *testing.T) {
	ok, refusals := FeasibleBackends(validSpec())
	if len(ok) != 2 {
		t.Fatalf("둘 다 가능해야 한다: %v (거절 %v)", ok, refusals)
	}
	if len(refusals) != 0 {
		t.Errorf("거절이 없어야 한다: %v", refusals)
	}
}

// 재부팅을 건너 안정하지 않은 식별자면 DRA 만 떨어진다.
func TestFeasibleBackendsRefusesDRAOnUnstableIdentity(t *testing.T) {
	s := validSpec()
	s.Identity.Source = npuv1alpha1.IdentitySourcePCIAddress
	s.Identity.StableAcrossReboot = false

	ok, refusals := FeasibleBackends(s)
	if len(ok) != 1 || ok[0] != npuv1alpha1.BackendDevicePlugin {
		t.Fatalf("device-plugin 만 남아야 한다: %v", ok)
	}
	if len(refusals) != 1 || refusals[0].Backend != npuv1alpha1.BackendDRA {
		t.Fatalf("DRA 거절 하나여야 한다: %v", refusals)
	}
	if !strings.Contains(refusals[0].Reason, "재부팅") {
		t.Errorf("거절 사유가 이유를 설명하지 않는다: %q", refusals[0].Reason)
	}
}

// 정합이 깨졌으면 둘 다 거절하고, 사유를 남긴다.
func TestFeasibleBackendsRefusesAllOnInvalidSpec(t *testing.T) {
	s := validSpec()
	s.Vendor = ""

	ok, refusals := FeasibleBackends(s)
	if len(ok) != 0 {
		t.Fatalf("전부 거절돼야 한다: %v", ok)
	}
	if len(refusals) != 2 {
		t.Fatalf("두 backend 모두 사유가 있어야 한다: %v", refusals)
	}
	for _, r := range refusals {
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("거절 사유가 비었다: %+v", r)
		}
	}
}

// 선언하지 않은 backend 는 가능해도 목록에 넣지 않는다.
func TestFeasibleBackendsHonorsDeclaredList(t *testing.T) {
	s := validSpec()
	s.Backends = []string{npuv1alpha1.BackendDRA}

	ok, _ := FeasibleBackends(s)
	if len(ok) != 1 || ok[0] != npuv1alpha1.BackendDRA {
		t.Fatalf("선언한 것만 나와야 한다: %v", ok)
	}
}
