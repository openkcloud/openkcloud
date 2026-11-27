// ============================================================
// acceleratorhealth_vendor_registration_test.go: 벤더 등록 지점 둘의 적합성 시험
// 상세: isAcceleratorNode·acceleratorOperationVendors 는 벤더 목록을 손으로 옮겨 적은 자료라
//
//	어긋날 수 있다. 두 대상이 비공개 식별자라 test/conformance 대신 이 패키지 안에 둔다.
//
// 생성일: 2026-09-28 | 수정일: 2026-11-03
// ============================================================
package controller

import (
	"os"
	"sort"
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// vendorPresentLabels 는 계약 8 이 보는 벤더별 present 라벨이다 — 기대값은 isAcceleratorNode
// 의 파생 로직이 아니라 detector 자신(node-device-detector/label.go computeLabels·vendorKey)
// 이 실제로 쓰는 값에서 직접 가져온다. nvidia·rebellions·tenstorrent 는
// vendorPresentLabel(vendor) 하나씩이고, furiosa 는 셋이다 — vendorPresentLabel(vendorFuriosa)
// (Warboy 포함 기본 키), vendorPresentLabel(vendorRngd)(RNGD 전용 키 "rngd"),
// furiosaFamilyNodeLabel(PCI 0x1ed2 공유 공통 라벨). "kcloud.ai/warboy.present" 는 틀린 값이라 여기 없다.
var vendorPresentLabels = map[string][]string{
	vendorNvidia:      {vendorPresentLabel(vendorNvidia)},
	vendorFuriosa:     {vendorPresentLabel(vendorFuriosa), vendorPresentLabel(vendorRngd), furiosaFamilyNodeLabel},
	vendorRebellions:  {vendorPresentLabel(vendorRebellions)},
	vendorTenstorrent: {vendorPresentLabel(vendorTenstorrent)},
}

// TestIsAcceleratorNodeRecognizesEveryVendorPresentLabel 는 계약 8번이다: 등록된 모든
// 벤더의 present 라벨을 가진 노드에 isAcceleratorNode 가 참인지 본다. 벤더 하나를 놓치면
// AcceleratorHealth Reconcile 이 그 벤더 노드를 에러·이벤트·로그 없이 무시한다.
// vendorPresentLabels 에 없는 벤더가 생기면 표를 갱신하지 않고는 넘어갈 수 없게 실패로
// 낸다. 노드 fixture 는 벤더별 라벨 하나만 갖고, detector 가 항상 함께 쓰는
// kcloud.ai/accelerator.present 는 갖지 않는다 — 등록된 벤더가 빠짐없이 담기는지만 지킨다.
func TestIsAcceleratorNodeRecognizesEveryVendorPresentLabel(t *testing.T) {
	for _, vendor := range []string{vendorNvidia, vendorFuriosa, vendorRebellions, vendorTenstorrent} {
		labels, ok := vendorPresentLabels[vendor]
		if !ok || len(labels) == 0 {
			t.Errorf("%s 의 present 라벨이 vendorPresentLabels 에 없다", vendor)
			continue
		}
		for _, label := range labels {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{label: "true"},
			}}
			if !isAcceleratorNode(node) {
				t.Errorf("%s 의 present 라벨 %q 를 isAcceleratorNode 가 인식하지 못한다", vendor, label)
			}
		}
	}
}

// TestIsAcceleratorNodeRecognizesGenericPresentLabels 는 벤더 라벨 없이 범용 라벨만 가진
// 노드도 감시 대상인지 본다. 새 이름 accelerator.present 와 옛 이름 gpu.present(구 node-manager
// 가 붙임) 둘 다 인식해야 node-manager 와 operator 의 롤링 업그레이드 순서에 상관없이 노드가
// 감시에서 빠지지 않는다.
func TestIsAcceleratorNodeRecognizesGenericPresentLabels(t *testing.T) {
	for _, label := range []string{npuv1alpha1.AcceleratorPresentLabel, "kcloud.ai/gpu.present"} {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{label: "true"}}}
		if !isAcceleratorNode(node) {
			t.Errorf("범용 present 라벨 %q 를 isAcceleratorNode 가 인식하지 못한다", label)
		}
	}
	if isAcceleratorNode(&corev1.Node{}) {
		t.Error("라벨 없는 노드를 가속기 노드로 본다")
	}
}

// acceleratorOperationCRDSchema 는 config/crd/bases/npu.ai_acceleratoroperations.yaml 에서
// spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.vendor.enum 까지만
// 내려가는 최소 뼈대다 — internal/webhook/acceleratorpartitionpolicy_enum_test.go 의
// acppCRDSchema 와 같은 모양이다(CRD 마다 파일이 달라 그대로 재사용하지는 못한다).
type acceleratorOperationCRDSchema struct {
	Spec struct {
		Versions []struct {
			Schema struct {
				OpenAPIV3Schema struct {
					Properties struct {
						Spec struct {
							Properties struct {
								Vendor struct {
									Enum []string `json:"enum"`
								} `json:"vendor"`
							} `json:"properties"`
						} `json:"spec"`
					} `json:"properties"`
				} `json:"openAPIV3Schema"`
			} `json:"schema"`
		} `json:"versions"`
	} `json:"spec"`
}

// TestAcceleratorOperationVendorsMatchesCRDEnum 은 계약 9번이다:
// acceleratorOperationVendors 화이트리스트의 키 집합이 AcceleratorOperationSpec.Vendor 의
// CRD enum(API 서버가 실제로 강제하는 값)과 같은지 본다. 그 맵 자신의 주석이 "두 곳이
// 갈라지면 한쪽만 고치고 고쳤다고 믿게 된다" 고 경고하는 바로 그 위험을 시험으로 만든
// 것이다. 비교 대상은 계약 7번(test/conformance)과 같은 근거지(생성된 CRD YAML)다.
func TestAcceleratorOperationVendorsMatchesCRDEnum(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/npu.ai_acceleratoroperations.yaml")
	if err != nil {
		t.Fatalf("CRD yaml 읽기 실패: %v", err)
	}
	var doc acceleratorOperationCRDSchema
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("CRD yaml 파싱 실패: %v", err)
	}
	if len(doc.Spec.Versions) == 0 {
		t.Fatalf("spec.versions 가 비어 있다")
	}
	enum := append([]string(nil), doc.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Vendor.Enum...)
	sort.Strings(enum)

	got := make([]string, 0, len(acceleratorOperationVendors))
	for v := range acceleratorOperationVendors {
		got = append(got, v)
	}
	sort.Strings(got)

	if len(got) != len(enum) {
		t.Fatalf("acceleratorOperationVendors 키 %d개, CRD enum %d개 — 개수부터 다르다: %v vs %v", len(got), len(enum), got, enum)
	}
	for i := range got {
		if got[i] != enum[i] {
			t.Fatalf("acceleratorOperationVendors 키 집합 = %v, CRD enum = %v — 서로 다르다", got, enum)
		}
	}
}
