// ============================================================
// metrics.go: DeviceSample 을 Prometheus 지표로 방출한다(공식 furiosa-metrics-exporter 이름·라벨)
// 상세: 지표 이름·라벨은 furiosa-ai/furiosa-metrics-exporter(Apache-2.0)와 같다. 공식에 없는 governor·
// throttle_reason 은 같은 접두로 둔다. 읽지 못한 값은 내지 않는다 — 0 을 내면 사실이 아닌 값을 그린다.
// 생성일: 2026-08-07 | 수정일: 2026-10-15
// ============================================================
package main

import (
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// 공통 라벨 순서. namespace·pod·container 는 --kube-resources-label 을 켤 때만 뒤에 붙는다(공식과
// 같다 — 공식 바이너리 기본값은 꺼짐이고 공식 차트는 켠다).
var baseLabels = []string{"arch", "core", "device", "uuid", "pci_bus_id", "firmware_version", "driver_version", "hostname"}

// kubeLabels 는 장치를 할당받은 컨테이너 라벨이다. 할당되지 않은 장치는 빈 값이고, Prometheus 는
// 빈 값 라벨을 저장하지 않으므로 라벨이 없는 것과 같다.
var kubeLabels = []string{"namespace", "pod", "container"}

// descSet 은 한 라벨 구성의 지표 종류 묶음이다. 같은 지표 이름에 라벨 구성이 둘이면 수집이 거절되므로
// 프로세스는 둘 중 하나만 쓴다.
type descSet struct {
	coreUtil, coreFreq, governor, throttleReason, dramUsage, dramTotal,
	power, temp, alive, totalCycle, taskCycle, throttleEvents *prometheus.Desc
}

func newDescSet(labels []string) *descSet {
	d := func(name, help string, extra ...string) *prometheus.Desc {
		return prometheus.NewDesc(name, help, append(append([]string{}, labels...), extra...), nil)
	}
	return &descSet{
		coreUtil:       d("furiosa_npu_core_utilization", "PE 코어 사용률(%)"),
		coreFreq:       d("furiosa_npu_core_frequency", "PE 코어 동작 주파수(MHz)"),
		governor:       d("furiosa_npu_governor", "활성 전력 모드. 활성 모드만 1 로 낸다", "mode"),
		throttleReason: d("furiosa_npu_throttle_reason", "쓰로틀 사유 비트마스크. 0 이 정상. 비트 정의는 노드의 throttle_reason_list 에 있다"),
		dramUsage:      d("furiosa_npu_dram_usage", "DRAM 사용 바이트(공유 영역 포함)"),
		dramTotal:      d("furiosa_npu_dram_total", "DRAM 전체 바이트(공유 영역 포함)"),
		power:          d("furiosa_npu_hw_power", "장치 전력(W)", "label"),
		temp:           d("furiosa_npu_hw_temperature", "장치 온도(°C)", "label"),
		alive:          d("furiosa_npu_alive", "장치 liveness(0/1)"),
		totalCycle:     d("furiosa_npu_total_cycle_count", "PE 코어 누적 사이클 수"),
		taskCycle:      d("furiosa_npu_task_execution_cycle", "PE 코어가 작업을 실행한 누적 사이클 수"),
		throttleEvents: d("furiosa_npu_throttling_events_count", "최근 300초 동안 쓰로틀 사유 비트가 바뀐 횟수", "label"),
	}
}

var (
	plainDescs = newDescSet(baseLabels)
	kubeDescs  = newDescSet(append(append([]string{}, baseLabels...), kubeLabels...))
)

// Metrics 는 수집기다. Samples 는 마지막 수집 회차의 스냅샷을 돌려준다. Node 는 hostname 라벨 값이다.
// Throttle 은 장치(UUID)의 사유 비트별 최근 변화 횟수이고, 한 번도 읽지 못한 장치는 ok=false 다.
// Owner 는 장치(UUID)를 할당받은 컨테이너다. nil 이면 namespace·pod·container 라벨을 두지 않는다.
type Metrics struct {
	Node     string
	Samples  func() []DeviceSample
	Throttle func(uuid string) (counts map[string]float64, ok bool)
	Owner    func(uuid string) podRef
}

func (m *Metrics) descs() *descSet {
	if m.Owner != nil {
		return kubeDescs
	}
	return plainDescs
}

// Describe 는 이 수집기가 낼 수 있는 지표 종류를 알린다.
func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	d := m.descs()
	for _, x := range []*prometheus.Desc{d.coreUtil, d.coreFreq, d.governor, d.throttleReason,
		d.dramUsage, d.dramTotal, d.power, d.temp, d.alive, d.totalCycle, d.taskCycle, d.throttleEvents} {
		ch <- x
	}
}

// labels 는 baseLabels(Owner 가 있으면 kubeLabels 까지) 순서의 값이다. core 는 코어 단위 지표면 코어
// 번호, 장치 단위 지표면 코어 범위다. o 는 Collect 가 장치마다 한 번 조회한 소유 컨테이너다.
func (m *Metrics) labels(s DeviceSample, o podRef, core string, extra ...string) []string {
	lv := []string{s.Arch, core, s.Name, s.UUID, s.PCI, s.FirmwareVersion, s.DriverVersion, m.Node}
	if m.Owner != nil {
		lv = append(lv, o.Namespace, o.Pod, o.Container)
	}
	return append(lv, extra...)
}

// Collect 는 스크레이프 시점의 스냅샷을 지표로 옮긴다.
func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	d := m.descs()
	gauge := func(x *prometheus.Desc, v float64, lv []string) {
		ch <- prometheus.MustNewConstMetric(x, prometheus.GaugeValue, v, lv...)
	}
	counter := func(x *prometheus.Desc, v float64, lv []string) {
		ch <- prometheus.MustNewConstMetric(x, prometheus.CounterValue, v, lv...)
	}
	for _, s := range m.Samples() {
		dev := coreRange(s.CoreNum)
		// 장치마다 한 번만 조회한다 — 한 스크레이프 안에서 같은 장치의 시계열이 같은 파드를 가리키게.
		var o podRef
		if m.Owner != nil {
			o = m.Owner(s.UUID)
		}
		for _, c := range s.Cores {
			gauge(d.coreUtil, c.UsagePercent, m.labels(s, o, itoa(c.Core)))
		}
		for _, c := range s.CoreClocks {
			gauge(d.coreFreq, float64(c.MHz), m.labels(s, o, itoa(c.Core)))
		}
		for _, c := range s.Cycles {
			counter(d.totalCycle, float64(c.Total), m.labels(s, o, itoa(c.Core)))
			counter(d.taskCycle, float64(c.TaskExec), m.labels(s, o, itoa(c.Core)))
		}
		// 비활성 모드 행은 내지 않는다. 값에 문자열을 담을 수 없어 활성 모드를 라벨로 세운다.
		if s.Governor != "" {
			gauge(d.governor, 1, m.labels(s, o, dev, s.Governor))
		}
		if s.ThrottleValid {
			gauge(d.throttleReason, float64(s.ThrottleReason), m.labels(s, o, dev))
		}
		if m.Throttle != nil {
			if c, ok := m.Throttle(s.UUID); ok {
				for _, l := range throttleLabels {
					gauge(d.throttleEvents, c[l.name], m.labels(s, o, dev, l.name))
				}
			}
		}
		if s.DramTotalBytes > 0 {
			gauge(d.dramUsage, float64(s.DramInUseBytes), m.labels(s, o, dev))
			gauge(d.dramTotal, float64(s.DramTotalBytes), m.labels(s, o, dev))
		}
		if s.PowerWatts > 0 {
			gauge(d.power, s.PowerWatts, m.labels(s, o, dev, "rms"))
		}
		if s.TempSocPeak > 0 {
			gauge(d.temp, s.TempSocPeak, m.labels(s, o, dev, "peak"))
		}
		if s.TempAmbient > 0 {
			gauge(d.temp, s.TempAmbient, m.labels(s, o, dev, "ambient"))
		}
		if s.AliveValid {
			alive := 0.0
			if s.Alive {
				alive = 1
			}
			gauge(d.alive, alive, m.labels(s, o, dev))
		}
	}
}

// coreRange 는 장치 단위 지표의 core 라벨이다. 공식과 같은 형식("0-<마지막 코어>", 코어가 하나면 "0")을 코어 수로 만든다.
func coreRange(n uint32) string {
	if n <= 1 {
		return "0"
	}
	return fmt.Sprintf("0-%d", n-1)
}

// itoa 는 코어 번호를 라벨 문자열로 만든다.
func itoa(v uint32) string {
	return strconv.FormatUint(uint64(v), 10)
}
