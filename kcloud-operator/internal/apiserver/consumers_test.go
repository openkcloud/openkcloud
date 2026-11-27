// ============================================================
// consumers_test.go: 소비자 귀속 판정 시험
// 상세: 픽스처를 실제 corev1/resourcev1 타입으로 만든다 — 프로덕션이 만들 수 없는
// 조합으로 시험하면 그 시험은 무효다.
// 생성일: 2026-08-07 | 수정일: 2026-10-11
// ============================================================
package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
)

// docDRAClaimName 은 템플릿(resourceClaimTemplateName) claim 시험에서 공유하는 템플릿 이름이다.
const docDRAClaimName = "doc-dra-claim"

// fakeReader 는 List 만 채운 client.Reader 다. 핸들러가 Pod·ResourceClaim·
// NodeDeviceReport 세 목록을 읽으므로 셋 다 돌려준다.
type fakeReader struct {
	pods      *corev1.PodList
	claims    *resourcev1.ResourceClaimList
	claimsErr error
	ndrs      *v1alpha1.NodeDeviceReportList
}

func (f fakeReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return fmt.Errorf("미구현")
}

func (f fakeReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	switch v := list.(type) {
	case *corev1.PodList:
		*v = *f.pods
	case *resourcev1.ResourceClaimList:
		if f.claimsErr != nil {
			return f.claimsErr
		}
		*v = *f.claims
	case *v1alpha1.NodeDeviceReportList:
		*v = *f.ndrs
	default:
		return fmt.Errorf("예상 못한 목록 타입 %T", list)
	}
	return nil
}

func podWithLimits(ns, name, node string, limits map[string]string) corev1.Pod {
	rl := corev1.ResourceList{}
	for k, v := range limits {
		rl[corev1.ResourceName(k)] = resource.MustParse(v)
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: corev1.PodSpec{
			NodeName:   node,
			Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Limits: rl}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestDevicePluginPodIsNodeAttributed(t *testing.T) {
	pod := podWithLimits("infer", "gpu-0", "k8s-worker1", map[string]string{"nvidia.com/gpu": "2"})

	got, ok := toConsumerView(pod, nil, nil)
	if !ok {
		t.Fatal("가속기 요청 Pod 가 걸러졌다")
	}
	if got.AllocationAPI != "devicePlugin" {
		t.Errorf("AllocationAPI = %q, 기대 devicePlugin", got.AllocationAPI)
	}
	if got.Attribution != AttributionNode {
		t.Errorf("Attribution = %q, 기대 node", got.Attribution)
	}
	if len(got.DeviceUIDs) != 0 {
		t.Errorf("DeviceUIDs = %v, 기대 빈 값 — 어느 카드인지 API 로 알 수 없다", got.DeviceUIDs)
	}
	if len(got.Requests) != 1 || got.Requests[0].Quantity != 2 || got.Requests[0].Vendor != "nvidia" {
		t.Errorf("Requests = %+v", got.Requests)
	}
}

func TestNonAcceleratorPodIsFilteredOut(t *testing.T) {
	pod := podWithLimits("default", "nginx", "k8s-worker1", map[string]string{"cpu": "1"})
	if _, ok := toConsumerView(pod, nil, nil); ok {
		t.Error("가속기를 요청하지 않은 Pod 가 목록에 들어갔다")
	}
}

func TestAllocatedDRAClaimIsDeviceAttributed(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "infer", Name: "vllm-0"},
		Spec: corev1.PodSpec{
			NodeName:       "rngd-1",
			ResourceClaims: []corev1.PodResourceClaim{{Name: "npu", ResourceClaimName: ptr.To("vllm-0-npu")}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	claim := resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "infer", Name: "vllm-0-npu"},
		Spec: resourcev1.ResourceClaimSpec{
			Devices: resourcev1.DeviceClaim{
				Requests: []resourcev1.DeviceRequest{{Name: "r0",
					Exactly: &resourcev1.ExactDeviceRequest{DeviceClassName: "rngd.furiosa.ai"}}},
			},
		},
		Status: resourcev1.ResourceClaimStatus{
			Allocation: &resourcev1.AllocationResult{
				Devices: resourcev1.DeviceAllocationResult{
					Results: []resourcev1.DeviceRequestAllocationResult{{Device: "npu0", Driver: "rngd.furiosa.ai", Pool: "rngd-1"}},
				},
			},
		},
	}

	got, ok := toConsumerView(pod, []resourcev1.ResourceClaim{claim}, nil)
	if !ok {
		t.Fatal("DRA Pod 가 걸러졌다")
	}
	if got.AllocationAPI != "dra" {
		t.Errorf("AllocationAPI = %q, 기대 dra", got.AllocationAPI)
	}
	if got.Attribution != "device" {
		t.Errorf("Attribution = %q, 기대 device", got.Attribution)
	}
	if len(got.DeviceUIDs) != 1 || got.DeviceUIDs[0] != "npu0" {
		t.Errorf("DeviceUIDs = %v, 기대 [npu0]", got.DeviceUIDs)
	}
}

// 미할당 claim 은 node 가 아니라 pending 이다. 둘의 뜻이 다르다 —
// node 는 영영 알 수 없고 pending 은 곧 알게 된다.
func TestUnallocatedDRAClaimIsPending(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "infer", Name: "vllm-1"},
		Spec: corev1.PodSpec{
			ResourceClaims: []corev1.PodResourceClaim{{Name: "npu", ResourceClaimName: ptr.To("vllm-1-npu")}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	claim := resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "infer", Name: "vllm-1-npu"},
	}

	got, ok := toConsumerView(pod, []resourcev1.ResourceClaim{claim}, nil)
	if !ok {
		t.Fatal("미할당 DRA Pod 가 걸러졌다")
	}
	if got.Attribution != "pending" {
		t.Errorf("Attribution = %q, 기대 pending", got.Attribution)
	}
	if len(got.DeviceUIDs) != 0 {
		t.Errorf("DeviceUIDs = %v, 기대 빈 값", got.DeviceUIDs)
	}
}

func consumerFixture() *corev1.PodList {
	return &corev1.PodList{Items: []corev1.Pod{
		podWithLimits("infer", "a", "k8s-worker1", map[string]string{"nvidia.com/gpu": "1"}),
		podWithLimits("infer", "b", "rngd-1", map[string]string{"furiosa.ai/rngd": "2"}),
		podWithLimits("default", "nginx", "k8s-worker1", map[string]string{"cpu": "1"}),
	}}
}

func getConsumers(t *testing.T, s *Server, query string) []ConsumerView {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleConsumers(rec, httptest.NewRequest("GET", "/api/v1/consumers"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []ConsumerView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("응답 파싱 실패: %v — body=%s", err, rec.Body.String())
	}
	return page.Items
}

func TestHandleConsumersFiltersByNode(t *testing.T) {
	s := &Server{
		Reader: fakeReader{pods: consumerFixture(), claims: &resourcev1.ResourceClaimList{}, ndrs: &v1alpha1.NodeDeviceReportList{}},
		Log:    logr.Discard(),
	}

	items := getConsumers(t, s, "?node=rngd-1")
	if len(items) != 1 || items[0].Name != "b" {
		t.Fatalf("items = %+v, 기대 rngd-1 의 b 하나", items)
	}
}

// 비가속기 Pod 는 필터 없이도 목록에 들어가지 않는다.
func TestHandleConsumersExcludesNonAccelerator(t *testing.T) {
	s := &Server{
		Reader: fakeReader{pods: consumerFixture(), claims: &resourcev1.ResourceClaimList{}, ndrs: &v1alpha1.NodeDeviceReportList{}},
		Log:    logr.Discard(),
	}

	items := getConsumers(t, s, "")
	if len(items) != 2 {
		t.Fatalf("items = %+v, 기대 2건(nginx 제외)", items)
	}
	for _, v := range items {
		if v.Name == "nginx" {
			t.Error("비가속기 Pod 가 목록에 들어갔다")
		}
	}
}

// 1.28 라인처럼 resourceclaims CRD 가 없으면 조회가 실패한다. 그때 목록 전체를
// 죽이지 말고 device-plugin 축만 내야 한다.
func TestHandleConsumersSurvivesMissingResourceClaims(t *testing.T) {
	s := &Server{
		Reader: fakeReader{pods: consumerFixture(), claimsErr: fmt.Errorf("no matches for kind ResourceClaim"), ndrs: &v1alpha1.NodeDeviceReportList{}},
		Log:    logr.Discard(),
	}

	items := getConsumers(t, s, "?vendor=furiosa")
	if len(items) != 1 || items[0].Name != "b" {
		t.Fatalf("items = %+v, 기대 furiosa 를 쓰는 b 하나", items)
	}
	if items[0].Attribution != AttributionNode {
		t.Errorf("Attribution = %q, 기대 node", items[0].Attribution)
	}
}

func TestDevicePluginPodIsDeviceAttributedWhenNDRReportsConsumer(t *testing.T) {
	pod := podWithLimits("a", "p1", "n1", map[string]string{"furiosa.ai/rngd": "1"})
	ndr := v1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: v1alpha1.NodeDeviceReportStatus{
		Consumers: []v1alpha1.DeviceConsumer{{DeviceID: "npu0", ResourceName: "furiosa.ai/rngd", Namespace: "a", Pod: "p1", Container: "c"}}}}
	v, ok := toConsumerView(pod, nil, []v1alpha1.NodeDeviceReport{ndr})
	if !ok || v.Attribution != AttributionDevice || !reflect.DeepEqual(v.DeviceUIDs, []string{"npu0"}) || v.DeviceIDSource != DeviceIDSourcePodResources {
		t.Fatalf("%+v", v)
	}
}

func TestDevicePluginPodStaysNodeAttributedWhenConsumersUnknown(t *testing.T) {
	pod := podWithLimits("a", "p1", "n1", map[string]string{"furiosa.ai/rngd": "1"})
	ndr := v1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: v1alpha1.NodeDeviceReportStatus{ConsumersError: "소켓 없음"}}
	v, _ := toConsumerView(pod, nil, []v1alpha1.NodeDeviceReport{ndr})
	if v.Attribution != AttributionNode || len(v.DeviceUIDs) != 0 || v.DeviceIDSource != "" {
		t.Fatalf("consumers 가 nil 이면 node 등급·DeviceUIDs 비움: %+v", v)
	}
}

func TestDevicePluginPodNotInConsumersListStaysNode(t *testing.T) {
	pod := podWithLimits("a", "p1", "n1", map[string]string{"furiosa.ai/rngd": "1"})
	ndr := v1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: v1alpha1.NodeDeviceReportStatus{Consumers: []v1alpha1.DeviceConsumer{}}}
	v, _ := toConsumerView(pod, nil, []v1alpha1.NodeDeviceReport{ndr})
	if v.Attribution != AttributionNode || len(v.DeviceUIDs) != 0 {
		t.Fatalf("빈 목록은 '쥔 컨테이너 없음' — 이 파드는 매칭되지 않는다: %+v", v)
	}
}

func TestDRAAttributionUnchangedByNDRConsumers(t *testing.T) {
	claimName := "c1"
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "p1"},
		Spec: corev1.PodSpec{NodeName: "n1", ResourceClaims: []corev1.PodResourceClaim{{Name: "gpu", ResourceClaimName: &claimName}}}}
	claim := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: claimName},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{
			Results: []resourcev1.DeviceRequestAllocationResult{{Request: "gpu", Driver: "gpu.nvidia.com", Pool: "n1", Device: "gpu-0"}}}}}}
	ndr := v1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: v1alpha1.NodeDeviceReportStatus{
		Consumers: []v1alpha1.DeviceConsumer{{DeviceID: "GPU-uuid", ResourceName: "nvidia.com/gpu", Namespace: "a", Pod: "p1", Container: "c"}}}}
	v, ok := toConsumerView(pod, []resourcev1.ResourceClaim{claim}, []v1alpha1.NodeDeviceReport{ndr})
	if !ok || v.Attribution != AttributionDevice || !reflect.DeepEqual(v.DeviceUIDs, []string{"gpu-0"}) || v.DeviceIDSource != DeviceIDSourceDRA {
		t.Fatalf("DRA 경로는 NDR consumers 와 무관하게 할당 결과를 쓴다: %+v", v)
	}
}

// spec.nodeName 으로 되짚는다(비어 있으면 metadata.name) — 다른 노드의 NDR 에 같은
// namespace/pod 소비자가 있어도 귀속하지 않는다(재스케줄 뒤 구 노드 NDR 이 최대
// 2분간 같은 이름을 들고 있는 창이 있다 — node-device-detector 의 PodResources
// 수집 신선도 창).
func TestDevicePluginPodIgnoresOtherNodesNDR(t *testing.T) {
	pod := podWithLimits("a", "p1", "n1", map[string]string{"furiosa.ai/rngd": "1"}) // 기존 헬퍼 시그니처에 맞춘다
	ndr := v1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n2"}, Status: v1alpha1.NodeDeviceReportStatus{
		Consumers: []v1alpha1.DeviceConsumer{{DeviceID: "npu0", ResourceName: "furiosa.ai/rngd", Namespace: "a", Pod: "p1", Container: "c"}}}}
	v, _ := toConsumerView(pod, nil, []v1alpha1.NodeDeviceReport{ndr})
	if v.Attribution != AttributionNode || len(v.DeviceUIDs) != 0 || v.DeviceIDSource != "" {
		t.Fatalf("다른 노드의 NDR 로는 귀속하지 않는다: %+v", v)
	}
}

// spec.nodeName 이 metadata.name 과 다른 NDR(재스케줄·수동 생성 등)에서도 device 등급으로
// 귀속해야 한다 — metadata.name 만 보면 이 사례에서 조용히 node 등급에 머문다.
func TestDevicePluginPodAttributesBySpecNodeNameNotMetadataName(t *testing.T) {
	pod := podWithLimits("a", "p1", "n1", map[string]string{"furiosa.ai/rngd": "1"})
	ndr := v1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "report-x"}, Spec: v1alpha1.NodeDeviceReportSpec{NodeName: "n1"},
		Status: v1alpha1.NodeDeviceReportStatus{
			Consumers: []v1alpha1.DeviceConsumer{{DeviceID: "npu0", ResourceName: "furiosa.ai/rngd", Namespace: "a", Pod: "p1", Container: "c"}}}}
	v, _ := toConsumerView(pod, nil, []v1alpha1.NodeDeviceReport{ndr})
	if v.Attribution != AttributionDevice || !reflect.DeepEqual(v.DeviceUIDs, []string{"npu0"}) || v.DeviceIDSource != DeviceIDSourcePodResources {
		t.Fatalf("metadata.name 이 달라도 spec.nodeName 으로 귀속해야 한다: %+v", v)
	}
}

// 파드가 컨테이너 둘로 서로 다른 가속기 자원을 쥐면, 그 노드 NDR 의 consumers
// 가 (컨테이너, resourceName, deviceID) 마다 한 항목씩 내므로 전부 모여야 한다.
func TestDevicePluginPodCollectsDeviceIDsAcrossContainersAndResources(t *testing.T) {
	pod := podWithLimits("a", "p1", "n1", map[string]string{"furiosa.ai/rngd": "1"})
	ndr := v1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: v1alpha1.NodeDeviceReportStatus{
		Consumers: []v1alpha1.DeviceConsumer{
			{DeviceID: "npu0", ResourceName: "furiosa.ai/rngd", Namespace: "a", Pod: "p1", Container: "c1"},
			{DeviceID: "GPU-abc", ResourceName: "nvidia.com/gpu", Namespace: "a", Pod: "p1", Container: "c2"},
		}}}
	v, ok := toConsumerView(pod, nil, []v1alpha1.NodeDeviceReport{ndr})
	if !ok || v.Attribution != AttributionDevice || v.DeviceIDSource != DeviceIDSourcePodResources ||
		!reflect.DeepEqual(v.DeviceUIDs, []string{"GPU-abc", "npu0"}) {
		t.Fatalf("컨테이너·자원이 여럿이어도 그 파드의 deviceID 는 전부 모인다: %+v", v)
	}
}

// 템플릿(resourceClaimTemplateName)으로 받은 claim 은 spec 에 이름이 없다. 실제 이름은
// kube-controller-manager(ResourceClaim 컨트롤러)가 지어 status.resourceClaimStatuses 에
// 싣는다 — 그 경로로 되짚지 않으면 할당 결과가 안 보이고 pending 으로 남는다.
func TestDRATemplateClaimIsResolvedFromPodStatus(t *testing.T) {
	tmpl, generated := docDRAClaimName, "doc-dra-probe-accel-j9szt"
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "p1"},
		Spec:   corev1.PodSpec{NodeName: "n1", ResourceClaims: []corev1.PodResourceClaim{{Name: "accel", ResourceClaimTemplateName: &tmpl}}},
		Status: corev1.PodStatus{ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "accel", ResourceClaimName: &generated}}}}
	claim := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: generated},
		Status: resourcev1.ResourceClaimStatus{Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{
			Results: []resourcev1.DeviceRequestAllocationResult{{Request: "accel", Driver: "tenstorrent.com", Pool: "n1", Device: "tt-0"}}}}}}
	v, ok := toConsumerView(pod, []resourcev1.ResourceClaim{claim}, nil)
	if !ok || v.Attribution != AttributionDevice || v.DeviceIDSource != DeviceIDSourceDRA || !reflect.DeepEqual(v.DeviceUIDs, []string{"tt-0"}) {
		t.Fatalf("템플릿 claim 의 할당 결과로 device 등급이어야 한다: %+v", v)
	}
	if len(v.Claims) != 1 || v.Claims[0].Name != generated || v.Claims[0].Status != "allocated" {
		t.Fatalf("claims 에 생성된 이름과 allocated 가 실려야 한다: %+v", v.Claims)
	}
}

// status 에 아직 생성 이름이 없으면(claim 미생성) 이름 없는 missing 으로 두고 pending 을 유지한다 —
// 지어내지 않는다.
func TestDRATemplateClaimWithoutStatusStaysPending(t *testing.T) {
	tmpl := docDRAClaimName
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "p1"},
		Spec: corev1.PodSpec{NodeName: "n1", ResourceClaims: []corev1.PodResourceClaim{{Name: "accel", ResourceClaimTemplateName: &tmpl}}}}
	v, ok := toConsumerView(pod, nil, nil)
	if !ok || v.Attribution != AttributionPending || len(v.Claims) != 1 || v.Claims[0].Name != "" || v.Claims[0].Status != "missing" {
		t.Fatalf("미생성 claim 은 pending·missing: %+v", v)
	}
}

// claim 의 status.reservedFor 가 이 파드를 담지 않으면(다른 파드가 이미 예약) 할당 결과가 있어도
// 이 파드에 귀속하지 않는다 — 같은 generation 의 템플릿 claim 을 다른 파드가 이미 쥐고 있을 수 있다.
func TestDRATemplateClaimReservedForOtherPodIsNotAttributed(t *testing.T) {
	tmpl, generated := docDRAClaimName, "doc-dra-probe-accel-j9szt"
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "p1", UID: "uid-p1"},
		Spec:   corev1.PodSpec{NodeName: "n1", ResourceClaims: []corev1.PodResourceClaim{{Name: "accel", ResourceClaimTemplateName: &tmpl}}},
		Status: corev1.PodStatus{ResourceClaimStatuses: []corev1.PodResourceClaimStatus{{Name: "accel", ResourceClaimName: &generated}}}}
	claim := resourcev1.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: generated},
		Status: resourcev1.ResourceClaimStatus{
			Allocation: &resourcev1.AllocationResult{Devices: resourcev1.DeviceAllocationResult{
				Results: []resourcev1.DeviceRequestAllocationResult{{Request: "accel", Driver: "tenstorrent.com", Pool: "n1", Device: "tt-0"}}}},
			ReservedFor: []resourcev1.ResourceClaimConsumerReference{{Resource: "pods", Name: "other", UID: "uid-other"}},
		}}
	v, ok := toConsumerView(pod, []resourcev1.ResourceClaim{claim}, nil)
	if !ok {
		t.Fatal("가속기를 요청한 파드이므로 ok 여야 한다")
	}
	if len(v.Claims) != 1 || v.Claims[0].Status != ClaimStatusReservedElsewhere {
		t.Fatalf("claim status=%+v, 기대 %q", v.Claims, ClaimStatusReservedElsewhere)
	}
	if v.Attribution != AttributionPending || len(v.DeviceUIDs) != 0 {
		t.Fatalf("다른 파드에 예약된 claim 의 장치를 이 파드에 귀속했다: attribution=%s uids=%v", v.Attribution, v.DeviceUIDs)
	}
}
