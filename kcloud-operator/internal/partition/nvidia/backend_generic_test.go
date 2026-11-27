// ============================================================
// backend_generic_test.go: 모델 미판정 표시(DriverModelUnknown)의 MPS 판정 거부 시험
// 상세: model 이 "generic" 이면 MIG 능력을 모른다는 뜻이라 MPS 지원을 단정하지 않아야 한다.
// 생성일: 2026-09-30
// ============================================================
package nvidia

import (
	"testing"

	"kcloud-operator/api/v1alpha1"
)

// TestMPSSupportForNonMigDeviceRefusesUnknownModel 은 모델이 미판정 표시일 때 판정을
// 거부하는지 본다 — 리터럴 "generic" 대신 v1alpha1.DriverModelUnknown 을 기준으로 삼는다.
func TestMPSSupportForNonMigDeviceRefusesUnknownModel(t *testing.T) {
	got := mpsSupportForNonMigDevice(modeUnknown, "", v1alpha1.DriverModelUnknown)
	if got.Supported || got.Reason != reasonMPSModeUnobserved {
		t.Fatalf("미판정 모델은 MPS 를 단정하지 않아야 한다: %+v", got)
	}
}
