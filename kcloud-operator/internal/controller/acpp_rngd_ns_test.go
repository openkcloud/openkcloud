// ============================================================
// acpp_rngd_ns_test.go: RNGD 논리분할이 만지는 통합 DS 좌표 시험
// 상세: 통합 device-plugin 이 kcloud 로 옮겨간 뒤에도 파티션 경로가 같은 DS 를
//       가리키는지 확인한다. 좌표가 갈라지면 파티션 적용이 DS 를 못 찾는다.
// 생성일: 2026-08-12
// ============================================================

package controller

import "testing"

func TestRngdUnifiedDSCoordinatesFollowController(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	if rngdUnifiedDSName != furiosaUnifiedDSName {
		t.Errorf("DS 이름 불일치: acpp=%q ncp=%q", rngdUnifiedDSName, furiosaUnifiedDSName)
	}
	if rngdUnifiedDSNS() != furiosaUnifiedDSNamespace() {
		t.Errorf("DS 네임스페이스 불일치: acpp=%q ncp=%q", rngdUnifiedDSNS(), furiosaUnifiedDSNamespace())
	}
	if rngdUnifiedDSNS() != "kcloud" {
		t.Errorf("네임스페이스: got %q, want kcloud", rngdUnifiedDSNS())
	}
}
