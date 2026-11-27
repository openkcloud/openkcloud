// ============================================================
// kernel_image_targets_test.go: 커널 이미지 발행 대상 목록과 조립 규약이 같은지 고정한다
// 상세: deploy/kernel-image-targets.yaml(발행 대상)의 태그 규약과 operator 의
//       resolveDriverImage 조립 결과, 차트 렌더, 묶음 선언이 서로 어긋나지 않는지 대조한다.
// 생성일: 2026-10-05 | 수정일: 2026-10-11
// ============================================================

package upgrade

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"kcloud-operator/api/v1alpha1"
)

// repoRoot 는 이 패키지에서 본 저장소 뿌리다.
const repoRoot = "../.."

// kernelImageTargets 는 deploy/kernel-image-targets.yaml 의 모양이다.
type kernelImageTargets struct {
	Kernels []string `json:"kernels"`
	Targets map[string]struct {
		BuildRevision string `json:"buildRevision"`
		PublicBundle  *bool  `json:"publicBundle"`
	} `json:"targets"`
}

// chartDriverValues 는 values.yaml 에서 조립에 쓰는 네 값만 꺼낸다.
type chartDriverValues struct {
	Global struct {
		Registry string `json:"registry"`
	} `json:"global"`
	DriverInstallPolicies map[string]struct {
		Driver struct {
			Repository         string `json:"repository"`
			Tag                string `json:"tag"`
			Version            string `json:"version"`
			KernelImageVariant string `json:"kernelImageVariant"`
		} `json:"driver"`
	} `json:"driverInstallPolicies"`
}

func readYAML(t *testing.T, rel string, into interface{}) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("%s 를 읽지 못했다: %v", rel, err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s 파싱 실패: %v", rel, err)
	}
}

// runScript 는 저장소 뿌리에서 bash 스크립트를 돌려 stdout 과 종료 코드를 돌려준다.
func runScript(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("bash", args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("%v 실행 실패: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// 발행 대상 목록이 만드는 태그와 operator 의 조립 결과가 같아야 한다.
// 조회하는 쪽은 image-tag-check.sh --kernel-refs 다 — 그 분기가 목록을 보지 않게 되면
// 이 모드가 사라져 여기서 걸린다.
func TestKernelImageRefsMatchAssembledTags(t *testing.T) {
	var targets kernelImageTargets
	readYAML(t, "deploy/kernel-image-targets.yaml", &targets)
	var values chartDriverValues
	readYAML(t, "deploy/helm/values.yaml", &values)

	if len(targets.Kernels) == 0 || len(targets.Targets) == 0 {
		t.Fatalf("발행 대상이 0건이다 — 커널 %d개, 벤더 %d개. 이 대조는 대상이 있어야 성립한다",
			len(targets.Kernels), len(targets.Targets))
	}

	// Go 쪽 기대값: 차트 값으로 정책을 만들고 런타임과 같은 함수로 조립한다.
	var want []string
	for vendor, spec := range targets.Targets {
		v, ok := values.DriverInstallPolicies[vendor]
		if !ok {
			t.Fatalf("values.yaml 에 driverInstallPolicies.%s 가 없다 — 목록이 가리키는 벤더가 차트에 없다", vendor)
		}
		policy := &v1alpha1.DriverInstallPolicy{Spec: v1alpha1.DriverInstallPolicySpec{
			Driver: v1alpha1.DriverSpec{
				Image:                    values.Global.Registry + "/" + v.Driver.Repository + ":" + v.Driver.Tag,
				Version:                  v.Driver.Version,
				KernelImageVariant:       v.Driver.KernelImageVariant,
				KernelImageBuildRevision: spec.BuildRevision,
			},
		}}
		for _, kernel := range targets.Kernels {
			ref, skip, _ := resolveDriverImage(policy, kernel)
			if skip != "" {
				t.Errorf("%s / %s: 조립이 거절됐다 — %s", vendor, kernel, skip)
				continue
			}
			if ref == policy.Spec.Driver.Image {
				t.Errorf("%s / %s: 조립하지 않고 기본 이미지로 떨어졌다 (%s)", vendor, kernel, ref)
				continue
			}
			want = append(want, ref)
		}
	}
	sort.Strings(want)

	stdout, stderr, code := runScript(t, nil, "hack/image-tag-check.sh", "--kernel-refs")
	if code != 0 {
		t.Fatalf("image-tag-check.sh --kernel-refs 가 %d 로 끝났다\n%s", code, stderr)
	}
	got := nonEmptyLines(stdout)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("게이트가 묻는 태그와 조립 결과가 다르다\n게이트:\n  %s\n조립:\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if len(got) != len(targets.Kernels)*len(targets.Targets) {
		t.Errorf("태그 %d개, want %d개(커널 %d × 벤더 %d)",
			len(got), len(targets.Kernels)*len(targets.Targets), len(targets.Kernels), len(targets.Targets))
	}
}

// 커널 목록이 비면 아무 참조도 내지 않아야 한다. 이 가드가 없으면 커널 토막이 빈 태그가
// 조립돼 조회 대상에 실린다. 목록을 비우는 것은 사전 컴파일 경로를 끄는 수단이 아니다 —
// 끄기는 차트의 kernelImageBuildRevision 이고 그 기본값이 이미 빈 값이다.
func TestKernelImageRefsEmptyWhenNoKernelsDeclared(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "empty-targets.yaml")
	if err := os.WriteFile(fixture, []byte("kernels: []\ntargets:\n  nvidia:\n    buildRevision: v1\n"), 0o600); err != nil {
		t.Fatalf("픽스처 작성 실패: %v", err)
	}
	env := []string{"KERNEL_IMAGE_TARGETS=" + fixture}

	stdout, stderr, code := runScript(t, env, "hack/kernel-image-targets.sh", "--refs")
	if code != 0 {
		t.Fatalf("--refs 가 %d 로 끝났다\n%s", code, stderr)
	}
	if got := nonEmptyLines(stdout); len(got) != 0 {
		t.Errorf("빈 커널 목록인데 참조 %d건이 나왔다: %v", len(got), got)
	}

	// 목록이 비었다고 묶음이 그냥 통과하면 아무도 선언하지 않은 이미지가 발행된다.
	stale := filepath.Join(t.TempDir(), "stale-bundle.yaml")
	if err := os.WriteFile(stale, []byte(
		"bundles:\n  kernel-images:\n    - image: tenstorrent-driver-ds\n      tag: 2.11.0-k6.8.0-138-generic-v1\n"),
		0o600); err != nil {
		t.Fatalf("픽스처 작성 실패: %v", err)
	}
	_, stderr, code = runScript(t, env, "hack/kernel-image-targets.sh", "--check-bundle", stale)
	if code == 0 {
		t.Errorf("빈 목록인데 항목이 남은 묶음을 통과시켰다")
	}
	if !strings.Contains(stderr, "묶음에 있는데 목록에 없다") {
		t.Errorf("남은 항목을 지목하지 않았다:\n%s", stderr)
	}
}

// publicBundle 이 참·거짓이 아니면 거절해야 한다. 조용히 yes 로 보면 오타 하나가 공개 묶음에
// 항목을 넣는다 — 그 선언 파일에는 스키마가 없어 유도 스크립트가 유일한 검사 자리다.
func TestKernelImageTargetsRejectNonBooleanPublicBundle(t *testing.T) {
	for _, bad := range []string{`"false"`, `0`, `nope`} {
		fixture := filepath.Join(t.TempDir(), "targets.yaml")
		body := "kernels: [6.8.0-138-generic]\ntargets:\n  nvidia:\n    buildRevision: v1\n" +
			"    publicBundle: " + bad + "\n"
		if err := os.WriteFile(fixture, []byte(body), 0o600); err != nil {
			t.Fatalf("픽스처 작성 실패: %v", err)
		}
		env := []string{"KERNEL_IMAGE_TARGETS=" + fixture}
		stdout, stderr, code := runScript(t, env, "hack/kernel-image-targets.sh", "--bundle")
		if code == 0 {
			t.Errorf("publicBundle: %s 를 받아들였다 (출력 %q)", bad, stdout)
			continue
		}
		if !strings.Contains(stderr, "publicBundle 이 참·거짓이 아니다") {
			t.Errorf("publicBundle: %s 의 거절 사유가 없다:\n%s", bad, stderr)
		}
	}
	// 참·거짓은 그대로 통한다 — 거절이 과하게 넓어지면 nvidia 제외가 깨진다.
	fixture := filepath.Join(t.TempDir(), "targets.yaml")
	body := "kernels: [6.8.0-138-generic]\ntargets:\n  nvidia:\n    buildRevision: v1\n" +
		"    publicBundle: false\n  tenstorrent:\n    buildRevision: v1\n"
	if err := os.WriteFile(fixture, []byte(body), 0o600); err != nil {
		t.Fatalf("픽스처 작성 실패: %v", err)
	}
	stdout, stderr, code := runScript(t, []string{"KERNEL_IMAGE_TARGETS=" + fixture},
		"hack/kernel-image-targets.sh", "--bundle")
	if code != 0 {
		t.Fatalf("참·거짓 값을 거절했다\n%s", stderr)
	}
	if got := nonEmptyLines(stdout); len(got) != 1 ||
		!strings.HasPrefix(got[0], "tenstorrent-driver-ds\t") {
		t.Errorf("publicBundle: false 가 공개 묶음에서 빠지지 않았다: %v", got)
	}
}

// 두 저장소가 어긋나면 잡혀야 한다. 한쪽만 고치는 일이 생기기 때문이다.
func TestKernelImageBundleDriftIsCaught(t *testing.T) {
	stdout, stderr, code := runScript(t, nil, "hack/kernel-image-targets.sh", "--bundle")
	if code != 0 {
		t.Fatalf("--bundle 이 %d 로 끝났다\n%s", code, stderr)
	}
	rows := nonEmptyLines(stdout)
	if len(rows) < 2 {
		t.Fatalf("공개 묶음 항목이 %d건이다 — 어긋남을 만들려면 둘 이상이어야 한다", len(rows))
	}

	// 맞는 묶음은 통과한다.
	write := func(name string, entries []string) string {
		p := filepath.Join(t.TempDir(), name)
		var b strings.Builder
		b.WriteString("bundles:\n  kernel-images:\n")
		for _, r := range entries {
			f := strings.SplitN(r, "\t", 2)
			b.WriteString("    - image: " + f[0] + "\n      tag: " + f[1] + "\n")
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
			t.Fatalf("픽스처 작성 실패: %v", err)
		}
		return p
	}
	if _, stderr, code = runScript(t, nil, "hack/kernel-image-targets.sh", "--check-bundle", write("match.yaml", rows)); code != 0 {
		t.Fatalf("맞는 묶음을 어긋남으로 봤다\n%s", stderr)
	}

	// 어긋난 묶음: 한 줄을 빼고 목록에 없는 한 줄을 더한다. 양방향 모두 지목해야 한다.
	drift := append([]string{"tenstorrent-driver-ds\t2.11.0-k9.9.9-999-generic-v1"}, rows[1:]...)
	_, stderr, code = runScript(t, nil, "hack/kernel-image-targets.sh", "--check-bundle", write("drift.yaml", drift))
	if code == 0 {
		t.Fatalf("어긋난 묶음을 통과시켰다")
	}
	for _, want := range []string{"목록에 있는데 묶음에 없다", "묶음에 있는데 목록에 없다", rows[0]} {
		if !strings.Contains(stderr, want) {
			t.Errorf("어긋남 보고에 %q 가 없다:\n%s", want, stderr)
		}
	}

	// 형제 images 저장소가 있으면 실제 묶음도 같은 대조를 받는다. 없으면(CI) 이 부분만 빠지고
	// 위 픽스처 대조는 그대로 돈다 — 이 시험이 통째로 건너뛰지는 않는다.
	// 스크립트는 저장소 뿌리에서 도므로 경로를 절대경로로 넘긴다.
	real, err := filepath.Abs(filepath.Join(repoRoot, "../images/kcloud-operator-images/release-bundle.yaml"))
	if err != nil {
		t.Fatalf("형제 저장소 경로를 만들지 못했다: %v", err)
	}
	if _, err := os.Stat(real); err != nil {
		t.Logf("형제 images 저장소가 없어 실제 묶음 대조는 건너뛴다: %v", err)
		return
	}
	if _, stderr, code = runScript(t, nil, "hack/kernel-image-targets.sh", "--check-bundle", real); code != 0 {
		t.Errorf("images 저장소의 실제 묶음이 목록과 어긋난다\n%s", stderr)
	}
}

// upgradeHelmBinary 는 helm 실행 파일 경로를 돌려준다. 없으면 CI 에서는 하드 실패하고
// 로컬에서는 건너뛴다. CI 에서 건너뛰는 것은 "환경이 없어 건너뜀" 이 아니라 "있어야 할 것이
// 없음" 이고, 그 상태로 워크플로가 초록이면 차트 대조가 아무 신호 없이 사라진다.
//
// internal/controller 의 helm_presence_test.go 에 같은 판정의 원본(helmBinary)이 있다.
// 패키지가 달라 공유할 수 없어 사본으로 둔다 — **한쪽을 고치면 다른 쪽도 고칠 것.**
func upgradeHelmBinary(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("helm")
	if err == nil {
		return bin
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("CI 인데 helm 이 PATH 에 없다 — 차트 렌더 대조가 건너뛰면 그 검사는 " +
			"신호를 내지 않는다. 워크플로가 helm 을 깔아야 한다(.github/workflows/test.yml 의 " +
			"설치 단계). 다른 CI 를 붙일 때도 그쪽에 같은 설치 단계가 필요하다")
	}
	t.Skip("helm 이 PATH 에 없다 — 차트 렌더 대조 건너뜀(CI 에서는 하드 실패한다)")
	return ""
}

// renderedDriverImages 는 차트가 렌더한 DriverInstallPolicy 의 spec.driver.image 모음이다.
// 런타임이 조립의 밑으로 받는 것이 이 문자열이다.
func renderedDriverImages(t *testing.T) map[string]bool {
	t.Helper()
	bin := upgradeHelmBinary(t)
	out, err := exec.Command(bin, "template", "t", filepath.Join(repoRoot, "deploy/helm")).CombinedOutput()
	if err != nil {
		t.Fatalf("차트 렌더 실패: %v\n%s", err, out)
	}
	images := map[string]bool{}
	for _, doc := range strings.Split("\n"+string(out), "\n---\n") {
		var d struct {
			Kind string `json:"kind"`
			Spec struct {
				Driver struct {
					Image string `json:"image"`
				} `json:"driver"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
			t.Fatalf("렌더 문서 파싱 실패: %v\n%s", err, doc)
		}
		if d.Kind == "DriverInstallPolicy" && d.Spec.Driver.Image != "" {
			images[d.Spec.Driver.Image] = true
		}
	}
	if len(images) == 0 {
		t.Fatal("렌더에 DriverInstallPolicy 의 driver.image 가 0건이다 — 대조할 것이 없다")
	}
	return images
}

// 조립의 밑이 되는 이미지를 차트 렌더와 대조한다. 위 대조는 values.yaml 에서
// "<registry>/<repository>:<tag>" 를 직접 만드는데, 런타임이 실제로 받는 것은 차트가
// kcloud-operator.image 헬퍼로 렌더한 문자열이다. 헬퍼가 경로 토막을 더하면 bash·Go 두 공식은
// 그대로 일치해 위 시험은 초록인데 굽는 경로와 당기는 경로가 갈린다. 어긋나도 image-tag-check
// 는 404 로 보고하고, 미발행이 정답 상태라 그 404 가 구별되지 않는다 — 그래서 여기서 본다.
func TestKernelImageBaseImageMatchesChartRender(t *testing.T) {
	var targets kernelImageTargets
	readYAML(t, "deploy/kernel-image-targets.yaml", &targets)
	var values chartDriverValues
	readYAML(t, "deploy/helm/values.yaml", &values)
	if len(targets.Targets) == 0 {
		t.Fatal("발행 대상 벤더가 0건이다 — 이 대조는 대상이 있어야 성립한다")
	}

	rendered := renderedDriverImages(t)
	for vendor := range targets.Targets {
		v, ok := values.DriverInstallPolicies[vendor]
		if !ok {
			t.Fatalf("values.yaml 에 driverInstallPolicies.%s 가 없다", vendor)
		}
		want := values.Global.Registry + "/" + v.Driver.Repository + ":" + v.Driver.Tag
		if !rendered[want] {
			got := make([]string, 0, len(rendered))
			for img := range rendered {
				got = append(got, img)
			}
			sort.Strings(got)
			t.Errorf("%s: 조립의 밑으로 쓰는 %q 가 차트 렌더에 없다 — 렌더된 driver.image:\n  %s",
				vendor, want, strings.Join(got, "\n  "))
		}
	}
}

// --bundle-name 은 release-prepare.sh 가 "태그를 만들지 않을 묶음" 을 받는 유일한 경로다.
// 그 쪽은 빈 값만 거르므로, 이름이 여러 줄로 나오거나 빈 값이 되면 가드가 조용히 비켜난다.
func TestKernelImageTargetsBundleNameIsOneLine(t *testing.T) {
	stdout, stderr, code := runScript(t, nil, "hack/kernel-image-targets.sh", "--bundle-name")
	if code != 0 {
		t.Fatalf("--bundle-name 이 %d 로 끝났다\n%s", code, stderr)
	}
	got := nonEmptyLines(stdout)
	if len(got) != 1 {
		t.Fatalf("묶음 이름이 %d줄이다: %v — release-prepare 의 비교가 어긋난다", len(got), got)
	}
	if strings.ContainsAny(got[0], "\"' \t") {
		t.Errorf("묶음 이름에 따옴표·공백이 섞였다: %q", got[0])
	}

	// 그 이름이 실제 묶음 검사가 보는 묶음이어야 한다 — --check-bundle 이 다른 키를 보면
	// release-prepare 는 엉뚱한 이름의 태그 생성을 막는다.
	fixture := filepath.Join(t.TempDir(), "bundle.yaml")
	body := "bundles:\n  " + got[0] + ":\n    - image: tenstorrent-driver-ds\n      tag: 9.9.9-k0-v0\n"
	if err := os.WriteFile(fixture, []byte(body), 0o600); err != nil {
		t.Fatalf("픽스처 작성 실패: %v", err)
	}
	if _, stderr, code = runScript(t, nil, "hack/kernel-image-targets.sh", "--check-bundle", fixture); code == 0 {
		t.Errorf("목록에 없는 항목만 담은 %s 묶음을 통과시켰다", got[0])
	} else if !strings.Contains(stderr, got[0]) {
		t.Errorf("어긋남 보고가 묶음 이름 %q 를 적지 않는다:\n%s", got[0], stderr)
	}
}
