// ============================================================
// events_test.go: 가속기 관련 이벤트 조회 테스트
// 상세: 무관한 이벤트가 섞이지 않는지, 최신순 정렬인지, ?kind= 필터가 걸리는지 본다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"net/http"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ev(name, kind, objName, reason string, ts time.Time) corev1.Event {
	return corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: "kcloud"},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: objName, Namespace: "kcloud"},
		Reason:         reason,
		Message:        reason + " happened",
		Type:           corev1.EventTypeNormal,
		Count:          1,
		LastTimestamp:  metav1.NewTime(ts),
	}
}

func TestBuildEvents_FiltersAndSorts(t *testing.T) {
	base := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	in := []corev1.Event{
		ev("e1", "Pod", "some-pod", "Scheduled", base),                                // 무관 — 제외
		ev("e2", "AcceleratorPartitionPolicy", "gpu-policy", "Applied", base),         // 포함
		ev("e3", "AcceleratorWorkload", "train", "Rejected", base.Add(5*time.Minute)), // 포함(더 최신)
	}
	got := BuildEvents(in)
	if len(got) != 2 {
		t.Fatalf("가속기 관련 이벤트 2개만 남아야 함: got %d (%#v)", len(got), got)
	}
	if got[0].Name != "train" {
		t.Fatalf("최신순이어야 함: 첫 항목 %q", got[0].Name)
	}
	if got[0].Kind != "AcceleratorWorkload" || got[0].Reason != "Rejected" {
		t.Fatalf("필드가 어긋남: %#v", got[0])
	}
}

func TestEvents_EndpointAndKindFilter(t *testing.T) {
	base := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	e1 := ev("e1", "AcceleratorPartitionPolicy", "gpu-policy", "Applied", base)
	e2 := ev("e2", "AcceleratorWorkload", "train", "Rejected", base)
	s, _ := newServer(t, okAuth(), &e1, &e2)

	w := do(t, s, "GET", "/api/v1/events", "", "tok")
	if w.Code != http.StatusOK {
		t.Fatalf("200 이어야 함: got %d (%s)", w.Code, w.Body.String())
	}
	if _, total, _, _ := decodePage[EventView](t, w.Body.Bytes()); total != 2 {
		t.Fatalf("2개여야 함: total=%d", total)
	}

	w = do(t, s, "GET", "/api/v1/events?kind=AcceleratorWorkload", "", "tok")
	items, total, _, _ := decodePage[EventView](t, w.Body.Bytes())
	if total != 1 || len(items) != 1 || items[0].Kind != "AcceleratorWorkload" {
		t.Fatalf("kind 필터가 걸려야 함: total=%d items=%#v", total, items)
	}
}
