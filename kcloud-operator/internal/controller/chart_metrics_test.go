// ============================================================
// chart_metrics_test.go: metrics 인자가 값에 따라 켜지고 기본은 꺼져 있는지 고정한다.
// 상세: 제어면 비용 측정은 controller-runtime metrics 를 스크레이프한다. 통로가 차트에서
// 안 열리면 측정이 불가능하고, 기본으로 열리면 라이브에 인증 없는 지표 포트가 생긴다.
// 생성일: 2026-08-10 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func readChartFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/helm/" + rel)
	if err != nil {
		t.Fatalf("%s 읽기 실패: %v", rel, err)
	}
	return string(raw)
}

// metricsValues 는 values.yaml 의 metrics 블록만 파싱한다. 문자열 앵커(strings.Index)는
// "metrics:" 를 설명 주석줄에서도 찾아내므로, 주석과 실제 키 사이에 빈 줄 하나만 들어와도
// 엉뚱한 텍스트를 붙잡는다 — YAML 로 실제 파싱해 그 함정을 없앤다.
type metricsValues struct {
	Metrics *struct {
		Enabled bool `json:"enabled"`
		Port    int  `json:"port"`
		Secure  bool `json:"secure"`
	} `json:"metrics"`
}

// 기본값이 꺼져 있어야 한다. 켜져 있으면 라이브에 지표 포트가 생긴다.
func TestChartMetricsDisabledByDefault(t *testing.T) {
	v := readChartFile(t, "values.yaml")
	var cv metricsValues
	if err := yaml.Unmarshal([]byte(v), &cv); err != nil {
		t.Fatalf("values.yaml 파싱 실패: %v", err)
	}
	if cv.Metrics == nil {
		t.Fatal("values.yaml 에 metrics 블록이 없다")
	}
	if cv.Metrics.Enabled {
		t.Errorf("metrics 기본값이 false 가 아니다: %+v", *cv.Metrics)
	}
}

// 템플릿이 그 값으로 인자를 렌더해야 한다. 조건과 인자 둘 다 있어야 의미가 있다.
func TestChartRendersMetricsArgsWhenEnabled(t *testing.T) {
	d := readChartFile(t, "templates/deployment.yaml")
	for _, want := range []string{
		"if .Values.metrics.enabled",
		"--metrics-bind-address=:{{ .Values.metrics.port }}",
		"--metrics-secure={{ .Values.metrics.secure }}",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("deployment.yaml 에 %q 가 없다", want)
		}
	}
}

// TestChartMetricsServiceNeedsBothSwitches 는 Service 가 두 스위치를 모두 요구하는지 본다.
//
// metrics 자체가 꺼진 채 Service 만 만들면 아무것도 듣지 않는 포트를 가리키는 Service 가 선다.
// 깨는 뮤테이션: metrics-service.yaml 의 and 조건을 or 로 바꾸면 첫 단언이 실패한다.
func TestChartMetricsServiceNeedsBothSwitches(t *testing.T) {
	// "kind: Service\n" — 개행까지 포함해야 한다. "kind: Service" 만 찾으면 기본
	// 렌더되는 "kind: ServiceAccount" 를 오탐한다(접두어가 같다).
	onlyService := helmTemplate(t, "--set", "metrics.service.enabled=true")
	if strings.Contains(onlyService, "kind: Service\n") {
		t.Error("metrics 가 꺼져 있는데 지표 Service 가 렌더됐다 — 듣는 프로세스가 없는 포트를 가리킨다")
	}
	both := helmTemplate(t, "--set", "metrics.enabled=true", "--set", "metrics.service.enabled=true")
	if !strings.Contains(both, "kind: Service\n") {
		t.Error("두 스위치를 다 켰는데 지표 Service 가 렌더되지 않았다")
	}
	if !strings.Contains(both, "name: metrics") {
		t.Error("Service 는 생겼는데 컨테이너 포트가 없다 — targetPort 가 붙을 곳이 없다")
	}
}

// TestChartServiceMonitorNeedsBothSwitches 는 ServiceMonitor 가 별도 스위치를 요구하는지 본다.
//
// monitoring.coreos.com CRD 가 없는 클러스터에 렌더하면 helm install 이 통째로 실패한다.
func TestChartServiceMonitorNeedsBothSwitches(t *testing.T) {
	onlyMetrics := helmTemplate(t, "--set", "metrics.enabled=true")
	if strings.Contains(onlyMetrics, "kind: ServiceMonitor") {
		t.Error("serviceMonitor 스위치가 꺼져 있는데 ServiceMonitor 가 렌더됐다")
	}
	both := helmTemplate(t, "--set", "metrics.enabled=true", "--set", "metrics.serviceMonitor.enabled=true")
	if !strings.Contains(both, "kind: ServiceMonitor") {
		t.Error("두 스위치를 다 켰는데 ServiceMonitor 가 렌더되지 않았다")
	}
}

// TestChartMetricsDeadKeyRemoved 는 죽은 metricsService 키가 되살아나지 않는지 고정한다.
func TestChartMetricsDeadKeyRemoved(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "helm", "values.yaml"))
	if err != nil {
		t.Fatalf("values.yaml 읽기 실패: %v", err)
	}
	if strings.Contains(string(raw), "metricsService:") {
		t.Error("metricsService 키가 남아 있다 — 어느 템플릿도 읽지 않는 값이라 포트가 둘로 갈린다")
	}
}

// TestChartMetricsOnlyRendersNoNullVolumeMounts 는 metrics 만 켠 구성에서 volumeMounts 가
// 빈 값(null)으로 렌더되지 않는지 고정한다.
//
// ports: 와 volumeMounts: 가 같은 if 블록을 공유하는데 metrics 는 마운트가 없다. 바깥 if 에
// metrics 를 넣으면 metrics 단독 구성에서 volumeMounts: 키만 하위 항목 없이 렌더돼 null 이
// 된다. 마운트는 webhook/api 만 필요하므로 volumeMounts 는 별도 가드를 가져야 한다.
func TestChartMetricsOnlyRendersNoNullVolumeMounts(t *testing.T) {
	out := helmTemplate(t, "--set", "metrics.enabled=true")
	if strings.Contains(out, "volumeMounts:") {
		t.Error("metrics 만 켰는데 volumeMounts: 가 렌더됐다 — 마운트가 없어 null 로 렌더된다")
	}
}
