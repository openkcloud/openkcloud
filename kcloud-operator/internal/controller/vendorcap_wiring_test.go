// ============================================================
// vendorcap_wiring_test.go: 선언 레지스트리와 실제 벤더 배선의 일치 가드
// 상세: "명세서에 지원이라 적혔는데 Go 타입이 없거나, 그 반대"를 빌드 시점에 잡는다. partition
// ⟺ backendFor, timeSlicing/multiProcess ⟺ SharingBackend 구현, devicePlugin ⟺
// NPUClusterPolicyReconciler 의 ensure*DevicePlugin 메서드가 서로 맞아야 한다.
// 생성일: 2026-09-19 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"testing"

	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/vendorcap"
)

func supported(s vendorcap.Support) bool {
	return s.State == vendorcap.StateNative || s.State == vendorcap.StateSoftware
}

func TestVendorcapMatchesPartitionWiring(t *testing.T) {
	r := &AcceleratorPartitionPolicyReconciler{}
	for _, vendor := range []string{"nvidia", "furiosa", "rebellions", "tenstorrent"} {
		declared := supported(vendorcap.LookupVendor(vendor, vendorcap.CapPartition))
		_, err := r.backendFor(vendor)
		wired := err == nil
		if declared != wired {
			t.Errorf("%s partition: 레지스트리=%v, backendFor 배선=%v — 어긋남", vendor, declared, wired)
		}
	}
}

func TestVendorcapMatchesSharingWiring(t *testing.T) {
	r := &AcceleratorPartitionPolicyReconciler{}
	for _, vendor := range []string{"nvidia", "furiosa"} { // backendFor 가 있는 벤더만
		b, err := r.backendFor(vendor)
		if err != nil {
			t.Fatalf("%s: backendFor 실패 %v", vendor, err)
		}
		_, sharingErr := partition.SharingFor(b)
		wired := sharingErr == nil
		ts := supported(vendorcap.LookupVendor(vendor, vendorcap.CapTimeSlicing))
		mp := supported(vendorcap.LookupVendor(vendor, vendorcap.CapMultiProcess))
		if (ts || mp) != wired {
			t.Errorf("%s sharing: 레지스트리(ts=%v,mp=%v), SharingBackend 구현=%v — 어긋남", vendor, ts, mp, wired)
		}
	}
}

// devicePluginEnsure 는 "레지스트리가 devicePlugin 을 지원이라 선언한 칸" ↔ "실제 ensure 함수"
// 대응표다. 값은 메서드 식(method expression)이라 컴파일 시점 참조다 — ensure 함수가 이름이
// 바뀌거나 사라지면 이 파일이 빌드되지 않는다(그게 이 표의 목적이다). 시그니처는 벤더마다
// 다르므로(RNGD 는 partitionPolicy 인자를 더 받는다) any 로 담고 존재만 본다.
var devicePluginEnsure = map[vendorcap.Key]any{
	{Vendor: "nvidia"}:                     (*NPUClusterPolicyReconciler).ensureNvidiaDevicePlugin,
	{Vendor: "furiosa", Product: "warboy"}: (*NPUClusterPolicyReconciler).ensureFuriosaDevicePlugin,
	{Vendor: "furiosa", Product: "rngd"}:   (*NPUClusterPolicyReconciler).ensureFuriosaRngdDevicePlugin,
	{Vendor: "rebellions"}:                 (*NPUClusterPolicyReconciler).ensureRebellionsDevicePlugin,
	{Vendor: "tenstorrent"}:                (*NPUClusterPolicyReconciler).ensureTenstorrentDevicePlugin,
}

// 선언 ⇔ 배선 양방향을 본다: 지원이라 선언한 칸은 ensure 함수가 있어야 하고,
// ensure 함수가 있는 칸은 레지스트리에 지원으로 적혀 있어야 한다. 한쪽만 보면 레지스트리에
// 칸을 추가하고 구현을 잊거나, 구현을 지우고 레지스트리를 안 고쳐도 초록으로 남는다.
func TestVendorcapMatchesDevicePluginWiring(t *testing.T) {
	for _, k := range vendorcap.Keys() {
		s, ok := vendorcap.Lookup(k.Vendor, k.Product, vendorcap.CapDevicePlugin)
		if !ok || !supported(s) {
			continue
		}
		if devicePluginEnsure[k] == nil {
			t.Errorf("%s/%s devicePlugin=%s 로 선언됐는데 ensure*DevicePlugin 배선이 없다",
				k.Vendor, k.Product, s.State)
		}
	}
	for k := range devicePluginEnsure {
		s, ok := vendorcap.Lookup(k.Vendor, k.Product, vendorcap.CapDevicePlugin)
		if !ok {
			t.Errorf("%s/%s 는 ensure*DevicePlugin 이 있는데 레지스트리에 그 칸이 없다", k.Vendor, k.Product)
			continue
		}
		if !supported(s) {
			t.Errorf("%s/%s 는 ensure*DevicePlugin 이 있는데 레지스트리 선언은 %s 다",
				k.Vendor, k.Product, s.State)
		}
	}
}
