#!/usr/bin/env bash
# ============================================================
# host-pinned-version_test.sh: entrypoint.sh 의 host apt 설치 경로가 정책 버전을 고정해 설치하는지 시험
# 상세: 발행 이미지는 Dockerfile.v17 의 INSTALL_METHOD=hostnamespace 로 이 경로를 탄다. ns·
#       apt_wait 를 가짜 함수로 두고 실제 install_driver_pkg_on_host 를 source 해 부른다
#       (복사본이 아니다). docker·컨테이너·host 접촉 없이 도는 순수 bash 시험이다.
# 생성일: 2026-10-06 | 수정일: 2026-10-28
# ============================================================
# shellcheck disable=SC2034
# 파일 전체에 적용 — DRIVER_VERSION 대입은 source 한 entrypoint.sh 의 함수가 전역으로
# 읽는다. 이 파일 안에서는 직접 참조되지 않아 shellcheck 에는 미사용으로 보인다(거짓 양성).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
installs="$tmp/installs"; err="$tmp/err"

ENTRYPOINT_LIB_ONLY=1
# shellcheck source=/dev/null
source "$here/entrypoint.sh"
# source 가 entrypoint.sh 의 set -Eeuo 를 이 셸에 적용한다. 맨 호출 한 줄이 FAIL 메시지
# 없이 하네스를 끝내지 않도록 errexit/ERR 트랩만 끈다(-u 는 유지).
set +eE

# host 명령은 전부 ns 를 지나므로 ns 하나만 가짜로 둔다.
MADISON=""
APT_RC=0
ns_default() {
  case "$*" in
    *"apt-cache madison"*) printf '%s\n' "${MADISON}" ;;
    *"apt-get install"*)   echo "$*" >> "$installs"; return "${APT_RC}" ;;
    *"dpkg-query -W -f='\${source:Package}' nvidia-driver-"*) printf '%s' "${SRC_PKG:-}" ;;
    *"dpkg-query -W -f='\${db:Status-Abbrev} \${binary:Package} \${source:Package}\n'"*) printf '%s\n' "${DPKG_LIST:-}"; return "${DPKG_LIST_RC:-0}" ;;
    *)                     return 0 ;;
  esac
}
ns() { ns_default "$@"; }
apt_wait() { return 0; }

# 각 사례는 자기 환경을 직접 설정한다(앞 사례 상태에 의존하지 않는다).
reset() { : > "$installs"; : > "$err"; APT_RC=0; SRC_PKG=; DPKG_LIST=; DPKG_LIST_RC=0; DRIVER_PKG_OPTS=; DRIVER_PKG_LIST=; DRIVER_PKG_PINNED=; }
lines() { wc -l < "$installs" | tr -d ' '; }

MADISON_BOTH=$'nvidia-driver-580 | 580.178.04-0ubuntu1 | https://archive.example jammy-updates/restricted amd64 Packages\nnvidia-driver-580 | 580.159.03-0ubuntu1 | https://archive.example jammy-updates/restricted amd64 Packages'
MADISON_ONLY_178='nvidia-driver-580 | 580.178.04-0ubuntu1 | https://archive.example jammy-updates/restricted amd64 Packages'
MADISON_TAB_EPOCH=$'nvidia-driver-580\t|\t1:580.178.04-0ubuntu1\t|\thttps://archive.example jammy-updates/restricted amd64 Packages'

# (1) 정책 버전이 저장소에 있음 → 세 패키지 모두 =버전
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case1: rc=$rc (0 기대)"; cat "$err"; exit 1; }
[[ "$(lines)" == "1" ]] || { echo "FAIL case1: install 호출 $(lines)줄 (1 기대)"; cat "$installs"; exit 1; }
for p in nvidia-driver-580 nvidia-dkms-580 nvidia-kernel-source-580; do
  grep -q "${p}=580.159.03-0ubuntu1" "$installs" \
    || { echo "FAIL case1: ${p}=580.159.03-0ubuntu1 없음: $(cat "$installs")"; exit 1; }
done

# (2) 정책 버전이 저장소에 없음 → 설치 호출 0회, rc 2, stderr 에 원하는 값과 후보
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_ONLY_178"
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 2 ]] || { echo "FAIL case2: rc=$rc (2 기대)"; cat "$err"; exit 1; }
[[ "$(lines)" == "0" ]] || { echo "FAIL case2: 저장소 부재인데 설치 $(lines)줄: $(cat "$installs")"; exit 1; }
grep -q '580\.159\.03' "$err" || { echo "FAIL case2: stderr 에 정책 버전 없음: $(cat "$err")"; exit 1; }
grep -q '580\.178\.04' "$err" || { echo "FAIL case2: stderr 에 후보 없음: $(cat "$err")"; exit 1; }

# (3) major 만 적은 정책 버전 → 고정 없이 계열 설치
reset; DRIVER_VERSION=580; MADISON="$MADISON_BOTH"
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case3: rc=$rc (0 기대)"; cat "$err"; exit 1; }
[[ "$(lines)" == "1" ]] || { echo "FAIL case3: install 호출 $(lines)줄 (1 기대)"; cat "$installs"; exit 1; }
grep -q -- '-y nvidia-driver-580$' "$installs" \
  || { echo "FAIL case3: 고정 없는 계열 설치가 아니다: $(cat "$installs")"; exit 1; }

# (4) 탭 구분 + epoch → 비교는 epoch 제외, 설치 인자는 epoch 포함
reset; DRIVER_VERSION=580.178.04; MADISON="$MADISON_TAB_EPOCH"
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case4: rc=$rc (0 기대)"; cat "$err"; exit 1; }
grep -q 'nvidia-driver-580=1:580\.178\.04-0ubuntu1' "$installs" \
  || { echo "FAIL case4: epoch 포함 고정 인자 없음: $(cat "$installs")"; exit 1; }

# (5) 고정값이 있는데 apt-get install 이 실패 → rc 2, 설치 시도 1회뿐(변형·다음 후보 없음)
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"; APT_RC=1
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 2 ]] || { echo "FAIL case5: rc=$rc (2 기대)"; cat "$err"; exit 1; }
[[ "$(lines)" == "1" ]] || { echo "FAIL case5: install 호출 $(lines)줄 (1 기대): $(cat "$installs")"; exit 1; }
grep -q '다른 후보로 대체하지 않는다' "$err" \
  || { echo "FAIL case5: 대체 금지 사유가 stderr 에 없음: $(cat "$err")"; exit 1; }
grep -q '고정 설치 목록' "$err" \
  || { echo "FAIL case5: 고정 설치 목록이 stderr 에 없음: $(cat "$err")"; exit 1; }

# (6) DRIVER_VERSION 이 빈 값 → 고정 없이 계열 설치
reset; DRIVER_VERSION=; MADISON="$MADISON_BOTH"
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case6: rc=$rc (0 기대)"; cat "$err"; exit 1; }
[[ "$(lines)" == "1" ]] || { echo "FAIL case6: install 호출 $(lines)줄 (1 기대)"; cat "$installs"; exit 1; }
grep -q -- '-y nvidia-driver-580$' "$installs" \
  || { echo "FAIL case6: 고정 없는 계열 설치가 아니다: $(cat "$installs")"; exit 1; }

# (7) 정책 계열이 아닌 후보(nvidia-driver-570) → 고정 대상이 아니다
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"
rc=0; install_driver_pkg_on_host nvidia-driver-570 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case7: rc=$rc (0 기대)"; cat "$err"; exit 1; }
[[ "$(lines)" == "1" ]] || { echo "FAIL case7: install 호출 $(lines)줄 (1 기대)"; cat "$installs"; exit 1; }
grep -q -- '-y nvidia-driver-570$' "$installs" \
  || { echo "FAIL case7: 다른 계열 후보가 고정 없이 설치되지 않았다: $(cat "$installs")"; exit 1; }

# (8)(9) 재부팅 유예 가드. 가드가 보는 신호 네 개는 호출부("모든 후보 실패" 블록)가 ns 로 모아
#   전역에 남기므로, 여기서는 그 네 신호를 흉내 낸 호스트 형상을 전역에 직접 둔다 — 타겟 major
#   패키지가 dpkg 상 설치됨(dpkg-query 가 'install ok installed' 1줄), 디스크 nvidia.ko 가 타겟
#   major, 모듈 미로드(/proc 가 비어 ACTIVE_MAJOR 빈 값). 네 조건이 모두 참인 상태라 PIN_FAILED
#   만이 결과를 가른다. DRIVER_VERSION 을 major 만으로 명시한다 — 비워 두면 사례 7 이 남긴
#   580.159.03(x.y.z)이 남아 사례 12~15 의 patch 비교 분기가 사례 9 를 깨뜨린다.
DRIVER_VERSION=580; EXPECTED_MAJOR=580; TARGET_PKG_OK=1; DISK_MOD_MAJOR=580; ACTIVE_MAJOR=""

# (8) 고정 실패 → 가드 미적용(rc 1). 정책 버전을 깔지 못했으므로 needs-reboot 마커 + exit 0
#     (설치 성공 오보)으로 가지 않고 All driver candidates failed → exit 1 로 간다.
PIN_FAILED=1
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 1 ]] \
  || { echo "FAIL case8: rc=$rc (1 기대) — 고정 실패인데 재부팅 유예 가드가 걸린다"; exit 1; }

# (9) 고정 실패 아님 → 가드 적용(rc 0). cross-major 모듈 로드 지연 유예라는 본래 용도는 남는다.
PIN_FAILED=0
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 0 ]] \
  || { echo "FAIL case9: rc=$rc (0 기대) — cross-major 재부팅 유예 가드가 사라졌다"; exit 1; }

# (10)(11) 후보 루프. install_and_activate_driver 는 ENTRYPOINT_LIB_ONLY 가드 뒤에 정의되므로
#   여기서는 없다 — 호출 인자를 기록하고 정해진 rc 를 돌려주는 stub 으로 둔다. stub 은 이
#   지점 이후 사례에서만 쓴다.
calls="$tmp/calls"
CAND_RC1=0; CAND_RC2=0   # 첫 후보·둘째 후보가 돌려줄 rc
install_and_activate_driver() {
  echo "$1 $2" >> "$calls"
  [[ "$(wc -l < "$calls")" -eq 1 ]] && return "$CAND_RC1"
  return "$CAND_RC2"
}
DRIVER_VERSION=580.159.03; EXPECTED_MAJOR=580
DRIVER_CANDIDATES=(nvidia-driver-580 nvidia-driver-570)

# (10) 첫 후보가 2(고정 실패) → 둘째 후보를 시도하지 않고 PIN_FAILED=1 로 끝낸다
: > "$calls"; CAND_RC1=2; CAND_RC2=0; INSTALL_SUCCESS=; PIN_FAILED=
install_from_candidates >/dev/null 2>"$err"
[[ "$PIN_FAILED" == "1" ]] \
  || { echo "FAIL case10: PIN_FAILED=$PIN_FAILED (1 기대)"; cat "$err"; exit 1; }
[[ "$INSTALL_SUCCESS" == "0" ]] \
  || { echo "FAIL case10: INSTALL_SUCCESS=$INSTALL_SUCCESS (0 기대)"; exit 1; }
[[ "$(wc -l < "$calls" | tr -d ' ')" == "1" ]] \
  || { echo "FAIL case10: 후보 호출 $(wc -l < "$calls")줄 (1 기대) — 고정 실패 뒤 다음 후보를 시도했다: $(cat "$calls")"; exit 1; }
grep -q 'nvidia-driver-580 580' "$calls" \
  || { echo "FAIL case10: 첫 후보에 기대 major 를 넘기지 않았다: $(cat "$calls")"; exit 1; }

# (11) 첫 후보가 1(고정 아닌 실패)·둘째가 0 → 둘째까지 시도해 성공, 고정 실패 표식 없음
: > "$calls"; CAND_RC1=1; CAND_RC2=0; INSTALL_SUCCESS=; PIN_FAILED=
install_from_candidates >/dev/null 2>"$err"
[[ "$INSTALL_SUCCESS" == "1" ]] \
  || { echo "FAIL case11: INSTALL_SUCCESS=$INSTALL_SUCCESS (1 기대)"; cat "$err"; exit 1; }
[[ "$PIN_FAILED" == "0" ]] \
  || { echo "FAIL case11: PIN_FAILED=$PIN_FAILED (0 기대) — rc 1 실패를 고정 실패로 읽는다"; exit 1; }
[[ "$(wc -l < "$calls" | tr -d ' ')" == "2" ]] \
  || { echo "FAIL case11: 후보 호출 $(wc -l < "$calls")줄 (2 기대): $(cat "$calls")"; exit 1; }

# (12) 정책 x.y.z 인데 디스크 모듈이 다른 patch 이면 가드를 걸지 않는다(rc 1).
#      major 만 비교하면 580.178.04 가 깔린 채로도 재부팅을 맡기게 되고, 부팅 뒤 검증기는
#      정확 일치를 보므로 그 자리에서 어긋난다.
DRIVER_VERSION=580.159.03; EXPECTED_MAJOR=580; TARGET_PKG_OK=1
DISK_MOD_VER=580.178.04; DISK_MOD_MAJOR=580; ACTIVE_MAJOR=""; PIN_FAILED=0
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case12: rc=$rc (1 기대) — 다른 patch 버전인데 재부팅 유예 가드가 걸린다"; exit 1; }

# (13) 정책 x.y.z 와 디스크 모듈이 정확히 같으면 가드가 걸린다(rc 0).
DRIVER_VERSION=580.159.03; EXPECTED_MAJOR=580; TARGET_PKG_OK=1
DISK_MOD_VER=580.159.03; DISK_MOD_MAJOR=580; ACTIVE_MAJOR=""; PIN_FAILED=0
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case13: rc=$rc (0 기대) — 정확 일치인데 가드가 사라졌다"; exit 1; }

# (14) major 만 적은 정책은 종전대로 major 비교다(rc 0) — 디스크 모듈 patch 는 비교하지 않는다.
DRIVER_VERSION=580; EXPECTED_MAJOR=580; TARGET_PKG_OK=1
DISK_MOD_VER=580.178.04; DISK_MOD_MAJOR=580; ACTIVE_MAJOR=""; PIN_FAILED=0
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case14: rc=$rc (0 기대) — major 정책에서 가드가 사라졌다"; exit 1; }

# (15) apt_wait 가 실패하면 고정 설치 경로는 apt-get 을 부르지 않고 2 로 끝낸다.
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"
apt_wait() { return 1; }
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
apt_wait() { return 0; }
[[ "$rc" -eq 2 && "$(lines)" -eq 0 ]] \
  || { echo "FAIL case15: rc=$rc lines=$(lines) (2·0 기대) — 잠금 대기 실패를 호출부가 무시한다"; exit 1; }

# (16) 타겟 패키지도 디스크 모듈도 host 에 없어 dpkg-query·modinfo(ns) 가 둘 다 실패해도
#      collect_reboot_guard_state 는 set -Eeuo pipefail 아래서 죽지 않고 TARGET_PKG_OK=0·
#      DISK_MOD_VER=빈 문자열로 끝나야 한다 — 그래야 "모든 후보 실패" 진단 앞에서 조용히 죽지
#      않는다. ns 를 실패로 바꾸고 호출 기록으로 두 조회가 ns 까지 갔는지도 단정한다(CURRENT_KERNEL
#      이 없으면 ns 호출 전에 변수 해제 오류가 난다). capture 는 두 statement 로 나눈다 — 한 줄로
#      합치면 bash 가 치환 안쪽의 set -e 를 통째로 무시한다.
EXPECTED_MAJOR=580; CURRENT_KERNEL=5.15.0-test
nscalls="$tmp/nscalls"; : > "$nscalls"
ns() { echo "$*" >> "$nscalls"; return 1; }
out="$(set -Eeuo pipefail; collect_reboot_guard_state; echo "ok=${TARGET_PKG_OK} mod=[${DISK_MOD_VER}]")"
rc=$?
ns() { ns_default "$@"; }
[[ "$rc" -eq 0 && "$out" == "ok=0 mod=[]" ]] \
  || { echo "FAIL case16: rc=$rc out=[$out] (0·'ok=0 mod=[]' 기대) — 패키지·모듈 부재 조회가 진단 전에 스크립트를 끊는다"; exit 1; }
grep -q 'dpkg-query .*nvidia-driver-580' "$nscalls" \
  || { echo "FAIL case16: dpkg-query 조회가 ns 까지 가지 않았다: $(cat "$nscalls")"; exit 1; }
grep -q 'modinfo -k 5\.15\.0-test ' "$nscalls" \
  || { echo "FAIL case16: modinfo 조회가 ns 까지 가지 않았다: $(cat "$nscalls")"; exit 1; }

# (17) 정책 x.y.z·디스크 모듈 일치인데 메타패키지만 다른 patch 로 남은 사전 컴파일
#      노드에서는 가드가 걸린다(rc 0) — 사전 컴파일은 메타패키지를 설치하지 않아 그
#      버전은 노드의 옛 apt 이력일 뿐이고, 부팅 뒤 검증기는 디스크 모듈 버전만 본다.
DRIVER_VERSION=580.126.09; EXPECTED_MAJOR=580; TARGET_PKG_OK=1
DISK_MOD_VER=580.126.09; DISK_MOD_MAJOR=580; ACTIVE_MAJOR=""; PIN_FAILED=0
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 0 ]] \
  || { echo "FAIL case17: rc=$rc (0 기대) — 사전 컴파일 노드의 메타패키지 patch 불일치로 회복 가능한 전이가 막힌다"; exit 1; }

# (18) 같은 계열 patch 교체: 정책 580.178.04, 디스크 모듈 580.178.04, 로드 모듈 580.126.09 →
#      구 모듈이 물려 있어 재부팅으로만 교체된다(rc 0). major 만 비교하면 580==580 이라 놓친다.
DRIVER_VERSION=580.178.04; EXPECTED_MAJOR=580; TARGET_PKG_OK=1
DISK_MOD_VER=580.178.04; DISK_MOD_MAJOR=580; ACTIVE_MAJOR=580; ACTIVE_NVRM=580.126.09; PIN_FAILED=0
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case18: rc=$rc (0 기대) — 같은 계열 patch 교체에서 재부팅 유예가 걸리지 않는다"; exit 1; }

# (19) 이미 정책 patch 가 로드돼 있으면 유예하지 않는다(rc 1).
ACTIVE_NVRM=580.178.04
rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case19: rc=$rc (1 기대) — 정책 버전이 로드됐는데 재부팅을 맡긴다"; exit 1; }
ACTIVE_NVRM=""

# (20) 성공 뒤 판정: 설치를 건너뛴 경로(다운그레이드 가드·동일 버전·Host 채택)는 마커를 세우지 않는다.
#      건너뛰면 DRIVER_VERSION 이 정책(낮은 값)으로 남아 전체 비교가 "다름" 을 내기 때문이다.
rc=0; loaded_needs_reboot 580.178.04 580.126.09 1 || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case20: rc=$rc (1 기대) — 설치를 건너뛰었는데 재부팅 마커를 세운다"; exit 1; }

# (21) 실제로 설치한 경로에서 로드 patch 가 정책과 다르면 마커를 세운다(rc 0), 같으면 세우지 않는다(rc 1),
#      major 만 적은 정책은 major 비교(rc 1), 로드 버전을 못 읽으면 세우지 않는다(rc 1).
rc=0; loaded_needs_reboot 580.126.09 580.178.04 0 || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case21a: rc=$rc (0 기대)"; exit 1; }
rc=0; loaded_needs_reboot 580.178.04 580.178.04 0 || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case21b: rc=$rc (1 기대)"; exit 1; }
rc=0; loaded_needs_reboot 580.126.09 580 0 || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case21c: rc=$rc (1 기대)"; exit 1; }
rc=0; loaded_needs_reboot unknown 580.178.04 0 || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case21d: rc=$rc (1 기대)"; exit 1; }

# (21e)(21f) 다른 계열이 로드돼 있으면 정책 형태와 무관하게 마커를 세운다(rc 0).
rc=0; loaded_needs_reboot 570.1.1 580 0 || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case21e(major 정책·다른 major): rc=$rc (0 기대)"; exit 1; }
rc=0; loaded_needs_reboot 570.86.10 580.178.04 0 || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case21f(x.y.z 정책·다른 계열): rc=$rc (0 기대)"; exit 1; }

# (22) install_from_candidates 종료 상태: 모든 후보가 실패해도 함수는 0 을 돌려준다(호출부가 전역으로 판정).
: > "$calls"; CAND_RC1=1; CAND_RC2=1; INSTALL_SUCCESS=; PIN_FAILED=
rc=0; install_from_candidates >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 && "$INSTALL_SUCCESS" == "0" && "$PIN_FAILED" == "0" ]] \
  || { echo "FAIL case22: rc=$rc INSTALL_SUCCESS=$INSTALL_SUCCESS PIN_FAILED=$PIN_FAILED (0·0·0 기대)"; exit 1; }
: > "$calls"
out="$(set -Eeuo pipefail; install_from_candidates >/dev/null 2>&1; echo reached)"
[[ "$out" == "reached" ]] || { echo "FAIL case22b: errexit 아래서 install_from_candidates 가 스크립트를 끊는다"; exit 1; }

# (23) 가드 형상: 기준선(조건이 모두 참)에서 조건 하나만 거짓으로 바꾸면 rc 1. 각 사례는 그 조건이
#      결과를 가르도록 정책 형태를 고른다 — 디스크 major·EXPECTED_MAJOR 는 x.y.z 정책에서 다른
#      조건이 대신 막아 주므로 major 정책으로 본다.
guard_base() { DRIVER_VERSION=580.178.04; EXPECTED_MAJOR=580; TARGET_PKG_OK=1; DISK_MOD_VER=580.178.04; DISK_MOD_MAJOR=580; ACTIVE_MAJOR=""; ACTIVE_NVRM=""; PIN_FAILED=0; }
guard_base; rc=0; reboot_defer_applicable || rc=$?; [[ "$rc" -eq 0 ]] || { echo "FAIL case23-base: rc=$rc"; exit 1; }
guard_base; DRIVER_VERSION=580; DISK_MOD_MAJOR=570; DISK_MOD_VER=570.86.10; rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case23a(DKMS 실패·디스크 모듈이 다른 major): rc=$rc"; exit 1; }
guard_base; DRIVER_VERSION=580; ACTIVE_MAJOR=580; rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case23b(major 정책·타겟 로드됨): rc=$rc"; exit 1; }
guard_base; DRIVER_VERSION=; EXPECTED_MAJOR=; DISK_MOD_MAJOR=; DISK_MOD_VER=; ACTIVE_MAJOR=580; rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case23c(EXPECTED_MAJOR 빈 값): rc=$rc"; exit 1; }
guard_base; TARGET_PKG_OK=0; rc=0; reboot_defer_applicable || rc=$?
[[ "$rc" -eq 1 ]] || { echo "FAIL case23d(타겟 패키지 없음): rc=$rc"; exit 1; }

# (24) apply_reboot_defer: rebootStrategy=Never 면 마커 없이 1, IfNeeded 면 마커를 남기고 0,
#      가드가 거짓이면 IfNeeded 여도 마커 없이 1. ns 는 needs-reboot 마커 명령만 기록한다.
MARKER_DIR="$tmp/m"; marks="$tmp/marks"
ns() { case "$*" in *"needs-reboot"*) echo "$*" >> "$marks" ;; esac; return 0; }
guard_base; : > "$marks"
rc=0; ( REBOOT_STRATEGY=Never; apply_reboot_defer ) >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 1 && ! -s "$marks" ]] || { echo "FAIL case24a(Never): rc=$rc marks=$(cat "$marks")"; exit 1; }
guard_base; : > "$marks"
rc=0; ( REBOOT_STRATEGY=IfNeeded; apply_reboot_defer ) >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 && -s "$marks" ]] || { echo "FAIL case24b(IfNeeded): rc=$rc marks=$(cat "$marks")"; exit 1; }
guard_base; PIN_FAILED=1; : > "$marks"
rc=0; ( REBOOT_STRATEGY=IfNeeded; apply_reboot_defer ) >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 1 && ! -s "$marks" ]] || { echo "FAIL case24c(가드 거짓): rc=$rc marks=$(cat "$marks")"; exit 1; }
ns() { ns_default "$@"; }

# (25) 성공 뒤 호출부 형상: 세 번째 인자(설치 생략 여부)를 빼면 SKIP_DRIVER_INSTALL=1 인 실행이
#      정책보다 높은 호스트에서 재부팅 마커를 세워 루프를 만든다. 호출이 이 형태 그대로 한 번 있어야 한다.
# shellcheck disable=SC2016
site='loaded_needs_reboot "${INSTALLED_VER}" "${DRIVER_VERSION:-}" "${SKIP_DRIVER_INSTALL:-0}"'
[[ "$(grep -cF -- "$site" "$here/entrypoint.sh")" == "1" ]] \
  || { echo "FAIL case25: 성공 뒤 호출부가 '$site' 형태로 한 번 있지 않다"; exit 1; }

# (26) 같은 계열 하향: 설치된 사용자 공간 패키지(같은 소스)도 같은 버전으로 고정하고 --allow-downgrades.
#      다른 소스(nvidia-container-toolkit)·제거 뒤 설정만 남은 패키지(rc)·이름에 버전이 든
#      패키지(nvidia-firmware-580-<버전>)는 넣지 않는다.
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"; SRC_PKG=nvidia-graphics-drivers-580
DPKG_LIST=$'ii  nvidia-driver-580 nvidia-graphics-drivers-580\nii  nvidia-dkms-580 nvidia-graphics-drivers-580\nii  nvidia-kernel-source-580 nvidia-graphics-drivers-580\nii  libnvidia-compute-580:amd64 nvidia-graphics-drivers-580\nii  nvidia-utils-580 nvidia-graphics-drivers-580\nii  nvidia-kernel-common-580 nvidia-graphics-drivers-580\nii  nvidia-firmware-580-580.178.04 nvidia-graphics-drivers-580\nii  nvidia-container-toolkit libnvidia-container\nrc  libnvidia-gl-580:amd64 nvidia-graphics-drivers-580'
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 && "$(lines)" == "1" ]] || { echo "FAIL case26: rc=$rc lines=$(lines)"; cat "$err"; exit 1; }
for p in libnvidia-compute-580:amd64 nvidia-utils-580 nvidia-kernel-common-580; do
  grep -q " ${p}=580.159.03-0ubuntu1" "$installs" || { echo "FAIL case26: ${p} 고정 없음: $(cat "$installs")"; exit 1; }
done
grep -q -- '--allow-downgrades' "$installs" || { echo "FAIL case26: --allow-downgrades 없음: $(cat "$installs")"; exit 1; }
grep -q 'nvidia-container-toolkit\|libnvidia-gl-580' "$installs" && { echo "FAIL case26: 다른 소스·rc 패키지가 섞임: $(cat "$installs")"; exit 1; }
grep -q 'nvidia-firmware' "$installs" && { echo "FAIL case26: 이름에 버전이 든 패키지가 섞임(옛 이름에 새 버전을 요구): $(cat "$installs")"; exit 1; }
[[ "$(grep -o 'nvidia-driver-580=' "$installs" | wc -l | tr -d ' ')" == "1" ]] || { echo "FAIL case26: 메타패키지 중복: $(cat "$installs")"; exit 1; }
for p in nvidia-dkms-580 nvidia-kernel-source-580; do
  [[ "$(grep -o "${p}=" "$installs" | wc -l | tr -d ' ')" == "1" ]] || { echo "FAIL case26: ${p} 중복: $(cat "$installs")"; exit 1; }
done

# (27) 메타패키지가 설치되지 않은 새 노드: 세 패키지만 고정하고 --allow-downgrades 는 붙인다.
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case27: rc=$rc"; cat "$err"; exit 1; }
[[ "$(tr ' ' '\n' < "$installs" | grep -c '=580.159.03-0ubuntu1')" == "3" ]] || { echo "FAIL case27: 고정 3개가 아님: $(cat "$installs")"; exit 1; }
grep -q -- '--allow-downgrades' "$installs" || { echo "FAIL case27: --allow-downgrades 없음"; exit 1; }

# (28) 고정하지 않는 경로(major 만 적은 정책)는 --allow-downgrades 를 붙이지 않는다.
reset; DRIVER_VERSION=580; MADISON="$MADISON_BOTH"; SRC_PKG=nvidia-graphics-drivers-580
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 && "$(lines)" == "1" ]] || { echo "FAIL case28: rc=$rc lines=$(lines)"; cat "$err"; exit 1; }
grep -q ' nvidia-driver-580$' "$installs" || { echo "FAIL case28: 고정 없는 메타패키지 설치 줄 없음: $(cat "$installs")"; exit 1; }
grep -q -- '--allow-downgrades' "$installs" && { echo "FAIL case28: 고정 없는 설치에 --allow-downgrades"; exit 1; }

# (29) 메타패키지는 있는데 전체 목록 조회가 실패하면 세 패키지만 고정하고 stderr 에 경고한다.
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"; SRC_PKG=nvidia-graphics-drivers-580
DPKG_LIST=$'ii  libnvidia-compute-580:amd64 nvidia-graphics-drivers-580'; DPKG_LIST_RC=2
rc=0; install_driver_pkg_on_host nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case29: rc=$rc"; cat "$err"; exit 1; }
[[ "$(tr ' ' '\n' < "$installs" | grep -c '=580.159.03-0ubuntu1')" == "3" ]] || { echo "FAIL case29: 고정 3개가 아님: $(cat "$installs")"; exit 1; }
grep -q '같은 소스(nvidia-graphics-drivers-580) 설치 패키지 조회 실패' "$err" || { echo "FAIL case29: 경고 없음: $(cat "$err")"; exit 1; }

# (30) driver_pkg_args 는 설치와 같은 목록을 전역에 남기고 설치를 부르지 않는다.
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_BOTH"; SRC_PKG=nvidia-graphics-drivers-580
DPKG_LIST=$'ii  nvidia-driver-580 nvidia-graphics-drivers-580\nii  nvidia-utils-580 nvidia-graphics-drivers-580'
rc=0; driver_pkg_args nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 0 ]] || { echo "FAIL case30: rc=$rc (0 기대)"; cat "$err"; exit 1; }
[[ "$DRIVER_PKG_OPTS" == "--allow-downgrades" && "$DRIVER_PKG_PINNED" == "580.159.03-0ubuntu1" ]] \
  || { echo "FAIL case30: opts=[$DRIVER_PKG_OPTS] pinned=[$DRIVER_PKG_PINNED]"; exit 1; }
want30="nvidia-driver-580=580.159.03-0ubuntu1 nvidia-dkms-580=580.159.03-0ubuntu1 nvidia-kernel-source-580=580.159.03-0ubuntu1 nvidia-utils-580=580.159.03-0ubuntu1"
[[ "$DRIVER_PKG_LIST" == "$want30" ]] || { echo "FAIL case30: list=[$DRIVER_PKG_LIST]"; exit 1; }
[[ "$(lines)" == "0" ]] || { echo "FAIL case30: 인자 계산이 설치를 불렀다: $(cat "$installs")"; exit 1; }

# (31) 고정 대상이 아니면 패키지 이름 하나·옵션 없음, 정책 버전이 저장소에 없으면 2.
reset; DRIVER_VERSION=580; MADISON="$MADISON_BOTH"
DRIVER_PKG_OPTS=stale; DRIVER_PKG_LIST=stale; DRIVER_PKG_PINNED=stale   # 앞 호출이 남긴 값을 이 호출이 덮어쓰는지 직접 본다
rc=0; driver_pkg_args nvidia-driver-580 >/dev/null 2>&1 || rc=$?
[[ "$rc" -eq 0 && "$DRIVER_PKG_LIST" == "nvidia-driver-580" && -z "$DRIVER_PKG_OPTS" && -z "$DRIVER_PKG_PINNED" ]] \
  || { echo "FAIL case31a: rc=$rc (0 기대) list=[$DRIVER_PKG_LIST] opts=[$DRIVER_PKG_OPTS] pinned=[$DRIVER_PKG_PINNED]"; exit 1; }
reset; DRIVER_VERSION=580.159.03; MADISON="$MADISON_ONLY_178"
rc=0; driver_pkg_args nvidia-driver-580 >/dev/null 2>"$err" || rc=$?
[[ "$rc" -eq 2 ]] || { echo "FAIL case31b: rc=$rc (2 기대)"; exit 1; }

# (32) lib-only source 만으로 점검에 필요한 함수가 모두 정의된다. 이 시험의 stub(apt_wait 등)을 물려받으면 가드 뒤로 옮겨도 통과하므로
#      새 프로세스에서 확인하고, 빠진 함수 이름을 모두 낸다.
missing32="$(ENTRYPOINT_LIB_ONLY=1 bash -c 'source "$1" >/dev/null 2>&1
  for f in apt_wait nvidia_gpu_bindings nvidia_has_installable_gpu driver_pkg_args; do declare -F "$f" >/dev/null || echo "$f"; done' _ "$here/entrypoint.sh")"
[[ -z "$missing32" ]] \
  || { echo "FAIL case32: lib-only source 만으로 정의돼야 하는 점검 헬퍼가 새 프로세스에 없다: $(tr '\n' ' ' <<< "$missing32")"; exit 1; }

echo "PASS: host-pinned-version_test"
