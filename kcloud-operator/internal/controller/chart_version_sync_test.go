// ============================================================
// chart_version_sync_test.go: chart 선언 버전과 배포 프리셋이 갈라지지 않는지 고정한다.
// 상세: Chart.yaml 의 appVersion 은 릴리스마다 손으로 올리고, 실제 배포는 values 의
// image.tag 로 결정된다. 둘이 갈라지면 선언값을 진실로 믿는 판단이 틀리게 된다.
// 생성일: 2026-08-06 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// chartAppVersion 은 deploy/helm/Chart.yaml 의 appVersion 을 읽는다.
func chartAppVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/helm/Chart.yaml")
	if err != nil {
		t.Fatalf("Chart.yaml 읽기 실패: %v", err)
	}
	re := regexp.MustCompile(`(?m)^appVersion:\s*(\S+)\s*$`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatal("Chart.yaml 에 appVersion 이 없다")
	}
	return strings.Trim(string(m[1]), `"'`)
}

// presetImageTag 는 values 프리셋 파일의 image.tag 를 읽는다.
func presetImageTag(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s 읽기 실패: %v", path, err)
	}
	return imageTagIn(t, raw, path)
}

// imageTagIn 은 values 문서 바이트에서 image.tag 를 읽는다. 작업 트리의 파일과 고정
// 커밋의 blob 이 같은 파서를 쓰도록 분리한 함수다.
func imageTagIn(t *testing.T, raw []byte, name string) string {
	t.Helper()
	// image: 블록 아래 tag: 만 잡는다(detector 등 다른 블록의 tag 와 섞이지 않게).
	re := regexp.MustCompile(`(?m)^image:\s*\n(?:\s+\S.*\n)*?\s+tag:\s*(\S+)\s*$`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s 에 image.tag 가 없다", name)
	}
	return strings.Trim(string(m[1]), `"'`)
}

// line128Suffix 는 K8s 1.28 라인 operator 이미지 태그의 접미사다.
const line128Suffix = "-k8s1.28"

// chartPairedRelease 는 Chart.yaml 의 npu.ai/paired-release 주석을 읽는다. 반대 라인의
// 릴리스 번호이고, 그 라인의 프리셋이 가리켜야 할 태그다.
func chartPairedRelease(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/helm/Chart.yaml")
	if err != nil {
		t.Fatalf("Chart.yaml 읽기 실패: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s+npu\.ai/paired-release:\s*(\S+)\s*$`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatal("Chart.yaml annotations 에 npu.ai/paired-release 가 없다 — " +
			"반대 라인 프리셋이 어느 릴리스를 가리켜야 하는지 판정할 수 없다")
	}
	return strings.Trim(string(m[1]), `"'`)
}

// presetMinorRe 는 프리셋 파일 이름에서 그 프리셋이 겨냥하는 K8s 마이너를 뽑는다
// (values-k8s1.34.yaml, values-k8s1.34-dra.yaml 둘 다 1.34).
var presetMinorRe = regexp.MustCompile(`^values-k8s(\d+\.\d+)(?:-[^.]+)?\.yaml$`)

// kubeVersionFloorRe 는 Chart.yaml 의 kubeVersion 선언에서 하한 마이너를 뽑는다.
var kubeVersionFloorRe = regexp.MustCompile(`>=\s*(\d+\.\d+)`)

// chartKubeVersionFloor 는 Chart.yaml 의 kubeVersion 선언과 그 하한을 돌려준다.
// 선언이 없으면 치명 실패다 — 범위 밖 설치를 막는 유일한 수단이 사라진 것이다.
func chartKubeVersionFloor(t *testing.T) (string, int) {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/helm/Chart.yaml")
	if err != nil {
		t.Fatalf("Chart.yaml 읽기 실패: %v", err)
	}
	// 값 전체를 (.+?) 로 잡는다 — (\S+) 는 상한이 붙은 ">=1.31.0-0 <1.40.0-0" 에서
	// 아예 매치하지 못해 "kubeVersion 이 없다" 는 엉뚱한 사유로 떨어진다.
	m := regexp.MustCompile(`(?m)^kubeVersion:\s*(.+?)\s*$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("Chart.yaml 에 kubeVersion 이 없다 — 범위 밖 K8s 설치를 막을 수단이 없다")
	}
	decl := strings.Trim(string(m[1]), `"'`)
	f := kubeVersionFloorRe.FindStringSubmatch(decl)
	if f == nil {
		t.Fatalf("kubeVersion %q 에서 >= 하한을 읽지 못했다", decl)
	}
	floor, err := minorOf(f[1])
	if err != nil {
		t.Fatalf("kubeVersion 하한 %q 해석 실패: %v", f[1], err)
	}
	return decl, floor
}

// otherLinePresetEnv 는 반대 라인 프리셋 파일 경로를 받는 환경 변수다. 그 프리셋은 이
// 차트에 없어 작업 트리에서 읽을 수 없으므로, hack/release-prepare.sh 가
// `git show release/k8s-1.28:deploy/helm/values-k8s1.28.yaml` 결과를 임시 파일로 꺼내
// 이 변수로 넘긴다. 그 스크립트는 ref 가 없으면 건너뛰지 않고 실패로 센다.
const otherLinePresetEnv = "KCLOUD_OTHER_LINE_PRESET"

// TestChartAppVersionMatchesPresets 는 이 라인 프리셋이 Chart.yaml 의 appVersion 을
// 가리키는지 본다. 두 라인은 릴리스 번호가 따로 붙으므로(1.28 = v0.6.0-k8s1.28,
// 1.34 = v0.7.0) 접미사만 붙여 유도할 수 없다. 반대 라인 프리셋은 kubeVersion 선언과
// 실재를 맞추기 위해 이 차트에는 없다 — 짝 릴리스 번호 대조는
// release_manifest_sync_test.go 의 TestReleaseManifestPairedLineMatchesAnnotation 이 한다.
// 같은 대조를 여기 또 적으면 한쪽만 고쳐 어긋난다.
func TestChartAppVersionMatchesPresets(t *testing.T) {
	const preset134 = "../../deploy/helm/values-k8s1.34.yaml"
	app := chartAppVersion(t)
	paired := chartPairedRelease(t)

	// 이 차트는 1.31~1.34 라인이다. appVersion 에 1.28 접미가 붙으면 라인을 혼동한 것이다.
	if strings.HasSuffix(app, line128Suffix) {
		t.Fatalf("appVersion=%q — 이 차트는 1.31~1.34 라인이다(kubeVersion >=1.31.0-0). "+
			"1.28 라인은 release/k8s-1.28 브랜치가 따로 올린다", app)
	}
	if !strings.HasSuffix(paired, line128Suffix) {
		t.Errorf("paired-release=%q — 반대 라인은 반드시 %s 접미를 가져야 한다. "+
			"이 선언이 어느 라인을 가리키는지 알 수 없으면 짝 대조가 뜻을 잃는다",
			paired, line128Suffix)
	}

	if got := presetImageTag(t, preset134); got != app {
		t.Errorf("%s: image.tag=%q, want %q (이 브랜치가 만드는 이미지 = Chart.yaml appVersion)",
			preset134, got, app)
	}
}

// TestChartCarriesNoOutOfRangePreset 은 이 차트가 kubeVersion 범위 밖 라인의 프리셋을
// 싣지 않는지 본다. 싣고 있으면 선언과 실재가 어긋나고, 그 조합으로 설치하면 하한보다
// 낮은 라인의 operator 가 1.31+ CRD 를 덮어쓴다. 이름 하나를 집지 않고 deploy/helm 의
// values-k8s*.yaml 전부를 본다 — 파일 이름만 막으면 다른 이름으로 같은 어긋남이 다시 들어온다.
func TestChartCarriesNoOutOfRangePreset(t *testing.T) {
	decl, floor := chartKubeVersionFloor(t)
	entries, err := os.ReadDir("../../deploy/helm")
	if err != nil {
		t.Fatalf("deploy/helm 읽기 실패: %v", err)
	}
	var checked int
	for _, e := range entries {
		m := presetMinorRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		checked++
		got, err := minorOf(m[1])
		if err != nil {
			t.Errorf("%s: 이름의 K8s 버전 %q 를 해석하지 못했다: %v", e.Name(), m[1], err)
			continue
		}
		if got < floor {
			t.Errorf("%s 는 K8s %s 라인 프리셋인데 이 차트의 kubeVersion 은 %q 다 — "+
				"하한보다 낮은 라인의 프리셋은 그 라인 브랜치에만 둔다(1.26~1.30 은 "+
				"release/k8s-1.28)", e.Name(), m[1], decl)
		}
	}
	// 글롭이 어긋나면 0회 순회로 통과한다 — 표본이 있었는지 먼저 단언한다.
	if checked == 0 {
		t.Fatal("deploy/helm 에 values-k8s*.yaml 프리셋이 없어 한 번도 대조하지 않았다")
	}
}

// TestChartKubeVersionRejectsOtherLine 은 kubeVersion 선언이 라인 경계를 실제로 막는지
// helm 으로 확인한다. 선언을 적어 두고 helm 이 그것을 보는지 확인하지 않으면, 범위 밖
// 클러스터에 그대로 설치되고 선언은 주석만 한 값이 된다.
//
// 주의: os/exec 로 helm 을 부르므로 손으로 돌릴 때는 -count=1 을 붙일 것(캐시가 차트
// 변경을 보지 못한다). make test 는 -coverprofile 이 캐시를 끈다.
func TestChartKubeVersionRejectsOtherLine(t *testing.T) {
	bin := helmBinary(t)
	for _, c := range []struct {
		kube   string
		reject bool
	}{
		{"1.30.5", true},  // 1.26~1.30 라인 상단 — 이 차트가 거절해야 한다
		{"1.31.0", false}, // 선언 하한
		{"1.34.1", false}, // 선언 상한 없음 — 라인 최신까지 통과해야 한다
	} {
		out, err := exec.Command(bin, "template", "t", "../../deploy/helm",
			"--kube-version", c.kube).CombinedOutput()
		switch {
		case c.reject && err == nil:
			t.Errorf("--kube-version %s 가 통과했다 — kubeVersion 선언이 경계를 막지 못한다",
				c.kube)
		case c.reject && !strings.Contains(string(out), "kubeVersion"):
			t.Errorf("--kube-version %s 가 거절된 사유가 kubeVersion 이 아니다:\n%s", c.kube, out)
		case !c.reject && err != nil:
			t.Errorf("--kube-version %s 가 거절됐다 — 범위 안인데 설치가 막힌다: %v\n%s",
				c.kube, err, out)
		}
	}
}

// TestChartRejectsOtherLineImageTag 는 반대 라인 operator 태그를 render 단계에서
// 거절하는지 본다. kubeVersion 은 클러스터 버전만 보므로 1.31+ 클러스터에
// `--set image.tag=v0.6.2-k8s1.28` 로 라인을 섞는 경로를 막지 못한다. 바이너리 쪽
// 표식 대조(internal/crdapply)는 전부 닫지 못한다 — 이미 발행된 구 이미지에는 그 코드가
// 없고, 그 구 바이너리는 표식을 지우기까지 한다. render 단계 거절은 이미지 내용과
// 무관하게 install 경로까지 닫는다.
func TestChartRejectsOtherLineImageTag(t *testing.T) {
	bin := helmBinary(t)
	render := func(args ...string) (string, error) {
		out, err := exec.Command(bin, append([]string{"template", "t", "../../deploy/helm"}, args...)...).
			CombinedOutput()
		return string(out), err
	}

	out, err := render("--set", "image.tag=v0.6.2-k8s1.28")
	if err == nil {
		t.Error("반대 라인 태그가 통과했다 — 구 라인 operator 가 CRD 를 덮어쓰는 경로가 열려 있다")
	} else if !strings.Contains(out, "image.tag") {
		t.Errorf("거절 사유가 image.tag 를 가리키지 않아 운영자가 무엇을 고칠지 모른다:\n%s", out)
	}

	if out, err := render("--set", "image.tag="+chartAppVersion(t)); err != nil {
		t.Errorf("같은 라인 태그가 거절됐다: %v\n%s", err, out)
	}

	// 프리셋과 기본값 전부 통과해야 한다. 프리셋이 반대 라인 태그를 싣고 있으면 그 자체가
	// 결함이고, 여기서 잡힌다.
	presets, err := filepath.Glob("../../deploy/helm/values*.yaml")
	if err != nil || len(presets) == 0 {
		t.Fatalf("프리셋 글롭이 비었다 — 0회 순회로 통과하는 것을 막는다 (err=%v)", err)
	}
	for _, path := range presets {
		if out, err := render("-f", path); err != nil {
			t.Errorf("%s 렌더가 거절됐다: %v\n%s", path, err, out)
		}
	}
}

// TestDefaultValuesTagMatchesAppVersion 은 기본 values.yaml 도 이 브랜치의 릴리스를
// 가리키는지 본다. 기본값이 낡으면 프리셋 없이 설치하는 경로가 옛 operator 이미지를 당긴다.
func TestDefaultValuesTagMatchesAppVersion(t *testing.T) {
	app := chartAppVersion(t)
	if got := presetImageTag(t, "../../deploy/helm/values.yaml"); got != app {
		t.Errorf("values.yaml: image.tag=%q, want %q (Chart.yaml appVersion)", got, app)
	}
}

// imageCoordinateKey 는 이미지 좌표를 담는 키인지 본다. 이런 키가 프리셋에 있으면
// 그 이미지가 라인별로 갈린다는 뜻이다.
func imageCoordinateKey(k string) bool {
	switch k {
	case "repository", "tag", "image":
		return true
	}
	return strings.HasSuffix(k, "Image") || strings.HasSuffix(k, "Repository")
}

// findImageCoordinates 는 노드를 훑어 이미지 좌표 키의 경로를 모은다.
func findImageCoordinates(node any, path string, out *[]string) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	for k, v := range m {
		p := k
		if path != "" {
			p = path + "." + k
		}
		// 최상위 image 블록은 operator 이미지다 — 막지 않고 안으로 들어가 어느 좌표를
		// 건드리는지까지 본다(tag 는 허용, repository 는 아니다).
		if imageCoordinateKey(k) && p != "image" {
			*out = append(*out, p)
			continue
		}
		findImageCoordinates(v, p, out)
	}
}

// TestReleaseLinePresetsShareNonOperatorImages 는 두 프리셋이 operator 이외의 이미지를
// 재정의하지 않는지 본다. 라인별로 갈리는 산출물은 operator 하나뿐이라는 것이 이 설계의
// 전제다(k8s 라이브러리를 링크하는 것이 operator 뿐 — detector 는 k8s.io v0.29, tenstorrent
// device-plugin 은 kubelet API 만, 나머지는 k8s 의존 없음). 프리셋에 다른 이미지가 끼면
// 그 전제가 조용히 깨진다. 이미지 좌표만 막는다 — 기능 토글(api.enabled 등)은 프리셋에
// 있어야 하는 것이라 막지 않는다. 반대 라인 프리셋은 otherLinePresetEnv 로 받는다.
func TestReleaseLinePresetsShareNonOperatorImages(t *testing.T) {
	paths := []string{"../../deploy/helm/values-k8s1.34.yaml"}
	if p := os.Getenv(otherLinePresetEnv); p != "" {
		paths = append(paths, p)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", path, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s 파싱 실패: %v", path, err)
		}
		var found []string
		findImageCoordinates(doc, "", &found)
		for _, p := range found {
			if p == "image.tag" {
				continue // operator 이미지 — 이것만 라인별로 갈린다
			}
			t.Errorf("%s: %s 가 이미지를 재정의한다 — 라인별로 갈리는 것은 operator 뿐이어야 한다",
				path, p)
		}
	}
}

// ── 범위 선언 일치 ──────────────────────────────────────────────────
//
// 이 차트를 설치할 수 있는 K8s 범위를 적는 자리가 일곱이다(Chart.yaml 의 kubeVersion·
// description, values-k8s1.34.yaml 머리글, release.yaml 의 range, INSTALL.md 표,
// README.md, deploy/helm/README.md). 한 곳만 고치고 나머지를 두면 아무 데서도 실패하지
// 않는다 — 이 시험이 그 자리들을 묶어 대조한다.

// otherLineRange 는 반대 라인(1.26~1.30, release/k8s-1.28 브랜치)의 범위다. 같은
// 파일·같은 문장에 공존하는 이 범위를 이 차트의 범위로 잘못 읽지 않으려고 식별용으로
// 둔다. 이 시험은 그 라인의 문장을 바꾸지 않는다 — 그 범위가 바뀌면 이 상수도 함께
// 갱신해야 한다.
var otherLineRange = [2]int{126, 130}

// rangeTildeRe 는 "1.31~1.34" 꼴에서 두 마이너를 뽑는다. 붙임표(-)는 release.yaml 의 range
// 필드와 Chart.yaml description 의 반대 라인 절에서만 쓰여 물결표(~)만 본다. 앞을 숫자·점이
// 아닌 문자(또는 줄머리)로 막고, 뒤에 ".숫자" 가 더 붙으면(m[3]) 세 토막 버전 범위("0.8.0~0.9.0")
// 라 호출부가 건너뛴다 — 그러지 않으면 가운데 "8.0~0.9" 를 K8s 마이너 범위로 잘못 읽는다.
// 두 토막 버전 범위("0.8~0.9")는 K8s 마이너 범위와 글자 모양이 같아 구분되지 않는다 — 그
// 꼴을 문서에 쓰려면 이 정규식을 다시 봐야 한다.
var rangeTildeRe = regexp.MustCompile(`(?:^|[^\d.])(\d+\.\d+)~(\d+\.\d+)(\.\d+)?`)

// chartDescRangeRe 는 Chart.yaml description 의 "targets Kubernetes 1.31+ (verified
// through 1.34" 구절에서 하한·상한을 뽑는다. description 안에는 반대 라인 구절("for
// Kubernetes 1.26-1.30")도 있어 전체를 쓸어 보면 안 되고, 이 구절에만 붙는 글자
// ("targets Kubernetes"·"verified through")로 자리를 좁힌다.
var chartDescRangeRe = regexp.MustCompile(
	`targets Kubernetes (\d+\.\d+)\+ \(verified through (\d+\.\d+)`)

// preset134HeaderRangeRe 는 values-k8s1.34.yaml 머리글의 "K8s 1.31~1.34 릴리스 세트"
// 에서 범위를 뽑는다.
var preset134HeaderRangeRe = regexp.MustCompile(`K8s (\d+\.\d+)~(\d+\.\d+) 릴리스 세트`)

// rejectsBelowRe 는 "1.31 미만을 거절" 꼴에서 하한만 뽑는다(README.md 가 이 표현을
// 쓴다 — 상한은 이 구절에 없다).
var rejectsBelowRe = regexp.MustCompile(`(\d+\.\d+)\s*미만을 거절`)

// deployHelmReadmeKubeVersionRe 는 deploy/helm/README.md 가 Chart.yaml 의 kubeVersion
// 값을 그대로 인용하는 자리에서 하한만 뽑는다. 순수 ">=…" 만 보는 kubeVersionFloorRe 를
// 그대로 쓰면 이 파일에 다른 ">=" 가 생겨도 조용히 집을 수 있어 "kubeVersion:" 접두까지
// 본다.
var deployHelmReadmeKubeVersionRe = regexp.MustCompile(`kubeVersion:\s*">=\s*(\d+\.\d+)`)

// mustFindAllMinorPairs 는 re 가 raw 에서 찾은 전체 매치를 돌려주고, 0회면 그 자리에서
// 실패시킨다. 정규식이 아무것도 못 찾아도 통과하면, 문장이 통째로 바뀌거나 파일이
// 옮겨지는 순간 이 시험이 조용히 꺼진다.
func mustFindAllMinorPairs(t *testing.T, re *regexp.Regexp, raw []byte, where string) [][][]byte {
	t.Helper()
	ms := re.FindAllSubmatch(raw, -1)
	if len(ms) == 0 {
		t.Fatalf("%s: %q 패턴이 한 번도 매치하지 않았다 — 문장이 바뀌었거나 파일이 옮겨졌다",
			where, re.String())
	}
	return ms
}

// rangeLabel 은 minorOf 스케일의 정수를 "1.31" 꼴로 되돌린다(에러 메시지용).
func rangeLabel(minor int) string {
	return fmt.Sprintf("%d.%d", minor/100, minor%100)
}

// tildeRangesAgree 는 raw 안의 모든 "~" 범위 표기가 canon 아니면 otherLineRange 뿐이고
// canon 이 적어도 한 번 나오는지 본다. 집합 밖의 값이 하나라도 있거나 canon 이 전혀
// 없으면 그 자리가 드리프트했다는 뜻이다.
func tildeRangesAgree(t *testing.T, raw []byte, where string, canon [2]int) {
	t.Helper()
	ms := mustFindAllMinorPairs(t, rangeTildeRe, raw, where)
	var sawCanon bool
	for _, m := range ms {
		if len(m[3]) > 0 {
			continue // "1.31~1.34.0" 처럼 세 토막 — 버전 범위이고 K8s 마이너 범위가 아니다
		}
		lo, err := minorOf(string(m[1]))
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		hi, err := minorOf(string(m[2]))
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		switch got := [2]int{lo, hi}; got {
		case canon:
			sawCanon = true
		case otherLineRange:
			// 반대 라인 문장 — 이 시험이 건드리지 않는 범위다.
		default:
			t.Errorf("%s: 범위 표기 %s~%s 가 정본(%s~%s)과도 반대 라인(1.26~1.30)과도 맞지 않는다",
				where, rangeLabel(got[0]), rangeLabel(got[1]), rangeLabel(canon[0]), rangeLabel(canon[1]))
		}
	}
	if !sawCanon {
		t.Errorf("%s: 정본 범위(%s~%s)를 가리키는 문장이 없다",
			where, rangeLabel(canon[0]), rangeLabel(canon[1]))
	}
}

// floorOnlyAgrees 는 raw 안의 모든 매치가 canonLo 와 같은지 본다(">=1.31.0-0", "1.31
// 미만을 거절" 처럼 하한만 적은 자리에 쓴다).
func floorOnlyAgrees(t *testing.T, re *regexp.Regexp, raw []byte, where string, canonLo int) {
	t.Helper()
	ms := mustFindAllMinorPairs(t, re, raw, where)
	for _, m := range ms {
		got, err := minorOf(string(m[1]))
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		if got != canonLo {
			t.Errorf("%s: 하한 %s 가 정본 %s 와 다르다", where, rangeLabel(got), rangeLabel(canonLo))
		}
	}
}

// TestVersionRangeStatementsAgreeAcrossRepo 는 이 차트를 설치할 수 있는 K8s 범위를 적는
// 일곱 자리가 서로 같은 말을 하는지 본다. 정본은 둘이다 — 하한은 Chart.yaml 의 kubeVersion
// (helm 이 강제한다), 상한은 release.yaml 의 kubernetesProfiles[].range 다. 나머지 다섯
// 자리(Chart.yaml description·values-k8s1.34.yaml 머리글·INSTALL.md·README.md·
// deploy/helm/README.md)는 표기 형태가 달라도 같은 마이너 번호를 가리켜야 한다.
// 깨는 뮤테이션: 일곱 자리 중 어느 한 곳의 숫자만 바꿔도 실패한다.
func TestVersionRangeStatementsAgreeAcrossRepo(t *testing.T) {
	_, canonLo := chartKubeVersionFloor(t) // 하한 정본: Chart.yaml 의 kubeVersion

	m := loadReleaseManifest(t)
	ownedChart := chartVersionField(t)
	var canonHi int
	var sawOwned bool
	for _, p := range m.KubernetesProfiles {
		if p.ChartVersion != ownedChart {
			continue
		}
		lo, hi, err := parseMinorRange(p.Range)
		if err != nil {
			t.Fatalf("release.yaml 프로파일 %q 의 range 해석 실패: %v", p.Range, err)
		}
		if lo != canonLo {
			t.Errorf("release.yaml 프로파일 %q 의 하한 %s 가 Chart.yaml kubeVersion 하한 %s 와 다르다",
				p.Range, rangeLabel(lo), rangeLabel(canonLo))
		}
		canonHi = hi // 확인된 상한 정본: release.yaml 의 range
		sawOwned = true
		break
	}
	if !sawOwned {
		t.Fatalf("release.yaml 에 이 브랜치 차트 버전 %q 의 프로파일이 없어 상한 정본을 구하지 못했다",
			ownedChart)
	}
	canon := [2]int{canonLo, canonHi}

	readRaw := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", path, err)
		}
		return raw
	}

	// Chart.yaml description: "... targets Kubernetes 1.31+ (verified through 1.34 ..."
	for _, dm := range mustFindAllMinorPairs(t, chartDescRangeRe,
		readRaw("../../deploy/helm/Chart.yaml"), "Chart.yaml description") {
		lo, err := minorOf(string(dm[1]))
		if err != nil {
			t.Fatalf("Chart.yaml description: %v", err)
		}
		hi, err := minorOf(string(dm[2]))
		if err != nil {
			t.Fatalf("Chart.yaml description: %v", err)
		}
		if lo != canon[0] || hi != canon[1] {
			t.Errorf("Chart.yaml description 의 범위 %s+..%s 가 정본 %s~%s 와 다르다",
				rangeLabel(lo), rangeLabel(hi), rangeLabel(canon[0]), rangeLabel(canon[1]))
		}
	}

	// values-k8s1.34.yaml 머리글: "K8s 1.31~1.34 릴리스 세트"
	for _, pm := range mustFindAllMinorPairs(t, preset134HeaderRangeRe,
		readRaw("../../deploy/helm/values-k8s1.34.yaml"), "values-k8s1.34.yaml 머리글") {
		lo, err := minorOf(string(pm[1]))
		if err != nil {
			t.Fatalf("values-k8s1.34.yaml 머리글: %v", err)
		}
		hi, err := minorOf(string(pm[2]))
		if err != nil {
			t.Fatalf("values-k8s1.34.yaml 머리글: %v", err)
		}
		if lo != canon[0] || hi != canon[1] {
			t.Errorf("values-k8s1.34.yaml 머리글의 범위 %s~%s 가 정본 %s~%s 와 다르다",
				rangeLabel(lo), rangeLabel(hi), rangeLabel(canon[0]), rangeLabel(canon[1]))
		}
	}

	// INSTALL.md 표: "| 1.31~1.34 | …"
	tildeRangesAgree(t, readRaw("../../deploy/helm/INSTALL.md"), "deploy/helm/INSTALL.md", canon)

	// README.md: "# Kubernetes 1.31~1.34" 등 "~" 전부 + "1.31 미만을 거절"
	readmeRaw := readRaw("../../README.md")
	tildeRangesAgree(t, readmeRaw, "README.md", canon)
	floorOnlyAgrees(t, rejectsBelowRe, readmeRaw, "README.md(미만을 거절)", canon[0])

	// deploy/helm/README.md: "Kubernetes 1.31~1.34" 등 "~" 전부 + kubeVersion 인용
	helmReadmeRaw := readRaw("../../deploy/helm/README.md")
	tildeRangesAgree(t, helmReadmeRaw, "deploy/helm/README.md", canon)
	floorOnlyAgrees(t, deployHelmReadmeKubeVersionRe, helmReadmeRaw,
		"deploy/helm/README.md(kubeVersion 인용)", canon[0])
}
