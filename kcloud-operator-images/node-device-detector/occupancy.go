// ============================================================
// occupancy.go: node-agent PE 점유 수집 (Furiosa RNGD, 무특권 sysfs)
// 상세: rngd_mgmt 의 pe_occupancy 는 PE 당 한 줄(0/1)이고 0444 라 특권이 필요 없다. mgmt 노드는
// PCI 가 없어 라벨에 device 이름(npu0)을 쓴다. Warboy pe_status 는 의미 미확정이라 추정하지
// 않고 제외한다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PeOccupancy 는 가속기 1개의 PE 점유 집계다.
type PeOccupancy struct {
	Vendor   string
	Model    string
	Device   string // 예: npu0
	Occupied float64
	Total    float64
}

// rngdMgmtClassDir 는 RNGD 관리 노드의 sysfs class 경로(루트 상대)다.
const rngdMgmtClassDir = "class/rngd_mgmt"

// collectOccupancy 는 실제 노드에서 PE 점유를 수집한다(Scan 이 호출).
func collectOccupancy() []PeOccupancy { return collectOccupancyFrom(sysRoot) }

// collectOccupancyFrom 은 주어진 sysfs 루트에서 RNGD PE 점유를 수집한다.
// 경로 부재·파싱 실패는 조용히 skip(fail-open) — 텔레메트리가 스캔을 막지 않는다.
func collectOccupancyFrom(root string) []PeOccupancy {
	entries, err := os.ReadDir(filepath.Join(root, rngdMgmtClassDir))
	if err != nil {
		return nil
	}
	var out []PeOccupancy
	for _, e := range entries {
		name := e.Name() // rngd!npu0mgmt / rngd!npu0pe0 / ...
		if !strings.HasSuffix(name, "mgmt") {
			continue // 관리 노드에만 pe_occupancy 가 있다
		}
		raw := readFileTrim(filepath.Join(root, rngdMgmtClassDir, name, "pe_occupancy"))
		if raw == "" {
			continue
		}
		occ, total, ok := parsePeOccupancy(raw)
		if !ok {
			continue
		}
		out = append(out, PeOccupancy{
			Vendor: "furiosa", Model: "rngd",
			Device:   deviceNameFromMgmt(name),
			Occupied: occ, Total: total,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

// parsePeOccupancy 는 PE 당 한 줄(0/1)인 원문을 (점유 수, 전체 수)로 집계한다.
// 숫자가 아닌 줄이 하나라도 있으면 실패로 본다(추정 금지).
func parsePeOccupancy(raw string) (occupied, total float64, ok bool) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := strconv.ParseFloat(line, 64)
		if err != nil {
			return 0, 0, false
		}
		total++
		if v != 0 {
			occupied++
		}
	}
	return occupied, total, total > 0
}

// deviceNameFromMgmt 는 class 엔트리명(rngd!npu0mgmt)에서 장치 이름(npu0)을 뽑는다.
func deviceNameFromMgmt(entry string) string {
	name := entry
	if i := strings.LastIndex(name, "!"); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(name, "mgmt")
}
