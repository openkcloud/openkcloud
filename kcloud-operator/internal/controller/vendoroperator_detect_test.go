// ============================================================
// vendoroperator_detect_test.go: 벤더 operator 감지 단위·reconcile 시험
// 상세: vendorOperatorPresent fake mapper 판정(nvidia·rebellions·tenstorrent) +
//
//	managed 벤더별 조건·경고 전이 검증(nvidia 는 envtest 로도 재확인)
//
// 생성일: 2026-09-20 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// TestNvidiaGPUOperatorPresent: fake RESTMapper 로 ClusterPolicy 서빙 여부만 판정한다
// (envtest 없이). draServedByMapper 단위 시험과 같은 방식.
func TestNvidiaGPUOperatorPresent(t *testing.T) {
	gv := schema.GroupVersion{Group: "nvidia.com", Version: "v1"}

	served := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	served.Add(gv.WithKind("ClusterPolicy"), meta.RESTScopeRoot)
	if !nvidiaGPUOperatorPresent(served) {
		t.Error("ClusterPolicy 가 등록된 mapper 는 present=true 여야 한다")
	}

	empty := meta.NewDefaultRESTMapper(nil)
	if nvidiaGPUOperatorPresent(empty) {
		t.Error("빈 mapper(미서빙)는 present=false 여야 한다")
	}

	if nvidiaGPUOperatorPresent(nil) {
		t.Error("nil mapper 는 present=false 여야 한다")
	}
}

// getVendorOpCond 는 정책을 다시 읽어 VendorOperatorDetected 조건을 돌려준다(없으면 nil).
func getVendorOpCond(key types.NamespacedName) *metav1.Condition {
	var p npuv1alpha1.NPUClusterPolicy
	ExpectWithOffset(1, k8sClient.Get(ctx, key, &p)).To(Succeed())
	return meta.FindStatusCondition(p.Status.Conditions, "VendorOperatorDetected")
}

// countEventsContaining 은 FakeRecorder 채널을 비우며 sub 를 포함한 이벤트 수를 센다.
func countEventsContaining(rec *record.FakeRecorder, sub string) int {
	n := 0
	for {
		select {
		case e := <-rec.Events:
			if strings.Contains(e, sub) {
				n++
			}
		default:
			return n
		}
	}
}

var _ = Describe("VendorOperatorDetected — NVIDIA GPU Operator 감지", func() {
	It("managed nvidia 에서 감지 시 조건 True·경고 1회, 미감지로 바뀌면 False", func() {
		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-vendorop-detect", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/npu-op-detector:test"},
				Nvidia:   npuv1alpha1.NvidiaSpec{Enabled: true, DevicePluginImage: "img:v1"},
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		key := types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}
		DeferCleanup(func() {
			deleteNCPAndWait(policy.Name, policy.Namespace)
			for _, n := range []string{"nvidia-device-plugin", "nvidia-device-plugin-flat"} {
				_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: "kube-system"}})
			}
		})

		gv := schema.GroupVersion{Group: "nvidia.com", Version: "v1"}
		served := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
		served.Add(gv.WithKind("ClusterPolicy"), meta.RESTScopeRoot)

		rec := record.NewFakeRecorder(200)
		r := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: rec, mapper: served}
		req := reconcile.Request{NamespacedName: key}

		// 1) 감지 전이: 조건 True + reason NvidiaGPUOperator
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		cond := getVendorOpCond(key)
		Expect(cond).NotTo(BeNil(), "VendorOperatorDetected 조건이 있어야 한다")
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("NvidiaGPUOperator"))

		// 2) 재조정(여전히 감지): 새 경고 없음 — 전이에서만 이벤트
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(getVendorOpCond(key).Status).To(Equal(metav1.ConditionTrue))
		Expect(countEventsContaining(rec, "VendorOperatorDetected")).To(Equal(1),
			"경고는 False→True 전이 1회만 나야 한다")

		// 3) 미감지로 전환(mapper 에서 ClusterPolicy 사라짐): 조건 False/NotDetected
		r.mapper = meta.NewDefaultRESTMapper(nil)
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		cond = getVendorOpCond(key)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("NotDetected"))
	})
})

// TestDetectNvidiaGPUOperatorWarnsOnlyOnTransition: 경고는 최초/False→True 전이에서만 1회.
// prev 가 조건 슬라이스 내부 포인터라 SetStatusCondition 제자리 수정이 전이 판정을 가리던
// 결함(라이브 재현)을 고정한다 — 클라이언트 없이 detect 를 직접 부른다.
func TestDetectNvidiaGPUOperatorWarnsOnlyOnTransition(t *testing.T) {
	gv := schema.GroupVersion{Group: "nvidia.com", Version: "v1"}
	served := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	served.Add(gv.WithKind("ClusterPolicy"), meta.RESTScopeRoot)

	managedNvidia := func(pre *metav1.ConditionStatus) *npuv1alpha1.NPUClusterPolicy {
		p := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-detect-unit"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Nvidia: npuv1alpha1.NvidiaSpec{Enabled: true, DevicePluginImage: "img:v1"},
			},
		}
		if pre != nil {
			meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
				Type: "VendorOperatorDetected", Status: *pre, Reason: "seed", Message: "seed",
			})
		}
		return p
	}
	falseStatus, trueStatus := metav1.ConditionFalse, metav1.ConditionTrue

	cases := []struct {
		name string
		pre  *metav1.ConditionStatus
		want int
	}{
		{"조건 없음(최초)→감지: 경고 1회", nil, 1},
		{"조건 False 선존재→감지: 경고 1회", &falseStatus, 1},
		{"조건 True 선존재→재감지: 경고 0회(스팸 방지)", &trueStatus, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := record.NewFakeRecorder(10)
			r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served}
			p := managedNvidia(tc.pre)
			r.detectNvidiaGPUOperator(context.Background(), p)

			cond := meta.FindStatusCondition(p.Status.Conditions, "VendorOperatorDetected")
			if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "NvidiaGPUOperator" {
				t.Fatalf("조건이 True/NvidiaGPUOperator 여야 한다: %+v", cond)
			}
			if got := countEventsContaining(rec, "VendorOperatorDetected"); got != tc.want {
				t.Errorf("경고 이벤트 %d, want %d", got, tc.want)
			}
		})
	}
}

// TestVendorOperatorPresent: fake RESTMapper 로 rebellions·tenstorrent 벤더 시그니처 GroupKind
// 등록/미등록에 대해 감지 true/false 를 판정한다(TestNvidiaGPUOperatorPresent 와 동일 방식).
func TestVendorOperatorPresent(t *testing.T) {
	cases := []struct {
		name   string
		vendor string
		gk     schema.GroupKind
	}{
		{"rebellions RblnClusterPolicy", "rebellions", schema.GroupKind{Group: "rebellions.ai", Kind: "RblnClusterPolicy"}},
		{"tenstorrent TenstorrentDriverPolicy", "tenstorrent", schema.GroupKind{Group: "driver.tenstorrent.com", Kind: "TenstorrentDriverPolicy"}},
		{"tenstorrent TenstorrentFirmwarePolicy", "tenstorrent", schema.GroupKind{Group: "firmware.tenstorrent.com", Kind: "TenstorrentFirmwarePolicy"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gv := schema.GroupVersion{Group: tc.gk.Group, Version: "v1alpha1"}
			served := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
			served.Add(gv.WithKind(tc.gk.Kind), meta.RESTScopeRoot)
			if !vendorOperatorPresent(served, tc.vendor) {
				t.Errorf("%s 가 등록된 mapper 는 %s present=true 여야 한다", tc.gk, tc.vendor)
			}

			empty := meta.NewDefaultRESTMapper(nil)
			if vendorOperatorPresent(empty, tc.vendor) {
				t.Errorf("빈 mapper(미서빙)는 %s present=false 여야 한다", tc.vendor)
			}
		})
	}

	if vendorOperatorPresent(nil, "rebellions") || vendorOperatorPresent(nil, "tenstorrent") {
		t.Error("nil mapper 는 present=false 여야 한다")
	}
}

// firstSignatureMapper 는 vendor 시그니처의 첫 GroupKind 하나만 서빙하는 fake RESTMapper 를
// 만든다 — 여러 GroupKind 각각의 present 판정은 TestVendorOperatorPresent 가 이미 보므로,
// 전이 시험은 벤더당 하나만 서빙해도 충분하다.
func firstSignatureMapper(vendor string) meta.RESTMapper {
	gk := vendorOperatorSignatures[vendor].groupKinds[0]
	gv := schema.GroupVersion{Group: gk.Group, Version: "v1alpha1"}
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	m.Add(gv.WithKind(gk.Kind), meta.RESTScopeRoot)
	return m
}

// TestDetectVendorOperatorWarnsOnlyOnTransition: rebellions·tenstorrent 도 nvidia 와 같은
// "경고는 최초/False→True 전이에서만 1회" 규칙을 따르는지 확인한다
// (TestDetectNvidiaGPUOperatorWarnsOnlyOnTransition 과 동일 형태, 공유 detectVendorOperator 를
// managed=true 로 직접 불러 감지 True 경로를 편다).
func TestDetectVendorOperatorWarnsOnlyOnTransition(t *testing.T) {
	falseStatus, trueStatus := metav1.ConditionFalse, metav1.ConditionTrue

	cases := []struct {
		name   string
		vendor string
		pre    *metav1.ConditionStatus
		want   int
	}{
		{"rebellions 조건 없음(최초)→감지: 경고 1회", "rebellions", nil, 1},
		{"rebellions 조건 False 선존재→감지: 경고 1회", "rebellions", &falseStatus, 1},
		{"rebellions 조건 True 선존재→재감지: 경고 0회(스팸 방지)", "rebellions", &trueStatus, 0},
		{"tenstorrent 조건 없음(최초)→감지: 경고 1회", "tenstorrent", nil, 1},
		{"tenstorrent 조건 False 선존재→감지: 경고 1회", "tenstorrent", &falseStatus, 1},
		{"tenstorrent 조건 True 선존재→재감지: 경고 0회(스팸 방지)", "tenstorrent", &trueStatus, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := vendorOperatorSignatures[tc.vendor]
			rec := record.NewFakeRecorder(10)
			r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: firstSignatureMapper(tc.vendor)}
			p := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "np-detect-unit-" + tc.vendor}}
			if tc.pre != nil {
				meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
					Type: sig.conditionType, Status: *tc.pre, Reason: "seed", Message: "seed",
				})
			}
			r.detectVendorOperator(context.Background(), p, true, tc.vendor)

			cond := meta.FindStatusCondition(p.Status.Conditions, sig.conditionType)
			if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != sig.detectedReason {
				t.Fatalf("조건이 True/%s 여야 한다: %+v", sig.detectedReason, cond)
			}
			if got := countEventsContaining(rec, sig.conditionType); got != tc.want {
				t.Errorf("경고 이벤트 %d, want %d", got, tc.want)
			}
		})
	}
}

// countingCRDReader 는 apiextensionsv1 스킴만 등록한 fake client 를 감싸 Get 호출 횟수를 센다 —
// "조건이 False 면 CRD GET 자체를 하지 않는다"를 직접 단언하기 위한 시험 전용 wrapper 다.
type countingCRDReader struct {
	client.Reader
	gets int
}

func (c *countingCRDReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c.gets++
	return c.Reader.Get(ctx, key, obj, opts...)
}

// newCountingCRDReader 는 objs 만 담은 fake client 기반 countingCRDReader 를 만든다(objs 를
// 비우면 어떤 CRD 이름으로 GET 해도 NotFound 다).
func newCountingCRDReader(t *testing.T, objs ...client.Object) *countingCRDReader {
	t.Helper()
	s := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatalf("apiextensionsv1 스킴 등록 실패: %v", err)
	}
	return &countingCRDReader{Reader: fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()}
}

// newVendorRecheckFixture 는 nvidia 시그니처를 서빙하는 fake mapper, 그 매핑에 대응하는 CRD
// 이름, VendorOperatorDetected 조건을 seed 상태로 미리 채운 policy 를 만든다 — 재확인 4경로
// 시험이 이 fixture 를 공유한다.
func newVendorRecheckFixture(t *testing.T, seed metav1.ConditionStatus) (*npuv1alpha1.NPUClusterPolicy, meta.RESTMapper, string) {
	t.Helper()
	served := firstSignatureMapper("nvidia")
	mapping, err := served.RESTMapping(vendorOperatorSignatures["nvidia"].groupKinds[0])
	if err != nil {
		t.Fatalf("매핑 준비 실패: %v", err)
	}
	crdName := mapping.Resource.Resource + "." + mapping.Resource.Group

	p := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "np-vendorop-recheck"},
		Spec:       npuv1alpha1.NPUClusterPolicySpec{Nvidia: npuv1alpha1.NvidiaSpec{Enabled: true}},
	}
	meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
		Type: vendorOperatorDetectedCondition, Status: seed, Reason: "seed", Message: "seed",
	})
	return p, served, crdName
}

// TestDetectVendorOperatorCRDRecheck: RESTMapper 캐시가 stale 하게 present=true 를 돌려줘도,
// 조건이 이미 True 인 동안은 reconcile 마다 APIReader 로 CRD 존재를 재확인해 실제 제거를
// 잡아내는지 4가지 경로로 확인한다.
func TestDetectVendorOperatorCRDRecheck(t *testing.T) {
	t.Run("조건 True + CRD 존재 → True 유지, 새 경고 없음", func(t *testing.T) {
		p, served, crdName := newVendorRecheckFixture(t, metav1.ConditionTrue)
		reader := newCountingCRDReader(t, &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: crdName},
		})
		rec := record.NewFakeRecorder(10)
		r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served, APIReader: reader}

		r.detectNvidiaGPUOperator(context.Background(), p)

		cond := meta.FindStatusCondition(p.Status.Conditions, vendorOperatorDetectedCondition)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Fatalf("조건이 True 로 유지돼야 한다: %+v", cond)
		}
		if reader.gets != 1 {
			t.Errorf("CRD GET 1회여야 한다, got %d", reader.gets)
		}
		if got := countEventsContaining(rec, vendorOperatorDetectedCondition); got != 0 {
			t.Errorf("이미 True 였으므로 새 경고 0회여야 한다, got %d", got)
		}
	})

	t.Run("조건 True + CRD GET NotFound → False(OperatorCRDRemoved)", func(t *testing.T) {
		p, served, _ := newVendorRecheckFixture(t, metav1.ConditionTrue)
		reader := newCountingCRDReader(t) // CRD 객체 없음 → Get 은 NotFound
		rec := record.NewFakeRecorder(10)
		r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served, APIReader: reader}

		r.detectNvidiaGPUOperator(context.Background(), p)

		cond := meta.FindStatusCondition(p.Status.Conditions, vendorOperatorDetectedCondition)
		if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != reasonOperatorCRDRemoved {
			t.Fatalf("조건이 False/OperatorCRDRemoved 여야 한다: %+v", cond)
		}
		if reader.gets != 1 {
			t.Errorf("CRD GET 1회여야 한다, got %d", reader.gets)
		}
	})

	t.Run("조건 False + CRD 실재 → 재확인으로 True 확정, GET 1회", func(t *testing.T) {
		// 이전 상태가 False 여도 mapper 가 present 라고 하면 재확인한다 — CRD 가 실재하면 True 로
		// 올바르게 확정한다. 재확인을 "이전 True" 로만 걸면 flapping 이 생기므로 present 로 건다.
		p, served, crdName := newVendorRecheckFixture(t, metav1.ConditionFalse)
		reader := newCountingCRDReader(t, &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: crdName},
		})
		rec := record.NewFakeRecorder(10)
		r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served, APIReader: reader}

		r.detectNvidiaGPUOperator(context.Background(), p)

		cond := meta.FindStatusCondition(p.Status.Conditions, vendorOperatorDetectedCondition)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Fatalf("mapper present + CRD 실재이므로 True 로 가야 한다: %+v", cond)
		}
		if reader.gets != 1 {
			t.Errorf("present 벤더는 이전 상태와 무관하게 재확인 GET 1회여야 한다, got %d", reader.gets)
		}
	})

	t.Run("mapper 가 absent 인 벤더 → CRD GET 0회(비용 상한)", func(t *testing.T) {
		// 감지 비용은 mapper 가 present 라고 한 벤더에만 든다 — nvidia 만 서빙하는 mapper 로
		// rebellions 를 감지하면 present=false 라 재확인 GET 을 하지 않는다.
		p, served, _ := newVendorRecheckFixture(t, metav1.ConditionTrue)
		p.Spec.Rebellions = npuv1alpha1.RebellionsSpec{Enabled: true}
		reader := newCountingCRDReader(t)
		rec := record.NewFakeRecorder(10)
		r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served, APIReader: reader}

		r.detectRebellionsOperator(context.Background(), p)

		if reader.gets != 0 {
			t.Errorf("mapper 에 없는 벤더는 재확인 GET 0회여야 한다, got %d", reader.gets)
		}
	})

	t.Run("APIReader nil → 재확인 없이 기존 동작 유지", func(t *testing.T) {
		p, served, _ := newVendorRecheckFixture(t, metav1.ConditionTrue)
		rec := record.NewFakeRecorder(10)
		r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served, APIReader: nil}

		r.detectNvidiaGPUOperator(context.Background(), p)

		cond := meta.FindStatusCondition(p.Status.Conditions, vendorOperatorDetectedCondition)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Fatalf("APIReader nil 이면 재확인 없이 기존 동작(True 유지)이어야 한다: %+v", cond)
		}
	})
}

// TestDetectVendorOperatorRecheckPreventsFlapping 은 CRD 가 제거된 뒤 두 pass 연속으로 조건이
// False 로 유지되는지 본다. 재확인을 "이전 상태 True" 로만 걸면, 첫 pass 가 False 로 내린 다음
// pass 는 재확인을 건너뛰고 stale mapper 캐시가 다시 True 로 올려 매 pass 조건이 뒤집힌다
// (2분마다 Warning 이벤트). 재확인을 present 기준으로 돌리면 이 flapping 이 사라진다.
func TestDetectVendorOperatorRecheckPreventsFlapping(t *testing.T) {
	p, served, _ := newVendorRecheckFixture(t, metav1.ConditionTrue)
	reader := newCountingCRDReader(t) // CRD 객체 없음 → GET 은 NotFound
	rec := record.NewFakeRecorder(10)
	r := &NPUClusterPolicyReconciler{Recorder: rec, mapper: served, APIReader: reader}

	// pass N: 조건 True → CRD 재확인 NotFound → False(OperatorCRDRemoved)
	r.detectNvidiaGPUOperator(context.Background(), p)
	cond := meta.FindStatusCondition(p.Status.Conditions, vendorOperatorDetectedCondition)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != reasonOperatorCRDRemoved {
		t.Fatalf("pass N: False/OperatorCRDRemoved 여야 한다: %+v", cond)
	}
	if reader.gets != 1 {
		t.Fatalf("pass N: CRD GET 은 pass 당 1회여야 한다, got %d", reader.gets)
	}
	_ = countEventsContaining(rec, vendorOperatorDetectedCondition) // pass N 이벤트 비움

	// pass N+1: 이전 상태가 False 여도 재확인해 False 유지 — 캐시가 다시 True 로 올리지 않는다.
	r.detectNvidiaGPUOperator(context.Background(), p)
	cond = meta.FindStatusCondition(p.Status.Conditions, vendorOperatorDetectedCondition)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("pass N+1: False 로 유지돼야 한다(flapping 금지): %+v", cond)
	}
	if reader.gets != 2 {
		t.Fatalf("pass N+1: CRD GET 은 pass 당 1회(누적 2)여야 한다, got %d", reader.gets)
	}
	if got := countEventsContaining(rec, vendorOperatorDetectedCondition); got != 0 {
		t.Fatalf("pass N+1: 새 이벤트 0회여야 한다(flapping 경고 금지), got %d", got)
	}
}

// TestDetectVendorOperatorPresence 는 세 값 감지 계약을 고정한다. delegated 배포 모드가 이
// 값 하나로 갈리므로 Absent 와 Unknown 을 반드시 구분해야 한다: Unknown 은 감지 시그니처가
// 없어 판정 불가(Absent 로 두면 "없음 확정"으로 읽혀 delegated 선언을 잘못 거절하고 호출부가
// 매번 오경고한다), Absent 는 시그니처가 있고 CRD 미서빙을 확인(delegated 면 경고 대상),
// Present 는 시그니처 CRD 가 서빙됨이다. furiosa 만 시그니처가 없다(vendoroperator_detect.go
// 상단 주석) — rebellions·tenstorrent 는 시그니처가 있어 Absent/Present 로 판정된다.
func TestDetectVendorOperatorPresence(t *testing.T) {
	ctx := context.Background()

	t.Run("시그니처 없는 벤더는 Unknown", func(t *testing.T) {
		got, err := DetectVendorOperator(ctx, meta.NewDefaultRESTMapper(nil), "furiosa")
		if err != nil {
			t.Fatalf("furiosa: %v", err)
		}
		if got != PresenceUnknown {
			t.Errorf("furiosa = %s, want %s", got, PresenceUnknown)
		}
	})

	t.Run("시그니처 있고 CRD 미서빙이면 Absent", func(t *testing.T) {
		for _, v := range []string{"nvidia", "rebellions", "tenstorrent"} {
			got, err := DetectVendorOperator(ctx, meta.NewDefaultRESTMapper(nil), v)
			if err != nil {
				t.Fatalf("%s: %v", v, err)
			}
			if got != PresenceAbsent {
				t.Errorf("%s = %s, want %s", v, got, PresenceAbsent)
			}
		}
	})

	t.Run("시그니처 CRD 가 서빙되면 Present", func(t *testing.T) {
		for _, v := range []string{"nvidia", "rebellions", "tenstorrent"} {
			got, err := DetectVendorOperator(ctx, firstSignatureMapper(v), v)
			if err != nil {
				t.Fatalf("%s: %v", v, err)
			}
			if got != PresencePresent {
				t.Errorf("%s = %s, want %s", v, got, PresencePresent)
			}
		}
	})
}

// erroringRESTMapper 는 RESTMapping 이 항상 지정된 오류를 돌려주는 fake RESTMapper 다 —
// no-match 가 아닌 discovery 실패(예: 일시 장애)를 흉내낸다. 이 시험 경로는 RESTMapping 만
// 부르므로 나머지 메서드는 호출되지 않는다.
type erroringRESTMapper struct{ err error }

func (m erroringRESTMapper) KindFor(schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, m.err
}

func (m erroringRESTMapper) KindsFor(schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, m.err
}

func (m erroringRESTMapper) ResourceFor(schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, m.err
}

func (m erroringRESTMapper) ResourcesFor(schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, m.err
}

func (m erroringRESTMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, m.err
}

func (m erroringRESTMapper) RESTMappings(schema.GroupKind, ...string) ([]*meta.RESTMapping, error) {
	return nil, m.err
}

func (m erroringRESTMapper) ResourceSingularizer(resource string) (string, error) {
	return resource, m.err
}

// TestDetectVendorOperatorPresenceUnknownOnDiscoveryError: RESTMapping 이 no-match 가 아닌
// 오류(discovery 일시 장애 등)를 돌려주면 Unknown 이어야 한다 — Absent 로 뭉개면 delegated
// 선언에 warnDelegatedOperatorAbsent 가 "위임 대상 없음"을 잘못 경고한다. no-match 오류와
// 그 외 오류를 구분하지 않으면(둘 다 Absent) 이 시험이 실패한다.
func TestDetectVendorOperatorPresenceUnknownOnDiscoveryError(t *testing.T) {
	injected := errors.New("discovery 일시 장애")
	got, err := DetectVendorOperator(context.Background(), erroringRESTMapper{err: injected}, "nvidia")
	if got != PresenceUnknown {
		t.Errorf("presence = %s, want %s", got, PresenceUnknown)
	}
	if !errors.Is(err, injected) {
		t.Errorf("err = %v, want %v", err, injected)
	}
}

// TestDetectVendorOperatorRecommendationGateDelegated: managed 게이트는 IsOperatorOwned() 를
// 써야 한다 — !IsExternal() 로 되돌리면 Enabled=true 인 delegated 정책도 "관리 중"으로 잘못
// 판정해, 이미 위임한 벤더에 또 delegated 전환을 권고하는 조건 True 가 선다. Enabled=true +
// delegated 는 webhook 이 막지 않는 조합이다(devicePluginImage 요구는 IsOperatorOwned() 일
// 때만 걸린다) — 세 호출부(detectNvidiaGPUOperator·detectRebellionsOperator·
// detectTenstorrentOperator)를 모두 돈다.
func TestDetectVendorOperatorRecommendationGateDelegated(t *testing.T) {
	cases := []struct {
		vendor string
		seed   func(*npuv1alpha1.NPUClusterPolicySpec)
		detect func(*NPUClusterPolicyReconciler, *npuv1alpha1.NPUClusterPolicy)
	}{
		{
			vendor: "nvidia",
			seed: func(s *npuv1alpha1.NPUClusterPolicySpec) {
				s.Nvidia = npuv1alpha1.NvidiaSpec{Enabled: true, ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"}}
			},
			detect: func(r *NPUClusterPolicyReconciler, p *npuv1alpha1.NPUClusterPolicy) {
				r.detectNvidiaGPUOperator(context.Background(), p)
			},
		},
		{
			vendor: "rebellions",
			seed: func(s *npuv1alpha1.NPUClusterPolicySpec) {
				s.Rebellions = npuv1alpha1.RebellionsSpec{Enabled: true, ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"}}
			},
			detect: func(r *NPUClusterPolicyReconciler, p *npuv1alpha1.NPUClusterPolicy) {
				r.detectRebellionsOperator(context.Background(), p)
			},
		},
		{
			vendor: "tenstorrent",
			seed: func(s *npuv1alpha1.NPUClusterPolicySpec) {
				s.Tenstorrent = npuv1alpha1.TenstorrentSpec{Enabled: true, ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"}}
			},
			detect: func(r *NPUClusterPolicyReconciler, p *npuv1alpha1.NPUClusterPolicy) {
				r.detectTenstorrentOperator(context.Background(), p)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.vendor, func(t *testing.T) {
			p := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "np-detect-delegated-" + tc.vendor}}
			tc.seed(&p.Spec)
			r := &NPUClusterPolicyReconciler{Recorder: record.NewFakeRecorder(10), mapper: firstSignatureMapper(tc.vendor)}
			tc.detect(r, p)

			cond := meta.FindStatusCondition(p.Status.Conditions, vendorOperatorSignatures[tc.vendor].conditionType)
			if cond == nil || cond.Status != metav1.ConditionFalse {
				t.Fatalf("delegated+Enabled=true 는 재권고 조건이 False 여야 한다(이미 위임한 벤더): %+v", cond)
			}
		})
	}
}

// perKindRESTMapper 는 GroupKind 별로 다른 오류를 돌려주는 fake RESTMapper 다 —
// 시그니처가 둘인 벤더(tenstorrent)에서 두 오류가 모두 보존되는지 본다.
type perKindRESTMapper struct {
	erroringRESTMapper
	errs map[schema.GroupKind]error
}

func (m perKindRESTMapper) RESTMapping(gk schema.GroupKind, _ ...string) (*meta.RESTMapping, error) {
	if err, ok := m.errs[gk]; ok {
		return nil, err
	}
	return nil, m.err
}

// TestDetectVendorOperatorKeepsEveryDiscoveryError 는 GroupKind 둘이 서로 다른 오류로
// 실패할 때 마지막 것만 남기지 않고 둘 다 반환 오류에서 찾을 수 있는지 본다.
func TestDetectVendorOperatorKeepsEveryDiscoveryError(t *testing.T) {
	errDriver := errors.New("driver 조회 실패")
	errFirmware := errors.New("firmware 조회 실패")
	m := perKindRESTMapper{
		erroringRESTMapper: erroringRESTMapper{err: errors.New("unused")},
		errs: map[schema.GroupKind]error{
			{Group: "driver.tenstorrent.com", Kind: "TenstorrentDriverPolicy"}:     errDriver,
			{Group: "firmware.tenstorrent.com", Kind: "TenstorrentFirmwarePolicy"}: errFirmware,
		},
	}
	got, err := DetectVendorOperator(context.Background(), m, "tenstorrent")
	if got != PresenceUnknown {
		t.Fatalf("presence = %s, want %s", got, PresenceUnknown)
	}
	for _, want := range []error{errDriver, errFirmware} {
		if !errors.Is(err, want) {
			t.Errorf("err = %v 에 %v 가 없다", err, want)
		}
	}
}
