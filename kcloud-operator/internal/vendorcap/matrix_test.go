// ============================================================
// matrix_test.go: 매트릭스 렌더가 golden 과 같은지
// 상세: 레지스트리를 바꾸면 golden 도 같이 갱신해야 통과한다(문서와 코드의 동기 강제).
// 생성일: 2026-09-19 | 수정일: 2026-10-11
// ============================================================

package vendorcap

import (
	"os"
	"strings"
	"testing"
)

func TestRenderMatrixMatchesGolden(t *testing.T) {
	got := RenderMatrix()
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("testdata 디렉터리 생성 실패: %v", err)
		}
		_ = os.WriteFile("testdata/compatibility_matrix.golden.md", []byte(got), 0o644)
		return
	}
	want, err := os.ReadFile("testdata/compatibility_matrix.golden.md")
	if err != nil {
		t.Fatalf("golden 읽기 실패(처음이면 UPDATE_GOLDEN=1 go test 로 생성): %v", err)
	}
	if got != string(want) {
		t.Fatalf("매트릭스가 golden 과 다르다 — 레지스트리 변경 시 UPDATE_GOLDEN=1 로 갱신하고 docs 도 재생성하라")
	}
}

func TestMatrixVerificationColumnComesFromSupport(t *testing.T) {
	// 매트릭스의 검증 열이 생성기 안의 별도 판정이 아니라 Support.Assurance() 에서 나와야
	// 한다. 두 곳에서 같은 판정을 내리면 한쪽만 바뀌어 매트릭스와 코드가 어긋난다.
	s, ok := Lookup("nvidia", "", CapTimeSlicing)
	if !ok {
		t.Fatalf("nvidia/timeSlicing 항목 누락")
	}
	md := RenderMatrix()
	line := findMatrixLine(t, md, "nvidia", "timeSlicing")
	if !strings.Contains(line, string(s.Assurance())) {
		t.Errorf("매트릭스 줄에 검증 수준 %q 가 없다: %s", s.Assurance(), line)
	}
}

func TestVerificationColumnShowsUnsupportedAssuranceButNotUnknown(t *testing.T) {
	// Unsupported 는 Unknown 과 다르다 — 아무것도 안 본 Unknown 과 달리 Unsupported 는
	// 하드웨어로 재현해 "지원 안 함" 을 확인했을 수 있다(tenstorrent timeSlicing 이
	// 실제 사례: co-run 실측으로 확인한 Unsupported). 검증 열이 Unsupported 를 Unknown
	// 과 똑같이 감추면 그 차이가 매트릭스에서 사라진다. 한쪽만 단언하면 다음 사람이
	// 반대쪽(Unknown 이 — 대신 값을 보이는 회귀)을 눈치 못 채므로 둘 다 확인한다.
	unsupported, ok := Lookup("tenstorrent", "", CapTimeSlicing)
	if !ok {
		t.Fatalf("tenstorrent/timeSlicing 항목 누락")
	}
	if unsupported.State != StateUnsupported || unsupported.Assurance() != AssuranceHardware {
		t.Fatalf("tenstorrent timeSlicing = state %s assurance %s, want Unsupported/hardware", unsupported.State, unsupported.Assurance())
	}

	// 이 레지스트리에는 실제 Unknown 항목이 없다(설계 목표). 렌더 로직이 Unknown 을
	// Unsupported 와 다르게 보이는지는 여전히 지켜야 하므로, 기존 칸 하나를 이 테스트
	// 동안만 합성 Unknown 값으로 바꿔 확인하고 끝나면 되돌린다.
	const synthVendor, synthProduct = "rebellions", ""
	const synthCap = CapIsolation
	synthKey := Key{synthVendor, synthProduct}
	origSupport := registry[synthKey][synthCap]
	t.Cleanup(func() { registry[synthKey][synthCap] = origSupport })
	registry[synthKey][synthCap] = unknown("테스트 전용 합성 Unknown — 렌더 로직 확인 뒤 원복")

	md := RenderMatrix()
	if line := findMatrixLine(t, md, "tenstorrent", "timeSlicing"); !strings.Contains(line, "| Unsupported | hardware |") {
		t.Errorf("Unsupported 항목이 확인 수준(hardware)을 안 보인다: %s", line)
	}
	if line := findMatrixLine(t, md, synthVendor, string(synthCap)); !strings.Contains(line, "| Unknown | — |") {
		t.Errorf("Unknown 항목이 여전히 — 를 보이지 않는다: %s", line)
	}
}

func findMatrixLine(t *testing.T, md, vendor, capability string) string {
	t.Helper()
	for _, l := range strings.Split(md, "\n") {
		if strings.Contains(l, "| "+vendor+" ") && strings.Contains(l, "| "+capability+" ") {
			return l
		}
	}
	t.Fatalf("매트릭스에 %s/%s 줄이 없다", vendor, capability)
	return ""
}
