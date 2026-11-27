// ============================================================
// policy_test.go: 실효 정책 해석 시험
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package health

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// TestDefaultPolicyIsUsableWithoutCRD 는 CRD 가 하나도 없어도 감시가 돌아야 함을 고정한다.
// 정책이 없으면 아무것도 안 보는 설계는 "정책을 안 만들면 조용히 감시가 꺼진다" 가 된다.
func TestDefaultPolicyIsUsableWithoutCRD(t *testing.T) {
	p := Resolve(nil, map[string]string{"kcloud.ai/nvidia.present": "true"})
	if p.NDRFreshness <= 0 {
		t.Fatalf("기본 NDR 신선도 임계가 비었다: %+v", p)
	}
	if p.RecoveryCooldown <= 0 {
		t.Fatalf("기본 쿨다운이 비었다: %+v", p)
	}
	if !p.RemediationFor(ReasonDevicePluginDown).CreateOperation {
		t.Fatal("기본값에서 device-plugin 장애는 복구 작업을 만들어야 한다")
	}
	if p.RemediationFor(ReasonObservationStale).CreateOperation {
		t.Fatal("관측 부재만으로 파괴적 복구를 만들면 안 된다")
	}
}

// TestResolvePicksMatchingPolicyOnly 는 selector 가 맞는 정책만 반영되는지 본다.
func TestResolvePicksMatchingPolicyOnly(t *testing.T) {
	match := v1alpha1.AcceleratorHealthPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "b-match"},
		Spec: v1alpha1.AcceleratorHealthPolicySpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"gpu": "true"}},
			Checks:   v1alpha1.HealthChecks{NDRFreshnessSeconds: 120},
		},
	}
	other := v1alpha1.AcceleratorHealthPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "a-other"},
		Spec: v1alpha1.AcceleratorHealthPolicySpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"gpu": "false"}},
			Checks:   v1alpha1.HealthChecks{NDRFreshnessSeconds: 999},
		},
	}
	p := Resolve([]v1alpha1.AcceleratorHealthPolicy{other, match}, map[string]string{"gpu": "true"})
	if p.NDRFreshness != 120*time.Second {
		t.Fatalf("NDRFreshness = %v, want 120s (맞는 정책만 반영)", p.NDRFreshness)
	}
}

// TestRemediationOverrideWins 는 정책이 적은 조치가 기본 조치를 덮는지 본다.
func TestRemediationOverrideWins(t *testing.T) {
	pol := v1alpha1.AcceleratorHealthPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "override"},
		Spec: v1alpha1.AcceleratorHealthPolicySpec{
			Remediation: map[string]v1alpha1.RemediationRule{
				ReasonDevicePluginDown: {Action: v1alpha1.RemediationStopNewAllocation},
			},
		},
	}
	p := Resolve([]v1alpha1.AcceleratorHealthPolicy{pol}, nil)
	r := p.RemediationFor(ReasonDevicePluginDown)
	if r.CreateOperation {
		t.Fatal("정책이 배치 차단만 지시했는데 복구 작업을 만들려 한다")
	}
	if !r.StopAllocation {
		t.Fatal("배치 차단이 반영되지 않았다")
	}
}

// TestMonitorIntervalDefaultsAndOverride 는 감시 주기가 기본값을 갖고 정책으로 낮춰지는지 본다.
// 기본 30초로는 자가치유가 빠른 장애를 아예 관측하지 못한다.
func TestMonitorIntervalDefaultsAndOverride(t *testing.T) {
	if got := DefaultPolicy().MonitorInterval; got != 30*time.Second {
		t.Fatalf("기본 감시 주기 = %v, want 30s", got)
	}
	p := Resolve([]v1alpha1.AcceleratorHealthPolicy{{
		ObjectMeta: metav1.ObjectMeta{Name: "fast"},
		Spec: v1alpha1.AcceleratorHealthPolicySpec{
			Checks: v1alpha1.HealthChecks{MonitorIntervalSeconds: 5},
		},
	}}, nil)
	if p.MonitorInterval != 5*time.Second {
		t.Fatalf("감시 주기 = %v, want 5s (정책이 낮춘 값이 반영돼야 한다)", p.MonitorInterval)
	}
}
