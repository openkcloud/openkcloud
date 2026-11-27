// ============================================================
// chart_image_manifest_test.go: 차트가 렌더하는 kcloud 이미지가 image-manifest.yaml 에 같은 태그로 있는지 고정한다.
// 상세: air-gap 사이트는 image-manifest 대로 미러한다. 차트 기본 태그만 올리고 목록을 그대로 두면 미러에 없는
// 태그를 당겨 ImagePullBackOff 가 난다. exporter 는 기본값이 꺼짐이라 켜서 렌더한다(1.31~1.34 프리셋이 켠다).
// 생성일: 2026-10-15
// ============================================================
package controller

import (
	"os"
	"regexp"
	"testing"

	"sigs.k8s.io/yaml"
)

var kcloudImageRefRe = regexp.MustCompile(`ghcr\.io/openkcloud/([a-z0-9._/-]+):([A-Za-z0-9._-]+)`)

func TestChartKcloudImagesListedInImageManifest(t *testing.T) {
	refs := kcloudImageRefRe.FindAllStringSubmatch(helmTemplate(t, "--set", "furiosa.exporter.enabled=true"), -1)
	if len(refs) == 0 {
		t.Fatal("렌더에서 ghcr.io/openkcloud 이미지를 하나도 못 찾았다 — 0회 대조로 통과하는 것을 막는다")
	}
	raw, err := os.ReadFile("../../deploy/integration/image-manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Kcloud []struct {
			Image string `json:"image"`
			Tag   string `json:"tag"`
		} `json:"kcloud"`
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{}
	for _, e := range m.Kcloud {
		tags[e.Image] = e.Tag
	}
	for _, r := range refs {
		name, tag := r[1], r[2]
		want, ok := tags[name]
		switch {
		case !ok:
			t.Errorf("%s 가 image-manifest.yaml 의 kcloud 절에 없다", name)
		case want != tag:
			t.Errorf("%s: 차트 렌더 태그 %q, image-manifest 태그 %q — 기본 태그를 올렸으면 목록도 함께 올릴 것", name, tag, want)
		}
	}
}
