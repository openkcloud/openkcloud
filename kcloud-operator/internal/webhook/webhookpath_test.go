// ============================================================
// webhookpath_test.go: 매니저 등록 validator ↔ 배포 webhook 설정 경로 일치 테스트
// 상세: ctrl.NewWebhookManagedBy 등록만으로는 API 서버가 요청을 보내지 않는다 — ValidatingWebhookConfiguration
// 은 controller-gen 없이 deploy/helm/templates/webhook.yaml 에 손으로 적으므로, 등록된 경로를 같은 공식으로 계산해 대조한다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================
package webhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// generateValidatePath 는 sigs.k8s.io/controller-runtime/pkg/builder.generateValidatePath(비공개)와
// 동일한 규칙이다: "/validate-" + group(점→대시) + "-" + version + "-" + strings.ToLower(kind).
// controller-runtime 이 그 공식을 바꾸면 이 테스트도 같이 깨져야 코드-yaml 이 조용히 어긋나지 않는다.
func generateValidatePath(group, version, kind string) string {
	return "/validate-" + strings.ReplaceAll(group, ".", "-") + "-" + version + "-" + strings.ToLower(kind)
}

// 등록은 됐는데 yaml 이 안 고쳐지면 admission 요청이 서버에 닿지 못한다 — 이 테스트가 그 어긋남을 잡는다.
func TestWebhookConfigMatchesRegisteredValidators(t *testing.T) {
	group := npuv1alpha1.GroupVersion.Group
	version := npuv1alpha1.GroupVersion.Version
	// Setup() 이 ctrl.NewWebhookManagedBy(mgr).For(...).WithValidator(...) 로 등록하는 타입들.
	registered := []struct{ kind, resource string }{
		{"DriverInstallPolicy", "driverinstallpolicies"},
		{"NPUClusterPolicy", "npuclusterpolicies"},
		{"AcceleratorClass", "acceleratorclasses"},
		{"AcceleratorWorkload", "acceleratorworkloads"},
		{"AcceleratorPartitionPolicy", "acceleratorpartitionpolicies"},
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "helm", "templates", "webhook.yaml"))
	if err != nil {
		t.Fatalf("read deploy/helm/templates/webhook.yaml: %v", err)
	}
	yaml := string(raw)

	for _, r := range registered {
		path := generateValidatePath(group, version, r.kind)
		if !strings.Contains(yaml, "path: "+path) {
			t.Errorf("%s: registered on the manager but path %q is not wired in deploy/helm/templates/webhook.yaml — admission requests never reach it", r.kind, path)
		}
		if !strings.Contains(yaml, `resources: ["`+r.resource+`"]`) {
			t.Errorf("%s: resources entry %q not found in deploy/helm/templates/webhook.yaml", r.kind, r.resource)
		}
	}
}
