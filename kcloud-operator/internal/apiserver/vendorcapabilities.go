// ============================================================
// vendorcapabilities.go: GET /api/v1/vendors/capabilities — 벤더×능력 선언·검증 뷰
// 상세: /api/v1/capabilities(노드 스케줄 후보, 관측 상태)와는 다른 질문에 답한다.
//       CLI(kubectl npu capabilities)와 같은 pkg/npuctl 함수를 호출한다.
// 생성일: 2026-09-19
// ============================================================

package apiserver

import (
	"net/http"

	"kcloud-operator/pkg/npuctl"
)

func (s *Server) handleVendorCapabilities(w http.ResponseWriter, r *http.Request) {
	rows, err := npuctl.CollectVendorCapabilitiesWith(r.Context(), s.Reader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
