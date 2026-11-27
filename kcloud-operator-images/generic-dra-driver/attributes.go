// ============================================================
// attributes.go: 장치 속성·capacity 수집
// 상세: host sysfs 에서 PCI 속성을 읽어 descriptor 선언과 합친다. 벤더 지식 없이 커널이 주는
// /sys/dev/char 링크만 쓰고, 읽히지 않는 항목은 붙이지 않는다(빈 값은 셀렉터가 유효값으로 읽는다).
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

// sysfsUnknown 은 링크가 올라오지 않은 PCI 속성에 커널이 적는 값이다.
const sysfsUnknown = "Unknown"

// maxDeclarations 는 선언으로 받을 수 있는 속성·capacity 수다. id 와 pciBusID,
// 그리고 PCI 관측 자리를 DRA 상한에서 뺀 나머지다.
var maxDeclarations = resourceapi.ResourceSliceMaxAttributesAndCapacitiesPerDevice - 2 - len(pciSysfsAttrs)

// pciSysfsAttrs 는 PCI 장치 디렉터리에서 그대로 읽히는 속성이다. asInt 면 정수로 싣고 minInt
// 보다 작은 값은 붙이지 않는다 — numa_node 는 모를 때 -1, max_link_width 는 모를 때 0 이다.
//
// 링크 속성은 current_link_* 가 아니라 max_link_* 에서 읽는다 — 셀렉터가 맞출 것은 장치의
// 능력이지, 절전으로 내려앉은 그 순간의 협상 결과가 아니다. 슬라이스는 NewDriver 에서 한 번만
// 만들고 갱신하지 않으므로, 이후 링크가 바뀌어도 반영되지 않는다.
var pciSysfsAttrs = []struct {
	name   string
	file   string
	asInt  bool
	minInt int64
}{
	{name: "pciDeviceID", file: "device"},
	{name: "vendorID", file: "vendor"},
	{name: "numaNode", file: "numa_node", asInt: true, minInt: 0},
	{name: "pciLinkSpeed", file: "max_link_speed"},
	{name: "pciLinkWidth", file: "max_link_width", asInt: true, minInt: 1},
}

var (
	// pciBDFRE 는 sysfs 가 쓰는 PCI 함수 주소 표기다(소문자 16진).
	pciBDFRE = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-9a-f]$`)
	// cIdentRE 는 DRA 속성 이름 규칙이다(C 식별자).
	cIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// devMajorMinor 는 장치 노드의 char major:minor 를 돌려준다.
//
// 시험이 이 함수를 대체한다 — 임시 디렉터리에는 char 장치를 만들 수 없다(CAP_MKNOD
// 가 필요하다). 대체되는 것은 stat 한 번뿐이고 그 뒤 sysfs 조회는 시험도 실물과 같은
// 경로를 읽는다.
var devMajorMinor = func(path string) (uint32, uint32, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0, false
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return 0, 0, false
	}
	rdev := uint64(st.Rdev)
	major := uint32(((rdev >> 32) & 0xfffff000) | ((rdev >> 8) & 0xfff))
	minor := uint32(((rdev >> 12) & 0xffffff00) | (rdev & 0xff))
	return major, minor, true
}

// readSysfs 는 sysfs 파일 하나를 읽어 둘레 공백을 떼고 돌려준다. 못 읽으면 ok=false.
func readSysfs(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// pciDeviceDir 는 장치 노드에서 PCI 함수 주소(BDF)와 그 sysfs 디렉터리를 찾는다.
// 커널이 주는 /sys/dev/char/<major>:<minor> 링크만 쓴다 — /dev 경로에서 BDF 로 가는
// 규칙은 벤더마다 다르므로 추측하지 않는다. 못 찾으면 ok=false 이고 호출부는 PCI
// 속성을 아예 붙이지 않는다.
func pciDeviceDir(hostRoot, nodeGlob string) (string, string, bool) {
	// Glob 은 정렬된 결과를 준다. 한 장치의 anchor glob 이 여러 노드로 펴지면
	// 첫 노드를 대표로 쓴다.
	matches, err := filepath.Glob(filepath.Join(hostRoot, nodeGlob))
	if err != nil || len(matches) == 0 {
		return "", "", false
	}
	major, minor, ok := devMajorMinor(matches[0])
	if !ok {
		return "", "", false
	}
	link := filepath.Join(hostRoot, "sys/dev/char", fmt.Sprintf("%d:%d", major, minor))
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", "", false
	}
	// 해석된 sysfs 경로에서 장치에 가장 가까운 PCI 함수 성분을 집는다. 장치가 PCI
	// 아래 달려 있지 않으면(misc 제어 노드 등) 아무 성분도 맞지 않는다.
	for p := target; ; {
		if bdf := filepath.Base(p); pciBDFRE.MatchString(bdf) {
			dir := filepath.Join(hostRoot, "sys/bus/pci/devices", bdf)
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				return "", "", false
			}
			return bdf, dir, true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", "", false
		}
		p = parent
	}
}

// rejectAttrValue 는 문자열 속성 값을 쓸 수 없는 이유를 돌려준다. nil 이면 쓸 수 있다.
// 기동 시 선언 검증과 발행 시 sysfs 관측이 같은 판정을 쓴다 — 기준이 두 곳으로 갈라지면
// 한쪽이 거른 값이 다른 쪽으로 새어 나간다.
func rejectAttrValue(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("값이 비었다")
	}
	if len(value) > resourceapi.DeviceAttributeMaxValueLength {
		return fmt.Errorf("값이 %d자를 넘는다", resourceapi.DeviceAttributeMaxValueLength)
	}
	return nil
}

// putString 은 쓸 수 있는 값일 때만 문자열 속성을 붙인다 — 빈 문자열을 붙이면
// 셀렉터가 그것을 유효한 값으로 읽는다.
func putString(m map[resourceapi.QualifiedName]resourceapi.DeviceAttribute, name, value string) {
	if rejectAttrValue(value) != nil {
		return
	}
	v := value
	m[resourceapi.QualifiedName(name)] = resourceapi.DeviceAttribute{StringValue: &v}
}

// deviceResources 는 장치 하나에 붙일 속성과 capacity 를 모은다.
//
// 선언(descriptor)을 먼저 깔고 sysfs 관측으로 덮는다 — 관측이 선언보다 우선한다.
// 셋째 반환값은 PCI 주소를 찾았는지다. 오류는 돌려주지 않는다: 장치 하나를 못 읽어도
// 나머지는 그대로 발행돼야 한다.
func deviceResources(cfg *Config, hostRoot string, dev Device) (map[resourceapi.QualifiedName]resourceapi.DeviceAttribute, map[resourceapi.QualifiedName]resourceapi.DeviceCapacity, bool) {
	attrs := map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{}
	caps := map[resourceapi.QualifiedName]resourceapi.DeviceCapacity{}

	for name, value := range cfg.Attributes {
		putString(attrs, name, value)
	}
	for name, value := range cfg.Capacity {
		q, err := resource.ParseQuantity(value)
		if err != nil {
			continue
		}
		caps[resourceapi.QualifiedName(name)] = resourceapi.DeviceCapacity{Value: q}
	}

	pciFound := false
	if len(dev.NodeGlobs) > 0 {
		// 첫 glob 만 본다 — 그것이 {{id}} 에서 펴진 장치별 노드다. 공유 제어 노드로
		// 찾으면 노드의 모든 장치가 같은 PCI 함수로 매핑된다.
		if bdf, dir, ok := pciDeviceDir(hostRoot, dev.NodeGlobs[0]); ok {
			pciFound = true
			putString(attrs, "pciBusID", bdf)
			collectPCIAttrs(attrs, dir)
			collectPCICapacity(caps, cfg.CapacityFiles, dir)
		}
	}

	id := dev.ID
	attrs["id"] = resourceapi.DeviceAttribute{StringValue: &id}
	if len(caps) == 0 {
		return attrs, nil, pciFound
	}
	return attrs, caps, pciFound
}

// collectPCIAttrs 는 PCI 장치 디렉터리에서 읽히는 속성만 붙인다.
func collectPCIAttrs(attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute, dir string) {
	for _, a := range pciSysfsAttrs {
		raw, ok := readSysfs(filepath.Join(dir, a.file))
		if !ok || raw == sysfsUnknown {
			continue
		}
		if !a.asInt {
			putString(attrs, a.name, raw)
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < a.minInt {
			continue
		}
		attrs[resourceapi.QualifiedName(a.name)] = resourceapi.DeviceAttribute{IntValue: &n}
	}
}

// collectPCICapacity 는 descriptor 가 가리킨 sysfs 파일에서 용량을 읽어 capacity 로
// 낸다. 값은 바이트 수다. 읽히지 않거나 수가 아니면 내지 않는다.
func collectPCICapacity(caps map[resourceapi.QualifiedName]resourceapi.DeviceCapacity, files map[string]string, dir string) {
	for name, rel := range files {
		raw, ok := readSysfs(filepath.Join(dir, rel))
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		caps[resourceapi.QualifiedName(name)] = resourceapi.DeviceCapacity{Value: *resource.NewQuantity(n, resource.BinarySI)}
	}
}

// validQualifiedName 은 DRA 속성·capacity 이름 규칙을 본다.
// C 식별자이거나 DNS subdomain + "/" + C 식별자여야 한다.
func validQualifiedName(name string) error {
	id := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		domain, rest := name[:i], name[i+1:]
		if errs := validation.IsDNS1123Subdomain(domain); len(errs) > 0 {
			return fmt.Errorf("도메인 접두사가 DNS subdomain 이 아니다: %q", domain)
		}
		// IsDNS1123Subdomain 은 253자까지 받지만 DRA 상한은 더 좁다. 여기서 막지 않으면
		// API 서버가 ResourceSlice 전체를 거절한다.
		if len(domain) > resourceapi.DeviceMaxDomainLength {
			return fmt.Errorf("도메인 접두사가 %d자를 넘는다", resourceapi.DeviceMaxDomainLength)
		}
		id = rest
	}
	if !cIdentRE.MatchString(id) || len(id) > resourceapi.DeviceMaxIDLength {
		return fmt.Errorf("이름이 C 식별자(%d자 이하)가 아니다", resourceapi.DeviceMaxIDLength)
	}
	return nil
}

// validateDeclarations 는 descriptor 가 선언한 속성·capacity 를 본다. 잘못된 이름이나
// 값 하나가 ResourceSlice 발행 전체를 거절시키므로 기동 전에 걸러낸다.
func validateDeclarations(c *Config) error {
	for _, m := range []map[string]string{c.Attributes, c.Capacity, c.CapacityFiles} {
		for name := range m {
			if err := validQualifiedName(name); err != nil {
				return fmt.Errorf("선언한 이름 %q: %w", name, err)
			}
		}
	}
	for name, value := range c.Attributes {
		// 발행 때 조용히 빠지는 대신 기동 때 막는다 — 선언한 값이 안 나가는 것을
		// 운영자가 알아야 한다.
		if err := rejectAttrValue(value); err != nil {
			return fmt.Errorf("attributes[%q]: %w", name, err)
		}
	}
	for name, value := range c.Capacity {
		if _, err := resource.ParseQuantity(value); err != nil {
			return fmt.Errorf("capacity[%q] 가 Quantity 표기가 아니다: %q", name, value)
		}
	}
	for name, rel := range c.CapacityFiles {
		if rel == "" || strings.HasPrefix(rel, "/") || rel != filepath.Clean(rel) ||
			rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("capacityFiles[%q] 는 PCI 디렉터리 아래 상대 경로여야 한다: %q", name, rel)
		}
	}
	if n := len(c.Attributes) + len(c.Capacity) + len(c.CapacityFiles); n > maxDeclarations {
		return fmt.Errorf("선언이 %d개다 — sysfs 관측 자리를 포함하면 장치 하나당 32개를 넘는다", n)
	}
	return nil
}
