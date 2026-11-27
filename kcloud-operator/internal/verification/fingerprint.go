// ============================================================
// fingerprint.go: 환경 지문 계산
// 상세: 검증 근거가 어느 환경에서 나왔는지를 노드(bootID·kernel)와 NodeDeviceReport(driver·firmware),
// 그리고 요청 정책의 generation 으로 고정한다. 관측하지 못한 축은 비워 둔다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"sort"

	corev1 "k8s.io/api/core/v1"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// 지문 필드 이름 — AcceleratorVerificationPolicy 의 invalidateOn 값과 같은 어휘를 쓴다.
const (
	FieldBootID          = "BootID"
	FieldKernelVersion   = "KernelVersion"
	FieldDriverVersion   = "DriverVersion"
	FieldFirmwareVersion = "FirmwareVersion"
	FieldGeneration      = "Generation"
)

// Compute 는 노드 + NodeDeviceReport + 정책 generation 으로 지문을 만든다. vendor 가 비어 있지 않으면
// 그 벤더 장치만 본다 — 무관한 벤더의 드라이버 교체가 이 정책의 근거를 무효화하면 안 되기 때문이다.
// 같은 벤더 장치가 여럿이면 PCI 주소가 가장 작은 장치를 쓴다(결정론적 선택 — 순서가 흔들리면
// 아무것도 안 바뀐 상태에서도 evidence 가 계속 만료된다). 관측하지 못한 축은 비워 둔다 — 빈 값을
// "같다" 로 취급할지는 무효화 규칙(freshness.go)이 정하며, 여기서 그럴듯한 값을 채우지 않는다.
func Compute(node *corev1.Node, ndr *v1alpha1.NodeDeviceReport, vendor string, generation int64) v1alpha1.EnvironmentFingerprint {
	fp := v1alpha1.EnvironmentFingerprint{Generation: generation}
	if node != nil {
		fp.BootID = node.Status.NodeInfo.BootID
		fp.KernelVersion = node.Status.NodeInfo.KernelVersion
	}
	if d := pickDevice(ndr, vendor); d != nil {
		fp.DriverVersion = d.DriverVersion
		fp.FirmwareVersion = d.FirmwareVersion
	}
	return fp
}

// pickDevice 는 지문의 driver/firmware 를 읽을 장치를 고른다(없으면 nil).
func pickDevice(ndr *v1alpha1.NodeDeviceReport, vendor string) *v1alpha1.DeviceEntry {
	if ndr == nil {
		return nil
	}
	cands := make([]v1alpha1.DeviceEntry, 0, len(ndr.Status.Devices))
	for _, d := range ndr.Status.Devices {
		if vendor == "" || d.Vendor == vendor {
			cands = append(cands, d)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].PCIeAddress < cands[j].PCIeAddress })
	return &cands[0]
}

// ChangedFields 는 두 지문 사이에서 달라진 축의 이름을 정렬된 순서로 반환한다.
// 무효화 사유를 사람이 읽을 수 있게 남기는 것이 목적이라 "달라졌다" 만이 아니라 "어디가" 를 준다.
func ChangedFields(old, cur v1alpha1.EnvironmentFingerprint) []string {
	var out []string
	if old.BootID != cur.BootID {
		out = append(out, FieldBootID)
	}
	if old.DriverVersion != cur.DriverVersion {
		out = append(out, FieldDriverVersion)
	}
	if old.FirmwareVersion != cur.FirmwareVersion {
		out = append(out, FieldFirmwareVersion)
	}
	if old.Generation != cur.Generation {
		out = append(out, FieldGeneration)
	}
	if old.KernelVersion != cur.KernelVersion {
		out = append(out, FieldKernelVersion)
	}
	sort.Strings(out)
	return out
}
