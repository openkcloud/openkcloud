// ============================================================
// resources.go: 벤더↔device-plugin 리소스명 카탈로그
// 상세: 추상 모드를 클러스터가 광고하는 extended resource 이름으로 옮기는 유일한 지점. NVIDIA mixed
// MIG 만 profile 별 리소스명을 쓰고 나머지는 flat 광고라, 파티션 노드는 리소스명이 아니라 노드 후보 목록으로 구분한다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package intent

import (
	"fmt"
	"sort"
	"strings"

	"kcloud-operator/api/v1alpha1"
)

// migResourcePrefix 는 nvidia device-plugin 이 mig-strategy=mixed 에서 쓰는 접두다.
const migResourcePrefix = "nvidia.com/mig-"

// productResource 는 (벤더, product) → 전체 장치 리소스명이다. product 가 "" 인 항목은
// 그 벤더의 유일한 제품이라 product 생략을 허용한다는 뜻이다. 값은 클러스터가 광고 중인
// 이름이 정본이다(furiosa 는 npuclusterpolicy_controller.go 의 Warboy/RNGD 리소스명과 대조 확인).
var productResource = map[string]map[string]string{
	"nvidia":      {"": "nvidia.com/gpu"},
	"furiosa":     {"rngd": "furiosa.ai/rngd", "warboy": "beta.furiosa.ai/npu"},
	"rebellions":  {"": "rebellions.ai/ATOM"},
	"tenstorrent": {"": "tenstorrent.com/blackhole"},
}

// WholeDeviceResource 는 (vendor, product) 의 전체 장치 리소스명이다. 모르는 벤더는 "", nil 을
// 돌려준다(그냥 미지원). 벤더 아래 제품이 하나뿐이면 product 값은 무시한다 — AcceleratorMapping.
// Product 는 그 벤더에선 그냥 사람이 읽는 표기(예 nvidia 의 "A30")일 뿐 구분할 게 없기 때문이다.
// 제품이 여럿인 벤더(furiosa)만 product 로 실제 분기하며, 비었거나 못 알아보는 값이면 조용히
// 아무거나 고르지 않고 에러를 돌려준다 — furiosa 를 product 없이 물으면 RNGD 인지 Warboy 인지
// 알 길이 없다.
func WholeDeviceResource(vendor, product string) (string, error) {
	products, ok := productResource[strings.ToLower(vendor)]
	if !ok {
		return "", nil
	}
	if len(products) == 1 {
		for _, res := range products {
			return res, nil
		}
	}
	if res, ok := products[strings.ToLower(product)]; ok {
		return res, nil
	}
	names := make([]string, 0, len(products))
	for p := range products {
		names = append(names, p)
	}
	sort.Strings(names)
	return "", fmt.Errorf("intent: %s: cannot determine resource name — product must be one of %v, got %q", vendor, names, product)
}

// KnownVendor 는 이 카탈로그가 그 벤더의 리소스명을 아는지다. 모르는 벤더는 어떤 리소스를
// 봐야 하는지 알 수 없으므로 "그 벤더의 광고가 0 이다" 를 증명할 방법이 없다 — 호출자는
// 찾은 게 없는 것과 0 임을 확인한 것을 구분해야 한다.
func KnownVendor(vendor string) bool {
	_, ok := productResource[strings.ToLower(vendor)]
	return ok
}

// PartitionResource 는 nativeProfile 파티션이 광고되는 리소스명이다.
func PartitionResource(vendor, product, nativeProfile string) (string, error) {
	if strings.EqualFold(vendor, "nvidia") && nativeProfile != "" {
		return migResourcePrefix + nativeProfile, nil
	}
	return WholeDeviceResource(vendor, product)
}

// ResourceFor 는 추상 모드에 맞는 리소스명이다. 분할 계열만 profile 을 본다.
func ResourceFor(m v1alpha1.AcceleratorMapping, mode string) (string, error) {
	switch mode {
	case v1alpha1.AccessModePartitioned, v1alpha1.AccessModePartitionedShared:
		return PartitionResource(m.Vendor, m.Product, m.NativeProfile)
	default:
		return WholeDeviceResource(m.Vendor, m.Product)
	}
}

// ValidateDRAMapping 은 DRA 축 두 필드가 함께 채워졌는지 본다.
// 한쪽만 있으면 관리자가 절반만 설정한 것이고, 그 상태로 통과시키면 노드 후보가
// 조용히 0이 되어 "왜 후보가 없나" 를 되짚을 수 없게 된다.
func ValidateDRAMapping(m v1alpha1.AcceleratorMapping) error {
	if (m.DeviceClassName == "") != (m.DRADriver == "") {
		return fmt.Errorf("AcceleratorClass mapping for vendor %q sets only one of deviceClassName/draDriver; set both or neither", m.Vendor)
	}
	return nil
}

// VendorForResource 는 리소스명에서 벤더를 되짚는다. 가속기 리소스가 아니면 "".
func VendorForResource(name string) string {
	if strings.HasPrefix(name, migResourcePrefix) {
		return "nvidia"
	}
	for vendor, products := range productResource {
		for _, res := range products {
			if res == name {
				return vendor
			}
		}
	}
	return ""
}

// AcceleratorResourceDomains 는 등록된 벤더의 가속기 extended resource 도메인 접두 집합이다(정렬·중복
// 제거). 정확한 제품명이 아니라 도메인으로 보는 이유는 판정이 fail-closed 여야 하기 때문이다 — 카탈로그에
// 없는 제품명이나 device-plugin 변형 이름(nvidia.com/mig-1g.5gb 등)도 그 벤더의 사용으로 세야 한다.
// uninstall 게이트(internal/uninstall/gate.go)와 드라이버 업그레이드 drain(internal/upgrade/state_machine.go)이
// 이 집합 하나를 함께 쓴다 — 두 경로가 각자 나열하면 벤더 도메인이 한쪽에서만 빠질 위험이 있다.
func AcceleratorResourceDomains() []string {
	seen := make(map[string]bool)
	var domains []string
	for _, products := range productResource {
		for _, res := range products {
			i := strings.Index(res, "/")
			if i < 0 {
				continue
			}
			d := res[:i+1]
			if !seen[d] {
				seen[d] = true
				domains = append(domains, d)
			}
		}
	}
	sort.Strings(domains)
	return domains
}
