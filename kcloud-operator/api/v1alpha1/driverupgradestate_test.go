// driverupgradestate_test.go: DriverUpgradeState CRD 필드 노출 시험
// 생성일: 2026-09-17 | 수정일: 2026-10-13

package v1alpha1

import (
	"os"
	"strings"
	"testing"
)

func TestDriverUpgradeStateCRDHasDriftFirstSeen(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/npu.ai_driverupgradestates.yaml")
	if err != nil {
		t.Fatalf("CRD 읽기 실패: %v", err)
	}
	if !strings.Contains(string(raw), "driftFirstSeen") {
		t.Error("CRD 에 driftFirstSeen 필드가 없다 — make manifests 재생성 필요")
	}
}

func TestDriverUpgradeStateCRDHasRolledBackFields(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/npu.ai_driverupgradestates.yaml")
	if err != nil {
		t.Fatalf("CRD 읽기 실패: %v", err)
	}
	for _, f := range []string{"rolledBackVersion", "rolledBackGeneration"} {
		if !strings.Contains(string(raw), f) {
			t.Errorf("CRD 에 %s 필드가 없다 — make manifests 재생성 필요", f)
		}
	}
}

func TestUpgradeCycleClosed(t *testing.T) {
	closed := []string{"", UpgradeStateIdle, UpgradeStateFailed, UpgradeStateHostAhead, UpgradeStateUnverifiedVersion}
	open := []string{UpgradeStateRequired, UpgradeStatePreFlight, UpgradeStateCordoning, UpgradeStateDraining,
		UpgradeStateUpgrading, UpgradeStateValidating, UpgradeStateRebootRequired, UpgradeStateRebooting,
		UpgradeStateUncordoning, UpgradeStateRollback}
	for _, s := range closed {
		if !UpgradeCycleClosed(s) {
			t.Errorf("%q 는 사이클이 끝난 상태다", s)
		}
	}
	for _, s := range open {
		if UpgradeCycleClosed(s) {
			t.Errorf("%q 는 사이클 중간이다 — 라벨을 지우면 진행 중인 사이클을 깬다", s)
		}
	}
}
