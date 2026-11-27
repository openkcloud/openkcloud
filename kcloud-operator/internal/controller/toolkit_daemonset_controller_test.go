// ============================================================
// toolkit_daemonset_controller_test.go: renderToolkitDaemonSet / vendor 게이트 단위 테스트
// 상세: NVIDIA toolkit DS 구조(driver-validation init container, containerd
//        config/socket 마운트, ownerRef, IfNotPresent), vendor 게이트, 이미지 override 검증.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
)

// toolkitTestPolicy 는 toolkit 활성화된 최소 NVIDIA DIP 를 만든다(toolkit 은 현재
// NVIDIA 만 대상이므로 vendor/model 은 고정). image 로 override 케이스를 검증한다.
func toolkitTestPolicy(image string) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: "nvidia",
			Model:  "generic",
			Driver: v1alpha1.DriverSpec{Image: "registry.example/driver:1.0.0", Mode: "daemonset"},
			Toolkit: &v1alpha1.ToolkitSpec{
				Enabled: true,
				Image:   image,
			},
		},
	}
}

// TestToolkitVendorSupported 는 NVIDIA 만 toolkit DS 대상이고 나머지 vendor 는
// skip 됨을 검증한다(회귀 0 게이트).
func TestToolkitVendorSupported(t *testing.T) {
	cases := map[string]bool{
		"nvidia":      true,
		"NVIDIA":      true,
		"furiosa":     false,
		"rebellions":  false,
		"tenstorrent": false,
		"":            false,
	}
	for vendor, want := range cases {
		if got := toolkitVendorSupported(vendor); got != want {
			t.Errorf("toolkitVendorSupported(%q)=%v, want %v", vendor, got, want)
		}
	}
}

// TestRenderToolkitDaemonSet_Nvidia 는 NVIDIA toolkit DS 의 핵심 구조를 검증한다.
func TestRenderToolkitDaemonSet_Nvidia(t *testing.T) {
	ds := renderToolkitDaemonSet(toolkitTestPolicy(""))

	wantName := naming.ToolkitDSName("nvidia", "generic")
	if ds.Name != wantName {
		t.Errorf("DS name=%q, want %q", ds.Name, wantName)
	}
	if ds.Namespace != "kube-system" {
		t.Errorf("DS namespace=%q, want kube-system", ds.Namespace)
	}

	// ownerRef → DIP (cascade 삭제)
	if len(ds.OwnerReferences) != 1 || ds.OwnerReferences[0].Kind != "DriverInstallPolicy" {
		t.Errorf("ownerRef 미설정 또는 Kind 불일치: %+v", ds.OwnerReferences)
	}

	spec := ds.Spec.Template.Spec

	// driver Ready 대기 init container
	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Name != "driver-validation" {
		t.Fatalf("driver-validation init container 미설정: %+v", spec.InitContainers)
	}

	// 전 컨테이너 IfNotPresent (air-gap 불변식과 동일 규약)
	assertAllContainersIfNotPresent(t, "toolkit", spec)

	// containerd config/socket host 마운트 존재
	main := spec.Containers[0]
	// entrypoint(/work/nvidia-toolkit)의 필수 positional 인자 DESTINATION(install root).
	// 누락 시 "the install root must be specified" 로 즉시 종료 → containerd 등록 실패.
	if len(main.Args) != 1 || main.Args[0] != hostToolkitInstallDir {
		t.Errorf("main container Args=%v, want [%q] (install root DESTINATION)", main.Args, hostToolkitInstallDir)
	}
	wantMounts := map[string]bool{"containerd-config": false, "containerd-socket": false, "toolkit-install-dir": false}
	for _, m := range main.VolumeMounts {
		if _, ok := wantMounts[m.Name]; ok {
			wantMounts[m.Name] = true
		}
	}
	for name, found := range wantMounts {
		if !found {
			t.Errorf("main container 에 %q 마운트 없음 (containerd patch 불가)", name)
		}
	}

	// privileged (containerd config 쓰기/재시작 필요)
	if main.SecurityContext == nil || main.SecurityContext.Privileged == nil || !*main.SecurityContext.Privileged {
		t.Errorf("toolkit main container privileged 아님")
	}

	// set-as-default=false (기존 워크로드 무영향) + driver-root=/ (호스트 설치 드라이버)
	env := map[string]string{}
	for _, e := range main.Env {
		env[e.Name] = e.Value
	}
	if env["CONTAINERD_SET_AS_DEFAULT"] != "false" {
		t.Errorf("CONTAINERD_SET_AS_DEFAULT=%q, want false", env["CONTAINERD_SET_AS_DEFAULT"])
	}
	// NVIDIA_DRIVER_ROOT=/ : 호스트 설치 드라이버에서 nvidia-container-cli 의
	// "change root failed" 방지(기본값 /run/nvidia/driver 는 컨테이너 드라이버 전용).
	if env["NVIDIA_DRIVER_ROOT"] != "/" {
		t.Errorf("NVIDIA_DRIVER_ROOT=%q, want /", env["NVIDIA_DRIVER_ROOT"])
	}
}

// TestRenderToolkitDaemonSet_EnvPassthrough 는 DIP.spec.toolkit.env 가 DS main
// container env 뒤에 append 되어(기본값 override 가능) 전달되는지 검증한다.
func TestRenderToolkitDaemonSet_EnvPassthrough(t *testing.T) {
	pol := toolkitTestPolicy("")
	pol.Spec.Toolkit.Env = []v1alpha1.KV{
		{Name: "NVIDIA_DRIVER_ROOT", Value: "/run/nvidia/driver"},
		{Name: "CUSTOM_FLAG", Value: "1"},
	}
	main := renderToolkitDaemonSet(pol).Spec.Template.Spec.Containers[0]

	// passthrough env 가 존재하고, append 규약상 사용자 값이 기본값보다 뒤에 와서
	// 최종 승자가 된다(env 는 마지막 정의가 우선).
	var lastDriverRoot, custom string
	for _, e := range main.Env {
		switch e.Name {
		case "NVIDIA_DRIVER_ROOT":
			lastDriverRoot = e.Value
		case "CUSTOM_FLAG":
			custom = e.Value
		}
	}
	if custom != "1" {
		t.Errorf("passthrough CUSTOM_FLAG=%q, want 1", custom)
	}
	if lastDriverRoot != "/run/nvidia/driver" {
		t.Errorf("passthrough override 후 NVIDIA_DRIVER_ROOT 최종값=%q, want /run/nvidia/driver", lastDriverRoot)
	}
	if main.Env[len(main.Env)-1].Name != "CUSTOM_FLAG" {
		t.Errorf("passthrough env 가 기본 env 뒤에 append 되지 않음: %+v", main.Env)
	}
}

// TestRenderToolkitDaemonSet_ImageOverride 는 Toolkit.Image override 가 반영되고,
// 미지정 시 기본 이미지가 쓰이는지 검증한다.
func TestRenderToolkitDaemonSet_ImageOverride(t *testing.T) {
	custom := "registry.example.com:5000/kcloud/nvidia/container-toolkit:v1.17.8"
	ds := renderToolkitDaemonSet(toolkitTestPolicy(custom))
	if got := ds.Spec.Template.Spec.Containers[0].Image; got != custom {
		t.Errorf("override image=%q, want %q", got, custom)
	}

	def := renderToolkitDaemonSet(toolkitTestPolicy(""))
	if got := def.Spec.Template.Spec.Containers[0].Image; got != nvidiaToolkitImageDefault {
		t.Errorf("default image=%q, want %q", got, nvidiaToolkitImageDefault)
	}
}

// TestCreateOrUpdateDS_RetriesOnConflict 는 동시 reconcile 로 Update 가 409 Conflict 를
// 받아도 RetryOnConflict 가 재-Get→재-Update 로 흡수해 에러 없이 수렴함을 검증한다.
func TestCreateOrUpdateDS_RetriesOnConflict(t *testing.T) {
	pol := toolkitTestPolicy("")
	pol.Name = "nvidia-gpu-ds"
	desired := renderToolkitDaemonSet(pol)

	// spec 이 다른 기존 DS 를 심어 Update 경로를 강제한다.
	existing := desired.DeepCopy()
	existing.Spec.Template.Spec.Containers = nil

	var updateCalls int
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(existing).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updateCalls++
				if updateCalls == 1 {
					return apierrors.NewConflict(
						schema.GroupResource{Group: "apps", Resource: "daemonsets"},
						obj.GetName(), errors.New("stale resourceVersion"))
				}
				return cl.Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &ToolkitDaemonSetReconciler{Client: c}

	if err := r.createOrUpdateDS(context.Background(), desired); err != nil {
		t.Fatalf("createOrUpdateDS 가 재시도에도 에러 반환: %v", err)
	}
	if updateCalls < 2 {
		t.Fatalf("Conflict 재시도 기대(>=2 Update), got %d", updateCalls)
	}
	var got appsv1.DaemonSet
	if err := c.Get(context.Background(), types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &got); err != nil {
		t.Fatalf("update 후 get: %v", err)
	}
	if len(got.Spec.Template.Spec.Containers) == 0 {
		t.Fatalf("재시도 후 DS spec 이 desired 로 수렴하지 않음")
	}
}

// TestCreateOrUpdateToolkitDS_DeletesLegacy 는 접두사를 뗀 새 이름으로 ensure 하기 전에
// 옛 이름(kcloud-nvidia-toolkit) DS 를 지우는지 검증한다. 두 DS 가 남으면 같은 노드의
// containerd 설정을 서로 덮어써 유효 설정을 알 수 없게 된다.
func TestCreateOrUpdateToolkitDS_DeletesLegacy(t *testing.T) {
	legacy := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: naming.ToolkitLegacyDSName, Namespace: naming.KubeSystemNamespace},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(legacy).Build()
	r := &ToolkitDaemonSetReconciler{Client: c}

	if err := r.createOrUpdateToolkitDS(context.Background(), toolkitTestPolicy("")); err != nil {
		t.Fatalf("createOrUpdateToolkitDS 오류: %v", err)
	}

	var old appsv1.DaemonSet
	err := c.Get(context.Background(), types.NamespacedName{
		Name: naming.ToolkitLegacyDSName, Namespace: naming.KubeSystemNamespace}, &old)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("옛 이름 DaemonSet 이 남아 있다: err=%v", err)
	}

	var newDS appsv1.DaemonSet
	newName := naming.ToolkitDSName("nvidia", "generic")
	if err := c.Get(context.Background(), types.NamespacedName{
		Name: newName, Namespace: naming.KubeSystemNamespace}, &newDS); err != nil {
		t.Fatalf("새 이름 DaemonSet 없음: %v", err)
	}
}
