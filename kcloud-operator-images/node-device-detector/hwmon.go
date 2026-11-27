// ============================================================
// hwmon.go: node-agent 장치 텔레메트리 수집 (커널 hwmon, 무특권)
// 상세: /sys/class/hwmon 을 훑어 device 링크의 PCI 주소가 스캔이 이미 인식한 가속기 집합에
// 있는 hwmon 만 고른다 — 이름 allowlist 없이 coretemp/nvme 같은 무관 hwmon 이 자동 배제된다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// sysRoot 는 sysfs 루트다. H() 가 /sys 를 hostPrefix 로 감싸지 않으므로(컨테이너
// 네임스페이스의 /sys 를 그대로 사용) 별도 변수로 두고 테스트에서만 교체한다.
var sysRoot = "/sys"

// DeviceSensor 는 가속기 1개의 센서 1개 값이다. Kind 는 "temp"(°C) 또는 "power"(W).
type DeviceSensor struct {
	Vendor string
	Model  string
	PCI    string
	Kind   string
	Label  string
	Value  float64
}

// pciMeta 는 PCI 주소에 대응하는 벤더/모델이다(Detected 의 vendor/model 과 동일 표기).
type pciMeta struct{ vendor, model string }

// rePciAddr 는 sysfs 경로 세그먼트가 PCI 주소(0000:af:00.0)인지 판정한다.
var rePciAddr = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$`)

// 센서 종류별 파일 glob. 벤더마다 파일명이 달라(실측: RNGD power1_average,
// Blackhole power1_input, ATOM card_power_input / temp_input) 패턴으로 흡수한다.
// glob 의 `*` 는 빈 문자열도 매칭하므로 temp*_input 하나로 temp_input 까지 덮는다.
var sensorGlobs = map[string][]string{
	"temp":  {"temp*_input"},
	"power": {"power*_input", "power*_average", "card_power*_input"},
}

// 센서 종류별 스케일: 커널 hwmon 규약상 온도는 m°C, 전력은 µW.
var sensorScale = map[string]float64{"temp": 1000, "power": 1e6}

// accelPciIndex 는 가속기 PCI 주소 → (vendor, model) 색인을 만든다.
// 매칭 함수는 scan.go 의 벤더 판정을 그대로 재사용해 판정 기준을 이중화하지 않는다.
func accelPciIndex(root string) map[string]pciMeta {
	idx := map[string]pciMeta{}
	dir := filepath.Join(root, "bus/pci/devices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return idx
	}
	for _, e := range entries {
		addr := e.Name()
		base := filepath.Join(dir, addr)
		v := readFileTrim(base + "/vendor")
		if v == "" {
			continue
		}
		d := readFileTrim(base + "/device")
		cls := strings.ToLower(readFileTrim(base + "/class"))
		switch {
		case matchNvidia(addr, v, d, cls):
			idx[addr] = pciMeta{"nvidia", "generic"}
		case matchRngd(addr, v, d, cls):
			idx[addr] = pciMeta{"furiosa", "rngd"}
		case matchWarboy(addr, v, d, cls):
			idx[addr] = pciMeta{"furiosa", "warboy"}
		case matchRebellions(addr, v, d, cls):
			idx[addr] = pciMeta{"rebellions", "atom"}
		case matchTenstorrent(addr, v, d, cls):
			idx[addr] = pciMeta{"tenstorrent", "blackhole-p150"}
		}
	}
	return idx
}

// hwmonPci 는 hwmon 디렉터리의 device 링크를 따라가 소속 가속기 PCI 주소를 찾는다.
// 링크 말단이 PCI 가 아닌 경우(실측 ATOM: .../0000:c3:00.0/rebellions/rbln0)를 위해
// 경로를 뒤에서부터 훑어 색인에 있는 첫 PCI 세그먼트를 취한다.
func hwmonPci(hwmonDir string, idx map[string]pciMeta) (string, bool) {
	target, err := filepath.EvalSymlinks(filepath.Join(hwmonDir, "device"))
	if err != nil {
		return "", false
	}
	seg := strings.Split(target, string(os.PathSeparator))
	for i := len(seg) - 1; i >= 0; i-- {
		if !rePciAddr.MatchString(seg[i]) {
			continue
		}
		if _, ok := idx[seg[i]]; ok {
			return seg[i], true
		}
	}
	return "", false
}

// sensorLabel 은 값 파일에 대응하는 라벨을 고른다. `<stem>_label` 파일이 있으면 그
// 내용을(실측: PEAK / MEMORY0 / asic_temp), 없으면 stem 자체를(실측 ATOM: card_power)
// 소문자 스네이크로 정규화해 쓴다.
func sensorLabel(dir, file string) string {
	stem := file
	for _, suf := range []string{"_input", "_average"} {
		stem = strings.TrimSuffix(stem, suf)
	}
	label := readFileTrim(filepath.Join(dir, stem+"_label"))
	if label == "" {
		label = stem
	}
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(label), " ", "_"))
}

// collectSensors 는 실제 노드에서 센서를 수집한다(Scan 이 호출).
func collectSensors() []DeviceSensor {
	return collectSensorsFrom(sysRoot)
}

// collectSensorsFrom 은 주어진 sysfs 루트에서 가속기 센서를 수집한다.
// 어떤 실패(경로 부재·파싱 실패·링크 깨짐)든 조용히 건너뛴다 — 텔레메트리 부재가
// 스캔·라벨·검증 경로를 막으면 안 된다(fail-open).
func collectSensorsFrom(root string) []DeviceSensor {
	idx := accelPciIndex(root)
	if len(idx) == 0 {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, "class/hwmon"))
	if err != nil {
		return nil
	}

	var out []DeviceSensor
	for _, e := range entries {
		dir := filepath.Join(root, "class/hwmon", e.Name())
		pci, ok := hwmonPci(dir, idx)
		if !ok {
			continue // 가속기에 속하지 않은 hwmon(coretemp/nvme/...)
		}
		meta := idx[pci]
		for kind, globs := range sensorGlobs {
			for _, g := range globs {
				matches, _ := filepath.Glob(filepath.Join(dir, g))
				for _, f := range matches {
					raw := readFileTrim(f)
					n, err := strconv.ParseFloat(raw, 64)
					if err != nil {
						continue
					}
					out = append(out, DeviceSensor{
						Vendor: meta.vendor, Model: meta.model, PCI: pci,
						Kind:  kind,
						Label: sensorLabel(dir, filepath.Base(f)),
						Value: n / sensorScale[kind],
					})
				}
			}
		}
	}

	// 시리즈 순서를 스크레이프 간 안정시킨다(map 순회 비결정성 제거).
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.PCI != b.PCI {
			return a.PCI < b.PCI
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Label < b.Label
	})
	return out
}
