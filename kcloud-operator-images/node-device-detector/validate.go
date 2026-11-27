// ============================================================
// validate.go: node-agent Validation 능력 (5-step 비특권 검증)
// 상세: Snapshot + allocatable(k8s) + host FS 로 벤더별 5단계 검증(NDR.status.validation)을
// 만든다. driverModule/deviceNode/devicePlugin/sampleWorkload 는 게이트, runtime/CLI 는 best-effort 다.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// vendorProbe 는 한 벤더에 대한 검증 원천 신호를 담는다(FS/k8s 조회 결과).
// buildVendorSteps 는 이 순수 입력만으로 step 을 만들어 단위 테스트가 쉽다.
type vendorProbe struct {
	driverLoaded bool
	driverVer    string
	deviceNode   bool
	devGlobKnown bool
	allocatable  int64
	runtimeOK    bool
	runtimeNA    bool   // 벤더 runtime 검사 비적용(비-nvidia)
	cliSnapshot  string // best-effort 벤더 CLI 한 줄(미가용 시 공란)
	// excluded 는 이 노드가 operator 판정으로 배포 대상에서 제외됐는지다(kcloud.ai/excluded
	// 라벨, main 이 Node 객체에서 읽어 넘긴다). detector 는 배제 규칙을 다시 계산하지
	// 않는다 — 라벨만 읽는다. true 면 devicePlugin/sampleWorkload 가 해당없음이 된다.
	excluded bool
	// excludedReason 은 operator 가 발행한 배제 사유 라벨 값 그대로다(control-plane | policy).
	// 비어 있으면 사유 없이 배제 사실만 적는다 — 사유를 추측하지 않는다.
	excludedReason string
	// userspaceVer 는 host 의 libnvidia-ml.so.1 링크 대상 버전(NVIDIA 만, 비어있으면 미가용).
	// driverVer(커널 모듈)와 대조해 패키지만 올리고 모듈을 재적재하지 않은 상태를 잡는다.
	userspaceVer string
}

// excludedMessage 는 배제 노드의 devicePlugin/sampleWorkload 사유 문구다.
//
// 사유는 operator 가 라벨로 발행한 값을 그대로 옮긴다. 문구에 "control-plane" 을 박아 두면
// excludeNodeSelector 로 배제된 워커(사유 policy)의 보고서에 거짓이 적힌다 — detector 는
// 배제 판정도 사유도 스스로 계산하지 않는다.
func excludedMessage(reason string) string {
	const base = "노드가 배포 대상에서 제외됨"
	if reason == "" {
		return base
	}
	return base + "(" + reason + ")"
}

// Validate 는 Snapshot 과 노드 allocatable 로 검증을 수행한다.
// 가속기 device 가 없으면 nil(오탐 0). allocatable·excluded·excludedReason 은 caller(main)가
// k8s Node 에서 읽어 넘긴다 — validate 를 k8s 의존에서 분리해 테스트 가능하게 하기 위함이다.
func Validate(snap *Snapshot, allocatable map[string]int64, excluded bool, excludedReason string) *ValidationResult {
	if snap == nil || len(snap.Devices) == 0 {
		return nil
	}
	var vendors []string
	seen := map[string]bool{}
	perVendor := map[string][]ValidationStep{}
	for _, d := range snap.Devices {
		key := vendorKey(d.vendor, d.model)
		if seen[key] {
			continue
		}
		seen[key] = true
		vendors = append(vendors, key)
		perVendor[key] = buildVendorSteps(key, probeVendor(key, d, allocatable, excluded, excludedReason))
	}
	return aggregate(vendors, perVendor)
}

// probeVendor 는 벤더 하나의 검증 원천을 수집한다(FS/exec/allocatable).
func probeVendor(key string, d Detected, allocatable map[string]int64, excluded bool, excludedReason string) vendorProbe {
	p := vendorProbe{
		driverLoaded:   d.loaded,
		driverVer:      d.ver,
		allocatable:    allocatableForVendor(key, allocatable),
		excluded:       excluded,
		excludedReason: excludedReason,
	}
	globs := vendorDevGlobs[key]
	p.devGlobKnown = len(globs) > 0
	p.deviceNode = anyGlobMatches(globs)
	if key == "nvidia" {
		p.runtimeOK = nvidiaRuntimePresent()
		p.userspaceVer = nvidiaUserspaceVersion()
	} else {
		p.runtimeNA = true
	}
	p.cliSnapshot = vendorCLISnapshot(key)
	return p
}

// buildVendorSteps 는 순수 함수 — vendorProbe 만으로 5 step 을 만든다.
// 게이트 step(driverModule/deviceNode/devicePlugin/sampleWorkload)은 확정 판정,
// runtime 은 best-effort 정보(overall passed 에서 제외)로 둔다.
func buildVendorSteps(key string, p vendorProbe) []ValidationStep {
	steps := make([]ValidationStep, 0, 5)

	// ① driverModule — 드라이버 모듈 로드 + 버전. 커널 모듈과 사용자공간 라이브러리는 같은 버전이어야
	// 한다 — 패키지만 올리고 모듈을 재적재하지 않으면 nvidia-smi 가 "Driver/library version
	// mismatch" 로 죽는다. 이 판정은 배제 여부와 무관하다(배제는 devicePlugin/sampleWorkload 만
	// 가린다). 한쪽 버전을 못 읽으면 대조하지 않되, "대조해서 같았다" 와 "대조를 못 했다" 를
	// 메시지로 구분한다 — partition/nvidia 의 Verified vs VerificationRequired 와 같은 구별이다.
	dm := ValidationStep{Name: "driverModule"}
	switch {
	case !p.driverLoaded:
		dm.Message = "driver module not loaded"
	case p.driverVer != "" && p.userspaceVer != "" && p.driverVer != p.userspaceVer:
		dm.Message = fmt.Sprintf("kernel/userspace mismatch: kernel=%s userspace=%s", p.driverVer, p.userspaceVer)
	default:
		dm.Passed = true
		msg := "driver loaded"
		if p.driverVer != "" {
			msg += " version=" + p.driverVer
		}
		switch {
		case p.driverVer != "" && p.userspaceVer != "":
			msg += "; userspace=" + p.userspaceVer + " 대조 일치"
		case key == "nvidia":
			// NVIDIA 만 사용자공간 버전을 채우므로 이 구별도 NVIDIA 에서만 뜻이 있다.
			msg += "; userspace 버전 미확인 — 대조 보류"
		}
		if p.cliSnapshot != "" {
			msg += "; cli=" + p.cliSnapshot
		}
		dm.Message = msg
	}
	steps = append(steps, dm)

	// ② deviceNode — /dev 노드 존재(best-effort)
	dn := ValidationStep{Name: "deviceNode"}
	switch {
	case p.deviceNode:
		dn.Passed = true
		dn.Message = "device node present"
	case !p.driverLoaded:
		dn.Passed = false
		dn.Message = "no device node (driver not loaded)"
	case !p.devGlobKnown:
		dn.Passed = true
		dn.Message = "device node glob 미정의(best-effort skip)"
	default:
		// 드라이버는 로드됐으나 알려진 glob 미매치 → NPU 경로 미확정 가능.
		// 오탐(false FAIL) 방지를 위해 soft-pass 하고 사유를 남긴다.
		dn.Passed = true
		dn.Message = "device node glob 미매치(best-effort, 벤더 경로 미확정)"
	}
	steps = append(steps, dn)

	// ③ devicePlugin — allocatable>0 (k8s). 배제 노드는 배포 대상이 아니므로 해당없음.
	dp := ValidationStep{Name: "devicePlugin"}
	switch {
	case p.excluded:
		dp.NotApplicable = true
		dp.Passed = true
		dp.Message = excludedMessage(p.excludedReason)
	case p.allocatable > 0:
		dp.Passed = true
		dp.Message = fmt.Sprintf("allocatable=%d", p.allocatable)
	default:
		dp.Message = "allocatable=0 (device-plugin 미등록/미가용)"
	}
	steps = append(steps, dp)

	// ④ runtime — 컨테이너 런타임/툴킷(NVIDIA 만 검사, best-effort · overall 제외)
	rt := ValidationStep{Name: "runtime"}
	switch {
	case p.runtimeNA:
		rt.Passed = true
		rt.Message = "n/a (벤더 runtime 검사 없음)"
	case p.runtimeOK:
		rt.Passed = true
		rt.Message = "container runtime/toolkit present"
	default:
		rt.Passed = false
		rt.Message = "nvidia container runtime/toolkit 미검출(best-effort)"
	}
	steps = append(steps, rt)

	// ⑤ sampleWorkload — 1차 간접 판정(allocatable>0 + device-plugin Ready).
	// 실제 리소스 요청 Pod spawn 은 node-agent 권한 밖 → operator Job 위임(v1.1).
	// 배제 노드는 devicePlugin 과 같은 사유로 해당없음이다.
	sw := ValidationStep{Name: "sampleWorkload"}
	switch {
	case p.excluded:
		sw.NotApplicable = true
		sw.Passed = true
		sw.Message = excludedMessage(p.excludedReason)
	case p.allocatable > 0:
		sw.Passed = true
		sw.Message = "간접 판정(allocatable>0). 실제 Pod spawn 은 operator Job 위임(v1.1)"
	default:
		sw.Message = "간접 판정 실패(allocatable=0)"
	}
	steps = append(steps, sw)

	return steps
}

// gateSteps 는 overall passed 집계에 포함되는 확정 판정 step 이름들이다.
// runtime 은 best-effort 라 제외해 healthy 노드 false FAIL 을 방지한다.
var gateSteps = map[string]bool{
	"driverModule":   true,
	"deviceNode":     true,
	"devicePlugin":   true,
	"sampleWorkload": true,
}

// aggregate 는 벤더별 step 을 단일 ValidationResult 로 합친다.
// 멀티벤더 노드는 step 이름에 "<vendor>/" 접두사를 붙여 FAIL step 식별을 유지한다.
// overall Passed 는 gateSteps 전부 통과일 때만 true. NotApplicable 단계는 게이트
// 실패로 세지 않는다 — 통과도 실패도 아닌 상태이기 때문이다.
func aggregate(vendors []string, perVendor map[string][]ValidationStep) *ValidationResult {
	res := &ValidationResult{Passed: true, LastRunTime: nowFunc(), Vendor: strings.Join(vendors, ",")}
	multi := len(vendors) > 1
	for _, key := range vendors {
		for _, s := range perVendor[key] {
			name := s.Name
			if multi {
				name = key + "/" + s.Name
			}
			res.Steps = append(res.Steps, ValidationStep{Name: name, Passed: s.Passed, NotApplicable: s.NotApplicable, Message: s.Message})
			if gateSteps[s.Name] && !s.NotApplicable && !s.Passed {
				res.Passed = false
			}
		}
	}
	return res
}

/* ---------- probing 헬퍼(비순수) ---------- */

// allocatableResourceForVendor 는 노드 allocatable 에서 벤더 리소스명·수량을 찾는다.
// 정확 매칭(values.yaml 기준) 우선, 실패 시 벤더 substring fallback(CR 재정의 대응).
// 반환: (매칭 리소스명, 수량, 발견여부).
func allocatableResourceForVendor(key string, alloc map[string]int64) (string, int64, bool) {
	names := matchedResourceNames(key, alloc)
	if len(names) == 0 {
		return "", 0, false
	}
	var total int64
	for _, n := range names {
		total += alloc[n]
	}
	// 대표 이름은 수량이 있는 것을 우선한다 — MIG 노드에서 nvidia.com/gpu=0 을 대표로 적으면
	// 메시지가 "0개 광고" 로 읽힌다. 동률이면 정렬 순서로 결정론을 지킨다.
	rep := names[0]
	for _, n := range names {
		if alloc[n] > alloc[rep] {
			rep = n
		}
	}
	return rep, total, true
}

// matchedResourceNames 는 벤더에 속하는 allocatable 키를 중복 없이 정렬해 돌려준다.
//
// 첫 매칭 하나만 보면 **하나의 물리 장치가 프로파일별 리소스명으로 갈리는 경우를 놓친다.**
// MIG 를 적용한 노드는 nvidia.com/gpu=0 과 nvidia.com/mig-1g.6gb=8 을 함께 광고하는데,
// 정확 매칭이 nvidia.com/gpu 에서 끝나 0 을 돌려주면 device-plugin 이 죽은 것으로 판정돼,
// validation.passed 가 내려가 파티션 정책 검증까지 막힌다(A30 2장 MIG 적용 실측).
func matchedResourceNames(key string, alloc map[string]int64) []string {
	seen := map[string]bool{}
	var out []string
	mark := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, rn := range vendorResourceNames[key] {
		if _, ok := alloc[rn]; ok {
			mark(rn)
		}
	}
	for _, sub := range vendorSubstrings[key] {
		subL := strings.ToLower(sub)
		for k := range alloc {
			if strings.Contains(strings.ToLower(k), subL) {
				mark(k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// allocatableForVendor 는 벤더 리소스 수량만 반환한다(없으면 0).
func allocatableForVendor(key string, alloc map[string]int64) int64 {
	_, v, _ := allocatableResourceForVendor(key, alloc)
	return v
}

// anyGlobMatches 는 컨테이너 /dev 네임스페이스와 마운트된 host /dev(/host/dev) 양쪽에서
// glob 매치를 시도한다(H() 는 /dev 를 prefix 하지 않으므로 host mount 는 별도 확인).
func anyGlobMatches(globs []string) bool {
	for _, g := range globs {
		if countGlob(g) > 0 {
			return true
		}
		if countGlob(filepath.Join(hostPrefix, g)) > 0 {
			return true
		}
	}
	return false
}

// userspaceLibSoname 은 NVML 소비자(nvidia-smi 등)가 실제로 dlopen 하는 이름이다.
// 이 링크의 대상이 그 프로세스가 쓰는 사용자공간 버전이다.
const userspaceLibSoname = "libnvidia-ml.so.1"

// libSonamePrefix 는 링크 대상 파일명에서 버전만 남기기 위해 떼는 접두다.
const libSonamePrefix = "libnvidia-ml.so."

// userspaceNvidiaVersion 은 libDir/libnvidia-ml.so.1 링크 대상에서 버전을 읽는다. 디렉터리를
// 훑어 파일명에서 버전을 뽑는 방식은 두 번 틀린다 — 자릿수를 가정하면 open kernel module 의
// 2-component 버전(595.84)을 못 잡고(scan.go 의 reNvrmVer 와 같은 함정), cross-major 전이 중
// 구 패키지가 남아 여러 버전 파일이 공존하면 구버전을 집어 거짓 불일치를 낸다. 로더가 실제로
// 여는 링크 대상 하나만 보면 둘 다 피하고, 링크가 없으면 빈 문자열로 대조를 보류한다.
func userspaceNvidiaVersion(libDir string) string {
	target, err := os.Readlink(filepath.Join(libDir, userspaceLibSoname))
	if err != nil {
		return "" // 링크 부재/일반 파일/디렉터리 없음 전부 여기로 온다
	}
	// 대상은 절대경로("/usr/lib/.../libnvidia-ml.so.595.84")일 수도 상대경로
	// ("libnvidia-ml.so.595.84")일 수도 있다 — 파일명만 쓰므로 둘 다 같게 다룬다.
	name := filepath.Base(target)
	if !strings.HasPrefix(name, libSonamePrefix) {
		return ""
	}
	// 자릿수는 박지 않는다. scan.go 가 드라이버 버전에 쓰는 것과 같은 판정을 재사용한다.
	if v := strings.TrimPrefix(name, libSonamePrefix); reSemverish.MatchString(v) {
		return v
	}
	return ""
}

// nvidiaUserspaceVersion 은 hostLibraryPath 의 후보 디렉터리(이미 /host 접두 포함)를
// 순서대로 시도해 첫 성공을 돌려준다(배포판마다 라이브러리 경로가 다르다).
func nvidiaUserspaceVersion() string {
	for _, dir := range strings.Split(hostLibraryPath, ":") {
		if v := userspaceNvidiaVersion(dir); v != "" {
			return v
		}
	}
	return ""
}

// nvidiaRuntimePresent 는 host 에 NVIDIA 컨테이너 런타임/툴킷 존재를 best-effort 로 본다.
func nvidiaRuntimePresent() bool {
	return nvidiaUserlandPresent() ||
		nvidiaToolkitPkgInstalled() ||
		hostExists("/usr/bin/nvidia-container-runtime")
}

// vendorCLISnapshot 은 벤더 CLI 를 host mount 로 best-effort exec 해 한 줄 요약을 얻는다.
// distroless nonroot 에서 host 바이너리 exec 은 라이브러리 의존으로 실패할 수 있으므로,
// 어떤 실패든 공란을 반환하며 PASS 판정에는 영향을 주지 않는다.
func vendorCLISnapshot(key string) string {
	var bin string
	var args []string
	switch key {
	case "nvidia":
		bin, args = "/usr/bin/nvidia-smi", []string{"-L"}
	default:
		return "" // 그 외 벤더 CLI 경로 미확정 → skip
	}
	if !hostExists(bin) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, H(bin), args...).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}
