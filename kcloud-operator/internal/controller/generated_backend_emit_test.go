// ============================================================
// generated_backend_emit_test.go: 배포물 발행 facade 시험
// 상세: 라이브 재현에 쓰는 경로다. 손으로 쓴 YAML 을 쓰지 않으려면 발행이 코드여야 한다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func rngdDescriptor() *npuv1alpha1.AcceleratorDescriptor {
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
			Backends: []string{
				npuv1alpha1.BackendDevicePlugin, npuv1alpha1.BackendDRA,
			},
		},
	}
}

func TestRenderGeneratedBackendsCoversBothBackends(t *testing.T) {
	objs, err := RenderGeneratedBackends(
		rngdDescriptor(), "dp:v1", "dra:v1", "kcloud")
	if err != nil {
		t.Fatalf("발행 실패: %v", err)
	}
	kinds := map[string]int{}
	for _, o := range objs {
		kinds[o.GetObjectKind().GroupVersionKind().Kind]++
	}
	// device-plugin: ConfigMap+DaemonSet, DRA: ConfigMap+DaemonSet+DeviceClass+SA+CR+CRB
	for k, want := range map[string]int{
		"ConfigMap": 2, "DaemonSet": 2, "DeviceClass": 1,
		"ServiceAccount": 1, "ClusterRole": 1, "ClusterRoleBinding": 1,
	} {
		if kinds[k] != want {
			t.Errorf("%s 가 %d개여야 한다: %d", k, want, kinds[k])
		}
	}

	// 종류만 세면 두 이미지가 뒤바뀌어도 통과한다. 어느 DaemonSet 에 어느 이미지가
	// 들어갔는지를 직접 본다 - 뒤바뀌면 DRA 드라이버 자리에 device-plugin 이 뜬다.
	images := map[string]string{}
	sas := map[string]string{}
	for _, o := range objs {
		ds, ok := o.(*appsv1.DaemonSet)
		if !ok {
			continue
		}
		images[ds.Labels["app.kubernetes.io/component"]] = ds.Spec.Template.Spec.Containers[0].Image
		sas[ds.Labels["app.kubernetes.io/component"]] = ds.Spec.Template.Spec.ServiceAccountName
	}
	if images["generated-device-plugin"] != "dp:v1" {
		t.Errorf("device-plugin 배포물의 이미지가 다르다: %q", images["generated-device-plugin"])
	}
	if images["generated-dra-driver"] != "dra:v1" {
		t.Errorf("DRA 배포물의 이미지가 다르다: %q", images["generated-dra-driver"])
	}
	// ServiceAccount 가 비면 드라이버는 Running 인데 ResourceSlice 가 하나도 안 나온다.
	if sas["generated-dra-driver"] == "" {
		t.Error("DRA DaemonSet 에 ServiceAccount 가 없다")
	}

	// 장치 발견이 런타임으로 넘어갔으므로, 컴파일 출력에는 descriptor 의 deviceNodes
	// 템플릿이 치환 없이 그대로 실려야 한다 - 여기서 어긋나면 런타임이 장치를 못 찾는다.
	var cfg struct {
		DeviceNodes []string `json:"deviceNodes"`
	}
	for _, o := range objs {
		cm, ok := o.(*corev1.ConfigMap)
		if !ok {
			continue
		}
		if err := json.Unmarshal([]byte(cm.Data["backend.json"]), &cfg); err != nil {
			t.Fatalf("%s 의 backend.json 파싱 실패: %v", cm.Name, err)
		}
		if len(cfg.DeviceNodes) != 1 || cfg.DeviceNodes[0] != "/dev/rngd/{{id}}*" {
			t.Errorf("%s 의 deviceNodes 가 descriptor 템플릿과 다르다: %v", cm.Name, cfg.DeviceNodes)
		}
	}
}

// 선언하지 않은 backend 의 배포물은 나오지 않는다.
func TestRenderGeneratedBackendsHonorsDeclaredBackends(t *testing.T) {
	d := rngdDescriptor()
	d.Spec.Backends = []string{npuv1alpha1.BackendDevicePlugin}
	objs, err := RenderGeneratedBackends(d, "dp:v1", "dra:v1", "kcloud")
	if err != nil {
		t.Fatalf("발행 실패: %v", err)
	}
	for _, o := range objs {
		if o.GetObjectKind().GroupVersionKind().Kind == "DeviceClass" {
			t.Fatal("선언하지 않은 DRA backend 의 배포물이 나왔다")
		}
	}
}

// sharingDescriptor 는 sharing(replicas 2)을 선언한 device-plugin 전용 descriptor 다.
func sharingDescriptor(vendor, product string) *npuv1alpha1.AcceleratorDescriptor {
	return &npuv1alpha1.AcceleratorDescriptor{
		ObjectMeta: metav1.ObjectMeta{Name: vendor + "-share"},
		Spec: npuv1alpha1.AcceleratorDescriptorSpec{
			Vendor:         vendor,
			Product:        product,
			AllocationUnit: npuv1alpha1.AllocationUnitDevice,
			Identity: npuv1alpha1.DescriptorIdentity{
				Source:             npuv1alpha1.IdentitySourcePCIAddress,
				StableAcrossReboot: true,
			},
			DeviceNodes: []string{"/dev/" + vendor + "/{{id}}"},
			Backends:    []string{npuv1alpha1.BackendDevicePlugin},
			Sharing:     &npuv1alpha1.DescriptorSharing{Mode: npuv1alpha1.SharingModeTimeSliced, Replicas: 2},
		},
	}
}

// 발행 facade 도 sharing 게이트를 지난다. 컨트롤러는 Render 에 오기 전에 구조화된 refusal 로
// 막지만 cmd/backendtool 은 이 함수가 유일한 관문이라, 게이트가 여기 없으면 timeSlicing 이
// Unsupported 인 벤더의 replicas YAML 도 도구로 뽑힐 수 있다.
func TestRenderGeneratedBackendsRefusesSharingForUnsupportedVendor(t *testing.T) {
	_, err := RenderGeneratedBackends(sharingDescriptor("rngd", "rngd"), "dp:v3", "", "kcloud")
	if err == nil {
		t.Fatal("timeSlicing 이 Unsupported 인 rngd 는 sharing 렌더가 거절돼야 한다")
	}
	if !strings.Contains(err.Error(), "timeSlicing") {
		t.Fatalf("거절 사유에 timeSlicing 이 없다: %v", err)
	}
}

func TestRenderGeneratedBackendsAllowsSharingForSoftwareVendor(t *testing.T) {
	objs, err := RenderGeneratedBackends(sharingDescriptor("nvidia", "a30"), "dp:v3", "", "kcloud")
	if err != nil {
		t.Fatalf("timeSlicing 이 Software 인 nvidia 는 통과해야 한다: %v", err)
	}
	var cfg string
	for _, o := range objs {
		if cm, ok := o.(*corev1.ConfigMap); ok {
			cfg = cm.Data["backend.json"]
		}
	}
	if !strings.Contains(cfg, `"replicas":2`) {
		t.Fatalf("device-plugin 설정에 replicas 2 가 없다: %q", cfg)
	}
}
