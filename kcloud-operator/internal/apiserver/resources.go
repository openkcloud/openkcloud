// ============================================================
// resources.go: 추상 API CR 투영(/classes, /policies, /workloads)
// 상세: CR 원본을 그대로 내보내지 않고 화면이 쓰는 필드만 투영한다(managedFields·finalizer
//       등 노이즈 제거).
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"net/http"
	"regexp"
	"sort"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kcloud-operator/api/v1alpha1"
)

// axisPattern 은 intent.Reject.Error() 의 꼬리 "(axis: <축>)" 이다. AW 컨트롤러가 그 문자열을
// condition message 에 그대로 넣으므로 여기서 되짚는다(축을 따로 저장하는 필드는 없다).
var axisPattern = regexp.MustCompile(`\(axis: ([^)]+)\)\s*$`)

// axisFromMessage 는 메시지 꼬리에서 축을 꺼낸다. 못 찾으면 빈 문자열 — 호출부는 그럴 때도
// 원문 메시지를 그대로 노출한다(축이 없다고 사유를 지우지 않는다).
func axisFromMessage(msg string) string {
	m := axisPattern.FindStringSubmatch(msg)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// RejectionView 는 사용자가 고칠 수 있는 형태의 거절이다.
type RejectionView struct {
	Axis    string `json:"axis,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// ClassView 는 AcceleratorClass 투영이다.
type ClassView struct {
	Name         string                           `json:"name"`
	Class        string                           `json:"class,omitempty"`
	Requirements v1alpha1.AcceleratorRequirements `json:"requirements,omitempty"`
	Mappings     []v1alpha1.AcceleratorMapping    `json:"mappings,omitempty"`
}

func toClassView(c v1alpha1.AcceleratorClass) ClassView {
	return ClassView{
		Name: c.Name, Class: c.Spec.Class,
		Requirements: c.Spec.Requirements, Mappings: c.Spec.Mappings,
	}
}

// PolicyTargetView 는 정책이 노드 하나에 실제로 만든 결과다.
type PolicyTargetView struct {
	NodeName   string           `json:"nodeName"`
	Phase      string           `json:"phase,omitempty"`
	Profiles   []string         `json:"profiles,omitempty"`
	Advertised map[string]int32 `json:"advertised,omitempty"`
}

// PolicyView 는 AcceleratorPartitionPolicy 투영이다.
type PolicyView struct {
	Name               string            `json:"name"`
	Vendor             string            `json:"vendor,omitempty"`
	Phase              string            `json:"phase,omitempty"`
	NodeSelector       map[string]string `json:"nodeSelector,omitempty"`
	RequestedProfiles  []string          `json:"requestedProfiles,omitempty"`
	SharingMode        string            `json:"sharingMode"`
	SharingReplicas    int32             `json:"sharingReplicas,omitempty"`
	Generation         int64             `json:"generation"`
	ObservedGeneration int64             `json:"observedGeneration"`
	// Converged 는 observedGeneration==generation 이다. false 면 아래 status 값들은 아직
	// 이전 세대의 사실이므로 UI 가 "적용됨" 으로 렌더하면 안 된다.
	Converged bool               `json:"converged"`
	Targets   []PolicyTargetView `json:"targets,omitempty"`
}

func toPolicyView(p v1alpha1.AcceleratorPartitionPolicy) PolicyView {
	v := PolicyView{
		Name:               p.Name,
		Vendor:             p.Spec.Vendor,
		Phase:              p.Status.Phase,
		NodeSelector:       p.Spec.NodeSelector,
		SharingMode:        p.Spec.EffectiveSharingMode(),
		Generation:         p.Generation,
		ObservedGeneration: p.Status.ObservedGeneration,
		Converged:          p.Status.ObservedGeneration == p.Generation,
	}
	if p.Spec.Sharing != nil && p.Spec.Sharing.TimeSlicing != nil {
		v.SharingReplicas = p.Spec.Sharing.TimeSlicing.Replicas
	}
	for _, l := range p.Spec.Layout {
		if l.Profile != "" {
			v.RequestedProfiles = append(v.RequestedProfiles, l.Profile)
		}
	}
	for i := range p.Status.Targets {
		t := &p.Status.Targets[i]
		tv := PolicyTargetView{
			NodeName:   t.NodeName,
			Phase:      t.Phase,
			Advertised: t.Advertisement.AdvertisedResources,
		}
		for _, e := range t.ResolvedLayout {
			if e.Profile != "" {
				tv.Profiles = append(tv.Profiles, e.Profile)
			}
		}
		v.Targets = append(v.Targets, tv)
	}
	return v
}

// WorkloadView 는 AcceleratorWorkload 투영이다.
type WorkloadView struct {
	Namespace      string                       `json:"namespace"`
	Name           string                       `json:"name"`
	Class          string                       `json:"class"`
	Mode           string                       `json:"mode"`
	Implementation string                       `json:"implementation,omitempty"`
	AccessReplicas int32                        `json:"accessReplicas,omitempty"`
	Image          string                       `json:"image,omitempty"`
	Phase          string                       `json:"phase,omitempty"`
	Resolved       *v1alpha1.ResolvedAllocation `json:"resolved,omitempty"`
	Rejection      *RejectionView               `json:"rejection,omitempty"`
}

func toWorkloadView(aw v1alpha1.AcceleratorWorkload) WorkloadView {
	acc := aw.Spec.Accelerator
	v := WorkloadView{
		Namespace:      aw.Namespace,
		Name:           aw.Name,
		Class:          acc.Class,
		Mode:           acc.Access.Mode,
		Implementation: acc.Access.Implementation,
		AccessReplicas: acc.Access.Replicas,
		Image:          aw.Spec.Workload.Image,
		Phase:          aw.Status.Phase,
		Resolved:       aw.Status.Resolved,
	}
	// 구조 필드가 있으면 그것이 정본이다. 없으면(구버전 operator 가 쓴 객체) condition message
	// 꼬리에서 축을 되짚는 폴백을 쓴다 — 축이 없다고 사유를 지우지는 않는다.
	// Translated=False 만 거절이다. WorkloadReady=False 는 "번역은 됐지만 아직 안 떴다" 라
	// 사용자가 고칠 축이 없다 — 거절로 뭉개지 않는다.
	if r := aw.Status.Rejection; r != nil {
		v.Rejection = &RejectionView{Axis: r.Axis, Reason: r.Reason, Message: r.Message}
	} else if c := apimeta.FindStatusCondition(aw.Status.Conditions, v1alpha1.AWCondTranslated); c != nil && c.Status == metav1.ConditionFalse {
		v.Rejection = &RejectionView{
			Axis:    axisFromMessage(c.Message),
			Reason:  c.Reason,
			Message: c.Message,
		}
	}
	return v
}

func (s *Server) handleClasses(w http.ResponseWriter, r *http.Request) {
	var list v1alpha1.AcceleratorClassList
	if err := s.Reader.List(r.Context(), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]ClassView, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, toClassView(list.Items[i]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	limit, offset := pageParams(r)
	writePage(w, out, limit, offset)
}

func (s *Server) handlePolicies(w http.ResponseWriter, r *http.Request) {
	var list v1alpha1.AcceleratorPartitionPolicyList
	if err := s.Reader.List(r.Context(), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]PolicyView, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, toPolicyView(list.Items[i]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	limit, offset := pageParams(r)
	writePage(w, out, limit, offset)
}

func (s *Server) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	var list v1alpha1.AcceleratorWorkloadList
	if err := s.Reader.List(r.Context(), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]WorkloadView, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, toWorkloadView(list.Items[i]))
	}
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		out = filterSlice(out, func(v WorkloadView) bool { return v.Namespace == ns })
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	limit, offset := pageParams(r)
	writePage(w, out, limit, offset)
}
