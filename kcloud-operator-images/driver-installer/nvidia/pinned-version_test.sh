#!/usr/bin/env bash
# ============================================================
# pinned-version_test.sh: entrypoint-v17.sh 의 DKMS 경로가 정책 버전을 고정해 설치하는지 시험
# 상세: apt-cache/apt-get/apt-mark 를 가짜 함수로 두고 함수 정의만 source
#       (ENTRYPOINT_LIB_ONLY=1)해 install_nvidia_pkg_in_container 를 부른다.
# 생성일: 2026-10-06 | 수정일: 2026-10-11
# ============================================================
# shellcheck disable=SC2034
# 파일 전체에 적용 — 아래 전역 변수 대입(MAJOR/DRIVER_VERSION_RAW/VERSION_SOURCE/
# DESIRED_FROM_POLICY)은 source 한 entrypoint-v17.sh 의 함수가 전역으로 읽는다. 이
# 파일 안에서는 직접 참조되지 않아 shellcheck 에는 미사용으로 보인다(거짓 양성).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
calls="$tmp/calls"; : > "$calls"

# madison 출력은 MADISON_MODE 로 고른다 — 사례마다 직접 설정한다.
#   plain      : 평문 revision 두 줄(580.178.04, 580.126.09)
#   epoch_tab  : 탭 구분 + epoch 한 줄(1:580.178.04)
MADISON_MODE=plain
apt-cache() {
  case "$1" in
    search) echo "nvidia-driver-580 - NVIDIA driver metapackage" ;;
    madison)
      if [[ "${MADISON_MODE:-plain}" == "epoch_tab" ]]; then
        printf '%s\n' $'nvidia-driver-580\t|\t1:580.178.04-0ubuntu1\t|\thttps://archive.example jammy-updates/restricted amd64 Packages'
      else
        printf '%s\n' \
          "nvidia-driver-580 | 580.178.04-0ubuntu1 | https://archive.example jammy-updates/restricted amd64 Packages" \
          "nvidia-driver-580 | 580.126.09-0ubuntu1 | https://archive.example jammy-updates/restricted amd64 Packages"
      fi ;;
  esac
}
apt-get() { echo "apt-get $*" >> "$calls"; }
apt-mark() { echo "apt-mark $*" >> "$calls"; }
add-apt-repository() { :; }
export -f apt-cache apt-get apt-mark add-apt-repository

# 발행 이미지의 ENV 는 INSTALL_METHOD=hostnamespace 다. 그 값이면 source 가 가드보다 앞선
# 위임 분기로 들어가 legacy entrypoint.sh 를 exec 하므로, 라이브러리 모드에서는 명시한다.
INSTALL_METHOD=containerized
ENTRYPOINT_LIB_ONLY=1
# shellcheck source=/dev/null
source "$here/entrypoint-v17.sh"
# source 가 entrypoint-v17.sh 의 set -Eeuo 를 이 셸에 적용한다. 맨 호출 한 줄이 FAIL
# 메시지 없이 하네스를 끝내지 않도록 errexit/ERR 트랩만 끈다(-u 는 유지).
set +eE

# (1) 정책 버전이 저장소에 있음 → =버전 설치(hold 없음 — 컨테이너 안에서만 유효해 실효 없어 제거)
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580.178.04; VERSION_SOURCE=Policy; DESIRED_FROM_POLICY=1; MADISON_MODE=plain
install_nvidia_pkg_in_container >/dev/null 2>&1 || { echo "FAIL case1: install returned non-zero"; exit 1; }
grep -q 'apt-get install .*nvidia-driver-580=580.178.04-0ubuntu1 .*nvidia-dkms-580=580.178.04-0ubuntu1 .*nvidia-kernel-source-580=580.178.04-0ubuntu1' "$calls" || { echo "FAIL case1: pinned install missing"; cat "$calls"; exit 1; }
grep -q 'apt-mark hold' "$calls" && { echo "FAIL case1: unexpected hold (container-local, no effect — removed)"; exit 1; }

# (2) 정책 버전이 저장소에 없음 → 설치 호출 없이 실패
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580.999.99; VERSION_SOURCE=Policy; DESIRED_FROM_POLICY=1; MADISON_MODE=plain
if install_nvidia_pkg_in_container >/dev/null 2>"$tmp/err"; then echo "FAIL case2: should fail"; exit 1; fi
grep -q 'apt-get install' "$calls" && { echo "FAIL case2: installed despite missing version"; cat "$calls"; exit 1; }
grep -q '580.178.04-0ubuntu1' "$tmp/err" || { echo "FAIL case2: candidates not listed"; cat "$tmp/err"; exit 1; }

# (3) 호스트 버전 채택(DESIRED_FROM_POLICY=0) → 고정 없음
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580.178.04; VERSION_SOURCE=Host; DESIRED_FROM_POLICY=0; MADISON_MODE=plain
install_nvidia_pkg_in_container >/dev/null 2>&1 || { echo "FAIL case3: install returned non-zero"; exit 1; }
grep -q 'apt-get install .* nvidia-driver-580 nvidia-dkms-580 nvidia-kernel-source-580' "$calls" || { echo "FAIL case3: expected unpinned series install"; cat "$calls"; exit 1; }
grep -q 'apt-mark hold' "$calls" && { echo "FAIL case3: unexpected hold"; exit 1; }

# (4) VERSION_SOURCE=Host 여도 정책 버전이 desired 면(DESIRED_FROM_POLICY=1) 고정한다
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580.178.04; VERSION_SOURCE=Host; DESIRED_FROM_POLICY=1; MADISON_MODE=plain
install_nvidia_pkg_in_container >/dev/null 2>&1 || { echo "FAIL case4: install returned non-zero"; exit 1; }
grep -q 'apt-get install .*nvidia-driver-580=580.178.04-0ubuntu1 .*nvidia-dkms-580=580.178.04-0ubuntu1 .*nvidia-kernel-source-580=580.178.04-0ubuntu1' "$calls" || { echo "FAIL case4: VERSION_SOURCE=Host 여도 DESIRED_FROM_POLICY=1 이면 고정해야 한다"; cat "$calls"; exit 1; }

# (5) madison 출력에 epoch·탭이 섞여도 비교는 epoch 제외, 설치 인자는 epoch 포함
# (이 사례만 epoch_tab madison 을 쓴다 — case1/4 와 같은 580.178.04 를 쓰므로 평문 목록에
#  epoch 줄을 얹으면 "첫 일치" 규칙상 어느 한쪽 사례가 가려진다)
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580.178.04; VERSION_SOURCE=Policy; DESIRED_FROM_POLICY=1; MADISON_MODE=epoch_tab
install_nvidia_pkg_in_container >/dev/null 2>&1 || { echo "FAIL case5: install returned non-zero"; exit 1; }
grep -q 'apt-get install .*nvidia-driver-580=1:580.178.04-0ubuntu1 .*nvidia-dkms-580=1:580.178.04-0ubuntu1 .*nvidia-kernel-source-580=1:580.178.04-0ubuntu1' "$calls" || { echo "FAIL case5: epoch/tab pinned install missing"; cat "$calls"; exit 1; }

# (6) major 만 적은 정책 버전(580) → 고정 없이 계열 설치
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580; VERSION_SOURCE=Policy; DESIRED_FROM_POLICY=1; MADISON_MODE=plain
install_nvidia_pkg_in_container >/dev/null 2>&1 || { echo "FAIL case6: install returned non-zero"; exit 1; }
grep -q 'apt-get install .* nvidia-driver-580 nvidia-dkms-580 nvidia-kernel-source-580' "$calls" || { echo "FAIL case6: major-only 는 고정 없이 계열 설치해야 한다"; cat "$calls"; exit 1; }

# (7) 고정 상태에서 첫 설치가 실패해도 -server/-server-open 변형으로 넘어가지 않는다
# (이 사례 전용으로 apt-get 을 재정의 — 공유 stub 은 늘 0 을 돌려주므로 변형 분기에 도달하지
#  않는다. 계열 메타패키지 설치만 실패시키고 변형 설치는 성공시켜 두 방향을 대조한다)
apt-get() {
  echo "apt-get $*" >> "$calls"
  case "$*" in
    *install*nvidia-driver-580-server*) return 0 ;;
    *install*nvidia-driver-580*)        return 1 ;;
    *)                                  return 0 ;;
  esac
}
export -f apt-get
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580.178.04; VERSION_SOURCE=Policy; DESIRED_FROM_POLICY=1; MADISON_MODE=plain
if install_nvidia_pkg_in_container >/dev/null 2>"$tmp/err"; then echo "FAIL case7: 고정 설치 실패인데 0 을 돌려줬다"; cat "$calls"; exit 1; fi
n7="$(grep -c 'apt-get install' "$calls" || true)"
[[ "$n7" == "1" ]] || { echo "FAIL case7: 고정 상태 install 호출 ${n7}회 (1 기대)"; cat "$calls"; exit 1; }
grep -q '변형을 시도하지 않는다' "$tmp/err" || { echo "FAIL case7: 변형 생략 사유가 stderr 에 없다"; cat "$tmp/err"; exit 1; }
# 대조: 고정이 없으면(major 만) 같은 실패에서 변형까지 시도한다
: > "$calls"; MAJOR=580; DRIVER_VERSION_RAW=580; VERSION_SOURCE=Policy; DESIRED_FROM_POLICY=1; MADISON_MODE=plain
install_nvidia_pkg_in_container >/dev/null 2>&1 || { echo "FAIL case7: 고정 없음인데 변형 시도가 실패했다"; cat "$calls"; exit 1; }
n7="$(grep -c 'apt-get install' "$calls" || true)"
[[ "$n7" -ge 2 ]] || { echo "FAIL case7: 고정 없음 install 호출 ${n7}회 (2회 이상 기대)"; cat "$calls"; exit 1; }
grep -q 'nvidia-driver-580-server-open' "$calls" || { echo "FAIL case7: -server-open 변형 시도가 없다"; cat "$calls"; exit 1; }
# 사례 뒤에 공유 stub 으로 되돌린다
apt-get() { echo "apt-get $*" >> "$calls"; }
export -f apt-get

echo "PASS: pinned-version_test"
