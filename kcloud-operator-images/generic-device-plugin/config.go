// ============================================================
// config.go: 일반 device-plugin 설정
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
	ResourceName string   `json:"resourceName"`
	DeviceNodes  []string `json:"deviceNodes"`
	// Replicas 는 발견된 장치 하나를 몇 개로 광고할지다(격리 없는 SW 시분할). 0/1 이면 미적용.
	Replicas int32 `json:"replicas,omitempty"`
}

// LoadConfig 는 설정을 읽고 검증한다. 검증 실패면 기동하지 않는다 —
// 잘못된 설정으로 광고를 시작하면 스케줄러가 없는 장치에 파드를 붙인다.
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
	if !strings.Contains(c.ResourceName, "/") {
		return fmt.Errorf("resourceName 은 `벤더/자원` 형식이어야 한다: %q", c.ResourceName)
	}
	if len(c.DeviceNodes) == 0 {
		return fmt.Errorf("deviceNodes 가 비었다 — 발견할 것이 없다")
	}
	if c.Replicas < 0 || c.Replicas > 16 {
		return fmt.Errorf("replicas 는 0~16 범위여야 한다: %d", c.Replicas)
	}
	for i, g := range c.DeviceNodes {
		if !strings.HasPrefix(g, "/") {
			return fmt.Errorf("deviceNodes[%d] 는 절대경로여야 한다: %q", i, g)
		}
	}
	return nil
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

// discoverDevicesWithReplicas 는 발견 결과를 replicas 개로 복제한다(격리 없는 시분할).
// discoverDevices 의 두 return 경로(고정 장치/anchor) 어느 쪽이든 여기서 확장되므로
// 한쪽만 고치는 함정을 피한다.
func discoverDevicesWithReplicas(deviceNodes []string, root string, replicas int32) ([]Device, error) {
	base, err := discoverDevices(deviceNodes, root)
	if err != nil || replicas <= 1 {
		return base, err
	}
	out := make([]Device, 0, len(base)*int(replicas))
	for _, d := range base {
		for i := int32(0); i < replicas; i++ {
			out = append(out, Device{ID: fmt.Sprintf("%s-%d", d.ID, i), NodeGlobs: d.NodeGlobs})
		}
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
