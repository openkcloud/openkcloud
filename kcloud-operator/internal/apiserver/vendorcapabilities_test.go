// ============================================================
// vendorcapabilities_test.go: /api/v1/vendors/capabilities 응답 시험
// 생성일: 2026-09-19
// ============================================================

package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/pkg/npuctl"
)

func TestHandleVendorCapabilitiesReturnsRegistryRows(t *testing.T) {
	scheme, _ := npuctl.Scheme()
	ev := &v1alpha1.AcceleratorEvidence{ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec: v1alpha1.AcceleratorEvidenceSpec{NodeName: "n1"}}
	s := &Server{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(ev).Build()}
	rec := httptest.NewRecorder()
	s.handleVendorCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/v1/vendors/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var rows []npuctl.VendorCapabilityRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Vendor == "rebellions" && r.Capability == "partition" && r.Declared == "Unsupported" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rebellions partition Unsupported 행이 없다: %d rows", len(rows))
	}

	// assurance 는 VendorCapabilityRow 를 그대로 인코딩해 REST 에 실린다(핸들러에 변환 코드가
	// 없다). 타입 디코드만으로는 json 태그가 바뀌어도(값이 빈 문자열로 새 조용히) 못 잡으므로
	// 원문 키 유무를 직접 본다.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("응답 행이 비었다")
	}
	if _, ok := raw[0]["assurance"]; !ok {
		t.Fatalf("응답에 assurance 키가 없다 — VendorCapabilityRow json 태그 확인: %s", rec.Body.String())
	}
}
