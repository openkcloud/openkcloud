// ============================================================
// acceleratorpartitionpolicy_enum_test.go: ACPP vendor enum·rebellions 파티션 레지스트리 회귀 가드
// 상세: CRD YAML 의 spec.vendor enum 4값과, webhook 이 의존하는 vendorcap.LookupVendor("rebellions",
// CapPartition)의 Unsupported 선언이 유지되는지 고정한다(거절 메시지 문자열은 별도 시험이 본다).
// 생성일: 2026-09-20 | 수정일: 2026-10-11
// ============================================================
package webhook

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"kcloud-operator/internal/vendorcap"
)

// acppCRDSchema 는 config/crd/bases/npu.ai_acceleratorpartitionpolicies.yaml 에서
// spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.vendor.enum 까지만
// 내려가는 최소 뼈대다 — CRD 전체를 구조체로 옮기지 않는다.
type acppCRDSchema struct {
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

// CRD enum 이 "furiosa;nvidia" 로 되돌아가면(예: rebellions/tenstorrent 온보딩 커밋을 되돌리는
// 병합 사고) webhook 의 거절 시험(acceleratorpartitionpolicy_test.go)은 enum 자체를 보지
// 않으므로 못 잡는다 — 이 시험이 그 축을 직접 고정한다.
func TestACPPCRDVendorEnumRegression(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/npu.ai_acceleratorpartitionpolicies.yaml")
	if err != nil {
		t.Fatalf("CRD yaml 읽기 실패: %v", err)
	}
	var doc acppCRDSchema
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("CRD yaml 파싱 실패: %v", err)
	}
	if len(doc.Spec.Versions) == 0 {
		t.Fatalf("CRD spec.versions 비어 있음")
	}
	got := append([]string(nil), doc.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Vendor.Enum...)
	sort.Strings(got)
	want := []string{"furiosa", "nvidia", "rebellions", "tenstorrent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("spec.vendor enum 회귀: got %v, want %v", got, want)
	}
}

// webhook 의 거절 경로(validateACPP -> vendorCapability -> vendorcap.LookupVendor)는 그대로
// 두고 레지스트리 쪽 선언만 바뀌면(Reason/Source 문구 변경, State 완화) 위 CRD 시험도
// acceleratorpartitionpolicy_test.go 의 문자열 시험도 이 축을 못 잡는다 — 여기서 webhook 이
// 의존하는 registry 선언 자체를 직접 고정한다.
func TestVendorcapRebellionsPartitionUnsupportedReason(t *testing.T) {
	got := vendorcap.LookupVendor("rebellions", vendorcap.CapPartition)
	if got.State != vendorcap.StateUnsupported {
		t.Fatalf("rebellions partition State = %v, want %v", got.State, vendorcap.StateUnsupported)
	}
	if !strings.Contains(got.Reason, "vendor-capability-not-found") {
		t.Fatalf("Reason 에 vendor-capability-not-found 없음: %q", got.Reason)
	}
	// Source 는 비워 둔다 — 근거 문서가 이 저장소 밖(remote 없는 docs)이라 거절 메시지에
	// 붙이면 받는 사람이 도달할 수 없다. 대신 Reason 이 무엇을 조사했는지 적는다.
	if got.Source != "" {
		t.Fatalf("Source = %q, want 빈 문자열(도달 불가 경로 금지)", got.Source)
	}
	if !strings.Contains(got.Reason, "rblnlib-go v0.3.0") {
		t.Fatalf("Reason 에 조사 대상이 없다: %q", got.Reason)
	}
}
