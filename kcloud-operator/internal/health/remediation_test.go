// ============================================================
// remediation_test.go: 원인→작업 매핑과 중복 억제 시험
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package health

import (
	"testing"
	"time"

	"kcloud-operator/internal/operation"
)

// TestPlanMapsReasonToOperation 는 대응표대로 원인이 작업 타입으로 옮겨지는지 본다.
func TestPlanMapsReasonToOperation(t *testing.T) {
	now := time.Now()
	cases := map[string]operation.Type{
		ReasonDevicePluginDown:      operation.DevicePluginRestart,
		ReasonAdvertisementMismatch: operation.Revalidate,
	}
	for reason, want := range cases {
		got := PlanRecovery("worker1", reason, []string{"0000:18:00.0"}, now, DefaultPolicy())
		if !got.Create {
			t.Fatalf("%s: 작업이 만들어지지 않았다", reason)
		}
		if got.Type != want {
			t.Fatalf("%s: type = %s, want %s", reason, got.Type, want)
		}
	}
}

// TestPlanIsStableWithinCooldown 는 같은 원인이 이어지는 동안 트랜잭션 ID 가 같은지 본다.
func TestPlanIsStableWithinCooldown(t *testing.T) {
	p := DefaultPolicy()
	t0 := time.Unix(1_700_000_000, 0)
	a := PlanRecovery("worker1", ReasonDevicePluginDown, nil, t0, p)
	b := PlanRecovery("worker1", ReasonDevicePluginDown, nil, t0.Add(p.RecoveryCooldown/2), p)
	if a.TransactionID != b.TransactionID {
		t.Fatalf("쿨다운 안인데 ID 가 다르다: %s vs %s", a.TransactionID, b.TransactionID)
	}
	c := PlanRecovery("worker1", ReasonDevicePluginDown, nil, t0.Add(2*p.RecoveryCooldown), p)
	if a.TransactionID == c.TransactionID {
		t.Fatal("쿨다운이 지났는데 같은 ID 라 새 복구가 영영 안 만들어진다")
	}
}

// TestPlanSeparatesNodesAndReasons 는 노드·원인이 다르면 다른 작업이 되는지 본다.
func TestPlanSeparatesNodesAndReasons(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := PlanRecovery("worker1", ReasonDevicePluginDown, nil, now, DefaultPolicy())
	b := PlanRecovery("worker2", ReasonDevicePluginDown, nil, now, DefaultPolicy())
	if a.TransactionID == b.TransactionID {
		t.Fatal("다른 노드인데 같은 작업으로 묶였다")
	}
}

// TestNoOperationForObservationStale 는 관측이 끊긴 것만으로 복구를 만들지 않는지 본다.
func TestNoOperationForObservationStale(t *testing.T) {
	got := PlanRecovery("worker1", ReasonObservationStale, nil, time.Now(), DefaultPolicy())
	if got.Create {
		t.Fatal("관측이 끊긴 것만으로 복구 작업을 만들면 안 된다")
	}
}

// TestRecoverDeviceForDriverNotLoaded 는 드라이버 미로드에 복구를 한 번 요청하는지 본다.
// 본체(recoverdevice_participant.go)가 등록되지 않은 상태에서 이 작업을 만들면 "no
// participant registered" 로 즉시 종점이 되고, 그 종점이 실패로 세어져 세 번 만에 멀쩡한
// 노드가 격리된다 — 그래서 본체 등록 전에는 만들지 않는다. 등록된 뒤에는 반대가 옳다:
// 흔한 원인은 재시작으로 낫고, 안 나으면 반복 실패가 격리로 올린다.
// 깨는 뮤테이션: operationForReason 의 ReasonDriverNotLoaded 갈래를 지우면 Create 가 거짓이 되어 실패한다.
func TestRecoverDeviceForDriverNotLoaded(t *testing.T) {
	got := PlanRecovery("worker1", ReasonDriverNotLoaded, []string{"0000:18:00.0"}, time.Now(), DefaultPolicy())
	if !got.Create || got.Type != operation.RecoverDevice {
		t.Fatalf("드라이버 미로드에 RecoverDevice 를 요청하지 않았다: %+v", got)
	}
	// 장치 키를 선언해야 같은 노드의 드라이버·파티션 작업과 직렬화된다.
	if len(got.ResourceKeys) < 2 {
		t.Fatalf("자원 키가 비면 충돌 판정이 아무것도 못 찾는다: %+v", got.ResourceKeys)
	}
	// 곧바로 격리하지는 않는다 — 격리는 복구가 반복 실패한 뒤의 결론이다.
	if DefaultPolicy().RemediationFor(ReasonDriverNotLoaded).Quarantine {
		t.Fatalf("복구를 시도해 보기도 전에 격리하면 사람이 풀어 줘야만 돌아온다")
	}
}

// TestNoOperationForPartialDeviceFailure 는 부분 실패 전용 원인이 조치표에 없어 작업을 만들지
// 않는지 본다 — 이 원인은 일부러 조치표에 넣지 않았다: 넣으면 노드
// 범위 RecoverDevice 가 만들어져, 혼재 노드에서 고장난 장치 하나가 멀쩡한 다른 장치의 드라이버
// pod 까지 지운다.
// 깨는 뮤테이션: operationForReason 이나 defaultRemediation 에 ReasonPartialDeviceFailure 를
// 추가하면 Create 가 참이 되어 실패한다.
func TestNoOperationForPartialDeviceFailure(t *testing.T) {
	got := PlanRecovery("worker1", ReasonPartialDeviceFailure, []string{"0000:18:00.0"}, time.Now(), DefaultPolicy())
	if got.Create {
		t.Fatal("부분 장치 실패만으로 노드 범위 복구 작업을 만들면 안 된다")
	}
}
