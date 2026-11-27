// ============================================================
// vendoroperator_detect.go: 타 벤더 operator 설치 감지(NVIDIA·Rebellions·Tenstorrent)
// 상세: managed 벤더 옆에 그 벤더의 공개 operator CRD 가 있으면 delegated 전환을 조건·경고로
//
//	권고한다(자동 전환 없음). 감지 결과 Present·Absent·Unknown 세 값으로 배포 모드가 갈린다.
//
// 생성일: 2026-09-20 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

const vendorOperatorDetectedCondition = "VendorOperatorDetected"

// reasonOperatorCRDRemoved 는 재확인 GET 이 CRD 부재를 확인해 조건을 False 로 내릴 때의 사유다.
const reasonOperatorCRDRemoved = "OperatorCRDRemoved"

// OperatorPresence 는 벤더 operator 감지 결과 세 값이다. Absent 와 Unknown 을 반드시 구분한다:
// Absent 는 "시그니처로 조회했고 없음을 확인"(delegated 인데 이 값이면 호출부의 경고 대상),
// Unknown 은 "조회할 시그니처가 없어 판정 불가"(경고하면 시그니처 없는 벤더는 delegated 를
// 쓸 때마다 오경고). 둘을 한 값으로 합치면 호출부가 이 구분을 자리마다 다시 만들어야 하고,
// 그 틈으로 delegated 의 fallback-금지 규칙이 새어 들어온다.
type OperatorPresence string

const (
	// PresencePresent 는 벤더 operator 의 시그니처 CRD 가 클러스터에 서빙됨을 뜻한다.
	PresencePresent OperatorPresence = "Present"
	// PresenceAbsent 는 시그니처가 있고 그 CRD 가 서빙되지 않음을 확인했음을 뜻한다.
	PresenceAbsent OperatorPresence = "Absent"
	// PresenceUnknown 은 감지 시그니처가 없거나 discovery 실패로 판정할 수 없음을 뜻한다.
	PresenceUnknown OperatorPresence = "Unknown"
)

// vendorOperatorSignature 는 벤더 하나의 감지 시그니처(GroupKind 목록)와 조건/경고 문구를 묶는다.
// 조건 타입은 벤더마다 따로 둔다 — 벤더별 감지 상태(True/False)가 서로 독립이라 conditions
// 슬라이스는 Type 당 값 하나만 가지므로, nvidia·rebellions·tenstorrent 를 한 Type 에 합치면
// 나중에 감지된 벤더가 먼저 감지된 벤더의 상태를 덮어써 정보를 잃는다.
type vendorOperatorSignature struct {
	groupKinds      []schema.GroupKind
	conditionType   string
	detectedReason  string
	detectedMessage string
	absentMessage   string
}

// vendorOperatorSignatures 는 벤더→시그니처 표다. 목록 안 GroupKind 중 하나라도 RESTMapper 에
// 서빙되면 그 벤더의 operator 가 설치된 것으로 본다. 출처는 각 벤더의 공개 operator 리포다
// (nvidia: NVIDIA/gpu-operator, rebellions: github.com/rbln-sw/rbln-npu-operator, tenstorrent:
// github.com/tenstorrent/tt-k8s-driver-manager — tt-operator 자체는 CRD 없는 umbrella 차트다).
// kcloud 는 rebellions.ai API 그룹에 CRD 를 만들지 않으므로 이 시그니처와 충돌하지 않는다.
// furiosa 는 공개 operator 리포가 없어 시그니처 없음(Unknown) — 근거 없는 규칙을 만들지 않는다.
var vendorOperatorSignatures = map[string]vendorOperatorSignature{
	"nvidia": {
		groupKinds:      []schema.GroupKind{{Group: "nvidia.com", Kind: "ClusterPolicy"}},
		conditionType:   vendorOperatorDetectedCondition,
		detectedReason:  "NvidiaGPUOperator",
		detectedMessage: "NVIDIA GPU Operator 가 설치돼 있음(nvidia.com/ClusterPolicy) — nvidia.managementMode=delegated 를 권장(중복 배포 차단·operator 설치 지속 확인), 노드만 관측하려면 external",
		absentMessage:   "NVIDIA GPU Operator 미감지 또는 nvidia 가 kcloud 미배포(delegated/external)",
	},
	"rebellions": {
		groupKinds:      []schema.GroupKind{{Group: "rebellions.ai", Kind: "RblnClusterPolicy"}},
		conditionType:   "RebellionsOperatorDetected",
		detectedReason:  "RebellionsNPUOperator",
		detectedMessage: "Rebellions NPU Operator 가 설치돼 있음(rebellions.ai/RblnClusterPolicy) — rebellions.managementMode=delegated 를 권장(중복 배포 차단·operator 설치 지속 확인), 노드만 관측하려면 external",
		absentMessage:   "Rebellions NPU Operator 미감지 또는 rebellions 가 kcloud 미배포(delegated/external)",
	},
	"tenstorrent": {
		groupKinds: []schema.GroupKind{
			{Group: "driver.tenstorrent.com", Kind: "TenstorrentDriverPolicy"},
			{Group: "firmware.tenstorrent.com", Kind: "TenstorrentFirmwarePolicy"},
		},
		conditionType:   "TenstorrentOperatorDetected",
		detectedReason:  "TenstorrentOperator",
		detectedMessage: "Tenstorrent Operator 가 설치돼 있음(driver.tenstorrent.com/TenstorrentDriverPolicy) — tenstorrent.managementMode=delegated 를 권장(중복 배포 차단·operator 설치 지속 확인), 노드만 관측하려면 external",
		absentMessage:   "Tenstorrent Operator 미감지 또는 tenstorrent 가 kcloud 미배포(delegated/external)",
	},
}

// vendorOperatorMapping 은 벤더 시그니처 GroupKind 중 하나라도 REST mapper 에 서빙되는지 찾아
// 그 RESTMapping(재확인용)과 세 값 감지 결과를 함께 돌려준다. 호출부가 present 를 제각각
// 다시 계산하지 않도록 감지 판정을 이 함수 한 곳에 둔다: 시그니처 없음→Unknown, 시그니처
// 있으나 미서빙 확인→Absent, 서빙됨→Present, discovery 자체 실패(no-match 아님)→Unknown(일시
// 장애를 Absent 로 읽지 않는다). 한계: DynamicRESTMapper 는 매핑 적중을 캐시해 CRD 가 제거돼도
// 매니저 재시작 전까지 Present 를 돌려준다 — detectVendorOperator 가 APIReader 재확인으로 보정한다.
func vendorOperatorMapping(m apimeta.RESTMapper, vendor string) (*apimeta.RESTMapping, OperatorPresence, error) {
	sig, ok := vendorOperatorSignatures[vendor]
	if !ok || len(sig.groupKinds) == 0 {
		return nil, PresenceUnknown, nil
	}
	if m == nil {
		return nil, PresenceUnknown, nil
	}
	var errs []error
	for _, gk := range sig.groupKinds {
		mapping, err := m.RESTMapping(gk)
		switch {
		case err == nil:
			return mapping, PresencePresent, nil
		case apimeta.IsNoMatchError(err):
			// 이 GroupKind 는 미서빙 — 다음 시그니처를 본다.
		default:
			// 마지막 오류만 남기면 앞 GroupKind 의 원인이 로그에서 사라진다 — 전부 모은다.
			errs = append(errs, fmt.Errorf("%s: %w", gk.String(), err))
		}
	}
	if len(errs) > 0 {
		return nil, PresenceUnknown, errors.Join(errs...)
	}
	return nil, PresenceAbsent, nil
}

// DetectVendorOperator 는 vendor 의 공개 operator 가 클러스터에 있는지 Present·Absent·Unknown
// 세 값으로 돌려준다 — 배포 모드 판정(특히 delegated)이 이 하나로 갈린다. Present 면 delegated
// 가 유효하고, Absent 면 선언한 operator 가 실제로 없어 호출부가 경고할 근거가 되며, Unknown
// 이면 판정 수단이 없어 경고하지 않는다. 셋을 bool 로 누르면 Absent 와 Unknown 이 뭉개져
// delegated 규칙이 자리마다 다르게 해석된다 — 그래서 세 값 그대로 준다. ctx 는 호출 규약용이고
// 현재 감지 경로(RESTMapper 조회)는 쓰지 않는다.
func DetectVendorOperator(_ context.Context, mapper apimeta.RESTMapper, vendor string) (OperatorPresence, error) {
	_, presence, err := vendorOperatorMapping(mapper, vendor)
	return presence, err
}

// vendorOperatorPresent 는 감지 결과가 Present 인지만 필요한 호출부(시험 포함)용 얇은 래퍼다.
func vendorOperatorPresent(m apimeta.RESTMapper, vendor string) bool {
	_, presence, _ := vendorOperatorMapping(m, vendor)
	return presence == PresencePresent
}

// nvidiaGPUOperatorPresent 는 vendorOperatorPresent 의 nvidia 전용 얇은 래퍼다(기존 시험 호환).
func nvidiaGPUOperatorPresent(m apimeta.RESTMapper) bool {
	return vendorOperatorPresent(m, "nvidia")
}

// restMapper 는 감지에 쓸 REST mapper 를 준다. 시험이 주입한 mapper 가 있으면 그것을,
// 없으면 client 의 것(discovery 기반)을 쓴다.
func (r *NPUClusterPolicyReconciler) restMapper() apimeta.RESTMapper {
	if r.mapper != nil {
		return r.mapper
	}
	return r.RESTMapper()
}

// vendorOperatorCRDConfirmed 는 RESTMapper 캐시가 present=true 로 돌려준 매핑을, 캐시 없는
// APIReader 로 CustomResourceDefinition 자체를 GET 해 재확인한다. 매니저 캐시(r.Client)로 읽으면
// 클러스터 전체 CRD informer 가 새로 떠서 쓰지 않는다 — 그래서 r.APIReader 를 요구한다. GET 이
// NotFound 면 false(제거 확정)를, 그 외 오류(일시 장애 등)는 로그만 남기고 true 를 돌려줘 현재
// 조건을 흔들지 않는다(no flapping). 호출부가 r.APIReader nil 을 이미 걸러 호출하므로 이 함수는
// 그 가드를 다시 하지 않는다.
func (r *NPUClusterPolicyReconciler) vendorOperatorCRDConfirmed(ctx context.Context, mapping *apimeta.RESTMapping) bool {
	name := mapping.Resource.Resource + "." + mapping.Resource.Group
	crd := &apiextensionsv1.CustomResourceDefinition{}
	// 이 GET 은 매니저 ClusterRole 의 apiextensions.k8s.io/customresourcedefinitions get 규칙에
	// 기댄다(npuclusterpolicy_controller.go 의 kubebuilder 마커, deploy/helm/templates/rbac.yaml).
	// 그 규칙이 없으면 아래는 Forbidden 을 default 분기로 흡수해 재확인이 조용히 무력화된다.
	switch err := r.APIReader.Get(ctx, client.ObjectKey{Name: name}, crd); {
	case err == nil:
		return true
	case apierrors.IsNotFound(err):
		return false
	default:
		logf.FromContext(ctx).Error(err, "벤더 operator CRD 재확인 실패 — 기존 조건 유지", "crd", name)
		return true
	}
}

// detectVendorOperator 는 vendor 가 managed 인데 그 벤더의 공개 operator 가 설치돼 있으면
// vendorOperatorSignatures[vendor] 의 조건을 True 로 세우고, False→True 전이에서만 Warning 을
// 낸다(자동 external 전환은 없다). 미감지·external 이면 False(NotDetected)로 둔다. 조건만
// 세팅하고 Status().Update 는 호출부가 한 번에 flush 한다. mapper 가 present+APIReader 가 있으면
// 이전 조건과 무관하게 vendorOperatorCRDConfirmed 로 재확인한다 — "이전 True" 로만 걸면 False
// 로 내린 다음 pass 가 재확인을 건너뛰어 stale 캐시가 다시 True 로 뒤집는다(flapping 방지).
func (r *NPUClusterPolicyReconciler) detectVendorOperator(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, managed bool, vendor string) {
	sig, ok := vendorOperatorSignatures[vendor]
	if !ok {
		// 표에 없는 벤더 — 조건 Type 이 빈 문자열이면 CRD 패턴 검증에서 Status().Update 가 거절된다.
		return
	}
	mapping, presence, err := vendorOperatorMapping(r.restMapper(), vendor)
	if err != nil {
		logf.FromContext(ctx).Error(err, "벤더 operator 감지 조회 실패 — 미감지로 처리", "vendor", vendor)
	}
	present := presence == PresencePresent

	// 전이 판정용 이전 상태는 SetStatusCondition 전에 값으로 잡는다 — FindStatusCondition 은
	// 조건 슬라이스 내부를 가리키는 포인터라, SetStatusCondition 이 그 원소를 제자리 수정하면
	// prev.Status 도 True 로 바뀌어 전이가 가려진다(선존재 False→True 경고 누락).
	prevStatus := metav1.ConditionUnknown
	if prev := apimeta.FindStatusCondition(policy.Status.Conditions, sig.conditionType); prev != nil {
		prevStatus = prev.Status
	}

	crdRemoved := false
	if managed && present && r.APIReader != nil {
		// 이전 상태와 무관하게 재확인한다 — "이미 True" 일 때만 재확인하면, 재확인이 False 로
		// 내린 다음 pass 는 재확인을 건너뛰고 stale mapper 캐시가 다시 True 로 올려 조건이 매
		// pass 뒤집힌다(flapping·경고 반복). GET 은 mapper 가 present 라고 한 벤더에만 pass 당
		// 1회라, 감지 안 된 벤더의 비용은 여전히 0회다.
		// 일시 오류(NotFound 아님)는 vendorOperatorCRDConfirmed 가 true 를 돌려 현재 조건을
		// 그대로 두므로, 재확인 경로가 NotDetected/OperatorCRDRemoved 로 흘러가지 않는다.
		if !r.vendorOperatorCRDConfirmed(ctx, mapping) {
			present = false
			crdRemoved = true
		}
	}

	// Ready 조건(보고만, vendoroperator_status.go)은 managed 여부와 무관하게 present 면 세운다
	// — delegated(비관리) 상태에서 벤더 operator 가 실제로 Ready 인지가 가장 쓸모 있는 신호다.
	r.setVendorOperatorReadyCondition(ctx, policy, vendor, present)

	if !managed || !present {
		reason, message := "NotDetected", sig.absentMessage
		if crdRemoved {
			reason = reasonOperatorCRDRemoved
			message = fmt.Sprintf("%s.%s CRD 가 제거됨 — 재확인 결과 조건을 False 로 되돌림", mapping.Resource.Resource, mapping.Resource.Group)
		}
		apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               sig.conditionType,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: policy.Generation,
		})
		// CRD 제거로 True→False 로 내려가는 전이만 Normal 이벤트로 남긴다 — →True Warning 과
		// 대칭이며, 조건 값만 보고도 언제 사라졌는지 알 수 있게 한다. False 유지 pass 는 안 낸다.
		if crdRemoved && prevStatus == metav1.ConditionTrue {
			r.Recorder.Eventf(policy, corev1.EventTypeNormal, sig.conditionType, "%s", message)
		}
		return
	}

	apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:               sig.conditionType,
		Status:             metav1.ConditionTrue,
		Reason:             sig.detectedReason,
		Message:            sig.detectedMessage,
		ObservedGeneration: policy.Generation,
	})
	// 전이(→True, 최초 포함)에서만 경고 — 매 reconcile 마다 내면 이벤트가 넘친다.
	if prevStatus != metav1.ConditionTrue {
		r.Recorder.Eventf(policy, corev1.EventTypeWarning, sig.conditionType, sig.detectedMessage)
	}
}

// detectNvidiaGPUOperator 는 nvidia 가 managed 일 때 NVIDIA GPU Operator 감지를 본다.
// 판정·조건·경고 본문은 detectVendorOperator 를 따른다. 게이트는 IsOperatorOwned() 를 쓴다 —
// 이 경고는 "kcloud 가 배포 중인데 벤더 operator 도 있으니 delegated/external 로 바꾸라"는
// 권고라, 이미 delegated·external 인 벤더에는 내지 않는다(!IsExternal() 로 걸면 delegated 에도
// 나가 자기 자신을 권고한다).
func (r *NPUClusterPolicyReconciler) detectNvidiaGPUOperator(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) {
	managed := policy.Spec.Nvidia.Enabled && policy.Spec.Nvidia.IsOperatorOwned()
	r.detectVendorOperator(ctx, policy, managed, "nvidia")
}

// detectRebellionsOperator 는 rebellions 가 managed 일 때 Rebellions NPU Operator
// (rbln-npu-operator) 감지를 본다. 판정·조건·경고 본문은 detectVendorOperator 를 따른다.
func (r *NPUClusterPolicyReconciler) detectRebellionsOperator(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) {
	managed := policy.Spec.Rebellions.Enabled && policy.Spec.Rebellions.IsOperatorOwned()
	r.detectVendorOperator(ctx, policy, managed, "rebellions")
}

// detectTenstorrentOperator 는 tenstorrent 가 managed 일 때 Tenstorrent 드라이버/펌웨어
// operator(tt-k8s-driver-manager) 감지를 본다. 판정·조건·경고 본문은 detectVendorOperator 를 따른다.
func (r *NPUClusterPolicyReconciler) detectTenstorrentOperator(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) {
	managed := policy.Spec.Tenstorrent.Enabled && policy.Spec.Tenstorrent.IsOperatorOwned()
	r.detectVendorOperator(ctx, policy, managed, "tenstorrent")
}

// warnDelegatedOperatorAbsent 는 delegated 로 선언한 벤더의 위임 대상을 감지하지 못하면(Absent)
// 경고를 낸다 — 실제로 없으면 kcloud 도 배포하지 않는 벤더가 광고 주체 없이 방치되므로, 조용히
// managed 로 내려가는 대신 이 상태를 드러낸다. Absent(부재 확인)만 경고하고 Unknown(판정 수단
// 없음)은 경고하지 않는다 — 시그니처 없는 벤더의 오경고를 막는다.
// ponytail: 조정마다 낸다 — delegated 정책은 재큐가 드물고 k8s 이벤트 집계가 중복을 접는다.
// 소음이 문제되면 벤더별 조건으로 전이(→Absent)에서만 내도록 승격한다.
func (r *NPUClusterPolicyReconciler) warnDelegatedOperatorAbsent(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) {
	for _, vc := range []struct {
		vendor    string
		delegated bool
	}{
		{"nvidia", policy.Spec.Nvidia.IsDelegated()},
		{"furiosa", policy.Spec.Furiosa.IsDelegated()},
		{"rebellions", policy.Spec.Rebellions.IsDelegated()},
		{"tenstorrent", policy.Spec.Tenstorrent.IsDelegated()},
	} {
		if !vc.delegated {
			continue
		}
		presence, err := DetectVendorOperator(ctx, r.restMapper(), vc.vendor)
		if err != nil {
			logf.FromContext(ctx).Error(err, "delegated 벤더 operator 감지 조회 실패 — 경고 보류", "vendor", vc.vendor)
		}
		if presence != PresenceAbsent {
			continue
		}
		r.Recorder.Eventf(policy, corev1.EventTypeWarning, "DelegatedOperatorAbsent",
			"%s.managementMode=delegated 인데 벤더 operator 를 감지하지 못했다(Absent) — "+
				"kcloud 는 배포하지 않으므로 이 벤더는 광고 주체가 없다. 벤더 operator 를 설치하거나 managed 로 전환한다",
			vc.vendor)
	}
}
