// ============================================================
// pcidb_test.go: 표준 PCI ID 데이터베이스 파서·슬러그 단위 테스트
// 상세: 슬러그 규칙은 실측 DB 문자열로, 조회는 실제 pci.ids 발췌 픽스처로 고정한다. 벤더를
// 좁히지 않으면 엉뚱한 장치가 걸리는 것도 별도 케이스로 못박는다.
// 생성일: 2026-08-12 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugProduct(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"GA100GL [A30 PCIe]", "a30-pcie"},             // 실측: 10de:20b7
		{"GM204 [GeForce GTX 970]", "geforce-gtx-970"}, // 실측: 10de:13c2
		{"TU104GL [Tesla T4]", "tesla-t4"},             // 실측: 10de:1eb8
		{"GA100 [A100 PCIe 40GB]", "a100-pcie-40gb"},   // 실측: 10de:20f1
		{"GA107GL [A2 / A16]", "a2-a16"},               // 실측: 10de:25b6 — 한 ID 두 제품
		{"ZyWALL Turbo Card", "zywall-turbo-card"},     // 대괄호 없는 형태
	} {
		if got := slugProduct(tc.in); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.in, got, tc.want)
		}
	}
}

// pciFixture 는 실측 /usr/share/misc/pci.ids 발췌다(worker1·master·rngd-1,
// `grep -E "^\t(20b7|25b6|13c2|1eb8|20f1)  "`). 서브시스템 줄(탭 2개)과 다른 벤더 아래
// 같은 장치 번호(20b7)를 섞어 넣어 파서가 벤더 경계·탭 깊이를 지키는지 함께 본다.
const pciFixture = `10de  NVIDIA Corporation
	13c2  GM204 [GeForce GTX 970]
	1eb8  TU104GL [Tesla T4]
	20b7  GA100GL [A30 PCIe]
		10de 1450  A30 PCIe
	20f1  GA100 [A100 PCIe 40GB]
	25b6  GA107GL [A2 / A16]
1414  Microsoft Corporation
	20b7  Some Unrelated Device
`

func TestLookupPCIDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pci.ids")
	if err := os.WriteFile(path, []byte(pciFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ vendor, device, want string }{
		{"10de", "20b7", "a30-pcie"},
		{"10de", "25b6", "a2-a16"}, // DB 표기 그대로 — 실측 표가 이 값을 이기는 건 productFromPCI 몫(pciid_test.go)
		{"10de", "13c2", "geforce-gtx-970"},
		{"10de", "1eb8", "tesla-t4"},
		{"10de", "20f1", "a100-pcie-40gb"},
		{"1414", "20b7", "some-unrelated-device"}, // 벤더가 다르면 다른 제품 — 벤더로 좁혀야 하는 이유
		{"10de", "dead", ""},                      // 없는 장치
		{"dead", "20b7", ""},                      // 없는 벤더
		// 인자 대소문자 — 파일 줄만 접고 인자를 안 접으면 대문자 인자가 조용히 빈 문자열을
		// 받는다. 지금 호출자는 소문자로 넘기지만 두 번째 호출자가 생기면 걸린다.
		{"10DE", "20B7", "a30-pcie"},
		{"10De", "20b7", "a30-pcie"},
		{"10de", "25B6", "a2-a16"},
	} {
		if got := lookupPCIDB(path, tc.vendor, tc.device); got != tc.want {
			t.Errorf("lookupPCIDB(%s:%s) = %q, want %q", tc.vendor, tc.device, got, tc.want)
		}
	}
	if got := lookupPCIDB(filepath.Join(t.TempDir(), "missing.ids"), "10de", "20b7"); got != "" {
		t.Errorf("missing file: got %q, want \"\"", got)
	}
}
