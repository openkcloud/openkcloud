// ============================================================
// judgment_duplication_test.go: 장치 점유 판정 중복 방지 고정 시험
// 상세: podUsesDevice 를 장치 점유 판정의 유일한 함수로 고정하는 회귀 시험이다.
//       go/ast 로 비-테스트 소스를 파싱해 같은 판정 표지를 본문에 가진 함수가 더 있는지 본다.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================

package upgrade

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// judgmentFuncAllowlist 는 장치 점유 판정의 표지를 본문에 가져도 되는 유일한
// 패키지 수준 함수 이름이다. hasDeviceWorkloads·evictDevicePods 는 이 함수를 호출만
// 하므로 표지를 갖지 않는다 — 다른 함수가 판정을 다시 적으면 이 시험이 잡는다.
// 메서드는 이름이 같아도 허용하지 않는다(아래 fn.Recv 검사).
const judgmentFuncAllowlist = "podUsesDevice"

// TestPodDeviceJudgmentFuncIsNotDuplicated 는 internal/upgrade 안에서 podUsesDevice
// 외의 함수가 같은 판정을 본문에 복제하면 실패한다.
func TestPodDeviceJudgmentFuncIsNotDuplicated(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("패키지 디렉터리 훑기 실패: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("%s 파싱 실패: %v", name, perr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// receiver 가 있으면 허용 목록을 거치지 않는다 — 메서드는 전부 적발 대상이다.
			if fn.Recv == nil && fn.Name.Name == judgmentFuncAllowlist {
				continue
			}
			if hasDeviceJudgmentMarkers(fn.Body) {
				kind := "func"
				if fn.Recv != nil {
					kind = "method"
				}
				offenders = append(offenders, name+":"+kind+" "+fn.Name.Name)
			}
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("판정이 복제된 함수(허용 목록은 %s 뿐이다): %v",
			judgmentFuncAllowlist, offenders)
	}
}

// hasDeviceJudgmentMarkers 는 body 가 판정의 표지(ResourceClaims 참조 또는
// hasAcceleratorResourceName 호출)를 하나라도 담고 있는지 본다. OR 로 묶는다 —
// AND 로 묶으면 limits 축만 다시 적은 복제를 못 잡는다. hasAcceleratorResourceName
// 호출은 비-테스트 코드에 podUsesDevice 하나뿐이라 CPU·메모리 limits 만 읽는
// 함수는 걸리지 않는다.
func hasDeviceJudgmentMarkers(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if x.Sel.Name == "ResourceClaims" {
				found = true
			}
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "hasAcceleratorResourceName" {
				found = true
			}
		}
		return true
	})
	return found
}
