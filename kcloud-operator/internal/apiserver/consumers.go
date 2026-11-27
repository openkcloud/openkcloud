// ============================================================
// consumers.go: 가속기 소비자(Pod) 조회 — GET /api/v1/consumers
// 상세: 귀속 정밀도가 할당 API 마다 다르다 — DRA 는 장치 단위, device-plugin 은 노드
// 단위까지만 안다. 모르는 것은 추측해 채우지 않는다(fail-closed).
// 생성일: 2026-08-07 | 수정일: 2026-10-11
// ============================================================
package apiserver

import (
	"net/http"
	"sort"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

// 귀속 등급. 이 셋 외의 값을 만들지 않는다.
const (
	// AttributionDevice: 어느 장치인지 안다(DRA 할당 완료).
	AttributionDevice = "device"
	// AttributionNode: 노드까지만 안다(device-plugin 경로에서 PodResources 수집이 없거나 실패했을 때).
	AttributionNode = "node"
	// AttributionPending: DRA claim 은 있으나 아직 미할당. 곧 알게 된다.
	AttributionPending = "pending"
)

// DeviceUIDs 의 출처. 이 둘 외의 값을 만들지 않는다.
const (
	// DeviceIDSourceDRA: DeviceUIDs 가 ResourceClaim 할당 결과(res.Device)에서 왔다.
	DeviceIDSourceDRA = "dra-allocation"
	// DeviceIDSourcePodResources: DeviceUIDs 가 kubelet PodResources(device_ids 원문)에서 왔다.
	DeviceIDSourcePodResources = "kubelet-podresources"
)

// ResourceRequest 는 Pod 가 요청한 가속기 리소스 하나다.
type ResourceRequest struct {
	Resource string `json:"resource"`
	Quantity int64  `json:"quantity"`
	Vendor   string `json:"vendor,omitempty"`
}

// ClaimRef 는 Pod 가 참조하는 ResourceClaim 하나다.
type ClaimRef struct {
	Name        string `json:"name"`
	DeviceClass string `json:"deviceClass,omitempty"`
	Status      string `json:"status"` // allocated | pending | missing | reserved-elsewhere
}

// ClaimStatusReservedElsewhere: claim 은 있고 할당도 됐지만 status.reservedFor 가 이 파드를
// 담지 않는다 — 같은 이름의 claim 을 다른 파드가 이미 쥐고 있으면 그 장치는 이 파드의 것이 아니다.
const ClaimStatusReservedElsewhere = "reserved-elsewhere"

// ConsumerView 는 가속기를 요청한 Pod 하나다.
// pkg/npuctl.ConsumerView(장치를 쥔 컨테이너 하나, describe node CONSUMERS)와 이름만 같고 단위가 다르다.
// AcceleratorView(장치 행)에는 이 소비자 목록을 달지 않는다 — device-plugin 경로에서는
// 장치별 소비자를 알 수 없어 달면 거짓 신호가 된다.
type ConsumerView struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Node      string `json:"node,omitempty"`
	Phase     string `json:"phase"`
	// AllocationAPI 는 npuv1alpha1.AllocationAPIDevicePlugin 또는 AllocationAPIDRA 다.
	AllocationAPI string            `json:"allocationAPI"`
	Attribution   string            `json:"attribution"` // device | node | pending
	Requests      []ResourceRequest `json:"requests,omitempty"`
	// DeviceUIDs 는 Attribution == device 일 때만 채운다. 그 외에는 반드시 비운다 —
	// 빈 값은 "장치 없음" 이 아니라 "모른다" 는 뜻이고, UI 가 등급과 함께 읽는다.
	DeviceUIDs []string `json:"deviceUIDs,omitempty"`
	// DeviceIDSource 는 DeviceUIDs 의 출처다 — "dra-allocation"(ResourceClaim 할당 결과) 또는
	// "kubelet-podresources"(NDR consumers, device_ids 원문). 둘은 다른 식별자 공간이다 —
	// 콘솔은 kubelet 출처에 "(kubelet)" 표시를 붙이고, 다른 소비자는 이 필드로 구분한다.
	// Attribution != device 면 비운다. DRA claim 과 device-plugin 자원을 함께 쥔 파드는
	// DRA 가 우선이며 kubelet 쪽 ID 는 합치지 않는다(목록이 부분집합일 수 있다).
	DeviceIDSource string     `json:"deviceIDSource,omitempty"`
	Claims         []ClaimRef `json:"claims,omitempty"`
}

// toConsumerView 는 Pod 하나를 소비자 뷰로 옮긴다. 두 번째 반환값이 false 면
// 가속기를 요청하지 않은 Pod 이므로 목록에서 뺀다. ndrs 는 device-plugin 귀속을 device
// 등급으로 올리는 데만 쓴다(DRA 축은 claims 로 충분하다).
func toConsumerView(pod corev1.Pod, claims []resourcev1.ResourceClaim, ndrs []npuv1alpha1.NodeDeviceReport) (ConsumerView, bool) {
	v := ConsumerView{
		Namespace: pod.Namespace, Name: pod.Name,
		Node: pod.Spec.NodeName, Phase: string(pod.Status.Phase),
	}

	// device-plugin 축: 컨테이너 limits 를 합산한다. 가속기 리소스 판정은
	// intent.VendorForResource 하나만 쓴다 — 규칙이 두 곳에 생기면 갈린다.
	totals := map[string]int64{}
	for i := range pod.Spec.Containers {
		for name, q := range pod.Spec.Containers[i].Resources.Limits {
			if intent.VendorForResource(string(name)) == "" {
				continue
			}
			totals[string(name)] += q.Value()
		}
	}
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v.Requests = append(v.Requests, ResourceRequest{
			Resource: name, Quantity: totals[name], Vendor: intent.VendorForResource(name),
		})
	}

	// DRA 축: pod 가 참조하는 claim 을 찾아 할당 결과를 읽는다. 템플릿 claim 은
	// status 의 생성 이름으로 찾는다.
	byName := map[string]*resourcev1.ResourceClaim{}
	for i := range claims {
		if claims[i].Namespace == pod.Namespace {
			byName[claims[i].Name] = &claims[i]
		}
	}
	allocated := false
	for _, prc := range pod.Spec.ResourceClaims {
		name := claimNameOf(pod, prc)
		ref := ClaimRef{Name: name, Status: "missing"}
		if c := byName[name]; c != nil {
			ref.DeviceClass = deviceClassOf(c)
			// 분기 순서는 fail-closed 다. 예약 주인을 먼저 보는 이유: 다른 파드에 예약된 claim 도
			// status.allocation 이 채워져 있어, 할당 분기를 먼저 두면 남의 장치를 이 파드 소유로
			// 귀속한다. 예약이 이 파드면 그다음에 할당 유무로 allocated/pending 을 가른다.
			switch {
			case !reservedForPod(c, pod):
				ref.Status = ClaimStatusReservedElsewhere
			case c.Status.Allocation != nil:
				ref.Status = "allocated"
				allocated = true
				for _, res := range c.Status.Allocation.Devices.Results {
					v.DeviceUIDs = append(v.DeviceUIDs, res.Device)
				}
			default:
				ref.Status = "pending"
			}
		}
		v.Claims = append(v.Claims, ref)
	}

	switch {
	case len(v.Claims) > 0 && allocated:
		v.AllocationAPI, v.Attribution, v.DeviceIDSource = npuv1alpha1.AllocationAPIDRA, AttributionDevice, DeviceIDSourceDRA
	case len(v.Claims) > 0:
		v.AllocationAPI, v.Attribution = npuv1alpha1.AllocationAPIDRA, AttributionPending
		v.DeviceUIDs = nil
	case len(v.Requests) > 0:
		v.AllocationAPI, v.Attribution = npuv1alpha1.AllocationAPIDevicePlugin, AttributionNode
		v.DeviceUIDs = nil
		if ids := podResourcesDeviceIDs(ndrs, pod); ids != nil {
			v.Attribution, v.DeviceUIDs, v.DeviceIDSource = AttributionDevice, ids, DeviceIDSourcePodResources
		}
	default:
		return ConsumerView{}, false
	}
	sort.Strings(v.DeviceUIDs)
	return v, true
}

// podResourcesDeviceIDs 는 파드 노드의 NDR status.consumers 에서 이 파드의 deviceID 들을 모은다.
// consumers 가 nil(모름)이거나 파드가 없으면 nil — 호출자는 node 등급을 유지한다.
func podResourcesDeviceIDs(ndrs []npuv1alpha1.NodeDeviceReport, pod corev1.Pod) []string {
	for i := range ndrs {
		name := ndrs[i].Spec.NodeName
		if name == "" {
			name = ndrs[i].Name
		}
		if name != pod.Spec.NodeName || ndrs[i].Status.Consumers == nil {
			continue
		}
		var ids []string
		for _, c := range ndrs[i].Status.Consumers {
			if c.Namespace == pod.Namespace && c.Pod == pod.Name {
				ids = append(ids, c.DeviceID)
			}
		}
		return ids // 파드가 목록에 없으면 nil → node 유지
	}
	return nil
}

// claimNameOf 는 파드가 참조하는 ResourceClaim 의 실제 이름을 돌려준다. spec 에 직접 적은
// 이름이 있으면 그것이고, 템플릿(resourceClaimTemplateName)으로 받았으면 kube-controller-manager(ResourceClaim 컨트롤러)가 만든
// 이름이 status.resourceClaimStatuses 에만 있으므로 같은 항목 이름으로 되짚는다. 둘 다 없으면
// 빈 문자열 — 호출자는 missing 으로 둔다(아직 만들어지지 않은 claim 을 지어내지 않는다).
func claimNameOf(pod corev1.Pod, prc corev1.PodResourceClaim) string {
	if prc.ResourceClaimName != nil && *prc.ResourceClaimName != "" {
		return *prc.ResourceClaimName
	}
	for _, st := range pod.Status.ResourceClaimStatuses {
		if st.Name == prc.Name && st.ResourceClaimName != nil {
			return *st.ResourceClaimName
		}
	}
	return ""
}

// deviceClassOf 는 claim 의 첫 요청에서 DeviceClass 이름을 꺼낸다. 요청이
// 여럿이면 첫 것만 쓴다 — 화면 라벨 용도이고 정확한 목록은 claim 을 직접 본다.
func deviceClassOf(c *resourcev1.ResourceClaim) string {
	for _, req := range c.Spec.Devices.Requests {
		if req.Exactly != nil && req.Exactly.DeviceClassName != "" {
			return req.Exactly.DeviceClassName
		}
	}
	return ""
}

// reservedForPod 는 claim 의 reservedFor 가 비었거나(아직 예약 전) 이 파드를 담으면 true 다.
// 담았어도 다른 파드면 false — 같은 이름의 claim 을 다른 파드가 쥐고 있을 수 있다.
func reservedForPod(c *resourcev1.ResourceClaim, pod corev1.Pod) bool {
	if len(c.Status.ReservedFor) == 0 {
		return true
	}
	for _, r := range c.Status.ReservedFor {
		if r.Resource == "pods" && r.UID == pod.UID {
			return true
		}
	}
	return false
}

// handleConsumers 는 가속기를 요청한 Pod 목록을 낸다.
// 클러스터 전체 Pod 를 흘리면 화면이 무관한 Pod 로 덮이므로 toConsumerView 가
// 걸러 낸 것만 담는다.
func (s *Server) handleConsumers(w http.ResponseWriter, r *http.Request) {
	var pods corev1.PodList
	if err := s.Reader.List(r.Context(), &pods); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var claims resourcev1.ResourceClaimList
	// ResourceClaim 은 클러스터에 없을 수 있다(K8s 1.28 라인). 그때는 DRA 축을
	// 비운 채 device-plugin 축만 낸다 — 목록 전체를 죽이지 않는다.
	if err := s.Reader.List(r.Context(), &claims); err != nil {
		s.Log.V(1).Info("resourceclaims 조회 실패 — DRA 축 없이 진행", "error", err.Error())
	}
	// NodeDeviceReport 는 이 operator 가 설치하는 CRD 라 pods 와 같은 급으로 다룬다 —
	// 조회 실패를 삼키지 않는다(1.28 라인 예외는 resourceclaims 뿐이다).
	var ndrs npuv1alpha1.NodeDeviceReportList
	if err := s.Reader.List(r.Context(), &ndrs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]ConsumerView, 0, len(pods.Items))
	for i := range pods.Items {
		if v, ok := toConsumerView(pods.Items[i], claims.Items, ndrs.Items); ok {
			out = append(out, v)
		}
	}
	if node := r.URL.Query().Get("node"); node != "" {
		out = filterSlice(out, func(v ConsumerView) bool { return v.Node == node })
	}
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		out = filterSlice(out, func(v ConsumerView) bool { return v.Namespace == ns })
	}
	if vendor := r.URL.Query().Get("vendor"); vendor != "" {
		out = filterSlice(out, func(v ConsumerView) bool {
			for _, req := range v.Requests {
				if req.Vendor == vendor {
					return true
				}
			}
			return false
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	limit, offset := pageParams(r)
	writePageETag(w, r, out, limit, offset)
}
