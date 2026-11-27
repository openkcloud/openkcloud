// ============================================================
// rebellions_plugin_render_test.go: Rebellions device plugin 판별 DaemonSet 렌더링 시험
// 상세: v0.3·v0.4 형식의 인자·환경변수·마운트·볼륨·hostNetwork·livenessProbe·init container 를
//       ensureRebellionsDevicePlugin 결과로 고정한다. 판 전환 시 갱신, v0.4 ConfigMap 생략도 본다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	"context"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
)

const (
	rbllnsTestImageV03 = "docker.io/rebellions/k8s-device-plugin:v0.3.6"
	rbllnsTestImageV04 = "docker.io/rebellions/k8s-device-plugin:v0.4.3"
)

//nolint:unparam // objs 는 오늘 모든 호출에서 비어 있다 — 파드·DaemonSet 을 미리 채우는 시험이 같은 헬퍼를 쓰도록 열어 둔 인자라서
func newRbllnsTestReconciler(objs ...client.Object) *NPUClusterPolicyReconciler {
	s := sharingScheme()
	return &NPUClusterPolicyReconciler{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build(), Scheme: s}
}

func rbllnsTestPolicy(image string) *npuv1alpha1.NPUClusterPolicy {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Rebellions.Enabled = true
	p.Spec.Rebellions.DevicePluginImage = image
	p.Spec.Rebellions.Namespace = naming.KubeSystemNamespace
	return p
}

// rbllnsRenderDS 는 ensureRebellionsDevicePlugin 을 돌려 만든 DaemonSet 을 읽는다.
func rbllnsRenderDS(t *testing.T, r *NPUClusterPolicyReconciler, p *npuv1alpha1.NPUClusterPolicy) appsv1.DaemonSet {
	t.Helper()
	if err := r.ensureRebellionsDevicePlugin(context.Background(), p); err != nil {
		t.Fatalf("ensureRebellionsDevicePlugin: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: rbllnsDaemonSetName, Namespace: naming.KubeSystemNamespace}, &ds); err != nil {
		t.Fatalf("DaemonSet 이 생성되지 않았다: %v", err)
	}
	return ds
}

func rbllnsVolumeNames(vs []corev1.Volume) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

func rbllnsEnvMap(c corev1.Container) map[string]string {
	out := map[string]string{}
	for _, e := range c.Env {
		out[e.Name] = e.Value
	}
	return out
}

func TestRebellionsDSV03Render(t *testing.T) {
	t.Setenv(HostExecImageEnv, rbllnsTestHostExec)
	ds := rbllnsRenderDS(t, newRbllnsTestReconciler(), rbllnsTestPolicy(rbllnsTestImageV03))
	spec := ds.Spec.Template.Spec
	if !spec.HostNetwork {
		t.Error("v0.3 은 hostNetwork 를 쓴다")
	}
	c := spec.Containers[0]
	if !reflect.DeepEqual(c.Args, []string{"--log-dir=device-plugin", "--log-level=10"}) {
		t.Errorf("인자 %v", c.Args)
	}
	wantEnv := []corev1.EnvVar{{Name: "PATH",
		Value: "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/host/usr/bin:/host/driver/usr/bin"}}
	if !reflect.DeepEqual(c.Env, wantEnv) {
		t.Errorf("환경변수 %v", c.Env)
	}
	if c.LivenessProbe != nil {
		t.Error("v0.3 에 livenessProbe 가 붙었다")
	}
	wantVols := []string{"devicesock", "plugins-registry", "log", "config-volume", "device-info",
		"host-usr-bin", "host-driver-usr-bin", "host-dev", "host-sys", rbllnsCDIVolumeName}
	if got := rbllnsVolumeNames(spec.Volumes); !reflect.DeepEqual(got, wantVols) {
		t.Errorf("볼륨 %v, want %v", got, wantVols)
	}
	cdi := spec.Volumes[len(spec.Volumes)-1].HostPath
	if cdi == nil || cdi.Path != "/var/run/cdi" || cdi.Type == nil || *cdi.Type != corev1.HostPathDirectoryOrCreate {
		t.Errorf("cdi 볼륨 %+v", cdi)
	}
	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Name != rbllnsPrepContainerName ||
		spec.InitContainers[0].Image != rbllnsTestHostExec {
		t.Fatalf("init container %+v", spec.InitContainers)
	}
	if env := rbllnsEnvMap(spec.InitContainers[0]); env["GEN"] != rbllnsGenV03 || env["CDI_KIND"] != "rebellions.ai/npu" {
		t.Errorf("init env %v", env)
	}
}

func TestRebellionsDSV04Render(t *testing.T) {
	t.Setenv(HostExecImageEnv, rbllnsTestHostExec)
	ds := rbllnsRenderDS(t, newRbllnsTestReconciler(), rbllnsTestPolicy(rbllnsTestImageV04))
	spec := ds.Spec.Template.Spec
	if spec.HostNetwork {
		t.Error("v0.4 는 hostNetwork 를 쓰지 않는다")
	}
	c := spec.Containers[0]
	if len(c.Args) != 0 || len(c.Command) != 0 {
		t.Errorf("v0.4 는 이미지 진입점만 쓴다: command=%v args=%v", c.Command, c.Args)
	}
	if !reflect.DeepEqual(c.Env, []corev1.EnvVar{{Name: "USE_GENERIC_RESOURCE_NAME", Value: "false"}}) {
		t.Errorf("환경변수 %v", c.Env)
	}
	p := c.LivenessProbe
	if p == nil || p.GRPC == nil || p.GRPC.Port != 51515 || p.GRPC.Service == nil || *p.GRPC.Service != "liveness" ||
		p.PeriodSeconds != 10 || p.FailureThreshold != 3 {
		t.Errorf("livenessProbe %+v", p)
	}
	mounts := map[string]string{}
	for _, m := range c.VolumeMounts {
		mounts[m.Name] = m.MountPath
	}
	wantMounts := map[string]string{"devicesock": "/var/lib/kubelet/device-plugins", "host-dev": "/dev",
		"host-usr-bin": "/host/usr/bin", "host-driver-usr-bin": "/host/driver/usr/bin"}
	if !reflect.DeepEqual(mounts, wantMounts) {
		t.Errorf("마운트 %v, want %v", mounts, wantMounts)
	}
	wantVols := []string{"devicesock", "host-dev", "host-usr-bin", "host-driver-usr-bin", rbllnsCDIVolumeName}
	if got := rbllnsVolumeNames(spec.Volumes); !reflect.DeepEqual(got, wantVols) {
		t.Errorf("볼륨 %v, want %v", got, wantVols)
	}
	if len(spec.InitContainers) != 1 || rbllnsEnvMap(spec.InitContainers[0])["GEN"] != rbllnsGenV04 {
		t.Fatalf("init container %+v", spec.InitContainers)
	}
}

// 구판은 resourcePrefix 를 CDI vendor 로 쓴다 — 바꾼 prefix 가 init container 의 종류로 이어져야 한다.
func TestRebellionsDSV03CustomPrefixKind(t *testing.T) {
	t.Setenv(HostExecImageEnv, rbllnsTestHostExec)
	p := rbllnsTestPolicy(rbllnsTestImageV03)
	p.Spec.Rebellions.ResourcePrefix = "example.com"
	ds := rbllnsRenderDS(t, newRbllnsTestReconciler(), p)
	if got := rbllnsEnvMap(ds.Spec.Template.Spec.InitContainers[0])["CDI_KIND"]; got != "example.com/npu" {
		t.Errorf("CDI_KIND=%s, want example.com/npu", got)
	}
}

// HOST_EXEC_IMAGE 가 비면 init container 없이 렌더링한다(이미지가 빈 init container 는 API 가 거절).
func TestRebellionsDSWithoutHostExecImage(t *testing.T) {
	t.Setenv(HostExecImageEnv, "")
	ds := rbllnsRenderDS(t, newRbllnsTestReconciler(), rbllnsTestPolicy(rbllnsTestImageV03))
	if n := len(ds.Spec.Template.Spec.InitContainers); n != 0 {
		t.Errorf("init container %d 개", n)
	}
}

// 두 판 모두 컨테이너·init container 의 마운트마다 볼륨이 있어야 한다(fake client 는 검사하지 않는다).
func TestRebellionsDSAllMountsHaveVolumes(t *testing.T) {
	t.Setenv(HostExecImageEnv, rbllnsTestHostExec)
	for _, image := range []string{rbllnsTestImageV03, rbllnsTestImageV04} {
		ds := rbllnsRenderDS(t, newRbllnsTestReconciler(), rbllnsTestPolicy(image))
		have := map[string]bool{}
		for _, v := range ds.Spec.Template.Spec.Volumes {
			have[v.Name] = true
		}
		all := append(append([]corev1.Container{}, ds.Spec.Template.Spec.InitContainers...),
			ds.Spec.Template.Spec.Containers...)
		for _, c := range all {
			for _, m := range c.VolumeMounts {
				if !have[m.Name] {
					t.Errorf("%s: %s 의 마운트 %q 에 볼륨이 없다", image, c.Name, m.Name)
				}
			}
		}
	}
}

// v0.3 으로 만든 DaemonSet 이 이미지 태그 변경만으로 v0.4 형식으로 바뀐다.
func TestRebellionsDSGenerationSwitchUpdates(t *testing.T) {
	t.Setenv(HostExecImageEnv, rbllnsTestHostExec)
	r := newRbllnsTestReconciler()
	p := rbllnsTestPolicy(rbllnsTestImageV03)
	_ = rbllnsRenderDS(t, r, p)
	p.Spec.Rebellions.DevicePluginImage = rbllnsTestImageV04
	ds := rbllnsRenderDS(t, r, p)
	if ds.Spec.Template.Spec.HostNetwork || len(ds.Spec.Template.Spec.Containers[0].Args) != 0 {
		t.Error("태그를 v0.4.3 으로 바꿨는데 v0.4 형식으로 갱신되지 않았다")
	}
}

// v0.4 는 /etc/pcidp ConfigMap 을 만들지 않는다. v0.3 은 만든다.
func TestRbllnsConfigMapByGeneration(t *testing.T) {
	for _, tc := range []struct {
		image string
		want  bool
	}{{rbllnsTestImageV03, true}, {rbllnsTestImageV04, false}} {
		r := newRbllnsTestReconciler()
		if err := r.ensureRbllnsConfigMap(context.Background(), rbllnsTestPolicy(tc.image)); err != nil {
			t.Fatalf("ensureRbllnsConfigMap: %v", err)
		}
		var cm corev1.ConfigMap
		err := r.Get(context.Background(), types.NamespacedName{
			Name: rbllnsConfigMapNameDefault, Namespace: naming.KubeSystemNamespace}, &cm)
		if got := err == nil; got != tc.want {
			t.Errorf("%s: ConfigMap 존재=%v, want %v (err=%v)", tc.image, got, tc.want, err)
		}
		if err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("%s: 예상하지 못한 오류 %v", tc.image, err)
		}
	}
}
