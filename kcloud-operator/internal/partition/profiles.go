// ============================================================
// profiles.go: 노드 비의존 레이아웃 검증 — webhook·backend 공유 함수
// 상세: 형식·단일성·개수 경계만 판정한다. 프로파일 지원 여부는 노드 관측(mig -lgip)이 있어야
// 알 수 있어 단정하지 않는다(nvidia/backend.go 의 Validate 참조).
// 생성일: 2026-08-24 | 수정일: 2026-10-11
// ============================================================
package partition

import (
	"fmt"
	"regexp"
	"strings"

	"kcloud-operator/internal/upgrade"
)

// nvidiaProfileRe 는 MIG profile 표기 <digit>g.<digit>gb 를 앵커 검증한다(느슨한 substring 금지).
var nvidiaProfileRe = regexp.MustCompile(`^\d+g\.\d+gb$`)

// IsNvidiaMIGProfile 은 MIG 프로파일 표기(<digit>g.<digit>gb)인지 본다. webhook 과 nvidia backend 가
// 같은 판정을 쓰도록 여기 하나만 둔다 — 두 벌이면 한쪽만 고쳐 admission 과 컨트롤러가 갈린다.
func IsNvidiaMIGProfile(profile string) bool { return nvidiaProfileRe.MatchString(profile) }

// ValidateLayoutShape 는 노드를 보지 않고 판정할 수 있는 것만 본다 — 형식·단일성·개수 경계다.
// 프로파일이 그 제품에서 실제로 지원되는지는 노드 관측(nvidia-smi mig -lgip)이 있어야 알 수
// 있어 여기서는 단정하지 않는다(nvidia/backend.go 의 supported 표가 그 역할이다).
//
// 빈 레이아웃은 통과시킨다 — sharing 만 있는 정책이 정상이고, 레이아웃·sharing 이 둘 다
// 비었는지는 webhook(admission 레이어)이 ACPP 전체를 보고 판정한다.
func ValidateLayoutShape(vendor string, layout []Layout) error {
	if len(layout) == 0 {
		return nil
	}
	if len(layout) > 1 {
		return fmt.Errorf("partition: mixed placement unsupported, got %d layout entries", len(layout))
	}
	l := layout[0]
	if l.CountPerDevice <= 0 {
		return fmt.Errorf("partition: countPerDevice must be positive, got %d", l.CountPerDevice)
	}
	if vendor == "nvidia" && !IsNvidiaMIGProfile(l.Profile) {
		return fmt.Errorf("partition: invalid nvidia MIG profile format %q", l.Profile)
	}
	return nil
}

// migProfiles 는 확실히 아는 제품의 MIG 프로파일 표다. 기준: 이 저장소에 실측 근거
// (nvidia/testdata/lgip_*.txt 같은 `nvidia-smi mig -lgip` 캡처)가 있는 제품만 담는다 —
// 기억으로 채운 값은 여기 넣지 않는다. 근거 없이 채운 항목은 fail-open 을 뒤집는다: 값이
// 하나라도 틀리면 유효한 요청을 거절하고, 그건 표에 아예 없어 판정을 미루는 것보다 나쁘다.
var migProfiles = map[string][]string{
	// a30: internal/partition/nvidia/testdata/lgip_a30.txt 실측과 정확히 일치.
	"a30": {"1g.6gb", "2g.12gb", "4g.24gb"},
}

// KnownMIGProfiles 는 확실히 아는 제품의 MIG 프로파일 목록이다. 표에 없는 제품은 두 번째
// 반환값이 false 이고, 호출부는 판정하지 않는다(fail-open) — 표가 낡아 정상 요청을 막는 쪽이
// 잘못된 요청 하나를 통과시키는 쪽보다 나쁘다.
//
// product 는 detector 가 내는 슬러그(`a30`, `a100-pcie-40gb`)라 `-` 경계 접두어로 조회한다 —
// internal/upgrade.DeviceModelMatches 와 같은 관례이자 그 함수 자체다(순환 import 없음).
func KnownMIGProfiles(product string) ([]string, bool) {
	// generic 은 detector 가 제품을 판정하지 못했다는 표시다 — 표의 어떤 항목과도 같지 않다.
	// upgrade.DeviceModelMatches 는 원 도메인(업그레이드 정책 매칭)에서 generic 을 와일드카드로
	// 보므로 그대로 두면 표의 첫 항목에 걸린다(map 순회는 항목 하나뿐이라도 뒤집힌 fail-open —
	// "모르는데 걸림" — 을 만든다). 그 의미론은 이 자리로 옮겨오지 않는다.
	if product == "" || strings.EqualFold(product, "generic") {
		return nil, false
	}
	for known, profiles := range migProfiles {
		if upgrade.DeviceModelMatches(product, known) {
			return profiles, true
		}
	}
	return nil, false
}
