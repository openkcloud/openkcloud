// ============================================================
// scan.go: 노드 디바이스 스캔 능력 (PCI/드라이버/벤더 감지 → Snapshot)
// 상세: 5벤더(NVIDIA/Furiosa Warboy/RNGD/Rebellions/Tenstorrent) PCI·드라이버 감지 로직 +
// 공통 호스트 FS 헬퍼. 매 주기 1회 스캔해 Snapshot 을 만들고 소비자가 공유한다.
// 생성일: 2026-03-25 | 수정일: 2026-10-27
// ============================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

/* ---------- 공통 유틸 ---------- */

var hostPrefix = "/host"

// 단, 커널 가상 FS(/proc, /sys, /dev)는 항상 컨테이너 네임스페이스의 경로를 그대로 사용한다.
// 이유: /host 밑에 bind-mount 되어 있지 않거나 의미가 달라 오탐/누락을 유발함.
func H(p string) string {
	if strings.HasPrefix(p, "/proc/") ||
		strings.HasPrefix(p, "/sys/") ||
		strings.HasPrefix(p, "/dev/") {
		return p
	}
	p = strings.TrimPrefix(p, "/")
	return filepath.Join(hostPrefix, p)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// markerPath 는 드라이버 설치기가 남기는 마커의 호스트 경로를 고른다.
// 개명(npu-operator → kcloud-operator) 후 경로를 먼저 보고, 없으면 개명 전 경로로
// 물러난다. 설치기가 노드를 한 번 이관하기 전까지는 옛 경로에만 마커가 있다.
func markerPath(name string) string {
	p := H(filepath.Join(markerDir, name))
	if fileExists(p) {
		return p
	}
	if legacy := H(filepath.Join(legacyMarkerDir, name)); fileExists(legacy) {
		return legacy
	}
	return p
}

func readFileTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func countGlob(glob string) int {
	m, _ := filepath.Glob(glob)
	return len(m)
}

func countGlobMany(globs ...string) int {
	total := 0
	for _, g := range globs {
		if m, _ := filepath.Glob(g); len(m) > 0 {
			total += len(m)
		}
	}
	return total
}

// 경로 존재(호스트 기준)
func hostExists(p string) bool {
	return fileExists(H(p))
}

// PCI 장치에 현재 바인딩된 드라이버명을 반환한다.
// /sys/bus/pci/devices/<addr>/driver 심볼릭 링크의 base(예: "vfio-pci", "nvidia", "nouveau").
// 링크가 없으면(바인딩 안 됨) 빈 문자열을 반환한다.
func pciDriverBound(addr string) string {
	target, err := os.Readlink(H("/sys/bus/pci/devices/" + addr + "/driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// containsFold: 문자열 슬라이스에 대소문자 무시 매칭 원소가 있는지.
func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// bindingSummary: 주어진 벤더 매칭 함수(match)에 해당하는 PCI 장치의 드라이버 바인딩 현황을
// 집계한다(pciDriverBound 재사용). total=매칭 장치 수, vfio=vfio-pci 바인딩 수(패스스루 예약),
// own=ownDrivers 바인딩 수(자기 벤더), free=그 외(미바인딩·타 드라이버·링크 없음 포함).
//
// ⚠️ NPU 드라이버는 표준 PCI driver 로 안 붙어 링크가 없을 수 있다 — 그 경우 free 로 분류되고,
// driverBinding() 이 driverLoaded(module 존재)로 보완한다.
func bindingSummary(match func(addr, v, d, cls string) bool, ownDrivers []string) (total, vfio, own, free int) {
	entries, _ := os.ReadDir(H(hostSysPciDevices))
	for _, e := range entries {
		addr := e.Name() // 0000:BB:DD.F
		base := filepath.Join(H(hostSysPciDevices), addr)
		v := readFileTrim(base + "/vendor")
		d := readFileTrim(base + "/device")
		cls := strings.ToLower(readFileTrim(base + "/class"))
		if v == "" {
			continue
		}
		if !match(addr, v, d, cls) {
			continue
		}
		total++
		switch drv := pciDriverBound(addr); {
		case drv == "vfio-pci":
			vfio++
		case drv != "" && containsFold(ownDrivers, drv):
			own++
		default:
			free++
		}
	}
	return
}

// driverBinding: 벤더 device 의 대표 바인딩 문자열을 계산한다(벤더 무관). 전량 vfio →
// "vfio-pci", 전량 자기드라이버 → ownName, vfio+own 혼재나 일부 vfio → "mixed", own 바인딩
// 존재 → ownName, PCI 링크 없이도 module 로드됨(loaded=true) → ownName(NPU 오탐 방지),
// 그 외 → "none". loaded=false 로 호출하면 nvidiaDriverBinding 과 동일한 결과를 낸다.
func driverBinding(ownName string, total, vfio, own int, loaded bool) string {
	if total == 0 {
		return "none"
	}
	if vfio == total {
		return "vfio-pci"
	}
	if own == total {
		return ownName
	}
	if vfio > 0 && own > 0 {
		return "mixed"
	}
	if own > 0 {
		return ownName
	}
	if vfio > 0 {
		return "mixed"
	}
	// vfio 0, own 0: PCI driver 링크가 없는 상태. NPU 드라이버가 module 로
	// 로드된 경우 free 로 두면 "none" 오탐이 되므로 자기 벤더명으로 보정한다.
	if loaded {
		return ownName
	}
	return "none"
}

/* ---------- 벤더별 PCI 매칭 (bindingSummary 용) ---------- */

// NVIDIA GPU 물리 함수(.0): vendor 0x10de & class 0x03(VGA/3D/Display).
func matchNvidia(addr, v, d, cls string) bool {
	return strings.HasSuffix(addr, ".0") &&
		strings.EqualFold(v, "0x10de") && strings.HasPrefix(cls, "0x03")
}

// Furiosa Warboy: vendor 0x1ed2 이면서 RNGD(0x0001)가 아닌 장치.
// device 0x0000 이거나 class 0x12xx(class 읽기 실패 포함)면 Warboy 로 본다.
// furiosaPciCount() - rngdPciCount() 와 동일한 집계 기준.
func matchWarboy(addr, v, d, cls string) bool {
	if !strings.EqualFold(v, furiosaVendorID) {
		return false
	}
	if strings.EqualFold(d, "0x0001") { // RNGD 제외
		return false
	}
	return strings.EqualFold(d, "0x0000") || cls == "" || strings.HasPrefix(cls, "0x12")
}

// Furiosa RNGD: vendor 0x1ed2 & device 0x0001.
func matchRngd(addr, v, d, cls string) bool {
	return strings.EqualFold(v, furiosaVendorID) && strings.EqualFold(d, "0x0001")
}

// Rebellions Atom+: vendor 0x1eff & 등록된 device ID.
func matchRebellions(addr, v, d, cls string) bool {
	if !strings.EqualFold(v, rebellionsVendorID) {
		return false
	}
	for _, want := range builtinRebellionsIDs {
		if strings.EqualFold(d, want.Device) {
			return true
		}
	}
	return false
}

// Tenstorrent: vendor 0x1e52(신규 SKU 대응을 위해 벤더 ID 만으로 매칭).
func matchTenstorrent(addr, v, d, cls string) bool {
	return strings.EqualFold(v, tenstorrentVendorID)
}

// isAccelerator: 알려진 가속기 벤더 중 하나라도 매칭되면 true(passthroughReserved 용).
func isAccelerator(addr, v, d, cls string) bool {
	return matchNvidia(addr, v, d, cls) ||
		matchWarboy(addr, v, d, cls) ||
		matchRngd(addr, v, d, cls) ||
		matchRebellions(addr, v, d, cls) ||
		matchTenstorrent(addr, v, d, cls)
}

/* ---------- 경로/상수 ---------- */

type PciID struct {
	Vendor string `json:"vendor"`
	Device string `json:"device"`
}

// nvidia-smi host 경로(공용 상수, migLgip 도 재사용).
const nvidiaSmiHostBin = "/usr/bin/nvidia-smi"

// 유저랜드 유틸 존재 검사(실제 배포체크에 유용)
func nvidiaSmiPresent() bool {
	// 기본 경로 우선
	if hostExists(nvidiaSmiHostBin) {
		return true
	}
	return false
}

// migLgip 은 `nvidia-smi mig -lgip` 원문을 반환한다(ACPP 의 MIG profile discovery 소스).
// nvidia-smi 부재, 또는 MIG 미지원 GPU 의 비-0 exit 등 어떤 실패든 조용히 "" 를 반환한다(best-effort).
// 전역(노드 전체) 수집이라 다중 GPU 노드에서 GPU 별 상태가 뒤섞일 수 있다 — per-PCI 관측은
// observeMig 를 쓴다. buildDevices 가 PCI 를 못 찾을 때만 폴백으로 남긴다.
func migLgip() string {
	if !nvidiaSmiPresent() {
		return ""
	}
	// 3s 타임아웃 — 공유 Scan() 루프가 wedge 된 nvidia-smi 로 무한 블록되지 않도록
	// (vendorCLISnapshot 와 동일 패턴). 타임아웃/에러/MIG 미지원 non-zero exit 는 "".
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := hostCommand(ctx, nvidiaSmiHostBin, "mig", "-lgip").CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

// hostLoaderCandidates 는 host 의 동적 링커 후보 경로다(usr-merge 배포판 우선). 심볼릭 링크가 아닌
// 실체가 있는 경로를 먼저 둔다 — host 의 /usr/lib64/ld-linux-x86-64.so.2 는 /lib/... 를 가리키는
// 절대 심볼릭 링크라 컨테이너 안에서는 끊긴 링크가 된다.
var hostLoaderCandidates = []string{
	"/usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
	"/usr/lib64/ld-linux-x86-64.so.2",
	"/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
	"/lib64/ld-linux-x86-64.so.2",
}

// hostLibraryPath 는 host 바이너리가 필요로 하는 공유 라이브러리 경로다(libnvidia-ml 포함).
const hostLibraryPath = "/host/usr/lib/x86_64-linux-gnu:/host/usr/lib64:/host/usr/lib"

// hostCommand 는 host 바이너리 실행 커맨드를 만든다. distroless 이미지에는 동적 링커가 없어
// host 바이너리를 그대로 exec 하면 "fork/exec …: no such file or directory" 가 난다 — 파일이
// 없다는 뜻이 아니라 ELF 인터프리터를 못 찾았다는 뜻이라, nvidia-smi 가 host 에 있어도 MIG
// 관측이 통째로 실패한다. host 의 링커에 라이브러리 경로를 주어 대신 실행하고, 링커를 못
// 찾으면 기존처럼 직접 실행한다.
func hostCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	for _, ld := range hostLoaderCandidates {
		if !hostExists(ld) {
			continue
		}
		full := append([]string{"--library-path", hostLibraryPath, H(bin)}, args...)
		return exec.CommandContext(ctx, H(ld), full...)
	}
	return exec.CommandContext(ctx, H(bin), args...)
}

// runNvidiaSmi 는 host nvidia-smi 를 지정 인자로 실행하고 결합 출력을 반환한다.
// migLgip() 과 동일한 host-바이너리+타임아웃 패턴(exec.CommandContext(ctx, H(nvidiaSmiHostBin), ...))을
// 공유 헬퍼로 추출한 것 — per-PCI 호출(observeMig)이 여러 인자 조합으로 재사용한다.
func runNvidiaSmi(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := hostCommand(ctx, nvidiaSmiHostBin, args...).CombinedOutput()
	return string(out), err
}

// MigObservation 은 단일 GPU(PCI)의 MIG 관측 결과다(fail-closed).
type MigObservation struct {
	ModeCurrent, ModePending, Geometry, LgipOutput, Err string
}

// normalizeMode 는 nvidia-smi mig.mode.current/pending CSV 필드값을 정규화한다.
// 인식 못하는 값은 전부 "Unknown"(fail-closed) — 조용히 "Disabled" 로 오판하지 않는다.
func normalizeMode(s string) string {
	switch strings.TrimSpace(s) {
	case "Enabled":
		return "Enabled"
	case "Disabled":
		return "Disabled"
	case "N/A", "[N/A]":
		return "NA"
	default:
		return "Unknown"
	}
}

// migProfileLineRe 는 `mig -lgi`/`-lgip` 출력의 프로파일 행에서 "1g.6gb" 형식 토큰을 추출한다.
// "MIG 1g.6gb+me" 같은 +me 변형도 매칭(접미사는 무시).
var migProfileLineRe = regexp.MustCompile(`MIG\s+(\d+g\.\d+gb)`)

// parseMigObservation 은 fail-closed 로 관측을 판정한다: mode csv 파싱 실패, current
// Unknown, pending Unknown, 또는 Enabled 인데 -lgi 를 파싱 못하면 전부 Unknown+Err 를 반환하고
// 절대 조용히 "disabled" 로 보고하지 않는다.
func parseMigObservation(modeCsv, lgi, lgip string) MigObservation {
	f := strings.Split(strings.TrimSpace(modeCsv), ",")
	if len(f) != 2 || strings.TrimSpace(f[0]) == "" {
		return MigObservation{ModeCurrent: "Unknown", ModePending: "Unknown", Err: "unparseable mode csv"}
	}
	cur, pend := normalizeMode(f[0]), normalizeMode(f[1])
	if cur == "Unknown" {
		return MigObservation{ModeCurrent: "Unknown", ModePending: pend, LgipOutput: lgip, Err: "unrecognized mig.mode.current"}
	}
	if pend == "Unknown" {
		return MigObservation{ModeCurrent: cur, ModePending: "Unknown", LgipOutput: lgip, Err: "unrecognized mig.mode.pending"}
	}
	obs := MigObservation{ModeCurrent: cur, ModePending: pend, LgipOutput: lgip}
	switch cur {
	case "Disabled":
		obs.Geometry = "disabled"
	case "NA":
		// MIG 미지원 GPU(예: A2) — 분할될 여지가 없으므로 trivially "disabled".
		obs.Geometry = "disabled"
	case "Enabled":
		g, err := summarizeMigGeometryStrict(lgi)
		if err != nil {
			return MigObservation{ModeCurrent: "Unknown", ModePending: pend, LgipOutput: lgip, Err: "enabled but lgi unparseable: " + err.Error()}
		}
		obs.Geometry = g
	}
	return obs
}

// summarizeMigGeometryStrict 는 `mig -lgi` 원문을 요약한다. current=Enabled 인데 파싱 불가면
// error 를 반환한다(fail-closed) — Enabled 상태에서 조용히 "disabled" 로 보고하지 않는다.
func summarizeMigGeometryStrict(lgi string) (string, error) {
	if strings.Contains(lgi, "No MIG-enabled devices") {
		return "disabled", nil
	}
	if strings.TrimSpace(lgi) == "" {
		return "", fmt.Errorf("empty lgi")
	}
	counts := map[string]int{}
	for _, line := range strings.Split(lgi, "\n") {
		if m := migProfileLineRe.FindStringSubmatch(line); m != nil {
			counts[m[1]]++
		}
	}
	if len(counts) == 0 {
		return "", fmt.Errorf("no parseable GI in non-empty lgi")
	}
	parts := make([]string, 0, len(counts))
	for p, n := range counts {
		parts = append(parts, fmt.Sprintf("%s x%d", p, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", "), nil
}

// observeMig 는 지정 PCI 주소의 GPU 하나에 대해 mode/-lgi/-lgip 를 각각 조회하고
// fail-closed 로 판정한다. nvidia-smi 부재 또는 pci 공란이면
// 즉시 Unknown+Err(조회 자체를 시도하지 않음 — 안전하게 타겟할 수 없는 GPU).
func observeMig(pci string) MigObservation {
	if !nvidiaSmiPresent() || pci == "" {
		return MigObservation{ModeCurrent: "Unknown", ModePending: "Unknown", Err: "nvidia-smi absent or empty pci"}
	}
	mode, err := runNvidiaSmi(3*time.Second, "-i", pci, "--query-gpu=mig.mode.current,mig.mode.pending", "--format=csv,noheader")
	if err != nil {
		return MigObservation{ModeCurrent: "Unknown", ModePending: "Unknown", Err: "mode query failed: " + err.Error()}
	}
	lgi, _ := runNvidiaSmi(3*time.Second, "mig", "-i", pci, "-lgi")
	lgip, lgipErr := runNvidiaSmi(3*time.Second, "mig", "-i", pci, "-lgip")
	obs := parseMigObservation(mode, lgi, lgip)
	if lgipErr != nil {
		// lgip exec 실패를 조용히 삼키지 않는다 — Discover profile parsing 이 garbage/빈 출력을
		// 깨끗한 관측으로 오인하지 않도록 Err 에 신호를 남긴다(mode/geometry 는 그대로 유지).
		msg := "lgip query failed: " + lgipErr.Error()
		if obs.Err != "" {
			obs.Err = obs.Err + "; " + msg
		} else {
			obs.Err = msg
		}
	}
	return obs
}

// pciAddrsMatching 은 match 에 해당하는 PCI 장치의 주소 목록을 반환한다(bindingSummary 의
// 순회를 개수 대신 주소로 수집하는 판). 매칭 규칙을 새로 쓰지 않고 벤더 match 함수를 그대로
// 받는 이유는, 주소 목록과 *PciCount 집계가 다른 규칙으로 갈리면 fan-out 한 entry 수와
// count 가 어긋나기 때문이다. 정렬은 결정적 출력을 위함.
func pciAddrsMatching(match func(addr, v, d, cls string) bool) []string {
	entries, _ := os.ReadDir(H(hostSysPciDevices))
	var addrs []string
	for _, e := range entries {
		addr := e.Name() // 0000:BB:DD.F
		base := filepath.Join(H(hostSysPciDevices), addr)
		v := readFileTrim(base + "/vendor")
		d := readFileTrim(base + "/device")
		cls := strings.ToLower(readFileTrim(base + "/class"))
		if v == "" || !match(addr, v, d, cls) {
			continue
		}
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	return addrs
}

// vendorPciMatchers 는 vendorKey(types.go) → 그 벤더의 PCI 매칭 함수다. warboy 와 rngd 는
// 같은 벤더 ID(0x1ed2)를 device ID 로만 가르므로 키가 갈라져 있어야 한다.
var vendorPciMatchers = map[string]func(addr, v, d, cls string) bool{
	"nvidia":      matchNvidia,
	"furiosa":     matchWarboy, // vendorKey("furiosa","warboy")
	"rngd":        matchRngd,
	"rebellions":  matchRebellions,
	"tenstorrent": matchTenstorrent,
}

// vendorPciAddrs 는 벤더별 PCI 주소 목록을 1회 스캔으로 모은다(Snapshot 단일 소스).
//
// NVIDIA 만 주소를 남기면 나머지 벤더의 NDR entry 는 pcieAddress 가 비어, rngd backend 의
// deviceID() 가 model 폴백으로 카드를 뭉개고 intent.ApplyHealth 가 장치별 제외를 못 한다
// (report.go buildDevices 와 같은 문제). 열거는 드라이버 바인딩과 무관하다 — vendor/device/class
// 는 PCI 코어가 채우므로 드라이버가 안 붙어도 존재한다.
func vendorPciAddrs() map[string][]string {
	out := make(map[string][]string, len(vendorPciMatchers))
	for key, match := range vendorPciMatchers {
		if addrs := pciAddrsMatching(match); len(addrs) > 0 {
			out[key] = addrs
		}
	}
	return out
}

type PciIDs struct {
	Furiosa []PciID `json:"furiosa"`
}

var (
	cachedPciIDs      *PciIDs
	builtinFuriosaIDs = []PciID{
		{Vendor: "0x1ed2", Device: "0x0000"}, // Warboy
		{Vendor: "0x1ed2", Device: "0x0001"}, // RNGD
	}
	// Tenstorrent Blackhole PCI IDs
	// 0xb140 은 <tenstorrent-node> sysfs 실측값(/sys/bus/pci/devices/0000:af:00.0).
	// 0xb150 은 확인 못 한 placeholder 로 남긴다 — matchTenstorrent 가 벤더 ID 만으로
	// 매칭하므로 목록 누락이 감지 실패로 이어지지는 않는다.
	builtinTenstorrentIDs = []PciID{
		{Vendor: "0x1e52", Device: "0xb140"}, // Blackhole P150 (<tenstorrent-node> 실측)
		{Vendor: "0x1e52", Device: "0xb150"}, // (미확인)
	}
	// Rebellions Atom+ PCI IDs (공식 ConfigMap selectors 기준)
	builtinRebellionsIDs = []PciID{
		{Vendor: "0x1eff", Device: "0x0010"},
		{Vendor: "0x1eff", Device: "0x0011"},
		{Vendor: "0x1eff", Device: "0x1020"},
		{Vendor: "0x1eff", Device: "0x1021"},
		{Vendor: "0x1eff", Device: "0x1120"},
		{Vendor: "0x1eff", Device: "0x1121"},
		{Vendor: "0x1eff", Device: "0x1150"},
		{Vendor: "0x1eff", Device: "0x1151"},
		{Vendor: "0x1eff", Device: "0x1220"},
		{Vendor: "0x1eff", Device: "0x1221"},
		{Vendor: "0x1eff", Device: "0x1250"},
		{Vendor: "0x1eff", Device: "0x1251"},
	}
	pciIDsConfigPath    = "/etc/npu-detector/pci-ids.json" // 컨테이너 내부
	hostSysPciDevices   = "/sys/bus/pci/devices"
	hostSysModule       = "/sys/module"
	hostVarDpkgStatus   = "/var/lib/dpkg/status"
	markerDir           = "/var/lib/kcloud-operator"
	legacyMarkerDir     = "/var/lib/npu-operator"
	hostProcNvrmVers    = "/proc/driver/nvidia/version"
	furiosaVendorID     = "0x1ed2"
	rebellionsVendorID  = "0x1eff"
	tenstorrentVendorID = "0x1e52"
	reSemverish         = regexp.MustCompile(`^[0-9]+(\.[0-9A-Za-z\-]+)+$`)
	// NVRM 라인의 "Kernel Module" 다음 버전을 매칭한다(2-component 580.142 / 3-component 525.x.y
	// 모두 허용, 아키텍처 토큰(x86_64 등)은 건너뛴다 — open kernel module 은 "...for x86_64
	// 595.84" 처럼 토큰이 낀다. 못 맞으면 fallback reAnyVer 가 같은 줄의 GCC 컴파일러 버전을
	// 잘못 집어, 그 값이 보고되면 업그레이드 상태기계가 cordon 하고 없는 이미지 태그를
	// 받으려다 고착된다.
	reNvrmVer = regexp.MustCompile(
		`NVRM version:.*?Kernel Module\s+(?:[A-Za-z][\w\-]*\s+)*([0-9]+(?:\.[0-9]+)+)`)
	// 2-component 이상 허용 — /sys/module/nvidia/version 은 "595.84" 처럼 2-component 로만
	// 적히므로 3-component 강제 패턴으로는 영영 안 잡힌다.
	reAnyVer           = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)+)`)
	reNvidiaDriverPkg  = regexp.MustCompile(`^nvidia-driver-[0-9]+(-server)?(-open)?$`)
	furiosaDriverNames = []string{"npu-mgmt", "npu_mgmt", "npu-pdma", "npu_pdma"}
	furiosaModuleNames = []string{"npu_mgmt", "npu_pdma"}
)

func init() {
	if v := os.Getenv("DETECTOR_HOST_PREFIX"); v != "" {
		hostPrefix = v
	}
}

func loadPciIDs() *PciIDs {
	if cachedPciIDs != nil {
		return cachedPciIDs
	}
	ids := &PciIDs{}
	if b, err := os.ReadFile(pciIDsConfigPath); err == nil {
		_ = json.Unmarshal(b, ids)
	}
	// 기본 내장 ID 병행(비어있을 때 안전망)
	if len(ids.Furiosa) == 0 {
		ids.Furiosa = append(ids.Furiosa, builtinFuriosaIDs...)
	}
	cachedPciIDs = ids
	return cachedPciIDs
}

func readFirstLine(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

/* ---------- Furiosa 감지 ---------- */

func furiosaPciCount() int {
	ids := loadPciIDs() // 있으면 정밀 매칭 우선
	entries, _ := os.ReadDir(H(hostSysPciDevices))
	count := 0
	for _, e := range entries {
		// sysfs 항목은 symlink가 많음. IsDir() 검사 금지!
		base := filepath.Join(H(hostSysPciDevices), e.Name())

		v := readFileTrim(base + "/vendor")                   // ex) 0x1ed2
		d := readFileTrim(base + "/device")                   // ex) 0x0000
		cls := strings.ToLower(readFileTrim(base + "/class")) // ex) 0x120000

		if v == "" {
			continue
		}

		// (1) ConfigMap에 명시된 디바이스 ID 매칭
		matched := false
		for _, want := range ids.Furiosa {
			if strings.EqualFold(v, want.Vendor) && strings.EqualFold(d, want.Device) {
				matched = true
				break
			}
		}
		if matched {
			count++
			continue
		}

		// (2) 벤더ID만으로도 카운트(+ class 0x12xx면 가산, class 읽기 실패시에도 허용)
		if strings.EqualFold(v, furiosaVendorID) && (cls == "" || strings.HasPrefix(cls, "0x12")) {
			count++
		}
	}
	return count
}

func furiosaCount() int {
	// /sys/bus/pci/drivers/<driver>/*:*  우선
	globs := []string{}
	for _, dn := range furiosaDriverNames {
		globs = append(globs, H("/sys/bus/pci/drivers/"+dn+"/*:*"))
	}
	if c := countGlobMany(globs...); c > 0 {
		return c
	}
	// /sys/module/<module>/drivers/pci:<driver>/*:* 보조
	globs = globs[:0]
	for _, mn := range furiosaModuleNames {
		for _, dn := range furiosaDriverNames {
			globs = append(globs, H("/sys/module/"+mn+"/drivers/pci:"+dn+"/*:*"))
		}
	}
	if c := countGlobMany(globs...); c > 0 {
		return c
	}
	return 0
}

func furiosaDriverLoaded() bool {
	// "로드"는 모듈/바인딩으로만 판단.
	for _, mn := range furiosaModuleNames {
		if fileExists(H("/sys/module/" + mn)) {
			return true
		}
	}
	if furiosaCount() > 0 { // 드라이버 바인딩
		return true
	}
	return false
}

// dpkg 상태 파일에서 Furiosa 관련 "설치됨" 패키지 버전만 파싱
func furiosaPkgSummaryFromStatus() (driverVer string, detail string) {
	data, err := os.ReadFile(H(hostVarDpkgStatus))
	if err != nil {
		return "", ""
	}
	want := map[string]string{
		"furiosa-driver-warboy": "",
		"furiosa-libhal-warboy": "",
		"furiosa-libnux":        "",
		"furiosa-toolkit":       "",
		"furiosa-compiler":      "",
	}
	lines := strings.Split(string(data), "\n")
	var pkg, ver, status string
	flush := func() {
		if pkg != "" && ver != "" && strings.Contains(status, "ok installed") {
			if _, ok := want[pkg]; ok {
				want[pkg] = ver
			}
		}
		pkg, ver, status = "", "", ""
	}
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "Package: "):
			flush()
			pkg = strings.TrimSpace(strings.TrimPrefix(ln, "Package: "))
		case strings.HasPrefix(ln, "Version: "):
			ver = strings.TrimSpace(strings.TrimPrefix(ln, "Version: "))
		case strings.HasPrefix(ln, "Status: "):
			status = strings.TrimSpace(strings.TrimPrefix(ln, "Status: "))
		case ln == "":
			flush()
		}
	}
	flush()

	driverVer = want["furiosa-driver-warboy"]

	var parts []string
	labels := []string{"driver", "libhal", "libnux", "toolkit", "compiler"}
	keys := []string{"furiosa-driver-warboy", "furiosa-libhal-warboy", "furiosa-libnux", "furiosa-toolkit", "furiosa-compiler"}
	for i, k := range keys {
		if want[k] != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", labels[i], want[k]))
		}
	}
	detail = strings.Join(parts, "; ")
	return
}

// nvidiaOnDiskVersion 는 설치된(디스크) nvidia 드라이버 패키지 버전을 dpkg status 에서 뽑는다.
// 디스크 버전 = 재부팅 후 로드될 버전이므로 로드된 모듈 버전(/proc)과 다르면 대역외 드리프트다.
// 반환은 숫자형(예 "580.178.04"). 파싱 불가·미설치·.run 설치면 "".
func nvidiaOnDiskVersion() string {
	data, err := os.ReadFile(H(hostVarDpkgStatus))
	if err != nil {
		return ""
	}
	var pkg, ver, status string
	var found string
	flush := func() {
		if found == "" && reNvidiaDriverPkg.MatchString(pkg) && strings.Contains(status, "ok installed") {
			if m := reAnyVer.FindStringSubmatch(ver); len(m) == 2 {
				found = m[1]
			}
		}
		pkg, ver, status = "", "", ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(ln, "Package: "):
			flush()
			pkg = strings.TrimSpace(strings.TrimPrefix(ln, "Package: "))
		case strings.HasPrefix(ln, "Version: "):
			ver = strings.TrimSpace(strings.TrimPrefix(ln, "Version: "))
		case strings.HasPrefix(ln, "Status: "):
			status = strings.TrimSpace(strings.TrimPrefix(ln, "Status: "))
		case ln == "":
			flush()
		}
	}
	flush()
	return found
}

func furiosaShortVersion() (short, detail string) {
	// 1) 마커 우선(내용은 detail로만 사용)
	if m := readFileTrim(markerPath("furiosa.dpkg")); m != "" {
		detail = strings.Join(strings.Fields(m), " ")
		if dv, det := furiosaPkgSummaryFromStatus(); dv != "" {
			if detail == "" && det != "" {
				detail = det
			}
			if reSemverish.MatchString(dv) {
				return dv, detail
			}
		}
		for _, tok := range strings.Split(m, "\n") {
			if strings.Contains(tok, "furiosa-driver-warboy") {
				fs := strings.Fields(tok)
				if len(fs) >= 2 && reSemverish.MatchString(fs[len(fs)-1]) {
					return fs[len(fs)-1], detail
				}
			}
		}
		return "", detail
	}
	// 2) 모듈 버전 + dpkg 보강
	for _, mn := range furiosaModuleNames {
		if v := readFileTrim(H("/sys/module/" + mn + "/version")); v != "" {
			if reSemverish.MatchString(v) {
				short = v
			}
			if dv, det := furiosaPkgSummaryFromStatus(); det != "" {
				if short == "" && reSemverish.MatchString(dv) {
					short = dv
				}
				if detail == "" {
					detail = det
				}
			}
			return short, detail
		}
	}
	// 3) dpkg status
	if dv, det := furiosaPkgSummaryFromStatus(); dv != "" || det != "" {
		if reSemverish.MatchString(dv) {
			short = dv
		}
		detail = det
		return short, detail
	}
	return "", ""
}

/* ---------- RNGD 감지 ---------- */

func rngdPciCount() int {
	entries, _ := os.ReadDir(H(hostSysPciDevices))
	count := 0
	for _, e := range entries {
		base := filepath.Join(H(hostSysPciDevices), e.Name())
		v := readFileTrim(base + "/vendor")
		d := readFileTrim(base + "/device")
		if v == "" {
			continue
		}
		if strings.EqualFold(v, "0x1ed2") && strings.EqualFold(d, "0x0001") {
			count++
		}
	}
	return count
}

func rngdDriverLoaded() bool {
	return fileExists(H("/sys/module/furiosa_rngd"))
}

func rngdPkgSummaryFromStatus() (driverVer string, detail string) {
	data, err := os.ReadFile(H(hostVarDpkgStatus))
	if err != nil {
		return "", ""
	}
	want := map[string]string{
		"furiosa-driver-rngd": "",
	}
	lines := strings.Split(string(data), "\n")
	var pkg, ver, status string
	flush := func() {
		if pkg != "" && ver != "" && strings.Contains(status, "ok installed") {
			if _, ok := want[pkg]; ok {
				want[pkg] = ver
			}
		}
		pkg, ver, status = "", "", ""
	}
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "Package: "):
			flush()
			pkg = strings.TrimSpace(strings.TrimPrefix(ln, "Package: "))
		case strings.HasPrefix(ln, "Version: "):
			ver = strings.TrimSpace(strings.TrimPrefix(ln, "Version: "))
		case strings.HasPrefix(ln, "Status: "):
			status = strings.TrimSpace(strings.TrimPrefix(ln, "Status: "))
		case ln == "":
			flush()
		}
	}
	flush()

	driverVer = want["furiosa-driver-rngd"]
	if driverVer != "" {
		detail = fmt.Sprintf("driver=%s", driverVer)
	}
	return
}

func rngdShortVersion() (short, detail string) {
	// 1) 모듈 버전 우선
	if v := readFileTrim(H("/sys/module/furiosa_rngd/version")); v != "" {
		if reSemverish.MatchString(v) {
			short = v
		}
		if dv, det := rngdPkgSummaryFromStatus(); det != "" {
			if short == "" && reSemverish.MatchString(dv) {
				short = dv
			}
			if detail == "" {
				detail = det
			}
		}
		return short, detail
	}
	// 2) dpkg status
	if dv, det := rngdPkgSummaryFromStatus(); dv != "" || det != "" {
		if reSemverish.MatchString(dv) {
			short = dv
		}
		detail = det
		return short, detail
	}
	return "", ""
}

/* ---------- Rebellions Atom+ 감지 ---------- */

func rblnPciCount() int {
	entries, _ := os.ReadDir(H(hostSysPciDevices))
	count := 0
	for _, e := range entries {
		base := filepath.Join(H(hostSysPciDevices), e.Name())
		v := readFileTrim(base + "/vendor")
		d := readFileTrim(base + "/device")
		if v == "" {
			continue
		}
		if !strings.EqualFold(v, rebellionsVendorID) {
			continue
		}
		for _, want := range builtinRebellionsIDs {
			if strings.EqualFold(d, want.Device) {
				count++
				break
			}
		}
	}
	return count
}

func rblnDriverLoaded() bool {
	return fileExists(H(filepath.Join(hostSysModule, "rebellions")))
}

func rblnPkgSummaryFromStatus() (driverVer string, detail string) {
	data, err := os.ReadFile(H(hostVarDpkgStatus))
	if err != nil {
		return "", ""
	}
	// 커널 드라이버 패키지만 본다. rebellions-fw·rebellions-tool·rbln-sdk 는 펌웨어·도구 버전이라
	// 드라이버 버전으로 보고하면 틀린다. <rebellions-node>(ATOM+) 의 실제 패키지 이름은 rebellions-dkms 다.
	want := map[string]string{
		"rebellions-dkms": "",
		"rebellions":      "",
	}
	lines := strings.Split(string(data), "\n")
	var pkg, ver, status string
	flush := func() {
		if pkg != "" && ver != "" && strings.Contains(status, "ok installed") {
			if _, ok := want[pkg]; ok {
				want[pkg] = ver
			}
		}
		pkg, ver, status = "", "", ""
	}
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "Package: "):
			flush()
			pkg = strings.TrimSpace(strings.TrimPrefix(ln, "Package: "))
		case strings.HasPrefix(ln, "Version: "):
			ver = strings.TrimSpace(strings.TrimPrefix(ln, "Version: "))
		case strings.HasPrefix(ln, "Status: "):
			status = strings.TrimSpace(strings.TrimPrefix(ln, "Status: "))
		case ln == "":
			flush()
		}
	}
	flush()

	driverVer = want["rebellions-dkms"]
	if driverVer == "" {
		driverVer = want["rebellions"]
	}
	if driverVer != "" {
		detail = fmt.Sprintf("driver=%s", driverVer)
	}
	return
}

func rblnShortVersion() (short, detail string) {
	// 1) 커널 모듈 버전 우선 — 모듈이 올라와 있으면 패키지 관리자와 무관하게 값이 있다.
	if v := readFileTrim(H(filepath.Join(hostSysModule, "rebellions", "version"))); v != "" {
		if reSemverish.MatchString(v) {
			short = v
		}
		return short, fmt.Sprintf("driver=%s", v)
	}
	// 2) 모듈이 없을 때(미적재·재부팅 대기)는 설치된 드라이버 패키지에서 읽는다.
	if dv, det := rblnPkgSummaryFromStatus(); dv != "" || det != "" {
		if reSemverish.MatchString(dv) {
			short = dv
		}
		detail = det
		return short, detail
	}
	return "", ""
}

/* ---------- Tenstorrent Blackhole 감지 ---------- */

func ttPciCount() int {
	entries, _ := os.ReadDir(H(hostSysPciDevices))
	count := 0
	for _, e := range entries {
		base := filepath.Join(H(hostSysPciDevices), e.Name())
		v := readFileTrim(base + "/vendor")
		d := readFileTrim(base + "/device")
		if v == "" {
			continue
		}
		if !strings.EqualFold(v, tenstorrentVendorID) {
			continue
		}
		// builtinTenstorrentIDs 에 등록된 device ID 우선 매칭;
		// 미등록 device ID 는 벤더 ID(0x1e52)만으로도 집계(신규 SKU 대응).
		// TODO: <tenstorrent-node> lspci 결과로 0xb150 확인 후 정밀 매칭만 남길 것.
		matched := false
		for _, want := range builtinTenstorrentIDs {
			if strings.EqualFold(d, want.Device) {
				matched = true
				break
			}
		}
		if matched || strings.EqualFold(v, tenstorrentVendorID) {
			count++
		}
	}
	return count
}

func ttDriverLoaded() bool {
	// Tenstorrent 드라이버 모듈명(tenstorrent 또는 tt_kmd)
	return fileExists(H("/sys/module/tenstorrent")) ||
		fileExists(H("/sys/module/tt_kmd"))
}

func ttShortVersion() (short, detail string) {
	// 1) 커널 모듈 버전 우선
	for _, mn := range []string{"tenstorrent", "tt_kmd"} {
		if v := readFileTrim(H("/sys/module/" + mn + "/version")); v != "" {
			if reSemverish.MatchString(v) {
				short = v
			}
			detail = v
			return short, detail
		}
	}
	// 2) dpkg 에서 tenstorrent 관련 패키지 조회
	data, err := os.ReadFile(H(hostVarDpkgStatus))
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(data), "\n")
	var pkg, ver, status string
	flush := func() {
		if pkg != "" && ver != "" && strings.Contains(status, "ok installed") &&
			strings.Contains(pkg, "tenstorrent") {
			if short == "" && reSemverish.MatchString(ver) {
				short = ver
			}
			if detail == "" {
				detail = fmt.Sprintf("driver=%s", ver)
			}
		}
		pkg, ver, status = "", "", ""
	}
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "Package: "):
			flush()
			pkg = strings.TrimSpace(strings.TrimPrefix(ln, "Package: "))
		case strings.HasPrefix(ln, "Version: "):
			ver = strings.TrimSpace(strings.TrimPrefix(ln, "Version: "))
		case strings.HasPrefix(ln, "Status: "):
			status = strings.TrimSpace(strings.TrimPrefix(ln, "Status: "))
		case ln == "":
			flush()
		}
	}
	flush()
	return short, detail
}

/* ---------- NVIDIA 감지 ---------- */

// 바인딩된 GPU 개수(드라이버가 실제로 붙은 장치 수)
func nvidiaGpuCountBound() int {
	entries, err := os.ReadDir(H("/proc/driver/nvidia/gpus"))
	if err != nil {
		return 0
	}
	cnt := 0
	for _, e := range entries {
		if e.IsDir() {
			cnt++
		}
	}
	return cnt
}

// PCI 상에서 보이는 NVIDIA 물리 함수(.0) 개수(드라이버 無여도 잡힘)
func nvidiaPciCount() int {
	entries, _ := os.ReadDir(H("/sys/bus/pci/devices"))
	cnt := 0
	for _, e := range entries {
		addr := e.Name() // 0000:BB:DD.F
		if !strings.HasSuffix(addr, ".0") {
			continue // 물리 함수만 집계
		}
		base := filepath.Join(H("/sys/bus/pci/devices"), addr)
		v := readFileTrim(base + "/vendor")                   // 0x10de
		cls := strings.ToLower(readFileTrim(base + "/class")) // 0x03xxxx (VGA/3D/Display)
		if strings.EqualFold(v, "0x10de") && strings.HasPrefix(cls, "0x03") {
			cnt++
		}
	}
	return cnt
}

// NVIDIA GPU 물리 함수(.0, vendor 0x10de & class 0x03)를 순회하며 드라이버 바인딩 현황을
// 집계한다. total=감지된 물리 함수 수(nvidiaPciCount 와 동일 기준), vfio=vfio-pci 바인딩 수
// (패스스루 예약), nvidiaBound=nvidia 드라이버 바인딩 수, free=그 외(미바인딩/nouveau 등).
func nvidiaBindingSummary() (total, vfio, nvidiaBound, free int) {
	return bindingSummary(matchNvidia, []string{"nvidia"})
}

// 대표 바인딩 문자열: 전량 vfio → "vfio-pci", nvidia 바인딩 존재 → "nvidia",
// 혼재 → "mixed", GPU 없음/미바인딩 → "none".
// loaded=false 로 driverBinding 을 호출해 기존 동작을 그대로 유지한다.
func nvidiaDriverBinding(total, vfio, nvidiaBound int) string {
	return driverBinding("nvidia", total, vfio, nvidiaBound, false)
}

// 표시용 총 개수: 바인딩된 개수와 PCI 개수 중 큰 값
func nvidiaCount() int {
	b := nvidiaGpuCountBound()
	p := nvidiaPciCount()
	if p > b {
		return p
	}
	return b
}

func nvidiaModulePresent() bool { return fileExists(H("/sys/module/nvidia")) }
func nvidiaCtlPresent() bool    { return fileExists(H("/dev/nvidiactl")) }

// 커널 레벨 드라이버 존재 신호(여러 경로 중 하나만 있어도 true)
func nvidiaKernelPresent() bool {
	if fileExists(H("/proc/driver/nvidia/version")) {
		return true
	}
	if fileExists(H("/sys/module/nvidia")) {
		return true
	}
	if fileExists(H("/dev/nvidiactl")) {
		return true
	}
	return false
}

// 호스트에 NVIDIA 유저랜드(실사용 도구) 존재 여부
func nvidiaUserlandPresent() bool {
	// 최신 GPU Operator 환경까지 고려해서, nvidia-smi / nvidia-container-cli / nvidia-ctk 중
	// 하나라도 있으면 "툴킷이 있다"고 본다.
	if fileExists(H("/usr/bin/nvidia-smi")) ||
		fileExists(H("/usr/bin/nvidia-container-cli")) ||
		fileExists(H("/usr/bin/nvidia-ctk")) {
		return true
	}
	return false
}

// optional: dpkg 상태에서 nvidia-driver-* 설치 여부 확인(참고용)
func nvidiaPkgInstalled() bool {
	data, err := os.ReadFile(H(hostVarDpkgStatus))
	if err != nil {
		return false
	}
	blocks := strings.Split(string(data), "\n\n")
	for _, bl := range blocks {
		if strings.Contains(bl, "Package: nvidia-driver-") &&
			strings.Contains(bl, "Status: install ok installed") {
			return true
		}
	}
	return false
}

// dpkg 상태에서 nvidia-container-toolkit 설치 여부 확인
func nvidiaToolkitPkgInstalled() bool {
	data, err := os.ReadFile(H(hostVarDpkgStatus))
	if err != nil {
		return false
	}
	blocks := strings.Split(string(data), "\n\n")
	for _, bl := range blocks {
		if strings.Contains(bl, "Package: nvidia-container-toolkit") &&
			strings.Contains(bl, "Status: install ok installed") {
			return true
		}
	}
	return false
}

// parseNvrmVersion 은 /proc/driver/nvidia/version 본문에서 드라이버 버전만 뽑는다.
// 못 뽑으면 빈 문자열 — 추측한 값을 돌려주지 않는다. 여기서 나온 값이 그대로 업그레이드
// 판정의 "현재 버전" 이 되므로, 틀린 값은 불필요한 노드 cordon 으로 이어진다.
func parseNvrmVersion(v string) string {
	if m := reNvrmVer.FindStringSubmatch(v); len(m) == 2 {
		return m[1]
	}
	// 느슨한 fallback 은 컴파일러 배너 앞까지만 본다. 그 줄에도 버전처럼 생긴 값이 있어
	// 컴파일러 버전이 드라이버 버전으로 둔갑하는 회귀가 두 차례(580.142 / 595.84) 난 적이 있다.
	head := v
	if i := strings.Index(head, "GCC version"); i >= 0 {
		head = head[:i]
	}
	if m := reAnyVer.FindStringSubmatch(head); len(m) == 2 {
		return m[1]
	}
	return ""
}

func nvidiaShortVersion() (short string, detail string) {
	// 유저랜드 없으면 버전은 공란으로(일관성)
	if !nvidiaUserlandPresent() && !nvidiaPkgInstalled() {
		return "", ""
	}
	// 커널이 있으면 /proc 우선, 없으면 /sys 보조
	if nvidiaKernelPresent() {
		if v := readFileTrim(H("/proc/driver/nvidia/version")); v != "" {
			detail = strings.Join(strings.Fields(v), " ")
			if s := parseNvrmVersion(v); s != "" {
				return s, detail
			}
		}
		if b := readFileTrim(H("/sys/module/nvidia/version")); b != "" {
			if m := reAnyVer.FindStringSubmatch(b); len(m) == 2 {
				return m[1], detail
			}
			if detail == "" {
				detail = b
			}
		}
	}
	return "", detail
}

// 최종 로드 판정: "장치 + 커널 드라이버 + 유저랜드/툴킷" 이 모두 있어야 true
func nvidiaDriverLoaded() bool {
	// 1) 물리 장치 존재
	hasDevice := nvidiaPciCount() > 0

	// 2) 커널 드라이버(모듈/캐릭터 디바이스/버전) 존재
	hasKernel := nvidiaKernelPresent()

	// 3) 유저랜드/툴킷 존재(바이너리 or dpkg 패키지)
	hasToolkit := nvidiaUserlandPresent() || nvidiaToolkitPkgInstalled() || nvidiaPkgInstalled()

	return hasDevice && hasKernel && hasToolkit
}

/* ---------- 공통 디텍트 ---------- */

type Detected struct {
	vendor    string
	model     string
	count     int
	loaded    bool
	ver       string // short
	detail    string // normalized single-line detail
	onDiskVer string // 설치된 nvidia 드라이버 패키지 버전(디스크 = 재부팅 후 로드될 버전). nvidia 만.
}

func detect() (out []Detected) {
	// RNGD PCI 개수를 먼저 계산하여 Warboy 집계에서 제외(이중 계산 방지)
	rngdCnt := rngdPciCount()

	// Furiosa Warboy: PCI 존재 + 드라이버 로드 여부 분리
	bound := furiosaCount()             // 드라이버 바인딩 개수(Warboy 전용 드라이버만 집계)
	pcic := furiosaPciCount() - rngdCnt // Warboy PCI 개수(RNGD 제외)
	fcnt := bound
	if pcic > fcnt {
		fcnt = pcic
	}
	fLoaded := furiosaDriverLoaded() // 모듈/바인딩만으로 판단

	// 하드웨어(PCI) 존재하는 경우에만 보고한다. 이전 실패한 설치에서 커널 모듈만 남아있는
	// 노드(예: NVIDIA-only 노드에 npu_mgmt 모듈이 leftover)가 Furiosa 로 오탐되어
	// 드라이버 Installer Job 이 잘못된 노드로 스케줄되는 문제를 방지한다.
	if fcnt > 0 {
		short, det := furiosaShortVersion() // 드라이버 없으면 "", ""
		out = append(out, Detected{
			vendor: "furiosa", model: "warboy",
			count: fcnt, loaded: fLoaded,
			ver: short, detail: det,
		})
	}

	// Furiosa RNGD: device=0x0001 전용 감지
	if rngdCnt > 0 {
		short, det := rngdShortVersion()
		out = append(out, Detected{
			vendor: "furiosa", model: "rngd",
			count: rngdCnt, loaded: rngdDriverLoaded(),
			ver: short, detail: det,
		})
	}

	// Rebellions Atom+: PCI 매칭 + 드라이버 로드 여부
	rblnCnt := rblnPciCount()
	if rblnCnt > 0 {
		short, det := rblnShortVersion()
		out = append(out, Detected{
			vendor: "rebellions", model: "atom",
			count: rblnCnt, loaded: rblnDriverLoaded(),
			ver: short, detail: det,
		})
	}

	// Tenstorrent Blackhole: PCI 벤더 0x1e52 매칭 + 드라이버 로드 여부
	ttCnt := ttPciCount()
	if ttCnt > 0 {
		short, det := ttShortVersion()
		out = append(out, Detected{
			vendor: "tenstorrent", model: "blackhole-p150",
			count: ttCnt, loaded: ttDriverLoaded(),
			ver: short, detail: det,
		})
	}

	// NVIDIA: 바인딩 개수와 PCI 개수 분리 후 표시용은 더 큰 값 사용
	nBound := nvidiaGpuCountBound() // 드라이버 바인딩된 개수
	nPci := nvidiaPciCount()        // PCI에서 보이는 개수
	ncnt := nBound
	if nPci > ncnt {
		ncnt = nPci
	}

	nLoaded := nvidiaDriverLoaded() // '로드됨'은 커널/드라이버 기준(모듈+버전+bound≥1)
	if ncnt > 0 || nLoaded {
		short, det := "", ""
		// 로드되지 않은 경우엔 버전 문자열을 빈 값으로 유지(오탐 방지)
		if nLoaded {
			short, det = nvidiaShortVersion()
		}
		out = append(out, Detected{
			vendor: "nvidia", model: "generic",
			count: ncnt, loaded: nLoaded,
			ver: short, detail: det,
			onDiskVer: nvidiaOnDiskVersion(),
		})
	}
	return
}

// deviceDriverBinding: Detected 항목(벤더/모델)에 맞는 대표 드라이버 바인딩을
// 계산한다. 각 벤더 PCI 필터 + 자기 드라이버명으로 bindingSummary 를 돌리고,
// driverLoaded(d.loaded)로 NPU 의 PCI 링크 부재를 보완한다.
func deviceDriverBinding(d Detected) string {
	switch {
	case d.vendor == "nvidia":
		total, vfio, own, _ := bindingSummary(matchNvidia, []string{"nvidia"})
		return driverBinding("nvidia", total, vfio, own, d.loaded)
	case d.vendor == "furiosa" && d.model == "rngd":
		total, vfio, own, _ := bindingSummary(matchRngd, []string{"furiosa_rngd", "furiosa-rngd"})
		return driverBinding("furiosa", total, vfio, own, d.loaded)
	case d.vendor == "furiosa": // warboy(기본)
		total, vfio, own, _ := bindingSummary(matchWarboy, furiosaDriverNames)
		return driverBinding("furiosa", total, vfio, own, d.loaded)
	case d.vendor == "rebellions":
		total, vfio, own, _ := bindingSummary(matchRebellions, []string{"rebellions"})
		return driverBinding("rebellions", total, vfio, own, d.loaded)
	case d.vendor == "tenstorrent":
		total, vfio, own, _ := bindingSummary(matchTenstorrent, []string{"tenstorrent", "tt_kmd", "tt-kmd"})
		return driverBinding("tenstorrent", total, vfio, own, d.loaded)
	}
	return "none"
}

// nodePassthroughReserved: 노드에 가속기 device 가 ≥1개 있고, 전 벤더를 통틀어
// 전량 vfio-pci 로 바인딩(=free/own 없음)된 경우 true. 멀티벤더 노드도 전 벤더
// 합산으로 판정한다(각 PCI 장치는 벤더 ID 로 배타 매칭되어 중복 집계 없음).
func nodePassthroughReserved() bool {
	total, vfio, _, _ := bindingSummary(isAccelerator, nil)
	return total > 0 && vfio == total
}

/* ---------- Scan 능력 (Snapshot 생성) ---------- */

// Scan 은 노드를 1회 스캔하여 Snapshot 을 만든다. 매 주기 이 결과 하나를
// report/validate/metrics/label 소비자가 공유한다(중복 스캔 없음).
// 기존 detect()/nodePassthroughReserved() 를 그대로 재사용하므로 감지 동작은 불변이다.
func Scan(node string) *Snapshot {
	return &Snapshot{
		Node:                node,
		Devices:             detect(),
		PassthroughReserved: nodePassthroughReserved(),
		MigLgip:             migLgip(),
		PciAddrs:            vendorPciAddrs(),
		Sensors:             collectSensors(),
		Occupancy:           collectOccupancy(),
		ScanTime:            nowFunc(),
	}
}
