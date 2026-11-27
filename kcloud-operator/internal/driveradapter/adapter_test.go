// ============================================================
// adapter_test.go: DriverAdapter 레지스트리 완전성·키 정합·현재값 잠금 시험
// 생성일: 2026-09-28 | 수정일: 2026-10-11
// ============================================================
package driveradapter

import (
	"reflect"
	"testing"

	"kcloud-operator/internal/vendorcap"
)

// TestEveryVendorcapKeyHasAdapter 는 능력 모델이 아는 (vendor, product) 마다 어댑터가
// 있는지 본다. 없으면 그 벤더는 컨트롤러가 어댑터로 물을 수 없어 조건 분기가 남는다.
func TestEveryVendorcapKeyHasAdapter(t *testing.T) {
	for _, k := range vendorcap.Keys() {
		if _, ok := Lookup(k.Vendor, k.Product); !ok {
			t.Errorf("%v 에 DriverAdapter 가 없다", k)
		}
	}
}

// TestAdapterKeyMatchesRegistryKey 는 조회한 어댑터가 자기 키를 바르게 돌려주는지 본다.
// 키가 어긋나면 조회는 되는데 판단은 다른 벤더 것을 쓰게 된다.
func TestAdapterKeyMatchesRegistryKey(t *testing.T) {
	for _, k := range Keys() {
		a, ok := Lookup(k.Vendor, k.Product)
		if !ok {
			t.Fatalf("%v 조회 실패", k)
		}
		if a.Key() != k {
			t.Errorf("어댑터 키 = %v, want %v", a.Key(), k)
		}
	}
}

// TestLookupUnknownReturnsFalse 는 등록되지 않은 조합이 nil,false 로 정직하게 답하는지 본다.
func TestLookupUnknownReturnsFalse(t *testing.T) {
	if a, ok := Lookup("acme", "gpu"); ok || a != nil {
		t.Errorf("미등록 조합이 %v, ok=%v — nil,false 이어야 한다", a, ok)
	}
}

// TestAdapterValues 는 각 메서드가 지금 컨트롤러가 내는 값을 그대로 돌려주는지 잠근다.
// 이 태스크는 판단을 옮기지 않으므로, 뒤 태스크가 분기를 옮길 때 이 값이 바뀌면 동작이
// 달라졌다는 뜻이다.
func TestAdapterValues(t *testing.T) {
	sel := func(label string) map[string]string { return map[string]string{label: presentLabelValue} }

	want := map[Key]struct {
		selector     map[string]string
		rmmod        string
		toolkit      bool
		aptAuth      bool
		runtimeClass string
	}{
		{Vendor: vendorNvidia, Product: ""}:             {sel("kcloud.ai/nvidia.present"), "rmmod nvidia_uvm nvidia_drm nvidia || true", true, false, "nvidia"},
		{Vendor: vendorFuriosa, Product: productWarboy}: {sel("kcloud.ai/furiosa.present"), "rmmod npu_pdma npu_mgmt || true", false, true, ""},
		{Vendor: vendorFuriosa, Product: productRngd}:   {sel("kcloud.ai/rngd.present"), "rmmod furiosa_rngd || true", false, false, ""},
		{Vendor: vendorRebellions, Product: ""}:         {sel("kcloud.ai/rebellions.present"), shellNoop, false, false, ""},
		{Vendor: vendorTenstorrent, Product: ""}:        {nil, shellNoop, false, false, ""},
	}

	if len(want) != len(Keys()) {
		t.Fatalf("잠금 표가 %d 키, 레지스트리는 %d 키 — 표를 맞춰라", len(want), len(Keys()))
	}

	for k, w := range want {
		a, ok := Lookup(k.Vendor, k.Product)
		if !ok {
			t.Fatalf("%v 조회 실패", k)
		}
		if got := a.PresentNodeSelector(); !reflect.DeepEqual(got, w.selector) {
			t.Errorf("%v PresentNodeSelector = %v, want %v", k, got, w.selector)
		}
		if got := a.RmmodCommand(); got != w.rmmod {
			t.Errorf("%v RmmodCommand = %q, want %q", k, got, w.rmmod)
		}
		if got := a.ToolkitRequired(); got != w.toolkit {
			t.Errorf("%v ToolkitRequired = %v, want %v", k, got, w.toolkit)
		}
		if got := a.AptAuthRequired(); got != w.aptAuth {
			t.Errorf("%v AptAuthRequired = %v, want %v", k, got, w.aptAuth)
		}
		if got := a.RuntimeClass(); got != w.runtimeClass {
			t.Errorf("%v RuntimeClass = %q, want %q", k, got, w.runtimeClass)
		}
	}
}

// TestPresentNodeSelectorNotShared 는 돌려준 맵을 고쳐도 다음 호출값이 그대로인지 본다 —
// 저장한 맵을 공유하면 한 호출자의 수정이 레지스트리를 오염시킨다.
func TestPresentNodeSelectorNotShared(t *testing.T) {
	a, ok := Lookup(vendorNvidia, "")
	if !ok {
		t.Fatal("nvidia 조회 실패")
	}
	first := a.PresentNodeSelector()
	for key := range first {
		first[key] = "mutated"
	}
	if reflect.DeepEqual(a.PresentNodeSelector(), first) {
		t.Errorf("두 번째 호출이 첫 맵의 수정을 그대로 반영 — 맵이 공유되고 있다: %v", first)
	}
}

// TestForModelResolvesModelToProduct 는 ForModel 이 (vendor, model) 을 어느 어댑터로 푸는지
// 고정한다. 빈 model·모르는 model 은 그 벤더 기본 product, 대문자 벤더는 소문자로, 모르는
// 벤더는 nil,false(호출부 default 경로)로 간다. 컨트롤러·Job 두 소비자가 이 함수로 옮겨
// 오므로 그 해소 동작을 여기서 잠근다.
func TestForModelResolvesModelToProduct(t *testing.T) {
	cases := []struct {
		vendor, model string
		wantKey       Key
		wantOK        bool
	}{
		{"nvidia", "", Key{Vendor: vendorNvidia}, true},
		{"nvidia", "generic", Key{Vendor: vendorNvidia}, true},
		{"NVIDIA", "generic", Key{Vendor: vendorNvidia}, true},
		{"furiosa", "warboy", Key{Vendor: vendorFuriosa, Product: productWarboy}, true},
		{"furiosa", "", Key{Vendor: vendorFuriosa, Product: productWarboy}, true},
		{"furiosa", "bogus", Key{Vendor: vendorFuriosa, Product: productWarboy}, true},
		{"furiosa", "rngd", Key{Vendor: vendorFuriosa, Product: productRngd}, true},
		{"Furiosa", "RNGD", Key{Vendor: vendorFuriosa, Product: productRngd}, true},
		{"rebellions", "", Key{Vendor: vendorRebellions}, true},
		{"rebellions", "anything", Key{Vendor: vendorRebellions}, true},
		{"tenstorrent", "", Key{Vendor: vendorTenstorrent}, true},
		{"acme", "", Key{}, false},
		{"acme", "gpu", Key{}, false},
		{"", "", Key{}, false},
	}
	for _, tc := range cases {
		a, ok := ForModel(tc.vendor, tc.model)
		if ok != tc.wantOK {
			t.Errorf("ForModel(%q,%q) ok=%v, want %v", tc.vendor, tc.model, ok, tc.wantOK)
			continue
		}
		if !tc.wantOK {
			if a != nil {
				t.Errorf("ForModel(%q,%q)=%v, want nil", tc.vendor, tc.model, a)
			}
			continue
		}
		if a.Key() != tc.wantKey {
			t.Errorf("ForModel(%q,%q) key=%v, want %v", tc.vendor, tc.model, a.Key(), tc.wantKey)
		}
	}
}

// TestAptAuthSecretName 은 secret 이름이 배포 문서(image-manifest.yaml 의 auth 항목)와
// 같은 값으로 고정돼 있는지 본다 — 바꾸려면 문서와 함께 바꿔야 한다.
func TestAptAuthSecretName(t *testing.T) {
	if AptAuthSecretName != "furiosa-apt-auth" {
		t.Fatalf("AptAuthSecretName = %q, want furiosa-apt-auth", AptAuthSecretName)
	}
}
