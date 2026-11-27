// ============================================================
// verify.go: descriptor 로 만들 수 있는 backend 를 컴파일 전에 판정한다
// 상세: 거절을 값으로 돌려준다. 조용히 빈 목록만 주면 관리자가 왜 backend 가 안
//
//	나왔는지 되짚을 수 없다 - 후보 0 의 사유를 잃는 것이 가장 나쁘다.
//
// 생성일: 2026-08-10
// ============================================================
package descriptor

import (
	"fmt"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// FeasibleBackends 는 선언된 backend 중 실제로 생성 가능한 것과, 거절된 것의 사유를 낸다.
// 선언하지 않은 backend 는 가능하더라도 목록에 넣지 않는다 - 판정기는 선언을 넓히지 않는다.
func FeasibleBackends(spec npuv1alpha1.AcceleratorDescriptorSpec) (
	[]string, []npuv1alpha1.DescriptorRefusal) {

	declared := spec.Backends
	var ok []string
	var refusals []npuv1alpha1.DescriptorRefusal

	refuse := func(b, reason string) {
		refusals = append(refusals, npuv1alpha1.DescriptorRefusal{Backend: b, Reason: reason})
	}

	// 정합이 깨졌으면 어느 쪽으로도 못 간다. 선언된 전부에 같은 사유를 남긴다.
	if errs := Validate(spec); len(errs) > 0 {
		reason := fmt.Sprintf("descriptor 정합 오류: %v", errs.ToAggregate())
		for _, b := range declared {
			refuse(b, reason)
		}
		return nil, refusals
	}

	for _, b := range declared {
		switch b {
		case npuv1alpha1.BackendDevicePlugin:
			// Validate 가 장치 노드 ≥ 1 을 이미 보장한다.
			ok = append(ok, b)

		case npuv1alpha1.BackendDRA:
			if !spec.Identity.StableAcrossReboot {
				refuse(b, "식별자가 재부팅을 건너 안정하지 않다 — "+
					"ResourceSlice 의 장치 이름이 바뀌면 기존 claim 이 그 장치를 되짚지 못한다")
				continue
			}
			ok = append(ok, b)

		default:
			// Validate 가 걸렀어야 하는 값이다. 여기 오면 판정 규칙이 갈린 것이므로 남긴다.
			refuse(b, fmt.Sprintf("모르는 backend: %s", b))
		}
	}

	return ok, refusals
}
