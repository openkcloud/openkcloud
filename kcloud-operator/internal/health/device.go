// ============================================================
// device.go: 장치 단위 health 판정 (순수)
// 상세: 노드 단위 신호가 답하지 못하는 질문 — "이 노드의 어느 장치가 문제인가" 를 답한다.
//
//	클러스터를 읽지 않는다. 수집은 컨트롤러가, 배치 반영은 internal/intent 가 한다.
//
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package health

import (
	"fmt"
	"sort"
)

// 장치 단위 신호 이름.
const (
	SignalDeviceDriver      = "DeviceDriver"
	SignalDeviceObservation = "DeviceObservation"
)

// DeviceInput 은 장치 하나의 관측값이다.
type DeviceInput struct {
	PCI                 string
	Vendor              string
	Model               string
	DriverLoaded        bool
	MigObservationError string
	MigModeCurrent      string
}

// DeviceResult 는 장치 하나의 판정이다.
type DeviceResult struct {
	PCI     string
	Vendor  string
	State   string
	Reason  string
	Signals []Signal
}

// EvaluateDevices 는 장치별로 판정한다.
func EvaluateDevices(devs []DeviceInput, p EffectivePolicy) []DeviceResult {
	out := make([]DeviceResult, 0, len(devs))
	for _, d := range devs {
		out = append(out, evaluateDevice(d, p))
	}
	// PCI 로 정렬해 순서를 결정론적으로 만든다 — NDR 의 장치 나열 순서에 기대면
	// (detector 재시작 등으로) 순서만 바뀌어도 equalHealthStatus 가 "바뀜" 으로 오판해
	// 불필요한 status write 를 만든다.
	sort.Slice(out, func(i, j int) bool { return out[i].PCI < out[j].PCI })
	return out
}

func evaluateDevice(d DeviceInput, p EffectivePolicy) DeviceResult {
	res := DeviceResult{PCI: d.PCI, Vendor: d.Vendor}
	record := func(name string, ok bool, msg string) {
		res.Signals = append(res.Signals, Signal{Name: name, Target: d.PCI, OK: ok, Message: msg})
	}

	// MIG 관측 실패는 상태를 좌우하지 않는다 — "MIG 를 못 읽는다" 와 "GPU 를 못 쓴다" 는 다른
	// 사실이다(예: master 의 GeForce GTX 970 은 MIG 자체를 지원하지 않아 관측이
	// 영원히 실패하지만 GPU 로서는 멀쩡하다). 신호로만 남겨 운영자가 볼 수 있게 하고, 상태는
	// 드라이버 축으로만 정한다. 보고서 신선도는 이미 NDRFreshness 가 따로 감시한다.
	if d.MigObservationError != "" {
		record(SignalDeviceObservation, false,
			fmt.Sprintf("장치를 관측하지 못했다: %s", d.MigObservationError))
	} else {
		record(SignalDeviceObservation, true, "")
	}

	if p.Enabled(SignalDriverHeartbeat) && !d.DriverLoaded {
		record(SignalDeviceDriver, false, "이 장치의 드라이버가 로드돼 있지 않다")
		res.State, res.Reason = StateUnhealthy, ReasonDriverNotLoaded
		return res
	}
	record(SignalDeviceDriver, true, "")

	res.State = StateHealthy
	return res
}

// WorstDeviceState 는 장치 상태를 노드 상태 하나로 접는다 — 가장 나쁜 장치를 따른다.
// 장치가 없으면 빈 문자열(판정 대상 없음)을 돌려준다.
// StateUnknown 등급은 랭크 맵에 남아 있지만 EvaluateDevices 가 이 상태를 내지 않는 한
// 죽은 갈래다 — 관측 실패를 다시 상태에 반영하게 되면 되살아난다.
func WorstDeviceState(devs []DeviceResult) (string, string) {
	rank := map[string]int{StateHealthy: 0, StateUnknown: 1, StateDegraded: 2, StateUnhealthy: 3}
	worst, reason, best := "", "", -1
	for _, d := range devs {
		if r, ok := rank[d.State]; ok && r > best {
			worst, reason, best = d.State, d.Reason, r
		}
	}
	return worst, reason
}

// anyDeviceHealthy 는 노드에 아직 쓸 수 있는 장치가 남아 있는지다. Unknown 장치는 건전으로도
// 고장으로도 세지 않는다 — 관측 공백만으로 판정이 흔들리지 않게 하기 위해서다.
func anyDeviceHealthy(devs []DeviceResult) bool {
	for _, d := range devs {
		if d.State == StateHealthy {
			return true
		}
	}
	return false
}
