// ============================================================
// cdi_test.go: CDI 스펙 작성·삭제 시험
// 상세: 스펙에 훅을 넣지 않는 것과 장치 이름이 정적인 것을 고정한다. 이름이 바뀌면 claim
// 재생성마다 CDI 장치 참조가 끊긴다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCDISpec(t *testing.T) {
	dir := t.TempDir()
	name, err := writeCDISpec(dir, "npu.example.com", "npu0",
		[]string{"/dev/rngd/npu0mgmt", "/dev/rngd/npu0pe0"})
	if err != nil {
		t.Fatalf("writeCDISpec: %v", err)
	}
	// 장치 이름은 정적이어야 한다. claim UID 가 들어가면 재부팅·재생성마다 바뀐다.
	if name != "npu.example.com/device=npu0" {
		t.Fatalf("장치 이름이 정적이 아니다: %s", name)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if len(files) != 1 {
		t.Fatalf("스펙 파일 1개여야 한다: %v", files)
	}
	body, _ := os.ReadFile(files[0])
	s := string(body)

	for _, want := range []string{"cdiVersion", "npu.example.com", "npu0", "/dev/rngd/npu0mgmt", "/dev/rngd/npu0pe0"} {
		if !strings.Contains(s, want) {
			t.Errorf("스펙에 %q 가 없다:\n%s", want, s)
		}
	}
	// 훅은 넣지 않는다.
	if strings.Contains(s, "hooks") || strings.Contains(s, "createContainer") {
		t.Errorf("스펙에 훅이 들어갔다:\n%s", s)
	}
}

func TestRemoveCDISpec(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeCDISpec(dir, "npu.example.com", "npu0", []string{"/dev/a"}); err != nil {
		t.Fatal(err)
	}
	if err := removeCDISpec(dir, "npu.example.com", "npu0"); err != nil {
		t.Fatalf("removeCDISpec: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if len(files) != 0 {
		t.Errorf("스펙이 남았다: %v", files)
	}
}

// 없는 것을 지우는 것은 오류가 아니다. 회수 경로가 두 번 불려도 실패하면 안 된다.
func TestRemoveCDISpecIsIdempotent(t *testing.T) {
	if err := removeCDISpec(t.TempDir(), "npu.example.com", "gone"); err != nil {
		t.Errorf("없는 스펙 삭제가 실패했다: %v", err)
	}
}
