// ============================================================
// chart_default_images_test.go: 코드 기본 벤더 이미지와 차트 기본값이 같은지 대조한다
// 상세: 정책 CR 이 이미지를 비우면 코드 기본값이, helm 설치는 차트 값이 쓰인다. 둘이 다르면
//       설치 경로에 따라 다른 벤더 이미지가 뜬다.
// 생성일: 2026-10-14 | 수정일: 2026-10-14
// ============================================================

package controller

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// chartValues 는 deploy/helm/values.yaml 을 맵으로 읽는다.
func chartValues(t *testing.T) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/helm/values.yaml")
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	var v map[string]interface{}
	if err := yaml.Unmarshal(raw, &v); err != nil {
		t.Fatalf("values.yaml 해석 실패: %v", err)
	}
	return v
}

// valueAt 은 점으로 나눈 경로의 문자열 값을 읽는다. 없거나 문자열이 아니면 시험을 멈춘다.
func valueAt(t *testing.T, v map[string]interface{}, path string) string {
	t.Helper()
	var cur interface{} = v
	for _, k := range strings.Split(path, ".") {
		m, ok := cur.(map[string]interface{})
		if !ok {
			t.Fatalf("values.yaml %s: %q 의 상위가 맵이 아니다", path, k)
		}
		if cur, ok = m[k]; !ok {
			t.Fatalf("values.yaml 에 %s 가 없다", path)
		}
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("values.yaml %s 가 문자열이 아니다: %v", path, cur)
	}
	return s
}

func TestChartDefaultImagesMatchCode(t *testing.T) {
	v := chartValues(t)
	ref := func(prefix string) string {
		return valueAt(t, v, prefix+".upstream") + "/" + valueAt(t, v, prefix+".repository") + ":" + valueAt(t, v, prefix+".tag")
	}
	rngdDP := valueAt(t, v, "furiosa.rngd.devicePluginUpstream") + "/" +
		valueAt(t, v, "furiosa.rngd.devicePluginRepository") + ":" + valueAt(t, v, "furiosa.rngd.devicePluginTag")
	cases := []struct{ name, code, chart string }{
		{"Furiosa RNGD device-plugin", furiosaRngdDevicePluginImageDefault, rngdDP},
		{"Furiosa RNGD DRA driver", draDriverImageFuriosaDefault, ref("furiosa.rngd.dra")},
		{"Rebellions DRA driver", draDriverImageRebellionsDefault, ref("rebellions.dra")},
		{"NVIDIA DRA driver", draDriverImageNvidiaDefault, ref("nvidia.dra")},
		{"Tenstorrent DRA driver", draDriverImageTenstorrentDefault, ref("tenstorrent.dra")},
		{"Tenstorrent fabric manager", draFMImageTenstorrentDefault, ref("tenstorrent.dra.fabricManager")},
		{"MPS control daemon", mpsControlImageDefault, ref("acpp.mpsControlDaemon")},
		{"DCGM exporter", dcgmExporterImageDefault, valueAt(t, v, "nvidia.dcgmExporter.image")},
	}
	for _, c := range cases {
		if c.code != c.chart {
			t.Errorf("%s: 코드 기본값 %q, 차트 기본값 %q", c.name, c.code, c.chart)
		}
	}
}
