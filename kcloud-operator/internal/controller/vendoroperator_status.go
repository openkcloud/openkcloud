// ============================================================
// vendoroperator_status.go: 벤더 operator CR 의 상태를 읽어 NPUClusterPolicy 조건으로 보고한다(보고만, 개입 없음)
// 상세: 감지(vendoroperator_detect.go)는 CRD 존재만 본다. 여기서는 그 벤더의 ClusterPolicy 류
//
//	CR 을 읽어 Ready 를 보고만 한다(개입 없음) — present 면 managed 여부와 무관하게 호출한다.
//
// 생성일: 2026-10-09 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// reasonStatusProbeUnavailable 은 상태 조회 자체가 안 돼(표에 없음·CRD/RBAC 로 막힘·조회 오류)
// Unknown 으로 남길 때의 사유다.
const reasonStatusProbeUnavailable = "StatusProbeUnavailable"

// reasonNoClusterPolicy 는 조회는 됐으나 CR 이 하나도 없을 때의 사유다.
const reasonNoClusterPolicy = "NoClusterPolicy"

// vendorOperatorStatusProbe 는 벤더의 ClusterPolicy 류 CR 에서 Ready 상태를 읽는 방법이다.
type vendorOperatorStatusProbe struct {
	gvr         schema.GroupVersionResource
	statePath   []string // unstructured.NestedString 경로
	readyValues []string // 이 값이면 Ready
}

// vendorOperatorStatusProbes 는 벤더 → 상태 조회 방법이다. 출처: reference/nvidia-gpu-operator
// api/nvidia/v1/clusterpolicy_types.go(status.state ∈ ignored|ready|notReady), reference/rbln-npu-operator
// api/v1beta1/rblnclusterpolicy_status_types.go(status.state string). tenstorrent 는 참조 소스가
// 없어 표에 없고, 그 조건은 Unknown(reason StatusProbeUnavailable)으로 남는다.
var vendorOperatorStatusProbes = map[string]vendorOperatorStatusProbe{
	"nvidia":     {gvr: schema.GroupVersionResource{Group: "nvidia.com", Version: "v1", Resource: "clusterpolicies"}, statePath: []string{"status", "state"}, readyValues: []string{"ready"}},
	"rebellions": {gvr: schema.GroupVersionResource{Group: "rebellions.ai", Version: "v1beta1", Resource: "rblnclusterpolicies"}, statePath: []string{"status", "state"}, readyValues: []string{"ready", "Ready"}},
}

// vendorOperatorCRKind 는 vendorOperatorSignatures(vendoroperator_detect.go)에서 벤더의 CR
// Kind(단수)를 가져온다. Ready 조회용 Kind 를 이 파일에 따로 적지 않는 이유: 감지(Detected)와
// 상태(Ready)가 보는 CR 이 같은 것이라, Kind 를 표 둘에 나눠 적으면 하나만 고치고 잊어 드리프트가
// 난다(게이트가 보는 범위 밖의 두 번째 정의).
func vendorOperatorCRKind(vendor string) (string, bool) {
	sig, ok := vendorOperatorSignatures[vendor]
	if !ok || len(sig.groupKinds) == 0 {
		return "", false
	}
	return sig.groupKinds[0].Kind, true
}

// listKindFor 는 벤더의 ClusterPolicy 류 CR 의 List Kind(예: nvidia→ClusterPolicyList,
// rebellions→RblnClusterPolicyList)를 돌려준다.
func listKindFor(vendor string) string {
	kind, ok := vendorOperatorCRKind(vendor)
	if !ok {
		return ""
	}
	return kind + "List"
}

// vendorOperatorState 는 readVendorOperatorState 의 결과다.
type vendorOperatorState struct {
	probe bool // 표(vendorOperatorStatusProbes)에 있는 벤더인가
	found bool // CR 이 하나 이상 있는가
	// blocked 는 CRD 미서빙 또는 권한 없음으로 조회 자체가 막혔다는 뜻이다 — found=false 인 이유가
	// "CR 없음"이 아니라 "조회 불가" 라 호출부가 NoClusterPolicy 대신 StatusProbeUnavailable 로 가른다.
	blocked bool
	name    string // 첫 CR 이름(이름순)
	state   string
	ready   bool
}

// readVendorOperatorState 는 vendor 의 ClusterPolicy 류 CR 을 캐시 없는 APIReader 로 읽어
// 상태를 돌려준다. 표에 없는 벤더나 APIReader 가 nil 이면 probe=false(조회 수단 없음)로
// 즉시 돌아간다 — List 자체를 시도하지 않는다(비용 상한).
func (r *NPUClusterPolicyReconciler) readVendorOperatorState(ctx context.Context, vendor string) (vendorOperatorState, error) {
	p, ok := vendorOperatorStatusProbes[vendor]
	if !ok || r.APIReader == nil {
		return vendorOperatorState{}, nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{Group: p.gvr.Group, Version: p.gvr.Version, Kind: listKindFor(vendor)})
	if err := r.APIReader.List(ctx, list); err != nil {
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) || apierrors.IsForbidden(err) {
			return vendorOperatorState{probe: true, blocked: true}, nil // CRD 없음·권한 없음은 Unknown 으로 보고
		}
		return vendorOperatorState{probe: true}, err
	}
	if len(list.Items) == 0 {
		return vendorOperatorState{probe: true}, nil
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].GetName() < list.Items[j].GetName() })
	item := list.Items[0]
	state, _, _ := unstructured.NestedString(item.Object, p.statePath...)
	st := vendorOperatorState{probe: true, found: true, name: item.GetName(), state: state}
	for _, v := range p.readyValues {
		if state == v {
			st.ready = true
		}
	}
	return st, nil
}

// vendorOperatorReadyConditionType 는 벤더 → Ready 조건 Type 표다. vendorOperatorSignatures 의
// conditionType(Detected)과 별도로 두는 이유: nvidia 의 Detected Type 은 과거 호환으로 범용 이름
// VendorOperatorDetected 를 쓰지만(vendoroperator_detect.go 의 vendorOperatorDetectedCondition),
// Ready 조건은 세 벤더가 독립으로 True/False/Unknown 을 가지므로 Type 자체를 벤더 이름으로 가른다.
var vendorOperatorReadyConditionType = map[string]string{
	"nvidia":      "NvidiaOperatorReady",
	"rebellions":  "RebellionsOperatorReady",
	"tenstorrent": "TenstorrentOperatorReady",
}

// setVendorOperatorReadyCondition 는 readVendorOperatorState 의 결과를 <Vendor>OperatorReady
// 조건으로 세운다(보고만). present 가 아니면 상태 조회 자체를 생략하고 Unknown/NotDetected 로
// 둔다 — 조건 Type 은 지우지 않는다(detectVendorOperator 의 Detected 조건과 같은 규약).
func (r *NPUClusterPolicyReconciler) setVendorOperatorReadyCondition(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, vendor string, present bool) {
	readyType, ok := vendorOperatorReadyConditionType[vendor]
	if !ok {
		return
	}

	// 전이 판정용 이전 상태는 SetStatusCondition 전에 값으로 잡는다 — detectVendorOperator 와
	// 같은 이유(FindStatusCondition 포인터가 제자리 수정되면 전이가 가려진다).
	prevStatus := metav1.ConditionUnknown
	if prev := apimeta.FindStatusCondition(policy.Status.Conditions, readyType); prev != nil {
		prevStatus = prev.Status
	}

	kind, _ := vendorOperatorCRKind(vendor)
	cond := metav1.Condition{Type: readyType, ObservedGeneration: policy.Generation}

	if !present {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionUnknown, "NotDetected",
			fmt.Sprintf("%s operator 가 감지되지 않아 상태 조회를 생략함", vendor)
	} else {
		st, err := r.readVendorOperatorState(ctx, vendor)
		switch {
		case err != nil:
			logf.FromContext(ctx).Error(err, "벤더 operator 상태 조회 실패 — Unknown 으로 보고", "vendor", vendor)
			cond.Status, cond.Reason, cond.Message = metav1.ConditionUnknown, reasonStatusProbeUnavailable,
				fmt.Sprintf("%s 상태 조회 오류: %v", vendor, err)
		case !st.probe:
			cond.Status, cond.Reason, cond.Message = metav1.ConditionUnknown, reasonStatusProbeUnavailable,
				fmt.Sprintf("%s 는 상태 조회 표에 없음", vendor)
		case st.blocked:
			cond.Status, cond.Reason, cond.Message = metav1.ConditionUnknown, reasonStatusProbeUnavailable,
				fmt.Sprintf("%s CR 조회가 막힘(CRD 또는 RBAC 확인 필요)", kind)
		case !st.found:
			cond.Status, cond.Reason, cond.Message = metav1.ConditionUnknown, reasonNoClusterPolicy,
				fmt.Sprintf("%s CR 이 없음", kind)
		case st.ready:
			cond.Status, cond.Reason, cond.Message = metav1.ConditionTrue, "Ready",
				fmt.Sprintf("%s %s status.state=%s", kind, st.name, st.state)
		default:
			cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "NotReady",
				fmt.Sprintf("%s %s status.state=%s", kind, st.name, st.state)
		}
	}

	apimeta.SetStatusCondition(&policy.Status.Conditions, cond)

	// 전이(→True, →False)에서만 이벤트 — Unknown 으로의 전이는 내지 않는다(보고만, 소음 최소화).
	switch {
	case cond.Status == metav1.ConditionTrue && prevStatus != metav1.ConditionTrue:
		r.Recorder.Eventf(policy, corev1.EventTypeNormal, "VendorOperatorReady", "%s", cond.Message)
	case cond.Status == metav1.ConditionFalse && prevStatus != metav1.ConditionFalse:
		r.Recorder.Eventf(policy, corev1.EventTypeWarning, "VendorOperatorNotReady", "%s", cond.Message)
	}
}
