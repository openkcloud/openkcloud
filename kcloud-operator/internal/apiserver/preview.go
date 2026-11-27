// ============================================================
// preview.go: "이렇게 요청하면 어떻게 되는가" 미리보기(POST /api/v1/preview)
// 상세: Impact Analysis 화면의 데이터원이다. 쓰기는 없다 — SAR 도
//       읽기 등급(list)이다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

// PreviewRequest 는 AcceleratorWorkload.spec.accelerator 와 같은 모양이다(이미지·Pod 개수 없음).
type PreviewRequest struct {
	Class        string                            `json:"class"`
	Access       v1alpha1.AccessSpec               `json:"access"`
	Requirements *v1alpha1.AcceleratorRequirements `json:"requirements,omitempty"`
	Preferences  *v1alpha1.AcceleratorPreferences  `json:"preferences,omitempty"`
}

// PreviewResponse 는 번역 결과다. 거절도 200 으로 담긴다.
type PreviewResponse struct {
	Accepted  bool                         `json:"accepted"`
	Resolved  *v1alpha1.ResolvedAllocation `json:"resolved,omitempty"`
	Rejection *RejectionView               `json:"rejection,omitempty"`
}

// handlePreview 는 요청을 번역해 결과 또는 거절 축을 돌려준다. 판정은 intent.Translate 를
// 그대로 호출한다(컨트롤러·webhook 과 같은 함수) — 결정 로직을 복제하면 화면과 실제 결과가
// 갈라진다. 거절은 오류가 아니라 정상 응답이다(항상 200 + rejection).
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req PreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Class) == "" {
		writeError(w, http.StatusBadRequest, "class is required")
		return
	}
	// CRD 기본값(implementation=auto)은 평범한 구조체에는 적용되지 않는다. 비면 auto 로 채워
	// 컨트롤러 경로와 같은 입력이 되게 한다.
	if req.Access.Implementation == "" {
		req.Access.Implementation = v1alpha1.ImplementationAuto
	}

	var class v1alpha1.AcceleratorClass
	if err := s.Reader.Get(r.Context(), client.ObjectKey{Name: req.Class}, &class); err != nil {
		if apierrors.IsNotFound(err) {
			// 클래스가 없는 것은 요청 형식 오류가 아니라 "이 요청은 성립하지 않는다" 는
			// 판정이다 — 다른 거절과 같은 모양으로 돌려준다.
			writeJSON(w, http.StatusOK, PreviewResponse{Rejection: &RejectionView{
				Axis:    "spec.accelerator.class",
				Reason:  v1alpha1.AWReasonClassNotFound,
				Message: fmt.Sprintf("AcceleratorClass %q not found", req.Class),
			}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 임시 워크로드를 세워 BuildRequest 에 넘긴다 — 요구사항 병합 규칙(클래스와 워크로드 중
	// 강한 쪽이 이긴다)을 여기서 다시 구현하지 않기 위해서다. 이 값은 저장되지 않는다.
	aw := &v1alpha1.AcceleratorWorkload{Spec: v1alpha1.AcceleratorWorkloadSpec{
		Accelerator: v1alpha1.AcceleratorRequest{
			Class:        req.Class,
			Access:       req.Access,
			Requirements: req.Requirements,
			Preferences:  req.Preferences,
		},
	}}

	snap, dra, err := intent.Load(r.Context(), s.Reader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := intent.TranslateWithDRA(intent.BuildRequest(aw, &class), snap, dra)
	if err != nil {
		var rj *intent.Reject
		if errors.As(err, &rj) {
			writeJSON(w, http.StatusOK, PreviewResponse{Rejection: &RejectionView{
				Axis: rj.Axis, Reason: rj.Reason, Message: rj.Message,
			}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, PreviewResponse{Accepted: true, Resolved: res})
}
