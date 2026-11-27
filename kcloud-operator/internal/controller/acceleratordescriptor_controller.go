// ============================================================
// acceleratordescriptor_controller.go: AcceleratorDescriptor 감시 컨트롤러
// 상세: 검증기·판정기를 돌려 status.feasibleBackends 와 refusals 를 채운다. 배포물
//       적용은 이 컨트롤러가, resolve 는 NDR 카드별 entry(count=1, pcieAddress — report.go fan-out)에서 한다.
// 생성일: 2026-09-15 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/descriptor"
	"kcloud-operator/internal/vendorcap"
	"kcloud-operator/internal/verification"
)

// AcceleratorDescriptorReconciler 는 descriptor 의 판정 결과를 status 에 반영하고,
// 관리되지 않는 벤더의 descriptor 는 backend 배포물을 auto-apply 한다.
type AcceleratorDescriptorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// DPImage/DRAImage 는 생성 device-plugin/DRA DaemonSet 이 쓸 일반 런타임 이미지다.
	// 비면 해당 backend 배포물은 렌더 단계에서 그 이미지로 뜨지 못한다(helm 주입).
	DPImage  string
	DRAImage string
	// GeneratedNamespace 는 namespaced 생성물(ConfigMap/DaemonSet/SA)을 둘 네임스페이스다
	// (operator 자기 네임스페이스).
	GeneratedNamespace string
	// Verification 이 있으면 feasible 판정이 성공한 노드마다 Verify 를 불러 AcceleratorEvidence
	// 를 upsert 한다. ACPP 가 없는 벤더(tenstorrent/rebellions 등)도 이 경로로 근거를 쌓는다.
	// probe Pod 실행은 AllocationProbe 를 켠 AcceleratorVerificationPolicy 가 있을 때만 Verify
	// 내부가 돌린다 — 여기서는 정책 유무를 보지 않는다. nil 이면(테스트 구성) 배선을 건너뛴다.
	Verification *verification.Verifier
}

// +kubebuilder:rbac:groups=npu.ai,resources=acceleratordescriptors,verbs=get;list;watch
// +kubebuilder:rbac:groups=npu.ai,resources=acceleratordescriptors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=npu.ai,resources=nodedevicereports,verbs=get;list;watch
// +kubebuilder:rbac:groups=npu.ai,resources=npuclusterpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete;bind;escalate
// +kubebuilder:rbac:groups=resource.k8s.io,resources=deviceclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=resource.k8s.io,resources=resourceslices;resourceclaims,verbs=get;list;watch

// Reconcile 은 검증과 판정을 돌려 status 를 맞춘다. 배포물은 만들지 않는다.
func (r *AcceleratorDescriptorReconciler) Reconcile(
	ctx context.Context, req ctrl.Request,
) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	var desc npuv1alpha1.AcceleratorDescriptor
	if err := r.Get(ctx, req.NamespacedName, &desc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 정합이 깨진 descriptor 는 어느 backend 로도 갈 수 없다. 판정기를 부르기
	// 전에 거절 사유를 확정해 두어야 "가능" 과 "미판정" 이 섞이지 않는다.
	if errs := descriptor.Validate(desc.Spec); len(errs) > 0 {
		desc.Status.FeasibleBackends = nil
		desc.Status.ResolvedDevices = nil
		desc.Status.Refusals = []npuv1alpha1.DescriptorRefusal{{
			Backend: "*",
			Reason:  fmt.Sprintf("descriptor 정합 오류: %v", errs.ToAggregate()),
		}}
		if err := r.Status().Update(ctx, &desc); err != nil {
			return ctrl.Result{}, fmt.Errorf("status 갱신 실패: %w", err)
		}
		logger.Info("descriptor 정합 오류로 전량 거절", "name", desc.Name)
		return ctrl.Result{}, nil
	}

	feasible, refusals := descriptor.FeasibleBackends(desc.Spec)
	desc.Status.FeasibleBackends = feasible

	var ndrs npuv1alpha1.NodeDeviceReportList
	if err := r.List(ctx, &ndrs); err != nil {
		return ctrl.Result{}, fmt.Errorf("NodeDeviceReport 목록 조회 실패: %w", err)
	}
	desc.Status.ResolvedDevices = resolveDevices(desc.Spec, ndrs.Items)

	// 벤더 evidence probe: ACPP 가 없는 벤더도 근거를 쌓도록, feasible 판정이 성공하고 장치가
	// resolve 된 노드마다 Verify 를 부른다. 아래 auto-apply 판정(vendor-managed·sharing·중복
	// winner·이미지 미설정)과는 독립이다 — 배포물을 안 만들기로 한 descriptor 도 하드웨어가
	// 동작하는지 근거는 남겨야 한다. probe Pod 실행 여부는 Verify 내부가 정책으로 가른다.
	// 노드 하나의 실패로 이 descriptor 전체의 판정·배포물 반영을 막지 않는다(비치명 side effect).
	r.verifyResolvedDevices(ctx, &desc, feasible)

	// 배포물 적용 판정. 중재·발견가능 거절은 status 를 한 번만 쓰도록 여기서
	// refusals 에 접어 넣고, 그 결과로 apply 할지 prune 할지 정한다(이중 Status().Update 회피).
	applyOK := len(feasible) > 0
	if applyOK {
		var policies npuv1alpha1.NPUClusterPolicyList
		if err := r.List(ctx, &policies); err != nil {
			return ctrl.Result{}, fmt.Errorf("NPUClusterPolicy 목록 조회 실패: %w", err)
		}
		switch {
		case descriptorVendorManaged(desc.Spec, policies.Items):
			// 중재(자문형): 벤더가 NPUClusterPolicy 관리하 — 같은 /dev 이중 광고 방지로 거절.
			refusals = append(refusals, npuv1alpha1.DescriptorRefusal{
				Backend: "*",
				Reason: fmt.Sprintf("vendor %s 는 NPUClusterPolicy 가 관리 중 — "+
					"이중 광고 방지로 auto-apply 거절", desc.Spec.Vendor),
			})
			applyOK = false
		default:
			if reason := autoApplyDiscoveryRefusal(desc.Spec, ndrs.Items); reason != "" {
				refusals = append(refusals, npuv1alpha1.DescriptorRefusal{
					Backend: "*", Reason: reason,
				})
				applyOK = false
			}
		}
	}
	// sharing(SW 시분할) 게이트: registry 가 그 vendor/product 의 timeSlicing
	// 을 Software 로 선언하지 않으면 거절한다 — 동시 접근 안전 실측 없이 격리 없는 공유를
	// 조용히 내보내지 않는다. gocyclo 상한(30) 때문에 판정은 sharingRefusal 헬퍼로 뺐다.
	if applyOK {
		if rf := sharingRefusal(desc.Spec); rf != nil {
			refusals = append(refusals, *rf)
			applyOK = false
		}
	}
	// 같은 vendor/product 를 선언한 descriptor 가 여럿이면 같은 리소스명·DeviceClass 를 두고
	// SSA 소유권을 다툰다(같은 하드웨어 이중 광고). 이름 사전순 먼저인 하나만 적용하고 나머지는
	// 거절한다 — 결정적이라 flapping 이 없다(winner 삭제 시 sibling watch 로 재평가).
	// ponytail: winner 를 "사전순 먼저"로 단순화 — winner 가 다른 사유로 미적용이면 loser 도
	// 함께 거절될 수 있으나(같은 하드웨어라 안전측), 적용가능한 winner 선출은 sibling 별 평가가 필요.
	if applyOK {
		winner, err := r.duplicateWinnerName(ctx, &desc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if winner != desc.Name {
			refusals = append(refusals, npuv1alpha1.DescriptorRefusal{
				Backend: "*",
				Reason: fmt.Sprintf("같은 vendor/product(%s)를 descriptor %q 가 이미 auto-apply 중 — "+
					"같은 하드웨어 이중 광고 방지로 거절", descriptor.ResourceNameFor(desc.Spec), winner),
			})
			applyOK = false
		}
	}
	// DRA 는 클러스터가 resource.k8s.io/v1 를 서빙할 때만 적용 가능하다.
	draServed := r.clusterServesDRA()
	if applyOK {
		// 이미지 미설정 backend 는 적용 대상에서 뺀다 — 빈 이미지 DaemonSet 은 InvalidImageName
		// 로 죽어 기능이 조용히 무력화된다. 사유는 backend 별 refusal 로 남긴다. 적용할 backend
		// 가 하나도 안 남으면 아무것도 만들지 않는다(깨진 DaemonSet 대신 설명 있는 거절).
		applicable := 0
		for _, b := range feasible {
			switch b {
			case npuv1alpha1.BackendDevicePlugin:
				if r.DPImage == "" {
					refusals = append(refusals, npuv1alpha1.DescriptorRefusal{
						Backend: npuv1alpha1.BackendDevicePlugin,
						Reason:  "devicePlugin auto-apply 이미지 미설정(--generated-dp-image)",
					})
				} else {
					applicable++
				}
			case npuv1alpha1.BackendDRA:
				switch {
				case r.DRAImage == "":
					refusals = append(refusals, npuv1alpha1.DescriptorRefusal{
						Backend: npuv1alpha1.BackendDRA,
						Reason:  "dra auto-apply 이미지 미설정(--generated-dra-image)",
					})
				case !draServed:
					refusals = append(refusals, npuv1alpha1.DescriptorRefusal{
						Backend: npuv1alpha1.BackendDRA,
						Reason:  "클러스터가 resource.k8s.io/v1(DeviceClass)를 서빙하지 않음 — DRA 미지원(k8s 1.34+ 필요)",
					})
				default:
					applicable++
				}
			}
		}
		if applicable == 0 {
			applyOK = false
		}
	}
	desc.Status.Refusals = refusals

	if err := r.Status().Update(ctx, &desc); err != nil {
		return ctrl.Result{}, fmt.Errorf("status 갱신 실패: %w", err)
	}

	if !applyOK {
		// 적용하지 않기로 했으면 이 descriptor 소유의 기존 생성물을 전부 회수한다.
		if err := r.pruneGeneratedBackends(ctx, &desc, nil); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("descriptor 판정 반영(미적용)",
			"name", desc.Name, "feasible", len(feasible), "refused", len(refusals))
		return ctrl.Result{}, nil
	}

	draImg := r.DRAImage
	if !draServed {
		draImg = "" // 미서빙이면 DRA 렌더를 건너뛴다(빈 이미지 skip 재사용) — DeviceClass NoMatch 백오프 방지
	}
	objs, err := RenderGeneratedBackends(&desc, r.DPImage, draImg, r.GeneratedNamespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("배포물 렌더 실패: %w", err)
	}
	if err := r.applyGeneratedBackends(ctx, &desc, objs); err != nil {
		return ctrl.Result{}, err
	}
	// 적용 뒤 desired 에 없는 자기 소유 생성물을 회수한다(backend 축소 시 남는 이전 종류).
	if err := r.pruneGeneratedBackends(ctx, &desc, objs); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("descriptor 판정·배포물 반영",
		"name", desc.Name, "feasible", len(feasible), "refused", len(refusals), "applied", len(objs))
	return ctrl.Result{}, nil
}

// verifyResolvedDevices 는 feasible 판정이 성공한 노드마다 Verify 를 불러 AcceleratorEvidence 를
// upsert 한다. Reconcile 밖으로 뺀 것은 gocyclo 상한(30) 때문이다(sharingRefusal 과 같은 사유).
// probe Pod 실행 여부는 Verify 내부가 정책으로 가른다 — 여기서는 Request 만 만든다. 에러는
// 반환하지 않는다 — 노드 하나의 Verify 실패(예: 등록 해제된 노드를 가리키는 낡은 NodeDeviceReport
// 로 Node 조회가 실패하는 경우, NotFound 를 관용하지 않는다)가 전체 재조정이나 나머지 노드의
// evidence 수집을 막으면 안 된다. acpp 의 같은 호출(gateReady)도 로그만 남기고 진행한다.
func (r *AcceleratorDescriptorReconciler) verifyResolvedDevices(
	ctx context.Context, desc *npuv1alpha1.AcceleratorDescriptor, feasible []string,
) {
	if r.Verification == nil || len(feasible) == 0 {
		return
	}
	resourceName := descriptor.ResourceNameFor(desc.Spec)
	for _, nd := range desc.Status.ResolvedDevices {
		// 관측 맵은 키(resolve 된 장치 ID)만 채우고 값(geometry)은 비운다 — 비우지 않으면
		// DeviceObservation 이 "관측 없음" 으로 실패해 evidence 가 매 재조정마다 등급을 못 받는다.
		// descriptor 는 per-PCI geometry 를 관측하지 않아 존재만 남기고 비교는 하지 않는다
		// (acceleratorpartitionpolicy_controller.go 의 rngd 분기와 같은 관용구). TargetDevices 도
		// 비운다 — stableID 의 "PCI-" 접두사가 NodeDeviceReport 의 PCI 키와 안 맞아 채우면 보고서
		// 대조가 실패한다. 대조는 제품명과 resolve 된 장치 수로 하고, device-plugin 검증은 안 쓴다.
		observed := make(map[string]string, len(nd.IDs))
		for _, id := range nd.IDs {
			observed[id] = ""
		}
		if _, err := r.Verification.Verify(ctx, verification.Request{
			NodeName:          nd.Node,
			Vendor:            desc.Spec.Vendor,
			SourcePolicy:      desc.Name,
			Generation:        desc.Generation,
			Expectation:       verification.Expectation{ProbeResource: resourceName},
			ObservedGeometry:  observed,
			Product:           desc.Spec.Product,
			ExpectedDevices:   len(nd.IDs),
			DescriptorManaged: true,
		}); err != nil {
			logf.FromContext(ctx).Error(err, "descriptor evidence 검증 실패", "descriptor", desc.Name, "node", nd.Node)
		}
	}
}

// sharingSupport 는 descriptor 의 sharing 게이트에 쓸 vendor/product 의 timeSlicing 선언이다.
// registry 가 그 (vendor, product) 키를 알면 그 값을, 모르면 제품을 가르지 않는 벤더
// (product="")의 값으로만 폴백한다. LookupVendor 로 폴백하지 않는 이유: 그 함수는 벤더의
// 아무 제품 값이나 물려주므로 furiosa/<모르는 제품> 이 warboy 나 rngd 의 판정을 상속한다 —
// 제품을 모르는 채로 다른 제품의 실측을 빌려 쓰면 안 된다. 둘 다 없으면 빈 State/Reason 대신
// Unknown + 사유를 합성해 돌려준다.
func sharingSupport(vendor, product string) vendorcap.Support {
	rv, rp := descriptorRegistryKey(vendor, product)
	return registryCapSupport(rv, rp, vendorcap.CapTimeSlicing)
}

// registryCapSupport 는 레지스트리 키(descriptorRegistryKey 로 이미 변환한 vendor/product)의
// 능력 선언이다. 키를 알면 그 값을, 모르면 제품을 가르지 않는 벤더(product="")의 값으로만
// 폴백한다. 제품을 아는 게이트가 공유하는 조회 경로다 — 장치 리셋 게이트도 이것을 쓴다.
func registryCapSupport(vendor, product string, c vendorcap.Capability) vendorcap.Support {
	if s, ok := vendorcap.Lookup(vendor, product, c); ok {
		return s
	}
	if s, ok := vendorcap.Lookup(vendor, "", c); ok {
		return s
	}
	return vendorcap.Support{State: vendorcap.StateUnknown, Reason: "레지스트리에 vendor/product 키 없음"}
}

// sharingRefusal 은 spec.sharing 이 선언됐는데 vendor/product 의 timeSlicing 이 Software 가
// 아니면 거절 사유를 돌려준다(선언이 없거나 Software 면 nil). Reconcile 인라인 대신 헬퍼로
// 뺀 것은 gocyclo 상한(30) 때문이다. Backend 는 "*" — 이 거절은 backend 하나가 아니라
// descriptor 전체의 auto-apply 를 막는다(vendor-managed·duplicate-winner 등 다른 전체
// 거절과 같은 관용구).
func sharingRefusal(spec npuv1alpha1.AcceleratorDescriptorSpec) *npuv1alpha1.DescriptorRefusal {
	if spec.Sharing == nil {
		return nil
	}
	s := sharingSupport(spec.Vendor, spec.Product)
	if s.State == vendorcap.StateSoftware {
		return nil
	}
	return &npuv1alpha1.DescriptorRefusal{
		Backend: "*",
		Reason: fmt.Sprintf("sharing 거절: vendor %s 의 timeSlicing 이 %s (%s) — "+
			"동시 접근 안전 실측 없이 SW 시분할을 켜지 않는다", spec.Vendor, s.State, s.Reason),
	}
}

// normalizeVendor 는 NDR vendor 어휘를 descriptor vendor 어휘로 맞춘다. NDR 은
// vendor="furiosa"/model="rngd" 로 내지만 descriptor 는 "rngd" 하나로 부른다(rngd 특례).
// 그 외 벤더는 어휘가 같아 그대로 통과한다.
func normalizeVendor(vendor, model string) string {
	if strings.EqualFold(vendor, "furiosa") && strings.EqualFold(model, vendorRngd) {
		return vendorRngd
	}
	return vendor
}

// descriptorRegistryKey 는 descriptor 어휘(vendor/product)를 internal/vendorcap 레지스트리
// 키로 맞춘다. normalizeVendor 와 반대 방향의 같은 "rngd 특례" 다 — descriptor 는 RNGD 를
// vendor="rngd" 하나로 부르지만(internal/descriptor/compile_test.go 의 rngdSpec 참고)
// registry·internal/intent 어휘는 furiosa 아래 product="rngd" 로 가른다
// (internal/intent/resources.go 의 productResource). furiosa 벤더 자체는 product 를
// 그대로 넘긴다.
func descriptorRegistryKey(vendor, product string) (string, string) {
	switch strings.ToLower(vendor) {
	case vendorRngd:
		return vendorFuriosa, vendorRngd
	case vendorFuriosa:
		return vendorFuriosa, product
	default:
		return vendor, product
	}
}

// matchesDescriptor 는 NDR 카드 하나가 descriptor 의 vendor/product 에 맞는지 본다.
func matchesDescriptor(d npuv1alpha1.DeviceEntry, spec npuv1alpha1.AcceleratorDescriptorSpec) bool {
	return strings.EqualFold(normalizeVendor(d.Vendor, d.Model), spec.Vendor) &&
		strings.EqualFold(d.Model, spec.Product)
}

// stableID 는 매칭된 카드의 노드 내 안정 식별자다. PCI 주소가 현재 유일한 실제 소스라
// Identity.Source(uuid·serial)와 무관하게 PCI 로 폴백한다(rngd/backend.go deviceID 관례).
func stableID(d npuv1alpha1.DeviceEntry) string {
	if d.PCIeAddress != "" {
		return "PCI-" + d.PCIeAddress
	}
	return d.Model
}

// resolveDevices 는 descriptor 의 vendor/product 에 맞는 NDR 카드들을 노드별로 모은다.
// 노드명·ID 모두 정렬해 출력을 결정적으로 만든다.
func resolveDevices(spec npuv1alpha1.AcceleratorDescriptorSpec, ndrs []npuv1alpha1.NodeDeviceReport) []npuv1alpha1.NodeDeviceIDs {
	byNode := map[string][]string{}
	for _, ndr := range ndrs {
		node := ndr.Spec.NodeName
		if node == "" {
			node = ndr.Name
		}
		for _, d := range ndr.Status.Devices {
			if matchesDescriptor(d, spec) {
				byNode[node] = append(byNode[node], stableID(d))
			}
		}
	}
	if len(byNode) == 0 {
		return nil
	}
	nodes := make([]string, 0, len(byNode))
	for node := range byNode {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)

	resolved := make([]npuv1alpha1.NodeDeviceIDs, 0, len(nodes))
	for _, node := range nodes {
		ids := byNode[node]
		sort.Strings(ids)
		resolved = append(resolved, npuv1alpha1.NodeDeviceIDs{Node: node, IDs: ids})
	}
	return resolved
}

// SetupWithManager 는 이 reconciler 를 매니저에 등록한다.
func (r *AcceleratorDescriptorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&npuv1alpha1.AcceleratorDescriptor{}).
		Watches(&npuv1alpha1.NodeDeviceReport{}, handler.EnqueueRequestsFromMapFunc(r.descriptorsForNDR)).
		// 벤더 토글(NPUClusterPolicy) 시 전 descriptor 를 재평가해 중재 결과를 갱신한다 —
		// 어느 descriptor 가 어느 벤더인지 policy 쪽에서 알 수 없어 전체를 재큐한다(NDR 관례 재사용).
		Watches(&npuv1alpha1.NPUClusterPolicy{}, handler.EnqueueRequestsFromMapFunc(r.descriptorsForNDR)).
		// 같은 vendor/product 중복 판정은 형제 descriptor 에 의존한다 — 생성·삭제·스펙 변경 시
		// 전 descriptor 를 재평가해 winner 를 재선출한다(status 변경 루프는 Generation 술어로 배제).
		Watches(&npuv1alpha1.AcceleratorDescriptor{}, handler.EnqueueRequestsFromMapFunc(r.descriptorsForNDR),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("acceleratordescriptor").
		Complete(r)
}

// duplicateWinnerName 은 같은 vendor/product(리소스명)를 선언한 descriptor 들 중 이름 사전순
// 먼저인 것을 돌려준다. 그 이름이 이 descriptor 가 아니면 이 descriptor 는 loser 다 — 같은
// 하드웨어를 두 backend 가 이중 광고하는 것을 막기 위해 하나만 적용한다. 결정적이라 flapping 이 없다.
func (r *AcceleratorDescriptorReconciler) duplicateWinnerName(ctx context.Context, desc *npuv1alpha1.AcceleratorDescriptor) (string, error) {
	var all npuv1alpha1.AcceleratorDescriptorList
	if err := r.List(ctx, &all); err != nil {
		return "", fmt.Errorf("AcceleratorDescriptor 목록 조회 실패: %w", err)
	}
	key := descriptor.ResourceNameFor(desc.Spec)
	winner := desc.Name
	for i := range all.Items {
		o := &all.Items[i]
		if o.Name != desc.Name && descriptor.ResourceNameFor(o.Spec) == key && o.Name < winner {
			winner = o.Name
		}
	}
	return winner, nil
}

// descriptorsForNDR 는 NDR 변경 시 모든 AcceleratorDescriptor 를 재큐한다. 카드가 어느
// descriptor 에 맞는지는 NDR 쪽에서 알 수 없으므로 전체를 다시 본다(npuclusterpolicy_controller.go
// mapNodeToClusterPolicies 와 같은 관례).
func (r *AcceleratorDescriptorReconciler) descriptorsForNDR(ctx context.Context, _ client.Object) []reconcile.Request {
	var list npuv1alpha1.AcceleratorDescriptorList
	if err := r.List(ctx, &list); err != nil {
		logf.FromContext(ctx).Error(err, "NDR 이벤트 매핑 중 AcceleratorDescriptor 목록 조회 실패")
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
	}
	return reqs
}
