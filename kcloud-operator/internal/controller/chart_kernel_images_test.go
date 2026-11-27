// ============================================================
// chart_kernel_images_test.go: 네 벤더 모두 커널별 사전 컴파일 이미지 맵을 차트로 줄 수 있는지 고정한다.
// 상세: 이 맵이 렌더되지 않는 벤더는 helm 으로 사전 컴파일 이미지를 설정할 수 없고, 라이브
// 오브젝트를 직접 고쳐야 한다 — 그 수정은 다음 업그레이드에서 사라진다.
// 생성일: 2026-09-24 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// kernelImagesVendors 는 DriverInstallPolicy 를 렌더하는 벤더 키다. 차트에 벤더가 늘면 여기에 더한다.
var kernelImagesVendors = []string{"furiosa", "nvidia", "rngd", "tenstorrent"}

// 템플릿이 네 벤더 모두에서 kernelImages 를 렌더해야 한다.
func TestChartRendersKernelImagesForEveryVendor(t *testing.T) {
	tpl := readChartFile(t, "templates/driverinstallpolicy.yaml")
	const emit = "kernelImages: {{ toYaml . | nindent 6 }}"
	for _, v := range kernelImagesVendors {
		guard := "{{- with .Values.driverInstallPolicies." + v + ".driver.kernelImages }}"
		gi := strings.Index(tpl, guard)
		if gi < 0 {
			t.Errorf("%s 블록에 kernelImages 렌더 가드가 없다: %q", v, guard)
			continue
		}
		// 가드를 닫는 {{- end }} 까지로 구간을 한정해 emit 줄을 센다. 파일 전체
		// 합계만 보면 emit 이 엉뚱한 벤더 블록에 끼어 있어도(가드 수·전체 emit
		// 수는 그대로라) 놓친다 — 이 구간 검사가 그 오배치를 잡는다.
		body := tpl[gi+len(guard):]
		ei := strings.Index(body, "{{- end }}")
		if ei < 0 {
			t.Errorf("%s 가드를 닫는 {{- end }} 를 찾지 못했다", v)
			continue
		}
		if got := strings.Count(body[:ei], emit); got != 1 {
			t.Errorf("%s 가드 구간의 kernelImages emit 줄 %d개, want 1개", v, got)
		}
	}
}

// values 의 네 벤더 driver 맵에 kernelImages 키가 있어야 한다(기본은 빈 맵).
func TestChartValuesDeclareKernelImagesForEveryVendor(t *testing.T) {
	raw := readChartFile(t, "values.yaml")
	var v struct {
		DriverInstallPolicies map[string]struct {
			Driver map[string]interface{} `json:"driver"`
		} `json:"driverInstallPolicies"`
	}
	if err := yaml.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("values.yaml 파싱 실패: %v", err)
	}
	for _, name := range kernelImagesVendors {
		vendor, ok := v.DriverInstallPolicies[name]
		if !ok {
			t.Fatalf("values 에 driverInstallPolicies.%s 가 없다", name)
		}
		ki, ok := vendor.Driver["kernelImages"]
		if !ok {
			t.Errorf("driverInstallPolicies.%s.driver 에 kernelImages 키가 없다", name)
			continue
		}
		m, ok := ki.(map[string]interface{})
		if !ok {
			t.Errorf("driverInstallPolicies.%s.driver.kernelImages 가 맵이 아니다: %T", name, ki)
			continue
		}
		if len(m) != 0 {
			t.Errorf("driverInstallPolicies.%s.driver.kernelImages 기본값이 비어 있지 않다: %v", name, m)
		}
	}
}
