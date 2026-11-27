// ============================================================
// quiesce.go: 장치 변경 전 노드 quiesce 오케스트레이션 (cordon → 배출 → 검증 → 복원)
// 상세: MIG mode enable 등 "워크로드가 없어야 하는" 변경의 공통 선결 절차다. 점유 판정은
// device-plugin(resources.limits)과 DRA(ResourceClaim) 두 축을 모두 본다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package partition

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/internal/draclaim"
)

// Quiescer 는 한 벤더의 장치 리소스를 점유한 워크로드를 노드에서 비우는 오케스트레이터다.
type Quiescer struct {
	Client client.Client
	// ResourcePrefixes 는 접두사 매칭 리소스(예: "nvidia.com/mig-").
	ResourcePrefixes []string
	// ExtraResources 는 정확 일치 리소스(예: "nvidia.com/gpu").
	ExtraResources []string
}

// NvidiaQuiescer 는 nvidia.com/gpu 및 MIG 조각 점유 워크로드를 대상으로 한다.
func NvidiaQuiescer(c client.Client) *Quiescer {
	return &Quiescer{Client: c, ResourcePrefixes: []string{"nvidia.com/mig-"}, ExtraResources: []string{"nvidia.com/gpu"}}
}

// RngdQuiescer 는 furiosa.ai/rngd 점유 워크로드를 대상으로 한다.
func RngdQuiescer(c client.Client) *Quiescer {
	return &Quiescer{Client: c, ExtraResources: []string{"furiosa.ai/rngd"}}
}

// holdsDevice 는 pod 이 대상 리소스를 점유하는지다(containers + initContainers).
//
// DRA 로 뜬 pod 은 resources.limits 가 비어 있다 — 장치를 extended resource 가 아니라
// pod.spec.resourceClaims 로 쥐기 때문이다. limits 만 보면 그 pod 은 배출 대상에서 빠지고
// 그대로 -mig 1 이 나간다. claim 을 DeviceClass→driver 로 되짚어 벤더를 가리지는 않는다 —
// 안전 게이트라 과하게 잡는 쪽이 옳다(모자라면 장치가 밑에서 재구성된다).
func (q *Quiescer) holdsDevice(p *corev1.Pod) bool {
	if len(p.Spec.ResourceClaims) > 0 {
		return true
	}
	for _, ctrs := range [][]corev1.Container{p.Spec.Containers, p.Spec.InitContainers} {
		for _, ctr := range ctrs {
			for name := range ctr.Resources.Limits {
				n := string(name)
				for _, e := range q.ExtraResources {
					if n == e {
						return true
					}
				}
				for _, pre := range q.ResourcePrefixes {
					if strings.HasPrefix(n, pre) {
						return true
					}
				}
			}
		}
	}
	return false
}

// mirrorPodAnnotationKey 는 kubelet 이 static pod 미러에 붙이는 annotation 이다
// (k8s.io/kubernetes/pkg/kubelet/types.ConfigMirrorAnnotationKey 와 동일 값 — 의존성 추가 없이 리터럴 사용).
const mirrorPodAnnotationKey = "kubernetes.io/config.mirror"

// isExemptPod 는 배출 대상에서 제외되는 pod 인지다: DaemonSet 소유(device-plugin 자신 등, 삭제해도
// 즉시 재생성) + static/mirror pod(Node 소유이거나 mirror annotation 보유 — API 서버로 evict/delete 불가).
// 이 예외를 놓치면 그런 pod 이 장치를 점유할 때 EvictDeviceWorkloads 가 evict 실패로 전체
// 중단되거나 AssertQuiesced 가 영구히 실패한다.
func isExemptPod(p *corev1.Pod) bool {
	if _, mirrored := p.Annotations[mirrorPodAnnotationKey]; mirrored {
		return true
	}
	for _, o := range p.OwnerReferences {
		if o.Kind == "DaemonSet" || o.Kind == "Node" {
			return true
		}
	}
	return false
}

// Cordon 은 노드를 unschedulable 로 만든다(멱등).
func (q *Quiescer) Cordon(ctx context.Context, node string) error {
	var n corev1.Node
	if err := q.Client.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return err
	}
	if n.Spec.Unschedulable {
		return nil
	}
	base := n.DeepCopy()
	n.Spec.Unschedulable = true
	return q.Client.Patch(ctx, &n, client.MergeFrom(base))
}

// Uncordon 은 Cordon 의 역이다(멱등).
func (q *Quiescer) Uncordon(ctx context.Context, node string) error {
	var n corev1.Node
	if err := q.Client.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return err
	}
	if !n.Spec.Unschedulable {
		return nil
	}
	base := n.DeepCopy()
	n.Spec.Unschedulable = false
	return q.Client.Patch(ctx, &n, client.MergeFrom(base))
}

// EvictDeviceWorkloads 는 장치 점유 pod 을 Eviction API 로 배출하고(PDB 존중), 배출 대상에서
// 제외된(isExemptPod) 장치 점유 pod 수를 참고용으로 돌려준다.
//
// remaining 은 정보성 카운트일 뿐 완료·재시도 신호가 아니다 — DaemonSet/static pod 은 영구히
// 배출되지 않으므로 이 값이 0 이 되기를 기다리며 requeue 하면 무한 대기할 수 있다. 완료 판정은
// 오직 AssertQuiesced(같은 예외 규칙 적용)가 nil 을 돌려주는지로만 한다.
func (q *Quiescer) EvictDeviceWorkloads(ctx context.Context, node string) (int, error) {
	var pods corev1.PodList
	if err := q.Client.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node}); err != nil {
		return 0, err
	}
	remaining := 0
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if !q.holdsDevice(p) {
			continue
		}
		if isExemptPod(p) {
			remaining++
			continue
		}
		eviction := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace}}
		if err := q.Client.SubResource("eviction").Create(ctx, p, eviction); err != nil {
			if apierrors.IsNotFound(err) {
				continue // 이미 사라짐 — 배출 완료로 취급.
			}
			return remaining, fmt.Errorf("evict pod %s/%s: %w", p.Namespace, p.Name, err)
		}
	}
	return remaining, nil
}

// AssertQuiesced 는 cordon 됨 + 장치 점유 non-exempt(isExemptPod) pod 이 없음 + 이 노드에 할당된
// ResourceClaim 이 없음을 검증한다.
//
// claim 축을 pod 축과 함께 여기서 보는 이유: DRA 는 장치를 Pod 이 아니라 ResourceClaim 이 쥔다.
// Pod 이 사라져도 claim 이 아직 deallocate 되지 않았으면 장치는 여전히 잡혀 있고, 반대로
// 다른 컨트롤러가 만든 claim 은 대응하는 Pod 이 이 노드에 없을 수도 있다.
func (q *Quiescer) AssertQuiesced(ctx context.Context, node string) error {
	var n corev1.Node
	if err := q.Client.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return err
	}
	if !n.Spec.Unschedulable {
		return fmt.Errorf("node %s not cordoned", node)
	}
	var pods corev1.PodList
	if err := q.Client.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node}); err != nil {
		return err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if q.holdsDevice(p) && !isExemptPod(p) {
			return fmt.Errorf("node %s still has device-holding pod %s", node, p.Name)
		}
	}
	return draclaim.AssertNoneAllocatedOnNode(ctx, q.Client, n.Name)
}

// DeviceHoldingPods 는 이 노드에서 장치를 쥐고 있는 pod 들이다(namespace/name). cordon 여부를
// 묻지 않는다 — "지금 비어 있는가" 만 답한다. 점유 판정 규칙은 holdsDevice 한 곳에만 둔다.
func (q *Quiescer) DeviceHoldingPods(ctx context.Context, node string) ([]string, error) {
	var pods corev1.PodList
	if err := q.Client.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node}); err != nil {
		return nil, err
	}
	var out []string
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if q.holdsDevice(p) && !isExemptPod(p) {
			out = append(out, p.Namespace+"/"+p.Name)
		}
	}
	return out, nil
}
