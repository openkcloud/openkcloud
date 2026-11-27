// ============================================================
// release_manifest_sync_test.go: deploy/integration/release.yaml 이 차트와 어긋나지 않는지 고정한다.
// 상세: release.yaml 은 Magnum 같은 외부 자동화가 읽는 진입점이다. chartVersion·operatorTag 가
// 실제 차트와 어긋나면 외부 자동화가 없는 차트를 당기거나 잘못된 이미지를 배포한다.
// 생성일: 2026-09-17 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

type releaseProfile struct {
	Range        string `json:"range"`
	ChartVersion string `json:"chartVersion"`
	ValuesFile   string `json:"valuesFile"`
	ValuesURL    string `json:"valuesURL"`
	OperatorTag  string `json:"operatorTag"`
}

type releaseManifest struct {
	KubernetesProfiles []releaseProfile `json:"kubernetesProfiles"`
}

// loadReleaseManifest 는 release.yaml 을 구조로 읽는다.
func loadReleaseManifest(t *testing.T) releaseManifest {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/integration/release.yaml")
	if err != nil {
		t.Fatalf("release.yaml 을 읽지 못했다: %v", err)
	}
	var m releaseManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("release.yaml 파싱 실패: %v", err)
	}
	if len(m.KubernetesProfiles) == 0 {
		t.Fatal("kubernetesProfiles 가 비었다 — 이 시험이 항진명제가 된다")
	}
	return m
}

// chartVersionField 는 deploy/helm/Chart.yaml 의 version 필드를 읽는다. chartAppVersion
// 과 달리 appVersion 이 아니라 차트 자체 버전이다 — release.yaml 의 chartVersion 이
// 대조해야 할 값.
func chartVersionField(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/helm/Chart.yaml")
	if err != nil {
		t.Fatalf("Chart.yaml 읽기 실패: %v", err)
	}
	re := regexp.MustCompile(`(?m)^version:\s*(\S+)\s*$`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatal("Chart.yaml 에 version 이 없다")
	}
	return strings.Trim(string(m[1]), `"'`)
}

// TestReleaseManifestMatchesChart 는 이 브랜치가 소유한 프로파일의 chartVersion·
// operatorTag 가 Chart.yaml 과 일치하는지 본다.
// 깨는 뮤테이션: Chart.yaml 의 version 을 올리고 release.yaml 을 그대로 두면 실패한다.
func TestReleaseManifestMatchesChart(t *testing.T) {
	m := loadReleaseManifest(t)
	wantChart := chartVersionField(t)
	wantTag := chartAppVersion(t)

	var owned *releaseProfile
	for i := range m.KubernetesProfiles {
		if m.KubernetesProfiles[i].ChartVersion == wantChart {
			owned = &m.KubernetesProfiles[i]
			break
		}
	}
	if owned == nil {
		t.Fatalf("release.yaml 에 이 브랜치 차트 버전 %q 를 쓰는 프로파일이 없다 — "+
			"Chart.yaml 을 올리고 release.yaml 을 갱신하지 않았는지 확인할 것", wantChart)
	}
	if owned.OperatorTag != wantTag {
		t.Errorf("프로파일 %q 의 operatorTag = %q, Chart.yaml 의 appVersion = %q. "+
			"차트를 올릴 때 release.yaml 의 operatorTag 도 함께 올릴 것",
			owned.Range, owned.OperatorTag, wantTag)
	}
}

// TestReleaseManifestPairedLineMatchesAnnotation 은 다른 릴리스 라인의 프로파일이
// Chart.yaml 의 paired-release 선언과 일치하는지 본다. 그 라인의 차트는 다른 브랜치에
// 있으므로 값 자체는 대조할 수 없고, 짝 선언이 유일한 근거다. 짝 라인은 operatorTag
// 접미(line128Suffix)로 집는다 — "소유 프로파일이 아닌 것"으로 고르면 소유 판정이 깨지는
// 순간 이 브랜치 자신의 프로파일이 짝 라인으로 잘못 재분류된다.
// 깨는 뮤테이션: Chart.yaml 의 paired-release 만 올리고 release.yaml 을 두면 실패한다.
func TestReleaseManifestPairedLineMatchesAnnotation(t *testing.T) {
	m := loadReleaseManifest(t)
	paired := chartPairedRelease(t)
	pairedIs128 := strings.HasSuffix(paired, line128Suffix)

	var checked int
	for _, p := range m.KubernetesProfiles {
		if strings.HasSuffix(p.OperatorTag, line128Suffix) != pairedIs128 {
			continue
		}
		checked++
		if p.OperatorTag != paired {
			t.Errorf("프로파일 %q 의 operatorTag = %q 인데 Chart.yaml 의 "+
				"npu.ai/paired-release = %q 다 — 둘 중 한쪽이 갱신되지 않았다. "+
				"어느 쪽이 정본인지는 짝 라인 브랜치의 Chart.yaml 이 정한다",
				p.Range, p.OperatorTag, paired)
		}
	}
	if checked == 0 {
		t.Fatalf("release.yaml 에 짝 라인(paired-release %q) 프로파일이 없다 — "+
			"대조가 0회라 이 시험이 항진명제가 된다", paired)
	}
}

// valuesURLRe 는 valuesURL 에서 커밋 SHA 와 저장소 내 경로를 뽑는다. 브랜치 주소는
// 같은 주소가 다른 내용을 돌려주므로 고정값으로 받지 않는다(valuesBrowseURL 은 사람용).
var valuesURLRe = regexp.MustCompile(
	`^https://raw\.githubusercontent\.com/[^/]+/[^/]+/([0-9a-f]{40})/(\S+)$`)

// repoIsShallow 는 이 체크아웃이 shallow clone 인지 본다. shallow 면 고정 커밋의 객체가
// 애초에 없어(GitHub Actions 의 checkout 기본값이 fetch-depth: 1) 고정 내용을 대조할
// 수 없다 — 핀이 틀린 것과 구별해야 하므로 이 경우만 대조를 건너뛴다.
func repoIsShallow(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--is-shallow-repository").Output()
	if err != nil {
		t.Fatalf("git rev-parse 실패 — 고정 커밋을 대조할 수 없다: %v", err)
	}
	return strings.TrimSpace(string(out)) == labelValueTrue
}

// TestReleaseManifestPinnedValuesMatchOperatorTag 는 외부 자동화가 -f 로 실제로 읽는
// valuesURL 이 그 프로파일의 operatorTag 와 같은 이미지를 가리키는지 본다.
// chartVersion·operatorTag·valuesFile 만 대조하면 SHA 를 옛 커밋에 둔 채 나머지를 올려도
// 통과하고, 자동화는 새 차트에 옛 프리셋의 image.tag 를 먹인다 — release.yaml 이 막아야
// 하는 사고다.
// 깨는 뮤테이션: valuesURL 의 SHA 를 image.tag 가 다른 커밋으로 바꾸면 실패한다.
func TestReleaseManifestPinnedValuesMatchOperatorTag(t *testing.T) {
	m := loadReleaseManifest(t)
	ownedChart := chartVersionField(t)
	shallow := repoIsShallow(t)

	var checked int
	for _, p := range m.KubernetesProfiles {
		if p.ChartVersion != ownedChart {
			continue // 짝 라인 프리셋을 담은 커밋은 그 브랜치 이력에 있다
		}
		checked++
		sub := valuesURLRe.FindStringSubmatch(p.ValuesURL)
		if sub == nil {
			t.Errorf("프로파일 %q 의 valuesURL 이 40자 커밋 SHA 로 고정돼 있지 않다: %q",
				p.Range, p.ValuesURL)
			continue
		}
		sha, path := sub[1], sub[2]
		if want := "deploy/helm/" + p.ValuesFile; path != want {
			t.Errorf("프로파일 %q: valuesURL 은 %q 를 가리키는데 valuesFile 은 %q 다",
				p.Range, path, want)
		}
		if shallow {
			t.Logf("프로파일 %q: shallow clone 이라 고정 커밋 %s 의 내용을 대조하지 못했다",
				p.Range, sha[:8])
			continue
		}
		blob, err := exec.Command("git", "show", sha+":"+path).Output()
		if err != nil {
			t.Errorf("프로파일 %q: 고정 커밋 %s 의 %s 를 읽지 못했다 — 자동화가 읽는 값을 "+
				"검증할 수 없다는 것 자체가 알릴 사실이다: %v", p.Range, sha[:8], path, err)
			continue
		}
		if got := imageTagIn(t, blob, p.ValuesURL); got != p.OperatorTag {
			t.Errorf("프로파일 %q: 고정 커밋 %s 의 %s 는 image.tag=%q 인데 operatorTag 는 "+
				"%q 다. 프리셋을 push 한 뒤 valuesURL 의 SHA 를 다시 고정할 것",
				p.Range, sha[:8], path, got, p.OperatorTag)
		}
	}
	if checked == 0 {
		t.Fatalf("이 브랜치 차트 버전 %q 를 쓰는 프로파일이 없어 한 번도 대조하지 않았다",
			ownedChart)
	}
}

// TestReleaseManifestValuesFilesExist 는 프로파일이 가리키는 프리셋이 실재하는지 본다.
// 이 브랜치가 소유한 프로파일만 본다 — 다른 라인의 프리셋은 그 브랜치에 있다.
func TestReleaseManifestValuesFilesExist(t *testing.T) {
	m := loadReleaseManifest(t)
	ownedChart := chartVersionField(t)
	var checked int
	for _, p := range m.KubernetesProfiles {
		if p.ChartVersion != ownedChart {
			continue
		}
		checked++
		path := "../../deploy/helm/" + p.ValuesFile
		if _, err := os.Stat(path); err != nil {
			t.Errorf("프로파일 %q 의 valuesFile %q 가 없다: %v", p.Range, p.ValuesFile, err)
		}
	}
	// 필터가 어긋나면 0회 순회로 통과한다 — 표본이 있었는지 먼저 단언한다.
	if checked == 0 {
		t.Fatalf("이 브랜치 차트 버전 %q 를 쓰는 프로파일이 없어 한 번도 확인하지 않았다",
			ownedChart)
	}
}

// TestReleaseManifestRangesAreWellFormedAndDisjoint 는 프로파일의 K8s 범위가 형식에 맞고
// 서로 겹치지 않는지 본다. 범위가 겹치면 Add-on 이 같은 클러스터 버전에 두 프로파일을
// 모두 맞다고 보아 어느 차트를 설치할지 모호해진다. 이 파일의 다른 시험들은 프로파일
// "안" 의 값을 보고, 이 시험은 프로파일 "사이" 를 본다.
// 깨는 뮤테이션: 한쪽 range 를 다른 쪽과 겹치게 바꾸면 실패한다.
func TestReleaseManifestRangesAreWellFormedAndDisjoint(t *testing.T) {
	m := loadReleaseManifest(t)
	if len(m.KubernetesProfiles) < 2 {
		t.Fatalf("프로파일이 %d개라 겹침을 판정할 수 없다 — 이 시험의 전제가 깨졌다",
			len(m.KubernetesProfiles))
	}

	type span struct {
		label  string
		lo, hi int
	}
	spans := make([]span, 0, len(m.KubernetesProfiles))
	for _, p := range m.KubernetesProfiles {
		lo, hi, err := parseMinorRange(p.Range)
		if err != nil {
			t.Errorf("프로파일 %q 의 range 형식이 잘못됐다: %v", p.Range, err)
			continue
		}
		if lo > hi {
			t.Errorf("프로파일 %q 의 하한이 상한보다 크다", p.Range)
			continue
		}
		spans = append(spans, span{label: p.Range, lo: lo, hi: hi})
	}

	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			a, b := spans[i], spans[j]
			if a.lo <= b.hi && b.lo <= a.hi {
				t.Errorf("범위 %q 와 %q 가 겹친다 — 같은 클러스터 버전에 두 프로파일이 모두 맞아 "+
					"Add-on 이 어느 차트를 설치할지 정할 수 없다", a.label, b.label)
			}
		}
	}
}

// parseMinorRange 는 "1.31-1.34" 를 비교 가능한 정수 쌍으로 바꾼다(1.31 → 131).
func parseMinorRange(r string) (int, int, error) {
	parts := strings.Split(r, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("형식이 %q 인데 \"<하한>-<상한>\" 이어야 한다", r)
	}
	lo, err := minorOf(parts[0])
	if err != nil {
		return 0, 0, err
	}
	hi, err := minorOf(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return lo, hi, nil
}

// minorOf 는 "1.31" 을 131 로 바꾼다. 마이너까지만 비교하면 충분하다 — 프로파일이 패치를 적지 않는다.
func minorOf(v string) (int, error) {
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d.%d", &major, &minor); err != nil {
		return 0, fmt.Errorf("버전 %q 를 해석하지 못했다: %w", v, err)
	}
	return major*100 + minor, nil
}
