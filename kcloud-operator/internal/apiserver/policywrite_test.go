// ============================================================
// policywrite_test.go: 정책 생성·삭제 핸들러 단위 테스트
// 상세: 조립 규칙은 pkg/npuctl 이 검사한다. 여기서는 핸들러가 맡은 세 갈래만 본다 —
//       본문 파싱 실패, 이름 없는 삭제, dryRun 질의 해석(적용 여부가 갈리는 지점).
// 생성일: 2026-08-11
// ============================================================

package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// decodeCreate 는 응답 본문을 CreatePolicyResponse 로 읽는다.
func decodeCreate(t *testing.T, w *httptest.ResponseRecorder) CreatePolicyResponse {
	t.Helper()
	var got CreatePolicyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("응답이 CreatePolicyResponse 가 아니다: %v (본문 %s)", err, w.Body.String())
	}
	return got
}

const validPolicyBody = `{"name":"pw-1","vendor":"nvidia","nodeName":"n1",` +
	`"intent":"shared","sharing":"timeSliced","replicas":2}`

// 깨진 본문은 400 이어야 한다. 200 + rejection 으로 내면 화면이 "거절"과 "말이 안 되는 요청"을
// 구별하지 못한다.
func TestPolicyWrite_본문_파싱_실패는_400(t *testing.T) {
	s, _ := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true})
	w := do(t, s, "POST", "/api/v1/policies", `{"name":`, "tok")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("깨진 JSON 은 400 이어야 한다: got %d (%s)", w.Code, w.Body.String())
	}
	got := decodeCreate(t, w)
	if got.Rejection == nil || got.Rejection.Reason != "BadRequest" {
		t.Fatalf("거절 사유가 BadRequest 여야 한다: %+v", got.Rejection)
	}
	if got.Created {
		t.Fatal("파싱 실패인데 created 가 참이다")
	}
}

// 이름 없는 삭제는 400 이며 클라이언트까지 내려가지 않는다. 라우트가 이름 없는 경로를
// 404 로 막지만, 핸들러 자신도 빈 이름으로 삭제를 시도하지 않아야 한다.
func TestPolicyWrite_이름_없는_삭제는_400(t *testing.T) {
	s, _ := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true})
	w := httptest.NewRecorder()
	s.handleDeletePolicy(w, httptest.NewRequest("DELETE", "/api/v1/policies/", http.NoBody))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("이름이 없으면 400 이어야 한다: got %d (%s)", w.Code, w.Body.String())
	}
	// 라우트 쪽도 이름 없는 삭제를 통과시키지 않는다.
	if rw := do(t, s, "DELETE", "/api/v1/policies/", "", "tok"); rw.Code != http.StatusNotFound {
		t.Fatalf("이름 없는 경로는 라우트에서 404 여야 한다: got %d", rw.Code)
	}
}

// 상태를 바꾸는 라우트는 누가 눌렀는지 감사 기록에 남겨야 한다 — 재부팅을 부를 수 있는
// 조작의 주체가 로그에서 빠지면 추적할 수 없다.
func TestPolicyWrite_상태_변경은_감사_기록을_남긴다(t *testing.T) {
	for _, name := range []string{"createPolicy", "deletePolicy"} {
		if !strings.Contains(policyWriteSource(t), `s.audit(r, "`+name+`"`) {
			t.Errorf("%s 경로에 감사 기록 호출이 없다", name)
		}
	}
	// dry-run 은 아무것도 만들지 않으므로 남기지 않는다 — 남기면 검사와 적용이 로그에서 섞인다.
	if !strings.Contains(policyWriteSource(t), "if !dry {") {
		t.Error("dry-run 을 감사 기록에서 갈라내지 않는다")
	}
}

// policyWriteSource 는 핸들러 원본을 읽는다. 감사 기록은 로그로만 나가 응답에 흔적이 없어
// httptest 로는 확인할 수 없다 — 호출부의 존재를 원본에서 고정한다.
func policyWriteSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("policywrite.go")
	if err != nil {
		t.Fatalf("policywrite.go 읽기 실패: %v", err)
	}
	return string(b)
}

// dryRun=true 는 판정만 받고 아무것도 남기지 않는다. 질의 해석이 뒤집히면 관문을
// 누르는 것만으로 정책이 적용된다.
func TestPolicyWrite_dryRun_은_남기지_않는다(t *testing.T) {
	s, crc := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true})
	w := do(t, s, "POST", "/api/v1/policies?dryRun=true", validPolicyBody, "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("dry-run 은 200 이어야 한다: got %d (%s)", w.Code, w.Body.String())
	}
	got := decodeCreate(t, w)
	if !got.DryRun || got.Created {
		t.Fatalf("dryRun=true 면 dryRun 참·created 거짓이어야 한다: %+v", got)
	}
	var acpp npuv1alpha1.AcceleratorPartitionPolicy
	if err := crc.Get(context.Background(), types.NamespacedName{Name: "pw-1"}, &acpp); err == nil {
		t.Fatal("dry-run 인데 정책이 클러스터에 남았다")
	}
}

// dryRun 없는 요청은 실제로 만든다 — 위 시험만 있으면 "아무것도 안 만드는 라우트"도 통과한다.
func TestPolicyWrite_dryRun_없으면_생성(t *testing.T) {
	s, crc := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true})
	w := do(t, s, "POST", "/api/v1/policies", validPolicyBody, "tok")
	got := decodeCreate(t, w)
	if !got.Created || got.DryRun || got.Name != "pw-1" {
		t.Fatalf("생성 응답이 아니다: %+v (본문 %s)", got, w.Body.String())
	}
	var acpp npuv1alpha1.AcceleratorPartitionPolicy
	if err := crc.Get(context.Background(), types.NamespacedName{Name: "pw-1"}, &acpp); err != nil {
		t.Fatalf("정책이 만들어져 있어야 한다: %v", err)
	}
	if acpp.Spec.Sharing == nil || acpp.Spec.Sharing.TimeSlicing.Replicas != 2 {
		t.Fatalf("요청한 공유 설정이 실려야 한다: %+v", acpp.Spec.Sharing)
	}
}
