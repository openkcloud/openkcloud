// ============================================================
// attributes_test.go: 장치 속성·capacity 수집 시험
// 상세: 하드웨어 없이 돈다 — host root 를 임시 디렉터리에 두고 실물과 같은 sysfs 나무(/sys/dev/char
// 링크, /sys/bus/pci/devices 링크, PCI 속성 파일)를 깐다. char 장치는 못 만들어 major:minor 함수만 대체한다.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	resourceapi "k8s.io/api/resource/v1"
)

// fixture 는 host root 하나와 그 안의 장치 노드·sysfs 나무를 들고 있다.
type fixture struct {
	t    *testing.T
	root string
	rdev map[string][2]uint32
}

// newFixture 는 빈 host root 를 만들고 major:minor 조회를 표로 대체한다.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, root: t.TempDir(), rdev: map[string][2]uint32{}}
	prev := devMajorMinor
	devMajorMinor = func(path string) (uint32, uint32, bool) {
		mm, ok := f.rdev[path]
		if !ok {
			return 0, 0, false
		}
		return mm[0], mm[1], true
	}
	t.Cleanup(func() { devMajorMinor = prev })
	return f
}

// addDevice 는 장치 노드 하나와 그것이 달린 PCI 함수의 sysfs 나무를 만든다.
// 실물 배치를 그대로 흉내 낸다 — /sys/dev/char 와 /sys/bus/pci/devices 는 둘 다
// /sys/devices 아래 실체를 가리키는 상대 심볼릭 링크다. attrs 에 없는 파일은 만들지
// 않는다(읽히지 않는 항목을 흉내 내려면 그냥 빼면 된다).
func (f *fixture) addDevice(devRel, bdf string, major, minor uint32, attrs map[string]string) {
	f.t.Helper()
	name := filepath.Base(devRel)
	pciDir := filepath.Join(f.root, "sys/devices/pci0000:00", bdf)
	f.mkdir(filepath.Join(pciDir, "subsys", name))
	for file, value := range attrs {
		f.write(filepath.Join(pciDir, file), value)
	}

	busDir := filepath.Join(f.root, "sys/bus/pci/devices")
	f.mkdir(busDir)
	f.symlink(filepath.Join("../../../devices/pci0000:00", bdf), filepath.Join(busDir, bdf))

	charDir := filepath.Join(f.root, "sys/dev/char")
	f.mkdir(charDir)
	f.symlink(filepath.Join("../../devices/pci0000:00", bdf, "subsys", name),
		filepath.Join(charDir, fmt.Sprintf("%d:%d", major, minor)))

	node := f.addNodeOnly(devRel)
	f.rdev[node] = [2]uint32{major, minor}
}

// addNodeOnly 는 PCI 나무 없이 장치 노드만 만든다 — /dev 에서 BDF 로 가는 길이 없는
// 장치(벤더가 /sys/dev/char 를 거치지 않는 경우)를 흉내 낸다.
func (f *fixture) addNodeOnly(devRel string) string {
	f.t.Helper()
	node := filepath.Join(f.root, devRel)
	f.mkdir(filepath.Dir(node))
	f.write(node, "")
	return node
}

func (f *fixture) mkdir(p string) {
	f.t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) write(p, content string) {
	f.t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) symlink(target, link string) {
	f.t.Helper()
	if err := os.Symlink(target, link); err != nil {
		f.t.Fatal(err)
	}
}

// fullPCIAttrs 는 PCI 디렉터리에서 다 읽히는 경우다.
func fullPCIAttrs() map[string]string {
	return map[string]string{
		"device":         "0x1e3d\n",
		"vendor":         "0x1f9f\n",
		"numa_node":      "1\n",
		"max_link_speed": "16.0 GT/s PCIe\n",
		"max_link_width": "16\n",
	}
}

func acmeConfig() *Config {
	return &Config{
		DriverName:  "npu.example.com",
		DeviceClass: "npu.example.com",
		DeviceNodes: []string{"/dev/acme/{{id}}", "/dev/acmectl"},
	}
}

func attrString(t *testing.T, attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute, name string) string {
	t.Helper()
	a, ok := attrs[resourceapi.QualifiedName(name)]
	if !ok {
		t.Fatalf("속성 %q 가 없다: %v", name, attrNames(attrs))
	}
	if a.StringValue == nil {
		t.Fatalf("속성 %q 가 문자열이 아니다: %+v", name, a)
	}
	return *a.StringValue
}

func attrInt(t *testing.T, attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute, name string) int64 {
	t.Helper()
	a, ok := attrs[resourceapi.QualifiedName(name)]
	if !ok {
		t.Fatalf("속성 %q 가 없다: %v", name, attrNames(attrs))
	}
	if a.IntValue == nil {
		t.Fatalf("속성 %q 가 정수가 아니다: %+v", name, a)
	}
	return *a.IntValue
}

func attrNames(attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute) []string {
	var out []string
	for n := range attrs {
		out = append(out, string(n))
	}
	return out
}

// assertPublishable 은 발행 전 장치가 DRA 구조 요건을 만족하는지 본다 — 이름이 있고,
// 속성·capacity 합이 32개 이하고, 속성마다 값이 정확히 하나 실려 있다. 값이 하나도
// 없는 속성이 섞이면 API 서버가 ResourceSlice 전체를 거절한다.
func assertPublishable(t *testing.T, devices []resourceapi.Device) {
	t.Helper()
	for _, d := range devices {
		if d.Name == "" {
			t.Error("장치 이름이 비었다")
		}
		if n := len(d.Attributes) + len(d.Capacity); n > resourceapi.ResourceSliceMaxAttributesAndCapacitiesPerDevice {
			t.Errorf("장치 %s 의 속성+capacity 가 %d개다", d.Name, n)
		}
		for name, a := range d.Attributes {
			set := 0
			for _, isSet := range []bool{a.StringValue != nil, a.IntValue != nil, a.BoolValue != nil, a.VersionValue != nil} {
				if isSet {
					set++
				}
			}
			if set != 1 {
				t.Errorf("장치 %s 속성 %s 에 값이 %d개 실렸다", d.Name, name, set)
			}
			if a.StringValue != nil && len(*a.StringValue) > resourceapi.DeviceAttributeMaxValueLength {
				t.Errorf("장치 %s 속성 %s 값이 길이 상한을 넘었다", d.Name, name)
			}
		}
	}
}

// 수용 기준 1: 장치 하나에 최소 네 속성이 붙는다.
func TestDeviceAttributesFromSysfs(t *testing.T) {
	f := newFixture(t)
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, fullPCIAttrs())
	f.addNodeOnly("dev/acmectl")

	discovered, err := discoverDevices(acmeConfig().DeviceNodes, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("장치 1개여야 한다: %+v", discovered)
	}
	attrs, _, pciFound := deviceResources(acmeConfig(), f.root, discovered[0])
	if !pciFound {
		t.Fatal("PCI 주소를 찾지 못했다")
	}
	if len(attrs) < 4 {
		t.Fatalf("속성이 네 개 미만이다: %v", attrNames(attrs))
	}
	if got := attrString(t, attrs, "pciBusID"); got != "0000:01:00.0" {
		t.Errorf("pciBusID: %q", got)
	}
	if got := attrString(t, attrs, "pciDeviceID"); got != "0x1e3d" {
		t.Errorf("pciDeviceID: %q", got)
	}
	if got := attrString(t, attrs, "vendorID"); got != "0x1f9f" {
		t.Errorf("vendorID: %q", got)
	}
	if got := attrInt(t, attrs, "numaNode"); got != 1 {
		t.Errorf("numaNode: %d", got)
	}
	if got := attrString(t, attrs, "pciLinkSpeed"); got != "16.0 GT/s PCIe" {
		t.Errorf("pciLinkSpeed: %q", got)
	}
	if got := attrInt(t, attrs, "pciLinkWidth"); got != 16 {
		t.Errorf("pciLinkWidth: %d", got)
	}
	if got := attrString(t, attrs, "id"); got != "0" {
		t.Errorf("id: %q", got)
	}
}

// 수용 기준 2: sysfs 에 없는 항목은 조용히 빠지고 빈 값이 붙지 않는다.
// numa_node 는 모를 때 -1 이고, max_link_speed 는 빈 파일일 수 있고,
// max_link_width 는 아예 없을 수 있다. 셋 다 속성으로 나오면 안 된다.
func TestDeviceAttributesSkipUnreadableAndEmpty(t *testing.T) {
	f := newFixture(t)
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, map[string]string{
		"device":         "0x1e3d\n",
		"vendor":         "0x1f9f\n",
		"numa_node":      "-1\n",
		"max_link_speed": "\n",
	})

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	attrs, caps, pciFound := deviceResources(acmeConfig(), f.root, discovered[0])
	if !pciFound {
		t.Fatal("PCI 주소를 찾지 못했다")
	}
	for _, name := range []string{"numaNode", "pciLinkSpeed", "pciLinkWidth"} {
		if _, ok := attrs[resourceapi.QualifiedName(name)]; ok {
			t.Errorf("읽히지 않는 %q 가 속성으로 붙었다: %+v", name, attrs[resourceapi.QualifiedName(name)])
		}
	}
	for name, a := range attrs {
		if a.StringValue != nil && *a.StringValue == "" {
			t.Errorf("속성 %q 에 빈 문자열이 붙었다 — 셀렉터가 유효한 값으로 읽는다", name)
		}
	}
	if len(caps) != 0 {
		t.Errorf("capacity 선언이 없는데 capacity 가 붙었다: %+v", caps)
	}
}

// 수용 기준 3: 읽기 실패가 드라이버를 죽이지 않는다. PCI 주소를 못 찾은 장치도
// 발행 목록에 남고, 몇 개를 못 찾았는지 수로 나온다.
func TestBuildDevicesSurvivesUnreadableDevice(t *testing.T) {
	f := newFixture(t)
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, fullPCIAttrs())
	f.addNodeOnly("dev/acme/1") // sysfs 나무 없음 — /dev 에서 BDF 로 가는 길이 없다

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(discovered) != 2 {
		t.Fatalf("장치 2개여야 한다: %+v", discovered)
	}

	devices, pciMissing := buildDevices(acmeConfig(), f.root, discovered)
	if len(devices) != 2 {
		t.Fatalf("장치 하나를 못 읽어 발행 목록이 줄었다: %+v", devices)
	}
	if pciMissing != 1 {
		t.Errorf("PCI 매핑 실패 수: got %d want 1", pciMissing)
	}
	if got := attrString(t, devices[0].Attributes, "pciBusID"); got != "0000:01:00.0" {
		t.Errorf("읽히는 장치의 pciBusID: %q", got)
	}
	if _, ok := devices[1].Attributes["pciBusID"]; ok {
		t.Error("PCI 주소를 못 찾은 장치에 pciBusID 가 붙었다 — 추측한 값이다")
	}
	if got := attrString(t, devices[1].Attributes, "id"); got != "1" {
		t.Errorf("못 읽은 장치의 id: %q", got)
	}
	assertPublishable(t, devices)

	// host root 가 아예 없어도 발행 목록은 그대로 나온다.
	devices, pciMissing = buildDevices(acmeConfig(), filepath.Join(f.root, "없는경로"), discovered)
	if len(devices) != 2 || pciMissing != 2 {
		t.Fatalf("host root 부재에서 장치 2개·실패 2개여야 한다: %d개, 실패 %d", len(devices), pciMissing)
	}
	assertPublishable(t, devices)
}

// 수용 기준 4: capacity 가 있는 장치와 없는 장치가 한 ResourceSlice 에 섞여도
// 발행이 성립한다. 장치 하나만 용량 파일을 들고 있는 경우다.
func TestBuildDevicesMixesDevicesWithAndWithoutCapacity(t *testing.T) {
	f := newFixture(t)
	withMem := fullPCIAttrs()
	withMem["mem_total_bytes"] = "85899345920\n"
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, withMem)
	f.addDevice("dev/acme/1", "0000:02:00.0", 235, 1, fullPCIAttrs())

	cfg := acmeConfig()
	cfg.CapacityFiles = map[string]string{"memory": "mem_total_bytes"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("설정이 거절됐다: %v", err)
	}

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	devices, pciMissing := buildDevices(cfg, f.root, discovered)
	if pciMissing != 0 {
		t.Fatalf("PCI 매핑이 둘 다 돼야 한다: 실패 %d", pciMissing)
	}
	if len(devices) != 2 {
		t.Fatalf("장치 2개여야 한다: %+v", devices)
	}
	mem, ok := devices[0].Capacity["memory"]
	if !ok {
		t.Fatalf("용량 파일이 있는 장치에 capacity 가 없다: %+v", devices[0].Capacity)
	}
	if got := mem.Value.String(); got != "80Gi" {
		t.Errorf("memory capacity: %q", got)
	}
	if devices[1].Capacity != nil {
		t.Errorf("용량 파일이 없는 장치에 capacity 맵이 남았다: %+v", devices[1].Capacity)
	}
	assertPublishable(t, devices)
}

// 수용 기준 5: descriptor 에 적은 속성은 ResourceSlice 에 나타나고, 같은 이름을
// sysfs 에서도 읽으면 sysfs 쪽을 쓴다(관측이 선언보다 우선한다).
func TestDeclaredAttributesAppearAndSysfsWins(t *testing.T) {
	f := newFixture(t)
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, fullPCIAttrs())

	cfg := acmeConfig()
	cfg.Attributes = map[string]string{
		"productName": "ACME NPU X1",
		"vendorID":    "선언한값", // sysfs 가 같은 이름을 읽는다 — 관측이 이긴다
	}
	cfg.Capacity = map[string]string{"memory": "16Gi"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("설정이 거절됐다: %v", err)
	}

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	attrs, caps, _ := deviceResources(cfg, f.root, discovered[0])
	if got := attrString(t, attrs, "productName"); got != "ACME NPU X1" {
		t.Errorf("선언한 속성이 안 나왔다: %q", got)
	}
	if got := attrString(t, attrs, "vendorID"); got != "0x1f9f" {
		t.Errorf("선언이 관측을 덮었다: vendorID %q", got)
	}
	mem, ok := caps["memory"]
	if !ok || mem.Value.String() != "16Gi" {
		t.Errorf("선언한 capacity 가 안 나왔다: %+v", caps)
	}
}

// 수용 기준 5 뒤쪽: 선언이 없으면 sysfs 에서 읽은 것만 나온다.
func TestWithoutDeclarationsOnlySysfsAttributes(t *testing.T) {
	f := newFixture(t)
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, fullPCIAttrs())

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	attrs, caps, _ := deviceResources(acmeConfig(), f.root, discovered[0])
	if _, ok := attrs["productName"]; ok {
		t.Error("선언하지 않은 속성이 나왔다")
	}
	if len(caps) != 0 {
		t.Errorf("선언하지 않은 capacity 가 나왔다: %+v", caps)
	}
	want := []string{"id", "pciBusID", "pciDeviceID", "vendorID", "numaNode", "pciLinkSpeed", "pciLinkWidth"}
	if len(attrs) != len(want) {
		t.Fatalf("속성 집합이 sysfs 결과와 다르다: %v", attrNames(attrs))
	}
	for _, name := range want {
		if _, ok := attrs[resourceapi.QualifiedName(name)]; !ok {
			t.Errorf("%q 가 없다: %v", name, attrNames(attrs))
		}
	}
}

// 공유 제어 노드로 PCI 를 찾으면 노드의 모든 장치가 같은 PCI 함수로 매핑된다.
// 장치별 노드가 glob 목록 앞에 오는 것에 기대므로 그 순서를 고정한다.
func TestPCIAddressComesFromPerDeviceNode(t *testing.T) {
	f := newFixture(t)
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, fullPCIAttrs())
	f.addDevice("dev/acme/1", "0000:02:00.0", 235, 1, fullPCIAttrs())
	// 공유 제어 노드는 별개 PCI 함수에 달려 있다 — 이것으로 찾으면 둘 다 같은 값이 된다.
	f.addDevice("dev/acmectl", "0000:03:00.0", 10, 60, fullPCIAttrs())

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}", "/dev/acmectl"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	devices, _ := buildDevices(acmeConfig(), f.root, discovered)
	got := []string{
		attrString(t, devices[0].Attributes, "pciBusID"),
		attrString(t, devices[1].Attributes, "pciBusID"),
	}
	if got[0] != "0000:01:00.0" || got[1] != "0000:02:00.0" {
		t.Fatalf("장치별 노드가 아니라 공유 노드로 PCI 를 찾았다: %v", got)
	}
}

func TestValidateDeclarationsRejects(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*Config)
	}{
		{"빈 선언값", func(c *Config) { c.Attributes = map[string]string{"productName": "  "} }},
		{"속성 이름이 C 식별자가 아님", func(c *Config) { c.Attributes = map[string]string{"product-name": "X1"} }},
		{"도메인 접두사가 잘못됨", func(c *Config) { c.Attributes = map[string]string{"NOT_A_DOMAIN/x": "X1"} }},
		{"선언값이 길이 상한을 넘음", func(c *Config) {
			c.Attributes = map[string]string{
				"productName": strings.Repeat("x", resourceapi.DeviceAttributeMaxValueLength+1),
			}
		}},
		{"Quantity 가 아닌 capacity", func(c *Config) { c.Capacity = map[string]string{"memory": "많이"} }},
		{"capacityFiles 절대경로", func(c *Config) { c.CapacityFiles = map[string]string{"memory": "/etc/passwd"} }},
		{"capacityFiles 상위 탈출", func(c *Config) { c.CapacityFiles = map[string]string{"memory": "../../../etc/passwd"} }},
		{"capacityFiles 가 .. 자체", func(c *Config) { c.CapacityFiles = map[string]string{"memory": ".."} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := acmeConfig()
			tc.apply(c)
			if err := c.Validate(); err == nil {
				t.Fatal("잘못된 선언이 통과했다 — 발행 전체가 거절된다")
			}
		})
	}
}

func TestValidateDeclarationsAcceptsDomainPrefixedName(t *testing.T) {
	c := acmeConfig()
	c.Attributes = map[string]string{
		"dra.example.com/productName": "ACME NPU X1",
		// 길이 상한에 딱 맞는 값은 거절하지 않는다.
		"atLimit": strings.Repeat("x", resourceapi.DeviceAttributeMaxValueLength),
	}
	// 점으로 시작하는 파일 이름은 상위 탈출이 아니다.
	c.CapacityFiles = map[string]string{"memory": "..mem_total_bytes"}
	if err := c.Validate(); err != nil {
		t.Fatalf("유효한 선언이 거절됐다: %v", err)
	}
}

// addDeviceWithoutBusEntry 는 /sys/dev/char 링크는 PCI 함수 아래를 가리키는데
// /sys/bus/pci/devices 에는 그 함수가 없는 장치를 만든다 — 커널 나무가 반쪽만 보일 때다.
func (f *fixture) addDeviceWithoutBusEntry(devRel, bdf string, major, minor uint32) {
	f.t.Helper()
	name := filepath.Base(devRel)
	f.mkdir(filepath.Join(f.root, "sys/devices/pci0000:00", bdf, "subsys", name))
	charDir := filepath.Join(f.root, "sys/dev/char")
	f.mkdir(charDir)
	f.symlink(filepath.Join("../../devices/pci0000:00", bdf, "subsys", name),
		filepath.Join(charDir, fmt.Sprintf("%d:%d", major, minor)))
	f.rdev[f.addNodeOnly(devRel)] = [2]uint32{major, minor}
}

// 링크가 올라오지 않으면 커널이 "Unknown" 을 적는다. 그것이 속성으로 붙으면 셀렉터가
// 유효한 값으로 읽는다.
func TestDeviceAttributesSkipKernelUnknown(t *testing.T) {
	f := newFixture(t)
	attrs := fullPCIAttrs()
	attrs["max_link_speed"] = "Unknown\n"
	attrs["max_link_width"] = "Unknown\n"
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, attrs)

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	got, _, _ := deviceResources(acmeConfig(), f.root, discovered[0])
	for _, name := range []string{"pciLinkSpeed", "pciLinkWidth"} {
		if a, ok := got[resourceapi.QualifiedName(name)]; ok {
			t.Errorf("커널이 모른다고 한 %q 가 붙었다: %+v", name, a)
		}
	}
}

// 링크 속성은 능력값(max_link_*)에서 와야 한다. 슬라이스는 한 번만 발행되므로 절전으로
// 내려앉은 current_link_* 를 읽으면 그 값이 영구 광고된다.
func TestLinkAttributesComeFromMaxNotCurrent(t *testing.T) {
	f := newFixture(t)
	attrs := fullPCIAttrs()
	attrs["current_link_speed"] = "2.5 GT/s PCIe\n"
	attrs["current_link_width"] = "1\n"
	f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, attrs)

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	got, _, _ := deviceResources(acmeConfig(), f.root, discovered[0])
	if speed := attrString(t, got, "pciLinkSpeed"); speed != "16.0 GT/s PCIe" {
		t.Errorf("협상된 속도가 광고됐다: %q", speed)
	}
	if width := attrInt(t, got, "pciLinkWidth"); width != 16 {
		t.Errorf("협상된 폭이 광고됐다: %d", width)
	}
}

// PCI 함수가 /sys/bus/pci/devices 에 없으면 속성을 읽을 데가 없다 — 주소만 붙이면
// 셀렉터에 근거 없는 값이 남는다.
func TestNoPCIAttributesWithoutBusEntry(t *testing.T) {
	f := newFixture(t)
	f.addDeviceWithoutBusEntry("dev/acme/0", "0000:01:00.0", 235, 0)

	discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	got, _, pciFound := deviceResources(acmeConfig(), f.root, discovered[0])
	if pciFound {
		t.Error("bus 항목이 없는데 PCI 주소를 찾았다고 했다")
	}
	if _, ok := got["pciBusID"]; ok {
		t.Errorf("bus 항목이 없는데 pciBusID 가 붙었다: %+v", got)
	}
	if len(got) != 1 {
		t.Errorf("id 말고 다른 속성이 붙었다: %v", attrNames(got))
	}
}

// 용량이 0 이거나 수가 아니면 내지 않는다 — 0 을 광고하면 스케줄러가 아무 요청도
// 맞출 수 없는 장치를 들고 있게 된다.
func TestCapacitySkipsZeroAndNonNumeric(t *testing.T) {
	for _, raw := range []string{"0\n", "-1\n", "없음\n"} {
		t.Run(strings.TrimSpace(raw), func(t *testing.T) {
			f := newFixture(t)
			attrs := fullPCIAttrs()
			attrs["mem_total_bytes"] = raw
			f.addDevice("dev/acme/0", "0000:01:00.0", 235, 0, attrs)

			cfg := acmeConfig()
			cfg.CapacityFiles = map[string]string{"memory": "mem_total_bytes"}
			discovered, err := discoverDevices([]string{"/dev/acme/{{id}}"}, f.root)
			if err != nil {
				t.Fatalf("발견 실패: %v", err)
			}
			_, caps, _ := deviceResources(cfg, f.root, discovered[0])
			if caps != nil {
				t.Errorf("쓸 수 없는 용량 %q 가 capacity 로 나갔다: %+v", raw, caps)
			}
		})
	}
}

// putString 은 빈 값·공백만인 값·길이 상한을 넘는 값을 거른다.
func TestPutStringGuards(t *testing.T) {
	m := map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{}
	putString(m, "empty", "")
	putString(m, "blank", "   ")
	putString(m, "tooLong", strings.Repeat("x", resourceapi.DeviceAttributeMaxValueLength+1))
	putString(m, "atLimit", strings.Repeat("x", resourceapi.DeviceAttributeMaxValueLength))
	for _, name := range []string{"empty", "blank", "tooLong"} {
		if a, ok := m[resourceapi.QualifiedName(name)]; ok {
			t.Errorf("%q 가 붙었다: %q", name, *a.StringValue)
		}
	}
	if _, ok := m["atLimit"]; !ok {
		t.Error("길이 상한에 딱 맞는 값이 거절됐다")
	}
}

// 선언이 너무 많으면 sysfs 관측 자리를 합쳐 장치 하나당 상한을 넘는다.
func TestValidateDeclarationsRejectsTooMany(t *testing.T) {
	for _, n := range []int{maxDeclarations, maxDeclarations + 1} {
		c := acmeConfig()
		c.Attributes = map[string]string{}
		for i := 0; i < n; i++ {
			c.Attributes[fmt.Sprintf("attr%d", i)] = "x"
		}
		err := c.Validate()
		if n <= maxDeclarations && err != nil {
			t.Errorf("선언 %d개가 거절됐다: %v", n, err)
		}
		if n > maxDeclarations && err == nil {
			t.Errorf("선언 %d개가 통과했다 — 장치 하나당 상한을 넘는다", n)
		}
	}
}

// operatorDeclarationBudget 은 operator 의 AcceleratorDescriptor 가 받아 주는 선언 수다
// (api/v1alpha1 의 attributes·capacity·capacityFiles 각 MaxProperties=8, 8×3). 복사한 값이라
// operator 쪽 변경은 이 시험이 못 본다 — 반대 방향은 operator 의
// internal/descriptor/validate_test.go 가 생성된 CRD 의 maxProperties=8 을 어서션해 맡는다.
// 이 수를 바꾸려면 두 시험을 함께 고쳐야 한다.
const operatorDeclarationBudget = 8 * 3

// 관측 자리를 늘려 선언 예산이 operator 가 받아 주는 수보다 작아지면 여기서 걸린다.
func TestMaxDeclarationsCoversOperatorBudget(t *testing.T) {
	if maxDeclarations < operatorDeclarationBudget {
		t.Fatalf("선언 예산이 %d 로 줄어 operator 가 받아 주는 %d 를 못 담는다"+
			" (DRA 상한 %d, id·pciBusID 2, pciSysfsAttrs %d) —"+
			" 관측 자리를 줄이거나 operator 의 MaxProperties 를 함께 낮춰야 한다",
			maxDeclarations, operatorDeclarationBudget,
			resourceapi.ResourceSliceMaxAttributesAndCapacitiesPerDevice, len(pciSysfsAttrs))
	}
}

// 도메인 접두사 상한은 DNS subdomain(253자)이 아니라 DRA 상한(63자)이다.
// 넘겨 보내면 API 서버가 ResourceSlice 전체를 거절한다.
func TestValidateDeclarationsDomainLength(t *testing.T) {
	for _, n := range []int{resourceapi.DeviceMaxDomainLength, resourceapi.DeviceMaxDomainLength + 1} {
		// "a...a.com" 꼴로 정확히 n 자를 만든다.
		domain := strings.Repeat("a", n-4) + ".com"
		if len(domain) != n {
			t.Fatalf("도메인 길이를 %d 로 만들지 못했다: %d", n, len(domain))
		}
		c := acmeConfig()
		c.Attributes = map[string]string{domain + "/productName": "ACME NPU X1"}
		err := c.Validate()
		if n <= resourceapi.DeviceMaxDomainLength && err != nil {
			t.Errorf("%d자 도메인이 거절됐다: %v", n, err)
		}
		if n > resourceapi.DeviceMaxDomainLength && err == nil {
			t.Errorf("%d자 도메인이 통과했다 — 발행 전체가 거절된다", n)
		}
	}
}

// devMajorMinor 의 실물 경로를 돈다. 시험 대체 없이 호스트의 char 장치를 stat 한다 —
// major:minor 해독이 틀리면 /sys/dev/char 조회가 다른 장치를 가리킨다.
func TestDevMajorMinorReadsRealCharDevices(t *testing.T) {
	for path, want := range map[string][2]uint32{
		"/dev/null":    {1, 3},
		"/dev/zero":    {1, 5},
		"/dev/urandom": {1, 9},
	} {
		major, minor, ok := devMajorMinor(path)
		if !ok {
			t.Errorf("%s 를 읽지 못했다", path)
			continue
		}
		if major != want[0] || minor != want[1] {
			t.Errorf("%s: got %d:%d want %d:%d", path, major, minor, want[0], want[1])
		}
	}

	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{regular, dir, filepath.Join(dir, "없는파일")} {
		if major, minor, ok := devMajorMinor(path); ok {
			t.Errorf("%s 가 char 장치로 잡혔다: %d:%d", path, major, minor)
		}
	}
}
