// ============================================================
// operator.go: operator 조정 경로 Prometheus 지표
// 상세: ACPP 적용·health 판정·evidence 기록·webhook 거부·벤더별 노드 수를
//       controller-runtime 지표 레지스트리에 등록해 operator 의 /metrics 로 노출한다.
//       엔드포인트 자체는 metrics.enabled 로 켜야 열린다(기본 꺼짐).
// 생성일: 2026-09-22 | 수정일: 2026-09-22
// ============================================================

package metrics

import (
	"reflect"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/vendorcap"
)

var (
	// ACPPApplyTotal: ACPP 적용 결과 총 횟수 (vendor, mode, result 레이블)
	// result 값: ready | disabled | rejected | rollback
	ACPPApplyTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kcloud_acpp_apply_total",
			Help: "ACPP 적용 결과 총 횟수 (result: ready/disabled/rejected/rollback)",
		},
		[]string{"vendor", "mode", "result"},
	)

	// HealthVerdictTotal: 노드 health 판정 총 횟수 (vendor, verdict 레이블)
	// verdict 값은 internal/health 의 판정 상수(Healthy/Degraded/Unhealthy/Unknown/Quarantined).
	HealthVerdictTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kcloud_health_verdict_total",
			Help: "노드 health 판정 총 횟수 (verdict: Healthy/Degraded/Unhealthy/Unknown/Quarantined/Recovering)",
		},
		[]string{"vendor", "verdict"},
	)

	// EvidenceRecordsTotal: 검증 근거(AcceleratorEvidence) 기록 총 횟수 (vendor, level 레이블)
	// no-op 게이트를 지나 실제로 status 를 쓴 경우에만 증가한다.
	EvidenceRecordsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kcloud_evidence_records_total",
			Help: "검증 근거 기록 총 횟수 (실제 status 갱신 시에만 증가)",
		},
		[]string{"vendor", "level"},
	)

	// WebhookRejections: admission webhook 거부 총 횟수 (kind 레이블 = 거부된 CRD Kind)
	WebhookRejections = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kcloud_webhook_rejections_total",
			Help: "admission webhook 거부 총 횟수 (kind: 거부된 CRD 종류)",
		},
		[]string{"kind"},
	)

	// NPUNodes: 벤더별 NodeDeviceReport 가 있는 노드 수 게이지 (vendor 레이블)
	NPUNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcloud_npu_nodes",
			Help: "벤더별 NodeDeviceReport 가 있는 노드 수",
		},
		[]string{"vendor"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		ACPPApplyTotal,
		HealthVerdictTotal,
		EvidenceRecordsTotal,
		WebhookRejections,
		NPUNodes,
	)
	seedZeroSeries()
}

// 지표 벡터를 0 초기화할 때 쓰는 레이블 목록. Prometheus Vec 는 자식이 하나도 없으면
// HELP/TYPE 를 내지 않아 /metrics 에 이름조차 안 나온다. 등록 시 알려진 조합을 0 으로
// 만들어 이벤트가 나기 전에도 다섯 지표가 항상 노출되게 한다.
var (
	// acppApplyResults 는 이 지표가 쓰는 result 값 전량(코드 계약).
	acppApplyResults = []string{"ready", "disabled", "rejected", "rollback"}
	// acppApplyModes 는 SharingMode enum 전량(api/v1alpha1).
	acppApplyModes = []string{
		npuv1alpha1.SharingModeExclusive,
		npuv1alpha1.SharingModeTimeSliced,
		npuv1alpha1.SharingModeMPS,
	}
	// HealthVerdicts 는 internal/health 판정 상수와 일치해야 한다. health 를 import 하면
	// 순환(health→internal/operation→internal/metrics)이라 문자열로 두고, 일치는 외부 테스트
	// 패키지(operator_labels_test.go)가 internal/health 를 대조해 지킨다.
	HealthVerdicts = []string{"Healthy", "Degraded", "Unhealthy", "Unknown", "Quarantined", "Recovering"}
	// evidenceLevels 는 AcceleratorEvidence Level enum 전량(api/v1alpha1).
	evidenceLevels = []string{
		npuv1alpha1.EvidenceLevelObserved,
		npuv1alpha1.EvidenceLevelFunctionallyVerified,
		npuv1alpha1.EvidenceLevelAllocationVerified,
	}
)

// metricVendors 는 능력 레지스트리가 아는 벤더 전량이다(중복 제거). 지표 레이블 vendor 의 집합.
func metricVendors() []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, k := range vendorcap.Keys() {
		if k.Vendor != "" && !seen[k.Vendor] {
			seen[k.Vendor] = true
			out = append(out, k.Vendor)
		}
	}
	return out
}

// webhookKinds 는 webhook 이 검증하는 CRD 종류 전량이다. 타입 이름에서 뽑아(리터럴 회피)
// webhook 등록·RecordWebhookRejection 이 쓰는 문자열과 같게 맞춘다.
func webhookKinds() []string {
	return []string{
		reflect.TypeOf(npuv1alpha1.DriverInstallPolicy{}).Name(),
		reflect.TypeOf(npuv1alpha1.NPUClusterPolicy{}).Name(),
		reflect.TypeOf(npuv1alpha1.AcceleratorClass{}).Name(),
		reflect.TypeOf(npuv1alpha1.AcceleratorWorkload{}).Name(),
		reflect.TypeOf(npuv1alpha1.AcceleratorPartitionPolicy{}).Name(),
	}
}

// seedZeroSeries 는 알려진 레이블 조합을 0 으로 만들어 다섯 지표가 표본 전에도 이름·HELP·
// TYPE 을 내게 한다. Add(0)/Set(0) 이라 멱등 — 재호출해도 값이 늘지 않는다.
func seedZeroSeries() {
	for _, ven := range metricVendors() {
		for _, m := range acppApplyModes {
			for _, r := range acppApplyResults {
				ACPPApplyTotal.WithLabelValues(ven, m, r).Add(0)
			}
		}
		for _, vd := range HealthVerdicts {
			HealthVerdictTotal.WithLabelValues(ven, vd).Add(0)
		}
		for _, l := range evidenceLevels {
			EvidenceRecordsTotal.WithLabelValues(ven, l).Add(0)
		}
		NPUNodes.WithLabelValues(ven).Set(0)
	}
	for _, k := range webhookKinds() {
		WebhookRejections.WithLabelValues(k).Add(0)
	}
}

// RecordACPPApply 는 ACPP 적용 결과 카운터를 result 별로 올린다.
func RecordACPPApply(vendor, mode, result string) {
	ACPPApplyTotal.WithLabelValues(vendor, mode, result).Inc()
}

// RecordHealthVerdict 는 노드 health 판정 카운터를 verdict 별로 올린다.
func RecordHealthVerdict(vendor, verdict string) {
	HealthVerdictTotal.WithLabelValues(vendor, verdict).Inc()
}

// RecordEvidenceRecord 는 검증 근거 기록 카운터를 올린다(실제 기록 시에만 호출한다).
func RecordEvidenceRecord(vendor, level string) {
	EvidenceRecordsTotal.WithLabelValues(vendor, level).Inc()
}

// RecordWebhookRejection 은 webhook 거부 카운터를 kind 별로 올린다.
func RecordWebhookRejection(kind string) {
	WebhookRejections.WithLabelValues(kind).Inc()
}

// SetNPUNodes 는 벤더별 NodeDeviceReport 노드 수 게이지를 설정한다. 벤더가 사라지면
// 호출자가 0 을 넘겨 옛 값이 남지 않게 한다.
func SetNPUNodes(vendor string, n int) {
	NPUNodes.WithLabelValues(vendor).Set(float64(n))
}
