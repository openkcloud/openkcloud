// ============================================================
// main.go: descriptor 배포물 발행과 backend 대조를 하는 명령줄 도구
// 상세: 컨트롤러가 없는 동안 라이브 재현이 지나갈 유일한 발행 경로다. 손으로 쓴
// YAML 을 쓰지 않으려고 존재한다.
// 생성일: 2026-08-10
// ============================================================
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/controller"
	"kcloud-operator/internal/descriptor"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "사용법: backendtool emit|compare [옵션]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "emit":
		os.Exit(runEmit(os.Args[2:]))
	case "compare":
		os.Exit(runCompare(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "모르는 명령: %s\n", os.Args[1])
		os.Exit(2)
	}
}

func runEmit(args []string) int {
	fs := flag.NewFlagSet("emit", flag.ExitOnError)
	path := fs.String("descriptor", "", "AcceleratorDescriptor YAML 경로")
	ns := fs.String("namespace", "kcloud", "배포물 네임스페이스")
	dpImage := fs.String("dp-image", "", "device-plugin backend 런타임 이미지")
	draImage := fs.String("dra-image", "", "DRA backend 런타임 이미지")
	only := fs.String("backend", "", "devicePlugin 또는 dra 하나만 발행(빈 값이면 전부)")
	_ = fs.Parse(args)

	if *path == "" {
		fmt.Fprintln(os.Stderr, "-descriptor 는 필수다")
		return 2
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var desc npuv1alpha1.AcceleratorDescriptor
	if err := yaml.Unmarshal(raw, &desc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// 선언하지 않은 backend 를 CLI 가 만들어 내면 "descriptor 가 단일 출처" 라는
	// 주장이 도구 자신에 의해 깨진다. 좁히기만 하고 넓히지 않는다.
	if *only != "" {
		declared := false
		for _, b := range desc.Spec.Backends {
			if b == *only {
				declared = true
			}
		}
		if !declared {
			fmt.Fprintf(os.Stderr,
				"descriptor 가 %q 를 선언하지 않았다 — 선언한 것: %v\n", *only, desc.Spec.Backends)
			return 2
		}
		desc.Spec.Backends = []string{*only}
	}
	objs, err := controller.RenderGeneratedBackends(
		&desc, *dpImage, *draImage, *ns)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, o := range objs {
		b, err := sigsyaml.Marshal(o)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("---\n%s", b)
	}
	return 0
}

func runCompare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	aPath := fs.String("a", "", "관측치 A JSON 경로")
	bPath := fs.String("b", "", "관측치 B JSON 경로")
	asJSON := fs.Bool("json", false, "마크다운 대신 JSON 출력")
	_ = fs.Parse(args)

	a, err := readObservation(*aPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	b, err := readObservation(*bPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := descriptor.Compare(a, b)
	if *asJSON {
		out, _ := json.Marshal(c)
		fmt.Println(string(out))
	} else {
		fmt.Print(c.MarkdownTable())
	}
	// 불일치를 종료 코드로 낸다. 스크립트가 눈으로 표를 읽지 않아도 되게.
	if !c.AllMatch {
		return 1
	}
	return 0
}

func readObservation(path string) (descriptor.BackendObservation, error) {
	var o descriptor.BackendObservation
	if path == "" {
		return o, fmt.Errorf("관측치 경로가 없다")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return o, err
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, err
	}
	// 빈 관측을 통과시키면 "관측 못 함" 이 "같음" 으로 둔갑한다.
	if len(o.DeviceNodes) == 0 {
		return o, fmt.Errorf("%s: 장치 노드 템플릿이 비었다 — 관측 실패로 본다", path)
	}
	return o, nil
}
