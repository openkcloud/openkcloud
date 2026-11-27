// ============================================================
// metrics_test.go: 방출 지표 이름·라벨·값 시험(공식 furiosa-metrics-exporter 이름·라벨)
// 생성일: 2026-08-07 | 수정일: 2026-10-13
// ============================================================
package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const lbl = `arch="rngd",device="npu0",driver_version="2026.3.1, e28c5c0",firmware_version="2026.3.0, 2d3f72a",hostname="rngd-1",pci_bus_id="0000:27:00.0",uuid="U-A"`

func sample() DeviceSample {
	return DeviceSample{
		PCI: "0000:27:00.0", Name: "npu0", UUID: "U-A", Arch: "rngd", CoreNum: 8,
		FirmwareVersion: "2026.3.0, 2d3f72a", DriverVersion: "2026.3.1, e28c5c0",
	}
}

var (
	labelBlock = regexp.MustCompile(`\{[^}]*\}`)
	labelPair  = regexp.MustCompile(`\w+="[^"]*"`)
)

// sortLabels 는 기대 텍스트의 라벨 쌍을 이름순으로 맞춘다. GatherAndCompare 는 텍스트 그대로 비교하고
// 수집 쪽 라벨은 이름순으로 나오므로, 기대값을 적은 순서와 무관하게 비교하려면 먼저 정렬해야 한다.
func sortLabels(want string) string {
	return labelBlock.ReplaceAllStringFunc(want, func(b string) string {
		pairs := labelPair.FindAllString(b, -1)
		sort.Strings(pairs)
		return "{" + strings.Join(pairs, ",") + "}"
	})
}

func gather(t *testing.T, s DeviceSample, name, want string) {
	t.Helper()
	m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return []DeviceSample{s} }}
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	if err := testutil.GatherAndCompare(reg, strings.NewReader(sortLabels(want)), name); err != nil {
		t.Error(err)
	}
}

func TestMetricsCoreUtilizationPerCore(t *testing.T) {
	s := sample()
	s.Cores = []CoreReading{{Core: 0, UsagePercent: 42.5}}
	gather(t, s, "furiosa_npu_core_utilization", `
# HELP furiosa_npu_core_utilization PE 코어 사용률(%)
# TYPE furiosa_npu_core_utilization gauge
furiosa_npu_core_utilization{`+lbl+`,core="0"} 42.5
`)
}

func TestMetricsCoreFrequencyPerCore(t *testing.T) {
	s := sample()
	s.CoreClocks = []CoreClock{{Core: 1, MHz: 1500}}
	gather(t, s, "furiosa_npu_core_frequency", `
# HELP furiosa_npu_core_frequency PE 코어 동작 주파수(MHz)
# TYPE furiosa_npu_core_frequency gauge
furiosa_npu_core_frequency{`+lbl+`,core="1"} 1500
`)
}

func TestMetricsDeviceLevelUsesCoreRange(t *testing.T) {
	s := sample()
	s.DramTotalBytes, s.DramInUseBytes = 100, 25
	gather(t, s, "furiosa_npu_dram_usage", `
# HELP furiosa_npu_dram_usage DRAM 사용 바이트(공유 영역 포함)
# TYPE furiosa_npu_dram_usage gauge
furiosa_npu_dram_usage{`+lbl+`,core="0-7"} 25
`)
	gather(t, s, "furiosa_npu_dram_total", `
# HELP furiosa_npu_dram_total DRAM 전체 바이트(공유 영역 포함)
# TYPE furiosa_npu_dram_total gauge
furiosa_npu_dram_total{`+lbl+`,core="0-7"} 100
`)
}

func TestMetricsPowerAndTemperatureLabels(t *testing.T) {
	s := sample()
	s.PowerWatts, s.TempSocPeak, s.TempAmbient = 91.5, 61, 33
	gather(t, s, "furiosa_npu_hw_power", `
# HELP furiosa_npu_hw_power 장치 전력(W)
# TYPE furiosa_npu_hw_power gauge
furiosa_npu_hw_power{`+lbl+`,core="0-7",label="rms"} 91.5
`)
	gather(t, s, "furiosa_npu_hw_temperature", `
# HELP furiosa_npu_hw_temperature 장치 온도(°C)
# TYPE furiosa_npu_hw_temperature gauge
furiosa_npu_hw_temperature{`+lbl+`,core="0-7",label="ambient"} 33
furiosa_npu_hw_temperature{`+lbl+`,core="0-7",label="peak"} 61
`)
}

// liveness 를 읽었으면 0 도 낸다(장치가 죽었다는 사실). 읽지 못했으면 내지 않는다(공식과 같다).
func TestMetricsAliveZeroWhenRead(t *testing.T) {
	s := sample()
	s.AliveValid, s.Alive = true, false
	gather(t, s, "furiosa_npu_alive", `
# HELP furiosa_npu_alive 장치 liveness(0/1)
# TYPE furiosa_npu_alive gauge
furiosa_npu_alive{`+lbl+`,core="0-7"} 0
`)
}

func TestMetricsAliveOmittedWhenUnread(t *testing.T) {
	gather(t, sample(), "furiosa_npu_alive", "")
}

func TestMetricsGovernorAndThrottleReason(t *testing.T) {
	s := sample()
	s.Governor, s.ThrottleReason, s.ThrottleValid = "performance", 2, true
	gather(t, s, "furiosa_npu_governor", `
# HELP furiosa_npu_governor 활성 전력 모드. 활성 모드만 1 로 낸다
# TYPE furiosa_npu_governor gauge
furiosa_npu_governor{`+lbl+`,core="0-7",mode="performance"} 1
`)
	gather(t, s, "furiosa_npu_throttle_reason", `
# HELP furiosa_npu_throttle_reason 쓰로틀 사유 비트마스크. 0 이 정상. 비트 정의는 노드의 throttle_reason_list 에 있다
# TYPE furiosa_npu_throttle_reason gauge
furiosa_npu_throttle_reason{`+lbl+`,core="0-7"} 2
`)
}

// 사유를 읽었으면 0 도 낸다(쓰로틀 없음이라는 사실). 읽지 못한 것과는 의미가 다르다.
func TestMetricsEmitsThrottleReasonZeroWhenValid(t *testing.T) {
	s := sample()
	s.ThrottleValid, s.ThrottleReason = true, 0
	gather(t, s, "furiosa_npu_throttle_reason", `
# HELP furiosa_npu_throttle_reason 쓰로틀 사유 비트마스크. 0 이 정상. 비트 정의는 노드의 throttle_reason_list 에 있다
# TYPE furiosa_npu_throttle_reason gauge
furiosa_npu_throttle_reason{`+lbl+`,core="0-7"} 0
`)
}

func TestMetricsCycleCounters(t *testing.T) {
	s := sample()
	s.Cycles = []CoreCycle{{Core: 3, Total: 1000, TaskExec: 400}}
	gather(t, s, "furiosa_npu_total_cycle_count", `
# HELP furiosa_npu_total_cycle_count PE 코어 누적 사이클 수
# TYPE furiosa_npu_total_cycle_count counter
furiosa_npu_total_cycle_count{`+lbl+`,core="3"} 1000
`)
	gather(t, s, "furiosa_npu_task_execution_cycle", `
# HELP furiosa_npu_task_execution_cycle PE 코어가 작업을 실행한 누적 사이클 수
# TYPE furiosa_npu_task_execution_cycle counter
furiosa_npu_task_execution_cycle{`+lbl+`,core="3"} 400
`)
}

func TestMetricsOmitsUnreadAxes(t *testing.T) {
	for _, n := range []string{"furiosa_npu_core_frequency", "furiosa_npu_governor", "furiosa_npu_throttle_reason",
		"furiosa_npu_dram_usage", "furiosa_npu_hw_power", "furiosa_npu_hw_temperature",
		"furiosa_npu_total_cycle_count", "furiosa_npu_task_execution_cycle"} {
		gather(t, sample(), n, "")
	}
}

func TestMetricsEmitsNothingWhenNoSamples(t *testing.T) {
	m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return nil }}
	if got := testutil.CollectAndCount(m); got != 0 {
		t.Errorf("지표 수 = %d, 기대 0", got)
	}
}

func TestCoreRange(t *testing.T) {
	for n, want := range map[uint32]string{0: "0", 1: "0", 8: "0-7"} {
		if got := coreRange(n); got != want {
			t.Errorf("coreRange(%d)=%q, %q 기대", n, got, want)
		}
	}
}
