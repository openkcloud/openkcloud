// ============================================================
// vendorcap_quiescer_guard_test.go: CapReset 등급 (vendor,product) 의 전용 Quiescer 배선 가드
// 상세: CapReset 이 Unsupported/Unknown 이 아닌 (vendor,product) 쌍마다 advertiseQuiescer 가 전용
// (확장 자원 인지) 판정기를 돌려주는지 본다. 기본 Quiescer 는 DRA claim 축만 보고 확장 자원
// 점유를 못 본다.
// 생성일: 2026-10-09 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"testing"

	"kcloud-operator/internal/vendorcap"
)

// TestResetCapableVendorsHaveDedicatedQuiescer 는 CapReset 이 Unsupported 가 아닌 (vendor,product)
// 쌍마다 전용 점유 판정기가 있는지 본다. 리셋은 장치를 쥔 파드를 먼저 비워야 하는데, 기본
// Quiescer 는 DRA claim 만 보고 확장 자원 점유를 못 본다. advertiseQuiescer 의 키는 registry 의
// (vendor,product) 와 어휘가 달라(furiosa+rngd → "rngd", 그 외는 vendor 그대로)
// descriptorRegistryKey 의 반대 방향으로 직접 번역한다 — vendor 단위로만 조회하면 furiosa/rngd
// 만 승격돼도 "furiosa" 키로 default 에 걸려 이미 있는 RngdQuiescer 를 없다고 오탐한다.
func TestResetCapableVendorsHaveDedicatedQuiescer(t *testing.T) {
	for _, k := range vendorcap.Keys() {
		s, ok := vendorcap.Lookup(k.Vendor, k.Product, vendorcap.CapReset)
		if !ok || s.State == vendorcap.StateUnsupported || s.State == vendorcap.StateUnknown {
			continue
		}
		key := k.Vendor
		if k.Vendor == vendorFuriosa && k.Product == vendorRngd {
			key = vendorRngd
		}
		q := advertiseQuiescer(nil, key)
		if len(q.ResourcePrefixes) == 0 && len(q.ExtraResources) == 0 {
			t.Errorf("%s/%s: CapReset=%s 인데 advertiseQuiescer(%q) 가 기본 판정기(확장 자원 미인지)를 "+
				"돌려준다 — partition 패키지에 전용 Quiescer 를 더하고 advertiseQuiescer 에 등록할 것",
				k.Vendor, k.Product, s.State, key)
		}
	}
}
