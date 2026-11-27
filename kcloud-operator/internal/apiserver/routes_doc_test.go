// ============================================================
// routes_doc_test.go: /api 라우트 개수를 server.go 에서 세어 문서 표기와 고정
// 상세: 라우트 수가 바뀌면 문서(rest_api.md·status.md·03-objective-mapping.md)도 같이
//       갱신해야 한다는 계약을 지킨다.
// 생성일: 2026-09-21
// ============================================================

package apiserver

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// wantAPIRoutes 는 server.go 가 등록하는 /api/ 관리 라우트 개수다. UI 라우트(GET /ui/{$},
// GET /{$})는 세지 않는다. 라우트를 더하거나 빼면 이 상수와 문서(status.md·
// 03-objective-mapping.md 의 라우트 수 표기)를 함께 갱신해야 한다.
const wantAPIRoutes = 20

// apiRoutePattern 은 server.go 에서 관리 API 라우트 등록을 센다.
var apiRoutePattern = regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|DELETE|PUT|PATCH) /api/`)

// TestAPIRouteCount 는 server.go 의 라우트 등록 수를 상수와 대조하고, docs 저장소가 옆에
// 있으면 문서 표기와도 대조한다. 라우트 개수만 보며, openapi 스펙의 필드 구조가 어긋나는
// 것은 tester 저장소의 TestSpecMatchesServerRoutes 가 본다.
func TestAPIRouteCount(t *testing.T) {
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("server.go 읽기 실패: %v", err)
	}
	got := len(apiRoutePattern.FindAllIndex(raw, -1))
	if got != wantAPIRoutes {
		t.Fatalf("server.go 의 /api 라우트 수=%d, wantAPIRoutes=%d — 라우트를 더하거나 뺐으면 "+
			"이 상수와 문서(status.md·03-objective-mapping.md)를 함께 갱신하라",
			got, wantAPIRoutes)
	}

	t.Run("문서표기", func(t *testing.T) { checkDocRouteCounts(t, got) })
}

// docRouteAssertion 은 문서 한 곳의 라우트 개수 표기를 codeCount 와 대조할 대상이다. re 는
// 그 문장의 고정 부분(줄 번호가 밀려도 살아남는 문구)에 숫자를 붙여 잡는다 — 두 문서가
// 조사·단위를 다르게 써서("17종"·"17라우트") 공통 정규식 하나로는 못 잡고, status.md 쪽
// 줄은 같은 문장에 다른 숫자(과거 라우트 수 14, UI 종수 2 등)도 섞여 있어 그 앞 문구까지
// 함께 앵커링해야 엉뚱한 숫자를 집지 않는다.
type docRouteAssertion struct {
	path string         // ../../../docs 기준 상대 경로
	desc string         // 실패 메시지용 위치 설명
	re   *regexp.Regexp // 캡처 그룹 1 = 라우트 개수
}

var docRouteAssertions = []docRouteAssertion{
	{"status.md", "구현 상태 표 관리 API 행(:96 부근)",
		regexp.MustCompile(`관리 API 라우트\s*([0-9]+)\s*종`)},
	{"status.md", "openapi 각주(:369 부근)",
		regexp.MustCompile(`지금 관리 API 라우트는\s*([0-9]+)\s*종`)},
	{"research/03-objective-mapping.md", "공유 정책 설정 표(:143 부근)", // docs-path-check: ignore
		regexp.MustCompile(`REST API \|.*?([0-9]+)라우트`)},
	{"operations/rest_api.md", "드리프트 안내 문단(:957 부근)", // docs-path-check: ignore
		regexp.MustCompile(`라우트가\s*([0-9]+)\s*개가 됐다`)},
	{"operations/rest_api.md", "openapi.json 설명 행(:945 부근)", // docs-path-check: ignore
		regexp.MustCompile(`관리 API 라우트\s*([0-9]+)\s*개`)},
	{"operations/rest_api.md", "mock 모드 주석(:968 부근)", // docs-path-check: ignore
		regexp.MustCompile(`mock 모드 — 라이브 클러스터 없이 관리 API\s*([0-9]+)\s*라우트`)},
}

// extractDocNumber 는 docsDir 밑 a.path 파일을 읽어 a.re 로 라우트 개수를 뽑는다. 실패하면
// t.Errorf 로 기록하고 ok=false 를 돌린다(호출자가 이어서 비교할 수 없기 때문).
func extractDocNumber(t *testing.T, docsDir string, a docRouteAssertion) (n int, ok bool) {
	t.Helper()
	full := docsDir + "/" + a.path
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Errorf("%s 읽기 실패: %v", full, err)
		return 0, false
	}
	m := a.re.FindSubmatch(raw)
	if m == nil {
		t.Errorf("%s(%s) 에서 라우트 개수 표기를 찾지 못함 — 문구가 바뀌었으면 "+
			"routes_doc_test.go 의 정규식을 갱신하라", full, a.desc)
		return 0, false
	}
	n, err = strconv.Atoi(string(m[1]))
	if err != nil {
		t.Errorf("%s(%s) 의 라우트 개수 %q 를 숫자로 변환 실패: %v", full, a.desc, m[1], err)
		return 0, false
	}
	return n, true
}

// checkDocRouteCounts 는 docsDir(../../../docs)가 있을 때만 문서 표기를 codeCount 와
// 대조한다. docs 저장소가 없는 환경(CI)에서는 건너뛴다 — make capmatrix-check 와 같은
// 로컬 전용 검사다.
func checkDocRouteCounts(t *testing.T, codeCount int) {
	t.Helper()
	const docsDir = "../../../docs"
	if _, err := os.Stat(docsDir); err != nil {
		t.Skipf("docs 저장소 없음(%s) — 건너뜀", docsDir)
	}

	for _, a := range docRouteAssertions {
		n, ok := extractDocNumber(t, docsDir, a)
		if ok && n != codeCount {
			t.Errorf("%s/%s(%s) 라우트 표기=%d, server.go 실제 개수=%d — 문서를 코드에 맞춰 갱신하라",
				docsDir, a.path, a.desc, n, codeCount)
		}
	}
}
