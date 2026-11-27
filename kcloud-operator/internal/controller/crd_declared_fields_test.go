// ============================================================
// crd_declared_fields_test.go: 렌더된 CR 과 샘플이 CRD 에 선언된 필드만 쓰는지 고정한다
// 상세: 게이트 전부가 초록이어도 기본 설치가 API 서버에서 거절되는 구멍을 막는다. CRD 에서
// 필드를 지우고 values.schema.json 이나 샘플 동기화를 빠뜨리면 렌더는 성공하지만 그 CR 이
// 미선언 필드를 싣고, kubectl 1.25+ 의 strict 서버 측 필드 검증이 그 매니페스트를 거절해
// 설치가 깨진다. 다른 어떤 게이트도 렌더된 CR 을 CRD 스키마와 대조하지 않는다.
// 생성일: 2026-10-05 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// crdAPIVersion 은 이 저장소가 소유한 CR 의 apiVersion 이다. 렌더 출력에는 상위 자원도
// 섞여 있으므로(Deployment·DaemonSet, crds/ 의 CustomResourceDefinition 자신) 이 값으로
// 우리 CR 만 골라낸다.
const crdAPIVersion = "npu.ai/v1alpha1"

// crdSchemas 는 config/crd/bases/*.yaml 에서 kind → v1alpha1 openAPIV3Schema 를 읽는다.
// 생성물을 권위로 삼는 것이 핵심이다 — Go 타입이 아니라 API 서버가 실제로 받는 스키마와
// 대조해야 "지웠는데 값 공급 자리가 남았다" 를 잡는다.
func crdSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	files, err := filepath.Glob("../../config/crd/bases/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("config/crd/bases/*.yaml 를 찾지 못했다: %v", err)
	}
	out := map[string]map[string]any{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", f, err)
		}
		var crd struct {
			Spec struct {
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
				Versions []struct {
					Name   string `json:"name"`
					Schema struct {
						OpenAPIV3Schema map[string]any `json:"openAPIV3Schema"`
					} `json:"schema"`
				} `json:"versions"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s 파싱 실패: %v", f, err)
		}
		for _, v := range crd.Spec.Versions {
			if v.Name == "v1alpha1" {
				out[crd.Spec.Names.Kind] = v.Schema.OpenAPIV3Schema
			}
		}
	}
	return out
}

// undeclaredFields 는 obj 의 필드를 schema 와 대조해 선언되지 않은 필드 경로를 모은다.
func undeclaredFields(path string, obj any, schema map[string]any, out *[]string) {
	if schema == nil {
		// 부모가 하위 스키마를 주지 않는 경우다 — 그 자리는 부모 쪽에서 이미 보고한다.
		return
	}
	// CRD 가 임의 키를 허용한다고 스스로 선언한 자리는 대조하지 않는다. 지금 생성물에는
	// 이 표시가 0건이지만, 나중에 생기면 거짓 양성이 나므로 미리 비켜 둔다.
	if preserve, ok := schema["x-kubernetes-preserve-unknown-fields"].(bool); ok && preserve {
		return
	}
	switch v := obj.(type) {
	case map[string]any:
		// map 타입(additionalProperties)은 키가 사이트 값이다 — nodeSelector 의 노드 라벨,
		// kernelImages 의 커널 릴리스 문자열처럼 차트가 미리 적을 수 없는 자리다.
		// 키는 통과시키고 값만 따라 내려간다.
		if ap, ok := schema["additionalProperties"]; ok {
			apSchema, _ := ap.(map[string]any)
			for k, val := range v {
				undeclaredFields(path+"."+k, val, apSchema, out)
			}
			return
		}
		props, _ := schema["properties"].(map[string]any)
		for k, val := range v {
			sub, ok := props[k].(map[string]any)
			if !ok {
				*out = append(*out, path+"."+k)
				continue
			}
			undeclaredFields(path+"."+k, val, sub, out)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for i, el := range v {
			undeclaredFields(fmt.Sprintf("%s[%d]", path, i), el, items, out)
		}
	}
}

// crDocs 는 여러 자원이 담긴 YAML 묶음에서 이 저장소 소유 CR 만 골라 돌려준다.
func crDocs(t *testing.T, src, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	r := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(body)))
	for {
		doc, err := r.Read()
		if err != nil {
			break
		}
		if len(strings.TrimSpace(string(doc))) == 0 {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal(doc, &obj); err != nil {
			t.Fatalf("%s: 문서 파싱 실패: %v\n%s", src, err, doc)
		}
		if obj["apiVersion"] == crdAPIVersion {
			out = append(out, obj)
		}
	}
	return out
}

// checkCR 은 CR 하나를 자기 kind 의 CRD 스키마와 대조해 미선언 필드를 돌려준다.
// metadata 는 빼고 본다 — CRD 는 그 안을 선언하지 않고(생성물이 properties 없는 object 로
// 둔다) API 서버가 자기 ObjectMeta 스키마로 검증한다. 여기서 보면 전부 거짓 양성이 된다.
func checkCR(t *testing.T, src string, cr map[string]any, schemas map[string]map[string]any) []string {
	t.Helper()
	kind, _ := cr["kind"].(string)
	schema, ok := schemas[kind]
	if !ok {
		t.Errorf("%s: kind=%q 의 CRD 가 config/crd/bases 에 없다", src, kind)
		return nil
	}
	trimmed := map[string]any{}
	for k, v := range cr {
		if k != "metadata" {
			trimmed[k] = v
		}
	}
	name, _ := cr["metadata"].(map[string]any)
	var found []string
	undeclaredFields(fmt.Sprintf("%s/%v", kind, name["name"]), trimmed, schema, &found)
	return found
}

// chartPresets 는 기본값과 values-*.yaml 프리셋 전부를 helm template 인자로 돌려준다.
// 글롭이라 프리셋이 새로 생기면 시험을 고치지 않아도 대조 범위에 들어온다.
func chartPresets(t *testing.T) [][]string {
	t.Helper()
	presets := [][]string{{}}
	files, err := filepath.Glob("../../deploy/helm/values-*.yaml")
	if err != nil {
		t.Fatalf("프리셋 글롭 실패: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		presets = append(presets, []string{"-f", f})
	}
	return presets
}

// 차트가 렌더하는 CR 전부가 생성된 CRD 에 선언된 필드만 써야 한다. 하나라도 어긋나면
// 그 설치는 kubectl 1.25+ 에서 거절된다 — 렌더가 성공했다는 사실이 설치 가능을 뜻하지
// 않는다. values.schema.json 은 키를 받고 CRD 는 거절하는 어긋남이 바로 이 구멍이다.
//
// os/exec 로 helm 을 부르므로 Go 테스트 캐시가 그 의존을 못 본다 — 손으로 돌릴 때는
// `-count=1` 을 붙일 것. helm 부재 판정은 helm_presence_test.go 의 helmBinary 하나다.
func TestRenderedCustomResourcesUseOnlyDeclaredFields(t *testing.T) {
	schemas := crdSchemas(t)
	for _, args := range chartPresets(t) {
		name := "기본값"
		if len(args) == 2 {
			name = filepath.Base(args[1])
		}
		t.Run(name, func(t *testing.T) {
			var checked int
			for _, cr := range crDocs(t, name, helmTemplate(t, args...)) {
				checked++
				for _, f := range checkCR(t, name, cr, schemas) {
					t.Errorf("%s: CRD 미선언 필드 %s — 이 매니페스트는 strict 필드 검증에서 거절된다", name, f)
				}
			}
			if checked == 0 {
				t.Fatalf("%s: %s CR 이 0개다 — 대조가 비었으면 통과가 아니다", name, crdAPIVersion)
			}
			t.Logf("%s: CR %d개 대조", name, checked)
		})
	}
}

// config/samples 의 CR 도 같은 대조를 받는다. 샘플은 사람이 그대로 적용하는 경로라
// 거절되는 샘플은 차트 결함과 같은 값이다. toolkit.version 은 살아 있는 driver.version 과
// 키 이름이 같아 단순 grep 으로는 누락을 구별할 수 없다 — 이 구조 대조만 잡는다.
func TestSampleCustomResourcesUseOnlyDeclaredFields(t *testing.T) {
	schemas := crdSchemas(t)
	files, err := filepath.Glob("../../config/samples/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("config/samples/*.yaml 를 찾지 못했다: %v", err)
	}
	var checked int
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", f, err)
		}
		src := filepath.Base(f)
		for _, cr := range crDocs(t, src, string(raw)) {
			checked++
			for _, uf := range checkCR(t, src, cr, schemas) {
				t.Errorf("%s: CRD 미선언 필드 %s — 이 샘플을 적용하면 거절된다", src, uf)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("config/samples 에 %s CR 이 0개다 — 대조가 비었으면 통과가 아니다", crdAPIVersion)
	}
	t.Logf("config/samples: CR %d개 대조", checked)
}
