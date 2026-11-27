// ============================================================
// operator_test.go: operator 조정 경로 지표의 기록 동작 고정
// 상세: 다섯 기록 함수가 올바른 레이블 조합으로 값을 올리는지 확인한다.
// 생성일: 2026-09-22 | 수정일: 2026-09-22
// ============================================================

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// TestRecordACPPApply_CountsPerLabel 은 vendor·mode·result 조합마다 따로 누적되는지 본다.
// 레이블을 접으면 rollback 이 ready 에 섞인다.
func TestRecordACPPApply_CountsPerLabel(t *testing.T) {
	ACPPApplyTotal.Reset()

	RecordACPPApply("nvidia", "mps", "ready")
	RecordACPPApply("nvidia", "mps", "ready")
	RecordACPPApply("nvidia", "exclusive", "disabled")
	RecordACPPApply("nvidia", "timeSliced", "rejected")
	RecordACPPApply("nvidia", "exclusive", "rollback")

	cases := []struct {
		vendor, mode, result string
		want                 float64
	}{
		{"nvidia", "mps", "ready", 2},
		{"nvidia", "exclusive", "disabled", 1},
		{"nvidia", "timeSliced", "rejected", 1},
		{"nvidia", "exclusive", "rollback", 1},
		{"nvidia", "mps", "rollback", 0},
	}
	for _, tc := range cases {
		got := testutil.ToFloat64(ACPPApplyTotal.WithLabelValues(tc.vendor, tc.mode, tc.result))
		if got != tc.want {
			t.Errorf("%s/%s/%s = %v, want %v", tc.vendor, tc.mode, tc.result, got, tc.want)
		}
	}
}

// TestRecordHealthVerdict_CountsPerLabel 은 vendor·verdict 조합마다 따로 쌓이는지 본다.
func TestRecordHealthVerdict_CountsPerLabel(t *testing.T) {
	HealthVerdictTotal.Reset()

	RecordHealthVerdict("nvidia", "Healthy")
	RecordHealthVerdict("nvidia", "Healthy")
	RecordHealthVerdict("nvidia", "Unhealthy")
	RecordHealthVerdict("furiosa", "Degraded")

	cases := []struct {
		vendor, verdict string
		want            float64
	}{
		{"nvidia", "Healthy", 2},
		{"nvidia", "Unhealthy", 1},
		{"furiosa", "Degraded", 1},
		{"furiosa", "Healthy", 0},
	}
	for _, tc := range cases {
		got := testutil.ToFloat64(HealthVerdictTotal.WithLabelValues(tc.vendor, tc.verdict))
		if got != tc.want {
			t.Errorf("%s/%s = %v, want %v", tc.vendor, tc.verdict, got, tc.want)
		}
	}
}

// TestRecordEvidenceRecord_CountsPerLabel 은 vendor·level 조합마다 따로 쌓이는지 본다.
func TestRecordEvidenceRecord_CountsPerLabel(t *testing.T) {
	EvidenceRecordsTotal.Reset()

	RecordEvidenceRecord("nvidia", "hardware")
	RecordEvidenceRecord("nvidia", "hardware")
	RecordEvidenceRecord("furiosa", "advertisement")

	if got := testutil.ToFloat64(EvidenceRecordsTotal.WithLabelValues("nvidia", "hardware")); got != 2 {
		t.Errorf("nvidia/hardware = %v, want 2", got)
	}
	if got := testutil.ToFloat64(EvidenceRecordsTotal.WithLabelValues("furiosa", "advertisement")); got != 1 {
		t.Errorf("furiosa/advertisement = %v, want 1", got)
	}
}

// TestRecordWebhookRejection_CountsPerKind 는 거부가 CRD Kind 별로 나뉘는지 본다.
func TestRecordWebhookRejection_CountsPerKind(t *testing.T) {
	WebhookRejections.Reset()

	RecordWebhookRejection("DriverInstallPolicy")
	RecordWebhookRejection("DriverInstallPolicy")
	RecordWebhookRejection("AcceleratorPartitionPolicy")

	if got := testutil.ToFloat64(WebhookRejections.WithLabelValues("DriverInstallPolicy")); got != 2 {
		t.Errorf("DriverInstallPolicy = %v, want 2", got)
	}
	if got := testutil.ToFloat64(WebhookRejections.WithLabelValues("AcceleratorPartitionPolicy")); got != 1 {
		t.Errorf("AcceleratorPartitionPolicy = %v, want 1", got)
	}
}

// TestSetNPUNodes_SetsPerVendorAndDropsToZero 는 게이지가 벤더별로 설정되고, 다시
// 0 으로 내리면 옛 값이 남지 않는지 본다(벤더가 사라진 경우의 규약).
func TestSetNPUNodes_SetsPerVendorAndDropsToZero(t *testing.T) {
	NPUNodes.Reset()

	SetNPUNodes("nvidia", 3)
	SetNPUNodes("furiosa", 1)

	if got := testutil.ToFloat64(NPUNodes.WithLabelValues("nvidia")); got != 3 {
		t.Errorf("nvidia = %v, want 3", got)
	}
	if got := testutil.ToFloat64(NPUNodes.WithLabelValues("furiosa")); got != 1 {
		t.Errorf("furiosa = %v, want 1", got)
	}

	// 벤더가 사라지면 0 으로 내린다 — 게이지는 마지막 값을 유지하므로 명시적으로 0 을 써야 한다.
	SetNPUNodes("furiosa", 0)
	if got := testutil.ToFloat64(NPUNodes.WithLabelValues("furiosa")); got != 0 {
		t.Errorf("furiosa after drop = %v, want 0", got)
	}
}

// TestSeedZeroSeries_ExposesAllVectors 는 등록 시 0 초기화가 다섯 벡터를 알려진 레이블
// 조합으로 채워, 표본이 나기 전에도 /metrics 에 이름·TYPE 이 나오는지 본다. Reset 후 다시
// seed 하므로 테스트 순서에 무관하다.
func TestSeedZeroSeries_ExposesAllVectors(t *testing.T) {
	ACPPApplyTotal.Reset()
	HealthVerdictTotal.Reset()
	EvidenceRecordsTotal.Reset()
	WebhookRejections.Reset()
	NPUNodes.Reset()

	seedZeroSeries()

	vendors := metricVendors()
	if len(vendors) == 0 {
		t.Fatal("벤더 목록이 비었다 — vendorcap.Keys() 확인")
	}
	counts := []struct {
		name string
		got  int
		want int
	}{
		{"kcloud_acpp_apply_total", testutil.CollectAndCount(ACPPApplyTotal), len(vendors) * len(acppApplyModes) * len(acppApplyResults)},
		{"kcloud_health_verdict_total", testutil.CollectAndCount(HealthVerdictTotal), len(vendors) * len(HealthVerdicts)},
		{"kcloud_evidence_records_total", testutil.CollectAndCount(EvidenceRecordsTotal), len(vendors) * len(evidenceLevels)},
		{"kcloud_webhook_rejections_total", testutil.CollectAndCount(WebhookRejections), len(webhookKinds())},
		{"kcloud_npu_nodes", testutil.CollectAndCount(NPUNodes), len(vendors)},
	}
	for _, c := range counts {
		if c.got < c.want {
			t.Errorf("%s: 시계열 %d개, want >= %d — 0 초기화 누락", c.name, c.got, c.want)
		}
	}

	// /metrics 스크랩(레지스트리 gather)에 다섯 이름이 나오는지 — Vec 는 자식이 있어야 노출된다.
	seen := map[string]bool{
		"kcloud_acpp_apply_total":         false,
		"kcloud_health_verdict_total":     false,
		"kcloud_evidence_records_total":   false,
		"kcloud_webhook_rejections_total": false,
		"kcloud_npu_nodes":                false,
	}
	mfs, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("registry gather 실패: %v", err)
	}
	for _, mf := range mfs {
		if _, ok := seen[mf.GetName()]; ok {
			seen[mf.GetName()] = true
		}
	}
	for name, ok := range seen {
		if !ok {
			t.Errorf("%s 가 /metrics 에 없다 — HELP/TYPE 미출력", name)
		}
	}
}
