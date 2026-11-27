// ============================================================
// import_test.go: 직접 변경 금지 계약의 컴파일 그래프 강제
// 상세: Health 가 장치를 바꿀 수 있는 패키지를 아예 참조하지 못하게 한다. 문서로 적어 둔 금지는
//
//	다음 사람이 한 줄 import 로 깬다 — 그래서 의존성으로 못 박는다.
//
// 생성일: 2026-08-04
// ============================================================
package health

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHealthDoesNotImportMutationPackages 는 health 가 변경 권한 패키지를 참조하지 않는지 본다.
func TestHealthDoesNotImportMutationPackages(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list 를 돌릴 수 없다(샌드박스 등): %v", err)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	banned := map[string]string{
		"kcloud-operator/internal/controller":     "컨트롤러(장치·DaemonSet 변경 경로)",
		"kcloud-operator/internal/partition":      "파티션 backend(하드웨어 직접 조작)",
		"kcloud-operator/internal/partition/rngd": "파티션 backend(하드웨어 직접 조작)",
	}
	for _, d := range deps {
		if why, bad := banned[strings.TrimSpace(d)]; bad {
			t.Errorf("health 가 %s 를 참조한다(%s) — 직접 변경 금지 계약 위반", d, why)
		}
		if strings.HasPrefix(d, "kcloud-operator/internal/partition/") {
			t.Errorf("health 가 %s 를 참조한다 — 파티션 backend 는 금지다", d)
		}
	}
}
