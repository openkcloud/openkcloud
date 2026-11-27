// ============================================================
// lint_workflow_gates_test.go: lint 워크플로가 Makefile 게이트를 실제로 부르는지 고정한다.
// 상세: make 타깃은 있어도 CI 가 부르지 않으면 한 번도 돌지 않은 채 초록이 된다. 워크플로
//
//	YAML 의 run 단계에서 `make <타깃>` 호출을 찾고, 단계가 사라지면 여기서 실패한다.
//
// 생성일: 2026-10-05 | 수정일: 2026-10-13
// ============================================================
package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflowRunsMake 는 워크플로 파일의 어느 run 단계가 `make <target>` 을 부르는지 돌려준다.
// run 블록은 여러 줄일 수 있어 줄 단위로 본다. job 이 0개면 대조가 비었으므로 실패한다.
func workflowRunsMake(t *testing.T, wfPath, target string) bool {
	t.Helper()
	want := "make " + target
	for _, step := range workflowSteps(t, wfPath) {
		for _, line := range strings.Split(step.Run, "\n") {
			line = strings.TrimSpace(line)
			if line == want || strings.HasPrefix(line, want+" ") {
				return true
			}
		}
	}
	return false
}

func TestLintWorkflowRunsDocCiteCheck(t *testing.T) {
	const wfPath = "../../.github/workflows/lint.yml"
	if !workflowRunsMake(t, wfPath, "doc-cite-check") {
		t.Errorf("%s 에 `make doc-cite-check` 단계가 없다 — 타깃은 있어도 CI 가 부르지 않으면 "+
			"한 번도 돌지 않은 채 초록이 된다", wfPath)
	}
}

// docs-path-check·private-ip-check·lint-config·workspace-cite-check·comment-check·
// vendor-branch-check 는 make 타깃은 있어도 어느 워크플로가 부르지 않으면 한 번도 돌지 않은
// 채 초록이 된다(git 훅도 부르지 않는다 — 사람이 손으로 돌릴 때만 돈다). 같은 표에 고정한다.
func TestLintWorkflowRunsGateTargets(t *testing.T) {
	const wfPath = "../../.github/workflows/lint.yml"
	for _, target := range []string{"docs-path-check", "private-ip-check", "lint-config", "workspace-cite-check", "comment-check",
		"vendor-branch-check"} {
		t.Run(target, func(t *testing.T) {
			if !workflowRunsMake(t, wfPath, target) {
				t.Errorf("%s 에 `make %s` 단계가 없다 — 타깃은 있어도 CI 가 부르지 않으면 "+
					"한 번도 돌지 않은 채 초록이 된다", wfPath, target)
			}
		})
	}
}

// 증명: 판별은 `make <타깃>` 정확 일치(뒤에 인자가 붙는 것은 허용)이고 접미가 다른 타깃은 아니다.
// 깨는 뮤테이션: HasPrefix 만 남기면 (b) 가 true 가 되어 실패한다.
func TestWorkflowRunsMakeMatchesExactTarget(t *testing.T) {
	cases := []struct {
		name string
		run  string
		want bool
	}{
		{"정확 일치", "make doc-cite-check", true},
		{"인자 동반", "make doc-cite-check OTHER=1", true},
		{"접미 다른 타깃", "make doc-cite-check-extra", false},
		{"여러 줄 블록", "go mod tidy\n  make doc-cite-check\n", true},
		{"다른 타깃만", "make test", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			wf := filepath.Join(dir, "wf.yml")
			body := "jobs:\n  j:\n    steps:\n      - name: s\n        run: |\n"
			for _, line := range strings.Split(strings.TrimRight(c.run, "\n"), "\n") {
				body += "          " + line + "\n"
			}
			if err := os.WriteFile(wf, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := workflowRunsMake(t, wf, "doc-cite-check"); got != c.want {
				t.Fatalf("run=%q: got %v want %v", c.run, got, c.want)
			}
		})
	}
}

// workflowGolangciLintVersions 는 golangci-lint-action 을 부르는 단계마다 그
// working-directory(루트는 "") 를 version 에 대응시켜 돌려준다. 이 action 은 `run:` 이
// 아니라 `uses:`+`with:` 라 workflowRunsMake 의 줄 단위 대조가 안 맞는다.
func workflowGolangciLintVersions(t *testing.T, wfPath string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, step := range workflowSteps(t, wfPath) {
		if !strings.HasPrefix(step.Uses, "golangci/golangci-lint-action@") {
			continue
		}
		dir, _ := step.With["working-directory"].(string)
		version, _ := step.With["version"].(string)
		out[dir] = version
	}
	return out
}

// cmd/kubectl-npu 는 별도 go.mod 라 루트 golangci-lint-action 실행이 내려가지 않는다.
// working-directory 로 같은 action 을 한 번 더 부르지 않으면 그 모듈은 CI 린트를 한 번도
// 받지 않은 채 초록이다. version 이 루트와 갈리면 같은 코드에 다른 판정이 나므로 같이 고정한다.
func TestLintWorkflowLintsKubectlNpuModule(t *testing.T) {
	const wfPath = "../../.github/workflows/lint.yml"
	versions := workflowGolangciLintVersions(t, wfPath)
	rootVersion, rootOK := versions[""]
	if !rootOK || rootVersion == "" {
		t.Fatalf("%s 에 루트 golangci-lint-action 단계가 없다", wfPath)
	}
	nestedVersion, nestedOK := versions["cmd/kubectl-npu"]
	if !nestedOK {
		t.Errorf("%s 에 working-directory: cmd/kubectl-npu 로 golangci-lint-action 을 부르는 "+
			"단계가 없다 — 그 모듈은 별도 go.mod 라 루트 실행이 내려가지 않아 CI 린트를 "+
			"한 번도 받지 않은 채 초록이 된다", wfPath)
		return
	}
	if nestedVersion != rootVersion {
		t.Errorf("%s: cmd/kubectl-npu 린트 단계의 version=%q 이 루트 단계의 %q 와 다르다 — "+
			"같은 코드에 다른 판정이 난다", wfPath, nestedVersion, rootVersion)
	}
}
