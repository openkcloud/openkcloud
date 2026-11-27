// ============================================================
// hwmon_test.go: hwmon 텔레메트리 수집기 단위 테스트
// 상세: 4벤더(RNGD/Warboy/ATOM/Blackhole) 실측 sysfs 레이아웃을 합성 트리로 재현해 PCI 조인·
// 단위 정규화·라벨 해석·무관 hwmon 배제를 검증한다. 값·파일명은 실측값 그대로 쓴다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSysFile 은 합성 sysfs 트리에 파일 하나를 만든다.
func writeSysFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
}

// addPciDevice 는 /sys/bus/pci/devices/<addr> 에 vendor/device/class 를 심는다.
func addPciDevice(t *testing.T, root, addr, vendor, device, class string) {
	t.Helper()
	base := filepath.Join(root, "bus/pci/devices", addr)
	writeSysFile(t, filepath.Join(base, "vendor"), vendor)
	writeSysFile(t, filepath.Join(base, "device"), device)
	writeSysFile(t, filepath.Join(base, "class"), class)
}

// addHwmon 은 hwmon 디렉터리를 만들고 device 심볼릭 링크를 devicePath 로 건다.
// devicePath 가 "" 면 링크를 만들지 않는다(coretemp 같은 비-PCI hwmon 재현).
func addHwmon(t *testing.T, root, name, devicePath string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "class/hwmon", name)
	writeSysFile(t, filepath.Join(dir, "name"), files["name"])
	for f, v := range files {
		if f == "name" {
			continue
		}
		writeSysFile(t, filepath.Join(dir, f), v)
	}
	if devicePath != "" {
		if err := os.MkdirAll(devicePath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(devicePath, filepath.Join(dir, "device")); err != nil {
			t.Fatal(err)
		}
	}
}

// buildFixture 는 4벤더 + 무관 hwmon 이 섞인 노드를 합성한다(실측 레이아웃).
func buildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dev := filepath.Join(root, "devices")

	// RNGD: vendor 0x1ed2 / device 0x0001, hwmon "rngd0", 전력은 power1_average.
	addPciDevice(t, root, "0000:27:00.0", "0x1ed2", "0x0001", "0x120000")
	rngdPath := filepath.Join(dev, "pci0000:26/0000:26:01.0/0000:27:00.0")
	addHwmon(t, root, "hwmon4", rngdPath, map[string]string{
		"name":           "rngd0",
		"temp1_input":    "36238",
		"temp1_label":    "PEAK",
		"temp10_input":   "34000",
		"temp10_label":   "MEMORY0",
		"power1_average": "37000000",
		"power1_label":   "RMS_TOTAL",
	})

	// Warboy: vendor 0x1ed2 / device 0x0000, hwmon "npu0", 전력 센서 없음.
	addPciDevice(t, root, "0000:af:00.0", "0x1ed2", "0x0000", "0x120000")
	warboyPath := filepath.Join(dev, "pci0000:ae/0000:ae:00.0/0000:af:00.0")
	addHwmon(t, root, "hwmon1", warboyPath, map[string]string{
		"name":        "npu0",
		"temp1_input": "47000",
		"temp1_label": "Peak",
	})

	// ATOM: 비표준 파일명(temp_input / card_power_input), device 링크 말단이 PCI 가 아님.
	addPciDevice(t, root, "0000:c3:00.0", "0x1eff", "0x1120", "0x120000")
	atomPath := filepath.Join(dev, "pci0000:c0/0000:c0:01.1/0000:c3:00.0/rebellions/rbln0")
	addHwmon(t, root, "hwmon0", atomPath, map[string]string{
		"name":             "rebellions",
		"temp_input":       "35000",
		"card_power_input": "18278596",
		"card_p_state":     "14",
	})

	// Tenstorrent Blackhole: 온도·전력 외 curr/fan/in 센서는 무시돼야 한다.
	addPciDevice(t, root, "0000:b1:00.0", "0x1e52", "0xb140", "0x120000")
	ttPath := filepath.Join(dev, "pci0000:b0/0000:b0:00.0/0000:b1:00.0")
	addHwmon(t, root, "hwmon7", ttPath, map[string]string{
		"name":         "blackhole",
		"temp1_input":  "54379",
		"temp1_label":  "asic_temp",
		"power1_input": "42000000",
		"power1_label": "power",
		"curr1_input":  "58000",
		"curr1_label":  "current",
		"fan1_input":   "1200",
		"in0_input":    "800",
	})

	// 무관 hwmon 2종: PCI 링크 없음(coretemp) / 가속기 아닌 PCI(nvme).
	addHwmon(t, root, "hwmon9", "", map[string]string{
		"name":        "coretemp",
		"temp1_input": "55000",
	})
	addPciDevice(t, root, "0000:02:00.0", "0x144d", "0xa80a", "0x010802")
	nvmePath := filepath.Join(dev, "pci0000:00/0000:00:1d.0/0000:02:00.0")
	addHwmon(t, root, "hwmon8", nvmePath, map[string]string{
		"name":        "nvme",
		"temp1_input": "41000",
	})

	return root
}

// findSensor 는 (pci, kind, label) 로 센서를 찾는다.
func findSensor(sensors []DeviceSensor, pci, kind, label string) (DeviceSensor, bool) {
	for _, s := range sensors {
		if s.PCI == pci && s.Kind == kind && s.Label == label {
			return s, true
		}
	}
	return DeviceSensor{}, false
}

func TestCollectSensors_AllVendors(t *testing.T) {
	root := buildFixture(t)
	sensors := collectSensorsFrom(root)

	cases := []struct {
		name              string
		pci, kind, label  string
		wantVendor, model string
		wantValue         float64
	}{
		// 온도 m°C → °C, 전력 µW → W.
		{"rngd temp", "0000:27:00.0", "temp", "peak", "furiosa", "rngd", 36.238},
		{"rngd mem temp", "0000:27:00.0", "temp", "memory0", "furiosa", "rngd", 34.0},
		{"rngd power", "0000:27:00.0", "power", "rms_total", "furiosa", "rngd", 37.0},
		{"warboy temp", "0000:af:00.0", "temp", "peak", "furiosa", "warboy", 47.0},
		// ATOM 은 _label 파일이 없어 파일명 stem 이 라벨이 된다.
		{"atom temp", "0000:c3:00.0", "temp", "temp", "rebellions", "atom", 35.0},
		{"atom power", "0000:c3:00.0", "power", "card_power", "rebellions", "atom", 18.278596},
		{"tt temp", "0000:b1:00.0", "temp", "asic_temp", "tenstorrent", "blackhole-p150", 54.379},
		{"tt power", "0000:b1:00.0", "power", "power", "tenstorrent", "blackhole-p150", 42.0},
	}
	for _, tc := range cases {
		s, ok := findSensor(sensors, tc.pci, tc.kind, tc.label)
		if !ok {
			t.Errorf("%s: sensor(%s,%s,%s) missing", tc.name, tc.pci, tc.kind, tc.label)
			continue
		}
		if s.Value < tc.wantValue-0.0001 || s.Value > tc.wantValue+0.0001 {
			t.Errorf("%s: value = %v, want %v", tc.name, s.Value, tc.wantValue)
		}
		if s.Vendor != tc.wantVendor || s.Model != tc.model {
			t.Errorf("%s: vendor/model = %s/%s, want %s/%s", tc.name, s.Vendor, s.Model, tc.wantVendor, tc.model)
		}
	}
}

// 무관 hwmon(coretemp/nvme)과 비-온도/전력 센서(curr/fan/in)는 방출되면 안 된다.
func TestCollectSensors_ExcludesNonAccelerator(t *testing.T) {
	root := buildFixture(t)
	for _, s := range collectSensorsFrom(root) {
		if s.Value == 55.0 || s.Value == 41.0 {
			t.Errorf("non-accelerator hwmon leaked: %+v", s)
		}
		if s.Kind != "temp" && s.Kind != "power" {
			t.Errorf("unexpected sensor kind: %+v", s)
		}
	}
}

// hwmon 이 하나도 없는 노드(NVIDIA 전용 등)에서 오류 없이 빈 결과를 낸다.
func TestCollectSensors_NoHwmon(t *testing.T) {
	root := t.TempDir()
	addPciDevice(t, root, "0000:18:00.0", "0x10de", "0x20b7", "0x030200")
	if got := collectSensorsFrom(root); len(got) != 0 {
		t.Errorf("expected no sensors, got %+v", got)
	}
}

// sysfs 자체가 없어도 panic 없이 빈 결과(fail-open).
func TestCollectSensors_MissingRoot(t *testing.T) {
	if got := collectSensorsFrom(filepath.Join(t.TempDir(), "nope")); len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

// 결과는 정렬돼 있어야 한다(스크레이프 간 시리즈 순서 안정).
func TestCollectSensors_Sorted(t *testing.T) {
	sensors := collectSensorsFrom(buildFixture(t))
	for i := 1; i < len(sensors); i++ {
		a, b := sensors[i-1], sensors[i]
		if a.PCI > b.PCI || (a.PCI == b.PCI && a.Kind > b.Kind) ||
			(a.PCI == b.PCI && a.Kind == b.Kind && a.Label > b.Label) {
			t.Fatalf("unsorted at %d: %+v then %+v", i, a, b)
		}
	}
}

// TestLiveHwmonDump 은 실제 노드에서 /sys 를 직접 읽어 수집 결과를 덤프한다.
// 합성 픽스처가 놓칠 수 있는 실 sysfs 형태(심볼릭 링크 깊이·파일명 변형)를 확인하는 용도로,
// KCLOUD_LIVE_HWMON=1 일 때만 동작한다(CI 는 항상 skip).
func TestLiveHwmonDump(t *testing.T) {
	if os.Getenv("KCLOUD_LIVE_HWMON") != "1" {
		t.Skip("set KCLOUD_LIVE_HWMON=1 to run on a real node")
	}
	sensors := collectSensorsFrom("/sys")
	if len(sensors) == 0 {
		t.Log("no accelerator sensors found on this node")
	}
	for _, s := range sensors {
		t.Logf("%s/%s %s %s=%s %.3f", s.Vendor, s.Model, s.PCI, s.Kind, s.Label, s.Value)
	}
}
