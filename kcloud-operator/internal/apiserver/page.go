// ============================================================
// page.go: 목록 응답 공통 규약(페이지네이션 봉투) + 그룹 지정 인가 헬퍼
// 상세: 새로 추가하는 모든 GET 목록 엔드포인트가 같은 봉투 {items,total,limit,offset} 를
//       쓴다. 기존 엔드포인트(status/nodes/{node}/vendors)의 응답 형태는 건드리지 않는다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"net/http"
	"strconv"
)

const (
	// defaultLimit: 파라미터가 없을 때 한 페이지 크기.
	defaultLimit = 100
	// maxLimit: 한 번에 직렬화할 상한. 서버가 전체를 집약한 뒤 자르므로 응답 크기만 제한하면 된다.
	maxLimit = 500
)

// listEnvelope 는 모든 목록 응답의 공통 형태다. total 은 필터 적용 후·페이지 적용 전 개수라
// UI 가 "N개 중 M개" 를 정확히 말할 수 있다.
type listEnvelope struct {
	Items  any `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// pageParams 는 ?limit=&offset= 를 읽는다. 파싱 실패·음수는 기본값으로 흡수한다.
func pageParams(r *http.Request) (limit, offset int) {
	limit, offset = defaultLimit, 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	return limit, offset
}

// slicePage 는 [offset, offset+limit) 을 잘라낸다. 범위를 넘으면 빈 슬라이스다(오류 아님).
// nil 이 아니라 빈 슬라이스를 돌려주는 이유는 JSON 이 null 대신 [] 로 나가야 하기 때문이다.
func slicePage[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// pageEnvelope 는 잘라낸 목록을 봉투 구조체로 만든다. writePage·writePageETag 가 공유해
// 봉투 조립 코드가 두 곳에 따로 생기지 않게 한다.
func pageEnvelope[T any](items []T, limit, offset int) listEnvelope {
	return listEnvelope{
		Items:  slicePage(items, limit, offset),
		Total:  len(items),
		Limit:  limit,
		Offset: offset,
	}
}

// writePage 는 잘라낸 목록을 봉투에 담아 200 으로 쓴다.
func writePage[T any](w http.ResponseWriter, items []T, limit, offset int) {
	writeJSON(w, http.StatusOK, pageEnvelope(items, limit, offset))
}

// writePageETag 는 writePage 와 같되 ETag 조건부 조회를 적용한다(writeJSONETag 참고).
func writePageETag[T any](w http.ResponseWriter, r *http.Request, items []T, limit, offset int) {
	writeJSONETag(w, r, http.StatusOK, pageEnvelope(items, limit, offset))
}

// filterSlice 는 새 슬라이스를 만들어 거른다(입력 배열을 덮어쓰지 않는다).
func filterSlice[T any](items []T, keep func(T) bool) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

// qualifiedResource 는 인가 실패 메시지에 쓰는 리소스 표기다. core 그룹("")은 접미를 붙이지
// 않는다 — "events." 같은 꼬리는 사용자가 고칠 RBAC 규칙을 오히려 헷갈리게 한다.
func qualifiedResource(group, resource string) string {
	if group == "" {
		return resource
	}
	return resource + "." + group
}
