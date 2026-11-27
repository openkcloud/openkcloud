// ============================================================
// version_drift_test.go: 버전 드리프트 판정 단위 시험
// 상세: 관측 부재 → 억제 → 일치 → 디바운스 → 확정 다섯 판정의 게이트 순서를 고정한다.
// 생성일: 2026-09-17 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"testing"
	"time"
)

func TestVersionDriftSuppressedWhileNotIdle(t *testing.T) {
	v, reason := EvaluateVersionDrift(VersionDriftInput{
		State:           "Upgrading",
		DeclaredVersion: "580.173.02",
		ObservedVersion: "595.91.07",
		ObservedAt:      time.Now(),
		Grace:           10 * time.Minute,
		Since:           time.Now().Add(-time.Hour),
	})
	if v != VersionDriftSuppressed {
		t.Errorf("verdict=%q reason=%q, want suppressed — 업그레이드 중에는 불일치가 정상이다", v, reason)
	}
}

// TestVersionDriftConfirmedWhileParked 는 허용 목록 밖 버전이 주차하는 UnverifiedVersion 에서
// 억제하지 않음을 본다 — 그 상태는 Job 도 cordon 도 만들지 않는 보고 전용 종점이고, 자동
// 업그레이드를 끈 정책의 노드가 바로 거기 머문다. 억제하면 호스트 밖 변경 감지가 영구히 꺼진다.
func TestVersionDriftConfirmedWhileParked(t *testing.T) {
	v, reason := EvaluateVersionDrift(VersionDriftInput{
		State:           "UnverifiedVersion",
		DeclaredVersion: "580.173.02",
		ObservedVersion: "595.91.07",
		ObservedAt:      time.Now(),
		Grace:           10 * time.Minute,
		Since:           time.Now().Add(-time.Hour),
	})
	if v != VersionDriftConfirmed {
		t.Errorf("verdict=%q reason=%q, want confirmed — 주차 상태는 사이클 중간이 아니다", v, reason)
	}
}

func TestVersionDriftMatchWhenEqual(t *testing.T) {
	v, _ := EvaluateVersionDrift(VersionDriftInput{
		State:           "Idle",
		DeclaredVersion: "595.91.07",
		ObservedVersion: "595.91.07",
		ObservedAt:      time.Now(),
		Grace:           10 * time.Minute,
		Since:           time.Now().Add(-time.Hour),
	})
	if v != VersionDriftNone {
		t.Errorf("verdict=%q, want none", v)
	}
}

func TestVersionDriftDebouncedWithinGrace(t *testing.T) {
	v, _ := EvaluateVersionDrift(VersionDriftInput{
		State:           "Idle",
		DeclaredVersion: "580.173.02",
		ObservedVersion: "595.91.07",
		ObservedAt:      time.Now(),
		Grace:           10 * time.Minute,
		Since:           time.Now().Add(-time.Minute), // 유예 안
	})
	if v != VersionDriftPending {
		t.Errorf("verdict=%q, want pending — 유예 안의 불일치는 아직 확정이 아니다", v)
	}
}

func TestVersionDriftConfirmedAfterGrace(t *testing.T) {
	v, reason := EvaluateVersionDrift(VersionDriftInput{
		State:           "Idle",
		DeclaredVersion: "580.173.02",
		ObservedVersion: "595.91.07",
		ObservedAt:      time.Now(),
		Grace:           10 * time.Minute,
		Since:           time.Now().Add(-time.Hour), // 유예 초과
	})
	if v != VersionDriftConfirmed {
		t.Fatalf("verdict=%q, want confirmed", v)
	}
	if reason == "" {
		t.Error("확정인데 사유가 비었다 — 사람이 무엇이 어긋났는지 알 수 없다")
	}
}

// TestVersionDriftNoObservationIsNotDrift 는 관측이 없을 때를 드리프트로 읽지 않는지 본다.
// 확인하지 못한 것을 어긋남으로 읽으면 NDR 이 늦는 노드마다 거짓 경보가 난다.
func TestVersionDriftNoObservationIsNotDrift(t *testing.T) {
	v, _ := EvaluateVersionDrift(VersionDriftInput{
		State:           "Idle",
		DeclaredVersion: "580.173.02",
		ObservedVersion: "",
		ObservedAt:      time.Time{},
		Grace:           10 * time.Minute,
		Since:           time.Now().Add(-time.Hour),
	})
	if v != VersionDriftUnknown {
		t.Errorf("verdict=%q, want unknown", v)
	}
}

// TestVersionDriftUnknownWhenNoObservationEvenIfNotIdle 는 관측이 없으면 상태와 무관하게
// Unknown 인지 본다 — 관측 게이트가 억제 게이트보다 먼저다(drift.go 와 같은 순서).
// 이 순서가 뒤집히면 State≠Idle 인 무관측 입력이 Suppressed 로 새어 두 검출기가 갈라진다.
func TestVersionDriftUnknownWhenNoObservationEvenIfNotIdle(t *testing.T) {
	v, _ := EvaluateVersionDrift(VersionDriftInput{
		State:           "Upgrading",
		DeclaredVersion: "580.173.02",
		ObservedVersion: "",
		ObservedAt:      time.Time{},
		Grace:           10 * time.Minute,
		Since:           time.Now().Add(-time.Hour),
	})
	if v != VersionDriftUnknown {
		t.Errorf("verdict=%q, want unknown — 관측 없음이 억제보다 먼저 판정돼야 한다", v)
	}
}

// TestVersionDriftPendingOnFirstObservation 는 Since 가 아직 없는 최초 감지에서 Pending 인지 본다.
//
// 호출자가 불일치를 처음 보면 Since 를 아직 기록하지 않았다(zero). 그 부트스트랩을 확정으로
// 바로 올리면 첫 관측만으로 경보가 난다. drift.go 의 FirstSuspectedAt==nil 대응 분기다.
func TestVersionDriftPendingOnFirstObservation(t *testing.T) {
	v, _ := EvaluateVersionDrift(VersionDriftInput{
		State:           "Idle",
		DeclaredVersion: "580.173.02",
		ObservedVersion: "595.91.07",
		ObservedAt:      time.Now(),
		Grace:           10 * time.Minute,
		Since:           time.Time{}, // 아직 기록 없음(부트스트랩)
	})
	if v != VersionDriftPending {
		t.Errorf("verdict=%q, want pending — 최초 감지(Since 없음)는 확정이 아니다", v)
	}
}
