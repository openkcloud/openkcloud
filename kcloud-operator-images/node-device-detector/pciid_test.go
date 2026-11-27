// ============================================================
// pciid_test.go: PCI 장치 ID → 제품명 판정 단위 테스트
// 상세: sysfs 의 vendor/device 파일을 임시 트리로 주입해 표 조회와 미등록·경로부재·빈주소의
// 빈 문자열 폴백을 못박는다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// 픽스처 값은 k8s-worker1 sysfs 실측이다(nvidia-smi 제품명과 대조: 0000:18:00.0=0x10de:0x20b7
// A30, 0000:86:00.0=0x10de:0x25b6 A2). 0xdead 는 표에 없는 NVIDIA 장치를 흉내낸 것으로,
// 이 자리가 빈 문자열이어야 호출부가 기존 generic 폴백을 유지한다.
func TestProductFromPCI(t *testing.T) {
	root := t.TempDir()
	fakePciDev(t, root, "0000:18:00.0", "0x10de", "0x20b7", "0x030200")
	fakePciDev(t, root, "0000:86:00.0", "0x10de", "0x25b6", "0x030200")
	fakePciDev(t, root, "0000:99:00.0", "0x10de", "0xdead", "0x030200")

	for _, tc := range []struct {
		addr, want string
	}{
		{"0000:18:00.0", "a30"},
		{"0000:86:00.0", "a2"},
		{"0000:99:00.0", ""}, // 표에 없는 식별자 → 빈 값(호출부가 generic 폴백)
		{"0000:00:00.0", ""}, // sysfs 경로 부재
		{"", ""},             // 빈 주소
	} {
		if got := productFromPCI(root, tc.addr); got != tc.want {
			t.Errorf("productFromPCI(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// 빈 주소가 sysfs 루트 자체를 읽어 엉뚱한 값을 내지 않는지 못박는다. filepath.Join(root, "")
// 은 root 로 접히므로, 루트에 vendor/device 파일이 놓이면 빈 주소가 제품명을 답할 수 있다.
func TestProductFromPCI_EmptyAddrDoesNotReadRoot(t *testing.T) {
	root := t.TempDir()
	fakePciDev(t, root, ".", "0x10de", "0x20b7", "0x030200") // 루트에 직접 vendor/device
	if got := productFromPCI(root, ""); got != "" {
		t.Errorf("빈 주소가 sysfs 루트를 읽었다: got %q, want \"\"", got)
	}
	// 픽스처가 실제로 루트에 놓였는지 확인(전제 자체가 틀리면 위 단언은 항진이다).
	if v := readFileTrim(filepath.Join(root, "vendor")); v != "0x10de" {
		t.Fatalf("픽스처 전제 실패: root/vendor = %q", v)
	}
}

// TestProductFromPCI_TablePrecedesDB 는 계층 순서를 못박는다. 25b6 는 실측 표에
// "a2"(nvidia-smi 대조 확정), 표준 DB 에는 "A2 / A16"(a2-a16 슬러그)로 있다.
// 표가 이겨야 한다 — DB 값이 나오면 계층이 뒤집힌 것이다.
func TestProductFromPCI_TablePrecedesDB(t *testing.T) {
	sysfsRoot := t.TempDir()
	fakePciDev(t, sysfsRoot, "0000:86:00.0", "0x10de", "0x25b6", "0x030200")

	dbPath := filepath.Join(t.TempDir(), "pci.ids")
	if err := os.WriteFile(dbPath, []byte("10de  NVIDIA Corporation\n\t25b6  GA107GL [A2 / A16]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDB := pciDBPath
	pciDBPath = dbPath
	t.Cleanup(func() { pciDBPath = oldDB })

	if got := productFromPCI(sysfsRoot, "0000:86:00.0"); got != "a2" {
		t.Errorf("productFromPCI = %q, want %q (표가 DB 를 이겨야 한다)", got, "a2")
	}
}

// TestProductFromPCI_FallsBackToDB 는 실측 표에 없는 ID 가 표준 DB 로 채워지는지 본다.
func TestProductFromPCI_FallsBackToDB(t *testing.T) {
	sysfsRoot := t.TempDir()
	fakePciDev(t, sysfsRoot, "0000:99:00.0", "0x10de", "0x20f1", "0x030200") // 표에 없음(20f1)

	dbPath := filepath.Join(t.TempDir(), "pci.ids")
	if err := os.WriteFile(dbPath, []byte("10de  NVIDIA Corporation\n\t20f1  GA100 [A100 PCIe 40GB]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDB := pciDBPath
	pciDBPath = dbPath
	t.Cleanup(func() { pciDBPath = oldDB })

	if got := productFromPCI(sysfsRoot, "0000:99:00.0"); got != "a100-pcie-40gb" {
		t.Errorf("productFromPCI = %q, want %q", got, "a100-pcie-40gb")
	}
}

// TestProductFromPCI_NoDBFileStillEmpty 는 pci.ids 없는 노드에서도 죽지 않고
// 빈 문자열(기존 generic 폴백)을 돌리는지 본다.
func TestProductFromPCI_NoDBFileStillEmpty(t *testing.T) {
	sysfsRoot := t.TempDir()
	fakePciDev(t, sysfsRoot, "0000:99:00.0", "0x10de", "0x20f1", "0x030200")

	oldDB := pciDBPath
	pciDBPath = filepath.Join(t.TempDir(), "no-such-file.ids")
	t.Cleanup(func() { pciDBPath = oldDB })

	if got := productFromPCI(sysfsRoot, "0000:99:00.0"); got != "" {
		t.Errorf("productFromPCI = %q, want \"\"", got)
	}
}

// TestMigCapableReStillMatchesSlug 는 slugProduct 가 만드는 형태("a30-pcie")가
// internal/partition/nvidia 의 migCapableRe(`(?i)\b(a30|a100|h100)\b`)에 여전히
// 걸리는지 이 패키지 안에서 같은 정규식으로 못박는다. 안 걸리면 MIG 판정이 깨진다.
func TestMigCapableReStillMatchesSlug(t *testing.T) {
	migCapableRe := regexp.MustCompile(`(?i)\b(a30|a100|h100)\b`)
	for _, tc := range []struct {
		slug string
		want bool
	}{
		{slugProduct("GA100GL [A30 PCIe]"), true},       // a30-pcie
		{slugProduct("GA100 [A100 PCIe 40GB]"), true},   // a100-pcie-40gb
		{slugProduct("GA107GL [A2 / A16]"), false},      // a2-a16
		{slugProduct("GM204 [GeForce GTX 970]"), false}, // geforce-gtx-970
	} {
		if got := migCapableRe.MatchString(tc.slug); got != tc.want {
			t.Errorf("migCapableRe.MatchString(%q) = %v, want %v", tc.slug, got, tc.want)
		}
	}
}
