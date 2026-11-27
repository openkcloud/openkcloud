// ============================================================
// precondition.go: MIG apply 전제 2종 분리 — 항상(PCI·관측) / hardware변경시(cordon·idle·MIG-safe)
// 상세: no-diff Ready 는 항상-전제만. fail-closed.
// 생성일: 2026-07-24 | 수정일: 2026-07-24
// ============================================================
package nvidia

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MigDevice struct {
	PCI, ModeCurrent, ModePending, Geometry, ObsError string
}

type AlwaysResult struct {
	PCIMissing, ObservationFailed, DriverReady bool
}
type HWResult struct {
	NodeCordoned, GPUBusy, MigUnsafe, MigModeNotEnabled bool
}

func podHoldsGPU(pod corev1.Pod) bool {
	for _, ctr := range pod.Spec.Containers {
		for name := range ctr.Resources.Limits {
			n := string(name)
			if n == "nvidia.com/gpu" || strings.HasPrefix(n, "nvidia.com/mig-") {
				return true
			}
		}
	}
	return false
}

// migSafe 는 GI 생성 안전인지다: 관측 성공 + MIG mode Enabled(사전조건) + 기존 GI 없음
// (geometry 빈 문자열). Disabled/Unknown/기존-GI(geometry 비어있지 않음)는 unsafe(fail-closed).
func migSafe(d MigDevice) bool {
	return d.ObsError == "" && d.ModeCurrent == modeEnabled && d.Geometry == ""
}

// CheckAlways 는 어느 상태에서나 필요한 전제다(PCI·관측 유효성).
func CheckAlways(devs []MigDevice, driverReady bool) AlwaysResult {
	r := AlwaysResult{DriverReady: driverReady}
	for _, d := range devs {
		if d.PCI == "" {
			r.PCIMissing = true
		}
		if d.ObsError != "" || d.ModeCurrent == "Unknown" {
			r.ObservationFailed = true
		}
	}
	return r
}

// CheckHardwareChange 는 hardware 변경(mutation) 직전에만 필요한 전제다.
func CheckHardwareChange(ctx context.Context, c client.Client, node string, devs []MigDevice) (HWResult, error) {
	var r HWResult
	var n corev1.Node
	if err := c.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return r, err
	}
	r.NodeCordoned = n.Spec.Unschedulable
	for _, d := range devs {
		switch {
		case d.ModeCurrent != modeEnabled:
			// MIG mode 는 외부 사전조건 — 미enable 이면 별도 reason 으로 구분한다.
			r.MigModeNotEnabled = true
		case !migSafe(d):
			// Enabled 이나 기존 GI 존재(관리 밖) → ExistingMigConfiguration.
			r.MigUnsafe = true
		}
	}
	var pods corev1.PodList
	if err := c.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node}); err != nil {
		return r, err
	}
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if podHoldsGPU(p) {
			r.GPUBusy = true
			break
		}
	}
	return r, nil
}
