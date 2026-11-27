// ============================================================
// chart_advertise_test.go: 광고 주체 축이 차트에서 실제로 노출되는지 고정한다.
// 상세: advertiseBy 는 벤더 블록마다 inline 으로 있다. 템플릿이 그 값을 렌더하지 않으면
// 값 파일에 적어도 CR 에 닿지 않는다. 템플릿 참조와 기본 values 의 키 존재까지 보고,
// 렌더 결과가 API 스키마와 맞는지는 helm 이 필요해 여기서 보지 않는다.
// 생성일: 2026-08-07 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"os"
	"strings"
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// chartAdvertiseVendors 는 광고 축을 가진 벤더의 **values 경로**다. 컨트롤러가 읽는
// advertiseVendors() 맵의 키와 1:1 이어야 한다 — 그쪽 키는 벤더 이름이고 이쪽은
// 값 파일에서의 위치라 이름이 다를 수 있다(rngd 는 furiosa 아래에 있다).
var chartAdvertiseVendors = []string{
	"nvidia",
	"furiosa",
	"furiosa.rngd",
	"tenstorrent",
	"rebellions",
}

// TestAdvertiseVendorCountMatchesChart 는 컨트롤러가 실제로 읽는 벤더 수와 차트
// 노출 목록의 길이가 같은지 본다. 벤더를 늘리면서 차트 노출을 빠뜨리면 여기서 걸린다.
func TestAdvertiseVendorCountMatchesChart(t *testing.T) {
	got := len(advertiseVendors(&npuv1alpha1.NPUClusterPolicy{}))
	if got != len(chartAdvertiseVendors) {
		t.Errorf("컨트롤러가 읽는 벤더 %d 개, 차트 노출 목록 %d 개 — "+
			"벤더를 늘렸다면 clusterpolicy.yaml·values.yaml·이 목록을 함께 고칠 것",
			got, len(chartAdvertiseVendors))
	}
}

// TestChartExposesAdvertiseBy 는 각 벤더의 advertiseBy 가 템플릿과 기본 values 에
// 모두 있는지 본다. 둘 중 하나만 있으면 값을 적어도 CR 에 닿지 않거나, 값 파일에
// 적을 자리가 없다.
func TestChartExposesAdvertiseBy(t *testing.T) {
	tpl, err := os.ReadFile("../../deploy/helm/templates/clusterpolicy.yaml")
	if err != nil {
		t.Fatalf("clusterpolicy.yaml 읽기 실패: %v", err)
	}
	vals, err := os.ReadFile("../../deploy/helm/values.yaml")
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	body := string(tpl)
	for _, v := range chartAdvertiseVendors {
		want := ".Values." + v + ".advertiseBy"
		if !strings.Contains(body, want) {
			t.Errorf("clusterpolicy.yaml 이 %s 를 렌더하지 않는다 — "+
				"값 파일에 적어도 CR 에 닿지 않는다", want)
		}
	}
	// 기본 values 에 키가 몇 개 선언돼 있는지로 본다(중첩 경로라 키 이름은 같다).
	// 주석 줄에도 같은 문자열이 나오므로 실제 선언만 센다.
	declared := 0
	for _, line := range strings.Split(string(vals), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") && strings.HasPrefix(trimmed, "advertiseBy:") {
			declared++
		}
	}
	if declared != len(chartAdvertiseVendors) {
		t.Errorf("values.yaml 의 advertiseBy 선언이 %d 개, 벤더는 %d 개다",
			declared, len(chartAdvertiseVendors))
	}
}

// TestDRAProfileOnlyOverridesAdvertiseBy 는 v0.8.0 프로파일이 광고 축 외의 것을
// 바꾸지 않는지 본다. 이 파일이 v0.7.0 과의 유일한 차이라는 것이 세트 정의다 —
// 다른 값이 끼면 두 세트가 광고 축 말고도 달라진다.
func TestDRAProfileOnlyOverridesAdvertiseBy(t *testing.T) {
	const path = "../../deploy/helm/values-k8s1.34-dra.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s 읽기 실패: %v", path, err)
	}
	lines := strings.Split(string(raw), "\n")
	leafs := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// "key: value" 형태(값이 있는 줄)만 본다. 블록 헤더("nvidia:")는 건너뛴다.
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		leafs = append(leafs, k)
	}
	if len(leafs) == 0 {
		t.Fatal("DRA 프로파일이 아무것도 설정하지 않는다 — 그러면 v0.7.0 과 같은 것이다")
	}
	for _, k := range leafs {
		if k != "advertiseBy" {
			t.Errorf("%s 가 %q 를 설정한다 — 이 프로파일은 광고 축만 바꿔야 한다", path, k)
		}
	}
}
