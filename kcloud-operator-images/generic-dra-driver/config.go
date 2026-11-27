// ============================================================
// config.go: 일반 DRA 드라이버 설정
// 상세: 이 런타임은 벤더를 모른다. operator 가 AcceleratorDescriptor 로 컴파일한 발견 템플릿
// (deviceNodes) 을 받아, host /dev 를 못 보는 operator 대신 실재 장치 열거를 직접 한다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Device 는 발견된 광고 단위 하나다(런타임 내부 표현). config JSON 에는 나오지 않는다.
type Device struct {
	ID        string
	NodeGlobs []string
}

// Config 는 이 런타임의 전부다. 장치 목록이 아니라 발견 템플릿을 받는다 —
// 어떤 /dev 가 실재하는지는 런타임이 host 를 열거해 정한다(operator 는 host /dev 를 못 본다).
type Config struct {
	DriverName  string   `json:"driverName"`
	DeviceClass string   `json:"deviceClass"`
	DeviceNodes []string `json:"deviceNodes"`

	// Attributes 는 descriptor 가 선언한 속성이다. sysfs 로는 알 수 없는 값(제품명 등)이
	// 여기로 들어온다 — 이 런타임은 벤더 도구를 모른다. 같은 이름을 sysfs 에서도 읽으면
	// sysfs 쪽을 쓴다(관측이 선언보다 우선한다).
	Attributes map[string]string `json:"attributes,omitempty"`
	// Capacity 는 descriptor 가 선언한 capacity 다. 값은 Quantity 표기(예: "80Gi").
	Capacity map[string]string `json:"capacity,omitempty"`
	// CapacityFiles 는 capacity 이름 -> PCI 장치 sysfs 디렉터리 아래 상대 경로다.
	// 바이트 수를 담은 파일을 읽어 capacity 로 낸다. 어느 파일에 용량이 있는지는 벤더마다
	// 다르므로 그 지식은 descriptor 에 두고 런타임은 읽기만 한다. 읽히면 선언값을 덮는다.
	CapacityFiles map[string]string `json:"capacityFiles,omitempty"`
}

// LoadConfig 는 설정을 읽고 검증한다. 검증 실패면 기동하지 않는다 —
// 잘못된 설정으로 발행을 시작하면 스케줄러가 없는 장치에 파드를 붙인다.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("설정 읽기 실패: %w", err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("설정 파싱 실패: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate 는 설정 자체의 정합을 본다.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.DriverName) == "" {
		return fmt.Errorf("driverName 이 비었다")
	}
	if strings.TrimSpace(c.DeviceClass) == "" {
		return fmt.Errorf("deviceClass 가 비었다")
	}
	if len(c.DeviceNodes) == 0 {
		return fmt.Errorf("deviceNodes 가 비었다 — 발견할 것이 없다")
	}
	for i, g := range c.DeviceNodes {
		if !strings.HasPrefix(g, "/") {
			return fmt.Errorf("deviceNodes[%d] 는 절대경로여야 한다: %q", i, g)
		}
	}
	return validateDeclarations(c)
}

// resolveNodes 는 glob 을 실제 호스트 경로 목록으로 편다.
// root 는 호스트 루트가 컨테이너에 마운트된 위치다(보통 "/host"). 반환값에서는 벗겨낸다 —
// 컨테이너에 넣을 것은 호스트 관점 경로이기 때문이다.
func resolveNodes(globs []string, root string) ([]string, error) {
	var out []string
	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(root, g))
		if err != nil {
			return nil, fmt.Errorf("glob %q 해석 실패: %w", g, err)
		}
		for _, m := range matches {
			rel := strings.TrimPrefix(m, strings.TrimSuffix(root, "/"))
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

const devicePlaceholder = "{{id}}"

// discoverDevices 는 deviceNodes 템플릿을 host root 밑 실재 장치로 편다.
func discoverDevices(deviceNodes []string, root string) ([]Device, error) {
	var anchors, shared []string
	for _, t := range deviceNodes {
		if strings.Contains(t, devicePlaceholder) {
			anchors = append(anchors, t)
		} else {
			shared = append(shared, t)
		}
	}
	if len(anchors) == 0 {
		// {{id}} 가 없으면 고정 장치 하나다.
		return []Device{{ID: "0", NodeGlobs: append([]string{}, shared...)}}, nil
	}
	ids, err := discoverIDs(anchors[0], root)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(ids))
	for _, id := range ids {
		globs := make([]string, 0, len(anchors)+len(shared))
		for _, a := range anchors {
			globs = append(globs, strings.ReplaceAll(a, devicePlaceholder, id))
		}
		globs = append(globs, shared...)
		out = append(out, Device{ID: sanitizeID(id), NodeGlobs: globs})
	}
	return out, nil
}

// discoverIDs 는 anchor 의 {{id}} 자리에 실재하는 성분들을 host 에서 뽑는다.
// {{id}} 는 깨끗한 경로 성분이어야 한다(앞은 '/', 뒤는 비었거나 '/').
func discoverIDs(anchor, root string) ([]string, error) {
	i := strings.Index(anchor, devicePlaceholder)
	prefix, suffix := anchor[:i], anchor[i+len(devicePlaceholder):]
	if !strings.HasSuffix(prefix, "/") || (suffix != "" && !strings.HasPrefix(suffix, "/")) {
		return nil, fmt.Errorf("{{id}} 는 깨끗한 경로 성분이어야 한다(인접 와일드카드 불가): %q", anchor)
	}
	matches, err := filepath.Glob(filepath.Join(root, prefix+"*"+suffix))
	if err != nil {
		return nil, fmt.Errorf("glob 실패: %w", err)
	}
	base := filepath.Join(root, prefix)
	seen := map[string]bool{}
	var ids []string
	for _, m := range matches {
		// /dev/<벤더>/ 아래엔 by-id·by-path 같은 디렉터리가 섞인다 — 장치 노드가
		// 아니므로 제외한다(장치는 char/block 파일이지 디렉터리가 아니다).
		if fi, err := os.Stat(m); err != nil || fi.IsDir() {
			continue
		}
		rest := strings.TrimPrefix(m, base)
		rest = strings.TrimPrefix(rest, "/")
		if suffix != "" {
			rest = strings.TrimSuffix(rest, suffix)
		}
		rest = strings.Trim(rest, "/")
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		if !seen[rest] {
			seen[rest] = true
			ids = append(ids, rest)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// sanitizeID 는 ID 를 DNS-1123 label 로 정규화한다(DRA Device.Name 계약과 동일 규칙 재사용).
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "0"
	}
	return out
}
