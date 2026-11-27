// ============================================================
// validate_test.go: Validation 능력 단위 테스트
// 상세: buildVendorSteps/aggregate(순수) + Validate/allocatableForVendor 검증. 5-step 매핑,
// FAIL step 식별, 멀티벤더 접두사, allocatable 매칭을 커버한다.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stepByName 은 step 슬라이스에서 이름으로 단계를 찾는다(테스트 헬퍼).
func stepByName(steps []ValidationStep, name string) (ValidationStep, bool) {
	for _, s := range steps {
		if s.Name == name {
			return s, true
		}
	}
	return ValidationStep{}, false
}

func TestBuildVendorSteps_Healthy(t *testing.T) {
	p := vendorProbe{
		driverLoaded: true, driverVer: "580.65.06",
		deviceNode: true, devGlobKnown: true,
		allocatable: 2, runtimeOK: true,
	}
	steps := buildVendorSteps("nvidia", p)
	if len(steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(steps))
	}
	for _, name := range []string{"driverModule", "deviceNode", "devicePlugin", "runtime", "sampleWorkload"} {
		s, ok := stepByName(steps, name)
		if !ok {
			t.Fatalf("missing step %q", name)
		}
		if !s.Passed {
			t.Errorf("step %q expected pass, got fail: %s", name, s.Message)
		}
	}
}

func TestBuildVendorSteps_DriverUnloaded(t *testing.T) {
	// rmmod 주입 상황: 드라이버 미로드 + allocatable 0.
	p := vendorProbe{driverLoaded: false, devGlobKnown: true, allocatable: 0}
	steps := buildVendorSteps("nvidia", p)
	dm, _ := stepByName(steps, "driverModule")
	if dm.Passed {
		t.Errorf("driverModule should fail when driver unloaded")
	}
	dn, _ := stepByName(steps, "deviceNode")
	if dn.Passed {
		t.Errorf("deviceNode should fail when driver unloaded and no node present")
	}
	dp, _ := stepByName(steps, "devicePlugin")
	if dp.Passed {
		t.Errorf("devicePlugin should fail when allocatable=0")
	}
}

func TestBuildVendorSteps_DeviceNodeSoftPass(t *testing.T) {
	// 드라이버 로드됐으나 알려진 dev glob 미매치(NPU 경로 미확정) → soft-pass.
	p := vendorProbe{driverLoaded: true, deviceNode: false, devGlobKnown: true, allocatable: 1}
	steps := buildVendorSteps("rngd", p)
	dn, _ := stepByName(steps, "deviceNode")
	if !dn.Passed {
		t.Errorf("deviceNode should soft-pass when driver loaded but glob unmatched")
	}
}

func TestBuildVendorSteps_RuntimeNotGate(t *testing.T) {
	// runtime 실패는 게이트가 아니므로 나머지 통과 시 overall passed=true 여야 한다.
	p := vendorProbe{driverLoaded: true, deviceNode: true, devGlobKnown: true, allocatable: 1, runtimeOK: false}
	steps := buildVendorSteps("nvidia", p)
	rt, _ := stepByName(steps, "runtime")
	if rt.Passed {
		t.Errorf("runtime should report fail when toolkit missing")
	}
	res := aggregate([]string{"nvidia"}, map[string][]ValidationStep{"nvidia": steps})
	if !res.Passed {
		t.Errorf("overall should pass because runtime is not a gate step; got fail")
	}
}

func TestAggregate_GateFail(t *testing.T) {
	p := vendorProbe{driverLoaded: false, devGlobKnown: true, allocatable: 0}
	steps := buildVendorSteps("nvidia", p)
	res := aggregate([]string{"nvidia"}, map[string][]ValidationStep{"nvidia": steps})
	if res.Passed {
		t.Errorf("overall should fail when gate steps fail")
	}
	if res.Vendor != "nvidia" {
		t.Errorf("vendor = %q, want nvidia", res.Vendor)
	}
}

func TestAggregate_MultiVendorPrefix(t *testing.T) {
	nv := buildVendorSteps("nvidia", vendorProbe{driverLoaded: true, deviceNode: true, devGlobKnown: true, allocatable: 1, runtimeOK: true})
	tt := buildVendorSteps("tenstorrent", vendorProbe{driverLoaded: true, deviceNode: true, devGlobKnown: true, allocatable: 1, runtimeNA: true})
	res := aggregate([]string{"nvidia", "tenstorrent"}, map[string][]ValidationStep{"nvidia": nv, "tenstorrent": tt})
	if _, ok := stepByName(res.Steps, "nvidia/driverModule"); !ok {
		t.Errorf("multi-vendor steps should be prefixed with vendor; got %+v", res.Steps)
	}
	if _, ok := stepByName(res.Steps, "tenstorrent/devicePlugin"); !ok {
		t.Errorf("expected tenstorrent/devicePlugin step")
	}
	if res.Vendor != "nvidia,tenstorrent" {
		t.Errorf("vendor = %q, want nvidia,tenstorrent", res.Vendor)
	}
}

func TestValidate_NoDevices(t *testing.T) {
	if got := Validate(&Snapshot{Node: "n1"}, nil, false, ""); got != nil {
		t.Errorf("expected nil validation for node without devices, got %+v", got)
	}
}

func TestValidate_HealthyIntegration(t *testing.T) {
	// runtime/deviceNode/CLI 는 FS 조회지만, gate 는 driverModule/devicePlugin/sampleWorkload.
	// 테스트 호스트엔 가속기가 없어 dev glob 미매치 → deviceNode soft-pass.
	snap := &Snapshot{
		Node:    "worker1",
		Devices: []Detected{{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06"}},
	}
	alloc := map[string]int64{"nvidia.com/gpu": 2, "cpu": 8}
	res := Validate(snap, alloc, false, "")
	if res == nil {
		t.Fatal("expected non-nil validation")
	}
	if !res.Passed {
		t.Errorf("expected passed=true, got false: %+v", res.Steps)
	}
	dp, _ := stepByName(res.Steps, "devicePlugin")
	if !dp.Passed || dp.Message != "allocatable=2" {
		t.Errorf("devicePlugin step wrong: %+v", dp)
	}
}

func TestAllocatableForVendor(t *testing.T) {
	cases := []struct {
		key   string
		alloc map[string]int64
		want  int64
	}{
		{"nvidia", map[string]int64{"nvidia.com/gpu": 4}, 4},
		{"furiosa", map[string]int64{"beta.furiosa.ai/npu": 1}, 1},
		{"rngd", map[string]int64{"furiosa.ai/rngd": 2}, 2},
		{"tenstorrent", map[string]int64{"tenstorrent.com/blackhole": 3}, 3},
		{"rebellions", map[string]int64{"rebellions.ai/ATOM": 1}, 1},
		// substring fallback: CR 재정의로 정확 매칭 실패 시.
		{"rebellions", map[string]int64{"rebellions.ai/custom": 5}, 5},
		{"nvidia", map[string]int64{"cpu": 8}, 0},
	}
	for _, c := range cases {
		if got := allocatableForVendor(c.key, c.alloc); got != c.want {
			t.Errorf("allocatableForVendor(%q, %v) = %d, want %d", c.key, c.alloc, got, c.want)
		}
	}
}

func TestVendorKey(t *testing.T) {
	if got := vendorKey("furiosa", "rngd"); got != "rngd" {
		t.Errorf("vendorKey furiosa/rngd = %q, want rngd", got)
	}
	if got := vendorKey("furiosa", "warboy"); got != "furiosa" {
		t.Errorf("vendorKey furiosa/warboy = %q, want furiosa", got)
	}
	if got := vendorKey("nvidia", "generic"); got != "nvidia" {
		t.Errorf("vendorKey nvidia = %q, want nvidia", got)
	}
}

// TestBuildVendorSteps_NotExcludedStillFailsOnZeroAllocatable 은 회귀 방어다.
// 배제되지 않은 노드에서 allocatable=0 이면 devicePlugin·sampleWorkload 는 여전히
// 실패(NotApplicable 아님)여야 한다 — 배제 완화가 진짜 device-plugin 고장까지
// 덮으면 그 자체가 새 결함이다.
func TestBuildVendorSteps_NotExcludedStillFailsOnZeroAllocatable(t *testing.T) {
	p := vendorProbe{driverLoaded: true, devGlobKnown: true, allocatable: 0, excluded: false}
	steps := buildVendorSteps("nvidia", p)

	dp, _ := stepByName(steps, "devicePlugin")
	if dp.Passed || dp.NotApplicable {
		t.Errorf("devicePlugin should still fail (not NotApplicable) when not excluded: %+v", dp)
	}
	sw, _ := stepByName(steps, "sampleWorkload")
	if sw.Passed || sw.NotApplicable {
		t.Errorf("sampleWorkload should still fail (not NotApplicable) when not excluded: %+v", sw)
	}

	res := aggregate([]string{"nvidia"}, map[string][]ValidationStep{"nvidia": steps})
	if res.Passed {
		t.Errorf("overall should still fail for a non-excluded zero-allocatable node")
	}
}

// TestBuildVendorSteps_ExcludedNodeIsNotApplicable 는 배제 노드에서 devicePlugin·
// sampleWorkload 가 실패가 아니라 해당없음으로 뜨고, overall Passed 가 그 때문에
// 꺼지지 않는지 본다. driverModule 은 배제와 무관하게 그대로 평가된다.
func TestBuildVendorSteps_ExcludedNodeIsNotApplicable(t *testing.T) {
	p := vendorProbe{driverLoaded: true, devGlobKnown: true, allocatable: 0, excluded: true}
	steps := buildVendorSteps("nvidia", p)

	dp, _ := stepByName(steps, "devicePlugin")
	if !dp.NotApplicable || !dp.Passed {
		t.Errorf("devicePlugin should be NotApplicable+Passed on excluded node: %+v", dp)
	}
	if dp.Message != excludedMessage(p.excludedReason) {
		t.Errorf("devicePlugin message = %q, want %q", dp.Message, excludedMessage(p.excludedReason))
	}
	sw, _ := stepByName(steps, "sampleWorkload")
	if !sw.NotApplicable || !sw.Passed {
		t.Errorf("sampleWorkload should be NotApplicable+Passed on excluded node: %+v", sw)
	}
	dm, _ := stepByName(steps, "driverModule")
	if !dm.Passed {
		t.Errorf("driverModule should still evaluate normally on an excluded node: %+v", dm)
	}

	res := aggregate([]string{"nvidia"}, map[string][]ValidationStep{"nvidia": steps})
	if !res.Passed {
		t.Errorf("overall should pass when only NotApplicable steps are unmet: %+v", res.Steps)
	}
}

// TestBuildVendorSteps_ExcludedReasonComesFromLabel 은 배제 사유가 문구에 그대로
// 실리는지 본다. 문구에 "control-plane" 이 하드코딩돼 있으면 excludeNodeSelector 로
// 배제된 워커(사유 policy)의 보고서가 거짓이 된다.
func TestBuildVendorSteps_ExcludedReasonComesFromLabel(t *testing.T) {
	for _, tc := range []struct {
		reason  string
		want    string
		notWant string
	}{
		{"policy", "policy", "control-plane"},
		{"control-plane", "control-plane", ""},
		{"", "", "("}, // 사유를 모르면 괄호 없이 배제 사실만
	} {
		t.Run("reason="+tc.reason, func(t *testing.T) {
			steps := buildVendorSteps("nvidia", vendorProbe{
				driverLoaded: true, devGlobKnown: true, allocatable: 0,
				excluded: true, excludedReason: tc.reason,
			})
			for _, name := range []string{"devicePlugin", "sampleWorkload"} {
				s, _ := stepByName(steps, name)
				if tc.want != "" && !strings.Contains(s.Message, tc.want) {
					t.Errorf("%s 문구에 사유 %q 가 없다: %q", name, tc.want, s.Message)
				}
				if tc.notWant != "" && strings.Contains(s.Message, tc.notWant) {
					t.Errorf("%s 문구에 %q 가 있다(사유를 추측했다): %q", name, tc.notWant, s.Message)
				}
			}
		})
	}
}

// TestValidate_ExcludedReasonThreadsThroughFromEntryPoint 는 Validate() 진입점부터
// 사유가 벤더 step 까지 실제로 전달되는지 본다 — 단위 함수만 보면 연결 누락을 놓친다.
func TestValidate_ExcludedReasonThreadsThroughFromEntryPoint(t *testing.T) {
	snap := &Snapshot{
		Node:    "k8s-worker9",
		Devices: []Detected{{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "595.84"}},
	}
	res := Validate(snap, nil, true, "policy")
	dp, _ := stepByName(res.Steps, "devicePlugin")
	if !strings.Contains(dp.Message, "policy") {
		t.Errorf("Validate 가 사유를 벤더 step 까지 전달하지 않았다: %q", dp.Message)
	}
}

// TestValidate_ExcludedThreadsThroughToVendorSteps 는 Validate() 진입점부터 excluded
// 가 벤더 step 까지 실제로 전달되는지 본다(단위 함수만 보면 연결 누락을 놓친다).
func TestValidate_ExcludedThreadsThroughToVendorSteps(t *testing.T) {
	snap := &Snapshot{
		Node:    "k8s-master",
		Devices: []Detected{{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "580.159.03"}},
	}
	res := Validate(snap, nil, true, "control-plane")
	if res == nil {
		t.Fatal("expected non-nil validation")
	}
	dp, _ := stepByName(res.Steps, "devicePlugin")
	if !dp.NotApplicable {
		t.Errorf("Validate 가 excluded 를 벤더 step 까지 전달하지 않았다: %+v", dp)
	}
	if !res.Passed {
		t.Errorf("배제 노드는 devicePlugin/sampleWorkload 해당없음만으로 overall pass 여야 한다: %+v", res.Steps)
	}
}

// TestAllocatableForVendorSumsMigProfiles 는 MIG 로 갈린 광고를 합산하는지 고정한다.
// MIG 를 적용한 노드는 nvidia.com/gpu=0 과 nvidia.com/mig-<profile>=N 을 함께 광고한다.
// 정확 매칭 하나로 끝내면 0 을 읽어 device-plugin 이 죽은 것으로 판정하고, 그 거짓 실패가
// 노드 보고서의 validation.passed 를 내려 파티션 정책 검증을 영구 차단한다.
func TestAllocatableForVendorSumsMigProfiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		alloc map[string]int64
		want  int64
	}{
		{
			name: "mig 적용 노드 — flat 은 0 이고 프로파일에 수량이 있다",
			key:  "nvidia",
			alloc: map[string]int64{
				"nvidia.com/gpu":        0,
				"nvidia.com/mig-1g.6gb": 8,
			},
			want: 8,
		},
		{
			name: "혼재 노드 — flat 과 프로파일이 함께 있다",
			key:  "nvidia",
			alloc: map[string]int64{
				"nvidia.com/gpu":         1,
				"nvidia.com/mig-2g.12gb": 2,
			},
			want: 3,
		},
		{
			name:  "mig 미적용 노드 회귀 없음",
			key:   "nvidia",
			alloc: map[string]int64{"nvidia.com/gpu": 2},
			want:  2,
		},
		{
			name:  "다른 벤더는 nvidia 광고를 세지 않는다",
			key:   "tenstorrent",
			alloc: map[string]int64{"nvidia.com/gpu": 0, "nvidia.com/mig-1g.6gb": 8, "tenstorrent.com/blackhole": 1},
			want:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := allocatableForVendor(tc.key, tc.alloc); got != tc.want {
				t.Errorf("allocatableForVendor(%q) = %d, want %d", tc.key, got, tc.want)
			}
		})
	}
}

// TestUserspaceNvidiaVersion 은 libnvidia-ml.so.1 링크 대상에서 버전을 읽는지 본다.
// 실측 두 벌을 모두 담는다 — 프로프라이어터리 3-component(제어 노드: libnvidia-ml.so.580.173.02)
// 와 open kernel module 2-component(검증 노드: libnvidia-ml.so.595.84). 후자는 자릿수를 박은
// 패턴으로는 못 잡아 대조 기능이 무동작이 되는 입력이다.
func TestUserspaceNvidiaVersion(t *testing.T) {
	// link 는 libnvidia-ml.so.1 이 가리킬 대상 파일명이다(빈 문자열이면 링크를 안 만든다).
	// abs 면 절대경로로, 아니면 상대경로로 링크한다.
	for _, tc := range []struct {
		name string
		real string
		link string
		abs  bool
		want string
	}{
		{"open module 2-component 상대경로", "libnvidia-ml.so.595.84", "libnvidia-ml.so.595.84", false, "595.84"},
		{"프로프라이어터리 3-component 상대경로", "libnvidia-ml.so.580.173.02", "libnvidia-ml.so.580.173.02", false, "580.173.02"},
		{"절대경로 링크", "libnvidia-ml.so.595.84", "libnvidia-ml.so.595.84", true, "595.84"},
		{"링크 없음(실파일만 있어도 보류)", "libnvidia-ml.so.595.84", "", false, ""},
		{"soname 이 링크가 아니라 일반 파일", userspaceLibSoname, "", false, ""},
		{"링크 대상이 버전 모양 아님", "libnvidia-ml.so.deb-orig", "libnvidia-ml.so.deb-orig", false, ""},
		{"링크가 무관한 파일을 가리킴", "libcuda.so.595.84", "libcuda.so.595.84", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.real), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.link != "" {
				target := tc.link
				if tc.abs {
					target = filepath.Join(dir, tc.link)
				}
				if err := os.Symlink(target, filepath.Join(dir, userspaceLibSoname)); err != nil {
					t.Fatal(err)
				}
			}
			if got := userspaceNvidiaVersion(dir); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	if got := userspaceNvidiaVersion(t.TempDir()); got != "" {
		t.Errorf("empty dir: got %q, want \"\"", got)
	}
	if got := userspaceNvidiaVersion(filepath.Join(t.TempDir(), "no-such-dir")); got != "" {
		t.Errorf("missing dir: got %q, want \"\"", got)
	}
}

// TestUserspaceNvidiaVersion_CoexistingPackagesFollowTheLink 는 cross-major 전이 중
// 구 패키지가 남아 여러 버전이 공존할 때, 디렉터리 순서상 앞선 구버전이 아니라 로더가
// 실제로 여는 링크 대상을 고르는지 본다. 파일명 훑기는 사전순 최소(580.159.03)를 집어
// 커널이 595.84 인 정상 노드에 거짓 불일치를 냈다.
func TestUserspaceNvidiaVersion_CoexistingPackagesFollowTheLink(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"libnvidia-ml.so.580.159.03",
		"libnvidia-ml.so.580.173.02",
		"libnvidia-ml.so.595.84",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("libnvidia-ml.so.595.84", filepath.Join(dir, userspaceLibSoname)); err != nil {
		t.Fatal(err)
	}
	if got := userspaceNvidiaVersion(dir); got != "595.84" {
		t.Errorf("got %q, want 595.84 (링크 대상이 아니라 다른 후보를 집었다)", got)
	}
}

// TestBuildVendorSteps_DriverVersionMismatch 는 실측 값(k8s-master)으로
// 커널·사용자공간 버전이 다르면 driverModule 이 실패하고 사유에 둘 다 담기는지 본다.
func TestBuildVendorSteps_DriverVersionMismatch(t *testing.T) {
	p := vendorProbe{driverLoaded: true, devGlobKnown: true, driverVer: "580.159.03", userspaceVer: "580.173.02"}
	steps := buildVendorSteps("nvidia", p)
	dm, _ := stepByName(steps, "driverModule")
	if dm.Passed {
		t.Errorf("driverModule should fail on kernel/userspace mismatch: %+v", dm)
	}
	if !strings.Contains(dm.Message, "580.159.03") || !strings.Contains(dm.Message, "580.173.02") {
		t.Errorf("driverModule message should carry both versions: %q", dm.Message)
	}
}

// TestBuildVendorSteps_DriverVersionMatchPasses 는 회귀 방어다 — 버전이 같으면
// 여전히 통과해야 한다.
func TestBuildVendorSteps_DriverVersionMatchPasses(t *testing.T) {
	p := vendorProbe{driverLoaded: true, devGlobKnown: true, driverVer: "580.65.06", userspaceVer: "580.65.06"}
	steps := buildVendorSteps("nvidia", p)
	dm, _ := stepByName(steps, "driverModule")
	if !dm.Passed {
		t.Errorf("driverModule should pass when versions match: %+v", dm)
	}
}

// TestBuildVendorSteps_DriverVersionUnknownSideNotFailed 는 "모르는 것을 실패로 적지
// 않는다"를 고정한다 — 한쪽 버전을 못 읽으면 대조하지 않고 기존 driverLoaded 판정을 유지.
func TestBuildVendorSteps_DriverVersionUnknownSideNotFailed(t *testing.T) {
	for _, p := range []vendorProbe{
		{driverLoaded: true, devGlobKnown: true, driverVer: "580.159.03", userspaceVer: ""},
		{driverLoaded: true, devGlobKnown: true, driverVer: "", userspaceVer: "580.173.02"},
		{driverLoaded: true, devGlobKnown: true},
	} {
		dm, _ := stepByName(buildVendorSteps("nvidia", p), "driverModule")
		if !dm.Passed {
			t.Errorf("driverModule should not fail when a version side is unknown: probe=%+v step=%+v", p, dm)
		}
	}
}

// TestBuildVendorSteps_WithheldComparisonIsDistinguishable 는 "대조해서 같았다" 와
// "대조를 못 했다" 가 다른 문구로 나오는지 고정한다. 두 상태가 같은 글자로 보이면
// 대조 기능이 무동작이 돼도 운영자가 알아볼 수 없다.
func TestBuildVendorSteps_WithheldComparisonIsDistinguishable(t *testing.T) {
	matched, _ := stepByName(buildVendorSteps("nvidia", vendorProbe{
		driverLoaded: true, devGlobKnown: true, driverVer: "595.84", userspaceVer: "595.84",
	}), "driverModule")
	withheld, _ := stepByName(buildVendorSteps("nvidia", vendorProbe{
		driverLoaded: true, devGlobKnown: true, driverVer: "595.84", userspaceVer: "",
	}), "driverModule")

	if !matched.Passed || !withheld.Passed {
		t.Fatalf("둘 다 통과여야 한다: matched=%+v withheld=%+v", matched, withheld)
	}
	if matched.Message == withheld.Message {
		t.Fatalf("대조 성공과 대조 보류가 같은 문구다: %q", matched.Message)
	}
	if !strings.Contains(matched.Message, "대조 일치") {
		t.Errorf("대조 성공 문구에 일치 표시가 없다: %q", matched.Message)
	}
	if !strings.Contains(withheld.Message, "대조 보류") {
		t.Errorf("대조 보류 문구에 보류 표시가 없다: %q", withheld.Message)
	}

	// 비-NVIDIA 는 사용자공간 버전을 애초에 채우지 않으므로 보류 문구가 붙으면 거짓이다.
	rngd, _ := stepByName(buildVendorSteps("rngd", vendorProbe{
		driverLoaded: true, devGlobKnown: true, runtimeNA: true, driverVer: "2026.3.0",
	}), "driverModule")
	if strings.Contains(rngd.Message, "대조 보류") {
		t.Errorf("비-NVIDIA 벤더에 대조 보류 문구가 붙었다: %q", rngd.Message)
	}
}

// TestBuildVendorSteps_ExcludedNodeStillEvaluatesDriverModule 은 핵심 전제를 고정한다 —
// 배제는 devicePlugin/sampleWorkload 만 가리고 driverModule 은 그대로 평가된다.
// k8s-master 는 실측으로 배제 노드이면서 동시에 버전 불일치다.
func TestBuildVendorSteps_ExcludedNodeStillEvaluatesDriverModule(t *testing.T) {
	p := vendorProbe{
		driverLoaded: true, devGlobKnown: true, allocatable: 0, excluded: true,
		driverVer: "580.159.03", userspaceVer: "580.173.02",
	}
	steps := buildVendorSteps("nvidia", p)

	dm, _ := stepByName(steps, "driverModule")
	if dm.Passed || dm.NotApplicable {
		t.Errorf("driverModule 은 배제와 무관하게 실패로 드러나야 한다: %+v", dm)
	}
	dp, _ := stepByName(steps, "devicePlugin")
	if !dp.NotApplicable || !dp.Passed {
		t.Errorf("devicePlugin 은 배제 노드에서 해당없음이어야 한다: %+v", dp)
	}
	sw, _ := stepByName(steps, "sampleWorkload")
	if !sw.NotApplicable || !sw.Passed {
		t.Errorf("sampleWorkload 는 배제 노드에서 해당없음이어야 한다: %+v", sw)
	}

	// overall Passed 는 driverModule(NotApplicable 아닌 게이트 실패) 때문에 false 여야 한다.
	// 배제가 이 실패를 덮으면(=여기서 true 가 나오면) 드라이버 고장이 화면에서 사라진다.
	res := aggregate([]string{"nvidia"}, map[string][]ValidationStep{"nvidia": steps})
	if res.Passed {
		t.Errorf("배제가 driverModule 실패를 덮었다 — overall Passed 는 false 여야 한다: %+v", res.Steps)
	}
}
