// ============================================================
// policywrite.go: 공유·분할 정책 생성·삭제 요청 처리(POST/DELETE /api/v1/policies)
// 상세: 요청을 파싱해 npuctl 에 넘기고 결과를 응답으로 옮기기만 한다. 조립은
//       pkg/npuctl/policy.go 가, 적용 가능 여부는 reconciler 가 맡는다 — 여기에 복제하지 않는다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"kcloud-operator/pkg/npuctl"
)

// CreatePolicyResponse 는 dry-run 과 실제 생성이 같은 모양을 돌려준다 — 화면이 두 경로를
// 다르게 파싱하면 관문과 본판정이 갈라진다.
type CreatePolicyResponse struct {
	Created   bool           `json:"created"`
	DryRun    bool           `json:"dryRun"`
	Name      string         `json:"name,omitempty"`
	Rejection *RejectionView `json:"rejection,omitempty"`
}

// handleCreatePolicy 는 정책을 만든다. dryRun=true 가 태우는 것은 API 서버의
// admission(CRD 스키마·CEL 규칙과 AcceleratorPartitionPolicy 의 validating webhook)뿐이다 —
// 노드·장치가 그 방식을 실제로 할 수 있는가, profile 이 하드웨어에 실재하는가, 다른 정책이
// 이미 그 노드를 잡고 있는가는 보지 않는다. 그 판정은 reconciler 가 하며 못 하는 조합은
// ACPPPhaseUnsupported 로 떨어진다. 관문을 "적용 가능 여부 판정" 으로 소개하면 통과가
// 성공을 뜻한다고 오인하게 된다.
func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, CreatePolicyResponse{
			Rejection: &RejectionView{Reason: "BadRequest", Message: err.Error()}})
		return
	}
	var req npuctl.PolicyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, CreatePolicyResponse{
			Rejection: &RejectionView{Reason: "BadRequest", Axis: "요청 본문", Message: err.Error()}})
		return
	}
	dry := r.URL.Query().Get("dryRun") == "true"
	name, err := s.Ctl.CreatePartitionPolicy(r.Context(), req, dry)
	if err != nil {
		// 거절은 오류가 아니라 정상 응답이다(preview 와 같은 계약). 조립 실패든 webhook
		// 거절이든 사용자가 고칠 수 있는 정보라 200 + rejection 으로 낸다. 원문을 요약하지 않는다.
		reason, axis := "InvalidRequest", "정책 입력"
		if apierrors.IsInvalid(err) || apierrors.IsForbidden(err) {
			reason, axis = "Rejected", "정책 검증"
		}
		writeJSON(w, http.StatusOK, CreatePolicyResponse{DryRun: dry,
			Rejection: &RejectionView{Reason: reason, Axis: axis, Message: err.Error()}})
		return
	}
	if !dry {
		// 정책 생성은 재부팅을 부를 수 있는 조작이다 — 기존 제어 라우트와 같이 누가 눌렀는지
		// 남긴다. dry-run 은 아무것도 만들지 않으므로 남기지 않는다.
		s.audit(r, "createPolicy", name,
			fmt.Sprintf("vendor=%s node=%s intent=%s sharing=%s replicas=%d profile=%s",
				req.Vendor, req.NodeName, req.Intent, req.Sharing, req.Replicas, req.Profile))
	}
	writeJSON(w, http.StatusOK, CreatePolicyResponse{Created: !dry, DryRun: dry, Name: name})
}

// handleDeletePolicy 는 정책을 지운다. 되돌리기 버튼이 이 라우트를 부른다.
func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, CreatePolicyResponse{
			Rejection: &RejectionView{Reason: "BadRequest", Message: "정책 이름이 없다"}})
		return
	}
	if err := s.Ctl.DeletePartitionPolicy(r.Context(), name); err != nil {
		writeJSON(w, http.StatusOK, CreatePolicyResponse{
			Rejection: &RejectionView{Reason: "DeleteFailed", Message: err.Error()}})
		return
	}
	// 삭제는 광고를 되돌려 그 장치를 쓰던 워크로드에 영향을 준다 — 생성과 같이 남긴다.
	s.audit(r, "deletePolicy", name, "")
	writeJSON(w, http.StatusOK, CreatePolicyResponse{Name: name})
}
