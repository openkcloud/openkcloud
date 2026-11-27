// parity_test.go: CLI·REST 두 진입점이 pkg/npuctl 참조를 유지하는지 고정
// 상세: 같은 물음을 두 곳에서 각자 구현하면 한쪽만 고쳐져 답이 갈린다. cmd/kubectl-npu·
// internal/apiserver 각각에 pkg/npuctl 을 import 하는 파일이 있는지 go/parser 로 본다 —
// 문자열 검색은 주석만 있어도 통과하므로 실제 import 선언만 판정 대상으로 삼는다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
package npuctl

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sharedPackagePath = "kcloud-operator/pkg/npuctl"

// TestCLIAndRESTImportSharedPackage 는 두 진입점이 pkg/npuctl 을 계속 import 하는지 본다.
func TestCLIAndRESTImportSharedPackage(t *testing.T) {
	dirs := []string{"../../cmd/kubectl-npu", "../../internal/apiserver"}
	for _, dir := range dirs {
		if !dirImportsPackage(t, dir, sharedPackagePath) {
			t.Errorf("%s 아래 어떤 파일도 %q 를 import 하지 않는다", dir, sharedPackagePath)
		}
	}
}

// dirImportsPackage 는 dir 이하 .go 파일(_test.go 제외)의 import 선언만 파싱해
// importPath 를 쓰는 파일이 하나라도 있으면 true 를 낸다.
func dirImportsPackage(t *testing.T, dir, importPath string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == importPath {
				found = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s 훑기 실패: %v", dir, err)
	}
	return found
}
