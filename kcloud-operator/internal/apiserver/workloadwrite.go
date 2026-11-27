// ============================================================
// workloadwrite.go: 배포 생성·삭제 요청 처리 — 거절은 200+rejection, 기존 정책 라우트와 같은 계약
// 상세: 요청을 파싱해 npuctl 에 넘기고 결과를 응답으로 옮기기만 한다. 조립·검증은
//       CRD 스키마와 webhook(internal/webhook/acceleratorworkload.go, 차트의
//       vacceleratorworkload.npu.ai)·AW 컨트롤러 몫이고 여기서 복제하지 않는다.
// 생성일: 2026-10-06 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// reasonRejected 는 webhook/admission 거절의 RejectionView.Reason 값이다. 정책 쓰기
// 경로(policywrite.go)와 같은 문자열이지만 그쪽은 다른 파일이라 상수를 공유하지 않는다 —
// goconst 중복 경고는 이 파일 쪽 호출부 하나를 상수로 좁혀 해소한다.
const reasonRejected = "Rejected"

// reasonBadRequest 는 입력 자체가 말이 안 될 때의 RejectionView.Reason 값이다(본문 파싱
// 실패·필수 필드 누락 등). authzNamespaced(server.go)의 네임스페이스 파싱 실패도 이 상수를 쓴다.
const reasonBadRequest = "BadRequest"

// CreateWorkloadRequest 는 배포 화면이 보내는 생성 요청이다.
type CreateWorkloadRequest struct {
	Namespace string                              `json:"namespace"`
	Name      string                              `json:"name"`
	Spec      npuv1alpha1.AcceleratorWorkloadSpec `json:"spec"`
}

// CreateWorkloadResponse 는 생성·삭제가 같은 모양을 돌려준다. Phase 는 생성 직후 status.phase 라
// 대개 비어 있다 — 번역 결과는 GET /api/v1/workloads 로 추적한다.
type CreateWorkloadResponse struct {
	Created   bool           `json:"created"`
	Namespace string         `json:"namespace,omitempty"`
	Name      string         `json:"name,omitempty"`
	UID       string         `json:"uid,omitempty"`
	Phase     string         `json:"phase,omitempty"`
	Rejection *RejectionView `json:"rejection,omitempty"`
}

// handleCreateWorkload 는 AcceleratorWorkload 를 만든다. 되돌리기 버튼의 짝인
// handleDeleteWorkload 와 같이 거절을 오류가 아니라 200+rejection 으로 낸다 — 정책
// 쓰기 라우트(handleCreatePolicy)와 같은 계약이다.
func (s *Server) handleCreateWorkload(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, CreateWorkloadResponse{Rejection: &RejectionView{Reason: reasonBadRequest, Message: err.Error()}})
		return
	}
	var req CreateWorkloadRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, CreateWorkloadResponse{Rejection: &RejectionView{Reason: reasonBadRequest, Axis: "요청 본문", Message: err.Error()}})
		return
	}
	if req.Namespace == "" || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, CreateWorkloadResponse{Rejection: &RejectionView{Reason: reasonBadRequest, Axis: "요청 본문", Message: "namespace 와 name 이 필요하다"}})
		return
	}
	aw := &npuv1alpha1.AcceleratorWorkload{ObjectMeta: metav1.ObjectMeta{Namespace: req.Namespace, Name: req.Name}, Spec: req.Spec}
	if err := s.Ctl.CreateAcceleratorWorkload(r.Context(), aw); err != nil {
		reason, axis := "InvalidRequest", "배포 입력"
		if apierrors.IsInvalid(err) || apierrors.IsForbidden(err) {
			reason, axis = reasonRejected, "배포 검증"
		}
		if apierrors.IsAlreadyExists(err) {
			reason, axis = "AlreadyExists", "이름"
		}
		writeJSON(w, http.StatusOK, CreateWorkloadResponse{Rejection: &RejectionView{Reason: reason, Axis: axis, Message: err.Error()}})
		return
	}
	s.audit(r, "createWorkload", req.Namespace+"/"+req.Name, fmt.Sprintf("class=%s mode=%s image=%s", req.Spec.Accelerator.Class, req.Spec.Accelerator.Access.Mode, req.Spec.Workload.Image))
	writeJSON(w, http.StatusOK, CreateWorkloadResponse{Created: true, Namespace: aw.Namespace, Name: aw.Name, UID: string(aw.UID), Phase: aw.Status.Phase})
}

// handleDeleteWorkload 는 배포를 지운다. 없는 대상을 지우면 오류가 아니라
// 200+rejection(NotFound) 으로 낸다 — 되돌리기를 두 번 눌러도 안전해야 한다.
func (s *Server) handleDeleteWorkload(w http.ResponseWriter, r *http.Request) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	if ns == "" || name == "" {
		writeJSON(w, http.StatusBadRequest, CreateWorkloadResponse{Rejection: &RejectionView{Reason: reasonBadRequest, Message: "namespace 와 name 이 필요하다"}})
		return
	}
	if err := s.Ctl.DeleteAcceleratorWorkload(r.Context(), ns, name); err != nil {
		reason := "DeleteFailed"
		if apierrors.IsNotFound(err) {
			reason = "NotFound"
		}
		writeJSON(w, http.StatusOK, CreateWorkloadResponse{Namespace: ns, Name: name, Rejection: &RejectionView{Reason: reason, Message: err.Error()}})
		return
	}
	s.audit(r, "deleteWorkload", ns+"/"+name, "")
	writeJSON(w, http.StatusOK, CreateWorkloadResponse{Namespace: ns, Name: name})
}
