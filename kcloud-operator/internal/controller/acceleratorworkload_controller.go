// ============================================================
// acceleratorworkload_controller.go: AcceleratorWorkload 컨트롤러
// 상세: 추상 워크로드의 최종 권위 판정이다. admission webhook 은 우회될 수 있고 승인 후
// capability 가 바뀔 수도 있어, 같은 intent.Translate 를 다시 돌려 status 에 남긴다. 성공하면
// Deployment 를 소유하고, 거절되면 Deployment 없이 Condition=False 로 사유만 남긴다 — 실행 중
// Pod 은 강제로 죽이지 않고, 광고가 사라지면 자연히 Pending 된다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driveradapter"
	"kcloud-operator/internal/intent"
	"kcloud-operator/internal/upgrade"
)

// podInjectLabel 은 기존 PodMutator(internal/webhook.InjectLabel)의 opt-in 라벨이다.
// 렌더한 Pod 에 붙여 runtimeClass 주입 경로를 재사용한다(새 mutating 경로를 만들지 않는다).
const podInjectLabel = "kcloud.ai/inject"

// awWorkloadLabel 은 렌더한 Deployment/Pod 를 CR 로 되짚는 selector 다.
const awWorkloadLabel = "kcloud.ai/accelerator-workload"

// awRequeueInterval 은 클러스터 capability 가 바뀌면 판정도 바뀌므로 주기적으로 다시 본다.
const awRequeueInterval = 2 * time.Minute

// errDeploymentNotOwned 는 같은 이름의 Deployment 가 남의 것일 때다. 뺏지 않는다.
var errDeploymentNotOwned = errors.New("deployment is not owned by this AcceleratorWorkload")

// errResourceClaimTemplateNotOwned 는 같은 이름의 ResourceClaimTemplate 이 남의 것일 때다.
var errResourceClaimTemplateNotOwned = errors.New("resourceclaimtemplate is not owned by this AcceleratorWorkload")

// errResourceClaimTemplateDrift 는 살아있는 템플릿이 요청하는 DeviceClass 가 지금 번역이
// 확정한 것과 다를 때다. spec 이 불변이라 Update 로는 못 고치고, 지웠다 다시 만들면 그 템플릿으로
// 이미 뜬 Pod 의 claim 이 끊긴다 — operator 가 혼자 결정할 일이 아니므로 사유만 드러낸다.
var errResourceClaimTemplateDrift = errors.New("resourceclaimtemplate deviceClassName no longer matches the resolved allocation")

// draClaimName 은 pod.spec.resourceClaims 안에서 이 claim 을 가리키는 이름이다(컨테이너의
// resources.claims 도 같은 이름으로 참조한다).
const draClaimName = "accel"

// draClaimTemplateName 은 이 워크로드가 소유하는 ResourceClaimTemplate 의 이름이다.
func draClaimTemplateName(awName string) string {
	return awName + "-accel"
}

// AcceleratorWorkloadReconciler 는 추상 워크로드를 Deployment 로 실현한다.
type AcceleratorWorkloadReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorworkloads,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratorworkloads/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=resource.k8s.io,resources=deviceclasses;resourceslices;resourceclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=resource.k8s.io,resources=resourceclaimtemplates,verbs=get;list;watch;create;update;patch;delete

func (r *AcceleratorWorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var aw npuv1alpha1.AcceleratorWorkload
	if err := r.Get(ctx, req.NamespacedName, &aw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var class npuv1alpha1.AcceleratorClass
	if err := r.Get(ctx, types.NamespacedName{Name: aw.Spec.Accelerator.Class}, &class); err != nil {
		if apierrors.IsNotFound(err) {
			return r.rejected(ctx, &aw, npuv1alpha1.AWReasonClassNotFound, "",
				fmt.Sprintf("AcceleratorClass %q does not exist", aw.Spec.Accelerator.Class))
		}
		return ctrl.Result{}, err
	}

	snap, dra, err := intent.Load(ctx, r.Client)
	if err != nil {
		// 재시도해도 같은 결과인 실패(주로 RBAC)를 백오프에 묻어 두면 CR 은 영영 빈 status 로 남는다.
		if nonRetryableAPIError(err) {
			return r.rejected(ctx, &aw, npuv1alpha1.AWReasonCapabilityUnverified, "",
				fmt.Sprintf("cluster capability cannot be read, so nothing can be confirmed: %v", err))
		}
		return ctrl.Result{}, err
	}
	res, err := intent.TranslateWithDRA(intent.BuildRequest(&aw, &class), snap, dra)
	if err != nil {
		var rj *intent.Reject
		if errors.As(err, &rj) {
			return r.rejected(ctx, &aw, rj.Reason, rj.Axis, rj.Error())
		}
		return ctrl.Result{}, err
	}

	rct := renderResourceClaimTemplate(&aw, res)
	if rct == nil {
		// DRA 를 벗어난(devicePlugin 으로 되돌린) 워크로드가 남긴 템플릿은 지운다 — 그냥 두면
		// ownerRef GC 가 CR 삭제 때까지 미루므로 devicePlugin 으로 도는 워크로드가 쓰지도 않는
		// claim 템플릿을 계속 갖고 있게 된다.
		if err := r.deleteOwnedResourceClaimTemplate(ctx, &aw); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err := ctrl.SetControllerReference(&aw, rct, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.applyResourceClaimTemplate(ctx, rct); err != nil {
			if errors.Is(err, errResourceClaimTemplateNotOwned) {
				return ctrl.Result{RequeueAfter: awRequeueInterval},
					r.translated(ctx, &aw, res, nil, npuv1alpha1.AWReasonDeploymentConflict, err.Error())
			}
			// 번역은 성공했고 status.resolved 에는 새 DeviceClass 가 실린다 — Pod 이 아직 옛
			// 클래스를 청구한다는 사실을 WorkloadReady=False + Warning 이벤트로 남겨야 사용자가
			// 자기 수정이 반영되지 않았음을 알 수 있다.
			if errors.Is(err, errResourceClaimTemplateDrift) {
				return ctrl.Result{RequeueAfter: awRequeueInterval},
					r.translated(ctx, &aw, res, nil, npuv1alpha1.AWReasonResourceClaimTemplateDrift, err.Error())
			}
			if nonRetryableAPIError(err) {
				return ctrl.Result{RequeueAfter: awRequeueInterval},
					r.translated(ctx, &aw, res, nil, npuv1alpha1.AWReasonDeploymentInvalid, err.Error())
			}
			return ctrl.Result{}, err
		}
	}

	dep := renderDeployment(&aw, res)
	if err := ctrl.SetControllerReference(&aw, dep, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	live, err := r.applyDeployment(ctx, dep)
	if err != nil {
		if errors.Is(err, errDeploymentNotOwned) {
			// 번역 자체는 성공했다 — Rejected 로 뭉개지 않고 실현 실패 사유를 따로 남긴다.
			return ctrl.Result{RequeueAfter: awRequeueInterval},
				r.translated(ctx, &aw, res, nil, npuv1alpha1.AWReasonDeploymentConflict, err.Error())
		}
		// Invalid/Forbidden/quota/terminating namespace 는 재시도해도 같다. 백오프로 영영 돌면서
		// status 를 비워 두면 사용자는 operator 로그 말고는 볼 것이 없다.
		if nonRetryableAPIError(err) {
			return ctrl.Result{RequeueAfter: awRequeueInterval},
				r.translated(ctx, &aw, res, nil, npuv1alpha1.AWReasonDeploymentInvalid, err.Error())
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: awRequeueInterval}, r.translated(ctx, &aw, res, live, "", "")
}

// nonRetryableAPIError 는 같은 입력으로 다시 불러도 같은 결과가 나오는 API 실패다.
// (Invalid=잘못된 리소스명 등, Forbidden=RBAC·quota·terminating namespace, BadRequest=요청 형식.)
// applyDeployment/Load 가 %w 로 감싸도 apierrors 가 errors.As 로 풀어 본다.
func nonRetryableAPIError(err error) bool {
	return apierrors.IsInvalid(err) || apierrors.IsForbidden(err) || apierrors.IsBadRequest(err)
}

// applyDeployment 는 없으면 만들고, 우리가 소유한 것이면 우리가 정하는 필드만 맞춘다.
// 바뀐 게 없으면 쓰지 않는다 — 재조정마다 Update 를 날리면 Pod 이 계속 다시 뜬다.
func (r *AcceleratorWorkloadReconciler) applyDeployment(ctx context.Context, want *appsv1.Deployment) (*appsv1.Deployment, error) {
	var live appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: want.Name, Namespace: want.Namespace}, &live)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, want); err != nil {
			return nil, fmt.Errorf("create deployment %s/%s: %w", want.Namespace, want.Name, err)
		}
		return want, nil
	}
	if err != nil {
		return nil, err
	}
	owner := metav1.GetControllerOf(&live)
	if owner == nil || owner.UID != want.OwnerReferences[0].UID {
		return nil, fmt.Errorf("deployment %s/%s already exists and is not controlled by this workload: %w",
			want.Namespace, want.Name, errDeploymentNotOwned)
	}
	merged := mergeDeployment(&live, want)
	if apiequality.Semantic.DeepEqual(live.Spec, merged.Spec) && apiequality.Semantic.DeepEqual(live.Labels, merged.Labels) {
		return &live, nil
	}
	if err := r.Update(ctx, merged); err != nil {
		return nil, fmt.Errorf("update deployment %s/%s: %w", want.Namespace, want.Name, err)
	}
	return merged, nil
}

// applyResourceClaimTemplate 은 DRA 경로에서 쓸 ResourceClaimTemplate 을 소유·생성한다. applyDeployment
// 와 같은 소유권 규율(남의 것은 안 뺏는다)을 따르지만, spec 이 불변(K8s 제약)이라 Deployment 처럼
// 필드별 병합을 할 게 없다 — 없으면 만들고, 있으면 요청하는 DeviceClass 가 지금 번역 결과와
// 같은지만 본다. 여기서 비교하지 않으면 AcceleratorClass 의 deviceClassName 을 바꾼 뒤
// status.resolved 는 새 값을, Pod 은 계속 옛 값을 가리키는데 아무 데도 그 사실이 남지 않는다.
func (r *AcceleratorWorkloadReconciler) applyResourceClaimTemplate(ctx context.Context, want *resourcev1.ResourceClaimTemplate) error {
	var live resourcev1.ResourceClaimTemplate
	err := r.Get(ctx, types.NamespacedName{Name: want.Name, Namespace: want.Namespace}, &live)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, want); err != nil {
			return fmt.Errorf("create resourceclaimtemplate %s/%s: %w", want.Namespace, want.Name, err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	owner := metav1.GetControllerOf(&live)
	if owner == nil || owner.UID != want.OwnerReferences[0].UID {
		return fmt.Errorf("resourceclaimtemplate %s/%s already exists and is not controlled by this workload: %w",
			want.Namespace, want.Name, errResourceClaimTemplateNotOwned)
	}
	if got, expected := claimDeviceClassName(&live), claimDeviceClassName(want); got != expected {
		return fmt.Errorf("resourceclaimtemplate %s/%s still requests deviceClass %q but the resolved allocation is %q; "+
			"spec is immutable, so delete the template (and the pods holding claims from it) to adopt the new class: %w",
			want.Namespace, want.Name, got, expected, errResourceClaimTemplateDrift)
	}
	return nil
}

// claimDeviceClassName 은 이 템플릿이 요청하는 DeviceClass 다(없으면 빈 문자열). 우리가 만든
// 템플릿은 요청이 하나뿐이지만, 예전 버전이 만든 모양도 그대로 읽을 수 있게 첫 유효값을 쓴다.
func claimDeviceClassName(t *resourcev1.ResourceClaimTemplate) string {
	for _, req := range t.Spec.Spec.Devices.Requests {
		if req.Exactly != nil && req.Exactly.DeviceClassName != "" {
			return req.Exactly.DeviceClassName
		}
	}
	return ""
}

// deleteOwnedResourceClaimTemplate 은 DRA 경로를 벗어났을 때 우리가 만든 템플릿을 회수한다.
// resource.k8s.io 를 서빙하지 않는 클러스터에서는 조회 자체가 NoMatch 다 — 거기서는 만든 적도
// 없으므로 지울 것도 없다(이걸 오류로 올리면 DRA 없는 클러스터의 모든 워크로드가 재조정 실패한다).
func (r *AcceleratorWorkloadReconciler) deleteOwnedResourceClaimTemplate(ctx context.Context, aw *npuv1alpha1.AcceleratorWorkload) error {
	var live resourcev1.ResourceClaimTemplate
	err := r.Get(ctx, types.NamespacedName{Name: draClaimTemplateName(aw.Name), Namespace: aw.Namespace}, &live)
	if err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil
		}
		return client.IgnoreNotFound(err)
	}
	if owner := metav1.GetControllerOf(&live); owner == nil || owner.UID != aw.UID {
		return nil // 남의 것은 안 건드린다(applyResourceClaimTemplate 과 같은 규율).
	}
	return client.IgnoreNotFound(r.Delete(ctx, &live))
}

// mergeDeployment 는 우리가 소유하는 필드만 live 위에 얹는다. live.Spec 을 통째로 갈아끼우면
// API server 가 채워 넣은 기본값(dnsPolicy·strategy·imagePullPolicy…)이 매번 지워졌다 다시
// 채워져 "항상 다름" 이 되고, 그 결과 재조정마다 rolling restart 가 난다.
func mergeDeployment(live, want *appsv1.Deployment) *appsv1.Deployment {
	out := live.DeepCopy()
	// 라벨은 우리 키만 맞춘다. 통째로 갈아끼우면 운영자가 붙인 quiesce opt-in
	// (npu.ai/quiesce-on-driver-upgrade)이 2분 안에 지워져 이 워크로드는 영영 quiesce 대상이 못 된다.
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	for k, v := range want.Labels {
		out.Labels[k] = v
	}
	// driver upgrade quiesce 가 scale=0 으로 내려둔 동안에는 replicas 를 되돌리지 않는다.
	// 되돌리면 rmmod 창 안에서 GPU 를 쥔 Pod 이 다시 뜬다. 이 annotation 은 quiesce 가 걸 때
	// 쓰고 복구할 때 지우므로(upgrade.QuiesceLabeledDeployments/restore), 창이 닫히면 저절로 풀린다.
	if _, quiesced := out.Annotations[upgrade.QuiesceReplicasBackupAnnotation]; !quiesced {
		out.Spec.Replicas = want.Spec.Replicas
	}
	out.Spec.Selector = want.Spec.Selector
	out.Spec.Template.Labels = want.Spec.Template.Labels
	out.Spec.Template.Spec.Affinity = want.Spec.Template.Spec.Affinity
	// 이 필드도 우리 소유다 — 안 맞추면 이 수정 이전에 만들어진 Deployment 는 영영 runtimeClass 없이 남는다.
	out.Spec.Template.Spec.RuntimeClassName = want.Spec.Template.Spec.RuntimeClassName
	// DRA 경로의 claim 참조도 우리 소유다 — 빠뜨리면 기존 Deployment 는 재조정을 거쳐도 영영
	// pod.spec.resourceClaims 없이 남아 컨테이너의 claims 참조가 허공을 가리킨다.
	out.Spec.Template.Spec.ResourceClaims = want.Spec.Template.Spec.ResourceClaims
	wc := want.Spec.Template.Spec.Containers[0]
	if len(out.Spec.Template.Spec.Containers) != 1 {
		out.Spec.Template.Spec.Containers = want.Spec.Template.Spec.Containers
		return out
	}
	c := &out.Spec.Template.Spec.Containers[0]
	c.Name, c.Image, c.Command, c.Args, c.Env, c.Resources = wc.Name, wc.Image, wc.Command, wc.Args, wc.Env, wc.Resources
	return out
}

func (r *AcceleratorWorkloadReconciler) rejected(ctx context.Context, aw *npuv1alpha1.AcceleratorWorkload, reason, axis, message string) (ctrl.Result, error) {
	// condition 을 덮기 전에 직전 사유를 본다 — 아래 이벤트 발행이 "바뀐 것만" 이어야 한다.
	changed := conditionReasonChanged(aw, npuv1alpha1.AWCondTranslated, reason)
	aw.Status.ObservedGeneration = aw.Generation
	aw.Status.Phase = npuv1alpha1.AWPhaseRejected
	// 더 이상 성립하지 않는 번역 결과를 남겨두면 사용자가 그 리소스명을 아직 유효한 것으로 읽는다.
	aw.Status.Resolved = nil
	// 축을 구조로 남긴다. condition message 는 그대로 둔다 — 사람이 읽는 경로이자
	// 구버전 객체를 읽는 소비자의 폴백이다.
	aw.Status.Rejection = &npuv1alpha1.WorkloadRejection{Axis: axis, Reason: reason, Message: message}
	apimeta.SetStatusCondition(&aw.Status.Conditions, metav1.Condition{
		Type: npuv1alpha1.AWCondTranslated, Status: metav1.ConditionFalse,
		Reason: reason, Message: message, ObservedGeneration: aw.Generation,
	})
	// 거절된 워크로드는 Ready 가 아니다. spec 이 그대로면 observedGeneration 으로는 낡음을
	// 알 수 없으므로, 예전 조정이 남긴 WorkloadReady=True 를 여기서 직접 내려야 한다.
	apimeta.SetStatusCondition(&aw.Status.Conditions, metav1.Condition{
		Type: npuv1alpha1.AWCondWorkloadReady, Status: metav1.ConditionFalse,
		Reason: reason, Message: message, ObservedGeneration: aw.Generation,
	})
	// 사유가 그대로면 이벤트를 다시 내지 않는다 — 2분 주기 × 영구 거절이면 하루 ~720개가 쌓여
	// describe 를 못 읽게 만들고 etcd 를 흔든다.
	if r.Recorder != nil && changed {
		r.Recorder.Event(aw, corev1.EventTypeWarning, reason, message)
	}
	// 클러스터가 바뀌면 판정도 바뀌므로 주기적으로 다시 본다(에러로 올리지 않는다 — 사용자 입력 문제다).
	return ctrl.Result{RequeueAfter: awRequeueInterval}, r.Status().Update(ctx, aw)
}

// translated 는 번역 성공 status 를 쓴다. blockedReason 이 비어 있지 않으면 Deployment 를
// 실현하지 못한 것이므로 WorkloadReady 를 그 사유로 False 로 남긴다.
func (r *AcceleratorWorkloadReconciler) translated(ctx context.Context, aw *npuv1alpha1.AcceleratorWorkload,
	res *intent.Result, dep *appsv1.Deployment, blockedReason, blockedMessage string) error {
	// 실현 실패 사유는 WorkloadReady 에 실리므로 그 condition 의 직전 사유와 비교한다
	// (Translated 는 이 경로에서 언제나 AWReasonTranslated 라 변화를 담지 못한다).
	blockedChanged := conditionReasonChanged(aw, npuv1alpha1.AWCondWorkloadReady, blockedReason)
	aw.Status.ObservedGeneration = aw.Generation
	resolved := *res
	aw.Status.Resolved = &resolved
	// 번역이 성공했으면 이전 거절을 지운다(남으면 고친 뒤에도 거절로 보인다).
	aw.Status.Rejection = nil
	apimeta.SetStatusCondition(&aw.Status.Conditions, metav1.Condition{
		Type: npuv1alpha1.AWCondTranslated, Status: metav1.ConditionTrue,
		Reason: npuv1alpha1.AWReasonTranslated, Message: res.Explanation, ObservedGeneration: aw.Generation,
	})

	ready := metav1.Condition{Type: npuv1alpha1.AWCondWorkloadReady, Status: metav1.ConditionFalse,
		Reason: npuv1alpha1.AWReasonWorkloadNotReady, Message: "no pod is ready yet", ObservedGeneration: aw.Generation}
	aw.Status.Phase = npuv1alpha1.AWPhaseTranslated
	switch {
	case blockedReason != "":
		ready.Reason, ready.Message = blockedReason, blockedMessage
		if r.Recorder != nil && blockedChanged {
			r.Recorder.Event(aw, corev1.EventTypeWarning, blockedReason, blockedMessage)
		}
	case dep != nil && dep.Status.ReadyReplicas > 0:
		aw.Status.Phase = npuv1alpha1.AWPhaseRunning
		ready.Status, ready.Reason = metav1.ConditionTrue, npuv1alpha1.AWReasonWorkloadReady
		ready.Message = fmt.Sprintf("%d/%d pods ready", dep.Status.ReadyReplicas, dep.Status.Replicas)
	}
	apimeta.SetStatusCondition(&aw.Status.Conditions, ready)
	return r.Status().Update(ctx, aw)
}

// conditionReasonChanged 는 그 condition 의 사유가 지금 쓰려는 것과 다른지 본다.
// 아직 그 condition 이 없으면(첫 조정) 바뀐 것으로 본다 — 첫 사유는 반드시 이벤트로 알린다.
func conditionReasonChanged(aw *npuv1alpha1.AcceleratorWorkload, condType, reason string) bool {
	cur := apimeta.FindStatusCondition(aw.Status.Conditions, condType)
	return cur == nil || cur.Reason != reason
}

// renderDeployment 는 번역 결과를 그대로 Pod 사양으로 옮긴다.
// 노드 후보는 nodeAffinity 로 박는다 — RNGD 처럼 파티션을 flat 광고하는 벤더는 리소스명만으로
// 올바른 노드를 고를 수 없기 때문이다.
func renderDeployment(aw *npuv1alpha1.AcceleratorWorkload, res *intent.Result) *appsv1.Deployment {
	replicas := int32(1)
	if aw.Spec.Workload.Replicas != nil {
		replicas = *aw.Spec.Workload.Replicas
	}
	selector := map[string]string{awWorkloadLabel: aw.Name}
	podLabels := map[string]string{
		awWorkloadLabel:                aw.Name,
		"app.kubernetes.io/managed-by": "kcloud-operator",
		podInjectLabel:                 labelValueTrue,
	}
	// DRA 경로는 extended resource 를 쓰지 않는다 — limits 까지 채우면 스케줄러가 같은 장치를
	// device-plugin 자원과 DRA claim 양쪽으로 이중 요청한다. 대신 pod.spec.resourceClaims 를
	// 가리키는 claim 참조를 컨테이너에 단다.
	var limits corev1.ResourceList
	var claims []corev1.ResourceClaim
	var podClaims []corev1.PodResourceClaim
	if res.AllocationAPI == npuv1alpha1.AllocationAPIDRA {
		tmplName := draClaimTemplateName(aw.Name)
		podClaims = []corev1.PodResourceClaim{{Name: draClaimName, ResourceClaimTemplateName: &tmplName}}
		claims = []corev1.ResourceClaim{{Name: draClaimName}}
	} else {
		limits = corev1.ResourceList{
			corev1.ResourceName(res.ResourceName): *resource.NewQuantity(int64(res.Quantity), resource.DecimalSI),
		}
	}
	// NVIDIA 는 runtimeClass 를 여기서 직접 박는다. 노드의 containerd 기본 런타임은 runc 이고
	// nvidia hook 은 이 핸들러에서만 돌므로, 없으면 컨테이너는 /dev/nvidia* 없이 뜨면서도 Pod 은
	// Ready 가 되어 Running 이라고 거짓말한다. PodMutator 는 webhook.enabled=false(차트 기본값)
	// 면 아예 불리지 않으므로 거기 기댈 수 없다(podInjectLabel 은 계속 붙여도 무해한 이중 방어다).
	// runtimeClass 판정은 벤더마다 달라 driveradapter 가 담당하고, 조회 키는 대소문자를 구분한다.
	var runtimeClass *string
	if a, ok := driveradapter.Lookup(res.Vendor, ""); ok {
		if rc := a.RuntimeClass(); rc != "" {
			runtimeClass = &rc
		}
	}
	var affinity *corev1.Affinity
	if len(res.Nodes) > 0 {
		affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: corev1.LabelHostname, Operator: corev1.NodeSelectorOpIn, Values: res.Nodes,
					}},
				}},
			},
		}}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: aw.Name, Namespace: aw.Namespace, Labels: selector},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					Affinity:         affinity,
					RuntimeClassName: runtimeClass,
					ResourceClaims:   podClaims,
					Containers: []corev1.Container{{
						Name:      "workload",
						Image:     aw.Spec.Workload.Image,
						Command:   aw.Spec.Workload.Command,
						Args:      aw.Spec.Workload.Args,
						Env:       aw.Spec.Workload.Env,
						Resources: corev1.ResourceRequirements{Limits: limits, Claims: claims},
					}},
				},
			},
		},
	}
}

// renderResourceClaimTemplate 은 DRA 경로에서만 쓴다. Pod 이 참조할 accel claim 을 만든다.
// device-plugin 경로에서는 nil 이다 — Reconcile 은 nil 이면 이 리소스를 아예 건드리지 않는다.
func renderResourceClaimTemplate(aw *npuv1alpha1.AcceleratorWorkload, res *intent.Result) *resourcev1.ResourceClaimTemplate {
	if res.AllocationAPI != npuv1alpha1.AllocationAPIDRA {
		return nil
	}
	return &resourcev1.ResourceClaimTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: draClaimTemplateName(aw.Name), Namespace: aw.Namespace},
		Spec: resourcev1.ResourceClaimTemplateSpec{
			Spec: resourcev1.ResourceClaimSpec{
				Devices: resourcev1.DeviceClaim{
					Requests: []resourcev1.DeviceRequest{{
						Name: draClaimName,
						Exactly: &resourcev1.ExactDeviceRequest{
							DeviceClassName: res.DeviceClassName,
							Count:           int64(res.Quantity),
						},
					}},
				},
			},
		},
	}
}

func (r *AcceleratorWorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.AcceleratorWorkload{}).
		Owns(&appsv1.Deployment{}).
		Owns(&resourcev1.ResourceClaimTemplate{}).
		Complete(r)
}
