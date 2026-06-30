// ============================================================
// driver_upgrade.go: Driver Upgrade Controller Prometheus 메트릭
// 상세: controller-runtime 메트릭 레지스트리를 사용하여 드라이버 업그레이드
//       관련 메트릭(카운터, 히스토그램, 게이지)을 등록 및 제공
// 생성일: 2026-04-13
// ============================================================

package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// UpgradeTotal: 드라이버 업그레이드 완료 총 횟수 (vendor, result 레이블)
	// result 값: success | failure | rollback
	UpgradeTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kcloud_driver_upgrade_total",
			Help: "드라이버 업그레이드 완료 총 횟수 (result: success/failure/rollback)",
		},
		[]string{"vendor", "result"},
	)

	// UpgradeDurationSeconds: 업그레이드 단계별 소요 시간 (vendor, phase 레이블)
	// phase 값: preflight | cordoning | draining | upgrading | validating | uncordoning
	UpgradeDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kcloud_driver_upgrade_duration_seconds",
			Help:    "드라이버 업그레이드 단계별 소요 시간 (초)",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"vendor", "phase"},
	)

	// UpgradeState: 노드별 현재 업그레이드 상태 게이지 (node, vendor, state 레이블)
	// 값: 1=active, 0=inactive
	UpgradeState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcloud_driver_upgrade_state",
			Help: "노드별 드라이버 업그레이드 현재 상태 (1=active, 0=inactive)",
		},
		[]string{"node", "vendor", "state"},
	)

	// RollbackTotal: 드라이버 롤백 총 횟수 (vendor 레이블)
	RollbackTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kcloud_driver_rollback_total",
			Help: "드라이버 업그레이드 롤백 총 횟수",
		},
		[]string{"vendor"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		UpgradeTotal,
		UpgradeDurationSeconds,
		UpgradeState,
		RollbackTotal,
	)
}

// RecordUpgradeComplete는 업그레이드 완료(성공/실패/롤백) 카운터를 증가시킵니다.
func RecordUpgradeComplete(vendor, result string) {
	UpgradeTotal.WithLabelValues(vendor, result).Inc()
}

// RecordPhaseDuration은 특정 업그레이드 단계의 소요 시간을 기록합니다.
func RecordPhaseDuration(vendor, phase string, duration time.Duration) {
	UpgradeDurationSeconds.WithLabelValues(vendor, phase).Observe(duration.Seconds())
}

// SetUpgradeState는 노드의 현재 업그레이드 상태를 설정합니다.
// 이전 상태는 0으로, 새 상태는 1로 설정합니다.
func SetUpgradeState(node, vendor, state string) {
	// 해당 노드/vendor의 모든 상태를 0으로 초기화 후 현재 상태만 1로 설정
	allStates := []string{
		"Idle", "UpgradeRequired", "PreFlight",
		"Cordoning", "Draining", "Upgrading",
		"Validating", "Uncordoning", "Rollback", "Failed",
	}
	for _, s := range allStates {
		UpgradeState.WithLabelValues(node, vendor, s).Set(0)
	}
	if state != "" {
		UpgradeState.WithLabelValues(node, vendor, state).Set(1)
	}
}

// RecordRollback은 롤백 카운터를 증가시킵니다.
func RecordRollback(vendor string) {
	RollbackTotal.WithLabelValues(vendor).Inc()
}
