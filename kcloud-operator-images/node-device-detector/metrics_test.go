// ============================================================
// metrics_test.go: Metrics exporter 단위 테스트
// 상세: metricsCollector 를 레지스트리에 등록해 Gather 결과의 최소셋 값/라벨을 검증.
// 생성일: 2026-07-16 | 수정일: 2026-07-29
// ============================================================
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gaugeValue 는 Gather 결과에서 name+labels 에 해당하는 gauge 값을 찾는다(테스트 헬퍼).
func gaugeValue(mfs []*dto.MetricFamily, name string, labels map[string]string) (float64, bool) {
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m, labels) {
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	got := map[string]string{}
	for _, lp := range m.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func gatherFor(t *testing.T, snap *Snapshot, alloc map[string]int64) []*dto.MetricFamily {
	t.Helper()
	return gatherWithAllocated(t, snap, alloc, nil)
}

// gatherWithAllocated 는 allocated(Pod requests 합)까지 주입해 Gather 한다.
func gatherWithAllocated(t *testing.T, snap *Snapshot, alloc, allocated map[string]int64) []*dto.MetricFamily {
	t.Helper()
	m := newMetricsCollector()
	m.Update(snap, alloc, allocated)
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	return mfs
}

func TestMetrics_MinimalSet(t *testing.T) {
	snap := &Snapshot{
		Node:                "worker1",
		Devices:             []Detected{{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06"}},
		PassthroughReserved: false,
		Validation:          &ValidationResult{Passed: true, Vendor: "nvidia"},
		ScanTime:            time.Unix(1700000000, 0),
	}
	mfs := gatherFor(t, snap, map[string]int64{"nvidia.com/gpu": 2})

	if v, ok := gaugeValue(mfs, "kcloud_node_device_count", map[string]string{"node": "worker1", "vendor": "nvidia", "model": "generic"}); !ok || v != 2 {
		t.Errorf("device_count = %v ok=%v, want 2", v, ok)
	}
	if v, ok := gaugeValue(mfs, "kcloud_node_driver_loaded", map[string]string{"vendor": "nvidia"}); !ok || v != 1 {
		t.Errorf("driver_loaded = %v ok=%v, want 1", v, ok)
	}
	if _, ok := gaugeValue(mfs, "kcloud_node_driver_info", map[string]string{"version": "580.65.06"}); !ok {
		t.Errorf("driver_info with version label missing")
	}
	if v, ok := gaugeValue(mfs, "kcloud_node_device_allocatable", map[string]string{"vendor": "nvidia", "resource": "nvidia.com/gpu"}); !ok || v != 2 {
		t.Errorf("allocatable = %v ok=%v, want 2", v, ok)
	}
	if v, ok := gaugeValue(mfs, "kcloud_node_validation_passed", map[string]string{"node": "worker1", "vendor": "nvidia"}); !ok || v != 1 {
		t.Errorf("validation_passed = %v ok=%v, want 1", v, ok)
	}
	if v, ok := gaugeValue(mfs, "kcloud_node_agent_scrape_timestamp", map[string]string{"node": "worker1"}); !ok || v != 1700000000 {
		t.Errorf("scrape_timestamp = %v ok=%v, want 1700000000", v, ok)
	}
}

func TestMetrics_DriverUnloadedNoVersion(t *testing.T) {
	// 드라이버 미로드 + 버전 공란: driver_loaded=0, driver_info 시리즈 없음.
	snap := &Snapshot{
		Node:     "rngd-1",
		Devices:  []Detected{{vendor: "furiosa", model: "rngd", count: 1, loaded: false, ver: ""}},
		ScanTime: time.Unix(1, 0),
	}
	mfs := gatherFor(t, snap, nil)
	if v, ok := gaugeValue(mfs, "kcloud_node_driver_loaded", map[string]string{"vendor": "furiosa", "model": "rngd"}); !ok || v != 0 {
		t.Errorf("driver_loaded = %v ok=%v, want 0", v, ok)
	}
	if _, ok := gaugeValue(mfs, "kcloud_node_driver_info", map[string]string{"model": "rngd"}); ok {
		t.Errorf("driver_info should be absent when version empty")
	}
}

// 장치 텔레메트리: hwmon 센서가 있으면 kcloud_device_* 를 PCI/센서 라벨과 함께 방출한다.
func TestMetrics_DeviceSensors(t *testing.T) {
	snap := &Snapshot{
		Node:     "rngd-1",
		Devices:  []Detected{{vendor: "furiosa", model: "rngd", count: 1, loaded: true, ver: "2026.2.1"}},
		ScanTime: time.Unix(1, 0),
		Sensors: []DeviceSensor{
			{Vendor: "furiosa", Model: "rngd", PCI: "0000:27:00.0", Kind: "temp", Label: "peak", Value: 36.238},
			{Vendor: "furiosa", Model: "rngd", PCI: "0000:27:00.0", Kind: "power", Label: "rms_total", Value: 37},
		},
	}
	mfs := gatherFor(t, snap, nil)

	if v, ok := gaugeValue(mfs, "kcloud_device_temperature_celsius", map[string]string{
		"node": "rngd-1", "vendor": "furiosa", "model": "rngd", "pci": "0000:27:00.0", "sensor": "peak",
	}); !ok || v != 36.238 {
		t.Errorf("temperature = %v ok=%v, want 36.238", v, ok)
	}
	if v, ok := gaugeValue(mfs, "kcloud_device_power_watts", map[string]string{
		"node": "rngd-1", "pci": "0000:27:00.0", "rail": "rms_total",
	}); !ok || v != 37 {
		t.Errorf("power = %v ok=%v, want 37", v, ok)
	}
}

// 회귀 0: 센서가 없으면 기존 7 패밀리만 나오고 kcloud_device_* 패밀리는 생기지 않는다.
func TestMetrics_NoSensorsNoDeviceSeries(t *testing.T) {
	snap := &Snapshot{
		Node:     "worker1",
		Devices:  []Detected{{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06"}},
		ScanTime: time.Unix(1700000000, 0),
	}
	for _, mf := range gatherFor(t, snap, nil) {
		if strings.HasPrefix(mf.GetName(), "kcloud_device_") {
			t.Errorf("unexpected family without sensors: %s", mf.GetName())
		}
	}
}

func TestMetrics_NilSnapshotEmpty(t *testing.T) {
	m := newMetricsCollector() // Update 미호출 → snap nil
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(mfs) != 0 {
		t.Errorf("expected no metrics for nil snapshot, got %d families", len(mfs))
	}
}

// PE 점유(RNGD)는 occupied/total 두 시리즈로 방출된다.
func TestMetrics_PeOccupancy(t *testing.T) {
	snap := &Snapshot{
		Node:      "rngd-1",
		Devices:   []Detected{{vendor: "furiosa", model: "rngd", count: 1, loaded: true}},
		ScanTime:  time.Unix(1, 0),
		Occupancy: []PeOccupancy{{Vendor: "furiosa", Model: "rngd", Device: "npu0", Occupied: 3, Total: 8}},
	}
	mfs := gatherFor(t, snap, nil)
	if v, ok := gaugeValue(mfs, "kcloud_device_pe_occupancy", map[string]string{"node": "rngd-1", "device": "npu0"}); !ok || v != 3 {
		t.Errorf("pe_occupancy = %v ok=%v, want 3", v, ok)
	}
	if v, ok := gaugeValue(mfs, "kcloud_device_pe_total", map[string]string{"device": "npu0"}); !ok || v != 8 {
		t.Errorf("pe_total = %v ok=%v, want 8", v, ok)
	}
}

// allocated 는 allocatable 과 동일한 벤더 매핑으로 방출된다(MIG 조각 리소스 포함).
func TestMetrics_Allocated(t *testing.T) {
	snap := &Snapshot{
		Node:     "worker1",
		Devices:  []Detected{{vendor: "nvidia", model: "generic", count: 1, loaded: true}},
		ScanTime: time.Unix(1, 0),
	}
	mfs := gatherWithAllocated(t, snap,
		map[string]int64{"nvidia.com/gpu": 2},
		map[string]int64{"nvidia.com/gpu": 1})
	if v, ok := gaugeValue(mfs, "kcloud_node_device_allocated", map[string]string{"vendor": "nvidia", "resource": "nvidia.com/gpu"}); !ok || v != 1 {
		t.Errorf("allocated = %v ok=%v, want 1", v, ok)
	}
	// allocatable 은 그대로 유지되어야 한다(회귀 0).
	if v, ok := gaugeValue(mfs, "kcloud_node_device_allocatable", map[string]string{"resource": "nvidia.com/gpu"}); !ok || v != 2 {
		t.Errorf("allocatable = %v ok=%v, want 2", v, ok)
	}
}

// allocated 가 없으면(nil) 해당 시리즈를 내지 않는다.
func TestMetrics_AllocatedAbsent(t *testing.T) {
	snap := &Snapshot{
		Node:     "worker1",
		Devices:  []Detected{{vendor: "nvidia", model: "generic", count: 1, loaded: true}},
		ScanTime: time.Unix(1, 0),
	}
	for _, mf := range gatherFor(t, snap, map[string]int64{"nvidia.com/gpu": 2}) {
		if mf.GetName() == "kcloud_node_device_allocated" {
			t.Error("allocated 시리즈가 nil 입력에서 나오면 안 된다")
		}
	}
}
