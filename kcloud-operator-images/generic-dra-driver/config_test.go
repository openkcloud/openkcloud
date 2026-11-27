// ============================================================
// config_test.go: 설정 검증·발견 시험
// 상세: 잘못된 설정으로 기동해 스케줄러가 없는 장치에 파드를 붙이는 것을 막는다. 장치는
// 이제 설정이 아니라 host /dev 발견 결과이므로, root tempdir 에 파일을 깔아 발견을 유도한다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func validConfig() Config {
	return Config{
		DriverName:  "npu.example.com",
		DeviceClass: "npu.example.com",
		DeviceNodes: []string{"/dev/rngd/{{id}}/mgmt"},
	}
}

func TestConfigValidateAccepts(t *testing.T) {
	c := validConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("유효한 설정이 거절됐다: %v", err)
	}
}

func TestConfigValidateRejectsEmptyDriverName(t *testing.T) {
	c := validConfig()
	c.DriverName = ""
	if err := c.Validate(); err == nil {
		t.Fatal("빈 driverName 이 통과했다")
	}
}

func TestConfigValidateRejectsEmptyDeviceClass(t *testing.T) {
	c := validConfig()
	c.DeviceClass = ""
	if err := c.Validate(); err == nil {
		t.Fatal("빈 deviceClass 가 통과했다")
	}
}

func TestConfigValidateRejectsNoDeviceNodes(t *testing.T) {
	c := validConfig()
	c.DeviceNodes = nil
	if err := c.Validate(); err == nil {
		t.Fatal("빈 deviceNodes 가 통과했다")
	}
}

func TestConfigValidateRejectsRelativeDeviceNode(t *testing.T) {
	c := validConfig()
	c.DeviceNodes = []string{"dev/rngd/{{id}}/mgmt"}
	if err := c.Validate(); err == nil {
		t.Fatal("상대경로 deviceNode 가 통과했다")
	}
}

// touch 는 root 밑에 빈 파일을 만든다(host /dev 흉내).
func touch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverCleanAnchor(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/tenstorrent/0")
	got, err := discoverDevices([]string{"/dev/tenstorrent/{{id}}"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	want := []Device{{ID: "0", NodeGlobs: []string{"/dev/tenstorrent/0"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("발견 결과가 다르다: %+v", got)
	}
}

func TestDiscoverMultipleIndicesAndShared(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/acme/0")
	touch(t, root, "dev/acme/1")
	touch(t, root, "dev/acmectl")
	got, err := discoverDevices([]string{"/dev/acme/{{id}}", "/dev/acmectl"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("장치 2개여야 한다: %+v", got)
	}
	if got[0].ID != "0" || got[1].ID != "1" {
		t.Fatalf("인덱스가 정렬·추출되지 않았다: %+v", got)
	}
	// 공통 고정 노드가 모든 장치에 붙어야 한다.
	if got[0].NodeGlobs[1] != "/dev/acmectl" || got[1].NodeGlobs[1] != "/dev/acmectl" {
		t.Fatalf("공통 노드가 안 붙었다: %+v", got)
	}
}

func TestDiscoverFixedSingleDevice(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/nvidia0")
	touch(t, root, "dev/nvidiactl")
	got, err := discoverDevices([]string{"/dev/nvidia0", "/dev/nvidiactl"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(got) != 1 || got[0].ID != "0" {
		t.Fatalf("고정 단일 장치여야 한다: %+v", got)
	}
}

func TestDiscoverRejectsAdjacentWildcard(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/rngd/npu0")
	// {{id}} 뒤에 * 가 붙으면 인덱스 성분이 모호하다 — 거절.
	if _, err := discoverDevices([]string{"/dev/rngd/{{id}}*"}, root); err == nil {
		t.Fatal("인접 와일드카드는 거절돼야 한다")
	}
}

// by-id·by-path 같은 디렉터리가 /dev/<벤더>/ 아래 섞여 있어도 장치로 세면 안 된다.
// tenstorrent 는 /dev/tenstorrent/ 아래 카드 번호(0)와 by-id 디렉터리가 함께 있어, 디렉터리까지 세면 과다광고된다.
func TestDiscoverSkipsNonDeviceDirs(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/tenstorrent/0")
	if err := os.MkdirAll(filepath.Join(root, "dev/tenstorrent/by-id"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := discoverDevices([]string{"/dev/tenstorrent/{{id}}"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(got) != 1 || got[0].ID != "0" {
		t.Fatalf("디렉터리(by-id)가 장치로 잡혔다: %+v", got)
	}
}
