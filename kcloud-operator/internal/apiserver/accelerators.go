// ============================================================
// accelerators.go: 가속기 인벤토리 조회 엔드포인트(/accelerators, /nodes/{node}/accelerators)
// 상세: 읽기 전용. Reader 로 노드·ACPP·NDR·ResourceClaim 을 읽어 BuildInventory 로 접어
//       페이지네이션 봉투로 낸다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

// loadInventory 는 인벤토리 입력 3종을 읽어 집약한다.
func (s *Server) loadInventory(ctx context.Context) ([]AcceleratorView, error) {
	var nodes corev1.NodeList
	if err := s.Reader.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	var acpps v1alpha1.AcceleratorPartitionPolicyList
	if err := s.Reader.List(ctx, &acpps); err != nil {
		return nil, fmt.Errorf("list acceleratorpartitionpolicies: %w", err)
	}
	var ndrs v1alpha1.NodeDeviceReportList
	if err := s.Reader.List(ctx, &ndrs); err != nil {
		return nil, fmt.Errorf("list nodedevicereports: %w", err)
	}
	var healths v1alpha1.AcceleratorHealthList
	if err := s.Reader.List(ctx, &healths); err != nil {
		return nil, fmt.Errorf("list acceleratorhealths: %w", err)
	}
	var pods corev1.PodList
	if err := s.Reader.List(ctx, &pods); err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	// DRA 가용성은 intent.Load 가 판정한 것을 그대로 받는다(preview 와 같은 경로) — 빈
	// DRACapability 를 넘기면 DRA 로만 광고하는 노드가 스냅샷에서 통째로 빠져 그 노드의
	// stale/공유 상태가 이 목록에서 조용히 사라진다. 같은 dra 값을 attachDRAAllocation 에도
	// 재사용한다 — DeviceClass·ResourceSlice List 를 다시 하면 이중 조회다.
	_, dra, err := intent.Load(ctx, s.Reader)
	if err != nil {
		return nil, err
	}
	var claims resourcev1.ResourceClaimList
	// ResourceClaim 은 클러스터에 없을 수 있다(K8s 1.28 라인) — 그때는 DRA 집계 축만 비우고
	// device-plugin 축은 그대로 낸다(handleConsumers 와 같은 완화).
	if err := s.Reader.List(ctx, &claims); err != nil {
		s.Log.V(1).Info("resourceclaims 조회 실패 — DRA 집계 없이 진행", "error", err.Error())
	}
	items := BuildInventory(nodes.Items, acpps.Items, ndrs.Items, dra)
	attachHealth(items, healths.Items)
	attachAllocation(items, pods.Items)
	attachDRAAllocation(items, dra, claims.Items)
	attachVendorID(items, nodes.Items)
	return items, nil
}

// handleAccelerators 는 전체 물리 장치 목록이다. ?vendor= ?node= 로 거르고 ?limit= ?offset= 로 자른다.
func (s *Server) handleAccelerators(w http.ResponseWriter, r *http.Request) {
	items, err := s.loadInventory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if v := r.URL.Query().Get("vendor"); v != "" {
		items = filterSlice(items, func(a AcceleratorView) bool { return strings.EqualFold(a.Vendor, v) })
	}
	if n := r.URL.Query().Get("node"); n != "" {
		items = filterSlice(items, func(a AcceleratorView) bool { return a.NodeName == n })
	}
	limit, offset := pageParams(r)
	writePageETag(w, r, items, limit, offset)
}

// handleAcceleratorByUID 는 UID 하나를 찾는다. 합성 ID 가 "/" 를 포함하므로 경로 나머지 전체가 UID 다.
func (s *Server) handleAcceleratorByUID(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if uid == "" {
		// 후행 슬래시는 {uid...} 를 uid="" 로 매칭시킨다 — 이걸 그냥 두면 UID 가
		// 없는 Pending 행(zero value)이 조회돼 버린다. 빈 UID 는 애초에 조회 대상이 아니다.
		writeError(w, http.StatusNotFound, "accelerator uid is required")
		return
	}
	items, err := s.loadInventory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range items {
		if items[i].UID == uid {
			writeJSONETag(w, r, http.StatusOK, items[i])
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("accelerator %q not found", uid))
}

// handleNodeAccelerators 는 노드 하나의 장치 목록이다. 오타 난 노드 이름이 조용히 빈 목록으로
// 보이지 않도록 노드 존재를 먼저 확인한다(없으면 404).
func (s *Server) handleNodeAccelerators(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("node")
	var n corev1.Node
	if err := s.Reader.Get(r.Context(), client.ObjectKey{Name: name}, &n); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := s.loadInventory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items = filterSlice(items, func(a AcceleratorView) bool { return a.NodeName == name })
	limit, offset := pageParams(r)
	writePageETag(w, r, items, limit, offset)
}
