// ============================================================
// preview_test.go: "이렇게 요청하면 어떻게 되는가" preview 테스트
// 상세: preview 는 아무것도 쓰지 않으며 거절도 200 이다. 거절 시 사용자가 고칠 축이
//       반드시 실려야 한다. 판정 자체는 intent 패키지의 테스트가 덮으므로 여기서는
//       "같은 함수를 부르고 결과를 손대지 않는다" 만 확인한다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"encoding/json"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
)

func previewObjects() []client.Object {
	return append(invObjects(), &v1alpha1.AcceleratorClass{
		ObjectMeta: metav1.ObjectMeta{Name: "whole-gpu"},
		Spec: v1alpha1.AcceleratorClassSpec{
			Class:    "large",
			Mappings: []v1alpha1.AcceleratorMapping{{Vendor: "nvidia"}},
		},
	})
}

func TestPreview_AcceptedExclusive(t *testing.T) {
	s, _ := newServer(t, okAuth(), previewObjects()...)
	body := `{"class":"whole-gpu","access":{"mode":"exclusive"}}`
	w := do(t, s, "POST", "/api/v1/preview", body, "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
	var got PreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	if !got.Accepted || got.Resolved == nil {
		t.Fatalf("exclusive 는 수락되어야 함: %#v", got)
	}
	if got.Resolved.ResourceName != "nvidia.com/gpu" || got.Resolved.Quantity != 1 {
		t.Fatalf("번역 결과를 손대면 안 됨: %#v", got.Resolved)
	}
}

func TestPreview_RejectedCarriesAxis(t *testing.T) {
	s, _ := newServer(t, okAuth(), previewObjects()...)
	// worker1 은 time-slicing 이 적용돼 있지 않으므로 shared 는 거절된다.
	body := `{"class":"whole-gpu","access":{"mode":"shared","replicas":4}}`
	w := do(t, s, "POST", "/api/v1/preview", body, "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("거절도 200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
	var got PreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	if got.Accepted || got.Rejection == nil {
		t.Fatalf("거절되어야 함: %#v", got)
	}
	if got.Rejection.Axis == "" {
		t.Fatalf("사용자가 고칠 축이 비어 있으면 안 됨: %#v", got.Rejection)
	}
	if got.Rejection.Reason == "" || got.Rejection.Message == "" {
		t.Fatalf("사유와 메시지가 모두 있어야 함: %#v", got.Rejection)
	}
}

func TestPreview_UnknownClassIsRejectionNot404(t *testing.T) {
	s, _ := newServer(t, okAuth(), previewObjects()...)
	w := do(t, s, "POST", "/api/v1/preview", `{"class":"nope","access":{"mode":"exclusive"}}`, "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("없는 클래스도 200 + rejection 이어야 함: got %d", w.Code)
	}
	var got PreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	if got.Accepted || got.Rejection == nil || got.Rejection.Reason != v1alpha1.AWReasonClassNotFound {
		t.Fatalf("ClassNotFound 여야 함: %#v", got)
	}
}

func TestPreview_MissingClass400(t *testing.T) {
	s, _ := newServer(t, okAuth(), previewObjects()...)
	if w := do(t, s, "POST", "/api/v1/preview", `{"access":{"mode":"exclusive"}}`, "tok"); w.Code != http.StatusBadRequest {
		t.Fatalf("class 누락은 400 이어야 함: got %d", w.Code)
	}
}

func TestPreview_BadJSON400(t *testing.T) {
	s, _ := newServer(t, okAuth(), previewObjects()...)
	if w := do(t, s, "POST", "/api/v1/preview", `{`, "tok"); w.Code != http.StatusBadRequest {
		t.Fatalf("깨진 JSON 은 400 이어야 함: got %d", w.Code)
	}
}

// preview 는 아무것도 쓰지 않는다 — 호출 후 AcceleratorWorkload 가 생기면 안 된다.
func TestPreview_WritesNothing(t *testing.T) {
	s, crc := newServer(t, okAuth(), previewObjects()...)
	do(t, s, "POST", "/api/v1/preview", `{"class":"whole-gpu","access":{"mode":"exclusive"}}`, "tok")
	var list v1alpha1.AcceleratorWorkloadList
	if err := crc.List(t.Context(), &list); err != nil {
		t.Fatalf("list 실패: %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("preview 가 워크로드를 만들었다: %d개", len(list.Items))
	}
}

// 이 라우트가 실제로 읽는 리소스(ACPP·NDR·AcceleratorClass) 중 어느 하나라도 권한이
// 없으면 403 이어야 한다("읽는 리소스는 전부 인가한다" 규칙).
func TestPreview_ForbiddenWithoutAuthz(t *testing.T) {
	for _, resource := range []string{"acceleratorpartitionpolicies", "nodedevicereports", "acceleratorclasses"} {
		opts := okAuth()
		opts.denyResources = map[string]bool{resource: true}
		s, _ := newServer(t, opts, previewObjects()...)
		body := `{"class":"whole-gpu","access":{"mode":"exclusive"}}`
		if w := do(t, s, "POST", "/api/v1/preview", body, "tok"); w.Code != http.StatusForbidden {
			t.Fatalf("%s 권한 없으면 403 이어야 함: got %d", resource, w.Code)
		}
	}
}
