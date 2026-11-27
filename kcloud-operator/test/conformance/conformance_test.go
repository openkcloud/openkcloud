// ============================================================
// conformance_test.go: 벤더 온보딩 계약 1~7·10번 적합성 시험(8·9번은 internal/controller, README.md 참조)
// 상세: 능력 모델·어댑터·자원 이름 사상·이미지 매니페스트·Helm 기본값·CRD enum, 여섯 표층이
// 한 벤더에 대해 같은 이야기를 하는지 본다. 미지원은 정상 상태다 — "답은 했는가" 만 본다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
// ============================================================
package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"kcloud-operator/internal/driveradapter"
	"kcloud-operator/internal/intent"
	"kcloud-operator/internal/vendorcap"
	"kcloud-operator/internal/verifyprobe"
)

// repoRoot 는 이 패키지(test/conformance)에서 저장소 루트까지의 상대 경로다.
const repoRoot = "../.."

// capKey 는 (vendorcap.Key, Capability) 쌍이다 — Unknown 예외 목록의 키로 쓴다.
type capKey struct {
	Key vendorcap.Key
	Cap vendorcap.Capability
}

// unknownExceptions 는 State 가 Unknown 인 채로 남아 있어도 계약 2번이 실패로 보지 않는
// (key, capability) 목록이다. Reason 이 비어 있지 않은지는 registry_test.go 의
// TestEveryEntryIsHonest 가 따로 지킨다 — 여기서는 "왜 아직 미확정인지" 사유를 적어 둔다.
// 목록에 없는 Unknown 이 새로 생기면 TestEveryVendorSatisfiesConformance 가 그 벤더·능력을
// 지목해 실패하므로, 조사해 상태를 확정하거나 사유와 함께 이 목록에 등재해야 한다. 지금은
// 비어 있다 — 마지막 항목(rebellions/CapIsolation)이 Unsupported+AssuranceHardware 로 확정돼 있다.
var unknownExceptions = map[capKey]string{}

// subtestName 은 t.Run 이름이다. product 가 빈 벤더(nvidia·rebellions·tenstorrent)도
// 읽히도록 벤더 이름만 쓰고, product 가 있으면(furiosa) "/" 로 붙인다.
func subtestName(k vendorcap.Key) string {
	if k.Product == "" {
		return k.Vendor
	}
	return k.Vendor + "/" + k.Product
}

// vendorSet 은 vendorcap.Keys() 에서 뽑은 유일한 벤더 이름 집합이다(정렬됨). furiosa 처럼
// product 가 둘인 벤더(rngd·warboy)는 한 번만 센다 — CRD enum·image-manifest·Helm values
// 는 product 가 아니라 vendor 단위로 조직돼 있어 계약 5~7 번은 이 집합을 쓴다. 키 다섯을
// 그대로 비교하면 furiosa 가 둘로 겹쳐 세어져 첫 실행에서 어느 CRD enum 과도 못 맞는다.
func vendorSet() []string {
	keys := vendorcap.Keys()
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if seen[k.Vendor] {
			continue
		}
		seen[k.Vendor] = true
		out = append(out, k.Vendor)
	}
	sort.Strings(out)
	return out
}

// TestEveryVendorSatisfiesConformance 는 계약 1~4번이다: 능력 모델에 15종 전부가 있고
// 정직성 규칙(Native·Software 는 Source, Unsupported 는 Reason, Unknown 은 예외 목록에
// 등재)을 지키며, DriverAdapter 가 있고 자기 키를 정확히 돌려주며, devicePlugin 을
// 지원한다고 선언한 벤더에만 광고 자원 이름이 있는지 본다.
func TestEveryVendorSatisfiesConformance(t *testing.T) {
	for _, k := range vendorcap.Keys() {
		t.Run(subtestName(k), func(t *testing.T) {
			for _, c := range vendorcap.Capabilities() {
				s, ok := vendorcap.Lookup(k.Vendor, k.Product, c)
				if !ok {
					t.Errorf("능력 %s 항목이 없다 — 모든 키×능력은 명시돼야 한다", c)
					continue
				}
				switch s.State {
				case vendorcap.StateNative, vendorcap.StateSoftware:
					if strings.TrimSpace(s.Source) == "" {
						t.Errorf("%s(%s) 에 근거(Source)가 없다", c, s.State)
					}
				case vendorcap.StateUnsupported:
					if strings.TrimSpace(s.Reason) == "" {
						t.Errorf("%s(Unsupported) 에 사유(Reason)가 없다", c)
					}
				case vendorcap.StateUnknown:
					if _, excepted := unknownExceptions[capKey{Key: k, Cap: c}]; !excepted {
						t.Errorf("%s(Unknown) 이 unknownExceptions 목록에 없다 — 조사해 상태를 "+
							"확정하거나 사유와 함께 등재해야 한다", c)
					}
				default:
					t.Errorf("%s: 알 수 없는 State %q", c, s.State)
				}
			}

			a, ok := driveradapter.Lookup(k.Vendor, k.Product)
			if !ok {
				t.Fatalf("DriverAdapter 가 없다")
			}
			if a.Key() != k {
				t.Errorf("어댑터 키 = %v, want %v", a.Key(), k)
			}

			dp, ok := vendorcap.Lookup(k.Vendor, k.Product, vendorcap.CapDevicePlugin)
			if !ok {
				t.Fatalf("devicePlugin 능력 항목이 없다")
			}
			name, err := intent.WholeDeviceResource(k.Vendor, k.Product)
			if err != nil {
				t.Fatalf("WholeDeviceResource(%s,%s): %v", k.Vendor, k.Product, err)
			}
			advertises := dp.State == vendorcap.StateNative || dp.State == vendorcap.StateSoftware
			if advertises && name == "" {
				t.Errorf("devicePlugin 을 지원한다는데 광고 자원 이름이 없다")
			}
			if !advertises && name != "" {
				t.Errorf("devicePlugin 미지원인데 광고 자원 이름 %q 가 있다", name)
			}
		})
	}
}

// manifestImage 는 image-manifest.yaml 의 kcloud:/vendor: 절 원소 중 이 시험이 보는
// 필드만 담는다.
type manifestImage struct {
	Image     string `json:"image"`
	Condition string `json:"condition"`
}

// imageManifest 는 deploy/integration/image-manifest.yaml 의 관심 필드만 담는다.
type imageManifest struct {
	Kcloud          []manifestImage `json:"kcloud"`
	Vendor          []manifestImage `json:"vendor"`
	AptRepositories []struct {
		Vendor string `json:"vendor"`
	} `json:"aptRepositories"`
}

// vendorToken 은 문자열을 `-`·`/`·`_`·`:`·`.`·공백·괄호·쉼표 경계로 나눠 벤더 이름과 같은
// 토큰이 있는지 본다 — "probe-rebellions"·"rebellions/rbln-workload"·"Tenstorrent 노드" 는
// 맞고 "xrebellionsy" 는 아니다.
func vendorToken(s, vendor string) bool {
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		switch r {
		case '-', '/', '_', ':', '.', ' ', '(', ')', ',', '·':
			return true
		}
		return false
	}) {
		if tok == vendor {
			return true
		}
	}
	return false
}

// manifestListsVendor 는 image·condition·aptRepositories.vendor 어디든 그 벤더 이름이
// 토큰으로 등장하는지 본다. 벤더마다 등재 방식이 달라 한 필드만 보면 놓친다 —
// rebellions 는 이미지 이름과 condition 에, tenstorrent 는 condition 문구에만 나오는 항목이 있다.
func manifestListsVendor(m imageManifest, vendor string) bool {
	v := strings.ToLower(vendor)
	images := append(append([]manifestImage{}, m.Kcloud...), m.Vendor...)
	for _, img := range images {
		if vendorToken(img.Image, v) || vendorToken(img.Condition, v) {
			return true
		}
	}
	for _, apt := range m.AptRepositories {
		if vendorToken(apt.Vendor, v) {
			return true
		}
	}
	return false
}

// TestManifestListsVendorRequiresWholeToken 은 벤더 이름이 낱말 경계 안의 토큰으로 있을 때만
// 등재로 보는지 본다 — 다른 낱말 속 부분 문자열은 등재가 아니다.
func TestManifestListsVendorRequiresWholeToken(t *testing.T) {
	m := imageManifest{Kcloud: []manifestImage{
		{Image: "probe-rebellions"},
		{Image: "x-nvidiay", Condition: "NVIDIAX 노드"},
		{Image: "kcloud-tt-device-plugin", Condition: "Tenstorrent 노드"},
	}}
	if !manifestListsVendor(m, "rebellions") {
		t.Error("probe-rebellions 는 rebellions 토큰을 담는다")
	}
	if !manifestListsVendor(m, "tenstorrent") {
		t.Error("condition 의 Tenstorrent 는 대소문자 무시 토큰이다")
	}
	if manifestListsVendor(m, "nvidia") {
		t.Error("x-nvidiay·NVIDIAX 는 nvidia 토큰이 아니다 — 부분일치를 허용하면 안 된다")
	}
	m2 := imageManifest{AptRepositories: []struct {
		Vendor string `json:"vendor"`
	}{{Vendor: "furiosa-warboy"}}}
	if !manifestListsVendor(m2, "furiosa") {
		t.Error("furiosa-warboy 는 furiosa 토큰을 담는다")
	}
}

// TestEveryVendorListedInImageManifest 는 계약 5번이다: 등록된 모든 벤더가
// deploy/integration/image-manifest.yaml 에 등재돼 있어야 한다 — 없으면 air-gap 미러가 그
// 벤더 이미지를 조회할 방법이 없다.
func TestEveryVendorListedInImageManifest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "deploy/integration/image-manifest.yaml"))
	if err != nil {
		t.Fatalf("image-manifest.yaml 읽기 실패: %v", err)
	}
	var m imageManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("image-manifest.yaml 파싱 실패: %v", err)
	}
	for _, vendor := range vendorSet() {
		if !manifestListsVendor(m, vendor) {
			t.Errorf("%s 이미지가 image-manifest.yaml 에 없다", vendor)
		}
	}
}

// topLevelKeyRe 는 values.yaml 최상위 키 줄(들여쓰기 없이 문자로 시작)을 고른다 — 벤더
// 블록의 끝을 찾는 데 쓴다.
var topLevelKeyRe = regexp.MustCompile(`^[A-Za-z]`)

// vendorBlockHasDocumentedDefault 는 values.yaml 텍스트에서 그 벤더의 최상위 블록을 찾아
// enabled: 필드가 있고 그 바로 다음 줄이 주석인지 본다. YAML 라이브러리는 주석을 버리므로
// 여기선 줄 단위 텍스트로 본다 — 이 파일의 네 벤더 블록이 실제로 이 모양이다(벤더 키 다음
// 줄이 "enabled: true", 그다음 줄이 그 값을 설명하는 주석).
func vendorBlockHasDocumentedDefault(lines []string, vendor string) bool {
	start := -1
	for i, line := range lines {
		if line == vendor+":" {
			start = i
			break
		}
	}
	if start == -1 {
		return false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if topLevelKeyRe.MatchString(lines[i]) {
			end = i
			break
		}
	}
	for i := start + 1; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "enabled: true" || trimmed == "enabled: false" {
			return i+1 < end && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "#")
		}
	}
	return false
}

// TestEveryVendorHasDocumentedHelmDefault 는 계약 6번이다: 등록된 모든 벤더가
// deploy/helm/values.yaml 에 최상위 블록을 갖고, enabled 기본값 바로 다음 줄에 그 값을
// 설명하는 주석이 있어야 한다.
func TestEveryVendorHasDocumentedHelmDefault(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "deploy/helm/values.yaml"))
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	for _, vendor := range vendorSet() {
		if !vendorBlockHasDocumentedDefault(lines, vendor) {
			t.Errorf("%s 블록에 enabled 기본값과 이유 주석이 없다(deploy/helm/values.yaml)", vendor)
		}
	}
}

// walkPath 는 파싱된 YAML(map[string]any) 안에서 "a.b.c" 꼴 경로를 따라 내려간다. 배열
// 인덱싱은 하지 않는다 — spec.versions 배열은 호출부(readCRDVendorEnum)가 따로 다룬다.
func walkPath(doc map[string]any, path string) (any, error) {
	var cur any = doc
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q 에서 map 이 아니다(%T)", key, cur)
		}
		v, ok := m[key]
		if !ok {
			return nil, fmt.Errorf("키 %q 가 없다", key)
		}
		cur = v
	}
	return cur, nil
}

// readCRDVendorEnum 은 config/crd/bases/<file> 을 읽어 spec.versions[0] 아래 path 가
// 가리키는 enum 목록을 돌려준다. CRD 4종이 vendor 필드 위치만 다르므로(3종은 spec 바로
// 아래, AcceleratorClass 는 spec.mappings 배열 원소 아래) 구조체를 따로 선언하는 대신 이
// 경로 문자열 하나로 공통 처리한다.
func readCRDVendorEnum(file, path string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "config/crd/bases", file))
	if err != nil {
		return nil, fmt.Errorf("%s 읽기 실패: %w", file, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s 파싱 실패: %w", file, err)
	}
	v, err := walkPath(doc, "spec.versions")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	versions, ok := v.([]any)
	if !ok || len(versions) == 0 {
		return nil, fmt.Errorf("%s: spec.versions 가 비어 있거나 배열이 아니다(%T)", file, v)
	}
	version0, ok := versions[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: spec.versions[0] 이 map 이 아니다(%T)", file, versions[0])
	}
	v, err = walkPath(version0, path)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", file, path, err)
	}
	enum, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s: enum 이 배열이 아니다(%T)", file, path, v)
	}
	out := make([]string, 0, len(enum))
	for _, e := range enum {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s: enum 항목이 문자열이 아니다(%T)", file, path, e)
		}
		out = append(out, s)
	}
	return out, nil
}

// crdEnumFiles 는 벤더 enum 을 각자 독립적으로 선언한 CRD 4종이다. `grep -rn 'validation:Enum'
// api/v1alpha1/*.go` 로 확인할 수 있다 — 다섯째 벤더가 생기면 이 grep 을 다시 돌려 새 CRD 가
// 늘었는지부터 확인한다.
var crdEnumFiles = []struct {
	file string
	path string
}{
	{"npu.ai_acceleratorevidences.yaml", "schema.openAPIV3Schema.properties.spec.properties.vendor.enum"},
	{"npu.ai_acceleratorpartitionpolicies.yaml", "schema.openAPIV3Schema.properties.spec.properties.vendor.enum"},
	{"npu.ai_acceleratoroperations.yaml", "schema.openAPIV3Schema.properties.spec.properties.vendor.enum"},
	{
		"npu.ai_acceleratorclasses.yaml",
		"schema.openAPIV3Schema.properties.spec.properties.mappings.items.properties.vendor.enum",
	},
}

// TestVendorEnumsAgreeAcrossCRDsAndRegistry 는 계약 7번이다: CRD 4종이 각자 독립적으로
// 선언한 Vendor enum(config/crd/bases — API 서버가 실제로 강제하는 값)이 vendorcap
// 레지스트리의 벤더 집합과 같아야 한다. 하나라도 어긋나면 벤더를 추가할 때 네 곳 중 하나를
// 빠뜨렸다는 뜻이다 — 컴파일러는 이 어긋남을 잡지 못한다.
func TestVendorEnumsAgreeAcrossCRDsAndRegistry(t *testing.T) {
	want := vendorSet()
	for _, src := range crdEnumFiles {
		got, err := readCRDVendorEnum(src.file, src.path)
		if err != nil {
			t.Fatalf("%v", err)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s(%s) enum=%v, vendorcap 등록 벤더=%v — 서로 다르다", src.file, src.path, got, want)
		}
	}
}

// TestEveryVendorResourceNameMatchesAnAcceleratorDomain 은 계약 10번이다: 등록된 모든
// 벤더의 WholeDeviceResource 자원명이 intent.AcceleratorResourceDomains 의 도메인 중
// 최소 하나와 겹쳐야 한다. 드라이버 업그레이드 drain 과 uninstall 게이트는 "이 리소스가
// 가속기인가" 를 이 도메인 집합 하나로 묻는다 — 도메인이 빠지면 두 판정이 그 벤더를 조용히
// 놓친다. AcceleratorResourceDomains 는 productResource 에서 뽑혀 평소엔 항상 참이지만,
// 도메인 추출 로직이 깨지는 변화를 잡기 위해 이 시험을 둔다.
func TestEveryVendorResourceNameMatchesAnAcceleratorDomain(t *testing.T) {
	domains := intent.AcceleratorResourceDomains()
	for _, k := range vendorcap.Keys() {
		t.Run(subtestName(k), func(t *testing.T) {
			name, err := intent.WholeDeviceResource(k.Vendor, k.Product)
			if err != nil {
				t.Fatalf("WholeDeviceResource(%s,%s): %v", k.Vendor, k.Product, err)
			}
			if name == "" {
				return // devicePlugin 미지원 벤더는 광고 자원 이름이 없다(계약 4 참조)
			}
			for _, d := range domains {
				if strings.Contains(name, d) {
					return
				}
			}
			t.Errorf("%s 의 자원명 %q 가 AcceleratorResourceDomains %v 의 어느 도메인과도 안 겹친다", subtestName(k), name, domains)
		})
	}
}

// TestPresentLabelSingleSource 는 벤더 존재 라벨 문자열이 api/v1alpha1.PresentNodeLabel 밖에서
// 리터럴이나 문자열 연결로 다시 조립되지 않는지 본다(주석 줄과 _test.go 는 제외).
func TestPresentLabelSingleSource(t *testing.T) {
	re := regexp.MustCompile(
		`"kcloud\.ai/(nvidia|furiosa|rngd|rebellions|tenstorrent)\.present"` +
			`|"kcloud\.ai/"\s*\+\s*[\w.]+\s*\+\s*"\.present"`,
	)
	var offenders []string
	for _, dir := range []string{"internal", "pkg", "cmd"} {
		_ = filepath.Walk(filepath.Join(repoRoot, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if re.MatchString(line) {
					offenders = append(offenders, fmt.Sprintf("%s:%d", path, i+1))
				}
			}
			return nil
		})
	}
	if len(offenders) > 0 {
		t.Errorf("present 라벨 리터럴이 남아 있다(api/v1alpha1.PresentNodeLabel 을 쓸 것):\n%s", strings.Join(offenders, "\n"))
	}
}

// TestEveryVendorHasVerifyProbe 는 등록된 벤더마다 검증 probe 명령이 있는지 본다 —
// 없으면 파티션·공유 적용 뒤 "장치가 보이는가" 를 물을 수단이 없다(rngd 는 furiosa 표를 쓴다).
func TestEveryVendorHasVerifyProbe(t *testing.T) {
	for _, vendor := range vendorSet() {
		if _, ok := verifyprobe.Lookup(vendor); !ok {
			t.Errorf("%s 에 검증 probe 명령이 없다(internal/verifyprobe)", vendor)
		}
	}
}
