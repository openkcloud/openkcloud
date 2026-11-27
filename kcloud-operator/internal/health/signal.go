// ============================================================
// signal.go: health 신호·원인 어휘
// 상세: 정책의 Disabled·Remediation 키와 판정 코드가 같은 문자열을 쓰게 만드는 단일 출처다.
//
//	두 어휘가 갈라지면 정책이 조용히 아무것도 하지 않게 된다.
//
// 생성일: 2026-08-04
// ============================================================
package health

// 신호 6종.
const (
	SignalNodeReady       = "NodeReady"
	SignalNDRFreshness    = "NDRFreshness"
	SignalDriverHeartbeat = "DriverHeartbeat"
	SignalDevicePlugin    = "DevicePlugin"
	SignalAdvertisement   = "Advertisement"
	SignalRepeatedFailure = "RepeatedFailure"
)

// 원인 7종. 상태가 Healthy 가 아닐 때 "무엇 때문인가" 를 한 단어로 말한다.
const (
	ReasonNodeNotReady          = "NodeNotReady"
	ReasonObservationStale      = "ObservationStale"
	ReasonDriverNotLoaded       = "DriverNotLoaded"
	ReasonDevicePluginDown      = "DevicePluginDown"
	ReasonAdvertisementMismatch = "AdvertisementMismatch"
	ReasonRepeatedRecoveryFail  = "RepeatedRecoveryFailure"
	// ReasonPartialDeviceFailure 는 장치 일부만 못 쓰는 상태다(건전한 장치가 남아 있다).
	// 조치표(remediation.go)에 일부러 안 넣는다 — 노드 범위 복구(RecoverDevice)를 만들면
	// 혼재 노드에서 고장난 장치 하나가 멀쩡한 다른 장치까지 노드 통째로 복구 대상에 올린다.
	ReasonPartialDeviceFailure = "PartialDeviceFailure"
)
