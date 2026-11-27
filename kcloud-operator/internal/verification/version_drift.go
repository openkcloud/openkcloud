// ============================================================
// version_drift.go: 드라이버 버전 선언값과 실측값의 어긋남 감지 (감지 전용 — 되돌리지 않는다)
// 상세: DUS 가 보고한 선언 버전과 NDR 실측 버전의 어긋남을 감지 전용으로 잡는다. 업그레이드
// 사이클 중 불일치는 억제하고, 유예 시간을 넘겨야 확정한다 — 자동 재설치는 하지 않는다.
// 생성일: 2026-09-17 | 수정일: 2026-10-13
// ============================================================
package verification

import (
	"fmt"
	"time"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// VersionDriftVerdict 는 버전 드리프트 판정이다.
type VersionDriftVerdict string

const (
	// VersionDriftNone 은 선언값과 실측값이 같다.
	VersionDriftNone VersionDriftVerdict = "none"
	// VersionDriftSuppressed 는 업그레이드 사이클 중이라 판정하지 않는다.
	VersionDriftSuppressed VersionDriftVerdict = "suppressed"
	// VersionDriftUnknown 은 실측값이 없어 판정할 수 없다.
	VersionDriftUnknown VersionDriftVerdict = "unknown"
	// VersionDriftPending 은 어긋났으나 아직 유예 시간 안이다.
	VersionDriftPending VersionDriftVerdict = "pending"
	// VersionDriftConfirmed 는 유예를 넘겨 확정된 어긋남이다.
	VersionDriftConfirmed VersionDriftVerdict = "confirmed"
)

// VersionDriftInput 은 판정에 필요한 값 전부다. 수집은 호출자가 한다.
type VersionDriftInput struct {
	// State 는 DriverUpgradeState.status.state 다. 버전 관측 상태(Idle·UnverifiedVersion·HostAhead)가
	// 아니면 억제한다 — 판정 집합은 v1alpha1.VersionObservationAllowed 가 갖는다.
	State string
	// DeclaredVersion 은 DUS 가 들고 있는 현재 버전(설치 시점 기록)이다.
	DeclaredVersion string
	// ObservedVersion 은 NodeDeviceReport 가 보고한 실제 로드 버전이다. 비면 판정 불가다.
	ObservedVersion string
	// ObservedAt 은 그 관측 시각이다.
	ObservedAt time.Time
	// Since 는 이 불일치가 처음 관측된 시각이다. 호출자가 상태에 보관한다.
	Since time.Time
	// Grace 는 확정까지 기다리는 시간이다.
	Grace time.Duration
}

// EvaluateVersionDrift 는 버전 어긋남을 판정하고 사유를 함께 돌려준다.
//
// 순서가 계약이다: 관측 없음 → 억제 → 일치 → 디바운스. 이것은 drift.go 의 EvaluateDrift 와
// 같은 순서다 — 비교 기준(관측)이 없으면 억제도 일치도 판정할 수 없으므로 맨 먼저 본다.
// (drift.go 는 기준 부재를 None 으로 접고, 버전 축은 Unknown 으로 구분한다. 두 검출기가
// 게이트 순서에서 갈라지지 않게 맞춘다.)
func EvaluateVersionDrift(in VersionDriftInput) (VersionDriftVerdict, string) {
	if in.ObservedVersion == "" {
		return VersionDriftUnknown, "실측 버전이 없다 — 확인하지 못한 것을 어긋남으로 읽지 않는다"
	}
	if !v1alpha1.VersionObservationAllowed(in.State) {
		return VersionDriftSuppressed, fmt.Sprintf("상태가 %q 라 판정하지 않는다", in.State)
	}
	if in.DeclaredVersion == in.ObservedVersion {
		return VersionDriftNone, ""
	}
	if in.Since.IsZero() || in.ObservedAt.Sub(in.Since) < in.Grace {
		return VersionDriftPending, fmt.Sprintf(
			"선언 %s ↔ 실측 %s 가 어긋나나 유예 %s 를 아직 넘지 않았다",
			in.DeclaredVersion, in.ObservedVersion, in.Grace)
	}
	return VersionDriftConfirmed, fmt.Sprintf(
		"선언 %s ↔ 실측 %s 가 %s 이상 어긋난 채 유지된다 — 호스트가 operator 밖에서 바뀌었을 수 있다. "+
			"감지 전용이므로 자동으로 되돌리지 않는다",
		in.DeclaredVersion, in.ObservedVersion, in.Grace)
}
