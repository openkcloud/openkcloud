// ============================================================
// rebellions_cdi_prep_test.go: rbln-cdi-prep init container 스크립트 실행 시험
// 상세: 임시 디렉터리를 노드 루트(ROOT)로 꾸며 스크립트를 sh 로 실제 실행한다. 종료 코드가 Go 상수와
//       같은지, 생성 파일이 CDI 스펙 형식인지(sigs.k8s.io/yaml 로 읽음), 실패 경로가 파일을 남기지 않는지 본다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const (
	rbllnsTestKind     = "rebellions.ai/npu"
	rbllnsTestCDIFile  = "var/run/cdi/kcloud-rebellions.yaml"
	rbllnsTestHostExec = "registry.example.com/kcloud-host-exec:test"
)

// rbllnsCDISpec 은 시험에서 읽는 CDI 스펙의 필요한 부분이다.
type rbllnsCDISpec struct {
	CDIVersion string `json:"cdiVersion"`
	Kind       string `json:"kind"`
	Devices    []struct {
		Name           string `json:"name"`
		ContainerEdits struct {
			Env         []string `json:"env"`
			DeviceNodes []struct {
				Path string `json:"path"`
			} `json:"deviceNodes"`
		} `json:"containerEdits"`
	} `json:"devices"`
}

// rbllnsFakeNode 는 노드 루트를 꾸민다. smiDir 은 rbln-smi 를 둘 곳("usr/bin", "driver/usr/bin",
// 빈 값이면 두지 않음)이다.
func rbllnsFakeNode(t *testing.T, devs []string, smiDir string, smiExec bool) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"dev", "usr/bin", "driver/usr/bin", "var/run/cdi"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range devs {
		if err := os.WriteFile(filepath.Join(root, "dev", d), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if smiDir != "" {
		mode := os.FileMode(0o644)
		if smiExec {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(root, smiDir, "rbln-smi"), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// rbllnsRunPrep 은 스크립트를 실행해 종료 코드, 출력, termination 메시지를 돌려준다.
func rbllnsRunPrep(t *testing.T, root, gen, kind string) (int, string, string) {
	t.Helper()
	termLog := filepath.Join(t.TempDir(), "termination-log")
	cmd := exec.Command("sh", "-c", rbllnsPrepScript)
	cmd.Env = append(os.Environ(), "ROOT="+root, "TERM_LOG="+termLog, "GEN="+gen, "CDI_KIND="+kind)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("스크립트 실행 실패: %v", err)
		}
		code = ee.ExitCode()
	}
	msg, _ := os.ReadFile(termLog)
	return code, out.String(), string(msg)
}

func rbllnsReadSpec(t *testing.T, root string) rbllnsCDISpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rbllnsTestCDIFile))
	if err != nil {
		t.Fatalf("CDI 파일이 없다: %v", err)
	}
	var s rbllnsCDISpec
	if err := yaml.Unmarshal(raw, &s); err != nil {
		t.Fatalf("CDI 파일 파싱 실패: %v\n%s", err, raw)
	}
	return s
}

// v0.3: 장치마다 rbln<N> 이름과 장치 노드 하나. 두 자리 번호는 넣고 RDS(rblnfs*)는 뺀다.
func TestRbllnsPrepV03WritesDeviceNames(t *testing.T) {
	root := rbllnsFakeNode(t, []string{"rbln0", "rbln1", "rbln10", "rblnfs0"}, "usr/bin", true)
	code, out, _ := rbllnsRunPrep(t, root, rbllnsGenV03, rbllnsTestKind)
	if code != 0 {
		t.Fatalf("종료 코드 %d\n%s", code, out)
	}
	s := rbllnsReadSpec(t, root)
	if s.CDIVersion != "0.5.0" || s.Kind != rbllnsTestKind {
		t.Errorf("cdiVersion=%q kind=%q", s.CDIVersion, s.Kind)
	}
	names := make([]string, 0, len(s.Devices))
	for _, d := range s.Devices {
		names = append(names, d.Name)
		if len(d.ContainerEdits.DeviceNodes) != 1 || d.ContainerEdits.DeviceNodes[0].Path != "/dev/"+d.Name {
			t.Errorf("%s 의 장치 노드가 /dev/%s 하나가 아니다: %+v", d.Name, d.Name, d.ContainerEdits.DeviceNodes)
		}
		if len(d.ContainerEdits.Env) != 0 {
			t.Errorf("%s 에 환경변수가 들어갔다: %v", d.Name, d.ContainerEdits.Env)
		}
	}
	if want := []string{"rbln0", "rbln1", "rbln10"}; !reflect.DeepEqual(names, want) {
		t.Errorf("장치 이름 %v, want %v", names, want)
	}
}

// v0.4: runtime 하나, 장치 노드 없이 환경변수만.
func TestRbllnsPrepV04WritesRuntime(t *testing.T) {
	root := rbllnsFakeNode(t, []string{"rbln0", "rbln1"}, "usr/bin", true)
	if code, out, _ := rbllnsRunPrep(t, root, rbllnsGenV04, rbllnsTestKind); code != 0 {
		t.Fatalf("종료 코드 %d\n%s", code, out)
	}
	s := rbllnsReadSpec(t, root)
	if len(s.Devices) != 1 || s.Devices[0].Name != "runtime" {
		t.Fatalf("장치가 runtime 하나가 아니다: %+v", s.Devices)
	}
	e := s.Devices[0].ContainerEdits
	if len(e.DeviceNodes) != 0 || !reflect.DeepEqual(e.Env, []string{"RBLN_KCLOUD_CDI=1"}) {
		t.Errorf("runtime 편집이 환경변수 하나가 아니다: %+v", e)
	}
}

// CDI 종류는 환경변수 CDI_KIND 를 그대로 쓴다(v0.3 은 resourcePrefix 를 따른다). 기본 종류만 쓰면
// 스크립트가 종류를 고정해도 시험이 통과하므로 다른 종류로 한 번 확인한다.
func TestRbllnsPrepKindFollowsEnv(t *testing.T) {
	const kind = "example.com/npu"
	root := rbllnsFakeNode(t, []string{"rbln0"}, "usr/bin", true)
	if code, out, _ := rbllnsRunPrep(t, root, rbllnsGenV03, kind); code != 0 {
		t.Fatalf("종료 코드 %d\n%s", code, out)
	}
	if s := rbllnsReadSpec(t, root); s.Kind != kind {
		t.Errorf("kind=%q, want %q", s.Kind, kind)
	}
}

// rbln-smi 가 /usr/local/bin(컨테이너 /host/driver/usr/bin)에만 있어도 통과한다.
func TestRbllnsPrepSmiInDriverDir(t *testing.T) {
	root := rbllnsFakeNode(t, []string{"rbln0"}, "driver/usr/bin", true)
	if code, out, _ := rbllnsRunPrep(t, root, rbllnsGenV03, rbllnsTestKind); code != 0 {
		t.Fatalf("종료 코드 %d\n%s", code, out)
	}
}

// 실패 경로는 Go 상수와 같은 종료 코드, 사유 토큰으로 시작하는 termination 메시지를 남기고 파일을 쓰지 않는다.
func TestRbllnsPrepFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		devs    []string
		smiDir  string
		smiExec bool
		want    int
		token   string
	}{
		{"rbln-smi 없음", []string{"rbln0"}, "", false, rbllnsPrepExitNoSmi, rbllnsReasonNoSmi},
		{"rbln-smi 실행 비트 없음", []string{"rbln0"}, "usr/bin", false, rbllnsPrepExitNoSmi, rbllnsReasonNoSmi},
		{"장치 없음(RDS 만)", []string{"rblnfs0"}, "usr/bin", true, rbllnsPrepExitNoDevice, rbllnsReasonNoDevice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := rbllnsFakeNode(t, tc.devs, tc.smiDir, tc.smiExec)
			code, out, msg := rbllnsRunPrep(t, root, rbllnsGenV03, rbllnsTestKind)
			if code != tc.want {
				t.Fatalf("종료 코드 %d, want %d\n%s", code, tc.want, out)
			}
			if !strings.HasPrefix(msg, tc.token+":") {
				t.Errorf("termination 메시지가 %s 로 시작하지 않는다: %q", tc.token, msg)
			}
			if _, err := os.Stat(filepath.Join(root, rbllnsTestCDIFile)); !os.IsNotExist(err) {
				t.Error("실패했는데 CDI 파일이 생겼다")
			}
		})
	}
}

// 쓰기 실패는 종료 코드 5 다. root 는 권한 검사를 건너뛰므로 일반 사용자로만 확인할 수 있다.
func TestRbllnsPrepWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 는 읽기 전용 디렉터리에도 쓸 수 있어 확인할 수 없다")
	}
	root := rbllnsFakeNode(t, []string{"rbln0"}, "usr/bin", true)
	cdi := filepath.Join(root, "var/run/cdi")
	if err := os.Chmod(cdi, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cdi, 0o755) })
	code, out, msg := rbllnsRunPrep(t, root, rbllnsGenV03, rbllnsTestKind)
	if code != rbllnsPrepExitWriteFailed {
		t.Fatalf("종료 코드 %d, want %d\n%s", code, rbllnsPrepExitWriteFailed, out)
	}
	if !strings.HasPrefix(msg, rbllnsReasonWriteFailed+":") {
		t.Errorf("termination 메시지: %q", msg)
	}
}

// v0.4 에서 벤더 툴킷 스펙(rbln.yaml)이 있으면 같은 우선순위 충돌을 피하려고 우리 파일을 지운다.
func TestRbllnsPrepV04SkipsWhenVendorSpecPresent(t *testing.T) {
	root := rbllnsFakeNode(t, []string{"rbln0"}, "usr/bin", true)
	cdi := filepath.Join(root, "var/run/cdi")
	vendorSpec := []byte("kind: rebellions.ai/npu\n")
	if err := os.WriteFile(filepath.Join(cdi, "rbln.yaml"), vendorSpec, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rbllnsTestCDIFile), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := rbllnsRunPrep(t, root, rbllnsGenV04, rbllnsTestKind); code != 0 {
		t.Fatalf("종료 코드 %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, rbllnsTestCDIFile)); !os.IsNotExist(err) {
		t.Error("벤더 스펙이 있는데 우리 파일이 남았다")
	}
	if got, _ := os.ReadFile(filepath.Join(cdi, "rbln.yaml")); string(got) != string(vendorSpec) {
		t.Error("벤더 스펙을 건드렸다")
	}
}

// 판이 바뀌면 같은 파일을 덮어쓰고, 임시 파일을 남기지 않는다.
func TestRbllnsPrepGenerationSwitchOverwrites(t *testing.T) {
	root := rbllnsFakeNode(t, []string{"rbln0", "rbln1"}, "usr/bin", true)
	if code, out, _ := rbllnsRunPrep(t, root, rbllnsGenV03, rbllnsTestKind); code != 0 {
		t.Fatalf("v0.3 종료 코드 %d\n%s", code, out)
	}
	if code, out, _ := rbllnsRunPrep(t, root, rbllnsGenV04, rbllnsTestKind); code != 0 {
		t.Fatalf("v0.4 종료 코드 %d\n%s", code, out)
	}
	if s := rbllnsReadSpec(t, root); len(s.Devices) != 1 || s.Devices[0].Name != "runtime" {
		t.Errorf("v0.4 로 덮어쓰지 않았다: %+v", s.Devices)
	}
	entries, err := os.ReadDir(filepath.Join(root, "var/run/cdi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("cdi 디렉터리에 파일이 %d 개다(임시 파일이 남았다): %v", len(entries), entries)
	}
}

// kubelet 은 container command 의 $$ 를 $ 로, $(VAR) 를 환경변수 값으로 바꾸므로 시험이 실행하는 스크립트와 파드가 실행하는 스크립트가 같은 글자여야 한다.
func TestRbllnsPrepScriptSurvivesKubeletExpansion(t *testing.T) {
	for _, form := range []string{"$$", "$("} {
		if strings.Contains(rbllnsPrepScript, form) {
			t.Errorf("스크립트에 %q 가 있다: kubelet 이 바꿔 파드에서 다르게 실행된다", form)
		}
	}
}

// init container 렌더: 이미지가 비면 nil, 아니면 env·마운트를 고정한다.
func TestRbllnsPrepInitContainer(t *testing.T) {
	if c := rbllnsPrepInitContainer("", rbllnsGenV03, rbllnsTestKind); c != nil {
		t.Fatalf("이미지가 빈데 init container 를 만들었다: %+v", c)
	}
	c := rbllnsPrepInitContainer(rbllnsTestHostExec, rbllnsGenV04, rbllnsTestKind)
	if c == nil || c.Name != rbllnsPrepContainerName || c.Image != rbllnsTestHostExec {
		t.Fatalf("init container: %+v", c)
	}
	if !reflect.DeepEqual(c.Command, []string{"sh", "-c", rbllnsPrepScript}) {
		t.Error("명령이 스크립트 실행이 아니다")
	}
	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	if env["GEN"] != rbllnsGenV04 || env["CDI_KIND"] != rbllnsTestKind {
		t.Errorf("env: %v", env)
	}
	mounts := map[string]string{}
	for _, m := range c.VolumeMounts {
		mounts[m.MountPath] = m.Name
		if (m.MountPath == "/host/var/run/cdi") == m.ReadOnly {
			t.Errorf("%s 의 ReadOnly=%v (cdi 만 쓰기)", m.MountPath, m.ReadOnly)
		}
	}
	want := map[string]string{"/host/dev": "host-dev", "/host/usr/bin": "host-usr-bin",
		"/host/driver/usr/bin": "host-driver-usr-bin", "/host/var/run/cdi": rbllnsCDIVolumeName}
	if !reflect.DeepEqual(mounts, want) {
		t.Errorf("마운트 %v, want %v", mounts, want)
	}
}

func TestRbllnsPrepReason(t *testing.T) {
	for code, want := range map[int32]string{
		0:                         "",
		rbllnsPrepExitNoSmi:       rbllnsReasonNoSmi,
		rbllnsPrepExitNoDevice:    rbllnsReasonNoDevice,
		rbllnsPrepExitWriteFailed: rbllnsReasonWriteFailed,
		1:                         rbllnsReasonPrepFailed,
		137:                       rbllnsReasonPrepFailed,
	} {
		if got := rbllnsPrepReason(code); got != want {
			t.Errorf("code %d: got %q, want %q", code, got, want)
		}
	}
}
