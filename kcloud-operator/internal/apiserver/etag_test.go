// ============================================================
// etag_test.go: ETag 조건부 조회 단위 테스트
// 상세: 같은 상태를 두 번 조회하면 둘째는 304·빈 본문, 상태가 바뀌면 ETag 도 바뀌는지 본다.
// 생성일: 2026-10-06 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kcloud-operator/api/v1alpha1"
)

func TestETag_SecondIdenticalGetIs304(t *testing.T) {
	s, _ := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true})
	first := do(t, s, "GET", "/api/v1/accelerators", "", "tok")
	if first.Code != http.StatusOK || first.Header().Get("ETag") == "" {
		t.Fatalf("첫 응답 code=%d etag=%q", first.Code, first.Header().Get("ETag"))
	}
	r := httptest.NewRequest("GET", "/api/v1/accelerators", nil)
	r.Header.Set("Authorization", "Bearer tok")
	r.Header.Set("If-None-Match", first.Header().Get("ETag"))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("같은 상태 재조회는 304·빈 본문이어야 한다: code=%d body=%d", w.Code, w.Body.Len())
	}
}

// 노드 하나만으로는 ACPP·NDR 이 없어 BuildInventory 가 행을 만들지 않는다(행이 0개면 cordon
// 전후 응답이 똑같이 빈 목록이라 ETag 도 안 바뀐다) — NDR 하나를 더해 행을 만든 뒤 cordon 한다.
func TestETag_ChangesWhenStateChanges(t *testing.T) {
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "n1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "a30", Count: 1},
		}},
	}
	s, crc := newServer(t, authnOpts{authenticated: true, username: "sa:admin", allowed: true}, &node, ndr)
	a := do(t, s, "GET", "/api/v1/accelerators", "", "tok").Header().Get("ETag")
	node.Spec.Unschedulable = true
	if err := crc.Update(context.Background(), &node); err != nil {
		t.Fatal(err)
	}
	b := do(t, s, "GET", "/api/v1/accelerators", "", "tok")
	if b.Code != http.StatusOK || b.Header().Get("ETag") == a {
		t.Fatalf("상태가 바뀌면 ETag 가 달라지고 200 이어야 한다: code=%d a=%q b=%q", b.Code, a, b.Header().Get("ETag"))
	}
}
