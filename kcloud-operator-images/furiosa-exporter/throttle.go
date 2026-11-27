// ============================================================
// throttle.go: 쓰로틀 사유 비트별 변화 계수(최근 창 안의 횟수)
// 상세: 공식 furiosa-metrics-exporter(Apache-2.0)의 집계 방식을 따른다 — 1초마다 읽은 사유 비트가 이전 값과 다르면
// 비트별로 1 을 세어 창(300초) 안의 개수를 gauge 로 낸다. 마지막 관측값은 창과 따로 쥔다(공식은 창이 비면 기준점을 잃는다).
// 사유 이름은 노드 드라이버가 내는 비트표(throttle_reason_list)를 따르고, 읽지 못하면 SMI 헤더의 8종을 쓴다.
// 생성일: 2026-10-13 | 수정일: 2026-10-15
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// throttleBit 은 쓰로틀 사유 비트 하나와 그 라벨 이름이다.
type throttleBit struct {
	name string
	bit  uint32
}

// headerThrottleLabels 는 furiosa-smi 헤더(FURIOSA_SMI_THROTTLE_REASON_*)의 8종이다. 공식 exporter 도
// 이 표를 쓴다.
var headerThrottleLabels = []throttleBit{
	{"idle", 1 << 0}, {"thermal_slowdown", 1 << 1}, {"app_power_cap", 1 << 2}, {"app_clock_cap", 1 << 3},
	{"hw_clock_cap", 1 << 4}, {"hw_bus_limit", 1 << 5}, {"hw_power_cap", 1 << 6}, {"other_reason", 1 << 7},
}

// throttleLabels 는 이 프로세스가 쓰는 비트표다. main 이 수집을 시작하기 전에 한 번 정하고, 노드
// 드라이버의 표가 바뀌면 프로세스를 끝내 새 표로 다시 시작한다(바꾸지 않고 다시 대입하지 않는다).
var throttleLabels = headerThrottleLabels

// throttleReasonListGlob 은 RNGD 드라이버가 사유 비트표를 내는 sysfs 파일이다. 드라이버에 따라
// 0x80 이 GOV_CLOCK_CAP 이고 OTHER_REASON 이 0x100 이라, 헤더 표로 세면 governor 클럭 제한이
// other_reason 으로 잡힌다. 그래서 노드의 표가 있으면 그 표를 쓴다.
const throttleReasonListGlob = "/sys/class/rngd_mgmt/*/throttle_reason_list"

var throttleListLine = regexp.MustCompile(`^THROTTLE_([A-Z0-9_]+)\s*=\s*0x([0-9a-fA-F]+)$`)

// parseThrottleReasonList 는 "THROTTLE_IDLE = 0x00000001" 꼴 줄에서 비트표를 만든다. 한 비트짜리
// 값만 받고, 이름이나 비트가 앞 줄과 겹치면 건너뛴다(같은 라벨이 두 번 나가면 스크레이프 전체가
// 실패한다). 받을 줄이 없으면 nil 이다.
func parseThrottleReasonList(text string) []throttleBit {
	var out []throttleBit
	seenName, seenBit := map[string]bool{}, map[uint32]bool{}
	for _, line := range strings.Split(text, "\n") {
		m := throttleListLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		v, err := strconv.ParseUint(m[2], 16, 32)
		if err != nil || v == 0 || v&(v-1) != 0 {
			continue
		}
		name, bit := strings.ToLower(m[1]), uint32(v)
		if seenName[name] || seenBit[bit] {
			continue
		}
		seenName[name], seenBit[bit] = true, true
		out = append(out, throttleBit{name: name, bit: bit})
	}
	return out
}

// throttleTableChanged 는 노드의 비트표를 다시 읽어 cur 와 다른지 본다. 파일이 없거나 읽을 줄이
// 없으면(드라이버 교체 중 장치가 잠깐 사라진 구간 등) 바뀌지 않은 것으로 본다.
func throttleTableChanged(glob string, cur []throttleBit) bool {
	t, src := loadThrottleLabels(glob)
	if src == "" {
		return false
	}
	if len(t) != len(cur) {
		return true
	}
	bits := make(map[string]uint32, len(cur))
	for _, b := range cur {
		bits[b.name] = b.bit
	}
	for _, b := range t {
		if v, ok := bits[b.name]; !ok || v != b.bit {
			return true
		}
	}
	return false
}

// loadThrottleLabels 는 glob 에 걸린 첫 비트표를 읽는다. 없거나 읽을 줄이 없으면 헤더 표다.
// 두 번째 값은 표를 읽은 파일이고, 헤더 표면 빈 문자열이다.
func loadThrottleLabels(glob string) ([]throttleBit, string) {
	paths, _ := filepath.Glob(glob)
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if t := parseThrottleReasonList(string(raw)); len(t) > 0 {
			return t, p
		}
	}
	return headerThrottleLabels, ""
}

// throttleChange 는 사유가 바뀐 시각과 그때 값이 달라진 비트 이름이다.
type throttleChange struct {
	at      time.Time
	changed []string
}

// throttleDevice 는 장치 하나의 마지막 관측값과 창 안의 변화다. 마지막 관측값은 변화 기록과 따로
// 쥔다 — 변화가 창에서 밀려나도 비교 기준은 남아야 오래 이어진 정상 구간 뒤의 첫 변화를 센다.
type throttleDevice struct {
	last    uint32
	changes []throttleChange
}

// throttleWindow 는 장치(UUID)별 사유 변화 기록이다. 첫 관측은 기준점이라 변화로 세지 않는다.
type throttleWindow struct {
	mu      sync.Mutex
	window  time.Duration
	devices map[string]*throttleDevice
}

func newThrottleWindow(window time.Duration) *throttleWindow {
	return &throttleWindow{window: window, devices: map[string]*throttleDevice{}}
}

func (w *throttleWindow) observe(uuid string, reason uint32, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	d, seen := w.devices[uuid]
	if !seen {
		w.devices[uuid] = &throttleDevice{last: reason}
		return
	}
	if d.last != reason {
		var changed []string
		for _, l := range throttleLabels {
			if reason&l.bit != d.last&l.bit {
				changed = append(changed, l.name)
			}
		}
		d.changes = append(d.changes, throttleChange{at: now, changed: changed})
		d.last = reason
	}
	cut := now.Add(-w.window)
	i := 0
	for i < len(d.changes) && d.changes[i].at.Before(cut) {
		i++
	}
	d.changes = d.changes[i:]
}

// counts 는 창 안의 비트별 변화 횟수다. 한 번도 관측하지 않은 장치는 ok=false.
func (w *throttleWindow) counts(uuid string, now time.Time) (map[string]float64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	d, ok := w.devices[uuid]
	if !ok {
		return nil, false
	}
	out := make(map[string]float64, len(throttleLabels))
	for _, l := range throttleLabels {
		out[l.name] = 0
	}
	cut := now.Add(-w.window)
	for _, c := range d.changes {
		if c.at.Before(cut) {
			continue
		}
		for _, n := range c.changed {
			out[n]++
		}
	}
	return out, true
}
