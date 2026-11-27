// ============================================================
// workloadwrite_test.go: 배포 생성·삭제 핸들러 단위 테스트
// 상세: webhook 거절은 fake client 의 interceptor 로 흉내 낸다(newServerWithInterceptor).
//       인증·인가 분기는 newServer 로 충분해 거기서 쓴다.
// 생성일: 2026-10-06
// ============================================================

package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/pkg/npuctl"
)

// newServerWithInterceptor 는 newServer 와 같은 scheme·fake 인증을 쓰되, controller-runtime
// client 에 interceptor 를 끼워 webhook 거절(admission 실패)을 흉내 낸다. 인증·인가는 항상
// 허용(admin) 으로 고정한다 — 이 헬퍼를 쓰는 시험은 webhook 거절 자체가 관심사라 인가 축은
// 다른 시험(newServer 기반)이 이미 덮는다.
func newServerWithInterceptor(t *testing.T, funcs interceptor.Funcs) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("clientgoscheme: %v", err)
	}
	if err := npuv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("npuv1alpha1: %v", err)
	}
	crc := crfake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(funcs).
		Build()

	cs := k8sfake.NewSimpleClientset()
	cs.PrependReactor("create", "tokenreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authnv1.TokenReview{Status: authnv1.TokenReviewStatus{
			Authenticated: true,
			User:          authnv1.UserInfo{Username: "sa:admin"},
		}}, nil
	})
	cs.PrependReactor("create", "subjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authzv1.SubjectAccessReview{Status: authzv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})

	return &Server{Authn: cs, Ctl: npuctl.NewFromClient(crc), Reader: crc, Log: logr.Discard()}
}

func TestCreateWorkload_HappyPathReturnsIdentity(t *testing.T) {
	s, crc := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true})
	body := `{"namespace":"team-a","name":"infer-1","spec":{"accelerator":{"class":"inference-medium","access":{"mode":"exclusive"}},"workload":{"image":"example/infer:1"}}}`
	w := do(t, s, "POST", "/api/v1/workloads", body, "tok")
	var resp CreateWorkloadResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &resp) != nil || !resp.Created || resp.Namespace != "team-a" || resp.Name != "infer-1" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var got npuv1alpha1.AcceleratorWorkload
	if err := crc.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "infer-1"}, &got); err != nil {
		t.Fatalf("CR 이 만들어지지 않았다: %v", err)
	}
}

func TestCreateWorkload_WebhookRejectionIsReturnedVerbatim(t *testing.T) {
	msg := "exclusive 모드는 timeSlicing.replicas 를 받지 않는다"
	s := newServerWithInterceptor(t, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			return apierrors.NewInvalid(npuv1alpha1.GroupVersion.WithKind("AcceleratorWorkload").GroupKind(), obj.GetName(),
				field.ErrorList{field.Invalid(field.NewPath("spec", "accelerator", "access"), "exclusive", msg)})
		}})
	w := do(t, s, "POST", "/api/v1/workloads", `{"namespace":"a","name":"x","spec":{"accelerator":{"class":"c","access":{"mode":"exclusive"}},"workload":{"image":"i"}}}`, "tok")
	var resp CreateWorkloadResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.Created || resp.Rejection == nil ||
		resp.Rejection.Reason != reasonRejected || !strings.Contains(resp.Rejection.Message, msg) {
		t.Fatalf("거절은 200 + rejection 에 원문 그대로여야 한다: code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateWorkload_ForbiddenWithoutCreateVerb(t *testing.T) {
	s, _ := newServer(t, authnOpts{authenticated: true, username: "sa:viewer", allowed: true,
		denyResources: map[string]bool{"acceleratorworkloads": true}})
	if w := do(t, s, "POST", "/api/v1/workloads", `{"namespace":"a","name":"x","spec":{}}`, "tok"); w.Code != http.StatusForbidden {
		t.Fatalf("create 권한 없으면 403: %d", w.Code)
	}
}

func TestCreateWorkload_BadJSONIs400(t *testing.T) {
	s, _ := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true})
	if w := do(t, s, "POST", "/api/v1/workloads", `{not json`, "tok"); w.Code != http.StatusBadRequest {
		t.Fatalf("본문 파싱 실패는 400: %d", w.Code)
	}
}

func TestDeleteWorkload_RemovesAndReportsNotFound(t *testing.T) {
	aw := npuv1alpha1.AcceleratorWorkload{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "x"}}
	s, crc := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true}, &aw)
	if w := do(t, s, "DELETE", "/api/v1/workloads/a/x", "", "tok"); w.Code != http.StatusOK {
		t.Fatalf("삭제 code=%d body=%s", w.Code, w.Body.String())
	}
	if err := crc.Get(context.Background(), client.ObjectKey{Namespace: "a", Name: "x"}, &aw); !apierrors.IsNotFound(err) {
		t.Fatalf("삭제 뒤에도 남아 있다: %v", err)
	}
	w := do(t, s, "DELETE", "/api/v1/workloads/a/x", "", "tok")
	var resp CreateWorkloadResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || resp.Rejection == nil || resp.Rejection.Reason != "NotFound" {
		t.Fatalf("없는 대상 삭제는 200 + rejection NotFound: code=%d body=%s", w.Code, w.Body.String())
	}
}

// 상태를 바꾸는 라우트는 누가 눌렀는지 남겨야 한다 — policywrite_test.go 와 같은 가드다.
func TestWorkloadWrite_상태_변경은_감사_기록을_남긴다(t *testing.T) {
	for _, name := range []string{"createWorkload", "deleteWorkload"} {
		if !strings.Contains(workloadWriteSource(t), `s.audit(r, "`+name+`"`) {
			t.Errorf("%s 경로에 감사 기록 호출이 없다", name)
		}
	}
}

// workloadWriteSource 는 핸들러 원본을 읽는다 — 감사 기록은 로그로만 나가 응답에 흔적이
// 없어 httptest 로는 확인할 수 없다.
func workloadWriteSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("workloadwrite.go")
	if err != nil {
		t.Fatalf("workloadwrite.go 읽기 실패: %v", err)
	}
	return string(b)
}
