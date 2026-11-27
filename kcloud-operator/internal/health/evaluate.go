// ============================================================
// evaluate.go: health 신호 판정과 상태 분류
// 상세: 클러스터를 읽지 않는 순수 함수다. 수집은 collect.go, 기록은 컨트롤러가 한다.
//
//	판정 우선순위가 규칙의 전부다 — 여러 신호가 동시에 깨지면 가장 보수적인 결론을 낸다.
//
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package health

import (
	"fmt"
	"sort"
	"time"
)

// 상태 5종 + Unknown. Unknown 은 "못 봤다" 이고 Healthy 도 Unhealthy 도 아니다.
const (
	StateUnknown     = "Unknown"
	StateHealthy     = "Healthy"
	StateDegraded    = "Degraded"
	StateUnhealthy   = "Unhealthy"
	StateQuarantined = "Quarantined"
	StateRecovering  = "Recovering"
)

// Inputs 는 판정에 필요한 관측값 전부다.
type Inputs struct {
	NodeReady bool
	// NDRObservedAt 은 노드 에이전트 보고의 관측 시각이다. nil 이면 보고 자체가 없거나 시각 미상이다.
	NDRObservedAt *time.Time
	DriverLoaded  bool
	// DriverFailures 는 드라이버 상태머신이 연속 실패한 횟수다.
	DriverFailures      int32
	DevicePluginReady   bool
	DevicePluginRolling bool
	// Expected 는 근거가 남긴 광고 기준선, Actual 은 현재 allocatable 이다.
	Expected map[string]int32
	Actual   map[string]int32
	// AdvertisementSuspectedAt 은 광고 부족을 처음 본 시각이다(유예 계산용).
	AdvertisementSuspectedAt *time.Time
	// RecoveryFailures 는 이 노드에서 종점 실패로 끝난 복구 작업 수다.
	RecoveryFailures int32
	// RecoveryInFlight 는 아직 끝나지 않은 복구 작업이 있는지다.
	RecoveryInFlight bool
	// Devices 는 장치별 관측값이다(PCI 를 식별할 수 있는 장치만 담긴다).
	Devices []DeviceInput
	Now     time.Time
}

// Signal 은 신호 하나의 판정이다.
type Signal struct {
	Name    string
	Target  string
	OK      bool
	Message string
}

// Result 는 노드 하나의 판정 결과다.
type Result struct {
	State             string
	Reason            string
	Signals           []Signal
	AllocationAllowed bool
	// Devices 는 장치별 판정이다. 노드 단위 신호가 먼저 걸리면(노드 미가용 등) 장치 판정이
	// 의미 없으므로 비어 있다.
	Devices []DeviceResult
}

// Evaluate 는 신호를 모아 상태 하나로 접는다.
//
// 우선순위: 반복 실패(격리) > 노드 미가용 > 관측 불가 > 복구 중 > 드라이버 부재 > 광고 주체 부재
// > 장치 고장(가장 나쁜 장치를 따름) > 광고 불일치 > 정상. 위쪽이 아래쪽을 덮는다 — 노드 단위
// 신호가 장치 축보다 먼저인 이유는 노드가 죽었거나 관측이 끊기면 장치별 판정 자체가 의미 없기 때문이다.
func Evaluate(in Inputs, p EffectivePolicy) Result {
	var sigs []Signal
	record := func(name, target string, ok bool, msg string) {
		sigs = append(sigs, Signal{Name: name, Target: target, OK: ok, Message: msg})
	}

	if p.Enabled(SignalRepeatedFailure) && in.RecoveryFailures >= p.RepeatedFailureThreshold {
		record(SignalRepeatedFailure, "", false,
			fmt.Sprintf("복구가 %d회 연속 실패했다", in.RecoveryFailures))
		return finish(StateQuarantined, ReasonRepeatedRecoveryFail, sigs, false)
	}
	record(SignalRepeatedFailure, "", true, "")

	if p.Enabled(SignalNodeReady) && !in.NodeReady {
		record(SignalNodeReady, "", false, "노드가 Ready 가 아니다")
		return finish(StateUnknown, ReasonNodeNotReady, sigs, false)
	}
	record(SignalNodeReady, "", true, "")

	if p.Enabled(SignalNDRFreshness) && !fresh(in.NDRObservedAt, in.Now, p.NDRFreshness) {
		record(SignalNDRFreshness, "", false, staleMessage(in.NDRObservedAt, in.Now))
		// 관측이 끊긴 상태에서 파괴적 복구를 만들지 않는다.
		return finish(StateUnknown, ReasonObservationStale, sigs, false)
	}
	record(SignalNDRFreshness, "", true, "")

	if in.RecoveryInFlight {
		record(SignalDevicePlugin, "", true, "복구 작업 진행 중")
		return finish(StateRecovering, "", sigs, false)
	}

	if p.Enabled(SignalDriverHeartbeat) && !in.DriverLoaded {
		record(SignalDriverHeartbeat, "", false, "드라이버가 로드돼 있지 않다")
		return finish(StateUnhealthy, ReasonDriverNotLoaded, sigs, false)
	}
	record(SignalDriverHeartbeat, "", true, "")

	if p.Enabled(SignalDevicePlugin) && !in.DevicePluginReady {
		record(SignalDevicePlugin, "", false, "device-plugin 이 Ready 가 아니다")
		return finish(StateUnhealthy, ReasonDevicePluginDown, sigs, false)
	}
	record(SignalDevicePlugin, "", true, "")

	devResults := EvaluateDevices(in.Devices, p)
	// 반환 경로와 무관하게 항상 싣는다 — 관측 실패 같은 장치 신호는 노드를 막지 않고도 남아야
	// status 에서 보인다. 조기 반환 분기 안에서만 붙이면 Healthy 로 끝나는 노드에서 관측 실패
	// 신호가 통째로 사라진다.
	for _, d := range devResults {
		sigs = append(sigs, d.Signals...)
	}
	if worst, reason := WorstDeviceState(devResults); worst != "" && worst != StateHealthy {
		// 노드 전체를 막아도 kubelet 광고(nvidia.com/gpu 등)는 그대로다 — AllocationAllowed
		// 는 우리 배치 경로에만 영향을 준다. 쓸 수 있는 장치가 하나라도 남아 있으면 노드를
		// 통째로 버리는 대신 Degraded 로 내려 그 장치만 배치 후보에서 빼게 한다(intent.ApplyHealth).
		state, allow := worst, false
		if anyDeviceHealthy(devResults) {
			// 원인도 부분 실패 전용 값으로 바꾼다. 고장난 장치의 원인(예: DriverNotLoaded)을
			// 그대로 노드 이유로 올리면 조치표가 노드 범위 RecoverDevice 를 만들어, 혼재 노드에서
			// 한 장치의 고장이 멀쩡한 장치의 드라이버 pod 까지 쿨다운마다 재시작시킨다. 조치표에
			// 없는 원인은 작업을 만들지 않는다는 기존 규율을 그대로 쓴다.
			state, reason, allow = StateDegraded, ReasonPartialDeviceFailure, true
		}
		res := finish(state, reason, sigs, allow)
		res.Devices = devResults
		return res
	}

	if p.Enabled(SignalAdvertisement) {
		if missing := shortfalls(in.Expected, in.Actual); len(missing) > 0 {
			switch {
			case in.DevicePluginRolling:
				// 롤아웃 중에는 광고가 잠시 빈다. 모르는 상태에서 고장을 선언하지 않는다.
				record(SignalAdvertisement, "", true, "롤아웃 중이라 판정을 미룬다")
			case graceElapsed(in.AdvertisementSuspectedAt, in.Now, p.AdvertisementGrace):
				record(SignalAdvertisement, "", false, "광고가 기준선에 못 미친다: "+missing[0])
				res := finish(StateDegraded, ReasonAdvertisementMismatch, sigs, false)
				res.Devices = devResults
				return res
			default:
				record(SignalAdvertisement, "", true, "유예 중")
			}
		} else {
			record(SignalAdvertisement, "", true, "")
		}
	}

	res := finish(StateHealthy, "", sigs, true)
	res.Devices = devResults
	return res
}

func finish(state, reason string, sigs []Signal, allow bool) Result {
	sort.Slice(sigs, func(i, j int) bool {
		if sigs[i].Name != sigs[j].Name {
			return sigs[i].Name < sigs[j].Name
		}
		return sigs[i].Target < sigs[j].Target
	})
	return Result{State: state, Reason: reason, Signals: sigs, AllocationAllowed: allow}
}

func fresh(at *time.Time, now time.Time, ttl time.Duration) bool {
	return at != nil && now.Sub(*at) <= ttl
}

func staleMessage(at *time.Time, now time.Time) string {
	if at == nil {
		return "노드 에이전트 보고가 없다"
	}
	return fmt.Sprintf("마지막 관측이 %s 전이다", now.Sub(*at).Round(time.Second))
}

func graceElapsed(first *time.Time, now time.Time, grace time.Duration) bool {
	if first == nil {
		return false // 이번이 첫 관측이면 유예를 시작만 한다
	}
	return now.Sub(*first) >= grace
}

// shortfalls 는 기준선에 못 미치는 자원을 사람이 읽을 문장으로 만든다(초과 광고는 문제 아님).
func shortfalls(expected, actual map[string]int32) []string {
	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		if actual[k] < expected[k] {
			out = append(out, fmt.Sprintf("%s=%d (기대 %d)", k, actual[k], expected[k]))
		}
	}
	return out
}

// AllowedTransition 은 상태 전이가 허용되는지다.
//
// 격리만 특별하다: 격리에서 나가는 길은 복구 중(Recovering) 하나뿐이다. 신호가 좋아졌다는 이유로
// 격리가 자동으로 풀리면, 복구가 반복 실패해 격리한 노드가 관측이 잠깐 좋아진 순간마다 다시
// 워크로드를 받는다 — 격리의 의미가 사라진다.
func AllowedTransition(from, to string) bool {
	if from == to || from == "" {
		return true
	}
	if from == StateQuarantined {
		return to == StateRecovering
	}
	return true
}
