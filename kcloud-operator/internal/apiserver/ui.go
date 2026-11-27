// ============================================================
// ui.go: 정적 대시보드 서빙(/ui/, /)
// 상세: 빌드 툴체인 없이 단일 HTML 을 embed 해서 낸다(air-gap: 배포 시점 npm install 불가).
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	_ "embed"
	"net/http"
)

//go:embed ui/index.html
var indexHTML []byte

// handleUI 는 대시보드 HTML 한 장을 낸다. 클러스터 데이터가 없는 응답이라 인증을 요구하지
// 않는다 — 브라우저가 주소창 요청에 Authorization 헤더를 싣지 못하기 때문이다. 데이터는
// 화면의 fetch 가 사용자 Bearer 토큰으로 /api/v1/* 를 호출해 가져오고, 그 경로가
// TokenReview/SAR 를 통과해 권한 상승은 없다(서버가 대신 읽어 주는 경로가 없다).
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 셸을 캐시하면 operator 업그레이드 후에도 옛 화면이 남는다.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(indexHTML)
}
