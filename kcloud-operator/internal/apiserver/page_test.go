// ============================================================
// page_test.go: 목록 봉투·페이지네이션·그룹 지정 인가 단위 테스트
// 상세: 잘못된 페이지 파라미터가 400 이 아니라 기본값으로 흡수되는지, 범위를 넘는 offset 이
//       빈 목록(오류 아님)인지, core 그룹 인가 거부 메시지가 그룹 접미 없이 나오는지 본다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPageParams_Defaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/accelerators", http.NoBody)
	limit, offset := pageParams(r)
	if limit != defaultLimit || offset != 0 {
		t.Fatalf("기본값이어야 함: got limit=%d offset=%d", limit, offset)
	}
}

func TestPageParams_GarbageIsDefault(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/accelerators?limit=abc&offset=-5", http.NoBody)
	limit, offset := pageParams(r)
	if limit != defaultLimit || offset != 0 {
		t.Fatalf("오타는 기본값으로 흡수되어야 함: got limit=%d offset=%d", limit, offset)
	}
}

func TestPageParams_ClampsToMax(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/accelerators?limit=99999", http.NoBody)
	if limit, _ := pageParams(r); limit != maxLimit {
		t.Fatalf("limit 은 %d 로 잘려야 함: got %d", maxLimit, limit)
	}
}

func TestSlicePage_OutOfRangeIsEmptyNotNil(t *testing.T) {
	got := slicePage([]int{1, 2, 3}, 10, 100)
	if got == nil || len(got) != 0 {
		t.Fatalf("범위 밖 offset 은 빈 슬라이스여야 함(nil 아님): %#v", got)
	}
}

func TestSlicePage_Window(t *testing.T) {
	got := slicePage([]int{1, 2, 3, 4, 5}, 2, 1)
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("[offset, offset+limit) 창이어야 함: %#v", got)
	}
}

func TestFilterSlice_DoesNotAliasInput(t *testing.T) {
	in := []int{1, 2, 3, 4}
	out := filterSlice(in, func(v int) bool { return v%2 == 0 })
	if len(out) != 2 || out[0] != 2 || out[1] != 4 {
		t.Fatalf("짝수만 남아야 함: %#v", out)
	}
	if in[0] != 1 || in[1] != 2 {
		t.Fatalf("입력이 덮어써짐: %#v", in)
	}
}

func TestQualifiedResource_CoreGroupHasNoSuffix(t *testing.T) {
	if got := qualifiedResource("", "events"); got != "events" {
		t.Fatalf("core 그룹은 접미가 없어야 함: %q", got)
	}
	if got := qualifiedResource("npu.ai", "acceleratorworkloads"); got != "acceleratorworkloads.npu.ai" {
		t.Fatalf("그룹 접미가 붙어야 함: %q", got)
	}
}

func TestAuthzGroup_ForbiddenMessageUsesGivenGroup(t *testing.T) {
	s, _ := newServer(t, authnOpts{authenticated: true, username: "u", allowed: false})
	h := s.authzGroup("", "list", "events", func(http.ResponseWriter, *http.Request) {})
	r := httptest.NewRequest("GET", "/api/v1/events", http.NoBody)
	r.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("무권한은 403 이어야 함: got %d", w.Code)
	}
	if body := w.Body.String(); !contains(body, `not allowed to list events`) {
		t.Fatalf("core 그룹 메시지에 events 가 그대로 나와야 함: %s", body)
	}
}

func TestWritePage_EnvelopeShape(t *testing.T) {
	w := httptest.NewRecorder()
	writePage(w, []int{1, 2, 3, 4, 5}, 2, 1)
	var env listEnvelope
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("응답 JSON 파싱 실패: %v", err)
	}
	if env.Total != 5 || env.Limit != 2 || env.Offset != 1 {
		t.Fatalf("봉투 메타데이터가 틀림: %#v", env)
	}
	items, ok := env.Items.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items 가 [offset,offset+limit) 창이어야 함: %#v", env.Items)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
