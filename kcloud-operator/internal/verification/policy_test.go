// ============================================================
// policy_test.go: 검증 정책 해석 테스트
// 상세: 내장 기본값, 노드 선택 매칭, 사전순 결정론, 부분 지정 시 기본값 승계를 고정한다.
// 생성일: 2026-07-31
// ============================================================
package verification

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

func avp(name string, sel map[string]string, mut func(*v1alpha1.AcceleratorVerificationPolicySpec)) v1alpha1.AcceleratorVerificationPolicy {
	p := v1alpha1.AcceleratorVerificationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.AcceleratorVerificationPolicySpec{NodeSelector: sel},
	}
	if mut != nil {
		mut(&p.Spec)
	}
	return p
}

func TestDefaultPolicyRunsThreeChecksAndSkipsTheProbe(t *testing.T) {
	p := DefaultPolicy()
	for _, c := range []string{
		v1alpha1.EvidenceCheckDeviceObservation,
		v1alpha1.EvidenceCheckNodeDeviceReport,
		v1alpha1.EvidenceCheckAdvertisement,
	} {
		if !p.Enabled(c) {
			t.Fatalf("기본값에서 %s 가 꺼져 있다", c)
		}
	}
	if p.Enabled(v1alpha1.EvidenceCheckAllocationProbe) {
		t.Fatalf("기본값에서 테스트 Pod 프로브가 켜져 있다 — 매 검증마다 Pod 를 띄우면 안 된다")
	}
	if p.TTL <= 0 || p.DriftGracePeriod <= 0 {
		t.Fatalf("기본 TTL/grace 가 비었다: %+v", p)
	}
}

func TestResolveWithNoPoliciesReturnsDefault(t *testing.T) {
	if got := Resolve(nil, map[string]string{"a": "b"}); got.TTL != DefaultPolicy().TTL {
		t.Fatalf("정책이 없으면 기본값이어야 한다: %+v", got)
	}
}

func TestResolveMatchesNodeLabels(t *testing.T) {
	list := []v1alpha1.AcceleratorVerificationPolicy{
		avp("strict", map[string]string{"role": "gpu"}, func(s *v1alpha1.AcceleratorVerificationPolicySpec) {
			s.Checks = []string{v1alpha1.EvidenceCheckDeviceObservation, v1alpha1.EvidenceCheckAllocationProbe}
			s.TTL = &metav1.Duration{Duration: 5 * time.Minute}
			s.InvalidateOn = []string{FieldFirmwareVersion}
			s.DriftGracePeriod = &metav1.Duration{Duration: 2 * time.Minute}
		}),
	}
	got := Resolve(list, map[string]string{"role": "gpu"})
	if !got.Enabled(v1alpha1.EvidenceCheckAllocationProbe) {
		t.Fatalf("매칭된 정책의 체크가 반영되지 않았다: %+v", got)
	}
	if got.Enabled(v1alpha1.EvidenceCheckAdvertisement) {
		t.Fatalf("정책이 checks 를 명시하면 그 목록이 전부다: %+v", got)
	}
	if got.TTL != 5*time.Minute {
		t.Fatalf("TTL = %v", got.TTL)
	}
	if len(got.InvalidateOn) != 1 || got.InvalidateOn[0] != FieldFirmwareVersion {
		t.Fatalf("매칭된 정책의 invalidateOn 이 반영되지 않았다: %+v", got.InvalidateOn)
	}
	if got.DriftGracePeriod != 2*time.Minute {
		t.Fatalf("매칭된 정책의 driftGracePeriod 가 반영되지 않았다: %v", got.DriftGracePeriod)
	}
	// 라벨이 안 맞으면 기본값.
	if other := Resolve(list, map[string]string{"role": "cpu"}); other.TTL != DefaultPolicy().TTL {
		t.Fatalf("비매칭 노드에 정책이 적용됐다: %+v", other)
	}
}

func TestResolveIsDeterministicByName(t *testing.T) {
	a := avp("aaa", nil, func(s *v1alpha1.AcceleratorVerificationPolicySpec) {
		s.TTL = &metav1.Duration{Duration: time.Minute}
	})
	z := avp("zzz", nil, func(s *v1alpha1.AcceleratorVerificationPolicySpec) {
		s.TTL = &metav1.Duration{Duration: time.Hour}
	})
	if got := Resolve([]v1alpha1.AcceleratorVerificationPolicy{z, a}, nil); got.TTL != time.Minute {
		t.Fatalf("사전순 첫 정책이 이겨야 한다: %v", got.TTL)
	}
}

func TestResolveCarriesDefaultsForUnsetFields(t *testing.T) {
	list := []v1alpha1.AcceleratorVerificationPolicy{
		avp("ttl-only", nil, func(s *v1alpha1.AcceleratorVerificationPolicySpec) {
			s.TTL = &metav1.Duration{Duration: 90 * time.Second}
		}),
	}
	got := Resolve(list, nil)
	if got.TTL != 90*time.Second {
		t.Fatalf("TTL = %v", got.TTL)
	}
	if !got.Enabled(v1alpha1.EvidenceCheckAdvertisement) {
		t.Fatalf("checks 미지정이면 기본 체크가 남아야 한다: %+v", got)
	}
	if got.DriftGracePeriod != DefaultPolicy().DriftGracePeriod {
		t.Fatalf("grace = %v", got.DriftGracePeriod)
	}
	if len(got.InvalidateOn) != len(DefaultPolicy().InvalidateOn) {
		t.Fatalf("invalidateOn = %v", got.InvalidateOn)
	}
}
