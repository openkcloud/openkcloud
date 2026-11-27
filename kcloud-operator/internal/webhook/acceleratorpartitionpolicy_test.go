// ============================================================
// acceleratorpartitionpolicy_test.go: ACPPValidator 시험
// 상세: 정상/형식오류/노드부재/빈요청 거부, fail-open(제품 미확인 통과), vendorcap 기반
// partition/sharing 미지원·Unknown 거절과 검사 순서, 삭제 중 재검증 생략, 되돌리기·sharing 자체 거절을 고정한다.
// 생성일: 2026-08-24 | 수정일: 2026-10-11
// ============================================================
package webhook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

const hostnameLabel = "kubernetes.io/hostname"

func acppNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{hostnameLabel: name}}}
}

func acppNDR(nodeName, model string) *npuv1alpha1.NodeDeviceReport {
	return &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: nodeName},
		Status:     npuv1alpha1.NodeDeviceReportStatus{Devices: []npuv1alpha1.DeviceEntry{{Vendor: "nvidia", Model: model}}},
	}
}

func specMIG(node, profile string) npuv1alpha1.AcceleratorPartitionPolicySpec {
	return npuv1alpha1.AcceleratorPartitionPolicySpec{
		NodeSelector: map[string]string{hostnameLabel: node},
		Vendor:       "nvidia",
		Layout:       []npuv1alpha1.PartitionLayout{{Profile: profile, CountPerDevice: 4}},
	}
}

func specEmpty(node string) npuv1alpha1.AcceleratorPartitionPolicySpec {
	return npuv1alpha1.AcceleratorPartitionPolicySpec{
		NodeSelector: map[string]string{hostnameLabel: node},
		Vendor:       "nvidia",
	}
}

func specSharing(node string) npuv1alpha1.AcceleratorPartitionPolicySpec {
	return npuv1alpha1.AcceleratorPartitionPolicySpec{
		NodeSelector: map[string]string{hostnameLabel: node},
		Vendor:       "nvidia",
		Sharing:      &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
	}
}

// TestACPPValidator: worker1 에 a30, master 에(제품 미확인 표시용) geforce-gtx-970 이 있는
// NodeDeviceReport 를 fake client 에 넣어 정상/거부/fail-open 경로를 함께 확인한다. 거부 메시지는
// 저장소의 다른 validator 들과 같이 영어로 통일돼 있으므로 wantMsg 도 "node" 로 맞춘다.
func TestACPPValidator(t *testing.T) {
	cases := []struct {
		name    string
		spec    npuv1alpha1.AcceleratorPartitionPolicySpec
		wantErr bool
		wantMsg string
	}{
		{"정상 MIG", specMIG("k8s-worker1", "1g.6gb"), false, ""},
		{"a30 에 없는 프로파일", specMIG("k8s-worker1", "1g.5gb"), true, "a30"},
		{"형식 오류", specMIG("k8s-worker1", "1g5gb"), true, "profile"},
		{"레이아웃도 sharing 도 없음", specEmpty("k8s-worker1"), true, "layout"},
		{"nodeSelector 가 아무 노드도 안 고름", specMIG("k8s-worker9", "1g.6gb"), true, "node"},
		{"모르는 제품은 통과", specMIG("k8s-master", "1g.5gb"), false, ""},
		{"sharing 만 있는 정책은 통과", specSharing("k8s-worker1"), false, ""},
	}

	c := build(t,
		acppNode("k8s-worker1"), acppNDR("k8s-worker1", "a30"),
		acppNode("k8s-master"), acppNDR("k8s-master", "geforce-gtx-970"),
	)

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("p%d", i)},
				Spec:       tc.spec,
			}
			err := validateACPP(context.Background(), c, acpp)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if err != nil && tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

func TestACPPValidatorWrongType(t *testing.T) {
	c := build(t)
	if err := validateACPP(context.Background(), c, &npuv1alpha1.NPUClusterPolicy{}); err == nil {
		t.Fatal("wrong type accepted")
	}
}

// 입구가 클러스터 상태에 의존해 정상 요청을 막으면 안 된다 — NodeList 조회 자체가 실패해도
// (API 서버 일시 오류 등) 판정을 보류하고 통과시켜야 한다. 이 시험이 없으면 acceleratorpartitionpolicy.go:138 의
// `return nil` 을 `return err` 로 뒤집어도 아무것도 깨지지 않는다.
func TestACPPValidatorNodeListFailurePasses(t *testing.T) {
	injected := errors.New("injected list failure")
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(acppNode("k8s-worker1"), acppNDR("k8s-worker1", "a30")).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*corev1.NodeList); ok {
					return injected
				}
				return cl.List(ctx, list, opts...)
			},
		}).Build()
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "list-fail"},
		Spec:       specMIG("k8s-worker1", "1g.6gb"),
	}
	if err := validateACPP(context.Background(), c, acpp); err != nil {
		t.Fatalf("NodeList 조회 실패가 정상 요청을 거절했다: %v", err)
	}
}

// 같은 이유로 NodeDeviceReport 조회 실패도 그 노드에 대한 판정만 건너뛰어야 한다 — acceleratorpartitionpolicy.go:151 의
// `continue` 를 거절로 뒤집어도 이 시험이 없으면 잡히지 않는다.
func TestACPPValidatorNDRGetFailurePasses(t *testing.T) {
	injected := errors.New("injected get failure")
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(acppNode("k8s-worker1"), acppNDR("k8s-worker1", "a30")).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*npuv1alpha1.NodeDeviceReport); ok {
					return injected
				}
				return cl.Get(ctx, key, obj, opts...)
			},
		}).Build()
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "get-fail"},
		Spec:       specMIG("k8s-worker1", "1g.6gb"),
	}
	if err := validateACPP(context.Background(), c, acpp); err != nil {
		t.Fatalf("NodeDeviceReport 조회 실패가 정상 요청을 거절했다: %v", err)
	}
}

// vendorcap 레지스트리가 그 벤더의 partition 을 Native/Software 로 선언하지 않으면
// ValidateLayoutShape 의 일반 형식 오류보다 먼저, 사유·근거 문서와 함께 거절해야 한다. layout 을
// 2개 엔트리로 준다 — ValidateLayoutShape 는 비어 있지 않은 layout 이 2개 이상이면 벤더와 무관하게
// "mixed placement unsupported" 로 거절한다(profiles.go:37-39). 검사 순서가 뒤집혀 레지스트리
// 검사가 ValidateLayoutShape 뒤로 밀리면 이 테스트는 그 일반 오류를 받아 실패한다 — 이 픽스처가
// 순서 자체를 고정한다(1개 엔트리면 두 검사 모두 통과해 순서가 뒤집혀도 우연히 통과할 수 있다).
func TestValidateACPPRejectsUnsupportedPartitionVendorWithReason(t *testing.T) {
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
		Vendor: "rebellions",
		Layout: []npuv1alpha1.PartitionLayout{{Profile: "x", CountPerDevice: 1}, {Profile: "y", CountPerDevice: 1}},
	}}
	err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp)
	if err == nil || !strings.Contains(err.Error(), "vendor-capability-not-found") {
		t.Fatalf("rebellions 파티션은 레지스트리 사유로 거절돼야 한다: %v", err)
	}
	if !strings.Contains(err.Error(), "rblnlib-go v0.3.0") {
		t.Fatalf("무엇을 조사했는지가 사유에 있어야 한다: %v", err)
	}
}

// 순서 고정 픽스처는 위 테스트와 같은 이유로 2개 엔트리를 쓴다. tenstorrent 의 partition 은 조사
// 결과 Unsupported 로 선언돼 있고, 사유가 근거 문서를 인용한다.
func TestValidateACPPRejectsUnsupportedPartitionVendorTenstorrent(t *testing.T) {
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
		Vendor: "tenstorrent",
		Layout: []npuv1alpha1.PartitionLayout{{Profile: "x", CountPerDevice: 1}, {Profile: "y", CountPerDevice: 1}},
	}}
	err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp)
	if err == nil || !strings.Contains(err.Error(), "Unsupported") {
		t.Fatalf("tenstorrent 파티션은 장치 분할 API 없음(Unsupported)으로 거절돼야 한다: %v", err)
	}
	// 미지원 사유가 무엇을 조사했는지 적어야 한다 — 추적 가능한 사유 있는 거절임을 못박는다.
	if !strings.Contains(err.Error(), "tt-kmd 2.11.0 소스") {
		t.Fatalf("무엇을 조사했는지가 사유에 있어야 한다: %v", err)
	}
	// Source 가 있으면 " (source)" 접미사가 붙는다 — 빈 괄호 " ()" 는 남으면 안 된다
	// (sourceSuffix 회귀 가드).
	if strings.Contains(err.Error(), "()") {
		t.Fatalf("거절 메시지에 빈 괄호를 남기면 안 된다: %v", err)
	}
}

// rebellions 의 sharing-only 요청은 layout 유무로 게이트를 건너뛰지 않고 사유와 함께 거절돼야
// 한다 — 건너뛰면 컨트롤러의 backendFor 까지 흘러가 "unknown vendor" 같은 일반 런타임 오류로만
// 실패한다. rebellions 는 partition backend 가 없어(nvidia·furiosa 만 있음) ACPP 경로 자체를
// 못 쓴다 — 그래서 이 요청은 timeSlicing 능력 검사에 닿기 전에 requirePartitionBackendForSharing
// (partition backend 부재)에 걸려 AcceleratorDescriptor 경로로 안내된다. 이 시험이 지키려는
// 성질은 "사유 있는 거절, 일반 오류 아님" 이고 메시지는 그 사유(sharing + AcceleratorDescriptor 안내)다.
func TestValidateACPPRejectsUnsupportedSharingVendorWithReason(t *testing.T) {
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
		Vendor:  "rebellions",
		Sharing: &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
	}}
	err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp)
	if err == nil || !strings.Contains(err.Error(), "sharing") {
		t.Fatalf("rebellions sharing-only 요청은 사유와 함께 거절돼야 한다: %v", err)
	}
	if !strings.Contains(err.Error(), "AcceleratorDescriptor") {
		t.Fatalf("partition-backend-부재 사유(descriptor 경로 안내)가 있어야 한다: %v", err)
	}
}

// nvidia timeSlicing 은 레지스트리상 Software 라 새 sharing 게이트가 거절하면 안 된다. 이
// fake client 는 노드가 없어 nodeSelector 매칭 실패 등 다른 사유로는 에러가 날 수 있으나(기존
// TestACPPValidator 케이스들과 같은 패턴), 그 에러가 sharing 게이트발이 아님을 못박는다.
func TestValidateACPPAllowsSupportedSharingVendor(t *testing.T) {
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
		Vendor:  "nvidia",
		Sharing: &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
	}}
	err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp)
	if err != nil && strings.Contains(err.Error(), "sharing timeSliced Unsupported") {
		t.Fatalf("nvidia timeSlicing 은 Software 로 선언돼 있어 sharing 게이트가 거절하면 안 된다: %v", err)
	}
}

// 삭제 중인 오브젝트는 재검증하지 않는다. 컨트롤러가 finalizer 를 지우려고 부르는 Update 도
// UPDATE 로 들어오므로, 그때 레지스트리 재분류나 대상 노드 소멸로 거절되면 ACPP 가
// Terminating 에서 못 빠져나온다. 같은 스펙이 삭제 중이 아니면 거절되는 것까지 함께 확인해
// 통과가 게이트 덕분임을 못박는다(그 대조가 없으면 검증을 통째로 지워도 초록이다).
func TestACPPValidateUpdateSkipsObjectBeingDeleted(t *testing.T) {
	v := &ACPPValidator{Client: build(t)}
	deleting := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting",
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
			Finalizers:        []string{"npu.ai/acpp"},
		},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "rebellions",
			Layout:  []npuv1alpha1.PartitionLayout{{Profile: "x", CountPerDevice: 1}},
			Sharing: &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
		},
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, deleting); err != nil {
		t.Fatalf("삭제 중 ACPP 는 통과해야 한다: %v", err)
	}

	live := deleting.DeepCopy()
	live.DeletionTimestamp = nil
	live.Finalizers = nil
	if _, err := v.ValidateUpdate(context.Background(), nil, live); err == nil {
		t.Fatal("삭제 중이 아닌 같은 스펙은 거절돼야 한다 — 위 통과가 삭제 게이트 덕분임을 보장할 수 없다")
	}
}

// layout 없는 sharing.mode=exclusive 는 "되돌리기" 요청이다 — 되돌릴 능력이 하나도 없는 벤더에서만
// 거절해야 한다. furiosa/warboy 는 partition(단일 코어)·timeSlicing(배타적 open, EBUSY)·
// multiProcess(벤더 broker 없음) 셋 다 Native/Software 가 아니라 내릴 것이 없다. rebellions 는
// timeSlicing 이 Software 라 되돌릴 공유 능력이 있으므로 이 예시에 들지 않는다 — rebellions 의
// exclusive 되돌리기는 partition backend 부재로 TestValidateACPPRejectsSharingForNonPartitionBackendVendor
// 가 대신 거절한다. warboy 는 제품이 갈리므로(rngd 는 Native) NDR 로 제품을 특정해야 이 경로가 성립한다.
func TestValidateACPPRejectsExclusiveRevertForVendorWithNothingToRevert(t *testing.T) {
	const node = "k8s-furiosa-warboy-revert"
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "revert-warboy"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			NodeSelector: map[string]string{hostnameLabel: node},
			Vendor:       "furiosa",
			Sharing:      &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeExclusive},
		},
	}
	c := build(t, acppNode(node), acppFuriosaNDR(node, "warboy"))
	err := validateACPP(context.Background(), c, acpp)
	if err == nil || !strings.Contains(err.Error(), "no partition or sharing capability to revert") {
		t.Fatalf("되돌릴 능력이 없는 벤더(furiosa/warboy)의 exclusive 요청은 거절돼야 한다: %v", err)
	}
}

// 반대쪽: 콘솔의 첫 선택지(intent=exclusive)가 만드는 모양은 통과해야 한다.
// pkg/npuctl.BuildPartitionPolicy 가 layout 없이 sharing.mode=exclusive 만 실은 ACPP 를
// 만들고, POST /api/v1/policies 와 그 dryRun 게이트가 이 검증을 지난다. 여기서 막으면
// 노드를 독점으로 되돌리는 경로가 화면에서 통째로 사라진다.
//
// furiosa 도 함께 본다 — partition 이 rngd 를 통해 Native 라 되돌릴 것이 있다.
func TestValidateACPPAllowsExclusiveRevertForCapableVendor(t *testing.T) {
	for _, vendor := range []string{"nvidia", "furiosa"} {
		t.Run(vendor, func(t *testing.T) {
			acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "revert-" + vendor},
				Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
					NodeSelector: map[string]string{hostnameLabel: "k8s-worker1"},
					Vendor:       vendor,
					Sharing:      &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeExclusive},
				},
			}
			c := build(t, acppNode("k8s-worker1"), acppNDR("k8s-worker1", "a30"))
			if err := validateACPP(context.Background(), c, acpp); err != nil {
				t.Fatalf("되돌리기 요청은 통과해야 한다: %v", err)
			}
		})
	}
}

// acppNDR 은 device.Vendor 를 "nvidia" 로 고정한다 — furiosa product-aware 시험은 그
// 대신 furiosa 장치를 실은 NDR 이 필요하다.
func acppFuriosaNDR(nodeName, model string) *npuv1alpha1.NodeDeviceReport {
	return &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: nodeName},
		Status:     npuv1alpha1.NodeDeviceReportStatus{Devices: []npuv1alpha1.DeviceEntry{{Vendor: "furiosa", Model: model}}},
	}
}

// furiosa 는 벤더 아래 제품이 둘이라(rngd/warboy) registry 의 partition 능력이 갈린다 —
// rngd 는 PE-fusion 으로 Native, warboy 는 단일 코어라 Unsupported. product-aware
// 게이트가 nodeSelector 가 고르는 노드의 NDR 로 실제 제품을 알아내지 못하면 LookupVendor 가
// rngd 값(Native)으로 폴백해 warboy 노드도 통과해버린다 — 그 회귀를 이 시험이 잡는다.
func TestValidateACPPProductAwareFuriosaPartition(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		wantErr bool
		wantMsg string
	}{
		{"warboy 는 단일 코어라 거절", "warboy", true, "단일 코어"},
		{"rngd 는 PE-fusion 이라 통과", "rngd", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := "k8s-furiosa-" + tc.model
			c := build(t, acppNode(node), acppFuriosaNDR(node, tc.model))
			acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "acpp-" + tc.model},
				Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
					NodeSelector: map[string]string{hostnameLabel: node},
					Vendor:       "furiosa",
					Layout:       []npuv1alpha1.PartitionLayout{{Profile: "x", CountPerDevice: 1}},
				},
			}
			err := validateACPP(context.Background(), c, acpp)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if err != nil && tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// nodeSelector 가 rngd·warboy 두 노드를 동시에 고르면(또는 selector 가 비어 둘 다 대상이면)
// 보수적으로 판정돼야 한다 — rngd 하나만 봤다면 Native 로 통과시켰을 요청을 warboy 가 섞여
// 있다는 이유로 거절해야 하고, 반대로 rngd 노드만 고르면 그대로 통과해야 한다.
func TestValidateACPPProductAwareFuriosaPartitionMixedNodes(t *testing.T) {
	const rngdNode, warboyNode = "k8s-furiosa-mix-rngd", "k8s-furiosa-mix-warboy"
	c := build(t,
		acppNode(rngdNode), acppFuriosaNDR(rngdNode, "rngd"),
		acppNode(warboyNode), acppFuriosaNDR(warboyNode, "warboy"),
	)
	layout := []npuv1alpha1.PartitionLayout{{Profile: "x", CountPerDevice: 1}}

	t.Run("빈 selector 는 전체 노드를 보고 warboy 로 보수적 거절", func(t *testing.T) {
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "acpp-mix-empty"},
			Spec:       npuv1alpha1.AcceleratorPartitionPolicySpec{Vendor: "furiosa", Layout: layout},
		}
		err := validateACPP(context.Background(), c, acpp)
		if err == nil || !strings.Contains(err.Error(), "단일 코어") {
			t.Fatalf("혼재 노드는 warboy 로 보수적 거절돼야 한다: %v", err)
		}
	})

	t.Run("rngd 노드만 고르면 통과", func(t *testing.T) {
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "acpp-mix-rngd-only"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector: map[string]string{hostnameLabel: rngdNode},
				Vendor:       "furiosa",
				Layout:       layout,
			},
		}
		if err := validateACPP(context.Background(), c, acpp); err != nil {
			t.Fatalf("rngd 만 고르면 통과해야 한다: %v", err)
		}
	})
}

// ACPP 는 partition backend 가 있는 벤더(nvidia·furiosa)만 쓸 수 있다 — partition 을
// Native/Software 로 선언하지 않은 벤더는 sharing 요청(exclusive 되돌리기 포함) 자체를 거절해야
// 한다. 이 게이트가 없으면 timeSlicing 은 Software 인데 partition 은 Unsupported 인 벤더의 요청이
// sharing 능력 검사는 통과하고 partition backend 부재로 backendFor 에서 "unknown vendor" 로만
// 죽는다 — 대신 AcceleratorDescriptor spec.sharing(generic device-plugin replicas) 경로로 보낸다.
// 픽스처가 tenstorrent 인 이유는 partition Unsupported 벤더는 timeSlicing 값과 무관하게 여기서 걸리기 때문이다.
func TestValidateACPPRejectsSharingForNonPartitionBackendVendor(t *testing.T) {
	t.Run("tenstorrent timeSliced 는 partition backend 가 없어 거절", func(t *testing.T) {
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "tenstorrent",
			Sharing: &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
		}}
		err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp)
		if err == nil || !strings.Contains(err.Error(), "AcceleratorDescriptor") {
			t.Fatalf("tenstorrent sharing timeSliced 는 ACPP 경로에서 거절되고 descriptor 경로를 안내해야 한다: %v", err)
		}
	})

	// tenstorrent 는 timeSlicing 이 Unsupported 라(vendorcap 레지스트리 참고) partition·timeSlicing·
	// multiProcess 가 모두 미지원이라, exclusive 되돌리기는 위 "되돌릴 능력이 없다" 규칙
	// (TestValidateACPPRejectsExclusiveRevertForVendorWithNothingToRevert)에 먼저 걸린다.
	// timeSliced 는 그 규칙을 지나 partition backend 부재로 거절된다.
	t.Run("tenstorrent exclusive 되돌리기는 되돌릴 능력이 없어 거절", func(t *testing.T) {
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "tenstorrent",
			Sharing: &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeExclusive},
		}}
		err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp)
		if err == nil || !strings.Contains(err.Error(), "no partition or sharing capability to revert") {
			t.Fatalf("tenstorrent 는 partition·timeSlicing·multiProcess 가 모두 미지원이라 exclusive 되돌리기에 되돌릴 능력이 없어 거절돼야 한다: %v", err)
		}
	})

	t.Run("nvidia timeSliced 는 partition Native 라 이 규칙에 안 걸린다", func(t *testing.T) {
		const node = "k8s-sharing-backend-nvidia"
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "sharing-backend-nvidia-ts"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector: map[string]string{hostnameLabel: node},
				Vendor:       "nvidia",
				Sharing:      &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
			},
		}
		c := build(t, acppNode(node), acppNDR(node, "a30"))
		if err := validateACPP(context.Background(), c, acpp); err != nil {
			t.Fatalf("nvidia 는 partition backend 가 있어 sharing timeSliced 가 통과해야 한다: %v", err)
		}
	})

	t.Run("furiosa rngd 노드의 exclusive-only 는 partition Native 라 이 규칙에 안 걸린다", func(t *testing.T) {
		const node = "k8s-sharing-backend-furiosa-rngd"
		acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "sharing-backend-furiosa-excl"},
			Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
				NodeSelector: map[string]string{hostnameLabel: node},
				Vendor:       "furiosa",
				Sharing:      &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeExclusive},
			},
		}
		c := build(t, acppNode(node), acppFuriosaNDR(node, "rngd"))
		if err := validateACPP(context.Background(), c, acpp); err != nil {
			t.Fatalf("furiosa/rngd 는 partition Native 라 exclusive 되돌리기가 통과해야 한다: %v", err)
		}
	})
}

// 게이트(requirePartitionBackendForSharing)는 partition backend 가 있는 벤더를 통과시킬 뿐
// 그 자체가 sharing 을 허용하는 건 아니다 — furiosa/rngd 는 partition Native 라 이 게이트는
// 통과하지만, timeSlicing 은 레지스트리상 Unsupported(배타적 open, EBUSY) 라 그 아래 기존
// sharing 능력 검사가 레지스트리 Reason 과 함께 거절해야 한다. partition backend 는 있는데 그
// 능력 자체가 Unsupported 인 벤더가 이 경로(게이트 통과 후 사유 있는 거절)를 타는지는 다른
// 시험이 다루지 않으므로 여기서 고정한다.
func TestValidateACPPRejectsFuriosaRngdTimeSlicedWithRegistryReason(t *testing.T) {
	const node = "k8s-furiosa-rngd-timesliced"
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-rngd-timesliced"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			NodeSelector: map[string]string{hostnameLabel: node},
			Vendor:       "furiosa",
			Sharing:      &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
		},
	}
	c := build(t, acppNode(node), acppFuriosaNDR(node, "rngd"))
	err := validateACPP(context.Background(), c, acpp)
	if err == nil || !strings.Contains(err.Error(), "sharing timeSliced") || !strings.Contains(err.Error(), "EBUSY") {
		t.Fatalf("furiosa/rngd timeSliced 는 레지스트리 EBUSY 사유로 거절돼야 한다: %v", err)
	}
	if strings.Contains(err.Error(), "AcceleratorDescriptor") {
		t.Fatalf("partition backend 가 있는 벤더라 partition-backend 게이트가 아니라 기존 sharing 능력 검사가 거절해야 한다: %v", err)
	}
}

// furiosa(rngd)의 삭제 경로는 정책값과 무관하게 Retain 으로 동작한다(handleDeletion 은 DS env 를
// 그대로 둔다). 지킬 수 없는 RestoreMode 는 입구에서 거절하고, Retain 은 이 사유로 막지 않는다.
func TestValidateACPPRejectsRestoreModeForNonNvidia(t *testing.T) {
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
		Vendor:         "furiosa",
		Layout:         []npuv1alpha1.PartitionLayout{{Profile: "2core.12gb", CountPerDevice: 4}},
		DeletionPolicy: npuv1alpha1.DeletionPolicyRestoreMode,
	}}
	err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp)
	if err == nil || !strings.Contains(err.Error(), "RestoreMode") || !strings.Contains(err.Error(), `supported only for spec.vendor "nvidia"`) {
		t.Fatalf("furiosa 의 RestoreMode 는 벤더 사유로 거절돼야 한다: %v", err)
	}
	acpp.Spec.DeletionPolicy = npuv1alpha1.DeletionPolicyRetain
	if err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), acpp); err != nil && strings.Contains(err.Error(), "RestoreMode") {
		t.Fatalf("Retain 은 deletionPolicy 사유로 거절되면 안 된다: %v", err)
	}
	nv := &npuv1alpha1.AcceleratorPartitionPolicy{Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
		Vendor:         "nvidia",
		Layout:         []npuv1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 4}},
		DeletionPolicy: npuv1alpha1.DeletionPolicyRestoreMode,
	}}
	if err := validateACPP(context.Background(), fake.NewClientBuilder().Build(), nv); err != nil && strings.Contains(err.Error(), "RestoreMode") {
		t.Fatalf("nvidia 의 RestoreMode 는 이 사유로 거절되면 안 된다: %v", err)
	}
}
