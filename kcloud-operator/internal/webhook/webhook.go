// ============================================================
// webhook.go: Admission webhook 핸들러 (DIP/NCP/AcceleratorClass/AcceleratorWorkload/
// AcceleratorPartitionPolicy validating + Pod mutating)
// 상세: spec 검증 거부 + 추상 워크로드 변환 검증 + opt-in 라벨 Pod(injectScope=namespace 면 라벨
//       네임스페이스의 Pod 전부)에 NVIDIA runtimeClass 주입
// 생성일: 2026-07-20 | 수정일: 2026-10-27
// ============================================================

package webhook

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/metrics"
)

// recordRejection 은 검증이 거부(err != nil)를 돌려줄 때 kind 별 거부 카운터를 올리고
// 그 err 를 그대로 통과시킨다. 통과(nil)는 세지 않는다. Create/Update 반환부에서만 감싼다 —
// validateXXX 함수는 순수 판정으로 남긴다.
func recordRejection(kind string, err error) error {
	if err != nil {
		metrics.RecordWebhookRejection(kind)
	}
	return err
}

// InjectLabel: 이 라벨이 "true" 인 Pod 만 mutating 대상(opt-in). 그 외 Pod 무개입.
const InjectLabel = "kcloud.ai/inject"

// Pod mutating 의 대상 범위. 차트 값 webhook.injectScope 와 매니저 플래그 --webhook-inject-scope 가
// 같은 문자열을 쓴다.
const (
	// InjectScopePod: InjectLabel=true 인 Pod 만 대상(기본).
	InjectScopePod = "pod"
	// InjectScopeNamespace: InjectLabel=true 인 네임스페이스의 Pod 전부가 대상이다. 네임스페이스 선택은
	// MutatingWebhookConfiguration 의 namespaceSelector 가 맡고, PodMutator 는 Pod 라벨을 보지 않는다.
	InjectScopeNamespace = "namespace"
)

// NormalizeInjectScope 는 빈 값을 InjectScopePod 로 보고, 두 값 외에는 오류를 돌려준다.
func NormalizeInjectScope(s string) (string, error) {
	switch s {
	case "", InjectScopePod:
		return InjectScopePod, nil
	case InjectScopeNamespace:
		return InjectScopeNamespace, nil
	}
	return "", fmt.Errorf("webhook inject scope %q: %q 또는 %q 만 허용한다", s, InjectScopePod, InjectScopeNamespace)
}

// nvidiaResourcePrefix: NVIDIA device-plugin 이 광고하는 extended resource 접두.
// 전체 장치(nvidia.com/gpu)와 mixed MIG 조각(nvidia.com/mig-<profile>)이 모두 여기 걸린다 —
// 리터럴 nvidia.com/gpu 만 보면 MIG Pod 이 runtimeClass 없이 떠서 /dev/nvidia* 를 못 받는다.
const nvidiaResourcePrefix = "nvidia.com/"

// nvidiaRuntimeClass: 이 차트가 만드는 RuntimeClass 이름(deploy/helm/templates/runtimeclass.yaml).
const nvidiaRuntimeClass = "nvidia"

// knownVendors: DriverInstallPolicy 에서 허용하는 vendor 집합.
var knownVendors = map[string]bool{
	nvidiaRuntimeClass: true,
	"furiosa":          true,
	"tenstorrent":      true,
	"rebellions":       true,
}

// Setup 은 여섯 개의 admission webhook(DIP/NCP/AcceleratorClass/AcceleratorWorkload/
// AcceleratorPartitionPolicy validating, Pod mutating)을 매니저에 등록한다.
// WebhookConfiguration(외부)이 없으면 admission 호출이 서버에 도달하지 않으므로,
// webhook.enabled=false 배포에서는 무해하게 미사용 상태로 남는다. injectScope 는 Pod mutating 의
// 대상 범위(InjectScopePod·InjectScopeNamespace)다.
func Setup(mgr ctrl.Manager, injectScope string) error {
	scope, scopeErr := NormalizeInjectScope(injectScope)
	if scopeErr != nil {
		return scopeErr
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&npuv1alpha1.DriverInstallPolicy{}).
		WithValidator(&DIPValidator{}).
		Complete(); err != nil {
		return fmt.Errorf("register DriverInstallPolicy validator: %w", err)
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&npuv1alpha1.NPUClusterPolicy{}).
		WithValidator(&NCPValidator{}).
		Complete(); err != nil {
		return fmt.Errorf("register NPUClusterPolicy validator: %w", err)
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&npuv1alpha1.AcceleratorClass{}).
		WithValidator(&ACValidator{}).
		Complete(); err != nil {
		return fmt.Errorf("register AcceleratorClass validator: %w", err)
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&npuv1alpha1.AcceleratorWorkload{}).
		WithValidator(&AWValidator{Client: mgr.GetClient()}).
		Complete(); err != nil {
		return fmt.Errorf("register AcceleratorWorkload validator: %w", err)
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&npuv1alpha1.AcceleratorPartitionPolicy{}).
		WithValidator(&ACPPValidator{Client: mgr.GetClient()}).
		Complete(); err != nil {
		return fmt.Errorf("register AcceleratorPartitionPolicy validator: %w", err)
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&corev1.Pod{}).
		WithDefaulter(&PodMutator{Scope: scope}).
		Complete(); err != nil {
		return fmt.Errorf("register Pod mutator: %w", err)
	}
	return nil
}

// DIPValidator 는 DriverInstallPolicy 의 cross-field/의미 검증을 수행한다.
// (CRD structural schema 가 잡지 못하는 규칙만: vendor 화이트리스트, duration 파싱,
// trackOnly/mode 조합, 이미지 태그 형식.)
type DIPValidator struct{}

func (v *DIPValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, recordRejection("DriverInstallPolicy", validateDIP(obj))
}

func (v *DIPValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return nil, recordRejection("DriverInstallPolicy", validateDIP(newObj))
}

func (v *DIPValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateDIP(obj runtime.Object) error {
	dip, ok := obj.(*npuv1alpha1.DriverInstallPolicy)
	if !ok {
		return fmt.Errorf("expected DriverInstallPolicy, got %T", obj)
	}
	s := dip.Spec

	if !knownVendors[strings.ToLower(s.Vendor)] {
		return fmt.Errorf("spec.vendor %q is not a known vendor (allowed: nvidia, furiosa, tenstorrent, rebellions)", s.Vendor)
	}
	// trackOnly=true 는 설치 경로가 없어 설치기 이미지를 비울 수 있다(Rebellions: 드라이버는 호스트 관리자가
	// 설치). 적혀 있으면 구문은 같은 규칙으로 검사한다.
	if !s.Driver.TrackOnly || strings.TrimSpace(s.Driver.Image) != "" {
		if err := validateImageRef(s.Driver.Image); err != nil {
			return fmt.Errorf("spec.driver.image: %w", err)
		}
	}
	// trackOnly=true 는 설치 경로를 만들지 않으므로 job 모드와만 정합한다.
	if s.Driver.TrackOnly && s.Driver.Mode != "" && s.Driver.Mode != "job" {
		return fmt.Errorf("spec.driver.trackOnly=true requires spec.driver.mode=job (got %q)", s.Driver.Mode)
	}
	if up := s.UpgradePolicy; up != nil {
		if up.MaxReboots < 0 {
			return fmt.Errorf("spec.upgradePolicy.maxReboots must be >= 0 (got %d)", up.MaxReboots)
		}
		for name, val := range map[string]string{
			"validationTimeout": up.ValidationTimeout,
			"drainTimeout":      up.DrainTimeout,
			"rebootTimeout":     up.RebootTimeout,
		} {
			if val == "" {
				continue
			}
			if _, err := time.ParseDuration(val); err != nil {
				return fmt.Errorf("spec.upgradePolicy.%s %q is not a valid duration: %w", name, val, err)
			}
		}
	}
	return nil
}

// validateImageRef 는 이미지 참조에 명시적 태그가 있는지 검증한다.
// (CRD Pattern 이 1차 방어하나, webhook 은 명확한 거부 메시지를 제공한다.)
func validateImageRef(img string) error {
	img = strings.TrimSpace(img)
	if img == "" {
		return fmt.Errorf("image must not be empty")
	}
	name := img
	if i := strings.LastIndex(img, "/"); i >= 0 {
		name = img[i+1:]
	}
	tagSep := strings.LastIndex(name, ":")
	if tagSep < 0 {
		return fmt.Errorf("image %q must include an explicit tag (name:tag)", img)
	}
	if strings.TrimSpace(name[tagSep+1:]) == "" {
		return fmt.Errorf("image %q has an empty tag", img)
	}
	return nil
}

// NCPValidator 는 NPUClusterPolicy 를 검증한다: enabled vendor 는 devicePluginImage 필수.
type NCPValidator struct{}

func (v *NCPValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, recordRejection("NPUClusterPolicy", validateNCP(obj))
}

func (v *NCPValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return nil, recordRejection("NPUClusterPolicy", validateNCP(newObj))
}

func (v *NCPValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateNCP(obj runtime.Object) error {
	ncp, ok := obj.(*npuv1alpha1.NPUClusterPolicy)
	if !ok {
		return fmt.Errorf("expected NPUClusterPolicy, got %T", obj)
	}
	s := ncp.Spec
	// enum 밖 managementMode(오타 등)는 세 도우미가 모두 거짓이다 — CRD 스키마가 보통은
	// 막지만, struct 를 직접 구성해 그 경로를 우회하는 호출자에도 안전해야 한다. 조용히
	// managed 로 읽으면 이미지는 있어도 컨트롤러의 IsOperatorOwned() 가 거짓이라 배포가
	// 은근슬쩍 멈춘다. furiosa.rngd 는 RngdSpec 에 관리 모드가 없어 Furiosa 를 검사한다.
	modes := []struct {
		name string
		m    npuv1alpha1.ManagementSpec
	}{
		{"nvidia", s.Nvidia.ManagementSpec},
		{"furiosa", s.Furiosa.ManagementSpec},
		{"rebellions", s.Rebellions.ManagementSpec},
		{"tenstorrent", s.Tenstorrent.ManagementSpec},
	}
	for _, m := range modes {
		if !m.m.IsOperatorOwned() && !m.m.IsDelegated() && !m.m.IsExternal() {
			return fmt.Errorf("spec.%s.managementMode %q must be one of managed, delegated, external", m.name, m.m.ManagementMode)
		}
	}
	// kcloud 가 배포 주체가 아닌 벤더(delegated·external)는 device-plugin 을 배포하지
	// 않으므로 devicePluginImage 를 요구하지 않는다.
	checks := []struct {
		name    string
		enabled bool
		owned   bool
		img     string
	}{
		{"nvidia", s.Nvidia.Enabled, s.Nvidia.IsOperatorOwned(), s.Nvidia.DevicePluginImage},
		{"furiosa", s.Furiosa.Enabled, s.Furiosa.IsOperatorOwned(), s.Furiosa.DevicePluginImage},
		{"furiosa.rngd", s.Furiosa.Rngd.Enabled, s.Furiosa.IsOperatorOwned(), s.Furiosa.Rngd.DevicePluginImage},
		{"rebellions", s.Rebellions.Enabled, s.Rebellions.IsOperatorOwned(), s.Rebellions.DevicePluginImage},
		{"tenstorrent", s.Tenstorrent.Enabled, s.Tenstorrent.IsOperatorOwned(), s.Tenstorrent.DevicePluginImage},
	}
	for _, c := range checks {
		if c.enabled && c.owned && strings.TrimSpace(c.img) == "" {
			return fmt.Errorf("spec.%s.enabled=true but devicePluginImage is empty", c.name)
		}
	}
	return nil
}

// PodMutator 는 NVIDIA GPU 를 요청하고 runtimeClassName 미지정인 Pod 에 nvidia runtimeClass 를
// 주입한다. Scope 가 InjectScopePod(기본·빈 값)면 opt-in 라벨(kcloud.ai/inject=true) Pod 만,
// InjectScopeNamespace 면 admission selector 가 보낸 Pod 전부가 대상이다. 그 외 Pod 는 무개입.
type PodMutator struct {
	// Scope 는 InjectScopePod 또는 InjectScopeNamespace 다. NormalizeInjectScope 로 정규화한 값을 넣는다.
	Scope string
}

func (m *PodMutator) Default(_ context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("expected Pod, got %T", obj)
	}
	// pod 범위: opt-in 라벨 없으면 개입하지 않음(objectSelector 이중 방어). namespace 범위는
	// 네임스페이스 선택을 admission namespaceSelector 가 하므로 Pod 라벨을 보지 않는다.
	if m.Scope != InjectScopeNamespace && pod.Labels[InjectLabel] != "true" {
		return nil
	}
	if podRequestsNvidiaGPU(pod) && (pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName == "") {
		rc := nvidiaRuntimeClass
		pod.Spec.RuntimeClassName = &rc
	}
	return nil
}

func podRequestsNvidiaGPU(pod *corev1.Pod) bool {
	containers := append([]corev1.Container{}, pod.Spec.Containers...)
	containers = append(containers, pod.Spec.InitContainers...)
	for _, c := range containers {
		if hasNvidiaResource(c.Resources.Limits) || hasNvidiaResource(c.Resources.Requests) {
			return true
		}
	}
	return false
}

func hasNvidiaResource(rl corev1.ResourceList) bool {
	for name := range rl {
		if strings.HasPrefix(string(name), nvidiaResourcePrefix) {
			return true
		}
	}
	return false
}
