// ============================================================
// driver_upgrade_test.go: 드라이버 업그레이드 지표의 기록 동작 고정
// 상세: 상태 게이지가 한 시점에 하나만 1인지, 카운터가 라벨별로 누적되는지 검증
// 생성일: 2026-09-15 | 수정일: 2026-10-12
// ============================================================

package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// allUpgradeStates 는 CRD 가 정의한 업그레이드 상태 전량이다. 지표가 미는 목록이
// 이것보다 좁으면 옛 시계열이 1로 남는다.
var allUpgradeStates = []string{
	npuv1alpha1.UpgradeStateIdle,
	npuv1alpha1.UpgradeStateRequired,
	npuv1alpha1.UpgradeStatePreFlight,
	npuv1alpha1.UpgradeStateCordoning,
	npuv1alpha1.UpgradeStateDraining,
	npuv1alpha1.UpgradeStateUpgrading,
	npuv1alpha1.UpgradeStateValidating,
	npuv1alpha1.UpgradeStateUncordoning,
	npuv1alpha1.UpgradeStateRollback,
	npuv1alpha1.UpgradeStateFailed,
	npuv1alpha1.UpgradeStateUnverifiedVersion,
	npuv1alpha1.UpgradeStateHostAhead,
	npuv1alpha1.UpgradeStateRebootRequired,
	npuv1alpha1.UpgradeStateRebooting,
}

// TestSetUpgradeState_OnlyOneSeriesIsOne 은 상태를 옮길 때마다 1인 시계열이
// 정확히 하나인지 본다. 전이 순서를 CRD 상태 전량으로 돌려, 미는 목록에서
// 빠진 상태가 있으면 그 상태를 지난 다음 회차에서 2가 되어 실패한다.
func TestSetUpgradeState_OnlyOneSeriesIsOne(t *testing.T) {
	UpgradeState.Reset()

	const node, vendor = "worker1", "nvidia"
	for _, cur := range allUpgradeStates {
		SetUpgradeState(node, vendor, cur)

		var ones int
		for _, s := range allUpgradeStates {
			if testutil.ToFloat64(UpgradeState.WithLabelValues(node, vendor, s)) == 1 {
				ones++
			}
		}
		if ones != 1 {
			t.Errorf("상태 %q 에서 1인 시계열 %d개, 기대 1개", cur, ones)
		}
	}
}

// TestRecordUpgradeComplete_CountsPerLabel 은 벤더·결과 라벨 조합마다 따로
// 누적되는지 본다. 라벨을 하나로 접으면 실패 건수가 성공에 섞인다.
func TestRecordUpgradeComplete_CountsPerLabel(t *testing.T) {
	UpgradeTotal.Reset()

	RecordUpgradeComplete("nvidia", "success")
	RecordUpgradeComplete("nvidia", "success")
	RecordUpgradeComplete("nvidia", "failed")
	RecordUpgradeComplete("furiosa", "success")

	cases := []struct {
		vendor, result string
		want           float64
	}{
		{"nvidia", "success", 2},
		{"nvidia", "failed", 1},
		{"furiosa", "success", 1},
		{"furiosa", "failed", 0},
	}
	for _, tc := range cases {
		got := testutil.ToFloat64(UpgradeTotal.WithLabelValues(tc.vendor, tc.result))
		if got != tc.want {
			t.Errorf("%s/%s = %v, want %v", tc.vendor, tc.result, got, tc.want)
		}
	}
}

// TestRecordRollback_CountsPerVendor 는 롤백 카운터가 벤더별로 나뉘는지 본다.
func TestRecordRollback_CountsPerVendor(t *testing.T) {
	RollbackTotal.Reset()

	RecordRollback("nvidia")
	RecordRollback("nvidia")
	RecordRollback("furiosa")

	if got := testutil.ToFloat64(RollbackTotal.WithLabelValues("nvidia")); got != 2 {
		t.Errorf("nvidia = %v, want 2", got)
	}
	if got := testutil.ToFloat64(RollbackTotal.WithLabelValues("furiosa")); got != 1 {
		t.Errorf("furiosa = %v, want 1", got)
	}
}

// TestRecordPhaseDuration_ObservesIntoLabeledSeries 는 관측이 벤더·단계 라벨
// 조합마다 따로 쌓이는지 본다. 히스토그램은 ToFloat64 대상이 아니므로 수집한
// 뒤 표본 개수와 합을 직접 읽는다. 개수만 보면 값이 틀려도 통과하므로 합도 본다.
func TestRecordPhaseDuration_ObservesIntoLabeledSeries(t *testing.T) {
	UpgradeDurationSeconds.Reset()

	RecordPhaseDuration("nvidia", "draining", 3*time.Second)
	RecordPhaseDuration("nvidia", "draining", 5*time.Second)
	RecordPhaseDuration("furiosa", "upgrading", 1*time.Second)

	// 라벨 조합마다 시계열이 하나씩 생긴다. 라벨을 접으면 둘이 하나가 된다.
	if got := testutil.CollectAndCount(UpgradeDurationSeconds); got != 2 {
		t.Errorf("시계열 %d개, want 2", got)
	}

	obs, err := UpgradeDurationSeconds.GetMetricWithLabelValues("nvidia", "draining")
	if err != nil {
		t.Fatalf("시계열 조회 실패: %v", err)
	}
	var m dto.Metric
	if err := obs.(prometheus.Metric).Write(&m); err != nil {
		t.Fatalf("수집 실패: %v", err)
	}
	if c := m.GetHistogram().GetSampleCount(); c != 2 {
		t.Errorf("관측 수 %d, want 2", c)
	}
	if s := m.GetHistogram().GetSampleSum(); s != 8 {
		t.Errorf("관측 합 %v초, want 8초", s)
	}
}
