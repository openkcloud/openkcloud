// ============================================================
// capabilities.go: 노드 단위 capability 조회(/api/v1/capabilities)
// 상세: AcceleratorCapability 의 조회 표면이다. 별도 CRD 대신 ACPP status 에
//       흡수한 축을 노드 기준으로 모은다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"fmt"
	"net/http"
	"sort"

	corev1 "k8s.io/api/core/v1"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

// DeviceCapabilityView 는 장치 하나의 capability 항목이다.
type DeviceCapabilityView struct {
	UID        string         `json:"uid"`
	Model      string         `json:"model,omitempty"`
	Capability CapabilityView `json:"capability"`
}

// NodeCapabilityView 는 노드 하나가 지금 무엇을 줄 수 있는가다.
type NodeCapabilityView struct {
	NodeName    string `json:"nodeName"`
	Schedulable bool   `json:"schedulable"`
	// Candidate 는 이 노드가 지금 번역(intent.Translate)의 후보가 될 수 있는가다.
	// false 면 CandidateReason 이 그 이유다 — UI 는 노드를 지우지 않고 이유를 보여준다.
	Candidate         bool                   `json:"candidate"`
	CandidateReason   string                 `json:"candidateReason,omitempty"`
	Vendor            string                 `json:"vendor,omitempty"`
	Advertised        map[string]int32       `json:"advertised,omitempty"`
	MemoryMiB         int64                  `json:"memoryMiB,omitempty"`
	SharingMode       string                 `json:"sharingMode"`
	SharingReplicas   int32                  `json:"sharingReplicas,omitempty"`
	PartitionProfiles []string               `json:"partitionProfiles,omitempty"`
	Policy            string                 `json:"policy,omitempty"`
	Devices           []DeviceCapabilityView `json:"devices,omitempty"`
	// DRADevices 는 이 노드가 DRA 로 내놓는 드라이버별 장치 수다. DRA 로만 광고하는 노드는
	// Advertised 도 Devices 도 비어 있어 candidate:true 의 근거가 화면에서 사라진다 — 후보인
	// 이유를 볼 수 있게 이 값을 낸다.
	DRADevices map[string]int32 `json:"draDevices,omitempty"`
}

// BuildNodeCapabilities 는 모든 노드를 capability 뷰로 접는다(순수 함수).
// 후보 판정은 intent.BuildSnapshot 과 정확히 같은 규칙을 쓴다 — 여기서 다시 판단하지 않고,
// 스냅샷에 나타나지 않은 노드의 사유만 이 함수가 붙인다. 장치 목록은 스냅샷의 nc.Devices 를
// 쓴다 — intent.applyACPPStatus 가 이미 수렴 게이트(generation/phase)를 통과시킨 값만 담기
// 때문이다(TargetStatus.Devices 원시값을 다시 읽으면 미수렴 노드의 낡은 장치가 새 나갈 수 있다).
// 탈락 사유는 intent.Reject 의 Axis/Reason 어휘를 재사용한다 — 별도 문자열 체계를 새로 만들지 않는다.
func BuildNodeCapabilities(nodes []corev1.Node, acpps []v1alpha1.AcceleratorPartitionPolicy, ndrs []v1alpha1.NodeDeviceReport, dra intent.DRACapability) []NodeCapabilityView {
	// cordon 을 지운 사본으로 스냅샷을 떠서 cordon 노드도 capability 를 갖게 한다.
	// cordon 자체는 아래에서 후보 탈락 사유로 따로 붙인다.
	snap := intent.BuildSnapshot(uncordonedCopy(nodes), acpps, ndrs, dra)
	byNode := make(map[string]intent.NodeCapability, len(snap))
	for i := range snap {
		byNode[snap[i].NodeName] = snap[i]
	}

	sorted := make([]corev1.Node, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	out := make([]NodeCapabilityView, 0, len(sorted))
	for i := range sorted {
		name := sorted[i].Name
		policy, _, _ := devicesForNode(acpps, name)
		v := NodeCapabilityView{
			NodeName:    name,
			Schedulable: !sorted[i].Spec.Unschedulable,
			SharingMode: v1alpha1.SharingModeExclusive,
			Policy:      policy,
		}
		nc, ok := byNode[name]
		switch {
		case !ok:
			v.Candidate = false
			v.CandidateReason = (&intent.Reject{Axis: intent.AxisCandidates, Reason: v1alpha1.AWReasonNoCandidateNodes,
				Message: fmt.Sprintf("node %s advertises no accelerator resource (device-plugin 이 이 노드에서 아무것도 광고하지 않는다)", name)}).Error()
		case nc.Stale:
			v.Candidate = false
			v.CandidateReason = (&intent.Reject{Axis: intent.AxisCandidates, Reason: v1alpha1.AWReasonCapabilityUnverified,
				Message: fmt.Sprintf("node %s: capability data is not trustworthy — %s", name, nc.StaleReason)}).Error()
		case sorted[i].Spec.Unschedulable:
			v.Candidate = false
			v.CandidateReason = (&intent.Reject{Axis: intent.AxisCandidates, Reason: v1alpha1.AWReasonNodeCordoned,
				Message: fmt.Sprintf("node %s is cordoned (unschedulable)", name)}).Error()
		default:
			v.Candidate = true
		}
		if ok {
			v.Vendor = nc.Vendor
			v.Advertised = nc.Advertised
			v.DRADevices = nc.DRADevices
			v.MemoryMiB = nc.MemoryMiB
			v.PartitionProfiles = nc.PartitionProfiles
			v.SharingReplicas = nc.SharingReplicas
			if nc.SharingMode != "" {
				v.SharingMode = nc.SharingMode
			}
			for _, d := range nc.Devices {
				v.Devices = append(v.Devices, DeviceCapabilityView{
					UID: d.ID, Model: d.Model, Capability: toCapabilityView(d),
				})
			}
		}
		out = append(out, v)
	}
	return out
}

// handleCapabilities 는 노드 단위 capability 목록이다.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	var nodes corev1.NodeList
	if err := s.Reader.List(r.Context(), &nodes); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("list nodes: %v", err))
		return
	}
	var acpps v1alpha1.AcceleratorPartitionPolicyList
	if err := s.Reader.List(r.Context(), &acpps); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("list acceleratorpartitionpolicies: %v", err))
		return
	}
	var ndrs v1alpha1.NodeDeviceReportList
	if err := s.Reader.List(r.Context(), &ndrs); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("list nodedevicereports: %v", err))
		return
	}
	// DRA 가용성은 intent.Load 가 판정한 것을 그대로 받는다(preview 와 같은 경로). 여기서
	// DeviceClass/ResourceSlice 를 따로 읽어 다시 판정하면 /capabilities 와 /preview 가 같은
	// 노드를 두고 서로 다른 답을 할 수 있다 — DRA 전용 노드가 스냅샷에서 통째로 빠지는 식으로.
	_, dra, err := intent.Load(r.Context(), s.Reader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit, offset := pageParams(r)
	writePage(w, BuildNodeCapabilities(nodes.Items, acpps.Items, ndrs.Items, dra), limit, offset)
}
