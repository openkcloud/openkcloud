// ============================================================
// evaluate_test.go: 신호 판정·상태 분류·전이 규칙 시험
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package health

import (
	"testing"
	"time"
)

func baseInputs(now time.Time) Inputs {
	seen := now.Add(-10 * time.Second)
	return Inputs{
		NodeReady: true, NDRObservedAt: &seen, DriverLoaded: true,
		DevicePluginReady: true,
		Expected:          map[string]int32{"nvidia.com/gpu": 2},
		Actual:            map[string]int32{"nvidia.com/gpu": 2},
		Now:               now,
	}
}

// TestHealthyWhenEverySignalHolds 는 모든 신호가 정상일 때만 Healthy 인지 본다.
func TestHealthyWhenEverySignalHolds(t *testing.T) {
	res := Evaluate(baseInputs(time.Now()), DefaultPolicy())
	if res.State != StateHealthy {
		t.Fatalf("state = %s (%s), want Healthy: %+v", res.State, res.Reason, res.Signals)
	}
	if !res.AllocationAllowed {
		t.Fatal("Healthy 인데 할당이 막혔다")
	}
	if len(res.Signals) < 5 {
		t.Fatalf("신호 기록이 %d개뿐이다 — 판정 근거를 남기지 않았다", len(res.Signals))
	}
}

// TestStaleObservationIsUnknownNotUnhealthy 는 관측이 끊긴 것을 고장으로 부르지 않는지 본다.
// 이 구분이 무너지면 detector 가 잠깐 죽은 클러스터에서 파괴적 복구가 쏟아진다.
func TestStaleObservationIsUnknownNotUnhealthy(t *testing.T) {
	now := time.Now()
	in := baseInputs(now)
	old := now.Add(-10 * time.Minute)
	in.NDRObservedAt = &old
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateUnknown {
		t.Fatalf("state = %s, want Unknown (관측 부재는 고장이 아니다)", res.State)
	}
	if res.Reason != ReasonObservationStale {
		t.Fatalf("reason = %s, want %s", res.Reason, ReasonObservationStale)
	}
	if res.AllocationAllowed {
		t.Fatal("모르는 노드에 신규 할당을 허용했다")
	}
}

// TestMissingObservationTimeIsUnknown 는 관측 시각 자체가 없을 때(구버전 node-agent)도
// Healthy 로 승격하지 않는지 본다.
func TestMissingObservationTimeIsUnknown(t *testing.T) {
	in := baseInputs(time.Now())
	in.NDRObservedAt = nil
	if got := Evaluate(in, DefaultPolicy()).State; got != StateUnknown {
		t.Fatalf("state = %s, want Unknown", got)
	}
}

// TestDevicePluginDownIsUnhealthy 는 광고 주체가 죽은 것을 Unhealthy 로 보는지 본다.
func TestDevicePluginDownIsUnhealthy(t *testing.T) {
	in := baseInputs(time.Now())
	in.DevicePluginReady = false
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateUnhealthy || res.Reason != ReasonDevicePluginDown {
		t.Fatalf("state=%s reason=%s, want Unhealthy/%s", res.State, res.Reason, ReasonDevicePluginDown)
	}
	if res.AllocationAllowed {
		t.Fatal("Unhealthy 인데 신규 할당이 허용됐다")
	}
}

// TestRolloutSuppressesAdvertisementSignal 은 정상 롤아웃 중의 광고 흔들림을 고장으로 부르지
// 않는지 본다. Stage 1 이 같은 이유로 억제 축을 뒀다 — 두 축이 다른 판단을 하면 안 된다.
func TestRolloutSuppressesAdvertisementSignal(t *testing.T) {
	now := time.Now()
	in := baseInputs(now)
	in.Actual = map[string]int32{"nvidia.com/gpu": 0}
	in.DevicePluginRolling = true
	first := now.Add(-5 * time.Minute)
	in.AdvertisementSuspectedAt = &first // 유예는 이미 지났지만 롤아웃이 우선이다
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateHealthy {
		t.Fatalf("state = %s (%s), 롤아웃 중 광고 흔들림은 억제돼야 한다", res.State, res.Reason)
	}
}

// TestAdvertisementNeedsGraceBeforeDegraded 는 첫 관측만으로 강등하지 않는지 본다.
func TestAdvertisementNeedsGraceBeforeDegraded(t *testing.T) {
	now := time.Now()
	in := baseInputs(now)
	in.Actual = map[string]int32{"nvidia.com/gpu": 0}
	if got := Evaluate(in, DefaultPolicy()).State; got != StateHealthy {
		t.Fatalf("state = %s, 첫 관측은 유예를 시작만 해야 한다", got)
	}
	first := now.Add(-31 * time.Second)
	in.AdvertisementSuspectedAt = &first
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateDegraded || res.Reason != ReasonAdvertisementMismatch {
		t.Fatalf("state=%s reason=%s, want Degraded/%s", res.State, res.Reason, ReasonAdvertisementMismatch)
	}
}

// TestRepeatedRecoveryFailureQuarantines 는 반복 실패가 격리로 끝나는지 본다.
func TestRepeatedRecoveryFailureQuarantines(t *testing.T) {
	in := baseInputs(time.Now())
	in.RecoveryFailures = 3
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateQuarantined || res.Reason != ReasonRepeatedRecoveryFail {
		t.Fatalf("state=%s reason=%s, want Quarantined/%s", res.State, res.Reason, ReasonRepeatedRecoveryFail)
	}
}

// TestRecoveryInFlightIsRecovering 는 복구 중인 노드를 Recovering 으로 두는지 본다.
func TestRecoveryInFlightIsRecovering(t *testing.T) {
	in := baseInputs(time.Now())
	in.RecoveryInFlight = true
	in.DevicePluginReady = false // 원인은 아직 남아 있다
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateRecovering {
		t.Fatalf("state = %s, want Recovering (복구가 도는 동안 새 복구를 부르지 않는다)", res.State)
	}
}

// TestDisabledSignalIsSkipped 는 정책이 끈 신호를 보지 않는지 본다.
func TestDisabledSignalIsSkipped(t *testing.T) {
	in := baseInputs(time.Now())
	in.DevicePluginReady = false
	p := DefaultPolicy()
	p.disabled[SignalDevicePlugin] = true
	if got := Evaluate(in, p).State; got != StateHealthy {
		t.Fatalf("state = %s, 끈 신호가 판정에 반영됐다", got)
	}
}

// TestQuarantineNeedsExplicitRecovery 는 격리에서 곧바로 Healthy 로 튀지 않는지 본다.
func TestQuarantineNeedsExplicitRecovery(t *testing.T) {
	if AllowedTransition(StateQuarantined, StateHealthy) {
		t.Fatal("격리에서 바로 Healthy 로 가면 안 된다")
	}
	if !AllowedTransition(StateQuarantined, StateRecovering) {
		t.Fatal("격리에서 복구 중으로는 갈 수 있어야 한다")
	}
}

// TestUnknownCanGoAnywhere 는 관측 재개 시 어느 상태로든 갈 수 있는지 본다.
func TestUnknownCanGoAnywhere(t *testing.T) {
	for _, to := range []string{StateHealthy, StateDegraded, StateUnhealthy, StateQuarantined} {
		if !AllowedTransition(StateUnknown, to) {
			t.Fatalf("Unknown → %s 가 막혔다", to)
		}
	}
}

// TestPartialDriverlessDoesNotCarryNodeScopedReason 는 장치 하나만 driverless 여도(건전한
// 장치가 남아 있으면) 노드 원인이 DriverNotLoaded 로 올라가지 않는지 본다. 원인 그대로 올리면
// 조치표(remediation.go)가 노드 범위 RecoverDevice 를 만들어, 혼재 노드에서 고장난 장치 하나가
// 멀쩡한 다른 장치의 드라이버 pod 까지 쿨다운마다 재시작시킨다.
// 깨는 뮤테이션: Evaluate 의 부분 실패 원인 치환(state, reason, allow = StateDegraded, ...)을
// 지우면 reason 이 DriverNotLoaded 로 남아 실패한다.
func TestPartialDriverlessDoesNotCarryNodeScopedReason(t *testing.T) {
	now := time.Now()
	in := baseInputs(now)
	in.Devices = []DeviceInput{
		{PCI: "0000:18:00.0", Vendor: "nvidia", DriverLoaded: true},
		{PCI: "0000:86:00.0", Vendor: "nvidia", DriverLoaded: false},
	}
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateDegraded {
		t.Fatalf("state = %s, want Degraded (시험 전제 확인: 건전한 장치가 남아 있다)", res.State)
	}
	if !res.AllocationAllowed {
		t.Fatal("건전한 장치가 남았는데 할당이 막혔다")
	}
	if res.Reason != ReasonPartialDeviceFailure {
		t.Fatalf("reason = %s, want %s — 조치표에 걸려 노드 범위 복구를 만든다",
			res.Reason, ReasonPartialDeviceFailure)
	}
}

// TestFullDriverlessStillCarriesDriverNotLoaded 는 노드의 모든 장치가 driverless 면 지금처럼
// DriverNotLoaded 로 남아 복구가 도는지 본다(회귀 방지 — 브랜치 이전 동작과 같아야 한다).
// 깨는 뮤테이션: Evaluate 의 드라이버 신호 분기(!in.DriverLoaded)를 지우면 Healthy 로 새 나가
// 실패한다.
func TestFullDriverlessStillCarriesDriverNotLoaded(t *testing.T) {
	now := time.Now()
	in := baseInputs(now)
	in.DriverLoaded = false
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateUnhealthy || res.Reason != ReasonDriverNotLoaded {
		t.Fatalf("state=%s reason=%s, want Unhealthy/%s", res.State, res.Reason, ReasonDriverNotLoaded)
	}
	if res.AllocationAllowed {
		t.Fatal("전 장치 driverless 인데 할당이 허용됐다")
	}
}

// TestObservationFailureDoesNotBlockAllocation 는 모든 장치의 MIG 관측이 실패해도 노드를
// Unknown/차단으로 보지 않는지 본다. 예: GeForce GTX 970(Maxwell)은 MIG 자체를 지원하지
// 않아 nvidia-smi 가 rc=18(NVML 초기화 실패)로 죽지만 GPU 는 멀쩡하다 — "MIG 를 못
// 읽는다" 를 "GPU 를 못 쓴다" 의 대리로 쓰면 틀린다. 보고서 신선도는 NDRFreshness 축이
// 따로 정확하게 감시한다.
// 깨는 뮤테이션: evaluateDevice 가 MigObservationError 로 다시 State=Unknown 을 내면 실패한다.
func TestObservationFailureDoesNotBlockAllocation(t *testing.T) {
	now := time.Now()
	in := baseInputs(now)
	in.Devices = []DeviceInput{
		{PCI: "0000:18:00.0", Vendor: "nvidia", DriverLoaded: true,
			MigObservationError: "Failed to initialize NVML: rc=18"},
		{PCI: "0000:86:00.0", Vendor: "nvidia", DriverLoaded: true,
			MigObservationError: "mode query failed: exit status 9"},
	}
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateHealthy {
		t.Fatalf("state = %s, want Healthy — MIG 를 못 읽는다고 GPU 를 못 쓰는 건 아니다", res.State)
	}
	if !res.AllocationAllowed {
		t.Fatal("MIG 관측 실패만으로 신규 할당을 막았다")
	}
	// 노드가 Healthy 로 끝나도 장치 신호는 Result.Signals(status.signals) 에 실려야 한다 —
	// 조기 반환 분기 안에서만 합치면 Healthy 노드에서 관측 실패 신호가 통째로 사라진다
	// (status.devices 는 맞아도 "왜" 를 볼 수 없다).
	found := false
	for _, s := range res.Signals {
		if s.Name == SignalDeviceObservation && !s.OK {
			found = true
		}
	}
	if !found {
		t.Fatal("관측 실패 사실이 status.signals 에 안 남았다 — 운영자가 원인을 볼 수 없다")
	}
}
