// ============================================================
// naming.go: 드라이버 DaemonSet 이름 생성 규칙 중앙화
// 상세: driver_daemonset_controller(생성)와 upgrade/state_machine(조회)이
//       동일한 DS 이름을 사용하도록 단일 헬퍼로 통일한다. 불일치 시
//       업그레이드 상태머신이 드라이버 DS를 찾지 못한다.
// 생성일: 2026-06-02 | 수정일: 2026-06-02
// ============================================================

// Package naming centralizes Kubernetes resource naming rules shared across
// the controller and upgrade packages to avoid divergence.
package naming

import "strings"

// DriverDSName returns the driver DaemonSet name for a given vendor/model.
//
// Mapping:
//   - nvidia      → "kcloud-nvidia-driver"     (model 무시)
//   - furiosa     → "kcloud-furiosa-<model>-driver" (model 비면 "kcloud-furiosa-driver")
//   - default     → "kcloud-<vendor>[-<model>]-driver"
//
// 빈 model 안전성: model 이 비어 있으면 이중 하이픈("--") 없이 model 세그먼트를 생략한다.
func DriverDSName(vendor, model string) string {
	v := strings.ToLower(vendor)
	m := strings.ToLower(model)
	switch v {
	case "nvidia":
		return "kcloud-nvidia-driver"
	case "furiosa":
		if m == "" {
			return "kcloud-furiosa-driver"
		}
		return "kcloud-furiosa-" + m + "-driver"
	default:
		if m == "" {
			return "kcloud-" + v + "-driver"
		}
		return "kcloud-" + v + "-" + m + "-driver"
	}
}
