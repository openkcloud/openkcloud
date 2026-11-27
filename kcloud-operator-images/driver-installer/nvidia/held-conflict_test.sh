#!/usr/bin/env bash
# ============================================================
# held-conflict_test.sh: hold 된 호스트 패키지가 설치를 막는 경우를 판정·보고하는지 시험
# 상세: entrypoint.sh 함수만 source 하고 ns·apt_wait 를 가짜로 둔다. apt 출력 고정 데이터는 Ubuntu 22.04
#       컨테이너에서 실측한 원문(A 간접 의존, B 목록에 hold 패키지, N hold 없음)이다. 가드 뒤에 정의된
#       install_and_activate_driver 본문과 실행부의 설치 블록은 sed 로 뽑아 실제 코드를 실행한다 — 글자 확인만으로는
#       반환값·종료 코드를 바꾸는 변경을 잡지 못한다. 사례별 범위는 각 사례 머리 주석(M·14 포함)에 있다.
# 생성일: 2026-10-27 | 수정일: 2026-11-16
# ============================================================
# shellcheck disable=SC2034
# 파일 전체에 적용 — 전역 대입(DRIVER_VERSION·TERMINATION_LOG 등)은 source 한 함수가 읽는다.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
ep="$here/entrypoint.sh"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
installs="$tmp/installs"; sims="$tmp/sims"; err="$tmp/err"

ENTRYPOINT_LIB_ONLY=1
# shellcheck source=/dev/null
source "$here/entrypoint.sh"
set +eE

FIX_A=$'Reading package lists...\nBuilding dependency tree...\nReading state information...\nSome packages could not be installed. This may mean that you have\nrequested an impossible situation or if you are using the unstable\ndistribution that some required packages have not yet been created\nor been moved out of Incoming.\nThe following information may help to resolve the situation:\n\nThe following packages have unmet dependencies:\n libx11-xcb1 : Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1ubuntu0.3 is to be installed\nE: Unable to correct problems, you have held broken packages.'
FIX_B=$'Reading package lists...\nBuilding dependency tree...\nReading state information...\nThe following NEW packages will be installed:\n  libx11-xcb1\nThe following held packages will be changed:\n  libx11-6\nThe following packages will be upgraded:\n  libx11-6\n1 upgraded, 1 newly installed, 0 to remove and 39 not upgraded.\nE: Held packages were changed and -y was used without --allow-change-held-packages.'
FIX_N=$'Reading package lists...\nBuilding dependency tree...\nReading state information...\nlibx11-6 is already the newest version (2:1.7.5-1).\nSome packages could not be installed. This may mean that you have\nrequested an impossible situation or if you are using the unstable\ndistribution that some required packages have not yet been created\nor been moved out of Incoming.\nThe following information may help to resolve the situation:\n\nThe following packages have unmet dependencies:\n libx11-xcb1 : Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1 is to be installed\nE: Unable to correct problems, you have held broken packages.'
FIX_ARCH=$'The following packages have unmet dependencies:\n libnvidia-gl-580:amd64 : Depends: libx11-6:amd64 (= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1ubuntu0.3 is to be installed\nE: Unable to correct problems, you have held broken packages.'
FIX_BOTH=$'The following packages have unmet dependencies:\n libx11-xcb1 : Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1ubuntu0.3 is to be installed\nThe following held packages will be changed:\n  nvidia-utils-580:amd64\nE: Held packages were changed and -y was used without --allow-change-held-packages.'
MADISON_BOTH=$'nvidia-driver-580 | 580.178.04-0ubuntu1 | https://archive.example jammy-updates/restricted amd64 Packages\nnvidia-driver-580 | 580.159.03-0ubuntu1 | https://archive.example jammy-updates/restricted amd64 Packages'
printf '%s\n' "$FIX_A" > "$tmp/a.out"; printf '%s\n' "$FIX_B" > "$tmp/b.out"
printf '%s\n' "$FIX_N" > "$tmp/n.out"; printf '%s\n' "$FIX_ARCH" > "$tmp/arch.out"; printf '%s\n' "$FIX_BOTH" > "$tmp/both.out"
# FIX_R: Ubuntu 22.04 노드 실측(apt 2.4.9) — hold 된 형제 패키지는 고정 목록에서 빠져 unmet 절에 나오고, E: 줄이 다르다.
FIX_R=$'The following packages have unmet dependencies:\n nvidia-driver-580 : Depends: nvidia-utils-580 (= 580.126.09-0ubuntu0.22.04.1) but 580.178.04-0ubuntu0.22.04.1 is to be installed\n                     Recommends: libnvidia-compute-580:i386 (= 580.126.09-0ubuntu0.22.04.1) but it is not installable\n nvidia-utils-580 : Depends: nvidia-kernel-common-580-580.178.04\n                    Depends: libnvidia-compute-580 (>= 580.178.04) but 580.126.09-0ubuntu0.22.04.1 is to be installed\nE: Error, pkgProblemResolver::Resolve generated breaks, this may be caused by held packages.'
printf '%s\n' "$FIX_R" > "$tmp/r.out"

SIM_OUT=""; SIM_RC=0; HOLDS=""; APT_RC=0; MADISON="$MADISON_BOTH"
ns_default() {
  case "$*" in
    *"apt-get -s -y install"*) echo "$*" >> "$sims"; printf '%s\n' "${SIM_OUT}"; return "${SIM_RC}" ;;
    *"apt-get install"*)        echo "$*" >> "$installs"; return "${APT_RC}" ;;
    *"apt-mark showhold"*)      printf '%s' "${HOLDS}" ;;
    *"apt-cache madison"*)      printf '%s\n' "${MADISON}" ;;
    *)                          return 0 ;;
  esac
}
ns() { ns_default "$@"; }
apt_wait() { return 0; }
reset() { : > "$installs"; : > "$sims"; : > "$err"; SIM_OUT=""; SIM_RC=0; HOLDS=""; APT_RC=0; HOLD_CONFLICT_MSG=""; }

# (1) 간접 의존 형태: unmet 절의 두 패키지를 모두 낸다.
[[ "$(unmet_dep_pkgs "$tmp/a.out" | paste -sd' ' -)" == "libx11-6 libx11-xcb1" ]] \
  || { echo "FAIL case1: $(unmet_dep_pkgs "$tmp/a.out" | paste -sd' ' -)"; exit 1; }

# (2) 목록에 hold 패키지가 든 형태: held 절의 패키지만 낸다. A 에는 그 절이 없다.
[[ "$(held_change_pkgs "$tmp/b.out")" == "libx11-6" ]] || { echo "FAIL case2a: $(held_change_pkgs "$tmp/b.out")"; exit 1; }
[[ -z "$(held_change_pkgs "$tmp/a.out")" ]] || { echo "FAIL case2b: A 에서 held 절을 읽었다"; exit 1; }

# (3) hold 충돌 판정은 showhold 와 교집합이다. hold 가 없거나 무관하면 비어야 한다(N 은 실측 — hold 없이도
#     "held broken packages" 가 나온다).
reset; HOLDS=$'libx11-6\nlibx11-data\n'
[[ "$(held_conflict_pkgs "$tmp/a.out")" == "libx11-6" ]] || { echo "FAIL case3a: $(held_conflict_pkgs "$tmp/a.out")"; exit 1; }
reset; HOLDS=""
[[ -z "$(held_conflict_pkgs "$tmp/n.out")" ]] || { echo "FAIL case3b: hold 없이 충돌로 판정"; exit 1; }
reset; HOLDS=$'libx11-data\n'
[[ -z "$(held_conflict_pkgs "$tmp/n.out")" ]] || { echo "FAIL case3c: 무관한 hold 로 충돌 판정"; exit 1; }
reset; HOLDS=$'libx11-6\n'
[[ "$(held_conflict_pkgs "$tmp/b.out")" == "libx11-6" ]] || { echo "FAIL case3d: $(held_conflict_pkgs "$tmp/b.out")"; exit 1; }

# (3e)(3f) 실측 형태: 이어지는 줄·Recommends 줄이 섞여도 hold 패키지만 고르고, 메시지에 거절 줄이 실린다.
reset; HOLDS=$'linux-image-generic\nnvidia-utils-580\n'
[[ "$(held_conflict_pkgs "$tmp/r.out")" == "nvidia-utils-580" ]] || { echo "FAIL case3e: $(held_conflict_pkgs "$tmp/r.out")"; exit 1; }
msg="$(hold_conflict_message "$tmp/r.out" "nvidia-utils-580")"
[[ "$msg" == *"Depends: nvidia-utils-580 (= 580.126.09-0ubuntu0.22.04.1)"* && "$msg" == *"pkgProblemResolver::Resolve generated breaks"* ]] \
  || { echo "FAIL case3f: $msg"; exit 1; }

# (3g) 판정 결과는 오류 로그 출력의 성공과 무관하다. stderr 가 닫혀 log_error 가 실패해도 hold 충돌이면 0 이다.
reset; HOLDS=$'libx11-6\n'
rc=0; classify_hold_conflict "$tmp/a.out" 2>&- || rc=$?
[[ "$rc" -eq 0 && -n "$HOLD_CONFLICT_MSG" ]] || { echo "FAIL case3g: rc=$rc msg=[$HOLD_CONFLICT_MSG]"; exit 1; }

# (4) 아키텍처 접미사(:amd64)는 떼고 비교한다.
reset; HOLDS=$'libx11-6\n'
[[ "$(held_conflict_pkgs "$tmp/arch.out")" == "libx11-6" ]] || { echo "FAIL case4: $(held_conflict_pkgs "$tmp/arch.out")"; exit 1; }

# (5) 메시지: 패키지 이름이 앞, apt 거절 줄 포함, 한 줄, 해제 명령 안내.
msg="$(hold_conflict_message "$tmp/a.out" "libx11-6")"
[[ "$msg" == "hold 된 패키지(libx11-6)가 드라이버 설치를 막는다"* ]] || { echo "FAIL case5a: $msg"; exit 1; }
[[ "$msg" == *"Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4)"* && "$msg" == *"apt-mark unhold libx11-6"* ]] \
  || { echo "FAIL case5b: $msg"; exit 1; }
[[ "$msg" != *$'\n'* ]] || { echo "FAIL case5c: 메시지에 줄바꿈"; exit 1; }

# (5d) 큰 출력: 거절 줄이 파이프 용량(64KB)을 넘어도 메시지에 첫 Depends 줄부터 세 줄이 남는다. head 로 끊으면
#      pipefail 아래서 grep 이 SIGPIPE 로 끝나 거절 줄이 빈 값이 되던 결함의 회귀 시험이다.
{ echo 'The following packages have unmet dependencies:'
  for i in $(seq 1 2000); do
    printf ' pkg%d : Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1ubuntu0.3 is to be installed\n' "$i"
  done
  echo 'E: Unable to correct problems, you have held broken packages.'; } > "$tmp/big.out"
msg="$(set -o pipefail; hold_conflict_message "$tmp/big.out" "libx11-6")"
[[ "$msg" == *"pkg1 : Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4)"* && "$msg" == *"pkg3 : Depends:"* && "$msg" != *"pkg4 : Depends:"* ]] \
  || { echo "FAIL case5d: 큰 출력에서 거절 줄 유실 또는 세 줄 초과: ${msg:0:300}"; exit 1; }

# (6) 종료 메시지는 2048 바이트 이하, UTF-8 이 깨지지 않는다.
TERMINATION_LOG="$tmp/term"
write_termination_message "$(printf 'x%.0s' $(seq 1 3000))"
[[ "$(wc -c < "$tmp/term")" -eq 2048 ]] || { echo "FAIL case6a: $(wc -c < "$tmp/term") bytes"; exit 1; }
# 앞 2048 바이트를 자르면 683번째 글자(3바이트)가 2바이트만 남고 iconv -c 가 그 조각을 버린다 — 온전한 682자(2046 바이트)가
# 남아야 한다. 상한만 보면 파일이 비거나 짧아도 통과하므로 크기와 내용을 함께 본다.
write_termination_message "$(printf '한%.0s' $(seq 1 1000))"
[[ "$(wc -c < "$tmp/term")" -eq 2046 && "$(cat "$tmp/term")" == "$(printf '한%.0s' $(seq 1 682))" ]] \
  && iconv -f UTF-8 -t UTF-8 "$tmp/term" >/dev/null 2>&1 \
  || { echo "FAIL case6b: $(wc -c < "$tmp/term") 바이트 (온전한 한 682자, 2046 바이트 기대) 또는 UTF-8"; exit 1; }

# (7) 고정 설치가 실패하고 같은 인자의 모의 설치가 hold 충돌이면 3, 실제 설치 시도는 한 번뿐.
reset; DRIVER_VERSION=580.159.03; APT_RC=100; SIM_OUT="$FIX_A"; SIM_RC=100; HOLDS=$'libx11-6\nlibx11-data\n'
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 3 ]] || { echo "FAIL case7a: rc=$rc (3 기대)"; cat "$err"; exit 1; }
[[ "$(wc -l < "$installs" | tr -d ' ')" == "1" ]] || { echo "FAIL case7b: 설치 $(wc -l < "$installs")번"; exit 1; }
grep -q -- '-s -y install --allow-downgrades nvidia-driver-580=580.159.03-0ubuntu1' "$sims" \
  || { echo "FAIL case7c: 모의 설치 인자: $(cat "$sims")"; exit 1; }
[[ "$HOLD_CONFLICT_MSG" == "hold 된 패키지(libx11-6)"* && "$HOLD_CONFLICT_MSG" == *"Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4)"* ]] \
  || { echo "FAIL case7d: hold 패키지 이름이 앞에 오고 apt 거절 줄이 실려야 한다: msg=$HOLD_CONFLICT_MSG"; exit 1; }
# 판정은 apt 출력의 영어 문구를 읽는다. 번역된 로케일에서도 같은 출력을 받도록 모의 설치만 LC_ALL=C 로 돌리고,
# 출력을 파싱하지 않는 실제 설치 명령에는 붙이지 않는다.
grep -q 'LC_ALL=C' "$sims" || { echo "FAIL case7e: 모의 설치에 LC_ALL=C 가 없다: $(cat "$sims")"; exit 1; }
grep -q 'LC_ALL' "$installs" && { echo "FAIL case7f: 실제 설치 명령에 LC_ALL 이 붙었다: $(cat "$installs")"; exit 1; }

# (8) hold 와 무관한 실패는 종전대로 2(고정)·1(고정 없음).
reset; DRIVER_VERSION=580.159.03; APT_RC=100; SIM_OUT="$FIX_N"; SIM_RC=100; HOLDS=""
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 2 ]] && grep -q '다른 후보로 대체하지 않는다' "$err" || { echo "FAIL case8a: rc=$rc"; exit 1; }
reset; DRIVER_VERSION=580; APT_RC=100; SIM_OUT="$FIX_N"; SIM_RC=100; HOLDS=""
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case8b: rc=$rc (1 기대)"; exit 1; }

# (9) 고정 없는 계열 설치도 hold 충돌이면 3.
reset; DRIVER_VERSION=580; APT_RC=100; SIM_OUT="$FIX_A"; SIM_RC=100; HOLDS=$'libx11-6\n'
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 3 ]] || { echo "FAIL case9: rc=$rc (3 기대)"; exit 1; }

# (10) 후보 루프: 첫 후보가 3 이면 다음 후보를 시도하지 않고 HOLD_CONFLICT·PIN_FAILED 를 세운다.
calls="$tmp/calls"; : > "$calls"
install_and_activate_driver() { echo "$1" >> "$calls"; return 3; }
DRIVER_CANDIDATES=(nvidia-driver-580 nvidia-driver-570); EXPECTED_MAJOR=580
install_from_candidates >/dev/null 2>&1
[[ "$HOLD_CONFLICT" == "1" && "$PIN_FAILED" == "1" && "$INSTALL_SUCCESS" == "0" ]] \
  || { echo "FAIL case10a: HOLD=$HOLD_CONFLICT PIN=$PIN_FAILED OK=$INSTALL_SUCCESS"; exit 1; }
[[ "$(wc -l < "$calls" | tr -d ' ')" == "1" ]] || { echo "FAIL case10b: 후보 $(wc -l < "$calls")개 시도"; exit 1; }

# (11) 실행부 형상: 종료 함수 호출 한 곳, 종료 코드 3 은 그 함수 안 한 곳, DKMS 오해 문구 제거, hold 를 바꾸는 토큰 없음.
#      동작은 (12)~(14) 가 실행으로 확인한다. 여기서는 호출 지점과 exit 지점이 늘거나 사라지지 않는 것만 글자로 고정한다.
fn="$(sed -n '/^exit_install_failed() {$/,/^}$/p' "$ep")"
[[ "$(grep -cE '^[[:space:]]+exit_install_failed[[:space:]]*$' "$ep")" == "1" ]] \
  || { echo "FAIL case11a: 실행부의 exit_install_failed 호출이 한 곳이 아니다"; exit 1; }
[[ "$(grep -cF 'exit "${EXIT_HOLD_CONFLICT}"' "$ep")" == "1" && "$(grep -cF 'exit "${EXIT_HOLD_CONFLICT}"' <<< "$fn")" == "1" ]] \
  || { echo "FAIL case11b: exit \"\${EXIT_HOLD_CONFLICT}\" 가 exit_install_failed 안의 한 곳이 아니다"; exit 1; }
grep -q 'please inspect DKMS logs above' "$ep" && { echo "FAIL case11c: DKMS 문구가 남았다"; exit 1; }
# 11d: 파일 전체에서 hold 를 바꾸는 토큰을 찾는다. 주석 줄과 사람에게 보이는 안내 문장(hold_conflict_message 의
#      printf 한 줄)만 쓸 수 있다. 변수에 담은 옵션(HELD_OPT=--allow-change-held-packages)과 여러 줄 ns 블록 안의
#      줄도 잡는다. 대소문자는 구분하지 않아 옵션 이름 변형(-o APT::Get::Allow-Change-Held-Packages=true)도 잡고,
#      hold 를 무시하는 --force-yes 도 같은 목록에 둔다.
hold_tokens='apt-mark[[:space:]]+unhold|allow-change-held-packages|ignore-hold|force-yes'
hold_scan() { grep -inE "$hold_tokens" "$1" | grep -vE '^[0-9]+:[[:space:]]*#' | grep -vF 'hold 를 풀지는 노드 이미지 관리자가 정한다'; }
# 찾는 식 자체의 시험: 변형 표기 세 줄은 걸리고 주석 줄은 걸리지 않는다. 실제 파일이 깨끗하다는 단정이 빈 검색 때문에 통과하지 않게 한다.
printf '%s\n' 'ns "apt-get install -y -o APT::Get::Allow-Change-Held-Packages=true x"' 'ns "apt-get install -y --force-yes x"' \
  'ns "APT-MARK  UNHOLD x"' '# --allow-change-held-packages 는 쓰지 않는다' > "$tmp/holdprobe"
[[ "$(hold_scan "$tmp/holdprobe" | wc -l | tr -d ' ')" == "3" ]] \
  || { echo "FAIL case11d: 변형 표기를 못 찾는다(3줄 기대):"; hold_scan "$tmp/holdprobe"; exit 1; }
hold_hits="$(hold_scan "$ep")"
[[ -z "$hold_hits" ]] || { echo "FAIL case11d: hold 를 바꾸는 토큰:"; echo "$hold_hits"; exit 1; }
[[ "${EXIT_HOLD_CONFLICT}" == "3" ]] || { echo "FAIL case11e: EXIT_HOLD_CONFLICT=${EXIT_HOLD_CONFLICT}"; exit 1; }

# (12)~(14) 공통 준비: 사례끼리 서로의 상태에 기대지 않도록 여기서 한 번 정한다.
CURRENT_KERNEL=5.15.0-171-generic
TERMINATION_LOG="$tmp/term-exit"
GENERIC_MSG='드라이버 설치 실패 — install Job 로그의 apt·DKMS 출력을 확인한다'

# (12) rc 전달 사슬: 가드 뒤에 정의돼 lib-only 로는 없는 install_and_activate_driver 의 실제 본문을 뽑아 eval 한다.
#      install_driver_pkg_on_host 가 3·2·1 을 돌려주면 같은 값이 나와야 한다. 3 이 1 로 바뀌면 후보 루프가 다음
#      후보로 대체하고, 2 가 바뀌면 고정 실패 표식이 사라진다. 사례 10 의 stub 이 같은 이름으로 이미 정의돼 있어 정의 여부만
#      보면 늘 참이므로, 뽑은 본문의 첫 줄을 확인하고 stub 을 지운 뒤 eval 이 함수를 정의했는지 type -t 로 본다.
body12="$(sed -n '/^install_and_activate_driver() {$/,/^}$/p' "$ep")"
[[ "$(head -n1 <<< "$body12")" == 'install_and_activate_driver() {' && "$(tail -n1 <<< "$body12")" == '}' ]] \
  || { echo "FAIL case12: install_and_activate_driver 본문을 못 뽑았다(첫 줄=[$(head -n1 <<< "$body12")])"; exit 1; }
unset -f install_and_activate_driver
eval "$body12"
[[ "$(type -t install_and_activate_driver)" == "function" ]] \
  || { echo "FAIL case12: 뽑은 본문을 eval 해도 install_and_activate_driver 가 함수로 정의되지 않았다"; exit 1; }
for want in 3 2 1; do
  rc=0
  ( select_prebuilt_deb() { return 1; }; install_driver_pkg_on_host() { return "${want}"; }
    install_and_activate_driver nvidia-driver-580 580 ) >/dev/null 2>&1 || rc=$?
  [[ "$rc" -eq "$want" ]] || { echo "FAIL case12: install_driver_pkg_on_host=${want} 인데 install_and_activate_driver=${rc}"; exit 1; }
done

# (13) 실패 종료: HOLD_CONFLICT=1 이면 종료 코드 3 과 hold 사유를, 아니면 1 과 일반 사유를 종료 메시지에 쓴다.
#      하위 셸에서 부르므로 종료 상태와 종료 메시지 파일을 함께 본다. 파일은 매번 비워 이전 내용이 통과시키지 않게 한다.
: > "$TERMINATION_LOG"
rc=0; ( HOLD_CONFLICT=1; HOLD_CONFLICT_MSG="hold 사유 X"; exit_install_failed ) >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 3 && "$(cat "$TERMINATION_LOG")" == "hold 사유 X" ]] \
  || { echo "FAIL case13a: rc=$rc (3 기대), 종료 메시지=[$(cat "$TERMINATION_LOG")]"; exit 1; }
: > "$TERMINATION_LOG"
rc=0; ( HOLD_CONFLICT=0; exit_install_failed ) >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 1 && "$(cat "$TERMINATION_LOG")" == "$GENERIC_MSG" ]] \
  || { echo "FAIL case13b: rc=$rc (1 기대), 종료 메시지=[$(cat "$TERMINATION_LOG")]"; exit 1; }
grep -q 'see the apt and DKMS output above' "$err" || { echo "FAIL case13c: 일반 실패 로그 문구가 없다: $(cat "$err")"; exit 1; }

# (14) 실행부 호출 지점: 실제 "DRIVER SELECTION & INSTALL" 블록을 뽑아 운영과 같이 set -Eeuo pipefail 로 돌린다.
#      후보 루프·재부팅 유예 판정·종료 함수는 실제 코드이고 가드 뒤 함수 둘만 가짜다. 후보가 3 이면 종료 코드 3 과
#      hold 사유, 1 이면 종료 코드 1, 0 이면 종료하지 않는다.

#      두 번째 인자 defer 는 재부팅 유예가 가능한 호스트(타겟 패키지 설치됨·디스크 모듈 580·로드된 모듈 없음)다. 가짜 ns 가 그
#      상태를 답하고 collect_reboot_guard_state·apply_reboot_defer 는 실제 본문이 읽는다. 유예가 가능한 호스트에서도 hold 충돌은
#      유예 없이 종료 코드 3 이고(후보 루프가 세운 PIN_FAILED 가 유예를 막는다), hold 가 아닌 실패는 유예(종료 0·needs-reboot
#      마커)다 — 종료 함수 호출을 유예 판정 앞으로 옮기거나 hold 분기의 PIN_FAILED 를 빼면 여기서 잡힌다.
block="$(sed -n '/^# --- DRIVER SELECTION & INSTALL ---$/,/^# =====/p' "$ep" | sed '$d')"
[[ -n "$block" ]] || { echo "FAIL case14: 실행부 블록을 못 뽑았다"; exit 1; }
marks="$tmp/marks"
run_block() {   # $1: install_and_activate_driver 가 돌려줄 값, $2: defer 이면 재부팅 유예가 가능한 호스트
  : > "$TERMINATION_LOG"; : > "$marks"
  ( T_RC="$1"; HOLD_CONFLICT_MSG="hold 사유 Y"; DRIVER_VERSION=580; RUN_MODE=job; MARKER_DIR=/var/lib/kcloud-operator
    choose_driver_candidates() { DRIVER_CANDIDATES=(nvidia-driver-580); }
    install_and_activate_driver() { return "${T_RC}"; }
    if [[ "${2:-}" == "defer" ]]; then
      ns() {
        case "$*" in
          "dpkg-query -W -f='\${Status}' nvidia-driver-580"*) echo "install ok installed" ;;
          "modinfo -k "*)                                      echo "580.159.03" ;;
          *"/needs-reboot"*)                                   echo "$*" >> "$marks" ;;
          *)                                                   return 0 ;;
        esac
      }
    fi
    set -Eeuo pipefail
    eval "${block}" ) >/dev/null 2>&1
}
rc=0; run_block 3 || rc=$?
[[ "$rc" -eq 3 && "$(cat "$TERMINATION_LOG")" == "hold 사유 Y" ]] \
  || { echo "FAIL case14a: rc=$rc (3 기대), 종료 메시지=[$(cat "$TERMINATION_LOG")]"; exit 1; }
rc=0; run_block 1 || rc=$?
[[ "$rc" -eq 1 && "$(cat "$TERMINATION_LOG")" == "$GENERIC_MSG" ]] \
  || { echo "FAIL case14b: rc=$rc (1 기대), 종료 메시지=[$(cat "$TERMINATION_LOG")]"; exit 1; }
rc=0; run_block 0 || rc=$?
[[ "$rc" -eq 0 && ! -s "$TERMINATION_LOG" ]] \
  || { echo "FAIL case14c: 성공인데 rc=$rc, 종료 메시지=[$(cat "$TERMINATION_LOG")]"; exit 1; }
# 14d: 유예가 가능한 호스트에서도 hold 충돌은 종료 코드 3 과 hold 사유이고 needs-reboot 마커를 남기지 않는다.
rc=0; run_block 3 defer || rc=$?
[[ "$rc" -eq 3 && "$(cat "$TERMINATION_LOG")" == "hold 사유 Y" && ! -s "$marks" ]] \
  || { echo "FAIL case14d: rc=$rc (3 기대), 종료 메시지=[$(cat "$TERMINATION_LOG")], 마커=[$(cat "$marks")] (비어야 한다)"; exit 1; }
# 14e: 같은 호스트에서 hold 가 아닌 실패는 유예다 — 종료 코드 0, 종료 메시지 없음, needs-reboot 마커. 종료 함수를 유예 판정 앞으로
#      옮기면 이 사례가 1 로 끝난다(14d 는 그 변경에서도 3 이라 통과하므로 이 사례가 순서를 고정한다).
rc=0; run_block 1 defer || rc=$?
[[ "$rc" -eq 0 && ! -s "$TERMINATION_LOG" && -s "$marks" ]] \
  || { echo "FAIL case14e: rc=$rc (0 기대), 종료 메시지=[$(cat "$TERMINATION_LOG")], 마커=[$(cat "$marks")] (남아야 한다)"; exit 1; }

# (M1) hold 지목은 의존 대상(오른쪽)이 먼저고, 대상 쪽에 hold 가 없을 때만 요구하는 쪽(왼쪽)을 쓴다. Breaks·Conflicts 로
#      막힌 거절은 그 줄이 메시지에 실린다.
# (M1a) 의존 대상(오른쪽)이 hold 면 그것만 지목한다 — 요구하는 쪽(왼쪽)도 hold 여도 넣지 않는다.
cat >"$tmp/m1a" <<'OUT'
The following packages have unmet dependencies:
 libx11-xcb1 : Depends: libx11-6 (= 2:1.7.5-1ubuntu0.4) but 2:1.7.5-1ubuntu0.3 is to be installed
E: Unable to correct problems, you have held broken packages.
OUT
reset; HOLDS=$'libx11-6\nlibx11-xcb1'
[[ "$(held_conflict_pkgs "$tmp/m1a")" == "libx11-6" ]] || { echo "FAIL caseM1a: $(held_conflict_pkgs "$tmp/m1a")"; exit 1; }

# (M1b) 대상 쪽 교집합이 비면 요구하는 쪽 hold 를 지목한다.
reset; HOLDS="libx11-xcb1"
[[ "$(held_conflict_pkgs "$tmp/m1a")" == "libx11-xcb1" ]] || { echo "FAIL caseM1b: $(held_conflict_pkgs "$tmp/m1a")"; exit 1; }

# (M1c) "held packages will be changed" 절은 교집합과 무관하게 지목한다.
printf 'The following held packages will be changed:\n  nvidia-utils-580\nE: Held packages were changed and -y was used without --allow-change-held-packages.\n' >"$tmp/m1c"
reset; HOLDS=""
[[ "$(held_conflict_pkgs "$tmp/m1c")" == "nvidia-utils-580" ]] || { echo "FAIL caseM1c: $(held_conflict_pkgs "$tmp/m1c")"; exit 1; }

# (M1d) Breaks 로 막힌 거절은 그 줄이 메시지에 실린다.
printf 'The following packages have unmet dependencies:\n nvidia-driver-580 : Breaks: libnvidia-compute-570 but 570.1 is to be installed\nE: Unable to correct problems, you have held broken packages.\n' >"$tmp/m1d"
reset; HOLDS="libnvidia-compute-570"
held="$(held_conflict_pkgs "$tmp/m1d")"; msg="$(hold_conflict_message "$tmp/m1d" "$held")"
[[ "$held" == "libnvidia-compute-570" && "$msg" == *"Breaks: libnvidia-compute-570"* ]] \
  || { echo "FAIL caseM1d: held=[$held] $msg"; exit 1; }

# (M1e) Conflicts 로 막힌 거절도 같다.
printf 'The following packages have unmet dependencies:\n nvidia-driver-580 : Conflicts: nvidia-driver-570\nE: Unable to correct problems, you have held broken packages.\n' >"$tmp/m1e"
reset; HOLDS="nvidia-driver-570"
held="$(held_conflict_pkgs "$tmp/m1e")"; msg="$(hold_conflict_message "$tmp/m1e" "$held")"
[[ "$held" == "nvidia-driver-570" && "$msg" == *"Conflicts: nvidia-driver-570"* ]] \
  || { echo "FAIL caseM1e: held=[$held] $msg"; exit 1; }

# (M2) unmet_dep_targets 의 분기별 고정 출력: 줄 하나에 분기 하나, 오른쪽 패키지(버전 조건 앞)만 낸다. PreDepends 는 지금까지 어느
#      사례도 보지 않았고 Breaks·Conflicts 는 M1d·M1e 가 hold 지목과 메시지를 거쳐서만 본다. 여기서는 파서 출력을 직접 본다.
#      (정규식이 앞 일치를 요구하지 않아 지금은 Depends 분기가 PreDepends 줄도 읽는다 — 파서를 다시 쓸 때 놓치지 않게 고정한다.)
targets_of() {   # $1: 거절 줄 하나 — 절 머리말과 E: 줄을 붙인 고정 출력에서 의존 대상을 한 줄로 모아 낸다
  printf 'The following packages have unmet dependencies:\n%s\nE: Unable to correct problems, you have held broken packages.\n' "$1" > "$tmp/m2"
  unmet_dep_targets "$tmp/m2" | paste -sd' ' -
}
[[ "$(targets_of ' pkg-a : Depends: dep-x (>= 1.0) but 0.9 is to be installed')" == "dep-x" ]] \
  || { echo "FAIL caseM2a(Depends): $(targets_of ' pkg-a : Depends: dep-x (>= 1.0) but 0.9 is to be installed')"; exit 1; }
[[ "$(targets_of ' pkg-a : PreDepends: dep-y (>= 1.0) but 0.9 is to be installed')" == "dep-y" ]] \
  || { echo "FAIL caseM2b(PreDepends): $(targets_of ' pkg-a : PreDepends: dep-y (>= 1.0) but 0.9 is to be installed')"; exit 1; }
[[ "$(targets_of ' pkg-a : Breaks: dep-z (<< 2.0) but 1.9 is to be installed')" == "dep-z" ]] \
  || { echo "FAIL caseM2c(Breaks): $(targets_of ' pkg-a : Breaks: dep-z (<< 2.0) but 1.9 is to be installed')"; exit 1; }
[[ "$(targets_of ' pkg-a : Conflicts: dep-w')" == "dep-w" ]] \
  || { echo "FAIL caseM2d(Conflicts): $(targets_of ' pkg-a : Conflicts: dep-w')"; exit 1; }

# (M3) 요구 쪽 대체 지목도 아키텍처 접미사를 뗀 이름을 낸다 — 대상(libx11-6)에는 hold 가 없고 요구하는 쪽(libnvidia-gl-580:amd64)만 hold 인
#      형태. 접미사가 남으면 showhold 이름과 맞지 않아 지목이 비고, 판정은 3 이 아니라 판정 불가가 된다.
reset; HOLDS="libnvidia-gl-580"
[[ "$(held_conflict_pkgs "$tmp/arch.out")" == "libnvidia-gl-580" ]] \
  || { echo "FAIL caseM3a: $(held_conflict_pkgs "$tmp/arch.out")"; exit 1; }
# (M3b) unmet 지목과 "held packages will be changed" 절이 함께 있으면 둘 다 지목한다 — 한쪽이 다른 쪽을 가리지 않는다. 절 쪽의
#       패키지도 접미사(:amd64)를 뗀다.
reset; HOLDS="libx11-6"
[[ "$(held_conflict_pkgs "$tmp/both.out" | paste -sd' ' -)" == "libx11-6 nvidia-utils-580" ]] \
  || { echo "FAIL caseM3b: $(held_conflict_pkgs "$tmp/both.out" | paste -sd' ' -)"; exit 1; }

echo "PASS: held-conflict_test"
