// ============================================================
// scan_test.go: 스캔 헬퍼 단위 테스트
// 상세: migLgip() 의 nvidia-smi 부재 경로(CI) 검증 + parseMigObservation 의 fail-closed
// 판정 단위 테스트 + Rebellions 드라이버 버전 탐지(커널 모듈 우선·rebellions-dkms 패키지).
// 픽스처는 worker1 A30(nvidia-smi 580 계열)·worker4 ATOM+(rebellions-dkms 3.0.0) 실측 형식을 쓴다.
// 생성일: 2026-07-23 | 수정일: 2026-11-16
// ============================================================
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakePciDev 는 가짜 sysfs PCI 노드 하나를 만든다(vendor/device/class 3파일).
func fakePciDev(t *testing.T, root, addr, vendor, device, class string) {
	t.Helper()
	dir := filepath.Join(root, addr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, val := range map[string]string{"vendor": vendor, "device": device, "class": class} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeSysfsPci 는 hostSysPciDevices 를 임시 디렉터리로 돌린다. H() 는 "/sys/" 접두를 그대로
// 두므로(컨테이너 네임스페이스 사용) hostPrefix 만으로는 PCI 트리를 가짜로 못 만든다.
func fakeSysfsPci(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	oldPrefix, oldPci := hostPrefix, hostSysPciDevices
	hostPrefix, hostSysPciDevices = tmp, "/pci"
	t.Cleanup(func() { hostPrefix, hostSysPciDevices = oldPrefix, oldPci })
	root := filepath.Join(tmp, "pci")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// 다중 카드 비-NVIDIA 노드에서 벤더별 주소가 섞이지 않고 정렬되어 나오는지 본다. 픽스처
// 값은 sysfs 실측이다(worker1 Warboy 1ed2:0000, rngd-1 RNGD 1ed2:0001, worker3 Blackhole
// 1e52:b140, 전부 class 0x120000). Warboy 와 RNGD 는 벤더 ID 가 같아 device ID 로만 갈린다.
// Warboy 2번째 카드는 실물이 없어 같은 시그니처로 합성한다.
func TestVendorPciAddrs_MultiCardNonNvidia(t *testing.T) {
	root := fakeSysfsPci(t)
	fakePciDev(t, root, "0000:af:00.0", "0x1ed2", "0x0000", "0x120000") // Warboy
	fakePciDev(t, root, "0000:5e:00.0", "0x1ed2", "0x0000", "0x120000") // Warboy 2번째(합성)
	fakePciDev(t, root, "0000:27:00.0", "0x1ed2", "0x0001", "0x120000") // RNGD
	fakePciDev(t, root, "0000:d8:00.0", "0x1e52", "0xb140", "0x120000") // Blackhole
	fakePciDev(t, root, "0000:00:1f.0", "0x8086", "0x0a03", "0x060100") // 칩셋(무관)

	got := vendorPciAddrs()
	want := map[string][]string{
		"furiosa":     {"0000:5e:00.0", "0000:af:00.0"}, // 정렬 순
		"rngd":        {"0000:27:00.0"},
		"tenstorrent": {"0000:d8:00.0"},
	}
	if len(got) != len(want) {
		t.Fatalf("벤더 키 집합이 다르다: got %v, want %v", got, want)
	}
	for k, w := range want {
		g := got[k]
		if len(g) != len(w) {
			t.Fatalf("%s: got %v, want %v", k, g, w)
		}
		for i := range w {
			if g[i] != w[i] {
				t.Fatalf("%s: got %v, want %v", k, g, w)
			}
		}
	}
}

// NVIDIA 열거는 물리 함수(.0) + class 0x03 만 잡는다(회귀 0). 같은 카드의 오디오 함수(.1)와
// 다른 클래스는 제외되어야 한다 — 여기가 깨지면 GPU 수가 부풀어 fan-out 이 유령 entry 를 만든다.
func TestVendorPciAddrs_NvidiaUnchanged(t *testing.T) {
	root := fakeSysfsPci(t)
	fakePciDev(t, root, "0000:18:00.0", "0x10de", "0x20b7", "0x030200") // A30 (worker1 실측)
	fakePciDev(t, root, "0000:86:00.0", "0x10de", "0x25b6", "0x030200") // A2  (worker1 실측)
	fakePciDev(t, root, "0000:18:00.1", "0x10de", "0x1aef", "0x040300") // HDA 오디오 함수
	fakePciDev(t, root, "0000:af:00.0", "0x1ed2", "0x0000", "0x120000") // Warboy(혼재 노드)

	got := vendorPciAddrs()["nvidia"]
	want := []string{"0000:18:00.0", "0000:86:00.0"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("nvidia addrs = %v, want %v", got, want)
	}
}

func TestMigLgip_AbsentReturnsEmpty(t *testing.T) {
	// nvidia-smi 미존재 환경(CI)에서는 "" 반환해야 함(회귀 0).
	if out := migLgip(); out != "" {
		t.Errorf("expected empty on absent nvidia-smi, got %q", out)
	}
}

// current=Enabled 인데 -lgi 가 파싱 불가(garbage) → Unknown+Err(조용히 disabled 로 보고 금지).
func TestParseMigObservation_FailClosed(t *testing.T) {
	obs := parseMigObservation("Enabled, Disabled\n", "garbage-not-parseable", "")
	if obs.ModeCurrent != "Unknown" || obs.Err == "" {
		t.Fatalf("Enabled+lgi 파싱실패 → Unknown+Err, got %+v", obs)
	}
}

// pending 이 인식 불가값("weird")이면 Err 를 세팅해야 함(apply 차단 근거).
func TestParseMigObservation_PendingUnknown(t *testing.T) {
	obs := parseMigObservation("Disabled, weird\n", "No MIG-enabled devices found.\n", "")
	if obs.Err == "" {
		t.Fatal("pending Unknown → ObservationError")
	}
}

// current=Disabled(실측 worker1 A30 mode csv 형식) → Geometry="disabled", Err 없음.
func TestParseMigObservation_Disabled(t *testing.T) {
	obs := parseMigObservation("Disabled, Disabled\n", "No MIG-enabled devices found.\n", "")
	if obs.ModeCurrent != "Disabled" || obs.Geometry != "disabled" || obs.Err != "" {
		t.Fatalf("%+v", obs)
	}
}

// Enabled + 정상 -lgi(실측 -lgip 형식의 "MIG 1g.6gb" 프로파일 행 2개) → geometry 요약 성공.
func TestParseMigObservation_EnabledParsesGeometry(t *testing.T) {
	lgi := "+-----------------------------------------------------------------------+\n" +
		"| GPU instance ID  ...                                                    |\n" +
		"|   0  MIG 1g.6gb          14     4/4        5.81      ...                |\n" +
		"|   1  MIG 1g.6gb          14     4/4        5.81      ...                |\n" +
		"+-----------------------------------------------------------------------+\n"
	obs := parseMigObservation("Enabled, Enabled\n", lgi, "")
	if obs.ModeCurrent != "Enabled" || obs.Err != "" {
		t.Fatalf("%+v", obs)
	}
	if obs.Geometry != "1g.6gb x2" {
		t.Errorf("geometry = %q, want %q", obs.Geometry, "1g.6gb x2")
	}
}

// FIX 2: mode csv 의 "N/A"(MIG 미지원 GPU, 예: A2) → ModeCurrent="NA", trivially
// 분할 불가하므로 Geometry="disabled", Err 없음(unhandled 폴스루 금지).
func TestParseMigObservation_NA(t *testing.T) {
	obs := parseMigObservation("N/A, N/A\n", "", "")
	if obs.ModeCurrent != "NA" || obs.Geometry != "disabled" || obs.Err != "" {
		t.Fatalf("%+v", obs)
	}
}

// FIX 3: -lgip exec 자체가 실패하면(mode/-lgi 는 정상) 조용히 넘기지 않고 obs.Err 에
// 신호를 남겨야 한다(garbage/빈 MigLgipOutput 이 downstream 에 "clean" 으로 오인되면 안 됨).
func TestObserveMig_LgipErrorSignaled(t *testing.T) {
	tmp := t.TempDir()
	old := hostPrefix
	hostPrefix = tmp
	defer func() { hostPrefix = old }()

	binDir := filepath.Join(tmp, "usr", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// mode/-lgi 는 정상 응답, -lgip 만 실패(exit 1) 하는 가짜 nvidia-smi.
	fake := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"-lgip\" ]; then\n" +
		"    echo 'lgip boom' >&2\n" +
		"    exit 1\n" +
		"  fi\n" +
		"done\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"-lgi\" ]; then\n" +
		"    echo 'No MIG-enabled devices found.'\n" +
		"    exit 0\n" +
		"  fi\n" +
		"done\n" +
		"echo 'Disabled, Disabled'\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "nvidia-smi"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}

	obs := observeMig("0000:01:00.0")
	if obs.ModeCurrent != "Disabled" || obs.Geometry != "disabled" {
		t.Errorf("mode/geometry 는 관측 그대로 유지되어야 함, got %+v", obs)
	}
	if obs.Err == "" {
		t.Fatal("lgip exec 실패인데 Err 미설정(조용히 삼킴, 금지)")
	}
}

// observeMig: nvidia-smi 미존재 환경(CI) 또는 pci 공란이면 조회 자체를 시도하지 않고
// 즉시 Unknown+Err(fail-closed, 타겟 불명 GPU 를 안전하게 조작하지 않음).
func TestObserveMig_AbsentOrEmptyPci(t *testing.T) {
	obs := observeMig("")
	if obs.ModeCurrent != "Unknown" || obs.ModePending != "Unknown" || obs.Err == "" {
		t.Fatalf("empty pci → Unknown+Err, got %+v", obs)
	}
	obs = observeMig("0000:01:00.0")
	if obs.ModeCurrent != "Unknown" || obs.Err == "" {
		t.Fatalf("nvidia-smi 부재(CI) → Unknown+Err, got %+v", obs)
	}
}

// TestHostCommandUsesHostLoader 는 host 링커가 있으면 그것으로 host 바이너리를 실행하는지 본다.
// distroless 에는 동적 링커가 없어 직접 exec 하면 ENOENT 가 나고, nvidia-smi 가 host 에
// 있어도 MIG 관측이 통째로 실패한다.
func TestHostCommandUsesHostLoader(t *testing.T) {
	dir := t.TempDir()
	old := hostPrefix
	hostPrefix = dir
	defer func() { hostPrefix = old }()

	ld := filepath.Join(dir, "usr/lib/x86_64-linux-gnu")
	if err := os.MkdirAll(ld, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := filepath.Join(ld, "ld-linux-x86-64.so.2")
	if err := os.WriteFile(loader, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := hostCommand(context.Background(), nvidiaSmiHostBin, "-L")
	if cmd.Path != loader {
		t.Fatalf("cmd.Path = %q, want host loader %q", cmd.Path, loader)
	}
	want := []string{loader, "--library-path", hostLibraryPath, H(nvidiaSmiHostBin), "-L"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %v, want %v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", cmd.Args, want)
		}
	}
}

// TestHostCommandFallsBackWithoutLoader 는 링커가 없으면 기존처럼 직접 실행하는지 고정한다.
func TestHostCommandFallsBackWithoutLoader(t *testing.T) {
	old := hostPrefix
	hostPrefix = t.TempDir()
	defer func() { hostPrefix = old }()
	cmd := hostCommand(context.Background(), nvidiaSmiHostBin, "-L")
	if cmd.Path != H(nvidiaSmiHostBin) {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, H(nvidiaSmiHostBin))
	}
}

// TestParseNvrmVersion 은 /proc/driver/nvidia/version 본문에서 드라이버 버전을 뽑는 규칙을 고정한다.
// 여기서 나온 값이 업그레이드 상태기계의 "현재 버전" 이 되므로, 틀리면 멀쩡한 노드가
// 교체 대상으로 판정돼 cordon 된다. 실제로 두 차례 그렇게 뚫렸다:
//   - 580.142(2-component) 를 못 잡아 GCC 줄로 흘렀고
//   - open kernel module 의 "for x86_64" 토큰을 못 넘어 다시 GCC 줄로 흘렀다(A30 595.84 실측)
func TestParseNvrmVersion(t *testing.T) {
	// GCC 배너는 두 회귀 모두의 오답 출처라 모든 입력에 그대로 붙여 둔다.
	const gcc = "  GCC version:  gcc version 11.4.0 (Ubuntu 11.4.0-1ubuntu1~22.04.3) "

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			// A30 실측. 회귀 재발 지점.
			name: "open kernel module",
			in: "NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  595.84  " +
				"Release Build  (dvs-builder@U22-I3-AM25-26-2)  Wed Jun 10 21:06:37 UTC 2026" + gcc,
			want: "595.84",
		},
		{
			name: "proprietary 3-component",
			in: "NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.159.03  " +
				"Tue Sep 30 12:00:00 UTC 2026" + gcc,
			want: "580.159.03",
		},
		{
			name: "proprietary 2-component",
			in:   "NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.142  Mon Jan 1 00:00:00 UTC 2026" + gcc,
			want: "580.142",
		},
		{
			// 어떤 형태든 못 뽑으면 컴파일러 버전을 집지 말고 빈 값이어야 한다.
			name: "unparseable must not fall through to compiler version",
			in:   "NVRM version: something entirely unexpected" + gcc,
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNvrmVersion(tc.in); got != tc.want {
				t.Errorf("parseNvrmVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNvrmRegexMatchesOpenKernelModule 은 fallback 이 아니라 NVRM 정규식 자체가 open kernel
// module 형식을 잡는지 고정한다. 이것을 따로 두는 이유: GCC 절단 fallback 이 결과적으로 같은
// 답을 내주기 때문에 parseNvrmVersion 결과만 보면 정규식이 망가져도 시험이 통과한다(변이 확인됨).
// 느슨한 fallback 은 "버전처럼 생긴 첫 토큰" 을 집는 규칙이라 정본이 될 수 없다.
func TestNvrmRegexMatchesOpenKernelModule(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"open", "NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  595.84  Release Build", "595.84"},
		{"proprietary", "NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.159.03  Tue Sep 30", "580.159.03"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := reNvrmVer.FindStringSubmatch(tc.in)
			if len(m) != 2 {
				t.Fatalf("reNvrmVer 가 매칭하지 못했다: %q", tc.in)
			}
			if m[1] != tc.want {
				t.Errorf("reNvrmVer = %q, want %q", m[1], tc.want)
			}
		})
	}
}

// TestNvidiaOnDiskVersion: dpkg status 에서 nvidia-driver-* 설치 버전을 파싱한다.
// 모든 실제 우분투 변종(기본/open/server/server-open)을 매칭하고,
// assistant 와 furiosa 같은 비드라이버 패키지는 무시한다.
func TestNvidiaOnDiskVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pkgName string
		pkgVer  string
		wantVer string
	}{
		{"basic", "nvidia-driver-580", "580.76.04-1ubuntu1", "580.76.04"},
		{"open", "nvidia-driver-580-open", "580.178.04-0ubuntu1", "580.178.04"},
		{"server", "nvidia-driver-580-server", "580.09.14-1ubuntu1", "580.09.14"},
		{"server-open", "nvidia-driver-535-server-open", "535.213.04-2ubuntu1", "535.213.04"},
		{"assistant-excluded", "nvidia-driver-assistant", "1.0.0-1ubuntu1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			old := hostPrefix
			hostPrefix = tmp
			t.Cleanup(func() { hostPrefix = old })

			dpkgStatus := fmt.Sprintf(`Package: %s
Version: %s
Status: install ok installed

Package: furiosa-libnux
Version: 1.9.8-2ubuntu1
Status: install ok installed

`, tc.pkgName, tc.pkgVer)

			dir := filepath.Join(tmp, "var/lib/dpkg")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "status"), []byte(dpkgStatus), 0o644); err != nil {
				t.Fatal(err)
			}

			got := nvidiaOnDiskVersion()
			if got != tc.wantVer {
				t.Errorf("nvidiaOnDiskVersion() = %q, want %q", got, tc.wantVer)
			}
		})
	}
}

// TestNvidiaOnDiskVersion_None: dpkg status 가 없으면 빈 문자열을 반환한다.
func TestNvidiaOnDiskVersion_None(t *testing.T) {
	tmp := t.TempDir()
	old := hostPrefix
	hostPrefix = tmp
	t.Cleanup(func() { hostPrefix = old })

	// dpkg status 파일을 만들지 않음
	got := nvidiaOnDiskVersion()
	if got != "" {
		t.Errorf("nvidiaOnDiskVersion() = %q, want empty string", got)
	}
}

/* ---------- Rebellions 드라이버 버전 ---------- */

// fakeSysModule 은 hostSysModule 을 임시 디렉터리로 돌린다. H() 는 "/sys/" 접두를 그대로 두므로
// fakeSysfsPci 와 같은 방식으로 접두를 바꿔야 가짜 모듈 트리를 만들 수 있다.
func fakeSysModule(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	oldPrefix, oldMod := hostPrefix, hostSysModule
	hostPrefix, hostSysModule = tmp, "/module"
	t.Cleanup(func() { hostPrefix, hostSysModule = oldPrefix, oldMod })
}

// writeHostFile 은 호스트 경로 p(H() 적용 전)에 body 를 쓴다.
func writeHostFile(t *testing.T, p, body string) {
	t.Helper()
	full := H(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	dpkgRblnDkms = "Package: rebellions-dkms\nVersion: 3.0.0\nStatus: install ok installed\n\n"
	// 같은 접두의 펌웨어·도구 패키지. 드라이버 버전으로 집으면 틀리도록 다른 버전을 준다.
	dpkgRblnOthers = "Package: rebellions-fw\nVersion: 9.9.9\nStatus: install ok installed\n\n" +
		"Package: rebellions-tool\nVersion: 9.9.9\nStatus: install ok installed\n\n" +
		"Package: rbln-sdk\nVersion: 0.10.3\nStatus: install ok installed\n\n"
)

// ATOM+ 2장 노드 실측: /sys/module/rebellions/version 과 rebellions-dkms 패키지가 모두 3.0.0 인데
// 패키지 이름 "rebellions" 만 찾으면 빈 버전이 나온다. 모듈 버전이 패키지 버전보다 우선한다.
func TestRblnShortVersionPrefersKernelModule(t *testing.T) {
	fakeSysModule(t)
	writeHostFile(t, filepath.Join(hostSysModule, "rebellions", "version"), "3.0.0\n")
	writeHostFile(t, hostVarDpkgStatus, "Package: rebellions-dkms\nVersion: 2.9.0\nStatus: install ok installed\n\n")
	short, detail := rblnShortVersion()
	if short != "3.0.0" || detail != "driver=3.0.0" {
		t.Fatalf("short=%q detail=%q, want 3.0.0 / driver=3.0.0 (모듈 버전 우선)", short, detail)
	}
	if !rblnDriverLoaded() {
		t.Errorf("모듈 디렉터리가 있는데 loaded=false")
	}
}

// 모듈이 올라와 있지 않으면(미적재·재부팅 대기) 설치된 rebellions-dkms 패키지 버전으로 물러난다.
func TestRblnShortVersionFallsBackToDkmsPackage(t *testing.T) {
	fakeSysModule(t)
	writeHostFile(t, hostVarDpkgStatus, dpkgRblnOthers+dpkgRblnDkms)
	short, detail := rblnShortVersion()
	if short != "3.0.0" || detail != "driver=3.0.0" {
		t.Fatalf("short=%q detail=%q, want 3.0.0 / driver=3.0.0 (rebellions-dkms)", short, detail)
	}
	if rblnDriverLoaded() {
		t.Errorf("모듈 디렉터리가 없는데 loaded=true")
	}
}

func TestRblnShortVersionIgnoresNonDriverPackages(t *testing.T) {
	fakeSysModule(t)
	writeHostFile(t, hostVarDpkgStatus, dpkgRblnOthers)
	if short, detail := rblnShortVersion(); short != "" || detail != "" {
		t.Fatalf("short=%q detail=%q, want 빈 값(펌웨어·도구 패키지는 드라이버가 아니다)", short, detail)
	}
}

func TestRblnShortVersionEmptyWithoutSources(t *testing.T) {
	fakeSysModule(t)
	if short, detail := rblnShortVersion(); short != "" || detail != "" {
		t.Fatalf("short=%q detail=%q, want 빈 값", short, detail)
	}
}
