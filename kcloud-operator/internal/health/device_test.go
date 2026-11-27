// ============================================================
// device_test.go: 장치 단위 판정 단위 시험
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package health

import (
	"testing"
	"time"
)

// TestEvaluateDevicesFlagsOnlyTheBadDevice 는 고장난 장치만 골라내는지 본다.
// 깨는 뮤테이션: EvaluateDevices 가 전 장치에 같은 상태를 주면 실패한다.
func TestEvaluateDevicesFlagsOnlyTheBadDevice(t *testing.T) {
	in := []DeviceInput{
		{PCI: "0000:18:00.0", DriverLoaded: true},
		{PCI: "0000:86:00.0", DriverLoaded: false},
	}
	got := EvaluateDevices(in, DefaultPolicy())
	if len(got) != 2 {
		t.Fatalf("장치 수가 다르다: %+v", got)
	}
	if got[0].State != StateHealthy {
		t.Fatalf("멀쩡한 장치를 고장으로 봤다: %+v", got[0])
	}
	if got[1].State != StateUnhealthy || got[1].Reason != ReasonDriverNotLoaded {
		t.Fatalf("고장난 장치를 못 잡았다: %+v", got[1])
	}
}

// TestObservationFailureDoesNotChangeDeviceState 는 MIG 관측 실패가 장치 상태를 바꾸지 않는지
// 본다 — "MIG 를 못 읽는다" 와 "GPU 를 못 쓴다" 는 다른 사실이다. 예: master 의 GPU 는
// GeForce GTX 970(Maxwell) 이라 MIG 자체를 지원하지 않고 드라이버/라이브러리 버전
// 불일치로 nvidia-smi 가 rc=18(NVML 초기화 실패)로 죽지만 GPU 자체는 멀쩡하다.
// 깨는 뮤테이션: MigObservationError 분기가 다시 State=Unknown 을 내면 실패한다.
func TestObservationFailureDoesNotChangeDeviceState(t *testing.T) {
	in := []DeviceInput{{PCI: "0000:18:00.0", DriverLoaded: true,
		MigObservationError: "Failed to initialize NVML: rc=18"}}
	got := EvaluateDevices(in, DefaultPolicy())
	if got[0].State != StateHealthy {
		t.Fatalf("MIG 관측 실패만으로 장치를 고장으로 읽었다: %+v", got[0])
	}
	found := false
	for _, s := range got[0].Signals {
		if s.Name == SignalDeviceObservation && !s.OK {
			found = true
		}
	}
	if !found {
		t.Fatal("관측 실패 사실이 신호에 안 남았다 — 운영자가 원인을 볼 수 없다")
	}
}

// TestObservationFailurePlusDriverlessIsStillUnhealthy 는 관측 실패와 별개로 드라이버
// 미로드는 여전히 Unhealthy 인지 본다 — 관측 축을 빼도 드라이버 축은 그대로 살아 있어야 한다.
// 깨는 뮤테이션: 드라이버 분기를 지우면 Healthy 가 되어 실패한다.
func TestObservationFailurePlusDriverlessIsStillUnhealthy(t *testing.T) {
	in := []DeviceInput{{PCI: "0000:18:00.0", DriverLoaded: false,
		MigObservationError: "Failed to initialize NVML: rc=18"}}
	got := EvaluateDevices(in, DefaultPolicy())
	if got[0].State != StateUnhealthy || got[0].Reason != ReasonDriverNotLoaded {
		t.Fatalf("드라이버 미로드 장치를 못 잡았다: %+v", got[0])
	}
}

// TestDeviceSignalsCarryPCI 는 신호에 장치 식별자가 실리는지 본다 — 이게 없으면
// 운영자가 "어느 장치가 문제인가" 를 status 만 보고 알 수 없다.
func TestDeviceSignalsCarryPCI(t *testing.T) {
	got := EvaluateDevices([]DeviceInput{{PCI: "0000:18:00.0", DriverLoaded: false}}, DefaultPolicy())
	for _, s := range got[0].Signals {
		if s.Target != "0000:18:00.0" {
			t.Fatalf("신호에 PCI 가 안 실렸다: %+v", s)
		}
	}
}

// TestWorstDeviceStateFolds 는 노드 상태가 가장 나쁜 장치를 따르는지 본다.
// 깨는 뮤테이션: WorstDeviceState 가 첫 장치만 보면 실패한다.
func TestWorstDeviceStateFolds(t *testing.T) {
	cases := []struct {
		name string
		in   []DeviceResult
		want string
	}{
		{"전부 정상", []DeviceResult{{State: StateHealthy}, {State: StateHealthy}}, StateHealthy},
		{"하나 고장", []DeviceResult{{State: StateHealthy}, {State: StateUnhealthy}}, StateUnhealthy},
		{"하나 관측불가", []DeviceResult{{State: StateHealthy}, {State: StateUnknown}}, StateUnknown},
		{"고장이 관측불가를 덮는다", []DeviceResult{{State: StateUnknown}, {State: StateUnhealthy}}, StateUnhealthy},
		{"장치 없음", nil, ""},
	}
	for _, c := range cases {
		if got, _ := WorstDeviceState(c.in); got != c.want {
			t.Fatalf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestDegradedResultStillCarriesDeviceResults 는 광고 불일치(Degraded)로 끝나도 장치 판정이
// status 에 남는지 본다 — 직전 사이클엔 채워져 있던 Devices 가 Degraded 로 넘어가는 순간
// 사라지면, 운영자가 원인을 볼 때 가장 필요한 타이밍에 정보가 없어진다.
// 깨는 뮤테이션: Degraded 반환 직전 res.Devices = devResults 대입을 지우면 실패한다.
func TestDegradedResultStillCarriesDeviceResults(t *testing.T) {
	now := time.Now()
	seen := now.Add(-10 * time.Second)
	first := now.Add(-31 * time.Second)
	in := Inputs{
		NodeReady: true, NDRObservedAt: &seen, DriverLoaded: true,
		DevicePluginReady:        true,
		Expected:                 map[string]int32{"nvidia.com/gpu": 2},
		Actual:                   map[string]int32{"nvidia.com/gpu": 0},
		AdvertisementSuspectedAt: &first,
		Devices:                  []DeviceInput{{PCI: "0000:18:00.0", DriverLoaded: true}},
		Now:                      now,
	}
	res := Evaluate(in, DefaultPolicy())
	if res.State != StateDegraded {
		t.Fatalf("state = %s, want Degraded (시험 전제 확인)", res.State)
	}
	if len(res.Devices) != 1 || res.Devices[0].PCI != "0000:18:00.0" {
		t.Fatalf("Degraded 인데 Devices 가 비었다: %+v", res.Devices)
	}
}

// TestEvaluateDevicesSortedByPCI 는 입력 순서와 무관하게 PCI 오름차순으로 나오는지 본다 —
// NDR 나열 순서에 기대면(detector 재시작 등) 순서만 바뀌어도 equalHealthStatus 가 인덱스별
// 비교에서 "바뀜" 으로 오판해 불필요한 status write 를 만든다.
// 깨는 뮤테이션: EvaluateDevices 끝의 sort.Slice 를 지우면 입력 순서 그대로 나와 실패한다.
func TestEvaluateDevicesSortedByPCI(t *testing.T) {
	in := []DeviceInput{
		{PCI: "0000:65:00.0", DriverLoaded: true},
		{PCI: "0000:03:00.0", DriverLoaded: true},
	}
	got := EvaluateDevices(in, DefaultPolicy())
	if got[0].PCI != "0000:03:00.0" || got[1].PCI != "0000:65:00.0" {
		t.Fatalf("PCI 순으로 정렬되지 않았다: %+v", got)
	}
}
