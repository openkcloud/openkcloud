// ============================================================
// probe.go: 벤더별 기능 확인 probe 명령표
// 상세: ACPP 검증기가 파티션·공유 적용 뒤 "장치가 실제로 보이는가" 를 컨테이너 안에서
// 묻는 한 줄 명령이다. 드라이버 어댑터(driveradapter)와 관심사가 달라(설치 생애주기 대
// 검증) 별도 표로 둔다(이미지·바이너리 경로 주입 방식은 registry 선언 주석 참고).
// 생성일: 2026-09-30 | 수정일: 2026-10-11
// ============================================================
package verifyprobe

import "sort"

// Probe 는 벤더 하나의 probe 명령이다.
type Probe struct {
	Command    string
	Args       []string
	Privileged bool
	HostPID    bool
}

// registry 의 키는 vendorcap 벤더 어휘다. rngd 는 furiosa 의 product 라 furiosa 항목을 쓴다.
// 이미지·호스트 바이너리 경로는 컨트롤러가 ACPP_PROBE_IMAGE_<VENDOR>·
// ACPP_PROBE_HOSTBIN_<VENDOR> 환경변수로 채운다(배포 환경마다 다르다).
var registry = map[string]Probe{
	"nvidia":      {Command: "nvidia-smi", Args: []string{"-L"}},
	"tenstorrent": {Command: "tt-smi", Args: []string{"-s"}},
	"furiosa":     {Command: "furiosa-smi", Args: []string{"info"}},
	"rebellions":  {Command: "rbln-stat", Privileged: true, HostPID: true},
}

// Lookup 은 벤더 이름의 probe 를 돌려준다. 미등록 벤더는 false 다.
func Lookup(vendor string) (Probe, bool) {
	p, ok := registry[vendor]
	return p, ok
}

// Vendors 는 등록된 벤더 이름을 정렬해 돌려준다(시험·표 렌더용, map 순회 순서에 기대지 않는다).
func Vendors() []string {
	out := make([]string, 0, len(registry))
	for v := range registry {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
