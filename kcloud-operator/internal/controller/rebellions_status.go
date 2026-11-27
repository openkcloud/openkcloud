// ============================================================
// rebellions_status.go: Rebellions device plugin 준비 상태를 NPUClusterPolicy 조건으로 보고
// 상세: init container(rbln-cdi-prep) 종료 코드, 시작하지 못한 파드(이미지 받기 실패 등), CrashLoopBackOff 를 노드별 사유로 모아
//       RebellionsDevicePluginReady 를 메모리에 세팅하고(flush 는 setReadyCondition) 바뀔 때만 이벤트를 남긴다. 1.34 라인과 1.28 라인이 같은 파일을 쓴다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	"context"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// RebellionsDevicePluginReady 조건 사유 중 init container 종료 코드와 무관한 것들.
const (
	rbllnsReasonPrepared         = "Prepared"
	rbllnsReasonHostExecUnset    = "HostExecImageUnset"
	rbllnsReasonPluginCrashLoop  = "PluginCrashLoop"
	rbllnsReasonPluginNotStarted = "PluginNotStarted"
)

// rbllnsReasonOrder 는 노드별 사유를 메시지에 적고 조건 Reason 을 고르는 순서다. 앞에 있을수록
// 먼저 고칠 원인이다. HostExecImageUnset 은 노드와 무관해 이 목록 앞에 따로 둔다.
var rbllnsReasonOrder = []string{
	rbllnsReasonNoSmi, rbllnsReasonNoDevice, rbllnsReasonWriteFailed,
	rbllnsReasonPrepFailed, rbllnsReasonPluginCrashLoop, rbllnsReasonPluginNotStarted,
}

// rbllnsStuckWaiting 은 컨테이너가 정상 기동 대기(ContainerCreating·PodInitializing)가 아닌 사유로
// 시작하지 못하고 있는지 본다. 이미지 받기 실패(ErrImagePull·ImagePullBackOff)나 컨테이너 설정
// 오류(CreateContainerConfigError)처럼 종료 기록이 남지 않는 경우다.
func rbllnsStuckWaiting(s corev1.ContainerStatus) bool {
	w := s.State.Waiting
	return w != nil && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing"
}

// rbllnsPodFailures 는 플러그인 파드에서 노드별 실패 사유를 모은다. 키는 사유, 값은 노드 집합이다 —
// 롤링 업데이트 중 같은 노드에 파드가 둘이어도 노드는 한 번만 센다.
func rbllnsPodFailures(pods []corev1.Pod) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	add := func(reason, node string) {
		if out[reason] == nil {
			out[reason] = map[string]struct{}{}
		}
		out[reason][node] = struct{}{}
	}
	for _, p := range pods {
		for _, s := range p.Status.InitContainerStatuses {
			if s.Name != rbllnsPrepContainerName {
				continue
			}
			// 지금 종료된 상태가 없으면 재시도 대기 중인 직전 종료를 본다.
			t := s.State.Terminated
			if t == nil && s.State.Waiting != nil {
				t = s.LastTerminationState.Terminated
			}
			if t == nil {
				// 종료 기록 없이 시작하지 못하는 사전 점검(이미지 받기 실패 등)도 실패다.
				if rbllnsStuckWaiting(s) {
					add(rbllnsReasonPrepFailed, p.Spec.NodeName)
				}
				continue
			}
			if reason := rbllnsPrepReason(t.ExitCode); reason != "" {
				add(reason, p.Spec.NodeName)
			}
		}
		for _, s := range p.Status.ContainerStatuses {
			if s.Name != rbllnsDaemonSetName || !rbllnsStuckWaiting(s) {
				continue
			}
			if s.State.Waiting.Reason == "CrashLoopBackOff" {
				add(rbllnsReasonPluginCrashLoop, p.Spec.NodeName)
			} else {
				add(rbllnsReasonPluginNotStarted, p.Spec.NodeName)
			}
		}
	}
	return out
}

// rebellionsPluginCondition 은 판 설명(예: "v0.4 형식(태그 v0.4.3)")과 실패 목록으로 조건을 만든다.
// 메시지에는 사유와 노드만 적는다 —
// init container 의 종료 메시지는 파드 상태(kubectl describe)에 남는다.
func rebellionsPluginCondition(note string, hostExecUnset bool, failures map[string]map[string]struct{},
	generation int64) metav1.Condition {
	parts := []string{"판: " + note}
	first := ""
	if hostExecUnset {
		first = rbllnsReasonHostExecUnset
		parts = append(parts, rbllnsReasonHostExecUnset+": HOST_EXEC_IMAGE 미설정, init container 없이 렌더링")
	}
	for _, reason := range rbllnsReasonOrder {
		nodes := failures[reason]
		if len(nodes) == 0 {
			continue
		}
		names := make([]string, 0, len(nodes))
		for n := range nodes {
			names = append(names, n)
		}
		sort.Strings(names)
		parts = append(parts, reason+": "+strings.Join(names, ", "))
		if first == "" {
			first = reason
		}
	}
	cond := metav1.Condition{
		Type:               npuv1alpha1.ConditionRebellionsDevicePluginReady,
		Status:             metav1.ConditionTrue,
		Reason:             rbllnsReasonPrepared,
		Message:            strings.Join(parts, "; "),
		ObservedGeneration: generation,
	}
	if first != "" {
		cond.Status, cond.Reason = metav1.ConditionFalse, first
	}
	return cond
}

// reportRebellionsDevicePlugin 은 조건을 메모리에만 세팅한다. active 가 거짓이면(Rebellions 꺼짐, 또는
// operator 가 배포하지 않는 모드) 조건을 없앤다. 파드 조회가 실패하면 조건을 바꾸지 않는다 —
// 조회 오류로 True 가 되면 안 된다.
func (r *NPUClusterPolicyReconciler) reportRebellionsDevicePlugin(ctx context.Context,
	policy *npuv1alpha1.NPUClusterPolicy, active bool) {
	if !active {
		apimeta.RemoveStatusCondition(&policy.Status.Conditions, npuv1alpha1.ConditionRebellionsDevicePluginReady)
		return
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(rbllnsResolveNamespace(policy)),
		client.MatchingLabels{"app.kubernetes.io/name": rbllnsDaemonSetName}); err != nil {
		logf.FromContext(ctx).Error(err, "Rebellions device plugin 파드 조회 실패 — 조건 유지")
		return
	}
	gen, note := rebellionsPluginGeneration(policy.Spec.Rebellions)
	cond := rebellionsPluginCondition(gen+" 형식("+note+")", HostExecImage() == "", rbllnsPodFailures(pods.Items),
		policy.Generation)

	// 이전 조건은 값으로 복사해 둔다 — SetStatusCondition 뒤에 포인터로 비교하면 전이가 보이지 않는다.
	var prev *metav1.Condition
	if p := apimeta.FindStatusCondition(policy.Status.Conditions, cond.Type); p != nil {
		saved := *p
		prev = &saved
	}
	apimeta.SetStatusCondition(&policy.Status.Conditions, cond)
	if prev != nil && prev.Status == cond.Status && prev.Reason == cond.Reason && prev.Message == cond.Message {
		return
	}
	if r.Recorder == nil {
		return
	}
	eventType := corev1.EventTypeNormal
	if cond.Status == metav1.ConditionFalse {
		eventType = corev1.EventTypeWarning
	}
	r.Recorder.Event(policy, eventType, cond.Reason, cond.Message)
}
