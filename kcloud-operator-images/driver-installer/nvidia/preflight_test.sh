#!/usr/bin/env bash
# ============================================================
# preflight_test.sh: 설치 전 점검(run_preflight·preflight.sh)이 hold 충돌만 막고 호스트를 바꾸지 않는지 시험
# 상세: entrypoint.sh 함수만 source 하고 ns·apt_wait·nvidia_gpu_bindings 를 가짜로 둔다. 모든 ns 호출을 기록해
#       허용 목록 밖의 호스트 명령이 한 번도 없는지 마지막에 단정한다(사례 9). 사례별 범위는 각 사례 머리 주석에 있다:
#       B 헤더·빌드 도구 모의 설치, H VERSION_SOURCE=Host 채택, L 점검 로그, D 기존 드라이버 감지, 10b 실제 프로세스 실행.
# 생성일: 2026-10-27 | 수정일: 2026-11-16
# ============================================================
# shellcheck disable=SC2034
# 파일 전체에 적용 — 전역 대입(DRIVER_VERSION·PREBUILT_ROOT 등)은 source 한 함수가 읽는다.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
nscalls="$tmp/nscalls"; allcalls="$tmp/allcalls"; : > "$allcalls"

ENTRYPOINT_LIB_ONLY=1
# shellcheck source=/dev/null
source "$here/entrypoint.sh"
set +eE

FIX_A=$'The following packages have unmet dependencies:\n libx11-xcb1 : Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1ubuntu0.3 is to be installed\nE: Unable to correct problems, you have held broken packages.'
MADISON_178='nvidia-driver-580 | 580.178.04-0ubuntu0.22.04.1 | http://archive.ubuntu.com/ubuntu jammy-updates/restricted amd64 Packages'
MADISON_OTHER='nvidia-driver-580 | 580.126.09-0ubuntu0.22.04.1 | https://ppa.launchpadcontent.net/graphics-drivers/ppa/ubuntu jammy/main amd64 Packages'
KERNEL=5.15.0-179-generic
# 호스트 /proc/driver/nvidia/version 원문 형식(로드된 모듈 버전은 580.126.09)
NVRM_580126=$'NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.126.09  Mon Jan 12 22:41:08 UTC 2026\nGCC version:  gcc version 11.4.0'

ns_default() {
  echo "$*" >> "$nscalls"; echo "$*" >> "$allcalls"
  case "$*" in
    "uname -r")                    echo "${KERNEL}" ;;
    *"apt-get update"*)            return "${UPDATE_RC}" ;;
    *"apt-get -s -y install"*"build-essential"*)
      # 설치된 도구가 옛 버전으로 hold 돼 있으면 apt 는 --no-upgrade 없이는 업그레이드를 시도하다 거절한다.
      if [[ -n "${BUILD_SIM_UPGRADE_OUT}" && "$*" != *"--no-upgrade"* ]]; then
        printf '%s\n' "${BUILD_SIM_UPGRADE_OUT}"; return 100
      fi
      printf '%s\n' "${BUILD_SIM_OUT}"; return "${BUILD_SIM_RC}" ;;
    *"apt-get -s -y install"*)     printf '%s\n' "${SIM_OUT}"; return "${SIM_RC}" ;;
    *"apt-mark showhold"*)         printf '%s' "${HOLDS}" ;;
    *"apt-cache madison"*)         printf '%s\n' "${MADISON}" ;;
    "[[ -d /usr/src/linux-headers-"*) [[ "${HEADERS_PRESENT}" == "1" ]] ;;
    "[[ -d /lib/modules/"*)        [[ "${MODULES_PRESENT}" == "1" ]] ;;
    "test -f /proc/driver/nvidia/version") [[ -n "${LOADED_NVRM}" ]] ;;
    "cat /proc/driver/nvidia/version"*)    printf '%s\n' "${LOADED_NVRM}" ;;
    # 설치된 패키지 조회: 호스트 dpkg-query 출력(DPKG_DRIVER_LINE)에 명령 문자열 뒤쪽의 grep·awk·head 를 같은 방식으로 적용한다.
    *"dpkg-query -W -f='\${Status} \${Version}"*) printf '%s\n' "${DPKG_DRIVER_LINE}" | grep '^install' | awk '{print $4}' | head -1 || true ;;
    *)                             return 0 ;;
  esac
}
ns() { ns_default "$@"; }
apt_wait() { return "${APT_WAIT_RC}"; }
nvidia_gpu_bindings() { echo "${GPU_BINDINGS}"; }
TERMINATION_LOG="$tmp/term"
reset() {
  : > "$nscalls"; : > "$tmp/term"; mkdir -p "$tmp/prebuilt"; PREBUILT_ROOT="$tmp/prebuilt"
  UPDATE_RC=0; SIM_OUT=""; SIM_RC=0; HOLDS=""; MADISON="$MADISON_178"; GPU_BINDINGS="1 1"
  DRIVER_VERSION=580.178.04; SKIP_ON_PASSTHROUGH=true
  BUILD_SIM_OUT=""; BUILD_SIM_RC=0; BUILD_SIM_UPGRADE_OUT=""; HEADERS_PRESENT=1; MODULES_PRESENT=1
  APT_WAIT_RC=0; VERSION_SOURCE=Policy; LOADED_NVRM=""; DPKG_DRIVER_LINE=""
}
apt_called() { grep -qE 'apt-get|apt-mark|apt-cache' "$nscalls"; }
last_sim() { grep 'apt-get -s' "$nscalls" | tail -n1; }   # 실패 메시지용: 마지막 모의 설치 명령

# (1) hold 충돌 → 3, 종료 메시지에 hold 패키지 이름(맨 앞)과 apt 거절 줄, 목록 갱신과 같은 인자의 모의 설치를 거친다.
reset; SIM_OUT="$FIX_A"; SIM_RC=100; HOLDS=$'libx11-6\nlibx11-data\n'
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 3 ]] || { echo "FAIL case1a: rc=$rc (3 기대)"; exit 1; }
[[ "$(cat "$tmp/term")" == "hold 된 패키지(libx11-6)"* && "$(cat "$tmp/term")" == *"Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4)"* ]] \
  || { echo "FAIL case1b: term=$(cat "$tmp/term")"; exit 1; }
grep -q 'apt-get update' "$nscalls" || { echo "FAIL case1c: 목록 갱신 없음"; exit 1; }
grep -q -- '-s -y install --allow-downgrades nvidia-driver-580=580.178.04-0ubuntu0.22.04.1 ' "$nscalls" \
  || { echo "FAIL case1d: $(cat "$nscalls")"; exit 1; }

# (2) 모의 설치 성공 → 0, 종료 메시지 없음.
reset; SIM_RC=0
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 && ! -s "$tmp/term" ]] || { echo "FAIL case2: rc=$rc term=$(cat "$tmp/term")"; exit 1; }

# (3) hold 와 무관한 apt 실패 → 1(판정 불가).
reset; SIM_OUT="$FIX_A"; SIM_RC=100; HOLDS=""
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 1 ]] && grep -q 'hold 와 무관' "$tmp/term" || { echo "FAIL case3: rc=$rc term=$(cat "$tmp/term")"; exit 1; }

# (4) 정책 버전이 저장소에 없음(PPA 추가 전 판정) → 1, 모의 설치는 부르지 않는다.
reset; MADISON="$MADISON_OTHER"
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 1 ]] && grep -q '저장소' "$tmp/term" || { echo "FAIL case4a: rc=$rc"; exit 1; }
grep -q -- '-s -y install' "$nscalls" && { echo "FAIL case4b: 버전이 없는데 모의 설치"; exit 1; }

# (5) GPU 전부 vfio-pci → 0, apt 호출 없음.
reset; GPU_BINDINGS="2 0"
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 ]] && ! apt_called || { echo "FAIL case5: rc=$rc"; exit 1; }

# (6) 실행 커널용 사전 컴파일 .deb 동봉 → 0, apt 호출 없음.
reset; mkdir -p "$tmp/prebuilt/$KERNEL"
: > "$tmp/prebuilt/$KERNEL/linux-modules-nvidia-580-${KERNEL}_580.178.04-0ubuntu0.22.04.1_amd64.deb"
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 ]] && ! apt_called || { echo "FAIL case6: rc=$rc"; exit 1; }
rm -f "$tmp/prebuilt/$KERNEL/linux-modules-nvidia-580-${KERNEL}_580.178.04-0ubuntu0.22.04.1_amd64.deb"

# (7) DRIVER_VERSION 비어 있음 → 0, apt 호출 없음.
reset; DRIVER_VERSION=""
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 ]] && ! apt_called || { echo "FAIL case7: rc=$rc"; exit 1; }

# (8) apt 목록 갱신 실패 → 1.
reset; UPDATE_RC=100
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 1 ]] && grep -q 'apt 목록 갱신 실패' "$tmp/term" || { echo "FAIL case8: rc=$rc"; exit 1; }

# (B1) 드라이버 모의 설치는 통과하지만 헤더·빌드 도구 모의 설치가 hold 에 막히면 3 과 종료 메시지.
reset; HOLDS="libc6"
BUILD_SIM_RC=100
BUILD_SIM_OUT='The following packages have unmet dependencies:
 libc6-dev : Depends: libc6 (= 2.35-0ubuntu3.8) but 2.35-0ubuntu3.4 is to be installed
E: Unable to correct problems, you have held broken packages.'
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 3 && "$(cat "$tmp/term")" == "hold 된 패키지(libc6)"* && "$(cat "$tmp/term")" == *"Depends: libc6 (= 2.35-0ubuntu3.8)"* ]] \
  || { echo "FAIL caseB1: rc=$rc term=$(cat "$tmp/term")"; exit 1; }

# (B2) 헤더·빌드 도구 모의 실패가 hold 와 무관하면(헤더 패키지가 저장소에 없음) 통과.
reset; HEADERS_PRESENT=0
BUILD_SIM_RC=100
BUILD_SIM_OUT='E: Unable to locate package linux-headers-6.8.0-99-generic'
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 && ! -s "$tmp/term" ]] || { echo "FAIL caseB2: rc=$rc term=$(cat "$tmp/term")"; exit 1; }

# (B3) 헤더·모듈이 있으면 빌드 도구만 모의 설치한다. 이미 설치된 도구를 올리지 않도록 --no-upgrade 가 붙는다. 헤더
#      디렉터리 검사 기록에도 linux-headers- 가 들어 있으므로 모의 설치(apt-get -s) 줄만 본다.
reset
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
grep -q -- '-s -y install --no-upgrade --no-install-recommends build-essential dkms gcc make' "$nscalls" \
  && ! grep -qE 'apt-get -s .*(linux-headers-|linux-modules-extra-)' "$nscalls" || { echo "FAIL caseB3: $(last_sim)"; exit 1; }

# (B4) 헤더가 없으면 그 커널의 헤더 패키지도 모의 설치 목록에 든다.
reset; HEADERS_PRESENT=0
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
grep -q -- "--no-upgrade --no-install-recommends linux-headers-${KERNEL} build-essential" "$nscalls" \
  || { echo "FAIL caseB4: $(last_sim)"; exit 1; }

# (B5) 모듈 디렉터리가 없으면 modules-extra 가, 헤더도 없으면 헤더 뒤에 modules-extra 가 모의 설치 목록에 든다.
reset; MODULES_PRESENT=0
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
grep -q -- "--no-upgrade --no-install-recommends linux-modules-extra-${KERNEL} build-essential" "$nscalls" \
  || { echo "FAIL caseB5a: $(last_sim)"; exit 1; }
reset; HEADERS_PRESENT=0; MODULES_PRESENT=0
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
grep -q -- "--no-upgrade --no-install-recommends linux-headers-${KERNEL} linux-modules-extra-${KERNEL} build-essential" "$nscalls" \
  || { echo "FAIL caseB5b: $(last_sim)"; exit 1; }

# (B6) 설치 경로가 점검과 같은 헬퍼·같은 인자를 쓴다. 호출부 모양으로 고정한다(정의 줄과 겹치지 않는 형태) — 설치 경로가
#      빌드 도구 목록이나 헤더·모듈 판정을 따로 적거나 --no-upgrade 를 빼면 위 사례(점검이 모의하는 인자는 B3)는 그대로
#      통과하므로, 설치 경로의 명령이 점검의 모의와 어긋나게 되돌리면 여기서 실패한다.
ep="$here/entrypoint.sh"
[[ "$(grep -cF 'apt_install --no-upgrade $(dkms_build_pkgs) || true' "$ep")" == "1" \
  && "$(grep -cF 'apt_install --no-upgrade ubuntu-drivers-common jq' "$ep")" == "1" \
  && "$(grep -cF 'elif kernel_headers_present "${CURRENT_KERNEL}"; then' "$ep")" == "1" \
  && "$(grep -cF 'if kernel_modules_present "${CURRENT_KERNEL}"; then' "$ep")" == "1" ]] \
  || { echo "FAIL caseB6: 설치 경로가 기반 도구 줄·빌드 도구 줄의 apt_install --no-upgrade 와 dkms_build_pkgs·kernel_headers_present·kernel_modules_present 를 쓰지 않는다"; exit 1; }

# (B7) 빌드 도구가 이미 옛 버전으로 설치돼 hold 돼 있으면(후보가 더 높음) 설치 경로는 --no-upgrade 로 올리지 않고 설치된
#      도구로 빌드한다. 점검의 모의 설치도 같은 인자로 업그레이드를 시도하지 않아야 이 노드를 막지 않는다.
reset; HOLDS="dkms"
BUILD_SIM_UPGRADE_OUT='The following held packages will be changed:
  dkms
The following packages will be upgraded:
  dkms
1 upgraded, 0 newly installed, 0 to remove and 35 not upgraded.
E: Held packages were changed and -y was used without --allow-change-held-packages.'
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 && ! -s "$tmp/term" ]] || { echo "FAIL caseB7: rc=$rc term=$(cat "$tmp/term")"; exit 1; }

# (H1) VERSION_SOURCE=Host 이고 호스트에 드라이버 패키지가 있으면 설치기가 그 버전을 채택하므로 통과하고 apt 를 부르지 않는다.
reset; VERSION_SOURCE=Host; DPKG_DRIVER_LINE="install ok installed 580.126.09-0ubuntu0.22.04.1"
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 && ! -s "$tmp/term" ]] && ! apt_called || { echo "FAIL caseH1: rc=$rc term=$(cat "$tmp/term")"; exit 1; }

# (H2) VERSION_SOURCE=Host 라도 호스트에 드라이버가 없으면 점검은 그대로 진행한다.
reset; VERSION_SOURCE=Host; DPKG_DRIVER_LINE=""
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
apt_called || { echo "FAIL caseH2: 점검을 건너뜀"; exit 1; }

# (H3) 호스트에 드라이버가 있어도 VERSION_SOURCE 가 Policy 면 정책 버전을 설치하므로 점검은 그대로 진행한다.
reset; DPKG_DRIVER_LINE="install ok installed 580.126.09-0ubuntu0.22.04.1"
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
apt_called || { echo "FAIL caseH3: Policy 인데 점검을 건너뜀"; exit 1; }

# (H4) 로드된 모듈 버전도 같은 판정을 거친다 — 패키지가 조회되지 않아도 로드된 모듈이 있으면 Host 채택으로 통과한다.
reset; VERSION_SOURCE=Host; LOADED_NVRM="$NVRM_580126"
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 ]] && ! apt_called || { echo "FAIL caseH4: rc=$rc"; exit 1; }

# (H5) 설치 경로도 같은 감지·채택 판정을 쓴다. 호출부 모양은 글자로 고정하고(점검 한 곳, 설치 경로 한 곳), 실제 "EXISTING DRIVER
#      CHECK"·"HOST VERSION RESPECT" 블록을 뽑아 운영과 같이 set -Eeuo pipefail 로 돌려 점검(H1~H4)과 같은 호스트 상태에서 같은
#      채택 결과와 감지 로그(출처 표기)가 나오는지 본다.
[[ "$(grep -cE '^[[:space:]]*detect_existing_driver_version$' "$ep")" == "2" && "$(grep -cF 'if host_driver_adopted; then' "$ep")" == "2" ]] \
  || { echo "FAIL caseH5: 점검과 설치 경로가 detect_existing_driver_version·host_driver_adopted 를 한 곳씩 부르지 않는다"; exit 1; }
flow="$({ sed -n '/^# --- EXISTING DRIVER CHECK ---$/,/^# unattended-upgrades blacklist/p' "$ep" | sed '$d'
          sed -n '/^# --- HOST VERSION RESPECT (c) ---$/,/^# If requested version matches existing/p' "$ep" | sed '$d'; })"
[[ "$flow" == *"Existing NVIDIA driver detected"* && "$flow" == *"adopting existing host driver"* ]] \
  || { echo "FAIL caseH5: 설치 경로 블록을 못 뽑았다"; exit 1; }
run_flow() {   # 정책 버전 580.178.04 로 시작해 채택 결과를 마지막 줄에 낸다. 로그(stdout·stderr)도 함께 받는다.
  ( DRIVER_VERSION=580.178.04; SKIP_DRIVER_INSTALL=0
    set -Eeuo pipefail
    eval "${flow}"
    echo "result DRIVER_VERSION=${DRIVER_VERSION} SKIP_DRIVER_INSTALL=${SKIP_DRIVER_INSTALL}" ) 2>&1
}
reset; VERSION_SOURCE=Host; DPKG_DRIVER_LINE="install ok installed 580.126.09-0ubuntu0.22.04.1"
out="$(run_flow)"
[[ "$out" == *"Existing NVIDIA driver detected (package): 580.126.09"* && "$out" == *"adopting existing host driver 580.126.09"* \
  && "$out" != *"읽지 못했다"* && "$out" == *"result DRIVER_VERSION=580.126.09 SKIP_DRIVER_INSTALL=1" ]] || { echo "FAIL caseH5a: $out"; exit 1; }
reset; VERSION_SOURCE=Host; LOADED_NVRM="$NVRM_580126"
out="$(run_flow)"
[[ "$out" == *"Existing NVIDIA driver detected (loaded): 580.126.09"* && "$out" != *"읽지 못했다"* \
  && "$out" == *"result DRIVER_VERSION=580.126.09 SKIP_DRIVER_INSTALL=1" ]] || { echo "FAIL caseH5b: $out"; exit 1; }
reset; DPKG_DRIVER_LINE="install ok installed 580.126.09-0ubuntu0.22.04.1"
out="$(run_flow)"
[[ "$out" != *"adopting"* && "$out" == *"result DRIVER_VERSION=580.178.04 SKIP_DRIVER_INSTALL=0" ]] || { echo "FAIL caseH5c: $out"; exit 1; }
reset; VERSION_SOURCE=Host
out="$(run_flow)"
[[ "$out" == *"no host driver detected"* && "$out" == *"result DRIVER_VERSION=580.178.04 SKIP_DRIVER_INSTALL=0" ]] \
  || { echo "FAIL caseH5d: $out"; exit 1; }
# H5e: 모듈 파일은 있으나 버전을 읽지 못하면(패키지도 없음) 채택하지 않고, 감지 로그 대신 경고 한 줄을 남긴다. 정상 감지(H5a·H5b)에는 그 경고가 없다.
reset; VERSION_SOURCE=Host; LOADED_NVRM="NVRM version: unreadable"
out="$(run_flow)"
[[ "$out" == *"[WARN] /proc/driver/nvidia/version 은 있으나 로드된 모듈 버전을 읽지 못했다"* && "$out" != *"Existing NVIDIA driver detected"* \
  && "$out" == *"no host driver detected"* && "$out" == *"result DRIVER_VERSION=580.178.04 SKIP_DRIVER_INSTALL=0" ]] \
  || { echo "FAIL caseH5e: $out"; exit 1; }

# (L1) apt 잠금 대기 실패는 목록 갱신 실패와 다른 사유로 남긴다.
reset; APT_WAIT_RC=1
rc=0; run_preflight >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 1 ]] && grep -q 'apt/dpkg 잠금' "$tmp/term" && ! grep -q '목록 갱신 실패' "$tmp/term" && ! grep -q 'apt-get update' "$nscalls" \
  || { echo "FAIL caseL1: rc=$rc term=$(cat "$tmp/term")"; exit 1; }

# (L2) 점검 시점(PPA 추가 전)의 정책 버전 부재는 [ERR] 로 남기지 않고, 같은 사실을 두 번 적지도 않는다 — 판정 불가 경고 한 줄만
#      남고 종료 메시지가 그 문장이다(resolve_pinned_pkg_version 은 PIN_LOOKUP_QUIET 에서 아무것도 쓰지 않는다).
#      하위 셸 없이 현재 셸에서 돌려, PIN_LOOKUP_QUIET 가 run_preflight 밖으로 새는지(local 이 빠졌는지) 바로 아래서 본다.
reset; MADISON=""
rc=0; run_preflight >"$tmp/l2.out" 2>&1 || rc=$?
out="$(cat "$tmp/l2.out")"
[[ "$rc" -eq 1 && "$out" != *"[ERR]"* && "$(grep -c '정책 버전' <<< "$out")" == "1" \
  && "$out" == *"[WARN] 설치 전 점검 판정 불가: 정책 버전 580.178.04 을 저장소에서 찾지 못했다(PPA 추가 전 판정)"* \
  && "$(cat "$tmp/term")" == "설치 전 점검 판정 불가: 정책 버전 580.178.04 을 저장소에서 찾지 못했다(PPA 추가 전 판정)"* ]] \
  || { echo "FAIL caseL2: rc=$rc out=$out term=$(cat "$tmp/term")"; exit 1; }
[[ -z "${PIN_LOOKUP_QUIET:-}" ]] || { echo "FAIL caseL2b: PIN_LOOKUP_QUIET=${PIN_LOOKUP_QUIET} 가 run_preflight 밖으로 샜다"; exit 1; }

# (D) detect_existing_driver_version: 로드된 모듈 버전이 먼저, 없으면 설치된 nvidia-driver-* 패키지의 upstream 버전.
#     출처는 EXISTING_VER_SOURCE(loaded|package), 아무것도 없으면 둘 다 빈 값. 호출부가 값을 캡처하지 않으므로 stdout 에 쓰지 않는다.
#     /proc/driver/nvidia/version 은 있으나 버전을 읽지 못하면 EXISTING_VER_UNPARSED=1 로 알리고 설치 경로가 경고를 남긴다(D4).
reset; LOADED_NVRM="$NVRM_580126"; DPKG_DRIVER_LINE="install ok installed 580.159.03-0ubuntu1"
[[ -z "$(detect_existing_driver_version)" ]] || { echo "FAIL caseD0: 감지 함수가 stdout 에 썼다"; exit 1; }
detect_existing_driver_version
[[ "$EXISTING_VER" == "580.126.09" && "$EXISTING_VER_SOURCE" == "loaded" && "$EXISTING_VER_UNPARSED" == "0" ]] \
  || { echo "FAIL caseD1: ver=[$EXISTING_VER] source=[$EXISTING_VER_SOURCE] unparsed=[$EXISTING_VER_UNPARSED] (로드된 모듈이 패키지보다 먼저)"; exit 1; }
reset; DPKG_DRIVER_LINE="install ok installed 580.126.09-0ubuntu0.22.04.1"
detect_existing_driver_version
[[ "$EXISTING_VER" == "580.126.09" && "$EXISTING_VER_SOURCE" == "package" && "$EXISTING_VER_UNPARSED" == "0" ]] \
  || { echo "FAIL caseD2: ver=[$EXISTING_VER] source=[$EXISTING_VER_SOURCE] unparsed=[$EXISTING_VER_UNPARSED]"; exit 1; }
reset
detect_existing_driver_version
[[ -z "$EXISTING_VER" && -z "$EXISTING_VER_SOURCE" && "$EXISTING_VER_UNPARSED" == "0" ]] \
  || { echo "FAIL caseD3: ver=[$EXISTING_VER] source=[$EXISTING_VER_SOURCE] unparsed=[$EXISTING_VER_UNPARSED]"; exit 1; }
reset; LOADED_NVRM="NVRM version: unreadable"
detect_existing_driver_version
[[ -z "$EXISTING_VER" && -z "$EXISTING_VER_SOURCE" && "$EXISTING_VER_UNPARSED" == "1" ]] \
  || { echo "FAIL caseD4: ver=[$EXISTING_VER] source=[$EXISTING_VER_SOURCE] unparsed=[$EXISTING_VER_UNPARSED] (모듈 파일은 있으나 버전을 못 읽음)"; exit 1; }

# (9) 모든 사례를 통틀어 호스트에서 실행한 명령은 허용 목록의 형태뿐이다. 금지 명령을 정규식으로 찾으면 목록에
#     없는 변형(apt-get -y --allow-downgrades install …)이 빠져나가므로 허용하는 쪽을 닫는다. 허용: 커널 조회,
#     목록 갱신, 모의 설치(옵션은 --allow-downgrades·--no-install-recommends·--no-upgrade 만 — hold 를 무시하는
#     옵션이 붙으면 점검이 무의미하다), hold 목록·dpkg·apt-cache 조회, 커널 헤더·모듈 디렉터리 존재 확인, 로드된 모듈
#     버전 조회(test -f·cat), 설치된 드라이버 패키지 조회(dpkg-query 뒤의 grep·awk·head 필터). 기록 한 줄에 &&·||·;·|
#     나 명령 치환으로 이어 붙인 명령은 먼저 단위로 나눠 각각 판정한다 — 허용 명령에 붙은 변경 명령이 묻히지 않게 한다.
host_cmd_violations() {   # $1: 기록 파일. 허용 목록 밖의 명령 단위를 한 줄에 하나 낸다.
  local unit re ok arg='[A-Za-z0-9._+:=~/-]+' pkg='[A-Za-z0-9][A-Za-z0-9._+:=~/-]*' glob="'[A-Za-z0-9._+:=~/*-]+'"
  local allowed=(
    '^uname -r$'
    '^apt-get update( -y)?$'
    "^(LC_ALL=C |DEBIAN_FRONTEND=[a-z]+ )*apt-get -s -y install( --allow-downgrades| --no-install-recommends| --no-upgrade)*( ${pkg})+\$"
    '^apt-mark showhold$'
    "^apt-cache( ${arg})+\$"
    "^dpkg-query( ${arg}| ${glob}| -f='[^']*')*( 2>/dev/null)?\$"
    "^\\[\\[ -d /(usr/src/linux-headers-|lib/modules/)${arg} \\]\\]\$"
    '^test -f /proc/driver/nvidia/version$'
    '^cat /proc/driver/nvidia/version( 2>/dev/null)?$'
    # 위 dpkg-query 뒤에 이어지는 필터. 인자는 고정이고 표준 입력만 읽는다.
    "^grep '\\^install'\$"
    "^awk '\\{print \\\$4\\}'\$"
    '^head -1$'
  )
  while IFS= read -r unit; do
    unit="${unit#"${unit%%[![:space:]]*}"}"; unit="${unit%"${unit##*[![:space:]]}"}"
    [[ -n "$unit" ]] || continue
    ok=0
    for re in "${allowed[@]}"; do [[ "$unit" =~ $re ]] && { ok=1; break; }; done
    [[ "$ok" -eq 1 ]] || printf '%s\n' "$unit"
  done < <(sed -E 's/(\$\(|[;|&`()])/\n/g' "$1")
}
# (9a) 판정 자체의 시험: 허용 형태는 통과하고, 변경 명령은 단독이든 허용 명령에 이어 붙었든 걸린다. 이어 붙이는 구분자마다
# (&&·;·||·|·$( ) 변경 명령을 한 줄씩 둔다 — 판정기의 분리 식에서 구분자 하나가 빠지면 그 줄이 한 덩어리로 남아 기대 목록과 어긋난다.
cat > "$tmp/probe" <<'EOF'
apt-get update -y
LC_ALL=C DEBIAN_FRONTEND=noninteractive apt-get -s -y install --allow-downgrades libnvidia-compute-580:amd64=2:580.178.04-0ubuntu0.22.04.1
dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package} ${source:Package}\n' 2>/dev/null
apt-get -y --allow-downgrades install libx11-6
apt-get update -y && apt-get -y install foo
apt-get update -y; apt-get -y install foo
apt-get update -y || apt-get -y install foo
apt-cache madison foo | xargs apt-get install
apt-get -s -y install foo $(apt-get -y remove bar)
apt-get -s -y install --allow-change-held-packages foo
apt-mark hold libx11-6
dpkg --configure -a
add-apt-repository -y ppa:graphics-drivers/ppa
LC_ALL=C DEBIAN_FRONTEND=noninteractive apt-get -s -y install --no-upgrade --no-install-recommends linux-headers-5.15.0-179-generic build-essential dkms gcc make
apt-get -s -y install --no-upgrade --no-install-recommends --allow-change-held-packages foo
[[ -d /usr/src/linux-headers-5.15.0-179-generic ]]
[[ -d /lib/modules/5.15.0-179-generic ]]
[[ -d /etc ]]
test -f /proc/driver/nvidia/version
cat /proc/driver/nvidia/version 2>/dev/null
dpkg-query -W -f='${Status} ${Version}\n' 'nvidia-driver-*' 2>/dev/null | grep '^install' | awk '{print $4}' | head -1
cat /proc/driver/nvidia/version > /etc/motd
dpkg-query -W 'nvidia-driver-*' | tee /etc/motd
awk '{print $1}'
EOF
want=$'apt-get -y --allow-downgrades install libx11-6\napt-get -y install foo\napt-get -y install foo\napt-get -y install foo\nxargs apt-get install\napt-get -y remove bar\napt-get -s -y install --allow-change-held-packages foo\napt-mark hold libx11-6\ndpkg --configure -a\nadd-apt-repository -y ppa:graphics-drivers/ppa\napt-get -s -y install --no-upgrade --no-install-recommends --allow-change-held-packages foo\n[[ -d /etc ]]\ncat /proc/driver/nvidia/version > /etc/motd\ntee /etc/motd\nawk \'{print $1}\''
[[ "$(host_cmd_violations "$tmp/probe")" == "$want" ]] \
  || { echo "FAIL case9a: 허용 목록 판정이 어긋났다:"; host_cmd_violations "$tmp/probe" | sed 's/^/  /'; exit 1; }
# 실제 기록에 적용한다. 기록이 비면 아무것도 단정하지 못하므로 모의 설치가 기록됐는지 먼저 본다.
grep -q -- 'apt-get -s -y install' "$allcalls" || { echo "FAIL case9: 모의 설치 기록이 없다 — 검사 대상이 비었다"; exit 1; }
violations="$(host_cmd_violations "$allcalls")"
[[ -z "$violations" ]] || { echo "FAIL case9: 허용 목록 밖 호스트 명령:"; sed 's/^/  /' <<< "$violations"; exit 1; }

# (10) preflight.sh 는 run_preflight 의 종료 코드를 그대로 낸다.
printf 'run_preflight() { return 3; }\n' > "$tmp/fake-entrypoint.sh"
rc=0; ENTRYPOINT_PATH="$tmp/fake-entrypoint.sh" bash "$here/preflight.sh" || rc=$?
[[ "$rc" -eq 3 ]] || { echo "FAIL case10: rc=$rc (3 기대)"; exit 1; }

# (10b) 실제 entrypoint.sh 로 판정 불가 경로의 종료 코드. entrypoint.sh 는 source 하면서 set -Eeuo pipefail 을 켜므로
#       preflight.sh 의 set +eE 가 없으면 run_preflight 안에서 실패한 호스트 명령의 종료 코드가 그대로 프로세스 종료
#       코드가 된다. 그 값이 3 이면 operator 는 hold 충돌로 읽어 설치를 막는다. 항상 3 으로 실패하는 가짜 nsenter 를
#       PATH 맨 앞에 두면 ns 가 호스트에 닿지 않아 GPU·네트워크·권한과 무관하게 같은 경로(apt_wait 실패)를 탄다.
#       SKIP_ON_PASSTHROUGH=false 로 GPU 조회를 건너뛰고, 종료 메시지는 임시 파일에 받는다. 기대: 종료 코드 1 과 사유.

#       임시 디렉터리가 noexec 이면 가짜를 실행할 수 없어 PATH 탐색이 실제 nsenter 로 넘어가므로, 가짜가 먼저
#       잡히는지 확인하고 아니면 실행하지 않는다.
#       가짜는 ns 가 PATH 에서 찾는 bare nsenter 일 때만 가로챈다. entrypoint.sh 의 ns 가 nsenter 를 절대경로로 부르도록 바뀌면
#       아래 실행이 특권 호스트에서 실제 nsenter 에 닿으므로, 실행 전에 ns 정의가 bare nsenter 인지 글자로 확인하고 아니면 중단한다.
[[ "$(grep -cE '^ns\(\) \{ nsenter ' "$ep")" == "1" && "$(grep -c '^ns()' "$ep")" == "1" ]] \
  || { echo "FAIL case10b: entrypoint.sh 의 ns 가 PATH 의 bare nsenter 를 부르는 한 줄 정의 하나가 아니다 — 가짜 nsenter 가 가로채지 못해 실제 호스트에 닿을 수 있어 실행하지 않는다: $(grep '^ns()' "$ep" | head -c 300)"; exit 1; }
mkdir -p "$tmp/fakebin"; printf '#!/bin/sh\nexit 3\n' > "$tmp/fakebin/nsenter"; chmod +x "$tmp/fakebin/nsenter"
[[ "$(PATH="$tmp/fakebin:$PATH"; command -v nsenter)" == "$tmp/fakebin/nsenter" ]] \
  || { echo "FAIL case10b: 가짜 nsenter 를 실행할 수 없다 — TMPDIR(${TMPDIR:-/tmp})이 noexec 인가? 실제 nsenter 가 호스트에 닿지 않게 중단한다"; exit 1; }
: > "$tmp/term-real"
rc=0
PATH="$tmp/fakebin:$PATH" ENTRYPOINT_PATH="$here/entrypoint.sh" TERMINATION_LOG="$tmp/term-real" \
  SKIP_ON_PASSTHROUGH=false DRIVER_VERSION=580.178.04 bash "$here/preflight.sh" >"$tmp/out-real" 2>&1 || rc=$?
[[ "$rc" -eq 1 ]] && grep -q '판정 불가' "$tmp/term-real" \
  || { echo "FAIL case10b: rc=$rc (1 기대) term=$(cat "$tmp/term-real") out=$(head -c 300 "$tmp/out-real")"; exit 1; }

# (11) 이미지가 preflight.sh 를 담고 실행 권한을 준다.
df="$here/Dockerfile.v17"
[[ "$(grep -cF 'COPY preflight.sh /usr/local/bin/preflight.sh' "$df")" == "1" ]] || { echo "FAIL case11a"; exit 1; }
grep -qF '/usr/local/bin/preflight.sh' <(sed -n '/^RUN chmod +x \/usr\/local\/bin\/entrypoint.sh/,/^$/p' "$df") \
  || { echo "FAIL case11b: chmod 목록에 없음"; exit 1; }

echo "PASS: preflight_test"
