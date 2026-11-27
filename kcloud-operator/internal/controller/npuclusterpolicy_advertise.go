// ============================================================
// npuclusterpolicy_advertise.go: 장치 광고 주체 선택 스위치(advertiseBy) 처리
// 상세: spec 에서 dra 로 선언된 벤더의 대상 노드에 dra-owned 라벨을 붙이고, 선언을 되돌리면
//       회수한다. 그 라벨이 있는 노드에서는 벤더 device-plugin DaemonSet 이 물러난다
//       (제외는 각 DaemonSet 렌더링에서 affinity 로 건다).
// 생성일: 2026-08-06 | 수정일: 2026-10-13
// ============================================================

package controller

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/equality"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
	"kcloud-operator/internal/partition"
)

// 광고 주체 축을 갖는 벤더 중 이 패키지에 아직 상수가 없던 둘. Rebellions ATOM 상수
// (vendorRebellions)는 dra_driver.go 에 이미 있어 여기 새로 선언하지 않는다.
const (
	vendorRngd        = "rngd"
	vendorTenstorrent = "tenstorrent"
)

// advertiseVendors 는 광고 주체 축을 갖는 벤더와 그 선언이다. 키는 vendorTable 의 다섯 벤더다.
func advertiseVendors(p *npuv1alpha1.NPUClusterPolicy) map[string]npuv1alpha1.AdvertiseSpec {
	// nil 정책은 빈 정책으로 본다 — 키 집합은 spec 값과 무관하게 다섯 벤더로 고정이라,
	// 정책 객체가 없는 자리(삭제 경로·시험)에서 불려도 같은 키를 돌려준다.
	if p == nil {
		p = &npuv1alpha1.NPUClusterPolicy{}
	}
	out := make(map[string]npuv1alpha1.AdvertiseSpec, len(vendorTable))
	for v, a := range vendorTable {
		out[v] = a.advertise(p)
	}
	return out
}

// vendorPresentLabel 은 그 벤더 장치가 이 노드에 있다는 자립 라벨이다(node-manager 부여).
func vendorPresentLabel(vendor string) string {
	return npuv1alpha1.PresentNodeLabel(vendor)
}

// vendorIsOperatorOwned 는 벤더 키를 배포 주체 여부로 매핑한다 — kcloud 가 배포하는 managed
// 일 때만 참이다. delegated·external 은 kcloud 가 소유하지 않으므로 거짓이다(vendorTable 을
// 읽는다). 배포·회수·광고 주체를 다루는 자리는 이 하나로 물어, 모드 문자열을 벤더마다 흩어
// 비교하지 않는다.
func vendorIsOperatorOwned(p *npuv1alpha1.NPUClusterPolicy, vendor string) bool {
	if a, ok := vendorTable[vendor]; ok {
		return a.owned(p)
	}
	// 표에 없는 벤더는 delegated·external 로 볼 근거가 없으므로 owned 로 본다 — 배포·회수를
	// 건너뛰지 않고 정상 경로가 처리한다(예: 렌더러 없는 벤더를 Blocked 로 거절).
	return true
}

// vendorDRAEnabled 는 이 벤더의 spec.<vendor>.dra.enabled 값이다. dra-retired 라벨
// 갱신에 쓴다 — dra.enabled 가 꺼지면 그 벤더의 DRA DaemonSet 자체가 배포되지 않으므로
// retired 로 스케줄을 막아 둘 이유가 없다(vendorTable 을 읽는다).
func vendorDRAEnabled(p *npuv1alpha1.NPUClusterPolicy, vendor string) bool {
	if a, ok := vendorTable[vendor]; ok {
		return a.draEnabled(p)
	}
	return false
}

// reconcileDRAOwnedLabels 는 spec 과 노드 라벨을 맞춘다. 부여와 회수가 같은 함수에 있어야
// 고아 라벨이 남지 않는다 — 붙이기만 하는 코드는 되돌릴 길을 만들지 않는다.
func (r *NPUClusterPolicyReconciler) reconcileDRAOwnedLabels(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) error {
	logger := logf.FromContext(ctx)

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return fmt.Errorf("노드 목록 조회 실패: %w", err)
	}

	// DRA 발행 실측. resource.k8s.io 가 없는 클러스터에서는 빈 값이 되고, 그러면 dra 전환은
	// 전부 거절된다 — device-plugin 을 끄기 전에 넘겨받을 쪽이 실재하는지부터 본다.
	dra, err := intent.LoadDRACapability(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("DRA 발행 상태 조회 실패: %w", err)
	}

	var switches []npuv1alpha1.AdvertiseSwitchStatus

	for vendor, adv := range advertiseVendors(policy) {
		// delegated·external 벤더는 kcloud 가 광고 주체(device-plugin/DRA)를 배포하지 않아 어느
		// 쪽이 광고하는지 모르므로 dra-owned 라벨을 관리하지 않는다 — managed 만 이 라벨을 다룬다.
		if !vendorIsOperatorOwned(policy, vendor) {
			continue
		}
		key := npuv1alpha1.DRAOwnedNodeLabel(vendor)
		retiredKey := npuv1alpha1.DRARetiredNodeLabel(vendor)
		draEnabled := vendorDRAEnabled(policy, vendor)
		// dra.enabled=false 로 retired 를 지우기 전에 그 벤더의 DRA DaemonSet 이 실제로
		// 사라졌는지 본다 — 아직 있는데 먼저 지우면 그 순간 노드가 다시 스케줄 후보가 돼
		// DaemonSet 컨트롤러가 새 파드를 잠깐 올렸다 곧 삭제와 함께 지우는 경합이 생긴다.
		// 벤더당 한 번만 조회한다 — 노드마다 다시 묻지 않는다.
		dsExists, err := r.draDaemonSetExists(ctx, advertiseVendorDRADSName(vendor))
		if err != nil {
			return fmt.Errorf("%s DRA DaemonSet 조회 실패: %w", vendor, err)
		}

		// devicePlugin(기본)이면 대상 노드가 없다 → 남아 있는 라벨은 전부 회수된다.
		selector := labels.Nothing()
		if adv.AdvertisedBy() == npuv1alpha1.AdvertiseByDRA {
			// nil selector 는 "안 좁힌다" 는 뜻이다. LabelSelectorAsSelector(nil) 은
			// Nothing 을 돌려주므로(=아무 노드도 안 맞음) 여기서 Everything 으로 갈라 준다.
			selector = labels.Everything()
			if adv.AdvertiseByNodeSelector != nil {
				s, err := metav1.LabelSelectorAsSelector(adv.AdvertiseByNodeSelector)
				if err != nil {
					return fmt.Errorf("%s advertiseByNodeSelector 해석 실패: %w", vendor, err)
				}
				selector = s
			}
		}

		mode := adv.AdvertisedBy()
		for i := range nodes.Items {
			node := &nodes.Items[i]
			present := node.Labels[vendorPresentLabel(vendor)] == labelValueTrue
			want := present && selector.Matches(labels.Set(node.Labels))
			_, has := node.Labels[key]

			// retired 라벨은 dra-owned 판정보다 먼저 정리한다 — want=true 로 돌아올 때
			// 발행자가 먼저 이 노드에 스케줄될 수 있어야(라벨이 먼저 지워져야)
			// refuseAdvertiseSwitch 의 발행 확인이 통과할 길이 생긴다. 이번 reconcile 에
			// dra-owned 자체는 거절돼도(발행자 미확인) retired 를 지우는 것은 무방하다.
			if err := r.reconcileDRARetiredLabel(ctx, node, retiredKey, want, has, draEnabled, dsExists); err != nil {
				return err
			}

			// devicePlugin 선언 벤더의 장치 노드도 항목을 남긴다 — 남은 dra-owned 라벨은 회수 대기다.
			if present && adv.AdvertisedBy() != npuv1alpha1.AdvertiseByDRA {
				entry := npuv1alpha1.AdvertiseSwitchStatus{Vendor: vendor, Node: node.Name, Mode: mode, Applied: !has}
				if has {
					entry.Reason = "dra-owned 라벨이 남아 있어 device-plugin 광고로 되돌리는 중"
				}
				switches = append(switches, entry)
			}

			// 새로 넘기려는 노드는 두 가지를 먼저 통과해야 한다. 이미 넘어간 노드는 다시 묻지
			// 않는다 — 되돌리는 길(want=false)은 어떤 경우에도 막지 않는다.
			if want && !has {
				refusal, perr := r.refuseAdvertiseSwitch(ctx, vendor, node.Name, dra)
				if perr != nil {
					return perr
				}
				if refusal != nil {
					refusal.Vendor, refusal.Node, refusal.Mode = vendor, node.Name, mode
					switches = append(switches, *refusal)
					r.Recorder.Eventf(policy, corev1.EventTypeWarning, "AdvertiseSwitchRefused",
						"노드 %s 의 %s 광고 주체 전환 거절: %s", node.Name, vendor, refusal.Reason)
					continue
				}
			}
			if want {
				switches = append(switches, npuv1alpha1.AdvertiseSwitchStatus{
					Vendor: vendor, Node: node.Name, Mode: mode, Applied: true,
				})
			}
			if want == has {
				continue
			}

			patch := client.MergeFrom(node.DeepCopy())
			if want {
				if node.Labels == nil {
					node.Labels = map[string]string{}
				}
				node.Labels[key] = labelValueTrue
			} else {
				delete(node.Labels, key)
			}
			if err := r.Patch(ctx, node, patch); err != nil {
				return fmt.Errorf("노드 %s 라벨 %s 갱신 실패: %w", node.Name, key, err)
			}
			logger.Info("DRA 소유 라벨 갱신", "node", node.Name, "label", key, "applied", want)
		}
	}

	return r.recordAdvertiseSwitch(ctx, policy, switches)
}

// reconcileDRARetiredLabel 은 노드 하나의 dra-retired 라벨을 dra-owned 회수·재획득에 맞춰 갱신한다.
// 부여: want=false 인데 has=true(dra-owned 회수 시점)이고 draEnabled 면 부여한다.
// 회수: want=true(다시 DRA 로)면 dsExists 와 무관하게 즉시 지운다. !draEnabled 면 dsExists=false
// 일 때만 지운다 — DaemonSet 이 아직 있는데 먼저 지우면 노드가 다시 스케줄 후보가 돼 그
// DaemonSet 컨트롤러가 새 파드를 잠깐 올렸다 삭제와 함께 지우는 경합이 생긴다. 그 외에는 이전
// 값을 그대로 둔다 — 회수된 채로 있는 동안 sticky 해야 다음 reconcile 도 이 노드를 피한다.
func (r *NPUClusterPolicyReconciler) reconcileDRARetiredLabel(
	ctx context.Context, node *corev1.Node, retiredKey string, want, has, draEnabled, dsExists bool,
) error {
	_, wasRetired := node.Labels[retiredKey]
	retired := wasRetired
	switch {
	case want:
		retired = false
	case !draEnabled && !dsExists:
		retired = false
	case has:
		retired = true
	}
	if retired == wasRetired {
		return nil
	}
	patch := client.MergeFrom(node.DeepCopy())
	if retired {
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		node.Labels[retiredKey] = labelValueTrue
	} else {
		delete(node.Labels, retiredKey)
	}
	return r.Patch(ctx, node, patch)
}

// refuseAdvertiseSwitch 는 이 노드를 지금 DRA 소유로 넘겨도 되는지 본다. 넘기면 안 되는
// 이유가 있으면 그 사유를, 없으면 nil 을 돌려준다.
func (r *NPUClusterPolicyReconciler) refuseAdvertiseSwitch(
	ctx context.Context, vendor, node string, dra intent.DRACapability,
) (*npuv1alpha1.AdvertiseSwitchStatus, error) {
	// ① 넘겨받을 쪽이 실재하는가 — 이 벤더 자신의 드라이버여야 한다. 노드에 ResourceSlice 가
	// 있다는 사실만으로는 부족하다 — 그게 다른 벤더의 드라이버가 낸 것이면(예: nvidia 슬라이스가
	// 있는 노드에서 tenstorrent 로 전환) device-plugin 을 끄는 순간 이 벤더의 광고는 양쪽 다
	// 0 이 된다. 드라이버 이름은 렌더 함수가 쓰는 것과 같은 상수(advertiseVendorDriverName,
	// dra_driver.go)에서 가져온다.
	driverName := advertiseVendorDriverName(vendor)
	if dra.SlicesByNodeDriver[node][driverName] == 0 {
		return &npuv1alpha1.AdvertiseSwitchStatus{
			Reason: fmt.Sprintf("이 노드에서 %s 드라이버가 ResourceSlice 를 발행하지 않는다 — 드라이버를 먼저 설치하라", driverName),
		}, nil
	}

	// ② 지금 장치를 쥐고 있는 워크로드가 있는가. 판정은 Quiescer 를 그대로 쓴다 —
	// 점유 규칙을 여기에 다시 쓰면 두 규칙이 갈라진다.
	pods, err := advertiseQuiescer(r.Client, vendor).DeviceHoldingPods(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("노드 %s 점유 조회 실패: %w", node, err)
	}
	if len(pods) > 0 {
		return &npuv1alpha1.AdvertiseSwitchStatus{
			Reason:       "장치를 쥔 워크로드가 있다 — 노드를 비우고 다시 시도하라",
			BlockingPods: pods,
		}, nil
	}
	return nil, nil
}

// advertiseQuiescer 는 벤더별 점유 판정기다. 매핑에 없는 벤더는 DRA claim 축만 보는
// 기본 판정기를 쓴다(holdsDevice 는 resourceClaims 를 벤더 무관으로 잡는다).
func advertiseQuiescer(c client.Client, vendor string) *partition.Quiescer {
	switch vendor {
	case vendorNvidia:
		return partition.NvidiaQuiescer(c)
	case vendorRngd:
		return partition.RngdQuiescer(c)
	default:
		return &partition.Quiescer{Client: c}
	}
}

// recordAdvertiseSwitch 는 노드별 전환 판정을 status 에 남긴다. 내용이 그대로면 쓰지 않는다 —
// 매 reconcile 마다 status 를 갱신하면 watch 가 자기 자신을 깨운다.
func (r *NPUClusterPolicyReconciler) recordAdvertiseSwitch(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, switches []npuv1alpha1.AdvertiseSwitchStatus,
) error {
	sort.Slice(switches, func(i, j int) bool {
		if switches[i].Vendor != switches[j].Vendor {
			return switches[i].Vendor < switches[j].Vendor
		}
		return switches[i].Node < switches[j].Node
	})
	if equality.Semantic.DeepEqual(policy.Status.AdvertiseSwitch, switches) {
		return nil
	}
	patch := client.MergeFrom(policy.DeepCopy())
	policy.Status.AdvertiseSwitch = switches
	if err := r.Status().Patch(ctx, policy, patch); err != nil {
		return fmt.Errorf("광고 주체 전환 상태 기록 실패: %w", err)
	}
	return nil
}

// excludeDRAOwnedNodes 는 이 벤더의 dra-owned 노드를 DaemonSet 스케줄 후보에서 뺀다.
// 라벨이 붙은 노드가 없으면 아무 영향이 없으므로 조건 없이 건다 — 분기를 두면 spec 을
// 되돌린 직후 한 박자 동안 두 광고가 겹칠 수 있다.
func excludeDRAOwnedNodes(spec *corev1.PodSpec, vendor string) {
	appendNodeAffinityRequirements(spec, corev1.NodeSelectorRequirement{
		Key:      npuv1alpha1.DRAOwnedNodeLabel(vendor),
		Operator: corev1.NodeSelectorOpDoesNotExist,
	})
}

// excludeDRARetiredNodes 는 이 벤더의 dra-retired 노드를 DRA DaemonSet 스케줄 후보에서
// 뺀다. excludeDRAOwnedNodes 와 대칭이지만 반대쪽(DRA 렌더)에 건다 — 광고를 device-plugin
// 으로 되돌린 노드에서 DRA 드라이버 파드가 계속 떠 있으면 ResourceSlice 가 지워지지 않고
// 이중광고 창이 닫히지 않는다.
func excludeDRARetiredNodes(spec *corev1.PodSpec, vendor string) {
	appendNodeAffinityRequirements(spec, corev1.NodeSelectorRequirement{
		Key:      npuv1alpha1.DRARetiredNodeLabel(vendor),
		Operator: corev1.NodeSelectorOpDoesNotExist,
	})
}

// reclaimDRANodeLabels 는 정책 삭제 경로에서 다섯 벤더의 dra-owned·dra-retired 라벨을
// 모든 노드에서 지운다. 정책이 없으면 광고 주체를 정하는 자리도 없으므로 두 라벨은
// 뜻을 잃는다 — 남겨 두면 다음 정책이 렌더하는 DRA DaemonSet 이 그 노드를 계속 피한다.
// 벤더 목록은 advertiseVendors 의 키를 그대로 쓴다(목록을 두 곳에 두지 않는다).
func (r *NPUClusterPolicyReconciler) reclaimDRANodeLabels(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy,
) error {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return fmt.Errorf("노드 목록 조회 실패: %w", err)
	}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		patch := client.MergeFrom(node.DeepCopy())
		changed := false
		for vendor := range advertiseVendors(policy) {
			for _, key := range []string{
				npuv1alpha1.DRAOwnedNodeLabel(vendor),
				npuv1alpha1.DRARetiredNodeLabel(vendor),
			} {
				if _, ok := node.Labels[key]; ok {
					delete(node.Labels, key)
					changed = true
				}
			}
		}
		if !changed {
			continue
		}
		if err := r.Patch(ctx, node, patch); err != nil {
			return fmt.Errorf("노드 %s DRA 라벨 회수 실패: %w", node.Name, err)
		}
	}
	return nil
}
