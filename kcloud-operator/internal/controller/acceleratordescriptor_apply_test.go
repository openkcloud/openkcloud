// ============================================================
// acceleratordescriptor_apply_test.go: descriptor auto-apply envtest
// 상세: 중재(관리 벤더 거절)·SSA apply·능동 prune 을 실 apiserver(envtest)에서 검증한다. fake
//       client 는 SSA 를 지원 못 해 DeviceClass(v1) 서빙 가능한 가장 높은 bin/k8s 버전을 직접 고른다.
// 생성일: 2026-09-18 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/descriptor"
	"kcloud-operator/internal/vendorcap"
)

// auto-apply 시험 전용 envtest client(Ginkgo suite 의 k8sClient 와 분리 — 그쪽은 1.31 을 잡는다).
var aaClient client.Client

// TestMain 은 이 패키지의 유일한 TestMain 이다(Ginkgo suite 는 BeforeSuite 로 자체 env 를 띄운다).
// resource.k8s.io/v1(DeviceClass)를 내는 apiserver 가 필요해 KUBEBUILDER_ASSETS 가 없으면
// bin/k8s 중 가장 높은 버전을 고른다.
func TestMain(m *testing.M) {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if dir := newestEnvtestBinDir(); dir != "" {
			env.BinaryAssetsDirectory = dir
		}
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic("clientgo scheme 등록 실패: " + err.Error())
	}
	if err := npuv1alpha1.AddToScheme(scheme); err != nil {
		panic("npu scheme 등록 실패: " + err.Error())
	}

	cfg, err := env.Start()
	if err != nil {
		panic("envtest 시작 실패: " + err.Error())
	}
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		_ = env.Stop()
		panic("client 생성 실패: " + err.Error())
	}
	aaClient = cl

	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

// newestEnvtestBinDir 는 bin/k8s 아래 가장 높은 버전 디렉터리를 고른다. 문자열 비교는
// 1.9 > 1.34 로 잘못 판정하므로 major.minor.patch 를 정수로 파싱해 비교한다.
func newestEnvtestBinDir() string {
	base := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	best := ""
	bestKey := [3]int{-1, -1, -1}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ver := e.Name()
		if i := strings.IndexByte(ver, '-'); i >= 0 {
			ver = ver[:i]
		}
		var a, b, c int
		if n, _ := fmt.Sscanf(ver, "%d.%d.%d", &a, &b, &c); n < 2 {
			continue
		}
		key := [3]int{a, b, c}
		if key[0] > bestKey[0] ||
			(key[0] == bestKey[0] && (key[1] > bestKey[1] ||
				(key[1] == bestKey[1] && key[2] > bestKey[2]))) {
			bestKey = key
			best = filepath.Join(base, e.Name())
		}
	}
	return best
}

// autoApplyReconciler 는 생성물을 default 네임스페이스(envtest 에 항상 존재)에 두는 reconciler 다.
func autoApplyReconciler() *AcceleratorDescriptorReconciler {
	return &AcceleratorDescriptorReconciler{
		Client:             aaClient,
		Scheme:             aaClient.Scheme(),
		DPImage:            "example.com/generic-dp:test",
		DRAImage:           "example.com/generic-dra:test",
		GeneratedNamespace: "default",
	}
}

func reconcileOnce(t *testing.T, r *AcceleratorDescriptorReconciler, name string) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), reconcileReq(name)); err != nil {
		t.Fatalf("Reconcile(%s) 실패: %v", name, err)
	}
}

func hasRefusalContaining(refusals []npuv1alpha1.DescriptorRefusal, sub string) bool {
	for _, rf := range refusals {
		if strings.Contains(rf.Reason, sub) {
			return true
		}
	}
	return false
}

// 벤더가 어느 NPUClusterPolicy 에도 없으면 backend 배포물을 생성한다.
func TestAutoApplyCreatesBackends(t *testing.T) {
	ctx := context.Background()
	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-create-tt"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "tenstorrent", Product: "blackhole-p150", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/tenstorrent/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
		},
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	reconcileOnce(t, autoApplyReconciler(), desc.Name)

	name := generatedDevicePluginNameFor(desc)
	var cm corev1.ConfigMap
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &cm); err != nil {
		t.Fatalf("생성 ConfigMap %q 이 없다: %v", name, err)
	}
	if cm.Labels[descriptorOwnerLabel] != desc.Name {
		t.Fatalf("ConfigMap 소유 라벨 미부여(%q 기대): %v", desc.Name, cm.Labels)
	}
	var ds appsv1.DaemonSet
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &ds); err != nil {
		t.Fatalf("생성 DaemonSet %q 이 없다: %v", name, err)
	}
	// cluster-scoped descriptor 가 namespaced 생성물의 controller ownerRef 로 걸렸는지 확인.
	if len(ds.OwnerReferences) == 0 || ds.OwnerReferences[0].Name != desc.Name {
		t.Fatalf("DaemonSet ownerRef 미설정: %+v", ds.OwnerReferences)
	}
}

// 벤더가 어느 NPUClusterPolicy 에서 Enabled 면 거절하고 배포물을 만들지 않는다.
func TestAutoApplyRefusedWhenVendorManaged(t *testing.T) {
	ctx := context.Background()
	// NPUClusterPolicy 는 namespaced 다 — arbitration List 는 전 네임스페이스를 훑으므로 위치는 무관.
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-managed-policy", Namespace: "default"},
		Spec:       npuv1alpha1.NPUClusterPolicySpec{Rebellions: npuv1alpha1.RebellionsSpec{Enabled: true}},
	}
	if err := aaClient.Create(ctx, policy); err != nil {
		t.Fatalf("NPUClusterPolicy 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, policy) })

	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-managed-rbln"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "rebellions", Product: "atom-plus", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/rebellions/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
		},
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	reconcileOnce(t, autoApplyReconciler(), desc.Name)

	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if !hasRefusalContaining(got.Status.Refusals, "관리 중") {
		t.Fatalf("관리중 거절 사유가 status 에 없다: %+v", got.Status.Refusals)
	}
	name := generatedDevicePluginNameFor(desc)
	err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &corev1.ConfigMap{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("거절됐는데 ConfigMap %q 이 존재/오류(err=%v)", name, err)
	}
}

// backend 에서 DRA 를 빼면 다음 reconcile 이 DRA 생성물을 회수한다(DP 는 남는다).
func TestAutoApplyPrunesDroppedBackend(t *testing.T) {
	ctx := context.Background()
	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prune-tt"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "tenstorrent", Product: "blackhole-prune", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/tenstorrent/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin, npuv1alpha1.BackendDRA},
		},
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	r := autoApplyReconciler()
	reconcileOnce(t, r, desc.Name)

	draName := generatedDRADriverNameFor(desc)
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: draName}, &appsv1.DaemonSet{}); err != nil {
		t.Fatalf("DRA DaemonSet %q 이 먼저 생성돼야 한다: %v", draName, err)
	}
	dcName := descriptor.DriverNameFor(desc.Spec)
	if err := aaClient.Get(ctx, types.NamespacedName{Name: dcName}, &resourcev1.DeviceClass{}); err != nil {
		t.Fatalf("DRA DeviceClass %q 이 먼저 생성돼야 한다: %v", dcName, err)
	}

	// DRA 를 backend 목록에서 뺀다.
	var cur npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &cur); err != nil {
		t.Fatalf("descriptor 재조회 실패: %v", err)
	}
	cur.Spec.Backends = []string{npuv1alpha1.BackendDevicePlugin}
	if err := aaClient.Update(ctx, &cur); err != nil {
		t.Fatalf("descriptor 갱신 실패: %v", err)
	}
	reconcileOnce(t, r, desc.Name)

	// DRA DaemonSet·DeviceClass 는 사라지고 DP DaemonSet 은 남아야 한다.
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: draName}, &appsv1.DaemonSet{}); !apierrors.IsNotFound(err) {
		t.Fatalf("DRA DaemonSet %q 이 prune 되지 않았다(err=%v)", draName, err)
	}
	if err := aaClient.Get(ctx, types.NamespacedName{Name: dcName}, &resourcev1.DeviceClass{}); !apierrors.IsNotFound(err) {
		t.Fatalf("DRA DeviceClass %q 이 prune 되지 않았다(err=%v)", dcName, err)
	}
	dpName := generatedDevicePluginNameFor(desc)
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: dpName}, &appsv1.DaemonSet{}); err != nil {
		t.Fatalf("DP DaemonSet %q 은 남아야 하는데 없다: %v", dpName, err)
	}
}

// 벤더가 device-plugin 은 꺼졌어도 per-vendor DRA 로 관리되면 거절한다(같은 물리 /dev 충돌).
func TestAutoApplyRefusedWhenVendorDRAManaged(t *testing.T) {
	ctx := context.Background()
	dpDisabledDRAOn := true
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-dra-managed-policy", Namespace: "default"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled: false,
				DRA:     &npuv1alpha1.DRASpec{Enabled: dpDisabledDRAOn},
			},
		},
	}
	if err := aaClient.Create(ctx, policy); err != nil {
		t.Fatalf("NPUClusterPolicy 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, policy) })

	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-dra-managed-nvidia"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "nvidia", Product: "a30-dra", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/nvidia/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDRA},
		},
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	reconcileOnce(t, autoApplyReconciler(), desc.Name)

	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if !hasRefusalContaining(got.Status.Refusals, "관리 중") {
		t.Fatalf("DRA 관리중 거절 사유가 status 에 없다: %+v", got.Status.Refusals)
	}
	draName := generatedDRADriverNameFor(desc)
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: draName}, &appsv1.DaemonSet{}); !apierrors.IsNotFound(err) {
		t.Fatalf("DRA 관리중인데 DaemonSet %q 이 생성됨(err=%v)", draName, err)
	}
	dcName := descriptor.DriverNameFor(desc.Spec)
	if err := aaClient.Get(ctx, types.NamespacedName{Name: dcName}, &resourcev1.DeviceClass{}); !apierrors.IsNotFound(err) {
		t.Fatalf("DRA 관리중인데 DeviceClass %q 이 생성됨(err=%v)", dcName, err)
	}
}

// 이미지가 비면 그 backend 는 적용하지 않고 refusal 만 남긴다(빈 이미지 DaemonSet 금지).
func TestAutoApplyRefusesWhenImageUnset(t *testing.T) {
	ctx := context.Background()
	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-noimg-tt"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "tenstorrent", Product: "tt-noimg", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/tenstorrent/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
		},
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	// 이미지 미설정 reconciler(플래그 기본값 = 빈 문자열 상태를 재현).
	r := &AcceleratorDescriptorReconciler{Client: aaClient, Scheme: aaClient.Scheme(), GeneratedNamespace: "default"}
	reconcileOnce(t, r, desc.Name)

	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if !hasRefusalContaining(got.Status.Refusals, "이미지 미설정") {
		t.Fatalf("이미지 미설정 거절 사유가 status 에 없다: %+v", got.Status.Refusals)
	}
	name := generatedDevicePluginNameFor(desc)
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Fatalf("이미지 미설정인데 ConfigMap %q 이 생성됨(err=%v)", name, err)
	}
}

// sharing 이 선언됐어도 레지스트리가 그 벤더/제품의 timeSlicing 을 Software 로 선언하지 않으면
// auto-apply 를 거절해야 한다 — 동시 접근 안전 실측 없이 SW 시분할을 켜면 격리 없는 공유가
// 조용히 나간다(RNGD 는 배타적 open 이라 Unsupported). vendor 는 "rngd" 다(furiosa 가 아니다) —
// descriptor 어휘는 RNGD 를 vendor="rngd" 로 부르지만 registry 키는 furiosa 아래 product="rngd"
// 라, descriptorRegistryKey 매핑을 거쳐야 프로덕션과 같은 조합이 된다. vendor="furiosa" 로 두면
// 우연히 같은 registry 키에 맞아 이 매핑 결함을 놓친다.
func TestAutoApplyRefusesSharingWhenVendorNotSoftwareTimeSlicing(t *testing.T) {
	ctx := context.Background()
	desc := &npuv1alpha1.AcceleratorDescriptor{ObjectMeta: metav1.ObjectMeta{Name: "aa-share-rngd"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "rngd", Product: "rngd", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/rngd/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			Sharing:     &npuv1alpha1.DescriptorSharing{Mode: npuv1alpha1.SharingModeTimeSliced, Replicas: 2},
		}}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })
	reconcileOnce(t, autoApplyReconciler(), desc.Name)
	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if !hasRefusalContaining(got.Status.Refusals, "timeSlicing") {
		t.Fatalf("rngd 는 timeSlicing Unsupported 라 sharing 거절돼야 한다: %+v", got.Status.Refusals)
	}
}

// registry Lookup 이 (vendor, product) 를 못 찾으면(ok=false) 폴백이 돌고, 그 결과가 Software 가
// 아니면 sharing 은 거절돼야 한다 — Lookup ok=false 일 때 폴백·거절 경로가 실제로 도는지 이
// 시험이 못박는다(조용히 통과하는 회귀 차단). 레지스트리에 아예 없는 벤더(acme)를 써서
// product·vendor "" 두 Lookup 이 모두 ok=false 가 되게 한다 — 그러면 sharingSupport 가 합성
// Unknown("vendor/product 키 없음")을 돌려주는 경로로 거절을 확인한다.
func TestAutoApplyRefusesSharingWhenVendorUnknownTimeSlicing(t *testing.T) {
	ctx := context.Background()
	desc := &npuv1alpha1.AcceleratorDescriptor{ObjectMeta: metav1.ObjectMeta{Name: "aa-share-unknown-vendor"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "acme", Product: "x", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/acme/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			Sharing:     &npuv1alpha1.DescriptorSharing{Mode: npuv1alpha1.SharingModeTimeSliced, Replicas: 2},
		}}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })
	reconcileOnce(t, autoApplyReconciler(), desc.Name)
	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if !hasRefusalContaining(got.Status.Refusals, "timeSlicing") {
		t.Fatalf("레지스트리에 없는 벤더는 합성 Unknown 이라 sharing 거절돼야 한다: %+v", got.Status.Refusals)
	}
}

// nvidia 의 timeSlicing 은 레지스트리상 Software 라 sharing 게이트가 거절하면 안 되고, 이
// fixture 는 다른 게이트(관리 중·중복·이미지 미설정)에도 걸리지 않아 실제로 backend 까지
// 적용돼야 한다 — refusal 부재만으론 게이트를 실제로 "통과"했는지 증명하지 못하므로(우연히
// 다른 이유로도 refusal 이 없을 수 있다) DaemonSet 생성까지 확인한다.
func TestAutoApplyAllowsSharingWhenVendorSoftwareTimeSlicing(t *testing.T) {
	ctx := context.Background()
	desc := &npuv1alpha1.AcceleratorDescriptor{ObjectMeta: metav1.ObjectMeta{Name: "aa-share-nvidia-ok"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor: "nvidia", Product: "a30-share", AllocationUnit: "device",
			Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
			DeviceNodes: []string{"/dev/nvidia/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			Sharing:     &npuv1alpha1.DescriptorSharing{Mode: npuv1alpha1.SharingModeTimeSliced, Replicas: 2},
		}}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })
	reconcileOnce(t, autoApplyReconciler(), desc.Name)
	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if hasRefusalContaining(got.Status.Refusals, "timeSlicing") {
		t.Fatalf("nvidia 는 timeSlicing Software 라 sharing 게이트가 거절하면 안 된다: %+v", got.Status.Refusals)
	}
	name := generatedDevicePluginNameFor(desc)
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &appsv1.DaemonSet{}); err != nil {
		t.Fatalf("sharing 게이트를 통과했으면 DaemonSet %q 이 실제로 적용돼야 한다: %v", name, err)
	}
	// DaemonSet 존재만으론 spec.sharing 이 실제로 컴파일에 실렸는지 못 본다 — ConfigMap 의
	// backend.json 에 replicas 가 들어갔는지까지 확인한다.
	var cm corev1.ConfigMap
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &cm); err != nil {
		t.Fatalf("생성 ConfigMap %q 이 없다: %v", name, err)
	}
	if !strings.Contains(cm.Data["backend.json"], `"replicas":2`) {
		t.Fatalf("ConfigMap backend.json 에 replicas 2 가 없다: %q", cm.Data["backend.json"])
	}
}

// sharingSupport 는 descriptor 어휘(vendor/product)를 registry 키로 매핑한 뒤 timeSlicing
// 을 읽는다. RNGD 는 descriptor vendor 가 "rngd" 인데 registry 키는 furiosa/rngd 라, 매핑
// 없이 물으면 벤더 키 자체가 없어 zero-value Support{}(State="")가 나오고, 그 값을 그대로
// 거절 사유에 실으면 "timeSlicing 이  ()" 처럼 State/Reason 이 빈 문구가 나간다. 폴백은
// 단일 제품 키(product="")까지만이다.
func TestSharingSupport(t *testing.T) {
	cases := []struct {
		name          string
		vendor        string
		product       string
		wantState     vendorcap.State
		wantReasonSub string
	}{
		{"rngd(descriptor 특례 vendor) 는 furiosa/rngd 로 매핑돼 Unsupported", "rngd", "rngd",
			vendorcap.StateUnsupported, "EBUSY"},
		// 검증 목적은 특정 State 값이 아니라 "furiosa 는 product 를 그대로 넘겨 direct Lookup
		// 이 성공한다"는 배선이다 — 값이 바뀌어도 여전히 유효한 현재 registry 값
		// (Unsupported/EBUSY, rngd 와 같은 드라이버 스택)으로 맞춘다.
		{"furiosa/warboy 는 direct Lookup 성공(현재 registry 값 Unsupported/EBUSY)", "furiosa", "warboy",
			vendorcap.StateUnsupported, "EBUSY"},
		// furiosa 는 registry 에 warboy·rngd 두 제품만 있고 product="" 키가 없다. 모르는
		// 제품(대문자 "RNGD" 는 descriptorRegistryKey 가 소문자 특례로 잡지 않는다)을 주면
		// 아무 제품의 판정도 상속하지 않고 Unknown 이어야 한다.
		{"furiosa + 모르는 제품은 다른 제품 판정을 상속하지 않고 Unknown", "furiosa", "RNGD",
			vendorcap.StateUnknown, "vendor/product 키 없음"},
		{"nvidia 는 단일 제품 키(\"\")로 폴백해 Software", "nvidia", "a30", vendorcap.StateSoftware, ""},
		// tenstorrent 는 registry 키가 tenstorrent/""(product 미분기) 뿐이라 descriptor
		// product(blackhole-p150)로 direct Lookup 이 안 되고 단일 제품 키로 폴백한다 —
		// 그 폴백값은 co-run 직렬화로 Unsupported 다.
		{"tenstorrent 는 단일 제품 키 폴백 — co-run 직렬화로 Unsupported", "tenstorrent", "blackhole-p150",
			vendorcap.StateUnsupported, "직렬화"},
		{"모르는 벤더는 Unknown + 사유 합성", "acme", "x", vendorcap.StateUnknown, "vendor/product 키 없음"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := sharingSupport(c.vendor, c.product)
			if s.State != c.wantState {
				t.Fatalf("State = %q, want %q (s=%+v)", s.State, c.wantState, s)
			}
			if c.wantReasonSub != "" && !strings.Contains(s.Reason, c.wantReasonSub) {
				t.Fatalf("Reason = %q, want substring %q", s.Reason, c.wantReasonSub)
			}
		})
	}
}

// discoverableForAutoApply 의 {{id}} 성분 규칙(다중 자리표시자 거절 포함).
func TestDiscoverableForAutoApply(t *testing.T) {
	cases := []struct {
		name    string
		nodes   []string
		wantErr bool
	}{
		{"치환자 없음(단일 고정)", []string{"/dev/tenstorrent0"}, false},
		{"깨끗한 단일 성분", []string{"/dev/rngd/{{id}}"}, false},
		{"뒤에 슬래시 성분", []string{"/dev/rngd/{{id}}/ctl"}, false},
		{"prefix 슬래시 안끝남", []string{"/dev/nvidia{{id}}"}, true},
		{"suffix 성분 아님(glob)", []string{"/dev/rngd/{{id}}*"}, true},
		{"다중 치환자", []string{"/dev/{{id}}/x{{id}}"}, true},
	}
	for _, c := range cases {
		spec := npuv1alpha1.AcceleratorDescriptorSpec{DeviceNodes: c.nodes}
		err := discoverableForAutoApply(spec)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: discoverableForAutoApply(%v) err=%v, wantErr=%v", c.name, c.nodes, err, c.wantErr)
		}
	}
}

// ndrWith 는 노드 하나의 NodeDeviceReport 를 vendor/model 쌍 목록으로 만든다.
func ndrWith(node string, vendorModel ...[2]string) npuv1alpha1.NodeDeviceReport {
	devs := make([]npuv1alpha1.DeviceEntry, 0, len(vendorModel))
	for _, vm := range vendorModel {
		devs = append(devs, npuv1alpha1.DeviceEntry{Vendor: vm[0], Model: vm[1], Count: 1})
	}
	return npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: node},
		Status:     npuv1alpha1.NodeDeviceReportStatus{Devices: devs},
	}
}

// mixedModelRefusal 은 같은 벤더의 다른 모델이 한 노드에 있고 템플릿이 {{id}} 로 열거할
// 때만 거절해야 한다 — {{id}} 열거는 파일명 글롭뿐이라 제품을 가르지 못한다.
func TestMixedModelRefusal(t *testing.T) {
	nvidiaMixed := ndrWith("k8s-worker1",
		[2]string{"nvidia", "a30"}, [2]string{"nvidia", "a2"}, [2]string{"furiosa", "warboy"})
	cases := []struct {
		name       string
		vendor     string
		product    string
		nodes      []string
		ndrs       []npuv1alpha1.NodeDeviceReport
		wantRefuse bool
		wantSub    string
	}{
		{
			name: "혼재 노드 + 열거 템플릿 → 거절", vendor: "nvidia", product: "a30",
			nodes: []string{"/dev/nvidia/{{id}}"}, ndrs: []npuv1alpha1.NodeDeviceReport{nvidiaMixed},
			wantRefuse: true, wantSub: "a2",
		},
		{
			name: "혼재 노드지만 고정 장치 → 거절 없음", vendor: "nvidia", product: "a30",
			nodes: []string{"/dev/nvidia0", "/dev/nvidiactl"},
			ndrs:  []npuv1alpha1.NodeDeviceReport{nvidiaMixed},
		},
		{
			name: "같은 모델 여러 장 → 거절 없음", vendor: "rebellions", product: "atom",
			nodes: []string{"/dev/rbln/{{id}}"},
			ndrs: []npuv1alpha1.NodeDeviceReport{ndrWith("node5",
				[2]string{"rebellions", "atom"}, [2]string{"rebellions", "atom"})},
		},
		{
			name: "다른 벤더만 섞임 → 거절 없음", vendor: "tenstorrent", product: "blackhole-p150",
			nodes: []string{"/dev/tenstorrent/{{id}}"},
			ndrs: []npuv1alpha1.NodeDeviceReport{ndrWith("k8s-worker3",
				[2]string{"tenstorrent", "blackhole-p150"}, [2]string{"nvidia", "a2"})},
		},
		{
			// 물리 벤더(d.Vendor)로 모으므로 normalizeVendor 의 rngd 특례가 거절을 가리지
			// 않는다 — 두 제품 모두 furiosa 카드라 /dev/furiosa/{{id}} 글롭이 제품을 가르지
			// 못한다. 출하 archetype 은 다른 경로(/dev/rngd/{{id}})라 피해가 없지만,
			// 판정은 경로 모양이 아니라 같은 벤더 다른 제품 여부여야 한다.
			name: "furiosa warboy + rngd → 거절", vendor: "furiosa", product: "warboy",
			nodes: []string{"/dev/furiosa/{{id}}"},
			ndrs: []npuv1alpha1.NodeDeviceReport{ndrWith("rngd-1",
				[2]string{"furiosa", "warboy"}, [2]string{"furiosa", "rngd"})},
			wantRefuse: true, wantSub: "rngd",
		},
		{
			// 반대 방향: descriptor 가 rngd(벤더 키가 rngd)여도 물리 벤더는 furiosa 라
			// 같은 노드의 warboy 를 다른 제품으로 센다.
			name: "rngd descriptor + warboy 혼재 → 거절", vendor: "rngd", product: "rngd",
			nodes: []string{"/dev/rngd/{{id}}"},
			ndrs: []npuv1alpha1.NodeDeviceReport{ndrWith("rngd-1",
				[2]string{"furiosa", "rngd"}, [2]string{"furiosa", "warboy"})},
			wantRefuse: true, wantSub: "warboy",
		},
		{
			// 모델 미판정 카드(빈 Model)도 거절은 유지한다 — 글롭이 그 장치를 descriptor
			// 제품으로 광고하기 때문이다. 사유 문자열에서만 사람이 읽을 수 있게 바꾼다.
			name: "빈 모델 카드 → 거절, 사유에 모델 미보고", vendor: "nvidia", product: "a30",
			nodes: []string{"/dev/nvidia/{{id}}"},
			ndrs: []npuv1alpha1.NodeDeviceReport{ndrWith("k8s-worker1",
				[2]string{"nvidia", "a30"}, [2]string{"nvidia", ""})},
			wantRefuse: true, wantSub: "(모델 미보고)",
		},
		{
			name: "제품이 그 노드에 없음 → 거절 없음", vendor: "nvidia", product: "h100",
			nodes: []string{"/dev/nvidia/{{id}}"}, ndrs: []npuv1alpha1.NodeDeviceReport{nvidiaMixed},
		},
		{
			name: "NDR 없음 → 거절 없음", vendor: "nvidia", product: "a30",
			nodes: []string{"/dev/nvidia/{{id}}"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := npuv1alpha1.AcceleratorDescriptorSpec{
				Vendor: c.vendor, Product: c.product, DeviceNodes: c.nodes,
			}
			got := mixedModelRefusal(spec, c.ndrs)
			if (got != "") != c.wantRefuse {
				t.Fatalf("mixedModelRefusal = %q, wantRefuse=%v", got, c.wantRefuse)
			}
			if c.wantSub != "" && !strings.Contains(got, c.wantSub) {
				t.Fatalf("사유 = %q, want substring %q", got, c.wantSub)
			}
		})
	}
}

// TestMixedModelRefusalIsDeterministic 은 노드 둘이 모두 혼재일 때 사유가 입력 순서로 갈리지
// 않는지 본다 — 사유가 reconcile 마다 바뀌면 status.refusals 가 떨리고 apiserver 가 같은
// reason+message 로 접는 이벤트도 매번 새 항목이 된다.
func TestMixedModelRefusalIsDeterministic(t *testing.T) {
	spec := npuv1alpha1.AcceleratorDescriptorSpec{
		Vendor: "tenstorrent", Product: "blackhole-p150",
		DeviceNodes: []string{"/dev/tenstorrent/{{id}}"},
	}
	// 노드 둘이 모두 혼재면 사유는 노드 이름 사전순 먼저인 쪽을 가리켜야 한다.
	a := ndrWith("tt-a", [2]string{"tenstorrent", "blackhole-p150"}, [2]string{"tenstorrent", "wormhole-n300"})
	b := ndrWith("tt-b", [2]string{"tenstorrent", "blackhole-p150"}, [2]string{"tenstorrent", "grayskull-e150"})
	first := mixedModelRefusal(spec, []npuv1alpha1.NodeDeviceReport{a, b})
	second := mixedModelRefusal(spec, []npuv1alpha1.NodeDeviceReport{b, a})
	if first != second {
		t.Fatalf("입력 순서로 사유가 갈린다:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, "tt-a") {
		t.Fatalf("사전순 먼저인 노드를 가리켜야 한다: %q", first)
	}
}

// 혼재 모델 노드의 descriptor 는 Reconcile 이 거절하고 생성물을 만들지 않아야 한다 —
// 순수 판정(mixedModelRefusal)이 호출부에 실제로 걸려 있는지를 본다.
func TestAutoApplyRefusesMixedModelNode(t *testing.T) {
	ctx := context.Background()
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-mixed-node"},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "aa-mixed-node"},
	}
	if err := aaClient.Create(ctx, ndr); err != nil {
		t.Fatalf("NDR 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, ndr) })
	ndr.Status.Devices = []npuv1alpha1.DeviceEntry{
		{Vendor: "tenstorrent", Model: "mixtest", Count: 1, PCIeAddress: "0000:af:00.0"},
		{Vendor: "tenstorrent", Model: "othermodel", Count: 1, PCIeAddress: "0000:b0:00.0"},
	}
	if err := aaClient.Status().Update(ctx, ndr); err != nil {
		t.Fatalf("NDR status 갱신 실패: %v", err)
	}

	spec := uniqSpec()
	spec.Product = "mixtest"
	desc := &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-mixed"}, Spec: spec,
	}
	if err := aaClient.Create(ctx, desc); err != nil {
		t.Fatalf("descriptor 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, desc) })

	reconcileOnce(t, autoApplyReconciler(), desc.Name)

	var got npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: desc.Name}, &got); err != nil {
		t.Fatalf("descriptor 조회 실패: %v", err)
	}
	if !hasRefusalContaining(got.Status.Refusals, "othermodel") {
		t.Fatalf("혼재 모델 거절 사유가 없다: %+v", got.Status.Refusals)
	}
	err := aaClient.Get(ctx,
		types.NamespacedName{Namespace: "default", Name: generatedDevicePluginNameFor(desc)},
		&appsv1.DaemonSet{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("거절된 descriptor 의 DaemonSet 이 있다(err=%v)", err)
	}
}

// uniqSpec 는 같은 vendor/product(중복 판정 대상)를 만드는 헬퍼다. 다른 테스트와 키가
// 겹치지 않도록 product 를 uniqtest 로 둔다.
func uniqSpec() npuv1alpha1.AcceleratorDescriptorSpec {
	return npuv1alpha1.AcceleratorDescriptorSpec{
		Vendor: "tenstorrent", Product: "uniqtest", AllocationUnit: "device",
		Identity:    npuv1alpha1.DescriptorIdentity{Source: "pciAddress", StableAcrossReboot: true},
		DeviceNodes: []string{"/dev/tenstorrent/{{id}}"},
		Backends:    []string{npuv1alpha1.BackendDevicePlugin},
	}
}

// 같은 vendor/product 를 선언한 descriptor 둘이면 이름 사전순 먼저(a)만 적용하고
// 나중(b)은 거절해야 한다 — 같은 하드웨어 이중 광고 방지.
func TestAutoApplyUniquenessRefusesDuplicate(t *testing.T) {
	ctx := context.Background()
	a := &npuv1alpha1.AcceleratorDescriptor{ObjectMeta: metav1.ObjectMeta{Name: "aa-uniq-a"}, Spec: uniqSpec()}
	b := &npuv1alpha1.AcceleratorDescriptor{ObjectMeta: metav1.ObjectMeta{Name: "aa-uniq-b"}, Spec: uniqSpec()}
	if err := aaClient.Create(ctx, a); err != nil {
		t.Fatalf("a 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, a) })
	if err := aaClient.Create(ctx, b); err != nil {
		t.Fatalf("b 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, b) })

	reconcileOnce(t, autoApplyReconciler(), a.Name)
	reconcileOnce(t, autoApplyReconciler(), b.Name)

	// winner(a) 적용됨.
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: generatedDevicePluginNameFor(a)}, &appsv1.DaemonSet{}); err != nil {
		t.Fatalf("winner(a) DaemonSet 이 없다: %v", err)
	}
	// loser(b) 거절 + 생성물 없음.
	var bd npuv1alpha1.AcceleratorDescriptor
	if err := aaClient.Get(ctx, types.NamespacedName{Name: b.Name}, &bd); err != nil {
		t.Fatalf("b 조회 실패: %v", err)
	}
	if !hasRefusalContaining(bd.Status.Refusals, "이미 auto-apply") {
		t.Fatalf("loser(b) 중복 거절 사유가 없다: %+v", bd.Status.Refusals)
	}
	err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: generatedDevicePluginNameFor(b)}, &appsv1.DaemonSet{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("loser(b) DaemonSet 이 있으면 안 된다(err=%v)", err)
	}
}

// winner 가 삭제되면 loser 가 승격해 적용돼야 한다(sibling watch 로 재평가되는 로직).
func TestAutoApplyUniquenessPromotesOnWinnerDelete(t *testing.T) {
	ctx := context.Background()
	a := &npuv1alpha1.AcceleratorDescriptor{ObjectMeta: metav1.ObjectMeta{Name: "aa-promo-a"}, Spec: uniqSpec()}
	b := &npuv1alpha1.AcceleratorDescriptor{ObjectMeta: metav1.ObjectMeta{Name: "aa-promo-b"}, Spec: uniqSpec()}
	if err := aaClient.Create(ctx, a); err != nil {
		t.Fatalf("a 생성 실패: %v", err)
	}
	if err := aaClient.Create(ctx, b); err != nil {
		t.Fatalf("b 생성 실패: %v", err)
	}
	t.Cleanup(func() { _ = aaClient.Delete(ctx, b) })
	reconcileOnce(t, autoApplyReconciler(), a.Name)
	reconcileOnce(t, autoApplyReconciler(), b.Name)

	// 초기: b 는 loser 라 미적용.
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: generatedDevicePluginNameFor(b)}, &appsv1.DaemonSet{}); !apierrors.IsNotFound(err) {
		t.Fatalf("초기 b(loser) DaemonSet 이 있으면 안 된다(err=%v)", err)
	}
	// winner(a) 삭제 → b 재reconcile → b 승격 적용.
	if err := aaClient.Delete(ctx, a); err != nil {
		t.Fatalf("a 삭제 실패: %v", err)
	}
	reconcileOnce(t, autoApplyReconciler(), b.Name)
	if err := aaClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: generatedDevicePluginNameFor(b)}, &appsv1.DaemonSet{}); err != nil {
		t.Fatalf("winner 삭제 후 b 가 승격 적용되지 않았다: %v", err)
	}
}

// external 벤더는 device-plugin/DRA 를 배포하지 않아도 물리 /dev 를 관측하므로,
// descriptor 중재는 관리 중으로 봐 같은 장치의 이중 광고를 거절해야 한다(Enabled=false 여도).
func TestDescriptorVendorManagedExternal(t *testing.T) {
	policies := []npuv1alpha1.NPUClusterPolicy{{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled:        false,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "external"},
			},
		},
	}}
	spec := npuv1alpha1.AcceleratorDescriptorSpec{Vendor: "nvidia"}
	if !descriptorVendorManaged(spec, policies) {
		t.Fatalf("external nvidia 는 descriptor 중재에서 관리 중으로 판정돼야 한다")
	}
}

// rngd 는 RngdSpec 에 관리 모드가 없어 상위 furiosa 의 모드를 따른다. furiosa 가 external 이면
// (Enabled=false·Rngd.Enabled=false 여도) 물리 /dev 를 관측하므로 rngd descriptor 도 관리 중이다.
func TestDescriptorVendorManagedExternalRngd(t *testing.T) {
	policies := []npuv1alpha1.NPUClusterPolicy{{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Enabled:        false,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "external"},
				Rngd:           npuv1alpha1.RngdSpec{Enabled: false},
			},
		},
	}}
	spec := npuv1alpha1.AcceleratorDescriptorSpec{Vendor: "rngd"}
	if !descriptorVendorManaged(spec, policies) {
		t.Fatalf("external furiosa 의 rngd descriptor 는 관리 중으로 판정돼야 한다")
	}
}

// delegated 벤더도 external 과 같은 이유로 관리 중이어야 한다 — 벤더 operator 에 위임했을
// 뿐 같은 물리 /dev 를 관측하므로, Enabled=false 여도 별도 descriptor 의 이중 광고를 거절해야
// 한다. 이 시험은 delegated 전용 OR 항이 없던 회귀를 잡는다.
func TestDescriptorVendorManagedDelegated(t *testing.T) {
	policies := []npuv1alpha1.NPUClusterPolicy{{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled:        false,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
			},
		},
	}}
	spec := npuv1alpha1.AcceleratorDescriptorSpec{Vendor: "nvidia"}
	if !descriptorVendorManaged(spec, policies) {
		t.Fatalf("delegated nvidia 는 descriptor 중재에서 관리 중으로 판정돼야 한다")
	}
}

// rngd 는 RngdSpec 에 관리 모드가 없어 상위 furiosa 의 모드를 따른다. furiosa 가 delegated 면
// (Enabled=false·Rngd.Enabled=false 여도) 물리 /dev 를 관측하므로 rngd descriptor 도 관리 중이다.
func TestDescriptorVendorManagedDelegatedRngd(t *testing.T) {
	policies := []npuv1alpha1.NPUClusterPolicy{{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Enabled:        false,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				Rngd:           npuv1alpha1.RngdSpec{Enabled: false},
			},
		},
	}}
	spec := npuv1alpha1.AcceleratorDescriptorSpec{Vendor: "rngd"}
	if !descriptorVendorManaged(spec, policies) {
		t.Fatalf("delegated furiosa 의 rngd descriptor 는 관리 중으로 판정돼야 한다")
	}
}

// 출하 archetype 은 auto-apply 발견 규칙을 통과해야 한다 — 통과하지 못하는 형태를 예시로
// 두면 그 파일을 복사한 사용자가 거절 사유부터 만나게 된다.
func TestShippedArchetypesPassDiscovery(t *testing.T) {
	dir := filepath.Join("..", "descriptor", "testdata", "archetypes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var checked int
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		checked++
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			var spec npuv1alpha1.AcceleratorDescriptorSpec
			if err := yaml.Unmarshal(raw, &spec); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if err := discoverableForAutoApply(spec); err != nil {
				t.Fatalf("발견 규칙 위반: %v", err)
			}
		})
	}
	// 픽스처 디렉터리가 비거나 .yaml 파일이 사라지면 0회 순회로 조용히 통과한다 — 표본이
	// 있었는지 먼저 단언한다.
	if checked == 0 {
		t.Fatal("archetypes 디렉터리에 .yaml 파일이 없어 발견 규칙을 한 번도 검사하지 않았다")
	}
}
