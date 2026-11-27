// ============================================================
// npuclusterpolicy_advertise_test.go: 광고 주체 선택 축(advertiseBy) 검증
// 상세: 미지정이 devicePlugin 으로 해석되는 하위 호환과, CRD 에 enum 이 실제로 실렸는지를 본다.
// 생성일: 2026-08-06
// ============================================================

package v1alpha1

import (
	"os"
	"strings"
	"testing"
)

// TestAdvertisedBy_DefaultsToDevicePlugin 은 하위 호환의 핵심 판정이다. 기존
// NPUClusterPolicy 는 이 필드를 갖고 있지 않으므로, 미지정이 곧 지금까지의 동작이어야 한다.
func TestAdvertisedBy_DefaultsToDevicePlugin(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", AdvertiseByDevicePlugin},
		{AdvertiseByDevicePlugin, AdvertiseByDevicePlugin},
		{AdvertiseByDRA, AdvertiseByDRA},
	}
	for _, c := range cases {
		got := AdvertiseSpec{AdvertiseBy: c.in}.AdvertisedBy()
		if got != c.want {
			t.Errorf("AdvertisedBy(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDRAOwnedNodeLabel 은 라벨 키 형식을 고정한다. DaemonSet affinity 와 reconciler 가
// 같은 문자열을 만들어야 하므로 조립을 한 함수로 묶고 그 결과를 여기서 못박는다.
func TestDRAOwnedNodeLabel(t *testing.T) {
	cases := map[string]string{
		"nvidia":      "kcloud.ai/nvidia.dra-owned",
		"tenstorrent": "kcloud.ai/tenstorrent.dra-owned",
	}
	for vendor, want := range cases {
		if got := DRAOwnedNodeLabel(vendor); got != want {
			t.Errorf("DRAOwnedNodeLabel(%q) = %q, want %q", vendor, got, want)
		}
	}
}

// TestCRDCarriesAdvertiseBy 는 생성된 CRD 에 축이 실제로 실렸는지 본다. 구조체에만 넣고
// make manifests 를 잊으면 클러스터가 그 필드를 통째로 잘라 버린다 — 코드는 멀쩡한데
// 라이브에서만 동작하지 않는 상태가 된다.
func TestCRDCarriesAdvertiseBy(t *testing.T) {
	b, err := os.ReadFile("../../config/crd/bases/npu.ai_npuclusterpolicies.yaml")
	if err != nil {
		t.Fatalf("CRD 읽기 실패: %v", err)
	}
	crd := string(b)
	if !strings.Contains(crd, "advertiseBy:") {
		t.Error("CRD 에 advertiseBy 없음 — make manifests 미실행")
	}
	if !strings.Contains(crd, "advertiseByNodeSelector:") {
		t.Error("CRD 에 advertiseByNodeSelector 없음")
	}
	// enum 이 빠지면 오타 값이 그대로 통과해 아무 일도 일어나지 않는 노드가 생긴다.
	if !strings.Contains(crd, "- devicePlugin") || !strings.Contains(crd, "- dra") {
		t.Error("CRD advertiseBy 에 enum(devicePlugin|dra) 없음")
	}
	// 벤더 블록 넷 모두에 실려야 한다. Rebellions ATOM 은 의도적으로 대상이 아니다.
	if n := strings.Count(crd, "advertiseBy:"); n < 4 {
		t.Errorf("advertiseBy 가 실린 벤더 블록 수 = %d, want >= 4", n)
	}
}
