// ============================================================
// throttle_test.go: 쓰로틀 사유 비트별 변화 계수(300초 창)·1초 표본·드라이버 비트표 시험
// 생성일: 2026-10-13 | 수정일: 2026-10-15
// ============================================================
package main

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var t0 = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

// 첫 관측은 기준점이라 세지 않는다. 같은 값이 이어지면 세지 않는다. 비트가 켜지고 꺼지는 양방향을 센다.
func TestThrottleWindowCountsBitChangesBothWays(t *testing.T) {
	w := newThrottleWindow(300 * time.Second)
	for i, r := range []uint32{0, 0, 2, 2, 0, 2 | 4} {
		w.observe("U", r, t0.Add(time.Duration(i)*time.Second))
	}
	got, ok := w.counts("U", t0.Add(6*time.Second))
	if !ok {
		t.Fatal("관측한 장치인데 ok=false")
	}
	// thermal_slowdown(1<<1): 0→2, 2→0, 0→6 세 번. app_power_cap(1<<2): 0→6 한 번.
	if got["thermal_slowdown"] != 3 || got["app_power_cap"] != 1 || got["idle"] != 0 {
		t.Errorf("counts=%v", got)
	}
	if len(got) != 8 {
		t.Errorf("라벨 %d개, 8 기대", len(got))
	}
}

// 기동 시점에 이미 켜져 있던 비트는 변화가 아니다. 이전 값이 없는 첫 관측을 0 으로 취급하면 여기서 센다.
func TestThrottleWindowFirstObservationIsBaseline(t *testing.T) {
	w := newThrottleWindow(300 * time.Second)
	w.observe("U", 2, t0)
	w.observe("U", 2, t0.Add(time.Second))
	got, _ := w.counts("U", t0.Add(2*time.Second))
	if got["thermal_slowdown"] != 0 {
		t.Errorf("기동 시점부터 켜진 비트를 변화로 셌다: %v", got)
	}
}

// 변화 없이 창보다 오래 지속된 뒤의 첫 변화도 센다. 기록이 창에서 밀려나며 기준점까지 잃으면
// 그 변화를 첫 관측으로 오인해 놓친다.
func TestThrottleWindowCountsChangeAfterLongSteadyState(t *testing.T) {
	w := newThrottleWindow(300 * time.Second)
	w.observe("U", 0, t0)
	w.observe("U", 0, t0.Add(400*time.Second))
	w.observe("U", 2, t0.Add(401*time.Second))
	got, _ := w.counts("U", t0.Add(402*time.Second))
	if got["thermal_slowdown"] != 1 {
		t.Errorf("긴 정상 구간 뒤의 첫 변화를 놓쳤다: %v", got)
	}
}

// 300초가 지난 변화는 창에서 빠진다.
func TestThrottleWindowDropsOldEvents(t *testing.T) {
	w := newThrottleWindow(300 * time.Second)
	w.observe("U", 0, t0)
	w.observe("U", 2, t0.Add(time.Second))
	got, _ := w.counts("U", t0.Add(302*time.Second))
	if got["thermal_slowdown"] != 0 {
		t.Errorf("창 밖 변화를 셌다: %v", got)
	}
}

func TestThrottleWindowUnknownDevice(t *testing.T) {
	if _, ok := newThrottleWindow(time.Minute).counts("X", t0); ok {
		t.Error("관측한 적 없는 장치인데 ok=true")
	}
}

func TestMetricsThrottlingEventsCount(t *testing.T) {
	s := sample()
	m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return []DeviceSample{s} },
		Throttle: func(string) (map[string]float64, bool) {
			c := map[string]float64{}
			for _, l := range throttleLabels {
				c[l.name] = 0
			}
			c["thermal_slowdown"] = 3
			return c, true
		}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	want := `
# HELP furiosa_npu_throttling_events_count 최근 300초 동안 쓰로틀 사유 비트가 바뀐 횟수
# TYPE furiosa_npu_throttling_events_count gauge
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="app_clock_cap"} 0
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="app_power_cap"} 0
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="hw_bus_limit"} 0
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="hw_clock_cap"} 0
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="hw_power_cap"} 0
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="idle"} 0
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="other_reason"} 0
furiosa_npu_throttling_events_count{` + lbl + `,core="0-7",label="thermal_slowdown"} 3
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(sortLabels(want)), "furiosa_npu_throttling_events_count"); err != nil {
		t.Error(err)
	}
}

// 창에 기록이 없는 장치(아직 한 번도 읽지 못함)와 창이 연결되지 않은 수집기는 지표를 내지 않는다 —
// 0 을 내면 "변화 없음" 이라는 사실이 아닌 값을 그린다.
func TestMetricsThrottlingEventsOmittedWhenUnobserved(t *testing.T) {
	s := sample()
	for name, fn := range map[string]func(string) (map[string]float64, bool){
		"미관측": func(string) (map[string]float64, bool) { return nil, false },
		"미연결": nil,
	} {
		m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return []DeviceSample{s} }, Throttle: fn}
		reg := prometheus.NewRegistry()
		reg.MustRegister(m)
		if err := testutil.GatherAndCompare(reg, strings.NewReader(""), "furiosa_npu_throttling_events_count"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

type throttleSrc struct {
	fakeSource
	mu     sync.Mutex
	reason uint32
	flap   bool // true 면 Devices 가 호출마다 정상 목록과 빈 목록을 번갈아 낸다
	calls  int
}

func (s *throttleSrc) Devices() ([]DeviceReading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.flap && s.calls%2 == 0 {
		return nil, nil
	}
	return s.readings, s.err
}

func (s *throttleSrc) ThrottleReasons() (map[string]uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]uint32{"UUID-A": s.reason}, nil
}

func (s *throttleSrc) set(reason uint32) {
	s.mu.Lock()
	s.reason = reason
	s.mu.Unlock()
}

// 판독이 실패하는 공급원이다. 못 읽은 표본은 창에 아무것도 남기지 않아야 한다.
type failingThrottleSrc struct{ fakeSource }

func (failingThrottleSrc) ThrottleReasons() (map[string]uint32, error) {
	return nil, errors.New("판독 실패")
}

func refresherWith(t *testing.T, src DeviceSource) *refresher {
	t.Helper()
	r := newRefresher(func() (DeviceSource, error) { return src, nil })
	r.giveUp = func() { t.Fatal("giveUp 이 불려서는 안 된다") }
	r.refresh()
	return r
}

// 표본이 공급원의 사유를 UUID 별로 창에 기록한다 — 켜지고 꺼지는 두 번의 변화가 센다.
func TestSampleThrottleRecordsReasonChanges(t *testing.T) {
	src := &throttleSrc{fakeSource: fakeSource{readings: liveDevice()}}
	r := refresherWith(t, src)
	for i, reason := range []uint32{0, 2, 0} {
		src.set(reason)
		r.sampleThrottle(t0.Add(time.Duration(i) * time.Second))
	}
	got, ok := r.throttle.counts("UUID-A", t0.Add(3*time.Second))
	if !ok || got["thermal_slowdown"] != 2 {
		t.Errorf("counts=%v ok=%v, thermal_slowdown 2 기대", got, ok)
	}
}

// 못 읽은 표본(공급원 없음·판독 미지원·판독 오류)은 "쓰로틀 해제" 로 읽히지 않고 건너뛴다.
func TestSampleThrottleSkipsWhenUnreadable(t *testing.T) {
	noSource := newRefresher(func() (DeviceSource, error) { return nil, errBoom })
	cases := map[string]*refresher{
		"공급원 없음": noSource,
		"판독 미지원": refresherWith(t, fakeSource{readings: liveDevice()}),
		"판독 오류":  refresherWith(t, failingThrottleSrc{fakeSource{readings: liveDevice()}}),
	}
	for name, r := range cases {
		r.sampleThrottle(t0)
		if _, ok := r.throttle.counts("UUID-A", t0); ok {
			t.Errorf("%s: 못 읽은 표본이 창에 기록됐다", name)
		}
	}
}

// 수집 회차가 공급원을 버리고 다시 만드는 동안 표본이 돌아도 경쟁·패닉이 없어야 한다(go test -race).
// Devices 가 정상 목록과 빈 목록을 번갈아 내므로 read() 가 r.src 를 유지하고 버리기를 반복한다.
func TestSampleThrottleSurvivesSourceReset(t *testing.T) {
	log.SetOutput(io.Discard) // 회차마다 찍는 재획득 로그를 줄인다
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	src := &throttleSrc{fakeSource: fakeSource{readings: liveDevice()}, flap: true}
	created := 0
	r := newRefresher(func() (DeviceSource, error) {
		created++
		return src, nil
	})
	r.giveUp = func() {}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.refresh()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.sampleThrottle(t0.Add(time.Duration(i) * time.Second))
		}
	}()
	wg.Wait()
	// 두 회차에 한 번꼴로 공급원을 다시 만든다(약 100회). 재획득 경로를 타지 않으면 이 시험은 의미가 없다.
	if created < 50 {
		t.Fatalf("공급원 재생성 %d회, 50회 이상 기대 — 재획득 경로를 타지 않았다", created)
	}
}

// rngd 드라이버 2026.3.1 의 /sys/class/rngd_mgmt/rngd!npu0mgmt/throttle_reason_list 원문이다.
const driverThrottleList = `Throttle reason bitmaps:
THROTTLE_IDLE                  = 0x00000001
THROTTLE_THERMAL_SLOWDOWN      = 0x00000002
THROTTLE_APP_POWER_CAP         = 0x00000004
THROTTLE_APP_CLOCK_CAP         = 0x00000008
THROTTLE_HW_CLOCK_CAP          = 0x00000010
THROTTLE_HW_BUS_LIMIT          = 0x00000020
THROTTLE_HW_POWER_CAP          = 0x00000040
THROTTLE_GOV_CLOCK_CAP         = 0x00000080
THROTTLE_OTHER_REASON          = 0x00000100
`

func TestParseThrottleReasonListDriverTable(t *testing.T) {
	got := parseThrottleReasonList(driverThrottleList)
	want := []throttleBit{
		{"idle", 0x1}, {"thermal_slowdown", 0x2}, {"app_power_cap", 0x4}, {"app_clock_cap", 0x8},
		{"hw_clock_cap", 0x10}, {"hw_bus_limit", 0x20}, {"hw_power_cap", 0x40},
		{"gov_clock_cap", 0x80}, {"other_reason", 0x100},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseThrottleReasonListSkipsNonBitLines(t *testing.T) {
	text := "Throttle reason bitmaps:\nTHROTTLE_NONE = 0x00000000\nTHROTTLE_BOTH = 0x00000003\nTHROTTLE_IDLE = 0x1\njunk\n"
	got := parseThrottleReasonList(text)
	if len(got) != 1 || got[0] != (throttleBit{"idle", 1}) {
		t.Errorf("got %v, want [idle 1] 만", got)
	}
}

func TestLoadThrottleLabelsPrefersDriverTable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rngd!npu0mgmt"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "rngd!npu0mgmt", "throttle_reason_list")
	if err := os.WriteFile(p, []byte(driverThrottleList), 0o644); err != nil {
		t.Fatal(err)
	}
	got, src := loadThrottleLabels(filepath.Join(dir, "*", "throttle_reason_list"))
	if src != p || len(got) != 9 {
		t.Errorf("src=%q len=%d, want %q 9", src, len(got), p)
	}
	got, src = loadThrottleLabels(filepath.Join(t.TempDir(), "*", "throttle_reason_list"))
	if src != "" || len(got) != len(headerThrottleLabels) {
		t.Errorf("파일 없음: src=%q len=%d, want 헤더 표", src, len(got))
	}
}

// 드라이버 표에서 0x80 은 governor 클럭 제한이다. 헤더 표로 세면 other_reason 으로 잡히던 값이다.
func TestThrottleWindowCountsGovClockCapWithDriverTable(t *testing.T) {
	prev := throttleLabels
	throttleLabels = parseThrottleReasonList(driverThrottleList)
	t.Cleanup(func() { throttleLabels = prev })

	w := newThrottleWindow(300 * time.Second)
	w.observe("U-A", 0, t0)
	w.observe("U-A", 0x80, t0.Add(time.Second))
	w.observe("U-A", 0x100, t0.Add(2*time.Second))
	c, ok := w.counts("U-A", t0.Add(3*time.Second))
	if !ok {
		t.Fatal("관측한 장치인데 ok=false")
	}
	if c["gov_clock_cap"] != 2 || c["other_reason"] != 1 || len(c) != 9 {
		t.Errorf("counts = %v, want gov_clock_cap 2(켜짐·꺼짐), other_reason 1, 9종", c)
	}
}

func TestParseThrottleReasonListSkipsDuplicates(t *testing.T) {
	text := "THROTTLE_IDLE = 0x1\nTHROTTLE_IDLE = 0x2\nTHROTTLE_OTHER = 0x1\nTHROTTLE_HW = 0x4\n"
	got := parseThrottleReasonList(text)
	want := []throttleBit{{"idle", 1}, {"hw", 4}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestThrottleTableChanged(t *testing.T) {
	dir := t.TempDir()
	glob := filepath.Join(dir, "*", "throttle_reason_list")
	if throttleTableChanged(glob, headerThrottleLabels) {
		t.Error("파일이 없는데 바뀐 것으로 봤다 — 드라이버 교체 중 재시작이 난다")
	}
	if err := os.MkdirAll(filepath.Join(dir, "rngd!npu0mgmt"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "rngd!npu0mgmt", "throttle_reason_list")
	if err := os.WriteFile(p, []byte(driverThrottleList), 0o644); err != nil {
		t.Fatal(err)
	}
	if !throttleTableChanged(glob, headerThrottleLabels) {
		t.Error("헤더 표로 떠 있는데 드라이버 표(9종)를 바뀌지 않은 것으로 봤다")
	}
	if throttleTableChanged(glob, parseThrottleReasonList(driverThrottleList)) {
		t.Error("같은 표인데 바뀐 것으로 봤다 — 매 주기 재시작한다")
	}
}

// 드라이버 표로 정하면 쓰로틀 횟수 지표도 그 표의 이름으로 나가야 한다(main 의 대입 뒤 Collect 경로).
func TestMetricsThrottlingEventsUseDriverTable(t *testing.T) {
	prev := throttleLabels
	throttleLabels = parseThrottleReasonList(driverThrottleList)
	t.Cleanup(func() { throttleLabels = prev })

	w := newThrottleWindow(300 * time.Second)
	w.observe("U-A", 0, t0)
	w.observe("U-A", 0x80, t0.Add(time.Second))
	s := sample()
	m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return []DeviceSample{s} },
		Throttle: func(uuid string) (map[string]float64, bool) { return w.counts(uuid, t0.Add(2*time.Second)) }}
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var series int
	var gov float64 = -1
	for _, mf := range mfs {
		if mf.GetName() != "furiosa_npu_throttling_events_count" {
			continue
		}
		for _, mt := range mf.GetMetric() {
			series++
			for _, lp := range mt.GetLabel() {
				if lp.GetName() == "label" && lp.GetValue() == "gov_clock_cap" {
					gov = mt.GetGauge().GetValue()
				}
			}
		}
	}
	if series != 9 || gov != 1 {
		t.Errorf("시계열 %d개, gov_clock_cap=%v — want 9개, 1", series, gov)
	}
}
