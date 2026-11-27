// ============================================================
// dra_driver_test.go: DRA 드라이버 렌더러와 배포 게이트 시험
// 상세: 벤더 chart 를 그대로 쓰지 않고 우리 라벨·이미지로 옮기는 것이 렌더러의
// 존재 이유다. 그 치환이 실제로 일어나는지를 고정한다.
// 생성일: 2026-08-07 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func furiosaDRAPolicy() *npuv1alpha1.NPUClusterPolicy {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Furiosa.Enabled = true
	p.Spec.Furiosa.Rngd.Enabled = true
	p.Spec.Furiosa.Rngd.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	return p
}

func findDS(objs []client.Object) *appsv1.DaemonSet {
	for _, o := range objs {
		if ds, ok := o.(*appsv1.DaemonSet); ok {
			return ds
		}
	}
	return nil
}

// 벤더 chart 의 affinity 는 NFD 라벨과 furiosa-feature-discovery 라벨을 요구한다.
// 우리 클러스터에는 셋 다 없어서 그대로 쓰면 DaemonSet 이 어느 노드에도 안 뜬다.
// 렌더러는 그것을 우리 자립 라벨로 바꿔야 한다.
func TestRenderFuriosaDRADriver_UsesOurNodeLabel(t *testing.T) {
	objs := renderFuriosaDRADriver(furiosaDRAPolicy())
	ds := findDS(objs)
	if ds == nil {
		t.Fatal("DaemonSet 이 렌더되지 않았다")
	}
	sel := ds.Spec.Template.Spec.NodeSelector
	if sel["kcloud.ai/rngd.present"] != labelValueTrue {
		t.Errorf("nodeSelector=%v — 우리 자립 라벨이 없다", sel)
	}
	// 벤더 affinity(NFD 라벨 요구)는 사라져야 한다. Affinity == nil 로는 더 못 잰다 — 이
	// 렌더가 스스로 retired 배제 조건 둘(furiosa·rngd)을 붙이므로 항상 non-nil 이다.
	// 남은 match expression 이 그 둘 뿐인지로 벤더 affinity 부재를 확인한다.
	if !dsExcludesDRARetired(ds, vendorFuriosa) || !dsExcludesDRARetired(ds, vendorRngd) {
		t.Fatal("retired 배제 조건(furiosa·rngd)이 없다")
	}
	if n := countMatchExpressions(ds); n != 2 {
		t.Errorf("match expression %d개(want 2) — 벤더 affinity(NFD 등)가 남아 있는 것으로 보인다", n)
	}
}

// 이미지는 spec 이 주면 그것, 아니면 렌더러 기본값이다. 사설 미러 경로는 차트가
// spec 에 넣는다 — 이 저장소는 레지스트리를 CR 이 아니라 helm 에서 조립한다.
func TestRenderFuriosaDRADriver_DefaultImage(t *testing.T) {
	ds := findDS(renderFuriosaDRADriver(furiosaDRAPolicy()))
	img := ds.Spec.Template.Spec.Containers[0].Image
	if !strings.Contains(img, "furiosa-dra-driver:2026.1.1") {
		t.Errorf("image=%q — 기본 이미지가 아니다", img)
	}
}

// 이 이미지에는 ENTRYPOINT 가 없다. command 를 주지 않으면 args 가 CMD 를 밀어내
// 바이너리가 사라지고 컨테이너가 exec 실패로 죽는다.
func TestRenderFuriosaDRADriver_SetsCommand(t *testing.T) {
	ds := findDS(renderFuriosaDRADriver(furiosaDRAPolicy()))
	c := ds.Spec.Template.Spec.Containers[0]
	if len(c.Command) == 0 {
		t.Fatal("command 가 비어 있다 — 이미지에 ENTRYPOINT 가 없어 args 만으로는 못 뜬다")
	}
	if !strings.Contains(c.Command[0], "dra-kubelet-plugin") {
		t.Errorf("command=%v — 드라이버 바이너리가 아니다", c.Command)
	}
}

// 1.34 세대 권한이다. 빠지면 드라이버가 조용히 실패한다.
func TestRenderFuriosaDRADriver_HasAssociatedNodeVerb(t *testing.T) {
	var found bool
	for _, o := range renderFuriosaDRADriver(furiosaDRAPolicy()) {
		cr, ok := o.(*rbacv1.ClusterRole)
		if !ok {
			continue
		}
		for _, r := range cr.Rules {
			for _, res := range r.Resources {
				if res != "resourceclaims/driver" {
					continue
				}
				for _, v := range r.Verbs {
					if v == "associated-node:update" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Error("resourceclaims/driver 의 associated-node:update 권한이 없다")
	}
}

// CDI 는 containerd 기본 비활성이다. init container 가 켜지 않으면 장치 주입이 안 된다.
func TestRenderFuriosaDRADriver_HasCDIInitContainer(t *testing.T) {
	ds := findDS(renderFuriosaDRADriver(furiosaDRAPolicy()))
	if len(ds.Spec.Template.Spec.InitContainers) == 0 {
		t.Fatal("init container 가 없다 — containerd CDI 를 켤 주체가 없다")
	}
	init := ds.Spec.Template.Spec.InitContainers[0]
	joined := strings.Join(init.Command, " ") + " " + strings.Join(init.Args, " ")
	if !strings.Contains(joined, "enable_cdi") {
		t.Errorf("init container 가 enable_cdi 를 다루지 않는다: %q", joined)
	}
	if !ds.Spec.Template.Spec.HostPID {
		t.Error("hostPID 가 없다 — nsenter -t 1 로 호스트 containerd 를 재시작할 수 없다")
	}
}

// 이미지·인자·환경변수만 덮을 수 있어야 한다.
func TestRenderFuriosaDRADriver_Overrides(t *testing.T) {
	p := furiosaDRAPolicy()
	p.Spec.Furiosa.Rngd.DRA.Image = "reg.example.com/kcloud/custom:9.9"
	p.Spec.Furiosa.Rngd.DRA.Args = []string{"--flag"}
	ds := findDS(renderFuriosaDRADriver(p))
	c := ds.Spec.Template.Spec.Containers[0]
	if c.Image != "reg.example.com/kcloud/custom:9.9" {
		t.Errorf("image=%q — 오버라이드가 무시됐다", c.Image)
	}
	if len(c.Args) != 1 || c.Args[0] != "--flag" {
		t.Errorf("args=%v — 오버라이드가 무시됐다", c.Args)
	}
}

// DeviceClass 가 없으면 사용자가 요청할 대상이 없다.
func TestRenderFuriosaDRADriver_HasDeviceClass(t *testing.T) {
	objs := renderFuriosaDRADriver(furiosaDRAPolicy())
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, o.GetObjectKind().GroupVersionKind().Kind+"/"+o.GetName())
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, "DeviceClass/npu.furiosa.ai") {
		t.Errorf("DeviceClass 가 없다: %v", names)
	}
}

// newDRAReconciler 는 resource.k8s.io 를 담은 scheme 으로 reconciler 를 만든다.
// newTestScheme 에는 그 그룹이 없어 DeviceClass·ResourceSlice 를 못 담는다.
func newDRAReconciler(objs ...client.Object) *NPUClusterPolicyReconciler {
	s := sharingScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return &NPUClusterPolicyReconciler{Client: c, Scheme: s}
}

// 켜면 배포된다.
func TestEnsureDRADriver_CreatesWhenEnabled(t *testing.T) {
	p := furiosaDRAPolicy()
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(context.Background(),
		types.NamespacedName{Name: draDSNameFuriosa, Namespace: draNamespace}, &ds); err != nil {
		t.Fatalf("DaemonSet 이 생성되지 않았다: %v", err)
	}
	var dc resourcev1.DeviceClass
	if err := r.Get(context.Background(),
		types.NamespacedName{Name: draDriverNameFuriosa}, &dc); err != nil {
		t.Fatalf("DeviceClass 가 생성되지 않았다: %v", err)
	}
}

// 끄면 회수된다. 켰다 끄는 왕복이 성립해야 정책이 되돌릴 수 있는 축이 된다.
func TestEnsureDRADriver_DeletesWhenDisabled(t *testing.T) {
	p := furiosaDRAPolicy()
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("최초 배포 실패: %v", err)
	}
	p.Spec.Furiosa.Rngd.DRA.Enabled = false
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("회수 실패: %v", err)
	}
	var ds appsv1.DaemonSet
	err := r.Get(context.Background(),
		types.NamespacedName{Name: draDSNameFuriosa, Namespace: draNamespace}, &ds)
	if !apierrors.IsNotFound(err) {
		t.Errorf("DaemonSet 이 남아 있다(err=%v)", err)
	}
}

// 렌더러가 없는 벤더는 배포하지 않고 사유를 남긴다. Tenstorrent 가 실렌더러를
// 갖게 되며(본 파일 draRenderers 참조) 네 벤더 전부 render != nil 이 됐다 — 그래서
// 이 시험은 실재 벤더가 아니라 합성 draVendor 로 reconcileOneDRADriver 의
// render==nil 분기 자체를 겨눈다. 다음에 다섯째 벤더가 추가돼도 이 시험은 깨지지 않는다.
func TestEnsureDRADriver_RefusesUnsupportedVendor(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	r := newDRAReconciler(p)
	v := draVendor{name: "no-renderer-vendor", spec: &npuv1alpha1.DRASpec{Enabled: true}}
	st, err := r.reconcileOneDRADriver(context.Background(), p, v)
	if err != nil {
		t.Fatalf("거절은 에러가 아니어야 한다: %v", err)
	}
	if st.Phase != npuv1alpha1.DRAPhaseBlocked {
		t.Errorf("phase=%q, want Blocked", st.Phase)
	}
	if st.Message == "" {
		t.Error("사유가 비었다 — 조용히 안 되는 것과 이유 있는 거절은 다르다")
	}
}

// 발행물이 없으면 Ready 로 올리지 않는다. 파드가 떴다고 DRA 를 쓸 수 있는 것이 아니다.
func TestEnsureDRADriver_NotReadyWithoutSlices(t *testing.T) {
	p := furiosaDRAPolicy()
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	for _, s := range p.Status.DRADrivers {
		if s.Vendor == vendorFuriosa && s.Phase == npuv1alpha1.DRAPhaseReady {
			t.Error("ResourceSlice 가 없는데 Ready 다")
		}
	}
}

// 발행물이 있으면 그 수만큼 노드를 센다.
func TestEnsureDRADriver_ReadyCountsPublishingNodes(t *testing.T) {
	slice := &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-1-npu"},
		Spec: resourcev1.ResourceSliceSpec{
			Driver:   draDriverNameFuriosa,
			NodeName: ptr.To("rngd-1"),
			Pool:     resourcev1.ResourcePool{Name: "rngd-1", ResourceSliceCount: 1},
		},
	}
	p := furiosaDRAPolicy()
	r := newDRAReconciler(p, slice)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	for _, s := range p.Status.DRADrivers {
		if s.Vendor != vendorFuriosa {
			continue
		}
		if s.Phase != npuv1alpha1.DRAPhaseReady {
			t.Errorf("phase=%q, want Ready", s.Phase)
		}
		if s.PublishedNodes != 1 {
			t.Errorf("publishedNodes=%d, want 1", s.PublishedNodes)
		}
	}
}

// 벤더 chart 의 namespace Role 은 pods·deployments·jobs 전권이다. 그것을 옮기면
// operator 자신이 그 전권을 들고 있어야 하고(권한 상승 방지), 장치 발행자에게
// 줄 이유도 없다. 옮기지 않았다는 사실을 고정한다.
func TestRenderFuriosaDRADriver_NoWildcardNamespaceRole(t *testing.T) {
	for _, o := range renderFuriosaDRADriver(furiosaDRAPolicy()) {
		if role, ok := o.(*rbacv1.Role); ok {
			t.Fatalf("namespace Role 이 렌더됐다: %+v", role.Rules)
		}
	}
}

// NVIDIA 는 "깔되 광고 안 함" 상태가 없다. chart 가 gpus.enabled=false 면 DaemonSet 도
// DeviceClass 도 내지 않고 RBAC 껍데기만 남는다. 그 상태로 깔면 사용자는 깔렸다고
// 믿는데 아무것도 발행되지 않는다.
func TestNvidiaDRABlockReason_RequiresDRAAdvertise(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Nvidia.Enabled = true
	p.Spec.Nvidia.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	p.Spec.Nvidia.AdvertiseBy = npuv1alpha1.AdvertiseByDevicePlugin

	reason := nvidiaDRABlockReason(p)
	if reason == "" {
		t.Fatal("device-plugin 광고 중인데 배포를 허용했다")
	}
	if !strings.Contains(reason, "advertiseBy") {
		t.Errorf("사유가 축을 지목하지 않는다: %q", reason)
	}
}

// dra 로 넘긴 상태면 배포해도 된다.
func TestNvidiaDRABlockReason_AllowsWhenDRAAdvertise(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Nvidia.Enabled = true
	p.Spec.Nvidia.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	p.Spec.Nvidia.AdvertiseBy = npuv1alpha1.AdvertiseByDRA

	if reason := nvidiaDRABlockReason(p); reason != "" {
		t.Errorf("허용해야 하는데 거절했다: %q", reason)
	}
}

// chart 값은 DaemonSet 하나에 하나다. 노드 일부만 넘기는 구성은 표현할 수 없으므로
// 조용히 반쪽 상태가 되게 두지 않고 거절한다.
func TestNvidiaDRABlockReason_RefusesPartialSwitch(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Nvidia.Enabled = true
	p.Spec.Nvidia.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	p.Spec.Nvidia.AdvertiseBy = npuv1alpha1.AdvertiseByDRA
	p.Spec.Nvidia.AdvertiseByNodeSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"kubernetes.io/hostname": "k8s-worker1"},
	}

	reason := nvidiaDRABlockReason(p)
	if reason == "" {
		t.Fatal("부분 전환을 허용했다 — chart 값이 노드 단위가 아니라 표현할 수 없다")
	}
	if !strings.Contains(reason, "전부") {
		t.Errorf("사유가 전부-아니면-전무 제약을 설명하지 않는다: %q", reason)
	}
}

// Furiosa 는 이 제약이 없다. NVIDIA 게이트가 다른 벤더에 새면 안 된다.
func TestNvidiaDRABlockReason_IgnoresOtherVendors(t *testing.T) {
	p := furiosaDRAPolicy()
	p.Spec.Furiosa.Rngd.AdvertiseBy = npuv1alpha1.AdvertiseByDevicePlugin
	if reason := nvidiaDRABlockReason(p); reason != "" {
		t.Errorf("NVIDIA 축이 아닌데 사유를 냈다: %q", reason)
	}
}

func nvidiaDRAPolicy() *npuv1alpha1.NPUClusterPolicy {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Nvidia.Enabled = true
	p.Spec.Nvidia.AdvertiseBy = npuv1alpha1.AdvertiseByDRA
	p.Spec.Nvidia.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	return p
}

// chart affinity 는 NFD 4종과 외부 operator 라벨 1종의 OR 이다. 우리는 둘 다 안 쓴다.
func TestRenderNvidiaDRADriver_UsesOurNodeLabel(t *testing.T) {
	ds := findDS(renderNvidiaDRADriver(nvidiaDRAPolicy()))
	if ds == nil {
		t.Fatal("DaemonSet 이 렌더되지 않았다")
	}
	if ds.Spec.Template.Spec.NodeSelector["kcloud.ai/nvidia.present"] != labelValueTrue {
		t.Errorf("nodeSelector=%v — 우리 자립 라벨이 없다", ds.Spec.Template.Spec.NodeSelector)
	}
	// TestRenderFuriosaDRADriver_UsesOurNodeLabel 과 같은 이유로 Affinity == nil 대신
	// match expression 개수로 벤더 affinity(NFD·외부 operator 라벨) 부재를 확인한다.
	if !dsExcludesDRARetired(ds, vendorNvidia) {
		t.Fatal("retired 배제 조건이 없다")
	}
	if n := countMatchExpressions(ds); n != 1 {
		t.Errorf("match expression %d개(want 1) — 벤더 affinity(NFD·외부 operator 라벨 등)가 남아 있는 것으로 보인다", n)
	}
}

// CDI init container 가 벤더 prestart 보다 먼저 와야 한다. 순서가 뒤집히면
// prestart 가 CDI 없는 상태에서 돈다.
func TestRenderNvidiaDRADriver_CDIInitRunsFirst(t *testing.T) {
	ds := findDS(renderNvidiaDRADriver(nvidiaDRAPolicy()))
	inits := ds.Spec.Template.Spec.InitContainers
	if len(inits) < 2 {
		t.Fatalf("init container 가 %d 개 — CDI 와 벤더 prestart 둘이어야 한다", len(inits))
	}
	if inits[0].Name != "enable-cdi" {
		t.Errorf("첫 init container=%q — CDI 활성화가 먼저여야 한다", inits[0].Name)
	}
}

// compute-domain 은 다중 노드 NVLink 용이다. GPU 노드가 하나뿐이라 뺀다.
// chart 는 값을 꺼도 관련 오브젝트를 남기므로 렌더러가 걸러야 한다.
func TestRenderNvidiaDRADriver_ExcludesComputeDomain(t *testing.T) {
	for _, o := range renderNvidiaDRADriver(nvidiaDRAPolicy()) {
		if strings.Contains(o.GetName(), "compute-domain") {
			t.Errorf("compute-domain 오브젝트가 남았다: %s", o.GetName())
		}
		cr, ok := o.(*rbacv1.ClusterRole)
		if !ok {
			continue
		}
		for _, r := range cr.Rules {
			for _, g := range r.APIGroups {
				if g == "resource.nvidia.com" {
					t.Errorf("다중 노드 NVLink 전용 API 그룹 권한이 남았다: %+v", r)
				}
			}
		}
	}
}

// 발행 관측이 이 driver 이름으로 ResourceSlice 를 센다. DeviceClass 셋이 다 같은
// driver 를 가리키므로 이름이 틀리면 영원히 배포됨 상태에 머문다.
func TestRenderNvidiaDRADriver_DeviceClassesPresent(t *testing.T) {
	var got []string
	for _, o := range renderNvidiaDRADriver(nvidiaDRAPolicy()) {
		if o.GetObjectKind().GroupVersionKind().Kind == "DeviceClass" {
			got = append(got, o.GetName())
		}
	}
	for _, want := range []string{"gpu.nvidia.com", "mig.nvidia.com", "vfio.gpu.nvidia.com"} {
		var found bool
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("DeviceClass %q 가 없다 (있는 것: %v)", want, got)
		}
	}
}

// 이미지는 spec 이 주면 그것, 아니면 렌더러 기본값이다. 사설 미러 경로는 차트가
// spec 에 넣는다 — 이 저장소는 레지스트리를 CR 이 아니라 helm 에서 조립한다.
func TestRenderNvidiaDRADriver_DefaultImage(t *testing.T) {
	ds := findDS(renderNvidiaDRADriver(nvidiaDRAPolicy()))
	img := ds.Spec.Template.Spec.Containers[0].Image
	if !strings.Contains(img, "dra-driver-nvidia-gpu:") {
		t.Errorf("image=%q — 기본 이미지가 아니다", img)
	}
	if ds.Spec.Template.Spec.InitContainers[1].Image != img {
		t.Errorf("prestart 이미지가 본체와 다르다: %q", ds.Spec.Template.Spec.InitContainers[1].Image)
	}
}

// 이 드라이버는 호스트 드라이버 루트를 읽고 kubelet 플러그인 디렉터리에 쓴다.
// 볼륨 하나가 빠지면 컨테이너는 뜨지만 발행이 안 된다.
func TestRenderNvidiaDRADriver_HasVendorVolumes(t *testing.T) {
	ds := findDS(renderNvidiaDRADriver(nvidiaDRAPolicy()))
	have := map[string]bool{}
	for _, v := range ds.Spec.Template.Spec.Volumes {
		have[v.Name] = true
	}
	for _, want := range []string{"plugins-registry", "plugins", "cdi", "driver-root", "driver-root-parent"} {
		if !have[want] {
			t.Errorf("볼륨 %q 가 없다 (있는 것: %v)", want, have)
		}
	}
}

// 벤더 게이트가 배포 자체를 막는지 본다. 사유만 남기고 오브젝트를 만들면 안 된다.
func TestEnsureDRADriver_NvidiaBlockedWithoutDRAAdvertise(t *testing.T) {
	p := nvidiaDRAPolicy()
	p.Spec.Nvidia.AdvertiseBy = npuv1alpha1.AdvertiseByDevicePlugin
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("거절은 에러가 아니어야 한다: %v", err)
	}
	var ds appsv1.DaemonSet
	err := r.Get(context.Background(),
		types.NamespacedName{Name: draDSNameNvidia, Namespace: draNamespace}, &ds)
	if !apierrors.IsNotFound(err) {
		t.Errorf("거절했는데 DaemonSet 이 생겼다(err=%v)", err)
	}
	for _, s := range p.Status.DRADrivers {
		if s.Vendor != vendorNvidia {
			continue
		}
		if s.Phase != npuv1alpha1.DRAPhaseBlocked {
			t.Errorf("phase=%q, want Blocked", s.Phase)
		}
	}
}

// dra 로 넘긴 상태면 실제로 배포된다.
func TestEnsureDRADriver_NvidiaDeploysWhenAllowed(t *testing.T) {
	p := nvidiaDRAPolicy()
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(context.Background(),
		types.NamespacedName{Name: draDSNameNvidia, Namespace: draNamespace}, &ds); err != nil {
		t.Fatalf("DaemonSet 이 생성되지 않았다: %v", err)
	}
}

// control-plane 노드에도 벤더 라벨이 붙어 있을 수 있다. 장치가 없는 노드에 올리면
// 벤더 prestart 가 init 에서 멈춘다.
func TestEnsureDRADriver_ExcludesControlPlane(t *testing.T) {
	p := nvidiaDRAPolicy()
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(context.Background(),
		types.NamespacedName{Name: draDSNameNvidia, Namespace: draNamespace}, &ds); err != nil {
		t.Fatalf("DaemonSet 조회 실패: %v", err)
	}
	aff := ds.Spec.Template.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil {
		t.Fatal("control-plane 배제 affinity 가 없다")
	}
	var found bool
	for _, term := range aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, req := range term.MatchExpressions {
			if req.Operator == corev1.NodeSelectorOpDoesNotExist {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("control-plane 라벨 부재 조건이 없다: %+v", aff.NodeAffinity)
	}
}

// CDI 활성화는 "파일에 썼다" 가 아니라 "containerd 가 실제로 켰다" 로 판정해야 한다. 같은
// plugin 테이블을 다시 선언하는 drop-in(예: conf.d 의 다른 파일)이 있으면 base 에 쓴 값이
// 덮여 enable_cdi 가 false 로 남을 수 있다.
func TestRenderCDIInitContainer_UsesEffectiveConfigAndDropIn(t *testing.T) {
	c := renderCDIInitContainer()
	script := strings.Join(c.Command, " ")

	for _, want := range []string{
		"containerd config dump", // 파일이 아니라 유효 설정으로 판정
		"CDI target",             // 마지막에 선언한 파일을 골라 거기에 쓴다
		"imports",                // drop-in 을 쓰는지 base 를 고치는지 가르는 조건
	} {
		if !strings.Contains(script, want) {
			t.Errorf("스크립트에 %q 가 없다", want)
		}
	}
	// 적용 확인 없이 성공으로 끝나면 장치 주입이 안 되는 채로 드라이버만 뜬다.
	if !strings.Contains(script, "CDI 적용 실패") {
		t.Error("적용 확인 실패 시 종료하는 경로가 없다")
	}
	// 같은 테이블에 키가 두 번 들어가면 containerd 가 파싱에 실패한다.
	if !strings.Contains(script, "enable_cdi[[:space:]]*=") {
		t.Error("기존 키 존재 여부를 보지 않는다 — 중복 키를 만들 수 있다")
	}
	// 새 drop-in 을 만들면 그 plugin 테이블의 다른 설정(nvidia 런타임 등록 등)이
	// 통째로 사라진다. 파일을 만들지 않고 기존 선언 파일을 고쳐야 한다.
	if strings.Contains(script, "99-zz-cdi.toml") || strings.Contains(script, "cat > $DROPIN") {
		t.Error("새 drop-in 을 만든다 — 같은 테이블의 다른 설정을 지운다")
	}
	// 실패하면 되돌려야 한다. 반쯤 고친 채로 두면 노드가 망가진 상태로 남는다.
	if !strings.Contains(script, "원복한다") {
		t.Error("적용 실패 시 원복 경로가 없다")
	}
}

// Rebellions 도 다른 벤더와 같은 DRA 토글을 갖는다. 필드가 없으면 정책으로 켤 방법이 없다.
func TestRebellionsSpec_HasDRAToggle(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Rebellions.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	if p.Spec.Rebellions.DRA == nil || !p.Spec.Rebellions.DRA.Enabled {
		t.Fatal("RebellionsSpec.DRA 를 켤 수 없다")
	}
}

func rebellionsDRAPolicy() *npuv1alpha1.NPUClusterPolicy {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Rebellions.Enabled = true
	p.Spec.Rebellions.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	return p
}

// 벤더 chart 의 nodeSelector 는 rebellions.ai/npu.present 를 요구한다. 그 라벨을 붙이는
// 주체가 우리 클러스터에 없으므로 자립 라벨로 바꿔야 어느 노드에든 뜬다.
func TestRenderRebellionsDRADriver_UsesOurNodeLabel(t *testing.T) {
	ds := findDS(renderRebellionsDRADriver(rebellionsDRAPolicy()))
	if ds == nil {
		t.Fatal("DaemonSet 이 렌더되지 않았다")
	}
	if ds.Spec.Template.Spec.NodeSelector["kcloud.ai/rebellions.present"] != labelValueTrue {
		t.Errorf("nodeSelector=%v — 자립 라벨이 없다", ds.Spec.Template.Spec.NodeSelector)
	}
}

// 이미지는 spec 이 주면 그것, 아니면 렌더러 기본값이다.
func TestRenderRebellionsDRADriver_DefaultImage(t *testing.T) {
	ds := findDS(renderRebellionsDRADriver(rebellionsDRAPolicy()))
	if img := ds.Spec.Template.Spec.Containers[0].Image; !strings.Contains(img, "k8s-dra-driver-npu") {
		t.Errorf("image=%q — 기본 이미지가 아니다", img)
	}
}

// 이 이미지의 진입점은 npu-kubelet-plugin 이다. command 를 주지 않으면 기동하지 않는다.
func TestRenderRebellionsDRADriver_SetsCommand(t *testing.T) {
	ds := findDS(renderRebellionsDRADriver(rebellionsDRAPolicy()))
	c := ds.Spec.Template.Spec.Containers[0]
	if len(c.Command) == 0 || c.Command[0] != "npu-kubelet-plugin" {
		t.Errorf("command=%v — 벤더 진입점이 아니다", c.Command)
	}
}

// DeviceClass 가 없으면 사용자가 claim 을 쓸 수 없다. 이름과 driver 표현식이 벤더 것과 같아야 한다.
func TestRenderRebellionsDRADriver_HasDeviceClass(t *testing.T) {
	var found bool
	for _, o := range renderRebellionsDRADriver(rebellionsDRAPolicy()) {
		dc, ok := o.(*resourcev1.DeviceClass)
		if !ok {
			continue
		}
		found = true
		if dc.Name != draDriverNameRebellions {
			t.Errorf("DeviceClass 이름=%q, want %q", dc.Name, draDriverNameRebellions)
		}
		if len(dc.Spec.Selectors) == 0 || dc.Spec.Selectors[0].CEL == nil ||
			!strings.Contains(dc.Spec.Selectors[0].CEL.Expression, draDriverNameRebellions) {
			t.Errorf("selector 가 driver 이름을 걸러내지 않는다: %+v", dc.Spec.Selectors)
		}
	}
	if !found {
		t.Fatal("DeviceClass 가 렌더되지 않았다")
	}
}

// draRenderers 는 네 벤더 렌더러를 표 시험으로 도는 목록이다. Rebellions 렌더러의
// 결함(host-dev 볼륨 삭제·기본 이미지 오타)이 device-plugin 전용으로만 막혀 있던
// 문제를 넷 모두로 일반화한다.
// container 는 그 벤더의 드라이버 컨테이너 이름이다. 목록 위치가 아니라 이름으로 골라야
// 한다 — 위치로 고르면 시험이 신원이 아니라 순서를 고정해서, 컨테이너를 하나 더 붙이는
// 편집이 시험을 깨고 소스를 시험에 맞춰 재정렬하게 만든다(Tenstorrent 에서 실제로 그랬다).
var draRenderers = []struct {
	name      string
	policy    func() *npuv1alpha1.NPUClusterPolicy
	render    func(*npuv1alpha1.NPUClusterPolicy) []client.Object
	image     string
	container string
}{
	{"furiosa", furiosaDRAPolicy, renderFuriosaDRADriver, draDriverImageFuriosaDefault, "plugin"},
	{"nvidia", nvidiaDRAPolicy, renderNvidiaDRADriver, draDriverImageNvidiaDefault, "gpus"},
	{"rebellions", rebellionsDRAPolicy, renderRebellionsDRADriver, draDriverImageRebellionsDefault, "plugin"},
	{"tenstorrent", tenstorrentDRAPolicy, renderTenstorrentDRADriver, draDriverImageTenstorrentDefault, "plugin"},
}

// containerByName 은 PodSpec 의 컨테이너를 이름으로 고른다. 없으면 nil 이다.
func containerByName(ds *appsv1.DaemonSet, name string) *corev1.Container {
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == name {
			return &ds.Spec.Template.Spec.Containers[i]
		}
	}
	return nil
}

// VolumeMount 가 대응 Volume 을 잃으면 파드가 뜨지 못한다. fake client 는 PodSpec 을
// 검증하지 않아 이 결함은 렌더러 시험 자체가 잡아야 한다(과제 1 의 Critical 과 같은 종류).
func TestDRARenderers_AllMountsHaveVolumes(t *testing.T) {
	for _, tc := range draRenderers {
		t.Run(tc.name, func(t *testing.T) {
			ds := findDS(tc.render(tc.policy()))
			if ds == nil {
				t.Fatal("DaemonSet 이 렌더되지 않았다")
			}
			volumes := map[string]bool{}
			for _, v := range ds.Spec.Template.Spec.Volumes {
				volumes[v.Name] = true
			}
			containers := append(
				append([]corev1.Container{}, ds.Spec.Template.Spec.InitContainers...),
				ds.Spec.Template.Spec.Containers...)
			for _, c := range containers {
				for _, m := range c.VolumeMounts {
					if !volumes[m.Name] {
						t.Errorf("container %q: volumeMount %q 에 대응 volume 이 없다", c.Name, m.Name)
					}
				}
			}
		})
	}
}

// TestDRARenderers_HaveCDIInitContainer 는 세 렌더러가 모두 CDI 활성화
// init container 와 그것이 마운트하는 볼륨을 갖는지 본다. 하나라도 빠지면
// containerd 의 enable_cdi 기본값이 false 라 장치 주입이 조용히 실패한다.
func TestDRARenderers_HaveCDIInitContainer(t *testing.T) {
	for _, tc := range draRenderers {
		t.Run(tc.name, func(t *testing.T) {
			ds := findDS(tc.render(tc.policy()))
			if ds == nil {
				t.Fatal("DaemonSet 이 렌더되지 않았다")
			}

			var found bool
			for _, c := range ds.Spec.Template.Spec.InitContainers {
				if c.Name == "enable-cdi" {
					found = true
				}
			}
			if !found {
				t.Error("enable-cdi init container 가 없다")
			}
			if !ds.Spec.Template.Spec.HostPID {
				t.Error("HostPID 가 false 다 — nsenter 가 호스트 네임스페이스에 못 든다")
			}

			var hasVol bool
			for _, v := range ds.Spec.Template.Spec.Volumes {
				if v.Name == "host-etc-containerd" {
					hasVol = true
				}
			}
			if !hasVol {
				t.Error("host-etc-containerd 볼륨이 없다")
			}
		})
	}
}

// 기본 이미지는 상수와 완전히 같아야 한다. strings.Contains 부분일치는 태그가
// 엉뚱한 값으로 바뀌어도 통과시킨다.
func TestDRARenderers_DefaultImageExact(t *testing.T) {
	for _, tc := range draRenderers {
		t.Run(tc.name, func(t *testing.T) {
			ds := findDS(tc.render(tc.policy()))
			if ds == nil {
				t.Fatal("DaemonSet 이 렌더되지 않았다")
			}
			c := containerByName(ds, tc.container)
			if c == nil {
				t.Fatalf("드라이버 컨테이너 %q 가 없다", tc.container)
			}
			if c.Image != tc.image {
				t.Errorf("image=%q, want %q", c.Image, tc.image)
			}
		})
	}
}

// objectExists 는 render() 가 돌려준 오브젝트와 같은 종류·이름·네임스페이스의
// 오브젝트가 클러스터에 있는지 본다. applyDRAObject 와 같은 방식으로 타입을 복제해
// Get 한다 — 오브젝트 종류마다 분기를 손으로 쓰지 않는다.
func objectExists(t *testing.T, r *NPUClusterPolicyReconciler, o client.Object) bool {
	t.Helper()
	cur, ok := o.DeepCopyObject().(client.Object)
	if !ok {
		t.Fatalf("오브젝트 복제 실패: %T", o)
	}
	err := r.Get(context.Background(), client.ObjectKeyFromObject(o), cur)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get %T %s: %v", o, client.ObjectKeyFromObject(o), err)
	}
	return true
}

// 왕복 시험이 DaemonSet 삭제만 보면 나머지 오브젝트(ServiceAccount·ClusterRole·
// ClusterRoleBinding·DeviceClass)가 회수되지 않아도 통과한다. render() 가 돌려준
// 목록을 그대로 대상으로 삼아 오브젝트가 추가돼도 시험이 따라간다.
func TestDRARenderers_FullTeardown(t *testing.T) {
	for _, tc := range draRenderers {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			p := tc.policy()
			r := newDRAReconciler(p)
			if err := r.ensureDRADriver(ctx, p); err != nil {
				t.Fatalf("배포 실패: %v", err)
			}
			objs := tc.render(p)
			if len(objs) == 0 {
				t.Fatal("render() 가 빈 목록을 돌려줬다 — 아래 두 반복문이 빈 동작이 된다")
			}
			for _, o := range objs {
				if !objectExists(t, r, o) {
					t.Errorf("%T %s 가 배포되지 않았다", o, client.ObjectKeyFromObject(o))
				}
			}

			var spec *npuv1alpha1.DRASpec
			for _, v := range draVendors(p) {
				if v.name == tc.name {
					spec = v.spec
				}
			}
			if spec == nil {
				t.Fatalf("draVendors 에 %q 가 없다", tc.name)
			}
			spec.Enabled = false
			if err := r.ensureDRADriver(ctx, p); err != nil {
				t.Fatalf("회수 실패: %v", err)
			}
			for _, o := range objs {
				if objectExists(t, r, o) {
					t.Errorf("%T %s 가 회수되지 않았다", o, client.ObjectKeyFromObject(o))
				}
			}
		})
	}
}

// Rebellions ClusterRole 이 resourceslices 에 create·update 를 못 가지면 발행이
// 조용히 실패한다. phase 는 CrashLoop 과 구분 없이 Installing 에 고착된다.
func TestRenderRebellionsDRADriver_ClusterRoleWritesResourceSlices(t *testing.T) {
	var haveCreate, haveUpdate bool
	for _, o := range renderRebellionsDRADriver(rebellionsDRAPolicy()) {
		cr, ok := o.(*rbacv1.ClusterRole)
		if !ok {
			continue
		}
		for _, rule := range cr.Rules {
			var forResourceSlices bool
			for _, res := range rule.Resources {
				if res == "resourceslices" {
					forResourceSlices = true
				}
			}
			if !forResourceSlices {
				continue
			}
			for _, v := range rule.Verbs {
				if v == "create" {
					haveCreate = true
				}
				if v == "update" {
					haveUpdate = true
				}
			}
		}
	}
	if !haveCreate || !haveUpdate {
		t.Errorf("resourceslices create/update 권한 없음: create=%v update=%v", haveCreate, haveUpdate)
	}
}

// 켜면 배포되고 끄면 회수된다. 왕복이 성립해야 되돌릴 수 있는 축이 된다.
func TestEnsureDRADriver_Rebellions_RoundTrip(t *testing.T) {
	p := rebellionsDRAPolicy()
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("배포 실패: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: draDSNameRebellions, Namespace: draNamespace}, &ds); err != nil {
		t.Fatalf("DaemonSet 이 생성되지 않았다: %v", err)
	}
	p.Spec.Rebellions.DRA.Enabled = false
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("회수 실패: %v", err)
	}
	err := r.Get(context.Background(), types.NamespacedName{
		Name: draDSNameRebellions, Namespace: draNamespace}, &ds)
	if !apierrors.IsNotFound(err) {
		t.Errorf("DaemonSet 이 남아 있다(err=%v)", err)
	}
}

// rebellionsResourceSlice 는 지정 노드에서 Rebellions DRA 드라이버가 낸 발행물이다.
func rebellionsResourceSlice(name, node string) *resourcev1.ResourceSlice {
	return &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.ResourceSliceSpec{
			Driver:   draDriverNameRebellions,
			NodeName: ptr.To(node),
			Pool:     resourcev1.ResourcePool{Name: node, ResourceSliceCount: 1},
		},
	}
}

// Rebellions ATOM 은 광고 주체 전환 축이 없어(RebellionsSpec 에 AdvertiseSpec 미포함) DRA 를
// 켜도 device-plugin 광고가 그대로 살아 있을 수 있다. 같은 노드가 rebellions.ai/ATOM 을
// allocatable(device-plugin)과 ResourceSlice(DRA) 양쪽으로 동시에 내면 스케줄러는 카드 1장을
// 자원 2개로 본다 — 이 시험은 그 상태를 감지해 변경시에만 경고하는지 고정한다.
func TestReconcileOneDRADriver_RebellionsDualAdvertiseWarns(t *testing.T) {
	p := rebellionsDRAPolicy()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"rebellions.ai/ATOM": resource.MustParse("2"),
		}},
	}
	slice := rebellionsResourceSlice("n1-npu", "n1")
	r := newDRAReconciler(p, node, slice)
	rec := record.NewFakeRecorder(10)
	r.Recorder = rec
	ctx := context.Background()

	if err := r.ensureDRADriver(ctx, p); err != nil {
		t.Fatalf("1회차 ensureDRADriver: %v", err)
	}
	if got := len(rec.Events); got != 1 {
		t.Fatalf("1회차 이벤트 %d건, want 1 — 이중광고를 처음 봤으면 알려야 한다", got)
	}
	event := <-rec.Events
	if !strings.Contains(event, "이중광고") || !strings.Contains(event, "n1") {
		t.Errorf("이벤트가 이중광고·노드명을 담지 않았다: %q", event)
	}
	var msg string
	for _, s := range p.Status.DRADrivers {
		if s.Vendor == vendorRebellions {
			msg = s.Message
		}
	}
	if !strings.Contains(msg, "이중광고") {
		t.Errorf("벤더 status.message 에 이중광고 통지가 없다: %q", msg)
	}

	// 2회차: spec 은 그대로이고 직전 status 에 같은 통지가 남아 있다.
	if err := r.ensureDRADriver(ctx, p); err != nil {
		t.Fatalf("2회차 ensureDRADriver: %v", err)
	}
	if got := len(rec.Events); got != 0 {
		t.Errorf("2회차 이벤트 %d건, want 0 — 바뀐 것이 없는데 경고가 다시 나갔다", got)
	}
}

// device-plugin 광고가 없는(allocatable 미등록) 노드에서 DRA 만 발행 중이면 이중광고가
// 아니다 — 오탐으로 불필요한 경고를 내지 않는지 본다.
func TestReconcileOneDRADriver_RebellionsNoDualAdvertiseNoWarn(t *testing.T) {
	p := rebellionsDRAPolicy()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	slice := rebellionsResourceSlice("n1-npu", "n1")
	r := newDRAReconciler(p, node, slice)
	rec := record.NewFakeRecorder(10)
	r.Recorder = rec

	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	if got := len(rec.Events); got != 0 {
		t.Errorf("이벤트 %d건, want 0 — device-plugin 광고가 없는데 이중광고로 오판했다", got)
	}
}

// 이중광고 판정은 노드별 AND(그 노드가 DRA 발행 AND device-plugin allocatable)여야 한다.
// 클러스터 전체 OR("DRA 발행 노드가 있고, 어딘가에 allocatable>0 인 노드가 있으면 전부
// 이중광고")로 잘못 구현돼도 단일 노드 픽스처로는 못 잡는다. 세 노드를 한 클러스터에 같이
// 둬 교집합만 걸리는지 고정한다.
func TestReconcileOneDRADriver_RebellionsDualAdvertisePerNodeIntersection(t *testing.T) {
	p := rebellionsDRAPolicy()
	dualNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "dual-node"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"rebellions.ai/ATOM": resource.MustParse("2"),
		}},
	}
	draOnlyNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "dra-only-node"}}
	pluginOnlyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "plugin-only-node"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"rebellions.ai/ATOM": resource.MustParse("1"),
		}},
	}
	r := newDRAReconciler(p, dualNode, draOnlyNode, pluginOnlyNode,
		rebellionsResourceSlice("dual-node-npu", "dual-node"),
		rebellionsResourceSlice("dra-only-node-npu", "dra-only-node"),
		// pluginOnlyNode 는 ResourceSlice 가 없다 — device-plugin 만 광고한다.
	)
	rec := record.NewFakeRecorder(10)
	r.Recorder = rec

	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	if got := len(rec.Events); got != 1 {
		t.Fatalf("이벤트 %d건, want 1 — 교집합 노드(dual-node)는 하나뿐이다", got)
	}
	event := <-rec.Events
	if !strings.Contains(event, "dual-node") {
		t.Errorf("이벤트가 이중광고 노드(dual-node)를 담지 않았다: %q", event)
	}
	if strings.Contains(event, "dra-only-node") {
		t.Errorf("DRA 만 내는 노드를 이중광고로 오판했다(클러스터 전체 OR 의심): %q", event)
	}
	if strings.Contains(event, "plugin-only-node") {
		t.Errorf("device-plugin 만 광고하는 노드를 이중광고로 오판했다(클러스터 전체 OR 의심): %q", event)
	}
}

// draResourceSlice 는 지정 드라이버가 노드 n1(DualAdvertisement 시험 전부가 쓰는 노드)에서
// 낸 발행물이다. rebellionsResourceSlice 와 달리 드라이버를 고정하지 않는다 —
// DualAdvertisement 시험은 벤더마다 다른 드라이버로 슬라이스를 만들어야 한다(다른 벤더
// 슬라이스가 섞여도 오판하지 않는지를 보는 시험 포함). 노드를 인자로 받지 않는 이유는
// unparam(golangci-lint) — 모든 호출부가 그 노드의 Allocatable 을 미리 심어 둔 "n1" 을 쓴다.
func draResourceSlice(name, driver string) *resourcev1.ResourceSlice {
	const node = "n1"
	return &resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.ResourceSliceSpec{
			Driver:   driver,
			NodeName: ptr.To(node),
			Pool:     resourcev1.ResourcePool{Name: node, ResourceSliceCount: 1},
			Devices:  []resourcev1.Device{{Name: "dev-0"}},
		},
	}
}

// TestDualAdvertiseCondition_TrueWhenBothPathsAdvertise 는 device-plugin allocatable 과
// 그 벤더 자신의 드라이버가 낸 ResourceSlice 가 같은 노드에 동시에 있으면 top-level
// Condition 이 True 가 되고, 메시지가 그 노드를 지목하는 것을 단정한다.
func TestDualAdvertiseCondition_TrueWhenBothPathsAdvertise(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"tenstorrent.com/blackhole": resource.MustParse("1"),
		}},
	}
	slice := draResourceSlice("n1-tt", draDriverNameTenstorrent)
	r := newDRAReconciler(p, node, slice)

	r.detectDualAdvertisement(context.Background(), p)

	cond := apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionDualAdvertisement)
	if cond == nil {
		t.Fatal("DualAdvertisement condition 이 없다")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("status = %v, want True", cond.Status)
	}
	if cond.Reason != "DualAdvertiseDetected" {
		t.Errorf("reason = %q, want DualAdvertiseDetected", cond.Reason)
	}
	if !strings.Contains(cond.Message, "n1") {
		t.Errorf("메시지가 노드를 지목하지 않는다: %q", cond.Message)
	}
}

// TestDualAdvertiseCondition_FalseWhenOnlyOnePath 는 device-plugin·DRA 둘 중 하나만 광고
// 중이면(allocatable 0 이거나 슬라이스가 없거나) Condition 이 False 로 남는 것을 단정한다.
func TestDualAdvertiseCondition_FalseWhenOnlyOnePath(t *testing.T) {
	cases := []struct {
		name  string
		node  *corev1.Node
		slice *resourcev1.ResourceSlice
	}{
		{
			name:  "allocatable 0(DRA 만 발행)",
			node:  &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
			slice: draResourceSlice("n1-tt", draDriverNameTenstorrent),
		},
		{
			name: "슬라이스 없음(device-plugin 만 광고)",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "n1"},
				Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
					"tenstorrent.com/blackhole": resource.MustParse("1"),
				}},
			},
			slice: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &npuv1alpha1.NPUClusterPolicy{}
			objs := []client.Object{p, tc.node}
			if tc.slice != nil {
				objs = append(objs, tc.slice)
			}
			r := newDRAReconciler(objs...)

			r.detectDualAdvertisement(context.Background(), p)

			cond := apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionDualAdvertisement)
			if cond == nil {
				t.Fatal("DualAdvertisement condition 이 없다")
			}
			if cond.Status != metav1.ConditionFalse {
				t.Errorf("status = %v, want False (message %q)", cond.Status, cond.Message)
			}
		})
	}
}

// TestDualAdvertiseCondition_IgnoresOtherVendorSlices 는 다른 벤더(nvidia) 드라이버가 낸
// 슬라이스가 tenstorrent 판정에 들어가지 않는 것을 단정한다 — 노드가 tenstorrent
// device-plugin 으로는 광고 중이어도, DRA 쪽이 tenstorrent 자신의 드라이버가 아니면
// 이중광고가 아니다.
func TestDualAdvertiseCondition_IgnoresOtherVendorSlices(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"tenstorrent.com/blackhole": resource.MustParse("1"),
		}},
	}
	nvSlice := draResourceSlice("n1-nv", draDriverNameNvidia)
	r := newDRAReconciler(p, node, nvSlice)

	r.detectDualAdvertisement(context.Background(), p)

	cond := apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionDualAdvertisement)
	if cond == nil {
		t.Fatal("DualAdvertisement condition 이 없다")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("status = %v, want False — nvidia 슬라이스를 tenstorrent 로 오판했다: %q", cond.Status, cond.Message)
	}
}

// TestDualAdvertiseCondition_DistinguishesFuriosaAndRngd 는 rngd 드라이버(furiosa 와 공유하는
// draDriverNameFuriosa)가 낸 슬라이스와 furiosa.ai/rngd allocatable 이 함께 있는 노드를 rngd
// 이중광고로만 잡고 furiosa(warboy) 판정에는 영향이 없는 것을 단정한다. 기존 시험은
// tenstorrent·nvidia 조합만 시험해 furiosa·rngd 는 리소스명이 실제로 갈라지는지 직접 확인하지
// 않았다 — 드라이버 이름은 같아도 리소스명(advertiseVendorResourceName)이 다르므로 갈려야 한다.
func TestDualAdvertiseCondition_DistinguishesFuriosaAndRngd(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"furiosa.ai/rngd": resource.MustParse("1"),
		}},
	}
	slice := draResourceSlice("n1-rngd", draDriverNameFuriosa)
	r := newDRAReconciler(p, node, slice)

	r.detectDualAdvertisement(context.Background(), p)

	cond := apimeta.FindStatusCondition(p.Status.Conditions, npuv1alpha1.ConditionDualAdvertisement)
	if cond == nil {
		t.Fatal("DualAdvertisement condition 이 없다")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("status = %v, want True(rngd 이중광고): %q", cond.Status, cond.Message)
	}
	if !strings.Contains(cond.Message, "rngd: n1") {
		t.Errorf("메시지가 rngd 를 지목하지 않는다: %q", cond.Message)
	}
	if strings.Contains(cond.Message, "furiosa: n1") {
		t.Errorf("furiosa(warboy) 까지 이중광고로 오판했다(리소스명이 다른데): %q", cond.Message)
	}
}

// TestDualAdvertiseCondition_EventOnlyOnTransition 는 DualAdvertise 경고가 False→True
// 전이(최초 포함)에서만 나가고, 같은 True 상태가 반복되는 reconcile 에서는 다시 나가지
// 않는 것을 단정한다.
func TestDualAdvertiseCondition_EventOnlyOnTransition(t *testing.T) {
	p := &npuv1alpha1.NPUClusterPolicy{}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"tenstorrent.com/blackhole": resource.MustParse("1"),
		}},
	}
	slice := draResourceSlice("n1-tt", draDriverNameTenstorrent)
	r := newDRAReconciler(p, node, slice)
	rec := record.NewFakeRecorder(10)
	r.Recorder = rec
	ctx := context.Background()

	r.detectDualAdvertisement(ctx, p)
	if got := len(rec.Events); got != 1 {
		t.Fatalf("1회차 이벤트 %d건, want 1 — False→True 전이를 처음 봤으면 알려야 한다", got)
	}
	<-rec.Events

	r.detectDualAdvertisement(ctx, p)
	r.detectDualAdvertisement(ctx, p)
	if got := len(rec.Events); got != 0 {
		t.Errorf("같은 True 상태 반복인데 이벤트 %d건 — 전이가 아니면 다시 나가면 안 된다", got)
	}
}

// countMatchExpressions 는 이 DaemonSet 의 required nodeAffinity 전체 match expression
// 개수다. "벤더 affinity(NFD 등)가 사라졌다" 를 Affinity==nil 로 재던 시험들이, retired
// 배제가 추가되며(항상 non-nil) 이 카운트 방식으로 바뀌었다 — 예상한 것 말고는 남지 않았는지
// 확인하는 용도다.
func countMatchExpressions(ds *appsv1.DaemonSet) int {
	na := ds.Spec.Template.Spec.Affinity
	if na == nil || na.NodeAffinity == nil ||
		na.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return 0
	}
	n := 0
	for _, term := range na.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		n += len(term.MatchExpressions)
	}
	return n
}

// dsExcludesDRARetired 는 렌더된 PodSpec 이 해당 벤더의 dra-retired 라벨 부재를 요구하는지
// 본다(npuclusterpolicy_advertise_test.go 의 dsExcludesDRAOwned 와 대칭).
func dsExcludesDRARetired(ds *appsv1.DaemonSet, vendor string) bool {
	key := npuv1alpha1.DRARetiredNodeLabel(vendor)
	na := ds.Spec.Template.Spec.Affinity
	if na == nil || na.NodeAffinity == nil ||
		na.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return false
	}
	for _, term := range na.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, e := range term.MatchExpressions {
			if e.Key == key && e.Operator == corev1.NodeSelectorOpDoesNotExist {
				return true
			}
		}
	}
	return false
}

// TestRenderDRADaemonSet_ExcludesRetiredNodes 는 advertiseVendors() 다섯 벤더 키 모두가
// 자신의 dra-retired 라벨 DoesNotExist 조건을, 실제로 그 벤더를 서비스하는 DRA DaemonSet
// 렌더에서 갖는 것을 단정한다. furiosa·rngd 는 DRA 드라이버가 하나뿐이라(draVendors,
// renderFuriosaDRADaemonSet) 같은 DaemonSet 이 두 라벨을 모두 배제해야 한다 — device-plugin
// 쪽 통합 DS 가 excludeDRAOwnedNodes 를 furiosa·rngd 둘 다에 거는 것과 대칭이다. NVIDIA 는
// nvidiaDRABlockReason 게이트로 이미 막혀 있지만 규칙을 하나로 두기 위해 포함한다.
func TestRenderDRADaemonSet_ExcludesRetiredNodes(t *testing.T) {
	labels := map[string]string{"app": "x"}
	spec := &npuv1alpha1.DRASpec{Enabled: true}
	furiosaDS := renderFuriosaDRADaemonSet(spec, labels)
	cases := []struct {
		vendor string
		ds     *appsv1.DaemonSet
	}{
		{vendorNvidia, renderNvidiaDRADaemonSet(spec, labels)},
		{vendorFuriosa, furiosaDS},
		{vendorRngd, furiosaDS},
		{vendorTenstorrent, renderTenstorrentDRADaemonSet(spec, labels)},
		{vendorRebellions, renderRebellionsDRADaemonSet(spec, labels)},
	}
	for _, tc := range cases {
		t.Run(tc.vendor, func(t *testing.T) {
			if !dsExcludesDRARetired(tc.ds, tc.vendor) {
				t.Errorf("%s: retired 노드를 제외하지 않음", tc.vendor)
			}
		})
	}
}

// HEALTHCHECK_PORT 오버라이드가 probe 포트를 따라가는지, 판정 불가능한 값(ValueFrom·
// 범위 밖·파싱 실패)에서는 probe 자체를 안 붙이는지를 표로 고정한다. 이 시험이 없으면
// 다음 편집이 되읽기 로직을 조용히 되돌려도 아무도 모른다.
func TestRenderRebellionsDRADaemonSet_HealthcheckProbe(t *testing.T) {
	cases := []struct {
		name      string
		override  *npuv1alpha1.DRASpec
		wantPort  int32 // wantProbe=false 면 무시
		wantProbe bool
	}{
		{"기본값", nil, 51515, true},
		{"유효한 오버라이드", &npuv1alpha1.DRASpec{Enabled: true, Env: []corev1.EnvVar{
			{Name: "HEALTHCHECK_PORT", Value: "61000"},
		}}, 61000, true},
		{"벤더 관례상 끔(-1)", &npuv1alpha1.DRASpec{Enabled: true, Env: []corev1.EnvVar{
			{Name: "HEALTHCHECK_PORT", Value: "-1"},
		}}, 0, false},
		{"범위 밖(65536 초과)", &npuv1alpha1.DRASpec{Enabled: true, Env: []corev1.EnvVar{
			{Name: "HEALTHCHECK_PORT", Value: "99999"},
		}}, 0, false},
		{"파싱 실패", &npuv1alpha1.DRASpec{Enabled: true, Env: []corev1.EnvVar{
			{Name: "HEALTHCHECK_PORT", Value: "abc"},
		}}, 0, false},
		// Value 를 파싱 가능한 값으로 같이 채운다. ValueFrom 가드가 없으면 62000 이
		// 파싱돼 probe 가 붙으므로, 이 칸이 가드 하나만 고정한다. 앞의 "파싱 실패"
		// 칸과 같은 경로로 새지 않는다.
		{"ValueFrom — 렌더 시점 값 불명", &npuv1alpha1.DRASpec{Enabled: true, Env: []corev1.EnvVar{
			{Name: "HEALTHCHECK_PORT", Value: "62000", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			}},
		}}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := renderRebellionsDRADaemonSet(tc.override, map[string]string{"app": "x"})
			probe := ds.Spec.Template.Spec.Containers[0].LivenessProbe
			if !tc.wantProbe {
				if probe != nil {
					t.Errorf("probe=%+v, want nil", probe)
				}
				return
			}
			if probe == nil || probe.GRPC == nil {
				t.Fatal("probe 가 없다")
			}
			if probe.GRPC.Port != tc.wantPort {
				t.Errorf("probe port=%d, want %d", probe.GRPC.Port, tc.wantPort)
			}
		})
	}
}

func tenstorrentDRAPolicy() *npuv1alpha1.NPUClusterPolicy {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Tenstorrent.Enabled = true
	p.Spec.Tenstorrent.DRA = &npuv1alpha1.DRASpec{Enabled: true}
	return p
}

// TestRenderTenstorrentDRADriver_HasFabricManager 는 FM agent 컨테이너와 그
// 설정 ConfigMap 이 함께 나오는지 본다. 드라이버는 장치를 직접 찾지 못하므로
// FM 이 없으면 배포가 성립해도 ResourceSlice 가 비거나 기동이 실패한다.
func TestRenderTenstorrentDRADriver_HasFabricManager(t *testing.T) {
	objs := renderTenstorrentDRADriver(tenstorrentDRAPolicy())

	var ds *appsv1.DaemonSet
	var cm *corev1.ConfigMap
	for _, o := range objs {
		switch v := o.(type) {
		case *appsv1.DaemonSet:
			ds = v
		case *corev1.ConfigMap:
			cm = v
		}
	}
	if ds == nil || cm == nil {
		t.Fatalf("DaemonSet=%v ConfigMap=%v, 둘 다 있어야 한다", ds != nil, cm != nil)
	}
	if len(ds.Spec.Template.Spec.Containers) != 2 {
		t.Errorf("컨테이너 %d개, want 2(plugin·fabric-manager)", len(ds.Spec.Template.Spec.Containers))
	}
	if !strings.Contains(cm.Data["config.yaml"], "listen_address") {
		t.Errorf("ConfigMap 에 listen_address 가 없다: %q", cm.Data["config.yaml"])
	}

	fm := containerByName(ds, "fabric-manager")
	if fm == nil {
		t.Fatal("fabric-manager 컨테이너가 없다")
	}
	// 등호 표기(--host-id=0)는 벤더 바이너리가 Unknown argument 로 거부한다. 공백으로
	// 분리된 두 원소여야 하고, 한 원소로 합치면 파드가 기동에 실패한다.
	if len(fm.Args) != 2 || fm.Args[0] != "--host-id" || fm.Args[1] != "0" {
		t.Errorf("fabric-manager args=%q, want [\"--host-id\" \"0\"] — "+
			"등호 표기나 한 원소로 합친 형태는 벤더 바이너리가 거부한다", fm.Args)
	}

	// 벤더 이미지는 ENTRYPOINT 가 없고 Cmd 만 있다. Args 를 주면 Cmd 가 덮이므로 Command 를
	// 명시하지 않으면 kubelet 이 첫 인자를 실행 파일로 exec 해 StartError 로 죽는다.
	// 두 컨테이너 모두 벤더 바이너리를 Command 로 명시해야 한다.
	if len(fm.Command) != 1 || fm.Command[0] != "tt-fabric-manager-agent" {
		t.Errorf("fabric-manager command=%q, want [\"tt-fabric-manager-agent\"] — "+
			"Command 미지정 시 --host-id 를 exec 해 StartError", fm.Command)
	}
	plugin := containerByName(ds, "plugin")
	if plugin == nil {
		t.Fatal("plugin 컨테이너가 없다")
	}
	if len(plugin.Command) != 1 || plugin.Command[0] != "tt-dra-driver-kubeletplugin" {
		t.Errorf("plugin command=%q, want [\"tt-dra-driver-kubeletplugin\"] — "+
			"Command 미지정 시 --device-profile 을 exec 해 StartError", plugin.Command)
	}
	// tt-dra-driver-kubeletplugin 은 --node-name(=$NODE_NAME)이 필수다. downward API 로
	// 노드명을 주입하지 않으면 "Required flag node-name not set" 로 크래시한다.
	var hasNodeName bool
	for _, e := range plugin.Env {
		if e.Name == "NODE_NAME" && e.ValueFrom != nil && e.ValueFrom.FieldRef != nil &&
			e.ValueFrom.FieldRef.FieldPath == "spec.nodeName" {
			hasNodeName = true
		}
	}
	if !hasNodeName {
		t.Error("plugin 에 NODE_NAME(fieldRef spec.nodeName) env 가 없다 — --node-name 필수라 크래시한다")
	}

	// 볼륨이 가리키는 ConfigMap 이름과 실제 렌더된 ConfigMap 이름이 어긋나면 FM 이 설정
	// 파일을 못 찾아 기동을 거부하고 DaemonSet 이 CrashLoop 한다. Tenstorrent 라이브
	// 검증이 보류돼 있어 이 어긋남이 드러날 자리는 이 시험뿐이다.
	var ref string
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "fm-config" && v.ConfigMap != nil {
			ref = v.ConfigMap.Name
		}
	}
	if ref != cm.Name {
		t.Errorf("fm-config 볼륨이 가리키는 ConfigMap=%q, 렌더된 ConfigMap=%q", ref, cm.Name)
	}
}

// TestRenderTenstorrentDRADriver_NoFallbackDiscovery 는 금지 플래그가 섞이지
// 않았는지 본다. 그 플래그를 켜면 이 메인보드에서 빈 ResourceSlice 가 나온다.
func TestRenderTenstorrentDRADriver_NoFallbackDiscovery(t *testing.T) {
	objs := renderTenstorrentDRADriver(tenstorrentDRAPolicy())
	for _, o := range objs {
		ds, ok := o.(*appsv1.DaemonSet)
		if !ok {
			continue
		}
		for _, c := range ds.Spec.Template.Spec.Containers {
			for _, a := range c.Args {
				if strings.Contains(a, "force-fallback-discovery") {
					t.Errorf("컨테이너 %q 에 금지 플래그가 있다: %q", c.Name, a)
				}
			}
		}
	}
}

// TestRenderTenstorrentDRADriver_FabricManagerImageOverride 는 FM 이미지 덮어쓰기
// 경로를 고정한다. 이 경로가 조용히 깨지면 사설 레지스트리를 쓰는 배포에서만 드러나고
// 그 환경이 원인 추적이 가장 어렵다. 이 시험이 없으면 dra_driver.go 의 덮어쓰기 블록을
// 통째로 지워도 패키지 전체가 초록으로 남는다.
func TestRenderTenstorrentDRADriver_FabricManagerImageOverride(t *testing.T) {
	// fmContainer 는 렌더 결과에서 fabric-manager 컨테이너를 이름으로 집어 온다.
	// 목록 위치가 아니라 이름으로 고르는 이유는 컨테이너 순서가 계약이 아니기 때문이다.
	fmContainer := func(t *testing.T, p *npuv1alpha1.NPUClusterPolicy) corev1.Container {
		t.Helper()
		for _, o := range renderTenstorrentDRADriver(p) {
			ds, ok := o.(*appsv1.DaemonSet)
			if !ok {
				continue
			}
			if c := containerByName(ds, "fabric-manager"); c != nil {
				return *c
			}
		}
		t.Fatal("fabric-manager 컨테이너를 찾지 못했다")
		return corev1.Container{}
	}

	t.Run("미지정이면 렌더러 기본값", func(t *testing.T) {
		got := fmContainer(t, tenstorrentDRAPolicy()).Image
		if got != draFMImageTenstorrentDefault {
			t.Errorf("FM 이미지 = %q, want %q", got, draFMImageTenstorrentDefault)
		}
	})

	t.Run("지정하면 그 값이 실린다", func(t *testing.T) {
		const want = "mirror.example:5000/kcloud/tenstorrent/tt-fabric-manager:v1.2.3"
		p := tenstorrentDRAPolicy()
		p.Spec.Tenstorrent.DRA.FabricManagerImage = want
		if got := fmContainer(t, p).Image; got != want {
			t.Errorf("FM 이미지 = %q, want %q", got, want)
		}
	})

	// FabricManagerImage 는 평범한 string 이라 빈 문자열 지정과 아예 지정하지
	// 않음이 구조체 값으로 구별되지 않는다 — 이 칸은 "미지정이면 렌더러 기본값" 과
	// 독립적으로 실패할 수 없다. 필드가 포인터로 바뀌거나 defaulting webhook 이
	// 생기면 그때부터 독립적인 의미를 가진다. 계약을 문서로 박아 두는 값이 있어
	// 지금도 남긴다.
	t.Run("빈 문자열이면 기본값으로 되돌아간다", func(t *testing.T) {
		p := tenstorrentDRAPolicy()
		p.Spec.Tenstorrent.DRA.FabricManagerImage = ""
		got := fmContainer(t, p).Image
		if got != draFMImageTenstorrentDefault {
			t.Errorf("FM 이미지 = %q, want %q", got, draFMImageTenstorrentDefault)
		}
	})

	t.Run("드라이버 컨테이너는 끌려가지 않는다", func(t *testing.T) {
		p := tenstorrentDRAPolicy()
		p.Spec.Tenstorrent.DRA.FabricManagerImage = "mirror.example:5000/x/fm:v9"
		ds := findDS(renderTenstorrentDRADriver(p))
		if ds == nil {
			t.Fatal("DaemonSet 이 렌더되지 않았다")
		}
		plugin := containerByName(ds, "plugin")
		if plugin == nil {
			t.Fatal("plugin 컨테이너가 없다")
		}
		if plugin.Image != draDriverImageTenstorrentDefault {
			t.Errorf("plugin 이미지가 FM 덮어쓰기에 끌려갔다: %q", plugin.Image)
		}
	})
}

// TestTenstorrentDRAIgnoredArgsAreReported 는 반영하지 않는 설정을 조용히 버리지
// 않는지 고정한다. 이 벤더는 args 통째 교체가 드라이버를 못 뜨게 하므로 무시가
// 맞지만, 무시했다는 사실을 알리지 않으면 사용자는 반영됐다고 믿는다.
func TestTenstorrentDRAIgnoredArgsAreReported(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*npuv1alpha1.DRASpec)
		wantNote bool
	}{
		{"args 를 넣으면 알린다", func(s *npuv1alpha1.DRASpec) {
			s.Args = []string{"--whatever"}
		}, true},
		{"env 를 넣으면 알린다", func(s *npuv1alpha1.DRASpec) {
			s.Env = []corev1.EnvVar{{Name: "FOO", Value: "bar"}}
		}, true},
		{"아무것도 안 넣으면 알리지 않는다", func(s *npuv1alpha1.DRASpec) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tenstorrentDRAPolicy()
			tc.mutate(p.Spec.Tenstorrent.DRA)
			note := ignoredDRAConfigNote(vendorTenstorrent, p.Spec.Tenstorrent.DRA)
			if tc.wantNote && note == "" {
				t.Error("무시한 설정이 있는데 알리지 않았다")
			}
			if !tc.wantNote && note != "" {
				t.Errorf("무시한 설정이 없는데 알렸다: %q", note)
			}
		})
	}
}

// TestOtherVendorsDoNotReportIgnoredArgs 는 반영하는 벤더에서 잘못 알리지 않는지 본다.
// 세 벤더는 args·env 를 실제로 쓰므로(:143·:648·:804) 여기서 경고가 나오면 오탐이다.
func TestOtherVendorsDoNotReportIgnoredArgs(t *testing.T) {
	for _, vendor := range []string{vendorFuriosa, vendorNvidia, vendorRebellions} {
		spec := &npuv1alpha1.DRASpec{
			Enabled: true,
			Args:    []string{"--whatever"},
			Env:     []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
		}
		if note := ignoredDRAConfigNote(vendor, spec); note != "" {
			t.Errorf("%s: 반영하는 벤더인데 무시했다고 알렸다: %q", vendor, note)
		}
	}
}

// TestEnsureDRADriver_IgnoredNoteEventOnlyOnChange 는 무시 통지 경고가 조정마다 다시
// 나가지 않는지 고정한다. 발행물이 관측되지 않으면 draRequeue 가 30초마다 재조정하므로,
// 본류에서 무조건 내면 spec 이 그대로인데도 같은 경고가 30초마다 갱신된다.
func TestEnsureDRADriver_IgnoredNoteEventOnlyOnChange(t *testing.T) {
	p := tenstorrentDRAPolicy()
	p.Spec.Tenstorrent.DRA.Args = []string{"--whatever"}
	r := newDRAReconciler(p)
	rec := record.NewFakeRecorder(10)
	r.Recorder = rec
	ctx := context.Background()

	if err := r.ensureDRADriver(ctx, p); err != nil {
		t.Fatalf("1회차 ensureDRADriver: %v", err)
	}
	if got := len(rec.Events); got != 1 {
		t.Fatalf("1회차 이벤트 %d건, want 1 — 처음 보는 통지는 알려야 한다", got)
	}
	<-rec.Events

	// 2회차: spec 은 그대로이고 직전 status 에 같은 통지가 남아 있다.
	if err := r.ensureDRADriver(ctx, p); err != nil {
		t.Fatalf("2회차 ensureDRADriver: %v", err)
	}
	if got := len(rec.Events); got != 0 {
		t.Errorf("2회차 이벤트 %d건, want 0 — 바뀐 것이 없는데 경고가 다시 나갔다", got)
	}

	// 통지 내용이 바뀌면(args → args·env) 다시 알려야 한다.
	p.Spec.Tenstorrent.DRA.Env = []corev1.EnvVar{{Name: "FOO", Value: "bar"}}
	if err := r.ensureDRADriver(ctx, p); err != nil {
		t.Fatalf("3회차 ensureDRADriver: %v", err)
	}
	if got := len(rec.Events); got != 1 {
		t.Errorf("3회차 이벤트 %d건, want 1 — 통지가 바뀌면 다시 알려야 한다", got)
	}
}

// TestEnsureDRADriver_InstallingMessageIncludesIgnoredNote 는 발행물이 아직 없어
// Installing 메시지가 쓰이는 회차에도 무시 통지가 함께 남는지 본다. 두 문장이
// 같은 st.Message 자리를 다투므로 병합 없이는 어느 한쪽이 사라진다.
func TestEnsureDRADriver_InstallingMessageIncludesIgnoredNote(t *testing.T) {
	p := tenstorrentDRAPolicy()
	p.Spec.Tenstorrent.DRA.Args = []string{"--whatever"}
	r := newDRAReconciler(p)
	if err := r.ensureDRADriver(context.Background(), p); err != nil {
		t.Fatalf("ensureDRADriver: %v", err)
	}
	var st *npuv1alpha1.DRADriverStatus
	for i := range p.Status.DRADrivers {
		if p.Status.DRADrivers[i].Vendor == vendorTenstorrent {
			st = &p.Status.DRADrivers[i]
		}
	}
	if st == nil {
		t.Fatal("tenstorrent 상태가 없다")
	}
	if st.Phase != npuv1alpha1.DRAPhaseInstalling {
		t.Fatalf("phase=%q, want Installing(발행물 없음 전제가 깨졌다)", st.Phase)
	}
	if !strings.Contains(st.Message, "ResourceSlice 발행이 아직 관측되지 않음") {
		t.Errorf("Installing 메시지가 사라졌다: %q", st.Message)
	}
	if !strings.Contains(st.Message, "spec.dra.args") {
		t.Errorf("무시 통지가 사라졌다: %q", st.Message)
	}
}
