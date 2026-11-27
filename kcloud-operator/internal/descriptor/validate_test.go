// ============================================================
// validate_test.go: descriptor 정합 검증 시험
// 상세: 유효한 표본 하나를 두고 필드를 하나씩 무너뜨려 각 규칙이 실제로 잡는지 본다. 무효
// 표본은 손으로 따로 짓지 않고 유효 표본에서 파생시켜, 프로덕션이 만들 수 있는 조합만 시험한다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package descriptor

import (
	"os"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// validSpec 은 모든 규칙을 통과하는 표본이다. 무효 표본은 전부 여기서 파생한다.
func validSpec() npuv1alpha1.AcceleratorDescriptorSpec {
	return npuv1alpha1.AcceleratorDescriptorSpec{
		Vendor:         "nvidia",
		Product:        "a30",
		AllocationUnit: npuv1alpha1.AllocationUnitDevice,
		Identity: npuv1alpha1.DescriptorIdentity{
			Source:             npuv1alpha1.IdentitySourceUUID,
			StableAcrossReboot: true,
		},
		DeviceNodes: []string{"/dev/nvidia0", "/dev/nvidiactl"},
		Backends:    []string{npuv1alpha1.BackendDevicePlugin, npuv1alpha1.BackendDRA},
	}
}

func TestValidateAcceptsValidSpec(t *testing.T) {
	if errs := Validate(validSpec()); len(errs) != 0 {
		t.Fatalf("유효 표본이 거절됐다: %v", errs)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*npuv1alpha1.AcceleratorDescriptorSpec)
		wantSub string
	}{
		{
			name:    "벤더 공란",
			mutate:  func(s *npuv1alpha1.AcceleratorDescriptorSpec) { s.Vendor = "" },
			wantSub: "vendor",
		},
		{
			name:    "모르는 할당 단위",
			mutate:  func(s *npuv1alpha1.AcceleratorDescriptorSpec) { s.AllocationUnit = "half" },
			wantSub: "allocationUnit",
		},
		{
			name:    "모르는 식별자 출처",
			mutate:  func(s *npuv1alpha1.AcceleratorDescriptorSpec) { s.Identity.Source = "hostname" },
			wantSub: "identity.source",
		},
		{
			name:    "장치 노드 없음",
			mutate:  func(s *npuv1alpha1.AcceleratorDescriptorSpec) { s.DeviceNodes = nil },
			wantSub: "deviceNodes",
		},
		{
			name:    "장치 노드가 절대경로 아님",
			mutate:  func(s *npuv1alpha1.AcceleratorDescriptorSpec) { s.DeviceNodes = []string{"nvidia0"} },
			wantSub: "deviceNodes[0]",
		},
		{
			name:    "모르는 backend",
			mutate:  func(s *npuv1alpha1.AcceleratorDescriptorSpec) { s.Backends = []string{"cdi"} },
			wantSub: "backends[0]",
		},
		{
			name: "리셋이 필요한데 수단 없음",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.Cleanup.RequiresDeviceReset = true
			},
			wantSub: "cleanup.resetCommand",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := validSpec()
			c.mutate(&s)
			errs := Validate(s)
			if len(errs) == 0 {
				t.Fatalf("거절돼야 하는데 통과했다")
			}
			if !strings.Contains(errs.ToAggregate().Error(), c.wantSub) {
				t.Errorf("사유에 %q 가 없다: %v", c.wantSub, errs.ToAggregate())
			}
		})
	}
}

func TestValidateSharingReplicasRange(t *testing.T) {
	s := validSpec()
	s.Backends = []string{npuv1alpha1.BackendDevicePlugin}
	s.Sharing = &npuv1alpha1.DescriptorSharing{Mode: npuv1alpha1.SharingModeTimeSliced, Replicas: 1}
	if errs := Validate(s); len(errs) == 0 {
		t.Fatal("replicas 1 은 거절돼야 한다(2 이상)")
	}
	s.Sharing.Replicas = 4
	if errs := Validate(s); len(errs) != 0 {
		t.Fatalf("replicas 4 는 통과해야 한다: %v", errs)
	}
	s.Sharing.Replicas = 17
	if errs := Validate(s); len(errs) == 0 {
		t.Fatal("replicas 17 은 MaxSharingReplicas 초과로 거절")
	}
}

func TestValidateSharingRequiresDevicePluginBackend(t *testing.T) {
	s := validSpec()
	s.Backends = []string{npuv1alpha1.BackendDRA}
	s.Sharing = &npuv1alpha1.DescriptorSharing{Mode: npuv1alpha1.SharingModeTimeSliced, Replicas: 2}
	if errs := Validate(s); len(errs) == 0 {
		t.Fatal("시분할은 devicePlugin backend 에서만 — DRA 만 선언하면 거절")
	}
}

// TestValidateRejectsDeclarations 는 생성 DRA 런타임이 기동 때 거절하는 선언을 operator 가 먼저
// 거절하는지 본다. 여기서 안 막으면 거절 사유가 status.refusals 가 아니라 드라이버 파드 로그로
// 옮겨가고, operator 가 상위 참조를 담은 ConfigMap 을 써 준 뒤 런타임이 그것을 막는 구조가 된다.
// 조건은 런타임(generic-dra-driver 의 validateDeclarations)과 같은 것을 쓴다.
func TestValidateRejectsDeclarations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*npuv1alpha1.AcceleratorDescriptorSpec)
		wantSub string
	}{
		{
			name: "속성 이름이 C 식별자 아님",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.Attributes = map[string]string{"product-name": "RNGD"}
			},
			wantSub: "attributes[product-name]",
		},
		{
			name: "capacity 값이 Quantity 아님",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.Capacity = map[string]string{"memory": "48GB!"}
			},
			wantSub: "capacity[memory]",
		},
		{
			name: "capacityFiles 값이 절대경로",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.CapacityFiles = map[string]string{"memory": "/etc/shadow"}
			},
			wantSub: "capacityFiles[memory]",
		},
		{
			name: "capacityFiles 값에 상위 참조",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.CapacityFiles = map[string]string{"memory": "../../../etc/passwd"}
			},
			wantSub: "capacityFiles[memory]",
		},
		{
			name: "속성 값이 빈 문자열",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.Attributes = map[string]string{"productName": ""}
			},
			wantSub: "attributes[productName]",
		},
		{
			name: "속성 값이 64자를 넘음",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.Attributes = map[string]string{"productName": strings.Repeat("x", 65)}
			},
			wantSub: "attributes[productName]",
		},
		{
			name: "capacity 이름의 도메인 접두사가 DNS subdomain 아님",
			mutate: func(s *npuv1alpha1.AcceleratorDescriptorSpec) {
				s.Capacity = map[string]string{"Not_A_Domain/memory": "48Gi"}
			},
			wantSub: "capacity[Not_A_Domain/memory]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := validSpec()
			c.mutate(&s)
			errs := Validate(s)
			if len(errs) == 0 {
				t.Fatalf("거절돼야 하는데 통과했다")
			}
			if !strings.Contains(errs.ToAggregate().Error(), c.wantSub) {
				t.Errorf("사유에 %q 가 없다: %v", c.wantSub, errs.ToAggregate())
			}
		})
	}
}

// TestValidateAcceptsLegalDeclarations 는 런타임이 받아 주는 선언을 operator 가 거절하지 않는지
// 본다. 두 조건을 벌리면 사이트가 막히므로 거절 시험과 쌍으로 둔다.
func TestValidateAcceptsLegalDeclarations(t *testing.T) {
	s := validSpec()
	s.Attributes = map[string]string{"productName": "RNGD", "kcloud.ai/tier": "gold"}
	s.Capacity = map[string]string{"memory": "48Gi"}
	s.CapacityFiles = map[string]string{"sramBytes": "rngd/sram_total"}
	if errs := Validate(s); len(errs) != 0 {
		t.Fatalf("정당한 선언이 거절됐다: %v", errs.ToAggregate())
	}
}

// descriptorMaxPropertiesPerMap 은 attributes·capacity·capacityFiles 각각의 CRD
// MaxProperties 다. 세 맵 합(8 × 3)이 런타임의 선언 예산 안이어야 한다. 같은 수를
// 어서션하는 자리가 images 저장소의 generic-dra-driver/attributes_test.go
// operatorDeclarationBudget 에도 있다 — 거기는 런타임 예산이 이 합을 담는지, 여기는
// 그 합이 정말 8 × 3 인지 본다. 올리려면 두 자리를 함께 고쳐야 한다 — 한쪽만 올리면
// operator 가 통과시킨 descriptor 를 런타임이 거절해 드라이버가 기동 실패한다.
const descriptorMaxPropertiesPerMap int64 = 8

// validateDeclarations 가 개수를 세지 않는 전제는 "CRD MaxProperties 가 이미 런타임 예산 아래로
// 묶는다" 다. 그 전제를 지키는 장치가 이 시험이다 — 마커를 올리면 여기서 걸린다. 마커만 보지
// 않고 생성된 CRD 를 읽는 이유는 controller-gen 이 그것을 싣지 않으면 API 서버가 세지 않기
// 때문이다. 두 복사본을 다 읽는다 — crd-check 가 둘의 동일성을 보지만 이 전제는 그 게이트의
// 실행에 기대지 않는다.
func TestDescriptorDeclarationMapsCarryMaxProperties(t *testing.T) {
	for _, rel := range []string{
		"../../config/crd/bases/npu.ai_acceleratordescriptors.yaml",
		"../../deploy/helm/crds/npu.ai_acceleratordescriptors.yaml",
	} {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("%s 를 읽지 못했다: %v", rel, err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s 파싱 실패: %v", rel, err)
		}
		if len(crd.Spec.Versions) == 0 {
			t.Fatalf("%s 에 버전이 없다 — 읽을 스키마가 없다", rel)
		}
		for _, v := range crd.Spec.Versions {
			spec, ok := v.Schema.OpenAPIV3Schema.Properties["spec"]
			if !ok {
				t.Fatalf("%s %s: spec 스키마가 없다", rel, v.Name)
			}
			for _, name := range []string{"attributes", "capacity", "capacityFiles"} {
				m, ok := spec.Properties[name]
				if !ok {
					t.Errorf("%s %s: spec.%s 가 없다", rel, v.Name, name)
					continue
				}
				if m.MaxProperties == nil {
					t.Errorf("%s %s: spec.%s 에 maxProperties 가 없다 — API 서버가 개수를 세지 않는다",
						rel, v.Name, name)
					continue
				}
				if *m.MaxProperties != descriptorMaxPropertiesPerMap {
					t.Errorf("%s %s: spec.%s 의 maxProperties 가 %d 다, want %d — images 저장소의"+
						" operatorDeclarationBudget 도 함께 고쳐야 한다",
						rel, v.Name, name, *m.MaxProperties, descriptorMaxPropertiesPerMap)
				}
			}
		}
	}
}
