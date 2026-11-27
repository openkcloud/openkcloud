// ============================================================
// cdi.go: claim 에 붙일 CDI 스펙 작성과 회수
// 상세: 훅을 넣지 않고 장치 이름을 정적으로 쓴다. 이름이 정적이면 재생성 때도 같은 장치를
// 되짚는다 — claim UID 를 넣으면 재부팅·재생성마다 이름이 바뀐다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cdiKind 는 CDI 스펙의 kind 다. `<드라이버>/device` 형태를 쓴다.
func cdiKind(driverName string) string { return driverName + "/device" }

// cdiSpecPath 는 그 장치의 스펙 파일 경로다. 장치마다 파일 하나 - 회수가 단순해진다.
func cdiSpecPath(dir, driverName, deviceID string) string {
	safe := strings.NewReplacer("/", "-", ".", "-").Replace(driverName)
	return filepath.Join(dir, fmt.Sprintf("%s_%s.yaml", safe, deviceID))
}

// writeCDISpec 은 장치 하나의 스펙을 쓰고 CDI 장치 이름을 돌려준다.
func writeCDISpec(dir, driverName, deviceID string, nodes []string) (string, error) {
	if len(nodes) == 0 {
		return "", fmt.Errorf("장치 %s 에 넣을 노드가 없다", deviceID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("CDI 디렉터리 생성 실패: %w", err)
	}

	var b strings.Builder
	b.WriteString("cdiVersion: \"0.5.0\"\n")
	fmt.Fprintf(&b, "kind: %q\n", cdiKind(driverName))
	b.WriteString("devices:\n")
	fmt.Fprintf(&b, "  - name: %q\n", deviceID)
	b.WriteString("    containerEdits:\n      deviceNodes:\n")
	for _, n := range nodes {
		fmt.Fprintf(&b, "        - path: %q\n          permissions: \"rw\"\n", n)
	}

	p := cdiSpecPath(dir, driverName, deviceID)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("CDI 스펙 작성 실패: %w", err)
	}
	return cdiKind(driverName) + "=" + deviceID, nil
}

// removeCDISpec 은 그 장치의 스펙을 지운다. 없으면 성공으로 본다.
func removeCDISpec(dir, driverName, deviceID string) error {
	err := os.Remove(cdiSpecPath(dir, driverName, deviceID))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("CDI 스펙 삭제 실패: %w", err)
	}
	return nil
}
