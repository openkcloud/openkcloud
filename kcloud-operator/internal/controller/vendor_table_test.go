// ============================================================
// vendor_table_test.go: 벤더별 광고·배포 속성 함수 여섯의 결과 고정 시험
// 상세: advertiseVendors·소유 모드·DRA 켜짐·DRA 드라이버명·DaemonSet 이름·리소스명이
//       다섯 벤더 키와 모르는 벤더에서 돌려주는 값을 표로 고정한다.
// 생성일: 2026-10-13 | 수정일: 2026-10-13
// ============================================================

package controller

import (
	"context"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

func TestVendorFunctionsBaseline(t *testing.T) {
	ctx := context.Background()
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Tenstorrent.ResourceName = "tenstorrent.com/n300"
	p.Spec.Rebellions.ResourcePrefix, p.Spec.Rebellions.ResourceName = "rebellions.ai", "ATOM"
	cases := []struct {
		vendor, driver, ds, resource string
		owned                        bool
	}{
		{vendorNvidia, draDriverNameNvidia, draDSNameNvidia, nvidiaGPUResource, true},
		{vendorFuriosa, draDriverNameFuriosa, draDSNameFuriosa, mustWhole(t, "warboy"), true},
		{vendorRngd, draDriverNameFuriosa, draDSNameFuriosa, mustWhole(t, "rngd"), true},
		{vendorTenstorrent, draDriverNameTenstorrent, draDSNameTenstorrent, "tenstorrent.com/n300", true},
		{vendorRebellions, draDriverNameRebellions, draDSNameRebellions, "rebellions.ai/ATOM", true},
		{"unknown", "", "", "", true},
	}
	for _, c := range cases {
		if got := advertiseVendorDriverName(c.vendor); got != c.driver {
			t.Errorf("%s driver=%q, %q 기대", c.vendor, got, c.driver)
		}
		if got := advertiseVendorDRADSName(c.vendor); got != c.ds {
			t.Errorf("%s ds=%q, %q 기대", c.vendor, got, c.ds)
		}
		if got := advertiseVendorResourceName(ctx, p, c.vendor); got != c.resource {
			t.Errorf("%s resource=%q, %q 기대", c.vendor, got, c.resource)
		}
		if got := vendorIsOperatorOwned(p, c.vendor); got != c.owned {
			t.Errorf("%s owned=%v, %v 기대", c.vendor, got, c.owned)
		}
		if got := vendorDRAEnabled(p, c.vendor); got {
			t.Errorf("%s dra 빈 정책에서 true", c.vendor)
		}
	}
	if keys := advertiseVendors(nil); len(keys) != 5 {
		t.Errorf("advertiseVendors 키 %d개, 5 기대", len(keys))
	}

	// 빈 정책의 리소스명 기본값: tenstorrent·rebellions 는 spec 이 비면 기본 이름을 쓴다.
	empty := &npuv1alpha1.NPUClusterPolicy{}
	if got := advertiseVendorResourceName(ctx, empty, vendorTenstorrent); got != ttResourceDefault {
		t.Errorf("tenstorrent 기본 resource=%q, %q 기대", got, ttResourceDefault)
	}
	if got, want := advertiseVendorResourceName(ctx, empty, vendorRebellions),
		rbllnsResourcePrefixDfault+"/"+rbllnsResourceNameDefault; got != want {
		t.Errorf("rebellions 기본 resource=%q, %q 기대", got, want)
	}

	// 벤더마다 다른 노드 선택자를 넣어 advertiseVendors 의 각 키가 자기 spec 필드를 읽는지 본다.
	adv := &npuv1alpha1.NPUClusterPolicy{}
	sel := func(v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{"v": v}}
	}
	adv.Spec.Nvidia.AdvertiseByNodeSelector = sel(vendorNvidia)
	adv.Spec.Furiosa.AdvertiseByNodeSelector = sel(vendorFuriosa)
	adv.Spec.Furiosa.Rngd.AdvertiseByNodeSelector = sel(vendorRngd)
	adv.Spec.Tenstorrent.AdvertiseByNodeSelector = sel(vendorTenstorrent)
	adv.Spec.Rebellions.AdvertiseByNodeSelector = sel(vendorRebellions)
	for v, a := range advertiseVendors(adv) {
		if a.AdvertiseByNodeSelector == nil || a.AdvertiseByNodeSelector.MatchLabels["v"] != v {
			t.Errorf("advertiseVendors[%s] 가 %v 를 읽는다", v, a.AdvertiseByNodeSelector)
		}
	}

	// 한 벤더 spec 만 바꾼 정책에서 어느 키가 영향을 받는지 고정한다. rngd 의 DRA 는
	// spec.furiosa.rngd.dra 를 따로 보고, 소유 모드는 spec.furiosa 를 따른다.
	all := []string{vendorNvidia, vendorFuriosa, vendorRngd, vendorTenstorrent, vendorRebellions}
	on := &npuv1alpha1.DRASpec{Enabled: true}
	draCases := []struct {
		set  func(*npuv1alpha1.NPUClusterPolicy)
		want []string
	}{
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Nvidia.DRA = on }, []string{vendorNvidia}},
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Furiosa.DRA = on }, []string{vendorFuriosa}},
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Furiosa.Rngd.DRA = on }, []string{vendorRngd}},
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Tenstorrent.DRA = on }, []string{vendorTenstorrent}},
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Rebellions.DRA = on }, []string{vendorRebellions}},
	}
	for i, c := range draCases {
		q := &npuv1alpha1.NPUClusterPolicy{}
		c.set(q)
		for _, v := range all {
			if got, want := vendorDRAEnabled(q, v), slices.Contains(c.want, v); got != want {
				t.Errorf("dra 사례 %d: %s=%v, %v 기대", i, v, got, want)
			}
		}
	}
	ownedCases := []struct {
		set     func(*npuv1alpha1.NPUClusterPolicy)
		notMine []string
	}{
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Nvidia.ManagementMode = "delegated" }, []string{vendorNvidia}},
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Furiosa.ManagementMode = "delegated" },
			[]string{vendorFuriosa, vendorRngd}},
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Tenstorrent.ManagementMode = "external" },
			[]string{vendorTenstorrent}},
		{func(q *npuv1alpha1.NPUClusterPolicy) { q.Spec.Rebellions.ManagementMode = "external" },
			[]string{vendorRebellions}},
	}
	for i, c := range ownedCases {
		q := &npuv1alpha1.NPUClusterPolicy{}
		c.set(q)
		for _, v := range all {
			if got, want := vendorIsOperatorOwned(q, v), !slices.Contains(c.notMine, v); got != want {
				t.Errorf("owned 사례 %d: %s=%v, %v 기대", i, v, got, want)
			}
		}
		if !vendorIsOperatorOwned(q, "unknown") {
			t.Errorf("owned 사례 %d: 모르는 벤더가 owned 아님", i)
		}
	}
}

func mustWhole(t *testing.T, model string) string {
	t.Helper()
	r, err := intent.WholeDeviceResource(vendorFuriosa, model)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
