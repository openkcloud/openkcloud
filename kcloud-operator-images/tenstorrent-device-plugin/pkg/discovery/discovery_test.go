// ============================================================
// discovery_test.go: discovery 패키지 단위 테스트
// 상세: fake /dev/tenstorrent 디렉토리로 Discover() 검증
// 생성일: 2026-04-22 | 수정일: 2026-04-22
// ============================================================

package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsBlackhole(t *testing.T) {
	cases := []struct {
		cardType string
		want     bool
	}{
		{"blackhole", true},
		{"Blackhole", true},
		{"p150", true},
		{"p300", true},
		{"wormhole", false},
		{"grayskull", false},
		{"", true}, // sysfs 없으면 포함
	}
	for _, tc := range cases {
		got := isBlackhole(tc.cardType)
		if got != tc.want {
			t.Errorf("isBlackhole(%q) = %v, want %v", tc.cardType, got, tc.want)
		}
	}
}

func TestReadSysAttr_Missing(t *testing.T) {
	result := readSysAttr("/nonexistent/path", "attr")
	if result != "" {
		t.Errorf("존재하지 않는 sysfs 속성은 빈 문자열이어야 함, got %q", result)
	}
}

func TestReadNUMANode_Missing(t *testing.T) {
	n := readNUMANode("/nonexistent/path")
	if n != -1 {
		t.Errorf("존재하지 않는 NUMA 노드는 -1이어야 함, got %d", n)
	}
}

func TestDiscover_WithFakeDevices(t *testing.T) {
	// /dev/tenstorrent 경로를 임시 디렉토리로 교체하여 테스트
	origDevBase := devBasePath
	origSysBase := sysBasePath

	tmpDir := t.TempDir()
	fakeDevDir := filepath.Join(tmpDir, "dev", "tenstorrent")
	fakeSysDir := filepath.Join(tmpDir, "sys", "class", "tenstorrent")

	if err := os.MkdirAll(fakeDevDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fakeSysDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 패키지 변수 임시 교체
	setDevBasePath(fakeDevDir)
	setSysBasePath(fakeSysDir)
	defer func() {
		setDevBasePath(origDevBase)
		setSysBasePath(origSysBase)
	}()

	// fake device 파일 생성 (id: "0")
	devFile := filepath.Join(fakeDevDir, "0")
	if err := os.WriteFile(devFile, []byte{}, 0660); err != nil {
		t.Fatal(err)
	}

	// sysfs card_type 생성 (blackhole)
	sysDevDir := filepath.Join(fakeSysDir, "0")
	if err := os.MkdirAll(sysDevDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysDevDir, "card_type"), []byte("blackhole\n"), 0644); err != nil {
		t.Fatal(err)
	}

	devs, err := Discover()
	if err != nil {
		t.Fatalf("Discover() 실패: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("디바이스 1개 기대, got %d", len(devs))
	}
	if devs[0].ID != "0" {
		t.Errorf("ID = %q, want %q", devs[0].ID, "0")
	}
	if devs[0].CardType != "blackhole" {
		t.Errorf("CardType = %q, want %q", devs[0].CardType, "blackhole")
	}
	if devs[0].Health != "Healthy" {
		t.Errorf("Health = %q, want %q", devs[0].Health, "Healthy")
	}
}

// 패키지 수준 경로 변수 접근용 helper (테스트에서 오버라이드)
func setDevBasePath(p string) { devBasePathVar = p }
func setSysBasePath(p string) { sysBasePathVar = p }
