// ============================================================
// pcidb.go: 표준 PCI ID 데이터베이스에서 장치 제품명을 찾는다
// 상세: pci.ids 는 벤더 줄(들여쓰기 0) 아래 장치 줄(탭 1개)이 온다. 벤더 없이 조회하면 같은
// 장치번호가 다른 벤더 제품과 겹칠 수 있어, 반드시 벤더·장치 쌍으로 찾는다.
// 생성일: 2026-08-12 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// pciDBPath 는 표준 PCI ID 데이터베이스 경로다. host-usr 마운트(renderDetectorDS,
// 기존에 nvidia-smi userland 감지용으로 이미 있다)가 host 의 /usr 전체를 /host/usr 로
// 읽기 전용 마운트하므로, H() 로 계산하면 새 마운트 없이 host 의 pci.ids 를 그대로 읽는다.
// 시험은 이 변수를 임시 파일로 덮어써 주입한다. 이 DB 는 판이 낡아 최신 모델이 없을 수 있다 —
// 우리 결함이 아니라 관리자가 pciutils 의 update-pciids 를 돌리면 코드 재배포 없이 해소된다.
var pciDBPath = H("/usr/share/misc/pci.ids")

var bracket = regexp.MustCompile(`\[([^\]]+)\]`)
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugProduct 는 DB 표기를 화면·판정에 쓸 짧은 이름으로 줄인다.
// 대괄호 안이 마케팅 제품명이고 앞은 칩 코드다(GA100GL [A30 PCIe]).
func slugProduct(s string) string {
	if m := bracket.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(s, "-")
}

// lookupPCIDB 는 vendor/device 를 4자리 16진수("0x" 없이)로 받는다.
// 파일 줄만 소문자로 접고 인자는 그대로 쓰면 대문자 인자가 조용히 빈 문자열을 받는다
// (lookupPCIDB(f, "10DE", "20b7") → ""). 양쪽을 같이 접어 계약을 대칭으로 둔다.
func lookupPCIDB(path, vendor, device string) string {
	vendor, device = strings.ToLower(vendor), strings.ToLower(device)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	inVendor := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case !strings.HasPrefix(line, "\t"): // 벤더 줄
			inVendor = strings.HasPrefix(strings.ToLower(line), vendor+"  ")
		case inVendor && !strings.HasPrefix(line, "\t\t"): // 장치 줄
			body := strings.TrimPrefix(line, "\t")
			if strings.HasPrefix(strings.ToLower(body), device+"  ") {
				return slugProduct(strings.TrimSpace(body[len(device):]))
			}
		}
	}
	return ""
}
