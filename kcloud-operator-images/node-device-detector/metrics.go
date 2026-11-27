// ============================================================
// metrics.go: node-agent Metrics 능력 (Prometheus /metrics 최소셋)
// 상세: 커스텀 레지스트리로 :9100/metrics 를 goroutine 으로 노출한다. 매 스캔 Snapshot 을 공유하는
// Collector 가 벤더-무관 최소셋과 hwmon 텔레메트리(kcloud_device_*)를 함께 방출한다.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsAddr = ":9100"

// 최소셋 metric 설명자(전 벤더, 스캔만으로 산출 — 벤더 SDK 불요).
var (
	descDeviceCount  = prometheus.NewDesc("kcloud_node_device_count", "감지된 가속기 디바이스 수", []string{"node", "vendor", "model"}, nil)
	descDriverLoaded = prometheus.NewDesc("kcloud_node_driver_loaded", "드라이버 로드 여부(0/1)", []string{"node", "vendor", "model"}, nil)
	descDriverInfo   = prometheus.NewDesc("kcloud_node_driver_info", "드라이버 버전 정보(값=1, version 라벨)", []string{"node", "vendor", "model", "version"}, nil)
	descAllocatable  = prometheus.NewDesc("kcloud_node_device_allocatable", "device-plugin allocatable 수량", []string{"node", "vendor", "resource"}, nil)
	descPassthrough  = prometheus.NewDesc("kcloud_node_passthrough_reserved", "노드 전량 vfio-pci 예약 여부(0/1)", []string{"node"}, nil)
	descValidation   = prometheus.NewDesc("kcloud_node_validation_passed", "Validation 통과 여부(0/1)", []string{"node", "vendor"}, nil)
	descScrapeTs     = prometheus.NewDesc("kcloud_node_agent_scrape_timestamp", "마지막 스캔 시각(unix epoch)", []string{"node"}, nil)
)

// 장치 텔레메트리(hwmon 수집, 벤더 SDK 불요). 최소셋과 달리 장치(PCI) 단위 계열이라
// kcloud_device_ 접두사를 쓴다. NVIDIA 는 hwmon 미등록이라 dcgm-exporter 가 담당한다.
var (
	descTemperature = prometheus.NewDesc("kcloud_device_temperature_celsius", "장치 센서 온도(°C)", []string{"node", "vendor", "model", "pci", "sensor"}, nil)
	descPower       = prometheus.NewDesc("kcloud_device_power_watts", "장치 전력(W)", []string{"node", "vendor", "model", "pci", "rail"}, nil)
	// PE 점유(Furiosa RNGD). mgmt 노드가 PCI 를 노출하지 않아 pci 대신 device 라벨을 쓴다.
	descPeOccupancy = prometheus.NewDesc("kcloud_device_pe_occupancy", "점유된 PE 수", []string{"node", "vendor", "model", "device"}, nil)
	descPeTotal     = prometheus.NewDesc("kcloud_device_pe_total", "전체 PE 수", []string{"node", "vendor", "model", "device"}, nil)
	// 스케줄 관점 사용률: 노드의 Pod requests 합(전 벤더 공통 정의).
	descAllocated = prometheus.NewDesc("kcloud_node_device_allocated", "Pod requests 로 할당된 가속기 수량", []string{"node", "vendor", "resource"}, nil)
)

// metricsCollector 는 최신 Snapshot/allocatable 을 읽어 최소셋을 방출하는 커스텀 Collector.
// gauge 를 미리 세팅하지 않고 Collect 시점에 const metric 으로 만들어 stale 시리즈를 피한다.
type metricsCollector struct {
	mu        sync.RWMutex
	snap      *Snapshot
	alloc     map[string]int64
	allocated map[string]int64
}

func newMetricsCollector() *metricsCollector { return &metricsCollector{} }

// Update 는 매 스캔 주기 최신 Snapshot/allocatable/allocated 를 원자적으로 교체한다.
// allocated 는 노드 Pod requests 합(스케줄 관점 사용률)이며 nil 이면 해당 시리즈를 내지 않는다.
func (m *metricsCollector) Update(snap *Snapshot, alloc, allocated map[string]int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap = snap
	m.alloc = alloc
	m.allocated = allocated
}

// Describe 를 비워두면 unchecked collector 로 동작한다(Collect 에서 동적 방출).
func (m *metricsCollector) Describe(chan<- *prometheus.Desc) {}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// Collect 는 최신 Snapshot 으로 최소셋 metric 을 방출한다.
func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.mu.RLock()
	snap, alloc, allocated := m.snap, m.alloc, m.allocated
	m.mu.RUnlock()
	if snap == nil {
		return
	}
	node := snap.Node
	ch <- prometheus.MustNewConstMetric(descScrapeTs, prometheus.GaugeValue, float64(snap.ScanTime.Unix()), node)
	ch <- prometheus.MustNewConstMetric(descPassthrough, prometheus.GaugeValue, boolToFloat(snap.PassthroughReserved), node)

	seenVendor := map[string]bool{}
	for _, d := range snap.Devices {
		ch <- prometheus.MustNewConstMetric(descDeviceCount, prometheus.GaugeValue, float64(d.count), node, d.vendor, d.model)
		ch <- prometheus.MustNewConstMetric(descDriverLoaded, prometheus.GaugeValue, boolToFloat(d.loaded), node, d.vendor, d.model)
		if d.ver != "" {
			ch <- prometheus.MustNewConstMetric(descDriverInfo, prometheus.GaugeValue, 1, node, d.vendor, d.model, d.ver)
		}
		key := vendorKey(d.vendor, d.model)
		if !seenVendor[key] {
			seenVendor[key] = true
			if res, val, ok := allocatableResourceForVendor(key, alloc); ok {
				ch <- prometheus.MustNewConstMetric(descAllocatable, prometheus.GaugeValue, float64(val), node, key, res)
			}
			// 스케줄 관점 사용률. allocatable 과 동일한 벤더 매핑을 재사용한다.
			if res, val, ok := allocatableResourceForVendor(key, allocated); ok {
				ch <- prometheus.MustNewConstMetric(descAllocated, prometheus.GaugeValue, float64(val), node, key, res)
			}
		}
	}
	if snap.Validation != nil {
		ch <- prometheus.MustNewConstMetric(descValidation, prometheus.GaugeValue, boolToFloat(snap.Validation.Passed), node, snap.Validation.Vendor)
	}

	// 장치 텔레메트리: 센서가 없으면(hwmon 미등록 벤더·미지원 노드) 시리즈 자체를 내지 않는다.
	for _, s := range snap.Sensors {
		switch s.Kind {
		case "temp":
			ch <- prometheus.MustNewConstMetric(descTemperature, prometheus.GaugeValue, s.Value, node, s.Vendor, s.Model, s.PCI, s.Label)
		case "power":
			ch <- prometheus.MustNewConstMetric(descPower, prometheus.GaugeValue, s.Value, node, s.Vendor, s.Model, s.PCI, s.Label)
		}
	}

	// PE 점유(현재 RNGD 만). 미지원 노드에서는 시리즈 자체가 없다.
	for _, o := range snap.Occupancy {
		ch <- prometheus.MustNewConstMetric(descPeOccupancy, prometheus.GaugeValue, o.Occupied, node, o.Vendor, o.Model, o.Device)
		ch <- prometheus.MustNewConstMetric(descPeTotal, prometheus.GaugeValue, o.Total, node, o.Vendor, o.Model, o.Device)
	}
}

// startMetricsServer 는 커스텀 레지스트리에 collector 를 등록하고 :9100/metrics 를 goroutine 으로 연다.
// 로컬 비특권 HTTP 서버 — 신규 RBAC 불요.
func startMetricsServer(collector *metricsCollector) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collector)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srv := &http.Server{Addr: metricsAddr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Println("metrics server err:", err)
		}
	}()
}
