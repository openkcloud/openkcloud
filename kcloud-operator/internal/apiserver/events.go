// ============================================================
// events.go: 가속기 관련 Kubernetes Event 조회(/api/v1/events)
// 상세: History/Audit 화면의 데이터원이다. 별도 감사 저장소를 두지 않고
//       Kubernetes Event 를 그대로 쓴다 — 저장소가 둘이면 기록 기준이 둘로 갈린다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"net/http"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// acceleratorKinds 는 이 API 가 보여주는 이벤트의 involvedObject.kind 집합이다.
// 클러스터 전체 이벤트를 그대로 흘리면 화면이 Pod 스케줄 이벤트로 덮인다.
var acceleratorKinds = map[string]bool{
	"AcceleratorPartitionPolicy": true,
	"AcceleratorWorkload":        true,
	"NPUClusterPolicy":           true,
	"DriverInstallPolicy":        true,
	"DriverUpgradeState":         true,
	"NodeDeviceReport":           true,
}

// EventView 는 이벤트 하나의 투영이다.
type EventView struct {
	Namespace     string      `json:"namespace,omitempty"`
	Kind          string      `json:"kind"`
	Name          string      `json:"name"`
	Type          string      `json:"type,omitempty"`
	Reason        string      `json:"reason,omitempty"`
	Message       string      `json:"message,omitempty"`
	Count         int32       `json:"count,omitempty"`
	LastTimestamp metav1.Time `json:"lastTimestamp,omitempty"`
}

// eventTime 은 정렬 기준 시각이다. lastTimestamp → eventTime → creationTimestamp 순으로
// 있는 것을 쓴다(Events v1 로 기록된 이벤트는 lastTimestamp 가 비어 있다).
func eventTime(e corev1.Event) metav1.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp
	}
	if !e.EventTime.IsZero() {
		return metav1.NewTime(e.EventTime.Time)
	}
	return e.CreationTimestamp
}

// BuildEvents 는 가속기 관련 이벤트만 최신순으로 접는다(순수 함수). 시각이 같으면 이름으로
// 타이를 깨 총순서를 만든다(offset 페이지 걷기가 안정되려면 필수).
func BuildEvents(events []corev1.Event) []EventView {
	kept := make([]corev1.Event, 0, len(events))
	for i := range events {
		if acceleratorKinds[events[i].InvolvedObject.Kind] {
			kept = append(kept, events[i])
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ti, tj := eventTime(kept[i]), eventTime(kept[j])
		if !ti.Equal(&tj) {
			return tj.Before(&ti)
		}
		return kept[i].Name < kept[j].Name
	})
	out := make([]EventView, 0, len(kept))
	for i := range kept {
		e := &kept[i]
		out = append(out, EventView{
			Namespace:     e.InvolvedObject.Namespace,
			Kind:          e.InvolvedObject.Kind,
			Name:          e.InvolvedObject.Name,
			Type:          e.Type,
			Reason:        e.Reason,
			Message:       e.Message,
			Count:         e.Count,
			LastTimestamp: eventTime(*e),
		})
	}
	return out
}

// handleEvents 는 가속기 관련 이벤트 목록이다. ?kind= 로 한 종류만 볼 수 있다.
//
// 이 엔드포인트는 core "events" 의 list 권한을 요구한다(SAR). operator ServiceAccount 도
// 실제로 읽을 수 있어야 하므로 server.go 의 +kubebuilder:rbac 마커가 ClusterRole 에
// get;list;watch 를 추가한다 — 마커만으로는 배포되지 않는다. deploy/helm/templates/rbac.yaml
// 까지 3계층 확인 필수.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	var list corev1.EventList
	if err := s.Reader.List(r.Context(), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := BuildEvents(list.Items)
	if k := r.URL.Query().Get("kind"); k != "" {
		items = filterSlice(items, func(v EventView) bool { return v.Kind == k })
	}
	limit, offset := pageParams(r)
	writePage(w, items, limit, offset)
}
