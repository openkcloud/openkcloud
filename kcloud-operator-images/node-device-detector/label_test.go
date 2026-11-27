// ============================================================
// label_test.go: NFD/GFD 라벨 계산 단위 테스트
// 상세: computeLabels(순수) 라벨 집합 + sanitizeLabelValue 정규화 검증.
// 생성일: 2026-07-16 | 수정일: 2026-11-03
// ============================================================
package main

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestComputeLabels_Nvidia(t *testing.T) {
	snap := &Snapshot{
		Node:    "worker1",
		Devices: []Detected{{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06"}},
	}
	got := computeLabels(snap)
	want := map[string]string{
		"kcloud.ai/nvidia.product":                    "generic",
		"kcloud.ai/nvidia.count":                      "2",
		"kcloud.ai/driver-nvidia.loaded":              "true",
		"kcloud.ai/driver-nvidia.version":             "580.65.06",
		"kcloud.ai/accelerator.present":               "true",
		"kcloud.ai/gpu.present":                       "true", // 옛 이름, 0.9 에서 제거
		"kcloud.ai/nvidia.present":                    "true",
		"feature.node.kubernetes.io/pci-10de.present": "true",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("label %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestComputeLabels_RngdKey(t *testing.T) {
	// furiosa/rngd 는 vendorKey=rngd 로 분리되어야 한다.
	snap := &Snapshot{
		Node:    "rngd-1",
		Devices: []Detected{{vendor: "furiosa", model: "rngd", count: 4, loaded: true, ver: "2025.1.0"}},
	}
	got := computeLabels(snap)
	if got["kcloud.ai/rngd.product"] != "rngd" {
		t.Errorf("rngd.product = %q, want rngd", got["kcloud.ai/rngd.product"])
	}
	if got["kcloud.ai/rngd.count"] != "4" {
		t.Errorf("rngd.count = %q, want 4", got["kcloud.ai/rngd.count"])
	}
	if got["feature.node.kubernetes.io/pci-1ed2.present"] != "true" {
		t.Errorf("pci-1ed2.present missing: %+v", got)
	}
	if got["kcloud.ai/driver-rngd.loaded"] != "true" {
		t.Errorf("driver-rngd.loaded = %q, want true", got["kcloud.ai/driver-rngd.loaded"])
	}
	// 자립 셀렉터: rngd present + furiosa-family present(통합 DS 공통 키).
	if got["kcloud.ai/rngd.present"] != "true" {
		t.Errorf("rngd.present missing: %+v", got)
	}
	if got["kcloud.ai/furiosa-family.present"] != "true" {
		t.Errorf("furiosa-family.present missing: %+v", got)
	}
}

func TestComputeLabels_NoDevices(t *testing.T) {
	got := computeLabels(&Snapshot{Node: "plain"})
	if len(got) != 0 {
		t.Errorf("expected no labels for node without devices, got %+v", got)
	}
}

func TestComputeLabels_DriverUnloaded(t *testing.T) {
	// 미로드 시 version 라벨은 생략, loaded=false.
	snap := &Snapshot{
		Node:    "w",
		Devices: []Detected{{vendor: "tenstorrent", model: "blackhole-p150", count: 1, loaded: false, ver: ""}},
	}
	got := computeLabels(snap)
	if got["kcloud.ai/driver-tenstorrent.loaded"] != "false" {
		t.Errorf("loaded label = %q, want false", got["kcloud.ai/driver-tenstorrent.loaded"])
	}
	if _, ok := got["kcloud.ai/driver-tenstorrent.version"]; ok {
		t.Errorf("version label should be omitted when empty")
	}
	if got["kcloud.ai/tenstorrent.product"] != "blackhole-p150" {
		t.Errorf("product = %q, want blackhole-p150", got["kcloud.ai/tenstorrent.product"])
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"580.65.06", "580.65.06"},
		{"blackhole-p150", "blackhole-p150"},
		{"foo/bar baz", "foo_bar_baz"},
		{"  spaced  ", "spaced"},
		{"-leading.trailing-", "leading.trailing"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeLabelValue(c.in); got != c.want {
			t.Errorf("sanitizeLabelValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 63자 초과 절단.
	long := ""
	for i := 0; i < 80; i++ {
		long += "a"
	}
	if got := sanitizeLabelValue(long); len(got) != 63 {
		t.Errorf("long value len = %d, want 63", len(got))
	}
}

// TestLabelKeepsOperatorOwnedLabel 은 detector 가 자기가 만들 수 없는 이름의 kcloud.ai 라벨을
// 지우지 않는지 고정한다. 지우면 operator 가 붙인 셀렉터 라벨이 스캔마다 사라진다 — 실제로
// mig-active·dra-owned 둘 다 "소유 라벨 목록"에 새 이름을 빠뜨려 이 사고가 났다. 이 시험은
// 목록에 없는 새 이름들로도 확인한다.
func TestLabelKeepsOperatorOwnedLabel(t *testing.T) {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	keep := []string{
		"kcloud.ai/nvidia.mig-active",
		"kcloud.ai/nvidia.dra-owned",
		"kcloud.ai/health.quarantined",
	}
	labels := map[string]string{
		// detector 가 만들 수 있는 이름인데 이번 스캔의 desired 에 없다 = 진짜 stale.
		"kcloud.ai/rngd.present": "true",
		"kcloud.ai/rngd.count":   "4",
	}
	for _, k := range keep {
		labels[k] = "true"
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1", Labels: labels}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(node).Build()
	snap := &Snapshot{Node: "worker1",
		Devices: []Detected{{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06"}}}
	if err := Label(context.Background(), c, snap); err != nil {
		t.Fatalf("Label: %v", err)
	}
	var got corev1.Node
	if err := c.Get(context.Background(), client.ObjectKey{Name: "worker1"}, &got); err != nil {
		t.Fatalf("get node: %v", err)
	}
	for _, k := range keep {
		if got.Labels[k] != "true" {
			t.Errorf("남의 라벨 %s 가 제거됐다: %v", k, got.Labels)
		}
	}
	for _, k := range []string{"kcloud.ai/rngd.present", "kcloud.ai/rngd.count"} {
		if _, ok := got.Labels[k]; ok {
			t.Errorf("detector 자기 소유의 stale 라벨 %s 는 제거돼야 한다: %v", k, got.Labels)
		}
	}
}

// TestDetectorOwns 는 소유 판정을 이름 모양으로 고정한다. computeLabels 가 실제로 내는 키는
// 전부 소유로, operator 가 붙이는 키는 전부 비소유로 갈려야 한다.
func TestDetectorOwns(t *testing.T) {
	snap := &Snapshot{Node: "worker1", Devices: []Detected{
		{vendor: "nvidia", model: "A30", count: 2, loaded: true, ver: "580.65.06"},
		{vendor: "furiosa", model: "rngd", count: 4, loaded: true, ver: "1.2.3"},
	}}
	for k := range computeLabels(snap) {
		if !strings.HasPrefix(k, labelPrefix) {
			continue // NFD 네임스페이스는 애초에 재조정 대상이 아니다.
		}
		if !detectorOwns(k) {
			t.Errorf("detector 가 만든 키인데 비소유로 판정됐다: %s", k)
		}
	}
	for _, k := range []string{
		"kcloud.ai/nvidia.mig-active",
		"kcloud.ai/nvidia.dra-owned",
		"kcloud.ai/rngd.dra-owned",
		"kcloud.ai/health.quarantined",
		"npu.ai/driver-upgrading",
		// 배제 노드 표시(operator 가 붙임) — detector 가 지우면 스캔 주기마다 되살아났다
		// 사라지는 mig-active·dra-owned 와 같은 사고가 재발한다.
		"kcloud.ai/excluded",
		"kcloud.ai/excluded-reason",
	} {
		if detectorOwns(k) {
			t.Errorf("남의 키인데 소유로 판정됐다: %s", k)
		}
	}
}
