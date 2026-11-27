// ============================================================
// webhook_test.go: admission webhook 핸들러 단위 테스트
// 상세: DIP/NCP 거부·통과, Pod 주입/무개입 케이스
// 생성일: 2026-07-20 | 수정일: 2026-10-27
// ============================================================

package webhook

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/metrics"
)

func goodDIP() *npuv1alpha1.DriverInstallPolicy {
	return &npuv1alpha1.DriverInstallPolicy{
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "nvidia",
			Driver: npuv1alpha1.DriverSpec{
				Image: "registry.example.com:5000/kcloud/nvidia-driver-ds:580.159.03-v179",
				Mode:  "job",
			},
			UpgradePolicy: &npuv1alpha1.UpgradePolicy{
				MaxReboots:        1,
				ValidationTimeout: "10m",
			},
		},
	}
}

func TestValidateDIP(t *testing.T) {
	const modeJob = "job"
	cases := []struct {
		name    string
		mutate  func(*npuv1alpha1.DriverInstallPolicy)
		wantErr bool
	}{
		{"valid", func(*npuv1alpha1.DriverInstallPolicy) {}, false},
		{"unknown vendor", func(d *npuv1alpha1.DriverInstallPolicy) { d.Spec.Vendor = "banana" }, true},
		{"empty image", func(d *npuv1alpha1.DriverInstallPolicy) { d.Spec.Driver.Image = "" }, true},
		{"image no tag", func(d *npuv1alpha1.DriverInstallPolicy) { d.Spec.Driver.Image = "repo/nvidia-driver-ds" }, true},
		{"image empty tag", func(d *npuv1alpha1.DriverInstallPolicy) { d.Spec.Driver.Image = "repo/x:" }, true},
		{"bad duration", func(d *npuv1alpha1.DriverInstallPolicy) { d.Spec.UpgradePolicy.ValidationTimeout = "10x" }, true},
		{"trackOnly non-job", func(d *npuv1alpha1.DriverInstallPolicy) {
			d.Spec.Driver.TrackOnly = true
			d.Spec.Driver.Mode = "daemonset"
		}, true},
		{"trackOnly job ok", func(d *npuv1alpha1.DriverInstallPolicy) {
			d.Spec.Driver.TrackOnly = true
			d.Spec.Driver.Mode = modeJob
		}, false},
		{"trackOnly job no image ok", func(d *npuv1alpha1.DriverInstallPolicy) {
			d.Spec.Driver.TrackOnly = true
			d.Spec.Driver.Mode = modeJob
			d.Spec.Driver.Image = ""
		}, false},
		{"trackOnly job bad image still rejected", func(d *npuv1alpha1.DriverInstallPolicy) {
			d.Spec.Driver.TrackOnly = true
			d.Spec.Driver.Mode = modeJob
			d.Spec.Driver.Image = "repo/x:"
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := goodDIP()
			c.mutate(d)
			err := validateDIP(d)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got err=%v", c.wantErr, err)
			}
		})
	}

	// 거부가 kind 별 카운터를 올리는지 — 기록은 ValidateCreate 반환부에 있다(validateDIP 는 순수 판정).
	// 지표는 프로세스 전역이라 델타로 본다: 거부 1 + 통과 1 => 증가 1(통과는 세지 않는다).
	t.Run("거부 지표", func(t *testing.T) {
		bad := goodDIP()
		bad.Spec.Vendor = "notavendor"
		before := testutil.ToFloat64(metrics.WebhookRejections.WithLabelValues("DriverInstallPolicy"))
		if _, err := (&DIPValidator{}).ValidateCreate(context.Background(), bad); err == nil {
			t.Fatal("잘못된 vendor 는 거부되어야 한다")
		}
		if _, err := (&DIPValidator{}).ValidateCreate(context.Background(), goodDIP()); err != nil {
			t.Fatalf("정상 DIP 는 통과해야 한다: %v", err)
		}
		if got := testutil.ToFloat64(metrics.WebhookRejections.WithLabelValues("DriverInstallPolicy")) - before; got != 1 {
			t.Errorf("거부 카운터 증가 = %v, want 1 — ValidateCreate 가 거부를 세지 않는다", got)
		}
	})
}

func TestValidateNCP(t *testing.T) {
	good := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{Enabled: true, DevicePluginImage: "repo/dp:v1"},
		},
	}
	if err := validateNCP(good); err != nil {
		t.Fatalf("valid NCP rejected: %v", err)
	}
	bad := good.DeepCopy()
	bad.Spec.Nvidia.DevicePluginImage = ""
	if err := validateNCP(bad); err == nil {
		t.Fatalf("expected rejection for enabled vendor with empty image")
	}
	// disabled vendor with empty image is fine.
	disabled := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{Enabled: false},
		},
	}
	if err := validateNCP(disabled); err != nil {
		t.Fatalf("disabled vendor should pass: %v", err)
	}
	// external vendor with empty image is fine — kcloud only observes, no data plane to image.
	external := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled:        true,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "external"},
			},
		},
	}
	if err := validateNCP(external); err != nil {
		t.Fatalf("external vendor with empty image should pass: %v", err)
	}
	// furiosa 가 external 이면 rngd 도 그 모드를 따라 이미지 없이 통과한다(rngd 는 자체 관리 모드가 없다).
	rngdExternal := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Enabled:        false,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "external"},
				Rngd:           npuv1alpha1.RngdSpec{Enabled: true}, // devicePluginImage 비어 있음
			},
		},
	}
	if err := validateNCP(rngdExternal); err != nil {
		t.Fatalf("external furiosa 의 rngd should pass with empty image: %v", err)
	}
	// managed 인 rngd 가 이미지 없이 켜지면 여전히 거절된다(기존 동작 보존).
	rngdManaged := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Rngd: npuv1alpha1.RngdSpec{Enabled: true}, // managed, image empty
			},
		},
	}
	if err := validateNCP(rngdManaged); err == nil {
		t.Fatalf("managed rngd with empty image should be rejected")
	}
	// delegated vendor with empty image is fine — kcloud 는 배포하지 않고 벤더 operator 가 맡는다.
	// 벤더 operator 감지 여부는 컨트롤러의 몫이라 webhook 은 이미지를 요구하지 않는다.
	delegated := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{
				Enabled:        true,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
			},
		},
	}
	if err := validateNCP(delegated); err != nil {
		t.Fatalf("delegated vendor with empty image should pass: %v", err)
	}
	// furiosa 가 delegated 면 rngd 도 그 모드를 따라 이미지 없이 통과한다(rngd 는 자체 관리 모드가 없다).
	rngdDelegated := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{
				Enabled:        false,
				ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				Rngd:           npuv1alpha1.RngdSpec{Enabled: true}, // devicePluginImage 비어 있음
			},
		},
	}
	if err := validateNCP(rngdDelegated); err != nil {
		t.Fatalf("delegated furiosa 의 rngd should pass with empty image: %v", err)
	}
}

// 오타 등으로 enum 밖 문자열이 들어오면 세 도우미(IsOperatorOwned·IsDelegated·IsExternal)가
// 모두 거짓이 된다 — CRD 스키마가 보통은 막지만, 이 함수는 struct 를 직접 구성해 그 경로를
// 우회하는 호출자(테스트 포함)에도 안전해야 한다. 조용히 managed 로 읽으면 이미지 없이도
// 통과해 배포가 은근슬쩍 멈추는 사고로 이어진다(컨트롤러 IsOperatorOwned() 도 거짓이 됨).
func TestWebhookRejectsUnknownManagementMode(t *testing.T) {
	for _, vendor := range []string{"nvidia", "furiosa", "rebellions", "tenstorrent"} {
		t.Run(vendor, func(t *testing.T) {
			ncp := &npuv1alpha1.NPUClusterPolicy{}
			mode := npuv1alpha1.ManagementSpec{ManagementMode: "mangaged"} // 오타
			switch vendor {
			case "nvidia":
				ncp.Spec.Nvidia = npuv1alpha1.NvidiaSpec{Enabled: true, DevicePluginImage: "repo/dp:v1", ManagementSpec: mode}
			case "furiosa":
				ncp.Spec.Furiosa = npuv1alpha1.FuriosaSpec{Enabled: true, DevicePluginImage: "repo/dp:v1", ManagementSpec: mode}
			case "rebellions":
				ncp.Spec.Rebellions = npuv1alpha1.RebellionsSpec{Enabled: true, DevicePluginImage: "repo/dp:v1", ManagementSpec: mode}
			case "tenstorrent":
				ncp.Spec.Tenstorrent = npuv1alpha1.TenstorrentSpec{Enabled: true, DevicePluginImage: "repo/dp:v1", ManagementSpec: mode}
			}
			if err := validateNCP(ncp); err == nil {
				t.Fatalf("managementMode 오타(%q)가 조용히 통과했다", "mangaged")
			}
		})
	}
}

func gpuPod(labels map[string]string, gpu bool, rc string) *corev1.Pod {
	return resourcePod(labels, map[string]bool{"nvidia.com/gpu": gpu}, rc)
}

// resourcePod 는 요청 리소스명을 골라 쓸 수 있는 Pod 이다(true 인 이름만 limits 에 담는다).
func resourcePod(labels map[string]string, want map[string]bool, rc string) *corev1.Pod {
	c := corev1.Container{Name: "c"}
	for name, on := range want {
		if !on {
			continue
		}
		if c.Resources.Limits == nil {
			c.Resources.Limits = corev1.ResourceList{}
		}
		c.Resources.Limits[corev1.ResourceName(name)] = resource.MustParse("1")
	}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{c}},
	}
	if rc != "" {
		p.Spec.RuntimeClassName = &rc
	}
	return p
}

func TestPodMutator(t *testing.T) {
	m := &PodMutator{}
	optIn := map[string]string{InjectLabel: "true"}

	// opt-in + GPU + no runtimeClass → inject nvidia
	p := gpuPod(optIn, true, "")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName == nil || *p.Spec.RuntimeClassName != nvidiaRuntimeClass {
		t.Fatalf("expected nvidia runtimeClass injected, got %v", p.Spec.RuntimeClassName)
	}

	// no label → untouched
	p = gpuPod(nil, true, "")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName != nil {
		t.Fatalf("non-labeled pod must not be mutated, got %v", *p.Spec.RuntimeClassName)
	}

	// opt-in but no GPU → untouched
	p = gpuPod(optIn, false, "")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName != nil {
		t.Fatalf("non-GPU pod must not be mutated")
	}

	// opt-in + GPU but explicit runtimeClass → preserved
	p = gpuPod(optIn, true, "custom")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if *p.Spec.RuntimeClassName != "custom" {
		t.Fatalf("existing runtimeClass must be preserved, got %v", *p.Spec.RuntimeClassName)
	}
}

// mixed MIG 조각(nvidia.com/mig-*)도 GPU 요청이다. 리터럴 nvidia.com/gpu 만 보면 MIG Pod 이
// runtimeClass 없이 떠서 /dev/nvidia* 를 못 받는데도 Ready 가 된다.
func TestPodMutatorInjectsForMIGAndInitContainerResources(t *testing.T) {
	m := &PodMutator{}
	optIn := map[string]string{InjectLabel: "true"}

	p := resourcePod(optIn, map[string]bool{"nvidia.com/mig-1g.6gb": true}, "")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName == nil || *p.Spec.RuntimeClassName != nvidiaRuntimeClass {
		t.Fatalf("MIG resource must inject nvidia runtimeClass, got %v", p.Spec.RuntimeClassName)
	}

	// requests 만, 그리고 initContainer 에만 있는 경우도 같은 경로다.
	p = resourcePod(optIn, nil, "")
	p.Spec.InitContainers = []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceName("nvidia.com/mig-2g.12gb"): resource.MustParse("1")}}}}
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName == nil || *p.Spec.RuntimeClassName != nvidiaRuntimeClass {
		t.Fatalf("initContainer request must inject nvidia runtimeClass, got %v", p.Spec.RuntimeClassName)
	}

	// 다른 벤더는 여전히 무개입.
	p = resourcePod(optIn, map[string]bool{"furiosa.ai/rngd": true}, "")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName != nil {
		t.Fatalf("non-nvidia resource must not be mutated, got %v", *p.Spec.RuntimeClassName)
	}
}

// injectScope=namespace 에서는 Pod 라벨이 없어도 nvidia 요청 Pod 에 주입한다. 네임스페이스 선택은
// admission namespaceSelector 가 하므로 mutator 는 라벨을 보지 않는다. nvidia 자원·빈
// runtimeClassName 조건은 pod 범위와 같다.
func TestPodMutatorNamespaceScopeIgnoresPodLabel(t *testing.T) {
	m := &PodMutator{Scope: InjectScopeNamespace}

	p := gpuPod(nil, true, "")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName == nil || *p.Spec.RuntimeClassName != nvidiaRuntimeClass {
		t.Fatalf("namespace 범위는 라벨 없는 GPU Pod 에도 주입해야 한다, got %v", p.Spec.RuntimeClassName)
	}

	p = resourcePod(nil, map[string]bool{"furiosa.ai/rngd": true}, "")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName != nil {
		t.Fatalf("nvidia 자원이 없는 Pod 는 무개입이어야 한다, got %v", *p.Spec.RuntimeClassName)
	}

	p = gpuPod(nil, true, "custom")
	if err := m.Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if *p.Spec.RuntimeClassName != "custom" {
		t.Fatalf("명시한 runtimeClass 는 유지해야 한다, got %v", *p.Spec.RuntimeClassName)
	}

	// pod 범위(빈 Scope)는 라벨 없는 Pod 에 개입하지 않는다 — 기본 동작이 바뀌지 않았는지 같은 입력으로 본다.
	p = gpuPod(nil, true, "")
	if err := (&PodMutator{}).Default(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.RuntimeClassName != nil {
		t.Fatalf("pod 범위는 라벨 없는 Pod 에 개입하면 안 된다, got %v", *p.Spec.RuntimeClassName)
	}
}

func TestNormalizeInjectScope(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		wantErr  bool
	}{
		{"", InjectScopePod, false},
		{"pod", InjectScopePod, false},
		{"namespace", InjectScopeNamespace, false},
		{"Pod", "", true},
		{"cluster", "", true},
	} {
		got, err := NormalizeInjectScope(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("NormalizeInjectScope(%q) = (%q, %v), want (%q, err=%v)", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}
