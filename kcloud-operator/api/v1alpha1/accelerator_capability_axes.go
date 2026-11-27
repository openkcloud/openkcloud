// ============================================================
// accelerator_capability_axes.go: 가속기 capability 3축 타입 (sharing / isolation / allocationAPI)
// 상세: AcceleratorCapability 의 축을 별도 CRD 없이 ACPP status 에 흡수한다.
// 정직성 원칙 — 미실측은 Supported=false + Verification=Required 로 남긴다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

// 격리 등급. 낮은 등급 → 높은 등급 순.
const (
	IsolationNone      = "none"
	IsolationProcess   = "process"
	IsolationSubdevice = "subdevice"
	IsolationDevice    = "device"
	IsolationHardware  = "hardware"
)

// 검증 상태 — 실측 전 지원 주장을 금지하기 위한 축.
const (
	VerificationRequired      = "required"
	VerificationVerified      = "verified"
	VerificationNotApplicable = "notApplicable"
)

// 할당 API(노출 계층). devicePlugin 과 dra 둘 다 구현돼 있다.
const (
	// AllocationAPIAuto 는 "operator 가 고르라" 는 뜻이며 현재는 항상 devicePlugin 으로
	// 해석된다 — 자동 선택은 미구현이다.
	AllocationAPIAuto         = "auto"
	AllocationAPIDevicePlugin = "devicePlugin"
	AllocationAPIDRA          = "dra"
)

// SharingModeSupport 는 한 공유 방식의 지원·검증 상태다.
type SharingModeSupport struct {
	Supported bool `json:"supported"`
	// Verification 은 required|verified|notApplicable. 실측 전에는 required 로 남긴다.
	// +optional
	Verification string `json:"verification,omitempty"`
	// MaxReplicas 는 time-slicing 최대 replica(0=미정).
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
}

// SharingCapability 는 장치의 공유 방식별 지원 상태다.
type SharingCapability struct {
	// +optional
	TimeSlicing SharingModeSupport `json:"timeSlicing,omitempty"`
	// +optional
	MultiProcess SharingModeSupport `json:"multiProcess,omitempty"`
	// +optional
	Brokered SharingModeSupport `json:"brokered,omitempty"`
}

// IsolationCapability 는 실측·문서 근거가 있는 격리 등급이다. 근거 없으면 빈 문자열로 둔다.
type IsolationCapability struct {
	// +optional
	Compute string `json:"compute,omitempty"`
	// +optional
	Memory string `json:"memory,omitempty"`
	// +optional
	Fault string `json:"fault,omitempty"`
}
