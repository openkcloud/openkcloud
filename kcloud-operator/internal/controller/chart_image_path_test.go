// ============================================================
// chart_image_path_test.go: values 의 이미지 경로가 registry 와 조립됐을 때 발행 규약에
// 맞는 경로가 되는지 고정한다.
// 상세: kcloud-operator.image 는 "<global.registry>/<repo>:<tag>" 로 조립하므로 repo 는
// 중간 경로 없는 이미지 이름 하나여야 한다. values 파일과 Go 기본값의 일관성만 보고, 그
// 태그가 레지스트리에 실재하는지는 hack/image-tag-check.sh 의 몫이라 여기서 보지 않는다.
// 생성일: 2026-08-07 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// valuesScalar 는 values.yaml 에서 키 하나의 스칼라 값을 읽는다. 들여쓰기 깊이는
// 보지 않으므로 파일 전체에서 유일한 키에만 쓴다.
func valuesScalar(t *testing.T, key string) string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/helm/values.yaml")
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(\S+)\s*$`)
	m := re.FindAllSubmatch(raw, -1)
	if len(m) == 0 {
		t.Fatalf("values.yaml 에 %s 가 없다", key)
	}
	if len(m) > 1 {
		t.Fatalf("values.yaml 의 %s 가 %d 곳이다 — 이 헬퍼로는 못 고른다", key, len(m))
	}
	return strings.Trim(string(m[0][1]), `"'`)
}

// TestFuriosaUnifiedImageFollowsPublishConvention 는 통합 Furiosa device-plugin 이미지가
// 발행 규약에 맞는 이름 하나인지, 그리고 차트 값과 Go 기본값이 같은 문자열인지 본다.
// 둘이 갈리면 차트로 설치한 클러스터와 이미지를 적지 않은 CR 이 서로 다른 경로를 당긴다.
func TestFuriosaUnifiedImageFollowsPublishConvention(t *testing.T) {
	repo, tag, ok := strings.Cut(furiosaUnifiedImgDefault, ":")
	if !ok {
		t.Fatalf("furiosaUnifiedImgDefault = %q — <이미지>:<태그> 가 아니다", furiosaUnifiedImgDefault)
	}
	if strings.Contains(repo, "/") {
		t.Errorf("이미지 이름이 %q 다 — 발행 경로는 <registry>/<images.yaml 키>:<태그> 라 "+
			"중간 경로를 넣을 수 없다(발행본 없는 경로가 된다)", repo)
	}
	raw, err := os.ReadFile("../../deploy/helm/values.yaml")
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	for _, want := range []string{
		`devicePluginRepository: "` + repo + `"`,
		`devicePluginTag: "` + tag + `"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("values.yaml 에 없다: %s\n"+
				"차트 값과 furiosaUnifiedImgDefault 가 갈렸다 — 두 자리를 같이 고칠 것", want)
		}
	}
}

// TestOtherRepoPathsAreRegistryRelative 는 repository 계열 필드가 registry 상대경로를
// 유지하는지 본다. global.registry 가 이미 프로젝트 경로까지 가리키므로 값에 kcloud/ 를
// 또 붙이면 그쪽은 어느 레지스트리에도 없는 경로가 된다.
func TestOtherRepoPathsAreRegistryRelative(t *testing.T) {
	for _, key := range []string{"kubectlRepository"} {
		got := valuesScalar(t, key)
		if strings.HasPrefix(got, "kcloud/") {
			t.Errorf("%s = %q — registry 가 이미 kcloud 프로젝트까지 가리키므로 접두가 겹친다", key, got)
		}
	}
	// repository·devicePluginRepository 는 벤더마다 반복되므로 한꺼번에 본다.
	raw, err := os.ReadFile("../../deploy/helm/values.yaml")
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s+[A-Za-z]*[Rr]epository:\s*"?(kcloud/\S+?)"?\s*$`)
	if m := re.FindSubmatch(raw); m != nil {
		t.Errorf("repository 에 kcloud/ 접두가 붙었다: %q — registry 와 겹친다", string(m[1]))
	}
}
