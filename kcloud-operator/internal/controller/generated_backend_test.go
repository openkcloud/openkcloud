// ============================================================
// generated_backend_test.go: 생성된 backend 배포물 렌더 시험
// 상세: 설정이 ConfigMap 으로 들어가고 DaemonSet 이 그것을 읽는지, 그리고 노드 선택과
// control-plane 배제가 붙는지 고정한다. 배제를 빠뜨리면 장치 없는 노드에서 벤더 prestart 가
// 고착된다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/descriptor"
)

func testDescriptor() *npuv1alpha1.AcceleratorDescriptor {
	return &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor:         "rngd",
			Product:        "rngd",
			AllocationUnit: npuv1alpha1.AllocationUnitDevice,
			Identity: npuv1alpha1.DescriptorIdentity{
				Source:             npuv1alpha1.IdentitySourcePCIAddress,
				StableAcrossReboot: true,
			},
			DeviceNodes: []string{"/dev/rngd/{{id}}*"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
		},
	}
}

func TestRenderGeneratedDevicePlugin(t *testing.T) {
	cm, ds := renderGeneratedDevicePlugin(testDescriptor(), []byte(`{"resourceName":"rngd/rngd"}`),
		"reg/generic-device-plugin:0.1.0", "kube-system")

	if cm.Data["backend.json"] != `{"resourceName":"rngd/rngd"}` {
		t.Errorf("설정이 ConfigMap 에 안 들어갔다: %v", cm.Data)
	}
	if ds.Spec.Template.Spec.Containers[0].Image != "reg/generic-device-plugin:0.1.0" {
		t.Errorf("이미지가 다르다: %s", ds.Spec.Template.Spec.Containers[0].Image)
	}

	// 장치가 있는 노드에만 간다.
	if ds.Spec.Template.Spec.NodeSelector["kcloud.ai/rngd.present"] != "true" { //nolint:goconst // driver_daemonset_controller.go 의 무관한 셸 `"true"` 관용구와 값이 겹쳐 goconst 임계치에 걸린다
		t.Errorf("벤더 present 셀렉터가 없다: %v", ds.Spec.Template.Spec.NodeSelector)
	}

	// control-plane 배제. 빠뜨리면 장치 없는 master 에서 파드가 남는다.
	found := false
	for _, tol := range ds.Spec.Template.Spec.Tolerations {
		if strings.Contains(tol.Key, "control-plane") {
			found = true
		}
	}
	if found {
		t.Errorf("control-plane 을 허용하면 안 된다: %+v", ds.Spec.Template.Spec.Tolerations)
	}

	// 설정과 호스트 /dev, kubelet 소켓 디렉터리가 붙어야 동작한다.
	var haveCfg, haveDev, haveSock bool
	for _, v := range ds.Spec.Template.Spec.Volumes {
		switch {
		case v.ConfigMap != nil && v.ConfigMap.Name == cm.Name:
			haveCfg = true
		case v.HostPath != nil && v.HostPath.Path == "/dev":
			haveDev = true
		case v.HostPath != nil && strings.Contains(v.HostPath.Path, "device-plugins"):
			haveSock = true
		}
	}
	if !haveCfg || !haveDev || !haveSock {
		t.Errorf("볼륨이 빠졌다 cfg=%v dev=%v sock=%v", haveCfg, haveDev, haveSock)
	}
}

// descriptor 이름이 배포물 이름에 들어가야 여러 descriptor 가 공존한다.
func TestRenderGeneratedDevicePluginNamesAreScoped(t *testing.T) {
	_, ds := renderGeneratedDevicePlugin(testDescriptor(), []byte("{}"), "img", "kube-system")
	if !strings.Contains(ds.Name, "rngd") {
		t.Errorf("배포물 이름에 descriptor 이름이 없다: %s", ds.Name)
	}
}

// kubelet 소켓·plugin 디렉터리는 root 소유 0755 다. nonroot 로 뜨면 소켓을 열지 못하고
// 컨테이너가 죽는다 — 증상은 `bind: permission denied` 다.
// privileged 만으로는 UID 가 바뀌지 않으므로 두 backend 모두 UID 0 을 명시해야 한다.
func TestRenderGeneratedRunsAsRoot(t *testing.T) {
	_, dpDS := renderGeneratedDevicePlugin(testDescriptor(), []byte("{}"), "img", "kcloud")
	_, draDS, _ := renderGeneratedDRADriver(testDescriptor(), []byte("{}"), "img", "kcloud")

	for name, ds := range map[string]*appsv1.DaemonSet{"device-plugin": dpDS, "dra": draDS} {
		sc := ds.Spec.Template.Spec.Containers[0].SecurityContext
		if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 0 {
			t.Errorf("%s 가 root 로 뜨지 않는다: %+v", name, sc)
		}
	}
}

func TestRenderGeneratedDRADriver(t *testing.T) {
	d := testDescriptor()
	d.Spec.Backends = []string{npuv1alpha1.BackendDRA}

	cm, ds, dc := renderGeneratedDRADriver(d, []byte(`{"driverName":"x"}`),
		"reg/generic-dra-driver:0.1.0", "kcloud")

	if cm.Data["backend.json"] == "" {
		t.Error("설정이 ConfigMap 에 없다")
	}
	if ds.Spec.Template.Spec.Containers[0].Image != "reg/generic-dra-driver:0.1.0" {
		t.Errorf("이미지가 다르다: %s", ds.Spec.Template.Spec.Containers[0].Image)
	}
	if dc.Spec.Selectors == nil && dc.Name == "" {
		t.Error("DeviceClass 가 비었다")
	}
	// kubelet plugin 등록 경로와 CDI 디렉터리가 붙어야 주입이 된다.
	var haveRegistrar, havePlugins, haveCDI bool
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.HostPath == nil {
			continue
		}
		switch {
		case strings.Contains(v.HostPath.Path, "plugins_registry"):
			haveRegistrar = true
		case strings.HasSuffix(v.HostPath.Path, "/plugins"):
			havePlugins = true
		case strings.Contains(v.HostPath.Path, "cdi"):
			haveCDI = true
		}
	}
	if !haveRegistrar || !havePlugins || !haveCDI {
		t.Errorf("볼륨이 빠졌다 registrar=%v plugins=%v cdi=%v", haveRegistrar, havePlugins, haveCDI)
	}
}

// 배포물에 RBAC 이 없으면 드라이버가 뜨고도 ResourceSlice 를 발행하지 못하고,
// 노드에 장치가 하나도 나오지 않는다. 실패가 조용해서 특히 나쁘다.
func TestRenderGeneratedDRARBACGrantsResourceSlices(t *testing.T) {
	desc := testDescriptor()
	sa, cr, crb := renderGeneratedDRARBAC(desc, "kcloud")
	_, ds, _ := renderGeneratedDRADriver(desc, []byte("{}"), "img", "kcloud")

	if ds.Spec.Template.Spec.ServiceAccountName != sa.Name {
		t.Errorf("DaemonSet 이 전용 ServiceAccount 를 쓰지 않는다: %q", ds.Spec.Template.Spec.ServiceAccountName)
	}
	if crb.Subjects[0].Name != sa.Name || crb.RoleRef.Name != cr.Name {
		t.Errorf("바인딩이 어긋난다: subject=%q roleRef=%q", crb.Subjects[0].Name, crb.RoleRef.Name)
	}

	var canPublish bool
	for _, r := range cr.Rules {
		for _, res := range r.Resources {
			if res != "resourceslices" {
				continue
			}
			var create, update bool
			for _, v := range r.Verbs {
				create = create || v == "create"
				update = update || v == "update"
			}
			canPublish = create && update
		}
	}
	if !canPublish {
		t.Errorf("ResourceSlice 발행 권한이 없다: %+v", cr.Rules)
	}
}

// TestGeneratedDRAConfigMapCarriesDescriptorDeclarations 는 descriptor 에 적은 속성·capacity 가
// 생성 ConfigMap 까지 이어지는지, 적지 않으면 그 키가 ConfigMap 에 아예 없는지 본다.
// 빈 맵을 실으면 드라이버가 그것을 선언으로 읽어 "선언했는데 비었다" 와 "선언하지 않았다" 가
// 구분되지 않는다. 컴파일과 렌더를 함께 돌려 descriptor 에서 ConfigMap 까지 끝까지 본다.
func TestGeneratedDRAConfigMapCarriesDescriptorDeclarations(t *testing.T) {
	render := func(t *testing.T, d *npuv1alpha1.AcceleratorDescriptor) string {
		t.Helper()
		cfg, err := descriptor.CompileDRA(d.Spec)
		if err != nil {
			t.Fatalf("CompileDRA 실패: %v", err)
		}
		cm, _, _ := renderGeneratedDRADriver(d, cfg, "reg/generic-dra-driver:0.1.0", "kcloud")
		return cm.Data["backend.json"]
	}

	t.Run("선언하면 ConfigMap 에 나타난다", func(t *testing.T) {
		d := testDescriptor()
		d.Spec.Backends = []string{npuv1alpha1.BackendDRA}
		d.Spec.Attributes = map[string]string{"productName": "RNGD"}
		d.Spec.Capacity = map[string]string{"memory": "48Gi"}
		d.Spec.CapacityFiles = map[string]string{"memory": "rngd/mem_total"}

		got := render(t, d)
		for _, want := range []string{
			`"attributes":{"productName":"RNGD"}`,
			`"capacity":{"memory":"48Gi"}`,
			`"capacityFiles":{"memory":"rngd/mem_total"}`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("선언이 ConfigMap 에 없다: %s 를 찾지 못함\n%s", want, got)
			}
		}
	})

	t.Run("선언이 없으면 그 키가 없다", func(t *testing.T) {
		d := testDescriptor()
		d.Spec.Backends = []string{npuv1alpha1.BackendDRA}
		// 빈 맵을 명시한 경우도 선언 없음과 같이 다뤄야 한다.
		d.Spec.Attributes = map[string]string{}

		got := render(t, d)
		for _, key := range []string{"attributes", "capacity", "capacityFiles"} {
			if strings.Contains(got, `"`+key+`"`) {
				t.Errorf("선언하지 않은 %q 키가 ConfigMap 에 있다\n%s", key, got)
			}
		}
	})
}
